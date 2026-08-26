package subhd

import (
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/PuerkitoBio/goquery"
)

func TestSubHDSeriesAliasesDeduplicatesMetadata(t *testing.T) {
	aliases := subHDSeriesAliases(&models.MediaInfo{
		TitleCn: "妖精的尾巴", TitleEn: "Fairy Tail", OriginalTitle: "FAIRY TAIL",
	}, &series.SeriesInfo{Name: " 妖精的尾巴 "})
	if len(aliases) != 2 || aliases[0] != "妖精的尾巴" || aliases[1] != "Fairy Tail" {
		t.Fatalf("aliases = %#v, want Chinese and English aliases", aliases)
	}
}

func TestSubHDSeriesAliasesFallsBackToLocalName(t *testing.T) {
	aliases := subHDSeriesAliases(nil, &series.SeriesInfo{Name: "本地剧名"})
	if len(aliases) != 1 || aliases[0] != "本地剧名" {
		t.Fatalf("local aliases = %#v", aliases)
	}
}

func TestBuildSubHDSearchPlanIncludesSeasonAiredAndBareAbsolute(t *testing.T) {
	plan := buildSubHDSearchPlan([]string{"妖精的尾巴", "Fairy Tail"}, 8, []series.EpisodeInfo{{
		Season: 8, Episode: 11, AbsoluteEpisode: 288,
	}}, true)

	wanted := map[string]bool{
		"妖精的尾巴 第八季":         false,
		"Fairy Tail S08E11": false,
		"妖精的尾巴 288":         false,
		"Fairy Tail 288":    false,
	}
	for _, query := range plan {
		if _, ok := wanted[query.Keyword]; ok {
			wanted[query.Keyword] = true
		}
	}
	for query, found := range wanted {
		if !found {
			t.Fatalf("missing query %q in %#v", query, plan)
		}
	}
	if len(plan) > subHDMaxSearchQueries {
		t.Fatalf("query count = %d, limit = %d", len(plan), subHDMaxSearchQueries)
	}

	bareIndex, prefixedIndex := -1, -1
	for index, query := range plan {
		if query.Keyword == "妖精的尾巴 288" {
			bareIndex = index
		}
		if strings.Contains(query.Keyword, "E288") {
			prefixedIndex = index
		}
	}
	if bareIndex < 0 || prefixedIndex >= 0 && bareIndex > prefixedIndex {
		t.Fatalf("bare absolute query should precede prefixed variants: %#v", plan)
	}
}

func TestBuildSubHDSearchPlanOrdinarySeriesStaysSeasonFocused(t *testing.T) {
	plan := buildSubHDSearchPlan([]string{"The Expanse"}, 2, []series.EpisodeInfo{{
		Season: 2, Episode: 6, AbsoluteEpisode: 16,
	}}, false)
	for _, query := range plan {
		if query.Kind == "absolute" || strings.Contains(query.Keyword, "E06") {
			t.Fatalf("ordinary series plan unexpectedly contains anime episode fallback: %#v", plan)
		}
	}
}

func TestEpisodesForSeasonFiltersTargets(t *testing.T) {
	info := &series.SeriesInfo{NeedDlEpsKeyList: map[string]series.EpisodeInfo{
		"S1E1": {Season: 1, Episode: 1},
		"S2E1": {Season: 2, Episode: 1},
	}}
	got := episodesForSeason(info, 2)
	if len(got) != 1 || got[0].Season != 2 {
		t.Fatalf("episodesForSeason() = %#v", got)
	}
}

func TestSubHDTargetSeasonsFallsBackToEpisodeMap(t *testing.T) {
	info := &series.SeriesInfo{NeedDlEpsKeyList: map[string]series.EpisodeInfo{
		"S2E1": {Season: 2, Episode: 1},
		"S1E1": {Season: 1, Episode: 1},
	}}
	got := subHDTargetSeasons(info)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("subHDTargetSeasons() = %#v", got)
	}
}

func TestSubHDSearchResultAliasRejectsSubtitleKeywordCollision(t *testing.T) {
	if subHDSearchResultMatchesAlias("闪电侠 第四季 The Flash", "REBORN!") {
		t.Fatal("subtitle episode-title keyword collision matched the wrong series")
	}
	if !subHDSearchResultMatchesAlias("家庭教师HITMAN REBORN!", "家庭教师HITMAN REBORN! (2006)") {
		t.Fatal("year-suffixed local alias did not match the correct series")
	}
}

func TestBuildSubHDSearchPlanKeepsExpectedAlias(t *testing.T) {
	plan := buildSubHDSearchPlan([]string{"REBORN!"}, 4, nil, true)
	if len(plan) == 0 || plan[0].Alias != "REBORN!" {
		t.Fatalf("search plan alias = %#v", plan)
	}
}

func TestSubHDImageDetailURLAcceptsOnlyDetailLinks(t *testing.T) {
	for _, test := range []struct {
		name string
		html string
		want string
	}{
		{name: "detail", html: `<a href="/d/26952099"><img class="rounded-start" alt="The Flash"></a>`, want: "/d/26952099"},
		{name: "download", html: `<a href="/a/Nu12wz"><img class="rounded-start" alt="The Flash"></a>`},
		{name: "external", html: `<a href="https://example.com/d/1"><img class="rounded-start" alt="The Flash"></a>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(test.html))
			if err != nil {
				t.Fatal(err)
			}
			if got := subHDImageDetailURL(doc.Find("img")); got != test.want {
				t.Fatalf("detail URL = %q, want %q", got, test.want)
			}
		})
	}
}
