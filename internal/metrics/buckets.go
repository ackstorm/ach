// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// ForwarderDurationBuckets is the histogram bucket set for
// ach_forwarder_request_duration_seconds. Per Phase 5 D-11, the forwarder
// is a thin proxy in front of LiteLLM / upstream MCP+A2A backends; the
// observable forwarder latency tail beyond 10 seconds is upstream's
// latency, not the forwarder's. SSE long-poll requests deliberately
// fall into the +Inf bucket — they are not a useful latency signal for
// this metric.
//
// = prometheus.DefBuckets = {0.005, 0.01, 0.025, 0.05, 0.1, 0.25,
// 0.5, 1, 2.5, 5, 10}.
var ForwarderDurationBuckets = prometheus.DefBuckets

// ContentServiceDurationBuckets is the histogram bucket set for
// ach_content_service_request_duration_seconds. Per Phase 5 D-11, the
// content service streams artifact tarballs whose body size can drive
// the observed latency well past 10 seconds. The DefBuckets tail
// (10s) is extended with 30s and 60s bins so that a multi-megabyte
// tarball stream does not collapse into a single +Inf bucket and lose
// all p99/p999 signal.
//
// The 60s ceiling matches the http.Server.WriteTimeout=0 policy in
// cmd/ach/cmd/content_service.go (D-Discretion in 05-CONTEXT): no
// server-side write deadline; client-disconnect detection is via
// Request.Context() cancellation. Tail observations above 60s
// continue to land in +Inf, which is the correct signal for "tarball
// is genuinely too large or upstream link is slow".
var ContentServiceDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}
