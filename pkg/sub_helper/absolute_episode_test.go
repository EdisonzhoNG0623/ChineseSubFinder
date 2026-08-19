package sub_helper

import (
	"testing"

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

func TestAddFrontNameUsesGlobalMatchRank(t *testing.T) {
	info := supplier.SubInfo{FromWhere: "subdl", TopN: 7, MatchRank: 2}
	if got := AddFrontName(info, "episode.srt"); got != "[subdl]_2_episode.srt" {
		t.Fatalf("unexpected ranked name: %s", got)
	}
}
