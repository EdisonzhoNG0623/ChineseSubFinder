package downloader

import "github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"

// seriesDownloadOutcomeError makes an empty result an explicit failure. The
// old path passed nil to AutoDetectUpdateJobStatus when every supplier returned
// no usable subtitle, which marked the job Done and cleared its retry schedule.
func seriesDownloadOutcomeError(savedSubCount int, saveErr error) error {
	if savedSubCount < 1 && saveErr == nil {
		return task_queue.ErrNoSubFound
	}
	return saveErr
}
