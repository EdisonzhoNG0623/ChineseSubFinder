package task_queue

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestMain(m *testing.M) {
	configRoot, err := os.MkdirTemp("", "csf-task-queue-test-")
	if err != nil {
		panic(err)
	}
	settings.SetConfigRootPath(configRoot)
	code := m.Run()
	_ = os.RemoveAll(configRoot)
	os.Exit(code)
}

func TestRetryDelayClasses(t *testing.T) {
	tests := []struct {
		name     string
		error    string
		attempts int
		want     time.Duration
	}{
		{name: "no subtitle first", error: "No Sub Found", attempts: 1, want: 12 * time.Hour},
		{name: "no subtitle second", error: "No Sub Found", attempts: 2, want: 2 * 24 * time.Hour},
		{name: "no subtitle third", error: "No Sub Found", attempts: 3, want: 7 * 24 * time.Hour},
		{name: "all suppliers empty", error: "all site download sub not found", attempts: 4, want: 30 * 24 * time.Hour},
		{name: "empty supplier result", error: "No Sub Downloaded.", attempts: 2, want: 2 * 24 * time.Hour},
		{name: "no subtitle capped", error: "No Sub Found", attempts: 100, want: 30 * 24 * time.Hour},
		{name: "transient first", error: "context deadline exceeded", attempts: 1, want: 30 * time.Minute},
		{name: "transient capped", error: "connection reset by peer", attempts: 100, want: 6 * time.Hour},
		{name: "persistent capped", error: "no metadata file", attempts: 100, want: 7 * 24 * time.Hour},
		{name: "unknown capped", error: "unexpected supplier response", attempts: 100, want: 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := taskQueue2.OneJob{ErrorInfo: tt.error, DownloadTimes: tt.attempts}
			if got := retryDelay(job); got != tt.want {
				t.Fatalf("retryDelay() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextAttemptAtHonorsForceRunAndPersistedSchedule(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := taskQueue2.OneJob{
		DownloadTimes:   4,
		UpdateTime:      emby.Time(now.Add(-time.Hour)),
		NextAttemptTime: emby.Time(now.Add(12 * time.Hour)),
	}
	if got := nextAttemptAt(job); !got.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("nextAttemptAt() = %v", got)
	}

	job.ForceRun = true
	if got := nextAttemptAt(job); !got.IsZero() {
		t.Fatalf("forced job should be ready immediately, got %v", got)
	}
}

func TestNextAttemptAtAppliesNewNoSubMinimumToOldSchedule(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := taskQueue2.OneJob{
		DownloadTimes:   4,
		ErrorInfo:       "all site download sub not found",
		UpdateTime:      emby.Time(now),
		NextAttemptTime: emby.Time(now.Add(8 * time.Hour)),
	}
	if got := nextAttemptAt(job); !got.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Fatalf("nextAttemptAt() = %v, want current no-sub minimum", got)
	}
}

func TestQueueRetryLifecycleAndBackoff(t *testing.T) {
	const queueName = "task_queue_retry_policy_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	job := taskQueue2.OneJob{
		Id:           "retry-job",
		VideoType:    common.Movie,
		VideoFPath:   "/media/movie.mkv",
		JobStatus:    taskQueue2.Waiting,
		TaskPriority: DefaultTaskPriorityLevel,
		AddedTime:    emby.Time(time.Now()),
		UpdateTime:   emby.Time(time.Now()),
	}
	if ok, err := queue.Add(job); err != nil || !ok {
		t.Fatalf("Add() = %v, %v", ok, err)
	}

	queue.AutoDetectUpdateJobStatus(job, ErrNoSubFound)
	ok, current := queue.GetOneJobByID(job.Id)
	if !ok {
		t.Fatal("job missing after failure")
	}
	if current.JobStatus != taskQueue2.Waiting || current.TaskPriority != FirstRetryTaskPriorityLevel || current.RetryTimes != 1 {
		t.Fatalf("unexpected first retry state: %+v", current)
	}
	if !time.Time(current.NextAttemptTime).After(time.Now()) {
		t.Fatalf("next attempt was not scheduled: %v", current.NextAttemptTime)
	}
	if found, _, err := queue.GetOneWaitingJob(); err != nil || found {
		t.Fatalf("backed-off job selected: found=%v err=%v", found, err)
	}

	// Explicit user action bypasses the schedule once.
	current.ForceRun = true
	if ok, err := queue.Update(current); err != nil || !ok {
		t.Fatalf("Update(force) = %v, %v", ok, err)
	}
	found, forced, err := queue.GetOneWaitingJob()
	if err != nil || !found || forced.Id != job.Id {
		t.Fatalf("forced job not selected: found=%v job=%+v err=%v", found, forced, err)
	}
	forced.ForceRun = false
	queue.AutoDetectUpdateJobStatus(forced, errors.New("temporary network timeout"))

	_, current = queue.GetOneJobByID(job.Id)
	current.ForceRun = true
	queue.Update(current)
	queue.AutoDetectUpdateJobStatus(current, errors.New("temporary network timeout"))
	_, current = queue.GetOneJobByID(job.Id)
	if current.TaskPriority != FirstRetryTaskPriorityLevel+1 || current.RetryTimes != 0 {
		t.Fatalf("job was not degraded after max retries: %+v", current)
	}

	queue.AutoDetectUpdateJobStatus(current, nil)
	_, current = queue.GetOneJobByID(job.Id)
	if current.JobStatus != taskQueue2.Done || current.RetryTimes != 0 || current.ErrorInfo != "" || !time.Time(current.NextAttemptTime).IsZero() {
		t.Fatalf("success did not clear retry state: %+v", current)
	}
}

