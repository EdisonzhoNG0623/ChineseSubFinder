package subtitle_metrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistryStoresOnlyAggregateOutcome(t *testing.T) {
	name := "test-aggregate"
	RecordAttempt(name, 25*time.Millisecond, 2, nil)
	RecordAttempt(name, 10*time.Millisecond, 0, nil)
	RecordAttempt(name, 5*time.Millisecond, 0, errors.New("secret-bearing error must not be stored"))
	got := Snapshot()[name]
	if got.Attempts != 3 || got.CandidateHits != 1 || got.EmptyResults != 1 || got.Errors != 1 || got.Candidates != 2 {
		t.Fatalf("unexpected runtime metrics: %+v", got)
	}
	if got.LastErrorCode != "UNKNOWN" || got.LastErrorAt.IsZero() {
		t.Fatalf("missing bounded last-error diagnostic: %+v", got)
	}
}

func TestRegistryRecordsPartialCandidatesAndFailure(t *testing.T) {
	name := "test-partial-candidates"
	for i := 0; i < 3; i++ {
		RecordAttemptForCohort(name, CohortSeries, 50*time.Millisecond, 3, context.DeadlineExceeded)
	}
	got := SnapshotForCohort(CohortSeries)[name]
	if got.Attempts != 3 || got.CandidateHits != 3 || got.Candidates != 9 || got.Errors != 3 || got.Timeouts != 3 {
		t.Fatalf("partial provider outcome lost useful or degraded evidence: %+v", got)
	}
	if got.ConsecutiveErrors != 0 || got.ConsecutiveTimeouts != 0 || !got.CircuitOpenUntil.IsZero() {
		t.Fatalf("usable partial outcomes opened the unavailability circuit: %+v", got)
	}
	if allowed, _ := ShouldAttempt(name, time.Now()); !allowed {
		t.Fatal("usable partial outcomes must not block the provider")
	}

	RecordAttemptForCohort(name, CohortSeries, 50*time.Millisecond, 0, errors.New("provider unavailable"))
	RecordAttemptForCohort(name, CohortSeries, 50*time.Millisecond, 0, errors.New("provider unavailable"))
	if allowed, _ := ShouldAttempt(name, time.Now()); !allowed {
		t.Fatal("two ordinary failures must remain below the circuit threshold")
	}
	RecordAttemptForCohort(name, CohortSeries, 50*time.Millisecond, 0, errors.New("provider unavailable"))
	if allowed, _ := ShouldAttempt(name, time.Now()); allowed {
		t.Fatal("three pure failures after partial success must open the circuit")
	}
}

