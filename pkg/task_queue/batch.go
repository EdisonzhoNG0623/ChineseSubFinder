package task_queue

import (
	"errors"
	"fmt"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	queueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/emirpasic/gods/sets/treeset"
)

var ErrClaimUnavailable = errors.New("task queue claim is no longer available")

const outcomePersistenceAttempts = 3

// ErrPartialCommit means at least one new priority snapshot is durable while
// one or more source snapshots still need FlushDirtyPriorities. The in-memory
// transition is intentionally retained because rolling it back would diverge
// from the snapshots that were already replaced.
var ErrPartialCommit = errors.New("task queue outcomes partially persisted")

// JobOutcome is one member of an atomic in-memory batch transition. The queue
// persists each affected priority bucket once after all transitions are
// applied.
type JobOutcome struct {
	Job queueTypes.OneJob
	Err error
}

// ClaimBatch validates a selected primary and its series companions under one
// queue lock. The primary is durably marked Downloading; companions stay in
// their crash-safe Waiting state but are removed from the in-memory schedule
// until the primary outcome releases the claim.
func (t *TaskQueue) ClaimBatch(candidates []queueTypes.OneJob, now time.Time) ([]queueTypes.OneJob, error) {
	if len(candidates) == 0 {
		return nil, ErrClaimUnavailable
	}

	t.queueLock.Lock()
	defer t.queueLock.Unlock()

	primary, found := t.jobByIDLocked(candidates[0].Id)
	if !found || !jobReadyAt(primary, now) {
		return nil, ErrClaimUnavailable
	}
	if _, alreadyClaimed := t.claimedJobs[primary.Id]; alreadyClaimed {
		return nil, ErrClaimUnavailable
	}
	originalPrimary := primary
	originalDirtyPriorities := clonePrioritySet(t.dirtyPriorities)

	claimed := make([]queueTypes.OneJob, 0, len(candidates))
	claimed = append(claimed, primary)
	seen := map[string]struct{}{primary.Id: {}}
	for _, candidate := range candidates[1:] {
		if _, duplicate := seen[candidate.Id]; duplicate {
			continue
		}
		job, exists := t.jobByIDLocked(candidate.Id)
		if !exists || job.JobStatus != queueTypes.Waiting || !jobReadyAt(job, now) ||
			job.SeriesRootDirPath != primary.SeriesRootDirPath || job.Season != primary.Season {
			continue
		}
		seen[job.Id] = struct{}{}
		claimed = append(claimed, job)
	}
	policyFingerprint := settings.CurrentSearchPolicyFingerprint()
	t.nextClaimToken++
	if t.nextClaimToken == 0 {
		// Zero means "not claimed" on transient OneJob copies.
		t.nextClaimToken++
	}
	claimToken := t.nextClaimToken
	for index := range claimed {
		claimed[index].ClaimToken = claimToken
		claimed[index].NotBeforeTime = emby.Time{}
	}
	primary = claimed[0]

	// Reserve every currently-ready member of the active series, including
	// episodes beyond this bounded 4/8/12 search batch. Otherwise those entries
	// would keep NextWakeAt at "now" and make the dispatcher spin while the
	// series worker lock correctly excludes them.
	reserved := make([]string, 0, len(claimed))
	reserve := func(jobID string) {
		if _, exists := t.claimedJobs[jobID]; exists {
			return
		}
		t.claimedJobs[jobID] = primary.Id
		t.claimTokens[jobID] = claimToken
		reserved = append(reserved, jobID)
		t.removeScheduledLocked(jobID)
	}
	reserve(primary.Id)
	if primary.SeriesRootDirPath != "" {
		if setValue, exists := t.taskGroupBySeries.Get(primary.SeriesRootDirPath); exists {
			for _, idValue := range setValue.(*treeset.Set).Values() {
				jobID := idValue.(string)
				job, jobExists := t.jobByIDLocked(jobID)
				if jobExists && jobReadyAt(job, now) {
					reserve(jobID)
				}
			}
		}
	}
	t.claimMembers[primary.Id] = reserved
	t.claimOriginals[primary.Id] = originalPrimary
	t.claimPolicies[primary.Id] = policyFingerprint

	oldPriority := primary.TaskPriority
	primary.JobStatus = queueTypes.Downloading
	primary.ForceRun = false
	primary.UpdateTime = emby.Time(now)
	primary.StateRevision = nextStateRevision(originalPrimary.StateRevision)
	t.taskPriorityMapList[oldPriority].Put(primary.Id, primary)
	claimed[0] = primary
	claimSave := t.saveChangedPrioritiesWithResultLocked(
		map[int]struct{}{oldPriority: {}},
		map[int]struct{}{oldPriority: {}},
	)
	if claimSave.err != nil {
		// Keep the in-memory queue selectable when persistence rejects the
		// claim. The durable snapshot still contains the original job.
		t.taskPriorityMapList[oldPriority].Put(primary.Id, originalPrimary)
		t.releaseClaimLocked(primary.Id)
		t.dirtyPriorities = originalDirtyPriorities
		// Do not emit an immediate queue edge while persistence is unavailable.
		// The admitted worker's availability edge retains the dispatcher's bounded
		// retry timer; a new queue mutation can still wake it earlier.
		return nil, claimSave.err
	}
	// An unrelated snapshot may already be dirty from a safe partial commit.
	// Failure to flush it cannot invalidate the claim snapshot written above;
	// returning a claim error here would strand a durable Downloading job.
	if err := t.retryDirtyPrioritiesLocked(); err != nil {
		t.log.WithError(err).Warn("task queue claim committed with pending dirty snapshots")
	}
	t.signalWakeLocked()
	return claimed, nil
}

