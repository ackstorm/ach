---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 08
subsystem: operator
tags: [orphan-cleanup, litellm, runnable, manager.Runnable, hub-18.4, D-15, D-16, D-18, OP-15]

# Dependency graph
requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: |
      Plan 02-01 widened litellm.Client interface (ListUserKeys, RevokeKey);
      Plan 02-03 db.ListACHManagedLitellmUsers + isTransientPgErr;
      Plan 02-04 audit.NewLogger (D-17 audit=true predicate).
provides:
  - "internal/orphan package: Runnable + NewRunnable + Start(ctx) + TickOnce(ctx)"
  - "OrphanAgeFloor const (10 min) + OutcomeSuccess/OutcomeLiteLLMUnreachable/OutcomeRevokeFailed enum"
  - "db.ListActiveACHKeyIDs helper (UNION + DISTINCT over active pkid_/ekid_ key_ids)"
  - "Function-typed DB test seams (ListUsers, ListKeyIDs) — unit tests stub without testcontainers"
affects:
  - Plan 02-09 (cmd/operator/main.go — calls orphan.NewRunnable(realLiteLLM, dbPool, auditLog, orphanInterval, log) + mgr.Add(orphanRunnable))

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Function-typed test seams on a Runnable struct: ListUsers / ListKeyIDs as field-level func types defaulted to package helpers — preserves the production wiring while allowing unit tests to stub DB calls without spinning a Postgres container"
    - "manager.Runnable lifecycle mirrored from Plan 02-07 Snapshotter (ticker + ctx.Done loop + V(1) no-op logging on empty state) but with NO initial tick on Start (thundering-herd defense for multi-replica futures)"
    - "Hub §18.4 abort-on-unreachable: a single per-tick audit event with target.kind='tick' (not target.name) characterizes the whole abort; revoke failures emit per-key target.kind='litellm_key' events but do NOT abort the tick"
    - "D-18 audit event shape verification via top-level allow-list assertion (TestRunnable_AuditEventShape) — forbids leakage of key_alias / credential / body fields even as the implementation evolves"