func TestQueueRejectsBDMVStreamSegments(t *testing.T) {
	const queueName = "task_queue_bdmv_filter_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	job := taskQueue2.OneJob{Id: "bdmv", VideoFPath: "/media/Movie/BDMV/STREAM/00001.m2ts"}
	if ok, err := queue.Add(job); err != nil || ok {
		t.Fatalf("BDMV stream Add() = %v, %v", ok, err)
	}
	if queue.Size() != 0 {
		t.Fatalf("BDMV stream segment entered queue")
	}
}

func TestQueueStartupMigration(t *testing.T) {
	const queueName = "task_queue_startup_migration_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	now := time.Now()
	jobs := map[string]taskQueue2.OneJob{
		"bdmv": {
			Id: "bdmv", VideoFPath: "/media/Movie/BDMV/STREAM/00001.m2ts",
			JobStatus: taskQueue2.Waiting, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now),
		},
		"interrupted": {
			Id: "interrupted", VideoFPath: "/media/interrupted.mkv",
			JobStatus: taskQueue2.Downloading, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now),
		},
		"legacy-retry": {
			Id: "legacy-retry", VideoFPath: "/media/legacy-retry.mkv",
			JobStatus: taskQueue2.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
			DownloadTimes: 2, ErrorInfo: ErrNoSubFound.Error(),
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now),
		},
	}
	payload, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	center := cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester())
	if err = center.TaskQueueSave(DefaultTaskPriorityLevel, payload); err != nil {
		t.Fatal(err)
	}

	queue := NewTaskQueue(center)
	t.Cleanup(queue.Close)
	_, bdmv := queue.GetOneJobByID("bdmv")
	if bdmv.JobStatus != taskQueue2.Ignore || bdmv.ErrorInfo != "ignored BDMV stream segment" {
		t.Fatalf("BDMV startup migration failed: %+v", bdmv)
	}
	_, interrupted := queue.GetOneJobByID("interrupted")
	if interrupted.JobStatus != taskQueue2.Waiting || interrupted.DownloadTimes != 1 ||
		interrupted.ErrorInfo != "download interrupted by process restart" ||
		!time.Time(interrupted.NextAttemptTime).After(now) {
		t.Fatalf("interrupted job recovery failed: %+v", interrupted)
	}
	_, legacyRetry := queue.GetOneJobByID("legacy-retry")
	if !time.Time(legacyRetry.NextAttemptTime).After(now) {
		t.Fatalf("legacy retry schedule was not persisted: %+v", legacyRetry)
	}
}

