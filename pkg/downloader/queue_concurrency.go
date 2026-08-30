package downloader

import (
	"fmt"
	"sync"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	subSupplier "github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_supplier"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/supplier_search"
)

type seriesWorkerLock struct {
	mu sync.Mutex
}

func (d *Downloader) setSubSupplierHub(hub *subSupplier.SubSupplierHub) {
	d.subSupplierHubLock.Lock()
	d.subSupplierHub = hub
	d.subSupplierHubLock.Unlock()
}

func (d *Downloader) getSubSupplierHub() *subSupplier.SubSupplierHub {
	d.subSupplierHubLock.RLock()
	hub := d.subSupplierHub
	d.subSupplierHubLock.RUnlock()
	return hub
}

func (d *Downloader) tryStartQueueWorker() bool {
	select {
	case d.queueWorkerSlots <- struct{}{}:
		d.queueWorkerStateLock.Lock()
		d.activeQueueWorkers++
		active := d.activeQueueWorkers
		d.queueWorkerStateLock.Unlock()
		d.log.Debugf("Queue worker admitted active=%d capacity=%d", active, cap(d.queueWorkerSlots))
		return true
	default:
		d.log.Debugln("Queue worker capacity reached, skip this tick")
		return false
	}
}

func (d *Downloader) finishQueueWorker(didWork bool) {
	d.queueWorkerStateLock.Lock()
	active, shouldScheduleCleanup := d.updateQueueWorkerOnFinishLocked(didWork)
	d.queueWorkerStateLock.Unlock()
	<-d.queueWorkerSlots
	if d.downloadQueue != nil {
		d.downloadQueue.NotifyWorkerAvailable()
	}
	d.log.Debugf("Queue worker released active=%d capacity=%d", active, cap(d.queueWorkerSlots))
	if shouldScheduleCleanup {
		d.scheduleQueueCleanup()
	}
}

// updateQueueWorkerOnFinishLocked updates only the cleanup state machine.
func (d *Downloader) updateQueueWorkerOnFinishLocked(didWork bool) (active int, shouldScheduleCleanup bool) {
	if didWork {
		d.queueCleanupPending = true
	}
	d.activeQueueWorkers--
	active = d.activeQueueWorkers
	if active == 0 && d.queueCleanupPending && !d.queueCleanupScheduled {
		d.queueCleanupScheduled = true
		shouldScheduleCleanup = true
	}
	return active, shouldScheduleCleanup
}

// scheduleQueueCleanup never holds a worker slot or queue state lock while a
// timed-out legacy supplier is still running. Cleanup is attempted only at a
// process-wide Chrome/tmp idle boundary; a permanently stuck source merely
// postpones cleanup and cannot stall dispatcher capacity or later calls.
func (d *Downloader) scheduleQueueCleanup() {
	d.queueCleanupWG.Add(1)
	go func() {
		defer d.queueCleanupWG.Done()
		defer func() {
			d.queueWorkerStateLock.Lock()
			d.queueCleanupScheduled = false
			d.queueWorkerStateLock.Unlock()
		}()
		deferredLogged := false
		retryDelay := time.Second
		for {
			if d.ctx.Err() != nil {
				return
			}
			stop := false
			retry := false
			if supplier_search.TryWithSharedResourcesIdle(func() {
				if d.ctx.Err() != nil {
					stop = true
					return
				}
				d.queueWorkerStateLock.Lock()
				defer d.queueWorkerStateLock.Unlock()
				if d.activeQueueWorkers != 0 || !d.queueCleanupPending {
					stop = true
					return
				}
				// Holding both the provider registration boundary and worker-state
				// lock keeps new calls/workers out only for the brief cleanup itself.
				if err := pkg.ClearRootTmpFolder(); err != nil {
					d.log.Errorln("ClearRootTmpFolder", err)
					retry = true
				}
				if !pkg.LiteMode() {
					pkg.CloseChrome(d.log)
				}
				if !retry {
					d.queueCleanupPending = false
					stop = true
				}
			}) {
				if stop {
					return
				}
				if retry {
					timer := time.NewTimer(retryDelay)
					select {
					case <-timer.C:
					case <-d.ctx.Done():
						if !timer.Stop() {
							select {
							case <-timer.C:
							default:
							}
						}
						return
					}
					if retryDelay < 30*time.Second {
						retryDelay *= 2
						if retryDelay > 30*time.Second {
							retryDelay = 30 * time.Second
						}
					}
					continue
				}
			}
			if !deferredLogged {
				d.log.Debug("Queue cleanup deferred until timed-out provider calls finish")
				deferredLogged = true
			}
			select {
			case <-supplier_search.SharedResourcesIdleChan():
			case <-d.ctx.Done():
				return
			}
		}
	}()
}

