package downloader

import (
	"errors"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
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
