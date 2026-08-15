package subhd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"math"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/search"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/language"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/file_downloader"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/rod_helper"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/mix_media_info"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/notify_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_parser_hub"

	"github.com/PuerkitoBio/goquery"
	"github.com/Tnze/go.num/v2/zh"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/nfnt/resize"
	"github.com/sirupsen/logrus"
)

type Supplier struct {
	log              *logrus.Logger
	fileDownloader   *file_downloader.FileDownloader
	tt               time.Duration
	operationTimeout time.Duration
	isAlive          bool
}

const (
	subHDOperationTimeout    = 5 * time.Minute
	subHDBrowserCloseTimeout = 5 * time.Second
)

func NewSupplier(fileDownloader *file_downloader.FileDownloader) *Supplier {

	sup := Supplier{}
	sup.log = fileDownloader.Log
	sup.fileDownloader = fileDownloader

	if settings.Get().AdvancedSettings.Topic != common.DownloadSubsPerSite {
		settings.Get().AdvancedSettings.Topic = common.DownloadSubsPerSite
	}
	sup.isAlive = true // 默认是可以使用的，如果 check 后，再调整状态
	sup.operationTimeout = subHDOperationTimeout

	// 默认超时是 2 * 60s，如果是调试模式则是 5 min
	sup.tt = common.BrowserTimeOut
	if settings.Get().AdvancedSettings.DebugMode == true {
		sup.tt = common.OneMovieProcessTimeOut
	}

	return &sup
}

func (s *Supplier) applyOperationTimeout(browser *rod.Browser) *rod.Browser {
	timeout := s.operationTimeout
	if timeout <= 0 {
		timeout = subHDOperationTimeout
	}
	return browser.Timeout(timeout)
}

func (s *Supplier) newBrowserWithTimeout(options *rod_helper.BrowserOptions) (*rod.Browser, func(), error) {
	baseBrowser, err := rod_helper.NewBrowserEx(options)
	if err != nil {
		return nil, nil, err
	}

	browser := s.applyOperationTimeout(baseBrowser)
	closeBrowser := func() {
		browser.CancelTimeout()
		done := make(chan struct{})
		go func() {
			_ = baseBrowser.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(subHDBrowserCloseTimeout):
			s.log.Warningln(s.GetSupplierName(), "browser close timeout")
		}
	}

	return browser, closeBrowser, nil
}

func (s *Supplier) CheckAlive() (bool, int64) {
	opt := rod_helper.NewBrowserOptions(s.log, true, settings.Get())
	opt.SetPreLoadUrl(settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl)
	browser, closeBrowser, err := s.newBrowserWithTimeout(opt)
	if err != nil {
		// 启动探测失败可能只是 Chromium 首次安装或站点限流，不应在整个
		// 进程生命周期内删除 SubHD。真实搜索和下载会返回具体错误。
		s.log.Warningln(s.GetSupplierName(), "CheckAlive inconclusive, keep enabled", err)
		s.isAlive = true
		return true, 0
	}
	defer closeBrowser()

	begin := time.Now()
	_, page, err := rod_helper.HttpGetFromBrowser(browser,
		settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl, 30*time.Second)
	if err != nil {
		// SubHD 首屏经常受验证码或限流影响而超时。启动探测失败不应永久移除
		// 这个字幕源；真实搜索和下载仍会返回具体错误并由调用方处理。
		s.log.Warningln(s.GetSupplierName(), "CheckAlive inconclusive, keep enabled", err)
		s.isAlive = true
		return true, time.Since(begin).Milliseconds()
	}
	_ = page.Close()

	s.isAlive = true
	return true, time.Since(begin).Milliseconds()
}

func (s *Supplier) IsAlive() bool {
	return s.isAlive
}

func (s *Supplier) OverDailyDownloadLimit() bool {
	supplierSettings := settings.Get().AdvancedSettings.SuppliersSettings.SubHD
	if supplierSettings.DailyDownloadLimit == 0 {
		s.log.Warningln(s.GetSupplierName(), "DailyDownloadLimit is 0, will Skip Download")
		return true
	}
	if supplierSettings.DailyDownloadLimit < 0 {
		return false
	}

	// 需要查询今天的限额
	count, err := s.fileDownloader.CacheCenter.DailyDownloadCountGet(s.GetSupplierName(),
		pkg.GetPublicIP(s.log, settings.Get().AdvancedSettings.TaskQueue))
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), "DailyDownloadCountGet", err)
		return true
	}
	if supplierSettings.OverDailyDownloadLimit(count) {
		// 超限了
		s.log.Warningln(s.GetSupplierName(), "DailyDownloadLimit:", supplierSettings.DailyDownloadLimit, "Now Is:", count)
		return true
	} else {
		// 没有超限
		s.log.Infoln(s.GetSupplierName(), "DailyDownloadLimit:", supplierSettings.DailyDownloadLimit, "Now Is:", count)
		return false
	}
}

