// SPDX-License-Identifier: Apache-2.0

// Package metrics is the forwarder-side counter surface for Hub §18.5
// observability. Phase 4 shipped every counter as a no-op stub; Phase 5
// (this commit) ships the nil-tolerant delegation; bodies forward to
// internal/metrics.ForwarderCollectors set at cmd-level via
// InitCollectors. Phase 4 call sites are unchanged — D-19 thin-shim
// invariant.
//
// W8 (REVIEW) — Phase 5 owns metric-emission test coverage:
//
// Phase 4 deliberately shipped no test asserts that the call sites (e.g.
// IncJWTSuppressed(kind, "signing_failure") on MintJWT errors) actually
// run. Reason: the function bodies were empty, so any seam to observe
// them (test-only `var lastSuppressedReason string` etc.) would itself
// be Phase-4 code that Phase 5 immediately rewrites when wiring
// prometheus. Phase 5 inherits the responsibility to add coverage for
// FWD-08 emission and every other call site in this package; the
// nil-tolerant delegation here is paired with counters_test.go to
// regression-guard "forgot to call InitCollectors at startup → silent
// zero metrics" (T-05-06-06).
//
// Concurrency: the package-private `collectors` and `litellmUnreachable`
// vars are written ONLY at process start by InitCollectors (init.go).
// Read by every Inc* delegation on the hot path. NOT goroutine-safe
// for concurrent InitCollectors calls — production startup is
// single-threaded; tests use t.Cleanup to reset state between calls.
package metrics

import (
	coremetrics "github.com/ackstorm/ach/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// Package-private collector handles. Both nil until InitCollectors runs
// (init.go) — the four Inc* delegations check for nil and no-op in the
// absence of init, preserving Phase 4's zero-cost stub behavior for
// unit tests that don't wire the metrics layer.
var (
	collectors         *coremetrics.ForwarderCollectors
	litellmUnreachable *prometheus.CounterVec
)

// IncRequests increments ach_forwarder_requests_total{route, key_type, outcome}
// per Hub §18.5 normative label-value enums:
//
//	route    ∈ {/v1, /gemini, /mcp, /a2a}
//	key_type ∈ {pk, ek, none}
//	outcome  ∈ {forwarded, unauthorized_resource, unauthorized_team,
//	             expired_or_revoked, litellm_unreachable, internal_error,
//	             invalid_key_format, invalid_key_type, https_required}
//
// D-19 thin-shim: Phase 4 ships a no-op stub; Phase 5 (this commit) wires
// the nil-tolerant delegation to *metrics.ForwarderCollectors set via
// InitCollectors. Call sites in internal/forwarder/proxy/handlers.go
// stay byte-identical.
func IncRequests(route, keyType, outcome string) {
	if collectors != nil {
		collectors.IncRequest(route, keyType, outcome)
	}
}

// IncJWTSigned increments ach_forwarder_jwt_signed_total{kind} per Hub §18.5:
//
//	kind ∈ {MCPServer, A2AAgent}
//
// D-19 thin-shim: nil-tolerant delegation to ForwarderCollectors.IncJWTSigned.
func IncJWTSigned(kind string) {
	if collectors != nil {
		collectors.IncJWTSigned(kind)
	}
}

// IncJWTSuppressed increments ach_forwarder_jwt_suppressed_total{kind, reason}
// per Hub §18.5:
//
//	kind   ∈ {MCPServer, A2AAgent}
//	reason ∈ {no_policy, policy_opt_out, signing_failure, list_failure}
//
// list_failure is emitted by bip.ResolveWinner when the controller-runtime
// cache List call returns a transient error (cache desync, mid-rotation
// indexer, etc.). The runtime still fails open (no JWT mint) — the
// counter is the observability seam that distinguishes "no policy
// matches this target" from "we never got to check". Doc follow-up:
// add list_failure to the Hub §18.5 normative reason enum in the next
// phase planning.
//
// D-19 thin-shim: nil-tolerant delegation to
// ForwarderCollectors.IncJWTSuppressed.
func IncJWTSuppressed(kind, reason string) {
	if collectors != nil {
		collectors.IncJWTSuppressed(kind, reason)
	}
}

// IncLiteLLMUnreachable increments ach_litellm_unreachable_total{caller="forwarder"}.
// Single counter spanning all callers per Hub §18.5 normative label-value
// enum. The caller="forwarder" label is hidden inside the shim so the
// zero-arg signature stays compatible with every Phase 4 call site.
//
// D-19 thin-shim: nil-tolerant delegation to the shared
// ach_litellm_unreachable_total CounterVec registered by Plan 05-01's
// MustRegisterLitellmUnreachable.
func IncLiteLLMUnreachable() {
	if litellmUnreachable != nil {
		litellmUnreachable.WithLabelValues("forwarder").Inc()
	}
}

// ObserveRequestDuration observes ach_forwarder_request_duration_seconds
// {route, key_type, status_class} per Hub §18.5:
//
//	route        ∈ {/v1, /gemini, /mcp, /a2a}
//	key_type     ∈ {pk, ek, none}
//	status_class ∈ {1xx, 2xx, 3xx, 4xx, 5xx}
//
// Emitted by internal/forwarder/proxy observeDuration on every proxied
// route. Bucket layout is ForwarderDurationBuckets per D-11.
//
// D-19 thin-shim: nil-tolerant delegation to
// ForwarderCollectors.ObserveRequestDuration.
func ObserveRequestDuration(route, keyType, statusClass string, seconds float64) {
	if collectors != nil {
		collectors.ObserveRequestDuration(route, keyType, statusClass, seconds)
	}
}
