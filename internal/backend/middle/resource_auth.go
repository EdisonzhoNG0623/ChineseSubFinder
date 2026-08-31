package middle

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/gin-gonic/gin"
)

const (
	resourceAuthCookieName       = "csf_resource_auth"
	resourceAuthContext          = "ChineseSubFinder/read-only-resource/v1"
	resourceHLSPlaylistContext   = "ChineseSubFinder/read-only-hls-playlist/v1"
	resourceHLSStreamContext     = "ChineseSubFinder/read-only-hls-stream/v1"
	resourceAuthTicketTTL        = 10 * time.Minute
	resourceHLSPlaylistTicketTTL = 10 * time.Minute
	resourceHLSStreamTicketTTL   = 6 * time.Hour
	resourceAuthTicketMaxLength  = 192
	ResourceAuthTicketHeader     = "X-CSF-Resource-Ticket"
	ResourceAuthTicketQueryParam = "resource_ticket"
	HLSPlaylistTicketHeader      = "X-CSF-HLS-Playlist-Ticket"
	HLSPlaylistTicketQueryParam  = "hls_ticket"
	HLSStreamTicketQueryParam    = "stream_ticket"
	hlsPlaylistTicketIssuableKey = "csf_hls_playlist_ticket_issuable"
	hlsStreamTicketIssuableKey   = "csf_hls_stream_ticket_issuable"
	hlsValidatedAccessTokenKey   = "csf_hls_validated_access_token"
)

// CheckAuthAfterSetup keeps the first-run setup helpers available until an
// administrator has been configured. From that point on they have the same
// authentication boundary as the management API.
func CheckAuthAfterSetup() gin.HandlerFunc {
	requireAuth := CheckAuth()
	return func(context *gin.Context) {
		current := settings.Get()
		if current == nil || current.UserInfo == nil ||
			current.UserInfo.Username == "" || current.UserInfo.Password == "" {
			context.Next()
			return
		}
		requireAuth(context)
	}
}

// IssueResourceAuthCookie bridges authenticated API calls to browser-native
// image, subtitle and media requests, which cannot attach the Axios
// Authorization header. Same-origin browsers receive an HttpOnly cookie;
// separate BACKEND_URL deployments receive a short-lived read-only ticket in
// a response header. Neither credential is accepted by CheckAuth.
func IssueResourceAuthCookie() gin.HandlerFunc {
	return func(context *gin.Context) {
		accessToken := common.GetAccessToken()
		authorization, validHeader := AuthorizationToken(context.GetHeader("Authorization"))
		if accessToken != "" && validHeader && authorization == accessToken {
			issueResourceCredentials(context, accessToken, time.Now())
		}
		context.Next()
	}
}

// CheckResourceAuth accepts the normal management Authorization header for
// programmatic callers and the restricted HttpOnly cookie for browser-native
// resource loads.
func CheckResourceAuth() gin.HandlerFunc {
	return func(context *gin.Context) {
		if context.Request.Method != http.MethodGet && context.Request.Method != http.MethodHead {
			rejectResourceAuth(context)
			return
		}

		accessToken := common.GetAccessToken()
		if accessToken == "" {
			rejectResourceAuth(context)
			return
		}

		if authorizeResourceRequest(context, accessToken, time.Now()) {
			context.Next()
			return
		}
		rejectResourceAuth(context)
	}
}

// CheckHLSStreamAuth adds one narrowly scoped credential to the ordinary
// resource boundary. A stream ticket is accepted only for the exact encoded
// video path present in this segment route; it cannot authenticate a playlist,
// another static resource, or a management API.
func CheckHLSStreamAuth() gin.HandlerFunc {
	return func(context *gin.Context) {
		if context.Request.Method != http.MethodGet && context.Request.Method != http.MethodHead {
			rejectResourceAuth(context)
			return
		}

		accessToken := common.GetAccessToken()
		if accessToken == "" {
			rejectResourceAuth(context)
			return
		}
		now := time.Now()
		if authorizeResourceHeaderOrCookie(context, accessToken, now) ||
			validHLSStreamTicket(
				context.Query(HLSStreamTicketQueryParam),
				accessToken,
				context.Param("videofpathbase64"),
				now,
			) {
			protectResourceResponse(context)
			context.Next()
			return
		}
		rejectResourceAuth(context)
	}
}

