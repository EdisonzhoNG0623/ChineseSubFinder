package supplier_search

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
)

// ProviderOutcome is the terminal state of one supplier invocation. It is
// deliberately independent from queue retry categories so supplier search can
// be reused without coupling it to the downloader.
type ProviderOutcome string

const (
	ProviderOutcomeHit         ProviderOutcome = "HIT"
	ProviderOutcomeEmpty       ProviderOutcome = "EMPTY"
	ProviderOutcomeError       ProviderOutcome = "ERROR"
	ProviderOutcomeTimeout     ProviderOutcome = "TIMEOUT"
	ProviderOutcomeCircuitOpen ProviderOutcome = "CIRCUIT_OPEN"
	ProviderOutcomeDailyLimit  ProviderOutcome = "DAILY_LIMIT"
	ProviderOutcomeCanceled    ProviderOutcome = "CANCELED"
)

// FailureKind describes how an unavailable supplier should be retried. The
// string markers emitted by SearchError remain compatible with the existing
// queue classifier while callers migrate to errors.As.
type FailureKind string

const (
	FailureNone            FailureKind = ""
	FailureTransient       FailureKind = "TRANSIENT"
	FailureProviderBlocked FailureKind = "PROVIDER_BLOCKED"
	FailureQuota           FailureKind = "QUOTA"
)

// ProviderReport keeps the evidence that used to be discarded while supplier
// results were merged. Err is intended for in-process diagnostics only; API
// responses should expose the stable outcome and failure kind instead.
type ProviderReport struct {
	Provider       string
	Phase          string
	Outcome        ProviderOutcome
	Failure        FailureKind
	CandidateCount int
	Duration       time.Duration
	RetryAt        time.Time
	Err            error `json:"-"`
}

func (r ProviderReport) degraded() bool {
	switch r.Outcome {
	case ProviderOutcomeHit, ProviderOutcomeEmpty:
		return false
	default:
		return true
	}
}

// SearchReport is the additive, structured search contract. Run and
// RunContext retain their legacy signatures; new consumers can use
// RunContextWithReport and make retry decisions from this evidence.
type SearchReport struct {
	Items      []supplier.SubInfo
	Providers  []ProviderReport
	Degraded   bool
	ContextErr error
}

// OutcomeError converts an incomplete empty search into a typed error. A
// genuine no-subtitle result is represented by nil only when every supplier
// that participated completed successfully with an empty result.
//
// Candidate hits always remain usable even if another provider failed. Parent
// cancellation is the exception: callers must not continue filesystem work
// after their context has ended.
func (r SearchReport) OutcomeError() error {
	if r.ContextErr != nil {
		return r.ContextErr
	}
	if len(r.Items) > 0 {
		return nil
	}
	if conclusivelyEmpty(r.Providers) {
		return nil
	}

	kind, retryAt := aggregateFailure(r.Providers)
	return &SearchError{Kind: kind, RetryAt: retryAt, ProviderCount: len(r.Providers)}
}

func conclusivelyEmpty(reports []ProviderReport) bool {
	if len(reports) == 0 {
		return false
	}
	for _, report := range reports {
		if report.Outcome != ProviderOutcomeEmpty {
			return false
		}
	}
	return true
}

// SearchError is safe to persist in the queue: it contains no paths, query
// terms, credentials, provider response bodies, or raw third-party errors.
type SearchError struct {
	Kind          FailureKind
	RetryAt       time.Time
	ProviderCount int
}

func (e *SearchError) Error() string {
	if e == nil {
		return ""
	}
	suffix := ""
	if !e.RetryAt.IsZero() {
		suffix = "; retry after " + e.RetryAt.UTC().Format(time.RFC3339)
	}
	switch e.Kind {
	case FailureProviderBlocked:
		return "supplier search provider blocked" + suffix
	case FailureQuota:
		// "too many requests" preserves the existing short-retry classifier
		// until queue diagnostics expose QUOTA as its own category.
		return "supplier quota exhausted (too many requests)" + suffix
	default:
		return "supplier search provider unavailable" + suffix
	}
}

// RetryAtTime is a small cross-package contract used by the queue to persist
// a provider-declared recovery time without importing supplier internals.
func (e *SearchError) RetryAtTime() time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.RetryAt
}

// SupplierError lets a provider communicate stable failure semantics without
// exposing its raw response to the queue. The original error remains available
// to internal logs through errors.Unwrap.
type SupplierError struct {
	Kind    FailureKind
	RetryAt time.Time
	Err     error
}

func (e *SupplierError) Error() string {
	if e == nil {
		return ""
	}
	label := strings.ToLower(string(e.Kind))
	if label == "" {
		label = "supplier"
	}
	if e.Err == nil {
		return label
	}
	return fmt.Sprintf("%s: %v", label, e.Err)
}

func (e *SupplierError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *SupplierError) RetryAtTime() time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.RetryAt
}

func NewSupplierError(kind FailureKind, retryAt time.Time, err error) error {
	return &SupplierError{Kind: kind, RetryAt: retryAt, Err: err}
}

func classifyFailure(err error) (FailureKind, time.Time) {
	if err == nil {
		return FailureNone, time.Time{}
	}
	var supplierErr *SupplierError
	if errors.As(err, &supplierErr) {
		kind := supplierErr.Kind
		if kind == FailureNone {
			kind = FailureTransient
		}
		return kind, supplierErr.RetryAt
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "verification"), strings.Contains(message, "captcha"),
		strings.Contains(message, "cloudflare"), strings.Contains(message, "forbidden"),
		strings.Contains(message, "status code 403"), strings.Contains(message, "http 403"):
		return FailureProviderBlocked, time.Time{}
	case strings.Contains(message, "quota"), strings.Contains(message, "daily limit"),
		strings.Contains(message, "too many requests"), strings.Contains(message, "status code 429"),
		strings.Contains(message, "http 429"):
		return FailureQuota, time.Time{}
	default:
		return FailureTransient, time.Time{}
	}
}

func aggregateFailure(reports []ProviderReport) (FailureKind, time.Time) {
	if len(reports) == 0 {
		return FailureTransient, time.Time{}
	}
	allQuota := true
	allBlockedOrQuota := true
	blocked := false
	var retryAt time.Time
	for _, report := range reports {
		if !report.degraded() {
			// A healthy empty mixed with unavailable providers is still an
			// incomplete search, so retry it as transient instead of sleeping on
			// a provider-specific quota/captcha schedule.
			allQuota = false
			allBlockedOrQuota = false
			continue
		}
		if !report.RetryAt.IsZero() && (retryAt.IsZero() || report.RetryAt.Before(retryAt)) {
			retryAt = report.RetryAt
		}
		switch report.Failure {
		case FailureQuota:
		case FailureProviderBlocked:
			allQuota = false
			blocked = true
		default:
			allQuota = false
			allBlockedOrQuota = false
		}
	}
	if allQuota {
		return FailureQuota, retryAt
	}
	if blocked && allBlockedOrQuota {
		return FailureProviderBlocked, retryAt
	}
	// A transient result means the provider set is heterogeneous (for example,
	// one healthy empty plus one quota-limited source) or includes an ordinary
	// transport failure. A provider-specific recovery time is not a safe lower
	// bound for that whole set, so let the queue apply its transient backoff.
	return FailureTransient, time.Time{}
}
