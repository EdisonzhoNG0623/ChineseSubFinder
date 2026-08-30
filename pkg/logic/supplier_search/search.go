package supplier_search

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type Query func(ifaces.ISupplier) ([]supplier.SubInfo, error)
type ContextQuery func(context.Context, ifaces.ISupplier) ([]supplier.SubInfo, error)
type FastEnough func([]supplier.SubInfo) bool

type retryAtSupplier interface {
	RetryAtTime() time.Time
}

var searchSequence atomic.Uint64
var providerLimiters sync.Map

var sharedResourceUses = struct {
	sync.Mutex
	active int
	idle   chan struct{}
}{idle: closedResourceIdleSignal()}

const slowHedgeDelay = 8 * time.Second

func NewSearchID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().Unix(), searchSequence.Add(1))
}

// Run searches low-latency sources first. Slow browser/API sources are only
// queried when the fast tier did not produce a strong deterministic match.
// Legacy supplier interfaces are intentionally kept unchanged.
func Run(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, fastEnough FastEnough, query Query) ([]supplier.SubInfo, error) {
	report := RunWithReport(ctx, logger, suppliers, searchID, fastEnough, query)
	return report.Items, report.ContextErr
}

// RunForCohort is the media-aware additive form of Run. Existing callers keep
// global routing behavior while new callers can isolate movie/series/anime
// observations.
func RunForCohort(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string,
	cohort subtitle_metrics.MediaCohort, fastEnough FastEnough, query Query) ([]supplier.SubInfo, error) {
	report := RunWithReportForCohort(ctx, logger, suppliers, searchID, cohort, fastEnough, query)
	return report.Items, report.ContextErr
}

// RunWithReport is the structured counterpart of Run for legacy suppliers.
func RunWithReport(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, fastEnough FastEnough, query Query) SearchReport {
	return RunWithReportForCohort(ctx, logger, suppliers, searchID, subtitle_metrics.CohortUnknown, fastEnough, query)
}

// RunWithReportForCohort is the media-aware additive form of RunWithReport.
func RunWithReportForCohort(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string,
	cohort subtitle_metrics.MediaCohort, fastEnough FastEnough, query Query) SearchReport {
	return RunContextWithReportForCohort(ctx, logger, suppliers, searchID, cohort, fastEnough, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
		return query(source)
	})
}

// RunContext is the context-aware extension of Run. Fast suppliers are still
// queried together, while slow suppliers are hedged in observed-value order
// and canceled as soon as a strong deterministic match is available.
func RunContext(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, fastEnough FastEnough, query ContextQuery) ([]supplier.SubInfo, error) {
	report := RunContextWithReport(ctx, logger, suppliers, searchID, fastEnough, query)
	return report.Items, report.ContextErr
}

// RunContextForCohort is the media-aware additive form of RunContext.
func RunContextForCohort(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string,
	cohort subtitle_metrics.MediaCohort, fastEnough FastEnough, query ContextQuery) ([]supplier.SubInfo, error) {
	report := RunContextWithReportForCohort(ctx, logger, suppliers, searchID, cohort, fastEnough, query)
	return report.Items, report.ContextErr
}

// RunContextWithReport preserves every terminal provider outcome so callers
// can distinguish a healthy empty search from provider unavailability. It is
// additive to RunContext, whose historical return semantics remain unchanged.
func RunContextWithReport(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, fastEnough FastEnough, query ContextQuery) SearchReport {
	return RunContextWithReportForCohort(ctx, logger, suppliers, searchID, subtitle_metrics.CohortUnknown, fastEnough, query)
}

