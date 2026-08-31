package task_queue

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	task_queue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/emirpasic/gods/maps/treemap"
	"github.com/emirpasic/gods/sets/treeset"
	"github.com/sirupsen/logrus"
)

type TaskQueue struct {
	queueName           string                        // 队列的名称
	log                 *logrus.Logger                // 日志
	center              *cache_center.CacheCenter     // 缓存中心
	persistPriority     func(int, []byte) error       // 可注入的优先级快照写入器
	taskPriorityMapList []*treemap.Map                // 这里有 0-10 个优先级划分的存储 List，每Add一个数据的时候需要切换到这个 List 中去 save
	taskKeyMap          *treemap.Map                  // 以每个任务的唯一 JobID 来存储每个 Job 的 优先级在哪里，这样可以快速查询
	taskGroupBySeries   *treemap.Map                  // 以每个任务的 SeriesRootPath 来存储每个任务，然后内层是一个 treeset，后续可以遍历删除即可
	waitingSchedule     []scheduledJobHeap            // waiting 任务的逐优先级到期索引
	doneSchedule        []scheduledJobHeap            // done 任务的逐优先级刷新索引
	scheduledJobs       map[string]*scheduledJob      // JobID 到索引节点，支持 O(log n) 更新
	claimedJobs         map[string]string             // batch member JobID -> primary claim ID
	claimMembers        map[string][]string           // primary claim ID -> reserved JobIDs
	claimTokens         map[string]uint64             // batch member JobID -> process-local claim generation
	claimOriginals      map[string]task_queue2.OneJob // primary claim ID -> pre-claim lifecycle snapshot
	claimPolicies       map[string]string             // primary claim ID -> policy used by this generation
	nextClaimToken      uint64                        // queueLock 保护的单调领取代次
	dirtyPriorities     map[int]struct{}              // 部分提交后尚未落盘的优先级快照
	wakeQueue           chan struct{}                 // 合并的 dispatcher 唤醒信号
	workerAvailable     chan struct{}                 // worker slot 释放信号（与队列变更分离）
	nextMaintenanceAt   time.Time                     // 避免每次取任务都全量维护
	runtimeStats        QueueRuntimeStats             // 低成本回归/诊断计数器
	queueLock           sync.Mutex                    // 公用这个锁
}

func NewTaskQueue(center *cache_center.CacheCenter) *TaskQueue {

	tq := &TaskQueue{queueName: center.GetName(),
		log:                 center.Log,
		center:              center,
		persistPriority:     center.TaskQueueSave,
		taskPriorityMapList: make([]*treemap.Map, 0),
		taskKeyMap:          treemap.NewWithStringComparator(),
		taskGroupBySeries:   treemap.NewWithStringComparator(),
	}
	tq.initializeScheduleIndex()
	for i := 0; i <= taskPriorityCount; i++ {
		tq.taskPriorityMapList = append(tq.taskPriorityMapList, treemap.NewWithStringComparator())
	}
	tq.read()

	tq.afterRead()
	tq.rebuildScheduleIndexesLocked()

	return tq
}

func (t *TaskQueue) Close() {
	t.center.Close()
}

func (t *TaskQueue) QueueName() string {
	return t.queueName
}

func (t *TaskQueue) Clear() error {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	err := t.center.TaskQueueClear()
	if err != nil {
		return err
	}

	for i := 0; i <= taskPriorityCount; i++ {
		t.taskPriorityMapList[i].Clear()
	}

	t.taskKeyMap.Clear()

	t.taskGroupBySeries.Clear()
	for priority := 0; priority <= taskPriorityCount; priority++ {
		t.waitingSchedule[priority] = nil
		t.doneSchedule[priority] = nil
	}
	t.scheduledJobs = make(map[string]*scheduledJob)
	t.claimedJobs = make(map[string]string)
	t.claimMembers = make(map[string][]string)
	t.claimTokens = make(map[string]uint64)
	t.claimOriginals = make(map[string]task_queue2.OneJob)
	t.claimPolicies = make(map[string]string)
	t.nextClaimToken = 0
	t.dirtyPriorities = make(map[int]struct{})
	t.nextMaintenanceAt = time.Time{}
	t.signalWakeLocked()

	return nil
}

