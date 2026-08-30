package supplier_search

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	err     error
	limited bool
	retryAt time.Time
}

func (s *searchSupplier) CheckAlive() (bool, int64)    { return true, 0 }
func (s *searchSupplier) IsAlive() bool                { return true }
func (s *searchSupplier) GetSupplierName() string      { return s.name }
func (s *searchSupplier) OverDailyDownloadLimit() bool { return s.limited }
func (s *searchSupplier) RetryAtTime() time.Time       { return s.retryAt }
func (s *searchSupplier) GetLogger() *logrus.Logger    { return log_helper.GetLogger4Tester() }
func (s *searchSupplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	if s.started != nil {
		s.started <- s.name
	}
	if s.release != nil {
		<-s.release
	}
	return s.items, s.err
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

func TestProviderCleanupWaitsForLegacyCallWithoutBlockingNewCalls(t *testing.T) {
	const name = "test-cleanup-barrier"
	providerLimiters.Delete(name)
	release := make(chan struct{})
	queryStarted := make(chan struct{})
	outcomes := make(chan supplierOutcome, 1)
	go runSupplierWithBudget(context.Background(), log_helper.GetLogger4Tester(), &searchSupplier{name: name},
		"cleanup-barrier", "fast", subtitle_metrics.CohortMovie,
		func(context.Context, ifaces.ISupplier) ([]supplier.SubInfo, error) {
			close(queryStarted)
			<-release
			return nil, nil
		}, outcomes, 20*time.Millisecond)
	<-queryStarted
	outcome := <-outcomes
	if outcome.report.Outcome != ProviderOutcomeTimeout {
		t.Fatalf("provider outcome = %+v", outcome.report)
	}

	cleanupEntered := false
	if TryWithSharedResourcesIdle(func() { cleanupEntered = true }) || cleanupEntered {
		t.Fatal("cleanup overlapped a timed-out legacy provider call")
	}

	// A pending cleanup does not install a waiting writer that starves later
	// searches while the legacy call remains stuck.
	secondFinished := BeginSharedResourceUse()
	secondFinished()
	if TryWithSharedResourcesIdle(nil) {
		t.Fatal("stuck legacy call disappeared from the active registry")
	}

	close(release)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !cleanupEntered {
		if TryWithSharedResourcesIdle(func() { cleanupEntered = true }) {
			break
		}
		select {
		case <-SharedResourcesIdleChan():
		case <-deadline.C:
			t.Fatal("cleanup did not run after provider completion")
		}
	}
}

func TestSharedResourceIdleSignalBroadcastsToAllWaiters(t *testing.T) {
	finish := BeginSharedResourceUse()
	first := SharedResourcesIdleChan()
	second := SharedResourcesIdleChan()
	if first != second {
		t.Fatal("waiters observed different active-use generations")
	}
	finish()
	for index, idle := range []<-chan struct{}{first, second} {
		select {
		case <-idle:
		default:
			t.Fatalf("idle signal did not wake waiter %d", index)
		}
	}
}

func TestExpiredBudgetDoesNotStartProviderAfterCleanupBoundary(t *testing.T) {
	cleanupEntered := make(chan struct{})
	releaseCleanup := make(chan struct{})
	cleanupDone := make(chan struct{})
	go func() {
		TryWithSharedResourcesIdle(func() {
			close(cleanupEntered)
			<-releaseCleanup
		})
		close(cleanupDone)
	}()
	<-cleanupEntered

	queryCalled := make(chan struct{}, 1)
	outcomes := make(chan supplierOutcome, 1)
	go runSupplierWithBudget(context.Background(), log_helper.GetLogger4Tester(), &searchSupplier{name: "expired-during-cleanup"},
		"expired-during-cleanup", "fast", subtitle_metrics.CohortMovie,
		func(context.Context, ifaces.ISupplier) ([]supplier.SubInfo, error) {
			queryCalled <- struct{}{}
			return nil, nil
		}, outcomes, 20*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	close(releaseCleanup)
	<-cleanupDone
	outcome := <-outcomes
	select {
	case <-queryCalled:
		t.Fatal("provider query started after its budget expired behind cleanup")
	default:
	}
	if outcome.report.Outcome != ProviderOutcomeTimeout || !errors.Is(outcome.report.Err, context.DeadlineExceeded) {
		t.Fatalf("expired provider outcome = %+v", outcome.report)
	}
}

func TestParentCancellationNeverCountsProviderFailure(t *testing.T) {
	const name = "test-parent-cancel-metrics"
	providerLimiters.Delete(name)
	for iteration := 0; iteration < 20; iteration++ {
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		release := make(chan struct{})
		outcomes := make(chan supplierOutcome, 1)
		go runSupplierWithBudget(ctx, log_helper.GetLogger4Tester(), &searchSupplier{name: name},
			"parent-cancel", "fast", subtitle_metrics.CohortMovie,
			func(context.Context, ifaces.ISupplier) ([]supplier.SubInfo, error) {
				close(started)
				<-release
				return nil, context.Canceled
			}, outcomes, time.Second)
		<-started
		cancel()
		close(release)
		if outcome := <-outcomes; outcome.report.Outcome != ProviderOutcomeCanceled {
			t.Fatalf("iteration %d outcome = %+v", iteration, outcome.report)
		}
	}
	if got := subtitle_metrics.Snapshot()[name]; got.Attempts != 0 || got.Errors != 0 || got.Timeouts != 0 {
		t.Fatalf("parent cancellation degraded provider metrics: %+v", got)
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

func TestReportKeepsOpenCircuitEvidence(t *testing.T) {
	name := "test-report-open-circuit"
	subtitle_metrics.RecordAttempt(name, time.Second, 0, context.DeadlineExceeded)
	subtitle_metrics.RecordAttempt(name, time.Second, 0, context.DeadlineExceeded)
	report := RunContextWithReport(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
		&searchSupplier{name: name},
	}, "test-report-circuit", nil, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
		return movieQuery(source)
	})
	var searchErr *SearchError
	if len(report.Providers) != 1 || report.Providers[0].Outcome != ProviderOutcomeCircuitOpen ||
		!errors.As(report.OutcomeError(), &searchErr) || searchErr.Kind != FailureTransient {
		t.Fatalf("open-circuit evidence was lost: %+v err=%v", report, report.OutcomeError())
	}
}

func TestTimeoutForSupplierTier(t *testing.T) {
	tests := []struct {
		name, phase string
		want        time.Duration
	}{
		{name: "xunlei", phase: "fast", want: 10 * time.Second},
		{name: "assrt", phase: "slow", want: 45 * time.Second},
		{name: "subtitle_best", phase: "slow", want: 30 * time.Second},
		{name: "subhd", phase: "slow", want: 45 * time.Second},
	}
	for _, test := range tests {
		if got := timeoutFor(test.name, test.phase); got != test.want {
			t.Errorf("timeoutFor(%q, %q) = %s, want %s", test.name, test.phase, got, test.want)
		}
	}
}

func TestAdaptiveTimeoutUsesObservedP95WithinBounds(t *testing.T) {
	record := subtitle_metrics.SupplierRuntime{Attempts: 20, AttemptBuckets: [6]int64{0, 20}}
	if got := adaptiveTimeoutFor("assrt", "slow", record); got != 30*time.Second {
		t.Fatalf("adaptive timeout = %s, want lower bound 30s", got)
	}
	record.AttemptBuckets = [6]int64{0, 0, 0, 20}
	if got := adaptiveTimeoutFor("assrt", "slow", record); got != 75*time.Second {
		t.Fatalf("adaptive timeout = %s, want upper bound 75s", got)
	}
}

func TestAssrtProviderLimiterIsSerial(t *testing.T) {
	providerLimiters.Delete("assrt")
	limiter := providerLimiter("assrt")
	limiter <- struct{}{}
	select {
	case limiter <- struct{}{}:
		t.Fatal("ASSRT limiter accepted a concurrent request")
	default:
	}
	<-limiter
}

func TestProviderBudgetIncludesLimiterAdmission(t *testing.T) {
	const name = "limiter-admission-budget-test"
	providerLimiters.Delete(name)
	limiter := providerLimiter(name)
	for index := 0; index < cap(limiter); index++ {
		limiter <- struct{}{}
	}
	t.Cleanup(func() {
		for len(limiter) > 0 {
			<-limiter
		}
		providerLimiters.Delete(name)
	})

	queryCalled := false
	outcomes := make(chan supplierOutcome, 1)
	started := time.Now()
	runSupplierWithBudget(context.Background(), log_helper.GetLogger4Tester(), &searchSupplier{name: name},
		"limiter-admission-budget", "fast", subtitle_metrics.CohortMovie,
		func(context.Context, ifaces.ISupplier) ([]supplier.SubInfo, error) {
			queryCalled = true
			return nil, nil
		}, outcomes, 30*time.Millisecond)
	outcome := <-outcomes
	if queryCalled {
		t.Fatal("provider query ran without limiter capacity")
	}
	if outcome.report.Outcome != ProviderOutcomeTimeout || !errors.Is(outcome.report.Err, context.DeadlineExceeded) {
		t.Fatalf("limiter admission outcome = %+v", outcome.report)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("limiter admission ignored provider budget: %s", elapsed)
	}
}

func TestRunReturnsAfterStrongSlowResultWithoutWaitingForEverySupplier(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan string, 2)
	sources := []ifaces.ISupplier{
		&searchSupplier{name: "subhd", started: started, items: []supplier.SubInfo{{Name: "strong"}}},
		&searchSupplier{name: "assrt", started: started, release: release},
	}
	startedAt := time.Now()
	got, err := Run(context.Background(), log_helper.GetLogger4Tester(), sources, "test-progressive", func(items []supplier.SubInfo) bool {
		return len(items) > 0
	}, movieQuery)
	if err != nil || len(got) != 1 || time.Since(startedAt) > time.Second {
		t.Fatalf("progressive slow tier did not stop promptly: items=%d elapsed=%s err=%v", len(got), time.Since(startedAt), err)
	}
	if first := <-started; first != "subhd" {
		t.Fatalf("first slow supplier = %q, want subhd", first)
	}
}

func TestReportDistinguishesHealthyEmptyFromUnavailable(t *testing.T) {
	t.Run("healthy empty", func(t *testing.T) {
		report := RunContextWithReport(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
			&searchSupplier{name: "report-empty-one"},
			&searchSupplier{name: "report-empty-two"},
		}, "report-empty", nil, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
			return movieQuery(source)
		})
		if report.Degraded || len(report.Providers) != 2 || report.OutcomeError() != nil {
			t.Fatalf("healthy empty report was not conclusive: %+v err=%v", report, report.OutcomeError())
		}
		for _, provider := range report.Providers {
			if provider.Outcome != ProviderOutcomeEmpty || provider.Failure != FailureNone {
				t.Fatalf("unexpected provider outcome: %+v", provider)
			}
		}
	})

	t.Run("empty plus failure is transient", func(t *testing.T) {
		report := RunContextWithReport(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
			&searchSupplier{name: "report-partial-empty"},
			&searchSupplier{name: "report-partial-error", err: errors.New("connection reset")},
		}, "report-partial", nil, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
			return movieQuery(source)
		})
		var searchErr *SearchError
		if !report.Degraded || !errors.As(report.OutcomeError(), &searchErr) || searchErr.Kind != FailureTransient {
			t.Fatalf("incomplete empty search did not request transient retry: %+v err=%v", report, report.OutcomeError())
		}
	})
}

