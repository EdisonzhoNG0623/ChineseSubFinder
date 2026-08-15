package task_queue

import (
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	task_queue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func (t *TaskQueue) BeforeGetOneJob() {
	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	now := time.Now()
	expirationDays := settings.Get().AdvancedSettings.TaskQueue.ExpirationTime
	jobsToDelete := make([]task_queue2.OneJob, 0)
	type prioritizedJob struct {
		priority int
		job      task_queue2.OneJob
	}
	jobsToTerminalize := make([]prioritizedJob, 0)
	changedPriorities := make(map[int]struct{})

	for taskPriority := 0; taskPriority <= taskPriorityCount; taskPriority++ {
		t.taskPriorityMapList[taskPriority].Each(func(key interface{}, value interface{}) {
			oneJob := value.(task_queue2.OneJob)
			// Preserve the existing retention cleanup, but do not mutate the map
			// while iterating it.
			if time.Time(oneJob.UpdateTime).AddDate(0, 0, expirationDays).After(now) == false {
				jobsToDelete = append(jobsToDelete, oneJob)
				return
			}

			// A waiting task outside its retry lifetime must become terminal before
			// supplier calls. Previously it was downloaded once more and only then
			// marked failed, which made a large legacy queue look like a hot loop.
			if oneJob.JobStatus == task_queue2.Waiting && retryLifetimeExpired(oneJob, now, expirationDays) {
				oneJob.JobStatus = task_queue2.Failed
				clearRetrySchedule(&oneJob)
				oneJob.UpdateTime = emby.Time(now)
				jobsToTerminalize = append(jobsToTerminalize, prioritizedJob{priority: taskPriority, job: oneJob})
			}
		})
	}

	for _, oneJob := range jobsToDelete {
		bok, err := t.del(oneJob.Id)
		if err != nil {
			t.log.Errorf("BeforeGetOneJob delete expired job %s: %s", oneJob.Id, err.Error())
			continue
		}
		if !bok {
			t.log.Errorf("BeforeGetOneJob delete expired job %s: not found", oneJob.Id)
		}
	}
	for _, item := range jobsToTerminalize {
		t.taskPriorityMapList[item.priority].Put(item.job.Id, item.job)
		changedPriorities[item.priority] = struct{}{}
	}
	for taskPriority := range changedPriorities {
		if err := t.save(taskPriority); err != nil {
			t.log.Errorf("BeforeGetOneJob persist terminal expired jobs priority %d: %s", taskPriority, err.Error())
		}
	}
	if len(jobsToTerminalize) > 0 {
		t.log.Infof("TaskQueue terminalized %d waiting jobs outside the %d-day retry lifetime", len(jobsToTerminalize), expirationDays)
	}
}

// GetOneJob 优先获取 GetOneWaitingJob 然后才是 GetOneDoneJob
func (t *TaskQueue) GetOneJob() (bool, task_queue2.OneJob, error) {
	found, waitingJob, err := t.GetOneWaitingJob()
	if err != nil {
		return false, task_queue2.OneJob{}, err
	}
	if found == false {
		return t.GetOneDoneJob()
	}

	return true, waitingJob, nil
}

// GetOneWaitingJob 获取一个元素，按优先级，0 - taskPriorityCount 的级别去拿去任务，不会移除任务
func (t *TaskQueue) GetOneWaitingJob() (bool, task_queue2.OneJob, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	// 如果队列里面没有东西，则返回 false
	if t.isEmpty() == true {
		return false, task_queue2.OneJob{}, nil
	}
	now := time.Now()
	for taskPriority := 0; taskPriority <= taskPriorityCount; taskPriority++ {
		found := false
		selected := task_queue2.OneJob{}
		selectedOrderTime := time.Time{}

		// Select the oldest eligible job within a priority. The old map.Any
		// selection depended on hash iteration order and could starve jobs.
		t.taskPriorityMapList[taskPriority].Each(func(key interface{}, value interface{}) {
			oneJob := value.(task_queue2.OneJob)
			if oneJob.JobStatus != task_queue2.Waiting {
				return
			}

			readyAt := nextAttemptAt(oneJob)
			if !readyAt.IsZero() && readyAt.After(now) {
				return
			}

			orderTime := readyAt
			if orderTime.IsZero() {
				orderTime = time.Time(oneJob.AddedTime)
			}
			if !found || orderTime.Before(selectedOrderTime) ||
				(orderTime.Equal(selectedOrderTime) && time.Time(oneJob.AddedTime).Before(time.Time(selected.AddedTime))) {
				found = true
				selected = oneJob
				selectedOrderTime = orderTime
			}
		})

		if found {
			t.log.Debugf("TaskQueue selected id=%s priority=%d attempts=%d ready_at=%s",
				selected.Id, selected.TaskPriority, selected.DownloadTimes, nextAttemptAt(selected).Format(time.RFC3339))
			return true, selected, nil
		}
	}

	return false, task_queue2.OneJob{}, nil
}

// GetOneDoneJob 获取一个元素，按优先级，0 - taskPriorityCount 的级别去拿去任务，不会移除任务
func (t *TaskQueue) GetOneDoneJob() (bool, task_queue2.OneJob, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	// 如果队列里面没有东西，则返回 false
	if t.isEmpty() == true {
		return false, task_queue2.OneJob{}, nil
	}

	now := time.Now()
	for taskPriority := 0; taskPriority <= taskPriorityCount; taskPriority++ {
		found := false
		selected := task_queue2.OneJob{}
		taskInterval := time.Duration(settings.Get().AdvancedSettings.TaskQueue.OneSubDownloadInterval) * time.Hour

		t.taskPriorityMapList[taskPriority].Each(func(key interface{}, value interface{}) {
			oneJob := value.(task_queue2.OneJob)
			if oneJob.JobStatus != task_queue2.Done ||
				time.Time(oneJob.CreatedTime).AddDate(0, 0, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime).Before(now) ||
				time.Time(oneJob.UpdateTime).Add(taskInterval).After(now) {
				return
			}
			if !found || time.Time(oneJob.UpdateTime).Before(time.Time(selected.UpdateTime)) {
				found = true
				selected = oneJob
			}
		})

		if found {
			return true, selected, nil
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
