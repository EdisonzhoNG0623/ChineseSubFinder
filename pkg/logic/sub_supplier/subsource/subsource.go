package subsource

import (
	"bytes"
	"context"
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
	"sync"
	"time"
	"unicode"

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
	apiBaseURL        = "https://api.subsource.net/api/v1/"
	requestTimeout    = 30 * time.Second
	downloadTimeout   = 2 * time.Minute
	maxJSONSize       = 4 << 20
	maxDownloadSize   = 25 << 20
	providerUserAgent = "ChineseSubFinder-SubSource/1.0"
)

type Supplier struct {
	log            *logrus.Logger
	fileDownloader *file_downloader.FileDownloader
	requestLock    sync.Mutex
	isAlive        bool
	baseURL        string
	lastRequestAt  time.Time
}

func NewSupplier(fileDownloader *file_downloader.FileDownloader) *Supplier {
	return &Supplier{log: fileDownloader.Log, fileDownloader: fileDownloader, isAlive: true, baseURL: apiBaseURL}
}

func (s *Supplier) CheckAlive() (bool, int64) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	started := time.Now()
	if err := s.configured(); err != nil {
		s.isAlive = false
		return false, 0
	}
	_, err := s.searchTitles(context.Background(), "tt0137523", nil, 0, 0)
	if err != nil {
		s.log.Warningln(s.GetSupplierName(), "Check Alive failed:", err)
		s.isAlive = false
		return false, 0
	}
	s.isAlive = true
	return true, time.Since(started).Milliseconds()
}

func (s *Supplier) IsAlive() bool                { return s.isAlive }
func (s *Supplier) GetSupplierName() string      { return common.SubSiteSubSource }
func (s *Supplier) GetLogger() *logrus.Logger    { return s.log }
func (s *Supplier) OverDailyDownloadLimit() bool { return false }
func (s *Supplier) GetSubListFromFile4Movie(path string) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4MovieContext(context.Background(), path)
}
func (s *Supplier) GetSubListFromFile4Series(info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4SeriesContext(context.Background(), info)
}
func (s *Supplier) GetSubListFromFile4Anime(info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4AnimeContext(context.Background(), info)
}

func (s *Supplier) GetSubListFromFile4MovieContext(ctx context.Context, videoPath string) ([]supplier.SubInfo, error) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	if err := s.configured(); err != nil {
		return nil, err
	}
	mediaInfo, err := mix_media_info.GetMixMediaInfo(s.fileDownloader.MediaInfoDealers, videoPath, true)
	if err != nil {
		return nil, fmt.Errorf("get media identity: %w", err)
	}
	year, _ := strconv.Atoi(firstFour(mediaInfo.Year))
	titles := []string{mediaInfo.OriginalTitle, mediaInfo.TitleEn, mediaInfo.TitleCn}
	movieID, err := s.searchTitles(ctx, mediaInfo.ImdbId, titles, year, 0)
	if err != nil || movieID <= 0 {
		return []supplier.SubInfo{}, err
	}
	items, err := s.querySubtitles(ctx, movieID, 0, 0)
	if err != nil {
		return nil, err
	}
	return s.downloadCandidates(ctx, videoPath, items, 0, 0, 0)
}

func (s *Supplier) GetSubListFromFile4SeriesContext(ctx context.Context, info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.downloadSeries(ctx, info)
}

func (s *Supplier) GetSubListFromFile4AnimeContext(ctx context.Context, info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.downloadSeries(ctx, info)
}

