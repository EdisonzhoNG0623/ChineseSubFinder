package subdl

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/local_http_proxy_server"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/file_downloader"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/mix_media_info"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

const (
	subDLDownloadTimeout = 6 * time.Minute
	subDLSearchTimeout   = 10 * time.Second
	subDLMaxDownloadSize = 25 << 20
)

type Supplier struct {
	log            *logrus.Logger
	fileDownloader *file_downloader.FileDownloader
	isAlive        bool
}

func NewSupplier(fileDownloader *file_downloader.FileDownloader) *Supplier {
	return &Supplier{
		log:            fileDownloader.Log,
		fileDownloader: fileDownloader,
		isAlive:        true,
	}
}

func (s *Supplier) CheckAlive() (bool, int64) {
	if !settings.Get().SubtitleSources.SubDLSettings.Enabled || settings.Get().SubtitleSources.SubDLSettings.ApiKey == "" {
		s.isAlive = false
		return false, 0
	}
	started := time.Now()
	_, err := s.query(map[string]string{
		"tmdb_id": "550",
		"type":    "movie",
	})
	if err != nil {
		s.log.Warningln(s.GetSupplierName(), "Check Alive failed:", err)
		s.isAlive = false
		return false, 0
	}
	s.isAlive = true
	return true, time.Since(started).Milliseconds()
}

func (s *Supplier) IsAlive() bool { return s.isAlive }

func (s *Supplier) GetSupplierName() string { return common.SubSiteSubDL }

func (s *Supplier) GetLogger() *logrus.Logger { return s.log }

func (s *Supplier) OverDailyDownloadLimit() bool {
	oneSettings := settings.Get().AdvancedSettings.SuppliersSettings.SubDL
	if oneSettings == nil || oneSettings.DailyDownloadLimit == 0 {
		return true
	}
	if oneSettings.DailyDownloadLimit < 0 {
		return false
	}
	count, err := s.fileDownloader.CacheCenter.DailyDownloadCountGet(
		s.GetSupplierName(), pkg.GetPublicIP(s.log, settings.Get().AdvancedSettings.TaskQueue),
	)
	if err != nil {
		s.log.Warningln(s.GetSupplierName(), "DailyDownloadCountGet", err)
		return true
	}
	return oneSettings.OverDailyDownloadLimit(count)
}

func (s *Supplier) GetSubListFromFile4Movie(filePath string) ([]supplier.SubInfo, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	return s.getSubListFromFile(filePath, true, 0, 0, 0)
}

func (s *Supplier) GetSubListFromFile4Series(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.downloadSeries(seriesInfo)
}

func (s *Supplier) GetSubListFromFile4Anime(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.downloadSeries(seriesInfo)
}

func (s *Supplier) configured() error {
	cfg := settings.Get().SubtitleSources.SubDLSettings
	if !cfg.Enabled {
		return nil
	}
	if cfg.ApiKey == "" {
		return errors.New("SubDL API key is empty")
	}
	return nil
}

func (s *Supplier) downloadSeries(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if !settings.Get().SubtitleSources.SubDLSettings.Enabled {
		return []supplier.SubInfo{}, nil
	}
	out := make([]supplier.SubInfo, 0)
	for _, episode := range seriesInfo.NeedDlEpsKeyList {
		items, err := s.getSubListFromFile(episode.FileFullPath, false, episode.Season, episode.Episode, episode.AbsoluteEpisode)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), episode.Title, err)
			continue
		}
		for i := range items {
			items[i].Season = episode.Season
			items[i].Episode = episode.Episode
		}
		out = append(out, items...)
	}
	return out, nil
}

