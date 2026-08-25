package subtitle_metrics

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var attemptBucketUpperMillis = [...]int64{1000, 5000, 15000, 60000, 300000}

type SupplierRuntime struct {
	Name                string    `json:"name"`
	Health              string    `json:"health"`
	LastCheckedAt       time.Time `json:"last_checked_at,omitempty"`
	LatencyMillis       int64     `json:"latency_millis"`
	CooldownUntil       time.Time `json:"cooldown_until,omitempty"`
	Attempts            int64     `json:"attempts"`
	CandidateHits       int64     `json:"candidate_hits"`
	EmptyResults        int64     `json:"empty_results"`
	Errors              int64     `json:"errors"`
	Candidates          int64     `json:"candidates"`
	LastAttemptAt       time.Time `json:"last_attempt_at,omitempty"`
	LastAttemptMs       int64     `json:"last_attempt_millis"`
	TotalAttemptMs      int64     `json:"total_attempt_millis"`
	MaxAttemptMs        int64     `json:"max_attempt_millis"`
	AttemptBuckets      [6]int64  `json:"attempt_buckets"`
	Timeouts            int64     `json:"timeouts"`
	CircuitSkips        int64     `json:"circuit_skips"`
	ConsecutiveErrors   int64     `json:"consecutive_errors"`
	ConsecutiveTimeouts int64     `json:"consecutive_timeouts"`
	CircuitOpenUntil    time.Time `json:"circuit_open_until,omitempty"`
}

func (s SupplierRuntime) AverageAttemptMillis() int64 {
	if s.Attempts == 0 {
		return 0
	}
	return s.TotalAttemptMs / s.Attempts
}

// P95AttemptMillis is a bounded histogram estimate. It deliberately avoids
// retaining individual requests, media names, URLs, or errors.
func (s SupplierRuntime) P95AttemptMillis() int64 {
	if s.Attempts == 0 {
		return 0
	}
	target := (s.Attempts*95 + 99) / 100
	var seen int64
	for index, count := range s.AttemptBuckets {
		seen += count
		if seen < target {
			continue
		}
		if index < len(attemptBucketUpperMillis) {
			return attemptBucketUpperMillis[index]
		}
		return s.MaxAttemptMs
	}
	return s.MaxAttemptMs
}

type registry struct {
	mu        sync.RWMutex
	suppliers map[string]SupplierRuntime

	persistMu   sync.RWMutex
	persistPath string
	persistCh   chan struct{}
	workerOnce  sync.Once
}

type persistedState struct {
	Version   int                        `json:"version"`
	Suppliers map[string]SupplierRuntime `json:"suppliers"`
}

var processRegistry = registry{
	suppliers: make(map[string]SupplierRuntime),
	persistCh: make(chan struct{}, 1),
}

// ConfigurePersistence loads and enables persistence for bounded aggregate
// supplier metrics. Passing an empty path disables future writes.
func ConfigurePersistence(path string) error {
	processRegistry.persistMu.Lock()
	processRegistry.persistPath = path
	processRegistry.persistMu.Unlock()
	if path == "" {
		return nil
	}
	processRegistry.workerOnce.Do(func() { go processRegistry.persistenceWorker() })

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		var state persistedState
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
		if state.Version != 1 {
			return errors.New("unsupported supplier metrics version")
		}
		processRegistry.mu.Lock()
		for name, record := range state.Suppliers {
			if strings.TrimSpace(name) == "" {
				continue
			}
			record.Name = name
			processRegistry.suppliers[name] = record
		}
		processRegistry.mu.Unlock()
	}

	return nil
}

func FlushPersistence() error {
	processRegistry.persistMu.RLock()
	path := processRegistry.persistPath
	processRegistry.persistMu.RUnlock()
	if path == "" {
		return nil
	}
	return writeSnapshot(path, Snapshot())
}

