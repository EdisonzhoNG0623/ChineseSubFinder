package task_queue

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	queueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func newIndexedQueueFromJobs(t testing.TB, queueName string, priority int, jobs map[string]queueTypes.OneJob) *TaskQueue {
	t.Helper()
	cache_center.DelDb(queueName)
	payload, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	center := cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester())
	if err = center.TaskQueueSave(priority, payload); err != nil {
		center.Close()
		t.Fatal(err)
	}
	queue := NewTaskQueue(center)
	t.Cleanup(func() {
		queue.Close()
		cache_center.DelDb(queueName)
	})
	return queue
}

func futureWaitingJobs(count int) map[string]queueTypes.OneJob {
	now := time.Now()
	jobs := make(map[string]queueTypes.OneJob, count)
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("future-%05d", index)
		jobs[id] = queueTypes.OneJob{
			Id: id, VideoType: common.Movie, VideoFPath: "/media/" + id + ".mkv",
			JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 1,
			ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(24 * time.Hour)),
		}
	}
	return jobs
}

func TestIdleSelectionInspectsHeapHeadsInsteadOfFullQueue(t *testing.T) {
	jobs := futureWaitingJobs(1000)
	now := time.Now()
	for index := 0; index < 1000; index++ {
		id := fmt.Sprintf("done-%05d", index)
		jobs[id] = queueTypes.OneJob{
			Id: id, VideoType: common.Movie, VideoFPath: "/media/" + id + ".mkv",
			JobStatus: queueTypes.Done, TaskPriority: FirstRetryTaskPriorityLevel,
			AddedTime: emby.Time(now), CreatedTime: emby.Time(now), UpdateTime: emby.Time(now),
		}
	}
	queue := newIndexedQueueFromJobs(t, "task_queue_idle_index_test", FirstRetryTaskPriorityLevel, jobs)
	before := queue.RuntimeStats()
	found, _, err := queue.GetOneJob()
	if err != nil || found {
		t.Fatalf("GetOneJob() = found=%v err=%v", found, err)
	}
	after := queue.RuntimeStats()
	if inspected := after.SelectionInspections - before.SelectionInspections; inspected != 2 {
		t.Fatalf("idle selection inspected %d candidates, want one waiting and one done heap head", inspected)
	}

	queue.BeforeGetOneJob()
	queue.BeforeGetOneJob()
	afterMaintenance := queue.RuntimeStats()
	if scans := afterMaintenance.MaintenanceScans - after.MaintenanceScans; scans != 1 {
		t.Fatalf("back-to-back maintenance performed %d full scans, want 1", scans)
	}
}

func TestBatchClaimAndOutcomesPersistEachPriorityOnce(t *testing.T) {
	const queueName = "task_queue_batch_persistence_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	candidates := make([]queueTypes.OneJob, 0, 12)
	for episode := 1; episode <= 12; episode++ {
		job := queueTypes.OneJob{
			Id: fmt.Sprintf("episode-%02d", episode), VideoType: common.Series,
			VideoFPath: fmt.Sprintf("/media/series/S01E%02d.mkv", episode), SeriesRootDirPath: "/media/series",
			Season: 1, Episode: episode, JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now.Add(-time.Duration(episode) * time.Minute)), UpdateTime: emby.Time(now),
		}
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
		candidates = append(candidates, job)
	}

	beforeClaim := queue.RuntimeStats()
	claimed, err := queue.ClaimBatch(candidates, now)
	if err != nil || len(claimed) != len(candidates) {
		t.Fatalf("ClaimBatch() = %d jobs, %v", len(claimed), err)
	}
	afterClaim := queue.RuntimeStats()
	if writes := afterClaim.PersistenceWrites - beforeClaim.PersistenceWrites; writes != 1 {
		t.Fatalf("claim persistence writes = %d, want 1", writes)
	}
	if _, scheduled := queue.NextWakeAt(); scheduled {
		t.Fatal("reserved members remained in the ready index")
	}

	outcomes := make([]JobOutcome, 0, len(claimed))
	for _, job := range claimed {
		outcomes = append(outcomes, JobOutcome{Job: job, Err: ErrNoSubFound})
	}
	beforeOutcomes := queue.RuntimeStats()
	if err = queue.ApplyOutcomes(outcomes); err != nil {
		t.Fatal(err)
	}
	afterOutcomes := queue.RuntimeStats()
	// All twelve jobs move from priority 5 to priority 6. Each source and
	// destination snapshot is persisted exactly once.
	if writes := afterOutcomes.PersistenceWrites - beforeOutcomes.PersistenceWrites; writes != 2 {
		t.Fatalf("batch outcome persistence writes = %d, want 2", writes)
	}
	for _, candidate := range candidates {
		_, job := queue.GetOneJobByID(candidate.Id)
		if job.JobStatus != queueTypes.Waiting || job.TaskPriority != FirstRetryTaskPriorityLevel || job.DownloadTimes != 1 {
			t.Fatalf("unexpected batch result for %s: %+v", candidate.Id, job)
		}
	}
}

func TestPriorityPersistenceOrderProtectsChainedMoves(t *testing.T) {
	changed := map[int]struct{}{5: {}, 6: {}, 7: {}}
	ordered, acyclic := priorityPersistenceOrder(changed, nil, []priorityMove{
		{jobID: "a", from: 5, to: 6},
		{jobID: "b", from: 6, to: 7},
	})
	if !acyclic || len(ordered) != 3 || ordered[0] != 7 || ordered[1] != 6 || ordered[2] != 5 {
		t.Fatalf("unsafe chained priority order: %v acyclic=%v", ordered, acyclic)
	}
	if _, acyclic = priorityPersistenceOrder(map[int]struct{}{5: {}, 6: {}}, nil, []priorityMove{
		{jobID: "a", from: 5, to: 6},
		{jobID: "b", from: 6, to: 5},
	}); acyclic {
		t.Fatal("cyclic priority moves were treated as directly writable")
	}
}

