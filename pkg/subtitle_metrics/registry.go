package subtitle_metrics

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var attemptBucketUpperMillis = [...]int64{1000, 5000, 15000, 60000, 300000}

const persistenceRetryDelay = 30 * time.Second

// MediaCohort is a bounded label used to keep routing observations for movies,
// episodic series and anime independent. Unknown values intentionally collapse
// to the global bucket so callers cannot create unbounded metric cardinality.
type MediaCohort string

const (
	CohortUnknown MediaCohort = ""
	CohortMovie   MediaCohort = "movie"
	CohortSeries  MediaCohort = "series"
	CohortAnime   MediaCohort = "anime"
)

func NormalizeCohort(cohort MediaCohort) MediaCohort {
	switch MediaCohort(strings.ToLower(strings.TrimSpace(string(cohort)))) {
	case CohortMovie:
		return CohortMovie
	case CohortSeries:
		return CohortSeries
	case CohortAnime:
		return CohortAnime
	default:
		return CohortUnknown
	}
}

// Label returns the fixed, non-sensitive value used in production logs.
func (c MediaCohort) Label() string {
	if normalized := NormalizeCohort(c); normalized != CohortUnknown {
		return string(normalized)
	}
	return "global"
}

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
	LastErrorAt         time.Time `json:"last_error_at,omitempty"`
	LastErrorCode       string    `json:"last_error_code,omitempty"`
	LastAttemptMs       int64     `json:"last_attempt_millis"`
	TotalAttemptMs      int64     `json:"total_attempt_millis"`
	MaxAttemptMs        int64     `json:"max_attempt_millis"`
	AttemptBuckets      [6]int64  `json:"attempt_buckets"`
	Timeouts            int64     `json:"timeouts"`
	CircuitSkips        int64     `json:"circuit_skips"`
	Selections          int64     `json:"selections"`
	Saves               int64     `json:"saves"`
	LastSaveAt          time.Time `json:"last_save_at,omitempty"`
	CacheHits           int64     `json:"cache_hits"`
	EarlyStops          int64     `json:"early_stops"`
	ConsecutiveErrors   int64     `json:"consecutive_errors"`
	ConsecutiveTimeouts int64     `json:"consecutive_timeouts"`
	CircuitOpenUntil    time.Time `json:"circuit_open_until,omitempty"`
}

func RecordSelection(name string) {
	RecordSelectionForCohort(name, CohortUnknown)
}

func RecordSelectionForCohort(name string, cohort MediaCohort) {
	recordCounterForCohort(name, cohort, func(record *SupplierRuntime) { record.Selections++ })
}

func RecordSave(name string) {
	RecordSaveForCohort(name, CohortUnknown)
}

func RecordSaveForCohort(name string, cohort MediaCohort) {
	now := time.Now()
	recordCounterForCohort(name, cohort, func(record *SupplierRuntime) {
		record.Saves++
		record.LastSaveAt = now
	})
}

func RecordCacheHit(name string) {
	recordCounter(name, func(record *SupplierRuntime) { record.CacheHits++ })
}

func RecordEarlyStop(name string) {
	RecordEarlyStopForCohort(name, CohortUnknown)
}

func RecordEarlyStopForCohort(name string, cohort MediaCohort) {
	recordCounterForCohort(name, cohort, func(record *SupplierRuntime) { record.EarlyStops++ })
}

func recordCounter(name string, update func(*SupplierRuntime)) {
	recordCounterForCohort(name, CohortUnknown, update)
}

func recordCounterForCohort(name string, cohort MediaCohort, update func(*SupplierRuntime)) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	cohort = NormalizeCohort(cohort)
	processRegistry.mu.Lock()
	record := processRegistry.suppliers[name]
	record.Name = name
	update(&record)
	processRegistry.suppliers[name] = record
	if cohort != CohortUnknown {
		cohortSuppliers := processRegistry.cohortSuppliersLocked(cohort)
		cohortRecord := cohortSuppliers[name]
		cohortRecord.Name = name
		update(&cohortRecord)
		cohortSuppliers[name] = cohortRecord
	}
	processRegistry.mu.Unlock()
	processRegistry.schedulePersistence()
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
	cohorts   map[MediaCohort]map[string]SupplierRuntime

	flushMu     sync.Mutex
	persistMu   sync.RWMutex
	persistPath string
	persistCh   chan struct{}
	workerOnce  sync.Once

	persistErrorMu      sync.Mutex
	lastPersistErrorLog time.Time
}

