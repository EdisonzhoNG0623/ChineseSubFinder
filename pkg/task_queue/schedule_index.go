package task_queue

import (
	"container/heap"
	"encoding/json"
	"sort"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	queueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

// QueueRuntimeStats exposes inexpensive counters used by regression tests and
// production diagnostics. SelectionInspections counts heap candidates, not
// queue rows; PersistenceWrites counts priority snapshots written to disk.
type QueueRuntimeStats struct {
	SelectionInspections uint64
	PersistenceWrites    uint64
	MaintenanceScans     uint64
}

type scheduleKind uint8

const (
	waitingSchedule scheduleKind = iota
	doneSchedule
)

type scheduledJob struct {
	jobID    string
	priority int
	kind     scheduleKind
	dueAt    time.Time
	orderAt  time.Time
	heapAt   int
}

type scheduledJobHeap []*scheduledJob

func (h scheduledJobHeap) Len() int { return len(h) }

func (h scheduledJobHeap) Less(i, j int) bool {
	left, right := h[i], h[j]
	if !left.dueAt.Equal(right.dueAt) {
		return scheduleTimeBefore(left.dueAt, right.dueAt)
	}
	if !left.orderAt.Equal(right.orderAt) {
		return scheduleTimeBefore(left.orderAt, right.orderAt)
	}
	return left.jobID < right.jobID
}

func (h scheduledJobHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapAt = i
	h[j].heapAt = j
}

func (h *scheduledJobHeap) Push(value interface{}) {
	item := value.(*scheduledJob)
	item.heapAt = len(*h)
	*h = append(*h, item)
}

func (h *scheduledJobHeap) Pop() interface{} {
	old := *h
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	item.heapAt = -1
	*h = old[:last]
	return item
}

func scheduleTimeBefore(left, right time.Time) bool {
	if left.IsZero() {
		return !right.IsZero()
	}
	if right.IsZero() {
		return false
	}
	return left.Before(right)
}

func scheduleDue(dueAt, now time.Time) bool {
	return dueAt.IsZero() || !dueAt.After(now)
}

func (t *TaskQueue) initializeScheduleIndex() {
	t.waitingSchedule = make([]scheduledJobHeap, taskPriorityCount+1)
	t.doneSchedule = make([]scheduledJobHeap, taskPriorityCount+1)
	t.scheduledJobs = make(map[string]*scheduledJob)
	t.claimedJobs = make(map[string]string)
	t.claimMembers = make(map[string][]string)
	t.claimTokens = make(map[string]uint64)
	t.claimOriginals = make(map[string]queueTypes.OneJob)
	t.claimPolicies = make(map[string]string)
	t.dirtyPriorities = make(map[int]struct{})
	t.wakeQueue = make(chan struct{}, 1)
	t.workerAvailable = make(chan struct{}, 1)
	for priority := 0; priority <= taskPriorityCount; priority++ {
		heap.Init(&t.waitingSchedule[priority])
		heap.Init(&t.doneSchedule[priority])
	}
}

func (t *TaskQueue) scheduleHeap(item *scheduledJob) *scheduledJobHeap {
	if item.kind == waitingSchedule {
		return &t.waitingSchedule[item.priority]
	}
	return &t.doneSchedule[item.priority]
}

func (t *TaskQueue) removeScheduledLocked(jobID string) {
	item, found := t.scheduledJobs[jobID]
	if !found {
		return
	}
	heap.Remove(t.scheduleHeap(item), item.heapAt)
	delete(t.scheduledJobs, jobID)
}

func (t *TaskQueue) upsertScheduledLocked(job queueTypes.OneJob) {
	t.removeScheduledLocked(job.Id)
	if _, claimed := t.claimedJobs[job.Id]; claimed {
		return
	}

	item := &scheduledJob{jobID: job.Id, priority: job.TaskPriority, heapAt: -1}
	switch job.JobStatus {
	case queueTypes.Waiting:
		item.kind = waitingSchedule
		item.dueAt = nextAttemptAt(job)
		// Wake at retry-lifetime expiry as well. This terminalizes a job on
		// time even when its calculated backoff extends past that boundary.
		if !job.ForceRun {
			expiresAt := time.Time(job.AddedTime).AddDate(0, 0, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime)
			if !item.dueAt.IsZero() && !expiresAt.IsZero() && expiresAt.Before(item.dueAt) {
				item.dueAt = expiresAt
			}
		}
		item.orderAt = item.dueAt
		if item.orderAt.IsZero() {
			item.orderAt = time.Time(job.AddedTime)
		}
	case queueTypes.Done:
		item.kind = doneSchedule
		item.orderAt = time.Time(job.UpdateTime)
		item.dueAt = item.orderAt.Add(time.Duration(settings.Get().AdvancedSettings.TaskQueue.OneSubDownloadInterval) * time.Hour)
		createdExpiry := time.Time(job.CreatedTime).AddDate(0, 0, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime)
		if createdExpiry.Before(item.dueAt) {
			return
		}
	default:
		return
	}
	if notBefore := time.Time(job.NotBeforeTime); !isUnsetRetryTime(notBefore) &&
		(item.dueAt.IsZero() || notBefore.After(item.dueAt)) {
		item.dueAt = notBefore
	}

	t.scheduledJobs[job.Id] = item
	heap.Push(t.scheduleHeap(item), item)
}

func (t *TaskQueue) rebuildScheduleIndexesLocked() {
	for priority := 0; priority <= taskPriorityCount; priority++ {
		t.waitingSchedule[priority] = nil
		t.doneSchedule[priority] = nil
		heap.Init(&t.waitingSchedule[priority])
		heap.Init(&t.doneSchedule[priority])
	}
	t.scheduledJobs = make(map[string]*scheduledJob, t.taskKeyMap.Size())
	for priority := 0; priority <= taskPriorityCount; priority++ {
		t.taskPriorityMapList[priority].Each(func(_ interface{}, value interface{}) {
			t.upsertScheduledLocked(value.(queueTypes.OneJob))
		})
	}
}

func (t *TaskQueue) signalWakeLocked() {
	select {
	case t.wakeQueue <- struct{}{}:
	default:
	}
}

// WakeChan is a coalescing edge notification. Consumers must always query
// NextWakeAt after receiving it; the schedule index remains authoritative, so
// coalescing multiple mutations cannot lose work.
func (t *TaskQueue) WakeChan() <-chan struct{} {
	return t.wakeQueue
}

// WorkerAvailableChan is separate from queue mutations so an admitted worker
// that found no work can be rate-limited, while a saturated dispatcher still
// reacts immediately when any slot is actually returned.
func (t *TaskQueue) WorkerAvailableChan() <-chan struct{} {
	return t.workerAvailable
}

// NotifyWorkerAvailable emits a coalescing edge after every slot release.
func (t *TaskQueue) NotifyWorkerAvailable() {
	select {
	case t.workerAvailable <- struct{}{}:
	default:
	}
}

// NotifySettingsChanged rebuilds derived due times after retry/refresh
// settings change and wakes the dispatcher immediately.
func (t *TaskQueue) NotifySettingsChanged() {
	t.queueLock.Lock()
	t.rebuildScheduleIndexesLocked()
	t.signalWakeLocked()
	t.queueLock.Unlock()
}

// NextWakeAt returns the earliest time at which either a waiting task or a
// completed task becomes eligible. Ready work is reported as now.
func (t *TaskQueue) NextWakeAt() (time.Time, bool) {
	t.queueLock.Lock()
	defer t.queueLock.Unlock()

	now := time.Now()
	var next time.Time
	for priority := 0; priority <= taskPriorityCount; priority++ {
		for _, scheduled := range []scheduledJobHeap{t.waitingSchedule[priority], t.doneSchedule[priority]} {
			if len(scheduled) == 0 {
				continue
			}
			dueAt := scheduled[0].dueAt
			if scheduleDue(dueAt, now) {
				return now, true
			}
			if next.IsZero() || dueAt.Before(next) {
				next = dueAt
			}
		}
	}
	return next, !next.IsZero()
}

func (t *TaskQueue) noteSelectionInspectionLocked() {
	t.runtimeStats.SelectionInspections++
}

func (t *TaskQueue) RuntimeStats() QueueRuntimeStats {
	t.queueLock.Lock()
	defer t.queueLock.Unlock()
	return t.runtimeStats
}

func sortedPriorities(priorities map[int]struct{}) []int {
	out := make([]int, 0, len(priorities))
	for priority := range priorities {
		out = append(out, priority)
	}
	sort.Ints(out)
	return out
}

type prioritySaveResult struct {
	saved   map[int]struct{}
	pending map[int]struct{}
	err     error
}

type priorityMove struct {
	jobID    string
	from     int
	to       int
	original queueTypes.OneJob
}

// saveChangedPrioritiesWithResultLocked writes destination buckets before
// source-only buckets and reports the exact commit boundary. A caller can roll
// back safely when saved is empty. Once any destination is durable, the new
// in-memory state must remain authoritative and every unwritten snapshot is
// tracked as dirty for an explicit retry.
func (t *TaskQueue) saveChangedPrioritiesWithResultLocked(changed, destinations map[int]struct{}, moves ...priorityMove) prioritySaveResult {
	result := prioritySaveResult{
		saved:   make(map[int]struct{}, len(changed)),
		pending: make(map[int]struct{}, len(changed)),
	}
	ordered, acyclic := priorityPersistenceOrder(changed, destinations, moves)
	if !acyclic {
		// A cyclic move graph (for example P5->P6 and P6->P5) has no safe
		// final-snapshot write order. First persist additive snapshots containing
		// both the old source members and new destination members. Once every
		// bucket has that safety copy, final snapshots may be replaced in any
		// order without making a job disappear across a crash.
		for priority := range changed {
			t.dirtyPriorities[priority] = struct{}{}
		}
		for _, priority := range sortedPriorities(changed) {
			if err := t.saveAdditivePriorityLocked(priority, moves); err != nil {
				result.err = err
				for pending := range changed {
					result.pending[pending] = struct{}{}
				}
				return result
			}
			result.saved[priority] = struct{}{}
		}
		ordered = sortedPriorities(changed)
	}

	for index, priority := range ordered {
		if err := t.save(priority); err != nil {
			result.err = err
			for _, pending := range ordered[index:] {
				result.pending[pending] = struct{}{}
				t.dirtyPriorities[pending] = struct{}{}
			}
			return result
		}
		result.saved[priority] = struct{}{}
	}
	return result
}

func priorityPersistenceOrder(changed, destinations map[int]struct{}, moves []priorityMove) ([]int, bool) {
	if len(moves) == 0 {
		ordered := make([]int, 0, len(changed))
		seen := make(map[int]struct{}, len(changed))
		for _, priorities := range []map[int]struct{}{destinations, changed} {
			for _, priority := range sortedPriorities(priorities) {
				if _, exists := seen[priority]; exists {
					continue
				}
				seen[priority] = struct{}{}
				ordered = append(ordered, priority)
			}
		}
		return ordered, true
	}

	indegree := make(map[int]int, len(changed))
	edges := make(map[int]map[int]struct{}, len(changed))
	for priority := range changed {
		indegree[priority] = 0
	}
	for _, move := range moves {
		if move.from == move.to {
			continue
		}
		if _, sourceChanged := changed[move.from]; !sourceChanged {
			continue
		}
		if _, destinationChanged := changed[move.to]; !destinationChanged {
			continue
		}
		// The destination must be durable before the source is cleared.
		if edges[move.to] == nil {
			edges[move.to] = make(map[int]struct{})
		}
		if _, duplicate := edges[move.to][move.from]; duplicate {
			continue
		}
		edges[move.to][move.from] = struct{}{}
		indegree[move.from]++
	}

	ready := make([]int, 0, len(changed))
	for priority, degree := range indegree {
		if degree == 0 {
			ready = append(ready, priority)
		}
	}
	sort.Ints(ready)
	ordered := make([]int, 0, len(changed))
	for len(ready) > 0 {
		priority := ready[0]
		ready = ready[1:]
		ordered = append(ordered, priority)
		for destination := range edges[priority] {
			indegree[destination]--
			if indegree[destination] == 0 {
				ready = append(ready, destination)
				sort.Ints(ready)
			}
		}
	}
	return ordered, len(ordered) == len(changed)
}

func (t *TaskQueue) saveAdditivePriorityLocked(priority int, moves []priorityMove) error {
	snapshot := make(map[string]queueTypes.OneJob, t.taskPriorityMapList[priority].Size()+len(moves))
	t.taskPriorityMapList[priority].Each(func(key interface{}, value interface{}) {
		snapshot[key.(string)] = value.(queueTypes.OneJob)
	})
	for _, move := range moves {
		if move.from == priority && move.from != move.to {
			snapshot[move.jobID] = move.original
		}
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err = t.persistPriority(priority, payload); err != nil {
		return err
	}
	t.runtimeStats.PersistenceWrites++
	return nil
}

func (t *TaskQueue) retryDirtyPrioritiesLocked() error {
	for _, priority := range sortedPriorities(t.dirtyPriorities) {
		if err := t.save(priority); err != nil {
			return err
		}
	}
	return nil
}

// FlushDirtyPriorities retries snapshots left behind by a safe partial commit.
// Low-frequency maintenance and later queue mutations also reach this path through
// saveChangedPrioritiesLocked, so recovery does not depend on another outcome.
func (t *TaskQueue) FlushDirtyPriorities() error {
	t.queueLock.Lock()
	defer t.queueLock.Unlock()
	return t.retryDirtyPrioritiesLocked()
}

// saveChangedPrioritiesLocked is the common best-effort wrapper for mutations
// that do not need to roll back their in-memory state. ApplyOutcomes uses the
// detailed variant above because it owns claim restoration.
func (t *TaskQueue) saveChangedPrioritiesLocked(changed, destinations map[int]struct{}, moves ...priorityMove) error {
	result := t.saveChangedPrioritiesWithResultLocked(changed, destinations, moves...)
	if result.err != nil {
		return result.err
	}
	return t.retryDirtyPrioritiesLocked()
}