// CheckHLSPlaylistAuth accepts the normal management header, the same-origin
// resource cookie, or a short-lived ticket bound to this exact playlist. It
// deliberately does not accept the general resource or HLS stream tickets.
func CheckHLSPlaylistAuth() gin.HandlerFunc {
	return func(context *gin.Context) {
		if context.Request.Method != http.MethodGet && context.Request.Method != http.MethodHead {
			rejectResourceAuth(context)
			return
		}

		accessToken := common.GetAccessToken()
		if accessToken == "" {
			rejectResourceAuth(context)
			return
		}
		now := time.Now()
		if authorizeResourceHeaderOrCookie(context, accessToken, now) {
			context.Set(hlsPlaylistTicketIssuableKey, true)
			context.Set(hlsValidatedAccessTokenKey, accessToken)
			protectResourceResponse(context)
			context.Next()
			return
		}
		if validHLSPlaylistTicket(
			context.Query(HLSPlaylistTicketQueryParam),
			accessToken,
			context.Param("videofpathbase64"),
			now,
		) {
			context.Set(hlsValidatedAccessTokenKey, accessToken)
			// Only the cross-origin, path-bound playlist capability needs to
			// mint a segment capability. Same-origin bearer/cookie requests can
			// keep every segment URL credential-free and use the HttpOnly cookie.
			context.Set(hlsStreamTicketIssuableKey, true)
			protectResourceResponse(context)
			context.Next()
			return
		}
		rejectResourceAuth(context)
	}
}

func authorizeResourceRequest(context *gin.Context, accessToken string, now time.Time) bool {
	if authorizeResourceHeaderOrCookie(context, accessToken, now) {
		return true
	}
	if validResourceTicket(context.Query(ResourceAuthTicketQueryParam), accessToken, now) {
		protectResourceResponse(context)
		return true
	}
	return false
}

func authorizeResourceHeaderOrCookie(context *gin.Context, accessToken string, now time.Time) bool {
	authorization, validHeader := AuthorizationToken(context.GetHeader("Authorization"))
	if validHeader && authorization == accessToken {
		issueResourceCredentials(context, accessToken, now)
		return true
	}

	cookieValue, err := context.Cookie(resourceAuthCookieName)
	expected := resourceCookieValue(accessToken)
	if err == nil && subtle.ConstantTimeCompare([]byte(cookieValue), []byte(expected)) == 1 {
		protectResourceResponse(context)
		return true
	}
	return false
}

// ClearResourceAuthCookie explicitly expires the same-origin browser bridge.
// The server-side token rotation performed by logout also invalidates every
// previously issued cookie and ticket immediately.
func ClearResourceAuthCookie() gin.HandlerFunc {
	return func(context *gin.Context) {
		clearResourceAuthCookie(context)
		context.Next()
	}
}