func jobReadyAt(job queueTypes.OneJob, now time.Time) bool {
	switch job.JobStatus {
	case queueTypes.Waiting:
		if !job.ForceRun && retryLifetimeExpired(job, now, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime) {
			return false
		}
		readyAt := nextAttemptAt(job)
		return readyAt.IsZero() || !readyAt.After(now)
	case queueTypes.Done:
		if notBefore := time.Time(job.NotBeforeTime); !isUnsetRetryTime(notBefore) && notBefore.After(now) {
			return false
		}
		if time.Time(job.CreatedTime).AddDate(0, 0, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime).Before(now) {
			return false
		}
		readyAt := time.Time(job.UpdateTime).Add(time.Duration(settings.Get().AdvancedSettings.TaskQueue.OneSubDownloadInterval) * time.Hour)
		return !readyAt.After(now)
	default:
		return false
	}
}

func (t *TaskQueue) jobByIDLocked(jobID string) (queueTypes.OneJob, bool) {
	priorityValue, found := t.taskKeyMap.Get(jobID)
	if !found {
		return queueTypes.OneJob{}, false
	}
	jobValue, found := t.taskPriorityMapList[priorityValue.(int)].Get(jobID)
	if !found {
		return queueTypes.OneJob{}, false
	}
	return jobValue.(queueTypes.OneJob), true
}

func (t *TaskQueue) releaseClaimLocked(claimID string) {
	members, found := t.claimMembers[claimID]
	if !found {
		return
	}
	delete(t.claimMembers, claimID)
	delete(t.claimOriginals, claimID)
	delete(t.claimPolicies, claimID)
	for _, jobID := range members {
		delete(t.claimedJobs, jobID)
		delete(t.claimTokens, jobID)
		if job, exists := t.jobByIDLocked(jobID); exists {
			t.upsertScheduledLocked(job)
		}
	}
}

func (t *TaskQueue) detachClaimMemberLocked(jobID string) {
	claimID, claimed := t.claimedJobs[jobID]
	if !claimed {
		return
	}
	if claimID == jobID {
		t.releaseClaimLocked(claimID)
		return
	}
	delete(t.claimedJobs, jobID)
	delete(t.claimTokens, jobID)
	members := t.claimMembers[claimID]
	for index, memberID := range members {
		if memberID == jobID {
			t.claimMembers[claimID] = append(members[:index], members[index+1:]...)
			break
		}
	}
}

