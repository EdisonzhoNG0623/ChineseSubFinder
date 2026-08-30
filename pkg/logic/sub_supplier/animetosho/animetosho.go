package animetosho

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/local_http_proxy_server"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/file_downloader"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/language"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
	"github.com/ulikunitz/xz"
)

const (
	requestTimeout      = 25 * time.Second
	downloadTimeout     = 2 * time.Minute
	requestInterval     = 400 * time.Millisecond
	maxJSONSize         = 4 << 20
	maxCompressedSize   = 5 << 20
	maxDecompressedSize = 5 << 20
	maxSearchItems      = 150
	maxDetailRequests   = 24
	maxRequestAttempts  = 3
	retryBaseDelay      = 500 * time.Millisecond
	providerUserAgent   = "ChineseSubFinder-AnimeTosho/1.0"
)

type Supplier struct {
	log               *logrus.Logger
	fileDownloader    *file_downloader.FileDownloader
	requestLock       sync.Mutex
	isAlive           bool
	baseURL           string
	attachmentBaseURL string
	lastRequestAt     time.Time
	httpClientFactory func(time.Duration, string) (*http.Client, error)
}

func NewSupplier(downloader *file_downloader.FileDownloader) *Supplier {
	return &Supplier{
		log:               downloader.Log,
		fileDownloader:    downloader,
		isAlive:           true,
		baseURL:           common.AnimeToshoRootURLDef,
		attachmentBaseURL: common.AnimeToshoAttachmentRootURLDef,
	}
}

func (s *Supplier) CheckAlive() (bool, int64) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	started := time.Now()
	if err := s.configured(); err != nil {
		s.isAlive = false
		return false, 0
	}
	var items []feedItem
	err := s.getJSON(context.Background(), s.searchEndpoint(url.Values{"q": {"Frieren"}}), &items)
	s.isAlive = err == nil
	if err != nil {
		s.log.Warningln(s.GetSupplierName(), "Check Alive failed:", err)
		return false, 0
	}
	return true, time.Since(started).Milliseconds()
}

func (s *Supplier) IsAlive() bool             { return s.isAlive }
func (s *Supplier) GetSupplierName() string   { return common.SubSiteAnimeTosho }
func (s *Supplier) GetLogger() *logrus.Logger { return s.log }

func (s *Supplier) OverDailyDownloadLimit() bool {
	one := settings.Get().AdvancedSettings.SuppliersSettings.AnimeTosho
	if one == nil || one.DailyDownloadLimit == 0 {
		return true
	}
	if one.DailyDownloadLimit < 0 {
		return false
	}
	count, err := s.fileDownloader.CacheCenter.DailyDownloadCountGet(
		s.GetSupplierName(), pkg.GetPublicIP(s.log, settings.Get().AdvancedSettings.TaskQueue))
	if err != nil {
		s.log.Warningln(s.GetSupplierName(), "DailyDownloadCountGet", err)
		return true
	}
	return one.OverDailyDownloadLimit(count)
}

func (s *Supplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	return []supplier.SubInfo{}, nil
}

func (s *Supplier) GetSubListFromFile4Series(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return []supplier.SubInfo{}, nil
}

func (s *Supplier) GetSubListFromFile4Anime(info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4AnimeContext(context.Background(), info)
}