// RunContextWithReportForCohort adds bounded media-aware routing without
// changing the SearchReport or legacy RunContext contracts.
func RunContextWithReportForCohort(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string,
	cohort subtitle_metrics.MediaCohort, fastEnough FastEnough, query ContextQuery) SearchReport {
	cohort = subtitle_metrics.NormalizeCohort(cohort)
	fast, slow := splitSuppliers(suppliers)
	fastResult := runTier(ctx, logger, fast, searchID, "fast", cohort, query)
	report := SearchReport{Items: fastResult.items, Providers: fastResult.providers}
	if err := ctx.Err(); err != nil {
		report.ContextErr = err
		report.Degraded = true
		return report
	}
	if fastEnough != nil && fastEnough(report.Items) {
		logger.WithFields(logrus.Fields{
			"event": "supplier_tier_complete", "search_id": searchID, "phase": "fast",
			"candidate_count": len(report.Items), "outcome": "strong_match",
		}).Info("slow supplier tier skipped")
		report.Degraded = reportsDegraded(report.Providers)
		return report
	}

	slowResult := runProgressiveTier(ctx, logger, slow, searchID, cohort, report.Items, fastEnough, query)
	report.Items = append(report.Items, slowResult.items...)
	report.Providers = append(report.Providers, slowResult.providers...)
	report.ContextErr = ctx.Err()
	report.Degraded = report.ContextErr != nil || reportsDegraded(report.Providers)
	return report
}

type supplierOutcome struct {
	provider string
	items    []supplier.SubInfo
	report   ProviderReport
}

type tierResult struct {
	items     []supplier.SubInfo
	providers []ProviderReport
}

func runTier(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID, phase string,
	cohort subtitle_metrics.MediaCohort, query ContextQuery) tierResult {
	if len(suppliers) == 0 {
		return tierResult{}
	}
	outcomes := make(chan supplierOutcome, len(suppliers))
	for _, source := range suppliers {
		go runSupplier(ctx, logger, source, searchID, phase, cohort, query, outcomes)
	}

	result := tierResult{items: make([]supplier.SubInfo, 0), providers: make([]ProviderReport, 0, len(suppliers))}
	for range suppliers {
		outcome := <-outcomes
		result.items = append(result.items, outcome.items...)
		result.providers = append(result.providers, outcome.report)
	}
	return result
}

func runProgressiveTier(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string,
	cohort subtitle_metrics.MediaCohort, existing []supplier.SubInfo, fastEnough FastEnough, query ContextQuery) tierResult {
	if len(suppliers) == 0 {
		return tierResult{}
	}
	suppliers, routingBasis := orderSlowSuppliers(suppliers, cohort)
	logger.WithFields(logrus.Fields{
		"event": "supplier_route_order", "search_id": searchID, "phase": "slow",
		"cohort": cohort.Label(), "routing_basis": routingBasis, "provider_order": supplierNames(suppliers),
	}).Info("slow supplier route selected")
	tierCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	outcomes := make(chan supplierOutcome, len(suppliers))
	next, completed := 0, 0
	startNext := func() {
		source := suppliers[next]
		next++
		go runSupplier(tierCtx, logger, source, searchID, "slow", cohort, query, outcomes)
	}
	startNext()
	timer := time.NewTimer(slowHedgeDelay)
	defer timer.Stop()
	all := append([]supplier.SubInfo(nil), existing...)
	result := tierResult{items: make([]supplier.SubInfo, 0), providers: make([]ProviderReport, 0, len(suppliers))}
	for completed < len(suppliers) {
		select {
		case outcome := <-outcomes:
			completed++
			all = append(all, outcome.items...)
			result.items = append(result.items, outcome.items...)
			result.providers = append(result.providers, outcome.report)
			if fastEnough != nil && fastEnough(all) {
				subtitle_metrics.RecordEarlyStopForCohort(outcome.provider, cohort)
				logger.WithFields(logrus.Fields{
					"event": "supplier_tier_complete", "search_id": searchID, "phase": "slow",
					"provider": outcome.provider, "candidate_count": len(all), "outcome": "strong_match",
				}).Info("remaining slow suppliers canceled")
				return result
			}
			if next < len(suppliers) && completed == next {
				startNext()
				resetTimer(timer, slowHedgeDelay)
			}
		case <-timer.C:
			if next < len(suppliers) {
				startNext()
				resetTimer(timer, slowHedgeDelay)
			}
		case <-ctx.Done():
			return result
		}
	}
	return result
}

