package assrt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"

	common2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/file_downloader"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/mix_media_info"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/notify_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/sirupsen/logrus"
)

const (
	assrtDownloadTimeout = 6 * time.Minute
	assrtSearchTimeout   = 15 * time.Second
	assrtDetailTimeout   = 20 * time.Second
	assrtQuotaTimeout    = 10 * time.Second
	assrtMaxDownloadSize = 3 * 1024 * 1024
)

type Supplier struct {
	log               *logrus.Logger
	fileDownloader    *file_downloader.FileDownloader
	isAlive           bool
	theSearchInterval time.Duration
	requestLock       sync.Mutex
	lastRequestAt     time.Time
}

func NewSupplier(fileDownloader *file_downloader.FileDownloader) *Supplier {

	sup := Supplier{}
	sup.log = fileDownloader.Log
	sup.fileDownloader = fileDownloader
	sup.isAlive = true // 默认是可以使用的，如果 check 后，再调整状态

	// ASSRT documents a five-requests-per-minute quota. Pace request starts at
	// that boundary instead of sleeping after every response.
	sup.theSearchInterval = 12 * time.Second

	return &sup
}

func (s *Supplier) CheckAlive() (bool, int64) {

	// 如果没有设置这个 API 接口，那么就任务是不可用的
	if settings.Get().SubtitleSources.AssrtSettings.Token == "" {
		s.isAlive = false
		return false, 0
	}

	// 计算当前时间
	startT := time.Now()
	userInfo, err := s.getUserInfo()
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), "CheckAlive", "Error", err)
		s.isAlive = false
		return false, 0
	}
	s.log.Infoln(s.GetSupplierName(), "CheckAlive", "UserInfo.Status:", userInfo.Status, "UserInfo.Quota:", userInfo.User.Quota)
	// 计算耗时
	s.isAlive = true
	return true, time.Since(startT).Milliseconds()
}

func (s *Supplier) IsAlive() bool {
	return s.isAlive
}

func (s *Supplier) OverDailyDownloadLimit() bool {

	if settings.Get().AdvancedSettings.SuppliersSettings.Assrt.DailyDownloadLimit == 0 {
		s.log.Warningln(s.GetSupplierName(), "DailyDownloadLimit is 0, will Skip Download")
		return true
	}

	// 对于这个接口暂时没有限制
	return false
}

func (s *Supplier) GetLogger() *logrus.Logger {
	return s.log
}

func (s *Supplier) GetSupplierName() string {
	return common2.SubSiteAssrt
}

func (s *Supplier) GetSubListFromFile4Movie(filePath string) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4MovieContext(context.Background(), filePath)
}

func (s *Supplier) GetSubListFromFile4MovieContext(ctx context.Context, filePath string) ([]supplier.SubInfo, error) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()

	outSubInfos := make([]supplier.SubInfo, 0)
	if settings.Get().SubtitleSources.AssrtSettings.Enabled == false {
		return outSubInfos, nil
	}

	if settings.Get().SubtitleSources.AssrtSettings.Token == "" {
		return nil, errors.New("Token is empty")
	}

	return s.getSubListFromFileContext(ctx, filePath, true)
}

func (s *Supplier) GetSubListFromFile4Series(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4SeriesContext(context.Background(), seriesInfo)
}

func (s *Supplier) GetSubListFromFile4SeriesContext(ctx context.Context, seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()

	outSubInfos := make([]supplier.SubInfo, 0)
	if settings.Get().SubtitleSources.AssrtSettings.Enabled == false {
		return outSubInfos, nil
	}

	if settings.Get().SubtitleSources.AssrtSettings.Token == "" {
		return nil, errors.New("Token is empty")
	}

	return s.downloadSub4SeriesContext(ctx, seriesInfo)
}

func (s *Supplier) GetSubListFromFile4Anime(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.GetSubListFromFile4AnimeContext(context.Background(), seriesInfo)
}

func (s *Supplier) GetSubListFromFile4AnimeContext(ctx context.Context, seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.requestLock.Lock()
	defer s.requestLock.Unlock()

	outSubInfos := make([]supplier.SubInfo, 0)
	if settings.Get().SubtitleSources.AssrtSettings.Enabled == false {
		return outSubInfos, nil
	}

	if settings.Get().SubtitleSources.AssrtSettings.Token == "" {
		return nil, errors.New("Token is empty")
	}

	return s.downloadSub4SeriesContext(ctx, seriesInfo)
}