func TestCyclicPriorityMovesPersistAdditiveSafetySnapshots(t *testing.T) {
	const queueName = "task_queue_cyclic_priority_persistence_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	originalA := queueTypes.OneJob{Id: "cycle-a", VideoType: common.Movie, VideoFPath: "/a.mkv",
		JobStatus: queueTypes.Waiting, TaskPriority: 5, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	originalB := queueTypes.OneJob{Id: "cycle-b", VideoType: common.Movie, VideoFPath: "/b.mkv",
		JobStatus: queueTypes.Waiting, TaskPriority: 6, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(originalA); err != nil || !added {
		t.Fatalf("Add(a) = %v, %v", added, err)
	}
	if added, err := queue.Add(originalB); err != nil || !added {
		t.Fatalf("Add(b) = %v, %v", added, err)
	}

	queue.queueLock.Lock()
	finalA, finalB := originalA, originalB
	finalA.TaskPriority, finalB.TaskPriority = 6, 5
	finalA.UpdateTime, finalB.UpdateTime = emby.Time(now.Add(time.Minute)), emby.Time(now.Add(time.Minute))
	queue.taskPriorityMapList[5].Remove(originalA.Id)
	queue.taskPriorityMapList[6].Remove(originalB.Id)
	queue.taskPriorityMapList[6].Put(finalA.Id, finalA)
	queue.taskPriorityMapList[5].Put(finalB.Id, finalB)
	queue.taskKeyMap.Put(finalA.Id, 6)
	queue.taskKeyMap.Put(finalB.Id, 5)

	disk := map[int]map[string]queueTypes.OneJob{
		5: {originalA.Id: originalA},
		6: {originalB.Id: originalB},
	}
	writes := 0
	queue.persistPriority = func(priority int, payload []byte) error {
		writes++
		if writes == 3 {
			return errors.New("simulated crash before final snapshots")
		}
		var jobs map[string]queueTypes.OneJob
		if err := json.Unmarshal(payload, &jobs); err != nil {
			return err
		}
		disk[priority] = jobs
		return nil
	}
	result := queue.saveChangedPrioritiesWithResultLocked(
		map[int]struct{}{5: {}, 6: {}}, map[int]struct{}{5: {}, 6: {}},
		priorityMove{jobID: originalA.Id, from: 5, to: 6, original: originalA},
		priorityMove{jobID: originalB.Id, from: 6, to: 5, original: originalB},
	)
	queue.queueLock.Unlock()
	if result.err == nil || writes != 3 {
		t.Fatalf("cyclic persistence result = %+v writes=%d", result, writes)
	}
	for _, jobID := range []string{originalA.Id, originalB.Id} {
		found := false
		for _, bucket := range disk {
			if _, exists := bucket[jobID]; exists {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("job %s disappeared across partial cyclic commit: %+v", jobID, disk)
		}
	}
}

func TestSameSecondPartialMoveRestartKeepsHigherStateRevision(t *testing.T) {
	const queueName = "task_queue_same_second_partial_restart_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	sameSecond := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "same-second-move", VideoType: common.Movie, VideoFPath: "/same-second.mkv",
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(sameSecond), UpdateTime: emby.Time(sameSecond),
	}
	if added, err := queue.Add(job); err != nil || !added {
		queue.Close()
		t.Fatalf("Add() = %v, %v", added, err)
	}
	_, original := queue.GetOneJobByID(job.Id)

	final := original
	final.TaskPriority = FirstRetryTaskPriorityLevel
	final.DownloadTimes = 1
	final.RetryTimes = 1
	final.ErrorInfo = "temporary network timeout"
	final.NextAttemptTime = emby.Time(sameSecond.Add(time.Hour))
	final.UpdateTime = emby.Time(sameSecond)
	final.StateRevision = nextStateRevision(original.StateRevision)

	queue.queueLock.Lock()
	queue.removeScheduledLocked(job.Id)
	queue.taskPriorityMapList[DefaultTaskPriorityLevel].Remove(job.Id)
	queue.taskPriorityMapList[FirstRetryTaskPriorityLevel].Put(job.Id, final)
	queue.taskKeyMap.Put(job.Id, FirstRetryTaskPriorityLevel)
	queue.upsertScheduledLocked(final)
	originalPersist := queue.persistPriority
	queue.persistPriority = func(priority int, payload []byte) error {
		if priority == DefaultTaskPriorityLevel {
			return errors.New("simulated crash before source snapshot")
		}
		return originalPersist(priority, payload)
	}
	result := queue.saveChangedPrioritiesWithResultLocked(
		map[int]struct{}{DefaultTaskPriorityLevel: {}, FirstRetryTaskPriorityLevel: {}},
		map[int]struct{}{FirstRetryTaskPriorityLevel: {}},
		priorityMove{jobID: job.Id, from: DefaultTaskPriorityLevel, to: FirstRetryTaskPriorityLevel, original: original},
	)
	queue.persistPriority = originalPersist
	queue.queueLock.Unlock()
	if result.err == nil || len(result.saved) != 1 {
		queue.Close()
		t.Fatalf("partial move result = %+v", result)
	}
	// Simulate a process crash: do not flush the dirty source bucket.
	queue.Close()

	restarted := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	defer restarted.Close()
	found, got := restarted.GetOneJobByID(job.Id)
	if !found {
		t.Fatal("job disappeared after partial move restart")
	}
	if got.TaskPriority != final.TaskPriority || got.StateRevision != final.StateRevision ||
		got.DownloadTimes != final.DownloadTimes || got.ErrorInfo != final.ErrorInfo {
		t.Fatalf("restart selected stale same-second source copy: got=%+v final=%+v", got, final)
	}

	persisted, err := restarted.center.TaskQueueRead()
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]queueTypes.OneJob
	if err = json.Unmarshal(persisted[DefaultTaskPriorityLevel], &source); err != nil {
		t.Fatal(err)
	}
	if _, exists := source[job.Id]; exists {
		t.Fatal("startup dedup did not remove stale same-second source copy")
	}
}