// ApplyOutcomes applies a full series batch before writing any queue snapshot.
// This replaces N calls to AutoDetectUpdateJobStatus with at most one write per
// source/destination priority while retaining the same retry state machine.
func (t *TaskQueue) ApplyOutcomes(outcomes []JobOutcome) error {
	if len(outcomes) == 0 {
		return nil
	}

	now := time.Now()
	t.queueLock.Lock()

	// Validate claim ownership before releasing or mutating anything. Deleted
	// companions and stale generations are skipped, while valid members of the
	// same batch can still complete. This prevents a late worker from
	// overwriting a user's reset/ignore action or a newer claim.
	seen := make(map[string]struct{}, len(outcomes))
	valid := make([]JobOutcome, 0, len(outcomes))
	skipped := 0
	for _, outcome := range outcomes {
		if _, duplicate := seen[outcome.Job.Id]; duplicate {
			continue
		}
		seen[outcome.Job.Id] = struct{}{}
		stored, exists := t.jobByIDLocked(outcome.Job.Id)
		if !exists {
			skipped++
			continue
		}
		currentToken, claimed := t.claimTokens[outcome.Job.Id]
		switch {
		case outcome.Job.ClaimToken != 0 && (!claimed || currentToken != outcome.Job.ClaimToken):
			skipped++
			continue
		case outcome.Job.ClaimToken == 0 && claimed:
			skipped++
			continue
		case outcome.Job.ClaimToken == 0 && !sameUnclaimedLifecycleSnapshot(outcome.Job, stored):
			// Token-zero outcomes are produced by pre-claim validation and startup
			// recovery. Treat their lifecycle fields as a compare-and-swap snapshot:
			// a manual ignore/reset after the worker read the job must win over the
			// stale validation error.
			skipped++
			continue
		}
		valid = append(valid, outcome)
	}
	if len(valid) == 0 {
		t.queueLock.Unlock()
		return ErrClaimUnavailable
	}

	// The first snapshot write is the transaction boundary. Until it succeeds,
	// both the jobs and their process-local claim remain fully recoverable so the
	// same worker can retry the outcomes without re-downloading the batch.
	originalJobs := make(map[string]queueTypes.OneJob, len(valid))
	for _, outcome := range valid {
		originalJobs[outcome.Job.Id], _ = t.jobByIDLocked(outcome.Job.Id)
	}
	originalClaimedJobs := cloneStringMap(t.claimedJobs)
	originalClaimMembers := cloneStringSliceMap(t.claimMembers)
	originalClaimTokens := cloneUint64Map(t.claimTokens)
	originalClaimOriginals := cloneJobMap(t.claimOriginals)
	originalClaimPolicies := cloneStringMap(t.claimPolicies)
	originalDirtyPriorities := clonePrioritySet(t.dirtyPriorities)

	claimIDs := make(map[string]struct{})
	attemptPolicies := make(map[string]string, len(valid))
	for _, outcome := range valid {
		if claimID, claimed := t.claimedJobs[outcome.Job.Id]; claimed {
			claimIDs[claimID] = struct{}{}
			attemptPolicies[outcome.Job.Id] = t.claimPolicies[claimID]
		}
	}
	for claimID := range claimIDs {
		t.releaseClaimLocked(claimID)
	}

	changed := make(map[int]struct{})
	destinations := make(map[int]struct{})
	moves := make([]priorityMove, 0, len(valid))
	applied := make([]JobOutcome, 0, len(valid))
	seen = make(map[string]struct{}, len(valid))
	for _, outcome := range valid {
		if _, duplicate := seen[outcome.Job.Id]; duplicate {
			continue
		}
		seen[outcome.Job.Id] = struct{}{}
		stored, _ := t.jobByIDLocked(outcome.Job.Id)

		job := outcome.Job
		job.ClaimToken = 0
		// The caller may enrich identity/search metadata, but persisted lifecycle
		// fields remain authoritative if another queue operation touched a member.
		job.TaskPriority = stored.TaskPriority
		job.JobStatus = stored.JobStatus
		job.RetryTimes = stored.RetryTimes
		job.DownloadTimes = stored.DownloadTimes
		job.ErrorInfo = stored.ErrorInfo
		job.NextAttemptTime = stored.NextAttemptTime
		job.ForceRun = stored.ForceRun
		policyFingerprint := attemptPolicies[job.Id]
		if policyFingerprint == "" {
			policyFingerprint = settings.CurrentSearchPolicyFingerprint()
		}
		stampSearchAttempt(&job, policyFingerprint)
		job.NotBeforeTime = emby.Time{}
		previousStatus, previousPriority := job.JobStatus, job.TaskPriority
		alreadyCompleted := outcome.Err == nil && (stored.JobStatus == queueTypes.Done || stored.JobStatus == queueTypes.Ignore)
		if !alreadyCompleted {
			job = t.transitionOutcomeLocked(job, outcome.Err, now)
		}
		job.UpdateTime = emby.Time(now)
		job.StateRevision = nextStateRevision(stored.StateRevision)

		t.removeScheduledLocked(job.Id)
		if previousPriority != job.TaskPriority {
			moves = append(moves, priorityMove{jobID: job.Id, from: previousPriority, to: job.TaskPriority, original: originalJobs[job.Id]})
			t.taskPriorityMapList[previousPriority].Remove(job.Id)
			changed[previousPriority] = struct{}{}
		}
		t.taskKeyMap.Put(job.Id, job.TaskPriority)
		t.taskPriorityMapList[job.TaskPriority].Put(job.Id, job)
		changed[job.TaskPriority] = struct{}{}
		destinations[job.TaskPriority] = struct{}{}
		t.upsertScheduledLocked(job)
		outcome.Job = job
		applied = append(applied, outcome)
		t.log.Infof("TaskQueue transition id=%s status=%d->%d priority=%d->%d attempts=%d retry=%d next=%s error_category=%s",
			job.Id, previousStatus, job.JobStatus, previousPriority, job.TaskPriority,
			job.DownloadTimes, job.RetryTimes, time.Time(job.NextAttemptTime).Format(time.RFC3339), ClassifyErrorInfo(job.ErrorInfo))
	}

	saveResult := t.saveChangedPrioritiesWithResultLocked(changed, destinations, moves...)
	if saveResult.err != nil && len(saveResult.saved) == 0 {
		for jobID, original := range originalJobs {
			if current, exists := t.jobByIDLocked(jobID); exists {
				t.taskPriorityMapList[current.TaskPriority].Remove(jobID)
			}
			t.taskKeyMap.Put(jobID, original.TaskPriority)
			t.taskPriorityMapList[original.TaskPriority].Put(jobID, original)
		}
		t.claimedJobs = originalClaimedJobs
		t.claimMembers = originalClaimMembers
		t.claimTokens = originalClaimTokens
		t.claimOriginals = originalClaimOriginals
		t.claimPolicies = originalClaimPolicies
		t.dirtyPriorities = originalDirtyPriorities
		t.rebuildScheduleIndexesLocked()
		t.signalWakeLocked()
		t.queueLock.Unlock()
		return saveResult.err
	}
	if saveResult.err != nil {
		t.signalWakeLocked()
		t.queueLock.Unlock()
		return fmt.Errorf("%w: saved %d priority snapshots, %d pending: %v",
			ErrPartialCommit, len(saveResult.saved), len(saveResult.pending), saveResult.err)
	}
	if err := t.retryDirtyPrioritiesLocked(); err != nil {
		t.signalWakeLocked()
		t.queueLock.Unlock()
		return fmt.Errorf("retry dirty task queue snapshots: %w", err)
	}
	t.signalWakeLocked()
	t.queueLock.Unlock()
	if skipped > 0 {
		t.log.Infof("TaskQueue ignored %d deleted or stale batch outcomes", skipped)
	}

	for _, outcome := range applied {
		result := "SUCCESS"
		if outcome.Err != nil {
			result = string(ClassifyErrorInfo(outcome.Err.Error()))
		}
		if err := t.center.TaskOutcomeAdd(now.Format("2006-01-02"), outcome.Job.VideoType.String(), result); err != nil {
			t.log.Warningln("TaskOutcomeAdd", err)
		}
	}
	return nil
}