func runSupplier(ctx context.Context, logger *logrus.Logger, source ifaces.ISupplier, searchID, phase string,
	cohort subtitle_metrics.MediaCohort, query ContextQuery, outcomes chan<- supplierOutcome) {
	runSupplierWithBudget(ctx, logger, source, searchID, phase, cohort, query, outcomes, timeoutFor(source.GetSupplierName(), phase))
}

func runSupplierWithBudget(ctx context.Context, logger *logrus.Logger, source ifaces.ISupplier, searchID, phase string,
	cohort subtitle_metrics.MediaCohort, query ContextQuery, outcomes chan<- supplierOutcome, budget time.Duration) {
	name := source.GetSupplierName()
	fields := logrus.Fields{
		"event": "supplier_search", "provider": name, "phase": phase,
		"search_id": searchID, "cohort": cohort.Label(),
	}
	if allowed, until := subtitle_metrics.ShouldAttempt(name, time.Now()); !allowed {
		subtitle_metrics.RecordCircuitSkip(name)
		fields["outcome"] = "circuit_open"
		fields["circuit_open_until"] = until.Format(time.RFC3339)
		logger.WithFields(fields).Warn("supplier search skipped")
		outcomes <- supplierOutcome{provider: name, report: ProviderReport{
			Provider: name, Phase: phase, Outcome: ProviderOutcomeCircuitOpen,
			Failure: FailureTransient, RetryAt: until,
		}}
		return
	}

	startedAt := time.Now()
	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	limiter := providerLimiter(name)
	select {
	case limiter <- struct{}{}:
	case <-budgetCtx.Done():
		duration := time.Since(startedAt)
		fields["duration_ms"] = duration.Milliseconds()
		fields["candidate_count"] = 0
		report := ProviderReport{
			Provider: name, Phase: phase, CandidateCount: 0, Duration: duration,
			Failure: FailureTransient, Err: budgetCtx.Err(),
		}
		if ctx.Err() != nil {
			report.Outcome = ProviderOutcomeCanceled
			fields["outcome"] = "canceled"
			logger.WithFields(fields).Info("supplier search canceled while waiting for provider capacity")
		} else {
			report.Outcome = ProviderOutcomeTimeout
			fields["outcome"] = "timeout"
			subtitle_metrics.RecordAttemptForCohort(name, cohort, duration, 0, budgetCtx.Err())
			logger.WithFields(fields).Warn("supplier search timed out while waiting for provider capacity")
		}
		outcomes <- supplierOutcome{provider: name, report: report}
		return
	}
	type callOutcome struct {
		items   []supplier.SubInfo
		err     error
		skipped bool
	}
	callResult := make(chan callOutcome, 1)
	// Register before spawning the legacy call. A goroutine that has not been
	// scheduled yet is still an active user of Chrome/tmp resources and must
	// prevent cleanup from starting underneath it.
	finishProviderCall := BeginSharedResourceUse()
	if budgetCtx.Err() != nil {
		finishProviderCall()
		<-limiter
		duration := time.Since(startedAt)
		fields["duration_ms"] = duration.Milliseconds()
		fields["candidate_count"] = 0
		report := ProviderReport{
			Provider: name, Phase: phase, CandidateCount: 0, Duration: duration,
			Failure: FailureTransient, Err: budgetCtx.Err(),
		}
		if ctx.Err() != nil {
			report.Outcome = ProviderOutcomeCanceled
			fields["outcome"] = "canceled"
			logger.WithFields(fields).Info("supplier search canceled before provider invocation")
		} else {
			report.Outcome = ProviderOutcomeTimeout
			fields["outcome"] = "timeout"
			subtitle_metrics.RecordAttemptForCohort(name, cohort, duration, 0, budgetCtx.Err())
			logger.WithFields(fields).Warn("supplier search timed out before provider invocation")
		}
		outcomes <- supplierOutcome{provider: name, report: report}
		return
	}
	go func() {
		defer finishProviderCall()
		defer func() { <-limiter }()
		result := callOutcome{}
		defer func() {
			if recovered := recover(); recovered != nil {
				result.err = fmt.Errorf("supplier panic: %v", recovered)
				logger.WithFields(fields).Errorf("supplier panic\n%s", debug.Stack())
			}
			callResult <- result
		}()
		if source.OverDailyDownloadLimit() {
			result.skipped = true
			return
		}
		result.items, result.err = query(budgetCtx, source)
	}()

	result := callOutcome{}
	canceledByParent := false
	select {
	case result = <-callResult:
	case <-budgetCtx.Done():
		result.err = budgetCtx.Err()
	}
	// Parent cancellation is authoritative even when a context-aware query and
	// ctx.Done become ready together and select happens to receive callResult.
	// Administrative stop and progressive early-stop must never degrade health
	// metrics or open a provider circuit.
	canceledByParent = ctx.Err() != nil
	duration := time.Since(startedAt)
	fields["duration_ms"] = duration.Milliseconds()
	fields["candidate_count"] = len(result.items)
	report := ProviderReport{
		Provider: name, Phase: phase, CandidateCount: len(result.items), Duration: duration, Err: result.err,
	}
	switch {
	case result.skipped:
		report.Outcome = ProviderOutcomeDailyLimit
		report.Failure = FailureQuota
		if provider, ok := source.(retryAtSupplier); ok {
			report.RetryAt = provider.RetryAtTime()
		}
		fields["outcome"] = "daily_limit"
		logger.WithFields(fields).Info("supplier search skipped")
	case canceledByParent:
		report.Outcome = ProviderOutcomeCanceled
		report.Failure = FailureTransient
		fields["outcome"] = "canceled"
		logger.WithFields(fields).Info("supplier search canceled")
	case result.err != nil:
		report.Failure, report.RetryAt = classifyFailure(result.err)
		subtitle_metrics.RecordAttemptForCohort(name, cohort, duration, len(result.items), result.err)
		if budgetCtx.Err() != nil {
			report.Outcome = ProviderOutcomeTimeout
			fields["outcome"] = "timeout"
			logger.WithFields(fields).Warn("supplier search timed out")
		} else {
			report.Outcome = ProviderOutcomeError
			fields["outcome"] = "error"
			logger.WithFields(fields).Warn("supplier search failed")
		}
	case len(result.items) == 0:
		report.Outcome = ProviderOutcomeEmpty
		subtitle_metrics.RecordAttemptForCohort(name, cohort, duration, 0, nil)
		fields["outcome"] = "empty"
		logger.WithFields(fields).Info("supplier search completed")
	default:
		report.Outcome = ProviderOutcomeHit
		subtitle_metrics.RecordAttemptForCohort(name, cohort, duration, len(result.items), nil)
		fields["outcome"] = "hit"
		logger.WithFields(fields).Info("supplier search completed")
	}
	outcomes <- supplierOutcome{provider: name, items: result.items, report: report}
}

