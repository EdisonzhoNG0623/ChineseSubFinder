package models

import (
	"path/filepath"
	"testing"
)

func TestGenerateUID4VideoPathIsStableAndDomainSeparated(t *testing.T) {
	const videoPath = "Feature.mkv"
	const expectedUID = "65087c86e99b0c2eb607779643b6cc495fb3000ec467222c3158ef49420a3492"

	exactUID := GenerateUID4VideoPath(videoPath)
	if exactUID != expectedUID {
		t.Fatalf("exact video UID changed: got %q, want %q", exactUID, expectedUID)
	}
	if got := NewSkipScanInfoByVideoPath(videoPath, true); got.UID != exactUID || !got.Skip {
		t.Fatalf("NewSkipScanInfoByVideoPath() = %+v, want UID %q with skip=true", got, exactUID)
	}

	legacyUIDs := map[string]string{
		"movie":  GenerateUID4Movie(videoPath),
		"series": GenerateUID4Series(filepath.Dir(videoPath), 1, 2),
	}
	for domain, legacyUID := range legacyUIDs {
		if exactUID == legacyUID {
			t.Fatalf("exact video UID collided with legacy %s UID %q", domain, legacyUID)
		}
	}
}

func TestGenerateUID4VideoPathCanonicalizesEquivalentPaths(t *testing.T) {
	canonical := filepath.Join("library", "Show", "Season 01", "Episode.S01E02.mkv")
	redundant := filepath.Join("library", "Show", "Season 01", "..", "Season 01", ".", "Episode.S01E02.mkv")

	if got, want := GenerateUID4VideoPath(redundant), GenerateUID4VideoPath(canonical); got != want {
		t.Fatalf("equivalent paths produced different exact UIDs: got %q, want %q", got, want)
	}

	sibling := filepath.Join("library", "Show", "Season 01", "Episode.S01E03.mkv")
	if GenerateUID4VideoPath(sibling) == GenerateUID4VideoPath(canonical) {
		t.Fatal("different exact video paths produced the same UID")
	}
}

func TestNewSkipScanInfoBySeriesExDoesNotPanicForUnparseablePath(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("NewSkipScanInfoBySeriesEx panicked for an unparseable path: %v", recovered)
		}
	}()

	info := NewSkipScanInfoBySeriesEx(filepath.Join("library", "Show", "not-an-episode"), true)
	if info == nil {
		t.Fatal("NewSkipScanInfoBySeriesEx returned nil")
	}
	if len(info.UID) != 64 {
		t.Fatalf("NewSkipScanInfoBySeriesEx returned malformed UID %q", info.UID)
	}
	if !info.Skip {
		t.Fatal("NewSkipScanInfoBySeriesEx did not preserve skip=true")
	}
}