func (s *Supplier) GetSubListFromFile4AnimeContext(ctx context.Context, info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	if err := s.configured(); err != nil {
		return nil, err
	}
	if info == nil || !info.IsAnime || len(info.NeedDlEpsKeyList) == 0 {
		return []supplier.SubInfo{}, nil
	}
	titles := uniqueTitles(append([]string{info.Name}, info.Aliases...), 3)
	if len(titles) == 0 {
		return []supplier.SubInfo{}, nil
	}
	items, err := s.search(ctx, titles)
	if err != nil {
		return nil, err
	}
	episodes := sortedEpisodes(info.NeedDlEpsKeyList)
	limit := settings.Get().AdvancedSettings.Topic
	if limit <= 0 {
		return []supplier.SubInfo{}, nil
	}
	results := make([]supplier.SubInfo, 0)
	detailCount := 0
	seenAttachments := make(map[int64]struct{})
	for _, episode := range episodes {
		matched := matchingFeedItems(items, titles, episode)
		foundForEpisode := 0
		for _, item := range matched {
			if detailCount >= maxDetailRequests || foundForEpisode >= limit {
				break
			}
			detailCount++
			detail, detailErr := s.detail(ctx, item.ID)
			if detailErr != nil {
				s.log.Warningln(s.GetSupplierName(), "detail request failed:", detailErr)
				continue
			}
			attachments := selectEpisodeAttachments(detail, episode)
			for _, attachment := range attachments {
				if foundForEpisode >= limit {
					break
				}
				if _, exists := seenAttachments[attachment.ID]; exists {
					continue
				}
				downloadURL, buildErr := s.attachmentURL(detail, item, attachment)
				if buildErr != nil {
					s.log.Warningln(s.GetSupplierName(), "skip unsafe attachment:", buildErr)
					continue
				}
				cacheKey := fmt.Sprintf("animetosho-%x", sha256.Sum256([]byte(strconv.FormatInt(attachment.ID, 10))))
				oneSub, downloadErr := s.fileDownloader.GetWithCustomDownloader(
					s.GetSupplierName(), int64(foundForEpisode), filepath.Base(episode.FileFullPath),
					"animetosho://attachment/"+strconv.FormatInt(attachment.ID, 10), 0, 0,
					func(_ *logrus.Logger, _ string) ([]byte, string, error) {
						return s.downloadAttachment(ctx, downloadURL, attachment)
					}, cacheKey)
				if downloadErr != nil {
					s.log.Warningln(s.GetSupplierName(), "download failed:", downloadErr)
					continue
				}
				oneSub.Name = firstNonEmpty(detail.TorrentName, item.TorrentName, filepath.Base(episode.FileFullPath))
				oneSub.Season = episode.Season
				oneSub.Episode = episode.Episode
				oneSub.AbsoluteEpisode = episode.AbsoluteEpisode
				oneSub.Language = attachmentLanguage(attachment)
				results = append(results, *oneSub)
				seenAttachments[attachment.ID] = struct{}{}
				foundForEpisode++
			}
		}
	}
	return results, nil
}

func (s *Supplier) configured() error {
	if !settings.Get().SubtitleSources.AnimeToshoSettings.Enabled {
		return errors.New("AnimeTosho is disabled")
	}
	return nil
}

func (s *Supplier) search(ctx context.Context, titles []string) ([]feedItem, error) {
	items := make([]feedItem, 0)
	seen := make(map[int64]struct{})
	var firstErr error
	for _, title := range titles {
		var response []feedItem
		if err := s.getJSON(ctx, s.searchEndpoint(url.Values{"q": {title}}), &response); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, item := range response {
			if item.ID <= 0 || len(items) >= maxSearchItems {
				continue
			}
			if _, exists := seen[item.ID]; exists {
				continue
			}
			seen[item.ID] = struct{}{}
			items = append(items, item)
		}
	}
	if len(items) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return items, nil
}

func (s *Supplier) detail(ctx context.Context, id int64) (*detailResponse, error) {
	var detail detailResponse
	endpoint := s.searchEndpoint(url.Values{"id": {strconv.FormatInt(id, 10)}, "show": {"torrent"}})
	if err := s.getJSON(ctx, endpoint, &detail); err != nil {
		return nil, err
	}
	if detail.ID == 0 {
		detail.ID = id
	}
	return &detail, nil
}

func (s *Supplier) searchEndpoint(values url.Values) string {
	root := strings.TrimRight(s.baseURL, "/")
	searchPath := common.AnimeToshoSearchURL
	if cfg := settings.Get().AdvancedSettings.SuppliersSettings.AnimeTosho; cfg != nil {
		if strings.TrimSpace(cfg.RootUrl) != "" && s.baseURL == common.AnimeToshoRootURLDef {
			root = strings.TrimRight(cfg.RootUrl, "/")
		}
		if strings.TrimSpace(cfg.SearchUrl) != "" {
			searchPath = cfg.SearchUrl
		}
	}
	return root + "/" + strings.TrimLeft(searchPath, "/") + "?" + values.Encode()
}