func (s *Supplier) getSubListFromFile(videoFPath string, isMovie bool, episodeMetadata ...series.EpisodeInfo) ([]supplier.SubInfo, error) {
	return s.getSubListFromFileContext(context.Background(), videoFPath, isMovie, episodeMetadata...)
}

func (s *Supplier) getSubListFromFileContext(ctx context.Context, videoFPath string, isMovie bool, episodeMetadata ...series.EpisodeInfo) ([]supplier.SubInfo, error) {

	defer func() {
		s.log.Debugln(s.GetSupplierName(), videoFPath, "End...")
	}()

	s.log.Debugln(s.GetSupplierName(), videoFPath, "Start...")

	outSubInfoList := make([]supplier.SubInfo, 0)
	mediaInfo, err := mix_media_info.GetMixMediaInfo(s.fileDownloader.MediaInfoDealers, videoFPath, isMovie)
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), videoFPath, "GetMixMediaInfo", err)
		return nil, err
	}
	var searchSubResult *SearchSubResult
	found := false
	targetEpisodes := make([]int, 0, 2)
	targetSeason := 0
	targetEpisode := 0
	absoluteEpisode := 0
	if !isMovie {
		if len(episodeMetadata) > 0 {
			targetSeason = episodeMetadata[0].Season
			targetEpisode = episodeMetadata[0].Episode
			absoluteEpisode = episodeMetadata[0].AbsoluteEpisode
		} else if parsed, parseErr := decode.GetVideoInfoFromFileName(filepath.Base(videoFPath)); parseErr == nil {
			targetSeason = parsed.Season
			targetEpisode = parsed.Episode
		}
		targetEpisodes = append(targetEpisodes, targetEpisode)
		if absoluteEpisode > 0 && absoluteEpisode != targetEpisode {
			targetEpisodes = append(targetEpisodes, absoluteEpisode)
		}
		for _, query := range assrtProviderSearchPlan(mediaInfo, targetSeason, targetEpisode, absoluteEpisode) {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			searchSubResult, err = s.getSubByKeyWordContext(ctx, query.Query)
			if err != nil {
				return nil, err
			}
			if assrtSearchResultHasMatchingCandidate(mediaInfo, targetEpisodes, searchSubResult) {
				found = true
				break
			}
		}
	} else {
		// Search aliases from most locally useful to most portable. Candidate
		// identity validation below makes these fallbacks safe.
		for _, keyWordType := range assrtSearchKeywordTypes(mediaInfo) {
			found, searchSubResult, err = s.getSubInfoExContext(ctx, mediaInfo, videoFPath, true, keyWordType)
			if err != nil {
				s.log.Errorln(s.GetSupplierName(), videoFPath, "GetSubInfoEx", keyWordType, err)
				return nil, err
			}
			if found {
				break
			}
		}
	}
	if !found {
		return nil, nil
	}

	videoFileName := filepath.Base(videoFPath)
	for index, subInfo := range searchSubResult.Sub.Subs {
		if err = ctx.Err(); err != nil {
			return outSubInfoList, err
		}
		candidateMatches, rejectedField := assrtCandidateFieldsMatchMediaForEpisodes(mediaInfo, targetEpisodes, subInfo.NativeName, subInfo.Videoname)
		if !candidateMatches {
			s.log.Warningf("assrt skip title mismatch: id=%d target_cn=%q target_en=%q candidate=%q",
				subInfo.Id, mediaInfo.TitleCn, mediaInfo.TitleEn, rejectedField)
			continue
		}

		// 获取具体的下载地址
		oneSubDetail, err := s.getSubDetailContext(ctx, subInfo.Id)
		if err != nil {
			if ctx.Err() != nil {
				return outSubInfoList, ctx.Err()
			}
			s.log.Errorln("getSubDetail", err)
			continue
		}

		if len(oneSubDetail.Sub.Subs) < 1 {
			continue
		}
		downloadDetail := oneSubDetail.Sub.Subs[0]
		detailMatches, rejectedField := assrtCandidateFieldsMatchMediaForEpisodes(mediaInfo, targetEpisodes, downloadDetail.NativeName,
			downloadDetail.Videoname, downloadDetail.Title, downloadDetail.Filename)
		if !detailMatches {
			s.log.Warningf("assrt skip detail title mismatch: id=%d target_cn=%q target_en=%q candidate=%q",
				subInfo.Id, mediaInfo.TitleCn, mediaInfo.TitleEn, rejectedField)
			continue
		}
		if downloadDetail.Size > assrtMaxDownloadSize {
			s.log.Warningf("assrt skip oversized subtitle bundle: id=%d size=%d limit=%d", subInfo.Id, downloadDetail.Size, assrtMaxDownloadSize)
			continue
		}
		// 这里需要注意的是 ASSRT 说明了，下载的地址是有时效性的，那么如果缓存整个地址则不是正确的
		// 需要缓存的应该是这个字幕的 ID
		nowSubDownloadUrl := downloadDetail.Url
		subInfo, err := s.fileDownloader.GetWithDownloadTimeout(s.GetSupplierName(), int64(index), videoFileName, nowSubDownloadUrl,
			0, 0,
			assrtDownloadTimeout,
			// 得到一个特殊的替代 FileDownloadUrl 的特征字符串
			fmt.Sprintf("%s-%s-%d", s.GetSupplierName(), subInfo.NativeName, subInfo.Id),
		)
		if err != nil {
			s.log.Error("FileDownloader.Get", err)
			continue
		}

		outSubInfoList = append(outSubInfoList, *subInfo)
		outSubInfoList[len(outSubInfoList)-1].AbsoluteEpisode = absoluteEpisode
		// 如果够了那么多个字幕就返回
		if len(outSubInfoList) >= settings.Get().AdvancedSettings.Topic {
			return outSubInfoList, nil
		}
	}

	return outSubInfoList, nil
}