func (s *Supplier) GetLogger() *logrus.Logger {
	return s.log
}

func (s *Supplier) GetSupplierName() string {
	return common.SubSiteSubHd
}

func (s *Supplier) GetSubListFromFile4Movie(filePath string) ([]supplier.SubInfo, error) {
	return s.getSubListFromFile4Movie(filePath)
}

func (s *Supplier) GetSubListFromFile4Series(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {

	// TODO 是用本地的 Browser 还是远程的，推荐是远程的
	browser, closeBrowser, err := s.newBrowserWithTimeout(rod_helper.NewBrowserOptions(s.log, true, settings.Get()))
	if err != nil {
		return nil, err
	}
	defer closeBrowser()

	mediaInfo, err := mix_media_info.GetMixMediaInfo(s.fileDownloader.MediaInfoDealers,
		seriesInfo.EpList[0].FileFullPath, false)
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), seriesInfo.EpList[0].FileFullPath, "GetMixMediaInfo", err)
		return nil, err
	}
	// 优先中文查询
	keyWord, err := mix_media_info.KeyWordSelect(mediaInfo, seriesInfo.EpList[0].FileFullPath, true, "cn")
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), seriesInfo.EpList[0].FileFullPath, "keyWordSelect", err)
		return nil, err
	}
	if keyWord == "" {
		// 更换英文译名
		keyWord, err = mix_media_info.KeyWordSelect(mediaInfo, seriesInfo.EpList[0].FileFullPath, true, "en")
		if err != nil {
			s.log.Errorln(s.GetSupplierName(), seriesInfo.EpList[0].FileFullPath, "keyWordSelect", err)
			return nil, err
		}
	}
	var subInfos = make([]supplier.SubInfo, 0)
	var subList = make([]HdListItem, 0)
	for value := range seriesInfo.NeedDlSeasonDict {
		// 第一级界面，找到影片的详情界面
		//keyword := seriesInfo.Name + " 第" + zh.Uint64(value).String() + "季"
		keyword := keyWord + " 第" + zh.Uint64(value).String() + "季"
		s.log.Infoln("Search Keyword:", keyword)
		detailPageUrl, err := s.step0(browser, keyword)
		if err != nil {
			s.log.Errorln("subhd step0", keyword)
			return nil, err
		}
		if detailPageUrl == "" {
			// 如果只是搜索不到，则继续换关键词
			s.log.Warning("subhd first search keyword", keyword, "not found")
			keyword = seriesInfo.Name
			s.log.Warning("subhd Retry", keyword)
			s.log.Infoln("Search Keyword:", keyword)
			detailPageUrl, err = s.step0(browser, keyword)
			if err != nil {
				s.log.Errorln("subhd step0", keyword)
				return nil, err
			}
		}
		if detailPageUrl == "" {
			s.log.Warning("subhd search keyword", keyword, "not found")
			continue
		}
		// 列举字幕
		oneSubList, err := s.step1(browser, detailPageUrl, false)
		if err != nil {
			s.log.Errorln("subhd step1", keyword)
			return nil, err
		}

		subList = append(subList, oneSubList...)
	}
	// 与剧集需要下载的集 List 进行比较，找到需要下载的列表
	// 找到那些 Eps 需要下载字幕的
	subInfoNeedDownload := s.whichEpisodeNeedDownloadSub(seriesInfo, subList)
	// 下载字幕
	for i, item := range subInfoNeedDownload {

		subInfo, err := s.fileDownloader.GetEx(s.GetSupplierName(), browser, item.Url, int64(i), item.Season, item.Episode, s.DownFile)
		if err != nil {
			s.log.Errorln(s.GetSupplierName(), "GetEx", item.Title, item.Season, item.Episode, err)
			continue
		}

		subInfos = append(subInfos, *subInfo)
	}

	return subInfos, nil
}

func (s *Supplier) GetSubListFromFile4Anime(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error) {
	panic("not implemented")
}