func TestClaimPersistenceFailureDoesNotEmitImmediateWake(t *testing.T) {
	const queueName = "task_queue_claim_failure_backoff_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	job := queueTypes.OneJob{Id: "claim-failure", VideoType: common.Movie, VideoFPath: "/failure.mkv",
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	for {
		select {
		case <-queue.WakeChan():
			continue
		default:
			goto drained
		}
	}
drained:
	queue.persistPriority = func(int, []byte) error { return errors.New("disk unavailable") }
	if _, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now); err == nil {
		t.Fatal("ClaimBatch unexpectedly survived persistence failure")
	}
	select {
	case <-queue.WakeChan():
		t.Fatal("claim persistence failure emitted a hot-loop wake edge")
	default:
	}
}

func TestApplyOutcomesSkipsMissingMemberAndAppliesValidOutcome(t *testing.T) {
	const queueName = "task_queue_batch_validation_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	job := queueTypes.OneJob{Id: "primary", VideoFPath: "/media/primary.mkv", VideoType: common.Movie,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	queue.Add(job)
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil {
		t.Fatal(err)
	}
	err = queue.ApplyOutcomes([]JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}, {Job: queueTypes.OneJob{Id: "missing"}, Err: errors.New("bad")}})
	if err != nil {
		t.Fatalf("valid outcome was blocked by missing member: %v", err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.JobStatus != queueTypes.Waiting || current.TaskPriority != FirstRetryTaskPriorityLevel || current.DownloadTimes != 1 {
		t.Fatalf("valid member was not applied: %+v", current)
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("claim maps retained members: jobs=%d claims=%d tokens=%d", len(queue.claimedJobs), len(queue.claimMembers), len(queue.claimTokens))
	}
}

func TestApplyOutcomesFirstWriteFailureRestoresJobsAndClaimForRetry(t *testing.T) {
	const queueName = "task_queue_outcome_first_write_rollback_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	jobs := make([]queueTypes.OneJob, 0, 2)
	for episode := 1; episode <= 2; episode++ {
		job := queueTypes.OneJob{Id: fmt.Sprintf("rollback-%d", episode), VideoType: common.Series,
			VideoFPath: fmt.Sprintf("/series/S01E%02d.mkv", episode), SeriesRootDirPath: "/series",
			Season: 1, Episode: episode, JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
		jobs = append(jobs, job)
	}
	claimed, err := queue.ClaimBatch(jobs, now)
	if err != nil || len(claimed) != len(jobs) {
		t.Fatalf("ClaimBatch() = %d jobs, %v", len(claimed), err)
	}
	outcomes := []JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}, {Job: claimed[1], Err: ErrNoSubFound}}
	originalPersist := queue.persistPriority
	queue.persistPriority = func(int, []byte) error { return errors.New("injected first write failure") }
	if err = queue.ApplyOutcomes(outcomes); err == nil {
		t.Fatal("ApplyOutcomes unexpectedly survived first write failure")
	}

	for index, job := range jobs {
		_, current := queue.GetOneJobByID(job.Id)
		wantStatus := queueTypes.Waiting
		if index == 0 {
			wantStatus = queueTypes.Downloading
		}
		if current.JobStatus != wantStatus || current.TaskPriority != DefaultTaskPriorityLevel || current.DownloadTimes != 0 {
			t.Fatalf("failed outcome did not restore %s: %+v", job.Id, current)
		}
	}
	if len(queue.claimedJobs) != len(jobs) || len(queue.claimMembers) != 1 || len(queue.claimTokens) != len(jobs) {
		t.Fatalf("claim was not restored: jobs=%d claims=%d tokens=%d", len(queue.claimedJobs), len(queue.claimMembers), len(queue.claimTokens))
	}
	if len(queue.dirtyPriorities) != 0 {
		t.Fatalf("rolled-back first write left dirty priorities: %v", queue.dirtyPriorities)
	}

	queue.persistPriority = originalPersist
	if err = queue.ApplyOutcomes(outcomes); err != nil {
		t.Fatalf("retry ApplyOutcomes() = %v", err)
	}
	for _, job := range jobs {
		_, current := queue.GetOneJobByID(job.Id)
		if current.JobStatus != queueTypes.Waiting || current.TaskPriority != FirstRetryTaskPriorityLevel || current.DownloadTimes != 1 {
			t.Fatalf("retry did not apply %s: %+v", job.Id, current)
		}
	}
}