func (s *Supplier) downloadSeries(ctx context.Context, info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	if err := s.configured(); err != nil {
		return nil, err
	}
	bySeason := make(map[int][]series.EpisodeInfo)
	for _, episode := range info.NeedDlEpsKeyList {
		bySeason[episode.Season] = append(bySeason[episode.Season], episode)
	}
	seasons := make([]int, 0, len(bySeason))
	for season := range bySeason {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)
	titles := append([]string{info.Name}, info.Aliases...)
	out := make([]supplier.SubInfo, 0)
	for _, season := range seasons {
		movieID, err := s.searchTitles(ctx, info.ImdbId, titles, info.Year, season)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), "title search failed:", err)
			continue
		}
		if movieID <= 0 {
			continue
		}
		seasonItems, queryErr := s.querySubtitles(ctx, movieID, season, 0)
		if queryErr != nil {
			s.log.Warningln(s.GetSupplierName(), "season search failed:", queryErr)
			continue
		}
		for _, episode := range bySeason[season] {
			if err = ctx.Err(); err != nil {
				return out, err
			}
			selected := selectCandidates(seasonItems, episode.Season, episode.Episode, episode.AbsoluteEpisode)
			if len(selected) == 0 {
				episodeItems, episodeErr := s.querySubtitles(ctx, movieID, episode.Season, episode.Episode)
				if episodeErr == nil {
					selected = selectCandidates(episodeItems, episode.Season, episode.Episode, episode.AbsoluteEpisode)
				}
			}
			if len(selected) == 0 && episode.AbsoluteEpisode > 0 && episode.AbsoluteEpisode != episode.Episode {
				absoluteItems, absoluteErr := s.querySubtitles(ctx, movieID, 0, episode.AbsoluteEpisode)
				if absoluteErr != nil {
					s.log.Warningln(s.GetSupplierName(), "absolute episode search failed:", absoluteErr)
				} else {
					selected = selectAbsoluteCandidates(absoluteItems, episode.AbsoluteEpisode)
				}
			}
			found, downloadErr := s.downloadCandidates(ctx, episode.FileFullPath, selected, episode.Season, episode.Episode, episode.AbsoluteEpisode)
			if downloadErr != nil {
				s.log.Warningln(s.GetSupplierName(), episode.Title, downloadErr)
				continue
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

func (s *Supplier) configured() error {
	cfg := settings.Get().SubtitleSources.SubSourceSettings
	if !cfg.Enabled {
		return errors.New("SubSource is disabled")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("SubSource API key is required")
	}
	return nil
}

func (s *Supplier) searchTitles(ctx context.Context, imdbID string, titles []string, year, season int) (int64, error) {
	if strings.TrimSpace(imdbID) != "" {
		params := url.Values{"searchType": {"imdb"}, "imdb": {strings.TrimSpace(imdbID)}}
		if season > 0 {
			params.Set("season", strconv.Itoa(season))
		}
		response, err := s.queryTitles(ctx, params)
		if err != nil {
			return 0, err
		}
		for _, item := range response.Data {
			if id := int64(item.MovieID); id > 0 {
				return id, nil
			}
		}
	}
	wanted := normalizedTitles(titles)
	for _, title := range titles {
		if strings.TrimSpace(title) == "" {
			continue
		}
		params := url.Values{"searchType": {"text"}, "q": {strings.ToLower(strings.TrimSpace(title))}}
		if season > 0 {
			params.Set("season", strconv.Itoa(season))
		}
		response, err := s.queryTitles(ctx, params)
		if err != nil {
			return 0, err
		}
		for _, item := range response.Data {
			if int64(item.MovieID) <= 0 || (year > 0 && int(item.ReleaseYear) > 0 && int(item.ReleaseYear) != year) {
				continue
			}
			if _, ok := wanted[normalizeTitle(item.Title)]; ok {
				return int64(item.MovieID), nil
			}
			if _, ok := wanted[normalizeTitle(item.AlternateTitle)]; ok {
				return int64(item.MovieID), nil
			}
		}
	}
	return 0, nil
}

func (s *Supplier) queryTitles(ctx context.Context, params url.Values) (*titleSearchResponse, error) {
	var response titleSearchResponse
	if err := s.getJSON(ctx, "movies/search?"+params.Encode(), &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (s *Supplier) querySubtitles(ctx context.Context, movieID int64, season, episode int) ([]subtitleItem, error) {
	params := url.Values{
		"language": {"chinese bg code"},
		"limit":    {"100"},
		"movieId":  {strconv.FormatInt(movieID, 10)},
	}
	if season > 0 {
		params.Set("seasonNumber", strconv.Itoa(season))
	}
	if episode > 0 {
		params.Set("episodeNumber", strconv.Itoa(episode))
	}
	var response subtitleSearchResponse
	if err := s.getJSON(ctx, "subtitles?"+params.Encode(), &response); err != nil {
		return nil, err
	}
	if !response.Success && response.SuccessPresent {
		return []subtitleItem{}, nil
	}
	valid := make([]subtitleItem, 0, len(response.Data))
	for _, item := range response.Data {
		if int64(item.SubtitleID) <= 0 || len(item.ReleaseInfo) == 0 || !isChinese(item.Language) {
			continue
		}
		valid = append(valid, item)
	}
	return valid, nil
}

func (s *Supplier) getJSON(ctx context.Context, path string, target interface{}) error {
	request, err := s.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err = s.waitRateLimit(ctx); err != nil {
		return err
	}
	response, err := s.client(requestTimeout).Do(request)
	if err != nil {
		return errors.New("SubSource API request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("SubSource API returned HTTP %d", response.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, maxJSONSize)).Decode(target); err != nil {
		return errors.New("SubSource API returned an invalid response")
	}
	return nil
}

func selectCandidates(items []subtitleItem, season, episode, absolute int) []subtitleItem {
	type ranked struct {
		item     subtitleItem
		priority int
	}
	rankedItems := make([]ranked, 0, len(items))
	for _, item := range items {
		candidateSeason, candidateEpisode := itemEpisode(item)
		switch {
		case candidateSeason == season && candidateEpisode == episode:
			rankedItems = append(rankedItems, ranked{item: item, priority: 0})
		case candidateEpisode == 0 && (candidateSeason == 0 || candidateSeason == season):
			rankedItems = append(rankedItems, ranked{item: item, priority: 1})
		case absolute > 0 && releaseContainsAbsolute(item.ReleaseInfo, absolute):
			rankedItems = append(rankedItems, ranked{item: item, priority: 2})
		}
	}
	sort.SliceStable(rankedItems, func(i, j int) bool { return rankedItems[i].priority < rankedItems[j].priority })
	out := make([]subtitleItem, 0, len(rankedItems))
	for _, item := range rankedItems {
		out = append(out, item.item)
	}
	return out
}

func selectAbsoluteCandidates(items []subtitleItem, absolute int) []subtitleItem {
	out := make([]subtitleItem, 0)
	for _, item := range items {
		_, episode := itemEpisode(item)
		if episode == absolute || releaseContainsAbsolute(item.ReleaseInfo, absolute) {
			out = append(out, item)
		}
	}
	return out
}

func itemEpisode(item subtitleItem) (int, int) {
	if int(item.SeasonNumber) > 0 || int(item.EpisodeNumber) > 0 {
		return int(item.SeasonNumber), int(item.EpisodeNumber)
	}
	for _, release := range item.ReleaseInfo {
		_, season, episode, err := decode.GetSeasonAndEpisodeFromSubFileName(release)
		if err == nil && (season > 0 || episode > 0) {
			return season, episode
		}
	}
	return 0, 0
}

func releaseContainsAbsolute(releases []string, episode int) bool {
	for _, release := range releases {
		if episode_identity.FilenameContainsAbsoluteEpisode(release, episode) {
			return true
		}
	}
	return false
}

func (s *Supplier) downloadCandidates(ctx context.Context, videoPath string, items []subtitleItem, season, episode, absolute int) ([]supplier.SubInfo, error) {
	limit := settings.Get().AdvancedSettings.Topic
	if limit <= 0 {
		return []supplier.SubInfo{}, nil
	}
	out := make([]supplier.SubInfo, 0, limit)
	for _, item := range items {
		candidateSeason, candidateEpisode := itemEpisode(item)
		isPack := candidateEpisode == 0
		cacheKey := fmt.Sprintf("subsource-%d", int64(item.SubtitleID))
		oneSub, err := s.fileDownloader.GetWithCustomDownloader(
			s.GetSupplierName(), int64(len(out)), filepath.Base(videoPath), "subsource://subtitle/"+strconv.FormatInt(int64(item.SubtitleID), 10),
			0, 0, func(_ *logrus.Logger, _ string) ([]byte, string, error) {
				return s.download(ctx, int64(item.SubtitleID))
			}, cacheKey,
		)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), "download failed:", err)
			continue
		}
		oneSub.Name = firstRelease(item.ReleaseInfo, filepath.Base(videoPath))
		oneSub.AbsoluteEpisode = absolute
		if isPack {
			oneSub.Season = season
			if candidateSeason > 0 {
				oneSub.Season = candidateSeason
			}
			oneSub.Episode = 0
			oneSub.IsFullSeason = true
		} else {
			oneSub.Season, oneSub.Episode = season, episode
		}
		out = append(out, *oneSub)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Supplier) download(ctx context.Context, subtitleID int64) ([]byte, string, error) {
	request, err := s.newRequest(ctx, http.MethodGet, "subtitles/"+strconv.FormatInt(subtitleID, 10)+"/download", nil)
	if err != nil {
		return nil, "", err
	}
	if err = s.waitRateLimit(ctx); err != nil {
		return nil, "", err
	}
	response, err := s.client(downloadTimeout).Do(request)
	if err != nil {
		return nil, "", errors.New("SubSource download request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("SubSource download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadSize+1))
	if err != nil || len(data) == 0 {
		return nil, "", errors.New("read SubSource download failed")
	}
	if len(data) > maxDownloadSize {
		return nil, "", errors.New("SubSource download exceeds 25 MiB safety limit")
	}
	return data, responseFileName(response.Header.Get("Content-Disposition"), subtitleID), nil
}

func (s *Supplier) waitRateLimit(ctx context.Context) error {
	wait := time.Until(s.lastRequestAt.Add(1050 * time.Millisecond))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.lastRequestAt = time.Now()
	return nil
}

func (s *Supplier) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, body)
	if err != nil {
		return nil, errors.New("create SubSource API request failed")
	}
	request.Header.Set("X-API-Key", settings.Get().SubtitleSources.SubSourceSettings.APIKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", providerUserAgent)
	return request, nil
}

func (s *Supplier) client(timeout time.Duration) *http.Client {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if proxyAddress := local_http_proxy_server.GetProxyUrl(); proxyAddress != "" {
		if proxyURL, err := url.Parse(proxyAddress); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), "api.subsource.net") {
				return errors.New("SubSource redirect target is not allowed")
			}
			return nil
		},
	}
}

