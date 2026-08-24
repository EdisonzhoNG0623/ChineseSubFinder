package v1

import (
	"testing"

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
