package task_queue

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestMarkSeriesEpisodesDoneBatchesOnlySavedEpisodes(t *testing.T) {
	const queueName = "task_queue_collection_backfill_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	seriesRoot := "/media/Bleach"
	jobs := []taskQueue2.OneJob{
		collectionQueueJob("episode-1", seriesRoot, 1, taskQueue2.Waiting, 6),
		collectionQueueJob("episode-2", seriesRoot, 2, taskQueue2.Failed, 7),
		collectionQueueJob("episode-3", seriesRoot, 3, taskQueue2.Waiting, 6),
		collectionQueueJob("current-job", seriesRoot, 4, taskQueue2.Waiting, 3),
	}
	jobs[1].ForceRun = true
	jobs[1].RetryTimes = 2
	jobs[1].ErrorInfo = "old failure"
	jobs[1].NextAttemptTime = emby.Time(time.Now().Add(time.Hour))
	for _, job := range jobs {
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
	}
	seriesJobs := queue.GetSeriesJobs(seriesRoot)
	if len(seriesJobs) != len(jobs) {
		t.Fatalf("GetSeriesJobs returned %d jobs, want %d", len(seriesJobs), len(jobs))
	}
	seriesJobs[0].VideoFPath = "/mutated-copy.mkv"
	_, storedJob := queue.GetOneJobByID(seriesJobs[0].Id)
	if storedJob.VideoFPath == seriesJobs[0].VideoFPath {
		t.Fatal("GetSeriesJobs exposed mutable queue state")
	}

	marked, err := queue.MarkSeriesEpisodesDone(seriesRoot, map[string]struct{}{
		pkg.GetEpisodeKeyName(1, 1): {},
		pkg.GetEpisodeKeyName(1, 2): {},
		pkg.GetEpisodeKeyName(1, 4): {},
	}, "current-job")
	if err != nil {
		t.Fatal(err)
	}
	if marked != 2 {
		t.Fatalf("marked = %d, want 2", marked)
	}

	for _, id := range []string{"episode-1", "episode-2"} {
		found, job := queue.GetOneJobByID(id)
		if !found || job.JobStatus != taskQueue2.Done || job.TaskPriority != DefaultTaskPriorityLevel {
			t.Fatalf("backfilled job %s not completed: %+v", id, job)
		}
		if job.RetryTimes != 0 || job.ErrorInfo != "" || job.ForceRun || !time.Time(job.NextAttemptTime).IsZero() {
			t.Fatalf("backfilled job %s retained retry state: %+v", id, job)
		}
	}
	for _, id := range []string{"episode-3", "current-job"} {
		_, job := queue.GetOneJobByID(id)
		if job.JobStatus != taskQueue2.Waiting {
			t.Fatalf("unrelated/excluded job %s changed: %+v", id, job)
		}
	}
}

func collectionQueueJob(id, seriesRoot string, episode int, status taskQueue2.JobStatus, priority int) taskQueue2.OneJob {
	now := emby.Time(time.Now())
	return taskQueue2.OneJob{
		Id:                id,
		VideoType:         common.Series,
		VideoFPath:        seriesRoot + "/episode.mkv",
		SeriesRootDirPath: seriesRoot,
		Season:            1,
		Episode:           episode,
		JobStatus:         status,
		TaskPriority:      priority,
		AddedTime:         now,
		UpdateTime:        now,
	}
}
