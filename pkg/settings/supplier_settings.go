package settings

import (
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

type SuppliersSettings struct {
	Xunlei       *OneSupplierSettings `json:"xunlei"`
	Shooter      *OneSupplierSettings `json:"shooter"`
	Assrt        *OneSupplierSettings `json:"assrt"`
	A4k          *OneSupplierSettings `json:"a4k"`
	SubHD        *OneSupplierSettings `json:"subhd"`
	Zimuku       *OneSupplierSettings `json:"zimuku"`
	SubtitleBest *OneSupplierSettings `json:"subtitle_best"`
}

func NewSuppliersSettings() *SuppliersSettings {
	return &SuppliersSettings{
		Xunlei:       NewOneSupplierSettings(common.SubSiteXunLei, common.SubXunLeiRootUrlDef, "", -1),
		Shooter:      NewOneSupplierSettings(common.SubSiteShooter, common.SubShooterRootUrlDef, "", -1),
		Assrt:        NewOneSupplierSettings(common.SubSiteAssrt, common.SubAssrtRootUrlDef, "", -1),
		A4k:          NewOneSupplierSettings(common.SubSiteA4K, common.SubA4kRootUrlDef, common.SubA4kSearchUrl, -1),
		SubtitleBest: NewOneSupplierSettings(common.SubSiteSubtitleBest, common.SubSubtitleBestRootUrlDef, common.SubSubtitleBestSearchMovieUrl, -1),
		// 自用模式不设置本地每日硬上限；每次任务仍只下载 Topic 指定的字幕数。
		SubHD:  NewOneSupplierSettings(common.SubSiteSubHd, common.SubSubHDRootUrlDef, common.SubSubHDSearchUrl, -1),
		Zimuku: NewOneSupplierSettings(common.SubSiteZiMuKu, common.SubZiMuKuRootUrlDef, common.SubZiMuKuSearchFormatUrl, -1),
	}
}

// ReSetSearchUrl 因为 SuppliersSettings 中每个网站的 searchUrl 参数没有开放更改，所以如果有变动，需要重新设置
func (s *SuppliersSettings) ReSetSearchUrl() {
	s.A4k.SearchUrl = common.SubA4kSearchUrl
	s.SubtitleBest.SearchUrl = common.SubSubtitleBestSearchMovieUrl
	s.SubHD.SearchUrl = common.SubSubHDSearchUrl
	s.Zimuku.SearchUrl = common.SubZiMuKuSearchFormatUrl
	// 字幕库旧域名已停用；只迁移内置旧值，保留用户显式配置的镜像站。
	if s.Zimuku.RootUrl == "https://zimuku.org" {
		s.Zimuku.RootUrl = common.SubZiMuKuRootUrlDef
	}
	// 全功能被移除前的 20 是程序内置默认值；自用恢复版迁移为不限。
	// 其他正数视为用户显式设置，不做覆盖。
	if s.SubHD.DailyDownloadLimit == 20 {
		s.SubHD.DailyDownloadLimit = -1
	}
	if s.Zimuku.DailyDownloadLimit == 20 {
		s.Zimuku.DailyDownloadLimit = -1
	}
}

type OneSupplierSettings struct {
	Name               string `json:"name"`
	RootUrl            string `json:"root_url"`
	SearchUrl          string `json:"search_url"`
	DailyDownloadLimit int    `json:"daily_download_limit" default:"-1"` // -1 是无限制
}

// OverDailyDownloadLimit reports whether a supplier should be skipped.
// Zero disables it, a negative value means unlimited, and a positive value is
// the maximum number of successful downloads per day.
func (s *OneSupplierSettings) OverDailyDownloadLimit(downloadCount int) bool {
	if s.DailyDownloadLimit == 0 {
		return true
	}
	if s.DailyDownloadLimit < 0 {
		return false
	}
	return downloadCount >= s.DailyDownloadLimit
}

func NewOneSupplierSettings(name string, rootUrl, searchUrl string, dailyDownloadLimit int) *OneSupplierSettings {
	return &OneSupplierSettings{Name: name, RootUrl: rootUrl, SearchUrl: searchUrl, DailyDownloadLimit: dailyDownloadLimit}
}

func (s *OneSupplierSettings) GetSearchUrl() string {
	return s.RootUrl + s.SearchUrl
}
