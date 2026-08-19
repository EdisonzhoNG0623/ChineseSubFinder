package subdl

import (
	"encoding/json"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
)

func TestAPIResponseDecodesMixedScalarTypes(t *testing.T) {
	payload := []byte(`{
		"status": true,
		"results": [{"sd_id": 7, "tmdb_id": 1399, "imdb_id": "tt0944947", "year": "2011"}],
		"subtitles": [{
			"id": "sub-1", "lang": "ZH", "url": "/subtitle/file.zip",
			"season": "1", "episode": 2, "full_season": 1,
			"unpack_files": [{"name": "Show.S01E02.zh.srt", "season_number": "1", "episode_number": 2}]
		}]
	}`)
	var response apiResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if string(response.Results[0].TMDBID) != "1399" || int(response.Results[0].Year) != 2011 {
		t.Fatalf("unexpected media result: %+v", response.Results[0])
	}
	got := response.Subtitles[0]
	if !bool(got.FullSeason) || int(got.Season) != 1 || int(got.UnpackFiles[0].EpisodeNumber) != 2 {
		t.Fatalf("unexpected subtitle: %+v", got)
	}
}

func TestStrictMediaQueryRequiresStableIdentity(t *testing.T) {
	if _, ok := strictMediaQuery(&models.MediaInfo{TitleCn: "同名影片"}, true, 0, 0); ok {
		t.Fatal("title-only media must not be queried")
	}
	params, ok := strictMediaQuery(&models.MediaInfo{TmdbId: "1399", ImdbId: "tt0944947"}, false, 3, 4)
	if !ok {
		t.Fatal("TMDB identity was rejected")
	}
	if params["tmdb_id"] != "1399" || params["type"] != "tv" || params["season_number"] != "3" || params["episode_number"] != "4" {
		t.Fatalf("unexpected query: %+v", params)
	}
}

func TestResponseMatchesOnlyRequestedIdentity(t *testing.T) {
	response := &apiResponse{Status: true, Results: []mediaResult{{TMDBID: "1399", IMDbID: "tt0944947"}}}
	if !responseMatchesMedia(response, &models.MediaInfo{TmdbId: "1399"}) {
		t.Fatal("matching TMDB ID was rejected")
	}
	if !responseMatchesMedia(response, &models.MediaInfo{ImdbId: "0944947"}) {
		t.Fatal("matching IMDb ID was rejected")
	}
	if responseMatchesMedia(response, &models.MediaInfo{TmdbId: "1400", ImdbId: "tt0000001"}) {
		t.Fatal("mismatched identity was accepted")
	}
}

func TestSelectCandidatesPrefersCoveringFullSeasonAndRejectsWrongEpisodes(t *testing.T) {
	items := []subtitleItem{
		{ID: "wrong-language", Lang: "EN", URL: "/subtitle/en.zip", Season: 1, Episode: 2},
		{ID: "wrong-episode", Lang: "ZH", URL: "/subtitle/e03.zip", Season: 1, Episode: 3},
		{ID: "exact", Lang: "ZH", URL: "/subtitle/e02.zip", ReleaseName: "Show.S01E02", Season: 1, Episode: 2},
		{ID: "collection", Language: "Chinese", URL: "/subtitle/s01.zip", ReleaseName: "Show Season 1", FullSeason: true,
			UnpackFiles: []unpackFile{{Name: "Show.S01E02.zh.srt", SeasonNumber: 1, EpisodeNumber: 2}}},
		{ID: "collection-without-unpack", Lang: "ZH", URL: "/subtitle/s01-alt.zip", Season: 1, FullSeason: true},
		{ID: "unrelated-collection", Lang: "ZH", URL: "/subtitle/s02.zip", Season: 2, FullSeason: true,
			UnpackFiles: []unpackFile{{Name: "Show.S02E02.zh.srt", SeasonNumber: 2, EpisodeNumber: 2}}},
	}
	selected := selectCandidates(items, false, 1, 2)
	if len(selected) != 3 {
		t.Fatalf("selected %d candidates, want 3: %+v", len(selected), selected)
	}
	seasonPacks := map[string]bool{string(selected[0].ID): true, string(selected[1].ID): true}
	if !seasonPacks["collection"] || !seasonPacks["collection-without-unpack"] || string(selected[2].ID) != "exact" {
		t.Fatalf("unexpected candidate order: %+v", selected)
	}
}

func TestSelectCandidatesAcceptsAbsoluteEpisodeFallback(t *testing.T) {
	items := []subtitleItem{
		{ID: "absolute", Lang: "ZH", URL: "/subtitle/absolute.zip", ReleaseName: "Fairy.Tail.288.CHS"},
		{ID: "wrong", Lang: "ZH", URL: "/subtitle/wrong.zip", ReleaseName: "Fairy.Tail.289.CHS"},
	}
	selected := selectCandidates(items, false, 8, 11, 288)
	if len(selected) != 1 || string(selected[0].ID) != "absolute" {
		t.Fatalf("absolute candidates = %#v, want only absolute", selected)
	}
}

func TestSafeDownloadURLAllowsOnlySubDLDownloadHost(t *testing.T) {
	got, err := safeDownloadURL("/subtitle/file.zip")
	if err != nil || got != "https://dl.subdl.com/subtitle/file.zip" {
		t.Fatalf("safeDownloadURL() = %q, %v", got, err)
	}
	for _, candidate := range []string{"http://dl.subdl.com/file.zip", "https://evil.example/file.zip", "https://dl.subdl.com.evil.example/file.zip"} {
		if _, err = safeDownloadURL(candidate); err == nil {
			t.Fatalf("unsafe URL %q was accepted", candidate)
		}
	}
}

func TestCredentialFreeURL(t *testing.T) {
	got := credentialFreeURL("https://dl.subdl.com/subtitle/123.zip?api_key=secret#fragment")
	if got != "https://dl.subdl.com/subtitle/123.zip" {
		t.Fatalf("credentialFreeURL() = %q", got)
	}
}

func TestResponseFileName(t *testing.T) {
	got := responseFileName("attachment; filename*=UTF-8''fight-club_chinese-bg-code-2033837.zip", "https://dl.subdl.com/subtitle/123.zip?api_key=secret")
	if got != "fight-club_chinese-bg-code-2033837.zip" {
		t.Fatalf("responseFileName() = %q", got)
	}
}
