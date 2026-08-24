package task_queue

import (
	"strings"
	"time"

	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

type ErrorCategory string

const (
	ErrorCategoryNone       ErrorCategory = "NONE"
	ErrorCategoryNoSubtitle ErrorCategory = "NO_SUBTITLE"
	ErrorCategoryTransient  ErrorCategory = "TRANSIENT"
	ErrorCategoryLocal      ErrorCategory = "LOCAL"
	ErrorCategoryBlocked    ErrorCategory = "PROVIDER_BLOCKED"
	ErrorCategoryUnknown    ErrorCategory = "UNKNOWN"
)

type RetryDiagnostic struct {
	Category       ErrorCategory
	IsScheduled    bool
	IsReady        bool
	IsForced       bool
	NextAttemptAt  time.Time
	RetryInSeconds int64
}

// DiagnoseRetry exposes the queue's retry decision as stable, structured data.
// It deliberately does not expose raw paths or supplier response bodies.
func DiagnoseRetry(job taskQueueTypes.OneJob, now time.Time) RetryDiagnostic {
	next := nextAttemptAt(job)
	diagnostic := RetryDiagnostic{
		Category:      ClassifyErrorInfo(job.ErrorInfo),
		IsForced:      job.ForceRun,
		NextAttemptAt: next,
	}
	if !next.IsZero() && job.JobStatus == taskQueueTypes.Waiting && !job.ForceRun {
		diagnostic.IsScheduled = true
		diagnostic.IsReady = !next.After(now)
		if next.After(now) {
			diagnostic.RetryInSeconds = int64(next.Sub(now).Round(time.Second) / time.Second)
		}
	}
	return diagnostic
}

func ClassifyErrorInfo(message string) ErrorCategory {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return ErrorCategoryNone
	}
	if strings.Contains(message, "verification") || strings.Contains(message, "captcha") ||
		strings.Contains(message, "cloudflare") || strings.Contains(message, "blocked") ||
		strings.Contains(message, "forbidden") || strings.Contains(message, "status code 403") {
		return ErrorCategoryBlocked
	}
	if isNoSubError(message) {
		return ErrorCategoryNoSubtitle
	}
	if isTransientError(message) {
		return ErrorCategoryTransient
	}
	if isPersistentLocalError(message) {
		return ErrorCategoryLocal
	}
	return ErrorCategoryUnknown
}
