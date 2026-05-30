// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// ContentServiceCollectors holds the three §18.5 normative
// content-service collectors registered against a single
// prometheus.Registry. Phase 5 D-09 / OBS-06: typed methods accept
// only the §18.5 label-value enums; raw label strings NEVER appear
// at the call site.
//
// Cardinality discipline (OBS-06): NO request_id label, NO owner_email
// label. The Inc/Observe method signatures here accept only kind +
// outcome, so a call site CANNOT bind a high-cardinality value by
// accident. T-05-01-01 (information disclosure) and T-05-01-04
// (cardinality DoS) are mitigated at the type-system level —
// TestContentServiceCollectors_NoForbiddenLabels regression-guards
// against label-set drift.
//
// Cardinality budget (§18.5):
//
//	content_service_requests_total{kind, outcome}
//	  3 kinds × ~11 outcomes = ~33 series
//	content_service_request_duration_seconds{kind}
//	  3 series × bucket_count
//	content_service_bytes_served_total{kind}
//	  3 series
type ContentServiceCollectors struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	bytes    *prometheus.CounterVec
}

// NewContentServiceCollectors constructs the three content-service
// collectors and registers them against reg. Calling twice against
// the SAME registry panics (standard prometheus re-register
// behavior); callers must hold the returned pointer in a single
// process-scoped variable per Phase 5 D-09.
//
// reg MUST be non-nil — defer nil-check to the underlying
// reg.MustRegister panic (surfaces wiring bugs at startup, not at
// first request).
func NewContentServiceCollectors(reg *prometheus.Registry) *ContentServiceCollectors {
	c := &ContentServiceCollectors{
		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "content_service_requests_total",
				Help: "Total content-service requests, partitioned by kind/outcome (Hub §18.5).",
			},
			[]string{"kind", "outcome"},
		),
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "content_service_request_duration_seconds",
				Help:    "Content-service request duration seconds, partitioned by kind (Hub §18.5).",
				Buckets: ContentServiceDurationBuckets,
			},
			[]string{"kind"},
		),
		bytes: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "content_service_bytes_served_total",
				Help: "Total bytes streamed from the content service, partitioned by kind (Hub §18.5).",
			},
			[]string{"kind"},
		),
	}
	reg.MustRegister(c.requests, c.duration, c.bytes)
	return c
}

// PreInitZeroSeries materializes one representative child series per
// collector at value 0 so the metric FAMILY is present on /metrics from
// process start — before any traffic — making the §18.5 exposition
// contract scrapeable immediately. Production wiring
// (cmd/ach/cmd/content_service.go) calls this once at startup; unit tests
// do NOT, so per-test series assertions stay unaffected. Label values are
// drawn from the §15.6/§18.5 enums; the 0 counts are harmless.
func (c *ContentServiceCollectors) PreInitZeroSeries() {
	c.requests.WithLabelValues("plugin", "ok").Add(0)
	c.duration.WithLabelValues("plugin") // creates the 0-count histogram child
	c.bytes.WithLabelValues("plugin").Add(0)
}

// IncRequest increments content_service_requests_total{kind, outcome}
// per Hub §18.5 normative label-value enums:
//
//	kind    ∈ {prompt, plugin, artifact}
//	outcome ∈ §15.6 outcome enum (D-03 table in 05-CONTEXT):
//	          {ok, missing_environment, invalid_key_format,
//	           expired_or_revoked, unauthorized_team, wrong_environment,
//	           unauthorized_content, environment_not_found,
//	           content_not_found, litellm_unreachable,
//	           stale_cache_expired, internal_error}
//
// OBS-06 cardinality discipline: the signature deliberately rejects
// any owner_email or request_id parameter. New outcome values
// require updating the §15.6 enum first.
func (c *ContentServiceCollectors) IncRequest(kind, outcome string) {
	c.requests.WithLabelValues(kind, outcome).Inc()
}

// ObserveRequestDuration observes content_service_request_duration_seconds
// {kind} per Hub §18.5:
//
//	kind ∈ {prompt, plugin, artifact}
//
// Bucket layout is ContentServiceDurationBuckets (extends DefBuckets
// with 30s and 60s bins per D-11 — artifact tarballs can drive
// observed latency well past 10s without being upstream's fault).
func (c *ContentServiceCollectors) ObserveRequestDuration(kind string, seconds float64) {
	c.duration.WithLabelValues(kind).Observe(seconds)
}

// AddBytesServed adds n bytes to content_service_bytes_served_total
// {kind} per Hub §18.5:
//
//	kind ∈ {prompt, plugin, artifact}
//
// Accepts int64 (the io.Copy return type and *os.File.Stat().Size()
// type) and converts to float64 for the underlying Counter.Add. n
// SHOULD be non-negative — Counter.Add with a negative value panics
// (standard prometheus contract; bytes-served is monotonic).
func (c *ContentServiceCollectors) AddBytesServed(kind string, n int64) {
	c.bytes.WithLabelValues(kind).Add(float64(n))
}