// Size 队列的长度，对外暴露，有锁
func (t *TaskQueue) Size() int {
	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	return t.taskKeyMap.Size()
}

// checkPriority 检测优先级，会校验范围
func (t *TaskQueue) checkPriority(oneJob task_queue2.OneJob) task_queue2.OneJob {

	if oneJob.TaskPriority > taskPriorityCount {
		oneJob.TaskPriority = taskPriorityCount
	}

	if oneJob.TaskPriority < 0 {
		oneJob.TaskPriority = 0
	}

	return oneJob
}

// degrade 降一级，会校验范围
func (t *TaskQueue) degrade(oneJob task_queue2.OneJob) task_queue2.OneJob {

	// A larger number means a lower priority. The old subtraction promoted
	// repeatedly failing jobs and caused them to crowd out fresh work.
	oneJob.TaskPriority += 1

	return t.checkPriority(oneJob)
}

// nextStateRevision advances the durable per-job generation used to choose a
// canonical copy after a crash leaves the same job in two priority snapshots.
// Saturating at MaxUint64 avoids wrapping a newest state back to the legacy
// zero revision. Reaching the limit is practically impossible, but preserving
// ordering is safer than silently reusing an older generation.
func nextStateRevision(current uint64) uint64 {
	if current == ^uint64(0) {
		return current
	}
	return current + 1
}

// Add 放入元素，放入的时候会根据 TaskPriority 进行归类，存在的不会新增和更新
func (t *TaskQueue) Add(oneJob task_queue2.OneJob) (bool, error) {
	if task_queue2.IsBDMVStreamFile(oneJob.VideoFPath) {
		t.log.Debugln("TaskQueue.Add skip BDMV stream segment", oneJob.VideoFPath)
		return false, nil
	}

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	if t.isExist(oneJob.Id) == true {
		// A recurring library scan can discover better episode numbering after a
		// conclusive miss (for example after metadata or anime mapping repair).
		// Merge only search-relevant evidence into that existing retry record;
		// lifecycle state and backoff remain authoritative.
		if merged, changed := t.mergeNoSubtitleEvidenceLocked(oneJob); changed {
			current, _ := t.jobByIDLocked(merged.Id)
			oldSeriesRoot := current.SeriesRootDirPath
			merged.StateRevision = nextStateRevision(current.StateRevision)
			t.taskPriorityMapList[merged.TaskPriority].Put(merged.Id, merged)
			if oldSeriesRoot != merged.SeriesRootDirPath {
				t.removeJobFromSeriesIndex(oldSeriesRoot, merged.Id)
				t.addJobToSeriesIndex(merged.SeriesRootDirPath, merged.Id)
			}
			t.upsertScheduledLocked(merged)
			if err := t.save(merged.TaskPriority); err != nil {
				// Persistence rejected the improved evidence. Restore every derived
				// in-memory index so a later scan can retry the merge instead of
				// believing the unpersisted fingerprint is authoritative.
				t.taskPriorityMapList[current.TaskPriority].Put(current.Id, current)
				if oldSeriesRoot != merged.SeriesRootDirPath {
					t.removeJobFromSeriesIndex(merged.SeriesRootDirPath, merged.Id)
					t.addJobToSeriesIndex(oldSeriesRoot, current.Id)
				}
				t.upsertScheduledLocked(current)
				return false, err
			}
			t.signalWakeLocked()
		}
		return false, nil
	}
	// 检查权限范围
	oneJob = t.checkPriority(oneJob)
	oneJob.StateRevision = nextStateRevision(0)
	// 插入到统一的 KeyMap
	t.taskKeyMap.Put(oneJob.Id, oneJob.TaskPriority)
	// 分配到具体的优先级 map 中
	t.taskPriorityMapList[oneJob.TaskPriority].Put(oneJob.Id, oneJob)
	// 如果是连续剧，则需要存储到 taskGroupBySeries 中
	jobIDSet, found := t.taskGroupBySeries.Get(oneJob.SeriesRootDirPath)
	if found == false {
		// 不存在
		nowJobIDSet := treeset.NewWithStringComparator()
		nowJobIDSet.Add(oneJob.Id)
		t.taskGroupBySeries.Put(oneJob.SeriesRootDirPath, nowJobIDSet)
	} else {
		// 存在
		nowJobIDSet := jobIDSet.(*treeset.Set)
		nowJobIDSet.Add(oneJob.Id)
		t.taskGroupBySeries.Put(oneJob.SeriesRootDirPath, nowJobIDSet)
	}
	t.upsertScheduledLocked(oneJob)
	err := t.save(oneJob.TaskPriority)
	if err != nil {
		t.removeScheduledLocked(oneJob.Id)
		t.removeJobFromSeriesIndex(oneJob.SeriesRootDirPath, oneJob.Id)
		t.taskPriorityMapList[oneJob.TaskPriority].Remove(oneJob.Id)
		t.taskKeyMap.Remove(oneJob.Id)
		return false, err
	}
	t.signalWakeLocked()

	return true, nil
}

