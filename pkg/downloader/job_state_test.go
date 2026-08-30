package downloader

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestMarkJobIgnoredClearsStaleDiagnosticState(t *testing.T) {
	job := taskQueueTypes.OneJob{
		JobStatus:       taskQueueTypes.Waiting,
		ErrorInfo:       "series metadata episode not found",
		RetryTimes:      3,
		NextAttemptTime: emby.Time(time.Now().Add(time.Hour)),
		ForceRun:        true,
	}

	markJobIgnored(&job)

	if job.JobStatus != taskQueueTypes.Ignore || job.ErrorInfo != "" || job.RetryTimes != 0 ||
		!time.Time(job.NextAttemptTime).IsZero() || job.ForceRun {
		t.Fatalf("ignored job retained stale state: %+v", job)
	}
}
