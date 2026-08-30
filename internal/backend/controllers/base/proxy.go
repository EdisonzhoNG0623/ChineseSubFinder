package base

import (
	"net/http"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/gin-gonic/gin"
)

func (cb *ControllerBase) CheckProxyHandler(c *gin.Context) {
	var err error
	defer func() {
		cb.ErrorProcess(c, "CheckProxyHandler", err)
	}()

	if cb.proxyCheckLocker.Lock() == false {
		c.JSON(http.StatusOK, backend.ReplyCommon{Message: "running"})
		return
	}
	defer cb.proxyCheckLocker.Unlock()

	checkProxy := backend.ReqCheckProxy{}
	if err = c.ShouldBindJSON(&checkProxy); err != nil {
		return
	}
	resolvedProxyPassword, ok := resolveMaskedSecret(c,
		checkProxy.ProxySettings.InputProxyPassword, settings.Get().AdvancedSettings.ProxySettings.InputProxyPassword)
	if !ok {
		return
	}
	checkProxy.ProxySettings.InputProxyPassword = resolvedProxyPassword

	// The proxy button explicitly tests the submitted endpoint even before the
	// settings form is saved. Build a request-local transport instead of
	// replacing the process-wide settings and local proxy bridge: downloads in
	// progress must never observe a temporary UI test configuration.
	checkProxy.ProxySettings.UseProxy = true
	client, clientErr := newIsolatedHTTPClient(checkProxy.ProxySettings, 30*time.Second)
	if clientErr != nil {
		err = clientErr
		return
	}

	outStatus := probeProxyTargets(c.Request.Context(), client, configuredProxyProbeTargets(settings.Get()))
	c.JSON(http.StatusOK, outStatus)
}