// ApplyOutcomesReliable is the production finalization contract. A first-write
// failure intentionally leaves the process-local claim intact so the exact
// outcome can be retried without downloading again. If storage remains
// unavailable after a small bounded retry, the claim is released and its
// primary is restored to Waiting in memory; a dirty snapshot records any
// recovery write that still needs repair. No worker exit can therefore leave a
// permanently claimed, unscheduled task behind.
func (t *TaskQueue) ApplyOutcomesReliable(outcomes []JobOutcome) error {
	var previousErr error
	for attempt := 0; attempt < outcomePersistenceAttempts; attempt++ {
		err := t.ApplyOutcomes(outcomes)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, ErrPartialCommit):
			// The new lifecycle state is authoritative and the claim is already
			// released. FlushDirtyPriorities will repair the stale source bucket.
			return err
		case errors.Is(err, ErrClaimUnavailable):
			// A concurrent reset/delete or an earlier attempt already released
			// the generation. There is no live claim left to recover.
			if previousErr != nil {
				return fmt.Errorf("task queue outcome applied with pending persistence repair: %w", previousErr)
			}
			return err
		default:
			previousErr = err
		}
		if attempt+1 < outcomePersistenceAttempts {
			time.Sleep(time.Duration(1<<attempt) * 25 * time.Millisecond)
		}
	}

	jobs := make([]queueTypes.OneJob, 0, len(outcomes))
	for _, outcome := range outcomes {
		jobs = append(jobs, outcome.Job)
	}
	releaseErr := t.ReleaseClaimsForRetry(jobs, time.Minute)
	if releaseErr != nil {
		return fmt.Errorf("persist task queue outcomes after %d attempts: %v; release for retry: %w",
			outcomePersistenceAttempts, previousErr, releaseErr)
	}
	return fmt.Errorf("persist task queue outcomes after %d attempts; claim released for retry: %w",
		outcomePersistenceAttempts, previousErr)
}

