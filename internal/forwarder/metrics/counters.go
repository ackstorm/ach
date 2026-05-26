// SPDX-License-Identifier: Apache-2.0

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
//	reason ∈ {no_policy, policy_opt_out, signing_failure}
//
// Phase 4 ships a no-op stub; Phase 5 (OBS-03..06) wires Prometheus.
func IncJWTSuppressed(kind, reason string) {}

// IncLiteLLMUnreachable increments litellm_unreachable_total{caller="forwarder"}.
// Single counter spanning all callers per Hub §18.5 normative label-value
// enum.
//
// Phase 4 ships a no-op stub; Phase 5 (OBS-03..06) wires Prometheus.
func IncLiteLLMUnreachable() {}
