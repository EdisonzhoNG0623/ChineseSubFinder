package mark_system

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/subparser"
)

func TestOrderByGlobalRankUsesSitePreferenceOnlyAsTieBreaker(t *testing.T) {
	marker := MarkingSystem{subSiteSequence: []string{"preferred", "other"}}
	got := marker.orderByGlobalRank(map[string][]subparser.FileInfo{
		"preferred": {{FromWhereSite: "preferred", FileFullPath: "/tmp/[preferred]_3_a.srt"}},
		"other": {
			{FromWhereSite: "other", FileFullPath: "/tmp/[other]_1_b.srt"},
			{FromWhereSite: "other", FileFullPath: "/tmp/[other]_3_c.srt"},
		},
	})
	if len(got) != 3 || got[0].FromWhereSite != "other" || got[1].FromWhereSite != "preferred" {
		t.Fatalf("unexpected global order: %#v", got)
	}
}

func TestGlobalRankRejectsLegacyOrMalformedPrefix(t *testing.T) {
	if got := globalRank("/tmp/[subdl]_12_name.srt"); got != 12 {
		t.Fatalf("got rank %d", got)
	}
	if got := globalRank("/tmp/no-rank.srt"); got != 1<<62-1 {
		t.Fatalf("malformed rank should sort last, got %d", got)
	}
}
