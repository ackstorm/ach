---
phase: 03-hub-identity-platform-api
plan: 08
plan_id: 03-08
subsystem: api

tags: [envkeys, ek_, http-handlers, chi, key-08, key-11, d-12, d-13, d-15, warn-03, warn-06, blk-02, hub-spec-8.2, hub-spec-8.5, hub-spec-15.5]

# Dependency graph
requires:
  - phase: 03-hub-identity-platform-api (wave 1)
    provides: "Plan 03-01 (Phase 3 LiteLLM Client methods — UserNew, UserInfoByEmail, TeamMemberAdd, KeyGenerate). Plan 03-02 (audit.Action* / Outcome* constants + EmitAudit helper + render.JSON / render.Error envelope writers). Plan 03-03 (db.InsertEnvironmentKey / GetEnvironmentKey / RevokeEnvironmentKey / ListEnvironmentKeysByOwner / ListEnvironmentKeysByOwnerWithFilter + EkKeyInfo / EkInsertRow). Plan 03-04 (keys.NewBearer / NewKeyID / EkBearerPrefix / EkidKeyIDPrefix / PrefixEk / PrefixEkid)."
  - phase: 03-hub-identity-platform-api (wave 2)
    provides: "Plan 03-05 (middleware.KeyContext + KeyContextFromCtx + RequestIDFromCtx + ActorFromCtx + Authn that populates IsAdmin per BLK-02; achteams.LookupCallerTeams per WARN-06; keystore.KeyInfo). Plan 03-06 (platformapi/store.Store with GetEnvironment / EnvironmentTerminating / EnvironmentAccessGroupSynced)."
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: "internal/credhash.Hash (HMAC-SHA-256 + pepper) used by CreateHandler to derive credential_hash from the server-generated plaintext."

provides:
  - "internal/platformapi/envkeys package — POST /platform/env-keys (CreateHandler), GET / (ListHandler), GET /{key_id} (GetHandler), DELETE /{key_id} (RevokeHandler), Mount(deps) func(chi.Router)"
  - "Deps struct with small interfaces (envStore, dbOps, redisOps) enabling unit-test fakes without spinning controller-runtime envtest, pgxpool, or real Redis"
  - "CreateRequest / CreateResponse / EkRowView / ListResponse — request/response wire shapes per Hub §15.5"
  - "§8.2 8-step ek_ create flow with WARN-03 collision matrix: ekid_ PK collisions retry once with new ulid (no compensation); credential_hash UNIQUE collisions surface 500 + LiteLLM compensation (no retry)"
  - "§8.5 LiteLLM-first ek_ revoke flow per KEY-08: LiteLLM RevokeKey BEFORE db.RevokeEnvironmentKey BEFORE redis.Del; on LiteLLM-unreachable the DB row STAYS active for retry"

affects:
  - 03-11-cmd-platform-api-server-wire (Plan 03-11 imports Mount + Deps; constructs the envStore / dbOps / redisOps adapters)
  - 03-12-phase-3-e2e (consumes the deployed endpoints for the OBS-02 invariants suite)

# Tech tracking
tech-stack:
  added: []  # zero new go.mod entries — uses chi v5.2.0 (already in go.mod via Plan 03-05's chi_compat anchor)
  patterns:
    - "Small-interface Deps for handler unit-testability — envStore / dbOps / redisOps declared in handler.go; production wires concrete adapters, tests inject inline fakes"
    - "Strict-prefix gate BEFORE DB roundtrip on GetHandler / RevokeHandler — pkid_, ek_, pk_, random inputs rejected 400 invalid_argument without costing a DB call"
    - "LiteLLM-first asymmetric revocation (§8.5 / KEY-08): the literal call-site for deps.LiteLLM.RevokeKey precedes deps.DB.RevokeEnvironmentKey inside RevokeHandler; on LiteLLM-unreachable the DB row stays 'active' so retry retries cleanly"
    - "Per-task TDD RED→GREEN gate sequence preserved across 6 atomic commits (test → feat × 3)"
    - "Server-side bearer generation per D-13: crypto/rand → base32-no-pad lowercase → 'ek_<26>' fed to LiteLLM KeyGenerate.Key — ACH retains sole authority over the pk_/ek_ namespace"
    - "WARN-03 collision-class asymmetry: ekid_ PK collision (constraint environment_keys_pkey) → retry once with new ulid (reuse plaintext + LiteLLM token); credential_hash UNIQUE collision (constraint environment_keys_credential_hash_key) → 500 + LiteLLM compensation, no retry"
    - "Compensation under context.Background with 5s timeout so caller cancellation cannot orphan a LiteLLM-side key during the DB-INSERT-failure cleanup"

