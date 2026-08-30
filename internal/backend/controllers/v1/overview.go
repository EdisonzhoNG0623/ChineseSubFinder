package v1

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ai_ambiguity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/gin-gonic/gin"
)

func (cb *ControllerBase) OverviewHandler(c *gin.Context) {
	days := 7
	if raw := c.Query("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || (value != 1 && value != 7 && value != 30) {
			c.JSON(http.StatusBadRequest, backendTypes.ReplyCommon{Message: "days must be 1, 7, or 30"})
			return
		}
		days = value
	}
	_, jobs, err := cb.cronHelper.DownloadQueue.GetAllJobs()
	if err != nil {
		cb.ErrorProcess(c, "OverviewHandler", err)
		return
	}
	outcomes, err := cb.cronHelper.FileDownloader.CacheCenter.TaskOutcomeSummary(days, time.Now())
	if err != nil {
		cb.ErrorProcess(c, "OverviewHandler", err)
		return
	}
	counts, err := cb.cronHelper.FileDownloader.CacheCenter.DailyDownloadCountSummary()
	if err != nil {
		cb.ErrorProcess(c, "OverviewHandler", err)
		return
	}
	used := make(map[string]int, len(counts))
	for _, count := range counts {
		used[count.SupplierName] = count.Count
	}
	// Query the queue clock first so an already-due NextWakeAt (returned as the
	// queue's current time) cannot appear a few microseconds in the future when
	// compared with the overview snapshot timestamp.
	queueNextAt, queueScheduled := cb.cronHelper.DownloadQueue.NextWakeAt()
	now := time.Now()
	schedule := cb.cronHelper.StatusSnapshot()
	queueSummary := summarizeJobs(jobs, now)
	runtime := subtitle_metrics.Snapshot()
	scheduleView := buildScheduleView(schedule.Status, schedule.NextScanAt, schedule.ActiveWorkers,
		queueSummary, jobs, queueNextAt, queueScheduled, now)
	scheduleView.LastSuccessAt = latestSupplierSaveAt(runtime)
	c.JSON(http.StatusOK, backendTypes.ReplyOverview{
		Queue: queueSummary, Schedule: scheduleView,
		Suppliers: buildSupplierDiagnostics(settings.Get(), runtime, used),
		Outcomes:  mapOutcomeCounts(outcomes), AI: mapAIRuntime(ai_ambiguity.Status()), GeneratedAt: now,
	})
}

func buildScheduleView(status string, nextScanAt time.Time, activeWorkers int, summary backendTypes.QueueSummary,
	jobs []taskQueueTypes.OneJob, queueNextAt time.Time, queueScheduled bool, now time.Time) backendTypes.ScheduleView {
	view := backendTypes.ScheduleView{
		Status: status, NextScanAt: nextScanAt, ActiveWorkers: activeWorkers,
		NextRetryAt: summary.EarliestRetryAt,
		CurrentJobs: make([]backendTypes.CurrentJobView, 0),
	}

	current := make([]taskQueueTypes.OneJob, 0, summary.Downloading)
	for _, job := range jobs {
		if job.JobStatus == taskQueueTypes.Downloading {
			current = append(current, job)
		}
	}
	sort.SliceStable(current, func(i, j int) bool {
		return time.Time(current[i].UpdateTime).After(time.Time(current[j].UpdateTime))
	})
	const currentJobLimit = 8
	if len(current) > currentJobLimit {
		current = current[:currentJobLimit]
	}
	for _, job := range current {
		view.CurrentJobs = append(view.CurrentJobs, backendTypes.CurrentJobView{
			ID: job.Id, Name: job.VideoName, VideoType: job.VideoType.String(), UpdatedAt: time.Time(job.UpdateTime),
		})
	}

	queueReady := queueScheduled && !queueNextAt.After(now)
	switch {
	case status == "stopped":
		view.Phase = "STOPPED"
	case status == "stopping":
		view.Phase = "STOPPING"
	case summary.Downloading > 0:
		view.Phase = "DOWNLOADING"
	case activeWorkers > 0:
		view.Phase = "PROCESSING"
	case summary.ReadyNow > 0 || queueReady:
		view.Phase = "READY"
	case summary.BackoffWaiting > 0:
		view.Phase = "BACKOFF"
	case queueScheduled:
		view.Phase = "SCHEDULED"
	default:
		view.Phase = "IDLE"
	}

	if status == "running" {
		if summary.Downloading > 0 || activeWorkers > 0 || summary.ReadyNow > 0 || queueReady {
			value := now
			view.NextActionAt = &value
		} else {
			setNextFutureTime(&view.NextActionAt, nextScanAt, now)
			if queueScheduled {
				setNextFutureTime(&view.NextActionAt, queueNextAt, now)
			}
		}
	}
	// LastCycleAt intentionally remains nil. The scheduler currently exposes no
	// authoritative cycle-completion event; a task timestamp must not impersonate one.
	return view
}

