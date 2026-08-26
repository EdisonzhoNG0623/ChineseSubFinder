package downloader

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
)

func TestSeriesDownloadOutcomeError(t *testing.T) {
	saveErr := errors.New("write failed")
	tests := []struct {
		name      string
		saved     int
		saveErr   error
		wantError error
	}{
		{name: "empty result", saved: 0, wantError: task_queue.ErrNoSubFound},
		{name: "write failure", saved: 0, saveErr: saveErr, wantError: saveErr},
		{name: "saved subtitle", saved: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := seriesDownloadOutcomeError(test.saved, test.saveErr); !errors.Is(got, test.wantError) {
				t.Fatalf("seriesDownloadOutcomeError(%d, %v) = %v, want %v", test.saved, test.saveErr, got, test.wantError)
			}
		})
	}
}

func TestRunSubtitleSaveWithContextReturnsOperationResult(t *testing.T) {
	want := errors.New("save failed")
	if got := runSubtitleSaveWithContext(context.Background(), func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("runSubtitleSaveWithContext() = %v, want %v", got, want)
	}
}

func TestRunSubtitleSaveWithContextConvertsPanicToError(t *testing.T) {
	got := runSubtitleSaveWithContext(context.Background(), func() error { panic("broken save") })
	if got == nil || !strings.Contains(got.Error(), "broken save") {
		t.Fatalf("runSubtitleSaveWithContext() = %v, want recovered panic", got)
	}
}

func TestRunSubtitleSaveWithContextReturnsOnCancellation(t *testing.T) {
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	got := runSubtitleSaveWithContext(ctx, func() error {
		<-release
		return nil
	})
	close(release)
	if !errors.Is(got, context.DeadlineExceeded) || time.Since(startedAt) > time.Second {
		t.Fatalf("cancellation not enforced promptly: elapsed=%s err=%v", time.Since(startedAt), got)
	}
}

func TestFullSeasonEpisodeSubs(t *testing.T) {
	fullSeasonSubs := map[string][]string{
		"S01E01": {"episode.ass"},
		"S01E02": {},
	}

	tests := []struct {
		name      string
		episode   string
		wantFound bool
	}{
		{name: "subtitle exists", episode: "S01E01", wantFound: true},
		{name: "empty subtitle list", episode: "S01E02", wantFound: false},
		{name: "episode missing", episode: "S01E03", wantFound: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, found := fullSeasonEpisodeSubs(fullSeasonSubs, test.episode)
			if found != test.wantFound {
				t.Fatalf("fullSeasonEpisodeSubs(%q) found = %t, want %t", test.episode, found, test.wantFound)
			}
		})
	}
}

func TestMappedCollectionEpisodesKeepsOnlySeriesInventory(t *testing.T) {
	organized := map[string][]string{
		"S4E35": {"/cache/35.ass"},
		"S4E36": {"/cache/36.ass"},
		"S4E99": {"/cache/99.ass"},
		"S4E0":  {"/cache/unresolved.ass"},
	}
	info := &series.SeriesInfo{EpList: []series.EpisodeInfo{
		{Season: 4, Episode: 35},
		{Season: 4, Episode: 36},
	}}

	got := mappedCollectionEpisodes(info, organized)
	if len(got) != 2 || len(got["S4E35"]) != 1 || len(got["S4E36"]) != 1 {
		t.Fatalf("mapped collection = %#v", got)
	}
	if _, exists := got["S4E99"]; exists {
		t.Fatal("episode outside the local series inventory was retained")
	}
}

func TestSeriesRequestedEpisodeOutcome(t *testing.T) {
	saveErr := errors.New("save failed")
	tests := []struct {
		name                  string
		requestedEpisodeSaved bool
		saveErr               error
		wantError             error
	}{
		{name: "requested episode saved", requestedEpisodeSaved: true},
		{name: "only sibling episode saved", wantError: task_queue.ErrNoSubFound},
		{name: "requested episode save failed", saveErr: saveErr, wantError: saveErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := seriesRequestedEpisodeOutcome(test.requestedEpisodeSaved, test.saveErr)
			if !errors.Is(got, test.wantError) {
				t.Fatalf("seriesRequestedEpisodeOutcome(%t, %v) = %v, want %v",
					test.requestedEpisodeSaved, test.saveErr, got, test.wantError)
			}
		})
	}
}
