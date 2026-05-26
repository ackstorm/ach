// SPDX-License-Identifier: Apache-2.0

// Package hydrate hosts the POST /platform/hydrate endpoint (Plan 03-09).
// This is the CLI's primary read endpoint — every `ach hydrate
// --environment <env>` call in Phase 6 + 7 lands here.
//
// Contract (Hub §15.1, §15.2 / D-16, D-17 / API-03, API-04):
//
//   - Accepts both pk_ and ek_ callers.
//   - Request body is strict JSON: {"environment": "<name>"} —
//     json.Decoder.DisallowUnknownFields() rejects extras with
//     400 invalid_argument.
//   - For pk_: body.environment is REQUIRED; missing -> 400 missing_environment
//     (audit OutcomeMissingEnvironment).
//   - For ek_: body.environment is OPTIONAL; mismatch with keyCtx.Environment
//     -> 403 wrong_environment (audit OutcomeWrongEnvironment).
//   - pk_ team-intersection check via teams.LookupCallerTeams; empty
//     intersection -> 403 unauthorized_team.
//   - Unknown environment -> 404 environment_not_found.
//   - Terminating Environments STILL serve hydrate (API-03 v9; drain semantics
//     are Phase 5 CS-09 concern).
//   - Response carries schemaVersion "v1alpha1" verbatim; runtime + context
//     blocks ALWAYS present with empty slices [] (not null, not absent) when
//     the underlying Environment has none.
//   - Each runtime item carries {id, endpoint}; runtime endpoints are
//     constructed against deps.BaseURL — Phase 3 freezes these shapes (Phase 4
//     Forwarder may extend prefixes):
//     models     -> ${BaseURL}/v1
//     mcpServers -> ${BaseURL}/mcp/<name>
//     a2aAgents  -> ${BaseURL}/a2a/<name>
//   - Each context item carries {name, id, downloadUrl}; downloadUrl is the
//     §15.6 Content Service URL:
//     ${BaseURL}/content/<kind>/<name>   (kind ∈ prompt|plugin|artifact)
//   - `id` is the resource name (NOT a CRD UID) — names are stable across
//     reconciles; UIDs change on delete+recreate.
//
// Plaintext NEVER appears in the response (Specifics block grep gate).
// Hydrate is read-only over the informer-cached Environment + computed URLs;
// no bearer values are in the read path.
package hydrate
