package subsource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
)

func TestAPIModelsAcceptStringAndNumericIDs(t *testing.T) {
	var titles titleSearchResponse
	if err := json.Unmarshal([]byte(`{"data":[{"movieId":"42","title":"Show"}]}`), &titles); err != nil {
		t.Fatal(err)
	}
	if int64(titles.Data[0].MovieID) != 42 {
		t.Fatalf("movie ID = %d", titles.Data[0].MovieID)
	}
	var subtitles subtitleSearchResponse
	if err := json.Unmarshal([]byte(`{"success":true,"data":[{"subtitleId":7,"language":"Chinese BG code","releaseInfo":["Show.S01E02"],"seasonNumber":"1","episodeNumber":2}]}`), &subtitles); err != nil {
		t.Fatal(err)
	}
	if !subtitles.SuccessPresent || !subtitles.Success || int(subtitles.Data[0].EpisodeNumber) != 2 {
		t.Fatalf("unexpected response: %+v", subtitles)
	}
}

func TestAPIKeyUsesHeaderNotQueryString(t *testing.T) {
	settings.SetConfigRootPath(t.TempDir())
	settings.Get().SubtitleSources.SubSourceSettings = settings.SubSourceSettings{Enabled: true, APIKey: "subsource-secret"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.RawQuery, "secret") || request.Header.Get("X-API-Key") != "subsource-secret" {
			t.Error("SubSource credential was not isolated to X-API-Key")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"movieId":42,"title":"Fight Club"}]}`))
	}))
	defer server.Close()

	supplier := &Supplier{baseURL: server.URL + "/"}
	id, err := supplier.searchTitles(context.Background(), "tt0137523", nil, 0, 0)
	if err != nil || id != 42 {
		t.Fatalf("searchTitles() = %d, %v", id, err)
	}
}

func TestSelectCandidatesDistinguishesExactAndSeasonPack(t *testing.T) {
	items := []subtitleItem{
		{SubtitleID: 1, Language: "Chinese BG code", ReleaseInfo: []string{"Show.S01E03"}},
		{SubtitleID: 2, Language: "Chinese BG code", ReleaseInfo: []string{"Show.S01E02"}},
		{SubtitleID: 3, Language: "Chinese BG code", ReleaseInfo: []string{"Show.S01.Complete"}, SeasonNumber: 1},
	}
	selected := selectCandidates(items, 1, 2, 0)
	if len(selected) != 2 || int64(selected[0].SubtitleID) != 2 || int64(selected[1].SubtitleID) != 3 {
		t.Fatalf("unexpected candidates: %+v", selected)
	}
}

func TestSelectAbsoluteCandidate(t *testing.T) {
	items := []subtitleItem{
		{SubtitleID: 1, ReleaseInfo: []string{"Fairy.Tail.288.CHS"}},
		{SubtitleID: 2, ReleaseInfo: []string{"Fairy.Tail.289.CHS"}},
	}
	selected := selectAbsoluteCandidates(items, 288)
	if len(selected) != 1 || int64(selected[0].SubtitleID) != 1 {
		t.Fatalf("unexpected absolute candidates: %+v", selected)
	}
}

func TestResponseFileNameSanitizesPath(t *testing.T) {
	if got := responseFileName(`attachment; filename="../../season.zip"`, 9); got != "season.zip" {
		t.Fatalf("responseFileName() = %q", got)
	}
}

func TestNormalizeTitleSupportsSafeTextFallback(t *testing.T) {
	if normalizeTitle(" Frieren: Beyond Journey's End ") != normalizeTitle("Frieren Beyond Journeys End") {
		t.Fatal("equivalent titles did not normalize equally")
	}
	if normalizeTitle("葬送的芙莉莲") == "" {
		t.Fatal("CJK title was discarded")
	}
}
