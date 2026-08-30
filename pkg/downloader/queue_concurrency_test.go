package downloader

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	subSupplier "github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_supplier"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/supplier_search"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

func TestTryStartQueueWorkerHonorsCapacity(t *testing.T) {
	d := &Downloader{
		log:              logrus.New(),
		queueWorkerSlots: make(chan struct{}, 2),
	}

	if !d.tryStartQueueWorker() {
		t.Fatal("first worker should be admitted")
	}
	if !d.tryStartQueueWorker() {
		t.Fatal("second worker should be admitted")
	}
	if d.tryStartQueueWorker() {
		t.Fatal("third worker should be rejected")
	}
	if got := d.activeQueueWorkers; got != 2 {
		t.Fatalf("active workers = %d, want 2", got)
	}
}

func TestSupplierHubSwapAndReadAreSynchronized(t *testing.T) {
	d := &Downloader{}
	hubs := []*subSupplier.SubSupplierHub{
		{},
		{},
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(index int) {
			defer wg.Done()
			d.setSubSupplierHub(hubs[index%len(hubs)])
		}(i)
		go func() {
			defer wg.Done()
			_ = d.getSubSupplierHub()
		}()
	}
	wg.Wait()
	if d.getSubSupplierHub() == nil {
		t.Fatal("supplier hub should remain available")
	}
}

func TestSeriesWorkerLockSerializesSameSeries(t *testing.T) {
	d := &Downloader{}
	unlockFirst := d.lockSeriesWorker("/series/a")
	acquired := make(chan struct{})
	go func() {
		unlockSecond := d.lockSeriesWorker("/series/a")
		close(acquired)
		unlockSecond()
	}()

	select {
	case <-acquired:
		t.Fatal("second worker acquired the same series while first held it")
	case <-time.After(30 * time.Millisecond):
	}

	unlockFirst()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second worker did not acquire the series after release")
	}
}

func TestRegisterSeriesWorkerAppearsInSnapshotUntilReleased(t *testing.T) {
	d := &Downloader{}
	release := d.registerSeriesWorker("/media/series-a")
	if _, found := d.activeSeriesSnapshot()["/media/series-a"]; !found {
		t.Fatal("registered series missing from active snapshot")
	}
	release()
	if _, found := d.activeSeriesSnapshot()["/media/series-a"]; found {
		t.Fatal("released series remains in active snapshot")
	}
}

func TestQueueLogUsesOneExplicitBatchForOverlappingJobs(t *testing.T) {
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	d := &Downloader{log: logger}

	endFirst := d.startQueueLog("job-a")
	endSecond := d.startQueueLog("job-b")
	endFirst()
	if strings.Contains(output.String(), "OneTimeSubtitleScanEnd") {
		t.Fatal("batch ended while the second job was still active")
	}
	endSecond()

	logs := output.String()
	if got := strings.Count(logs, "OneTimeSubtitleScanStart"); got != 1 {
		t.Fatalf("start marker count = %d, want 1", got)
	}
	if got := strings.Count(logs, "OneTimeSubtitleScanEnd"); got != 1 {
		t.Fatalf("end marker count = %d, want 1", got)
	}
	for _, want := range []string{"job-a", "job-b", "Queue log batch joined"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("batch log missing %q", want)
		}
	}
}

func TestCleanupPendingSurvivesUntilLastWorker(t *testing.T) {
	d := &Downloader{activeQueueWorkers: 2}
	active, scheduled := d.updateQueueWorkerOnFinishLocked(true)
	if active != 1 || scheduled {
		t.Fatalf("working worker exit returned active=%d scheduled=%t, want 1,false", active, scheduled)
	}
	active, scheduled = d.updateQueueWorkerOnFinishLocked(false)
	if active != 0 || !scheduled {
		t.Fatalf("last idle worker exit returned active=%d scheduled=%t, want 0,true", active, scheduled)
	}
	if !d.queueCleanupPending || !d.queueCleanupScheduled {
		t.Fatal("cleanup state was cleared before asynchronous cleanup")
	}
}

func TestIdleWorkersDoNotRequestCleanup(t *testing.T) {
	d := &Downloader{activeQueueWorkers: 1}
	active, cleanup := d.updateQueueWorkerOnFinishLocked(false)
	if active != 0 || cleanup {
		t.Fatalf("idle worker exit returned active=%d cleanup=%t, want 0,false", active, cleanup)
	}
}

func TestCanceledDownloaderStopsDeferredCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Downloader{
		ctx: ctx, log: logrus.New(),
		queueCleanupPending: true, queueCleanupScheduled: true,
	}
	finishSharedResourceUse := supplier_search.BeginSharedResourceUse()
	defer finishSharedResourceUse()
	d.scheduleQueueCleanup()
	cancel()

	done := make(chan struct{})
	go func() {
		d.queueCleanupWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled downloader retained a deferred cleanup goroutine")
	}
	d.queueWorkerStateLock.Lock()
	scheduled := d.queueCleanupScheduled
	pending := d.queueCleanupPending
	d.queueWorkerStateLock.Unlock()
	if scheduled || !pending {
		t.Fatalf("canceled cleanup state scheduled=%t pending=%t", scheduled, pending)
	}
}
