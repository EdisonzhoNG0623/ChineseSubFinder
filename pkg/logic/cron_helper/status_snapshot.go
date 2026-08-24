package cron_helper

import "time"

type StatusSnapshot struct {
	Status        string    `json:"status"`
	NextScanAt    time.Time `json:"next_scan_at,omitempty"`
	ActiveWorkers int       `json:"active_workers"`
}

func (ch *CronHelper) StatusSnapshot() StatusSnapshot {
	snapshot := StatusSnapshot{Status: ch.CronRunningStatusString()}
	ch.cronLock.Lock()
	cronInstance := ch.c
	entryID := ch.entryIDScanVideoProcess
	running := ch.cronHelperRunning
	ch.cronLock.Unlock()
	if running && cronInstance != nil {
		snapshot.NextScanAt = cronInstance.Entry(entryID).Next
	}
	if ch.Downloader != nil {
		snapshot.ActiveWorkers = ch.Downloader.ActiveQueueWorkers()
	}
	return snapshot
}
