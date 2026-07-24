// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// ForwarderCollectors holds the four §18.5 normative forwarder
// collectors registered against a single prometheus.Registry. Phase 5
// D-09: typed methods accept only the §18.5 label-value enums; raw
// label strings NEVER appear at the call site. The Phase 4
// internal/forwarder/metrics counter-hook stubs are backed by these
// collectors in Plan 05-06 (the package's signatures stay verbatim;
// only the bodies change).
//
// Cardinality budget (§18.5):
//
//	ach_forwarder_requests_total{route, key_type, outcome}
//	  4 routes × 3 key_types × 9 outcomes = 108
//	ach_forwarder_request_duration_seconds{route, key_type, status_class}
//	  4 × 3 × 5 = 60 series × bucket_count
//	ach_forwarder_jwt_signed_total{kind}                    2 series
//	ach_forwarder_jwt_suppressed_total{kind, reason}        2 × 4 = 8 series
type ForwarderCollectors struct {
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	jwtSigned     *prometheus.CounterVec
	jwtSuppressed *prometheus.CounterVec
}

// NewForwarderCollectors constructs the four forwarder collectors and
// registers them against reg. Calling this function twice against the
// SAME registry panics (standard prometheus re-register behavior);
// callers must hold the returned pointer in a single process-scoped
// variable per Phase 5 D-09.
//
// reg MUST be non-nil — the implementation defers nil-check to the
// underlying reg.MustRegister panic (DO NOT add a guard; the panic
// surfaces wiring bugs at startup, not at first request).
func NewForwarderCollectors(reg *prometheus.Registry) *ForwarderCollectors {
	c := &ForwarderCollectors{
		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ach_forwarder_requests_total",
				Help: "Total forwarder requests, partitioned by route/key_type/outcome (Hub §18.5).",
			},
			[]string{"route", "key_type", "outcome"},
		),
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "ach_forwarder_request_duration_seconds",
				Help:    "Forwarder request duration seconds, partitioned by route/key_type/status_class (Hub §18.5).",
				Buckets: ForwarderDurationBuckets,
			},
			[]string{"route", "key_type", "status_class"},
		),
		jwtSigned: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ach_forwarder_jwt_signed_total",
				Help: "Total backend-identity JWTs successfully minted, partitioned by target kind (Hub §18.5).",
			},
			[]string{"kind"},
		),
		jwtSuppressed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ach_forwarder_jwt_suppressed_total",
				Help: "Total JWT mints skipped or failed, partitioned by kind and suppression reason (Hub §18.5).",
			},
			[]string{"kind", "reason"},
		),
	}
	reg.MustRegister(c.requests, c.duration, c.jwtSigned, c.jwtSuppressed)
	return c
}

// PreInitZeroSeries materializes one representative child series per
// collector at value 0 so the metric FAMILY is present on /metrics from
// process start — before any traffic — making the §18.5 exposition
// contract scrapeable immediately (and rate() gap-free). Production
// wiring (cmd/ach/cmd/forwarder.go) calls this once at startup; unit
// tests do NOT, so per-test series assertions stay unaffected. Label
// values are drawn from the §18.5 enums; the 0 counts are harmless.
func (c *ForwarderCollectors) PreInitZeroSeries() {
	c.requests.WithLabelValues("/v1", "pk", "forwarded").Add(0)
	c.duration.WithLabelValues("/v1", "pk", "2xx") // creates the 0-count histogram child
	c.jwtSigned.WithLabelValues("mcp").Add(0)
	c.jwtSuppressed.WithLabelValues("mcp", "no_bip").Add(0)
}

// IncRequest increments ach_forwarder_requests_total{route, key_type, outcome}
// per Hub §18.5 normative label-value enums:
//
//	route    ∈ {/v1, /gemini, /mcp, /a2a}
//	key_type ∈ {pk, ek, none}
//	outcome  ∈ {forwarded, unauthorized_resource, unauthorized_team,
//	            expired_or_revoked, litellm_unreachable, internal_error,
//	            invalid_key_format, invalid_key_type, https_required}
//
// Callers MUST pass only values from those enums; adding a new outcome
// requires editing the §18.5 spec table first. T-05-01-04 (cardinality
// DoS) is mitigated by this discipline.
func (c *ForwarderCollectors) IncRequest(route, keyType, outcome string) {
	c.requests.WithLabelValues(route, keyType, outcome).Inc()
}

// ObserveRequestDuration observes ach_forwarder_request_duration_seconds
// {route, key_type, status_class} per Hub §18.5:
//
//	route        ∈ {/v1, /gemini, /mcp, /a2a}
//	key_type     ∈ {pk, ek, none}
//	status_class ∈ {1xx, 2xx, 3xx, 4xx, 5xx}
//
// Bucket layout is ForwarderDurationBuckets (= prometheus.DefBuckets
// per D-11; tail beyond 10s is upstream LiteLLM's, falls into +Inf).
func (c *ForwarderCollectors) ObserveRequestDuration(route, keyType, statusClass string, seconds float64) {
	c.duration.WithLabelValues(route, keyType, statusClass).Observe(seconds)
}

// IncJWTSigned increments ach_forwarder_jwt_signed_total{kind} per Hub
// §18.5:
//
//	kind ∈ {MCPServer, A2AAgent}
//
// Emitted on successful BackendIdentityPolicy JWT mint inside the
// forwarder before upstream call (Phase 4 FWD-08).
func (c *ForwarderCollectors) IncJWTSigned(kind string) {
	c.jwtSigned.WithLabelValues(kind).Inc()
}

// IncJWTSuppressed increments ach_forwarder_jwt_suppressed_total{kind, reason}
// per Hub §18.5:
//
//	kind   ∈ {MCPServer, A2AAgent}
//	reason ∈ {no_policy, policy_opt_out, signing_failure, list_failure}
//
// list_failure is emitted when the BIP cache List call returns a
// transient error (cache desync, mid-rotation indexer, etc.) — the
// forwarder still fails open (no JWT mint) but this counter
// distinguishes "no policy matches this target" from "we never got
// to check".
func (c *ForwarderCollectors) IncJWTSuppressed(kind, reason string) {
	c.jwtSuppressed.WithLabelValues(kind, reason).Inc()
}
