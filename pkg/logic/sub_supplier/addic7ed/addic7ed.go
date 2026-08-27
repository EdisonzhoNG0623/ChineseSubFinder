package addic7ed

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/local_http_proxy_server"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/file_downloader"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/language"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/PuerkitoBio/goquery"
	"github.com/sirupsen/logrus"
)

const (
	requestTimeout    = 30 * time.Second
	downloadTimeout   = 2 * time.Minute
	requestInterval   = 1500 * time.Millisecond
	showCacheDuration = 24 * time.Hour
	maxHTMLSize       = 8 << 20
	maxDownloadSize   = 5 << 20
	providerUserAgent = "ChineseSubFinder-Addic7ed/1.0"
)

type Supplier struct {
	log            *logrus.Logger
	fileDownloader *file_downloader.FileDownloader
	requestLock    sync.Mutex
	isAlive        bool
	baseURL        string
	lastRequestAt  time.Time
	showCache      map[string]int
	showCacheAt    time.Time
	jar            http.CookieJar
}

func NewSupplier(downloader *file_downloader.FileDownloader) *Supplier {
	jar, _ := cookiejar.New(nil)
	return &Supplier{
		log:            downloader.Log,
		fileDownloader: downloader,
		isAlive:        true,
		baseURL:        common.Addic7edRootURLDef,
		showCache:      make(map[string]int),
		jar:            jar,
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
	showID, err := s.resolveShowID(context.Background(), []string{"The Queen's Gambit"})
	if err == nil && showID <= 0 {
		err = errors.New("Addic7ed health-check title was not resolved")
	}
	s.isAlive = err == nil
	if err != nil {
		s.log.Warningln(s.GetSupplierName(), "Check Alive failed:", err)
		return false, 0
	}
	return true, time.Since(started).Milliseconds()
}

func (s *Supplier) IsAlive() bool             { return s.isAlive }
func (s *Supplier) GetSupplierName() string   { return common.SubSiteAddic7ed }
func (s *Supplier) GetLogger() *logrus.Logger { return s.log }

func (s *Supplier) OverDailyDownloadLimit() bool {
	one := settings.Get().AdvancedSettings.SuppliersSettings.Addic7ed
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

func (s *Supplier) GetSubListFromFile4Anime(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return []supplier.SubInfo{}, nil
}

func (s *Supplier) GetSubListFromFile4Series(info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4SeriesContext(context.Background(), info)
}

func (s *Supplier) GetSubListFromFile4SeriesContext(ctx context.Context, info *series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()
	if err := s.configured(); err != nil {
		return nil, err
	}
	if info == nil || info.IsAnime || len(info.NeedDlEpsKeyList) == 0 {
		return []supplier.SubInfo{}, nil
	}
	showID, err := s.resolveShowID(ctx, showTitles(info))
	if err != nil {
		return nil, err
	}
	if showID <= 0 {
		return []supplier.SubInfo{}, nil
	}
	bySeason := groupEpisodes(info.NeedDlEpsKeyList)
	seasons := make([]int, 0, len(bySeason))
	for seasonNumber := range bySeason {
		seasons = append(seasons, seasonNumber)
	}
	sort.Ints(seasons)
	limit := settings.Get().AdvancedSettings.Topic
	if limit <= 0 {
		return []supplier.SubInfo{}, nil
	}
	out := make([]supplier.SubInfo, 0)
	for _, seasonNumber := range seasons {
		candidates, queryErr := s.querySeason(ctx, showID, seasonNumber)
		if queryErr != nil {
			s.log.Warningln(s.GetSupplierName(), "season request failed:", queryErr)
			continue
		}
		for _, episode := range bySeason[seasonNumber] {
			matching := rankCandidates(candidates, episode, filepath.Base(episode.FileFullPath))
			for index, candidate := range matching {
				if index >= limit {
					break
				}
				downloadURL, safeErr := s.safeURL(candidate.DownloadPath)
				if safeErr != nil {
					s.log.Warningln(s.GetSupplierName(), "skip unsafe download URL:", safeErr)
					continue
				}
				refererURL, safeErr := s.safeURL(candidate.RefererPath)
				if safeErr != nil {
					continue
				}
				cacheKey := fmt.Sprintf("addic7ed-%x", sha256.Sum256([]byte(downloadURL)))
				oneSub, downloadErr := s.fileDownloader.GetWithCustomDownloader(
					s.GetSupplierName(), int64(index), filepath.Base(episode.FileFullPath),
					"addic7ed://subtitle/"+cacheKey, 0, 0,
					func(_ *logrus.Logger, _ string) ([]byte, string, error) {
						return s.download(ctx, downloadURL, refererURL, candidate)
					}, cacheKey)
				if downloadErr != nil {
					s.log.Warningln(s.GetSupplierName(), "download failed:", downloadErr)
					continue
				}
				oneSub.Name = candidate.Title
				oneSub.Season = episode.Season
				oneSub.Episode = episode.Episode
				oneSub.AbsoluteEpisode = episode.AbsoluteEpisode
				oneSub.Language = candidate.Language
				out = append(out, *oneSub)
			}
		}
	}
	return out, nil
}

func (s *Supplier) configured() error {
	if !settings.Get().SubtitleSources.Addic7edSettings.Enabled {
		return errors.New("Addic7ed is disabled")
	}
	return nil
}

func (s *Supplier) resolveShowID(ctx context.Context, titles []string) (int, error) {
	if time.Since(s.showCacheAt) >= showCacheDuration {
		s.showCache = make(map[string]int)
		s.showCacheAt = time.Now()
	}
	var firstErr error
	for _, title := range uniqueTitles(titles, 4) {
		cacheKey := normalize(title)
		if id := s.showCache[cacheKey]; id > 0 {
			return id, nil
		}
		searchEndpoint, err := s.safeURL("/search.php")
		if err != nil {
			return 0, err
		}
		parsed, _ := url.Parse(searchEndpoint)
		query := parsed.Query()
		query.Set("search", title)
		query.Set("Submit", "Search")
		parsed.RawQuery = query.Encode()
		searchDocument, err := s.getDocument(ctx, parsed.String(), requestTimeout)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		episodePath := findEpisodePath(searchDocument, title)
		if episodePath == "" {
			continue
		}
		episodeURL, err := s.safeURL(episodePath)
		if err != nil {
			continue
		}
		episodeDocument, err := s.getDocument(ctx, episodeURL, requestTimeout)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		id := parseShowID(episodeDocument, title)
		if id > 0 {
			s.showCache[cacheKey] = id
			return id, nil
		}
	}
	return 0, firstErr
}

func (s *Supplier) querySeason(ctx context.Context, showID, seasonNumber int) ([]candidate, error) {
	searchPath := common.Addic7edSearchURL
	if cfg := settings.Get().AdvancedSettings.SuppliersSettings.Addic7ed; cfg != nil && strings.TrimSpace(cfg.SearchUrl) != "" {
		searchPath = cfg.SearchUrl
	}
	endpoint, err := url.Parse(s.rootURL() + "/" + strings.TrimLeft(strings.TrimSpace(searchPath), "/"))
	if err != nil {
		return nil, errors.New("invalid Addic7ed search endpoint")
	}
	query := endpoint.Query()
	query.Set("show", strconv.Itoa(showID))
	query.Set("season", strconv.Itoa(seasonNumber))
	query.Set("langs", "|41||24||64|")
	endpoint.RawQuery = query.Encode()
	document, err := s.getDocument(ctx, endpoint.String(), requestTimeout)
	if err != nil {
		return nil, err
	}
	return parseCandidates(document), nil
}

func (s *Supplier) getDocument(ctx context.Context, endpoint string, timeout time.Duration) (*goquery.Document, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("create Addic7ed request failed")
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", providerUserAgent)
	if err = s.waitRateLimit(ctx); err != nil {
		return nil, err
	}
	client, err := s.httpClient(timeout, endpoint)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("Addic7ed request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Addic7ed returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxHTMLSize+1))
	if err != nil || len(data) == 0 {
		return nil, errors.New("read Addic7ed response failed")
	}
	if len(data) > maxHTMLSize {
		return nil, errors.New("Addic7ed response exceeds 8 MiB safety limit")
	}
	return goquery.NewDocumentFromReader(strings.NewReader(string(data)))
}

func (s *Supplier) download(ctx context.Context, endpoint, referer string, item candidate) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", errors.New("create Addic7ed download request failed")
	}
	request.Header.Set("Accept", "text/plain,application/octet-stream")
	request.Header.Set("Referer", referer)
	request.Header.Set("User-Agent", providerUserAgent)
	if err = s.waitRateLimit(ctx); err != nil {
		return nil, "", err
	}
	client, err := s.httpClient(downloadTimeout, endpoint)
	if err != nil {
		return nil, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", errors.New("Addic7ed download request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("Addic7ed download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadSize+1))
	if err != nil || len(data) == 0 {
		return nil, "", errors.New("read Addic7ed subtitle failed")
	}
	if len(data) > maxDownloadSize {
		return nil, "", errors.New("Addic7ed subtitle exceeds 5 MiB safety limit")
	}
	if looksLikeHTML(data) {
		return nil, "", errors.New("Addic7ed download returned HTML")
	}
	if !looksLikeSubtitle(data) {
		return nil, "", errors.New("Addic7ed download is not an SRT subtitle")
	}
	name := responseFileName(response.Header.Get("Content-Disposition"), item)
	return data, name, nil
}

func (s *Supplier) httpClient(timeout time.Duration, endpoint string) (*http.Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("Addic7ed endpoint must use HTTPS")
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
		Jar:       s.jar,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" || request.URL.User != nil || !strings.EqualFold(request.URL.Host, allowedAuthority) {
				return errors.New("Addic7ed redirect target is not allowed")
			}
			return nil
		},
	}, nil
}