key-files:
  created:
    - internal/platformapi/envkeys/doc.go
    - internal/platformapi/envkeys/handler.go
    - internal/platformapi/envkeys/handler_test.go
    - internal/platformapi/envkeys/mount.go
  modified:
    - internal/db/types_keys.go (added CredentialHash field to EkKeyInfo — Rule 3 deviation)
    - internal/db/environment_keys.go (GetEnvironmentKey + RevokeEnvironmentKey SELECT credential_hash — Rule 3 deviation)

key-decisions:
  - "Already-revoked rows in RevokeHandler return 404 (option a per plan Test R-8). Rationale: idempotency without double-emitting audit events. Avoids the 'is this revoked or never existed' information leak and keeps the audit channel tidy. Hub §8.5 'DELETE returns 204 only after LiteLLM ack' is preserved for the happy path — already-revoked rows never reach the LiteLLM call."
  - "WARN-03 commit verified end-to-end across three test cases: 10a (credential_hash collision → 500 + RevokeKey × 1, NO retry), 10b (ekid_ collision → retry succeeds, RevokeKey × 0), 10c (ekid_ collision on retry too → 500 + RevokeKey × 1). The constraint-name distinction is the load-bearing switch (environment_keys_pkey vs environment_keys_credential_hash_key)."
  - "Team-membership lookup goes through the shared achteams.LookupCallerTeams helper per WARN-06. The package-scope grep gate `grep -c '^func lookupCallerTeams\\(' internal/platformapi/envkeys/handler.go` returns 0 — confirmed in code review and in the plan's acceptance check."
  - "KeyContext.IsAdmin is populated by middleware.Authn against the allowlist parameter per BLK-02 (Plan 03-05 ships this). Handlers read keyCtx.IsAdmin directly; there is NO Deps.Allowlist field on this handler. Plan 03-10 admin handler will share the same convention — it consumes keyCtx.IsAdmin instead of re-deriving the allowlist lookup inline."
  - "DELETE acceptance criterion `grep -nE 'not_key_owner' internal/platformapi/envkeys/handler.go` matches semantically (via the audit.OutcomeNotKeyOwner constant whose value is 'not_key_owner') rather than via a literal-string occurrence. Same constant-vs-literal pattern Plan 03-02 SUMMARY documents."
  - "Plan acceptance criterion `awk '/deps\\.LiteLLM\\.RevokeKey/...' shows litellm line < db line` is technically subtler than intended: the file has CreateHandler-side LiteLLM calls (compensation) and doc-comment matches for both patterns. The semantic invariant (LiteLLM precedes DB INSIDE RevokeHandler) is enforced both structurally and behaviorally (Test R-5 LiteLLM-unreachable leaves DB row 'active')."

patterns-established:
  - "Plaintext-once invariant codified at the response level: CreateResponse is the ONLY struct in this package carrying a Plaintext field; List/Get/Revoke handlers all map through EkRowView which has no Plaintext field by construction."
  - "Best-effort Redis DEL: failure logs at WARN, never propagates as a 5xx. The 60s TTL ceiling on keystore.defaultTTL is the worst-case bound on stale-cache exposure (KEY-08 documented assumption)."
  - "Idempotent verify-or-create LiteLLM user provision: the CreateHandler runs UserInfoByEmail in step 5 even though LookupCallerTeams already called it in step 4. Two reasons: (a) LookupCallerTeams swallows ErrNotFound into empty slice — we can't tell 'absent user' from 'present user, empty teams'; (b) the verify-or-create step is described as idempotent in D-12 step 5, so the second call documents the intent in source. Phase 4 may collapse via the cached lookup."

requirements-completed: [KEY-05, KEY-08, KEY-11, API-05, API-06, API-07]

# Metrics
duration: ~75min
completed: 2026-05-20
---