// ReleaseClaimsForRetry abandons a process-local claim without recording a
// download attempt. It is used for administrative shutdown and as the final
// persistence-failure safety net. Every reserved series member is released;
// the durable primary is restored to its pre-claim lifecycle with an
// independent not-before delay.
func (t *TaskQueue) ReleaseClaimsForRetry(jobs []queueTypes.OneJob, retryDelay time.Duration) error {
	now := time.Now()
	if retryDelay < 0 {
		retryDelay = 0
	}
	t.queueLock.Lock()
	defer t.queueLock.Unlock()

	claimIDs := make(map[string]struct{})
	for _, job := range jobs {
		claimID, claimed := t.claimedJobs[job.Id]
		if !claimed || job.ClaimToken == 0 || t.claimTokens[job.Id] != job.ClaimToken {
			continue
		}
		claimIDs[claimID] = struct{}{}
	}
	if len(claimIDs) == 0 {
		return nil
	}

	changed := make(map[int]struct{}, len(claimIDs))
	for claimID := range claimIDs {
		primaryToken := t.claimTokens[claimID]
		original, hasOriginal := t.claimOriginals[claimID]
		members, hasClaim := t.claimMembers[claimID]
		members = append([]string(nil), members...)
		t.releaseClaimLocked(claimID)
		if !hasClaim || primaryToken == 0 {
			continue
		}
		for _, memberID := range members {
			recovered, exists := t.jobByIDLocked(memberID)
			if !exists {
				continue
			}
			currentRevision := recovered.StateRevision
			if memberID == claimID {
				if hasOriginal {
					recovered = original
				} else {
					recovered.JobStatus = queueTypes.Waiting
					recovered.ForceRun = false
				}
			}
			recovered.ClaimToken = 0
			recovered.NotBeforeTime = emby.Time(now.Add(retryDelay))
			recovered.StateRevision = nextStateRevision(currentRevision)
			t.taskPriorityMapList[recovered.TaskPriority].Put(recovered.Id, recovered)
			t.upsertScheduledLocked(recovered)
			changed[recovered.TaskPriority] = struct{}{}
		}
	}
	t.signalWakeLocked()
	if len(changed) == 0 {
		return nil
	}
	var saveErr error
	for attempt := 0; attempt < outcomePersistenceAttempts; attempt++ {
		result := t.saveChangedPrioritiesWithResultLocked(changed, changed)
		if result.err == nil {
			return t.retryDirtyPrioritiesLocked()
		}
		saveErr = result.err
		if attempt+1 < outcomePersistenceAttempts {
			time.Sleep(time.Duration(1<<attempt) * 25 * time.Millisecond)
		}
	}
	// Keep the recovered in-memory state. The helper already marks every
	// unwritten bucket dirty, and the next maintenance/claim/explicit flush
	// will replace the durable Downloading snapshot.
	return saveErr
}

