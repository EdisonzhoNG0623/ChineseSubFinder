package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/gin-gonic/gin"
)

func TestFilterJobsAndSummary(t *testing.T) {
	now := time.Now()
	jobs := []taskQueueTypes.OneJob{
		{VideoName: "Anime 01.mkv", VideoType: common.Anime, JobStatus: taskQueueTypes.Waiting, TaskPriority: 6, ErrorInfo: "No Sub Found", UpdateTime: emby.Time(now), ForceRun: true, SeriesRootDirPath: "/media/anime", Season: 1, AbsoluteEpisode: 13},
		{VideoName: "Anime 02.mkv", VideoType: common.Anime, JobStatus: taskQueueTypes.Waiting, TaskPriority: 6, SeriesRootDirPath: "/media/anime", Season: 1},
		{VideoName: "Movie.mkv", VideoType: common.Movie, JobStatus: taskQueueTypes.Done, TaskPriority: 5, UpdateTime: emby.Time(now)},
	}
	status := int(taskQueueTypes.Waiting)
	filtered := filterJobs(jobs, jobPageQuery{Status: &status, ErrorCategory: "NO_SUBTITLE"}, now)
	if len(filtered) != 1 || filtered[0].VideoName != "Anime 01.mkv" {
		t.Fatalf("unexpected filtered jobs: %+v", filtered)
	}
	summary := summarizeJobs(jobs, now)
	if summary.Total != 3 || summary.ByVideoType["anime"] != 2 || summary.ByStatus["done"] != 1 ||
		summary.WaitingSeries != 2 || summary.SeriesGroups != 1 || summary.BatchableGroups != 1 ||
		summary.EpisodeWaiting != 2 || summary.NumberingReady != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestQueueSummaryPreservesHistoricalErrorsAndAddsActionableErrors(t *testing.T) {
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(45 * time.Minute)
	oldReady := now.Add(-3 * time.Hour)
	newReady := now.Add(-time.Hour)
	lastDone := now.Add(-20 * time.Minute)
	jobs := []taskQueueTypes.OneJob{
		{JobStatus: taskQueueTypes.Waiting, ErrorInfo: "No Sub Found", DownloadTimes: 1, UpdateTime: emby.Time(now.Add(-30 * 24 * time.Hour)), NextAttemptTime: emby.Time(retryAt)},
		{JobStatus: taskQueueTypes.Waiting, AddedTime: emby.Time(oldReady)},
		{JobStatus: taskQueueTypes.Waiting, AddedTime: emby.Time(newReady), ForceRun: true},
		{JobStatus: taskQueueTypes.Downloading, ErrorInfo: "temporary network timeout"},
		{JobStatus: taskQueueTypes.Done, ErrorInfo: "No Sub Found", UpdateTime: emby.Time(lastDone)},
		{JobStatus: taskQueueTypes.Ignore, ErrorInfo: "series metadata root not found"},
	}

	summary := summarizeJobs(jobs, now)
	if summary.Downloading != 1 || summary.ReadyNow != 2 || summary.RetryScheduled != 1 {
		t.Fatalf("unexpected operational counts: %+v", summary)
	}
	if summary.EarliestRetryAt == nil || !summary.EarliestRetryAt.Equal(retryAt) {
		t.Fatalf("earliest retry = %v, want %v", summary.EarliestRetryAt, retryAt)
	}
	if summary.OldestReadyAt == nil || !summary.OldestReadyAt.Equal(oldReady) {
		t.Fatalf("oldest ready = %v, want %v", summary.OldestReadyAt, oldReady)
	}
	if summary.LastCompletedAt == nil || !summary.LastCompletedAt.Equal(lastDone) {
		t.Fatalf("last completion = %v, want %v", summary.LastCompletedAt, lastDone)
	}
	if summary.ByErrorCategory["NO_SUBTITLE"] != 2 || summary.ByErrorCategory["TRANSIENT"] != 1 || summary.ByErrorCategory["LOCAL"] != 1 {
		t.Fatalf("historical error summary changed semantics: %+v", summary.ByErrorCategory)
	}
	if summary.ActionableByErrorCategory["NO_SUBTITLE"] != 1 || summary.ActionableByErrorCategory["TRANSIENT"] != 1 ||
		summary.ActionableByErrorCategory["LOCAL"] != 0 {
		t.Fatalf("unexpected actionable errors: %+v", summary.ActionableByErrorCategory)
	}
	if summary.MetadataBlocked != 0 {
		t.Fatalf("ignored metadata error counted as actionable: %+v", summary)
	}
}

func TestFilterJobsByDerivedQueueState(t *testing.T) {
	now := time.Now()
	jobs := []taskQueueTypes.OneJob{
		{Id: "ready", JobStatus: taskQueueTypes.Waiting},
		{Id: "retry", JobStatus: taskQueueTypes.Waiting, ErrorInfo: "temporary network timeout", DownloadTimes: 1, UpdateTime: emby.Time(now), NextAttemptTime: emby.Time(now.Add(time.Hour))},
		{Id: "active", JobStatus: taskQueueTypes.Downloading},
		{Id: "historical", JobStatus: taskQueueTypes.Done, ErrorInfo: "series metadata root not found"},
		{Id: "blocked", JobStatus: taskQueueTypes.Waiting, ErrorInfo: "series metadata root not found"},
	}
	assertIDs := func(state string, want ...string) {
		t.Helper()
		got := filterJobs(jobs, jobPageQuery{QueueState: state}, now)
		if len(got) != len(want) {
			t.Fatalf("%s returned %+v, want %v", state, got, want)
		}
		for index := range want {
			if got[index].Id != want[index] {
				t.Fatalf("%s returned %+v, want %v", state, got, want)
			}
		}
	}
	assertIDs("ready", "ready", "blocked")
	assertIDs("retry_scheduled", "retry")
	assertIDs("downloading", "active")
	assertIDs("metadata_blocked", "blocked")
	historical := filterJobs(jobs, jobPageQuery{ErrorCategory: "LOCAL"}, now)
	if len(historical) != 2 || historical[0].Id != "historical" || historical[1].Id != "blocked" {
		t.Fatalf("legacy error filter no longer includes history: %+v", historical)
	}
	actionable := filterJobs(jobs, jobPageQuery{ErrorCategory: "LOCAL", ActionableOnly: true}, now)
	if len(actionable) != 1 || actionable[0].Id != "blocked" {
		t.Fatalf("actionable error filter returned historical tasks: %+v", actionable)
	}
}

func TestBackoffWaitingExcludesScheduledRetriesThatAreAlreadyReady(t *testing.T) {
	now := time.Now()
	jobs := []taskQueueTypes.OneJob{
		{Id: "future", JobStatus: taskQueueTypes.Waiting, DownloadTimes: 1, NextAttemptTime: emby.Time(now.Add(time.Hour))},
		{Id: "expired", JobStatus: taskQueueTypes.Waiting, DownloadTimes: 1, NextAttemptTime: emby.Time(now.Add(-time.Hour))},
	}
	summary := summarizeJobs(jobs, now)
	if summary.RetryScheduled != 2 || summary.BackoffWaiting != 1 || summary.ReadyNow != 1 {
		t.Fatalf("overlapping retry summary = %+v", summary)
	}
	legacy := filterJobs(jobs, jobPageQuery{QueueState: "retry_scheduled"}, now)
	backoff := filterJobs(jobs, jobPageQuery{QueueState: "backoff_waiting"}, now)
	if len(legacy) != 2 || len(backoff) != 1 || backoff[0].Id != "future" {
		t.Fatalf("legacy=%+v backoff=%+v", legacy, backoff)
	}
}

func TestParseJobPageQueryActionableOnlyIsAdditive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/jobs?errorCategory=NO_SUBTITLE&actionableOnly=true", nil)
	query, err := parseJobPageQuery(context)
	if err != nil || !query.ActionableOnly || query.ErrorCategory != "NO_SUBTITLE" {
		t.Fatalf("parse actionable query = %+v, %v", query, err)
	}

	context, _ = gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/jobs?actionableOnly=invalid", nil)
	if _, err = parseJobPageQuery(context); err == nil {
		t.Fatal("invalid actionableOnly was accepted")
	}
}

