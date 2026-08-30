package save_sub_helper

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
