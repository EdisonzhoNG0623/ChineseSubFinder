package middle

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/gin-gonic/gin"
)

func TestCheckAuthAfterSetupAllowsWizardAndProtectsConfiguredInstance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	current, initialized := settings.GetIfInitialized()
	if !initialized {
		settings.SetConfigRootPath(t.TempDir())
		current = settings.Get()
	}
	if current.UserInfo == nil {
		current.UserInfo = &settings.UserInfo{}
	}
	originalUser := *current.UserInfo
	originalToken := common.GetAccessToken()
	t.Cleanup(func() {
		*current.UserInfo = originalUser
		common.SetAccessToken(originalToken)
	})

	common.SetAccessToken("admin-token")
	router := gin.New()
	router.GET("/setup-helper", CheckAuthAfterSetup(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})

	current.UserInfo.Username = ""
	current.UserInfo.Password = ""
	response := performRequest(router, http.MethodGet, "/setup-helper", "", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("anonymous first-run helper status = %d, want %d", response.Code, http.StatusNoContent)
	}

	current.UserInfo.Username = "admin"
	current.UserInfo.Password = "configured"
	response = performRequest(router, http.MethodGet, "/setup-helper", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous configured helper status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(router, http.MethodGet, "/setup-helper", "Bearer wrong-token", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-token configured helper status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(router, http.MethodGet, "/setup-helper", "Bearer admin-token", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated configured helper status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestResourceAuthSupportsBearerAndRestrictedBrowserCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalToken := common.GetAccessToken()
	common.SetAccessToken("admin-token")
	t.Cleanup(func() { common.SetAccessToken(originalToken) })

	router := gin.New()
	router.GET("/api", CheckAuth(), IssueResourceAuthCookie(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})
	router.GET("/resource", CheckResourceAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "protected")
	})
	router.POST("/resource", CheckResourceAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "must not be reached")
	})

	response := performRequest(router, http.MethodGet, "/resource", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous resource status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	response = performRequest(router, http.MethodGet, "/api", "Bearer admin-token", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated API status = %d, want %d", response.Code, http.StatusNoContent)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("authenticated API cookies = %d, want 1", len(cookies))
	}
	resourceCookie := cookies[0]
	if resourceCookie.Name != resourceAuthCookieName || resourceCookie.Value == "admin-token" ||
		!resourceCookie.HttpOnly || resourceCookie.SameSite != http.SameSiteStrictMode || resourceCookie.Path != "/" {
		t.Fatalf("unsafe resource cookie: %+v", resourceCookie)
	}
	resourceTicket := response.Header().Get(ResourceAuthTicketHeader)
	if resourceTicket == "" || !validResourceTicket(resourceTicket, "admin-token", time.Now()) {
		t.Fatal("authenticated API did not issue a valid resource ticket")
	}
	if response.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("resource credential response missing privacy headers: %v", response.Header())
	}
	response = performRequest(router, http.MethodGet, "/api", "", resourceCookie)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("resource cookie authenticated management API with status %d", response.Code)
	}

	response = performRequest(router, http.MethodGet, "/resource", "", resourceCookie)
	if response.Code != http.StatusOK || response.Body.String() != "protected" {
		t.Fatalf("cookie-authenticated resource = %d %q", response.Code, response.Body.String())
	}

	ticketTarget := "/resource?" + url.Values{ResourceAuthTicketQueryParam: []string{resourceTicket}}.Encode()
	response = performRequest(router, http.MethodGet, ticketTarget, "", nil)
	if response.Code != http.StatusOK || response.Body.String() != "protected" {
		t.Fatalf("ticket-authenticated resource = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get(ResourceAuthTicketHeader) != "" || len(response.Result().Cookies()) != 0 {
		t.Fatal("resource ticket renewed itself or issued a cookie")
	}
	response = performRequest(router, http.MethodGet, "/api?"+url.Values{
		ResourceAuthTicketQueryParam: []string{resourceTicket},
	}.Encode(), "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("resource ticket authenticated management API with status %d", response.Code)
	}

	response = performRequest(router, http.MethodGet, "/resource", "Token admin-token", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy two-field authorization resource status = %d, want %d", response.Code, http.StatusOK)
	}
	response = performRequest(router, http.MethodPost, ticketTarget, "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("resource ticket POST status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	common.SetAccessToken("rotated-token")
	response = performRequest(router, http.MethodGet, "/resource", "", resourceCookie)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("stale resource cookie status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(router, http.MethodGet, ticketTarget, "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("stale resource ticket status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	response = performRequest(router, http.MethodGet, "/resource", "Bearer rotated-token", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("bearer-authenticated resource status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestResourceTicketRejectsExpiredMalformedAndTamperedValues(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ticket := newResourceTicket("admin-token", now)
	if !validResourceTicket(ticket, "admin-token", now.Add(resourceAuthTicketTTL-time.Second)) {
		t.Fatal("valid ticket was rejected before expiry")
	}
	if validResourceTicket(ticket, "admin-token", now.Add(resourceAuthTicketTTL+time.Second)) {
		t.Fatal("expired ticket was accepted")
	}

	expiresAt, signature, ok := splitResourceTicket(ticket)
	if !ok {
		t.Fatal("ticket did not contain a signature separator")
	}
	replacement := "A"
	if signature[0] == 'A' {
		replacement = "B"
	}
	tampered := expiresAt + "." + replacement + signature[1:]
	if validResourceTicket(tampered, "admin-token", now) {
		t.Fatal("tampered ticket was accepted")
	}
	for _, malformed := range []string{"", "invalid", ".signature", "123.", ticket + ".extra", strings.Repeat("x", resourceAuthTicketMaxLength+1)} {
		if validResourceTicket(malformed, "admin-token", now) {
			t.Fatalf("malformed ticket %q was accepted", malformed)
		}
	}
}

func TestHLSStreamTicketIsPathBoundReadOnlyAndResourceRestricted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalToken := common.GetAccessToken()
	common.SetAccessToken("admin-token")
	t.Cleanup(func() { common.SetAccessToken(originalToken) })

	const videoKey = "encoded-video-a"
	streamTicket := newHLSStreamTicket("admin-token", videoKey, time.Now())
	streamQuery := url.Values{HLSStreamTicketQueryParam: []string{streamTicket}}.Encode()

	router := gin.New()
	router.GET("/segments/:videofpathbase64", CheckHLSStreamAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "segment")
	})
	router.POST("/segments/:videofpathbase64", CheckHLSStreamAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "must not be reached")
	})
	router.GET("/playlist/:videofpathbase64", CheckHLSPlaylistAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "playlist")
	})
	router.GET("/resource", CheckResourceAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "resource")
	})
	router.GET("/api", CheckAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "management")
	})

	response := performRequest(router, http.MethodGet, "/segments/"+videoKey+"?"+streamQuery, "", nil)
	if response.Code != http.StatusOK || response.Body.String() != "segment" {
		t.Fatalf("stream ticket segment response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("stream response missing privacy headers: %v", response.Header())
	}
	if response.Header().Get(ResourceAuthTicketHeader) != "" || response.Header().Get(HLSPlaylistTicketHeader) != "" ||
		len(response.Result().Cookies()) != 0 {
		t.Fatal("stream ticket renewed itself or issued another credential")
	}

	for name, target := range map[string]string{
		"different video path": "/segments/encoded-video-b?" + streamQuery,
		"playlist":             "/playlist/" + videoKey + "?" + streamQuery,
		"generic resource":     "/resource?" + streamQuery,
		"management api":       "/api?" + streamQuery,
	} {
		t.Run(name, func(t *testing.T) {
			response := performRequest(router, http.MethodGet, target, "", nil)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("stream ticket reused for %s returned %d, want %d", name, response.Code, http.StatusUnauthorized)
			}
		})
	}
	generalTicket := newResourceTicket("admin-token", time.Now())
	response = performRequest(
		router,
		http.MethodGet,
		"/segments/"+videoKey+"?"+url.Values{ResourceAuthTicketQueryParam: []string{generalTicket}}.Encode(),
		"",
		nil,
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("general resource ticket authenticated HLS segment with status %d", response.Code)
	}

	response = performRequest(router, http.MethodPost, "/segments/"+videoKey+"?"+streamQuery, "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("stream ticket POST status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	common.SetAccessToken("rotated-token")
	response = performRequest(router, http.MethodGet, "/segments/"+videoKey+"?"+streamQuery, "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("rotated stream ticket status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestHLSPlaylistTicketIsPathBoundCannotSelfRenewAndRejectsOtherTicketTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalToken := common.GetAccessToken()
	common.SetAccessToken("admin-token")
	t.Cleanup(func() { common.SetAccessToken(originalToken) })

	const videoKey = "encoded-video-a"
	const streamTicketTestHeader = "X-Test-HLS-Stream-Ticket"
	router := gin.New()
	router.GET("/playlist/:videofpathbase64", CheckHLSPlaylistAuth(), func(context *gin.Context) {
		IssueHLSPlaylistTicket(context, context.Param("videofpathbase64"))
		context.Header(streamTicketTestHeader, NewHLSStreamTicket(context, context.Param("videofpathbase64")))
		context.String(http.StatusOK, "playlist")
	})
	router.POST("/playlist/:videofpathbase64", CheckHLSPlaylistAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "must not be reached")
	})
	router.GET("/resource", CheckResourceAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "resource")
	})
	router.GET("/segments/:videofpathbase64", CheckHLSStreamAuth(), func(context *gin.Context) {
		context.String(http.StatusOK, "segment")
	})

	issued := performRequest(router, http.MethodGet, "/playlist/"+videoKey, "Bearer admin-token", nil)
	if issued.Code != http.StatusOK {
		t.Fatalf("management-authenticated playlist status = %d, want %d", issued.Code, http.StatusOK)
	}
	playlistTicket := issued.Header().Get(HLSPlaylistTicketHeader)
	if playlistTicket == "" || !validHLSPlaylistTicket(playlistTicket, "admin-token", videoKey, time.Now()) {
		t.Fatal("management-authenticated playlist did not issue a valid path-bound ticket")
	}
	if streamTicket := issued.Header().Get(streamTicketTestHeader); streamTicket != "" {
		t.Fatalf("management-authenticated playlist leaked stream ticket %q", streamTicket)
	}
	issuedCookies := issued.Result().Cookies()
	if len(issuedCookies) != 1 {
		t.Fatalf("management-authenticated playlist cookies = %d, want 1", len(issuedCookies))
	}
	cookieResponse := performRequest(router, http.MethodGet, "/playlist/"+videoKey, "", issuedCookies[0])
	if cookieResponse.Code != http.StatusOK {
		t.Fatalf("cookie-authenticated playlist status = %d, want %d", cookieResponse.Code, http.StatusOK)
	}
	if streamTicket := cookieResponse.Header().Get(streamTicketTestHeader); streamTicket != "" {
		t.Fatalf("cookie-authenticated playlist leaked stream ticket %q", streamTicket)
	}
	playlistQuery := url.Values{HLSPlaylistTicketQueryParam: []string{playlistTicket}}.Encode()

	ticketResponse := performRequest(router, http.MethodGet, "/playlist/"+videoKey+"?"+playlistQuery, "", nil)
	if ticketResponse.Code != http.StatusOK || ticketResponse.Body.String() != "playlist" {
		t.Fatalf("ticket-authenticated playlist = %d %q", ticketResponse.Code, ticketResponse.Body.String())
	}
	if renewed := ticketResponse.Header().Get(HLSPlaylistTicketHeader); renewed != "" {
		t.Fatalf("playlist ticket renewed itself with %q", renewed)
	}
	streamTicket := ticketResponse.Header().Get(streamTicketTestHeader)
	if streamTicket == "" || !validHLSStreamTicket(streamTicket, "admin-token", videoKey, time.Now()) {
		t.Fatal("cross-origin playlist ticket did not mint a valid path-bound stream ticket")
	}
	if ticketResponse.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(ticketResponse.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("playlist response missing privacy headers: %v", ticketResponse.Header())
	}

	for name, target := range map[string]string{
		"different video path": "/playlist/encoded-video-b?" + playlistQuery,
		"generic resource":     "/resource?" + playlistQuery,
		"HLS segment":          "/segments/" + videoKey + "?" + playlistQuery,
	} {
		t.Run(name, func(t *testing.T) {
			response := performRequest(router, http.MethodGet, target, "", nil)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("playlist ticket reused for %s returned %d, want %d", name, response.Code, http.StatusUnauthorized)
			}
		})
	}
	for name, query := range map[string]string{
		"general resource ticket": url.Values{
			ResourceAuthTicketQueryParam: []string{newResourceTicket("admin-token", time.Now())},
		}.Encode(),
		"stream ticket": url.Values{
			HLSStreamTicketQueryParam: []string{newHLSStreamTicket("admin-token", videoKey, time.Now())},
		}.Encode(),
	} {
		t.Run(name, func(t *testing.T) {
			response := performRequest(router, http.MethodGet, "/playlist/"+videoKey+"?"+query, "", nil)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("%s authenticated playlist with status %d", name, response.Code)
			}
		})
	}

	response := performRequest(router, http.MethodPost, "/playlist/"+videoKey+"?"+playlistQuery, "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("playlist ticket POST status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestHLSPlaylistTicketRejectsExpiryAndTampering(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	const videoKey = "encoded-video"
	ticket := newHLSPlaylistTicket("admin-token", videoKey, now)
	if !validHLSPlaylistTicket(ticket, "admin-token", videoKey, now.Add(resourceHLSPlaylistTicketTTL-time.Second)) {
		t.Fatal("valid playlist ticket was rejected before expiry")
	}
	if validHLSPlaylistTicket(ticket, "admin-token", videoKey, now.Add(resourceHLSPlaylistTicketTTL+time.Second)) {
		t.Fatal("expired playlist ticket was accepted")
	}
	if validHLSPlaylistTicket(ticket, "admin-token", "different-video", now) {
		t.Fatal("playlist ticket was accepted for a different video path")
	}

	expiresAt, signature, ok := splitResourceTicket(ticket)
	if !ok {
		t.Fatal("playlist ticket did not contain a signature separator")
	}
	replacement := "A"
	if signature[0] == 'A' {
		replacement = "B"
	}
	if validHLSPlaylistTicket(expiresAt+"."+replacement+signature[1:], "admin-token", videoKey, now) {
		t.Fatal("tampered playlist ticket was accepted")
	}
}

func TestHLSStreamTicketRejectsExpiryAndTampering(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	const videoKey = "encoded-video"
	ticket := newHLSStreamTicket("admin-token", videoKey, now)
	if !validHLSStreamTicket(ticket, "admin-token", videoKey, now.Add(resourceHLSStreamTicketTTL-time.Second)) {
		t.Fatal("valid stream ticket was rejected before expiry")
	}
	if validHLSStreamTicket(ticket, "admin-token", videoKey, now.Add(resourceHLSStreamTicketTTL+time.Second)) {
		t.Fatal("expired stream ticket was accepted")
	}
	if validHLSStreamTicket(ticket, "admin-token", "different-video", now) {
		t.Fatal("stream ticket was accepted for a different video path")
	}

	expiresAt, signature, ok := splitResourceTicket(ticket)
	if !ok {
		t.Fatal("stream ticket did not contain a signature separator")
	}
	replacement := "A"
	if signature[0] == 'A' {
		replacement = "B"
	}
	if validHLSStreamTicket(expiresAt+"."+replacement+signature[1:], "admin-token", videoKey, now) {
		t.Fatal("tampered stream ticket was accepted")
	}
}

func TestResourceTicketTypesAreCryptographicallyDomainSeparatedAcrossRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalToken := common.GetAccessToken()
	common.SetAccessToken("admin-token")
	t.Cleanup(func() { common.SetAccessToken(originalToken) })

	const videoKey = "encoded-video"
	now := time.Now()
	generalTicket := newResourceTicket("admin-token", now)
	playlistTicket := newHLSPlaylistTicket("admin-token", videoKey, now)
	streamTicket := newHLSStreamTicket("admin-token", videoKey, now)

	router := gin.New()
	router.GET("/resource", CheckResourceAuth(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})
	router.GET("/playlist/:videofpathbase64", CheckHLSPlaylistAuth(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})
	router.GET("/segments/:videofpathbase64", CheckHLSStreamAuth(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})

	testCases := []struct {
		name   string
		target string
	}{
		{
			name: "playlist value relabeled as general resource",
			target: "/resource?" + url.Values{
				ResourceAuthTicketQueryParam: []string{playlistTicket},
			}.Encode(),
		},
		{
			name: "playlist value relabeled as stream",
			target: "/segments/" + videoKey + "?" + url.Values{
				HLSStreamTicketQueryParam: []string{playlistTicket},
			}.Encode(),
		},
		{
			name: "stream value relabeled as general resource",
			target: "/resource?" + url.Values{
				ResourceAuthTicketQueryParam: []string{streamTicket},
			}.Encode(),
		},
		{
			name: "stream value relabeled as playlist",
			target: "/playlist/" + videoKey + "?" + url.Values{
				HLSPlaylistTicketQueryParam: []string{streamTicket},
			}.Encode(),
		},
		{
			name: "general value relabeled as playlist",
			target: "/playlist/" + videoKey + "?" + url.Values{
				HLSPlaylistTicketQueryParam: []string{generalTicket},
			}.Encode(),
		},
		{
			name: "general value relabeled as stream",
			target: "/segments/" + videoKey + "?" + url.Values{
				HLSStreamTicketQueryParam: []string{generalTicket},
			}.Encode(),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := performRequest(router, http.MethodGet, testCase.target, "", nil)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("cross-domain ticket status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestClearResourceAuthCookieExpiresCookieAndServerTokenInvalidatesTicket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalToken := common.GetAccessToken()
	common.SetAccessToken("admin-token")
	t.Cleanup(func() { common.SetAccessToken(originalToken) })

	router := gin.New()
	router.GET("/api", CheckAuth(), IssueResourceAuthCookie(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})
	router.POST("/logout", CheckAuth(), ClearResourceAuthCookie(), func(context *gin.Context) {
		common.SetAccessToken("")
		context.Status(http.StatusNoContent)
	})
	router.GET("/resource", CheckResourceAuth(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})

	issued := performRequest(router, http.MethodGet, "/api", "Bearer admin-token", nil)
	resourceCookie := issued.Result().Cookies()[0]
	resourceTicket := issued.Header().Get(ResourceAuthTicketHeader)
	loggedOut := performRequest(router, http.MethodPost, "/logout", "Bearer admin-token", resourceCookie)
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", loggedOut.Code, http.StatusNoContent)
	}
	deletedCookies := loggedOut.Result().Cookies()
	if len(deletedCookies) != 1 || deletedCookies[0].Name != resourceAuthCookieName ||
		deletedCookies[0].Value != "" || deletedCookies[0].MaxAge >= 0 {
		t.Fatalf("logout did not explicitly expire resource cookie: %+v", deletedCookies)
	}

	for name, targetCookie := range map[string]*http.Cookie{"cookie": resourceCookie, "ticket": nil} {
		t.Run(name, func(t *testing.T) {
			target := "/resource"
			if name == "ticket" {
				target += "?" + url.Values{ResourceAuthTicketQueryParam: []string{resourceTicket}}.Encode()
			}
			response := performRequest(router, http.MethodGet, target, "", targetCookie)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("post-logout %s status = %d, want %d", name, response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func performRequest(router http.Handler, method, target, authorization string, cookie *http.Cookie) *httptest.ResponseRecorder {
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
