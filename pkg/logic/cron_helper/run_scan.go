package cron_helper

// RunScanNow starts one scan immediately without changing the configured cron
// schedule. The scan helper already rejects overlapping runs.
func (ch *CronHelper) RunScanNow() bool {
	ch.cronLock.Lock()
	canRun := ch.cronHelperRunning && !ch.stopping
	ch.cronLock.Unlock()
	if !canRun {
		return false
	}
	go func() {
		ch.Logger.Infoln("Manual scan requested")
		ch.scanVideoProcessAdd2DownloadQueue()
	}()
	return true
}
