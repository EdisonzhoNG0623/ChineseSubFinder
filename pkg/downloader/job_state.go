package downloader

import (
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func markJobIgnored(job *taskQueueTypes.OneJob) {
	job.JobStatus = taskQueueTypes.Ignore
	job.ErrorInfo = ""
	job.RetryTimes = 0
	job.NextAttemptTime = emby.Time{}
	job.ForceRun = false
}
