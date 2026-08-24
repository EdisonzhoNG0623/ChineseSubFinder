package task_queue

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestClassifyErrorInfo(t *testing.T) {
	tests := map[string]ErrorCategory{
		"":                          ErrorCategoryNone,
		"No Sub Found":              ErrorCategoryNoSubtitle,
		"context deadline exceeded": ErrorCategoryTransient,
		"permission denied":         ErrorCategoryLocal,
		"subhd page blocked by site verification": ErrorCategoryBlocked,
		"unexpected supplier response":            ErrorCategoryUnknown,
	}
	for message, want := range tests {
		if got := ClassifyErrorInfo(message); got != want {
			t.Fatalf("ClassifyErrorInfo(%q) = %s, want %s", message, got, want)
		}
	}
}

func TestDiagnoseRetry(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.Local)
	job := taskQueueTypes.OneJob{
		JobStatus:       taskQueueTypes.Waiting,
		ErrorInfo:       ErrNoSubFound.Error(),
		DownloadTimes:   1,
		UpdateTime:      emby.Time(now),
		NextAttemptTime: emby.Time(now.Add(12 * time.Hour)),
	}
	got := DiagnoseRetry(job, now)
	if !got.IsScheduled || got.IsReady || got.RetryInSeconds != int64((12*time.Hour)/time.Second) {
		t.Fatalf("unexpected retry diagnostic: %+v", got)
	}
}
