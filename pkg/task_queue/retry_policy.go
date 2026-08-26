package task_queue

import (
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

const (
	noSubRetryBase      = 12 * time.Hour
	noSubRetryMax       = 30 * 24 * time.Hour
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
		return noSubRetryDelay(attempts)
	case isTransientError(message):
		return exponentialBackoff(transientRetryBase, transientRetryMax, attempts)
	case isPersistentLocalError(message):
		return exponentialBackoff(persistentRetryBase, persistentRetryMax, attempts)
	default:
		return exponentialBackoff(unknownRetryBase, unknownRetryMax, attempts)
	}
}

// noSubRetryDelay keeps one relatively quick retry for newly released media,
// then backs off aggressively. Re-querying every one or two days after
// repeated empty results wastes supplier quota without materially improving
// the hit rate of a large historical queue.
func noSubRetryDelay(attempts int) time.Duration {
	switch {
	case attempts <= 1:
		return noSubRetryBase
	case attempts == 2:
		return 2 * 24 * time.Hour
	case attempts == 3:
		return 7 * 24 * time.Hour
	default:
		return noSubRetryMax
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
		strings.Contains(message, "all site download sub not found") ||
		strings.Contains(message, "no sub downloaded") ||
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
		"series metadata episode not found",
		"series metadata root not found",
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
	calculated := time.Time(oneJob.UpdateTime).Add(retryDelay(oneJob))
	if explicit := time.Time(oneJob.NextAttemptTime); !isUnsetRetryTime(explicit) {
		// Persisted schedules may have been written by an older, more aggressive
		// policy. Keep an explicit later time, but apply the current minimum now.
		if explicit.After(calculated) {
			return explicit
		}
		return calculated
	}
	return calculated
}

// emby.Time serializes its zero value as "0001-01-01T00:00:00" and parses
// it back in time.Local. That parsed value is not time.Time.IsZero() in
// non-UTC zones, even though it is the persisted representation of "unset".
func isUnsetRetryTime(value time.Time) bool {
	return value.IsZero() || value.Year() <= 1
}

func scheduleRetry(oneJob *taskQueue2.OneJob, now time.Time) {
	oneJob.NextAttemptTime = emby.Time(now.Add(retryDelay(*oneJob)))
	oneJob.ForceRun = false
}

func clearRetrySchedule(oneJob *taskQueue2.OneJob) {
	oneJob.NextAttemptTime = emby.Time{}
	oneJob.ForceRun = false
}

func retryLifetimeExpired(oneJob taskQueue2.OneJob, now time.Time, expirationDays int) bool {
	return time.Time(oneJob.AddedTime).AddDate(0, 0, expirationDays).Before(now)
}
