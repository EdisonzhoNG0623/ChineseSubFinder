package v1

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestBuildScheduleViewUsesOnlyAuthoritativeQueueState(t *testing.T) {
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(20 * time.Minute)
	jobs := []taskQueueTypes.OneJob{
		{Id: "active", VideoName: "Episode 2.mkv", VideoType: common.Series, JobStatus: taskQueueTypes.Downloading, UpdateTime: emby.Time(now.Add(-time.Minute))},
	}
	view := buildScheduleView("running", now.Add(time.Hour), 1, backendTypes.QueueSummary{
		Downloading: 1, EarliestRetryAt: &retryAt, LastCompletedAt: timePointer(now.Add(-10 * time.Minute)),
	}, jobs, retryAt, true, now)

	if view.Phase != "DOWNLOADING" || len(view.CurrentJobs) != 1 || view.CurrentJobs[0].ID != "active" {
		t.Fatalf("unexpected current runtime view: %+v", view)
	}
	if view.LastSuccessAt != nil {
		t.Fatalf("task update time must not impersonate a successful save: %v", view.LastSuccessAt)
	}
	if view.LastCycleAt != nil {
		t.Fatalf("untracked cycle must remain absent, got %v", view.LastCycleAt)
	}
	if view.NextActionAt == nil || !view.NextActionAt.Equal(now) {
		t.Fatalf("active work should be due now, got %v", view.NextActionAt)
	}
}

func TestLatestSupplierSaveAtUsesAuthoritativeSaveEvents(t *testing.T) {
	now := time.Now()
	got := latestSupplierSaveAt(map[string]subtitle_metrics.SupplierRuntime{
		"older": {LastSaveAt: now.Add(-time.Hour)},
		"newer": {LastSaveAt: now.Add(-time.Minute)},
	})
	if got == nil || !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("latest save = %v", got)
	}
	if got = latestSupplierSaveAt(map[string]subtitle_metrics.SupplierRuntime{}); got != nil {
		t.Fatalf("empty save evidence = %v, want nil", got)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestBuildScheduleViewChoosesEarliestKnownFutureAction(t *testing.T) {
	now := time.Now()
	retryAt := now.Add(20 * time.Minute)
	view := buildScheduleView("running", now.Add(time.Hour), 0, backendTypes.QueueSummary{
		RetryScheduled: 1, BackoffWaiting: 1, EarliestRetryAt: &retryAt,
	}, nil, retryAt, true, now)
	if view.Phase != "BACKOFF" || view.NextActionAt == nil || !view.NextActionAt.Equal(retryAt) {
		t.Fatalf("unexpected backoff view: %+v", view)
	}
}

func TestBuildScheduleViewUsesQueueIndexForCompletedRefresh(t *testing.T) {
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	refreshAt := now.Add(30 * time.Minute)
	view := buildScheduleView("running", now.Add(time.Hour), 0, backendTypes.QueueSummary{}, nil,
		refreshAt, true, now)
	if view.Phase != "SCHEDULED" || view.NextActionAt == nil || !view.NextActionAt.Equal(refreshAt) {
		t.Fatalf("future completed-task refresh was omitted: %+v", view)
	}

	view = buildScheduleView("running", now.Add(time.Hour), 0, backendTypes.QueueSummary{}, nil,
		now, true, now)
	if view.Phase != "READY" || view.NextActionAt == nil || !view.NextActionAt.Equal(now) {
		t.Fatalf("due completed-task refresh was not ready: %+v", view)
	}
}