func latestSupplierSaveAt(runtime map[string]subtitle_metrics.SupplierRuntime) *time.Time {
	var latest time.Time
	for _, record := range runtime {
		if record.LastSaveAt.After(latest) {
			latest = record.LastSaveAt
		}
	}
	if latest.IsZero() || latest.Year() <= 1 {
		return nil
	}
	value := latest
	return &value
}

func setNextFutureTime(destination **time.Time, candidate, now time.Time) {
	if candidate.IsZero() || candidate.Before(now) {
		return
	}
	setEarlierTime(destination, candidate)
}

func (cb *ControllerBase) AIStatusHandler(c *gin.Context) {
	config := settings.Get().ExperimentalFunction.AISettings
	c.JSON(http.StatusOK, backendTypes.ReplyAIStatus{
		Enabled: config.Enabled, Configured: config.BaseURL != "" && config.Model != "",
		HasAPIKey: config.APIKey != "", BaseURL: config.BaseURL, Model: config.Model,
		MinConfidence: config.MinConfidence, TimeoutSeconds: config.TimeoutSeconds, Runtime: mapAIRuntime(ai_ambiguity.Status()),
	})
}

func mapOutcomeCounts(values []cache_center.TaskOutcomeCount) []backendTypes.OutcomeCount {
	out := make([]backendTypes.OutcomeCount, 0, len(values))
	for _, value := range values {
		out = append(out, backendTypes.OutcomeCount{WhichDay: value.WhichDay, VideoType: value.VideoType, Outcome: value.Outcome, Count: value.Count})
	}
	return out
}

func mapAIRuntime(value ai_ambiguity.RuntimeStatus) backendTypes.AIRuntimeView {
	return backendTypes.AIRuntimeView{Enabled: value.Enabled, Configured: value.Configured, Attempts: value.Attempts,
		Matches: value.Matches, NoMatches: value.NoMatches, Abstentions: value.Abstentions, Errors: value.Errors,
		LastLatencyMs: value.LastLatencyMs, LastAttemptAt: value.LastAttemptAt}
}

func (cb *ControllerBase) AITestHandler(c *gin.Context) {
	config := settings.Get().ExperimentalFunction.AISettings
	if !config.Enabled || config.Validate() != nil {
		c.JSON(http.StatusBadRequest, backendTypes.ReplyCommon{Message: "AI resolver is not enabled and valid"})
		return
	}
	request := episode_identity.AmbiguityRequest{SchemaVersion: episode_identity.AmbiguitySchemaVersion,
		Media: episode_identity.Request{SeriesName: "Connectivity test", Season: 1, Episode: 1},
		Candidates: []episode_identity.CandidateFact{
			{CandidateID: "candidate-a", Supplier: "diagnostic", Name: "Connectivity test S01E01", Season: 1, Episode: 1, DeterministicScore: 1000},
			{CandidateID: "candidate-b", Supplier: "diagnostic", Name: "Connectivity test S01E02", Season: 1, Episode: 2, DeterministicScore: 850},
		},
	}
	result, err := ai_ambiguity.ConfiguredResolver().ResolveAmbiguity(c.Request.Context(), request)
	if err != nil {
		cb.log.WithFields(map[string]interface{}{"event": "ai_connectivity_test", "result": "error"}).Warning("AI connectivity test failed")
		c.JSON(http.StatusBadGateway, backendTypes.ReplyCommon{Message: sanitizeAIError(err)})
		return
	}
	cb.log.WithFields(map[string]interface{}{"event": "ai_connectivity_test", "decision": result.Decision}).Info("AI connectivity test completed")
	c.JSON(http.StatusOK, result)
}

func sanitizeAIError(err error) string {
	message := err.Error()
	if len(message) > 240 {
		message = message[:240]
	}
	if strings.Contains(strings.ToLower(message), "authorization") {
		return "AI authorization failed"
	}
	return message
}