func (s *Supplier) getJSON(ctx context.Context, endpoint string, out interface{}) error {
	response, err := s.doRequest(ctx, endpoint, requestTimeout, "application/json", "AnimeTosho")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("AnimeTosho returned HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxJSONSize+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) == 0 {
		return errors.New("read AnimeTosho response failed")
	}
	if len(data) > maxJSONSize {
		return errors.New("AnimeTosho response exceeds 4 MiB safety limit")
	}
	if err = json.Unmarshal(data, out); err != nil {
		return errors.New("AnimeTosho returned invalid JSON")
	}
	return nil
}

func (s *Supplier) attachmentURL(detail *detailResponse, item feedItem, attachment attachmentInfo) (string, error) {
	root, err := url.Parse(strings.TrimRight(s.attachmentBaseURL, "/"))
	if err != nil || root.Scheme != "https" || root.Hostname() == "" || root.User != nil {
		return "", errors.New("invalid AnimeTosho attachment endpoint")
	}
	videoName := ""
	if strings.TrimSpace(attachment.SourceFilename) != "" {
		videoName = path.Base(attachment.SourceFilename)
	}
	for _, file := range detail.Files {
		if videoName != "" {
			break
		}
		candidate := firstNonEmpty(file.Filename, file.Name, file.Path)
		if isVideoFile(candidate) {
			videoName = path.Base(candidate)
			break
		}
	}
	if videoName == "" {
		videoName = path.Base(firstNonEmpty(detail.TorrentName, item.TorrentName, item.Title))
	}
	videoName = strings.TrimSuffix(videoName, filepath.Ext(videoName))
	if videoName == "" || attachment.ID <= 0 || attachment.Info.TrackNum <= 0 {
		return "", errors.New("incomplete AnimeTosho attachment metadata")
	}
	codec := strings.ToLower(strings.TrimSpace(attachment.Info.Codec))
	langCode := safeLanguageCode(attachment.Info.Lang)
	if !supportedCodec(codec) || langCode == "" {
		return "", errors.New("unsupported AnimeTosho subtitle metadata")
	}
	fileName := fmt.Sprintf("%s_track%d.%s.%s.xz", videoName, attachment.Info.TrackNum, langCode, codec)
	root.Path = path.Join(root.Path, "storage", "attach", fmt.Sprintf("%08x", attachment.ID), fileName)
	return root.String(), nil
}

func (s *Supplier) downloadAttachment(ctx context.Context, endpoint string, attachment attachmentInfo) ([]byte, string, error) {
	response, err := s.doRequest(ctx, endpoint, downloadTimeout, "application/octet-stream", "AnimeTosho attachment")
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("AnimeTosho attachment returned HTTP %d", response.StatusCode)
	}
	compressed, err := io.ReadAll(io.LimitReader(response.Body, maxCompressedSize+1))
	if err != nil || len(compressed) == 0 {
		return nil, "", errors.New("read AnimeTosho attachment failed")
	}
	if len(compressed) > maxCompressedSize {
		return nil, "", errors.New("AnimeTosho attachment exceeds 5 MiB compressed limit")
	}
	data, err := decompressAttachment(compressed)
	if err != nil {
		return nil, "", err
	}
	if !looksLikeSubtitle(data, attachment.Info.Codec) {
		return nil, "", errors.New("AnimeTosho attachment is not a supported subtitle document")
	}
	return data, fmt.Sprintf("animetosho-%d.%s", attachment.ID, strings.ToLower(attachment.Info.Codec)), nil
}

func decompressAttachment(compressed []byte) ([]byte, error) {
	reader, err := xz.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, errors.New("AnimeTosho attachment is not valid XZ")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxDecompressedSize+1))
	if err != nil || len(data) == 0 {
		return nil, errors.New("decompress AnimeTosho attachment failed")
	}
	if len(data) > maxDecompressedSize {
		return nil, errors.New("AnimeTosho attachment exceeds 5 MiB decompressed limit")
	}
	if looksLikeHTML(data) {
		return nil, errors.New("AnimeTosho attachment returned HTML")
	}
	return data, nil
}