func TestReportPreservesHitsWhenAnotherSupplierFails(t *testing.T) {
	report := RunContextWithReport(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
		&searchSupplier{name: "report-hit", items: []supplier.SubInfo{{Name: "candidate"}}},
		&searchSupplier{name: "report-error", err: errors.New("temporary upstream failure")},
	}, "report-mixed", nil, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
		return movieQuery(source)
	})
	if len(report.Items) != 1 || !report.Degraded || report.OutcomeError() != nil {
		t.Fatalf("degraded hit was not preserved: %+v err=%v", report, report.OutcomeError())
	}
}

func TestReportPreservesPartialHitAndErrorFromSameProvider(t *testing.T) {
	report := RunContextWithReport(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
		&searchSupplier{
			name: "report-partial-provider", items: []supplier.SubInfo{{Name: "usable-candidate"}},
			err: errors.New("provider returned a usable partial response"),
		},
	}, "report-partial-provider", nil, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
		return movieQuery(source)
	})
	if len(report.Items) != 1 || !report.Degraded || report.OutcomeError() != nil {
		t.Fatalf("same-provider partial hit was not preserved: %+v err=%v", report, report.OutcomeError())
	}
	if len(report.Providers) != 1 || report.Providers[0].Outcome != ProviderOutcomeError ||
		report.Providers[0].CandidateCount != 1 {
		t.Fatalf("same-provider degraded evidence was lost: %+v", report.Providers)
	}
}