func TestApplyOutcomesReliableRetriesTransientFirstWriteFailure(t *testing.T) {
	const queueName = "task_queue_outcome_reliable_retry_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	job := queueTypes.OneJob{Id: "reliable-retry", VideoType: common.Movie, VideoFPath: "/media/reliable-retry.mkv",
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}

	originalPersist := queue.persistPriority
	failures := 0
	queue.persistPriority = func(priority int, payload []byte) error {
		if failures < 2 {
			failures++
			return errors.New("injected transient outcome write failure")
		}
		return originalPersist(priority, payload)
	}
	if err = queue.ApplyOutcomesReliable([]JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}}); err != nil {
		t.Fatalf("ApplyOutcomesReliable() = %v", err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	if failures != 2 || current.JobStatus != queueTypes.Waiting ||
		current.TaskPriority != FirstRetryTaskPriorityLevel || current.DownloadTimes != 1 {
		t.Fatalf("transient outcome was not retried: failures=%d job=%+v", failures, current)
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("successful reliable outcome retained claim: jobs=%d claims=%d tokens=%d",
			len(queue.claimedJobs), len(queue.claimMembers), len(queue.claimTokens))
	}
}

func TestApplyOutcomesReliableReleasesClaimAfterPersistentFailure(t *testing.T) {
	const queueName = "task_queue_outcome_reliable_release_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	job := queueTypes.OneJob{Id: "reliable-release", VideoType: common.Movie, VideoFPath: "/media/reliable-release.mkv",
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}

	originalPersist := queue.persistPriority
	queue.persistPriority = func(int, []byte) error { return errors.New("injected persistent outcome write failure") }
	if err = queue.ApplyOutcomesReliable([]JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}}); err == nil {
		t.Fatal("ApplyOutcomesReliable unexpectedly survived persistent failure")
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.JobStatus != queueTypes.Waiting || current.TaskPriority != DefaultTaskPriorityLevel ||
		!time.Time(current.NotBeforeTime).After(now) {
		t.Fatalf("failed outcome claim was not safely requeued: %+v", current)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.Before(now.Add(30*time.Second)) {
		t.Fatalf("persistent write recovery can hot-loop: next=%s ok=%v", next, ok)
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("persistent failure stranded claim: jobs=%d claims=%d tokens=%d",
			len(queue.claimedJobs), len(queue.claimMembers), len(queue.claimTokens))
	}
	if _, dirty := queue.dirtyPriorities[DefaultTaskPriorityLevel]; !dirty {
		t.Fatalf("failed recovery snapshot was not tracked dirty: %v", queue.dirtyPriorities)
	}

	queue.persistPriority = originalPersist
	if err = queue.FlushDirtyPriorities(); err != nil {
		t.Fatalf("FlushDirtyPriorities() = %v", err)
	}
	snapshots, err := queue.center.TaskQueueRead()
	if err != nil {
		t.Fatal(err)
	}
	var durable map[string]queueTypes.OneJob
	if err = json.Unmarshal(snapshots[DefaultTaskPriorityLevel], &durable); err != nil {
		t.Fatal(err)
	}
	if durable[job.Id].JobStatus != queueTypes.Waiting {
		t.Fatalf("durable recovery status = %d", durable[job.Id].JobStatus)
	}
}

func TestReleaseClaimsForRetryDoesNotCountAdministrativeShutdown(t *testing.T) {
	const queueName = "task_queue_administrative_release_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	job := queueTypes.OneJob{Id: "administrative-release", VideoType: common.Movie,
		VideoFPath: "/media/administrative-release.mkv", JobStatus: queueTypes.Waiting,
		TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}
	if err = queue.ReleaseClaimsForRetry(claimed, 15*time.Second); err != nil {
		t.Fatalf("ReleaseClaimsForRetry() = %v", err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.JobStatus != queueTypes.Waiting || current.TaskPriority != job.TaskPriority ||
		current.DownloadTimes != 0 || current.RetryTimes != 0 || current.ErrorInfo != "" {
		t.Fatalf("administrative release changed retry lifecycle: %+v", current)
	}
	retryAt := time.Time(current.NotBeforeTime)
	if retryAt.Before(now.Add(10*time.Second)) || retryAt.After(time.Now().Add(20*time.Second)) {
		t.Fatalf("administrative retry time = %s", retryAt)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.Before(now.Add(10*time.Second)) {
		t.Fatalf("administrative release remained immediately ready: next=%s ok=%v", next, ok)
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("administrative release retained claim: jobs=%d claims=%d tokens=%d",
			len(queue.claimedJobs), len(queue.claimMembers), len(queue.claimTokens))
	}
}

func TestAdministrativeReleaseRestoresPreClaimSearchEvidence(t *testing.T) {
	const queueName = "task_queue_administrative_evidence_restore_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{Id: "evidence-release", VideoType: common.Anime,
		VideoFPath: "/show/S01E01.mkv", SeriesRootDirPath: "/show", SeriesName: "Example",
		Season: 1, Episode: 1, SearchAliases: []string{"new alias"}, SearchFingerprint: "new-evidence",
		LastAttemptSearchFingerprint: "old-evidence", LastAttemptPolicyFingerprint: settings.CurrentSearchPolicyFingerprint(),
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now.Add(-24 * time.Hour)), UpdateTime: emby.Time(now.Add(-12 * time.Hour)),
		DownloadTimes: 2, RetryTimes: 2, ErrorInfo: ErrNoSubFound.Error(),
		NextAttemptTime: emby.Time(now.Add(48 * time.Hour)), SearchEvidenceVersion: queueTypes.SearchEvidenceVersion}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}
	if claimed[0].LastAttemptSearchFingerprint != job.LastAttemptSearchFingerprint {
		t.Fatalf("claim prematurely stamped evidence: %+v", claimed[0])
	}
	if err = queue.ReleaseClaimsForRetry(claimed, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	_, recovered := queue.GetOneJobByID(job.Id)
	if recovered.LastAttemptSearchFingerprint != job.LastAttemptSearchFingerprint ||
		recovered.LastAttemptPolicyFingerprint != job.LastAttemptPolicyFingerprint ||
		!time.Time(recovered.UpdateTime).Equal(time.Time(job.UpdateTime)) || recovered.DownloadTimes != job.DownloadTimes {
		t.Fatalf("administrative release did not restore pre-claim state: %+v", recovered)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.Before(now.Add(10*time.Second)) || next.After(now.Add(20*time.Second)) {
		t.Fatalf("restored evidence ignored administrative not-before: next=%s ok=%v", next, ok)
	}
}

func TestSeriesClaimReleaseDelaysEveryReservedMember(t *testing.T) {
	const queueName = "task_queue_series_release_delay_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now().Truncate(time.Second)
	jobs := make([]queueTypes.OneJob, 0, 3)
	for episode := 1; episode <= 3; episode++ {
		job := queueTypes.OneJob{Id: fmt.Sprintf("release-series-%d", episode), VideoType: common.Series,
			VideoFPath: fmt.Sprintf("/show/S01E%02d.mkv", episode), SeriesRootDirPath: "/show",
			Season: 1, Episode: episode, JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
		if episode == 3 {
			job.ForceRun = true
		}
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%d) = %v, %v", episode, added, err)
		}
		jobs = append(jobs, job)
	}
	claimed, err := queue.ClaimBatch(jobs[:2], now)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}
	if err = queue.ReleaseClaimsForRetry(claimed, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, original := range jobs {
		_, recovered := queue.GetOneJobByID(original.Id)
		notBefore := time.Time(recovered.NotBeforeTime)
		if notBefore.Before(now.Add(50*time.Second)) || notBefore.After(time.Now().Add(70*time.Second)) {
			t.Fatalf("member %s bypassed claim-wide delay: %+v", original.Id, recovered)
		}
	}
	if next, ok := queue.NextWakeAt(); !ok || next.Before(now.Add(50*time.Second)) {
		t.Fatalf("reserved series member can hot-loop: next=%s ok=%v", next, ok)
	}
}

func TestBatchTransitionLogDoesNotExposeRawOutcomeError(t *testing.T) {
	const queueName = "task_queue_safe_transition_log_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	job := queueTypes.OneJob{Id: "safe-log", VideoType: common.Movie, VideoFPath: "/media/private-title.mkv",
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	queue.log.SetOutput(&output)
	const secret = "https://user:private-token@example.com/search?api_key=hidden"
	if err = queue.ApplyOutcomes([]JobOutcome{{Job: claimed[0], Err: errors.New(secret)}}); err != nil {
		t.Fatal(err)
	}
	logged := output.String()
	if strings.Contains(logged, secret) || strings.Contains(logged, "private-token") || strings.Contains(logged, "api_key") {
		t.Fatalf("transition log exposed raw outcome: %s", logged)
	}
	if !strings.Contains(logged, "error_category=UNKNOWN") {
		t.Fatalf("transition log omitted bounded category: %s", logged)
	}
}

func TestApplyOutcomesSourceWriteFailureKeepsCommittedStateUntilDirtyRetry(t *testing.T) {
	const queueName = "task_queue_outcome_partial_commit_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	jobs := make([]queueTypes.OneJob, 0, 2)
	for episode := 1; episode <= 2; episode++ {
		job := queueTypes.OneJob{Id: fmt.Sprintf("partial-%d", episode), VideoType: common.Series,
			VideoFPath: fmt.Sprintf("/series/S01E%02d.mkv", episode), SeriesRootDirPath: "/series",
			Season: 1, Episode: episode, JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
		jobs = append(jobs, job)
	}
	claimed, err := queue.ClaimBatch(jobs, now)
	if err != nil || len(claimed) != len(jobs) {
		t.Fatalf("ClaimBatch() = %d jobs, %v", len(claimed), err)
	}
	originalPersist := queue.persistPriority
	writes := 0
	queue.persistPriority = func(priority int, payload []byte) error {
		writes++
		if writes == 2 {
			return errors.New("injected source write failure")
		}
		return originalPersist(priority, payload)
	}
	outcomes := []JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}, {Job: claimed[1], Err: ErrNoSubFound}}
	if err = queue.ApplyOutcomes(outcomes); !errors.Is(err, ErrPartialCommit) {
		t.Fatalf("ApplyOutcomes() = %v, want ErrPartialCommit", err)
	}
	for _, job := range jobs {
		_, current := queue.GetOneJobByID(job.Id)
		if current.JobStatus != queueTypes.Waiting || current.TaskPriority != FirstRetryTaskPriorityLevel || current.DownloadTimes != 1 {
			t.Fatalf("partial commit rolled back durable destination for %s: %+v", job.Id, current)
		}
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("partial commit retained claim: jobs=%d claims=%d tokens=%d", len(queue.claimedJobs), len(queue.claimMembers), len(queue.claimTokens))
	}
	if _, dirty := queue.dirtyPriorities[DefaultTaskPriorityLevel]; !dirty || len(queue.dirtyPriorities) != 1 {
		t.Fatalf("source priority was not tracked dirty: %v", queue.dirtyPriorities)
	}

	queue.persistPriority = originalPersist
	if err = queue.FlushDirtyPriorities(); err != nil {
		t.Fatalf("FlushDirtyPriorities() = %v", err)
	}
	if len(queue.dirtyPriorities) != 0 {
		t.Fatalf("dirty priorities survived retry: %v", queue.dirtyPriorities)
	}
	snapshots, err := queue.center.TaskQueueRead()
	if err != nil {
		t.Fatal(err)
	}
	var sourceJobs, destinationJobs map[string]queueTypes.OneJob
	if err = json.Unmarshal(snapshots[DefaultTaskPriorityLevel], &sourceJobs); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(snapshots[FirstRetryTaskPriorityLevel], &destinationJobs); err != nil {
		t.Fatal(err)
	}
	if len(sourceJobs) != 0 || len(destinationJobs) != len(jobs) {
		t.Fatalf("unexpected snapshots after dirty retry: source=%d destination=%d", len(sourceJobs), len(destinationJobs))
	}
}

