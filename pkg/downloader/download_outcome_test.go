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

type controlledContext struct {
	done chan struct{}
	err  error
}

func newControlledContext() *controlledContext {
	return &controlledContext{done: make(chan struct{})}
}

func (c *controlledContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *controlledContext) Done() <-chan struct{}       { return c.done }
func (c *controlledContext) Err() error                  { return c.err }
func (c *controlledContext) Value(interface{}) interface{} {
	return nil
}
func (c *controlledContext) finish(err error) {
	c.err = err
	close(c.done)
}

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

func TestRunSubtitleSaveWithContextWaitsForSaveAfterCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		returned <- runSubtitleSaveWithContext(ctx, func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	cancel()

	select {
	case got := <-returned:
		close(release)
		t.Fatalf("wrapper returned before the real save stopped: %v", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case got := <-returned:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("runSubtitleSaveWithContext() = %v, want context cancellation after save joined", got)
		}
	case <-time.After(time.Second):
		t.Fatal("wrapper did not return after the real save stopped")
	}
}

func TestRunSubtitleSaveWithContextKeepsSuccessfulSaveAfterDeadline(t *testing.T) {
	ctx := newControlledContext()
	called := false
	got := runSubtitleSaveWithContext(ctx, func() error {
		called = true
		ctx.finish(context.DeadlineExceeded)
		return nil
	})
	if !called || got != nil {
		t.Fatalf("deadline-crossing successful save called=%t err=%v, want success", called, got)
	}
}

func TestRunSubtitleSaveWithContextKeepsSaveErrorAfterDeadline(t *testing.T) {
	ctx := newControlledContext()
	want := errors.New("save failed after deadline")
	got := runSubtitleSaveWithContext(ctx, func() error {
		ctx.finish(context.DeadlineExceeded)
		return want
	})
	if !errors.Is(got, want) {
		t.Fatalf("runSubtitleSaveWithContext() = %v, want save error %v", got, want)
	}
}

func TestRunSubtitleSaveWithContextKeepsSaveErrorAfterCancellation(t *testing.T) {
	ctx := newControlledContext()
	want := errors.New("save failed during shutdown")
	got := runSubtitleSaveWithContext(ctx, func() error {
		ctx.finish(context.Canceled)
		return want
	})
	if !errors.Is(got, want) {
		t.Fatalf("runSubtitleSaveWithContext() = %v, want save error %v", got, want)
	}
}

func TestRunSubtitleSaveWithContextSkipsSaveWhenDeadlineAlreadyExceeded(t *testing.T) {
	ctx := newControlledContext()
	ctx.finish(context.DeadlineExceeded)
	called := false
	got := runSubtitleSaveWithContext(ctx, func() error {
		called = true
		return nil
	})
	if called || !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("already-expired save called=%t err=%v", called, got)
	}
}

func TestRunSubtitleSaveWithContextSkipsSaveWhenAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	got := runSubtitleSaveWithContext(ctx, func() error {
		called = true
		return nil
	})
	if called || !errors.Is(got, context.Canceled) {
		t.Fatalf("already-canceled save called=%t err=%v", called, got)
	}
}

func TestWaitQueueWorkerResultJoinsBlockingSaveOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	workerDone := make(chan queueWorkerResult, 1)
	go func() {
		workerDone <- queueWorkerResult{err: runSubtitleSaveWithContext(ctx, func() error {
			close(started)
			<-release
			return nil
		})}
	}()
	<-started

	cancelObserved := make(chan struct{})
	waitReturned := make(chan queueWorkerResult, 1)
	go func() {
		result, canceled := waitQueueWorkerResult(ctx, workerDone, func() { close(cancelObserved) })
		if !canceled {
			result.err = errors.New("worker wait did not observe cancellation")
		}
		waitReturned <- result
	}()
	cancel()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("worker wait did not observe cancellation")
	}

	select {
	case result := <-waitReturned:
		close(release)
		t.Fatalf("worker wait returned before subtitle save stopped: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case result := <-waitReturned:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("joined worker result = %v, want context cancellation", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker wait did not return after subtitle save stopped")
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

func TestMappedCollectionEpisodesUsesCompleteArchiveInventory(t *testing.T) {
	info := &series.SeriesInfo{
		EpList:        []series.EpisodeInfo{{Season: 1, Episode: 29}},
		ArchiveEpList: []series.EpisodeInfo{{Season: 1, Episode: 1}, {Season: 1, Episode: 29}},
	}
	got := mappedCollectionEpisodes(info, map[string][]string{
		"S1E1":  {"/cache/01.srt"},
		"S1E29": {"/cache/29.srt"},
	})
	if len(got) != 2 || len(got["S1E1"]) != 1 || len(got["S1E29"]) != 1 {
		t.Fatalf("complete archive inventory was not retained: %#v", got)
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