func TestProviderReportJSONOmitsRawError(t *testing.T) {
	encoded, err := json.Marshal(ProviderReport{
		Provider: "safe-provider",
		Outcome:  ProviderOutcomeError,
		Err:      errors.New("private path and credential must stay in process"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private path") || strings.Contains(string(encoded), "credential") {
		t.Fatalf("provider report serialized its raw error: %s", encoded)
	}
}

func TestLegacyRunAPIsKeepProviderErrorsOutOfReturnError(t *testing.T) {
	queryErr := errors.New("legacy provider failure")
	for _, test := range []struct {
		name string
		run  func(ifaces.ISupplier) ([]supplier.SubInfo, error)
	}{
		{
			name: "Run",
			run: func(source ifaces.ISupplier) ([]supplier.SubInfo, error) {
				return Run(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{source},
					"legacy-run", nil, movieQuery)
			},
		},
		{
			name: "RunContext",
			run: func(source ifaces.ISupplier) ([]supplier.SubInfo, error) {
				return RunContext(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{source},
					"legacy-run-context", nil, func(_ context.Context, one ifaces.ISupplier) ([]supplier.SubInfo, error) {
						return movieQuery(one)
					})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			items, err := test.run(&searchSupplier{
				name: "legacy-partial-" + test.name, items: []supplier.SubInfo{{Name: "candidate"}}, err: queryErr,
			})
			if err != nil || len(items) != 1 {
				t.Fatalf("legacy API changed provider-error behavior: items=%+v err=%v", items, err)
			}
		})
	}
}

func TestReportClassifiesSkipsAndTypedProviderErrors(t *testing.T) {
	t.Run("daily limit", func(t *testing.T) {
		retryAt := time.Now().Add(2 * time.Hour).Round(time.Second)
		report := RunContextWithReport(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
			&searchSupplier{name: "report-limited", limited: true, retryAt: retryAt},
		}, "report-limited", nil, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
			return movieQuery(source)
		})
		var searchErr *SearchError
		if !errors.As(report.OutcomeError(), &searchErr) || searchErr.Kind != FailureQuota ||
			report.Providers[0].Outcome != ProviderOutcomeDailyLimit || !searchErr.RetryAt.Equal(retryAt) {
			t.Fatalf("daily limit classification lost: %+v err=%v", report, report.OutcomeError())
		}
	})

	t.Run("provider blocked", func(t *testing.T) {
		retryAt := time.Now().Add(time.Hour).Round(time.Second)
		report := RunContextWithReport(context.Background(), log_helper.GetLogger4Tester(), []ifaces.ISupplier{
			&searchSupplier{name: "report-blocked", err: NewSupplierError(FailureProviderBlocked, retryAt, errors.New("challenge"))},
		}, "report-blocked", nil, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
			return movieQuery(source)
		})
		var searchErr *SearchError
		if !errors.As(report.OutcomeError(), &searchErr) || searchErr.Kind != FailureProviderBlocked ||
			!searchErr.RetryAt.Equal(retryAt) {
			t.Fatalf("typed provider failure classification lost: %+v err=%v", report, report.OutcomeError())
		}
	})
}

