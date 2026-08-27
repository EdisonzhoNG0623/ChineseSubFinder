package addic7ed

import (
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/language"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/PuerkitoBio/goquery"
)

func testDocument(t *testing.T, html string) *goquery.Document {
	t.Helper()
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestResolveShowParsersRequireExactTitle(t *testing.T) {
	search := testDocument(t, `<a href="serie/The_100/7/7/The_Queen%27s_Gambit">The 100 - 07x07 - The Queen's Gambit</a><a href="serie/The_Queen%27s_Gambit/1/1/Openings">The Queen's Gambit - 01x01 - Openings</a>`)
	path := findEpisodePath(search, "The Queen's Gambit")
	if path != "serie/The_Queen%27s_Gambit/1/1/Openings" {
		t.Fatalf("episode path = %q", path)
	}
	detail := testDocument(t, `<a href="/show/8107">The Queen's Gambit</a><a href="/show/9">Other</a>`)
	if got := parseShowID(detail, "The Queen's Gambit"); got != 8107 {
		t.Fatalf("show id = %d", got)
	}
}

func TestParseCandidatesAndRankVersion(t *testing.T) {
	html := `<table id="season"><tbody>
<tr class="completed"><td>1</td><td>3</td><td><a href="/serie/show/1/3/title">Episode</a></td><td>Chinese (Traditional)</td><td>WEB</td><td>100%</td><td></td><td></td><td></td><td><a href="/updated/24/1/3">Download</a></td></tr>
<tr class="completed"><td>1</td><td>3</td><td><a href="/serie/show/1/3/title">Episode</a></td><td>Chinese (Simplified)</td><td>NTb WEB-DL</td><td>100%</td><td></td><td></td><td></td><td><a href="/updated/41/1/3">Download</a></td></tr>
</tbody></table>`
	items := parseCandidates(testDocument(t, html))
	ranked := rankCandidates(items, series.EpisodeInfo{Season: 1, Episode: 3}, "Show.S01E03.NTb.WEB-DL.mkv")
	if len(ranked) != 2 || ranked[0].Language != language.ChineseSimple || ranked[0].Version != "NTb WEB-DL" {
		t.Fatalf("unexpected candidates: %+v", ranked)
	}
}

func TestSafeURLRejectsCrossHost(t *testing.T) {
	settings.SetConfigRootPath(t.TempDir())
	settings.Get().SubtitleSources.Addic7edSettings.Enabled = true
	s := &Supplier{baseURL: "https://www.addic7ed.com"}
	if _, err := s.safeURL("https://evil.example/subtitle.srt"); err == nil {
		t.Fatal("cross-host URL was accepted")
	}
	if _, err := s.safeURL("https://www.addic7ed.com:8443/subtitle.srt"); err == nil {
		t.Fatal("cross-port URL was accepted")
	}
	if got, err := s.safeURL("/updated/41/1/3"); err != nil || got != "https://www.addic7ed.com/updated/41/1/3" {
		t.Fatalf("same-host URL = %q, %v", got, err)
	}
}

func TestLooksLikeHTML(t *testing.T) {
	if !looksLikeHTML([]byte(" <!doctype html><title>blocked</title>")) || looksLikeHTML([]byte("1\n00:00:01,000 --> 00:00:02,000")) {
		t.Fatal("HTML response detection failed")
	}
}

func TestLooksLikeSubtitle(t *testing.T) {
	if !looksLikeSubtitle([]byte("1\n00:00:01,000 --> 00:00:02,000\nText")) || looksLikeSubtitle([]byte("download limit reached")) {
		t.Fatal("SRT response validation failed")
	}
}
