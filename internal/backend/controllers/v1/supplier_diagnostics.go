package v1

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/supplier_search"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/gin-gonic/gin"
)

func (cb *ControllerBase) SupplierDiagnosticsHandler(c *gin.Context) {
	counts, err := cb.cronHelper.FileDownloader.CacheCenter.DailyDownloadCountSummary()
	if err != nil {
		cb.ErrorProcess(c, "SupplierDiagnosticsHandler", err)
		return
	}
	used := make(map[string]int, len(counts))
	for _, count := range counts {
		used[count.SupplierName] = count.Count
	}
	c.JSON(http.StatusOK, backendTypes.ReplySupplierDiagnostics{
		Data:       buildSupplierDiagnostics(settings.Get(), subtitle_metrics.Snapshot(), used),
		IsChecking: cb.cronHelper.Downloader().IsSupplierCheckRunning(), GeneratedAt: time.Now(),
	})
}

func (cb *ControllerBase) SupplierCheckHandler(c *gin.Context) {
	if !cb.cronHelper.Downloader().StartSupplierCheckAsync() {
		c.JSON(http.StatusConflict, backendTypes.ReplyCommon{Message: "supplier check is already running"})
		return
	}
	c.JSON(http.StatusAccepted, backendTypes.ReplyCommon{Message: "supplier check started"})
}

type supplierDefinition struct {
	name, display, defaultURL string
	capabilities              []string
}

var supplierDefinitions = []supplierDefinition{
	{common.SubSiteXunLei, "迅雷", common.SubXunLeiRootUrlDef, []string{"电影", "散列匹配"}},
	{common.SubSiteShooter, "射手网", common.SubShooterRootUrlDef, []string{"电影", "散列匹配"}},
	{common.SubSiteAssrt, "ASSRT", common.SubAssrtRootUrlDef, []string{"电影", "剧集", "字幕包"}},
	{common.SubSiteA4K, "A4K", common.SubA4kRootUrlDef, []string{"自定义镜像"}},
	{common.SubSiteSubtitleBest, "SubtitleBest", common.SubSubtitleBestRootUrlDef, []string{"电影", "剧集", "精确 ID"}},
	{common.SubSiteSubDL, "SubDL", common.SubDLRootURLDef, []string{"电影", "剧集", "绝对集号"}},
	{common.SubSiteOpenSubtitles, "OpenSubtitles.com", common.OpenSubtitlesRootURLDef, []string{"电影", "剧集", "文件散列", "精确 ID"}},
	{common.SubSiteSubSource, "SubSource", common.SubSourceRootURLDef, []string{"电影", "剧集", "整季字幕包", "绝对集号"}},
	{common.SubSiteZiMuKu, "字幕库", common.SubZiMuKuRootUrlDef, []string{"电影", "剧集", "浏览器"}},
	{common.SubSiteSubHd, "SubHD", common.SubSubHDRootUrlDef, []string{"电影", "剧集", "动漫", "绝对集号", "别名回退"}},
}

