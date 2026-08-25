package supplier_search

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
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

var searchSequence atomic.Uint64
var providerLimiters sync.Map

const slowHedgeDelay = 8 * time.Second

func NewSearchID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().Unix(), searchSequence.Add(1))
}

// Run searches low-latency sources first. Slow browser/API sources are only
// queried when the fast tier did not produce a strong deterministic match.
// Legacy supplier interfaces are intentionally kept unchanged.
func Run(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, fastEnough FastEnough, query Query) ([]supplier.SubInfo, error) {
	return RunContext(ctx, logger, suppliers, searchID, fastEnough, func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
		return query(source)
	})
}

// RunContext is the context-aware extension of Run. Fast suppliers are still
// queried together, while slow suppliers are hedged in observed-value order
// and canceled as soon as a strong deterministic match is available.
func RunContext(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, fastEnough FastEnough, query ContextQuery) ([]supplier.SubInfo, error) {
	fast, slow := splitSuppliers(suppliers)
	results := runTier(ctx, logger, fast, searchID, "fast", query)
	if err := ctx.Err(); err != nil {
		return results, err
	}
	if fastEnough != nil && fastEnough(results) {
		logger.WithFields(logrus.Fields{
			"event": "supplier_tier_complete", "search_id": searchID, "phase": "fast",
			"candidate_count": len(results), "outcome": "strong_match",
		}).Info("slow supplier tier skipped")
		return results, nil
	}

	results = append(results, runProgressiveTier(ctx, logger, slow, searchID, results, fastEnough, query)...)
	return results, ctx.Err()
}

type supplierOutcome struct {
	provider string
	items    []supplier.SubInfo
}

func runTier(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID, phase string, query ContextQuery) []supplier.SubInfo {
	if len(suppliers) == 0 {
		return nil
	}
	outcomes := make(chan supplierOutcome, len(suppliers))
	for _, source := range suppliers {
		go runSupplier(ctx, logger, source, searchID, phase, query, outcomes)
	}

	all := make([]supplier.SubInfo, 0)
	for range suppliers {
		result := <-outcomes
		all = append(all, result.items...)
	}
	return all
}

func runProgressiveTier(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, existing []supplier.SubInfo, fastEnough FastEnough, query ContextQuery) []supplier.SubInfo {
	if len(suppliers) == 0 {
		return nil
	}
	suppliers = append([]ifaces.ISupplier(nil), suppliers...)
	sort.SliceStable(suppliers, func(i, j int) bool {
		return slowSupplierRank(suppliers[i].GetSupplierName()) < slowSupplierRank(suppliers[j].GetSupplierName())
	})
	tierCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	outcomes := make(chan supplierOutcome, len(suppliers))
	next, completed := 0, 0
	startNext := func() {
		source := suppliers[next]
		next++
		go runSupplier(tierCtx, logger, source, searchID, "slow", query, outcomes)
	}
	startNext()
	timer := time.NewTimer(slowHedgeDelay)
	defer timer.Stop()
	all := append([]supplier.SubInfo(nil), existing...)
	slowResults := make([]supplier.SubInfo, 0)
	for completed < len(suppliers) {
		select {
		case result := <-outcomes:
			completed++
			all = append(all, result.items...)
			slowResults = append(slowResults, result.items...)
			if fastEnough != nil && fastEnough(all) {
				subtitle_metrics.RecordEarlyStop(result.provider)
				logger.WithFields(logrus.Fields{
					"event": "supplier_tier_complete", "search_id": searchID, "phase": "slow",
					"provider": result.provider, "candidate_count": len(all), "outcome": "strong_match",
				}).Info("remaining slow suppliers canceled")
				return slowResults
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
			return slowResults
		}
	}
	return slowResults
}

func runSupplier(ctx context.Context, logger *logrus.Logger, source ifaces.ISupplier, searchID, phase string, query ContextQuery, outcomes chan<- supplierOutcome) {
	name := source.GetSupplierName()
	fields := logrus.Fields{"event": "supplier_search", "provider": name, "phase": phase, "search_id": searchID}
	if allowed, until := subtitle_metrics.ShouldAttempt(name, time.Now()); !allowed {
		subtitle_metrics.RecordCircuitSkip(name)
		fields["outcome"] = "circuit_open"
		fields["circuit_open_until"] = until.Format(time.RFC3339)
		logger.WithFields(fields).Warn("supplier search skipped")
		outcomes <- supplierOutcome{provider: name}
		return
	}

	limiter := providerLimiter(name)
	select {
	case limiter <- struct{}{}:
	case <-ctx.Done():
		outcomes <- supplierOutcome{provider: name}
		return
	}
	startedAt := time.Now()
	budgetCtx, cancel := context.WithTimeout(ctx, timeoutFor(name, phase))
	defer cancel()
	type callOutcome struct {
		items   []supplier.SubInfo
		err     error
		skipped bool
	}
	callResult := make(chan callOutcome, 1)
	go func() {
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
		canceledByParent = ctx.Err() != nil
	}
	duration := time.Since(startedAt)
	fields["duration_ms"] = duration.Milliseconds()
	fields["candidate_count"] = len(result.items)
	switch {
	case result.skipped:
		fields["outcome"] = "daily_limit"
		logger.WithFields(fields).Info("supplier search skipped")
	case canceledByParent:
		fields["outcome"] = "canceled"
		logger.WithFields(fields).Info("supplier search canceled")
	case result.err != nil:
		subtitle_metrics.RecordAttempt(name, duration, 0, result.err)
		if budgetCtx.Err() != nil {
			fields["outcome"] = "timeout"
			logger.WithFields(fields).Warn("supplier search timed out")
		} else {
			fields["outcome"] = "error"
			logger.WithFields(fields).Warn("supplier search failed")
		}
	case len(result.items) == 0:
		subtitle_metrics.RecordAttempt(name, duration, 0, nil)
		fields["outcome"] = "empty"
		logger.WithFields(fields).Info("supplier search completed")
	default:
		subtitle_metrics.RecordAttempt(name, duration, len(result.items), nil)
		fields["outcome"] = "hit"
		logger.WithFields(fields).Info("supplier search completed")
	}
	outcomes <- supplierOutcome{provider: name, items: result.items}
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
	case "zimuku":
		return 3
	default:
		return 4
	}
}

func providerLimiter(name string) chan struct{} {
	limit := 4
	switch strings.ToLower(name) {
	case "subhd", "zimuku":
		limit = 1
	case "assrt", "subtitle_best", "subtitlebest":
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
	case "assrt", "subhd", "zimuku", "subtitle_best", "subtitlebest":
		return true
	default:
		return false
	}
}

func timeoutFor(name, phase string) time.Duration {
	if phase == "fast" {
		switch strings.ToLower(name) {
		case "xunlei", "shooter", "subdl":
			return 10 * time.Second
		default:
			return 20 * time.Second
		}
	}
	switch strings.ToLower(name) {
	case "assrt":
		return 180 * time.Second
	case "subtitle_best", "subtitlebest":
		return 30 * time.Second
	default:
		return 60 * time.Second
	}
}
