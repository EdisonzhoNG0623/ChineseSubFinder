package task_queue

import (
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	queueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

// stampSearchAttempt records the exact public policy and episode identity an
// attempt is about to use. Credentials, paths and raw query strings are never
// included in either fingerprint.
func stampSearchAttempt(job *queueTypes.OneJob, policyFingerprint string) {
	if job == nil {
		return
	}
	job.RefreshSearchFingerprint()
	job.LastAttemptSearchFingerprint = job.SearchFingerprint
	job.LastAttemptPolicyFingerprint = policyFingerprint
	job.LegacySearchEvidenceBaseline = false
}

// migrateLegacySearchEvidence gives pre-fingerprint attempts the current
// policy/identity as a neutral baseline. Without this migration, deploying the
// feature itself would turn thousands of old misses into immediately-ready
// work even though no evidence actually changed.
func (t *TaskQueue) migrateLegacySearchEvidence() {
	policyFingerprint := settings.CurrentSearchPolicyFingerprint()
	changedPriorities := make(map[int]struct{})
	migrated := 0
	for priority := 0; priority <= taskPriorityCount; priority++ {
		t.taskPriorityMapList[priority].Each(func(key interface{}, value interface{}) {
			job := value.(queueTypes.OneJob)
			if job.DownloadTimes <= 0 || (job.VideoType != common.Series && job.VideoType != common.Anime) ||
				!isNoSubError(strings.ToLower(job.ErrorInfo)) || (job.LastAttemptPolicyFingerprint != "" &&
				job.SearchEvidenceVersion >= queueTypes.SearchEvidenceVersion) {
				return
			}
			// A v1 record that already persisted aliases has no unrecorded alias
			// fill to neutralize. Keeping the one-shot flag in that case would
			// suppress the next real alias correction and leave a conclusive miss
			// asleep until its ordinary backoff expires.
			needsNeutralAliasFill := len(queueTypes.NormalizeSearchAliases(job.SearchAliases...)) == 0
			stampSearchAttempt(&job, policyFingerprint)
			job.LegacySearchEvidenceBaseline = needsNeutralAliasFill
			job.StateRevision = nextStateRevision(job.StateRevision)
			t.taskPriorityMapList[priority].Put(key, job)
			changedPriorities[priority] = struct{}{}
			migrated++
		})
	}
	for priority := range changedPriorities {
		if err := t.save(priority); err != nil {
			t.log.Errorln("TaskQueue persist search evidence baseline failed", priority, err)
		}
	}
	if migrated > 0 {
		t.log.Infof("TaskQueue established search evidence baseline for %d legacy attempts", migrated)
	}
}

// mergeNoSubtitleEvidenceLocked accepts fresh scanner evidence without
// resetting an existing task's retry counters, timestamps, status or error.
// It is intentionally limited to waiting conclusive misses: policy/metadata
// improvements must not bypass local filesystem errors or terminal states.
func (t *TaskQueue) mergeNoSubtitleEvidenceLocked(incoming queueTypes.OneJob) (queueTypes.OneJob, bool) {
	existing, found := t.jobByIDLocked(incoming.Id)
	if !found || existing.JobStatus != queueTypes.Waiting || existing.DownloadTimes <= 0 ||
		!isNoSubError(strings.ToLower(existing.ErrorInfo)) ||
		(existing.VideoType != common.Series && existing.VideoType != common.Anime) ||
		(incoming.VideoType != common.Series && incoming.VideoType != common.Anime) {
		return existing, false
	}
	if _, claimed := t.claimedJobs[incoming.Id]; claimed {
		// The in-flight batch owns the attempt identity. A later recurring scan
		// will merge new evidence after its outcome releases the claim.
		return existing, false
	}

	changed := false
	setString := func(target *string, value string) {
		value = strings.TrimSpace(value)
		if value != "" && *target != value {
			*target = value
			changed = true
		}
	}
	setPositive := func(target *int, value int) {
		if value > 0 && *target != value {
			*target = value
			changed = true
		}
	}
	setAliases := func(values []string) {
		values = queueTypes.NormalizeSearchAliases(values...)
		if len(values) == 0 {
			return
		}
		current := queueTypes.NormalizeSearchAliases(existing.SearchAliases...)
		if equalSearchAliases(current, values) {
			return
		}
		existing.SearchAliases = append([]string(nil), values...)
		changed = true
	}

	setString(&existing.SeriesName, incoming.SeriesName)
	setAliases(incoming.SearchAliases)
	setString(&existing.SeriesRootDirPath, incoming.SeriesRootDirPath)
	setString(&existing.MediaServerInsideVideoID, incoming.MediaServerInsideVideoID)
	setPositive(&existing.Season, incoming.Season)
	setPositive(&existing.Episode, incoming.Episode)

	// Numbering candidates are comparable only when the new scanner produced
	// actual evidence. Equal confidence is accepted so corrected mapping data
	// can replace a stale value without inventing a confidence bump.
	hasIncomingNumbering := incoming.AbsoluteEpisode > 0 || incoming.SceneSeason > 0 ||
		incoming.SceneEpisode > 0 || strings.TrimSpace(incoming.NumberingSource) != ""
	if hasIncomingNumbering && (existing.NumberingSource == "" ||
		incoming.NumberingConfidence >= existing.NumberingConfidence) {
		if existing.AbsoluteEpisode != incoming.AbsoluteEpisode ||
			existing.SceneSeason != incoming.SceneSeason || existing.SceneEpisode != incoming.SceneEpisode ||
			existing.NumberingSource != strings.TrimSpace(incoming.NumberingSource) ||
			existing.NumberingConfidence != incoming.NumberingConfidence {
			existing.AbsoluteEpisode = incoming.AbsoluteEpisode
			existing.SceneSeason = incoming.SceneSeason
			existing.SceneEpisode = incoming.SceneEpisode
			existing.NumberingSource = strings.TrimSpace(incoming.NumberingSource)
			existing.NumberingConfidence = incoming.NumberingConfidence
			changed = true
		}
	}

	previousFingerprint := existing.SearchFingerprint
	existing.RefreshSearchFingerprint()
	searchFingerprintChanged := existing.SearchFingerprint != previousFingerprint
	if searchFingerprintChanged {
		changed = true
	}
	if searchFingerprintChanged && existing.LegacySearchEvidenceBaseline {
		// The first scanner pass after introducing evidence fields fills metadata
		// that may have existed during the old attempt but was never persisted.
		// Treat that one merge as a neutral baseline; later alias/title/numbering
		// changes compare normally and wake the conclusive miss immediately.
		existing.LastAttemptSearchFingerprint = existing.SearchFingerprint
		existing.LegacySearchEvidenceBaseline = false
	}
	return existing, changed
}

func equalSearchAliases(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