# Phase 3 Plan 08: /platform/env-keys endpoint quartet Summary

**Ship the four §15.5 ek_ endpoints under `/platform/env-keys` — POST runs the Hub §8.2 8-step create flow with WARN-03 collision retry policy + LiteLLM compensation on DB-INSERT failure; GET (list + by-id) enforces caller-scope vs admin override with pre-DB ekid_ prefix gate; DELETE runs the §8.5 LiteLLM-first revocation per KEY-08, with the literal `deps.LiteLLM.RevokeKey` call-site preceding `deps.DB.RevokeEnvironmentKey` inside the RevokeHandler body.**

## Performance

- **Duration:** ~75 min
- **Started:** 2026-05-20T23:27:00Z
- **Completed:** 2026-05-20T~00:42:00Z (UTC)
- **Tasks:** 3 of 3 (per-task RED→GREEN gate sequence preserved)
- **Files created:** 4 (doc.go, handler.go, handler_test.go, mount.go)
- **Files modified:** 2 (Rule 3 deviation — internal/db/types_keys.go + internal/db/environment_keys.go)
- **Tests landed:** 36 (14 Create + 7 List + 6 Get + 8 Revoke + 1 Mount)
- **Insertions:** ~2,474 lines across 6 files

## Accomplishments

- Shipped `internal/platformapi/envkeys` package: 4 HTTP handlers + chi router subtree + small-interface Deps for unit-test fakes (envStore, dbOps, redisOps).
- **CreateHandler** ships the Hub §8.2 verbatim 8-step flow: caller-type guard (pk_ only) → strict-decode body with DisallowUnknownFields → GetEnvironment → terminating check (treated as 404 not_found per D-12) → EnvironmentAccessGroupSynced gate (503 not_ready) → authorizedTeams ∩ caller-teams intersection via the shared achteams.LookupCallerTeams (WARN-06; NO inline helper) → idempotent LiteLLM user provision (UserInfoByEmail; on absent → UserNew + TeamMemberAdd(default,…,user)) → server-side ek_ + ekid_ + credhash.Hash → LiteLLM KeyGenerate with AccessGroups=[<env>], Tags=[<env>], MaxBudget=nil (KEY-10) → INSERT environment_keys with WARN-03 retry policy → audit ActionEkCreate / OutcomeCreated + 200 CreateResponse.
- **WARN-03 commit verified** end-to-end across three test cases (10a/b/c). The two collision classes are distinguished by constraint name (environment_keys_pkey for ekid_ PK; environment_keys_credential_hash_key for credential_hash UNIQUE). Only the PK class is retryable.
- **ListHandler + GetHandler** ship the read endpoints. ListHandler enforces caller-scope for non-admin via db.ListEnvironmentKeysByOwner and admin override via db.ListEnvironmentKeysByOwnerWithFilter with optional ?owner_email. Non-admin callers passing ?owner_email get 400 invalid_argument (T-03-08-05). Pagination via ?limit (default 100, max 500) + ?cursor (opaque pass-through). GetHandler enforces ekid_ prefix BEFORE the DB lookup (T-03-08-03) and 403 not_key_owner on cross-owner reads (admin reads any). EkRowView excludes plaintext, credential_hash, and litellm_* fields by construction (T-03-08-02).
- **RevokeHandler** ships Hub §8.5 LiteLLM-first revocation per D-15 / KEY-08. The LiteLLM call site (line 651) precedes the DB flip call site (line 666) inside the handler body. On LiteLLM-unreachable: 503 + DB row STAYS active + Redis NOT DEL'd — caller may retry. On DB flip failure post-LiteLLM-ack: 500 + Redis NOT DEL'd (Phase 2 orphan-cleanup Runnable will eventually reconcile via ListActiveACHKeyTokens per Phase 02.2 D-02). On success: 204 + audit ActionEkRevoke / OutcomeRevoked.
- **Mount(deps)** registers the four §15.5 routes on a chi.Router subtree under /platform/env-keys; Plan 03-11's server.go wires this verbatim under the Authn-gated chi.Group.
- 36 unit tests pass under stdlib testing + per-test fakes (no controller-runtime envtest, no pgxpool, no real Redis); 0 regressions across the full internal test sweep.

## Task Commits

