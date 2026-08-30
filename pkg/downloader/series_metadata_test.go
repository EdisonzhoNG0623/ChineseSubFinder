package downloader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLegacySeriesEpisodeFallsBackToExplicitFilename(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name        string
		wantSeason  int
		wantEpisode int
	}{
		{name: "Special - S00E23.mkv", wantSeason: 0, wantEpisode: 23},
		{name: "Show - S03E10.mp4", wantSeason: 3, wantEpisode: 10},
		{name: "Recap - S04E00.mp4", wantSeason: 4, wantEpisode: 0},
	}
	for _, test := range tests {
		videoPath := filepath.Join(dir, test.name)
		if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		season, episode, source, err := resolveLegacySeriesEpisode(videoPath)
		if err != nil {
			t.Fatalf("%q: %v", test.name, err)
		}
		if season != test.wantSeason || episode != test.wantEpisode || source != legacyEpisodeSourceFilename {
			t.Fatalf("%q = (%d, %d, %q), want (%d, %d, %q)", test.name, season, episode, source, test.wantSeason, test.wantEpisode, legacyEpisodeSourceFilename)
		}
	}
}

func TestResolveLegacySeriesEpisodeRejectsUnnumberedFile(t *testing.T) {
	videoPath := filepath.Join(t.TempDir(), "Unnumbered Episode.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := resolveLegacySeriesEpisode(videoPath); err == nil {
		t.Fatal("expected missing metadata error")
	}
}