// update 更新素，不存在则会失败，内部用，没有锁
func (t *TaskQueue) update(oneJob task_queue2.OneJob) (bool, error) {

	if t.isExist(oneJob.Id) == false {
		return false, nil
	}
	priorityValue, found := t.taskKeyMap.Get(oneJob.Id)
	if !found {
		return false, nil
	}
	oldPriority := priorityValue.(int)
	storedValue, found := t.taskPriorityMapList[oldPriority].Get(oneJob.Id)
	if !found {
		return false, nil
	}
	original := storedValue.(task_queue2.OneJob)
	oldSeriesRoot := original.SeriesRootDirPath

	// Stage the full in-memory move, then persist the destination snapshot
	// before deleting the durable source. A crash can therefore leave a
	// duplicate (repaired by startup dedup), never a missing job.
	oneJob.UpdateTime = emby.Time(time.Now())
	oneJob.ClaimToken = 0
	oneJob.StateRevision = nextStateRevision(original.StateRevision)
	t.removeScheduledLocked(oneJob.Id)
	oneJob = t.checkPriority(oneJob)
	newPriority := oneJob.TaskPriority
	if newPriority != oldPriority {
		t.taskPriorityMapList[oldPriority].Remove(oneJob.Id)
	}
	t.taskKeyMap.Put(oneJob.Id, newPriority)
	t.taskPriorityMapList[newPriority].Put(oneJob.Id, oneJob)
	if oldSeriesRoot != oneJob.SeriesRootDirPath {
		t.removeJobFromSeriesIndex(oldSeriesRoot, oneJob.Id)
		t.addJobToSeriesIndex(oneJob.SeriesRootDirPath, oneJob.Id)
	}

	rollback := func() {
		t.removeScheduledLocked(oneJob.Id)
		if newPriority != oldPriority {
			t.taskPriorityMapList[newPriority].Remove(oneJob.Id)
		}
		t.taskPriorityMapList[oldPriority].Put(original.Id, original)
		t.taskKeyMap.Put(original.Id, oldPriority)
		if oldSeriesRoot != oneJob.SeriesRootDirPath {
			t.removeJobFromSeriesIndex(oneJob.SeriesRootDirPath, oneJob.Id)
			t.addJobToSeriesIndex(oldSeriesRoot, original.Id)
		}
		t.upsertScheduledLocked(original)
	}
	if err := t.save(newPriority); err != nil {
		rollback()
		return false, err
	}

	// Only a durably accepted update may cancel an active worker claim. Its
	// later outcome carries the old generation and will be ignored.
	if _, claimed := t.claimedJobs[oneJob.Id]; claimed {
		t.detachClaimMemberLocked(oneJob.Id)
	}
	t.upsertScheduledLocked(oneJob)
	if newPriority != oldPriority {
		if err := t.save(oldPriority); err != nil {
			// Destination is already durable. Keep the new in-memory state; the
			// remaining durable duplicate is safe and startup dedup will remove it.
			// Track the stale source explicitly so a live process can repair it
			// through FlushDirtyPriorities or the next batched mutation.
			t.dirtyPriorities[oldPriority] = struct{}{}
			t.signalWakeLocked()
			return true, err
		}
	}
	t.signalWakeLocked()

	return true, nil
}