func buildSupplierDiagnostics(s *settings.Settings, runtime map[string]subtitle_metrics.SupplierRuntime, used map[string]int) []backendTypes.SupplierDiagnostic {
	out := make([]backendTypes.SupplierDiagnostic, 0, len(supplierDefinitions))
	for _, definition := range supplierDefinitions {
		one := supplierSettingsByName(s, definition.name)
		diagnostic := backendTypes.SupplierDiagnostic{Name: definition.name, DisplayName: definition.display,
			DefaultRootURL: definition.defaultURL, Capabilities: append([]string(nil), definition.capabilities...), Health: "UNKNOWN",
			SearchBudgetMs: supplier_search.CurrentTimeout(definition.name).Milliseconds()}
		if one != nil {
			diagnostic.RootURL, diagnostic.DailyLimit = one.RootUrl, one.DailyDownloadLimit
			diagnostic.Enabled = one.DailyDownloadLimit != 0
		} else if definition.name == common.SubSiteOpenSubtitles || definition.name == common.SubSiteSubSource {
			diagnostic.RootURL, diagnostic.DailyLimit, diagnostic.Enabled = definition.defaultURL, -1, true
		}
		diagnostic.Configured = supplierCredentialConfigured(s, definition.name)
		diagnostic.Enabled = diagnostic.Enabled && diagnostic.Configured
		if definition.name == common.SubSiteA4K && isRetiredA4KRoot(diagnostic.RootURL) {
			diagnostic.Enabled, diagnostic.Health, diagnostic.StatusMessage = false, "RETIRED", "公共服务域名已失效，可配置自有镜像"
		} else if !diagnostic.Enabled {
			diagnostic.Health, diagnostic.StatusMessage = "DISABLED", "未启用或缺少凭据"
		} else if pkg.LiteMode() && (definition.name == common.SubSiteZiMuKu || definition.name == common.SubSiteSubHd) {
			diagnostic.Enabled, diagnostic.Health, diagnostic.StatusMessage = false, "UNAVAILABLE_IN_MODE", "Lite 模式不包含浏览器字幕源"
		}
		diagnostic.DailyUsed = used[definition.name]
		if record, ok := runtime[definition.name]; ok && diagnostic.Enabled {
			if record.Health != "" {
				diagnostic.Health, diagnostic.LastCheckedAt, diagnostic.LatencyMillis = record.Health, record.LastCheckedAt, record.LatencyMillis
			}
			diagnostic.CooldownUntil, diagnostic.Attempts = record.CooldownUntil, record.Attempts
			diagnostic.CandidateHits, diagnostic.EmptyResults, diagnostic.Errors = record.CandidateHits, record.EmptyResults, record.Errors
			diagnostic.Candidates, diagnostic.LastAttemptAt, diagnostic.LastAttemptMillis = record.Candidates, record.LastAttemptAt, record.LastAttemptMs
			diagnostic.AverageAttemptMs, diagnostic.P95AttemptMs = record.AverageAttemptMillis(), record.P95AttemptMillis()
			diagnostic.Timeouts, diagnostic.CircuitSkips, diagnostic.CircuitOpenUntil = record.Timeouts, record.CircuitSkips, record.CircuitOpenUntil
			diagnostic.Selections, diagnostic.Saves = record.Selections, record.Saves
			diagnostic.CacheHits, diagnostic.EarlyStops = record.CacheHits, record.EarlyStops
			if time.Now().Before(record.CircuitOpenUntil) {
				diagnostic.Health = "DEGRADED"
				diagnostic.StatusMessage = "连续失败，已临时跳过以释放队列"
			}
		}
		switch {
		case !diagnostic.Enabled:
			diagnostic.AttemptState = "NOT_APPLICABLE"
		case diagnostic.Attempts == 0:
			diagnostic.AttemptState = "NOT_ATTEMPTED"
			diagnostic.NotAttemptedReason = "尚无适用任务，或服务重启后尚未轮到该字幕源"
		case !diagnostic.CircuitOpenUntil.IsZero() && time.Now().Before(diagnostic.CircuitOpenUntil):
			diagnostic.AttemptState = "SKIPPED_TEMPORARILY"
			diagnostic.NotAttemptedReason = "连续错误触发临时熔断，冷却后自动恢复"
		default:
			diagnostic.AttemptState = "ATTEMPTED"
		}
		out = append(out, diagnostic)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func supplierSettingsByName(s *settings.Settings, name string) *settings.OneSupplierSettings {
	if s == nil || s.AdvancedSettings == nil || s.AdvancedSettings.SuppliersSettings == nil {
		return nil
	}
	suppliers := s.AdvancedSettings.SuppliersSettings
	switch name {
	case common.SubSiteXunLei:
		return suppliers.Xunlei
	case common.SubSiteShooter:
		return suppliers.Shooter
	case common.SubSiteAssrt:
		return suppliers.Assrt
	case common.SubSiteA4K:
		return suppliers.A4k
	case common.SubSiteSubtitleBest:
		return suppliers.SubtitleBest
	case common.SubSiteSubDL:
		return suppliers.SubDL
	case common.SubSiteZiMuKu:
		return suppliers.Zimuku
	case common.SubSiteSubHd:
		return suppliers.SubHD
	default:
		return nil
	}
}

func supplierCredentialConfigured(s *settings.Settings, name string) bool {
	if s == nil || s.SubtitleSources == nil {
		return false
	}
	switch name {
	case common.SubSiteAssrt:
		return s.SubtitleSources.AssrtSettings.Enabled && strings.TrimSpace(s.SubtitleSources.AssrtSettings.Token) != ""
	case common.SubSiteSubtitleBest:
		return s.SubtitleSources.SubtitleBestSettings.Enabled && strings.TrimSpace(s.SubtitleSources.SubtitleBestSettings.ApiKey) != ""
	case common.SubSiteSubDL:
		return s.SubtitleSources.SubDLSettings.Enabled && strings.TrimSpace(s.SubtitleSources.SubDLSettings.ApiKey) != ""
	case common.SubSiteOpenSubtitles:
		cfg := s.SubtitleSources.OpenSubtitlesSettings
		return cfg.Enabled && strings.TrimSpace(cfg.APIKey) != "" && strings.TrimSpace(cfg.Username) != "" && cfg.Password != ""
	case common.SubSiteSubSource:
		return s.SubtitleSources.SubSourceSettings.Enabled && strings.TrimSpace(s.SubtitleSources.SubSourceSettings.APIKey) != ""
	default:
		return true
	}
}

func isRetiredA4KRoot(value string) bool {
	value = strings.TrimRight(strings.ToLower(strings.TrimSpace(value)), "/")
	return value == "https://a4k.net" || value == "https://www.a4k.net" || value == "http://a4k.net" || value == "http://www.a4k.net"
}
