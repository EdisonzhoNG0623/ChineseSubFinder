package downloader

import (
	"reflect"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestBuildSeriesEpisodeMapDeduplicatesAndSorts(t *testing.T) {
	got := buildSeriesEpisodeMap([]taskQueueTypes.OneJob{
		{Season: 1, Episode: 4}, {Season: 1, Episode: 2}, {Season: 1, Episode: 4}, {Season: 2, Episode: 1},
	})
	want := map[int][]int{1: {2, 4}, 2: {1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("episode map = %#v, want %#v", got, want)
	}
}

func TestSeriesBatchLimitGrowsForRepeatedHistoricalSearches(t *testing.T) {
	for _, test := range []struct {
		attempts int
		want     int
	}{{0, 4}, {1, 8}, {2, 8}, {3, 12}, {10, 12}} {
		if got := seriesBatchLimit(taskQueueTypes.OneJob{DownloadTimes: test.attempts}); got != test.want {
			t.Fatalf("attempts=%d batch=%d want=%d", test.attempts, got, test.want)
		}
	}
}

func TestSeriesSearchFingerprintIsPathFreeAndTracksIdentity(t *testing.T) {
	job := taskQueueTypes.OneJob{VideoFPath: "/media/private/Show/S01E01.mkv", Season: 1, Episode: 1}
	identity := seriesIdentity{seriesName: "Example Show", aliases: []string{"Original Show"}, absoluteEpisode: 1, numberingSource: "Anime-Lists"}
	first := seriesSearchFingerprint(job, identity)
	job.VideoFPath = "/different/private/path.mkv"
	if got := seriesSearchFingerprint(job, identity); got != first {
		t.Fatalf("path changed path-free fingerprint: %s != %s", got, first)
	}
	identity.absoluteEpisode = 13
	if got := seriesSearchFingerprint(job, identity); got == first {
		t.Fatal("identity change did not change fingerprint")
	}
	identity.absoluteEpisode = 1
	identity.aliases = append(identity.aliases, "新别名")
	if got := seriesSearchFingerprint(job, identity); got == first {
		t.Fatal("search alias change did not change fingerprint")
	}
}

func TestEnrichSeriesBatchCarriesIdentityWithoutPrematureAttemptStamp(t *testing.T) {
	job := taskQueueTypes.OneJob{VideoType: common.Series, VideoFPath: "/media/Show/S01E01.mkv", Season: 1, Episode: 1}
	identity := seriesIdentity{seriesName: "Example Show", aliases: []string{"Original Show"}, absoluteEpisode: 13, numberingSource: "anime-lists", numberingConfidence: 0.9}
	enriched := enrichSeriesBatchJobs([]taskQueueTypes.OneJob{job}, map[string]seriesIdentity{"S1E1": identity})
	if len(enriched) != 1 || enriched[0].SearchFingerprint == "" ||
		enriched[0].LastAttemptSearchFingerprint != "" || len(enriched[0].SearchAliases) != 1 {
		t.Fatalf("enriched identity was missing or prematurely stamped: %+v", enriched)
	}
}

func TestMergeSeriesSearchAliasesIncludesRemoteSupplierTitles(t *testing.T) {
	info := &series.SeriesInfo{Name: "Local Show", Aliases: []string{"Local AKA"}}
	mergeSeriesSearchAliases(info, "中文名", "English Name", "Original Name", "local show")
	want := []string{"Local Show", "Local AKA", "中文名", "English Name", "Original Name"}
	if !reflect.DeepEqual(info.Aliases, want) {
		t.Fatalf("merged aliases = %#v, want %#v", info.Aliases, want)
	}
}
