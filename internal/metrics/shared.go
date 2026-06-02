// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// MustRegisterLitellmUnreachable registers the single
// litellm_unreachable_total CounterVec on reg and returns the
// pointer so the caller can hold it for per-request Inc calls.
//
// Per Phase 5 D-09 / OBS-05 / Hub §18.5: every Hub component that
// makes a LiteLLM call (forwarder during upstream proxying, content
// service during pk_ TeamsResolver miss-path lookups, platform-api
// during /platform/login Team enrichment, operator during BIP /
// Marketplace sync) increments THIS counter with its own caller
// label. ONE collector per process; calling this function twice on
// the same Registry panics (standard prometheus re-register
// behavior), so callers must hold the returned pointer in a single
// process-scoped variable.
//
// reg accepts any prometheus.Registerer — *prometheus.Registry and
// controller-runtime's RegistererGatherer both satisfy the interface,
// so one function serves all four call sites.
//
// Label-value enum (§18.5 normative):
//
//	caller ∈ {"forwarder", "content_service", "platform_api", "operator"}
//
// Cardinality is bounded at 4 — adding a fifth caller requires
// updating the §18.5 spec table first.
func MustRegisterLitellmUnreachable(reg prometheus.Registerer) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "litellm_unreachable_total",
			Help: "Total LiteLLM upstream-unreachable events, partitioned by Hub caller (§18.5).",
		},
		[]string{"caller"},
	)
	reg.MustRegister(c)
	return c
}
