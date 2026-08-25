package subtitle_metrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