func (s *Supplier) getSubListFromFile4Movie(filePath string) ([]supplier.SubInfo, error) {
	/*
		虽然是传入视频文件路径，但是其实需要读取对应的视频文件目录下的
		movie.xml 以及 *.nfo，找到 IMDB id
		优先通过 IMDB id 去查找字幕
		如果找不到，再靠文件名提取影片名称去查找
	*/
	// 找到这个视频文件，尝试得到 IMDB ID
	// 目前测试来看，加入 年 这个关键词去搜索，对 2020 年后的影片有利，因为网站有统一的详细页面了，而之前的，没有，会影响识别
	// 所以，year >= 2020 年，则可以多加一个关键词（年）去搜索影片
	imdbInfo, err := decode.GetVideoNfoInfo4Movie(filePath)
	if err != nil {
		// 允许的错误，跳过，继续进行文件名的搜索
		s.log.Errorln("model.GetImdbInfo", err)
	}
	var subInfoList []supplier.SubInfo

	if imdbInfo.ImdbId != "" {
		// 先用 imdb id 找
		subInfoList, err = s.getSubListFromKeyword4Movie(imdbInfo.ImdbId)
		if err != nil {
			// 允许的错误，跳过，继续进行文件名的搜索
			s.log.Errorln(s.GetSupplierName(), "keyword:", imdbInfo.ImdbId)
			s.log.Errorln("getSubListFromKeyword4Movie", "IMDBID can not found sub", filePath, err)
		}
		// 如果有就优先返回
		if len(subInfoList) > 0 {
			return subInfoList, nil
		}
	}
	s.log.Infoln(s.GetSupplierName(), filePath, "No subtitle found", "KeyWord:", imdbInfo.ImdbId)
	mediaInfo, err := mix_media_info.GetMixMediaInfo(s.fileDownloader.MediaInfoDealers,
		filePath, true)
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), filePath, "GetMixMediaInfo", err)
		return nil, err
	}
	// 优先中文查询
	keyWord, err := mix_media_info.KeyWordSelect(mediaInfo, filePath, true, "cn")
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), filePath, "keyWordSelect", err)
		return nil, err
	}
	// 如果没有，那么就用文件名查找
	searchKeyword := search.VideoNameSearchKeywordMaker(s.log, keyWord, imdbInfo.Year)
	subInfoList, err = s.getSubListFromKeyword4Movie(searchKeyword)
	if err != nil {
		s.log.Errorln(s.GetSupplierName(), "keyword:", searchKeyword)
		return nil, err
	}
	if len(subInfoList) < 1 {
		// 切换到英文查询
		s.log.Infoln(s.GetSupplierName(), filePath, "No subtitle found", "KeyWord:", searchKeyword)
		keyWord, err = mix_media_info.KeyWordSelect(mediaInfo, filePath, true, "cn")
		if err != nil {
			s.log.Errorln(s.GetSupplierName(), filePath, "keyWordSelect", err)
			return nil, err
		}
		// 如果没有，那么就用文件名查找
		searchKeyword = search.VideoNameSearchKeywordMaker(s.log, keyWord, imdbInfo.Year)
		subInfoList, err = s.getSubListFromKeyword4Movie(searchKeyword)
		if err != nil {
			s.log.Errorln(s.GetSupplierName(), "keyword:", searchKeyword)
			return nil, err
		}
		if len(subInfoList) < 1 {
			s.log.Infoln(s.GetSupplierName(), filePath, "No subtitle found", "KeyWord:", searchKeyword)
		}
	}

	return subInfoList, nil
}

func (s *Supplier) getSubListFromKeyword4Movie(keyword string) ([]supplier.SubInfo, error) {

	s.log.Infoln("Search Keyword:", keyword)
	// TODO 是用本地的 Browser 还是远程的，推荐是远程的
	browser, closeBrowser, err := s.newBrowserWithTimeout(rod_helper.NewBrowserOptions(s.log, true, settings.Get()))
	if err != nil {
		return nil, err
	}
	defer closeBrowser()
	var subInfos []supplier.SubInfo
	detailPageUrl, err := s.step0(browser, keyword)
	if err != nil {
		return nil, err
	}
	// 没有搜索到字幕
	if detailPageUrl == "" {
		return nil, nil
	}
	subList, err := s.step1(browser, detailPageUrl, true)
	if err != nil {
		return nil, err
	}

	for i, item := range subList {

		subInfo, err := s.fileDownloader.GetEx(s.GetSupplierName(), browser, item.Url, int64(i), 0, 0, s.DownFile)
		if err != nil {
			s.log.Errorln(s.GetSupplierName(), "GetEx", item.Title, item.Season, item.Episode, err)
			continue
		}

		subInfos = append(subInfos, *subInfo)
	}

	return subInfos, nil
}

