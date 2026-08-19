package downloader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/sirupsen/logrus"
)

func TestCollectionBackfillCandidatesFillOnlyMissingEpisodes(t *testing.T) {
	dir := t.TempDir()
	episodePath := func(episode string) string { return filepath.Join(dir, "series - S01E"+episode+".mkv") }
	for _, episode := range []string{"01", "02", "04"} {
		if err := os.WriteFile(episodePath(episode), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "series - S01E02.zh.ass"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	combinedVideoPath := filepath.Join(dir, "series - S01E75-E76.mkv")
	if err := os.WriteFile(combinedVideoPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "series - S01E75.zh.ass"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	jobs := []taskQueue2.OneJob{
		{SeriesRootDirPath: dir, Season: 1, Episode: 1, VideoFPath: episodePath("01"), VideoName: filepath.Base(episodePath("01"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 2, VideoFPath: episodePath("02"), VideoName: filepath.Base(episodePath("02"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 4, VideoFPath: episodePath("04"), VideoName: filepath.Base(episodePath("04"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 75, VideoFPath: combinedVideoPath, VideoName: filepath.Base(combinedVideoPath)},
	}
	organized := map[string][]string{
		pkg.GetEpisodeKeyName(1, 1):  {"/cache/e01.ass"},
		pkg.GetEpisodeKeyName(1, 2):  {"/cache/e02.ass"},
		pkg.GetEpisodeKeyName(1, 4):  {"/cache/e04.ass"},
		pkg.GetEpisodeKeyName(1, 75): {"/cache/e75.ass"},
	}

	candidates, satisfiedEpisodeKeys, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		logrus.New(), jobs, organized, pkg.GetEpisodeKeyName(1, 4),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Episode != 1 {
		t.Fatalf("candidates = %+v, want only S01E01", candidates)
	}
	if skippedExisting != 2 {
		t.Fatalf("skippedExisting = %d, want 2", skippedExisting)
	}
	if _, ok := satisfiedEpisodeKeys[pkg.GetEpisodeKeyName(1, 2)]; !ok {
		t.Fatal("episode with an existing subtitle was not marked satisfied")
	}
	if _, ok := satisfiedEpisodeKeys[pkg.GetEpisodeKeyName(1, 75)]; !ok {
		t.Fatal("combined episode video was not satisfied by its parsed episode subtitle")
	}
}

func TestHasAdditionalCollectionEpisodes(t *testing.T) {
	targetKey := pkg.GetEpisodeKeyName(1, 4)
	if hasAdditionalCollectionEpisodes(map[string][]string{targetKey: {"/cache/e04.ass"}}, targetKey) {
		t.Fatal("single-episode result identified as a collection")
	}
	if !hasAdditionalCollectionEpisodes(map[string][]string{
		targetKey:                   {"/cache/e04.ass"},
		pkg.GetEpisodeKeyName(1, 5): {"/cache/e05.ass"},
	}, targetKey) {
		t.Fatal("multi-episode result was not identified as a collection")
	}
}
