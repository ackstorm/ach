// SPDX-License-Identifier: Apache-2.0

// Package orphan is the Hub §18.4 LiteLLM orphan-key cleanup loop (OP-15, D-15/D-16/D-18).
//
// The single exported type Runnable is a controller-runtime manager.Runnable
// that ticks every Interval (default 1h, minimum 5m per OP-15 / D-15; the
// interval is validated externally by the caller in cmd/operator/main.go via
// Plan 02-09's MustEnvDurationAtLeast helper). NewRunnable accepts a
// pre-validated time.Duration and trusts the caller — this package does not
// re-validate.
//
// Per-tick procedure (Hub §18.4 / D-16):
//
//  1. List ACH-managed litellm_user_id set from Postgres
//     (db.ListACHManagedLitellmUsers — Plan 02-03 helper).
//  2. List active ACH key_id set (db.ListActiveACHKeyIDs — Plan 02-08 helper).
//  3. For each managed user:
//     a. ListUserKeys via litellm.Client. A LiteLLM-unreachable error aborts
//     the tick cleanly with ONE audit event (D-18 outcome="litellm_unreachable").
//     b. For each returned key:
//     - skip if < OrphanAgeFloor (10 min) old per Hub §18.4 (race defender);
//     - skip if key_id appears in the active ACH key_id set;
//     - otherwise revoke via litellm.Client.RevokeKey + emit one audit
//     event per outcome (D-18 outcome="success" or "revoke_failed").
//
// A revoke failure does NOT abort the tick — sibling users may still have
// revokable orphans and are processed normally. Only ListUserKeys failures
// (Hub-defined "LiteLLM-unreachable") abort the whole tick; the next tick
// retries from a clean slate.
//
// Phase 2 invariant: personal_keys + environment_keys are both empty because
// Phase 3 introduces the first SSO/ek_ write paths. The Runnable's enumeration
// (db.ListACHManagedLitellmUsers) returns an empty user_id set on every tick;
// no LiteLLM calls or audit events are emitted in Phase 2 steady state. This
// is the expected behavior — exercising the empty-set path is part of the
// unit-test coverage.
//
// Phase 3 follow-up: Phase 3 will add a litellm_key_id column to personal_keys
// and environment_keys so the "absent from ACH active rows" membership test
// becomes a direct litellm_key_id comparison. Currently Phase 2's
// approximation flags every LiteLLM key as orphan since the active-set values
// are pkid_/ekid_ prefixed while LiteLLM key_ids are sk-... values that never
// match. The Runnable contract does not change — only db.ListActiveACHKeyIDs
// gets replaced by a more precise helper.
//
// Test seams: Runnable's ListUsers and ListKeyIDs fields are function-typed
// fields (default-pointed at db.ListACHManagedLitellmUsers and
// db.ListActiveACHKeyIDs). Unit tests override them with in-memory stubs to
// exercise TickOnce without spinning up a real Postgres container; production
// code never touches them.
//
// Audit event shape (D-18, Phase 2 scope is orphan revocations only):
//
//	slog.Info("operator.orphan-cleanup",
//	    "target.kind", "litellm_key",      // or "tick" for the unreachable abort
//	    "target.name", keyID,              // omitted for the abort event
//	    "outcome", "success"               // or "litellm_unreachable" / "revoke_failed"
//	    "user_id", userID,
//	)
//
// The audit logger (internal/audit.NewLogger) injects audit=true at the
// record top level — call sites do NOT pass that attribute.
package orphan