type persistedState struct {
	Version   int                                   `json:"version"`
	Suppliers map[string]SupplierRuntime            `json:"suppliers"`
	Cohorts   map[string]map[string]SupplierRuntime `json:"cohorts,omitempty"`
}

var processRegistry = registry{
	suppliers: make(map[string]SupplierRuntime),
	cohorts:   make(map[MediaCohort]map[string]SupplierRuntime),
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
		if state.Version != 1 && state.Version != 2 {
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
		if state.Version >= 2 {
			for rawCohort, suppliers := range state.Cohorts {
				cohort := NormalizeCohort(MediaCohort(rawCohort))
				if cohort == CohortUnknown {
					continue
				}
				cohortSuppliers := processRegistry.cohortSuppliersLocked(cohort)
				for name, record := range suppliers {
					if strings.TrimSpace(name) == "" {
						continue
					}
					record.Name = name
					cohortSuppliers[name] = record
				}
			}
		}
		processRegistry.mu.Unlock()
	}

	return nil
}

func FlushPersistence() error {
	return processRegistry.flushPersistence(writeSnapshot)
}

func (r *registry) flushPersistence(writer func(string, persistedState) error) error {
	// Keep the snapshot and its atomic replacement in one critical section. A
	// slower worker flush must not overwrite a newer explicit shutdown flush.
	r.flushMu.Lock()
	defer r.flushMu.Unlock()

	processRegistry.persistMu.RLock()
	path := processRegistry.persistPath
	processRegistry.persistMu.RUnlock()
	if path == "" {
		return nil
	}
	return writer(path, snapshotPersistedState())
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
	RecordAttemptForCohort(name, CohortUnknown, duration, candidateCount, err)
}

// RecordAttemptForCohort updates both the legacy global aggregate and one
// bounded media cohort. It stores counts, a latency histogram and an allowlisted
// error code only; media names, paths, URLs and raw errors are never retained.
func RecordAttemptForCohort(name string, cohort MediaCohort, duration time.Duration, candidateCount int, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	cohort = NormalizeCohort(cohort)
	now := time.Now()
	durationMillis := duration.Milliseconds()
	if durationMillis < 0 {
		durationMillis = 0
	}

	processRegistry.mu.Lock()
	record := processRegistry.suppliers[name]
	updateAttempt(&record, name, now, durationMillis, candidateCount, err)
	processRegistry.suppliers[name] = record
	if cohort != CohortUnknown {
		cohortSuppliers := processRegistry.cohortSuppliersLocked(cohort)
		cohortRecord := cohortSuppliers[name]
		updateAttempt(&cohortRecord, name, now, durationMillis, candidateCount, err)
		cohortSuppliers[name] = cohortRecord
	}
	processRegistry.mu.Unlock()
	processRegistry.schedulePersistence()
}

func updateAttempt(record *SupplierRuntime, name string, now time.Time, durationMillis int64, candidateCount int, err error) {
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
		record.LastErrorAt = now
		record.LastErrorCode = classifyError(err)
		// Some providers return usable partial candidates together with a
		// degraded/search-continuation error. Keep both signals: routing should
		// reward the useful result while still applying the failure penalty.
		if candidateCount > 0 {
			record.CandidateHits++
			record.Candidates += int64(candidateCount)
			if isTimeout(err) {
				record.Timeouts++
			}
			// Usable candidates prove that the provider is still available. Keep
			// the degraded/error evidence for routing, but do not let repeated
			// partial success open the unavailability circuit.
			record.ConsecutiveErrors = 0
			record.ConsecutiveTimeouts = 0
			record.CircuitOpenUntil = time.Time{}
			return
		}
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
		return
	}

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

