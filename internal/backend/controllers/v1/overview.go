package v1

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ai_ambiguity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
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
	now := time.Now()
	schedule := cb.cronHelper.StatusSnapshot()
	c.JSON(http.StatusOK, backendTypes.ReplyOverview{
		Queue: summarizeJobs(jobs, now), Schedule: backendTypes.ScheduleView{Status: schedule.Status, NextScanAt: schedule.NextScanAt, ActiveWorkers: schedule.ActiveWorkers},
		Suppliers: buildSupplierDiagnostics(settings.Get(), subtitle_metrics.Snapshot(), used),
		Outcomes:  mapOutcomeCounts(outcomes), AI: mapAIRuntime(ai_ambiguity.Status()), GeneratedAt: now,
	})
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