func (t *TaskQueue) addJobToSeriesIndex(seriesRoot, jobID string) {
	jobIDSet, found := t.taskGroupBySeries.Get(seriesRoot)
	if !found {
		jobIDSet = treeset.NewWithStringComparator()
	}
	jobIDSet.(*treeset.Set).Add(jobID)
	t.taskGroupBySeries.Put(seriesRoot, jobIDSet)
}

func (t *TaskQueue) removeJobFromSeriesIndex(seriesRoot, jobID string) {
	jobIDSet, found := t.taskGroupBySeries.Get(seriesRoot)
	if !found {
		return
	}
	set := jobIDSet.(*treeset.Set)
	set.Remove(jobID)
	if set.Empty() {
		t.taskGroupBySeries.Remove(seriesRoot)
		return
	}
	t.taskGroupBySeries.Put(seriesRoot, set)
}

// Update 更新素，不存在则会失败
func (t *TaskQueue) Update(oneJob task_queue2.OneJob) (bool, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	return t.update(oneJob)
}

// UpdateIfRevision applies a read-modify-write update only while the stored
// job is still the generation the caller read. Queue workers advance the
// revision when claiming or completing a job, so a stale UI/API snapshot must
// not cancel a newer claim or overwrite its outcome.
func (t *TaskQueue) UpdateIfRevision(oneJob task_queue2.OneJob, expectedRevision uint64) (bool, error) {
	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	stored, found := t.jobByIDLocked(oneJob.Id)
	if !found || stored.StateRevision != expectedRevision {
		return false, nil
	}
	return t.update(oneJob)
}

// AutoDetectUpdateJobStatus 根据任务的生命周期图，进行自动判断更新，见《任务的生命周期》流程图
func (t *TaskQueue) AutoDetectUpdateJobStatus(oneJob task_queue2.OneJob, inErr error) error {
	if err := t.ApplyOutcomesReliable([]JobOutcome{{Job: oneJob, Err: inErr}}); err != nil {
		t.log.Errorln("AutoDetectUpdateJobStatus", oneJob.VideoFPath, err)
		return err
	}
	return nil
}

func (t *TaskQueue) del(jobId string) (bool, error) {
	original, found := t.jobByIDLocked(jobId)
	if !found {
		return false, nil
	}
	originalClaimedJobs := cloneStringMap(t.claimedJobs)
	originalClaimMembers := cloneStringSliceMap(t.claimMembers)
	originalClaimTokens := cloneUint64Map(t.claimTokens)
	originalClaimOriginals := cloneJobMap(t.claimOriginals)
	originalClaimPolicies := cloneStringMap(t.claimPolicies)
	originalDirtyPriorities := clonePrioritySet(t.dirtyPriorities)
	taskPriority, removed := t.removeJobWithoutSaveLocked(jobId)
	if !removed {
		return false, nil
	}
	err := t.save(taskPriority)
	if err != nil {
		// No snapshot was accepted, so restore the complete in-memory deletion,
		// including a possible active series claim and all derived indexes.
		t.taskKeyMap.Put(original.Id, original.TaskPriority)
		t.taskPriorityMapList[original.TaskPriority].Put(original.Id, original)
		t.addJobToSeriesIndex(original.SeriesRootDirPath, original.Id)
		t.claimedJobs = originalClaimedJobs
		t.claimMembers = originalClaimMembers
		t.claimTokens = originalClaimTokens
		t.claimOriginals = originalClaimOriginals
		t.claimPolicies = originalClaimPolicies
		t.dirtyPriorities = originalDirtyPriorities
		t.rebuildScheduleIndexesLocked()
		t.signalWakeLocked()
		return false, err
	}
	t.signalWakeLocked()
	// 删除任务的时候也需要删除对应的日志
	pathRoot := filepath.Join(pkg.ConfigRootDirFPath(), "Logs")
	fileFPath := filepath.Join(pathRoot, common.OnceLogPrefix+jobId+".log")
	if pkg.IsFile(fileFPath) == true {
		err = os.Remove(fileFPath)
		if err != nil {
			t.log.Errorln("del job", jobId, "logfile,error:", err)
		}
	}

	return true, nil
}