func TestErrorClassificationIsBounded(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "TIMEOUT"},
		{errors.New("HTTP 429 rate limit for token secret"), "QUOTA"},
		{errors.New("download authorization returned HTTP 406"), "QUOTA"},
		{errors.New("authorization failed for api key secret"), "AUTH"},
		{errors.New("captcha verification forbidden"), "BLOCKED"},
		{errors.New("DNS connection refused"), "NETWORK"},
		{errors.New("provider returned status code 502 with private body"), "PROVIDER"},
		{errors.New("private unexpected text"), "UNKNOWN"},
	}
	for _, test := range tests {
		if got := classifyError(test.err); got != test.want {
			t.Fatalf("classifyError(%q) = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestRegistryTracksSelectionAndSaveConversion(t *testing.T) {
	name := "test-save-conversion"
	RecordSelection(name)
	RecordSelection(name)
	RecordSave(name)
	RecordCacheHit(name)
	RecordEarlyStop(name)
	got := Snapshot()[name]
	if got.Selections != 2 || got.Saves != 1 || got.CacheHits != 1 || got.EarlyStops != 1 || got.LastSaveAt.IsZero() {
		t.Fatalf("unexpected conversion metrics: %+v", got)
	}
}

func TestRegistryKeepsMediaCohortsIndependent(t *testing.T) {
	name := "test-cohort-isolation"
	RecordAttemptForCohort(name, CohortMovie, 25*time.Millisecond, 2, nil)
	RecordSelectionForCohort(name, CohortMovie)
	RecordSaveForCohort(name, CohortMovie)

	global := Snapshot()[name]
	movie := SnapshotForCohort(CohortMovie)[name]
	if global.Attempts != 1 || global.Saves != 1 || movie.Attempts != 1 || movie.Saves != 1 {
		t.Fatalf("cohort observation was not reflected in global and movie aggregates: global=%+v movie=%+v", global, movie)
	}
	if anime := SnapshotForCohort(CohortAnime)[name]; anime.Attempts != 0 || anime.Saves != 0 {
		t.Fatalf("movie observations leaked into anime cohort: %+v", anime)
	}
}

func TestRegistryTracksLatencyTimeoutAndCircuit(t *testing.T) {
	name := "test-timeout-circuit"
	RecordAttempt(name, 1500*time.Millisecond, 0, context.DeadlineExceeded)
	RecordAttempt(name, 2500*time.Millisecond, 0, context.DeadlineExceeded)
	got := Snapshot()[name]
	if got.Timeouts != 2 || got.AverageAttemptMillis() != 2000 || got.P95AttemptMillis() != 5000 {
		t.Fatalf("unexpected latency metrics: %+v", got)
	}
	if allowed, _ := ShouldAttempt(name, time.Now()); allowed {
		t.Fatal("expected circuit to be open after two consecutive timeouts")
	}

	RecordAttempt(name, 20*time.Millisecond, 1, nil)
	if allowed, _ := ShouldAttempt(name, time.Now()); !allowed {
		t.Fatal("expected successful attempt to close circuit")
	}
}

func TestRegistryPersistsAggregateState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supplier_metrics.json")
	if err := ConfigurePersistence(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigurePersistence("") }()

	name := "test-persisted"
	RecordAttempt(name, 25*time.Millisecond, 2, errors.New("secret-bearing error must not be stored"))
	if err := FlushPersistence(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, name) {
		t.Fatalf("persisted metrics missing supplier: %s", content)
	}
	if strings.Contains(content, "secret-bearing") {
		t.Fatalf("persisted metrics leaked error content: %s", content)
	}
}

func TestRegistryPersistsCohortsWithoutRawErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supplier_metrics_v2.json")
	if err := ConfigurePersistence(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigurePersistence("") }()

	name := "test-cohort-persisted"
	RecordAttemptForCohort(name, CohortAnime, 25*time.Millisecond, 0, errors.New("private anime title and token"))
	if err := FlushPersistence(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"version": 2`) || !strings.Contains(content, `"anime"`) {
		t.Fatalf("persisted state omitted versioned cohort aggregates: %s", content)
	}
	if strings.Contains(content, "private anime") || strings.Contains(content, "token") {
		t.Fatalf("persisted cohort metrics leaked raw error content: %s", content)
	}
}

func TestConfigurePersistenceLoadsVersionOneAsGlobalColdStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supplier_metrics_v1.json")
	const name = "test-version-one-provider"
	legacy := `{"version":1,"suppliers":{"` + name + `":{"name":"` + name + `","attempts":25,"candidate_hits":20,"saves":10}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePersistence(path); err != nil {
		t.Fatalf("version 1 state must remain loadable: %v", err)
	}
	defer func() { _ = ConfigurePersistence("") }()

	if got := Snapshot()[name]; got.Attempts != 25 || got.CandidateHits != 20 || got.Saves != 10 {
		t.Fatalf("legacy global aggregate was not restored: %+v", got)
	}
	if got := SnapshotForCohort(CohortMovie)[name]; got.Attempts != 0 {
		t.Fatalf("legacy global aggregate should remain a cold-start fallback, not be copied into cohorts: %+v", got)
	}
}

func TestFlushPersistenceSerializesSnapshotAndWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supplier_metrics.json")
	if err := ConfigurePersistence(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigurePersistence("") }()

	const name = "test-serialized-flush"
	processRegistry.mu.Lock()
	processRegistry.suppliers[name] = SupplierRuntime{Name: name, Attempts: 1}
	processRegistry.mu.Unlock()

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var writesMu sync.Mutex
	writes := make([]int64, 0, 2)
	call := 0
	writer := func(_ string, state persistedState) error {
		call++
		if call == 1 {
			close(firstEntered)
			<-releaseFirst
		}
		writesMu.Lock()
		writes = append(writes, state.Suppliers[name].Attempts)
		writesMu.Unlock()
		return nil
	}

	errorsCh := make(chan error, 2)
	go func() { errorsCh <- processRegistry.flushPersistence(writer) }()
	<-firstEntered

	processRegistry.mu.Lock()
	record := processRegistry.suppliers[name]
	record.Attempts = 2
	processRegistry.suppliers[name] = record
	processRegistry.mu.Unlock()
	go func() { errorsCh <- processRegistry.flushPersistence(writer) }()

	close(releaseFirst)
	for i := 0; i < 2; i++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	writesMu.Lock()
	defer writesMu.Unlock()
	if len(writes) != 2 || writes[0] != 1 || writes[1] != 2 {
		t.Fatalf("serialized flush order = %v, want [1 2]", writes)
	}
}

func TestPersistenceRetryRequeuesAQuietRegistry(t *testing.T) {
	retries := registry{persistPath: filepath.Join(t.TempDir(), "supplier_metrics.json"), persistCh: make(chan struct{}, 1)}
	time.AfterFunc(time.Millisecond, retries.schedulePersistence)
	select {
	case <-retries.persistCh:
	case <-time.After(time.Second):
		t.Fatal("persistence retry did not requeue a flush")
	}
}
