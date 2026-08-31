package cron_helper

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeQueueSchedule struct {
	lock      sync.Mutex
	wake      chan struct{}
	worker    chan struct{}
	next      time.Time
	scheduled bool
}

func newFakeQueueSchedule() *fakeQueueSchedule {
	return &fakeQueueSchedule{wake: make(chan struct{}, 1), worker: make(chan struct{}, 1)}
}

func (schedule *fakeQueueSchedule) WakeChan() <-chan struct{} { return schedule.wake }

func (schedule *fakeQueueSchedule) WorkerAvailableChan() <-chan struct{} { return schedule.worker }

func (schedule *fakeQueueSchedule) NextWakeAt() (time.Time, bool) {
	schedule.lock.Lock()
	defer schedule.lock.Unlock()
	return schedule.next, schedule.scheduled
}

func (schedule *fakeQueueSchedule) set(next time.Time, scheduled bool) {
	schedule.lock.Lock()
	schedule.next = next
	schedule.scheduled = scheduled
	schedule.lock.Unlock()
	select {
	case schedule.wake <- struct{}{}:
	default:
	}
}

func waitForCount(t *testing.T, count *int32, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(count) < want {
		select {
		case <-deadline:
			t.Fatalf("launch count = %d, want at least %d", atomic.LoadInt32(count), want)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestQueueDispatcherStopsAndRebuilds(t *testing.T) {
	schedule := newFakeQueueSchedule()
	dispatcher := &queueDispatcher{}
	var launches int32
	launch := func() bool {
		atomic.AddInt32(&launches, 1)
		schedule.set(time.Time{}, false)
		return true
	}

	firstGeneration := dispatcher.start(schedule, launch)
	schedule.set(time.Now(), true)
	waitForCount(t, &launches, 1)
	dispatcher.stop()

	schedule.set(time.Now(), true)
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&launches); got != 1 {
		t.Fatalf("stopped dispatcher launched work: %d", got)
	}

	secondGeneration := dispatcher.start(schedule, launch)
	if secondGeneration <= firstGeneration {
		t.Fatalf("dispatcher generation did not advance: %d -> %d", firstGeneration, secondGeneration)
	}
	waitForCount(t, &launches, 2)
	dispatcher.stop()
}

func TestQueueDispatcherRetriesAfterWorkerReleaseEdge(t *testing.T) {
	schedule := newFakeQueueSchedule()
	schedule.set(time.Now(), true)
	<-schedule.wake // let indexed readiness, not a stale mutation edge, drive launch
	dispatcher := &queueDispatcher{}
	var attempts int32
	dispatcher.start(schedule, func() bool {
		attempt := atomic.AddInt32(&attempts, 1)
		if attempt == 1 {
			return false // saturated pool
		}
		schedule.set(time.Time{}, false)
		return true
	})
	t.Cleanup(dispatcher.stop)

	waitForCount(t, &attempts, 1)
	// This edge models TaskQueue.NotifyWorkerAvailable after a saturated slot
	// is returned, including a worker that discovered no work.
	select {
	case schedule.worker <- struct{}{}:
	default:
	}
	waitForCount(t, &attempts, 2)
}

func TestQueueDispatcherHonorsPreexistingCoalescedWake(t *testing.T) {
	schedule := newFakeQueueSchedule()
	schedule.set(time.Now(), true) // edge exists before the loop starts
	// Additional edges coalesce, while the indexed state remains authoritative.
	schedule.set(time.Now(), true)

	dispatcher := &queueDispatcher{}
	var launches int32
	dispatcher.start(schedule, func() bool {
		atomic.AddInt32(&launches, 1)
		schedule.set(time.Time{}, false)
		return true
	})
	t.Cleanup(dispatcher.stop)
	waitForCount(t, &launches, 1)
}