func (s *Supplier) whichEpisodeNeedDownloadSub(seriesInfo *series.SeriesInfo, allSubList []HdListItem) []HdListItem {
	// 字幕很多，考虑效率，需要做成字典
	// key SxEx - SubInfos
	var allSubDict = make(map[string][]HdListItem)
	// 全季的字幕列表
	var oneSeasonSubDict = make(map[string][]HdListItem)
	for _, subInfo := range allSubList {
		_, season, episode, err := decode.GetSeasonAndEpisodeFromSubFileName(subInfo.Title)
		if err != nil {
			s.log.Errorln("whichEpisodeNeedDownloadSub.GetVideoInfoFromFileFullPath", subInfo.Title, err)
			continue
		}
		subInfo.Season = season
		subInfo.Episode = episode
		epsKey := pkg.GetEpisodeKeyName(season, episode)
		_, ok := allSubDict[epsKey]
		if ok == false {
			// 初始化
			allSubDict[epsKey] = make([]HdListItem, 0)
			if season != 0 && episode == 0 {
				oneSeasonSubDict[epsKey] = make([]HdListItem, 0)
			}
		}
		// 添加
		allSubDict[epsKey] = append(allSubDict[epsKey], subInfo)
		if season != 0 && episode == 0 {
			oneSeasonSubDict[epsKey] = append(oneSeasonSubDict[epsKey], subInfo)
		}
	}
	// 本地的视频列表，找到没有字幕的
	// 需要进行下载字幕的列表
	var subInfoNeedDownload = make([]HdListItem, 0)
	// 有那些 Eps 需要下载的，按 SxEx 反回 epsKey
	for epsKey, epsInfo := range seriesInfo.NeedDlEpsKeyList {
		// 从一堆字幕里面找合适的
		value, ok := allSubDict[epsKey]
		// 是否有
		if ok == true && len(value) > 0 {
			value[0].Season = epsInfo.Season
			value[0].Episode = epsInfo.Episode
			subInfoNeedDownload = append(subInfoNeedDownload, value[0])
		}
	}
	// 全季的字幕列表，也拼进去，后面进行下载
	for _, infos := range oneSeasonSubDict {

		if len(infos) < 1 {
			continue
		}
		subInfoNeedDownload = append(subInfoNeedDownload, infos[0])
	}

	// 返回前，需要把每一个 Eps 的 Season Episode 信息填充到每个 SubInfo 中
	return subInfoNeedDownload
}

// step0 找到这个影片的详情列表
func (s *Supplier) step0(browser *rod.Browser, keyword string) (string, error) {
	var err error
	defer func() {
		if err != nil {
			notify_center.Notify.Add("subhd_step0", err.Error())
		}
	}()

	result, page, err := rod_helper.HttpGetFromBrowser(browser, fmt.Sprintf(settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl+common.SubSubHDSearchUrl, url.QueryEscape(keyword)), s.tt)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = page.Close()
	}()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(result))
	if err != nil {
		return "", err
	}
	imgSelection := doc.Find("img.rounded-start")
	_, ok := imgSelection.Attr("src")
	if ok == true {

		if len(imgSelection.Nodes) < 1 {
			return "", common.SubHDStep0ImgParentLessThan1
		}
		step1Url := ""
		if imgSelection.Nodes[0].Parent.Data == "a" {
			// 第一个父级是不是超链接
			for _, attribute := range imgSelection.Nodes[0].Parent.Attr {
				if attribute.Key == "href" {
					step1Url = attribute.Val
					break
				}
			}
		} else if imgSelection.Nodes[0].Parent.Parent.Data == "a" {
			// 第二个父级是不是超链接
			for _, attribute := range imgSelection.Nodes[0].Parent.Parent.Attr {
				if attribute.Key == "href" {
					step1Url = attribute.Val
					break
				}
			}
		}
		if step1Url == "" {
			return "", common.SubHDStep0HrefIsNull
		}
		return step1Url, nil
	} else {
		// 当前 SubHD 的无结果页仍显示“共 0 条”，但站点限流时也可能
		// 省略计数节点。没有详情链接时按无结果处理，避免每个媒体条目刷 ERROR。
		return "", nil
	}
}

// step1 获取影片的详情字幕列表
func (s *Supplier) step1(browser *rod.Browser, detailPageUrl string, isMovieOrSeries bool) ([]HdListItem, error) {
	var err error
	defer func() {
		if err != nil {
			notify_center.Notify.Add("subhd_step1", err.Error())
		}
	}()
	detailPageUrl = pkg.AddBaseUrl(settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl, detailPageUrl)
	result, page, err := rod_helper.HttpGetFromBrowser(browser, detailPageUrl, s.tt)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = page.Close()
	}()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(result))
	if err != nil {
		return nil, err
	}
	var lists []HdListItem

	const subTableKeyword = ".pt-2"
	const oneSubTrTitleKeyword = "a.link-dark"
	const oneSubTrDownloadCountKeyword = "div.px-3"
	const oneSubLangAndTypeKeyword = ".text-secondary"

	doc.Find(subTableKeyword).EachWithBreak(func(i int, tr *goquery.Selection) bool {
		if tr.Find(oneSubTrTitleKeyword).Size() == 0 {
			return true
		}
		// 文件的下载页面，还需要分析
		downUrl, exists := tr.Find(oneSubTrTitleKeyword).Eq(0).Attr("href")
		if !exists {
			return true
		}
		// 文件名
		title := strings.TrimSpace(tr.Find(oneSubTrTitleKeyword).Text())
		// 字幕类型
		insideSubType := tr.Find(oneSubLangAndTypeKeyword).Text()
		if sub_parser_hub.IsSubTypeWanted(insideSubType) == false {
			return true
		}
		// 下载的次数
		downCount, err := decode.GetNumber2int(tr.Find(oneSubTrDownloadCountKeyword).Eq(1).Text())
		if err != nil {
			return true
		}

		listItem := HdListItem{}
		listItem.Url = downUrl
		listItem.BaseUrl = settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl
		listItem.Title = title
		listItem.DownCount = downCount

		// 电影，就需要第一个
		// 连续剧，需要多个
		if isMovieOrSeries == true {

			if len(lists) >= settings.Get().AdvancedSettings.Topic {
				return false
			}
		}
		lists = append(lists, listItem)
		return true
	})

	return lists, nil
}

