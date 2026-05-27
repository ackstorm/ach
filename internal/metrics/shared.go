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
// Label-value enum (§18.5 normative):
//
//	caller ∈ {"forwarder", "content_service", "platform_api", "operator"}
//
// Cardinality is bounded at 4 — adding a fifth caller requires
// updating the §18.5 spec table first.
func MustRegisterLitellmUnreachable(reg *prometheus.Registry) *prometheus.CounterVec {
	return MustRegisterLitellmUnreachableOn(reg)
}

// MustRegisterLitellmUnreachableOn is the prometheus.Registerer-shaped
// variant of MustRegisterLitellmUnreachable. Operator wiring (Plan
// 05-06 Task 5) requires this overload because
// sigs.k8s.io/controller-runtime/pkg/metrics.Registry is typed as a
// RegistererGatherer interface (prometheus.Registerer +
// prometheus.Gatherer), NOT a concrete *prometheus.Registry. The
// content-service / forwarder / platform-api wires (Tasks 1, 3, 4) use
// the *prometheus.Registry-shaped wrapper above; the operator wires
// through this Registerer-shaped variant.
//
// Same identity, same label dimension, same panic-on-double-register
// semantics — both functions construct the same CounterVec; the only
// difference is the Register call site.
func MustRegisterLitellmUnreachableOn(reg prometheus.Registerer) *prometheus.CounterVec {
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