// SnapshotForCohort returns a copy of one bounded cohort. It deliberately does
// not merge global observations so routing can decide explicitly when a cold
// start fallback is appropriate.
func SnapshotForCohort(cohort MediaCohort) map[string]SupplierRuntime {
	cohort = NormalizeCohort(cohort)
	if cohort == CohortUnknown {
		return Snapshot()
	}
	processRegistry.mu.RLock()
	defer processRegistry.mu.RUnlock()
	suppliers := processRegistry.cohorts[cohort]
	out := make(map[string]SupplierRuntime, len(suppliers))
	for name, record := range suppliers {
		out[name] = record
	}
	return out
}

func (r *registry) cohortSuppliersLocked(cohort MediaCohort) map[string]SupplierRuntime {
	if r.cohorts == nil {
		r.cohorts = make(map[MediaCohort]map[string]SupplierRuntime)
	}
	suppliers := r.cohorts[cohort]
	if suppliers == nil {
		suppliers = make(map[string]SupplierRuntime)
		r.cohorts[cohort] = suppliers
	}
	return suppliers
}

func snapshotPersistedState() persistedState {
	processRegistry.mu.RLock()
	defer processRegistry.mu.RUnlock()
	state := persistedState{
		Version:   2,
		Suppliers: make(map[string]SupplierRuntime, len(processRegistry.suppliers)),
		Cohorts:   make(map[string]map[string]SupplierRuntime, len(processRegistry.cohorts)),
	}
	for name, record := range processRegistry.suppliers {
		state.Suppliers[name] = record
	}
	for cohort, suppliers := range processRegistry.cohorts {
		if normalized := NormalizeCohort(cohort); normalized == CohortUnknown {
			continue
		}
		copyOfSuppliers := make(map[string]SupplierRuntime, len(suppliers))
		for name, record := range suppliers {
			copyOfSuppliers[name] = record
		}
		state.Cohorts[string(cohort)] = copyOfSuppliers
	}
	return state
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

// classifyError converts an arbitrary provider error into a small, stable
// allowlist. Raw error text can contain credentials, URLs, media names, or
// provider response bodies and must never be retained by the metrics store.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if isTimeout(err) {
		return "TIMEOUT"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "quota"), strings.Contains(message, "rate limit"),
		strings.Contains(message, "too many requests"), strings.Contains(message, "http 429"),
		strings.Contains(message, "status code 429"), strings.Contains(message, "http 406"),
		strings.Contains(message, "status code 406"):
		return "QUOTA"
	case strings.Contains(message, "authorization"), strings.Contains(message, "unauthorized"),
		strings.Contains(message, "authentication"), strings.Contains(message, "api key"),
		strings.Contains(message, "http 401"), strings.Contains(message, "status code 401"):
		return "AUTH"
	case strings.Contains(message, "captcha"), strings.Contains(message, "verification"),
		strings.Contains(message, "cloudflare"), strings.Contains(message, "forbidden"),
		strings.Contains(message, "http 403"), strings.Contains(message, "status code 403"):
		return "BLOCKED"
	case strings.Contains(message, "connection"), strings.Contains(message, "network"),
		strings.Contains(message, "dns"), strings.Contains(message, "no such host"),
		strings.Contains(message, "connection reset"), strings.Contains(message, "connection refused"):
		return "NETWORK"
	case strings.Contains(message, "invalid response"), strings.Contains(message, "status code"),
		strings.Contains(message, "http 5"):
		return "PROVIDER"
	default:
		return "UNKNOWN"
	}
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
				if err := FlushPersistence(); err != nil {
					r.reportPersistenceError(err)
					// A quiet process may not emit another metric after a transient
					// filesystem failure. Requeue one coalesced flush so the latest
					// aggregate snapshot is eventually repaired without a hot loop.
					time.AfterFunc(persistenceRetryDelay, r.schedulePersistence)
				}
				break
			}
			break
		}
	}
}

func (r *registry) reportPersistenceError(err error) {
	if err == nil {
		return
	}
	now := time.Now()
	r.persistErrorMu.Lock()
	defer r.persistErrorMu.Unlock()
	if !r.lastPersistErrorLog.IsZero() && now.Sub(r.lastPersistErrorLog) < 5*time.Minute {
		return
	}
	r.lastPersistErrorLog = now
	log.Printf("supplier metrics persistence failed: %v", err)
}

func writeSnapshot(path string, state persistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
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
