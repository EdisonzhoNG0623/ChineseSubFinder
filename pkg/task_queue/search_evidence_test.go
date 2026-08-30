package task_queue

import (
	"errors"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	queueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

func TestStartupEstablishesLegacySearchBaselineWithoutMassWake(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "legacy-no-sub", VideoType: common.Anime, VideoFPath: "/media/show/S01E01.mkv",
		SeriesRootDirPath: "/media/show", SeriesName: "Example", Season: 1, Episode: 1,
		AbsoluteEpisode: 1, NumberingSource: "anime-lists", NumberingConfidence: 0.9,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 2,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(48 * time.Hour)),
	}
	queue := newIndexedQueueFromJobs(t, "task_queue_search_baseline_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})

	_, migrated := queue.GetOneJobByID(job.Id)
	if migrated.SearchFingerprint == "" || migrated.LastAttemptSearchFingerprint != migrated.SearchFingerprint {
		t.Fatalf("legacy identity baseline not established: %+v", migrated)
	}
	if !migrated.LegacySearchEvidenceBaseline {
		t.Fatal("legacy queue entry was not marked for one neutral metadata merge")
	}
	if migrated.LastAttemptPolicyFingerprint != settings.CurrentSearchPolicyFingerprint() {
		t.Fatalf("legacy policy baseline = %q", migrated.LastAttemptPolicyFingerprint)
	}
	if next, ok := queue.NextWakeAt(); !ok || !next.After(now.Add(time.Hour)) {
		t.Fatalf("feature deployment woke an unchanged legacy miss: next=%s ok=%v", next, ok)
	}
}

func TestRecurringAliasEvidenceUsesNeutralLegacyFillThenWakesOnChange(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "alias-evidence", VideoType: common.Series, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", Season: 1, Episode: 1,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 2,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(48 * time.Hour)),
	}
	queue := newIndexedQueueFromJobs(t, "task_queue_alias_evidence_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})

	baseline := job
	baseline.SeriesName = "Example Show"
	baseline.SearchAliases = []string{"Original Show", "示例剧"}
	if added, err := queue.Add(baseline); err != nil || added {
		t.Fatalf("legacy alias fill Add() = %v, %v", added, err)
	}
	_, filled := queue.GetOneJobByID(job.Id)
	if filled.LegacySearchEvidenceBaseline || filled.SearchFingerprint == "" ||
		filled.LastAttemptSearchFingerprint != filled.SearchFingerprint {
		t.Fatalf("legacy alias fill was not a neutral baseline: %+v", filled)
	}
	if next, ok := queue.NextWakeAt(); !ok || !next.After(now.Add(time.Hour)) {
		t.Fatalf("neutral legacy alias fill woke queue: next=%s ok=%v", next, ok)
	}

	incoming := filled
	incoming.SearchAliases = append(incoming.SearchAliases, "New Search Alias")
	if added, err := queue.Add(incoming); err != nil || added {
		t.Fatalf("new alias Add() = %v, %v", added, err)
	}
	_, changed := queue.GetOneJobByID(job.Id)
	if changed.SearchFingerprint == changed.LastAttemptSearchFingerprint {
		t.Fatalf("real alias change did not invalidate NoSub evidence: %+v", changed)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.After(time.Now()) {
		t.Fatalf("real alias change did not wake queue: next=%s ok=%v", next, ok)
	}
}

func TestOutcomeCommitStampsPolicyAndIdentityForEverySearchMember(t *testing.T) {
	const queueName = "task_queue_search_claim_test"
	cache_center.DelDb(queueName)
	t.Cleanup(func() { cache_center.DelDb(queueName) })
	queue := NewTaskQueue(cache_center.NewCacheCenter(queueName, log_helper.GetLogger4Tester()))
	t.Cleanup(queue.Close)
	now := time.Now()
	jobs := []queueTypes.OneJob{
		{Id: "claim-1", VideoType: common.Series, VideoFPath: "/show/S01E01.mkv", SeriesRootDirPath: "/show",
			SeriesName: "Show", Season: 1, Episode: 1, JobStatus: queueTypes.Waiting,
			TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)},
		{Id: "claim-2", VideoType: common.Series, VideoFPath: "/show/S01E02.mkv", SeriesRootDirPath: "/show",
			SeriesName: "Show", Season: 1, Episode: 2, JobStatus: queueTypes.Waiting,
			TaskPriority: DefaultTaskPriorityLevel, AddedTime: emby.Time(now), UpdateTime: emby.Time(now)},
	}
	for _, job := range jobs {
		if added, err := queue.Add(job); err != nil || !added {
			t.Fatalf("Add(%s) = %v, %v", job.Id, added, err)
		}
	}
	claimed, err := queue.ClaimBatch(jobs, now)
	if err != nil || len(claimed) != len(jobs) {
		t.Fatalf("ClaimBatch() = %d, %v", len(claimed), err)
	}
	policy := settings.CurrentSearchPolicyFingerprint()
	for _, job := range claimed {
		if job.LastAttemptSearchFingerprint != "" || job.LastAttemptPolicyFingerprint != "" {
			t.Fatalf("claim was prematurely stamped before supplier work: %+v", job)
		}
	}
	if err = queue.ApplyOutcomes([]JobOutcome{{Job: claimed[0], Err: ErrNoSubFound}, {Job: claimed[1], Err: ErrNoSubFound}}); err != nil {
		t.Fatal(err)
	}
	for _, original := range jobs {
		_, persisted := queue.GetOneJobByID(original.Id)
		if persisted.LastAttemptSearchFingerprint == "" || persisted.LastAttemptPolicyFingerprint != policy ||
			persisted.SearchEvidenceVersion != queueTypes.SearchEvidenceVersion {
			t.Fatalf("attempt baseline was not persisted for %s: %+v", original.Id, persisted)
		}
	}
}

