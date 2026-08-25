package supplier_search

import (
	"context"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type searchSupplier struct {
	name    string
	started chan<- string
	release <-chan struct{}
	items   []supplier.SubInfo
}

func (s *searchSupplier) CheckAlive() (bool, int64)    { return true, 0 }
func (s *searchSupplier) IsAlive() bool                { return true }
func (s *searchSupplier) GetSupplierName() string      { return s.name }
func (s *searchSupplier) OverDailyDownloadLimit() bool { return false }
func (s *searchSupplier) GetLogger() *logrus.Logger    { return log_helper.GetLogger4Tester() }
func (s *searchSupplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	if s.started != nil {
		s.started <- s.name
	}
	if s.release != nil {
		<-s.release
	}
	return s.items, nil
}
func (s *searchSupplier) GetSubListFromFile4Series(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return nil, nil
}
func (s *searchSupplier) GetSubListFromFile4Anime(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return nil, nil
}

func movieQuery(source ifaces.ISupplier) ([]supplier.SubInfo, error) {
	return source.GetSubListFromFile4Movie("movie.mkv")
}

func TestRunSkipsSlowTierAfterStrongFastMatch(t *testing.T) {
	slowStarted := make(chan string, 1)
	sources := []ifaces.ISupplier{
		&searchSupplier{name: "xunlei", items: []supplier.SubInfo{{Name: "strong"}}},
		&searchSupplier{name: "assrt", started: slowStarted, items: []supplier.SubInfo{{Name: "slow"}}},
	}
	got, err := Run(context.Background(), log_helper.GetLogger4Tester(), sources, "test-tier", func(items []supplier.SubInfo) bool {
		return len(items) > 0
	}, movieQuery)
	if err != nil || len(got) != 1 || got[0].Name != "strong" {
		t.Fatalf("unexpected result: items=%+v err=%v", got, err)
	}
	select {
	case name := <-slowStarted:
		t.Fatalf("slow tier unexpectedly started: %s", name)
	default:
	}
}

func TestRunUsesSlowTierWhenFastMatchIsInsufficient(t *testing.T) {
	slowStarted := make(chan string, 1)
	sources := []ifaces.ISupplier{
		&searchSupplier{name: "xunlei", items: []supplier.SubInfo{{Name: "weak"}}},
		&searchSupplier{name: "assrt", started: slowStarted, items: []supplier.SubInfo{{Name: "fallback"}}},
	}
	got, err := Run(context.Background(), log_helper.GetLogger4Tester(), sources, "test-fallback", func([]supplier.SubInfo) bool {
		return false
	}, movieQuery)
	if err != nil || len(got) != 2 {
		t.Fatalf("unexpected fallback result: items=%+v err=%v", got, err)
	}
	select {
	case name := <-slowStarted:
		if name != "assrt" {
			t.Fatalf("unexpected slow supplier: %s", name)
		}
	default:
		t.Fatal("slow tier was not called")
	}
}

func TestRunReturnsOnParentDeadline(t *testing.T) {
	started := make(chan string, 1)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	_, err := Run(ctx, log_helper.GetLogger4Tester(), []ifaces.ISupplier{
		&searchSupplier{name: "test-deadline", started: started, release: release},
	}, "test-deadline", nil, movieQuery)
	close(release)
	if err == nil || time.Since(startedAt) > time.Second {
		t.Fatalf("deadline was not enforced promptly: elapsed=%s err=%v", time.Since(startedAt), err)
	}
}

func TestRunSkipsOpenCircuit(t *testing.T) {
	name := "test-open-circuit"
	subtitle_metrics.RecordAttempt(name, time.Second, 0, context.DeadlineExceeded)
	subtitle_metrics.RecordAttempt(name, time.Second, 0, context.DeadlineExceeded)
	started := make(chan string, 1)
	_, err := Run(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
		&searchSupplier{name: name, started: started},
	}, "test-circuit", nil, movieQuery)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("supplier with open circuit was called")
	default:
	}
}

func TestTimeoutForSupplierTier(t *testing.T) {
	tests := []struct {
		name, phase string
		want        time.Duration
	}{
		{name: "xunlei", phase: "fast", want: 20 * time.Second},
		{name: "assrt", phase: "slow", want: 180 * time.Second},
		{name: "subtitle_best", phase: "slow", want: 30 * time.Second},
		{name: "subhd", phase: "slow", want: 60 * time.Second},
	}
	for _, test := range tests {
		if got := timeoutFor(test.name, test.phase); got != test.want {
			t.Errorf("timeoutFor(%q, %q) = %s, want %s", test.name, test.phase, got, test.want)
		}
	}
}
