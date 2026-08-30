package supplier_search

import (
	"context"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
)

func TestRunContextForCohortRecordsBoundedMediaObservation(t *testing.T) {
	const name = "test-cohort-search-provider"
	items, err := RunContextForCohort(
		context.Background(), log_helper.GetLogger4Tester(),
		[]ifaces.ISupplier{&searchSupplier{name: name, items: []supplier.SubInfo{{Name: "candidate"}}}},
		"test-cohort-search", subtitle_metrics.CohortAnime, nil,
		func(_ context.Context, source ifaces.ISupplier) ([]supplier.SubInfo, error) {
			return movieQuery(source)
		},
	)
	if err != nil || len(items) != 1 {
		t.Fatalf("media-aware search failed: items=%v err=%v", items, err)
	}
	got := subtitle_metrics.SnapshotForCohort(subtitle_metrics.CohortAnime)[name]
	if got.Attempts != 1 || got.CandidateHits != 1 || got.Candidates != 1 {
		t.Fatalf("media-aware search did not record cohort aggregate: %+v", got)
	}
}

func TestDynamicRoutePrefersOpenSubtitlesOverSlowFailingAssrt(t *testing.T) {
	sources := slowTestSuppliers("assrt", "open_subtitles")
	movie := map[string]subtitle_metrics.SupplierRuntime{
		"assrt":          runtimeObservation(100, 10, 2, 60, 40, 90_000),
		"open_subtitles": runtimeObservation(100, 55, 32, 3, 1, 7_000),
	}

	ordered, basis := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortMovie, movie, nil)
	if got := supplierNames(ordered); got[0] != "open_subtitles" || basis != "cohort" {
		t.Fatalf("dynamic route = %v (%s), want open_subtitles first from movie cohort", got, basis)
	}
}

func TestDynamicRouteFallsBackToStaticOrderBelowSampleFloor(t *testing.T) {
	sources := slowTestSuppliers("open_subtitles", "assrt", "subhd")
	movie := map[string]subtitle_metrics.SupplierRuntime{
		"open_subtitles": runtimeObservation(routingMinAttempts-1, routingMinAttempts-1, routingMinAttempts-1, 0, 0, 100),
		"assrt":          runtimeObservation(routingMinAttempts-1, 0, 0, routingMinAttempts-1, 0, 120_000),
		"subhd":          runtimeObservation(routingMinAttempts-1, 0, 0, 0, 0, 120_000),
	}

	ordered, basis := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortMovie, movie, nil)
	want := []string{"subhd", "assrt", "open_subtitles"}
	if got := supplierNames(ordered); !equalStrings(got, want) || basis != "static" {
		t.Fatalf("low-sample route = %v (%s), want static %v", got, basis, want)
	}
}

func TestStaticFallbackPreservesInputOrderForEqualRanks(t *testing.T) {
	sources := slowTestSuppliers("unknown-z", "unknown-a")
	ordered, basis := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortMovie, nil, nil)
	want := []string{"unknown-z", "unknown-a"}
	if got := supplierNames(ordered); !equalStrings(got, want) || basis != "static" {
		t.Fatalf("equal-rank static fallback changed stable order: %v (%s)", got, basis)
	}
}

func TestDynamicRouteDemotesProvenZeroYieldProvider(t *testing.T) {
	sources := slowTestSuppliers("subhd", "assrt")
	series := map[string]subtitle_metrics.SupplierRuntime{
		"subhd": runtimeObservation(80, 0, 0, 0, 0, 2_000),
		"assrt": runtimeObservation(80, 20, 5, 2, 0, 20_000),
	}

	ordered, _ := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortSeries, series, nil)
	if got := supplierNames(ordered); got[0] != "assrt" {
		t.Fatalf("zero-yield provider was not demoted: %v", got)
	}
}

func TestMatureHighValueProviderBeatsUnsampledStaticPrior(t *testing.T) {
	sources := slowTestSuppliers("assrt", "open_subtitles")
	movie := map[string]subtitle_metrics.SupplierRuntime{
		"assrt":          {Attempts: 1},
		"open_subtitles": runtimeObservation(80, 44, 24, 2, 0, 7_000),
	}

	ordered, _ := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortMovie, movie, nil)
	if got := supplierNames(ordered); got[0] != "open_subtitles" {
		t.Fatalf("unsampled static prior suppressed mature high-value provider: %v", got)
	}
}

func TestDynamicRouteKeepsCohortsIndependent(t *testing.T) {
	sources := slowTestSuppliers("assrt", "open_subtitles")
	movie := map[string]subtitle_metrics.SupplierRuntime{
		"assrt":          runtimeObservation(60, 3, 1, 35, 20, 100_000),
		"open_subtitles": runtimeObservation(60, 45, 25, 1, 0, 5_000),
	}
	anime := map[string]subtitle_metrics.SupplierRuntime{
		"assrt":          runtimeObservation(60, 45, 30, 1, 0, 8_000),
		"open_subtitles": runtimeObservation(60, 2, 0, 30, 15, 90_000),
	}

	movieOrder, _ := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortMovie, movie, nil)
	animeOrder, _ := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortAnime, anime, nil)
	if movieFirst, animeFirst := supplierNames(movieOrder)[0], supplierNames(animeOrder)[0]; movieFirst != "open_subtitles" || animeFirst != "assrt" {
		t.Fatalf("cohort routes leaked: movie=%s anime=%s", movieFirst, animeFirst)
	}
}

func TestDynamicRouteUsesLegacyGlobalMetricsOnlyForColdStart(t *testing.T) {
	sources := slowTestSuppliers("assrt", "open_subtitles")
	global := map[string]subtitle_metrics.SupplierRuntime{
		"assrt":          runtimeObservation(50, 0, 0, 30, 20, 90_000),
		"open_subtitles": runtimeObservation(50, 35, 20, 1, 0, 5_000),
	}

	ordered, basis := orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortAnime, nil, global)
	if got := supplierNames(ordered); got[0] != "open_subtitles" || basis != "global_cold_start" {
		t.Fatalf("legacy cold-start route = %v (%s)", got, basis)
	}

	// Any cohort evidence ends the global fallback. Until that cohort reaches
	// the sample floor it deliberately returns to the deterministic static order.
	coldAnime := map[string]subtitle_metrics.SupplierRuntime{
		"assrt": {Attempts: 1},
	}
	ordered, basis = orderSlowSuppliersWithSnapshots(sources, subtitle_metrics.CohortAnime, coldAnime, global)
	if got := supplierNames(ordered); got[0] != "assrt" || basis != "static" {
		t.Fatalf("partially warmed cohort should use static order, got %v (%s)", got, basis)
	}
}

func slowTestSuppliers(names ...string) []ifaces.ISupplier {
	suppliers := make([]ifaces.ISupplier, 0, len(names))
	for _, name := range names {
		suppliers = append(suppliers, &searchSupplier{name: name})
	}
	return suppliers
}

func runtimeObservation(attempts, hits, saves, failures, timeouts, averageMillis int64) subtitle_metrics.SupplierRuntime {
	return subtitle_metrics.SupplierRuntime{
		Attempts: attempts, CandidateHits: hits, Saves: saves, Errors: failures, Timeouts: timeouts,
		TotalAttemptMs: attempts * averageMillis,
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