func TestClaimPersistenceFailureRollsBackReservation(t *testing.T) {
	const queueName = "task_queue_claim_rollback_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	job := queueTypes.OneJob{Id: "rollback", VideoFPath: "/media/rollback.mkv", VideoType: common.Movie,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	queue.Add(job)
	queue.persistPriority = func(int, []byte) error { return errors.New("injected persistence failure") }

	if _, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now); err == nil {
		t.Fatal("ClaimBatch unexpectedly survived persistence failure")
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.JobStatus != queueTypes.Waiting {
		t.Fatalf("failed claim left primary mutated: %+v", current)
	}
	found, selected, err := queue.GetOneWaitingJob()
	if err != nil || !found || selected.Id != job.Id {
		t.Fatalf("failed claim did not restore ready index: found=%v job=%+v err=%v", found, selected, err)
	}
}

func TestClaimRemainsSuccessfulWhenUnrelatedDirtyRetryFails(t *testing.T) {
	const queueName = "task_queue_claim_unrelated_dirty_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	job := queueTypes.OneJob{Id: "claim-with-dirty", VideoFPath: "/media/claim-with-dirty.mkv", VideoType: common.Movie,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}

	originalPersist := queue.persistPriority
	queue.dirtyPriorities[FirstRetryTaskPriorityLevel] = struct{}{}
	queue.persistPriority = func(priority int, payload []byte) error {
		if priority == FirstRetryTaskPriorityLevel {
			return errors.New("injected unrelated dirty retry failure")
		}
		return originalPersist(priority, payload)
	}
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.JobStatus != queueTypes.Downloading || len(queue.claimedJobs) != 1 {
		t.Fatalf("durable claim was rolled back by unrelated dirty failure: %+v claims=%v", current, queue.claimedJobs)
	}
	if _, dirty := queue.dirtyPriorities[FirstRetryTaskPriorityLevel]; !dirty {
		t.Fatal("unrelated dirty snapshot was lost")
	}
}

