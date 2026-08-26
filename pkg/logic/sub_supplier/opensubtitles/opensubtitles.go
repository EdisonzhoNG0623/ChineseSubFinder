package opensubtitles

import (
	"bytes"
	"context"
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
	"sync"
	"time"

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
	apiBaseURL        = "https://api.opensubtitles.com/api/v1/"
	requestTimeout    = 30 * time.Second
	downloadTimeout   = 2 * time.Minute
	maxJSONSize       = 4 << 20
	maxDownloadSize   = 25 << 20
	tokenLifetime     = 23 * time.Hour
	providerUserAgent = "ChineseSubFinder v1"
)

type Supplier struct {
	log            *logrus.Logger
	fileDownloader *file_downloader.FileDownloader
	requestLock    sync.Mutex
	isAlive        bool
	token          string
	tokenConfig    string
	tokenExpiresAt time.Time
	remaining      int
	baseURL        string
}

func NewSupplier(fileDownloader *file_downloader.FileDownloader) *Supplier {
	return &Supplier{log: fileDownloader.Log, fileDownloader: fileDownloader, isAlive: true, remaining: -1, baseURL: apiBaseURL}
}

func (s *Supplier) CheckAlive() (bool, int64) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	started := time.Now()
	if err := s.configured(); err != nil {
		s.isAlive = false
		return false, 0
	}
	if err := s.ensureToken(context.Background()); err != nil {
		s.log.Warningln(s.GetSupplierName(), "Check Alive failed:", err)
		s.isAlive = false
		return false, 0
	}
	s.isAlive = true
	return true, time.Since(started).Milliseconds()
}

func (s *Supplier) IsAlive() bool             { return s.isAlive }
func (s *Supplier) GetSupplierName() string   { return common.SubSiteOpenSubtitles }
func (s *Supplier) GetLogger() *logrus.Logger { return s.log }
func (s *Supplier) OverDailyDownloadLimit() bool {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	return s.remaining == 0
}
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
	params := make(url.Values)
	params.Set("languages", "zh-cn,zh-tw")
	if id := numericIMDbID(mediaInfo.ImdbId); id != "" {
		params.Set("imdb_id", id)
	} else if id := strings.TrimSpace(mediaInfo.TmdbId); id != "" {
		params.Set("tmdb_id", id)
	} else {
		return []supplier.SubInfo{}, nil
	}
	if settings.Get().SubtitleSources.OpenSubtitlesSettings.UseHash {
		if hash, hashErr := movieHash(videoPath); hashErr == nil {
			params.Set("moviehash", hash)
		}
	}
	items, err := s.search(ctx, params, 0, 0)
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
	parentIMDb := numericIMDbID(info.ImdbId)
	parentTMDB := strings.TrimSpace(info.TmdbId)
	if parentIMDb == "" && parentTMDB == "" {
		return []supplier.SubInfo{}, nil
	}
	out := make([]supplier.SubInfo, 0)
	for _, episode := range info.NeedDlEpsKeyList {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		params := make(url.Values)
		params.Set("languages", "zh-cn,zh-tw")
		if parentIMDb != "" {
			params.Set("parent_imdb_id", parentIMDb)
		} else {
			params.Set("parent_tmdb_id", parentTMDB)
		}
		params.Set("season_number", strconv.Itoa(episode.Season))
		params.Set("episode_number", strconv.Itoa(episode.Episode))
		if settings.Get().SubtitleSources.OpenSubtitlesSettings.UseHash {
			if hash, hashErr := movieHash(episode.FileFullPath); hashErr == nil {
				params.Set("moviehash", hash)
			}
		}
		items, err := s.search(ctx, params, episode.Season, episode.Episode)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), episode.Title, err)
			continue
		}
		if len(items) == 0 && episode.AbsoluteEpisode > 0 && episode.AbsoluteEpisode != episode.Episode {
			params.Set("episode_number", strconv.Itoa(episode.AbsoluteEpisode))
			params.Del("season_number")
			items, err = s.search(ctx, params, 0, episode.AbsoluteEpisode)
			if err != nil {
				s.log.Warningln(s.GetSupplierName(), "absolute episode search failed:", err)
				continue
			}
		}
		found, err := s.downloadCandidates(ctx, episode.FileFullPath, items, episode.Season, episode.Episode, episode.AbsoluteEpisode)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), episode.Title, err)
			continue
		}
		out = append(out, found...)
	}
	return out, nil
}

func (s *Supplier) configured() error {
	cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
	if !cfg.Enabled {
		return errors.New("OpenSubtitles is disabled")
	}
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Username) == "" || cfg.Password == "" {
		return errors.New("OpenSubtitles API key, username, and password are required")
	}
	return nil
}

