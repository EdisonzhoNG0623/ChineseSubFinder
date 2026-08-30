package task_queue

import (
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

func TestSearchEvidenceFingerprintIsPathFreeAndTracksIdentity(t *testing.T) {
	job := OneJob{
		VideoType: common.Anime, VideoFPath: "/private/media/Anime/episode.mkv",
		SeriesName: "Fairy Tail", Season: 8, Episode: 11, AbsoluteEpisode: 288,
		SceneSeason: 8, SceneEpisode: 10, NumberingSource: "Anime-Lists",
	}
	first := job.RefreshSearchFingerprint()
	if first == "" || job.SearchEvidenceVersion != SearchEvidenceVersion || strings.Contains(first, "private") || strings.Contains(first, "Fairy") {
		t.Fatalf("unsafe or empty fingerprint: %q", first)
	}
	job.VideoFPath = "/different/root/episode.mkv"
	if got := job.RefreshSearchFingerprint(); got != first {
		t.Fatalf("path changed fingerprint: %q != %q", got, first)
	}
	job.SearchAliases = []string{"FAIRY TAIL", "フェアリーテイル"}
	aliasFingerprint := job.RefreshSearchFingerprint()
	if aliasFingerprint == first {
		t.Fatal("query alias addition did not change fingerprint")
	}
	job.SearchAliases = []string{"FAIRY   TAIL", "fairy tail", "フェアリーテイル"}
	if got := job.RefreshSearchFingerprint(); got != aliasFingerprint {
		t.Fatalf("equivalent alias normalization changed fingerprint: %q != %q", got, aliasFingerprint)
	}
	first = aliasFingerprint
	job.AbsoluteEpisode++
	if got := job.RefreshSearchFingerprint(); got == first {
		t.Fatal("identity correction did not change fingerprint")
	}
}

func TestNormalizeSearchAliasesPreservesFallbackOrder(t *testing.T) {
	got := NormalizeSearchAliases(" Fairy   Tail ", "fairy tail", "フェアリーテイル", "")
	if len(got) != 2 || got[0] != "Fairy Tail" || got[1] != "フェアリーテイル" {
		t.Fatalf("normalized aliases = %#v", got)
	}
}

func TestRefreshSearchFingerprintSkipsMoviesAndIncompleteEpisodes(t *testing.T) {
	for _, job := range []OneJob{
		{VideoType: common.Movie, Season: 1, Episode: 1},
		{VideoType: common.Series, Season: 1},
	} {
		if got := job.RefreshSearchFingerprint(); got != "" {
			t.Fatalf("unexpected fingerprint for incomplete identity: %q", got)
		}
	}
}
