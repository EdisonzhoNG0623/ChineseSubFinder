package sub_supplier

import (
	"sync"
	"time"
)

const (
	supplierFailureThreshold = 3
	supplierProbeCooldown    = 6 * time.Hour
)

type supplierProbeState struct {
	consecutiveFailures int
	nextProbe           time.Time
}

type supplierHealthCooldown struct {
	mu     sync.Mutex
	states map[string]supplierProbeState
}

func newSupplierHealthCooldown() *supplierHealthCooldown {
	return &supplierHealthCooldown{states: make(map[string]supplierProbeState)}
}

func (h *supplierHealthCooldown) shouldProbe(name string, now time.Time) (bool, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[name]
	if !state.nextProbe.IsZero() && now.Before(state.nextProbe) {
		return false, state.nextProbe
	}
	return true, time.Time{}
}

func (h *supplierHealthCooldown) record(name string, alive bool, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if alive {
		delete(h.states, name)
		return
	}
	state := h.states[name]
	state.consecutiveFailures++
	if state.consecutiveFailures >= supplierFailureThreshold {
		state.nextProbe = now.Add(supplierProbeCooldown)
		state.consecutiveFailures = 0
	}
	h.states[name] = state
}

func shouldRemoveSupplier(name string, skipped map[string]struct{}, alive, overLimit bool) bool {
	_, wasSkipped := skipped[name]
	return wasSkipped || !alive || overLimit
}

var processSupplierHealthCooldown = newSupplierHealthCooldown()