func sameUnclaimedLifecycleSnapshot(candidate, current queueTypes.OneJob) bool {
	return candidate.StateRevision == current.StateRevision &&
		candidate.JobStatus == current.JobStatus &&
		candidate.TaskPriority == current.TaskPriority &&
		candidate.RetryTimes == current.RetryTimes &&
		candidate.DownloadTimes == current.DownloadTimes &&
		candidate.ErrorInfo == current.ErrorInfo &&
		time.Time(candidate.UpdateTime).Equal(time.Time(current.UpdateTime)) &&
		time.Time(candidate.NextAttemptTime).Equal(time.Time(current.NextAttemptTime)) &&
		time.Time(candidate.NotBeforeTime).Equal(time.Time(current.NotBeforeTime)) &&
		candidate.ForceRun == current.ForceRun &&
		candidate.SearchEvidenceVersion == current.SearchEvidenceVersion &&
		candidate.SearchFingerprint == current.SearchFingerprint &&
		candidate.SeriesRootDirPath == current.SeriesRootDirPath &&
		candidate.MediaServerInsideVideoID == current.MediaServerInsideVideoID
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneStringSliceMap(source map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(source))
	for key, value := range source {
		cloned[key] = append([]string(nil), value...)
	}
	return cloned
}

func cloneUint64Map(source map[string]uint64) map[string]uint64 {
	cloned := make(map[string]uint64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneJobMap(source map[string]queueTypes.OneJob) map[string]queueTypes.OneJob {
	cloned := make(map[string]queueTypes.OneJob, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func clonePrioritySet(source map[int]struct{}) map[int]struct{} {
	cloned := make(map[int]struct{}, len(source))
	for priority := range source {
		cloned[priority] = struct{}{}
	}
	return cloned
}

func (t *TaskQueue) transitionOutcomeLocked(job queueTypes.OneJob, outcome error, now time.Time) queueTypes.OneJob {
	job = t.checkPriority(job)
	if outcome == nil {
		if job.TaskPriority == 0 {
			job.JobStatus = queueTypes.Ignore
		} else {
			job.JobStatus = queueTypes.Done
		}
		job.TaskPriority = DefaultTaskPriorityLevel
		job.DownloadTimes++
		job.RetryTimes = 0
		job.ErrorInfo = ""
		clearRetrySchedule(&job)
	} else {
		job.ErrorInfo = outcome.Error()
		job.DownloadTimes++
		job.RetryTimes++
		job.ForceRun = false
		if retryLifetimeExpired(job, now, settings.Get().AdvancedSettings.TaskQueue.ExpirationTime) {
			job.JobStatus = queueTypes.Failed
			clearRetrySchedule(&job)
		} else {
			if job.TaskPriority == DefaultTaskPriorityLevel && job.RetryTimes == 1 {
				job.TaskPriority = FirstRetryTaskPriorityLevel
			} else if job.RetryTimes >= settings.Get().AdvancedSettings.TaskQueue.MaxRetryTimes {
				job.RetryTimes = 0
				job = t.degrade(job)
			}
			job.JobStatus = queueTypes.Waiting
			scheduleRetryForError(&job, now, outcome)
		}
		if job.TaskPriority == 0 {
			job.JobStatus = queueTypes.Ignore
			clearRetrySchedule(&job)
		}
	}
	if job.TaskPriority < DefaultTaskPriorityLevel {
		job.TaskPriority = DefaultTaskPriorityLevel
	}
	return job
}

// removeJobWithoutSaveLocked removes all in-memory indexes and returns the
// affected priority. The caller batches persistence.
func (t *TaskQueue) removeJobWithoutSaveLocked(jobID string) (int, bool) {
	job, found := t.jobByIDLocked(jobID)
	if !found {
		return 0, false
	}
	priority := job.TaskPriority
	t.detachClaimMemberLocked(jobID)
	t.removeScheduledLocked(jobID)
	t.removeJobFromSeriesIndex(job.SeriesRootDirPath, jobID)
	t.taskKeyMap.Remove(jobID)
	t.taskPriorityMapList[priority].Remove(jobID)
	return priority, true
}
