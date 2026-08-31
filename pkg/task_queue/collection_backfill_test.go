package task_queue

import (
	"errors"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestMarkSeriesEpisodesDoneBatchesOnlySavedEpisodes(t *testing.T) {
	const queueName = "task_queue_collection_backfill_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	seriesRoot := "/media/Bleach"
	jobs := []taskQueue2.OneJob{
		collectionQueueJob("episode-1", seriesRoot, 1, taskQueue2.Waiting, 6),
		collectionQueueJob("episode-1-alt", seriesRoot, 1, taskQueue2.Waiting, 6),
		collectionQueueJob("episode-2", seriesRoot, 2, taskQueue2.Failed, 7),
		collectionQueueJob("episode-3", seriesRoot, 3, taskQueue2.Waiting, 6),
		collectionQueueJob("current-job", seriesRoot, 4, taskQueue2.Waiting, 3),
	}
	jobs[1].ForceRun = true
	jobs[1].RetryTimes = 2
	jobs[1].ErrorInfo = "old failure"
	jobs[1].NextAttemptTime = emby.Time(time.Now().Add(time.Hour))
	for _, job := range jobs {
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
	}
	initialRevisions := make(map[string]uint64, len(jobs))
	for _, job := range jobs {
		_, stored := queue.GetOneJobByID(job.Id)
		initialRevisions[job.Id] = stored.StateRevision
	}
	seriesJobs := queue.GetSeriesJobs(seriesRoot)
	if len(seriesJobs) != len(jobs) {
		t.Fatalf("GetSeriesJobs returned %d jobs, want %d", len(seriesJobs), len(jobs))
	}
	seriesJobs[0].VideoFPath = "/mutated-copy.mkv"
	_, storedJob := queue.GetOneJobByID(seriesJobs[0].Id)
	if storedJob.VideoFPath == seriesJobs[0].VideoFPath {
		t.Fatal("GetSeriesJobs exposed mutable queue state")
	}

	videoPaths := map[string]struct{}{
		jobs[0].VideoFPath: {},
		jobs[2].VideoFPath: {},
		jobs[4].VideoFPath: {},
	}
	verifiedVideoPaths := NewVerifiedChineseVideoPaths(videoPaths)
	// Mutating the caller's working set after verification must not expand the
	// queue transition beyond the evidence snapshot.
	videoPaths[jobs[3].VideoFPath] = struct{}{}
	marked, err := queue.MarkSeriesEpisodesDone(seriesRoot, verifiedVideoPaths, "current-job")
	if err != nil {
		t.Fatal(err)
	}
	if marked != 2 {
		t.Fatalf("marked = %d, want 2", marked)
	}

	for _, id := range []string{"episode-1", "episode-2"} {
		found, job := queue.GetOneJobByID(id)
		if !found || job.JobStatus != taskQueue2.Done || job.TaskPriority != DefaultTaskPriorityLevel {
			t.Fatalf("backfilled job %s not completed: %+v", id, job)
		}
		if job.StateRevision != nextStateRevision(initialRevisions[id]) {
			t.Fatalf("backfilled job %s revision = %d, want %d", id, job.StateRevision, nextStateRevision(initialRevisions[id]))
		}
		if job.RetryTimes != 0 || job.ErrorInfo != "" || job.ForceRun || !time.Time(job.NextAttemptTime).IsZero() {
			t.Fatalf("backfilled job %s retained retry state: %+v", id, job)
		}
	}
	for _, id := range []string{"episode-1-alt", "episode-3", "current-job"} {
		_, job := queue.GetOneJobByID(id)
		if job.JobStatus != taskQueue2.Waiting {
			t.Fatalf("unrelated/excluded job %s changed: %+v", id, job)
		}
		if job.StateRevision != initialRevisions[id] {
			t.Fatalf("unrelated/excluded job %s revision changed: %d -> %d", id, initialRevisions[id], job.StateRevision)
		}
	}
}

func TestMarkSeriesEpisodesDonePersistenceFailureTracksDirtySnapshots(t *testing.T) {
	const queueName = "task_queue_collection_backfill_dirty_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	seriesRoot := "/media/backfill-dirty"
	job := collectionQueueJob("episode-dirty", seriesRoot, 1, taskQueue2.Waiting, FirstRetryTaskPriorityLevel)
	if added, err := queue.Add(job); err != nil || !added {
		t.Fatalf("Add() = %v, %v", added, err)
	}
	originalPersist := queue.persistPriority
	queue.persistPriority = func(int, []byte) error { return errors.New("injected backfill write failure") }
	marked, err := queue.MarkSeriesEpisodesDone(seriesRoot,
		NewVerifiedChineseVideoPaths(map[string]struct{}{job.VideoFPath: {}}), "")
	if err == nil || marked != 1 {
		t.Fatalf("MarkSeriesEpisodesDone() = %d, %v", marked, err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.JobStatus != taskQueue2.Done || current.TaskPriority != DefaultTaskPriorityLevel {
		t.Fatalf("failed persistence lost authoritative in-memory transition: %+v", current)
	}
	if len(queue.dirtyPriorities) != 2 {
		t.Fatalf("failed backfill did not track both snapshots: %v", queue.dirtyPriorities)
	}
	queue.persistPriority = originalPersist
	if err = queue.FlushDirtyPriorities(); err != nil {
		t.Fatalf("FlushDirtyPriorities() = %v", err)
	}
	if len(queue.dirtyPriorities) != 0 {
		t.Fatalf("dirty snapshots survived retry: %v", queue.dirtyPriorities)
	}
}

func TestMarkSeriesEpisodesDoneDefersClaimedCompanionToBatchOutcome(t *testing.T) {
	const queueName = "task_queue_collection_backfill_claimed_companion_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	seriesRoot := "/media/claimed-backfill"
	primary := collectionQueueJob("claimed-primary", seriesRoot, 1, taskQueue2.Waiting, DefaultTaskPriorityLevel)
	companion := collectionQueueJob("claimed-companion", seriesRoot, 2, taskQueue2.Waiting, DefaultTaskPriorityLevel)
	for _, job := range []taskQueue2.OneJob{primary, companion} {
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
	}
	claimed, err := queue.ClaimBatch([]taskQueue2.OneJob{primary, companion}, now)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimBatch() = %+v, %v", claimed, err)
	}
	_, reservedCompanion := queue.GetOneJobByID(companion.Id)
	if reservedCompanion.JobStatus != taskQueue2.Waiting || claimed[1].ClaimToken == 0 {
		t.Fatalf("invalid claimed-companion fixture: stored=%+v claimed=%+v", reservedCompanion, claimed[1])
	}
	reservedRevision := reservedCompanion.StateRevision

	marked, err := queue.MarkSeriesEpisodesDone(seriesRoot,
		NewVerifiedChineseVideoPaths(map[string]struct{}{companion.VideoFPath: {}}), primary.Id, companion.Id)
	if err != nil {
		t.Fatal(err)
	}
	if marked != 0 {
		t.Fatalf("MarkSeriesEpisodesDone() completed %d claimed companions, want 0", marked)
	}
	_, afterBackfill := queue.GetOneJobByID(companion.Id)
	if afterBackfill.JobStatus != taskQueue2.Waiting || afterBackfill.StateRevision != reservedRevision {
		t.Fatalf("backfill side transition polluted claimed companion: before=%+v after=%+v", reservedCompanion, afterBackfill)
	}

	if err = queue.ApplyOutcomesReliable([]JobOutcome{
		{Job: claimed[0], Err: nil},
		{Job: claimed[1], Err: ErrNoSubFound},
	}); err != nil {
		t.Fatal(err)
	}
	_, finalCompanion := queue.GetOneJobByID(companion.Id)
	if finalCompanion.JobStatus != taskQueue2.Waiting ||
		finalCompanion.TaskPriority != FirstRetryTaskPriorityLevel || finalCompanion.DownloadTimes != 1 {
		t.Fatalf("claimed companion outcome did not remain authoritative: %+v", finalCompanion)
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("claim state survived batch outcome: jobs=%v members=%v tokens=%v",
			queue.claimedJobs, queue.claimMembers, queue.claimTokens)
	}
}

func TestMarkSeriesEpisodesDoneCompletesReservedMemberOutsideActiveBatch(t *testing.T) {
	const queueName = "task_queue_collection_backfill_reserved_only_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)

	now := time.Now()
	seriesRoot := "/media/reserved-only-backfill"
	primary := collectionQueueJob("reserved-primary", seriesRoot, 1, taskQueue2.Waiting, DefaultTaskPriorityLevel)
	active := collectionQueueJob("reserved-active", seriesRoot, 2, taskQueue2.Waiting, DefaultTaskPriorityLevel)
	reservedOnly := collectionQueueJob("reserved-outside-batch", seriesRoot, 3, taskQueue2.Waiting, DefaultTaskPriorityLevel)
	for _, job := range []taskQueue2.OneJob{primary, active, reservedOnly} {
		job.Season = 1
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
	}
	claimed, err := queue.ClaimBatch([]taskQueue2.OneJob{primary, active}, now)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimBatch() = %+v, %v", claimed, err)
	}
	if _, reserved := queue.claimedJobs[reservedOnly.Id]; !reserved {
		t.Fatal("batch-external ready member was not reserved")
	}
	_, activeBefore := queue.GetOneJobByID(active.Id)

	marked, err := queue.MarkSeriesEpisodesDone(seriesRoot,
		NewVerifiedChineseVideoPaths(map[string]struct{}{
			active.VideoFPath:       {},
			reservedOnly.VideoFPath: {},
		}), primary.Id, active.Id)
	if err != nil {
		t.Fatal(err)
	}
	if marked != 1 {
		t.Fatalf("MarkSeriesEpisodesDone() = %d, want 1 reserved-only member", marked)
	}
	_, activeAfter := queue.GetOneJobByID(active.Id)
	if activeAfter.JobStatus != taskQueue2.Waiting || activeAfter.StateRevision != activeBefore.StateRevision {
		t.Fatalf("active batch companion was mutated: before=%+v after=%+v", activeBefore, activeAfter)
	}
	_, reservedAfter := queue.GetOneJobByID(reservedOnly.Id)
	if reservedAfter.JobStatus != taskQueue2.Done {
		t.Fatalf("reserved-only member was not completed: %+v", reservedAfter)
	}
	if _, stillClaimed := queue.claimedJobs[reservedOnly.Id]; stillClaimed {
		t.Fatalf("completed reserved-only member remained claimed: %v", queue.claimedJobs)
	}

	if err = queue.ApplyOutcomesReliable([]JobOutcome{
		{Job: claimed[0], Err: ErrNoSubFound},
		{Job: claimed[1], Err: nil},
	}); err != nil {
		t.Fatal(err)
	}
	_, finalActive := queue.GetOneJobByID(active.Id)
	if finalActive.JobStatus != taskQueue2.Done {
		t.Fatalf("active companion exact-path success was not applied: %+v", finalActive)
	}
	_, finalReserved := queue.GetOneJobByID(reservedOnly.Id)
	if finalReserved.JobStatus != taskQueue2.Done {
		t.Fatalf("reserved-only completion was overwritten: %+v", finalReserved)
	}
	if len(queue.claimedJobs) != 0 || len(queue.claimMembers) != 0 || len(queue.claimTokens) != 0 {
		t.Fatalf("claim state survived outcomes: jobs=%v members=%v tokens=%v",
			queue.claimedJobs, queue.claimMembers, queue.claimTokens)
	}
}

func TestGetReadySeriesJobsReturnsDueEpisodesInSameSeason(t *testing.T) {
	const queueName = "testReadySeriesBatch"
	cache_center.DelDb(queueName)
	defer cache_center.DelDb(queueName)

	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	defer queue.Close()
	now := time.Now()
	seriesRoot := "/media/series"
	jobs := []taskQueue2.OneJob{
		collectionQueueJob("primary", seriesRoot, 1, taskQueue2.Waiting, DefaultTaskPriorityLevel),
		collectionQueueJob("due-old", seriesRoot, 2, taskQueue2.Waiting, DefaultTaskPriorityLevel),
		collectionQueueJob("due-new", seriesRoot, 3, taskQueue2.Waiting, DefaultTaskPriorityLevel),
		collectionQueueJob("future", seriesRoot, 4, taskQueue2.Waiting, DefaultTaskPriorityLevel),
		collectionQueueJob("other-season", seriesRoot, 1, taskQueue2.Waiting, DefaultTaskPriorityLevel),
	}
	jobs[0].Season, jobs[0].AddedTime = 1, emby.Time(now.Add(-4*time.Hour))
	jobs[1].Season, jobs[1].AddedTime = 1, emby.Time(now.Add(-3*time.Hour))
	jobs[2].Season, jobs[2].AddedTime = 1, emby.Time(now.Add(-2*time.Hour))
	jobs[3].Season, jobs[3].NextAttemptTime = 1, emby.Time(now.Add(time.Hour))
	jobs[3].DownloadTimes, jobs[3].ErrorInfo = 1, "temporary network error"
	jobs[4].Season, jobs[4].AddedTime = 2, emby.Time(now.Add(-time.Hour))
	for _, job := range jobs {
		if ok, err := queue.Add(job); err != nil || !ok {
			t.Fatalf("add %s: ok=%v err=%v", job.Id, ok, err)
		}
	}

	got := queue.GetReadySeriesJobs(seriesRoot, 1, "primary", 2, now)
	if len(got) != 2 || got[0].Id != "due-old" || got[1].Id != "due-new" {
		t.Fatalf("ready series batch = %#v", got)
	}
}

func collectionQueueJob(id, seriesRoot string, episode int, status taskQueue2.JobStatus, priority int) taskQueue2.OneJob {
	now := emby.Time(time.Now())
	return taskQueue2.OneJob{
		Id:                id,
		VideoType:         common.Series,
		VideoFPath:        seriesRoot + "/" + id + ".mkv",
		SeriesRootDirPath: seriesRoot,
		Season:            1,
		Episode:           episode,
		JobStatus:         status,
		TaskPriority:      priority,
		AddedTime:         now,
		UpdateTime:        now,
	}
}
