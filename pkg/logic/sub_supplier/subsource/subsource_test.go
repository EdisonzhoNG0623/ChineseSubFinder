package subsource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
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
	if err := json.Unmarshal([]byte(`{"success":true,"data":[{"subtitleId":7,"language":"Chinese BG code","releaseInfo":["Show.S01E02"]}]}`), &subtitles); err != nil {
		t.Fatal(err)
	}
	if !subtitles.SuccessPresent || !subtitles.Success || int64(subtitles.Data[0].SubtitleID) != 7 {
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

func TestQueryChineseSubtitlesUsesSupportedCodesAndDeduplicates(t *testing.T) {
	settings.SetConfigRootPath(t.TempDir())
	settings.Get().AdvancedSettings.Topic = 2
	settings.Get().SubtitleSources.SubSourceSettings = settings.SubSourceSettings{Enabled: true, APIKey: "subsource-secret"}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		query := request.URL.Query()
		if query.Get("movieId") != "42" || query.Get("sort") != "popular" {
			t.Errorf("unexpected query: %s", request.URL.RawQuery)
		}
		if query.Has("seasonNumber") || query.Has("episodeNumber") {
			t.Errorf("unsupported episode filters were sent: %s", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch query.Get("language") {
		case "chinese_bg_code":
			_, _ = writer.Write([]byte(`{"success":true,"data":[{"subtitleId":7,"language":"Chinese BG code","releaseInfo":[]}]}`))
		case "chinese_bilingual":
			_, _ = writer.Write([]byte(`{"success":true,"data":[{"subtitleId":7,"language":"Chinese BG code"},{"subtitleId":8,"language":"Chinese Bilingual"}]}`))
		default:
			t.Errorf("unexpected language code: %q", query.Get("language"))
			_, _ = writer.Write([]byte(`{"success":true,"data":[]}`))
		}
	}))
	defer server.Close()

	supplier := &Supplier{baseURL: server.URL + "/"}
	items, err := supplier.queryChineseSubtitles(context.Background(), 42, 2)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(items) != 2 || int64(items[0].SubtitleID) != 7 || int64(items[1].SubtitleID) != 8 {
		t.Fatalf("requests=%d items=%+v", requests, items)
	}
}

func TestSelectSeriesCandidatesSeparatesEpisodesAndPacks(t *testing.T) {
	items := []subtitleItem{
		{SubtitleID: 1, ReleaseInfo: []string{"Show.S01E04.CHS"}},
		{SubtitleID: 2, ReleaseInfo: []string{"Show.S01E02.CHS"}},
		{SubtitleID: 3, ReleaseInfo: []string{"Show.S01.Complete"}},
	}
	episodes := []series.EpisodeInfo{{Season: 1, Episode: 2}, {Season: 1, Episode: 3}}
	selected := selectSeriesCandidates(items, 1, episodes)
	if len(selected) != 2 {
		t.Fatalf("selected = %+v", selected)
	}
	if int64(selected[0].item.SubtitleID) != 2 || selected[0].episode != 2 || selected[0].fullSeason {
		t.Fatalf("exact candidate = %+v", selected[0])
	}
	if int64(selected[1].item.SubtitleID) != 3 || !selected[1].fullSeason || selected[1].season != 1 {
		t.Fatalf("season candidate = %+v", selected[1])
	}
}

func TestSelectSeriesCandidatesMapsAbsoluteEpisode(t *testing.T) {
	items := []subtitleItem{{SubtitleID: 7, ReleaseInfo: []string{"Fairy.Tail.288.CHS"}}}
	episodes := []series.EpisodeInfo{{Season: 4, Episode: 11, AbsoluteEpisode: 288}}
	selected := selectSeriesCandidates(items, 4, episodes)
	if len(selected) != 1 || selected[0].episode != 11 || selected[0].absoluteEpisode != 288 || selected[0].fullSeason {
		t.Fatalf("absolute candidate = %+v", selected)
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
