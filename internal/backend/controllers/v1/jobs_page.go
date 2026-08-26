package v1

import (
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	backendTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
	"github.com/gin-gonic/gin"
)

type jobPageQuery struct {
	Page          int
	PageSize      int
	Status        *int
	VideoType     *int
	Priority      string
	ErrorCategory string
	Search        string
	SortBy        string
	SortOrder     string
}

func (cb *ControllerBase) JobsPageHandler(c *gin.Context) {
	query, err := parseJobPageQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, backendTypes.ReplyCommon{Message: err.Error()})
		return
	}
	_, jobs, err := cb.cronHelper.DownloadQueue.GetAllJobs()
	if err != nil {
		cb.ErrorProcess(c, "JobsPageHandler", err)
		return
	}
	now := time.Now()
	summary := summarizeJobs(jobs, now)
	filtered := filterJobs(jobs, query)
	sortJobs(filtered, query.SortBy, query.SortOrder)

	totalItems := len(filtered)
	totalPages := 0
	if totalItems > 0 {
		totalPages = (totalItems + query.PageSize - 1) / query.PageSize
	}
	start := (query.Page - 1) * query.PageSize
	if start > totalItems {
		start = totalItems
	}
	end := start + query.PageSize
	if end > totalItems {
		end = totalItems
	}
	views := make([]backendTypes.JobView, 0, end-start)
	for _, job := range filtered[start:end] {
		views = append(views, newJobView(job, now))
	}
	c.JSON(http.StatusOK, backendTypes.ReplyJobPage{
		Data:        views,
		Pagination:  backendTypes.JobPagination{Page: query.Page, PageSize: query.PageSize, TotalItems: totalItems, TotalPages: totalPages},
		Summary:     summary,
		GeneratedAt: now,
	})
}

func parseJobPageQuery(c *gin.Context) (jobPageQuery, error) {
	query := jobPageQuery{Page: 1, PageSize: 20, SortBy: "updatedAt", SortOrder: "desc"}
	var err error
	if raw := c.Query("page"); raw != "" {
		query.Page, err = strconv.Atoi(raw)
		if err != nil || query.Page < 1 {
			return query, newQueryError("page", "must be a positive integer")
		}
	}
	if raw := c.Query("pageSize"); raw != "" {
		query.PageSize, err = strconv.Atoi(raw)
		if err != nil || query.PageSize < 1 || query.PageSize > 200 {
			return query, newQueryError("pageSize", "must be between 1 and 200")
		}
	}
	if raw := c.Query("status"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < int(taskQueueTypes.Waiting) || value > int(taskQueueTypes.Ignore) {
			return query, newQueryError("status", "is invalid")
		}
		query.Status = &value
	}
	if raw := c.Query("videoType"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < int(common.Movie) || value > int(common.Anime) {
			return query, newQueryError("videoType", "is invalid")
		}
		query.VideoType = &value
	}
	query.Priority = strings.ToLower(strings.TrimSpace(c.Query("priority")))
	if query.Priority != "" && query.Priority != "high" && query.Priority != "middle" && query.Priority != "low" {
		value, parseErr := strconv.Atoi(query.Priority)
		if parseErr != nil || value < 0 || value > 10 {
			return query, newQueryError("priority", "is invalid")
		}
	}
	query.ErrorCategory = strings.ToUpper(strings.TrimSpace(c.Query("errorCategory")))
	if query.ErrorCategory != "" && !validErrorCategory(query.ErrorCategory) {
		return query, newQueryError("errorCategory", "is invalid")
	}
	query.Search = strings.TrimSpace(c.Query("search"))
	if len([]rune(query.Search)) > 200 {
		return query, newQueryError("search", "must be at most 200 characters")
	}
	if raw := c.Query("sortBy"); raw != "" {
		query.SortBy = raw
	}
	if query.SortBy != "updatedAt" && query.SortBy != "addedAt" && query.SortBy != "nextAttemptAt" && query.SortBy != "priority" && query.SortBy != "name" {
		return query, newQueryError("sortBy", "is invalid")
	}
	if raw := strings.ToLower(c.Query("sortOrder")); raw != "" {
		query.SortOrder = raw
	}
	if query.SortOrder != "asc" && query.SortOrder != "desc" {
		return query, newQueryError("sortOrder", "must be asc or desc")
	}
	return query, nil
}

