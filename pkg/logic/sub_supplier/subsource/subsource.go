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

// SubSource expects these API identifiers, not the display labels shown in
// subtitle results. Try the historically broad BG-code bucket first, then
// more specific variants until enough downloadable candidates are found.
var chineseLanguageCodes = []string{
	"chinese_bg_code",
	"chinese_bilingual",
	"chinese_simplified",
	"chinese_traditional",
	"chinese",
	"big_5_code",
	"chinese_cantonese",
}

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
	items, err := s.queryChineseSubtitles(ctx, movieID, settings.Get().AdvancedSettings.Topic)
	if err != nil {
		return nil, err
	}
	candidates := make([]downloadableCandidate, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, downloadableCandidate{item: item})
	}
	return s.downloadCandidates(ctx, videoPath, candidates)
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
		// Query every supported Chinese category for series: a category can
		// contain unrelated episodes even when it already exceeds Topic.
		seasonItems, queryErr := s.queryChineseSubtitles(ctx, movieID, 0)
		if queryErr != nil {
			s.log.Warningln(s.GetSupplierName(), "season search failed:", queryErr)
			continue
		}
		candidates := selectSeriesCandidates(seasonItems, season, bySeason[season])
		if len(candidates) == 0 || len(bySeason[season]) == 0 {
			continue
		}
		// /movies/search?season=N returns a season-scoped movie ID. The
		// subtitles endpoint has no episode filter or episode fields, so fetch
		// each chosen archive once and let the archive episode resolver map it
		// back to every requested local episode.
		representative := bySeason[season][0]
		found, downloadErr := s.downloadCandidates(ctx, representative.FileFullPath, candidates)
		if downloadErr != nil {
			s.log.Warningln(s.GetSupplierName(), representative.Title, downloadErr)
			continue
		}
		out = append(out, found...)
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

func (s *Supplier) queryChineseSubtitles(ctx context.Context, movieID int64, stopAfter int) ([]subtitleItem, error) {
	capacity := stopAfter
	if capacity < 1 {
		capacity = 1
	}
	items := make([]subtitleItem, 0, capacity)
	seen := make(map[int64]struct{}, capacity)
	var firstErr error
	for _, languageCode := range chineseLanguageCodes {
		languageItems, err := s.querySubtitles(ctx, movieID, languageCode)
		if err != nil {
			if ctx.Err() != nil {
				return items, ctx.Err()
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, item := range languageItems {
			id := int64(item.SubtitleID)
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			items = append(items, item)
		}
		if stopAfter > 0 && len(items) >= stopAfter {
			break
		}
	}
	if len(items) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return items, nil
}

func (s *Supplier) querySubtitles(ctx context.Context, movieID int64, languageCode string) ([]subtitleItem, error) {
	params := url.Values{
		"language": {languageCode},
		"limit":    {"100"},
		"movieId":  {strconv.FormatInt(movieID, 10)},
		"sort":     {"popular"},
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
		if int64(item.SubtitleID) <= 0 || !isChinese(item.Language) {
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

type downloadableCandidate struct {
	item            subtitleItem
	season          int
	episode         int
	absoluteEpisode int
	fullSeason      bool
}

func selectSeriesCandidates(items []subtitleItem, season int, episodes []series.EpisodeInfo) []downloadableCandidate {
	type ranked struct {
		candidate downloadableCandidate
		priority  int
	}
	rankedItems := make([]ranked, 0, len(items))
	for _, item := range items {
		candidateSeason, candidateEpisode := itemEpisode(item)
		matched := false
		for _, target := range episodes {
			if candidateEpisode > 0 && ((candidateSeason == target.Season && candidateEpisode == target.Episode) ||
				(candidateSeason == 0 && candidateEpisode == target.Episode)) {
				rankedItems = append(rankedItems, ranked{candidate: downloadableCandidate{
					item: item, season: target.Season, episode: target.Episode, absoluteEpisode: target.AbsoluteEpisode,
				}})
				matched = true
				break
			}
			if target.AbsoluteEpisode > 0 && target.AbsoluteEpisode != target.Episode &&
				releaseContainsAbsolute(item.ReleaseInfo, target.AbsoluteEpisode) {
				rankedItems = append(rankedItems, ranked{candidate: downloadableCandidate{
					item: item, season: target.Season, episode: target.Episode, absoluteEpisode: target.AbsoluteEpisode,
				}})
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if candidateEpisode == 0 && (candidateSeason == 0 || candidateSeason == season) {
			rankedItems = append(rankedItems, ranked{candidate: downloadableCandidate{
				item: item, season: season, fullSeason: true,
			}, priority: 1})
		}
	}
	sort.SliceStable(rankedItems, func(i, j int) bool { return rankedItems[i].priority < rankedItems[j].priority })
	out := make([]downloadableCandidate, 0, len(rankedItems))
	for _, item := range rankedItems {
		out = append(out, item.candidate)
	}
	return out
}

func itemEpisode(item subtitleItem) (int, int) {
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

func (s *Supplier) downloadCandidates(ctx context.Context, videoPath string, candidates []downloadableCandidate) ([]supplier.SubInfo, error) {
	limit := settings.Get().AdvancedSettings.Topic
	if limit <= 0 {
		return []supplier.SubInfo{}, nil
	}
	out := make([]supplier.SubInfo, 0, limit)
	for _, candidate := range candidates {
		item := candidate.item
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
		oneSub.AbsoluteEpisode = candidate.absoluteEpisode
		if candidate.fullSeason {
			oneSub.Season = candidate.season
			oneSub.Episode = 0
			oneSub.IsFullSeason = true
		} else {
			oneSub.Season, oneSub.Episode = candidate.season, candidate.episode
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
	SubtitleID  flexInt  `json:"subtitleId"`
	Language    string   `json:"language"`
	ReleaseInfo []string `json:"releaseInfo"`
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
