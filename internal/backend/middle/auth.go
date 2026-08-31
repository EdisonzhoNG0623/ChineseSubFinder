package middle

import (
	"net/http"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/common"
	"github.com/gin-gonic/gin"
)

func CheckAuth() gin.HandlerFunc {

	return func(context *gin.Context) {
		nowAccessToken, ok := AuthorizationToken(context.GetHeader("Authorization"))
		if !ok {
			context.JSON(http.StatusUnauthorized, backend.ReplyCheckAuth{Message: "Request Header Authorization Error"})
			context.Abort()
			return
		}
		if nowAccessToken == "" || nowAccessToken != common.GetAccessToken() {
			context.JSON(http.StatusUnauthorized, backend.ReplyCheckAuth{Message: "AccessToken Error"})
			context.Abort()
			return
		}
		// 向下传递消息
		context.Next()
	}
}

func CheckApiAuth() gin.HandlerFunc {

	return func(context *gin.Context) {
		nowAccessToken, ok := AuthorizationToken(context.GetHeader("Authorization"))
		if !ok {
			context.JSON(http.StatusUnauthorized, backend.ReplyCheckAuth{Message: "Request Header Authorization Error"})
			context.Abort()
			return
		}
		if nowAccessToken == "" {
			context.JSON(http.StatusUnauthorized, backend.ReplyCheckAuth{Message: "api_key_enabled == false or api_key is empty"})
			context.Abort()
			return
		} else if nowAccessToken != common.GetApiToken() {
			context.JSON(http.StatusUnauthorized, backend.ReplyCheckAuth{Message: "AccessToken Error"})
			context.Abort()
			return
		}
		// 向下传递消息
		context.Next()
	}
}

// AuthorizationToken preserves the long-standing management/API contract:
// Authorization must contain exactly two non-empty, whitespace-separated
// fields and the second field is the credential. The browser sends "Bearer",
// while existing API clients may use another scheme label.
func AuthorizationToken(value string) (string, bool) {
	fields := strings.Fields(value)
	if len(fields) != 2 {
		return "", false
	}
	return fields[1], true
}