func TestExpiredWaitingJobBecomesTerminalBeforeSelection(t *testing.T) {
	const queueName = "task_queue_expired_waiting_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	expirationDays := settings.Get().AdvancedSettings.TaskQueue.ExpirationTime
	expired := taskQueue2.OneJob{
		Id: "expired-waiting", VideoFPath: "/expired.mkv",
		JobStatus: taskQueue2.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		DownloadTimes: 76, RetryTimes: 1, ErrorInfo: ErrNoSubFound.Error(),
		AddedTime:  emby.Time(now.AddDate(0, 0, -expirationDays-1)),
		UpdateTime: emby.Time(now),
	}
	if ok, err := queue.Add(expired); err != nil || !ok {
		t.Fatalf("Add(expired) = %v, %v", ok, err)
	}

	queue.BeforeGetOneJob()
	_, current := queue.GetOneJobByID(expired.Id)
	if current.JobStatus != taskQueue2.Failed || current.DownloadTimes != expired.DownloadTimes ||
		!time.Time(current.NextAttemptTime).IsZero() {
		t.Fatalf("expired waiting job was not terminalized without another attempt: %+v", current)
	}
	if found, _, err := queue.GetOneWaitingJob(); err != nil || found {
		t.Fatalf("expired waiting job remained selectable: found=%v err=%v", found, err)
	}
}

func TestForcedExpiredWaitingJobBypassesRetryLifetime(t *testing.T) {
	const queueName = "task_queue_forced_expired_waiting_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	expirationDays := settings.Get().AdvancedSettings.TaskQueue.ExpirationTime
	forced := taskQueue2.OneJob{
		Id: "forced-expired", VideoFPath: "/forced-expired.mkv",
		JobStatus: taskQueue2.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		DownloadTimes: 76, RetryTimes: 1, ErrorInfo: ErrNoSubFound.Error(), ForceRun: true,
		AddedTime:  emby.Time(now.AddDate(0, 0, -expirationDays-1)),
		UpdateTime: emby.Time(now),
	}
	if ok, err := queue.Add(forced); err != nil || !ok {
		t.Fatalf("Add(forced expired) = %v, %v", ok, err)
	}

	queue.BeforeGetOneJob()
	_, current := queue.GetOneJobByID(forced.Id)
	if current.JobStatus != taskQueue2.Waiting || !current.ForceRun {
		t.Fatalf("forced expired job was terminalized: %+v", current)
	}
	found, selected, err := queue.GetOneWaitingJob()
	if err != nil || !found || selected.Id != forced.Id {
		t.Fatalf("forced expired job not selected: found=%v job=%+v err=%v", found, selected, err)
	}
}

func TestQueueSelectsOldestEligibleWithinPriority(t *testing.T) {
	const queueName = "task_queue_fairness_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	newer := taskQueue2.OneJob{Id: "newer", VideoFPath: "/newer.mkv", JobStatus: taskQueue2.Waiting, TaskPriority: 5, AddedTime: emby.Time(now)}
	older := taskQueue2.OneJob{Id: "older", VideoFPath: "/older.mkv", JobStatus: taskQueue2.Waiting, TaskPriority: 5, AddedTime: emby.Time(now.Add(-time.Hour))}
	queue.Add(newer)
	queue.Add(older)

	found, got, err := queue.GetOneWaitingJob()
	if err != nil || !found || got.Id != older.Id {
		t.Fatalf("GetOneWaitingJob() = found=%v id=%s err=%v", found, got.Id, err)
	}
}

func TestQueueSelectsOldestDoneJobForRefresh(t *testing.T) {
	const queueName = "task_queue_done_fairness_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	newer := taskQueue2.OneJob{
		Id: "done-newer", VideoFPath: "/done-newer.mkv", JobStatus: taskQueue2.Done, TaskPriority: 5,
		AddedTime: emby.Time(now.Add(-48 * time.Hour)), CreatedTime: emby.Time(now.Add(-24 * time.Hour)),
		UpdateTime: emby.Time(now.Add(-13 * time.Hour)),
	}
	older := taskQueue2.OneJob{
		Id: "done-older", VideoFPath: "/done-older.mkv", JobStatus: taskQueue2.Done, TaskPriority: 5,
		AddedTime: emby.Time(now.Add(-72 * time.Hour)), CreatedTime: emby.Time(now.Add(-48 * time.Hour)),
		UpdateTime: emby.Time(now.Add(-24 * time.Hour)),
	}
	queue.Add(newer)
	queue.Add(older)

	found, got, err := queue.GetOneDoneJob()
	if err != nil || !found || got.Id != older.Id {
		t.Fatalf("GetOneDoneJob() = found=%v id=%s err=%v", found, got.Id, err)
	}
}
