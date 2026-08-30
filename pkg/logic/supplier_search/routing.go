package supplier_search

import (
	"sort"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
)

// A provider needs enough completed calls before observations may move it away
// from the deterministic static order. This prevents a handful of lucky or
// unlucky calls from making routing oscillate.
const routingMinAttempts int64 = 12

type supplierRoute struct {
	supplier   ifaces.ISupplier
	staticRank int
	original   int
	score      float64
}

// orderSlowSuppliers uses cohort observations once that cohort is warm. A
// completely empty cohort may use legacy global metrics as a cold-start hint;
// once the cohort has any evidence, global observations no longer bleed into
// it. Suppliers below the sample floor retain their exact static prior.
func orderSlowSuppliers(suppliers []ifaces.ISupplier, cohort subtitle_metrics.MediaCohort) ([]ifaces.ISupplier, string) {
	return orderSlowSuppliersWithSnapshots(
		suppliers,
		cohort,
		subtitle_metrics.SnapshotForCohort(cohort),
		subtitle_metrics.Snapshot(),
	)
}

func orderSlowSuppliersWithSnapshots(suppliers []ifaces.ISupplier, cohort subtitle_metrics.MediaCohort,
	cohortSnapshot, globalSnapshot map[string]subtitle_metrics.SupplierRuntime) ([]ifaces.ISupplier, string) {

	cohort = subtitle_metrics.NormalizeCohort(cohort)
	observations := cohortSnapshot
	basis := "cohort"
	if cohort == subtitle_metrics.CohortUnknown {
		observations = globalSnapshot
		basis = "global"
	} else if snapshotAttemptsForSuppliers(suppliers, cohortSnapshot) == 0 {
		observations = globalSnapshot
		basis = "global_cold_start"
	}

	routes := make([]supplierRoute, 0, len(suppliers))
	anyDynamic := false
	for index, source := range suppliers {
		name := source.GetSupplierName()
		staticRank := slowSupplierRank(name)
		record := supplierRuntimeForName(observations, name)
		route := supplierRoute{
			supplier: source, staticRank: staticRank, original: index,
			score: staticRouteScore(staticRank),
		}
		if record.Attempts >= routingMinAttempts {
			route.score = observedRouteScore(record)
			anyDynamic = true
		}
		routes = append(routes, route)
	}
	if !anyDynamic {
		basis = "static"
	}

	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].score != routes[j].score {
			return routes[i].score > routes[j].score
		}
		if routes[i].staticRank != routes[j].staticRank {
			return routes[i].staticRank < routes[j].staticRank
		}
		return routes[i].original < routes[j].original
	})

	ordered := make([]ifaces.ISupplier, len(routes))
	for index, route := range routes {
		ordered[index] = route.supplier
	}
	return ordered, basis
}

func snapshotAttemptsForSuppliers(suppliers []ifaces.ISupplier, snapshot map[string]subtitle_metrics.SupplierRuntime) int64 {
	var attempts int64
	for _, source := range suppliers {
		attempts += supplierRuntimeForName(snapshot, source.GetSupplierName()).Attempts
	}
	return attempts
}

func supplierRuntimeForName(snapshot map[string]subtitle_metrics.SupplierRuntime, name string) subtitle_metrics.SupplierRuntime {
	if record, ok := snapshot[name]; ok {
		return record
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	if record, ok := snapshot[normalized]; ok {
		return record
	}
	for candidate, record := range snapshot {
		if strings.EqualFold(strings.TrimSpace(candidate), normalized) {
			return record
		}
	}
	return subtitle_metrics.SupplierRuntime{}
}

func staticRouteScore(rank int) float64 {
	// Static priors occupy 610..850. A mature high-value provider can beat an
	// unsampled source, while a mature zero-yield provider falls just below even
	// the unknown-source prior and is therefore automatically demoted.
	return 850 - float64(rank*30)
}

// observedRouteScore rewards useful candidates and subtitles that survive the
// selection/write path, then discounts provider failures, timeouts and wall
// time. Every term is bounded, so one counter cannot dominate indefinitely.
func observedRouteScore(record subtitle_metrics.SupplierRuntime) float64 {
	if record.Attempts <= 0 {
		return 0
	}
	attempts := float64(record.Attempts)
	hitRate := boundedRatio(record.CandidateHits, attempts)
	saveYield := boundedRatio(record.Saves, attempts)
	errorRate := boundedRatio(record.Errors, attempts)
	timeoutRate := boundedRatio(record.Timeouts, attempts)
	latencyPenalty := float64(record.AverageAttemptMillis()) / float64(120000)
	if latencyPenalty > 1 {
		latencyPenalty = 1
	}
	if latencyPenalty < 0 {
		latencyPenalty = 0
	}
	return 600 + 500*hitRate + 400*saveYield - 500*errorRate - 250*timeoutRate - 150*latencyPenalty
}

func boundedRatio(value int64, denominator float64) float64 {
	if value <= 0 || denominator <= 0 {
		return 0
	}
	ratio := float64(value) / denominator
	if ratio > 1 {
		return 1
	}
	return ratio
}

func supplierNames(suppliers []ifaces.ISupplier) []string {
	names := make([]string, 0, len(suppliers))
	for _, source := range suppliers {
		names = append(names, strings.ToLower(strings.TrimSpace(source.GetSupplierName())))
	}
	return names
}