type queryError struct{ field, message string }

func (e queryError) Error() string              { return e.field + " " + e.message }
func newQueryError(field, message string) error { return queryError{field: field, message: message} }

func validErrorCategory(value string) bool {
	switch task_queue.ErrorCategory(value) {
	case task_queue.ErrorCategoryNone, task_queue.ErrorCategoryNoSubtitle, task_queue.ErrorCategoryTransient,
		task_queue.ErrorCategoryLocal, task_queue.ErrorCategoryBlocked, task_queue.ErrorCategoryUnknown:
		return true
	default:
		return false
	}
}

func summarizeJobs(jobs []taskQueueTypes.OneJob, now time.Time) backendTypes.QueueSummary {
	summary := backendTypes.QueueSummary{
		ByStatus: make(map[string]int), ByVideoType: make(map[string]int), ByErrorCategory: make(map[string]int),
	}
	seriesGroups := make(map[string]int)
	readySeriesGroups := make(map[string]int)
	for _, job := range jobs {
		summary.Total++
		summary.ByStatus[job.JobStatus.String()]++
		summary.ByVideoType[job.VideoType.String()]++
		diagnostic := task_queue.DiagnoseRetry(job, now)
		summary.ByErrorCategory[string(diagnostic.Category)]++
		if diagnostic.IsScheduled {
			summary.RetryScheduled++
		}
		ready := job.JobStatus == taskQueueTypes.Waiting && (!diagnostic.IsScheduled || diagnostic.IsReady || diagnostic.IsForced)
		if ready {
			summary.ReadyNow++
		}
		if job.JobStatus == taskQueueTypes.Waiting && job.SeriesRootDirPath != "" && job.Season > 0 {
			summary.WaitingSeries++
			summary.EpisodeWaiting++
			if job.AbsoluteEpisode > 0 || (job.SceneSeason > 0 && job.SceneEpisode > 0) {
				summary.NumberingReady++
			}
			group := job.SeriesRootDirPath + "\x00" + strconv.Itoa(job.Season)
			seriesGroups[group]++
			if ready {
				readySeriesGroups[group]++
			}
		}
		message := strings.ToLower(job.ErrorInfo)
		if strings.Contains(message, "series metadata episode not found") || strings.Contains(message, "series metadata root not found") {
			summary.MetadataBlocked++
		}
	}
	summary.SeriesGroups = len(seriesGroups)
	for _, count := range readySeriesGroups {
		if count > 1 {
			summary.BatchableGroups++
		}
	}
	return summary
}

func filterJobs(jobs []taskQueueTypes.OneJob, query jobPageQuery) []taskQueueTypes.OneJob {
	filtered := make([]taskQueueTypes.OneJob, 0, len(jobs))
	search := strings.ToLower(query.Search)
	for _, job := range jobs {
		if query.Status != nil && int(job.JobStatus) != *query.Status {
			continue
		}
		if query.VideoType != nil && int(job.VideoType) != *query.VideoType {
			continue
		}
		if !matchesPriority(job.TaskPriority, query.Priority) {
			continue
		}
		if query.ErrorCategory != "" && string(task_queue.ClassifyErrorInfo(job.ErrorInfo)) != query.ErrorCategory {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(job.VideoName), search) &&
			!strings.Contains(strings.ToLower(job.VideoFPath), search) &&
			!strings.Contains(strings.ToLower(job.SeriesRootDirPath), search) &&
			!strings.Contains(strings.ToLower(job.MediaServerInsideVideoID), search) {
			continue
		}
		filtered = append(filtered, job)
	}
	return filtered
}

