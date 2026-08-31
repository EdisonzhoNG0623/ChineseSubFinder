package downloader

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
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

func TestBindSeriesInfoToClaimedJobsOverridesReverseScannedAlternateCut(t *testing.T) {
	targetPath := "/media/Show/target/Show S01E01.mkv"
	alternatePath := "/media/Show/alternate/Show S01E01.mkv"
	info := &series.SeriesInfo{
		EpList: []series.EpisodeInfo{{
			Season: 1, Episode: 1, FileFullPath: alternatePath, Dir: filepath.Dir(alternatePath),
			SubAlreadyDownloadedList: []series.SubInfo{{FileFullPath: alternatePath + ".srt"}},
		}},
		ArchiveEpList: []series.EpisodeInfo{{Season: 1, Episode: 1, FileFullPath: alternatePath}},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{"S1E1": {
			Season: 1, Episode: 1, FileFullPath: alternatePath, Dir: filepath.Dir(alternatePath),
			SubAlreadyDownloadedList: []series.SubInfo{{FileFullPath: alternatePath + ".srt"}},
		}},
	}
	job := taskQueueTypes.OneJob{
		Id: "target-job", Season: 1, Episode: 1, VideoFPath: targetPath,
		VideoName: "Show S01E01.mkv", MediaServerInsideVideoID: "target-media-id",
	}

	bindSeriesInfoToClaimedJobs(info, []taskQueueTypes.OneJob{job})
	bound := info.NeedDlEpsKeyList["S1E1"]
	if bound.FileFullPath != targetPath || bound.Dir != filepath.Dir(targetPath) ||
		bound.MediaServerInsideVideoID != job.MediaServerInsideVideoID || len(bound.SubAlreadyDownloadedList) != 0 {
		t.Fatalf("NeedDlEpsKeyList remained bound to alternate cut: %+v", bound)
	}
	if len(info.EpList) != 1 || info.EpList[0].FileFullPath != targetPath || len(info.EpList[0].SubAlreadyDownloadedList) != 0 {
		t.Fatalf("EpList remained bound to alternate cut: %+v", info.EpList)
	}
	if info.ArchiveEpList[0].FileFullPath != alternatePath {
		t.Fatalf("search-only archive inventory was unexpectedly rewritten: %+v", info.ArchiveEpList)
	}
}

func TestBindSeriesInfoToClaimedJobsAddsMissingScanEntry(t *testing.T) {
	targetPath := "/media/Show/target/Show S02E03.mkv"
	info := &series.SeriesInfo{}
	job := taskQueueTypes.OneJob{
		Id: "missing-scan-job", Season: 2, Episode: 3, VideoFPath: targetPath,
		VideoName: "Show S02E03.mkv", MediaServerInsideVideoID: "media-203",
	}
	duplicateCut := job
	duplicateCut.Id = "duplicate-cut"
	duplicateCut.VideoFPath = "/media/Show/alternate/Show S02E03.mkv"

	bindSeriesInfoToClaimedJobs(info, []taskQueueTypes.OneJob{job, duplicateCut})
	bound, found := info.NeedDlEpsKeyList["S2E3"]
	if !found || bound.FileFullPath != targetPath || bound.Season != 2 || bound.Episode != 3 {
		t.Fatalf("missing claimed episode was not reconstructed exactly: %+v", info.NeedDlEpsKeyList)
	}
	if len(info.EpList) != 1 || info.EpList[0].FileFullPath != targetPath {
		t.Fatalf("missing claimed episode was not added to EpList: %+v", info.EpList)
	}
	if len(info.ArchiveEpList) != 1 || info.ArchiveEpList[0].FileFullPath != targetPath {
		t.Fatalf("missing claimed episode was not added to collection inventory: %+v", info.ArchiveEpList)
	}
	if info.NeedDlSeasonDict[2] != 2 || info.SeasonDict[2] != 2 {
		t.Fatalf("missing claimed episode season maps not restored: need=%v all=%v", info.NeedDlSeasonDict, info.SeasonDict)
	}
}

func TestSeriesPreclaimDoesNotIgnoreMissingEpisodeMapEntry(t *testing.T) {
	info := &series.SeriesInfo{NeedDlEpsKeyList: map[string]series.EpisodeInfo{}}
	if _, found := info.NeedDlEpsKeyList["S2E3"]; found {
		t.Fatal("invalid fixture: episode entry unexpectedly exists")
	}
	if shouldIgnoreSeriesBeforeClaim(true) {
		t.Fatal("series-level download decision was true, but missing scan entry caused a preclaim ignore")
	}
	if !shouldIgnoreSeriesBeforeClaim(false) {
		t.Fatal("explicit series-level skip decision did not ignore the job")
	}
}

func TestSeriesBatchSaveOutcomesAreIsolatedByExactVideoPath(t *testing.T) {
	target := taskQueueTypes.OneJob{Season: 1, Episode: 1, VideoFPath: "/media/Show/target/Show S01E01.mkv"}
	alternatePath := "/media/Show/alternate/Show S01E01.mkv"
	fallback := task_queue.ErrNoSubFound

	for _, saveStage := range []string{"initial episode save", "full-season save"} {
		t.Run(saveStage+" alternate success", func(t *testing.T) {
			savedVideoPaths := make(map[string]struct{})
			saveErrorsByVideoPath := make(map[string]error)
			recordSeriesSaveResult(savedVideoPaths, saveErrorsByVideoPath, alternatePath, nil)
			if got := seriesBatchJobOutcome(target, savedVideoPaths, saveErrorsByVideoPath, fallback); !errors.Is(got, fallback) {
				t.Fatalf("alternate cut success completed target: %v", got)
			}
		})
	}

	saveErr := errors.New("target write failed")
	savedVideoPaths := make(map[string]struct{})
	saveErrorsByVideoPath := make(map[string]error)
	recordSeriesSaveResult(savedVideoPaths, saveErrorsByVideoPath, target.VideoFPath, saveErr)
	if got := seriesBatchJobOutcome(target, savedVideoPaths, saveErrorsByVideoPath, fallback); !errors.Is(got, saveErr) {
		t.Fatalf("exact target failure = %v, want %v", got, saveErr)
	}
	recordSeriesSaveResult(savedVideoPaths, saveErrorsByVideoPath, target.VideoFPath, nil)
	if got := seriesBatchJobOutcome(target, savedVideoPaths, saveErrorsByVideoPath, fallback); got != nil {
		t.Fatalf("exact target success = %v, want nil", got)
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
