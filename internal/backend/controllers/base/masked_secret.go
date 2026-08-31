package base

import (
	"net/http"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/middle"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/gin-gonic/gin"
)

// resolveMaskedSecret lets authenticated settings screens test an unchanged
// masked credential without returning it to the browser. Setup requests that
// submit a real value remain usable before login.
func resolveMaskedSecret(c *gin.Context, incoming, current string) (string, bool) {
	if !settings.IsMaskedSecret(incoming) {
		return incoming, true
	}
	token, ok := middle.AuthorizationToken(c.GetHeader("Authorization"))
	if !ok || token == "" || token != common.GetAccessToken() {
		c.JSON(http.StatusUnauthorized, backendTypes.ReplyCheckAuth{Message: "authentication required for masked credential"})
		return "", false
	}
	return current, true
}
