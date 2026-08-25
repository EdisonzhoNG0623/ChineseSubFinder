package backend

import (
	"time"

	taskQueueTypes "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"
)

type JobRetryView struct {
	Category       string    `json:"category"`
	IsScheduled    bool      `json:"is_scheduled"`
	IsReady        bool      `json:"is_ready"`
	IsForced       bool      `json:"is_forced"`
	NextAttemptAt  time.Time `json:"next_attempt_at,omitempty"`
	RetryInSeconds int64     `json:"retry_in_seconds"`
}

type JobView struct {
	taskQueueTypes.OneJob
	Retry    JobRetryView    `json:"retry"`
	Identity JobIdentityView `json:"identity"`
}

type JobQueryVariant struct {
	Kind     string `json:"kind"`
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
	Absolute int    `json:"absolute,omitempty"`
	Query    string `json:"query"`
}

type JobIdentityView struct {
	IsAnime             bool              `json:"is_anime"`
	SeriesName          string            `json:"series_name,omitempty"`
	Aliases             []string          `json:"aliases,omitempty"`
	Season              int               `json:"season,omitempty"`
	Episode             int               `json:"episode,omitempty"`
	AbsoluteEpisode     int               `json:"absolute_episode,omitempty"`
	SceneSeason         int               `json:"scene_season,omitempty"`
	SceneEpisode        int               `json:"scene_episode,omitempty"`
	NumberingSource     string            `json:"numbering_source,omitempty"`
	NumberingConfidence float64           `json:"numbering_confidence,omitempty"`
	QueryPlan           []JobQueryVariant `json:"query_plan,omitempty"`
}

type JobPagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

type QueueSummary struct {
	Total           int            `json:"total"`
	ByStatus        map[string]int `json:"by_status"`
	ByVideoType     map[string]int `json:"by_video_type"`
	ByErrorCategory map[string]int `json:"by_error_category"`
	RetryScheduled  int            `json:"retry_scheduled"`
	ReadyNow        int            `json:"ready_now"`
	WaitingSeries   int            `json:"waiting_series"`
	SeriesGroups    int            `json:"series_groups"`
	BatchableGroups int            `json:"batchable_groups"`
}

type ReplyJobPage struct {
	Data        []JobView     `json:"data"`
	Pagination  JobPagination `json:"pagination"`
	Summary     QueueSummary  `json:"summary"`
	GeneratedAt time.Time     `json:"generated_at"`
}