// Del 删除一个元素
func (t *TaskQueue) Del(jobId string) (bool, error) {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	return t.del(jobId)
}

func (t *TaskQueue) read() {

	taskQueueRead, err := t.center.TaskQueueRead()
	if err != nil {
		t.log.Errorln("read task queue TaskQueueRead error:", err)
		return
	}

	for i := 0; i <= taskPriorityCount; i++ {

		value, bok := taskQueueRead[i]
		if bok == false {
			continue
		}
		err = t.taskPriorityMapList[i].FromJSON(value)
		if err != nil {
			t.log.Errorln("read task queue FromJSON error:", err)
		}
		// 上面的操作仅仅是把 OneJob 的 JSON 弄了出来，还需要转换为 OneJob 的结构体
		// JobID - OneJob
		t.taskPriorityMapList[i].Each(func(key interface{}, value interface{}) {

			jsonString, err := json.Marshal(value)
			if err != nil {
				t.log.Panicln(err)
			}
			nowOneJob := task_queue2.OneJob{}
			err = json.Unmarshal(jsonString, &nowOneJob)
			if err != nil {
				t.log.Panicln(err)
			}
			t.taskPriorityMapList[i].Put(key, nowOneJob)
		})
	}

	t.deduplicateLoadedJobs()
	// Rebuild both indexes only after stale copies have been removed.
	for i := 0; i <= taskPriorityCount; i++ {
		t.taskPriorityMapList[i].Each(func(key interface{}, value interface{}) {
			t.taskKeyMap.Put(key, i)
			oneJob := value.(task_queue2.OneJob)
			jobIDSet, found := t.taskGroupBySeries.Get(oneJob.SeriesRootDirPath)
			if !found {
				jobIDSet = treeset.NewWithStringComparator()
			}
			jobIDSet.(*treeset.Set).Add(oneJob.Id)
			t.taskGroupBySeries.Put(oneJob.SeriesRootDirPath, jobIDSet)
		})
	}
}

type loadedJobCandidate struct {
	priority int
	job      task_queue2.OneJob
}

// deduplicateLoadedJobs repairs legacy queue files that contain the same job
// in multiple priority buckets. The newest copy wins; a priority-consistent
// copy wins an exact timestamp tie. Stale files are persisted immediately so
// the repair remains stable across restarts.
func (t *TaskQueue) deduplicateLoadedJobs() {
	canonical := make(map[string]loadedJobCandidate)
	for priority := 0; priority <= taskPriorityCount; priority++ {
		t.taskPriorityMapList[priority].Each(func(key interface{}, value interface{}) {
			job := value.(task_queue2.OneJob)
			current, found := canonical[job.Id]
			if !found || preferLoadedJobCandidate(loadedJobCandidate{priority: priority, job: job}, current) {
				canonical[job.Id] = loadedJobCandidate{priority: priority, job: job}
			}
		})
	}

	changedPriorities := make(map[int]struct{})
	removed := 0
	for priority := 0; priority <= taskPriorityCount; priority++ {
		keys := t.taskPriorityMapList[priority].Keys()
		for _, key := range keys {
			jobID := key.(string)
			if canonical[jobID].priority == priority {
				continue
			}
			t.taskPriorityMapList[priority].Remove(jobID)
			changedPriorities[priority] = struct{}{}
			removed++
		}
	}
	for priority := range changedPriorities {
		if err := t.save(priority); err != nil {
			t.log.Errorln("TaskQueue startup dedup persist failed", priority, err)
		}
	}
	if removed > 0 {
		t.log.Infof("TaskQueue startup deduplicated %d stale priority copies", removed)
	}
}