type prepareDownloadResponse struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
	Message string `json:"msg"`
}

type finalDownloadResponse struct {
	Success bool   `json:"success"`
	Pass    bool   `json:"pass"`
	URL     string `json:"url"`
	Message string `json:"msg"`
}

func parsePrepareDownloadResponse(body string) (string, error) {
	var response prepareDownloadResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return "", fmt.Errorf("subhd prepare download invalid response: %w", err)
	}
	if !response.Success {
		if response.Message == "" {
			response.Message = "site rejected download preparation"
		}
		return "", errors.New(response.Message)
	}
	if !strings.HasPrefix(response.URL, "/down/") {
		return "", fmt.Errorf("subhd prepare download returned unsafe URL %q", response.URL)
	}
	return response.URL, nil
}

func prepareDownload(page *rod.Page, sid string) (string, error) {
	result, err := page.Eval(`async (sid) => {
		const response = await fetch('/api/sub/prepare-download', {
			method: 'POST',
			headers: {'Content-Type': 'application/json'},
			body: JSON.stringify({sid: sid})
		});
		return await response.text();
	}`, sid)
	if err != nil {
		return "", fmt.Errorf("subhd prepare download request: %w", err)
	}
	return parsePrepareDownloadResponse(result.Value.Str())
}

func parseFinalDownloadResponse(body string) (string, error) {
	var response finalDownloadResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return "", fmt.Errorf("subhd final download invalid response: %w", err)
	}
	if !response.Success || !response.Pass {
		if response.Message == "" {
			response.Message = "site rejected final download"
		}
		return "", errors.New(response.Message)
	}

	downloadURL, err := url.Parse(response.URL)
	if err != nil || downloadURL.Scheme != "https" || !isAllowedSubHDDownloadHost(downloadURL.Hostname()) {
		return "", fmt.Errorf("subhd final download returned unsafe URL %q", response.URL)
	}
	return downloadURL.String(), nil
}

func isAllowedSubHDDownloadHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "subhd.me" || strings.HasSuffix(host, ".subhd.me") ||
		host == "subhd.tv" || strings.HasSuffix(host, ".subhd.tv") ||
		host == "subhdtw.com" || strings.HasSuffix(host, ".subhdtw.com")
}

