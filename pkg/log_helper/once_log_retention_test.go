package log_helper

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrimOnceLogsKeepsNewestByModTime(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	files := []struct {
		name string
		age  time.Duration
	}{
		{name: "Once-z-oldest.log", age: 30 * time.Minute},
		{name: "Once-a-middle.log", age: 20 * time.Minute},
		{name: "Once-m-newest.log", age: 10 * time.Minute},
	}

	matches := make([]string, 0, len(files))
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, []byte(file.name), 0o644); err != nil {
			t.Fatal(err)
		}
		stamp := base.Add(-file.age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		matches = append(matches, path)
	}

	trimOnceLogs(matches, 2)

	if _, err := os.Stat(filepath.Join(dir, "Once-z-oldest.log")); !os.IsNotExist(err) {
		t.Fatalf("oldest log should be removed, stat error = %v", err)
	}
	for _, name := range []string{"Once-a-middle.log", "Once-m-newest.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("newer log %s should remain: %v", name, err)
		}
	}
}

func TestTrimOnceLogsHonorsZeroLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Once-job.log")
	if err := os.WriteFile(path, []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	trimOnceLogs([]string{path}, 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("zero limit should remove all readable logs, stat error = %v", err)
	}
}