key-files:
  created:
    - internal/db/active_keys.go (ListActiveACHKeyIDs + godoc with Phase 3 follow-up)
    - internal/db/active_keys_test.go (//go:build integration — 4 tests)
    - internal/orphan/runnable.go (Runnable struct + Start + TickOnce + constants)
    - internal/orphan/doc.go (package docstring covering D-15/D-16/D-18 + Phase 2 invariant + Phase 3 follow-up)
    - internal/orphan/runnable_test.go (9 tests under -race)
  modified: []

key-decisions:
  - "Function-typed test seams over interface-based DB mocking: the db helpers take *pgxpool.Pool as their first arg which makes a clean interface ugly. Field-level `ListUsers func(...) ...` / `ListKeyIDs func(...) ...` defaulted in NewRunnable preserves the production wiring at the construction site while letting tests override the fields directly — zero ceremony, no test-only types in the production package."
  - "NO initial tick on Start (deliberate divergence from Snapshotter.Start which DOES tick immediately): the Snapshotter populates atomic.Pointer state needed by the very first reconcile, so cold-start data is load-bearing. The orphan loop has no consumer of its 'state' — it just makes upstream calls — so an immediate tick on operator startup would create a thundering-herd in any future multi-replica deployment. First orphan cleanup happens at t+Interval per Hub §18.4 cadence."
  - "Revoke failures do NOT abort the tick: per D-18 the audit event captures the actual outcome ('revoke_failed' is its own first-class outcome), so the failure is recorded and operators can investigate. Aborting on a single per-key failure would starve sibling users whose orphans are revokable. Only ListUserKeys failures abort (Hub §18.4 'LiteLLM-unreachable' definition is per-tick, not per-key)."
  - "audit event for abort uses target.kind='tick' (not 'litellm_key') and OMITS target.name: the abort characterizes the whole tick, not a specific key; misclassifying it under target.kind='litellm_key' would conflate per-key outcomes with per-tick outcomes in downstream log filtering. The user_id of the failing user is included as a per-event attribute for diagnosis."
  - "key_alias is logged via the operational logger (r.Log.Info) but NEVER in the audit event: the audit channel is high-signal / compliance-relevant and key_alias is user-chosen text that could carry sensitive substrings (Threat T-02-08-07). TestRunnable_AuditEventShape enforces an allow-list of top-level keys so the alias-leakage regression cannot reappear silently."

patterns-established:
  - "Two-layer audit safety: (1) audit.NewLogger handler injects audit=true; (2) per-emission code passes ONLY the documented D-18 fields. Tests assert BOTH the required fields are present AND a forbidden-key allow-list is enforced."
  - "Same go fmt curly-quote normalization observed in Plan 02-03 — `'` → `’` in active_keys.go docstring. Pre-applied by `make test-integration` pipeline; benign cosmetic change kept."

requirements-completed:
  - OP-15

# Metrics
duration: ~10min
completed: 2026-05-17
---

# Phase 2 Plan 08: Orphan LiteLLM Key Cleanup Runnable Summary

**Two new packages (internal/orphan + the active_keys helper in internal/db) implementing Hub §18.4 / D-15 / D-16 / D-18 — a controller-runtime manager.Runnable that ticks every Interval, enumerates ACH-managed LiteLLM users, lists per-user keys, identifies orphans (≥10min old + absent from active ACH key_id set), revokes them, and emits a single D-18-shaped audit event per outcome. LiteLLM-unreachable aborts the tick cleanly with ONE audit event; revoke failures emit per-key audit events but do NOT abort. Function-typed test seams (ListUsers / ListKeyIDs) allow 9 unit tests under -race without a testcontainers Postgres dependency.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-05-17T08:08:01Z
- **Completed:** 2026-05-17T08:18:04Z (approx — wall clock during this SUMMARY write)
- **Tasks:** 2 / 2
- **Files created:** 5 (1 db helper + 1 db integration test + 1 Runnable + 1 doc + 1 unit test)
- **Files modified:** 0

## Accomplishments

- **internal/db/active_keys.go** ships `ListActiveACHKeyIDs` — the active-key membership test the Runnable uses to distinguish orphan from currently-tracked. Mirrors Plan 02-03's `ListACHManagedLitellmUsers` UNION + DISTINCT pattern; no NULL/empty filter needed because `key_id` is the PRIMARY KEY with `LIKE 'pkid_%' / 'ekid_%'` CHECK constraints. 4 integration tests pass under `make test-integration` (38.5s including the Phase 2 baseline 13 tests).
- **internal/orphan/runnable.go** ships the `Runnable` type implementing `controller-runtime`'s `manager.Runnable`. Per-tick procedure executes Hub §18.4 exactly: list users → list active ACH key_ids → per user `ListUserKeys` → filter orphans (≥10min old AND key_id absent from active set) → revoke + audit. The `OrphanAgeFloor` constant (10 minutes) is the race defender; the `Outcome*` constants enforce stable D-18 outcome strings.
- **9 unit tests under `-race`** pass in 1.04s without any Postgres / Docker dependency, exercising every branch enumerated in the plan: empty-user no-op, single-orphan success, too-new skip, non-orphan skip, LiteLLM-unreachable abort, revoke-failure-continues-tick, multi-user abort-on-first-failure, exact D-18 audit shape (allow-list enforced), ctx-cancel lifecycle.
- **Audit event shape verified verbatim against D-18**: `msg="operator.orphan-cleanup"`, `target.kind=litellm_key` (or `tick` for the abort), `target.name=<keyID>` (omitted for the abort), `outcome ∈ {success, litellm_unreachable, revoke_failed}`, `user_id=<userID>`, plus `audit=true` injected by `audit.NewLogger`. The shape-verification test asserts that `key_alias`, `credential_hash`, `bearer`, `body`, `header`, and `err` are NEVER present in the audit event (enforced via a forbidden-key list AND a positive allow-list).

## Task Commits

1. **Task 1 — db.ListActiveACHKeyIDs + integration tests** — `cf02894` (feat)
2. **Task 2 — internal/orphan Runnable + doc + unit tests** — `8f7dedb` (feat)

**Plan metadata commit:** _(pending after this SUMMARY)_

## Files Created/Modified

### `internal/db/active_keys.go` (97 lines)

- `ListActiveACHKeyIDs(ctx, pool) ([]string, error)` returning the DISTINCT union of `personal_keys.key_id ∪ environment_keys.key_id WHERE status='active'`.
- Returns `([]string{}, nil)` on zero matches (never `nil`); pgconn class 08/57 errors propagate raw for caller-side backoff; other errors wrap via `fmt.Errorf("db: ListActiveACHKeyIDs: %w", err)`.
- Godoc documents the Phase 2 approximation semantic (Phase 2 has zero `pkid_`/`ekid_` rows so the membership test flags every LiteLLM `sk-...` key as orphan) and the Phase 3 follow-up (replace this helper with one that reads a `litellm_key_id` column).

### `internal/db/active_keys_test.go` (167 lines, `//go:build integration`)

Four tests reusing `setupPostgresForPhase2` and `mustExec` from `phase2_helpers_test.go` / `litellm_users_test.go`:

1. `TestListActiveACHKeyIDs_Empty` — fresh DB → `[]string{}` + `nil`.
2. `TestListActiveACHKeyIDs_PersonalKeysOnly` — two pk rows → both ids returned.
3. `TestListActiveACHKeyIDs_BothTablesDedupNotApplicable` — one pk + one ek (disjoint prefixes) → both returned.
4. `TestListActiveACHKeyIDs_ExcludesInactive` — revoked + expired (pk) and revoked (ek) excluded.

### `internal/orphan/runnable.go` (227 lines)

- `OrphanAgeFloor = 10 * time.Minute` (Hub §18.4 race defender).
- `OutcomeSuccess / OutcomeLiteLLMUnreachable / OutcomeRevokeFailed` const enum (D-18).
- `Runnable` struct with public `Client`, `DB`, `Audit`, `Interval`, `Log`, `ListUsers`, `ListKeyIDs` fields (the last two are function-typed test seams defaulted by `NewRunnable`).
- `NewRunnable(client, db, audit, interval, log) *Runnable` — wires defaults; trusts the interval as pre-validated by the caller (Plan 02-09 owns the ≥5m floor via `MustEnvDurationAtLeast`).
- `Start(ctx) error` — `manager.Runnable` lifecycle; ticker loop with `ctx.Done()` exit; NO initial tick (deliberate divergence from Snapshotter).
- `TickOnce(ctx)` — the Hub §18.4 per-tick procedure; exported for unit-test invocation.

### `internal/orphan/doc.go` (66 lines)

Package docstring covering: D-15/D-16/D-18 references, the per-tick procedure, Phase 2 invariant (empty user set every tick), Phase 3 follow-up (litellm_key_id column extension), test seams, and the D-18 audit event shape.

### `internal/orphan/runnable_test.go` (290 lines)

9 tests (all under `-race`): empty-user no-op, single-orphan success path, too-new-skip, non-orphan-skip, LiteLLM-unreachable abort (1 audit event), revoke-failure-continues (2 audit events, 0 abort), multi-user abort-on-first-failure (exactly 1 `ListUserKeys` call), exact D-18 audit shape with forbidden-key + allow-list enforcement, ctx-cancel lifecycle (Start returns nil within 1s of cancel).

The local `fakeLiteLLM` shadows the snapshot package's pattern (same field names where applicable; adds a `revokedKeys []string` slice and `revokeErr error` for the new methods).

## Decisions Made

- **Function-typed test seams over interface-based DB mocking** (load-bearing). The db helpers take `*pgxpool.Pool` as their first arg; wrapping them in an interface forces every test to define a stub `DBQueries` type. Field-level `ListUsers func(...) ...` + `ListKeyIDs func(...) ...` on the Runnable struct, defaulted by `NewRunnable` to the real db helpers, lets tests override the seam with one line (`r.ListUsers = func(...) { return []string{"u1"}, nil }`). Production callers never see the seam; the type signature is `*Runnable` either way.
- **NO initial tick on Start** (intentional divergence from Snapshotter). Snapshotter populates atomic-pointer state used by the very first reconcile; orphan loop has no first-tick consumer. An immediate tick on operator startup would thunder-herd if multiple operators ever restart simultaneously. The first cleanup happens at t+Interval per Hub §18.4 cadence.
- **Revoke failures emit `outcome=revoke_failed` per key and continue the tick.** Aborting on the first per-key failure would starve sibling users; D-18 explicitly catalogs `revoke_failed` as a first-class outcome, signaling that the operator wants per-key visibility.
- **Abort audit event uses `target.kind='tick'` (NOT `litellm_key`) and OMITS `target.name`.** The abort characterizes the whole tick, not a specific key. Conflating per-tick and per-key outcomes under the same `target.kind` would muddy downstream log filtering (which is the entire point of D-18's stable enum).
- **`key_alias` is logged via the operational logger but NEVER in the audit event.** The audit channel is high-signal / compliance-relevant; alias is user-chosen text that could carry sensitive substrings (T-02-08-07). The test's `forbidden` list + positive allow-list both enforce this — even a future "convenience" edit would fail tests.
- **Audit event shape verification via allow-list, not just required-key assertions.** `TestRunnable_AuditEventShape` checks both "required fields present" AND "only the documented top-level fields exist". This catches the regression class where a future edit adds a debugging field that accidentally carries plaintext.

## Deviations from Plan

None — plan executed exactly as written.

The plan's `<action>` blocks were followed precisely:

- `ListActiveACHKeyIDs` SQL matches the literal in the plan (UNION + DISTINCT, status='active' on both arms, no NULL/empty filter); 4 integration tests match the enumerated names (Empty / PersonalKeysOnly / BothTablesDedupNotApplicable / ExcludesInactive).
- `Runnable` struct surface matches the `<interfaces>` block verbatim (Client / DB / Audit / Interval / Log fields; OrphanAgeFloor constant; NewRunnable / Start / TickOnce method set).
- `TickOnce` implements the documented procedure exactly: ListUsers → empty-skip → ListKeyIDs → map-build → per-user ListUserKeys → abort-on-error → per-key cutoff + active-set filter → revoke + audit.
- 9 tests match the plan's enumerated names character-for-character (EmptyUsers / OneOrphan / SkipTooNew / SkipNonOrphan / LiteLLMUnreachable / RevokeFailureContinues / MultipleUsers_OneFailsListUserKeys / AuditEventShape / StartRespectsCtxCancel).
- Audit event shape matches D-18 verbatim: `target.kind` / `target.name` / `outcome` / `user_id`; abort uses `target.kind="tick"` and omits `target.name` exactly as the plan's `<action>` block writes it.

## Issues Encountered

- **`make test-integration` re-formats `internal/sources/http/fetcher_test.go`** (struct-literal field alignment) via the `go fmt` prerequisite. This is a pre-existing alignment issue in a file outside this plan's scope (Plan 02-02 surface). Per the SCOPE BOUNDARY rule, I reverted the change before committing and logged it to `.planning/phases/02-external-refs-marketplace-operator-reconciliation/deferred-items.md` for a future plan that legitimately edits the file. No functional change.
- A `go fmt` curly-quote normalization (`'` → `’`) was applied to one docstring in `internal/db/active_keys.go`; same pattern documented in Plan 02-03 SUMMARY, kept as a benign cosmetic change.

## Threat Model Coverage

All seven threats from the plan's `<threat_model>` block have implementation hooks:

- **T-02-08-01** (audit info disclosure) — `mitigate`: TestRunnable_AuditEventShape's forbidden-key + allow-list enforcement guarantees no `credential_hash` / `bearer` / `body` / `header` / `err` leak. (The success-path event explicitly excludes `err`; only failure events include it, and `err` carries upstream wire-error text — not plaintext key material — per LiteLLM API contract.)
- **T-02-08-02** (race: PK_ INSERT after achKeySet snapshot, before RevokeKey) — `mitigate`: `OrphanAgeFloor = 10 * time.Minute` defers revocation until the key is ≥10min old. A brand-new PK_ key is <1 second old; the race window is closed by the floor.
- **T-02-08-03** (audit event lost on crash) — `accept`: per-line JSON via `slog.NewJSONHandler` flushes per write; K8s containerd log pipe is per-line. Idempotency (re-tick) re-emits.
- **T-02-08-04** (DoS via 10000-keys-per-user) — `accept`: per-tick wall-clock budget bounds the work; next tick waits its turn.
- **T-02-08-05** (NoopClient returns empty → no orphans) — `accept`: Phase 1 / unit-test correct behavior; Plan 02-09 wires the real RESTClient.
- **T-02-08-06** (caller passes interval < OrphanAgeFloor) — `accept`: the floor is per-key, not per-tick; small intervals waste ticks but cannot revoke a too-new key.
- **T-02-08-07** (key_alias logged via r.Log.Info could echo sensitive substrings) — `mitigate`: alias is in the operational logger only; the audit event allow-list test asserts `key_alias` is absent.

## User Setup Required

None — Plan 02-09 will wire `cmd/operator/main.go` to call:

```go
orphanRunnable := orphan.NewRunnable(realLiteLLM, dbPool, auditLog, orphanInterval, log)
mgr.Add(orphanRunnable)
```

where `orphanInterval` is parsed via Plan 02-09's `MustEnvDurationAtLeast("ACH_ORPHAN_CLEANUP_INTERVAL", "1h", 5*time.Minute)`.

## Next Plan Readiness

- **Plan 02-09 (cmd/operator/main.go wire-up)** has the full surface it needs: `orphan.NewRunnable(realLiteLLM, dbPool, auditLog, orphanInterval, log)` returns the Runnable; `mgr.Add(orphanRunnable)` registers it. The `OrphanAgeFloor` constant is exported and Plan 02-09's `MustEnvDurationAtLeast` validation should use `5 * time.Minute` as the floor (Hub §18.4 / D-15), NOT `OrphanAgeFloor` — they're related but distinct (`OrphanAgeFloor` is the per-key age check; the env-var floor is the per-tick cadence).
- **Plan 03 (Platform API SSO landing)** is the first writer of `personal_keys.litellm_user_id` and `environment_keys.litellm_user_id`. Once those columns are populated, the Runnable's `userIDs` enumeration becomes non-empty on every tick; the per-user `ListUserKeys` calls start flowing; and the orphan detection becomes operationally meaningful. No code change in `internal/orphan` is required — the Phase 2 implementation is forward-compatible.
- **Threat surface:** no new network endpoints, auth paths, file access, or schema changes. The Runnable is a pure consumer of pre-existing surfaces (litellm.Client + db helpers + audit logger). No additional `threat_flag` entries.

## Self-Check

**Created files exist:**

- `internal/db/active_keys.go` — FOUND
- `internal/db/active_keys_test.go` — FOUND
- `internal/orphan/runnable.go` — FOUND
- `internal/orphan/doc.go` — FOUND
- `internal/orphan/runnable_test.go` — FOUND

**Commits exist on branch `worktree-agent-a9102571fccafa72b`:**

- `cf02894` (Task 1 — db.ListActiveACHKeyIDs) — FOUND
- `8f7dedb` (Task 2 — internal/orphan Runnable) — FOUND

**Verification commands (all six from `<verification>` block):**

1. `go build ./...` → exit 0 ✓
2. `go test ./internal/orphan/... -count=1 -race` → ok 1.04s ✓
3. `make test-integration TESTPATH=./internal/db/...` → ok 38.5s including 4 new `TestListActiveACHKeyIDs_*` tests ✓
4. `grep -c "operator.orphan-cleanup" internal/orphan/runnable.go` → 3 ✓
5. `grep -n "OrphanAgeFloor = 10 \* time.Minute" internal/orphan/runnable.go` → line 37 ✓
6. `grep -c "audit=true" internal/orphan/runnable.go` → 0 (audit logger injects it) ✓

**Phase boundaries untouched:** `git diff cf02894^..8f7dedb internal/snapshot/ internal/litellm/ internal/audit/ internal/db/litellm_users.go` returns no diff (Wave-1 packages unmodified).

## Self-Check: PASSED

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Plan: 08*
*Completed: 2026-05-17*