func (s *Supplier) downloadSubFileHTTP(subDownloadPageFullURL, subDownloadPageURL string) (*supplier.SubInfo, error) {
	client, err := pkg.NewHttpClient(subDownloadPageFullURL)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client.SetCookieJar(jar)

	detailResponse, err := client.R().Get(subDownloadPageFullURL)
	if err != nil {
		return nil, fmt.Errorf("subhd detail request: %w", err)
	}
	if !detailResponse.IsSuccess() {
		return nil, fmt.Errorf("subhd detail request returned HTTP %d", detailResponse.StatusCode())
	}
	detailDocument, err := goquery.NewDocumentFromReader(bytes.NewReader(detailResponse.Body()))
	if err != nil {
		return nil, fmt.Errorf("subhd detail parse: %w", err)
	}
	sid, exists := detailDocument.Find("button.subtitle-prepare-download").First().Attr("data-sid")
	if !exists || sid == "" {
		return nil, errors.New("subhd detail page missing prepare-download sid")
	}

	rootURL := settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl
	prepareResponse, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]string{"sid": sid}).
		Post(pkg.AddBaseUrl(rootURL, "/api/sub/prepare-download"))
	if err != nil {
		return nil, fmt.Errorf("subhd prepare request: %w", err)
	}
	if !prepareResponse.IsSuccess() {
		return nil, fmt.Errorf("subhd prepare request returned HTTP %d", prepareResponse.StatusCode())
	}
	downPath, err := parsePrepareDownloadResponse(prepareResponse.String())
	if err != nil {
		return nil, err
	}

	downPageResponse, err := client.R().Get(pkg.AddBaseUrl(rootURL, downPath))
	if err != nil {
		return nil, fmt.Errorf("subhd download page request: %w", err)
	}
	if !downPageResponse.IsSuccess() {
		return nil, fmt.Errorf("subhd download page returned HTTP %d", downPageResponse.StatusCode())
	}
	downDocument, err := goquery.NewDocumentFromReader(bytes.NewReader(downPageResponse.Body()))
	if err != nil {
		return nil, fmt.Errorf("subhd download page parse: %w", err)
	}
	finalSID, exists := downDocument.Find("button.download-submit").First().Attr("sid")
	if !exists || finalSID == "" {
		return nil, errors.New("subhd download page missing final-download sid")
	}

	finalResponse, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]string{"sid": finalSID}).
		Post(pkg.AddBaseUrl(rootURL, "/api/sub/down"))
	if err != nil {
		return nil, fmt.Errorf("subhd final request: %w", err)
	}
	if !finalResponse.IsSuccess() {
		return nil, fmt.Errorf("subhd final request returned HTTP %d", finalResponse.StatusCode())
	}
	fileURL, err := parseFinalDownloadResponse(finalResponse.String())
	if err != nil {
		return nil, err
	}

	fileResponse, err := client.R().Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("subhd file request: %w", err)
	}
	if !fileResponse.IsSuccess() {
		return nil, fmt.Errorf("subhd file request returned HTTP %d", fileResponse.StatusCode())
	}
	fileBytes := fileResponse.Body()
	prefixLength := len(fileBytes)
	if prefixLength > 512 {
		prefixLength = 512
	}
	if len(fileBytes) == 0 || bytes.Contains(bytes.ToLower(fileBytes[:prefixLength]), []byte("<html")) {
		return nil, errors.New("subhd file request returned HTML or empty content")
	}
	parsedFileURL, _ := url.Parse(fileURL)
	fileName := filepath.Base(parsedFileURL.Path)
	if fileName == "." || fileName == "/" || fileName == "" {
		return nil, errors.New("subhd file response missing filename")
	}

	return supplier.NewSubInfo(s.GetSupplierName(), 1, fileName, language.ChineseSimple,
		subDownloadPageURL, 0, 0, filepath.Ext(fileName), fileBytes), nil
}

// DownFile 下载字幕 过防水墙
func (s *Supplier) DownFile(browser *rod.Browser, subDownloadPageUrl string, TopN int64, Season, Episode int) (*supplier.SubInfo, error) {
	var err error
	defer func() {
		if err != nil {
			notify_center.Notify.Add("subhd_DownFile", err.Error())
		}
	}()
	subDownloadPageFullUrl := pkg.AddBaseUrl(settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl, subDownloadPageUrl)
	subInfo, httpErr := s.downloadSubFileHTTP(subDownloadPageFullUrl, subDownloadPageUrl)
	if httpErr == nil {
		subInfo.TopN = TopN
		subInfo.Season = Season
		subInfo.Episode = Episode
		return subInfo, nil
	}
	s.log.Warningln(s.GetSupplierName(), "direct download failed, fallback to browser", httpErr)
	if browser == nil {
		err = httpErr
		return nil, err
	}

	_, page, err := rod_helper.HttpGetFromBrowser(browser, subDownloadPageFullUrl, s.tt)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = page.Close()
	}()

	// 需要先判断是否先要输入验证码，然后才到下载界面
	// 下载字幕
	subInfo, err = s.downloadSubFile(browser, page, subDownloadPageUrl)
	if err != nil {
		return nil, err
	}

	subInfo.TopN = TopN
	subInfo.Season = Season
	subInfo.Episode = Episode

	return subInfo, nil
}