const assrtMaxQueriesPerEpisode = 6

func assrtProviderSearchPlan(mediaInfo *models.MediaInfo, season, episode, absoluteEpisode int) []episode_identity.QueryVariant {
	aliases := []string{mediaInfo.TitleCn, mediaInfo.TitleEn, mediaInfo.OriginalTitle}
	plan := episode_identity.BuildSearchPlan(aliases, episode_identity.Identity{
		Season: season, Episode: episode, AbsoluteEpisode: absoluteEpisode,
	})
	if absoluteEpisode <= 0 {
		if len(plan) > assrtMaxQueriesPerEpisode {
			return plan[:assrtMaxQueriesPerEpisode]
		}
		return plan
	}

	// ASSRT rate-limits requests. Preserve all precise aired queries, then try
	// bare absolute-number queries (the most common anime-site form), followed
	// by E/EP forms only while the bounded budget remains.
	prioritized := make([]episode_identity.QueryVariant, 0, assrtMaxQueriesPerEpisode)
	bareSuffix := fmt.Sprintf(" %d", absoluteEpisode)
	for _, query := range plan {
		if query.Kind != episode_identity.QueryAbsolute {
			prioritized = append(prioritized, query)
		}
	}
	for _, query := range plan {
		if query.Kind == episode_identity.QueryAbsolute && strings.HasSuffix(query.Query, bareSuffix) {
			prioritized = append(prioritized, query)
		}
	}
	for _, query := range plan {
		if query.Kind == episode_identity.QueryAbsolute && !strings.HasSuffix(query.Query, bareSuffix) {
			prioritized = append(prioritized, query)
		}
	}
	if len(prioritized) > assrtMaxQueriesPerEpisode {
		prioritized = prioritized[:assrtMaxQueriesPerEpisode]
	}
	return prioritized
}

func assrtSearchResultHasMatchingCandidate(mediaInfo *models.MediaInfo, targetEpisodes []int, result *SearchSubResult) bool {
	if result == nil {
		return false
	}
	for _, candidate := range result.Sub.Subs {
		if matched, _ := assrtCandidateFieldsMatchMediaForEpisodes(mediaInfo, targetEpisodes, candidate.NativeName, candidate.Videoname); matched {
			return true
		}
	}
	return false
}

