package base

import (
	"net/http"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/tmdb_api"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/gin-gonic/gin"
)

func (cb *ControllerBase) CheckTmdbApiHandler(c *gin.Context) {
	var err error
	defer func() {
		// 统一的异常处理
		cb.ErrorProcess(c, "CheckTmdbApiHandler", err)
	}()

	req := tmdb_api.Req{}
	err = c.ShouldBindJSON(&req)
	if err != nil {
		return
	}
	resolvedAPIKey, ok := resolveMaskedSecret(c, req.ApiKey, settings.Get().AdvancedSettings.TmdbApiSettings.ApiKey)
	if !ok {
		return
	}
	req.ApiKey = resolvedAPIKey
	resolvedProxyPassword, ok := resolveMaskedSecret(c,
		req.ProxySettings.InputProxyPassword, settings.Get().AdvancedSettings.ProxySettings.InputProxyPassword)
	if !ok {
		return
	}
	req.ProxySettings.InputProxyPassword = resolvedProxyPassword
	if req.ApiKey == "" {
		c.JSON(http.StatusOK, backend.ReplyCommon{Message: "false"})
		return
	}
	// Validate the submitted settings with a request-local transport. Changing
	// the global proxy bridge here could interrupt queue workers and make their
	// actual network policy diverge from the persisted search fingerprint.
	httpClient, err := newIsolatedHTTPClient(req.ProxySettings, time.Minute)
	if err != nil {
		return
	}
	httpClient = bindHTTPClientContext(httpClient, c.Request.Context())
	// 开始测试 tmdb api
	tmdbApi, err := tmdb_api.NewTmdbHelperWithHTTPClient(
		cb.fileDownloader.Log,
		req.ApiKey,
		req.UseAlternateBaseURL,
		httpClient)
	if err != nil {
		cb.fileDownloader.Log.Errorln("NewTmdbHelper", err)
		return
	}
	aliveStatus := tmdbApi.Alive()
	// 返回结果
	if aliveStatus == false {
		cb.fileDownloader.Log.Errorln("tmdbApi.Alive() == false")
		c.JSON(http.StatusOK, backend.ReplyCommon{Message: "false"})
		return
	} else {
		cb.fileDownloader.Log.Infoln("tmdbApi.Alive() == true")
		c.JSON(http.StatusOK, backend.ReplyCommon{Message: "true"})
		return
	}
}
