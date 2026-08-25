package settings

import "testing"

func TestTaskQueueDownloadConcurrencyDefaultsAndBounds(t *testing.T) {
	defaults := NewTaskQueue()
	if defaults.DownloadConcurrency != 2 {
		t.Fatalf("unexpected default concurrency: %d", defaults.DownloadConcurrency)
	}

	for _, invalid := range []int{0, -1, 5} {
		queue := &TaskQueue{MaxRetryTimes: 3, DownloadConcurrency: invalid, OneJobTimeOut: 300,
			Interval: 10, ExpirationTime: 90, DownloadSubDuringXDays: 7, OneSubDownloadInterval: 12}
		queue.Check()
		if queue.DownloadConcurrency != 2 {
			t.Fatalf("invalid concurrency %d normalized to %d", invalid, queue.DownloadConcurrency)
		}
	}

	queue := NewTaskQueue()
	queue.DownloadConcurrency = 4
	queue.Check()
	if queue.DownloadConcurrency != 4 {
		t.Fatalf("valid concurrency changed: %d", queue.DownloadConcurrency)
	}
}