func preferLoadedJobCandidate(candidate, current loadedJobCandidate) bool {
	if candidate.job.StateRevision != current.job.StateRevision {
		return candidate.job.StateRevision > current.job.StateRevision
	}
	if time.Time(candidate.job.UpdateTime).After(time.Time(current.job.UpdateTime)) {
		return true
	}
	return time.Time(candidate.job.UpdateTime).Equal(time.Time(current.job.UpdateTime)) &&
		candidate.job.TaskPriority == candidate.priority && current.job.TaskPriority != current.priority
}

func (t *TaskQueue) afterRead() {
	// Establish a baseline for records written before search fingerprints
	// existed. This deliberately runs before retry migration/index rebuild so
	// an upgrade does not wake every historical conclusive miss at once.
	t.migrateLegacySearchEvidence()

	interruptedJobs := make([]task_queue2.OneJob, 0)
	bdmvStreamJobs := make([]task_queue2.OneJob, 0)
	legacyRetryJobs := make([]task_queue2.OneJob, 0)

	for taskPriority := 0; taskPriority <= taskPriorityCount; taskPriority++ {
		t.taskPriorityMapList[taskPriority].Each(func(key interface{}, value interface{}) {
			oneJob := value.(task_queue2.OneJob)
			if task_queue2.IsBDMVStreamFile(oneJob.VideoFPath) {
				bdmvStreamJobs = append(bdmvStreamJobs, oneJob)
				return
			}
			if oneJob.JobStatus == task_queue2.Downloading {
				interruptedJobs = append(interruptedJobs, oneJob)
				return
			}
			if oneJob.JobStatus == task_queue2.Waiting && oneJob.DownloadTimes > 0 &&
				isUnsetRetryTime(time.Time(oneJob.NextAttemptTime)) && !oneJob.ForceRun {
				legacyRetryJobs = append(legacyRetryJobs, oneJob)
			}
		})
	}

	changedPriorities := make(map[int]struct{})
	for _, oneJob := range bdmvStreamJobs {
		oneJob.JobStatus = task_queue2.Ignore
		oneJob.ErrorInfo = "ignored BDMV stream segment"
		clearRetrySchedule(&oneJob)
		oneJob.UpdateTime = emby.Time(time.Now())
		oneJob.StateRevision = nextStateRevision(oneJob.StateRevision)
		priorityValue, found := t.taskKeyMap.Get(oneJob.Id)
		if !found {
			t.log.Errorln("afterRead ignore BDMV stream missing job index", oneJob.VideoFPath)
			continue
		}
		priority := priorityValue.(int)
		t.taskPriorityMapList[priority].Put(oneJob.Id, oneJob)
		changedPriorities[priority] = struct{}{}
	}
	for priority := range changedPriorities {
		if err := t.save(priority); err != nil {
			t.log.Errorln("afterRead persist BDMV stream migration failed", priority, err)
		}
	}
	if len(bdmvStreamJobs) > 0 {
		t.log.Infof("TaskQueue startup migration ignored %d BDMV stream segment jobs", len(bdmvStreamJobs))
	}

	// Older queue files predate NextAttemptTime. Persist the same inferred
	// schedule that selection already honors so restarts and diagnostics see
	// an explicit retry time rather than a misleading zero value.
	changedPriorities = make(map[int]struct{})
	for _, oneJob := range legacyRetryJobs {
		readyAt := nextAttemptAt(oneJob)
		if readyAt.IsZero() {
			continue
		}
		oneJob.NextAttemptTime = emby.Time(readyAt)
		oneJob.StateRevision = nextStateRevision(oneJob.StateRevision)
		priorityValue, found := t.taskKeyMap.Get(oneJob.Id)
		if !found {
			t.log.Errorln("afterRead migrate retry schedule missing job index", oneJob.VideoFPath)
			continue
		}
		priority := priorityValue.(int)
		t.taskPriorityMapList[priority].Put(oneJob.Id, oneJob)
		changedPriorities[priority] = struct{}{}
	}
	for priority := range changedPriorities {
		if err := t.save(priority); err != nil {
			t.log.Errorln("afterRead persist retry schedule migration failed", priority, err)
		}
	}
	if len(legacyRetryJobs) > 0 {
		t.log.Infof("TaskQueue startup migration persisted %d legacy retry schedules", len(legacyRetryJobs))
	}

	// A persisted Downloading state proves only that the process stopped before
	// a terminal queue commit. It does not prove a supplier attempt happened.
	// Recover without changing attempts, priority, error or search evidence, and
	// use an independent short delay to avoid a restart hot loop.
	changedPriorities = make(map[int]struct{})
	recoveryNotBefore := time.Now().Add(time.Minute)
	for _, oneJob := range interruptedJobs {
		oneJob.JobStatus = task_queue2.Waiting
		oneJob.ForceRun = false
		oneJob.ClaimToken = 0
		oneJob.NotBeforeTime = emby.Time(recoveryNotBefore)
		oneJob.StateRevision = nextStateRevision(oneJob.StateRevision)
		priorityValue, found := t.taskKeyMap.Get(oneJob.Id)
		if !found {
			t.log.Errorln("afterRead recover interrupted job missing index", oneJob.Id)
			continue
		}
		priority := priorityValue.(int)
		t.taskPriorityMapList[priority].Put(oneJob.Id, oneJob)
		changedPriorities[priority] = struct{}{}
	}
	for priority := range changedPriorities {
		if err := t.save(priority); err != nil {
			t.log.Errorln("afterRead persist interrupted job recovery failed", priority, err)
		}
	}
	if len(interruptedJobs) > 0 {
		t.log.Infof("TaskQueue recovered %d interrupted jobs without counting supplier failures", len(interruptedJobs))
	}
}

