package base

import (
	"net/http/httptest"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestResolveMaskedSecretRequiresAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	common.SetAccessToken("test-access-token")
	t.Cleanup(func() { common.SetAccessToken("") })

	unauthorizedContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	unauthorizedContext.Request = httptest.NewRequest("POST", "/check", nil)
	if _, ok := resolveMaskedSecret(unauthorizedContext, "******", "real-secret"); ok {
		t.Fatal("masked credential should require authentication")
	}

	for _, authorization := range []string{"Bearer test-access-token", "Token test-access-token"} {
		t.Run(authorization, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			authorizedContext, _ := gin.CreateTestContext(recorder)
			authorizedContext.Request = httptest.NewRequest("POST", "/check", nil)
			authorizedContext.Request.Header.Set("Authorization", authorization)
			value, ok := resolveMaskedSecret(authorizedContext, "******", "real-secret")
			if !ok || value != "real-secret" {
				t.Fatalf("masked credential was not restored: value=%q ok=%v", value, ok)
			}
		})
	}
}
