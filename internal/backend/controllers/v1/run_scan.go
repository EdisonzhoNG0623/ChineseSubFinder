package v1

import (
	"net/http"

	backend2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/gin-gonic/gin"
)

// RunScanHandler starts one immediate library scan while preserving the
// configured cron schedule. The /api/v1 group supplies API-key authentication.
func (cb *ControllerBase) RunScanHandler(c *gin.Context) {
	if !cb.cronHelper.RunScanNow() {
		c.JSON(http.StatusConflict, backend2.ReplyCommon{Message: "cron helper is not running"})
		return
	}
	c.JSON(http.StatusAccepted, backend2.ReplyCommon{Message: "scan started"})
}
