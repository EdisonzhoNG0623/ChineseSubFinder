package task_queue

import (
	"path/filepath"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/emirpasic/gods/sets/treeset"
)

// VerifiedChineseVideoPaths is an immutable snapshot of exact videos whose
// installed subtitle has been parsed successfully and confirmed to contain
// Chinese. Episode numbers alone are not sufficient evidence because one
// series root can contain multiple cuts or encodes of the same SxxExx.
type VerifiedChineseVideoPaths struct {
	values map[string]struct{}
}

// NewVerifiedChineseVideoPaths snapshots the downloader's successfully
// parsed Chinese-subtitle evidence before the queue transition begins.
func NewVerifiedChineseVideoPaths(videoPaths map[string]struct{}) VerifiedChineseVideoPaths {
	values := make(map[string]struct{}, len(videoPaths))
	for videoPath := range videoPaths {
		if videoPath == "" {
			continue
		}
		values[filepath.Clean(videoPath)] = struct{}{}
	}
	return VerifiedChineseVideoPaths{values: values}
}

// MarkSeriesEpisodesDone marks queued episodes completed after a collection
// archive has already supplied and saved their subtitles. Updates are persisted
// once per affected priority instead of rewriting the queue for every episode.
func (t *TaskQueue) MarkSeriesEpisodesDone(seriesRootDirPath string, videoPaths VerifiedChineseVideoPaths, activeBatchJobIDs ...string) (int, error) {
	if len(videoPaths.values) == 0 {
		return 0, nil
	}
	activeBatch := make(map[string]struct{}, len(activeBatchJobIDs))
	for _, jobID := range activeBatchJobIDs {
		if jobID != "" {
			activeBatch[jobID] = struct{}{}
		}
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
	completedReservedJobIDs := make([]string, 0)
	marked := 0
	for _, jobIDValue := range jobSetValue.(*treeset.Set).Values() {
		jobID := jobIDValue.(string)
		if _, active := activeBatch[jobID]; active {
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
		if _, found = videoPaths.values[filepath.Clean(job.VideoFPath)]; !found {
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
		if _, claimed := t.claimedJobs[jobID]; claimed {
			// ClaimBatch reserves every ready member of the series to keep the
			// dispatcher from spinning, even though only a bounded subset belongs
			// to the active download batch. Exact backfill evidence can complete a
			// reserved-only member, but active batch members remain authoritative
			// through ApplyOutcomes and were excluded above.
			completedReservedJobIDs = append(completedReservedJobIDs, jobID)
		}
		changedPriorities[job.TaskPriority] = struct{}{}
		destinationPriorities[job.TaskPriority] = struct{}{}
		marked++
	}

	if err := t.saveChangedPrioritiesLocked(changedPriorities, destinationPriorities, moves...); err != nil {
		return marked, err
	}
	for _, jobID := range completedReservedJobIDs {
		t.detachClaimMemberLocked(jobID)
		if job, exists := t.jobByIDLocked(jobID); exists {
			t.upsertScheduledLocked(job)
		}
	}
	t.signalWakeLocked()
	return marked, nil
}
