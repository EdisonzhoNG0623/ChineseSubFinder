package common_test

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

func TestSuppliersSettingsUsesCurrentZimukuDomain(t *testing.T) {
	supplierSettings := settings.NewSuppliersSettings()
	got := supplierSettings.Zimuku.RootUrl
	if got != common.SubZiMuKuRootUrlDef {
		t.Fatalf("Zimuku.RootUrl = %q, want %q", got, common.SubZiMuKuRootUrlDef)
	}
	if supplierSettings.Zimuku.DailyDownloadLimit != -1 || supplierSettings.SubHD.DailyDownloadLimit != -1 {
		t.Fatalf("restored browser suppliers must default to unlimited: Zimuku=%d SubHD=%d",
			supplierSettings.Zimuku.DailyDownloadLimit, supplierSettings.SubHD.DailyDownloadLimit)
	}
	if supplierSettings.A4k.DailyDownloadLimit != 0 {
		t.Fatalf("retired A4K supplier must default to disabled, got %d", supplierSettings.A4k.DailyDownloadLimit)
	}
}

func TestSuppliersSettingsDisablesRetiredA4kDomain(t *testing.T) {
	supplierSettings := settings.NewSuppliersSettings()
	supplierSettings.A4k.DailyDownloadLimit = -1

	supplierSettings.ReSetSearchUrl()

	if supplierSettings.A4k.DailyDownloadLimit != 0 {
		t.Fatalf("retired A4K endpoint remained enabled: %d", supplierSettings.A4k.DailyDownloadLimit)
	}
}

func TestSuppliersSettingsKeepsCustomA4kMirrorEnabled(t *testing.T) {
	supplierSettings := settings.NewSuppliersSettings()
	supplierSettings.A4k.RootUrl = "https://a4k.example"
	supplierSettings.A4k.DailyDownloadLimit = -1

	supplierSettings.ReSetSearchUrl()

	if supplierSettings.A4k.DailyDownloadLimit != -1 {
		t.Fatalf("custom A4K mirror was disabled: %d", supplierSettings.A4k.DailyDownloadLimit)
	}
}

func TestOneSupplierSettingsDailyDownloadLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		count int
		want  bool
	}{
		{name: "disabled", limit: 0, count: 0, want: true},
		{name: "unlimited", limit: -1, count: 100000, want: false},
		{name: "below limit", limit: 20, count: 19, want: false},
		{name: "at limit", limit: 20, count: 20, want: true},
		{name: "above limit", limit: 20, count: 21, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			supplierSettings := settings.NewOneSupplierSettings("test", "", "", test.limit)
			if got := supplierSettings.OverDailyDownloadLimit(test.count); got != test.want {
				t.Fatalf("OverDailyDownloadLimit(%d) = %t, want %t", test.count, got, test.want)
			}
		})
	}
}

func TestSuppliersSettingsMigratesLegacyZimukuDomain(t *testing.T) {
	supplierSettings := settings.NewSuppliersSettings()
	supplierSettings.Zimuku.RootUrl = "https://zimuku.org"
	supplierSettings.Zimuku.DailyDownloadLimit = 20
	supplierSettings.SubHD.DailyDownloadLimit = 20

	supplierSettings.ReSetSearchUrl()

	if supplierSettings.Zimuku.RootUrl != common.SubZiMuKuRootUrlDef {
		t.Fatalf("Zimuku.RootUrl = %q, want %q", supplierSettings.Zimuku.RootUrl, common.SubZiMuKuRootUrlDef)
	}
	if supplierSettings.Zimuku.DailyDownloadLimit != -1 || supplierSettings.SubHD.DailyDownloadLimit != -1 {
		t.Fatalf("legacy limits were not migrated: Zimuku=%d SubHD=%d",
			supplierSettings.Zimuku.DailyDownloadLimit, supplierSettings.SubHD.DailyDownloadLimit)
	}
}

func TestSuppliersSettingsKeepsCustomZimukuDomain(t *testing.T) {
	supplierSettings := settings.NewSuppliersSettings()
	supplierSettings.Zimuku.RootUrl = "https://zimuku.example"
	supplierSettings.Zimuku.DailyDownloadLimit = 50
	supplierSettings.SubHD.DailyDownloadLimit = 100

	supplierSettings.ReSetSearchUrl()

	if supplierSettings.Zimuku.RootUrl != "https://zimuku.example" {
		t.Fatalf("custom Zimuku.RootUrl was overwritten: %q", supplierSettings.Zimuku.RootUrl)
	}
	if supplierSettings.Zimuku.DailyDownloadLimit != 50 || supplierSettings.SubHD.DailyDownloadLimit != 100 {
		t.Fatalf("custom limits were overwritten: Zimuku=%d SubHD=%d",
			supplierSettings.Zimuku.DailyDownloadLimit, supplierSettings.SubHD.DailyDownloadLimit)
	}
}
