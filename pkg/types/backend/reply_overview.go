package backend

import "time"

type ScheduleView struct {
	Status        string           `json:"status"`
	Phase         string           `json:"phase"`
	NextScanAt    time.Time        `json:"next_scan_at,omitempty"`
	NextRetryAt   *time.Time       `json:"next_retry_at,omitempty"`
	NextActionAt  *time.Time       `json:"next_action_at,omitempty"`
	LastCycleAt   *time.Time       `json:"last_cycle_at,omitempty"`
	LastSuccessAt *time.Time       `json:"last_success_at,omitempty"`
	ActiveWorkers int              `json:"active_workers"`
	CurrentJobs   []CurrentJobView `json:"current_jobs"`
}

// CurrentJobView is intentionally bounded and excludes media paths and error
// bodies. The queue remains the source of truth for full job diagnostics.
type CurrentJobView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	VideoType string    `json:"video_type"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OutcomeCount struct {
	WhichDay  string `json:"which_day"`
	VideoType string `json:"video_type"`
	Outcome   string `json:"outcome"`
	Count     int    `json:"count"`
}

type AIRuntimeView struct {
	Enabled       bool      `json:"enabled"`
	Configured    bool      `json:"configured"`
	Attempts      int64     `json:"attempts"`
	Matches       int64     `json:"matches"`
	NoMatches     int64     `json:"no_matches"`
	Abstentions   int64     `json:"abstentions"`
	Errors        int64     `json:"errors"`
	LastLatencyMs int64     `json:"last_latency_millis"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
}

type ReplyOverview struct {
	Queue       QueueSummary         `json:"queue"`
	Schedule    ScheduleView         `json:"schedule"`
	Suppliers   []SupplierDiagnostic `json:"suppliers"`
	Outcomes    []OutcomeCount       `json:"outcomes"`
	AI          AIRuntimeView        `json:"ai"`
	GeneratedAt time.Time            `json:"generated_at"`
}

type ReplyAIStatus struct {
	Enabled        bool          `json:"enabled"`
	Configured     bool          `json:"configured"`
	HasAPIKey      bool          `json:"has_api_key"`
	BaseURL        string        `json:"base_url"`
	Model          string        `json:"model"`
	MinConfidence  float64       `json:"min_confidence"`
	TimeoutSeconds int           `json:"timeout_seconds"`
	Runtime        AIRuntimeView `json:"runtime"`
}
