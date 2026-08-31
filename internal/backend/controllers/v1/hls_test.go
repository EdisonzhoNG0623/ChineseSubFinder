package v1

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/middle"
	"github.com/gin-gonic/gin"
)

func TestHLSSegmentURLTemplatePropagatesEncodedResourceTicket(t *testing.T) {
	const ticket = "ticket.with + reserved&characters?"
	template := hlsSegmentURLTemplate("encoded-video", ticket)
	parts := strings.SplitN(template, "?", 2)
	if len(parts) != 2 || parts[0] != "../segments/{{.Resolution}}/{{.Segment}}/encoded-video" {
		t.Fatalf("unexpected HLS segment template %q", template)
	}
	query, err := url.ParseQuery(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if got := query.Get(middle.HLSStreamTicketQueryParam); got != ticket {
		t.Fatalf("propagated ticket = %q, want %q", got, ticket)
	}
	if withoutTicket := hlsSegmentURLTemplate("encoded-video", ""); strings.Contains(withoutTicket, "?") {
		t.Fatalf("empty ticket added a query string: %q", withoutTicket)
	}

	playlistURL, err := url.Parse("https://example.test/reverse-proxy/v1/preview/playlist/encoded-video")
	if err != nil {
		t.Fatal(err)
	}
	segmentReference, err := url.Parse(strings.ReplaceAll(strings.ReplaceAll(template, "{{.Resolution}}", "720"), "{{.Segment}}", "0"))
	if err != nil {
		t.Fatal(err)
	}
	resolved := playlistURL.ResolveReference(segmentReference)
	if resolved.Path != "/reverse-proxy/v1/preview/segments/720/0/encoded-video" {
		t.Fatalf("resolved segment path = %q", resolved.Path)
	}
}

func TestParseHLSSegmentParametersRestrictsGeneratedShape(t *testing.T) {
	segment, resolution, err := parseHLSSegmentParameters("12", "720")
	if err != nil || segment != 12 || resolution != 720 {
		t.Fatalf("valid segment parameters = %d, %d, %v", segment, resolution, err)
	}
	for _, testCase := range []struct {
		segment    string
		resolution string
	}{
		{segment: "-1", resolution: "720"},
		{segment: "0x10", resolution: "720"},
		{segment: "0", resolution: "1080"},
		{segment: "0", resolution: "-720"},
	} {
		if _, _, err = parseHLSSegmentParameters(testCase.segment, testCase.resolution); err == nil {
			t.Fatalf("accepted invalid segment parameters %+v", testCase)
		}
	}
}

func TestHLSContentTypesAreNativePlayerCompatible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, contentType := range map[string]string{
		"playlist": hlsPlaylistContentType,
		"segment":  hlsSegmentContentType,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			if name == "playlist" {
				setHLSPlaylistContentType(context)
			} else {
				setHLSSegmentContentType(context)
			}
			context.Status(http.StatusOK)
			if got := recorder.Header().Get("Content-Type"); got != contentType {
				t.Fatalf("Content-Type = %q, want %q", got, contentType)
			}
		})
	}
}