func TestNewJobPersistenceFailureRollsBackAndCanRetry(t *testing.T) {
	const queueName = "task_queue_add_rollback_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	job := queueTypes.OneJob{Id: "new-rollback", VideoFPath: "/media/new.mkv", VideoType: common.Movie,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	originalPersist := queue.persistPriority
	queue.persistPriority = func(int, []byte) error { return errors.New("injected persistence failure") }
	if added, err := queue.Add(job); err == nil || added {
		t.Fatalf("failed Add() = %v, %v", added, err)
	}
	if queue.Size() != 0 {
		t.Fatal("failed Add left an unpersisted job in memory")
	}
	queue.persistPriority = originalPersist
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("retry Add() = %v, %v", added, err)
	}
}

func TestUpdateSourceWriteFailureTracksDirtySnapshot(t *testing.T) {
	const queueName = "task_queue_update_source_dirty_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	job := queueTypes.OneJob{Id: "update-source-dirty", VideoFPath: "/media/update.mkv", VideoType: common.Movie,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	originalPersist := queue.persistPriority
	writes := 0
	queue.persistPriority = func(priority int, payload []byte) error {
		writes++
		if writes == 2 {
			return errors.New("injected update source write failure")
		}
		return originalPersist(priority, payload)
	}
	job.TaskPriority = FirstRetryTaskPriorityLevel
	updated, err := queue.Update(job)
	if err == nil || !updated {
		t.Fatalf("Update() = %v, %v", updated, err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.TaskPriority != FirstRetryTaskPriorityLevel {
		t.Fatalf("durable destination was rolled back: %+v", current)
	}
	if _, dirty := queue.dirtyPriorities[DefaultTaskPriorityLevel]; !dirty || len(queue.dirtyPriorities) != 1 {
		t.Fatalf("failed source snapshot was not tracked dirty: %v", queue.dirtyPriorities)
	}
	queue.persistPriority = originalPersist
	if err = queue.FlushDirtyPriorities(); err != nil {
		t.Fatalf("FlushDirtyPriorities() = %v", err)
	}
	snapshots, err := queue.center.TaskQueueRead()
	if err != nil {
		t.Fatal(err)
	}
	var sourceJobs map[string]queueTypes.OneJob
	if err = json.Unmarshal(snapshots[DefaultTaskPriorityLevel], &sourceJobs); err != nil {
		t.Fatal(err)
	}
	if _, exists := sourceJobs[job.Id]; exists {
		t.Fatal("dirty retry left stale source copy")
	}
}

func TestDeleteWriteFailureRestoresJobAndCanRetry(t *testing.T) {
	const queueName = "task_queue_delete_rollback_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	job := queueTypes.OneJob{Id: "delete-rollback", VideoFPath: "/media/delete.mkv", VideoType: common.Series,
		SeriesRootDirPath: "/media/series", Season: 1, Episode: 1,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	if claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now); err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}
	originalPersist := queue.persistPriority
	queue.persistPriority = func(int, []byte) error { return errors.New("injected delete write failure") }
	if deleted, err := queue.Del(job.Id); err == nil || deleted {
		t.Fatalf("failed Del() = %v, %v", deleted, err)
	}
	if exists, current := queue.GetOneJobByID(job.Id); !exists || current.Id != job.Id || current.JobStatus != queueTypes.Downloading {
		t.Fatalf("failed delete did not restore job: exists=%v job=%+v", exists, current)
	}
	if len(queue.claimedJobs) != 1 || len(queue.claimMembers) != 1 || len(queue.claimTokens) != 1 {
		t.Fatalf("failed delete did not restore claim: jobs=%v members=%v tokens=%v", queue.claimedJobs, queue.claimMembers, queue.claimTokens)
	}
	if len(queue.dirtyPriorities) != 0 {
		t.Fatalf("rolled-back delete left dirty snapshots: %v", queue.dirtyPriorities)
	}
	seriesJobs := queue.GetSeriesJobs(job.SeriesRootDirPath)
	if len(seriesJobs) != 1 || seriesJobs[0].Id != job.Id {
		t.Fatalf("failed delete did not restore series index: %+v", seriesJobs)
	}
	queue.persistPriority = originalPersist
	if deleted, err := queue.Del(job.Id); err != nil || !deleted {
		t.Fatalf("retry Del() = %v, %v", deleted, err)
	}
	if exists, _ := queue.GetOneJobByID(job.Id); exists {
		t.Fatal("retry delete left job in memory")
	}
}

