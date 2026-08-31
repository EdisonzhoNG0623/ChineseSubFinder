package downloader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	markSystem "github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/mark_system"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/save_sub_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	subcommon "github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/emby"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/normal"
	timelineFixerArtifacts "github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_timeline_fixer"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	languageTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/language"
	"github.com/sirupsen/logrus"
)

func TestMultiSubSelectionReturnsErrorWhenEveryCandidateFailsToParse(t *testing.T) {
	configureSubtitleSaveTestSettings(t, true)

	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	oldMarkedPath := filepath.Join(mediaDir, "movie.zh.forced.srt")
	if err := os.WriteFile(oldMarkedPath, []byte("previous forced subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidateDir := filepath.Join(mediaDir, "candidates")
	if err := os.Mkdir(candidateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	invalidCandidate := filepath.Join(candidateDir, "[assrt]_1_invalid.srt")
	if err := os.WriteFile(invalidCandidate, []byte("not a subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := logrus.New()
	downloader := newSubtitleSelectionTestDownloader(logger, normal.NewFormatter(logger), []string{"assrt"})
	err := downloader.oneVideoSelectBestSubForCohort(
		videoPath,
		[]string{invalidCandidate},
		subtitle_metrics.CohortMovie,
	)
	if err == nil {
		t.Fatal("oneVideoSelectBestSubForCohort() returned nil after every candidate failed to parse")
	}
	if !strings.Contains(err.Error(), "found none sub file") {
		t.Fatalf("oneVideoSelectBestSubForCohort() error = %q, want empty-selection error", err)
	}
	assertDownloaderTestFile(t, oldMarkedPath, "previous forced subtitle")
	assertDownloaderTestPathMissing(t, filepath.Join(mediaDir, "movie.zh.srt"))
}

func TestOneVideoSelectionWriteFailurePreservesOldMarker(t *testing.T) {
	configureSubtitleSaveTestSettings(t, false)
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	oldMarkedPath := filepath.Join(mediaDir, "movie.zh.forced.srt")
	if err := os.WriteFile(oldMarkedPath, []byte("previous forced subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidatePath, _ := writeDownloaderTestCandidate(t, mediaDir, "[assrt]_1_valid.srt", "新的中文字幕")

	logger := logrus.New()
	formatter := emby.NewFormatter()
	downloader := newSubtitleSelectionTestDownloader(logger, formatter, []string{common.SubSiteAssrt})
	_, defaultName, _ := formatter.GenerateMixSubName(videoPath, ".srt", languageTypes.ChineseSimple, "")
	if err := os.Mkdir(filepath.Join(mediaDir, defaultName), 0o700); err != nil {
		t.Fatal(err)
	}

	err := downloader.oneVideoSelectBestSubForCohort(videoPath, []string{candidatePath}, subtitle_metrics.CohortMovie)
	if err == nil {
		t.Fatal("oneVideoSelectBestSubForCohort() succeeded despite blocked subtitle destination")
	}
	assertDownloaderTestFile(t, oldMarkedPath, "previous forced subtitle")
	assertDownloaderTestPathMissing(t, filepath.Join(mediaDir, "movie.zh.srt"))
}

func TestOneVideoSelectionSuccessfulMarkedReplacementIsNotDemoted(t *testing.T) {
	configureSubtitleSaveTestSettings(t, false)
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	newSubtitle := subtitleFixture("新的中文字幕")
	candidatePath := filepath.Join(mediaDir, "[assrt]_1_valid.srt")
	if err := os.WriteFile(candidatePath, newSubtitle, 0o600); err != nil {
		t.Fatal(err)
	}

	logger := logrus.New()
	formatter := emby.NewFormatter()
	downloader := newSubtitleSelectionTestDownloader(logger, formatter, []string{common.SubSiteAssrt})
	_, defaultName, _ := formatter.GenerateMixSubName(videoPath, ".srt", languageTypes.ChineseSimple, "")
	defaultPath := filepath.Join(mediaDir, defaultName)
	if err := os.WriteFile(defaultPath, []byte("previous default subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := downloader.oneVideoSelectBestSubForCohort(videoPath, []string{candidatePath}, subtitle_metrics.CohortMovie); err != nil {
		t.Fatal(err)
	}
	assertDownloaderTestBytes(t, defaultPath, newSubtitle)
	unmarkedName, _, _ := formatter.GenerateMixSubName(videoPath, ".srt", languageTypes.ChineseSimple, "")
	assertDownloaderTestPathMissing(t, filepath.Join(mediaDir, unmarkedName))
}

func TestOneVideoSelectionUnmarkedPublishDoesNotGetOverwrittenByOldMarker(t *testing.T) {
	configureSubtitleSaveTestSettings(t, false)
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	newSubtitle := subtitleFixture("本轮新中文字幕")
	candidatePath := filepath.Join(mediaDir, "[assrt]_1_valid.srt")
	if err := os.WriteFile(candidatePath, newSubtitle, 0o600); err != nil {
		t.Fatal(err)
	}
	oldMarkedPath := filepath.Join(mediaDir, "movie.zh.forced.srt")
	oldUnmarkedPath := filepath.Join(mediaDir, "movie.zh.srt")
	if err := os.WriteFile(oldMarkedPath, []byte("previous forced subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldUnmarkedPath, []byte("previous unmarked subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldUnmarkedPath+timelineFixerArtifacts.BackUpExt, []byte("stale backup"), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := logrus.New()
	formatter := normal.NewFormatter(logger)
	downloader := newSubtitleSelectionTestDownloader(logger, formatter, []string{common.SubSiteAssrt})
	if err := downloader.oneVideoSelectBestSubForCohort(videoPath, []string{candidatePath}, subtitle_metrics.CohortMovie); err != nil {
		t.Fatal(err)
	}
	assertDownloaderTestBytes(t, oldUnmarkedPath, newSubtitle)
	assertDownloaderTestPathMissing(t, oldMarkedPath)
	assertDownloaderTestPathMissing(t, oldUnmarkedPath+timelineFixerArtifacts.BackUpExt)
}

func TestMultiSubNonDefaultWriteFailurePreservesExistingDefaultContents(t *testing.T) {
	configureSubtitleSaveTestSettings(t, true)
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	assrtPath, _ := writeDownloaderTestCandidate(t, mediaDir, "[assrt]_1_valid.srt", "字幕源一中文字幕")
	openSubtitlesPath, _ := writeDownloaderTestCandidate(t, mediaDir, "[open_subtitles]_2_valid.srt", "字幕源二中文字幕")

	logger := logrus.New()
	formatter := emby.NewFormatter()
	downloader := newSubtitleSelectionTestDownloader(logger, formatter, []string{common.SubSiteAssrt, common.SubSiteOpenSubtitles})
	oldDefaultContents := make(map[string]string)
	for index, site := range []string{common.SubSiteAssrt, common.SubSiteOpenSubtitles} {
		unmarkedName, defaultName, _ := formatter.GenerateMixSubName(videoPath, ".srt", languageTypes.ChineseSimple, site)
		defaultPath := filepath.Join(mediaDir, defaultName)
		oldDefaultContents[defaultPath] = fmt.Sprintf("previous %d default subtitle", index)
		if err := os.WriteFile(defaultPath, []byte(oldDefaultContents[defaultPath]), 0o600); err != nil {
			t.Fatal(err)
		}
		// The selector's map order is intentionally unspecified. Blocking both
		// non-default paths guarantees the selected secondary site fails before
		// either possible same-path authoritative default can be replaced.
		if err := os.Mkdir(filepath.Join(mediaDir, unmarkedName), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	err := downloader.oneVideoSelectBestSubForCohort(
		videoPath, []string{assrtPath, openSubtitlesPath}, subtitle_metrics.CohortMovie,
	)
	if err == nil {
		t.Fatal("oneVideoSelectBestSubForCohort() succeeded despite non-default destination collision")
	}
	for defaultPath, contents := range oldDefaultContents {
		assertDownloaderTestFile(t, defaultPath, contents)
	}
}

func TestSnapshotVideoSubtitleMarkersScopesMarkersAfterVideoStem(t *testing.T) {
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "Movie.default.cut.mkv")
	for name, contents := range map[string]string{
		"Movie.default.cut.zh.srt":        "ordinary subtitle",
		"Movie.default.cut.default.srt":   "immediate default marker",
		"Movie.default.cut.zh.forced.srt": "forced marker",
	} {
		if err := os.WriteFile(filepath.Join(mediaDir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snapshots, err := snapshotVideoSubtitleMarkers(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count = %d, want immediate default and forced only: %+v", len(snapshots), snapshots)
	}
	got := make(map[string]string, len(snapshots))
	for _, snapshot := range snapshots {
		got[filepath.Base(snapshot.MarkedPath())] = filepath.Base(snapshot.UnmarkedPath())
	}
	want := map[string]string{
		"Movie.default.cut.default.srt":   "Movie.default.cut.srt",
		"Movie.default.cut.zh.forced.srt": "Movie.default.cut.zh.srt",
	}
	for marked, unmarked := range want {
		if got[marked] != unmarked {
			t.Fatalf("snapshot mapping %q = %q, want %q; all=%v", marked, got[marked], unmarked, got)
		}
	}
	if _, misclassified := got["Movie.default.cut.zh.srt"]; misclassified {
		t.Fatal(".default. inside the video stem was misclassified as a subtitle marker")
	}
}

func TestSnapshotVideoSubtitleMarkersUsesUniqueLongestSiblingStem(t *testing.T) {
	t.Run("longer sibling owns its marker", func(t *testing.T) {
		mediaDir := t.TempDir()
		videoPath := filepath.Join(mediaDir, "Movie.mkv")
		for name, contents := range map[string]string{
			"Movie.mkv":                     "",
			"Movie.Extended.mkv":            "",
			"Movie.zh.default.srt":          "current marker",
			"Movie.Extended.zh.default.srt": "extended marker",
		} {
			if err := os.WriteFile(filepath.Join(mediaDir, name), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		snapshots, err := snapshotVideoSubtitleMarkers(videoPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshots) != 1 || filepath.Base(snapshots[0].MarkedPath()) != "Movie.zh.default.srt" {
			t.Fatalf("short video captured sibling marker: %+v", snapshots)
		}
	})

	t.Run("same stem with different video extensions is ambiguous", func(t *testing.T) {
		mediaDir := t.TempDir()
		videoPath := filepath.Join(mediaDir, "Movie.mkv")
		for _, name := range []string{"Movie.mkv", "Movie.mp4", "Movie.zh.default.srt"} {
			if err := os.WriteFile(filepath.Join(mediaDir, name), []byte(name), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		snapshots, err := snapshotVideoSubtitleMarkers(videoPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshots) != 0 {
			t.Fatalf("ambiguous same-stem marker was claimed by one video: %+v", snapshots)
		}
	})
}

func TestDemoteVideoSubtitleMarkersRemovesStaleUnmarkedBackup(t *testing.T) {
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	markedPath := filepath.Join(mediaDir, "movie.zh.forced.srt")
	unmarkedPath := filepath.Join(mediaDir, "movie.zh.srt")
	if err := os.WriteFile(markedPath, []byte("forced subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmarkedPath+timelineFixerArtifacts.BackUpExt, []byte("stale backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshots, err := snapshotVideoSubtitleMarkers(videoPath)
	if err != nil {
		t.Fatal(err)
	}

	demoteVideoSubtitleMarkers(logrus.New(), snapshots)
	assertDownloaderTestFile(t, unmarkedPath, "forced subtitle")
	assertDownloaderTestPathMissing(t, markedPath)
	assertDownloaderTestPathMissing(t, unmarkedPath+timelineFixerArtifacts.BackUpExt)
}

func TestDemoteVideoSubtitleMarkersBackupInspectionFailureClearsStaleUnmarkedBackup(t *testing.T) {
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	markedPath := filepath.Join(mediaDir, "movie.zh.default.srt")
	markedBackup := markedPath + timelineFixerArtifacts.BackUpExt
	unmarkedBackup := filepath.Join(mediaDir, "movie.zh.srt") + timelineFixerArtifacts.BackUpExt
	if err := os.WriteFile(markedPath, []byte("default subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markedBackup, []byte("marked backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmarkedBackup, []byte("stale unmarked backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshots, err := snapshotVideoSubtitleMarkers(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(markedBackup); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(markedBackup), markedBackup); err != nil {
		t.Skipf("symlink fixture unsupported: %v", err)
	}

	demoteVideoSubtitleMarkers(logrus.New(), snapshots)
	assertDownloaderTestPathMissing(t, unmarkedBackup)
}

func TestDemoteVideoSubtitleMarkersDoesNotOverwriteFirstMarkerOnCollision(t *testing.T) {
	mediaDir := t.TempDir()
	videoPath := filepath.Join(mediaDir, "movie.mkv")
	defaultPath := filepath.Join(mediaDir, "movie.zh.default.srt")
	forcedPath := filepath.Join(mediaDir, "movie.zh.forced.srt")
	unmarkedPath := filepath.Join(mediaDir, "movie.zh.srt")
	if err := os.WriteFile(defaultPath, []byte("default wins collision"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(forcedPath, []byte("forced must not overwrite"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshots, err := snapshotVideoSubtitleMarkers(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(snapshots))
	}

	demoteVideoSubtitleMarkers(logrus.New(), snapshots)
	assertDownloaderTestFile(t, unmarkedPath, "default wins collision")
	assertDownloaderTestPathMissing(t, defaultPath)
	assertDownloaderTestPathMissing(t, forcedPath)
}

func configureSubtitleSaveTestSettings(t *testing.T, saveMultiSub bool) {
	t.Helper()
	settings.SetConfigRootPath(t.TempDir())
	currentSettings := settings.Get()
	previousSaveMultiSub := currentSettings.AdvancedSettings.SaveMultiSub
	previousDebugMode := currentSettings.AdvancedSettings.DebugMode
	previousFixTimeLine := currentSettings.AdvancedSettings.FixTimeLine
	previousAutoChangeEncode := currentSettings.ExperimentalFunction.AutoChangeSubEncode.Enable
	previousChsChtChanger := currentSettings.ExperimentalFunction.ChsChtChanger.Enable
	currentSettings.AdvancedSettings.SaveMultiSub = saveMultiSub
	currentSettings.AdvancedSettings.DebugMode = false
	currentSettings.AdvancedSettings.FixTimeLine = false
	currentSettings.ExperimentalFunction.AutoChangeSubEncode.Enable = false
	currentSettings.ExperimentalFunction.ChsChtChanger.Enable = false
	t.Cleanup(func() {
		currentSettings.AdvancedSettings.SaveMultiSub = previousSaveMultiSub
		currentSettings.AdvancedSettings.DebugMode = previousDebugMode
		currentSettings.AdvancedSettings.FixTimeLine = previousFixTimeLine
		currentSettings.ExperimentalFunction.AutoChangeSubEncode.Enable = previousAutoChangeEncode
		currentSettings.ExperimentalFunction.ChsChtChanger.Enable = previousChsChtChanger
	})
}

func newSubtitleSelectionTestDownloader(logger *logrus.Logger, formatter ifaces.ISubFormatter, sites []string) *Downloader {
	return &Downloader{
		log:              logger,
		mk:               markSystem.NewMarkingSystem(logger, sites, 0),
		subFormatter:     formatter,
		subNameFormatter: subcommon.FormatterName(formatter.GetFormatterFormatterName()),
		SaveSubHelper:    save_sub_helper.NewSaveSubHelper(logger, formatter, nil),
	}
}

func writeDownloaderTestCandidate(t *testing.T, dir, name, text string) (string, []byte) {
	t.Helper()
	contents := subtitleFixture(text)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, contents
}

func assertDownloaderTestFile(t *testing.T, path, want string) {
	t.Helper()
	assertDownloaderTestBytes(t, path, []byte(want))
}

func assertDownloaderTestBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s contents differ: got %q want %q", filepath.Base(path), got, want)
	}
}

func assertDownloaderTestPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s still exists: %v", path, err)
	}
}