func RecordHealth(name, health string, latencyMillis int64, checkedAt, cooldownUntil time.Time) {
	processRegistry.mu.Lock()
	record := processRegistry.suppliers[name]
	record.Name = name
	record.Health = health
	record.LatencyMillis = latencyMillis
	record.LastCheckedAt = checkedAt
	record.CooldownUntil = cooldownUntil
	processRegistry.suppliers[name] = record
	processRegistry.mu.Unlock()
	processRegistry.schedulePersistence()
}

// RecordAttempt stores bounded aggregate data only. Media paths, URLs, errors,
// credentials and candidate titles must never enter this registry.
func RecordAttempt(name string, duration time.Duration, candidateCount int, err error) {
	now := time.Now()
	durationMillis := duration.Milliseconds()
	if durationMillis < 0 {
		durationMillis = 0
	}

	processRegistry.mu.Lock()
	record := processRegistry.suppliers[name]
	record.Name = name
	record.Attempts++
	record.LastAttemptAt = now
	record.LastAttemptMs = durationMillis
	record.TotalAttemptMs += durationMillis
	if durationMillis > record.MaxAttemptMs {
		record.MaxAttemptMs = durationMillis
	}
	record.AttemptBuckets[attemptBucket(durationMillis)]++
	if err != nil {
		record.Errors++
		record.ConsecutiveErrors++
		if isTimeout(err) {
			record.Timeouts++
			record.ConsecutiveTimeouts++
		} else {
			record.ConsecutiveTimeouts = 0
		}
		if record.ConsecutiveErrors >= 3 || record.ConsecutiveTimeouts >= 2 {
			record.CircuitOpenUntil = now.Add(circuitCooldown(name))
		}
	} else {
		record.ConsecutiveErrors = 0
		record.ConsecutiveTimeouts = 0
		record.CircuitOpenUntil = time.Time{}
		if candidateCount == 0 {
			record.EmptyResults++
		} else {
			record.CandidateHits++
			record.Candidates += int64(candidateCount)
		}
	}
	processRegistry.suppliers[name] = record
	processRegistry.mu.Unlock()
	processRegistry.schedulePersistence()
}

func ShouldAttempt(name string, now time.Time) (bool, time.Time) {
	processRegistry.mu.RLock()
	defer processRegistry.mu.RUnlock()
	record, ok := processRegistry.suppliers[name]
	if !ok || record.CircuitOpenUntil.IsZero() || !now.Before(record.CircuitOpenUntil) {
		return true, time.Time{}
	}
	return false, record.CircuitOpenUntil
}

func RecordCircuitSkip(name string) {
	processRegistry.mu.Lock()
	record := processRegistry.suppliers[name]
	record.Name = name
	record.CircuitSkips++
	processRegistry.suppliers[name] = record
	processRegistry.mu.Unlock()
	processRegistry.schedulePersistence()
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

func attemptBucket(durationMillis int64) int {
	for index, upper := range attemptBucketUpperMillis {
		if durationMillis <= upper {
			return index
		}
	}
	return len(attemptBucketUpperMillis)
}

func isTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func circuitCooldown(name string) time.Duration {
	switch strings.ToLower(name) {
	case "assrt", "subhd", "zimuku", "subtitle_best", "subtitlebest":
		return 15 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func (r *registry) schedulePersistence() {
	r.persistMu.RLock()
	enabled := r.persistPath != ""
	r.persistMu.RUnlock()
	if !enabled {
		return
	}
	select {
	case r.persistCh <- struct{}{}:
	default:
	}
}

func (r *registry) persistenceWorker() {
	for range r.persistCh {
		timer := time.NewTimer(500 * time.Millisecond)
		<-timer.C
		for {
			select {
			case <-r.persistCh:
				continue
			default:
				_ = FlushPersistence()
				break
			}
			break
		}
	}
}

func writeSnapshot(path string, suppliers map[string]SupplierRuntime) error {
	data, err := json.MarshalIndent(persistedState{Version: 1, Suppliers: suppliers}, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".supplier-metrics-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
