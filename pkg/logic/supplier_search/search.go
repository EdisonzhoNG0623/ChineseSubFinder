package supplier_search

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type Query func(ifaces.ISupplier) ([]supplier.SubInfo, error)
type FastEnough func([]supplier.SubInfo) bool

var searchSequence atomic.Uint64

func NewSearchID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().Unix(), searchSequence.Add(1))
}

// Run searches low-latency sources first. Slow browser/API sources are only
// queried when the fast tier did not produce a strong deterministic match.
// Legacy supplier interfaces are intentionally kept unchanged.
func Run(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID string, fastEnough FastEnough, query Query) ([]supplier.SubInfo, error) {
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

	results = append(results, runTier(ctx, logger, slow, searchID, "slow", query)...)
	return results, ctx.Err()
}

func runTier(ctx context.Context, logger *logrus.Logger, suppliers []ifaces.ISupplier, searchID, phase string, query Query) []supplier.SubInfo {
	if len(suppliers) == 0 {
		return nil
	}
	type outcome struct {
		items []supplier.SubInfo
	}
	outcomes := make(chan outcome, len(suppliers))
	for _, source := range suppliers {
		source := source
		go func() {
			name := source.GetSupplierName()
			fields := logrus.Fields{"event": "supplier_search", "provider": name, "phase": phase, "search_id": searchID}
			if allowed, until := subtitle_metrics.ShouldAttempt(name, time.Now()); !allowed {
				subtitle_metrics.RecordCircuitSkip(name)
				fields["outcome"] = "circuit_open"
				fields["circuit_open_until"] = until.Format(time.RFC3339)
				logger.WithFields(fields).Warn("supplier search skipped")
				outcomes <- outcome{}
				return
			}

			startedAt := time.Now()
			callResult := make(chan struct {
				items   []supplier.SubInfo
				err     error
				skipped bool
			}, 1)
			go func() {
				result := struct {
					items   []supplier.SubInfo
					err     error
					skipped bool
				}{}
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
				result.items, result.err = query(source)
			}()

			budgetCtx, cancel := context.WithTimeout(ctx, timeoutFor(name, phase))
			defer cancel()
			var result struct {
				items   []supplier.SubInfo
				err     error
				skipped bool
			}
			select {
			case result = <-callResult:
			case <-budgetCtx.Done():
				result.err = budgetCtx.Err()
			}
			duration := time.Since(startedAt)
			fields["duration_ms"] = duration.Milliseconds()
			fields["candidate_count"] = len(result.items)
			if result.skipped {
				fields["outcome"] = "daily_limit"
				logger.WithFields(fields).Info("supplier search skipped")
				outcomes <- outcome{}
				return
			}

			subtitle_metrics.RecordAttempt(name, duration, len(result.items), result.err)
			switch {
			case result.err != nil && budgetCtx.Err() != nil:
				fields["outcome"] = "timeout"
				logger.WithFields(fields).Warn("supplier search timed out")
			case result.err != nil:
				fields["outcome"] = "error"
				logger.WithFields(fields).Warn("supplier search failed")
			case len(result.items) == 0:
				fields["outcome"] = "empty"
				logger.WithFields(fields).Info("supplier search completed")
			default:
				fields["outcome"] = "hit"
				logger.WithFields(fields).Info("supplier search completed")
			}
			outcomes <- outcome{items: result.items}
		}()
	}

	all := make([]supplier.SubInfo, 0)
	for range suppliers {
		result := <-outcomes
		all = append(all, result.items...)
	}
	return all
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
		return 20 * time.Second
	}
	switch strings.ToLower(name) {
	case "assrt":
		return 90 * time.Second
	case "subtitle_best", "subtitlebest":
		return 30 * time.Second
	default:
		return 60 * time.Second
	}
}
