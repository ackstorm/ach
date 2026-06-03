// SPDX-License-Identifier: Apache-2.0

// Package envkeys ships the four HTTP handlers + chi router subtree
// for the /platform/env-keys endpoint family per Hub §15.5:
//
//   - POST   /platform/env-keys           — §8.2 8-step ek- create flow.
//   - GET    /platform/env-keys           — paginated list (caller-scoped
//     non-admin; admin override via ?owner_email=).
//   - GET    /platform/env-keys/{key_id}  — single-row read with ekid_
//     prefix gate + owner check.
//   - DELETE /platform/env-keys/{key_id}  — §8.5 LiteLLM-first revoke;
//     204 No Content only after LiteLLM ack.
//
// Discipline:
//
//   - Plaintext-once invariant. The bearer plaintext (ek-<64>) is
//     generated server-side via internal/keys.NewBearer in CreateHandler
//     and returned EXACTLY ONCE in the POST response body. It is NEVER
//     written to the DB (only the credhash hex), NEVER logged, NEVER
//     emitted in audit records, NEVER cached, and NEVER returned by
//     ListHandler / GetHandler / RevokeHandler. The only other handler
//     in the codebase that emits plaintext is the SSO callback (Plan
//     03-07 — pk-); this package is the second and final emitter.
//
//   - Asymmetric revocation (KEY-08, D-15). RevokeHandler runs §8.5
//     verbatim: read row → call litellm.RevokeKey FIRST → flip DB row
//     → DEL Redis. LiteLLM is the load-bearing barrier; the DB row
//     stays 'active' until LiteLLM acks so a retry retries cleanly.
//     The grep gate in PLAN.md's acceptance criteria asserts the
//     deps.LiteLLM.RevokeKey line number precedes the
//     db.RevokeEnvironmentKey line number in handler.go.
//
//   - LiteLLM compensation on Create-path INSERT failure (D-12 step 7).
//     KeyGenerate runs OUTSIDE any DB transaction; if the subsequent
//     InsertEnvironmentKey fails with a non-recoverable error the
//     handler calls deps.LiteLLM.RevokeKey on the LiteLLM-side token
//     to avoid orphaning the LiteLLM key. The compensation runs once
//     under context.Background() so caller cancellation cannot
//     interrupt the cleanup.
//
//   - WARN-03 collision retry policy. ekid_ PK collisions (key_id
//     UNIQUE violation on environment_keys_pkey) are retried ONCE with
//     a freshly generated keys.NewKeyID(keys.PrefixEkid), reusing the
//     same plaintext + credential_hash + LiteLLM token. credential_hash
//     UNIQUE collisions (1 in ~2^128 — astronomically unlikely) are
//     treated as hard failures and surface 500 db_insert_failed with
//     LiteLLM compensation, no retry. The retry distinguishes the two
//     constraint violations by pg constraint-name match.
//
//   - WARN-06 team-membership lookup. The §8.2 step-4 caller-team
//     intersection check imports teams.LookupCallerTeams from
//     internal/platformapi/teams (single shared helper). This package
//     does NOT define its own lookupCallerTeams — the static-analysis
//     grep gate `grep -nE 'func lookupCallerTeams\(' .` returns ZERO
//     matches here by design.
//
//   - Caller-type discipline. Every handler in this file gates on
//     keyCtx.KeyType == keys.PrefixPk before doing any other work; ek-
//     callers receive 401 invalid_key_type without further side
//     effects (no DB, no LiteLLM, no Redis). Management endpoints are
//     pk--only per API-11.
//
//   - DisallowUnknownFields. POST request bodies are parsed via
//     json.Decoder with DisallowUnknownFields() set; any unknown field
//     yields 400 invalid_argument BEFORE the §8.2 flow runs (D-16).
//
// External callers wire this package via Mount(deps) inside the chi
// route setup in Plan 03-11's cmd/platform-api/server.go.
package envkeys