func (s *Supplier) safeURL(relative string) (string, error) {
	root, err := url.Parse(s.rootURL())
	if err != nil || root.Scheme != "https" || root.Hostname() == "" || root.User != nil {
		return "", errors.New("invalid Addic7ed root URL")
	}
	reference, err := url.Parse(strings.TrimSpace(relative))
	if err != nil {
		return "", errors.New("invalid Addic7ed relative URL")
	}
	resolved := root.ResolveReference(reference)
	if resolved.Scheme != "https" || !strings.EqualFold(resolved.Host, root.Host) || resolved.User != nil {
		return "", errors.New("Addic7ed URL target is not allowed")
	}
	return resolved.String(), nil
}

func (s *Supplier) rootURL() string {
	root := strings.TrimRight(s.baseURL, "/")
	if cfg := settings.Get().AdvancedSettings.SuppliersSettings.Addic7ed; cfg != nil &&
		strings.TrimSpace(cfg.RootUrl) != "" && s.baseURL == common.Addic7edRootURLDef {
		root = strings.TrimRight(cfg.RootUrl, "/")
	}
	return root
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

type candidate struct {
	Season       int
	Episode      int
	Title        string
	LanguageName string
	Language     language.MyLanguage
	Version      string
	DownloadPath string
	RefererPath  string
}

func parseCandidates(document *goquery.Document) []candidate {
	out := make([]candidate, 0)
	document.Find("#season tbody tr.completed, #season tr.completed").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		if len(out) >= 500 {
			return false
		}
		cells := row.Find("td")
		if cells.Length() < 10 {
			return true
		}
		seasonNumber, seasonErr := strconv.Atoi(strings.TrimSpace(cells.Eq(0).Text()))
		episodeNumber, episodeErr := strconv.Atoi(strings.TrimSpace(cells.Eq(1).Text()))
		languageName := strings.TrimSpace(cells.Eq(3).Text())
		lang, ok := parseLanguage(languageName)
		downloadPath, hasDownload := cells.Eq(9).Find("a[href]").First().Attr("href")
		refererPath, hasReferer := cells.Eq(2).Find("a[href]").First().Attr("href")
		if seasonErr != nil || episodeErr != nil || seasonNumber < 0 || episodeNumber <= 0 || !ok || !hasDownload || !hasReferer {
			return true
		}
		out = append(out, candidate{
			Season: seasonNumber, Episode: episodeNumber, Title: strings.TrimSpace(cells.Eq(2).Text()),
			LanguageName: languageName, Language: lang, Version: strings.TrimSpace(cells.Eq(4).Text()),
			DownloadPath: downloadPath, RefererPath: refererPath,
		})
		return true
	})
	return out
}

