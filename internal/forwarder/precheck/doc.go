// SPDX-License-Identifier: Apache-2.0

// Package precheck implements the Forwarder per-route authorization step
// described in FWD-03 / Hub §5.1 step-4. CheckMCP and CheckA2A determine
// whether an authenticated request to /mcp/<name> or /a2a/<name> may
// proceed to the LiteLLM proxy.
//
// Two key types are handled distinctly:
//
//   - ek_  O(1) cached read of the bound Environment; <name> MUST appear
//     in spec.runtime.{mcpServers|a2aAgents}. Terminating envs fail
//     closed (D-15 pre-decision: narrow error surface).
//   - pk_  caller's LiteLLM teams (via keystore.TeamsResolver) MUST
//     intersect non-empty with AuthorizedTeams of at least one
//     Environment whose runtime list contains <name>. LiteLLM
//     unreachable maps to ErrLiteLLMUnreachable (Forwarder → 503).
//
// precheck reads env.Spec only — never env.Status — and never signs a
// JWT or invokes LiteLLM directly. The caller (Plan 04-07 handlers) is
// responsible for translating sentinel errors into HTTP outcomes.
package precheck