func (s *Supplier) downloadSubFile(browser *rod.Browser, page *rod.Page, subDownloadPageUrl string) (*supplier.SubInfo, error) {

	var err error
	var doc *goquery.Document
	downloadSuccess := false
	fileName := ""
	fileByte := []byte{0}
	tryErr := rod.Try(func() {
		tmpDir := filepath.Join(pkg.DefTmpFolder(), "downloads")
		wait := browser.Timeout(30 * time.Second).WaitDownload(tmpDir)
		getDownloadFile := func() ([]byte, string, error) {
			info := wait()

			if info == nil {
				return nil, "", errors.New("download sub timeout")
			}

			downloadPath := filepath.Join(tmpDir, info.GUID)
			defer func() { _ = os.Remove(downloadPath) }()
			b, err := os.ReadFile(downloadPath)
			if err != nil {
				return nil, "", err
			}
			return b, info.SuggestedFilename, nil
		}
		// 初始化页面用于查询元素
		pString := page.MustHTML()
		doc, err = goquery.NewDocumentFromReader(strings.NewReader(pString))
		if err != nil {
			return
		}

		// The current site prepares downloads through an authenticated JSON
		// endpoint and then opens /down/<sid> in a new tab. Clicking the button
		// and waiting on the original page misses that browser download. Reuse
		// the page session explicitly so Cloudflare/session cookies are kept.
		prepareButton := doc.Find("button.subtitle-prepare-download").First()
		if sid, exists := prepareButton.Attr("data-sid"); exists && sid != "" {
			downloadPath, prepareErr := prepareDownload(page, sid)
			if prepareErr != nil {
				err = prepareErr
				return
			}
			downloadURL := pkg.AddBaseUrl(settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl, downloadPath)
			_, err = page.Eval(`(url) => { window.location.href = url }`, downloadURL)
			if err != nil {
				return
			}
			fileByte, fileName, err = getDownloadFile()
			if err != nil {
				return
			}
			downloadSuccess = true
			return
		}

		// 点击“验证获取下载地址”
		s.log.Debugln("click '验证获取下载地址'")
		clickCodeBtn := doc.Find(btnClickCodeBtn)
		if len(clickCodeBtn.Nodes) < 1 {
			pageURL := ""
			if info, infoErr := page.Info(); infoErr == nil {
				pageURL = info.URL
			}
			err = fmt.Errorf("subhd download controls missing: title=%q url=%q cloudflare=%t body_bytes=%d",
				strings.TrimSpace(doc.Find("title").Text()), pageURL,
				strings.Contains(strings.ToLower(pString), "cloudflare"), len(pString))
			return
		}
		element := page.MustElement(btnClickCodeBtn)

		findInputCode, err := page.Element(InputCode)
		if err != nil {
			return
		}
		if findInputCode != nil {
			s.log.Debugln("find '验证' 关键词")
			// 那么需要填写验证码
			element.MustClick()
			time.Sleep(time.Second * 2)
			// 填写“验证码”
			s.log.Debugln("填写验证码")
			el := page.MustElement(InputCode)
			el.MustInput(common.SubhdCode)
			//page.MustEval(`$("#gzhcode").attr("value","` + common2.SubhdCode + `");`)
			// 是否有“完成验证”按钮
			s.log.Debugln("查找是否有交验证码按钮1")
			downBtn := doc.Find(btnCommitCode)
			if len(downBtn.Nodes) < 1 {
				return
			}
			s.log.Debugln("查找是否有交验证码按钮2")
			element = page.MustElement(btnCommitCode)
			benCommit := element.MustText()
			if strings.Contains(benCommit, "验证") == false {
				s.log.Errorln("btn not found 完整验证")
				return
			}
			s.log.Debugln("点击提交验证码")
			element.MustClick()
			time.Sleep(time.Second * 2)

			s.log.Debugln("点击下载按钮")
			// 点击下载按钮
			page.MustElement(btnClickCodeBtn).MustClick()

			time.Sleep(time.Second * 2)
		} else {

			s.log.Debugln("点击下载按钮")
			// 直接可以下载
			element.MustClick()
			time.Sleep(time.Second * 2)
		}

		// 更新 page 的实例对应的 doc Content
		pString = page.MustHTML()
		doc, err = goquery.NewDocumentFromReader(strings.NewReader(pString))
		if err != nil {
			return
		}
		// 是否有腾讯的防水墙
		hasWaterWall := false
		waterWall := doc.Find(TCode)
		if len(waterWall.Nodes) >= 1 {
			hasWaterWall = true
		}
		s.log.Debugln("Need pass WaterWall", hasWaterWall)
		// 过墙
		if hasWaterWall == true {
			s.passWaterWall(page)
		}
		fileByte, fileName, err = getDownloadFile()
		if err != nil {
			return
		}
		downloadSuccess = true
	})
	if tryErr != nil {
		return nil, tryErr
	}
	if err != nil {
		return nil, err
	}
	if downloadSuccess == false {
		return nil, common.SubHDStep2ExCannotFindDownloadBtn
	}
	inSubInfo := supplier.NewSubInfo(s.GetSupplierName(), 1, fileName, language.ChineseSimple, subDownloadPageUrl, 0, 0, filepath.Ext(fileName), fileByte)

	return inSubInfo, nil
}

