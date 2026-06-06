// SPDX-License-Identifier: Apache-2.0

// Package orphan is the Hub §18.4 LiteLLM orphan-key cleanup loop (OP-15, D-15/D-16/D-18).
//
// The single exported type Runnable is a controller-runtime manager.Runnable
// that ticks every Interval (default 1h, minimum 5m per OP-15 / D-15; the
// interval is validated externally by the caller in cmd/ach/cmd/operator.go via
// MustEnvDurationAtLeast). NewRunnable accepts a pre-validated time.Duration
// and trusts the caller — this package does not re-validate.
//
// # Ownership model (the load-bearing invariant)
//
// The loop revokes ONLY keys ACH minted. An ACH-minted LiteLLM key carries
// ach_key_id in its per-key metadata (stamped at mint in
// internal/platformapi/auth/sso.go and internal/platformapi/envkeys/handler.go);
// that value is in the key_id namespace (pkid_*/ekid_*). A key WITHOUT
// ach_key_id is foreign (manual dashboard, Terraform tf-*, token-factory) and
// is NEVER revoked. The opaque LiteLLM token is used only as the revoke
// handle, never as the membership key — confusing the two was the original
// namespace-mismatch bug that revoked every key fleet-wide.
//
// # Per-tick procedure (two-pass)
//
//  1. List ACH-managed litellm_user_id set (db.ListACHManagedLitellmUsers).
//  2. List active ACH key_id set (db.ListActiveACHKeyIDs) → achKeySet.
//  3. Pass 1 — per managed user, ListUserKeys via litellm.Client (a
//     LiteLLM-unreachable error aborts the tick cleanly with ONE audit event,
//     D-18 outcome="litellm_unreachable"). For each returned key, collect it
//     as a true-orphan candidate iff: it is ≥ OrphanAgeFloor (10 min) old
//     (race defender), it carries a non-empty ach_key_id (ownership gate), and
//     that ach_key_id is NOT in achKeySet (ACH no longer tracks it).
//  4. Guards (see Defense-in-depth below) inspect the FULL candidate set.
//  5. Pass 2 — revoke each candidate by its opaque token via
//     litellm.Client.RevokeKey, emitting one audit event per outcome
//     (D-18 "revoked" / "revoke_failed"). A single revoke failure does NOT
//     abort the tick — sibling candidates may still be revokable.
//
// # Defense-in-depth (a fail-open whole-fleet revoke was the incident)
//
//   - B3 dry-run (Runnable.DryRun, env ACH_ORPHAN_CLEANUP_DRY_RUN): logs
//     "WOULD revoke" per candidate + counts skipped{dry_run}, never calls
//     RevokeKey. The guard branches below also emit the dry-run preview so an
//     operator can inspect exactly the batches those guards would abort.
//   - B1 empty-active-set fail-safe: an empty achKeySet with ≥1 ACH-owned
//     candidate skips the whole tick (skipped_empty_active_set). See the
//     KNOWN LIMITATION note on Runnable.TickOnce.
//   - B2 circuit-breaker (Runnable.MaxRevoke, env ACH_ORPHAN_CLEANUP_MAX_REVOKE,
//     default 10): a tick with more candidates than the cap aborts revocation
//     (skipped_circuit_breaker) — a double-digit batch is itself the alarm.
//   - B5 metrics: ach_orphan_cleanup_{candidates,revoked}_total and
//     ach_orphan_cleanup_skipped_total{reason} on the operator /metrics.
//
// # Test seams
//
// Runnable's ListUsers and ListKeyIDs fields are function-typed (default
// db.ListACHManagedLitellmUsers / db.ListActiveACHKeyIDs). Unit tests override
// them with in-memory stubs to exercise TickOnce without a real Postgres;
// production code never touches them after NewRunnable wires the defaults.
//
// # Audit event shape (D-18)
//
//	slog.Info("operator.orphan-cleanup",
//	    "target.kind", "litellm_key",      // or "tick" for tick-level outcomes
//	    "target.name", token,              // omitted for tick-level events
//	    "outcome", "revoked"               // revoked | revoke_failed |
//	                                       // litellm_unreachable |
//	                                       // skipped_empty_active_set |
//	                                       // skipped_circuit_breaker
//	    "user_id", userID,
//	)
//
// The audit logger (internal/audit.NewLogger) injects audit=true at the
// record top level — call sites do NOT pass that attribute. No plaintext
// alias / bearer / error text is ever attached to an audit event.
package orphan