var showPathPattern = regexp.MustCompile(`^/show/([0-9]+)$`)

func findEpisodePath(document *goquery.Document, title string) string {
	wanted := normalize(title)
	found := ""
	count := 0
	document.Find("a[href]").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		count++
		if count > 500 {
			return false
		}
		href := strings.TrimSpace(selection.AttrOr("href", ""))
		parsed, err := url.Parse(href)
		if err != nil {
			return true
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 5 || !strings.EqualFold(parts[0], "serie") {
			return true
		}
		candidateTitle, err := url.PathUnescape(parts[1])
		if err == nil && normalize(strings.ReplaceAll(candidateTitle, "_", " ")) == wanted {
			found = href
			return false
		}
		return true
	})
	return found
}

func parseShowID(document *goquery.Document, title string) int {
	wanted := normalize(title)
	id := 0
	document.Find("a[href]").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		if normalize(selection.Text()) != wanted {
			return true
		}
		parsed, err := url.Parse(strings.TrimSpace(selection.AttrOr("href", "")))
		if err != nil {
			return true
		}
		match := showPathPattern.FindStringSubmatch(parsed.Path)
		if len(match) != 2 {
			return true
		}
		id, _ = strconv.Atoi(match[1])
		return id <= 0
	})
	return id
}

func showTitles(info *series.SeriesInfo) []string {
	titles := make([]string, 0, 2+len(info.Aliases)*2)
	for _, title := range append([]string{info.Name}, info.Aliases...) {
		titles = append(titles, title)
		if info.Year > 0 {
			titles = append(titles, fmt.Sprintf("%s (%d)", title, info.Year))
		}
	}
	return titles
}