func (s *Supplier) getSubListFromFile(videoPath string, isMovie bool, season, episode, absoluteEpisode int) ([]supplier.SubInfo, error) {
	if !settings.Get().SubtitleSources.SubDLSettings.Enabled {
		return []supplier.SubInfo{}, nil
	}
	mediaInfo, err := mix_media_info.GetMixMediaInfo(s.fileDownloader.MediaInfoDealers, videoPath, isMovie)
	if err != nil {
		return nil, fmt.Errorf("get media identity: %w", err)
	}
	params, ok := strictMediaQuery(mediaInfo, isMovie, season, episode)
	if !ok {
		s.log.Warningln(s.GetSupplierName(), "skip media without IMDb/TMDB identity:", filepath.Base(videoPath))
		return []supplier.SubInfo{}, nil
	}
	response, err := s.query(params)
	if err != nil {
		return nil, err
	}
	if !responseMatchesMedia(response, mediaInfo) {
		s.log.Warningln(s.GetSupplierName(), "skip response identity mismatch:", filepath.Base(videoPath))
		return []supplier.SubInfo{}, nil
	}

	candidates := selectCandidates(response.Subtitles, isMovie, season, episode, absoluteEpisode)
	if len(candidates) == 0 && !isMovie && absoluteEpisode > 0 && absoluteEpisode != episode {
		absoluteParams := make(map[string]string, len(params))
		for key, value := range params {
			absoluteParams[key] = value
		}
		delete(absoluteParams, "season_number")
		delete(absoluteParams, "full_season")
		delete(absoluteParams, "unpack")
		absoluteParams["episode_number"] = strconv.Itoa(absoluteEpisode)
		absoluteResponse, absoluteErr := s.query(absoluteParams)
		if absoluteErr != nil {
			s.log.Warningln(s.GetSupplierName(), "absolute episode search failed:", absoluteErr)
		} else if responseMatchesMedia(absoluteResponse, mediaInfo) {
			candidates = selectCandidates(absoluteResponse.Subtitles, false, season, episode, absoluteEpisode)
		}
	}
	out := make([]supplier.SubInfo, 0, settings.Get().AdvancedSettings.Topic)
	for index, candidate := range candidates {
		downloadURL, err := safeDownloadURL(candidate.URL)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), "skip unsafe download URL:", err)
			continue
		}
		cacheIdentity := strings.TrimSpace(string(candidate.ID))
		if cacheIdentity == "" {
			cacheIdentity = candidate.URL
		}
		cacheKey := fmt.Sprintf("subdl-%x", sha256.Sum256([]byte(cacheIdentity)))
		oneSub, err := s.fileDownloader.GetWithCustomDownloader(
			s.GetSupplierName(), int64(index), filepath.Base(videoPath), credentialFreeURL(downloadURL),
			0, 0, func(_ *logrus.Logger, _ string) ([]byte, string, error) {
				return s.download(downloadURL)
			}, cacheKey,
		)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), "download failed:", err)
			continue
		}
		oneSub.Season = season
		oneSub.Episode = episode
		oneSub.AbsoluteEpisode = absoluteEpisode
		out = append(out, *oneSub)
		if len(out) >= settings.Get().AdvancedSettings.Topic {
			break
		}
	}
	return out, nil
}

