// SPDX-License-Identifier: Apache-2.0

package orphan

import "github.com/prometheus/client_golang/prometheus"

// Skip-reason label values for ach_orphan_cleanup_skipped_total. Kept as
// exported constants so call sites and tests never drift on the strings.
const (
	SkipReasonDryRun             = "dry_run"
	SkipReasonEmptyActiveSet     = "empty_active_set"
	SkipReasonCircuitBreaker     = "circuit_breaker"
	SkipReasonLiteLLMUnreachable = "litellm_unreachable"
	SkipReasonRevokeFailed       = "revoke_failed"
)

// Metrics holds the orphan-cleanup Prometheus collectors. One set per
// operator process; NewMetrics registers them on the supplied registry
// (controller-runtime's crmetrics.Registry in production, a throwaway
// *prometheus.Registry in unit tests so each test starts from zero and
// no global double-register panic occurs).
//
// The Runnable instruments through the nil-guarded m* helpers, so a
// Runnable with a nil Metrics (none wired) simply records nothing.
type Metrics struct {
	// Candidates counts true orphans identified per tick (ACH-owned,
	// older than the floor, not in the active set) — BEFORE the B1/B2
	// guards or dry-run decide whether to actually revoke.
	Candidates prometheus.Counter
	// Revoked counts keys actually revoked (real, non-dry-run, success).
	Revoked prometheus.Counter
	// Skipped counts revocations NOT performed, partitioned by reason.
	Skipped *prometheus.CounterVec
}

// NewMetrics constructs and registers the orphan-cleanup collectors.
// reg.MustRegister panics on a duplicate registration (standard
// prometheus behavior) — that surfaces a double-wire bug at startup
// rather than silently dropping a counter.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Candidates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ach_orphan_cleanup_candidates_total",
			Help: "Total LiteLLM keys identified as true ACH orphans (ach_key_id present, older than floor, not in active set).",
		}),
		Revoked: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ach_orphan_cleanup_revoked_total",
			Help: "Total LiteLLM keys actually revoked by orphan-cleanup (excludes dry-run).",
		}),
		Skipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ach_orphan_cleanup_skipped_total",
			Help: "Total orphan revocations skipped, partitioned by reason.",
		}, []string{"reason"}),
	}
	reg.MustRegister(m.Candidates, m.Revoked, m.Skipped)
	// Expose the skipped family at 0 for every reason so dashboards see
	// the series before the first event.
	for _, r := range []string{
		SkipReasonDryRun, SkipReasonEmptyActiveSet, SkipReasonCircuitBreaker,
		SkipReasonLiteLLMUnreachable, SkipReasonRevokeFailed,
	} {
		m.Skipped.WithLabelValues(r).Add(0)
	}
	return m
}

// --- nil-guarded instrumentation helpers (called from TickOnce) ---

func (r *Runnable) mCandidates(n int) {
	if r.Metrics != nil {
		r.Metrics.Candidates.Add(float64(n))
	}
}

func (r *Runnable) mRevoked() {
	if r.Metrics != nil {
		r.Metrics.Revoked.Inc()
	}
}

func (r *Runnable) mSkipped(reason string, n int) {
	if r.Metrics != nil {
		r.Metrics.Skipped.WithLabelValues(reason).Add(float64(n))
	}
}