// save 需要把改变的数据保持到 K/V 数据库中，这个没有锁，所以需要在 Sync 中使用，不对外开放
func (t *TaskQueue) save(taskPriority int) error {

	b, err := t.taskPriorityMapList[taskPriority].ToJSON()
	if err != nil {
		return err
	}

	err = t.persistPriority(taskPriority, b)
	if err != nil {
		return err
	}
	delete(t.dirtyPriorities, taskPriority)
	t.runtimeStats.PersistenceWrites++

	return nil
}

// isExist 是否已经存在，对内，无锁
func (t *TaskQueue) isExist(jobID string) bool {
	_, bok := t.taskKeyMap.Get(jobID)
	return bok
}

// IsExist 是否已经存在，对外，有锁
func (t *TaskQueue) IsExist(jobID string) bool {

	defer t.queueLock.Unlock()
	t.queueLock.Lock()

	_, bok := t.taskKeyMap.Get(jobID)
	return bok
}

// isEmpty 对内，无锁
func (t *TaskQueue) isEmpty() bool {
	return t.taskKeyMap.Empty()
}

const (
	taskPriorityCount           = 10
	HighTaskPriorityLevel       = 3
	DefaultTaskPriorityLevel    = 5
	FirstRetryTaskPriorityLevel = 6
	LowTaskPriorityLevel        = 7
)

var (
	ErrNoSubFound = errors.New("No Sub Found")
)
