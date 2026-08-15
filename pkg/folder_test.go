package pkg

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestClearIdleSubFixCacheFolder(t *testing.T) {
	root := t.TempDir()
	oldFolder := filepath.Join(root, "old")
	newFolder := filepath.Join(root, "new")
	oldNested := filepath.Join(oldFolder, "nested")
	if err := os.MkdirAll(oldNested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newFolder, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldNested, "video.aac")
	newFile := filepath.Join(newFolder, "video.aac")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	for _, path := range []string{oldFile, oldNested, oldFolder} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	if err := ClearIdleSubFixCacheFolder(logrus.New(), root, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if IsDir(oldFolder) {
		t.Fatal("old cache folder was not removed")
	}
	if !IsDir(newFolder) {
		t.Fatal("recent cache folder was removed")
	}
}