func (s *Supplier) httpClient(timeout time.Duration, endpoint string) (*http.Client, error) {
	if s.httpClientFactory != nil {
		return s.httpClientFactory(timeout, endpoint)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("AnimeTosho endpoint must use HTTPS")
	}
	allowedAuthority := parsed.Host
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if proxyAddress := local_http_proxy_server.GetProxyUrl(); proxyAddress != "" {
		proxyURL, parseErr := url.Parse(proxyAddress)
		if parseErr != nil {
			return nil, errors.New("invalid configured HTTP proxy")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if !animeToshoRedirectAllowed(parsed, request.URL, allowedAuthority) {
				return errors.New("AnimeTosho redirect target is not allowed")
			}
			return nil
		},
	}, nil
}

func (s *Supplier) doRequest(ctx context.Context, endpoint string, timeout time.Duration, accept, operation string) (*http.Response, error) {
	client, err := s.httpClient(timeout, endpoint)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < maxRequestAttempts; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if requestErr != nil {
			return nil, fmt.Errorf("create %s request failed", operation)
		}
		request.Header.Set("Accept", accept)
		request.Header.Set("User-Agent", providerUserAgent)
		if requestErr = s.waitRateLimit(ctx); requestErr != nil {
			return nil, requestErr
		}
		response, requestErr := client.Do(request)
		if requestErr == nil && (!retryableHTTPStatus(response.StatusCode) || attempt == maxRequestAttempts-1) {
			return response, nil
		}
		if response != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
			_ = response.Body.Close()
		}
		if requestErr != nil {
			lastErr = fmt.Errorf("%s request failed", operation)
		} else {
			lastErr = fmt.Errorf("%s returned HTTP %d", operation, response.StatusCode)
		}
		if attempt < maxRequestAttempts-1 {
			if err = waitRetry(ctx, retryBaseDelay*time.Duration(1<<attempt)); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func retryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func animeToshoRedirectAllowed(origin, target *url.URL, allowedAuthority string) bool {
	if target == nil || target.Scheme != "https" || target.User != nil || target.Hostname() == "" {
		return false
	}
	if strings.EqualFold(target.Host, allowedAuthority) {
		return true
	}
	return origin != nil && strings.EqualFold(origin.Hostname(), "animetosho.org") &&
		(origin.Port() == "" || origin.Port() == "443") &&
		strings.EqualFold(target.Hostname(), "storage.animetosho.org") &&
		(target.Port() == "" || target.Port() == "443")
}

func (s *Supplier) waitRateLimit(ctx context.Context) error {
	wait := time.Until(s.lastRequestAt.Add(requestInterval))
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

type feedItem struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	TorrentName string `json:"torrent_name"`
}

type detailResponse struct {
	ID          int64            `json:"id"`
	Title       string           `json:"title"`
	TorrentName string           `json:"torrent_name"`
	Files       []detailFile     `json:"files"`
	Attachments []attachmentInfo `json:"attachments"`
}

type detailFile struct {
	Filename    string           `json:"filename"`
	Name        string           `json:"name"`
	Path        string           `json:"path"`
	Attachments []attachmentInfo `json:"attachments"`
}

type attachmentInfo struct {
	ID             int64  `json:"id"`
	Type           string `json:"type"`
	Size           int64  `json:"size"`
	SourceFilename string `json:"-"`
	Info           struct {
		Codec    string `json:"codec"`
		Lang     string `json:"lang"`
		Name     string `json:"name"`
		TrackNum int    `json:"tracknum"`
	} `json:"info"`
}

func matchingFeedItems(items []feedItem, titles []string, episode series.EpisodeInfo) []feedItem {
	out := make([]feedItem, 0)
	for _, item := range items {
		titleMatches := matchesAnyTitle(item.TorrentName, titles) || matchesAnyTitle(item.Title, titles)
		episodeMatches := matchesEpisode(item.TorrentName, episode) || matchesEpisode(item.Title, episode)
		if !titleMatches || !episodeMatches {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func matchesEpisode(name string, episode series.EpisodeInfo) bool {
	_, season, number, err := decode.GetSeasonAndEpisodeFromSubFileName(name)
	if err == nil {
		if season == episode.Season && number == episode.Episode {
			return true
		}
		if episode.SceneSeason > 0 && season == episode.SceneSeason && number == episode.SceneEpisode {
			return true
		}
	}
	if episode.AbsoluteEpisode > 0 && episode_identity.FilenameContainsAbsoluteEpisode(name, episode.AbsoluteEpisode) {
		return true
	}
	return episode.Season == 1 && episode_identity.FilenameContainsAbsoluteEpisode(name, episode.Episode)
}

func selectChineseAttachments(items []attachmentInfo) []attachmentInfo {
	out := make([]attachmentInfo, 0)
	seen := make(map[int64]struct{})
	for _, item := range items {
		if item.ID <= 0 || !strings.EqualFold(item.Type, "subtitle") || item.Info.TrackNum <= 0 || item.Size > maxDecompressedSize {
			continue
		}
		if !isChineseCode(item.Info.Lang) || !supportedCodec(strings.ToLower(strings.TrimSpace(item.Info.Codec))) {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return attachmentPriority(out[i]) < attachmentPriority(out[j])
	})
	return out
}

func selectEpisodeAttachments(detail *detailResponse, episode series.EpisodeInfo) []attachmentInfo {
	if detail == nil {
		return nil
	}
	items := make([]attachmentInfo, 0, len(detail.Attachments))
	for _, attachment := range detail.Attachments {
		if len(detail.Files) <= 1 || (attachment.SourceFilename != "" && matchesEpisode(attachment.SourceFilename, episode)) {
			items = append(items, attachment)
		}
	}
	for _, file := range detail.Files {
		sourceFilename := firstNonEmpty(file.Filename, file.Name, file.Path)
		if len(detail.Files) > 1 && !matchesEpisode(sourceFilename, episode) {
			continue
		}
		for _, attachment := range file.Attachments {
			attachment.SourceFilename = sourceFilename
			items = append(items, attachment)
		}
	}
	return selectChineseAttachments(items)
}

func attachmentPriority(item attachmentInfo) int {
	name := strings.ToLower(item.Info.Name)
	if strings.Contains(name, "simpl") || strings.Contains(name, "简") {
		return 0
	}
	if strings.Contains(name, "trad") || strings.Contains(name, "繁") {
		return 2
	}
	return 1
}

func attachmentLanguage(item attachmentInfo) language.MyLanguage {
	if attachmentPriority(item) == 2 || strings.EqualFold(item.Info.Lang, "zh-tw") {
		return language.ChineseTraditional
	}
	return language.ChineseSimple
}

func sortedEpisodes(values map[string]series.EpisodeInfo) []series.EpisodeInfo {
	out := make([]series.EpisodeInfo, 0, len(values))
	for _, episode := range values {
		out = append(out, episode)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Episode < out[j].Episode
	})
	return out
}

func uniqueTitles(values []string, limit int) []string {
	out := make([]string, 0, limit)
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		normalized := normalize(value)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, value)
		if len(out) == limit {
			break
		}
	}
	return out
}

func matchesAnyTitle(value string, titles []string) bool {
	normalized := normalize(value)
	for _, title := range titles {
		wanted := normalize(title)
		if len([]rune(wanted)) >= 3 && strings.Contains(normalized, wanted) {
			return true
		}
	}
	return false
}

func normalize(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func isChineseCode(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "chi" || value == "zho" || value == "zh" || strings.HasPrefix(value, "zh-")
}

func safeLanguageCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, r := range value {
		if (r < 'a' || r > 'z') && r != '-' {
			return ""
		}
	}
	if !isChineseCode(value) {
		return ""
	}
	return value
}

func supportedCodec(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ass", "ssa", "srt", "vtt":
		return true
	default:
		return false
	}
}

func isVideoFile(value string) bool {
	switch strings.ToLower(filepath.Ext(value)) {
	case ".mkv", ".mp4", ".avi", ".webm", ".mov", ".m2ts":
		return true
	default:
		return false
	}
}

func looksLikeHTML(data []byte) bool {
	prefix := strings.ToLower(strings.TrimSpace(string(data[:minInt(len(data), 256)])))
	return strings.HasPrefix(prefix, "<!doctype html") || strings.HasPrefix(prefix, "<html")
}

func looksLikeSubtitle(data []byte, codec string) bool {
	limit := minInt(len(data), 8192)
	prefix := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(data[:limit]), "\ufeff")))
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "ass", "ssa":
		return strings.Contains(prefix, "[script info]") || strings.Contains(prefix, "[events]")
	case "srt":
		return strings.Contains(prefix, "-->")
	case "vtt":
		return strings.HasPrefix(prefix, "webvtt") || strings.Contains(prefix, "-->")
	default:
		return false
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