func assrtSearchKeywordTypes(mediaInfo *models.MediaInfo) []string {
	types := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	for _, candidate := range []struct {
		kind  string
		title string
	}{
		{kind: "cn", title: mediaInfo.TitleCn},
		{kind: "en", title: mediaInfo.TitleEn},
		{kind: "org", title: mediaInfo.OriginalTitle},
	} {
		normalized := strings.ToLower(strings.TrimSpace(candidate.title))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		types = append(types, candidate.kind)
	}
	return types
}

func (s *Supplier) getSubInfoEx(mediaInfo *models.MediaInfo, videoFPath string, isMovie bool, keyWordType string) (bool, *SearchSubResult, error) {
	return s.getSubInfoExContext(context.Background(), mediaInfo, videoFPath, isMovie, keyWordType)
}

func (s *Supplier) getSubInfoExContext(ctx context.Context, mediaInfo *models.MediaInfo, videoFPath string, isMovie bool, keyWordType string) (bool, *SearchSubResult, error) {

	var searchSubResult *SearchSubResult
	var err error
	keyWord, err := mix_media_info.KeyWordSelect(mediaInfo, videoFPath, isMovie, keyWordType)
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), videoFPath, "keyWordSelect", err)
		return false, searchSubResult, err
	}
	searchSubResult, err = s.getSubByKeyWordContext(ctx, keyWord)
	if err != nil {
		s.log.Errorln("getSubByKeyWord", err)
		return false, searchSubResult, err
	}

	videoFileName := filepath.Base(videoFPath)
	if searchSubResult.Sub.Subs == nil || len(searchSubResult.Sub.Subs) == 0 {
		s.log.Infoln(s.GetSupplierName(), videoFileName, "No subtitle found", "KeyWord:", keyWord)
		return false, searchSubResult, nil
	} else {
		return true, searchSubResult, nil
	}
}

func (s *Supplier) downloadSub4Series(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	return s.downloadSub4SeriesContext(context.Background(), seriesInfo)
}

func (s *Supplier) downloadSub4SeriesContext(ctx context.Context, seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	var allSupplierSubInfo = make([]supplier.SubInfo, 0)

	index := 0
	// 这里拿到的 seriesInfo ，里面包含了，需要下载字幕的 Eps 信息
	for _, episodeInfo := range seriesInfo.NeedDlEpsKeyList {
		if err := ctx.Err(); err != nil {
			return allSupplierSubInfo, err
		}

		index++
		one, err := s.getSubListFromFileContext(ctx, episodeInfo.FileFullPath, false, episodeInfo)
		if err != nil {
			if ctx.Err() != nil {
				return allSupplierSubInfo, ctx.Err()
			}
			s.log.Errorln(s.GetSupplierName(), "getSubListFromFile", episodeInfo.FileFullPath, err)
			continue
		}
		if one == nil {
			// 没有搜索到字幕
			s.log.Infoln(s.GetSupplierName(), "Not Find Sub can be download",
				episodeInfo.Title, episodeInfo.Season, episodeInfo.Episode)
			continue
		}
		// 需要赋值给字幕结构
		for i := range one {
			one[i].Season = episodeInfo.Season
			one[i].Episode = episodeInfo.Episode
		}
		allSupplierSubInfo = append(allSupplierSubInfo, one...)
	}
	// 返回前，需要把每一个 Eps 的 Season Episode 信息填充到每个 SubInfo 中
	return allSupplierSubInfo, nil
}

func (s *Supplier) getSubByKeyWord(keyword string) (*SearchSubResult, error) {
	return s.getSubByKeyWordContext(context.Background(), keyword)
}