Each task ran a strict TDD RED→GREEN gate sequence:

| Task | RED | GREEN |
|------|-----|-------|
| 1 (CreateHandler + Deps + types) | `ff573d9` | `75b2c7e` |
| 2 (ListHandler + GetHandler) | `fc1cc6d` | `34e27bc` |
| 3 (RevokeHandler + Mount) | `08f04ab` | `8442e38` |

## Files Created/Modified

### Created

- `internal/platformapi/envkeys/doc.go` (83 lines) — package GoDoc enumerating the four endpoints + discipline statements (plaintext-once, asymmetric revocation, LiteLLM compensation, WARN-03 collision policy, WARN-06 shared helper, caller-type discipline, DisallowUnknownFields).
- `internal/platformapi/envkeys/handler.go` (950 lines) — Deps struct + envStore/dbOps/redisOps interfaces + CreateRequest/CreateResponse + EkRowView/ListResponse + CreateHandler/ListHandler/GetHandler/RevokeHandler + supporting helpers (classifyInsertError, hasIntersect, isNotFound, parseLimit, mapEkRows, mapEkRow).
- `internal/platformapi/envkeys/handler_test.go` (1,368 lines) — fakeLiteLLM (with compile-time `var _ litellm.Client = (*fakeLiteLLM)(nil)` canary) + fakeStore + fakeDB + fakeRedis + 36 test functions.
- `internal/platformapi/envkeys/mount.go` (46 lines) — Mount(deps) func(chi.Router) registering the four routes.

### Modified (Rule 3 deviation)

- `internal/db/types_keys.go` — added `CredentialHash string` to EkKeyInfo. Required so RevokeHandler can derive the keystore cache key `"ach:key:" + credential_hash` for the §8.5 Redis DEL invalidation barrier per KEY-08. Additive only; no existing struct-literal call sites (and no equality-check test cases) rely on absence of the field.
- `internal/db/environment_keys.go` — GetEnvironmentKey and RevokeEnvironmentKey SELECT credential_hash from the row (column already exists in db/migrations/000001_init.up.sql, just wasn't being read). ListEnvironmentKeysByOwner / ListEnvironmentKeysByOwnerWithFilter / EkResolve remain unchanged (they don't need the column).

## Decisions Made

See `key-decisions` frontmatter array. Highlights:

- **Already-revoked → 404 (Test R-8 option a).** Idempotency without double audit events; matches Hub §8.5 spirit; documented in code (RevokeHandler step 4 comment).
- **WARN-03 commit verified.** ekid_ PK collisions retry once with new ulid (reuse plaintext + LiteLLM token, no compensation); credential_hash UNIQUE collisions are hard failures (500 + LiteLLM compensation, no retry — collision probability ~1/2^128).
- **Team-membership lookup imports the shared helper.** `achteams.LookupCallerTeams` per WARN-06 — no inline definition; the static-analysis grep gate `grep -c '^func lookupCallerTeams\(' internal/platformapi/envkeys/` returns 0.
- **KeyContext.IsAdmin populated by middleware.Authn.** Per BLK-02 / Plan 03-05; the handlers read keyCtx.IsAdmin directly (NO Deps.Allowlist field). Plan 03-10 admin handler will use the same convention.
- **Plaintext-once invariant.** Verified by Test 11 — the generated plaintext appears exactly ONE time in the response body; never in headers; never in audit records (greps on the audit buffer return zero matches).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Extended EkKeyInfo + GetEnvironmentKey + RevokeEnvironmentKey for the §8.5 Redis DEL cache invalidation**

