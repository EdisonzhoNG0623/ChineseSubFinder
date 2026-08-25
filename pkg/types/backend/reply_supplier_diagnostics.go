package backend

import "time"

type SupplierDiagnostic struct {
	Name              string    `json:"name"`
	DisplayName       string    `json:"display_name"`
	RootURL           string    `json:"root_url"`
	DefaultRootURL    string    `json:"default_root_url"`
	Enabled           bool      `json:"enabled"`
	Configured        bool      `json:"configured"`
	Health            string    `json:"health"`
	StatusMessage     string    `json:"status_message"`
	Capabilities      []string  `json:"capabilities"`
	DailyLimit        int       `json:"daily_limit"`
	DailyUsed         int       `json:"daily_used"`
	LastCheckedAt     time.Time `json:"last_checked_at,omitempty"`
	LatencyMillis     int64     `json:"latency_millis"`
	CooldownUntil     time.Time `json:"cooldown_until,omitempty"`
	Attempts          int64     `json:"attempts"`
	CandidateHits     int64     `json:"candidate_hits"`
	EmptyResults      int64     `json:"empty_results"`
	Errors            int64     `json:"errors"`
	Candidates        int64     `json:"candidates"`
	LastAttemptAt     time.Time `json:"last_attempt_at,omitempty"`
	LastAttemptMillis int64     `json:"last_attempt_millis"`
	AverageAttemptMs  int64     `json:"average_attempt_millis"`
	P95AttemptMs      int64     `json:"p95_attempt_millis"`
	Timeouts          int64     `json:"timeouts"`
	CircuitSkips      int64     `json:"circuit_skips"`
	CircuitOpenUntil  time.Time `json:"circuit_open_until,omitempty"`
}

type ReplySupplierDiagnostics struct {
	Data        []SupplierDiagnostic `json:"data"`
	IsChecking  bool                 `json:"is_checking"`
	GeneratedAt time.Time            `json:"generated_at"`
}