func (s *Supplier) ensureToken(ctx context.Context) error {
	cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
	configHash := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.APIKey+"\x00"+cfg.Username+"\x00"+cfg.Password)))
	if s.token != "" && s.tokenConfig == configHash && time.Now().Before(s.tokenExpiresAt) {
		return nil
	}
	body, err := json.Marshal(map[string]string{"username": cfg.Username, "password": cfg.Password})
	if err != nil {
		return errors.New("encode OpenSubtitles login failed")
	}
	request, err := s.newRequest(ctx, http.MethodPost, "login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	response, err := s.client(requestTimeout, false).Do(request)
	if err != nil {
		return errors.New("OpenSubtitles login request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OpenSubtitles login returned HTTP %d", response.StatusCode)
	}
	var result loginResponse
	if err = decodeJSON(response.Body, &result); err != nil || strings.TrimSpace(result.Token) == "" {
		return errors.New("OpenSubtitles login returned an invalid response")
	}
	s.token, s.tokenConfig, s.tokenExpiresAt = result.Token, configHash, time.Now().Add(tokenLifetime)
	return nil
}

func (s *Supplier) search(ctx context.Context, params url.Values, season, episode int) ([]searchItem, error) {
	if err := s.ensureToken(ctx); err != nil {
		return nil, err
	}
	cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
	if !cfg.IncludeAITranslated {
		params.Set("ai_translated", "exclude")
	}
	if cfg.IncludeMachineTranslated {
		params.Set("machine_translated", "include")
	} else {
		params.Set("machine_translated", "exclude")
	}
	request, err := s.newRequest(ctx, http.MethodGet, "subtitles?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client(requestTimeout, false).Do(request)
	if err != nil {
		return nil, errors.New("OpenSubtitles search request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenSubtitles search returned HTTP %d", response.StatusCode)
	}
	var result searchResponse
	if err = decodeJSON(response.Body, &result); err != nil {
		return nil, errors.New("OpenSubtitles search returned an invalid response")
	}
	items := make([]searchItem, 0, len(result.Data))
	for _, item := range result.Data {
		attributes := item.Attributes
		if len(attributes.Files) == 0 || attributes.Files[0].FileID <= 0 || !isChinese(attributes.Language) {
			continue
		}
		if (!cfg.IncludeAITranslated && attributes.AITranslated) ||
			(!cfg.IncludeMachineTranslated && attributes.MachineTranslated) {
			continue
		}
		if season > 0 && attributes.FeatureDetails.SeasonNumber != season {
			continue
		}
		if episode > 0 && attributes.FeatureDetails.EpisodeNumber != episode {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].Attributes, items[j].Attributes
		if left.MovieHashMatch != right.MovieHashMatch {
			return left.MovieHashMatch
		}
		if left.FromTrusted != right.FromTrusted {
			return left.FromTrusted
		}
		return left.DownloadCount > right.DownloadCount
	})
	return items, nil
}

