package backend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/middle"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func TestBackendCORSAllowsAuthorizationAndExposesResourceTicket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cors.New(backendCORSConfig()))
	router.GET("/resource-ticket", func(context *gin.Context) {
		context.Header(middle.ResourceAuthTicketHeader, "read-only-ticket")
		context.Header(middle.HLSPlaylistTicketHeader, "playlist-ticket")
		context.Status(http.StatusNoContent)
	})

	preflight := httptest.NewRequest(http.MethodOptions, "/resource-ticket", nil)
	preflight.Header.Set("Origin", "https://frontend.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization")
	preflightResponse := httptest.NewRecorder()
	router.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", preflightResponse.Code, http.StatusNoContent)
	}
	if !headerListContains(preflightResponse.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("Authorization is not allowed by CORS: %v", preflightResponse.Header())
	}

	request := httptest.NewRequest(http.MethodGet, "/resource-ticket", nil)
	request.Header.Set("Origin", "https://frontend.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if !headerListContains(response.Header().Get("Access-Control-Expose-Headers"), middle.ResourceAuthTicketHeader) {
		t.Fatalf("resource ticket header is not exposed by CORS: %v", response.Header())
	}
	if !headerListContains(response.Header().Get("Access-Control-Expose-Headers"), middle.HLSPlaylistTicketHeader) {
		t.Fatalf("HLS playlist ticket header is not exposed by CORS: %v", response.Header())
	}
}

func headerListContains(value, expected string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), expected) {
			return true
		}
	}
	return false
}
