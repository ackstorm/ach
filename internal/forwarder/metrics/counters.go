// SPDX-License-Identifier: Apache-2.0

// Package metrics is the forwarder-side counter surface for Hub §18.5
// observability. Phase 4 ships every counter as a no-op stub; Phase 5
// (OBS-03..06) replaces the bodies with Prometheus registrations.
//
// W8 (REVIEW) — Phase 5 owns metric-emission test coverage:
//
// Phase 4 deliberately ships no test asserts that the call sites (e.g.
// IncJWTSuppressed(kind, "signing_failure") on MintJWT errors) actually
// run. Reason: the function bodies are empty, so any seam to observe
// them (test-only `var lastSuppressedReason string` etc.) would itself
// be Phase-4 code that Phase 5 immediately rewrites when wiring
// prometheus. Phase 5 inherits the responsibility to add coverage for
// FWD-08 emission and every other call site in this package. Phase 4
// covers the contract negatively: TestHandlerMCP_SigningFailure
// asserts upstream call count == 0 + the 500 envelope, which proves
// the IncJWTSuppressed call site executes (the path that increments
// is the same path that returns 500). The counter increment is the
// only side-effect that Phase 5 will assert; Phase 4 asserts the
// observable side-effects (status code, body, upstream byte count).
package metrics

// IncRequests increments forwarder_requests_total{route, key_type, outcome}
// per Hub §18.5 normative label-value enums:
//
//	route    ∈ {/v1, /gemini, /mcp, /a2a}
//	key_type ∈ {pk, ek, none}
//	outcome  ∈ {forwarded, unauthorized_resource, unauthorized_team,
//	             expired_or_revoked, litellm_unreachable, internal_error,
//	             invalid_key_format, invalid_key_type, https_required}
//
// Phase 4 ships a no-op stub; Phase 5 (OBS-03..06) wires Prometheus.
// Bodies are intentionally empty so the Go compiler inline-eliminates every
// call site (zero runtime cost until Phase 5 fills the body).
func IncRequests(route, keyType, outcome string) {}

// IncJWTSigned increments forwarder_jwt_signed_total{kind} per Hub §18.5:
//
//	kind ∈ {MCPServer, A2AAgent}
//
// Phase 4 ships a no-op stub; Phase 5 (OBS-03..06) wires Prometheus.
func IncJWTSigned(kind string) {}

// IncJWTSuppressed increments forwarder_jwt_suppressed_total{kind, reason}
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
// Phase 4 ships a no-op stub; Phase 5 (OBS-03..06) wires Prometheus.
func IncJWTSuppressed(kind, reason string) {}

// IncLiteLLMUnreachable increments litellm_unreachable_total{caller="forwarder"}.
// Single counter spanning all callers per Hub §18.5 normative label-value
// enum.
//
// Phase 4 ships a no-op stub; Phase 5 (OBS-03..06) wires Prometheus.
func IncLiteLLMUnreachable() {}