func (s *Supplier) passWaterWall(page *rod.Page) {

	const (
		waterIFrame = "#tcaptcha_iframe"
		dragBtn     = "#tcaptcha_drag_button"
		slideBg     = "#slideBg"
	)

	//等待驗證碼窗體載入
	page.MustElement(waterIFrame).MustWaitLoad()
	//進入到iframe
	iframe := page.MustElement(waterIFrame).MustFrame()
	// see iframe bug, see  https://github.com/go-rod/rod/issues/548
	p := page.Browser().MustPageFromTargetID(proto.TargetTargetID(iframe.FrameID))

	//等待拖動條加載, 延遲500秒檢測變化, 以確認加載完畢
	p.MustElement(dragBtn).MustWaitStable()
	//等待缺口圖像載入
	slideBgEl := p.MustElement(slideBg).MustWaitLoad()
	slideBgEl = slideBgEl.MustWaitStable()
	//取得帶缺口圖像
	shadowbg := slideBgEl.MustResource()
	// 取得原始圖像
	src := slideBgEl.MustProperty("src")
	fullbg, _, err := pkg.DownFile(s.log, strings.Replace(src.String(), "img_index=1", "img_index=0", 1))
	if err != nil {
		s.log.Errorln("passWaterWall.DownFile", err)
		return
	}
	//取得img展示的真實尺寸
	shape, err := slideBgEl.Shape()
	if err != nil {
		s.log.Errorln("passWaterWall.Shape", err)
		return
	}
	bgbox := shape.Box()
	height, width := uint(math.Round(bgbox.Height)), uint(math.Round(bgbox.Width))
	//裁剪圖像
	shadowbgImg, _ := jpeg.Decode(bytes.NewReader(shadowbg))
	shadowbgImg = resize.Resize(width, height, shadowbgImg, resize.Lanczos3)
	fullbgImg, _ := jpeg.Decode(bytes.NewReader(fullbg))
	fullbgImg = resize.Resize(width, height, fullbgImg, resize.Lanczos3)

	//啓始left，排除干擾部份，所以右移10個像素
	left := fullbgImg.Bounds().Min.X + 10
	//啓始top, 排除干擾部份, 所以下移10個像素
	top := fullbgImg.Bounds().Min.Y + 10
	//最大left, 排除干擾部份, 所以左移10個像素
	maxleft := fullbgImg.Bounds().Max.X - 10
	//最大top, 排除干擾部份, 所以上移10個像素
	maxtop := fullbgImg.Bounds().Max.Y - 10
	//rgb比较阈值, 超出此阈值及代表找到缺口位置
	threshold := 20
	//缺口偏移, 拖動按鈕初始會偏移27.5
	distance := -27.5
	//取絕對值方法
	abs := func(n int) int {
		if n < 0 {
			return -n
		}
		return n
	}
search:
	for i := left; i <= maxleft; i++ {
		for j := top; j <= maxtop; j++ {
			colorAR, colorAG, colorAB, _ := fullbgImg.At(i, j).RGBA()
			colorBR, colorBG, colorBB, _ := shadowbgImg.At(i, j).RGBA()
			colorAR, colorAG, colorAB = colorAR>>8, colorAG>>8, colorAB>>8
			colorBR, colorBG, colorBB = colorBR>>8, colorBG>>8, colorBB>>8
			if abs(int(colorAR)-int(colorBR)) > threshold ||
				abs(int(colorAG)-int(colorBG)) > threshold ||
				abs(int(colorAB)-int(colorBB)) > threshold {
				distance += float64(i)
				s.log.Debugln("對比完畢, 偏移量:", distance)
				break search
			}
		}
	}
	//獲取拖動按鈕形狀
	dragBtnBox := p.MustElement("#tcaptcha_drag_thumb").MustShape().Box()
	//启用滑鼠功能
	mouse := p.Mouse
	//模擬滑鼠移動至拖動按鈕處, 右移3的原因: 拖動按鈕比滑塊圖大3個像素
	mouse.MustMoveTo(dragBtnBox.X+3, dragBtnBox.Y+(dragBtnBox.Height/2))
	//按下滑鼠左鍵
	mouse.MustDown("left")
	//開始拖動
	err = mouse.MoveLinear(proto.Point{X: dragBtnBox.X + distance, Y: dragBtnBox.Y + (dragBtnBox.Height / 2)}, 20)
	if err != nil {
		s.log.Errorln("mouse.Move", err)
	}
	//鬆開滑鼠左鍵, 拖动完毕
	mouse.MustUp("left")

	if settings.Get().AdvancedSettings.DebugMode == true {
		//截圖保存
		page.MustScreenshot(pkg.DefDebugFolder(), "result.png")
	}
}

type HdListItem struct {
	Url        string `json:"url"`
	BaseUrl    string `json:"baseUrl"`
	Title      string `json:"title"`
	Ext        string `json:"ext"`
	AuthorInfo string `json:"authorInfo"`
	Lang       string `json:"lang"`
	Rate       string `json:"rate"`
	DownCount  int    `json:"downCount"`
	Season     int    // 第几季，默认-1
	Episode    int    // 第几集，默认-1
}

//type HdContent struct {
//	Filename string `json:"filename"`
//	Ext      string `json:"ext"`
//	Data     []byte `json:"data"`
//}

const TCode = "#TencentCaptcha"
const btnClickCodeBtn = "button.btn-danger"
const btnCommitCode = "button.btn-primary"
const InputCode = "#gzhcode" // id=gzhcode