func responseFileName(disposition string, subtitleID int64) string {
	if _, params, err := mime.ParseMediaType(disposition); err == nil {
		if name := filepath.Base(strings.TrimSpace(params["filename"])); name != "" && name != "." && name != "/" {
			return name
		}
	}
	return fmt.Sprintf("subsource-%d.zip", subtitleID)
}

func isChinese(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "chinese") || value == "zh" || value == "zho" || value == "chi" || strings.HasPrefix(value, "zh-")
}

func firstRelease(releases []string, fallback string) string {
	for _, release := range releases {
		if strings.TrimSpace(release) != "" {
			return strings.TrimSpace(release)
		}
	}
	return fallback
}

type titleSearchResponse struct {
	Data []titleItem `json:"data"`
}

type titleItem struct {
	MovieID        flexInt `json:"movieId"`
	Title          string  `json:"title"`
	AlternateTitle string  `json:"alternateTitle"`
	ReleaseYear    flexInt `json:"releaseYear"`
}

func normalizedTitles(titles []string) map[string]struct{} {
	out := make(map[string]struct{}, len(titles))
	for _, title := range titles {
		if normalized := normalizeTitle(title); normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func normalizeTitle(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
}

func firstFour(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 {
		return value[:4]
	}
	return value
}

type subtitleSearchResponse struct {
	Success        bool           `json:"success"`
	SuccessPresent bool           `json:"-"`
	Data           []subtitleItem `json:"data"`
}

func (r *subtitleSearchResponse) UnmarshalJSON(data []byte) error {
	type alias subtitleSearchResponse
	var raw struct {
		Success *bool          `json:"success"`
		Data    []subtitleItem `json:"data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Success != nil {
		r.Success, r.SuccessPresent = *raw.Success, true
	}
	r.Data = raw.Data
	return nil
}

type subtitleItem struct {
	SubtitleID    flexInt  `json:"subtitleId"`
	Language      string   `json:"language"`
	ReleaseInfo   []string `json:"releaseInfo"`
	SeasonNumber  flexInt  `json:"seasonNumber"`
	EpisodeNumber flexInt  `json:"episodeNumber"`
}

type flexInt int64

func (value *flexInt) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		*value = 0
		return nil
	}
	var number int64
	if err := json.Unmarshal(data, &number); err == nil {
		*value = flexInt(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return err
	}
	*value = flexInt(parsed)
	return nil
}
