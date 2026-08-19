package downloader

import (
	"fmt"
	"sync"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	subSupplier "github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_supplier"
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
	active, shouldCleanup := d.updateQueueWorkerOnFinishLocked(didWork)
	if shouldCleanup {
		if err := pkg.ClearRootTmpFolder(); err != nil {
			d.log.Errorln("ClearRootTmpFolder", err)
		}
		if !pkg.LiteMode() {
			pkg.CloseChrome(d.log)
		}
	}
	d.queueWorkerStateLock.Unlock()
	<-d.queueWorkerSlots
	d.log.Debugf("Queue worker released active=%d capacity=%d", active, cap(d.queueWorkerSlots))
}

// updateQueueWorkerOnFinishLocked updates only the cleanup state machine.
// The caller holds queueWorkerStateLock so cleanup cannot overlap a newly
// admitted worker.
func (d *Downloader) updateQueueWorkerOnFinishLocked(didWork bool) (active int, shouldCleanup bool) {
	if didWork {
		d.queueCleanupPending = true
	}
	d.activeQueueWorkers--
	active = d.activeQueueWorkers
	if active == 0 && d.queueCleanupPending {
		d.queueCleanupPending = false
		shouldCleanup = true
	}
	return active, shouldCleanup
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