func (s *Supplier) getSubByKeyWordContext(ctx context.Context, keyword string) (*SearchSubResult, error) {
	var searchSubResult SearchSubResult

	s.log.Infoln("Search KeyWord:", keyword)
	tt := url.QueryEscape(keyword)
	httpClient, err := pkg.NewHttpClient()
	if err != nil {
		return nil, err
	}
	httpClient.SetTimeout(assrtSearchTimeout)
	httpClient.SetRetryCount(0)
	if err = s.waitRateLimit(ctx); err != nil {
		return nil, err
	}
	resp, err := httpClient.R().
		SetContext(ctx).
		Get(settings.Get().AdvancedSettings.SuppliersSettings.Assrt.RootUrl +
			"/sub/search?q=" + tt +
			"&cnt=15&pos=0" +
			"&token=" + settings.Get().SubtitleSources.AssrtSettings.Token)
	if err != nil {
		return nil, err
	}
	/*
		这里有个梗， Sub 有值的时候是一个列表，但是如果为空的时候，又是一个空的结构体
		所以出现两个结构体需要去尝试解析
		SearchSubResultEmpty
		SearchSubResult
		比如这个情况：
		jsonString := "{\"sub\":{\"action\":\"search\",\"subs\":{},\"result\":\"succeed\",\"keyword\":\"追杀夏娃 S04E07\"},\"status\":0}"
	*/
	err = json.Unmarshal([]byte(resp.String()), &searchSubResult)
	if err != nil {
		listResultErr := err
		// 再此尝试解析空列表
		var searchSubResultEmpty SearchSubResultEmpty
		err = json.Unmarshal([]byte(resp.String()), &searchSubResultEmpty)
		if err != nil {
			// ASSRT 的无结果响应在不同时间可能既不是列表也不是旧版空对象。
			// 保留两个解析错误，不能解引用从未赋值的 error。
			decodeErr := fmt.Errorf("decode search response: list result: %v; empty result: %w", listResultErr, err)
			s.log.Warningln(s.GetSupplierName(), keyword, decodeErr)
			notify_center.Notify.Add(s.GetSupplierName()+" search response", fmt.Sprintf("keyword: %s, error: %s", keyword, decodeErr))
			return &searchSubResult, nil
		}
		// 赋值过去
		searchSubResult.Sub.Action = searchSubResultEmpty.Sub.Action
		searchSubResult.Sub.Result = searchSubResultEmpty.Sub.Result
		searchSubResult.Sub.Keyword = searchSubResultEmpty.Sub.Keyword
		searchSubResult.Status = searchSubResultEmpty.Status

		return &searchSubResult, nil
	}

	return &searchSubResult, nil
}

func (s *Supplier) getSubDetail(subID int) (OneSubDetail, error) {
	return s.getSubDetailContext(context.Background(), subID)
}

func (s *Supplier) getSubDetailContext(ctx context.Context, subID int) (OneSubDetail, error) {
	var subDetail OneSubDetail

	httpClient, err := pkg.NewHttpClient()
	if err != nil {
		return subDetail, err
	}
	httpClient.SetTimeout(assrtDetailTimeout)
	httpClient.SetRetryCount(0)
	if err = s.waitRateLimit(ctx); err != nil {
		return subDetail, err
	}
	resp, err := httpClient.R().
		SetContext(ctx).
		SetQueryParams(map[string]string{
			"token": settings.Get().SubtitleSources.AssrtSettings.Token,
			"id":    strconv.Itoa(subID),
		}).
		SetResult(&subDetail).
		Get(settings.Get().AdvancedSettings.SuppliersSettings.Assrt.RootUrl + "/sub/detail")
	if err != nil {
		if resp != nil {
			s.log.Errorln(s.GetSupplierName(), "NewHttpClient:", subID, err.Error())
			notify_center.Notify.Add(s.GetSupplierName()+" NewHttpClient", fmt.Sprintf("subID: %d, resp: %s, error: %s", subID, resp.String(), err.Error()))

			// 输出调试文件
			cacheCenterFolder, err := pkg.GetRootCacheCenterFolder()
			if err != nil {
				s.log.Errorln(s.GetSupplierName(), "GetRootCacheCenterFolder", err)
			}
			desJsonInfo := filepath.Join(cacheCenterFolder, strconv.Itoa(subID)+"--assrt_search_error_getSubDetail.json")
			// 写字符串到文件种
			file, _ := os.Create(desJsonInfo)
			defer func() {
				_ = file.Close()
			}()
			file.WriteString(resp.String())
		}
		return subDetail, err
	}

	return subDetail, nil
}

