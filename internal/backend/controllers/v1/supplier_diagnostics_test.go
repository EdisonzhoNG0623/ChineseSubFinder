package v1

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/subtitle_metrics"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

func TestSupplierDiagnosticsNeverExposeCredentialsAndRetireA4K(t *testing.T) {
	s := settings.NewSettings(t.TempDir())
	s.SubtitleSources.AssrtSettings.Enabled = true
	s.SubtitleSources.AssrtSettings.Token = "must-not-appear"
	diagnostics := buildSupplierDiagnostics(s, nil, nil)
	for _, diagnostic := range diagnostics {
		if diagnostic.Name == common.SubSiteA4K && (diagnostic.Enabled || diagnostic.Health != "RETIRED") {
			t.Fatalf("retired A4K should be disabled: %+v", diagnostic)
		}
		if diagnostic.RootURL == "must-not-appear" || diagnostic.StatusMessage == "must-not-appear" {
			t.Fatal("credential leaked through diagnostics")
		}
	}
}

func TestSupplierDiagnosticsExposeBoundedPerformanceAggregates(t *testing.T) {
	s := settings.NewSettings(t.TempDir())
	openUntil := time.Now().Add(time.Minute)
	runtime := map[string]subtitle_metrics.SupplierRuntime{
		common.SubSiteXunLei: {
			Name: common.SubSiteXunLei, Attempts: 2, TotalAttemptMs: 3000, MaxAttemptMs: 2000,
			AttemptBuckets: [6]int64{0, 2}, Timeouts: 1, CircuitSkips: 3, CircuitOpenUntil: openUntil,
			Selections: 4, Saves: 3, CacheHits: 5, EarlyStops: 2,
		},
	}
	for _, diagnostic := range buildSupplierDiagnostics(s, runtime, nil) {
		if diagnostic.Name != common.SubSiteXunLei {
			continue
		}
		if diagnostic.AverageAttemptMs != 1500 || diagnostic.P95AttemptMs != 5000 || diagnostic.Timeouts != 1 || diagnostic.CircuitSkips != 3 ||
			diagnostic.Selections != 4 || diagnostic.Saves != 3 || diagnostic.CacheHits != 5 || diagnostic.EarlyStops != 2 {
			t.Fatalf("unexpected performance aggregates: %+v", diagnostic)
		}
		if diagnostic.Health != "DEGRADED" || diagnostic.CircuitOpenUntil != openUntil {
			t.Fatalf("open circuit not reflected in diagnostics: %+v", diagnostic)
		}
		return
	}
	t.Fatal("xunlei diagnostic not found")
}

func TestSupplierAttemptWithoutHealthKeepsUnknownStatus(t *testing.T) {
	s := settings.NewSettings(t.TempDir())
	runtime := map[string]subtitle_metrics.SupplierRuntime{
		common.SubSiteXunLei: {Name: common.SubSiteXunLei, Attempts: 2, CandidateHits: 1},
	}
	for _, diagnostic := range buildSupplierDiagnostics(s, runtime, nil) {
		if diagnostic.Name == common.SubSiteXunLei && diagnostic.Health != "UNKNOWN" {
			t.Fatalf("attempt-only metric replaced health with %q", diagnostic.Health)
		}
	}
}

func TestSupplierDiagnosticsExplainWhyEnabledSourceWasNotAttempted(t *testing.T) {
	s := settings.NewSettings(t.TempDir())
	for _, diagnostic := range buildSupplierDiagnostics(s, nil, nil) {
		if diagnostic.Name != common.SubSiteXunLei {
			continue
		}
		if diagnostic.AttemptState != "NOT_ATTEMPTED" || diagnostic.NotAttemptedReason == "" {
			t.Fatalf("missing not-attempted explanation: %+v", diagnostic)
		}
		return
	}
	t.Fatal("xunlei diagnostic not found")
}

func TestPublicSupplierDiagnosticsFollowExplicitToggle(t *testing.T) {
	s := settings.NewSettings(t.TempDir())
	assertEnabled := func(name string, want bool) {
		t.Helper()
		for _, diagnostic := range buildSupplierDiagnostics(s, nil, nil) {
			if diagnostic.Name == name {
				if diagnostic.Enabled != want || diagnostic.Configured != want {
					t.Fatalf("%s enabled/configured = %v/%v, want %v", name, diagnostic.Enabled, diagnostic.Configured, want)
				}
				return
			}
		}
		t.Fatalf("diagnostic %s not found", name)
	}
	assertEnabled(common.SubSiteAnimeTosho, false)
	assertEnabled(common.SubSiteAddic7ed, false)
	s.SubtitleSources.AnimeToshoSettings.Enabled = true
	s.SubtitleSources.Addic7edSettings.Enabled = true
	assertEnabled(common.SubSiteAnimeTosho, true)
	assertEnabled(common.SubSiteAddic7ed, true)
}
