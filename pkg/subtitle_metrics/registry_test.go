package subtitle_metrics

import (
	"errors"
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
