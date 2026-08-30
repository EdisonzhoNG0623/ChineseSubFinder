package task_queue

import (
	"sort"
	"time"

	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/emirpasic/gods/sets/treeset"
)

// GetSeriesJobs returns a snapshot of all queued jobs for a series. Callers
// may inspect the copies without holding or mutating the queue lock.
func (t *TaskQueue) GetSeriesJobs(seriesRootDirPath string) []taskQueue2.OneJob {
	t.queueLock.Lock()
	defer t.queueLock.Unlock()

	jobIDSet, ok := t.taskGroupBySeries.Get(seriesRootDirPath)
	if !ok {
		return nil
	}

	jobs := make([]taskQueue2.OneJob, 0, jobIDSet.(*treeset.Set).Size())
	for _, oneID := range jobIDSet.(*treeset.Set).Values() {
		jobID := oneID.(string)
		priorityValue, found := t.taskKeyMap.Get(jobID)
		if !found {
			continue
		}
		jobValue, found := t.taskPriorityMapList[priorityValue.(int)].Get(jobID)
		if !found {
			continue
		}
		jobs = append(jobs, jobValue.(taskQueue2.OneJob))
	}
	return jobs
}

// GetReadySeriesJobs returns a bounded, deterministic snapshot of waiting
// episodes that can share one supplier search with the primary episode. It
// never advances a future retry or crosses a season boundary.
func (t *TaskQueue) GetReadySeriesJobs(seriesRootDirPath string, season int, excludeJobID string, limit int, now time.Time) []taskQueue2.OneJob {
	if seriesRootDirPath == "" || season <= 0 || limit <= 0 {
		return nil
	}

	t.queueLock.Lock()
	defer t.queueLock.Unlock()
	jobIDSet, ok := t.taskGroupBySeries.Get(seriesRootDirPath)
	if !ok {
		return nil
	}

	jobs := make([]taskQueue2.OneJob, 0, limit)
	for _, oneID := range jobIDSet.(*treeset.Set).Values() {
		jobID := oneID.(string)
		if jobID == excludeJobID {
			continue
		}
		if _, claimed := t.claimedJobs[jobID]; claimed {
			continue
		}
		priorityValue, found := t.taskKeyMap.Get(jobID)
		if !found {
			continue
		}
		jobValue, found := t.taskPriorityMapList[priorityValue.(int)].Get(jobID)
		if !found {
			continue
		}
		job := jobValue.(taskQueue2.OneJob)
		if job.JobStatus != taskQueue2.Waiting || job.Season != season {
			continue
		}
		readyAt := nextAttemptAt(job)
		if !readyAt.IsZero() && readyAt.After(now) {
			continue
		}
		jobs = append(jobs, job)
	}

	sort.SliceStable(jobs, func(i, j int) bool {
		left, right := nextAttemptAt(jobs[i]), nextAttemptAt(jobs[j])
		if left.IsZero() {
			left = time.Time(jobs[i].AddedTime)
		}
		if right.IsZero() {
			right = time.Time(jobs[j].AddedTime)
		}
		if !left.Equal(right) {
			return left.Before(right)
		}
		return jobs[i].Id < jobs[j].Id
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs
}