func (s *Supplier) waitRateLimit(ctx context.Context) error {
	wait := time.Until(s.lastRequestAt.Add(s.theSearchInterval))
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

func (s *Supplier) getUserInfo() (UserInfo, error) {

	var userInfo UserInfo

	httpClient, err := pkg.NewHttpClient()
	if err != nil {
		return userInfo, err
	}
	httpClient.SetTimeout(assrtQuotaTimeout)
	httpClient.SetRetryCount(0)
	resp, err := httpClient.R().
		SetQueryParams(map[string]string{
			"token": settings.Get().SubtitleSources.AssrtSettings.Token,
		}).
		SetResult(&userInfo).
		Get(settings.Get().AdvancedSettings.SuppliersSettings.Assrt.RootUrl + "/user/quota")
	if err != nil {
		if resp != nil {
			s.log.Errorln(s.GetSupplierName(), "NewHttpClient:", err.Error())
			notify_center.Notify.Add(s.GetSupplierName()+" NewHttpClient", fmt.Sprintf("resp: %s, error: %s", resp.String(), err.Error()))
		}
		return userInfo, err
	}

	return userInfo, nil
}

type SearchSubResultEmpty struct {
	Sub struct {
		Action string `json:"action"`
		Subs   struct {
		} `json:"subs"`
		Result  string `json:"result"`
		Keyword string `json:"keyword"`
	} `json:"sub"`
	Status int `json:"status"`
}

type SearchSubResult struct {
	Sub struct {
		Action string `json:"action"`
		Subs   []struct {
			Lang struct {
				Desc     string `json:"desc,omitempty"`
				Langlist struct {
					Langcht bool `json:"langcht,omitempty"`
					Langdou bool `json:"langdou,omitempty"`
					Langeng bool `json:"langeng,omitempty"`
					Langchs bool `json:"langchs,omitempty"`
				} `json:"langlist,omitempty"`
			} `json:"lang,omitempty"`
			Id          int             `json:"id,omitempty"`
			VoteScore   int             `json:"vote_score,omitempty"`
			Videoname   string          `json:"videoname,omitempty"`
			ReleaseSite string          `json:"release_site,omitempty"`
			Revision    json.RawMessage `json:"revision,omitempty"`
			Subtype     string          `json:"subtype,omitempty"`
			NativeName  string          `json:"native_name,omitempty"`
			UploadTime  string          `json:"upload_time,omitempty"`
		} `json:"subs,omitempty"`
		Result  string `json:"result,omitempty"`
		Keyword string `json:"keyword,omitempty"`
	} `json:"sub,omitempty"`
	Status int `json:"status,omitempty"`
}

type OneSubDetail struct {
	Sub struct {
		Action string `json:"action"`
		Subs   []struct {
			DownCount int `json:"down_count,omitempty"`
			ViewCount int `json:"view_count,omitempty"`
			Lang      struct {
				Desc     string `json:"desc,omitempty"`
				Langlist struct {
					Langcht bool `json:"langcht,omitempty"`
					Langdou bool `json:"langdou,omitempty"`
					Langeng bool `json:"langeng,omitempty"`
					Langchs bool `json:"langchs,omitempty"`
				} `json:"langlist,omitempty"`
			} `json:"lang,omitempty"`
			Size       int             `json:"size,omitempty"`
			Title      string          `json:"title,omitempty"`
			Videoname  string          `json:"videoname,omitempty"`
			Revision   json.RawMessage `json:"revision,omitempty"`
			NativeName string          `json:"native_name,omitempty"`
			UploadTime string          `json:"upload_time,omitempty"`
			Producer   struct {
				Producer string `json:"producer,omitempty"`
				Verifier string `json:"verifier,omitempty"`
				Uploader string `json:"uploader,omitempty"`
				Source   string `json:"source,omitempty"`
			} `json:"producer,omitempty"`
			Subtype     string `json:"subtype,omitempty"`
			VoteScore   int    `json:"vote_score,omitempty"`
			ReleaseSite string `json:"release_site,omitempty"`
			//Filelist    []struct {
			//	S   string `json:"s,omitempty"`
			//	F   string `json:"f,omitempty"`
			//	Url string `json:"url,omitempty"`
			//} `json:"filelist,omitempty"`
			Id       int    `json:"id,omitempty"`
			Filename string `json:"filename,omitempty"`
			Url      string `json:"url,omitempty"`
		} `json:"subs,omitempty"`
		Result string `json:"result,omitempty"`
	} `json:"sub,omitempty"`
	Status int `json:"status,omitempty"`
}

type UserInfo struct {
	User struct {
		Action string `json:"action,omitempty"`
		Result string `json:"result,omitempty"`
		Quota  int    `json:"quota,omitempty"`
	} `json:"user,omitempty"`
	Status int `json:"status,omitempty"`
}
