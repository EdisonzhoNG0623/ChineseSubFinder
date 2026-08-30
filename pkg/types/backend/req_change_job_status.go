package backend

import (
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

type ReqChangeJobStatus struct {
	Id           string                `json:"id"`            // 任务的唯一 ID
	TaskPriority *string               `json:"task_priority"` // 可选：high、middle 或 low
	JobStatus    *task_queue.JobStatus `json:"job_status"`    // 可选：Waiting(0) 或 Ignore(5)
}
