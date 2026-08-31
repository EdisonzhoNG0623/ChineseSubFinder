package middle

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestCheckApiAuthRejectsMalformedAuthorizationWithoutPanicking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldToken := common.GetApiToken()
	common.SetApiToken("test-api-token")
	t.Cleanup(func() { common.SetApiToken(oldToken) })

	for _, header := range []string{"", " ", "token", "Bearer", "Bearer token extra"} {
		t.Run(header, func(t *testing.T) {
			reached := false
			router := gin.New()
			router.GET("/", CheckApiAuth(), func(c *gin.Context) {
				reached = true
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("Authorization %q returned %d, want %d", header, response.Code, http.StatusUnauthorized)
			}
			if reached {
				t.Fatalf("Authorization %q reached the protected handler", header)
			}
		})
	}
}

func TestCheckApiAuthAcceptsConfiguredTokenOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldToken := common.GetApiToken()
	common.SetApiToken("test-api-token")
	t.Cleanup(func() { common.SetApiToken(oldToken) })

	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantNext   bool
	}{
		{name: "configured token", header: "Bearer test-api-token", wantStatus: http.StatusNoContent, wantNext: true},
		{name: "legacy scheme label", header: "Token test-api-token", wantStatus: http.StatusNoContent, wantNext: true},
		{name: "wrong token", header: "Bearer wrong-token", wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reached := false
			router := gin.New()
			router.GET("/", CheckApiAuth(), func(c *gin.Context) {
				reached = true
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if reached != test.wantNext {
				t.Fatalf("protected handler reached = %t, want %t", reached, test.wantNext)
			}
		})
	}
}

func TestCheckAuthSharesTwoFieldAuthorizationContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldToken := common.GetAccessToken()
	common.SetAccessToken("test-access-token")
	t.Cleanup(func() { common.SetAccessToken(oldToken) })

	for _, header := range []string{"Bearer test-access-token", "Token test-access-token"} {
		t.Run(header, func(t *testing.T) {
			router := gin.New()
			router.GET("/", CheckAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("Authorization %q returned %d, want %d", header, response.Code, http.StatusNoContent)
			}
		})
	}
}
