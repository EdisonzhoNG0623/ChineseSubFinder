package downloader

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/sirupsen/logrus"
)

func TestCollectionBackfillCandidatesFillOnlyMissingEpisodes(t *testing.T) {
	dir := t.TempDir()
	episodePath := func(episode string) string { return filepath.Join(dir, "series - S01E"+episode+".mkv") }
	for _, episode := range []string{"01", "02", "03", "04", "05"} {
		if err := os.WriteFile(episodePath(episode), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "series - S01E02.zh.srt"), subtitleFixture("这是有效的中文字幕"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "series - S01E03.en.srt"), subtitleFixture("This is an English subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "series - S01E04.zh.srt"), bytes.Repeat([]byte("not a subtitle\n"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	combinedVideoPath := filepath.Join(dir, "series - S01E75-E76.mkv")
	if err := os.WriteFile(combinedVideoPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "series - S01E75.zh.srt"), subtitleFixture("这是合并剧集的中文字幕"), 0o644); err != nil {
		t.Fatal(err)
	}
	jobs := []taskQueue2.OneJob{
		{SeriesRootDirPath: dir, Season: 1, Episode: 1, VideoFPath: episodePath("01"), VideoName: filepath.Base(episodePath("01"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 2, VideoFPath: episodePath("02"), VideoName: filepath.Base(episodePath("02"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 3, VideoFPath: episodePath("03"), VideoName: filepath.Base(episodePath("03"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 4, VideoFPath: episodePath("04"), VideoName: filepath.Base(episodePath("04"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 5, VideoFPath: episodePath("05"), VideoName: filepath.Base(episodePath("05"))},
		{SeriesRootDirPath: dir, Season: 1, Episode: 75, VideoFPath: combinedVideoPath, VideoName: filepath.Base(combinedVideoPath)},
	}
	organized := map[string][]string{
		pkg.GetEpisodeKeyName(1, 1):  {"/cache/e01.ass"},
		pkg.GetEpisodeKeyName(1, 2):  {"/cache/e02.ass"},
		pkg.GetEpisodeKeyName(1, 3):  {"/cache/e03.ass"},
		pkg.GetEpisodeKeyName(1, 4):  {"/cache/e04.ass"},
		pkg.GetEpisodeKeyName(1, 5):  {"/cache/e05.ass"},
		pkg.GetEpisodeKeyName(1, 75): {"/cache/e75.ass"},
	}

	candidates, satisfiedVideoPaths, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		logrus.New(), jobs, organized, episodePath("05"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 || candidates[0].Episode != 1 || candidates[1].Episode != 3 || candidates[2].Episode != 4 {
		t.Fatalf("candidates = %+v, want S01E01 plus English S01E03 and damaged S01E04", candidates)
	}
	if skippedExisting != 2 {
		t.Fatalf("skippedExisting = %d, want 2", skippedExisting)
	}
	for _, videoPath := range []string{episodePath("02"), combinedVideoPath} {
		if _, ok := satisfiedVideoPaths[filepath.Clean(videoPath)]; !ok {
			t.Fatalf("video with an existing valid Chinese subtitle was not recorded exactly: %s", videoPath)
		}
	}
	for _, videoPath := range []string{episodePath("03"), episodePath("04")} {
		if _, ok := satisfiedVideoPaths[filepath.Clean(videoPath)]; ok {
			t.Fatalf("non-Chinese or invalid subtitle incorrectly satisfied %s", videoPath)
		}
	}
}

func TestCollectionBackfillCandidatesBindEvidenceAndDeduplicationToVideoPath(t *testing.T) {
	root := t.TempDir()
	videoPath := func(version string, episode int) string {
		return filepath.Join(root, version, fmt.Sprintf("series - S01E%02d.mkv", episode))
	}
	targetPath := videoPath("target-cut", 1)
	alternatePath := videoPath("alternate-cut", 1)
	existingPath := videoPath("existing-cut", 2)
	missingPath := videoPath("missing-cut", 3)
	for _, path := range []string{targetPath, alternatePath, existingPath, missingPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(existingPath), "series - S01E02.zh.srt"),
		subtitleFixture("这是精确匹配视频的中文字幕"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	// This valid subtitle contains the same episode number as missingPath but is
	// neither in its directory nor named after its video. It must not satisfy it.
	if err := os.WriteFile(
		filepath.Join(root, "unrelated - S01E03.zh.srt"),
		subtitleFixture("这是无关目录中的中文字幕"), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	jobs := []taskQueue2.OneJob{
		{SeriesRootDirPath: root, Season: 1, Episode: 1, VideoFPath: targetPath, VideoName: filepath.Base(targetPath)},
		{SeriesRootDirPath: root, Season: 1, Episode: 1, VideoFPath: alternatePath, VideoName: filepath.Base(alternatePath)},
		{SeriesRootDirPath: root, Season: 1, Episode: 2, VideoFPath: existingPath, VideoName: filepath.Base(existingPath)},
		{SeriesRootDirPath: root, Season: 1, Episode: 3, VideoFPath: missingPath, VideoName: filepath.Base(missingPath)},
	}
	organized := map[string][]string{
		pkg.GetEpisodeKeyName(1, 1): {"/cache/e01.ass"},
		pkg.GetEpisodeKeyName(1, 2): {"/cache/e02.ass"},
		pkg.GetEpisodeKeyName(1, 3): {"/cache/e03.ass"},
	}

	candidates, satisfiedVideoPaths, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		logrus.New(), jobs, organized, targetPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].FileFullPath != alternatePath || candidates[1].FileFullPath != missingPath {
		t.Fatalf("candidates = %+v, want alternate S01E01 and unsatisfied S01E03 videos", candidates)
	}
	if skippedExisting != 1 {
		t.Fatalf("skippedExisting = %d, want 1", skippedExisting)
	}
	if len(satisfiedVideoPaths) != 1 {
		t.Fatalf("satisfiedVideoPaths = %v, want only exact S01E02 video", satisfiedVideoPaths)
	}
	if _, ok := satisfiedVideoPaths[filepath.Clean(existingPath)]; !ok {
		t.Fatalf("exact S01E02 video missing from evidence: %v", satisfiedVideoPaths)
	}
}

func TestCollectionBackfillDoesNotUseUnqueuedLongerSiblingSubtitle(t *testing.T) {
	dir := t.TempDir()
	queuedVideoPath := filepath.Join(dir, "Movie.mkv")
	unqueuedSiblingPath := filepath.Join(dir, "Movie.Extended.mkv")
	for _, videoPath := range []string{queuedVideoPath, unqueuedSiblingPath} {
		if err := os.WriteFile(videoPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(dir, "Movie.Extended.zh.srt"), subtitleFixture("这是加长版的中文字幕"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	jobs := []taskQueue2.OneJob{{
		SeriesRootDirPath: dir, Season: 1, Episode: 1,
		VideoFPath: queuedVideoPath, VideoName: filepath.Base(queuedVideoPath),
	}}

	candidates, satisfiedVideoPaths, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		logrus.New(), jobs, map[string][]string{pkg.GetEpisodeKeyName(1, 1): {"/cache/e01.srt"}}, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].FileFullPath != queuedVideoPath || skippedExisting != 0 {
		t.Fatalf("unqueued sibling evidence changed queued candidate: candidates=%+v skipped=%d", candidates, skippedExisting)
	}
	if len(satisfiedVideoPaths) != 0 {
		t.Fatalf("unqueued longer sibling satisfied queued short video: %v", satisfiedVideoPaths)
	}
}

func TestVideoHasValidChineseSubtitleDoesNotUseLongerSiblingEvidence(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "Movie.mkv")
	siblingPath := filepath.Join(dir, "Movie.Extended.mkv")
	for _, path := range []string{videoPath, siblingPath} {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Movie.zh.srt"), []byte("damaged current subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "Movie.Extended.zh.srt"), subtitleFixture("这是加长版的中文字幕"), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	valid, err := videoHasValidChineseSubtitle(logrus.New(), videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("damaged current subtitle was masked by a valid longer-sibling subtitle")
	}
}

func TestCollectionBackfillAcceptsStrictlyParsedSmallChineseSubtitleEvidence(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "series - S01E01.mkv")
	subtitlePath := filepath.Join(dir, "series - S01E01.zh.srt")
	if err := os.WriteFile(videoPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	smallSubtitle := []byte("1\n00:00:01,000 --> 00:00:02,000\n这是一条有效中文字幕\n\n2\n00:00:03,000 --> 00:00:04,000\n第二条中文字幕\n")
	if len(smallSubtitle) >= 1000 {
		t.Fatalf("fixture size = %d, want less than generic scanner threshold", len(smallSubtitle))
	}
	if err := os.WriteFile(subtitlePath, smallSubtitle, 0o644); err != nil {
		t.Fatal(err)
	}

	valid, err := videoHasValidChineseSubtitle(logrus.New(), videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("strictly parsed sub-1KB Chinese subtitle was rejected")
	}
	jobs := []taskQueue2.OneJob{{
		SeriesRootDirPath: dir, Season: 1, Episode: 1, VideoFPath: videoPath, VideoName: filepath.Base(videoPath),
	}}
	candidates, satisfiedVideoPaths, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		logrus.New(), jobs, map[string][]string{pkg.GetEpisodeKeyName(1, 1): {"/cache/e01.ass"}}, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 || skippedExisting != 1 {
		t.Fatalf("candidates=%v skippedExisting=%d, want existing small subtitle skipped", candidates, skippedExisting)
	}
	if _, ok := satisfiedVideoPaths[filepath.Clean(videoPath)]; !ok {
		t.Fatal("small Chinese subtitle did not create exact video evidence")
	}
}

func TestCollectionBackfillSkipsStaleVideoDirectoryWithoutBlockingValidJobs(t *testing.T) {
	root := t.TempDir()
	validDir := filepath.Join(root, "valid")
	if err := os.MkdirAll(validDir, 0o755); err != nil {
		t.Fatal(err)
	}
	validVideoPath := filepath.Join(validDir, "series - S01E02.mkv")
	if err := os.WriteFile(validVideoPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(validDir, "series - S01E02.zh.srt"), subtitleFixture("这是有效的中文字幕"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	staleVideoPath := filepath.Join(root, "deleted", "series - S01E01.mkv")
	jobs := []taskQueue2.OneJob{
		{SeriesRootDirPath: root, Season: 1, Episode: 1, VideoFPath: staleVideoPath, VideoName: filepath.Base(staleVideoPath)},
		{SeriesRootDirPath: root, Season: 1, Episode: 2, VideoFPath: validVideoPath, VideoName: filepath.Base(validVideoPath)},
	}
	organized := map[string][]string{
		pkg.GetEpisodeKeyName(1, 1): {"/cache/e01.ass"},
		pkg.GetEpisodeKeyName(1, 2): {"/cache/e02.ass"},
	}

	candidates, satisfiedVideoPaths, skippedExisting, err := collectionBackfillCandidatesFromJobs(
		logrus.New(), jobs, organized, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("stale video became a backfill candidate: %+v", candidates)
	}
	if skippedExisting != 1 {
		t.Fatalf("skippedExisting = %d, want valid existing video only", skippedExisting)
	}
	if len(satisfiedVideoPaths) != 1 {
		t.Fatalf("satisfiedVideoPaths = %v, want only valid existing video", satisfiedVideoPaths)
	}
	if _, ok := satisfiedVideoPaths[filepath.Clean(validVideoPath)]; !ok {
		t.Fatalf("valid job was blocked by stale directory: %v", satisfiedVideoPaths)
	}
}

func TestMergeBackfillBatchSuccessesRequiresExactVideoPath(t *testing.T) {
	targetPath := "/media/series/target-cut/series - S01E01.mkv"
	alternatePath := "/media/series/alternate-cut/series - S01E01.mkv"
	batchJobs := []taskQueue2.OneJob{{Season: 1, Episode: 1, VideoFPath: targetPath}}
	savedVideoPaths := make(map[string]struct{})

	mergeBackfillBatchSuccesses(savedVideoPaths, batchJobs, map[string]struct{}{alternatePath: {}})
	if _, saved := savedVideoPaths[canonicalSeriesVideoPath(targetPath)]; saved {
		t.Fatal("alternate cut evidence incorrectly completed the target cut")
	}
	mergeBackfillBatchSuccesses(savedVideoPaths, batchJobs, map[string]struct{}{targetPath: {}})
	if _, saved := savedVideoPaths[canonicalSeriesVideoPath(targetPath)]; !saved {
		t.Fatal("exact target video evidence did not complete its batch outcome")
	}
}

func TestVideoHasValidChineseSubtitleRejectsEnglishAndDamagedFiles(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "series - S01E01.mkv")
	subtitlePath := filepath.Join(dir, "series - S01E01.zh.srt")
	if err := os.WriteFile(videoPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"Chinese": subtitleFixture("这是有效的中文字幕"),
		"English": subtitleFixture("This is an English subtitle"),
		"Damaged": bytes.Repeat([]byte("not a subtitle\n"), 100),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(subtitlePath, data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := videoHasValidChineseSubtitle(logrus.New(), videoPath)
			if err != nil {
				t.Fatal(err)
			}
			if got != (name == "Chinese") {
				t.Fatalf("videoHasValidChineseSubtitle() = %v for %s subtitle", got, name)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "series - S01E010.zh.srt"), subtitleFixture("这是其他剧集的中文字幕"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subtitlePath, bytes.Repeat([]byte("not a subtitle\n"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := videoHasValidChineseSubtitle(logrus.New(), videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("prefix-colliding subtitle for S01E010 was accepted for S01E01")
	}
}

func subtitleFixture(text string) []byte {
	var out strings.Builder
	for index := 1; index <= 32; index++ {
		fmt.Fprintf(&out, "%d\n00:00:%02d,000 --> 00:00:%02d,900\n%s %d\n\n", index, index%60, (index+1)%60, text, index)
	}
	return []byte(out.String())
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
