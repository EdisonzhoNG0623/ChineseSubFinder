package task_queue

import (
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