func credentialFreeURL(downloadURL string) string {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return "subdl://download"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func (s *Supplier) download(downloadURL string) ([]byte, string, error) {
	request, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", errors.New("create SubDL download request failed")
	}
	request.Header.Set("User-Agent", "ChineseSubFinder-SubDL/1.0")
	client, err := s.httpClient(subDLDownloadTimeout)
	if err != nil {
		return nil, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", errors.New("SubDL download request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("SubDL download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, subDLMaxDownloadSize+1))
	if err != nil {
		return nil, "", errors.New("read SubDL download failed")
	}
	if len(data) > subDLMaxDownloadSize {
		return nil, "", errors.New("SubDL download exceeds 25 MiB safety limit")
	}
	return data, responseFileName(response.Header.Get("Content-Disposition"), downloadURL), nil
}

func responseFileName(contentDisposition, downloadURL string) string {
	if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
		if name := filepath.Base(strings.TrimSpace(params["filename"])); name != "." && name != "" {
			return name
		}
	}
	parsed, err := url.Parse(downloadURL)
	if err == nil {
		if name := filepath.Base(parsed.Path); name != "." && name != "/" && name != "" {
			return name
		}
	}
	return "subtitle.zip"
}

func strictMediaQuery(mediaInfo *models.MediaInfo, isMovie bool, season, episode int) (map[string]string, bool) {
	params := map[string]string{
		"languages":     "ZH",
		"subs_per_page": "30",
	}
	if isMovie {
		params["type"] = "movie"
	} else {
		params["type"] = "tv"
		params["season_number"] = fmt.Sprintf("%d", season)
		params["episode_number"] = fmt.Sprintf("%d", episode)
		params["full_season"] = "1"
		params["unpack"] = "1"
	}
	if strings.TrimSpace(mediaInfo.TmdbId) != "" {
		params["tmdb_id"] = strings.TrimSpace(mediaInfo.TmdbId)
		return params, true
	}
	if strings.TrimSpace(mediaInfo.ImdbId) != "" {
		params["imdb_id"] = strings.TrimSpace(mediaInfo.ImdbId)
		return params, true
	}
	return nil, false
}

func (s *Supplier) query(params map[string]string) (*apiResponse, error) {
	endpoint, err := url.Parse(settings.Get().AdvancedSettings.SuppliersSettings.SubDL.GetSearchUrl())
	if err != nil {
		return nil, errors.New("invalid SubDL API endpoint")
	}
	query := endpoint.Query()
	for key, value := range params {
		query.Set(key, value)
	}
	query.Set("api_key", settings.Get().SubtitleSources.SubDLSettings.ApiKey)
	endpoint.RawQuery = query.Encode()

	client, err := s.httpClient(subDLSearchTimeout)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("create SubDL API request failed")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ChineseSubFinder-SubDL/1.0")

	response, requestErr := client.Do(request)
	if requestErr != nil {
		return nil, errors.New("SubDL API request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("SubDL API returned HTTP %d", response.StatusCode)
	}
	var result apiResponse
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result); err != nil {
		return nil, errors.New("SubDL API returned an invalid response")
	}
	return &result, nil
}

func (s *Supplier) httpClient(timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if proxyAddress := local_http_proxy_server.GetProxyUrl(); proxyAddress != "" {
		parsedProxy, err := url.Parse(proxyAddress)
		if err != nil {
			return nil, errors.New("invalid configured HTTP proxy")
		}
		transport.Proxy = http.ProxyURL(parsedProxy)
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func responseMatchesMedia(response *apiResponse, mediaInfo *models.MediaInfo) bool {
	if response == nil || !response.Status || len(response.Results) == 0 {
		return false
	}
	wantTMDB := strings.TrimSpace(mediaInfo.TmdbId)
	wantIMDb := strings.TrimSpace(mediaInfo.ImdbId)
	wantIMDbNormalized := normalizeIMDbID(wantIMDb)
	for _, result := range response.Results {
		if wantTMDB != "" && strings.TrimSpace(string(result.TMDBID)) == wantTMDB {
			return true
		}
		if wantIMDbNormalized != "" && normalizeIMDbID(string(result.IMDbID)) == wantIMDbNormalized {
			return true
		}
	}
	return false
}

func normalizeIMDbID(value string) string {
	return strings.TrimLeft(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "tt"), "0")
}

func selectCandidates(items []subtitleItem, isMovie bool, season, episode int, absoluteEpisodes ...int) []subtitleItem {
	absoluteEpisode := 0
	if len(absoluteEpisodes) > 0 {
		absoluteEpisode = absoluteEpisodes[0]
	}
	type ranked struct {
		item     subtitleItem
		priority int
	}
	rankedItems := make([]ranked, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.URL) == "" || !isChineseLanguage(item) {
			continue
		}
		if isMovie {
			rankedItems = append(rankedItems, ranked{item: item, priority: 1})
			continue
		}
		if fullSeasonContains(item, season, episode) {
			rankedItems = append(rankedItems, ranked{item: item, priority: 0})
			continue
		}
		if candidateEpisode(item) == [2]int{season, episode} {
			rankedItems = append(rankedItems, ranked{item: item, priority: 1})
			continue
		}
		if absoluteEpisode > 0 && (episode_identity.FilenameContainsAbsoluteEpisode(item.ReleaseName, absoluteEpisode) ||
			episode_identity.FilenameContainsAbsoluteEpisode(item.Name, absoluteEpisode)) {
			rankedItems = append(rankedItems, ranked{item: item, priority: 2})
		}
	}
	sort.SliceStable(rankedItems, func(i, j int) bool {
		if rankedItems[i].priority != rankedItems[j].priority {
			return rankedItems[i].priority < rankedItems[j].priority
		}
		return rankedItems[i].item.ReleaseName < rankedItems[j].item.ReleaseName
	})
	out := make([]subtitleItem, 0, len(rankedItems))
	for _, one := range rankedItems {
		out = append(out, one.item)
	}
	return out
}

func isChineseLanguage(item subtitleItem) bool {
	language := strings.ToUpper(strings.TrimSpace(item.Lang + " " + item.Language))
	return strings.Contains(language, "ZH") || strings.Contains(language, "CHI") || strings.Contains(language, "CHINESE")
}

func fullSeasonContains(item subtitleItem, season, episode int) bool {
	if !bool(item.FullSeason) {
		return false
	}
	// SubDL v1 identifies season packs with full_season=true and the season
	// number; it does not always return unpack_files even when unpack=1.
	if int(item.Season) == season && season > 0 {
		return true
	}
	for _, unpacked := range item.UnpackFiles {
		if int(unpacked.SeasonNumber) == season && int(unpacked.EpisodeNumber) == episode {
			return true
		}
		if parsedEpisode(unpacked.Name) == [2]int{season, episode} {
			return true
		}
	}
	return false
}

func candidateEpisode(item subtitleItem) [2]int {
	if int(item.Season) > 0 && int(item.Episode) > 0 {
		return [2]int{int(item.Season), int(item.Episode)}
	}
	if parsed := parsedEpisode(item.ReleaseName); parsed != [2]int{} {
		return parsed
	}
	return parsedEpisode(item.Name)
}

func parsedEpisode(name string) [2]int {
	_, season, episode, err := decode.GetSeasonAndEpisodeFromSubFileName(name)
	if err != nil || season <= 0 || episode <= 0 {
		return [2]int{}
	}
	return [2]int{season, episode}
}

func safeDownloadURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if strings.HasPrefix(trimmed, "/") {
		trimmed = common.SubDLDownloadURLDef + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "dl.subdl.com") {
		return "", fmt.Errorf("unexpected host %q", parsed.Hostname())
	}
	return parsed.String(), nil
}
