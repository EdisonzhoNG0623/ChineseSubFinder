package task_queue

import (
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

const (
	noSubRetryBase      = 12 * time.Hour
	noSubRetryMax       = 14 * 24 * time.Hour
	transientRetryBase  = 30 * time.Minute
	transientRetryMax   = 6 * time.Hour
	persistentRetryBase = 12 * time.Hour
	persistentRetryMax  = 7 * 24 * time.Hour
	unknownRetryBase    = time.Hour
	unknownRetryMax     = 24 * time.Hour
)

func retryDelay(oneJob taskQueue2.OneJob) time.Duration {
	message := strings.ToLower(oneJob.ErrorInfo)
	attempts := oneJob.DownloadTimes
	if attempts < 1 {
		attempts = 1
	}

	switch {
	case isNoSubError(message):
		return exponentialBackoff(noSubRetryBase, noSubRetryMax, attempts)
	case isTransientError(message):
		return exponentialBackoff(transientRetryBase, transientRetryMax, attempts)
	case isPersistentLocalError(message):
		return exponentialBackoff(persistentRetryBase, persistentRetryMax, attempts)
	default:
		return exponentialBackoff(unknownRetryBase, unknownRetryMax, attempts)
	}
}

func exponentialBackoff(base, maximum time.Duration, attempts int) time.Duration {
	delay := base
	for step := 1; step < attempts && delay < maximum; step++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func isNoSubError(message string) bool {
	return strings.Contains(message, "no sub found") ||
		strings.Contains(message, "not one fit") ||
		strings.Contains(message, "no subtitle found")
}

func isTransientError(message string) bool {
	transientMarkers := []string{
		"context deadline exceeded",
		"timeout",
		"timed out",
		"connection reset",
		"connection refused",
		"temporary",
		"network",
		"tls handshake",
		"unexpected eof",
		"too many requests",
		"status code 429",
	}
	for _, marker := range transientMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isPersistentLocalError(message string) bool {
	persistentMarkers := []string{
		"no metadata file",
		"movie.xml",
		"permission denied",
		"read-only file system",
		"no such file or directory",
		"decode.getvideonfoinfo",
	}
	for _, marker := range persistentMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func nextAttemptAt(oneJob taskQueue2.OneJob) time.Time {
	if oneJob.ForceRun || oneJob.DownloadTimes == 0 {
		return time.Time{}
	}
	if explicit := time.Time(oneJob.NextAttemptTime); !explicit.IsZero() {
		return explicit
	}
	return time.Time(oneJob.UpdateTime).Add(retryDelay(oneJob))
}

func scheduleRetry(oneJob *taskQueue2.OneJob, now time.Time) {
	oneJob.NextAttemptTime = emby.Time(now.Add(retryDelay(*oneJob)))
	oneJob.ForceRun = false
}

func clearRetrySchedule(oneJob *taskQueue2.OneJob) {
	oneJob.NextAttemptTime = emby.Time{}
	oneJob.ForceRun = false
}