func (d *Downloader) lockSeriesWorker(seriesRoot string) func() {
	if seriesRoot == "" {
		return func() {}
	}
	value, _ := d.seriesWorkerLocks.LoadOrStore(seriesRoot, &seriesWorkerLock{})
	lock := value.(*seriesWorkerLock)
	lock.mu.Lock()
	return lock.mu.Unlock
}

func (d *Downloader) activeSeriesSnapshot() map[string]struct{} {
	d.queueWorkerStateLock.Lock()
	defer d.queueWorkerStateLock.Unlock()

	out := make(map[string]struct{}, len(d.activeSeriesWorkers))
	for seriesRoot, count := range d.activeSeriesWorkers {
		if count > 0 {
			out[seriesRoot] = struct{}{}
		}
	}
	return out
}

func (d *Downloader) ActiveQueueWorkers() int {
	d.queueWorkerStateLock.Lock()
	defer d.queueWorkerStateLock.Unlock()
	return d.activeQueueWorkers
}

func (d *Downloader) registerSeriesWorker(seriesRoot string) func() {
	if seriesRoot == "" {
		return func() {}
	}

	d.queueWorkerStateLock.Lock()
	if d.activeSeriesWorkers == nil {
		d.activeSeriesWorkers = make(map[string]int)
	}
	d.activeSeriesWorkers[seriesRoot]++
	d.queueWorkerStateLock.Unlock()

	return func() {
		d.queueWorkerStateLock.Lock()
		d.activeSeriesWorkers[seriesRoot]--
		if d.activeSeriesWorkers[seriesRoot] <= 0 {
			delete(d.activeSeriesWorkers, seriesRoot)
		}
		d.queueWorkerStateLock.Unlock()
	}
}

// startQueueLog groups overlapping jobs into one explicitly named batch.
// LoggerHub supports only one active start/end stream, so emitting one marker
// pair per worker would silently mix the second job into the first job's log.
func (d *Downloader) startQueueLog(jobID string) func() {
	d.queueLogLock.Lock()
	if d.queueLogActive == 0 {
		d.queueLogBatchID = fmt.Sprintf("batch-%d", time.Now().UnixNano())
		d.log.Infoln("------------------------------------------")
		d.log.Infoln(log_helper.OnceSubsScanStart + "#" + d.queueLogBatchID)
		d.log.Infof("Queue log batch started batch=%s job=%s", d.queueLogBatchID, jobID)
	} else {
		d.log.Infof("Queue log batch joined batch=%s job=%s", d.queueLogBatchID, jobID)
	}
	d.queueLogActive++
	d.queueLogLock.Unlock()

	return func() {
		d.queueLogLock.Lock()
		d.log.Infof("Queue log batch job finished batch=%s job=%s", d.queueLogBatchID, jobID)
		d.queueLogActive--
		if d.queueLogActive == 0 {
			d.log.Infof("Queue log batch finished batch=%s", d.queueLogBatchID)
			d.log.Infoln(log_helper.OnceSubsScanEnd)
			d.log.Infoln("------------------------------------------")
			d.queueLogBatchID = ""
		}
		d.queueLogLock.Unlock()
	}
}
