package sub_helper

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
)

func TestResolveOrganizedSubtitleEpisodeUsesAbsoluteMapping(t *testing.T) {
	season, episode, ok := resolveOrganizedSubtitleEpisode("Fairy.Tail.288.CHS.ass", supplier.SubInfo{
		Season: 8, Episode: 11, AbsoluteEpisode: 288,
	})
	if !ok || season != 8 || episode != 11 {
		t.Fatalf("resolved (%d,%d,%v), want (8,11,true)", season, episode, ok)
	}
}

func TestResolveOrganizedSubtitleEpisodeKeepsAiredNumbering(t *testing.T) {
	season, episode, ok := resolveOrganizedSubtitleEpisode("Show.S02E03.srt", supplier.SubInfo{})
	if !ok || season != 2 || episode != 3 {
		t.Fatalf("resolved (%d,%d,%v), want (2,3,true)", season, episode, ok)
	}
}

func TestSeriesEpisodeResolverMapsCollectionNamingConventions(t *testing.T) {
	resolver := newSeriesEpisodeResolver(&series.SeriesInfo{EpList: []series.EpisodeInfo{
		{Season: 4, Episode: 35, AbsoluteEpisode: 116},
		{Season: 4, Episode: 36, AbsoluteEpisode: 117},
	}})
	source := supplier.SubInfo{Season: 4, Episode: 0, IsFullSeason: true}

	tests := []struct {
		name        string
		path        string
		wantEpisode int
	}{
		{name: "language underscore", path: "简_35.ass", wantEpisode: 35},
		{name: "bracket episode", path: "[36] 繁体.ass", wantEpisode: 36},
		{name: "episode token", path: "家庭教师 EP035.chs.ass", wantEpisode: 35},
		{name: "chinese episode", path: "家庭教师 第36集.ass", wantEpisode: 36},
		{name: "chinese numeral episode", path: "第四季/第三十六集.ass", wantEpisode: 36},
		{name: "full width episode", path: "家庭教师 ＥＰ０３５.ass", wantEpisode: 35},
		{name: "season directory", path: "Season 04/36.ass", wantEpisode: 36},
		{name: "year before episode", path: "家庭教师.2023.36.chs.ass", wantEpisode: 36},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			season, episode, ok := resolver.Resolve(test.path, source)
			if !ok || season != 4 || episode != test.wantEpisode {
				t.Fatalf("Resolve(%q) = (%d,%d,%v), want (4,%d,true)", test.path, season, episode, ok, test.wantEpisode)
			}
		})
	}
}

func TestSeriesEpisodeResolverMapsUniqueAbsoluteNumber(t *testing.T) {
	resolver := newSeriesEpisodeResolver(&series.SeriesInfo{EpList: []series.EpisodeInfo{
		{Season: 4, Episode: 35, AbsoluteEpisode: 116},
	}})
	season, episode, ok := resolver.Resolve("116.ass", supplier.SubInfo{Season: 4, Episode: 0, IsFullSeason: true})
	if !ok || season != 4 || episode != 35 {
		t.Fatalf("absolute resolution = (%d,%d,%v), want (4,35,true)", season, episode, ok)
	}
}

func TestSeriesEpisodeResolverRejectsAmbiguousAiredAndAbsoluteNumber(t *testing.T) {
	resolver := newSeriesEpisodeResolver(&series.SeriesInfo{EpList: []series.EpisodeInfo{
		{Season: 4, Episode: 35, AbsoluteEpisode: 116},
		{Season: 4, Episode: 116},
	}})
	if season, episode, ok := resolver.Resolve("116.ass", supplier.SubInfo{Season: 4, Episode: 0, IsFullSeason: true}); ok {
		t.Fatalf("ambiguous number unexpectedly resolved to S%dE%d", season, episode)
	}
}

func TestSeriesEpisodeResolverUsesExactSourceForUnnumberedSingleEpisodeArchive(t *testing.T) {
	resolver := newSeriesEpisodeResolver(&series.SeriesInfo{EpList: []series.EpisodeInfo{
		{Season: 4, Episode: 35},
	}})
	season, episode, ok := resolver.Resolve("简体字幕.ass", supplier.SubInfo{Season: 4, Episode: 35})
	if !ok || season != 4 || episode != 35 {
		t.Fatalf("single episode fallback = (%d,%d,%v), want (4,35,true)", season, episode, ok)
	}
}

func TestSeriesEpisodeResolverRejectsUnscopedOrUnknownBareNumbers(t *testing.T) {
	resolver := newSeriesEpisodeResolver(&series.SeriesInfo{EpList: []series.EpisodeInfo{
		{Season: 1, Episode: 8},
	}})
	for _, test := range []struct {
		name   string
		path   string
		source supplier.SubInfo
	}{
		{name: "resolution is not episode", path: "Show.1080p.ass", source: supplier.SubInfo{Season: 1, Episode: 0, IsFullSeason: true}},
		{name: "bare number requires collection scope", path: "08.ass", source: supplier.SubInfo{Season: -1, Episode: -1}},
		{name: "episode absent from inventory", path: "09.ass", source: supplier.SubInfo{Season: 1, Episode: 0, IsFullSeason: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if season, episode, ok := resolver.Resolve(test.path, test.source); ok {
				t.Fatalf("Resolve(%q) unexpectedly matched S%dE%d", test.path, season, episode)
			}
		})
	}
}

func TestAddFrontNameUsesGlobalMatchRank(t *testing.T) {
	info := supplier.SubInfo{FromWhere: "subdl", TopN: 7, MatchRank: 2}
	if got := AddFrontName(info, "episode.srt"); got != "[subdl]_2_episode.srt" {
		t.Fatalf("unexpected ranked name: %s", got)
	}
}
