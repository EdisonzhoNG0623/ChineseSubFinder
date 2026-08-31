package save_sub_helper

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	languageTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/language"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/subparser"
	"github.com/sirupsen/logrus"
)

type fixedSubtitleFormatter struct{}

func (fixedSubtitleFormatter) GetFormatterName() string       { return "test" }
func (fixedSubtitleFormatter) GetFormatterFormatterName() int { return 0 }
func (fixedSubtitleFormatter) IsMatchThisFormat(string) (bool, string, string, languageTypes.MyLanguage, string) {
	return false, "", "", languageTypes.Unknown, ""
}
func (fixedSubtitleFormatter) GenerateMixSubName(string, string, languageTypes.MyLanguage, string) (string, string, string) {
	return "Episode.zh.srt", "Episode.zh.default.srt", "Episode.zh.forced.srt"
}
func (fixedSubtitleFormatter) GenerateMixSubNameBase(string, string, languageTypes.MyLanguage, string) (string, string, string) {
	return "Episode.zh.srt", "Episode.zh.default.srt", "Episode.zh.forced.srt"
}

func TestWriteSubFilePipelineFailurePreservesInstalledSubtitles(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "Episode.mkv")
	plainPath := filepath.Join(root, "Episode.zh.srt")
	plainBackupPath := plainPath + ".csf-bk"
	defaultPath := filepath.Join(root, "Episode.zh.default.srt")
	defaultBackupPath := defaultPath + ".csf-bk"
	if err := os.WriteFile(plainPath, []byte("old plain subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainBackupPath, []byte("old plain backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultPath, []byte("old default subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultBackupPath, []byte("old installed backup"), 0o644); err != nil {
		t.Fatal(err)
	}

	helper := NewSaveSubHelper(logrus.New(), fixedSubtitleFormatter{}, nil)
	injectedErr := errors.New("injected post-processing failure")
	err := helper.writeSubFile2VideoPathWithPipeline(videoPath, subparser.FileInfo{
		Name: "download.srt", Ext: ".srt", Lang: languageTypes.ChineseSimple, Data: []byte("new subtitle"),
	}, "", true, false, func(stagedPath string) error {
		if writeErr := os.WriteFile(stagedPath, []byte("partially processed subtitle"), 0o644); writeErr != nil {
			return writeErr
		}
		if writeErr := os.WriteFile(stagedPath+".csf-bk", []byte("staged backup"), 0o644); writeErr != nil {
			return writeErr
		}
		if writeErr := os.WriteFile(stagedPath+".csf-tmp", []byte("staged timeline output"), 0o644); writeErr != nil {
			return writeErr
		}
		return injectedErr
	})
	if !errors.Is(err, injectedErr) {
		t.Fatalf("WriteSubFile2VideoPath() error = %v, want %v", err, injectedErr)
	}
	assertFileContents(t, plainPath, "old plain subtitle")
	assertFileContents(t, plainBackupPath, "old plain backup")
	assertFileContents(t, defaultPath, "old default subtitle")
	assertFileContents(t, defaultBackupPath, "old installed backup")
	assertNoSubtitleStagingArtifacts(t, root)
}

func TestWriteSubFilePipelineInstallsThenRemovesOldNonDefault(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "Episode.mkv")
	plainPath := filepath.Join(root, "Episode.zh.srt")
	plainBackupPath := plainPath + ".csf-bk"
	defaultPath := filepath.Join(root, "Episode.zh.default.srt")
	defaultBackupPath := defaultPath + ".csf-bk"
	if err := os.WriteFile(plainPath, []byte("old plain subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainBackupPath, []byte("old plain backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultPath, []byte("old default subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultBackupPath, []byte("superseded installed backup"), 0o644); err != nil {
		t.Fatal(err)
	}

	helper := NewSaveSubHelper(logrus.New(), fixedSubtitleFormatter{}, nil)
	err := helper.writeSubFile2VideoPathWithPipeline(videoPath, subparser.FileInfo{
		Name: "download.srt", Ext: ".srt", Lang: languageTypes.ChineseSimple, Data: []byte("new subtitle"),
	}, "", true, false, func(stagedPath string) error {
		if err := os.WriteFile(stagedPath+".csf-bk", []byte("original downloaded subtitle"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(stagedPath, []byte("fully processed subtitle"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, defaultPath, "fully processed subtitle")
	assertFileContents(t, defaultBackupPath, "original downloaded subtitle")
	if _, err = os.Stat(plainPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old non-default subtitle still exists: %v", err)
	}
	if _, err = os.Lstat(plainBackupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old non-default subtitle backup still exists: %v", err)
	}
	assertNoSubtitleStagingArtifacts(t, root)
}

func TestWriteSubFilePipelineRemovesStaleBackupWhenNoNewBackupExists(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "Episode.mkv")
	targetPath := filepath.Join(root, "Episode.zh.srt")
	backupPath := targetPath + ".csf-bk"
	if err := os.WriteFile(targetPath, []byte("old processed subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, []byte("old downloaded subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}

	helper := NewSaveSubHelper(logrus.New(), fixedSubtitleFormatter{}, nil)
	err := helper.writeSubFile2VideoPathWithPipeline(videoPath, subparser.FileInfo{
		Name: "download.srt", Ext: ".srt", Lang: languageTypes.ChineseSimple, Data: []byte("new subtitle"),
	}, "", false, false, func(stagedPath string) error {
		// This save intentionally produces no .csf-bk (for example, timeline
		// correction is disabled or decides no correction is needed).
		return os.WriteFile(stagedPath, []byte("new unadjusted subtitle"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, targetPath, "new unadjusted subtitle")
	if _, err = os.Lstat(backupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale backup survived successful unadjusted install: %v", err)
	}
	assertNoSubtitleStagingArtifacts(t, root)
}

func TestWriteSubFileBackupPublishFailureKeepsCommittedSubtitle(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "Episode.mkv")
	targetPath := filepath.Join(root, "Episode.zh.srt")
	backupPath := targetPath + ".csf-bk"
	if err := os.WriteFile(targetPath, []byte("old subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory at the backup destination deterministically rejects a file
	// rename while leaving the main subtitle destination usable.
	if err := os.Mkdir(backupPath, 0o755); err != nil {
		t.Fatal(err)
	}

	var logOutput bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logOutput)
	helper := NewSaveSubHelper(logger, fixedSubtitleFormatter{}, nil)
	err := helper.writeSubFile2VideoPathWithPipeline(videoPath, subparser.FileInfo{
		Name: "download.srt", Ext: ".srt", Lang: languageTypes.ChineseSimple, Data: []byte("new subtitle"),
	}, "", false, false, func(stagedPath string) error {
		if writeErr := os.WriteFile(stagedPath+".csf-bk", []byte("original downloaded subtitle"), 0o644); writeErr != nil {
			return writeErr
		}
		return os.WriteFile(stagedPath, []byte("fully processed subtitle"), 0o644)
	})
	if err != nil {
		t.Fatalf("backup publication failure was returned after main commit: %v", err)
	}
	assertFileContents(t, targetPath, "fully processed subtitle")
	if info, statErr := os.Stat(backupPath); statErr != nil || !info.IsDir() {
		t.Fatalf("existing backup destination changed: info=%v error=%v", info, statErr)
	}
	if !strings.Contains(logOutput.String(), "publish processed subtitle backup") {
		t.Fatalf("backup publication failure was not logged: %s", logOutput.String())
	}
	assertNoSubtitleStagingArtifacts(t, root)
}

func TestVideoWriteTransactionsSerializeSameVideo(t *testing.T) {
	helper := NewSaveSubHelper(logrus.New(), fixedSubtitleFormatter{}, nil)
	videoPath := filepath.Join(t.TempDir(), "Episode.mkv")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- helper.WithVideoWriteLock(videoPath, func(*VideoWriteTransaction) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	waitForSubtitleTestSignal(t, firstEntered, "first transaction admission")

	secondStarted := make(chan struct{})
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- helper.WithVideoWriteLock(filepath.Clean(videoPath), func(*VideoWriteTransaction) error {
			close(secondEntered)
			return nil
		})
	}()
	waitForSubtitleTestSignal(t, secondStarted, "second transaction start")
	select {
	case <-secondEntered:
		t.Fatal("same-video transaction entered before the owner released its lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	waitForSubtitleTestSignal(t, secondEntered, "second transaction admission")
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	helper.videoWriteLocksMu.Lock()
	remainingLocks := len(helper.videoWriteLocks)
	helper.videoWriteLocksMu.Unlock()
	if remainingLocks != 0 {
		t.Fatalf("video write lock entries leaked: %d", remainingLocks)
	}
}

func TestVideoWriteTransactionsAllowDifferentVideosInParallel(t *testing.T) {
	helper := NewSaveSubHelper(logrus.New(), fixedSubtitleFormatter{}, nil)
	root := t.TempDir()
	release := make(chan struct{})
	entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 2)
	for index, videoName := range []string{"Episode-A.mkv", "Episode-B.mkv"} {
		index, videoPath := index, filepath.Join(root, videoName)
		go func() {
			done <- helper.WithVideoWriteLock(videoPath, func(*VideoWriteTransaction) error {
				close(entered[index])
				<-release
				return nil
			})
		}()
	}
	waitForSubtitleTestSignal(t, entered[0], "first video transaction")
	waitForSubtitleTestSignal(t, entered[1], "second video transaction")
	close(release)
	for range entered {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestVideoWriteTransactionsSerializeSameSubtitleNamespace(t *testing.T) {
	helper := NewSaveSubHelper(logrus.New(), fixedSubtitleFormatter{}, nil)
	root := t.TempDir()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- helper.WithVideoWriteLock(filepath.Join(root, "Movie.mkv"), func(*VideoWriteTransaction) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	waitForSubtitleTestSignal(t, firstEntered, "first same-stem transaction")

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- helper.WithVideoWriteLock(filepath.Join(root, "Movie.mp4"), func(*VideoWriteTransaction) error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("same subtitle namespace entered through a different video extension")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	waitForSubtitleTestSignal(t, secondEntered, "second same-stem transaction")
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestVideoWriteTransactionKeepsSubtitleAndBackupGenerationTogether(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "Episode.mkv")
	targetPath := filepath.Join(root, "Episode.zh.srt")
	backupPath := targetPath + ".csf-bk"
	helper := NewSaveSubHelper(logrus.New(), fixedSubtitleFormatter{}, nil)
	firstPublished := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- helper.WithVideoWriteLock(videoPath, func(*VideoWriteTransaction) error {
			err := helper.writeSubFile2VideoPathWithPipeline(videoPath, subparser.FileInfo{
				Name: "A.srt", Ext: ".srt", Lang: languageTypes.ChineseSimple, Data: []byte("A-source"),
			}, "", false, false, func(stagedPath string) error {
				if err := os.WriteFile(stagedPath+".csf-bk", []byte("A-backup"), 0o644); err != nil {
					return err
				}
				return os.WriteFile(stagedPath, []byte("A-main"), 0o644)
			})
			if err != nil {
				return err
			}
			close(firstPublished)
			<-releaseFirst
			return nil
		})
	}()
	waitForSubtitleTestSignal(t, firstPublished, "first subtitle generation publication")

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- helper.WithVideoWriteLock(videoPath, func(*VideoWriteTransaction) error {
			close(secondEntered)
			return helper.writeSubFile2VideoPathWithPipeline(videoPath, subparser.FileInfo{
				Name: "B.srt", Ext: ".srt", Lang: languageTypes.ChineseSimple, Data: []byte("B-source"),
			}, "", false, false, func(stagedPath string) error {
				if err := os.WriteFile(stagedPath+".csf-bk", []byte("B-backup"), 0o644); err != nil {
					return err
				}
				return os.WriteFile(stagedPath, []byte("B-main"), 0o644)
			})
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second generation entered before the first transaction completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	waitForSubtitleTestSignal(t, secondEntered, "second subtitle generation publication")
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, targetPath, "B-main")
	assertFileContents(t, backupPath, "B-backup")
	assertNoSubtitleStagingArtifacts(t, root)
}

func waitForSubtitleTestSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s contents = %q, want %q", filepath.Base(path), got, want)
	}
}

func assertNoSubtitleStagingArtifacts(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".csf-subtitle-") {
			t.Fatalf("subtitle staging artifact was not cleaned up: %s", entry.Name())
		}
	}
}

func TestWriteSubtitleFileAtomicallyReplacesReadOnlyTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide the POSIX rename and permission semantics exercised here")
	}

	root := t.TempDir()
	targetPath := filepath.Join(root, "Episode.zh.srt")
	if err := os.WriteFile(targetPath, []byte("old subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(targetPath, 0o444); err != nil {
		t.Fatal(err)
	}

	// A non-root process cannot truncate the existing inode even though it can
	// create files in the containing directory. This is equivalent to an old
	// 0644 subtitle owned by another UID on the media share.
	if os.Geteuid() != 0 {
		legacyFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_TRUNC, 0)
		if err == nil {
			_ = legacyFile.Close()
			t.Fatal("expected direct overwrite of read-only target to fail")
		}
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("expected permission error from direct overwrite, got %v", err)
		}
	}
	probePath := filepath.Join(root, ".directory-write-probe")
	if err := os.WriteFile(probePath, nil, 0o600); err != nil {
		t.Fatalf("test requires a writable containing directory: %v", err)
	}
	if err := os.Remove(probePath); err != nil {
		t.Fatal(err)
	}

	want := []byte("new subtitle")
	if err := writeSubtitleFileAtomically(targetPath, want); err != nil {
		t.Fatalf("replace read-only subtitle: %v", err)
	}

	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("subtitle contents = %q, want %q", got, want)
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o644 {
		t.Fatalf("subtitle mode = %04o, want owner-writable mode 0644", gotMode)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".csf-subtitle-") {
			t.Fatalf("temporary file was not cleaned up: %s", entry.Name())
		}
	}
}

func TestWriteSubtitleFileAtomicallyCleansTempAfterRenameFailure(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "existing-directory.srt")
	if err := os.Mkdir(targetPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := writeSubtitleFileAtomically(targetPath, []byte("subtitle")); err == nil {
		t.Fatal("expected replacing a directory to fail")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".csf-subtitle-") {
			t.Fatalf("temporary file was not cleaned up: %s", entry.Name())
		}
	}
}

func TestDemoteSubtitleMarkersRejectsSnapshotFromAnotherVideoTransaction(t *testing.T) {
	root := t.TempDir()
	videoA := filepath.Join(root, "Movie.mkv")
	videoB := filepath.Join(root, "Movie Extended.mkv")
	markedA := filepath.Join(root, "Movie.zh.default.srt")
	for path, contents := range map[string][]byte{
		videoA:  nil,
		videoB:  nil,
		markedA: []byte("A default must stay marked"),
	} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	helper := NewSaveSubHelper(logrus.New(), nil, nil)
	var snapshots []VideoSubtitleMarkerSnapshot
	if err := helper.WithVideoWriteLock(videoA, func(writer *VideoWriteTransaction) error {
		var snapshotErr error
		snapshots, snapshotErr = writer.SnapshotSubtitleMarkers()
		return snapshotErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if err := helper.WithVideoWriteLock(videoB, func(writer *VideoWriteTransaction) error {
		writer.DemoteSubtitleMarkers(snapshots)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(markedA); err != nil || string(got) != "A default must stay marked" {
		t.Fatalf("foreign transaction changed A marker: contents=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Movie.zh.srt")); !os.IsNotExist(err) {
		t.Fatalf("foreign transaction created A unmarked subtitle or stat failed: %v", err)
	}
}