func TestQueueSummaryJSONKeepsLegacyAndAddsActionableField(t *testing.T) {
	summary := summarizeJobs([]taskQueueTypes.OneJob{
		{JobStatus: taskQueueTypes.Waiting, ErrorInfo: "No Sub Found"},
		{JobStatus: taskQueueTypes.Done, ErrorInfo: "No Sub Found"},
	}, time.Now())
	payload, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]json.RawMessage
	if err = json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response["by_error_category"] == nil || response["actionable_by_error_category"] == nil {
		t.Fatalf("compatibility fields missing from response: %s", payload)
	}
}

func TestSortJobsByName(t *testing.T) {
	jobs := []taskQueueTypes.OneJob{{VideoName: "Zulu"}, {VideoName: "Alpha"}}
	sortJobs(jobs, "name", "asc", time.Now())
	if jobs[0].VideoName != "Alpha" {
		t.Fatalf("unexpected sort: %+v", jobs)
	}
}

func TestSortJobsByEffectiveNextAttemptAt(t *testing.T) {
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	jobs := []taskQueueTypes.OneJob{
		{Id: "administrative", JobStatus: taskQueueTypes.Waiting, ForceRun: true, NotBeforeTime: emby.Time(now.Add(3 * time.Hour))},
		{Id: "retry", JobStatus: taskQueueTypes.Waiting, DownloadTimes: 1, NextAttemptTime: emby.Time(now.Add(time.Hour))},
	}

	sortJobs(jobs, "nextAttemptAt", "asc", now)
	if jobs[0].Id != "retry" || jobs[1].Id != "administrative" {
		t.Fatalf("effective retry order = %s, %s", jobs[0].Id, jobs[1].Id)
	}
}