func resourceCookieValue(accessToken string) string {
	mac := hmac.New(sha256.New, []byte(accessToken))
	_, _ = mac.Write([]byte(resourceAuthContext))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newResourceTicket(accessToken string, now time.Time) string {
	expiresAt := strconv.FormatInt(now.Add(resourceAuthTicketTTL).Unix(), 10)
	signature := resourceTicketSignature(accessToken, expiresAt)
	return expiresAt + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// NewHLSStreamTicket returns an in-memory, path-bound capability for native
// HLS engines that cannot add Authorization headers to segment requests.
func NewHLSStreamTicket(context *gin.Context, videoFPathBase64 string) string {
	if !context.GetBool(hlsStreamTicketIssuableKey) {
		return ""
	}
	accessToken := context.GetString(hlsValidatedAccessTokenKey)
	if accessToken == "" || videoFPathBase64 == "" {
		return ""
	}
	return newHLSStreamTicket(accessToken, videoFPathBase64, time.Now())
}

func newHLSStreamTicket(accessToken, videoFPathBase64 string, now time.Time) string {
	return newScopedResourceTicket(
		accessToken,
		resourceHLSStreamContext,
		videoFPathBase64,
		resourceHLSStreamTicketTTL,
		now,
	)
}

// IssueHLSPlaylistTicket adds a short-lived, path-bound capability only when
// this request was authenticated by the management header or same-origin
// cookie. A playlist ticket can therefore never renew itself.
func IssueHLSPlaylistTicket(context *gin.Context, videoFPathBase64 string) {
	if !context.GetBool(hlsPlaylistTicketIssuableKey) {
		return
	}
	accessToken := context.GetString(hlsValidatedAccessTokenKey)
	if accessToken == "" || videoFPathBase64 == "" {
		return
	}
	context.Header(
		HLSPlaylistTicketHeader,
		newHLSPlaylistTicket(accessToken, videoFPathBase64, time.Now()),
	)
}

func newHLSPlaylistTicket(accessToken, videoFPathBase64 string, now time.Time) string {
	return newScopedResourceTicket(
		accessToken,
		resourceHLSPlaylistContext,
		videoFPathBase64,
		resourceHLSPlaylistTicketTTL,
		now,
	)
}

func newScopedResourceTicket(accessToken, context, scope string, ttl time.Duration, now time.Time) string {
	expiresAt := strconv.FormatInt(now.Add(ttl).Unix(), 10)
	signature := scopedResourceTicketSignature(accessToken, context, scope, expiresAt)
	return expiresAt + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func validResourceTicket(ticket, accessToken string, now time.Time) bool {
	if ticket == "" || accessToken == "" || len(ticket) > resourceAuthTicketMaxLength {
		return false
	}
	expiresAt, encodedSignature, found := splitResourceTicket(ticket)
	if !found || expiresAt == "" || encodedSignature == "" || strings.Contains(encodedSignature, ".") {
		return false
	}
	expiresUnix, err := strconv.ParseInt(expiresAt, 10, 64)
	if err != nil || now.Unix() > expiresUnix {
		return false
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil {
		return false
	}
	expectedSignature := resourceTicketSignature(accessToken, expiresAt)
	return subtle.ConstantTimeCompare(providedSignature, expectedSignature) == 1
}

func resourceTicketSignature(accessToken, expiresAt string) []byte {
	mac := hmac.New(sha256.New, []byte(accessToken))
	_, _ = mac.Write([]byte(resourceAuthContext))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(expiresAt))
	return mac.Sum(nil)
}

func validHLSStreamTicket(ticket, accessToken, videoFPathBase64 string, now time.Time) bool {
	return validScopedResourceTicket(ticket, accessToken, resourceHLSStreamContext, videoFPathBase64, now)
}

func validHLSPlaylistTicket(ticket, accessToken, videoFPathBase64 string, now time.Time) bool {
	return validScopedResourceTicket(ticket, accessToken, resourceHLSPlaylistContext, videoFPathBase64, now)
}

func validScopedResourceTicket(ticket, accessToken, context, scope string, now time.Time) bool {
	if ticket == "" || accessToken == "" || scope == "" || len(ticket) > resourceAuthTicketMaxLength {
		return false
	}
	expiresAt, encodedSignature, found := splitResourceTicket(ticket)
	if !found || expiresAt == "" || encodedSignature == "" || strings.Contains(encodedSignature, ".") {
		return false
	}
	expiresUnix, err := strconv.ParseInt(expiresAt, 10, 64)
	if err != nil || now.Unix() > expiresUnix {
		return false
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil {
		return false
	}
	expectedSignature := scopedResourceTicketSignature(accessToken, context, scope, expiresAt)
	return subtle.ConstantTimeCompare(providedSignature, expectedSignature) == 1
}

func splitResourceTicket(ticket string) (string, string, bool) {
	separator := strings.IndexByte(ticket, '.')
	if separator < 0 {
		return "", "", false
	}
	return ticket[:separator], ticket[separator+1:], true
}

func scopedResourceTicketSignature(accessToken, context, scope, expiresAt string) []byte {
	mac := hmac.New(sha256.New, []byte(accessToken))
	_, _ = mac.Write([]byte(context))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(scope))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(expiresAt))
	return mac.Sum(nil)
}

func issueResourceCredentials(context *gin.Context, accessToken string, now time.Time) {
	setResourceAuthCookie(context, resourceCookieValue(accessToken))
	context.Header(ResourceAuthTicketHeader, newResourceTicket(accessToken, now))
	protectResourceResponse(context)
}

func protectResourceResponse(context *gin.Context) {
	context.Header("Cache-Control", "private, no-store")
	context.Header("Referrer-Policy", "no-referrer")
}

func setResourceAuthCookie(context *gin.Context, value string) {
	secure := resourceCookieSecure(context)
	context.SetSameSite(http.SameSiteStrictMode)
	context.SetCookie(resourceAuthCookieName, value, 0, "/", "", secure, true)
}

func clearResourceAuthCookie(context *gin.Context) {
	secure := resourceCookieSecure(context)
	context.SetSameSite(http.SameSiteStrictMode)
	context.SetCookie(resourceAuthCookieName, "", -1, "/", "", secure, true)
}

func resourceCookieSecure(context *gin.Context) bool {
	return context.Request.TLS != nil || strings.EqualFold(context.GetHeader("X-Forwarded-Proto"), "https")
}

func rejectResourceAuth(context *gin.Context) {
	context.JSON(http.StatusUnauthorized, backendTypes.ReplyCheckAuth{Message: "resource authentication required"})
	context.Abort()
}
