package subtitle_metrics

import (
	"sync"
	"time"
)

type SupplierRuntime struct {
	Name          string    `json:"name"`
	Health        string    `json:"health"`
	LastCheckedAt time.Time `json:"last_checked_at,omitempty"`
	LatencyMillis int64     `json:"latency_millis"`
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
	Attempts      int64     `json:"attempts"`
	CandidateHits int64     `json:"candidate_hits"`
	EmptyResults  int64     `json:"empty_results"`
	Errors        int64     `json:"errors"`
	Candidates    int64     `json:"candidates"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastAttemptMs int64     `json:"last_attempt_millis"`
}

type registry struct {
	mu        sync.RWMutex
	suppliers map[string]SupplierRuntime
}

var processRegistry = registry{suppliers: make(map[string]SupplierRuntime)}

func RecordHealth(name, health string, latencyMillis int64, checkedAt, cooldownUntil time.Time) {
	processRegistry.mu.Lock()
	defer processRegistry.mu.Unlock()
	record := processRegistry.suppliers[name]
	record.Name = name
	record.Health = health
	record.LatencyMillis = latencyMillis
	record.LastCheckedAt = checkedAt
	record.CooldownUntil = cooldownUntil
	processRegistry.suppliers[name] = record
}

// RecordAttempt stores bounded aggregate data only. Media paths, URLs, errors,
// credentials and candidate titles must never enter this registry.
func RecordAttempt(name string, duration time.Duration, candidateCount int, err error) {
	processRegistry.mu.Lock()
	defer processRegistry.mu.Unlock()
	record := processRegistry.suppliers[name]
	record.Name = name
	record.Attempts++
	record.LastAttemptAt = time.Now()
	record.LastAttemptMs = duration.Milliseconds()
	if err != nil {
		record.Errors++
	} else if candidateCount == 0 {
		record.EmptyResults++
	} else {
		record.CandidateHits++
		record.Candidates += int64(candidateCount)
	}
	processRegistry.suppliers[name] = record
}

func Snapshot() map[string]SupplierRuntime {
	processRegistry.mu.RLock()
	defer processRegistry.mu.RUnlock()
	out := make(map[string]SupplierRuntime, len(processRegistry.suppliers))
	for name, record := range processRegistry.suppliers {
		out[name] = record
	}
	return out
}