func TestAggregateFailureOnlyPropagatesHomogeneousProviderRecoveryTimes(t *testing.T) {
	now := time.Now().Round(time.Second)
	early := now.Add(time.Hour)
	late := now.Add(4 * time.Hour)
	tests := []struct {
		name      string
		reports   []ProviderReport
		wantKind  FailureKind
		wantRetry time.Time
	}{
		{
			name: "all quota keeps earliest recovery",
			reports: []ProviderReport{
				{Outcome: ProviderOutcomeDailyLimit, Failure: FailureQuota, RetryAt: late},
				{Outcome: ProviderOutcomeError, Failure: FailureQuota, RetryAt: early},
			},
			wantKind: FailureQuota, wantRetry: early,
		},
		{
			name: "blocked and quota keeps earliest recovery",
			reports: []ProviderReport{
				{Outcome: ProviderOutcomeError, Failure: FailureProviderBlocked, RetryAt: late},
				{Outcome: ProviderOutcomeDailyLimit, Failure: FailureQuota, RetryAt: early},
			},
			wantKind: FailureProviderBlocked, wantRetry: early,
		},
		{
			name: "healthy empty mixed with blocked is transient without provider recovery",
			reports: []ProviderReport{
				{Outcome: ProviderOutcomeEmpty},
				{Outcome: ProviderOutcomeError, Failure: FailureProviderBlocked, RetryAt: late},
			},
			wantKind: FailureTransient,
		},
		{
			name: "transient mixed with quota does not inherit quota recovery",
			reports: []ProviderReport{
				{Outcome: ProviderOutcomeError, Failure: FailureTransient},
				{Outcome: ProviderOutcomeError, Failure: FailureQuota, RetryAt: late},
			},
			wantKind: FailureTransient,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, retryAt := aggregateFailure(test.reports)
			if kind != test.wantKind || !retryAt.Equal(test.wantRetry) {
				t.Fatalf("aggregateFailure() = (%s, %s), want (%s, %s)", kind, retryAt, test.wantKind, test.wantRetry)
			}
		})
	}
}
