package task_queue

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestStartupDeduplicatesStalePriorityCopies(t *testing.T) {
	const queueName = "task_queue_startup_dedup_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	center := cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester())
	jobID := "duplicate-job"
	now := time.Now().Truncate(time.Second)

	stale := *taskQueueTypes.NewOneJob(common.Series, "/media/series/S01E01.mkv", HighTaskPriorityLevel)
	stale.Id = jobID
	stale.SeriesRootDirPath = "/media/series"
	stale.JobStatus = taskQueueTypes.Downloading
	stale.UpdateTime = emby.Time(now.Add(-time.Hour))

	current := stale
	current.TaskPriority = DefaultTaskPriorityLevel
	current.JobStatus = taskQueueTypes.Waiting
	current.UpdateTime = emby.Time(now)

	saveBucket := func(priority int, job taskQueueTypes.OneJob) {
		t.Helper()
		data, err := json.Marshal(map[string]taskQueueTypes.OneJob{job.Id: job})
		if err != nil {
			t.Fatal(err)
		}
		if err := center.TaskQueueSave(priority, data); err != nil {
			t.Fatal(err)
		}
	}
	saveBucket(HighTaskPriorityLevel, stale)
	saveBucket(DefaultTaskPriorityLevel, current)

	queue := NewTaskQueue(center)
	defer queue.Close()

	found, got := queue.GetOneJobByID(jobID)
	if !found {
		t.Fatal("deduplicated job is missing")
	}
	if got.JobStatus != taskQueueTypes.Waiting || got.TaskPriority != DefaultTaskPriorityLevel {
		t.Fatalf("kept stale copy: status=%d priority=%d", got.JobStatus, got.TaskPriority)
	}

	persisted, err := center.TaskQueueRead()
	if err != nil {
		t.Fatal(err)
	}
	var staleBucket map[string]taskQueueTypes.OneJob
	if err := json.Unmarshal(persisted[HighTaskPriorityLevel], &staleBucket); err != nil {
		t.Fatal(err)
	}
	if _, exists := staleBucket[jobID]; exists {
		t.Fatal("stale priority copy was not removed from persistent storage")
	}
}
