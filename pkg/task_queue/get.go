package task_queue

import (
	"container/heap"
	"os"
	"path/filepath"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	task_queue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

const queueMaintenanceInterval = time.Hour

// BeforeGetOneJob preserves the historical public hook while rate-limiting
// its O(n) retention pass. Normal dispatch uses the schedule index and no
// longer scans the full queue every few seconds.
func (t *TaskQueue) BeforeGetOneJob() {
	t.runMaintenance(false, false)
}

// RunMaintenance performs the low-frequency fallback sweep. Besides retention
// and retry-lifetime cleanup it removes media files that disappeared while a
// task was sleeping. Synthetic Blu-ray paths remain valid when their BDMV
// structure can still be resolved.
func (t *TaskQueue) RunMaintenance() {
	t.runMaintenance(true, true)
}

func (t *TaskQueue) runMaintenance(force, pruneMissing bool) {
	t.queueLock.Lock()
	now := time.Now()
	if !force && now.Before(t.nextMaintenanceAt) {
		t.queueLock.Unlock()
		return
	}
	t.nextMaintenanceAt = now.Add(queueMaintenanceInterval)
	t.runtimeStats.MaintenanceScans++
	snapshot := make([]task_queue2.OneJob, 0, t.taskKeyMap.Size())
	for taskPriority := 0; taskPriority <= taskPriorityCount; taskPriority++ {
		t.taskPriorityMapList[taskPriority].Each(func(_ interface{}, value interface{}) {
			snapshot = append(snapshot, value.(task_queue2.OneJob))
		})
	}
	t.queueLock.Unlock()

	// NAS/file checks can be slow. Perform them without blocking queue claims,
	// additions or status updates, then revalidate every candidate under lock.
	missingPaths := make(map[string]string)
	if pruneMissing {
		for _, job := range snapshot {
			if job.VideoFPath != "" && job.JobStatus != task_queue2.Downloading && !queueJobMediaExists(job.VideoFPath) {
				missingPaths[job.Id] = job.VideoFPath
			}
		}
	}

	t.queueLock.Lock()
	expirationDays := settings.Get().AdvancedSettings.TaskQueue.ExpirationTime
	changedPriorities := make(map[int]struct{})
	removedIDs := make([]string, 0)
	terminalized := 0
	for _, snapshotJob := range snapshot {
		oneJob, exists := t.jobByIDLocked(snapshotJob.Id)
		if !exists || oneJob.JobStatus == task_queue2.Downloading {
			continue
		}
		if _, claimed := t.claimedJobs[oneJob.Id]; claimed {
			continue
		}

		retentionExpired := !time.Time(oneJob.UpdateTime).AddDate(0, 0, expirationDays).After(now)
		missing := false
		if missingPath, checkedMissing := missingPaths[oneJob.Id]; checkedMissing {
			// Do not delete a job whose media path changed while filesystem checks
			// were running; the next maintenance pass will inspect the new path.
			missing = oneJob.VideoFPath == missingPath
		}
		if retentionExpired || missing {
			priority, removed := t.removeJobWithoutSaveLocked(oneJob.Id)
			if removed {
				changedPriorities[priority] = struct{}{}
				removedIDs = append(removedIDs, oneJob.Id)
			}
			continue
		}

		// A waiting task outside its retry lifetime must become terminal before
		// supplier calls. Re-evaluate the live copy after the unlocked I/O pass.
		if oneJob.JobStatus == task_queue2.Waiting && !oneJob.ForceRun &&
			retryLifetimeExpired(oneJob, now, expirationDays) {
			oneJob.JobStatus = task_queue2.Failed
			clearRetrySchedule(&oneJob)
			oneJob.UpdateTime = emby.Time(now)
			oneJob.StateRevision = nextStateRevision(oneJob.StateRevision)
			t.taskPriorityMapList[oneJob.TaskPriority].Put(oneJob.Id, oneJob)
			changedPriorities[oneJob.TaskPriority] = struct{}{}
			terminalized++
		}
	}
	if err := t.saveChangedPrioritiesLocked(changedPriorities, changedPriorities); err != nil {
		t.log.Errorf("BeforeGetOneJob persist maintenance: %s", err.Error())
	}
	t.rebuildScheduleIndexesLocked()
	t.signalWakeLocked()
	t.queueLock.Unlock()

	for _, jobID := range removedIDs {
		removeQueueJobLog(t.log, jobID)
	}
	if terminalized > 0 {
		t.log.Infof("TaskQueue terminalized %d waiting jobs outside the %d-day retry lifetime", terminalized, expirationDays)
	}
	if len(removedIDs) > 0 {
		t.log.Infof("TaskQueue maintenance removed %d expired or missing jobs", len(removedIDs))
	}
}

func queueJobMediaExists(videoPath string) bool {
	isBluRay, _, _ := decode.IsFakeBDMVWorked(videoPath)
	return isBluRay || pkg.IsFile(videoPath)
}

func removeQueueJobLog(logPathLogger interface{ Errorln(...interface{}) }, jobID string) {
	pathRoot := filepath.Join(pkg.ConfigRootDirFPath(), "Logs")
	filePath := filepath.Join(pathRoot, common.OnceLogPrefix+jobID+".log")
	if !pkg.IsFile(filePath) {
		return
	}
	if err := os.Remove(filePath); err != nil {
		logPathLogger.Errorln("remove queue job log", jobID, err)
	}
}

// GetOneJob 优先获取 GetOneWaitingJob 然后才是 GetOneDoneJob
func (t *TaskQueue) GetOneJob() (bool, task_queue2.OneJob, error) {
	return t.GetOneJobExcludingSeries(nil)
}

// GetOneJobExcludingSeries selects the next eligible job while leaving active
// series untouched. Collection fan-out can write multiple episodes, so two
// workers must never process the same series concurrently.
func (t *TaskQueue) GetOneJobExcludingSeries(excludedSeries map[string]struct{}) (bool, task_queue2.OneJob, error) {
	found, waitingJob, err := t.getOneWaitingJob(excludedSeries)
	if err != nil {
		return false, task_queue2.OneJob{}, err
	}
	if found == false {
		return t.getOneDoneJob(excludedSeries)
	}

	return true, waitingJob, nil
}

// GetOneWaitingJob 获取一个元素，按优先级，0 - taskPriorityCount 的级别去拿去任务，不会移除任务
func (t *TaskQueue) GetOneWaitingJob() (bool, task_queue2.OneJob, error) {
	return t.getOneWaitingJob(nil)
}

func (t *TaskQueue) getOneWaitingJob(excludedSeries map[string]struct{}) (bool, task_queue2.OneJob, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	// 如果队列里面没有东西，则返回 false
	if t.isEmpty() == true {
		return false, task_queue2.OneJob{}, nil
	}
	now := time.Now()
	expirationDays := settings.Get().AdvancedSettings.TaskQueue.ExpirationTime
	changedPriorities := make(map[int]struct{})
	selected := task_queue2.OneJob{}
	found := false
	for taskPriority := 0; taskPriority <= taskPriorityCount; taskPriority++ {
		skipped := make([]*scheduledJob, 0)
		for len(t.waitingSchedule[taskPriority]) > 0 {
			item := t.waitingSchedule[taskPriority][0]
			t.noteSelectionInspectionLocked()
			if !scheduleDue(item.dueAt, now) {
				break
			}
			jobValue, exists := t.taskPriorityMapList[taskPriority].Get(item.jobID)
			if !exists {
				heap.Pop(&t.waitingSchedule[taskPriority])
				delete(t.scheduledJobs, item.jobID)
				continue
			}
			oneJob := jobValue.(task_queue2.OneJob)
			if oneJob.JobStatus != task_queue2.Waiting {
				t.removeScheduledLocked(oneJob.Id)
				continue
			}
			if !oneJob.ForceRun && retryLifetimeExpired(oneJob, now, expirationDays) {
				t.removeScheduledLocked(oneJob.Id)
				oneJob.JobStatus = task_queue2.Failed
				oneJob.UpdateTime = emby.Time(now)
				clearRetrySchedule(&oneJob)
				oneJob.StateRevision = nextStateRevision(oneJob.StateRevision)
				t.taskPriorityMapList[taskPriority].Put(oneJob.Id, oneJob)
				changedPriorities[taskPriority] = struct{}{}
				continue
			}
			if _, excluded := excludedSeries[oneJob.SeriesRootDirPath]; excluded && oneJob.SeriesRootDirPath != "" {
				heap.Pop(&t.waitingSchedule[taskPriority])
				skipped = append(skipped, item)
				continue
			}
			selected, found = oneJob, true
			break
		}
		for _, item := range skipped {
			heap.Push(&t.waitingSchedule[taskPriority], item)
		}
		if found {
			break
		}
	}
	if err := t.saveChangedPrioritiesLocked(changedPriorities, changedPriorities); err != nil {
		return false, task_queue2.OneJob{}, err
	}
	if !found {
		return false, task_queue2.OneJob{}, nil
	}
	t.log.Debugf("TaskQueue selected id=%s priority=%d attempts=%d ready_at=%s",
		selected.Id, selected.TaskPriority, selected.DownloadTimes, nextAttemptAt(selected).Format(time.RFC3339))
	return true, selected, nil
}

// GetOneDoneJob 获取一个元素，按优先级，0 - taskPriorityCount 的级别去拿去任务，不会移除任务
func (t *TaskQueue) GetOneDoneJob() (bool, task_queue2.OneJob, error) {
	return t.getOneDoneJob(nil)
}

func (t *TaskQueue) getOneDoneJob(excludedSeries map[string]struct{}) (bool, task_queue2.OneJob, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	// 如果队列里面没有东西，则返回 false
	if t.isEmpty() == true {
		return false, task_queue2.OneJob{}, nil
	}

	now := time.Now()
	for taskPriority := 0; taskPriority <= taskPriorityCount; taskPriority++ {
		skipped := make([]*scheduledJob, 0)
		for len(t.doneSchedule[taskPriority]) > 0 {
			item := t.doneSchedule[taskPriority][0]
			t.noteSelectionInspectionLocked()
			if !scheduleDue(item.dueAt, now) {
				break
			}
			jobValue, exists := t.taskPriorityMapList[taskPriority].Get(item.jobID)
			if !exists {
				heap.Pop(&t.doneSchedule[taskPriority])
				delete(t.scheduledJobs, item.jobID)
				continue
			}
			oneJob := jobValue.(task_queue2.OneJob)
			if oneJob.JobStatus != task_queue2.Done ||
				time.Time(oneJob.CreatedTime).AddDate(0, 0, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime).Before(now) {
				t.removeScheduledLocked(oneJob.Id)
				continue
			}
			if _, excluded := excludedSeries[oneJob.SeriesRootDirPath]; excluded && oneJob.SeriesRootDirPath != "" {
				heap.Pop(&t.doneSchedule[taskPriority])
				skipped = append(skipped, item)
				continue
			}
			for _, skippedItem := range skipped {
				heap.Push(&t.doneSchedule[taskPriority], skippedItem)
			}
			return true, oneJob, nil
		}
		for _, item := range skipped {
			heap.Push(&t.doneSchedule[taskPriority], item)
		}
	}

	return false, task_queue2.OneJob{}, nil
}

func (t *TaskQueue) GetJobsByStatus(status task_queue2.JobStatus) (bool, []task_queue2.OneJob, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	outOneJobs := make([]task_queue2.OneJob, 0)
	// 如果队列里面没有东西，则返回 false
	if t.isEmpty() == true {
		return false, nil, nil
	}

	for TaskPriority := 0; TaskPriority <= taskPriorityCount; TaskPriority++ {

		t.taskPriorityMapList[TaskPriority].Each(func(key interface{}, value interface{}) {

			tOneJob := task_queue2.OneJob{}
			tOneJob = value.(task_queue2.OneJob)
			if tOneJob.JobStatus == status {
				// 找到加入列表
				outOneJobs = append(outOneJobs, tOneJob)
			}
		})
	}

	return true, outOneJobs, nil
}

// GetJobsByPriorityAndStatus 根据任务优先级和状态获取任务列表
func (t *TaskQueue) GetJobsByPriorityAndStatus(taskPriority int, status task_queue2.JobStatus) (bool, []task_queue2.OneJob, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	outOneJobs := make([]task_queue2.OneJob, 0)
	// 如果队列里面没有东西，则返回 false
	if t.isEmpty() == true {
		return false, nil, nil
	}

	t.taskPriorityMapList[taskPriority].Each(func(key interface{}, value interface{}) {

		tOneJob := task_queue2.OneJob{}
		tOneJob = value.(task_queue2.OneJob)
		if tOneJob.JobStatus == status {
			// 找到加入列表
			outOneJobs = append(outOneJobs, tOneJob)
		}
	})

	return true, outOneJobs, nil
}

func (t *TaskQueue) GetAllJobs() (bool, []task_queue2.OneJob, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	outOneJobs := make([]task_queue2.OneJob, 0)
	// 如果队列里面没有东西，则返回 false
	if t.isEmpty() == true {
		return false, nil, nil
	}

	for TaskPriority := 0; TaskPriority <= taskPriorityCount; TaskPriority++ {

		t.taskPriorityMapList[TaskPriority].Each(func(key interface{}, value interface{}) {

			tOneJob := task_queue2.OneJob{}
			tOneJob = value.(task_queue2.OneJob)
			// 找到加入列表
			outOneJobs = append(outOneJobs, tOneJob)
		})
	}

	return true, outOneJobs, nil
}

func (t *TaskQueue) GetOneJobByID(jobId string) (bool, task_queue2.OneJob) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	outOneJob := task_queue2.OneJob{}

	taskPriority, bok := t.taskKeyMap.Get(jobId)
	if bok == false {
		return false, outOneJob
	}
	// 删除连续剧的 tree.Map 里面的 tree.Set 的元素
	needDelJobObj, bok := t.taskPriorityMapList[taskPriority.(int)].Get(jobId)
	if bok == false {
		return false, outOneJob
	}
	outOneJob = needDelJobObj.(task_queue2.OneJob)

	return true, outOneJob
}
