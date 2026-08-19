package sub_supplier

import (
	"testing"
	"time"
)

func TestSupplierHealthCooldownStartsAfterConsecutiveFailures(t *testing.T) {
	health := newSupplierHealthCooldown()
	now := time.Now()
	for i := 0; i < supplierFailureThreshold; i++ {
		health.record("site", false, now)
	}
	probe, next := health.shouldProbe("site", now.Add(time.Hour))
	if probe {
		t.Fatal("supplier should be cooling down")
	}
	if want := now.Add(supplierProbeCooldown); !next.Equal(want) {
		t.Fatalf("next probe = %v, want %v", next, want)
	}
	probe, _ = health.shouldProbe("site", next)
	if !probe {
		t.Fatal("supplier should be probed when cooldown expires")
	}
}

func TestSupplierHealthCooldownSuccessResetsFailures(t *testing.T) {
	health := newSupplierHealthCooldown()
	now := time.Now()
	health.record("site", false, now)
	health.record("site", false, now)
	health.record("site", true, now)
	health.record("site", false, now)
	probe, _ := health.shouldProbe("site", now.Add(time.Hour))
	if !probe {
		t.Fatal("a success should reset consecutive failures")
	}
}

func TestSkippedSupplierIsRemovedEvenWhenFreshInstanceDefaultsAlive(t *testing.T) {
	skipped := map[string]struct{}{"site": {}}
	if !shouldRemoveSupplier("site", skipped, true, false) {
		t.Fatal("a skipped supplier must be removed from the active hub")
	}
	if shouldRemoveSupplier("healthy", skipped, true, false) {
		t.Fatal("a healthy supplier that was probed must remain active")
	}
}
