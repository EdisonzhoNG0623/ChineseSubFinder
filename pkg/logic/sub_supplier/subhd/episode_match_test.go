package subhd

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/sirupsen/logrus"
)

func TestWhichEpisodeNeedDownloadSubMatchesAbsoluteEpisode(t *testing.T) {
	supplier := &Supplier{log: logrus.New()}
	seriesInfo := &series.SeriesInfo{NeedDlEpsKeyList: map[string]series.EpisodeInfo{
		"S8E11": {Season: 8, Episode: 11, AbsoluteEpisode: 288},
	}}
	items := []HdListItem{
		{Title: "Fairy Tail - 288 WEB.ass", Url: "/a/low", DownCount: 10},
		{Title: "Fairy Tail EP288 1080p.ass", Url: "/a/high", DownCount: 100},
		{Title: "Fairy Tail EP289 1080p.ass", Url: "/a/wrong", DownCount: 1000},
	}

	got := supplier.whichEpisodeNeedDownloadSub(seriesInfo, items)
	if len(got) != 1 {
		t.Fatalf("matched items = %#v, want one", got)
	}
	if got[0].Url != "/a/high" || got[0].Season != 8 || got[0].Episode != 11 {
		t.Fatalf("absolute match = %#v", got[0])
	}
}

func TestWhichEpisodeNeedDownloadSubPrefersExactAiredEpisode(t *testing.T) {
	supplier := &Supplier{log: logrus.New()}
	seriesInfo := &series.SeriesInfo{NeedDlEpsKeyList: map[string]series.EpisodeInfo{
		"S2E6": {Season: 2, Episode: 6, AbsoluteEpisode: 16},
	}}
	items := []HdListItem{
		{Title: "The Expanse S02E06.ass", Url: "/a/aired", DownCount: 1},
		{Title: "The Expanse EP16.ass", Url: "/a/absolute", DownCount: 100},
	}

	got := supplier.whichEpisodeNeedDownloadSub(seriesInfo, items)
	if len(got) != 1 || got[0].Url != "/a/aired" {
		t.Fatalf("exact aired match was not preferred: %#v", got)
	}
}

func TestBestSubHDItemIsDeterministic(t *testing.T) {
	items := []HdListItem{
		{Title: "b", Url: "/b", DownCount: 5},
		{Title: "a", Url: "/a", DownCount: 5},
	}
	got, ok := bestSubHDItem(items, map[string]struct{}{})
	if !ok || got.Url != "/a" {
		t.Fatalf("bestSubHDItem() = %#v, %v", got, ok)
	}
}

func TestWhichEpisodeNeedDownloadSubSelectsChineseSeasonPackage(t *testing.T) {
	supplier := &Supplier{log: logrus.New()}
	seriesInfo := &series.SeriesInfo{
		NeedDlSeasonDict: map[int]int{4: 4},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{
			"S4E35": {Season: 4, Episode: 35},
		},
	}
	items := []HdListItem{
		{Title: "家庭教师 REBORN! 第四季 简繁字幕合集", Url: "/season-four", DownCount: 10},
		{Title: "家庭教师 REBORN! 第三季 简繁字幕合集", Url: "/wrong-season", DownCount: 100},
	}

	got := supplier.whichEpisodeNeedDownloadSub(seriesInfo, items)
	if len(got) != 1 || got[0].Url != "/season-four" || got[0].Season != 4 || got[0].Episode != 0 {
		t.Fatalf("season package match = %#v", got)
	}
}

func TestWhichEpisodeNeedDownloadSubUsesSearchSeasonHintForCollection(t *testing.T) {
	supplier := &Supplier{log: logrus.New()}
	seriesInfo := &series.SeriesInfo{
		NeedDlSeasonDict: map[int]int{4: 4},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{
			"S4E35": {Season: 4, Episode: 35},
		},
	}
	items := []HdListItem{
		{Title: "家庭教师 REBORN! 074-101 简繁合集", Url: "/collection", DownCount: 10, SeasonHint: 4},
	}

	got := supplier.whichEpisodeNeedDownloadSub(seriesInfo, items)
	if len(got) != 1 || got[0].Season != 4 || got[0].Episode != 0 {
		t.Fatalf("hinted collection match = %#v", got)
	}
}