func TestRequestedTaskPriorityCompatibility(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		want   int
		wantOK bool
	}{
		{name: "canonical high", value: "high", want: task_queue.HighTaskPriorityLevel, wantOK: true},
		{name: "localized high", value: "高", want: task_queue.HighTaskPriorityLevel, wantOK: true},
		{name: "canonical middle", value: "middle", want: task_queue.DefaultTaskPriorityLevel, wantOK: true},
		{name: "legacy typo middle", value: "mddile", want: task_queue.DefaultTaskPriorityLevel, wantOK: true},
		{name: "localized middle", value: "中", want: task_queue.DefaultTaskPriorityLevel, wantOK: true},
		{name: "canonical low", value: "low", want: task_queue.LowTaskPriorityLevel, wantOK: true},
		{name: "localized low", value: "低", want: task_queue.LowTaskPriorityLevel, wantOK: true},
		{name: "empty rejected", value: ""},
		{name: "unknown rejected", value: "urgent"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := requestedTaskPriority(test.value)
			if got != test.want || ok != test.wantOK {
				t.Fatalf("requestedTaskPriority(%q) = (%d, %v), want (%d, %v)", test.value, got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestApplyRequestedJobChangesAreIndependentAndAtomic(t *testing.T) {
	notBefore := emby.Time(time.Now().Add(time.Hour))
	high := "high"
	priorityOnly := taskQueueTypes.OneJob{
		JobStatus: taskQueueTypes.Done, TaskPriority: 6, NotBeforeTime: notBefore,
	}
	changed, err := applyRequestedJobChanges(&priorityOnly, backendTypes.ReqChangeJobStatus{TaskPriority: &high})
	if err != nil || !changed {
		t.Fatalf("priority-only change = (%v, %v)", changed, err)
	}
	if priorityOnly.TaskPriority != task_queue.HighTaskPriorityLevel || priorityOnly.JobStatus != taskQueueTypes.Done ||
		priorityOnly.ForceRun || !time.Time(priorityOnly.NotBeforeTime).Equal(time.Time(notBefore)) {
		t.Fatalf("priority-only change touched unrelated state: %+v", priorityOnly)
	}

	waiting := taskQueueTypes.Waiting
	statusOnly := taskQueueTypes.OneJob{
		JobStatus: taskQueueTypes.Done, TaskPriority: 4, NotBeforeTime: notBefore,
	}
	changed, err = applyRequestedJobChanges(&statusOnly, backendTypes.ReqChangeJobStatus{JobStatus: &waiting})
	if err != nil || !changed {
		t.Fatalf("status-only change = (%v, %v)", changed, err)
	}
	if statusOnly.TaskPriority != 4 || statusOnly.JobStatus != taskQueueTypes.Waiting || !statusOnly.ForceRun ||
		!time.Time(statusOnly.NotBeforeTime).IsZero() {
		t.Fatalf("status-only change lost exact priority or retry semantics: %+v", statusOnly)
	}

	ignore := taskQueueTypes.Ignore
	ignoreOnly := taskQueueTypes.OneJob{JobStatus: taskQueueTypes.Downloading, TaskPriority: 6, ForceRun: true}
	changed, err = applyRequestedJobChanges(&ignoreOnly, backendTypes.ReqChangeJobStatus{JobStatus: &ignore})
	if err != nil || !changed || ignoreOnly.JobStatus != taskQueueTypes.Ignore || ignoreOnly.TaskPriority != 6 || ignoreOnly.ForceRun {
		t.Fatalf("ignore-only change = %+v, changed=%v err=%v", ignoreOnly, changed, err)
	}

	low := "low"
	invalidStatus := taskQueueTypes.JobStatus(99)
	original := taskQueueTypes.OneJob{JobStatus: taskQueueTypes.Done, TaskPriority: 4, ForceRun: true, NotBeforeTime: notBefore}
	invalid := original
	if _, err = applyRequestedJobChanges(&invalid, backendTypes.ReqChangeJobStatus{TaskPriority: &low, JobStatus: &invalidStatus}); err == nil {
		t.Fatal("invalid status was accepted")
	}
	if invalid.JobStatus != original.JobStatus || invalid.TaskPriority != original.TaskPriority || invalid.ForceRun != original.ForceRun ||
		!time.Time(invalid.NotBeforeTime).Equal(time.Time(original.NotBeforeTime)) {
		t.Fatalf("invalid multi-field request partially mutated job: got %+v want %+v", invalid, original)
	}
	if _, err = applyRequestedJobChanges(&invalid, backendTypes.ReqChangeJobStatus{}); err == nil {
		t.Fatal("empty patch was accepted")
	}

	active := taskQueueTypes.OneJob{JobStatus: taskQueueTypes.Downloading, TaskPriority: 6, ClaimToken: 42}
	if _, err = applyRequestedJobChanges(&active, backendTypes.ReqChangeJobStatus{TaskPriority: &high}); !errors.Is(err, errDownloadingPriorityChange) {
		t.Fatalf("active priority-only change = %v, want conflict", err)
	}
	if active.TaskPriority != 6 || active.JobStatus != taskQueueTypes.Downloading || active.ClaimToken != 42 {
		t.Fatalf("rejected active priority change mutated job: %+v", active)
	}
}

func TestReqChangeJobStatusPreservesOmittedFields(t *testing.T) {
	var request backendTypes.ReqChangeJobStatus
	if err := json.Unmarshal([]byte(`{"id":"job","task_priority":"high"}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.TaskPriority == nil || *request.TaskPriority != "high" || request.JobStatus != nil {
		t.Fatalf("unexpected optional request fields: %+v", request)
	}
}

func TestNewJobViewIncludesAnimeFallbackPlan(t *testing.T) {
	job := taskQueueTypes.OneJob{
		VideoType: common.Anime, SeriesName: "Example Anime", SeriesRootDirPath: "/media/Example Anime",
		Season: 8, Episode: 11, AbsoluteEpisode: 288, SceneSeason: 8, SceneEpisode: 10,
		NumberingSource: "anime-lists", NumberingConfidence: 1,
		SearchFingerprint: "0123456789abcdef",
	}
	view := newJobView(job, time.Now())
	if !view.Identity.IsAnime || view.Identity.AbsoluteEpisode != 288 || view.Identity.SearchFingerprint != job.SearchFingerprint || len(view.Identity.QueryPlan) < 3 {
		t.Fatalf("unexpected identity view: %+v", view.Identity)
	}
	foundAbsolute := false
	foundScene := false
	for _, query := range view.Identity.QueryPlan {
		if query.Kind == "ABSOLUTE" && query.Absolute == 288 {
			foundAbsolute = true
		}
		if query.Kind == "SCENE" && query.Season == 8 && query.Episode == 10 {
			foundScene = true
		}
	}
	if !foundAbsolute || !foundScene {
		t.Fatalf("missing fallback variants: %+v", view.Identity.QueryPlan)
	}
}