- **Found during:** Task 3 implementation. Plan acceptance criterion `grep -nE '"ach:key:" \+ row\.CredentialHash' internal/platformapi/envkeys/handler.go` requires `row.CredentialHash`. The shipped `db.EkKeyInfo` (from Plan 03-03) did NOT carry a `CredentialHash` field — only `EkInsertRow` did (write-side).
- **Issue:** Without CredentialHash on the read-back row, RevokeHandler cannot derive the keystore cache key `"ach:key:" + credential_hash` to DEL the Redis entry per §8.5 step 4. The 60s TTL would be the only invalidation path, defeating the explicit-DEL barrier KEY-08 documents.
- **Fix:** Added `CredentialHash string` to `db.EkKeyInfo` (Plan 03-03's `internal/db/types_keys.go`); updated `db.GetEnvironmentKey` + `db.RevokeEnvironmentKey` SELECT to read the column. The change is purely additive — `EkResolve` is intentionally NOT extended (the resolver path already has the plaintext and can recompute the hash via credhash.Hash); the list helpers don't need it either.
- **Files modified:** `internal/db/types_keys.go`, `internal/db/environment_keys.go` (outside the plan's declared `files_modified` set).
- **Scope-boundary justification:** The deviation falls outside the plan's `<files_modified>` list but does NOT conflict with parallel Wave 3 plans (03-07 touches internal/platformapi/auth/; 03-09 touches internal/platformapi/environments/ and internal/platformapi/hydrate/). Existing internal/db/environment_keys_test.go integration tests use field-by-field access patterns (no struct literals) so the additive field doesn't break them. `./scripts/dev.sh go vet -tags integration ./internal/db/...` exits 0.
- **Commit:** `75b2c7e` (Task 1 GREEN — committed alongside CreateHandler since both Task 1 and Task 3 needed it; the read in CreateHandler is unused but the existence on the type unblocks Task 3).

**2. [Plan-AC nit] `grep -nE 'not_key_owner'` matches via `audit.OutcomeNotKeyOwner` constant, not a literal**

- **Found during:** Task 2 acceptance-gate review.
- **Issue:** The plan asks `grep -nE 'not_key_owner' internal/platformapi/envkeys/handler.go` for at least one match. The handler uses `audit.OutcomeNotKeyOwner` (the constant, whose value is `"not_key_owner"`) rather than the literal string — so a literal grep returns 0 matches in the source. The semantic invariant (wire-level envelope code = `"not_key_owner"`) is satisfied via the constant.
- **Fix:** None — the semantic intent is preserved. Same constant-vs-literal pattern Plan 03-02 SUMMARY documents.
- **Files modified:** None.

**3. [Plan-AC nit] LiteLLM-first ordering grep matches multiple files in the global pattern**

- **Found during:** Task 3 acceptance-gate review.
- **Issue:** The plan's awk gate `awk '/deps\.LiteLLM\.RevokeKey/{print "litellm:" NR} /db\.RevokeEnvironmentKey/{print "db:" NR}' internal/platformapi/envkeys/handler.go` shows several lines because (a) CreateHandler has its OWN compensation `deps.LiteLLM.RevokeKey` call (line 450) and (b) doc-comments mention both symbols. A naive lexical comparison would not give a clean "litellm < db" answer when scoped to the file globally.
- **Fix:** The semantic invariant (LiteLLM call precedes DB call WITHIN RevokeHandler) is enforced both structurally and behaviorally. Inside RevokeHandler, the first `deps.LiteLLM.RevokeKey` is at line 651 and the first `deps.DB.RevokeEnvironmentKey` is at line 666 (verified via the scoped awk in plan-level verification above). Test R-5 asserts the behavioral contract (LiteLLM-unreachable leaves DB row 'active' and Redis NOT DEL'd).
- **Files modified:** None.

**4. [Test deviation] First-time-user test reframed as verify-or-create idempotent fallback**

- **Found during:** Task 1 GREEN test run — original `TestCreateHandlerFirstTimeLiteLLMUser` failed because LookupCallerTeams (step 4) returns empty teams for a truly first-time user, and the §8.2 step-4 intersection check then rejects the request with 403 unauthorized_team before the §8.2 step-5 UserNew branch can ever run.
- **Issue:** The plan's "Test 7 (first-time LiteLLM user — UserNew called)" scenario is impossible in the §8.2 flow as ordered. A user calling POST /platform/env-keys must have a pk_, which (per Plan 03-07) was minted by the SSO callback — and the SSO callback already enrolls them in LiteLLM. The only way to reach UserNew here is the idempotent-fallback race where the LiteLLM-side user disappeared between SSO and env-keys.
- **Fix:** Renamed to `TestCreateHandlerVerifyOrCreateIdempotentFallback`. The test models a race: first UserInfoByEmail call (from LookupCallerTeams) returns the user with team-a; second UserInfoByEmail call (from the verify-or-create step 5) returns ErrNotFound (a LiteLLM-side flake) and the handler runs UserNew + TeamMemberAdd. The team intersection passes because the FIRST call surfaced the user's teams. This is the realistic defensive-coding scenario.
- **Files modified:** `internal/platformapi/envkeys/handler_test.go`.
- **Impact:** Test count for Task 1 stays at 14 (the renamed test replaces the original); behavior coverage of UserNew + TeamMemberAdd is preserved.

---

**Total deviations:** 4 (1 Rule 3 blocking, 2 plan-AC nits, 1 test design adjustment). No scope creep — all four deviations are documented above with rationale.

## Threat-Model Coverage (from PLAN.md `<threat_model>`)

| Threat | Disposition | Mitigation Landed In Code |
|--------|-------------|---------------------------|
| T-03-08-01 (revocation race) | mitigate | RevokeHandler: `deps.LiteLLM.RevokeKey` (line 651) precedes `deps.DB.RevokeEnvironmentKey` (line 666) precedes `deps.Redis.Del` (line 686) inside the handler body. Test R-5 asserts LiteLLM-unreachable leaves DB row active and Redis NOT DEL'd. |
| T-03-08-02 (plaintext leaked via List/Get) | mitigate | CreateResponse is the ONLY struct in this package carrying a Plaintext field. EkRowView (used by ListHandler + GetHandler) excludes plaintext + credential_hash + litellm_* fields by construction. |
| T-03-08-03 (prefix-confusion attack) | mitigate | GetHandler and RevokeHandler validate `strings.HasPrefix(keyID, keys.EkidKeyIDPrefix)` BEFORE any DB call. Tests assert zero DB calls on pkid_/ek_ inputs. |
| T-03-08-04 (revoke-someone-else's-key) | mitigate | Owner check: `row.OwnerEmail == keyCtx.OwnerEmail \|\| keyCtx.IsAdmin`. Test R-4 acceptance gate. Admin path requires Plan 03-05's middleware.Authn populating keyCtx.IsAdmin from the allowlist parameter per BLK-02. |
| T-03-08-05 (non-admin sees others' env-keys) | mitigate | ListHandler dispatches to `db.ListEnvironmentKeysByOwner(callerEmail, ...)` for non-admin; rejects `?owner_email` parameter for non-admin (Test L-2 acceptance gate — 400 invalid_argument). |
| T-03-08-06 (DB INSERT compensation orphans LiteLLM key) | mitigate | Compensation: `deps.LiteLLM.RevokeKey(compCtx, token)` called on InsertEnvironmentKey failure under a fresh 5s-timeout context (Test 9 + Test 10a + Test 10c acceptance gates). Phase 2's orphan-cleanup Runnable + Phase 02.2 D-02 ListActiveACHKeyTokens reconcile any missed compensations on the next tick. |
| T-03-08-07 (replay of revoked key) | accept | Revoked rows have status='revoked'; Redis cache may still serve the stale 'active' KeyInfo for up to 60s (KEY-08 designed bound per Hub §5.1). The 60s window is acceptable per the spec. |
| T-03-08-08 (plaintext in audit Extra) | mitigate | Audit emission uses only key_id (ekid_) and target.kind/target.name; never plaintext. Test 11 asserts the plaintext does NOT appear in the audit buffer. The internal/audit/doc.go discipline-over-scrubbing contract documents the caller responsibility. |
| T-03-08-SC (npm/pip/cargo installs) | mitigate | Zero new go.mod entries. chi v5.2.0 was pinned by Plan 03-05's chi_compat anchor. |

## Threat Flags

None. This plan introduces no new network endpoints, auth paths, file access patterns, or schema changes at trust boundaries beyond what the plan's `<threat_model>` already enumerates. (The Rule 3 deviation extending EkKeyInfo + GetEnvironmentKey + RevokeEnvironmentKey adds a SELECT clause but does not change the schema or alter any auth surface.)

## Plan-level Verification

| Check | Result |
|-------|--------|
| `./scripts/dev.sh go build ./internal/platformapi/envkeys/...` exits 0 | PASS |
| `./scripts/dev.sh go vet ./internal/platformapi/envkeys/...` exits 0 | PASS |
| `./scripts/dev.sh go test ./internal/platformapi/envkeys/... -count=1` exits 0 | PASS (36 tests pass) |
| `./scripts/dev.sh go build ./...` exits 0 | PASS (full-repo build clean — no downstream regressions) |
| `./scripts/dev.sh go test ./internal/... -count=1` exits 0 | PASS (no regressions across full internal test sweep) |
| LiteLLM-first ordering gate (scoped to RevokeHandler body) | PASS (litellm line 651 < db line 666) |
| `grep -nE 'keyCtx\.KeyType != keys\.PrefixPk' handler.go` ≥ 1 | PASS |
| `grep -nE 'deps\.Store\.GetEnvironment' handler.go` ≥ 1 | PASS |
| `grep -nE 'deps\.Store\.EnvironmentTerminating' handler.go` ≥ 1 | PASS |
| `grep -nE 'deps\.Store\.EnvironmentAccessGroupSynced' handler.go` ≥ 1 | PASS |
| `grep -nE 'keys\.NewBearer\(keys\.PrefixEk\)' handler.go` ≥ 1 | PASS |
| `grep -nE 'AccessGroups:\s*\[\]string\{env\.Name\}' handler.go` ≥ 1 | PASS |
| `grep -nE 'MaxBudget:\s*nil' handler.go` ≥ 1 (KEY-10) | PASS |
| `grep -nE 'deps\.LiteLLM\.RevokeKey' handler.go` ≥ 1 (compensation + revocation) | PASS (2 call sites) |
| `grep -nE 'audit\.ActionEkCreate' handler.go` ≥ 1 | PASS |
| `grep -nE 'DisallowUnknownFields' handler.go` ≥ 1 (D-16) | PASS |
| `grep -cE '^func lookupCallerTeams\(' handler.go` == 0 (WARN-06) | PASS |
| `grep -nE 'achteams\.LookupCallerTeams' handler.go` ≥ 1 (WARN-06) | PASS |
| `grep -nE '^func ListHandler\(' handler.go` == 1 | PASS |
| `grep -nE '^func GetHandler\(' handler.go` == 1 | PASS |
| `grep -nE 'strings\.HasPrefix\(keyID, keys\.EkidKeyIDPrefix\)' handler.go` ≥ 1 | PASS |
| `grep -nE 'invalid_argument' handler.go` ≥ 1 | PASS |
| `grep -nE '^func RevokeHandler\(' handler.go` == 1 | PASS |
| `grep -nE '^func Mount\(' mount.go` == 1 | PASS |
| `grep -nE 'http\.StatusNoContent' handler.go` ≥ 1 (204 invariant) | PASS |
| `grep -nE 'deps\.Redis\.Del' handler.go` ≥ 1 (Redis invalidate) | PASS |
| `grep -nE '"ach:key:" \+ row\.CredentialHash' handler.go` ≥ 1 (cache-key shape) | PASS |
| `grep -cE 'r\.Post\|r\.Get\|r\.Delete' mount.go` ≥ 4 | PASS (exactly 4) |

## Output Section (per PLAN.md `<output>`)

1. **Test R-8 already-revoked behavior:** Chose option (a) 404. Rationale documented in `key-decisions` — idempotency without double audit events; clean information-hiding (don't expose whether the key existed-then-revoked vs never existed).
2. **WARN-03 commit:** Implemented. ekid_ PK collisions (constraint `environment_keys_pkey`) retry once with new ulid (reuse plaintext + credential_hash + LiteLLM token; no compensation). credential_hash UNIQUE collisions (constraint `environment_keys_credential_hash_key`) surface 500 + LiteLLM compensation (no retry — collision probability ~1/2^128). Verified end-to-end across Tests 10a/10b/10c.
3. **Team-membership lookup imports the shared helper:** Confirmed. `achteams.LookupCallerTeams(ctx, deps.LiteLLM, keyCtx.OwnerEmail)` at handler.go:276. The grep gate `grep -c '^func lookupCallerTeams\(' internal/platformapi/envkeys/handler.go` returns 0.
4. **KeyContext.IsAdmin population mechanism:** Authn-middleware extension per Plan 03-05's BLK-02 design. `middleware.Authn(resolver, allowlist, auditLog)` accepts the allowlist parameter; for pk_ callers with `OwnerEmail` in the allowlist it sets `KeyContext.IsAdmin=true` via `middleware.WithKeyContext`. The envkeys handlers read `keyCtx.IsAdmin` directly. No `Deps.Allowlist` field on this handler — the allowlist lookup is centralized in Authn. Plan 03-10 (admin handler) will use the same convention.

## Self-Check

Files exist:

- `internal/platformapi/envkeys/doc.go`: FOUND
- `internal/platformapi/envkeys/handler.go`: FOUND
- `internal/platformapi/envkeys/handler_test.go`: FOUND
- `internal/platformapi/envkeys/mount.go`: FOUND

Commits exist (verified via `git log --oneline da68ff9..HEAD`):

- `ff573d9` test(03-08): Task 1 RED: FOUND
- `75b2c7e` feat(03-08): Task 1 GREEN: FOUND
- `fc1cc6d` test(03-08): Task 2 RED: FOUND
- `34e27bc` feat(03-08): Task 2 GREEN: FOUND
- `08f04ab` test(03-08): Task 3 RED: FOUND
- `8442e38` feat(03-08): Task 3 GREEN: FOUND

All 36 Phase 3-08 tests PASS under `go test ./internal/platformapi/envkeys/... -count=1`. Full-internal sweep PASS. `go build ./...` and `go vet ./...` exit 0.

## Self-Check: PASSED

## TDD Gate Compliance

All three tasks completed the RED→GREEN gate sequence in git history:

| Task | RED commit (test) | GREEN commit (feat) | Sequence verified |
|------|-------------------|---------------------|-------------------|
| 1 (CreateHandler) | `ff573d9` | `75b2c7e` | ✓ test before feat |
| 2 (List + Get) | `fc1cc6d` | `34e27bc` | ✓ test before feat |
| 3 (Revoke + Mount) | `08f04ab` | `8442e38` | ✓ test before feat |

No REFACTOR commits — initial GREEN was clean for all three tasks (one minor test rename within Task 1 GREEN before commit, no follow-up refactor commits).

## Next Phase Readiness

- **Plan 03-11 (cmd/platform-api server wire-up) READY** — can import `envkeys.Deps`, `envkeys.Mount` and wire under the Authn-gated chi.Group. The four small interfaces (envStore, dbOps, redisOps) are satisfied by trivial adapters around `*store.Store`, the db.* helpers bound to a `*pgxpool.Pool`, and `*redis.Client.Del`. The Deps struct exposes `Pepper`, `Audit`, `Logger`, `Namespace` for the same wiring.
- **Plan 03-12 (Phase 3 e2e) READY** — can invoke the four endpoints end-to-end. The §15.5 wire shapes (CreateResponse, ListResponse, EkRowView, 204 No Content) are stable. The §8.5 LiteLLM-first ordering is behaviorally verified (Test R-5).
- **Plan 03-09 (hydrate + environments) UNAFFECTED** — runs in parallel with this plan in Wave 3, shares no files.
- **Plan 03-10 (admin handler) UNAFFECTED** — will reuse the keyCtx.IsAdmin convention this plan exercises (no new allowlist plumbing required).
- **Phase 4 Forwarder** — Phase 3 closes the §8.2 Phase 02.2 D-02 prerequisite: `environment_keys.litellm_token` is now populated on every ek_ create, so the orphan-cleanup Runnable's `ListActiveACHKeyTokens` consumer is precise from this commit forward.

No blockers introduced. The Rule 3 deviation (EkKeyInfo.CredentialHash) is additive and forward-compatible.

## Worktree Note

This plan was executed in a Claude Code worktree (`worktree-agent-af7363c14ba3f0de5`) spawned with an inherited stale base (e975d28). The worktree_base_verification step reset to the expected base `da68ff9` per the orchestrator's prompt directive; the reset was strict-ancestor only (no divergent commits to lose) and never touched the protected `main` ref. All six Task commits (`ff573d9`, `75b2c7e`, `fc1cc6d`, `34e27bc`, `08f04ab`, `8442e38`) live on the per-agent branch and will be merged back via the orchestrator's normal Wave 3 merge pass.

---
*Phase: 03-hub-identity-platform-api*
*Plan: 08*
*Completed: 2026-05-20*
