package cron_helper

import (
	"context"
	"sync"
	"time"
)

const (
	queueLaunchRetry      = 10 * time.Second
	queueFallbackCronSpec = "@every 15m"
)

type queueSchedule interface {
	WakeChan() <-chan struct{}
	WorkerAvailableChan() <-chan struct{}
	NextWakeAt() (time.Time, bool)
}

// queueDispatcher owns one cancellable event loop. start/stop are idempotent,
// and start always waits for a prior generation before replacing it.
type queueDispatcher struct {
	lock       sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
	generation uint64
}

func (dispatcher *queueDispatcher) start(schedule queueSchedule, launch func() bool) uint64 {
	dispatcher.stop()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	dispatcher.lock.Lock()
	dispatcher.generation++
	generation := dispatcher.generation
	dispatcher.cancel = cancel
	dispatcher.done = done
	dispatcher.lock.Unlock()

	go func() {
		defer close(done)
		runQueueDispatcher(ctx, schedule, launch)
	}()
	return generation
}

func (dispatcher *queueDispatcher) stop() {
	dispatcher.lock.Lock()
	cancel, done := dispatcher.cancel, dispatcher.done
	dispatcher.cancel = nil
	dispatcher.done = nil
	dispatcher.lock.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func runQueueDispatcher(ctx context.Context, schedule queueSchedule, launch func() bool) {
	var timer *time.Timer
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	defer stopTimer()

	for {
		nextWake, scheduled := schedule.NextWakeAt()
		now := time.Now()
		if scheduled && !nextWake.After(now) {
			if launch() {
				// A successful worker normally claims a task and sends a wake edge.
				// The retry protects against a supplier hub that is not ready yet.
				if timer == nil {
					timer = time.NewTimer(queueLaunchRetry)
				} else {
					stopTimer()
					timer.Reset(queueLaunchRetry)
				}
				select {
				case <-ctx.Done():
					return
				case <-schedule.WakeChan():
					stopTimer()
				case <-schedule.WorkerAvailableChan():
					// If an outcome changed the queue, its mutation edge is ready
					// too. Otherwise this admitted worker found no work; retain the
					// retry delay to avoid a supplier-not-ready hot loop.
					select {
					case <-ctx.Done():
						return
					case <-schedule.WakeChan():
						stopTimer()
					case <-timer.C:
					}
				case <-timer.C:
				}
				continue
			}

			// A saturated pool will signal when an outcome changes the queue. Do
			// not poll it while all workers are busy.
			select {
			case <-ctx.Done():
				return
			case <-schedule.WakeChan():
				continue
			case <-schedule.WorkerAvailableChan():
				continue
			}
		}

		var timerC <-chan time.Time
		if scheduled {
			delay := time.Until(nextWake)
			if delay < 0 {
				delay = 0
			}
			if timer == nil {
				timer = time.NewTimer(delay)
			} else {
				stopTimer()
				timer.Reset(delay)
			}
			timerC = timer.C
		}

		select {
		case <-ctx.Done():
			return
		case <-schedule.WakeChan():
			stopTimer()
		case <-schedule.WorkerAvailableChan():
			stopTimer()
		case <-timerC:
		}
	}
}