func closedResourceIdleSignal() chan struct{} {
	idle := make(chan struct{})
	close(idle)
	return idle
}

// BeginSharedResourceUse registers work that may touch the process-wide
// Chrome instance or root temporary directory. Registration happens before a
// worker/query goroutine is spawned, closing the scheduling gap with cleanup.
func BeginSharedResourceUse() func() {
	sharedResourceUses.Lock()
	if sharedResourceUses.active == 0 {
		sharedResourceUses.idle = make(chan struct{})
	}
	sharedResourceUses.active++
	sharedResourceUses.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			sharedResourceUses.Lock()
			sharedResourceUses.active--
			if sharedResourceUses.active == 0 {
				close(sharedResourceUses.idle)
			}
			sharedResourceUses.Unlock()
		})
	}
}

// TryWithSharedResourcesIdle runs fn only when no registered worker, health
// check or provider invocation can use Chrome/tmp. Registration and fn share
// one boundary, so use cannot start between the idle check and cleanup.
func TryWithSharedResourcesIdle(fn func()) bool {
	sharedResourceUses.Lock()
	defer sharedResourceUses.Unlock()
	if sharedResourceUses.active != 0 {
		return false
	}
	if fn != nil {
		fn()
	}
	return true
}

// SharedResourcesIdleChan is a broadcast edge: every waiter on the current
// generation wakes when its active-use count reaches zero. Callers must still
// re-check with TryWithSharedResourcesIdle because a new generation may start.
func SharedResourcesIdleChan() <-chan struct{} {
	sharedResourceUses.Lock()
	idle := sharedResourceUses.idle
	sharedResourceUses.Unlock()
	return idle
}