func uniqueTitles(values []string, limit int) []string {
	out := make([]string, 0, limit)
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := normalize(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == limit {
			break
		}
	}
	return out
}

func groupEpisodes(values map[string]series.EpisodeInfo) map[int][]series.EpisodeInfo {
	out := make(map[int][]series.EpisodeInfo)
	for _, episode := range values {
		out[episode.Season] = append(out[episode.Season], episode)
	}
	for seasonNumber := range out {
		sort.Slice(out[seasonNumber], func(i, j int) bool { return out[seasonNumber][i].Episode < out[seasonNumber][j].Episode })
	}
	return out
}

func rankCandidates(items []candidate, episode series.EpisodeInfo, videoName string) []candidate {
	out := make([]candidate, 0)
	for _, item := range items {
		if item.Season == episode.Season && item.Episode == episode.Episode {
			out = append(out, item)
		}
	}
	normalizedVideo := normalize(videoName)
	sort.SliceStable(out, func(i, j int) bool {
		iMatch := normalize(out[i].Version) != "" && strings.Contains(normalizedVideo, normalize(out[i].Version))
		jMatch := normalize(out[j].Version) != "" && strings.Contains(normalizedVideo, normalize(out[j].Version))
		if iMatch != jMatch {
			return iMatch
		}
		if out[i].Language != out[j].Language {
			return out[i].Language == language.ChineseSimple
		}
		return out[i].DownloadPath < out[j].DownloadPath
	})
	return out
}

func parseLanguage(value string) (language.MyLanguage, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(value, "simpl") || strings.Contains(value, "简") {
		return language.ChineseSimple, true
	}
	if strings.Contains(value, "trad") || strings.Contains(value, "繁") || strings.Contains(value, "canton") || strings.Contains(value, "粤") {
		return language.ChineseTraditional, true
	}
	return language.Unknown, false
}

func responseFileName(disposition string, item candidate) string {
	if _, params, err := mime.ParseMediaType(disposition); err == nil {
		if name := filepath.Base(strings.TrimSpace(params["filename"])); name != "" && name != "." && name != "/" {
			return name
		}
	}
	return fmt.Sprintf("addic7ed-s%02de%02d.srt", item.Season, item.Episode)
}

func normalize(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
}

func looksLikeHTML(data []byte) bool {
	limit := len(data)
	if limit > 256 {
		limit = 256
	}
	prefix := strings.ToLower(strings.TrimSpace(string(data[:limit])))
	return strings.HasPrefix(prefix, "<!doctype html") || strings.HasPrefix(prefix, "<html")
}

func looksLikeSubtitle(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	prefix := strings.TrimSpace(strings.TrimPrefix(string(data[:limit]), "\ufeff"))
	return strings.Contains(prefix, "-->")
}