func TestPrimaryStatusChangeReleasesAllSeriesReservations(t *testing.T) {
	const queueName = "task_queue_claim_release_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	jobs := make([]queueTypes.OneJob, 0, 6)
	for episode := 1; episode <= 6; episode++ {
		job := queueTypes.OneJob{Id: fmt.Sprintf("release-%d", episode), VideoType: common.Series,
			VideoFPath: fmt.Sprintf("/series/S01E%02d.mkv", episode), SeriesRootDirPath: "/series",
			Season: 1, Episode: episode, JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
		queue.Add(job)
		jobs = append(jobs, job)
	}
	claimed, err := queue.ClaimBatch(jobs[:4], now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ready := queue.NextWakeAt(); ready {
		t.Fatal("unprocessed same-series members were not reserved")
	}

	primary := claimed[0]
	primary.JobStatus = queueTypes.Ignore
	if updated, updateErr := queue.Update(primary); updateErr != nil || !updated {
		t.Fatalf("Update(primary) = %v, %v", updated, updateErr)
	}
	next, ready := queue.NextWakeAt()
	if !ready || next.After(time.Now()) {
		t.Fatalf("primary status change did not release companions: %v, %v", next, ready)
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 {
		t.Fatalf("claim maps retained members: jobs=%d claims=%d", len(queue.claimedJobs), len(queue.claimMembers))
	}
}

func TestLateOutcomeCannotOverwriteManualStatusChange(t *testing.T) {
	const queueName = "task_queue_stale_outcome_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	job := queueTypes.OneJob{Id: "manual-ignore", VideoFPath: "/media/movie.mkv", VideoType: common.Movie,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	queue.Add(job)
	claimed, err := queue.ClaimBatch([]queueTypes.OneJob{job}, now)
	if err != nil || claimed[0].ClaimToken == 0 {
		t.Fatalf("ClaimBatch() = %+v, %v", claimed, err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	current.JobStatus = queueTypes.Ignore
	if updated, updateErr := queue.Update(current); updateErr != nil || !updated {
		t.Fatalf("manual Update() = %v, %v", updated, updateErr)
	}
	if err = queue.ApplyOutcomes([]JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}}); !errors.Is(err, ErrClaimUnavailable) {
		t.Fatalf("late outcome error = %v, want ErrClaimUnavailable", err)
	}
	_, current = queue.GetOneJobByID(job.Id)
	if current.JobStatus != queueTypes.Ignore || current.DownloadTimes != 0 {
		t.Fatalf("late outcome overwrote manual action: %+v", current)
	}
}

func TestTokenZeroOutcomeCannotOverwriteConcurrentManualLifecycleChange(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*queueTypes.OneJob)
		check  func(testing.TB, queueTypes.OneJob)
	}{
		{
			name: "ignore",
			mutate: func(job *queueTypes.OneJob) {
				job.JobStatus = queueTypes.Ignore
				job.ForceRun = false
			},
			check: func(t testing.TB, job queueTypes.OneJob) {
				t.Helper()
				if job.JobStatus != queueTypes.Ignore || job.DownloadTimes != 0 || job.RetryTimes != 0 || job.ErrorInfo != "" {
					t.Fatalf("stale token-zero error overwrote manual ignore: %+v", job)
				}
			},
		},
		{
			name: "force_reset",
			mutate: func(job *queueTypes.OneJob) {
				job.JobStatus = queueTypes.Waiting
				job.ForceRun = true
				job.NotBeforeTime = emby.Time{}
			},
			check: func(t testing.TB, job queueTypes.OneJob) {
				t.Helper()
				if job.JobStatus != queueTypes.Waiting || !job.ForceRun || job.DownloadTimes != 0 || job.RetryTimes != 0 || job.ErrorInfo != "" {
					t.Fatalf("stale token-zero error overwrote manual reset: %+v", job)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queueName := "task_queue_token_zero_cas_" + test.name
			cache_center.DelDb(queueName)
			t.Cleanup(func() { cache_center.DelDb(queueName) })
			queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
			t.Cleanup(queue.Close)

			now := time.Now()
			job := queueTypes.OneJob{Id: "token-zero-" + test.name, VideoType: common.Movie,
				VideoFPath: "/media/token-zero-" + test.name + ".mkv", JobStatus: queueTypes.Waiting,
				TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
			if added, err := queue.Add(job); err != nil || !added {
				t.Fatalf("Add() = %v, %v", added, err)
			}
			_, stale := queue.GetOneJobByID(job.Id)
			manual := stale
			test.mutate(&manual)
			if updated, err := queue.Update(manual); err != nil || !updated {
				t.Fatalf("manual Update() = %v, %v", updated, err)
			}

			err := queue.ApplyOutcomes([]JobOutcome{{Job: stale, Err: errors.New("stale pre-claim validation error")}})
			if !errors.Is(err, ErrClaimUnavailable) {
				t.Fatalf("stale token-zero ApplyOutcomes() = %v, want ErrClaimUnavailable", err)
			}
			_, current := queue.GetOneJobByID(job.Id)
			test.check(t, current)
		})
	}
}

func TestDeletedCompanionDoesNotBlockValidBatchOutcomes(t *testing.T) {
	const queueName = "task_queue_deleted_companion_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	jobs := make([]queueTypes.OneJob, 0, 3)
	for episode := 1; episode <= 3; episode++ {
		job := queueTypes.OneJob{Id: fmt.Sprintf("deleted-%d", episode), VideoType: common.Series,
			VideoFPath: fmt.Sprintf("/series/S01E%02d.mkv", episode), SeriesRootDirPath: "/series",
			Season: 1, Episode: episode, JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel,
			AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
		queue.Add(job)
		jobs = append(jobs, job)
	}
	claimed, err := queue.ClaimBatch(jobs, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, deleteErr := queue.Del(jobs[1].Id); deleteErr != nil || !deleted {
		t.Fatalf("Del(companion) = %v, %v", deleted, deleteErr)
	}
	outcomes := []JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}, {Job: claimed[1], Err: ErrNoSubFound}, {Job: claimed[2], Err: ErrNoSubFound}}
	if err = queue.ApplyOutcomes(outcomes); err != nil {
		t.Fatalf("valid batch members were blocked by deleted companion: %v", err)
	}
	for _, index := range []int{0, 2} {
		_, current := queue.GetOneJobByID(jobs[index].Id)
		if current.DownloadTimes != 1 || current.JobStatus != queueTypes.Waiting {
			t.Fatalf("valid member %s not applied: %+v", jobs[index].Id, current)
		}
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("claim maps retained members: jobs=%d claims=%d tokens=%d", len(queue.claimedJobs), len(queue.claimMembers), len(queue.claimTokens))
	}
}

func TestQueueMutationsWakeAndReindexImmediately(t *testing.T) {
	const queueName = "task_queue_wake_reindex_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	job := queueTypes.OneJob{Id: "wake-job", VideoFPath: "/media/wake.mkv", VideoType: common.Movie,
		JobStatus: queueTypes.Waiting, TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now),
		DownloadTimes: 1, ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(time.Hour))}
	queue.Add(job)
	select {
	case <-queue.WakeChan():
	default:
		t.Fatal("Add did not wake dispatcher")
	}

	job.ForceRun = true
	if updated, err := queue.Update(job); err != nil || !updated {
		t.Fatalf("Update(force) = %v, %v", updated, err)
	}
	select {
	case <-queue.WakeChan():
	default:
		t.Fatal("force retry did not wake dispatcher")
	}
	if next, ok := queue.NextWakeAt(); !ok || next.After(time.Now()) {
		t.Fatalf("forced task was not reindexed ready: %v, %v", next, ok)
	}

	queue.NotifySettingsChanged()
	select {
	case <-queue.WakeChan():
	default:
		t.Fatal("settings reindex did not wake dispatcher")
	}
}