func reportsDegraded(reports []ProviderReport) bool {
	for _, report := range reports {
		if report.degraded() {
			return true
		}
	}
	return false
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func slowSupplierRank(name string) int {
	switch strings.ToLower(name) {
	case "subhd":
		return 0
	case "assrt":
		return 1
	case "subtitle_best", "subtitlebest":
		return 2
	case "open_subtitles":
		return 3
	case "subsource":
		return 4
	case "animetosho":
		return 5
	case "addic7ed":
		return 6
	case "zimuku":
		return 7
	default:
		return 8
	}
}

func providerLimiter(name string) chan struct{} {
	limit := 4
	switch strings.ToLower(name) {
	case "subhd", "zimuku", "assrt", "animetosho", "addic7ed":
		limit = 1
	case "subtitle_best", "subtitlebest", "open_subtitles", "subsource":
		limit = 2
	}
	value, _ := providerLimiters.LoadOrStore(strings.ToLower(name), make(chan struct{}, limit))
	return value.(chan struct{})
}

func splitSuppliers(suppliers []ifaces.ISupplier) ([]ifaces.ISupplier, []ifaces.ISupplier) {
	fast := make([]ifaces.ISupplier, 0, len(suppliers))
	slow := make([]ifaces.ISupplier, 0, len(suppliers))
	for _, source := range suppliers {
		if isSlow(source.GetSupplierName()) {
			slow = append(slow, source)
		} else {
			fast = append(fast, source)
		}
	}
	return fast, slow
}

func isSlow(name string) bool {
	switch strings.ToLower(name) {
	case "assrt", "subhd", "zimuku", "subtitle_best", "subtitlebest", "open_subtitles", "subsource", "animetosho", "addic7ed":
		return true
	default:
		return false
	}
}

func timeoutFor(name, phase string) time.Duration {
	record := subtitle_metrics.Snapshot()[strings.ToLower(name)]
	return adaptiveTimeoutFor(name, phase, record)
}

// CurrentTimeout exposes the same bounded budget used by runtime searches for
// diagnostics. It contains no request or media data.
func CurrentTimeout(name string) time.Duration {
	phase := "fast"
	if isSlow(name) {
		phase = "slow"
	}
	return timeoutFor(name, phase)
}

func adaptiveTimeoutFor(name, phase string, record subtitle_metrics.SupplierRuntime) time.Duration {
	baseline, minimum, maximum := supplierTimeoutBounds(name, phase)
	if record.Attempts < 10 {
		return baseline
	}
	observed := time.Duration(record.P95AttemptMillis()) * time.Millisecond
	if observed <= 0 {
		return baseline
	}
	budget := observed + observed/4 + 2*time.Second
	if budget < minimum {
		return minimum
	}
	if budget > maximum {
		return maximum
	}
	return budget
}

func supplierTimeoutBounds(name, phase string) (baseline, minimum, maximum time.Duration) {
	if phase == "fast" {
		switch strings.ToLower(name) {
		case "xunlei", "shooter", "subdl":
			return 10 * time.Second, 5 * time.Second, 15 * time.Second
		default:
			return 20 * time.Second, 10 * time.Second, 30 * time.Second
		}
	}
	switch strings.ToLower(name) {
	case "assrt":
		return 45 * time.Second, 30 * time.Second, 75 * time.Second
	case "subtitle_best", "subtitlebest":
		return 30 * time.Second, 10 * time.Second, 45 * time.Second
	case "open_subtitles", "subsource", "animetosho", "addic7ed":
		return 75 * time.Second, 30 * time.Second, 120 * time.Second
	default:
		return 45 * time.Second, 15 * time.Second, 60 * time.Second
	}
}
