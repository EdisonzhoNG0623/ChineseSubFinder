package task_queue

import (
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/emirpasic/gods/sets/treeset"
)

// MarkSeriesEpisodesDone marks queued episodes completed after a collection
// archive has already supplied and saved their subtitles. Updates are persisted
// once per affected priority instead of rewriting the queue for every episode.
func (t *TaskQueue) MarkSeriesEpisodesDone(seriesRootDirPath string, episodeKeys map[string]struct{}, excludeJobID string) (int, error) {
	if len(episodeKeys) == 0 {
		return 0, nil
	}

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	jobSetValue, found := t.taskGroupBySeries.Get(seriesRootDirPath)
	if !found {
		return 0, nil
	}

	changedPriorities := make(map[int]struct{})
	destinationPriorities := make(map[int]struct{})
	moves := make([]priorityMove, 0)
	marked := 0
	for _, jobIDValue := range jobSetValue.(*treeset.Set).Values() {
		jobID := jobIDValue.(string)
		if jobID == excludeJobID {
			continue
		}

		priorityValue, found := t.taskKeyMap.Get(jobID)
		if !found {
			continue
		}
		oldPriority := priorityValue.(int)
		jobValue, found := t.taskPriorityMapList[oldPriority].Get(jobID)
		if !found {
			continue
		}
		job := jobValue.(taskQueue2.OneJob)
		episodeKey := pkg.GetEpisodeKeyName(job.Season, job.Episode)
		if _, found = episodeKeys[episodeKey]; !found {
			continue
		}
		if job.JobStatus != taskQueue2.Waiting && job.JobStatus != taskQueue2.Failed {
			continue
		}

		if oldPriority == 0 {
			job.JobStatus = taskQueue2.Ignore
		} else {
			job.JobStatus = taskQueue2.Done
		}
		job.TaskPriority = DefaultTaskPriorityLevel
		job.DownloadTimes++
		job.RetryTimes = 0
		job.ErrorInfo = ""
		job.UpdateTime = emby.Time(time.Now())
		job.StateRevision = nextStateRevision(jobValue.(taskQueue2.OneJob).StateRevision)
		clearRetrySchedule(&job)
		t.removeScheduledLocked(jobID)

		if oldPriority != job.TaskPriority {
			moves = append(moves, priorityMove{jobID: job.Id, from: oldPriority, to: job.TaskPriority, original: jobValue.(taskQueue2.OneJob)})
			t.taskPriorityMapList[oldPriority].Remove(jobID)
			changedPriorities[oldPriority] = struct{}{}
		}
		t.taskKeyMap.Put(jobID, job.TaskPriority)
		t.taskPriorityMapList[job.TaskPriority].Put(jobID, job)
		t.upsertScheduledLocked(job)
		changedPriorities[job.TaskPriority] = struct{}{}
		destinationPriorities[job.TaskPriority] = struct{}{}
		marked++
	}

	if err := t.saveChangedPrioritiesLocked(changedPriorities, destinationPriorities, moves...); err != nil {
		return marked, err
	}
	t.signalWakeLocked()
	return marked, nil
}
