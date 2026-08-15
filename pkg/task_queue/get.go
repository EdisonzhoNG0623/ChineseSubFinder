package task_queue

import (
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"time"

	task_queue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func (t *TaskQueue) BeforeGetOneJob() {
	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	// 这里需要手动判断 Done 的任务是否超过三个月了，超过就需要手动删除
	for TaskPriority := 0; TaskPriority <= taskPriorityCount; TaskPriority++ {
		t.taskPriorityMapList[TaskPriority].Each(func(key interface{}, value interface{}) {

			nowOneJob := value.(task_queue2.OneJob)
			if //nowOneJob.JobStatus == task_queue.Done &&
			// 默认是 90day, A.After(B) : A > B == true
			(time.Time)(nowOneJob.UpdateTime).AddDate(0, 0, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime).After(time.Now()) == false {
				// 找到就删除
				bok, err := t.del(nowOneJob.Id)
				if err != nil {
					t.log.Errorf("GetOneWaitingJob.Del.Done ExpirationTime %v error: %s", settings.Get().AdvancedSettings.TaskQueue.ExpirationTime, err.Error())
					return
				}
				if bok == false {
					t.log.Errorf("GetOneWaitingJob.Del.Done ExpirationTime %v error: %s", settings.Get().AdvancedSettings.TaskQueue.ExpirationTime, "Del failed")
					return

				}
				return
			}
		})
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