func (s *Supplier) downloadCandidates(ctx context.Context, videoPath string, items []searchItem, season, episode, absolute int) ([]supplier.SubInfo, error) {
	limit := settings.Get().AdvancedSettings.Topic
	if limit <= 0 {
		return []supplier.SubInfo{}, nil
	}
	out := make([]supplier.SubInfo, 0, limit)
	for _, item := range items {
		file := item.Attributes.Files[0]
		cacheKey := fmt.Sprintf("opensubtitles-%d", file.FileID)
		oneSub, err := s.fileDownloader.GetWithCustomDownloader(
			s.GetSupplierName(), int64(len(out)), filepath.Base(videoPath), "opensubtitles://file/"+strconv.FormatInt(file.FileID, 10),
			0, 0, func(_ *logrus.Logger, _ string) ([]byte, string, error) {
				return s.download(ctx, file.FileID)
			}, cacheKey,
		)
		if err != nil {
			s.log.Warningln(s.GetSupplierName(), "download failed:", err)
			continue
		}
		oneSub.Name = firstNonEmpty(item.Attributes.Release, file.FileName, filepath.Base(videoPath))
		oneSub.Season, oneSub.Episode, oneSub.AbsoluteEpisode = season, episode, absolute
		out = append(out, *oneSub)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Supplier) download(ctx context.Context, fileID int64) ([]byte, string, error) {
	if err := s.ensureToken(ctx); err != nil {
		return nil, "", err
	}
	body, _ := json.Marshal(map[string]interface{}{"file_id": fileID, "sub_format": "srt"})
	request, err := s.newRequest(ctx, http.MethodPost, "download", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	response, err := s.client(requestTimeout, false).Do(request)
	if err != nil {
		return nil, "", errors.New("OpenSubtitles download authorization failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotAcceptable {
		s.remaining = 0
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("OpenSubtitles download authorization returned HTTP %d", response.StatusCode)
	}
	var result downloadResponse
	if err = decodeJSON(response.Body, &result); err != nil || strings.TrimSpace(result.Link) == "" {
		return nil, "", errors.New("OpenSubtitles download authorization returned an invalid response")
	}
	if result.Remaining != nil {
		s.remaining = *result.Remaining
	}
	link, err := safeDownloadURL(result.Link)
	if err != nil {
		return nil, "", errors.New("OpenSubtitles returned an unsafe download URL")
	}
	downloadRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, "", errors.New("create OpenSubtitles file request failed")
	}
	downloadRequest.Header.Set("User-Agent", providerUserAgent)
	fileResponse, err := s.client(downloadTimeout, true).Do(downloadRequest)
	if err != nil {
		return nil, "", errors.New("OpenSubtitles file request failed")
	}
	defer fileResponse.Body.Close()
	if fileResponse.StatusCode < 200 || fileResponse.StatusCode >= 300 {
		return nil, "", fmt.Errorf("OpenSubtitles file returned HTTP %d", fileResponse.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(fileResponse.Body, maxDownloadSize+1))
	if err != nil || len(data) == 0 {
		return nil, "", errors.New("read OpenSubtitles file failed")
	}
	if len(data) > maxDownloadSize {
		return nil, "", errors.New("OpenSubtitles file exceeds 25 MiB safety limit")
	}
	return data, safeFileName(result.FileName, fileResponse.Header.Get("Content-Disposition"), link), nil
}

func (s *Supplier) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	endpoint, err := url.Parse(s.baseURL + path)
	if err != nil {
		return nil, errors.New("invalid OpenSubtitles API endpoint")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, errors.New("create OpenSubtitles API request failed")
	}
	cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
	request.Header.Set("Api-Key", cfg.APIKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", providerUserAgent)
	if s.token != "" && path != "login" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	return request, nil
}

func (s *Supplier) client(timeout time.Duration, restrictDownloadHost bool) *http.Client {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if proxyAddress := local_http_proxy_server.GetProxyUrl(); proxyAddress != "" {
		if proxyURL, err := url.Parse(proxyAddress); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	if restrictDownloadHost {
		client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
			if _, err := safeDownloadURL(request.URL.String()); err != nil {
				return err
			}
			return nil
		}
	} else {
		base, _ := url.Parse(s.baseURL)
		client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
			if base == nil || !strings.EqualFold(request.URL.Scheme, base.Scheme) ||
				!strings.EqualFold(request.URL.Host, base.Host) {
				return errors.New("OpenSubtitles API redirect target is not allowed")
			}
			return nil
		}
	}
	return client
}

func decodeJSON(reader io.Reader, target interface{}) error {
	return json.NewDecoder(io.LimitReader(reader, maxJSONSize)).Decode(target)
}

func numericIMDbID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "tt")
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return ""
	}
	if _, err := strconv.ParseUint(value, 10, 64); err != nil {
		return ""
	}
	return value
}

func isChinese(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "zh" || value == "zho" || value == "chi" || strings.HasPrefix(value, "zh-")
}

func safeDownloadURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return "", errors.New("invalid HTTPS URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "opensubtitles.com" && !strings.HasSuffix(host, ".opensubtitles.com") {
		return "", fmt.Errorf("unexpected host %q", host)
	}
	return parsed.String(), nil
}

func safeFileName(apiName, disposition, rawURL string) string {
	for _, name := range []string{apiName, dispositionFileName(disposition), pathName(rawURL)} {
		name = filepath.Base(strings.TrimSpace(name))
		if name != "" && name != "." && name != "/" {
			return name
		}
	}
	return "subtitle.srt"
}

func dispositionFileName(value string) string {
	_, params, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return params["filename"]
}

func pathName(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return filepath.Base(parsed.Path)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "subtitle"
}

type loginResponse struct {
	Token string `json:"token"`
}

type searchResponse struct {
	Data []searchItem `json:"data"`
}

type searchItem struct {
	Attributes searchAttributes `json:"attributes"`
}

type searchAttributes struct {
	Language          string         `json:"language"`
	DownloadCount     int            `json:"download_count"`
	FromTrusted       bool           `json:"from_trusted"`
	AITranslated      bool           `json:"ai_translated"`
	MachineTranslated bool           `json:"machine_translated"`
	MovieHashMatch    bool           `json:"moviehash_match"`
	Release           string         `json:"release"`
	FeatureDetails    featureDetails `json:"feature_details"`
	Files             []subtitleFile `json:"files"`
}

type featureDetails struct {
	SeasonNumber  int `json:"season_number"`
	EpisodeNumber int `json:"episode_number"`
}

type subtitleFile struct {
	FileID   int64  `json:"file_id"`
	FileName string `json:"file_name"`
}

type downloadResponse struct {
	Link      string `json:"link"`
	FileName  string `json:"file_name"`
	Remaining *int   `json:"remaining"`
}