func matchesPriority(priority int, filter string) bool {
	if filter == "" {
		return true
	}
	switch filter {
	case "high":
		return priority <= 3
	case "middle":
		return priority >= 4 && priority <= 6
	case "low":
		return priority >= 7
	default:
		value, _ := strconv.Atoi(filter)
		return priority == value
	}
}

func sortJobs(jobs []taskQueueTypes.OneJob, field, order string) {
	sort.SliceStable(jobs, func(i, j int) bool {
		comparison := 0
		switch field {
		case "addedAt":
			comparison = compareTime(time.Time(jobs[i].AddedTime), time.Time(jobs[j].AddedTime))
		case "nextAttemptAt":
			comparison = compareTime(time.Time(jobs[i].NextAttemptTime), time.Time(jobs[j].NextAttemptTime))
		case "priority":
			comparison = jobs[i].TaskPriority - jobs[j].TaskPriority
		case "name":
			comparison = strings.Compare(strings.ToLower(jobs[i].VideoName), strings.ToLower(jobs[j].VideoName))
		default:
			comparison = compareTime(time.Time(jobs[i].UpdateTime), time.Time(jobs[j].UpdateTime))
		}
		if order == "desc" {
			return comparison > 0
		}
		return comparison < 0
	})
}

func compareTime(left, right time.Time) int {
	if left.Before(right) {
		return -1
	}
	if left.After(right) {
		return 1
	}
	return 0
}

func newJobView(job taskQueueTypes.OneJob, now time.Time) backendTypes.JobView {
	diagnostic := task_queue.DiagnoseRetry(job, now)
	aliases := make([]string, 0, 2)
	if job.SeriesName != "" {
		aliases = append(aliases, job.SeriesName)
	}
	if rootName := filepath.Base(filepath.Clean(job.SeriesRootDirPath)); rootName != "." && rootName != string(filepath.Separator) {
		if len(aliases) == 0 || !strings.EqualFold(aliases[0], rootName) {
			aliases = append(aliases, rootName)
		}
	}
	identity := episode_identity.Identity{
		Season: job.Season, Episode: job.Episode, AbsoluteEpisode: job.AbsoluteEpisode,
		SceneSeason: job.SceneSeason, SceneEpisode: job.SceneEpisode, Confidence: job.NumberingConfidence,
	}
	queryPlan := episode_identity.BuildSearchPlan(aliases, identity)
	queryViews := make([]backendTypes.JobQueryVariant, 0, len(queryPlan))
	for _, variant := range queryPlan {
		view := backendTypes.JobQueryVariant{Kind: string(variant.Kind), Query: variant.Query}
		switch variant.Kind {
		case episode_identity.QueryAired:
			view.Season, view.Episode = job.Season, job.Episode
		case episode_identity.QueryScene:
			view.Season, view.Episode = job.SceneSeason, job.SceneEpisode
		case episode_identity.QueryAbsolute:
			view.Absolute = job.AbsoluteEpisode
		}
		queryViews = append(queryViews, view)
	}
	return backendTypes.JobView{OneJob: job, Retry: backendTypes.JobRetryView{
		Category: string(diagnostic.Category), IsScheduled: diagnostic.IsScheduled, IsReady: diagnostic.IsReady,
		IsForced: diagnostic.IsForced, NextAttemptAt: diagnostic.NextAttemptAt, RetryInSeconds: diagnostic.RetryInSeconds,
	}, Identity: backendTypes.JobIdentityView{
		IsAnime:    job.VideoType == common.Anime || job.AbsoluteEpisode > 0,
		SeriesName: job.SeriesName, Aliases: aliases, Season: job.Season, Episode: job.Episode,
		AbsoluteEpisode: job.AbsoluteEpisode, SceneSeason: job.SceneSeason, SceneEpisode: job.SceneEpisode,
		NumberingSource: job.NumberingSource, NumberingConfidence: job.NumberingConfidence, QueryPlan: queryViews,
		SearchFingerprint: job.SearchFingerprint,
	}}
}
