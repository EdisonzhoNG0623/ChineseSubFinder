package backend

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/middle"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestProtectedStaticFSRejectsAnonymousAndSupportsBrowserCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mediaRoot := t.TempDir()
	const content = "private media metadata"
	if err := os.WriteFile(filepath.Join(mediaRoot, "poster.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	originalToken := common.GetAccessToken()
	common.SetAccessToken("admin-token")
	t.Cleanup(func() { common.SetAccessToken(originalToken) })

	router := gin.New()
	registerProtectedStaticFS(router, "/media", http.Dir(mediaRoot))

	response := requestStatic(router, http.MethodGet, "/media/poster.txt", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous static resource status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	responseFromBearer := requestStatic(router, http.MethodGet, "/media/poster.txt", "Bearer admin-token", nil)
	if responseFromBearer.Code != http.StatusOK || responseFromBearer.Body.String() != content {
		t.Fatalf("bearer static resource = %d %q", responseFromBearer.Code, responseFromBearer.Body.String())
	}
	cookies := responseFromBearer.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("bearer static response cookies = %d, want 1", len(cookies))
	}

	response = requestStatic(router, http.MethodGet, "/media/poster.txt", "", cookies[0])
	if response.Code != http.StatusOK || response.Body.String() != content {
		t.Fatalf("cookie static resource = %d %q", response.Code, response.Body.String())
	}

	ticket := responseFromBearer.Header().Get(middle.ResourceAuthTicketHeader)
	ticketTarget := "/media/poster.txt?" + url.Values{
		middle.ResourceAuthTicketQueryParam: []string{ticket},
	}.Encode()
	response = requestStatic(router, http.MethodGet, ticketTarget, "", nil)
	if response.Code != http.StatusOK || response.Body.String() != content {
		t.Fatalf("ticket static resource = %d %q", response.Code, response.Body.String())
	}

	response = requestStatic(router, http.MethodHead, "/media/poster.txt", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous static HEAD status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func requestStatic(router http.Handler, method, target, authorization string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