func TestLowFrequencyMaintenanceRemovesMissingMedia(t *testing.T) {
	const queueName = "task_queue_missing_media_maintenance_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	path := filepath.Join(t.TempDir(), "episode.mkv")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	job := queueTypes.OneJob{Id: "missing-media", VideoFPath: path, VideoType: common.Series,
		SeriesRootDirPath: filepath.Dir(path), Season: 1, Episode: 1, JobStatus: queueTypes.Waiting,
		TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)}
	queue.Add(job)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	originalPersist := queue.persistPriority
	queue.persistPriority = func(int, []byte) error { return errors.New("injected maintenance write failure") }
	queue.RunMaintenance()
	if exists, _ := queue.GetOneJobByID(job.Id); exists {
		t.Fatal("missing media job survived maintenance")
	}
	if _, dirty := queue.dirtyPriorities[DefaultTaskPriorityLevel]; !dirty {
		t.Fatalf("failed maintenance persistence was not tracked dirty: %v", queue.dirtyPriorities)
	}
	queue.persistPriority = originalPersist
	if err := queue.FlushDirtyPriorities(); err != nil {
		t.Fatalf("FlushDirtyPriorities() = %v", err)
	}
	if len(queue.dirtyPriorities) != 0 {
		t.Fatalf("maintenance dirty snapshot survived retry: %v", queue.dirtyPriorities)
	}
}

func BenchmarkIdleSelectionIndexed(b *testing.B) {
	queue := newIndexedQueueFromJobs(b, "task_queue_idle_index_benchmark", FirstRetryTaskPriorityLevel, futureWaitingJobs(10000))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		found, _, err := queue.GetOneWaitingJob()
		if err != nil || found {
			b.Fatalf("selection = %v, %v", found, err)
		}
	}
}