func TestStartupNeutralizesVersionOneAttemptEvenWithPolicyFingerprint(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "legacy-v1", VideoType: common.Anime, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", SeriesName: "Example", Season: 1, Episode: 1,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 2,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(48 * time.Hour)),
		SearchFingerprint: "0123456789abcdef01234567", LastAttemptSearchFingerprint: "0123456789abcdef01234567",
		LastAttemptPolicyFingerprint: settings.CurrentSearchPolicyFingerprint(),
	}
	queue := newIndexedQueueFromJobs(t, "task_queue_v1_search_baseline_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})
	_, migrated := queue.GetOneJobByID(job.Id)
	if migrated.SearchEvidenceVersion != queueTypes.SearchEvidenceVersion ||
		migrated.LastAttemptSearchFingerprint != migrated.SearchFingerprint || !migrated.LegacySearchEvidenceBaseline {
		t.Fatalf("v1 attempt was not migrated neutrally: %+v", migrated)
	}
	if next, ok := queue.NextWakeAt(); !ok || !next.After(now.Add(time.Hour)) {
		t.Fatalf("v1 migration woke a historical miss: next=%s ok=%v", next, ok)
	}
}

func TestVersionOneAttemptWithPersistedAliasesWakesOnNextRealAliasChange(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "legacy-v1-with-aliases", VideoType: common.Anime, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", SeriesName: "Example", SearchAliases: []string{"Existing Alias"},
		Season: 1, Episode: 1, JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 2,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(48 * time.Hour)),
		SearchFingerprint: "0123456789abcdef01234567", LastAttemptSearchFingerprint: "0123456789abcdef01234567",
		LastAttemptPolicyFingerprint: settings.CurrentSearchPolicyFingerprint(),
	}
	queue := newIndexedQueueFromJobs(t, "task_queue_v1_alias_search_baseline_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})
	_, migrated := queue.GetOneJobByID(job.Id)
	if migrated.SearchEvidenceVersion != queueTypes.SearchEvidenceVersion ||
		migrated.LastAttemptSearchFingerprint != migrated.SearchFingerprint || migrated.LegacySearchEvidenceBaseline {
		t.Fatalf("v1 aliases were not migrated as a complete baseline: %+v", migrated)
	}

	incoming := migrated
	incoming.SearchAliases = append(incoming.SearchAliases, "Genuinely New Alias")
	if added, err := queue.Add(incoming); err != nil || added {
		t.Fatalf("new alias Add() = %v, %v", added, err)
	}
	_, changed := queue.GetOneJobByID(job.Id)
	if changed.SearchFingerprint == changed.LastAttemptSearchFingerprint {
		t.Fatalf("real alias change was incorrectly neutralized: %+v", changed)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.After(time.Now()) {
		t.Fatalf("real alias change did not wake migrated v1 job: next=%s ok=%v", next, ok)
	}
}

func TestNonSearchMetadataDoesNotConsumeLegacyNeutralAliasFill(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "legacy-non-search", VideoType: common.Series, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", SeriesName: "Example", Season: 1, Episode: 1,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 2,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(48 * time.Hour)),
	}
	queue := newIndexedQueueFromJobs(t, "task_queue_legacy_non_search_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})
	_, migrated := queue.GetOneJobByID(job.Id)

	metadataOnly := migrated
	metadataOnly.MediaServerInsideVideoID = "new-media-id"
	if added, err := queue.Add(metadataOnly); err != nil || added {
		t.Fatalf("metadata merge Add() = %v, %v", added, err)
	}
	_, afterMetadata := queue.GetOneJobByID(job.Id)
	if !afterMetadata.LegacySearchEvidenceBaseline {
		t.Fatal("non-search metadata consumed the neutral search baseline")
	}

	aliasFill := afterMetadata
	aliasFill.SearchAliases = []string{"Original Example"}
	if added, err := queue.Add(aliasFill); err != nil || added {
		t.Fatalf("alias fill Add() = %v, %v", added, err)
	}
	_, afterAlias := queue.GetOneJobByID(job.Id)
	if afterAlias.LegacySearchEvidenceBaseline || afterAlias.LastAttemptSearchFingerprint != afterAlias.SearchFingerprint {
		t.Fatalf("first real alias fill was not neutral: %+v", afterAlias)
	}
	if next, ok := queue.NextWakeAt(); !ok || !next.After(now.Add(time.Hour)) {
		t.Fatalf("neutral alias fill woke queue: next=%s ok=%v", next, ok)
	}
}

func TestRecurringScanMergesImprovedNoSubtitleEvidenceAndWakesJob(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "improved-identity", VideoType: common.Anime, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", SeriesName: "Show", Season: 1, Episode: 1,
		AbsoluteEpisode: 1, NumberingSource: "old-map", NumberingConfidence: 0.8,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 2, RetryTimes: 2,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(48 * time.Hour)),
		LastAttemptPolicyFingerprint: settings.CurrentSearchPolicyFingerprint(),
	}
	job.RefreshSearchFingerprint()
	job.LastAttemptSearchFingerprint = job.SearchFingerprint
	queue := newIndexedQueueFromJobs(t, "task_queue_search_merge_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})

	incoming := job
	incoming.AbsoluteEpisode = 13
	incoming.NumberingSource = "corrected-map"
	incoming.NumberingConfidence = job.NumberingConfidence
	if added, err := queue.Add(incoming); err != nil || added {
		t.Fatalf("duplicate Add() = %v, %v", added, err)
	}
	_, merged := queue.GetOneJobByID(job.Id)
	if merged.AbsoluteEpisode != 13 || merged.SearchFingerprint == merged.LastAttemptSearchFingerprint {
		t.Fatalf("corrected identity was not merged: %+v", merged)
	}
	if merged.DownloadTimes != job.DownloadTimes || merged.RetryTimes != job.RetryTimes ||
		!time.Time(merged.UpdateTime).Equal(time.Time(job.UpdateTime)) ||
		!time.Time(merged.NextAttemptTime).Equal(time.Time(job.NextAttemptTime)) {
		t.Fatalf("evidence merge reset lifecycle state: %+v", merged)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.After(time.Now()) {
		t.Fatalf("corrected conclusive miss was not woken: next=%s ok=%v", next, ok)
	}
}

func TestEvidenceMergePersistenceFailureRollsBackAndCanRetry(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "rollback-evidence", VideoType: common.Anime, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", SeriesName: "Show", Season: 1, Episode: 1,
		AbsoluteEpisode: 1, NumberingSource: "old-map", NumberingConfidence: 0.8,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 1,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(48 * time.Hour)),
		LastAttemptPolicyFingerprint: settings.CurrentSearchPolicyFingerprint(),
	}
	job.RefreshSearchFingerprint()
	job.LastAttemptSearchFingerprint = job.SearchFingerprint
	queue := newIndexedQueueFromJobs(t, "task_queue_search_merge_rollback_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})
	originalPersist := queue.persistPriority
	queue.persistPriority = func(int, []byte) error { return errors.New("injected persistence failure") }

	incoming := job
	incoming.AbsoluteEpisode = 13
	incoming.NumberingSource = "corrected-map"
	if added, err := queue.Add(incoming); err == nil || added {
		t.Fatalf("failed duplicate Add() = %v, %v", added, err)
	}
	_, rolledBack := queue.GetOneJobByID(job.Id)
	if rolledBack.AbsoluteEpisode != job.AbsoluteEpisode || rolledBack.SearchFingerprint != job.SearchFingerprint {
		t.Fatalf("failed persistence left improved evidence in memory: %+v", rolledBack)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.Before(now.Add(time.Hour)) {
		t.Fatalf("failed persistence changed schedule: next=%s ok=%v", next, ok)
	}

	queue.persistPriority = originalPersist
	if added, err := queue.Add(incoming); err != nil || added {
		t.Fatalf("retry duplicate Add() = %v, %v", added, err)
	}
	_, retried := queue.GetOneJobByID(job.Id)
	if retried.AbsoluteEpisode != incoming.AbsoluteEpisode || retried.SearchFingerprint == retried.LastAttemptSearchFingerprint {
		t.Fatalf("evidence merge was not retryable after rollback: %+v", retried)
	}
}

func TestRecurringScanDoesNotBypassLocalErrorOrDowngradeEvidence(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	base := queueTypes.OneJob{
		Id: "protected-evidence", VideoType: common.Series, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", SeriesName: "Show", Season: 1, Episode: 1,
		AbsoluteEpisode: 12, NumberingSource: "trusted", NumberingConfidence: 0.9,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 1,
		ErrorInfo: "permission denied", NextAttemptTime: emby.Time(now.Add(6 * time.Hour)),
		LastAttemptPolicyFingerprint: settings.CurrentSearchPolicyFingerprint(),
	}
	base.RefreshSearchFingerprint()
	base.LastAttemptSearchFingerprint = base.SearchFingerprint
	queue := newIndexedQueueFromJobs(t, "task_queue_search_protection_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{base.Id: base})

	incoming := base
	incoming.AbsoluteEpisode = 1
	incoming.NumberingSource = "weak"
	incoming.NumberingConfidence = 0.2
	if added, err := queue.Add(incoming); err != nil || added {
		t.Fatalf("duplicate Add() = %v, %v", added, err)
	}
	_, current := queue.GetOneJobByID(base.Id)
	if current.AbsoluteEpisode != base.AbsoluteEpisode || current.SearchFingerprint != base.SearchFingerprint {
		t.Fatalf("local failure evidence was overwritten: %+v", current)
	}
	if next, ok := queue.NextWakeAt(); !ok || next.Before(now.Add(time.Hour)) {
		t.Fatalf("local failure was incorrectly woken: next=%s ok=%v", next, ok)
	}
}

func TestTokenZeroOutcomeCannotOverwriteConcurrentSearchEvidenceMerge(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	job := queueTypes.OneJob{
		Id: "stale-search-evidence", VideoType: common.Anime, VideoFPath: "/show/S01E01.mkv",
		SeriesRootDirPath: "/show", SeriesName: "Show", Season: 1, Episode: 1,
		JobStatus: queueTypes.Waiting, TaskPriority: FirstRetryTaskPriorityLevel,
		AddedTime: emby.Time(now), UpdateTime: emby.Time(now), DownloadTimes: 1,
		ErrorInfo: ErrNoSubFound.Error(), NextAttemptTime: emby.Time(now.Add(6 * time.Hour)),
	}
	job.RefreshSearchFingerprint()
	job.LastAttemptSearchFingerprint = job.SearchFingerprint
	job.LastAttemptPolicyFingerprint = settings.CurrentSearchPolicyFingerprint()
	queue := newIndexedQueueFromJobs(t, "task_queue_stale_search_evidence_test", FirstRetryTaskPriorityLevel,
		map[string]queueTypes.OneJob{job.Id: job})
	_, stale := queue.GetOneJobByID(job.Id)

	incoming := job
	incoming.AbsoluteEpisode = 13
	incoming.NumberingSource = "corrected-map"
	incoming.NumberingConfidence = 1
	if added, err := queue.Add(incoming); err != nil || added {
		t.Fatalf("evidence merge Add() = %v, %v", added, err)
	}
	if err := queue.ApplyOutcomes([]JobOutcome{{Job: stale, Err: errors.New("stale metadata validation")}}); !errors.Is(err, ErrClaimUnavailable) {
		t.Fatalf("stale token-zero outcome = %v, want ErrClaimUnavailable", err)
	}
	_, current := queue.GetOneJobByID(job.Id)
	if current.AbsoluteEpisode != 13 || current.NumberingSource != "corrected-map" ||
		current.DownloadTimes != job.DownloadTimes || current.ErrorInfo != job.ErrorInfo {
		t.Fatalf("stale outcome overwrote merged search evidence: %+v", current)
	}
}
