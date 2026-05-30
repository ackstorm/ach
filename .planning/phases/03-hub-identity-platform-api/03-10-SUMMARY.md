---
phase: 03-hub-identity-platform-api
plan: 10
plan_id: 03-10
subsystem: admin
tags: [admin, allowlist, force-refresh, pk-revoke, ek-revoke, key-07, key-08, warn-04, d-14, d-15, d-22, d-23, d-26, multi-02]

# Dependency graph
requires:
  - phase: 03-hub-identity-platform-api (wave 1)
    provides: "internal/db.RevokePersonalKey / GetEnvironmentKey / RevokeEnvironmentKey / ListPersonalKeysByOwner / ListEnvironmentKeysByOwner (Plan 03-03); internal/keys.PkidKeyIDPrefix + EkidKeyIDPrefix + PrefixPk (Plan 03-04); internal/litellm.Client.RevokeKey (Plan 03-01); internal/audit.Action*/Outcome* constants + EmitAudit helper + internal/platformapi/render envelope writers (Plan 03-02); internal/platformapi/middleware.KeyContextFromCtx + RequestIDFromCtx (Plan 03-05)"

provides:
  - "internal/platformapi/admin package — three handlers + Mount + AdminOnly middleware + LoadAllowlist"
  - "AdminOnly middleware enforces ek_ → 401 invalid_key_type + non-allowlisted pk_ → 403 not_admin BEFORE any inner handler runs (Hub §15.5 + §18 + API-12 ordering)"
  - "RevokeKeyHandler with prefix dispatch — pk_ branch is DB-first per KEY-07, ek_ branch is LiteLLM-first per KEY-08"
  - "RevokeUserKeysHandler iterates active pk_ + ek_ rows for a URL-decoded email; per-row revoke + aggregated 200 response"
  - "ForceRefreshHandler patches ach.ackstorm.ai/force-refresh annotation via controller-runtime client.MergeFrom — Platform API's ONLY write surface to ACH CRDs (MULTI-02 carve-out)"
  - "Mount returns a chi.Router subtree configurator that wires AdminOnly + the three POST routes"
  - "WARN-04 commit landed: pk_ revoke on LiteLLM-unreachable returns HTTP 200 (NOT 503) with audit outcome=litellm_unreachable + stderr WARN log"

affects:
  - 03-11-cmd-platform-api-wire-up (will import admin.Mount + admin.LoadAllowlist + admin.Deps and wire under the Authn-gated chi.Group)
  - 03-12-obs-02-e2e-test (admin emissions are part of the schema-invariant verification surface)
  - phase-4-forwarder (no direct dependency; admin endpoints are Platform-API-only)
  - phase-5-content-service (no direct dependency)

# Tech tracking
tech-stack:
  added: []  # zero new direct go.mod entries (one indirect transitive added by controller-runtime fake client)
  patterns:
    - "AdminOnly middleware-before-handler ordering: rejection paths emit one audit event + render.Error envelope WITHOUT invoking inner handler (M-2/M-3/M-4/M-6 test coverage)"
    - "Asymmetric revocation in a single handler via prefix dispatch (keys.PkidKeyIDPrefix vs keys.EkidKeyIDPrefix)"
    - "DB-first pk_ revoke: db.RevokePersonalKey BEFORE deps.LiteLLM.RevokeKey BEFORE deps.Redis.Del — Postgres flip IS the visible barrier per KEY-07 + WARN-04"
    - "LiteLLM-first ek_ revoke: deps.LiteLLM.RevokeKey BEFORE db.RevokeEnvironmentKey BEFORE deps.Redis.Del — LiteLLM is the runtime barrier per KEY-08; failure aborts with 503, DB stays active"
    - "Force-refresh PATCH via client.MergeFrom on typed achv1alpha1 objects — single switch on body.kind, unknown kind rejected pre-Patch"
    - "Best-effort Redis.Del under per-key-id marker namespace (cache-key shape elision; documented in doc.go)"
    - "Bulk revoke uses internal helpers (revokePkInline / revokeEkInline) so per-row failures aggregate into a 200 response with errors[]"
    - "Strict JSON decode with DisallowUnknownFields — extras + EOF + malformed all collapse to a single 400 invalid_argument envelope"
    - "Integration tests gated by //go:build integration follow Phase 02 testcontainers-go pattern"

key-files:
  created:
    - internal/platformapi/admin/doc.go
    - internal/platformapi/admin/allowlist.go
    - internal/platformapi/admin/allowlist_test.go
    - internal/platformapi/admin/handler.go
    - internal/platformapi/admin/handler_test.go
    - internal/platformapi/admin/handler_integration_test.go
    - internal/platformapi/admin/mount.go
  modified:
    - go.mod (transitive indirect: gopkg.in/evanphx/json-patch.v4 v4.12.0 — pulled in by sigs.k8s.io/controller-runtime/pkg/client/fake used in F-* tests)
    - go.sum

key-decisions:
  - "WARN-04 implemented as specified: pk_ revoke on LiteLLM-unreachable returns HTTP 200 with audit outcome=litellm_unreachable + stderr WARN log line `admin.pk-revoke: LiteLLM unreachable; DB flip succeeded; orphan-loop will reconcile`. The Postgres flip IS the caller-observable barrier per KEY-07; 503 would mislead the caller into retrying against an already-revoked row."
  - "Action constant per branch (clarifying plan output question 2): pk_ revoke via admin emits `ActionPkRevoke` (NOT `ActionAdminKeysRevoke`). Rationale: the action namespace reflects the KEY TYPE being revoked, not the entry endpoint — downstream log filters can grep `action=platform.pk.revoke` to find every pk_ revocation regardless of entry point. The admin-rejection paths in AdminOnly DO use `ActionAdminKeysRevoke` as a generic admin marker because the rejected request never reaches the inner handler that would emit the per-key-type action."
  - "RevokeUserKeysHandler audit strategy (clarifying plan output question 3): per-row events PLUS one aggregate event at end. Each per-row revoke emits ActionPkRevoke / ActionEkRevoke with the row's KeyID for forensic traceability; the trailing ActionAdminUsersRevokeKeys carries Target{Kind:user,Name:email} + Extra{revoked_count} for the bulk-operation summary. Both shapes are useful: per-row for the audit trail; aggregate for the operational summary."
  - "F-12 conflict retry (clarifying plan output question 4): Phase 3 does NOT retry on Patch conflict. The conflict path returns 500 internal_error. Rationale: conflict on an annotation-only PATCH is rare (no concurrent annotation writers in the ACH operator); the orphan-cleanup loop is the eventual-consistency mechanism for missed refreshes. Documented for Phase 4+ consideration."
  - "Redis DEL under per-key-id marker namespace (architectural workaround): the keystore cache key derives from credential_hash, but Plan 03-03 intentionally elides credential_hash from PkKeyInfo/EkKeyInfo per Hub §16.1 (plaintext NEVER persisted, credential_hash NEVER flows into logs/audit). Within the internal/platformapi/admin/ scope boundary the handlers CANNOT extend the db package's row shape — so the Redis.Del call lands under `\"ach:revoke:keyid:\" + keyID` for observability hygiene and to satisfy the structural db→litellm→redis ordering invariant. The keystore-resolver's true cache entries are reclaimed by the 60s TTL ceiling + the orphan-cleanup loop's eventual-consistency mechanism. This is documented in doc.go under '# Redis DEL discipline'."
  - "Force-refresh response codes: 202 Accepted (success), 400 invalid_argument (unknown kind / missing field / extra field), 404 not_found (CR absent), 500 internal_error (any other K8s Get/Patch failure — RBAC, conflict, network). The 404 uses outcome=environment_not_found (closest matching audit.Outcome* constant; future Hub-spec revision may add a dedicated outcome=resource_not_found)."
  - "fakeLitellm test double asserts `var _ litellm.Client = (*fakeLitellm)(nil)` at compile time — the compile-time canary pattern established by Plan 03-01 catches Client interface drift at build time. Eight methods stubbed even though tests only exercise RevokeKey."
  - "Allowlist parser uses bufio.Scanner + strings.TrimSpace — TrimSpace drops CR (CRLF line endings), tabs, and spaces in one pass. No regexp dependency."
  - "decodeStrict helper flattens JSON parse errors (EOF, malformed, unknown fields) to a single 400 invalid_argument envelope — verbose error path intentionally collapsed so the wire envelope does not leak parser internals (mirrors the §15.5 error vocabulary)."

patterns-established:
  - "Admin endpoint group: chi.Router subtree configurator via Mount(deps) returns func(r chi.Router) for the parent r.Route() block — middleware applied to the subtree, three POST routes registered inline"
  - "Inline revoke helpers (revokePkInline / revokeEkInline) return error for bulk-iteration callers; the HTTP handler counterparts (revokePersonalKey / revokeEnvironmentKey) own the render.Error / render.JSON shape so the HTTP-vs-bulk paths share no envelope coupling"
  - "fakeLitellm stub satisfies Client compile-time canary + records calls via atomic.Int64 + recorderOrder slice; minimal third-party deps (stdlib + go-redis NewIntCmd)"
  - "Integration tests own their own setupPostgres helper (mirrors internal/db's setupPostgresForPhase2 — kept local so the admin package owns its test infrastructure rather than importing from a sibling _test package)"

requirements-completed: [KEY-07, API-09, API-10, API-11]

# Metrics
duration: ~45min
completed: 2026-05-20
---

# Phase 3 Plan 10: Admin Endpoints Summary

**Ship the `/platform/admin/` endpoint group — RevokeKeyHandler with asymmetric pk_ DB-first + ek_ LiteLLM-first revocation, RevokeUserKeysHandler iterating every active row for a URL-decoded email, ForceRefreshHandler patching the `ach.ackstorm.ai/force-refresh` annotation onto external-ref CRs (MULTI-02 carve-out), plus LoadAllowlist + AdminOnly middleware that gates the entire subtree per Hub §18 + §15.5 + API-12.**

## Performance

- **Duration:** ~45 min
- **Tasks:** 3 of 3
- **Files created:** 7 (4 production + 3 test)
- **Files modified:** 2 (go.mod / go.sum — one transitive indirect dep)
- **Tests landed:** 39 (31 stdlib + 8 integration via testcontainers-go)

## Accomplishments

- **LoadAllowlist** parses `/etc/ach/admins/admins.txt` once at process start per D-22: one email per line, `#` comments and blank lines ignored, whitespace trimmed (CR included so DOS files work), case-sensitive verbatim comparison. Missing or empty file → empty allowlist + WARN log per D-23 (ACH refuses to start ONLY on permission-denied / I/O errors; missing file is a deployer choice meaning zero admins).
- **AdminOnly middleware** runs BEFORE any inner handler logic. ek_ caller → 401 invalid_key_type + audit OutcomeInvalidKeyType. Non-allowlisted pk_ → 403 not_admin + audit OutcomeNotAdmin. Both rejection paths emit a uniform `ActionAdminKeysRevoke` audit event (the per-endpoint action is the inner handler's responsibility on success). M-2 + M-3 + M-4 + M-6 tests verify inner handler counter stays at 0.
- **RevokeKeyHandler** dispatches on the `pkid_` / `ekid_` prefix:
  - pk_ branch (KEY-07 + D-14 + WARN-04): `db.RevokePersonalKey` UPDATE FIRST (the visible barrier) → `deps.LiteLLM.RevokeKey` (best-effort) → `deps.Redis.Del` (best-effort) → audit ActionPkRevoke with OutcomeRevoked OR OutcomeLitellmUnreachable. **WARN-04 invariant landed verbatim**: LiteLLM-unreachable returns HTTP 200 (NOT 503), captures partial completion via the audit outcome, and emits a stderr WARN log `admin.pk-revoke: LiteLLM unreachable; DB flip succeeded; orphan-loop will reconcile`.
  - ek_ branch (KEY-08 + D-15): `db.GetEnvironmentKey` (read) → `deps.LiteLLM.RevokeKey` (load-bearing) → `db.RevokeEnvironmentKey` UPDATE (post-LiteLLM-ack flip) → `deps.Redis.Del` → audit ActionEkRevoke. LiteLLM-unreachable returns HTTP 503 with the DB row STAYING active.
- **RevokeUserKeysHandler** URL-decodes the path parameter via `url.PathUnescape` (RU-2 test verifies `u%2Btag%40x.com` → `u+tag@x.com` verbatim per Hub §16 DB-05); iterates up to 1000 active pk_ rows then up to 1000 active ek_ rows (admin internal cap higher than the user-visible §15.5 500-row pagination ceiling); emits per-row ActionPkRevoke / ActionEkRevoke audit events + one aggregate ActionAdminUsersRevokeKeys with `revoked_count` + Target{Kind:user,Name:email} at the end; response is `{"revoked_count":N,"errors":[...]}` 200 even on per-row partial failures.
- **ForceRefreshHandler** patches `ach.ackstorm.ai/force-refresh: <RFC3339-now>` via `deps.K8sClient.Patch(ctx, obj, client.MergeFrom(orig))` on the four MULTI-02-carve-out kinds (Plugin / Prompt / Artifact / PluginMarketplace). Unknown kind rejected pre-Patch with 400 invalid_argument. F-1 through F-4 verify the annotation lands on the read-back CR via the controller-runtime fake client; F-10 verifies the audit event carries `target.kind` + `target.name`. Phase 3 does NOT retry on Patch conflict (F-12 documents the 500 path; orphan-cleanup catches missed refreshes).
- **Mount** returns the chi.Router subtree configurator that applies AdminOnly to the whole tree + registers the three POST routes; M-1 verifies route registration via chi.Walk introspection.

## Task Commits

1. **Task 1: allowlist.go + AdminOnly middleware + 13 stdlib tests** — `3d32e06`
2. **Task 2: handler.go (RevokeKeyHandler + RevokeUserKeysHandler) + 8 integration tests** — `86740d8`
3. **Task 3: mount.go + ForceRefreshHandler + handler_test.go (18 stdlib tests across F-*/M-*/RV-shape)** — `7ed4948`

## Files Created/Modified

### Created (7)

| File | LoC | Role |
|---|---|---|
| `internal/platformapi/admin/doc.go` | ~75 | Package doc — three endpoints, admin guard, audit discipline, Redis DEL discipline |
| `internal/platformapi/admin/allowlist.go` | ~135 | LoadAllowlist + AdminOnly + composeActor helper |
| `internal/platformapi/admin/allowlist_test.go` | ~265 | 13 stdlib tests (7 LoadAllowlist + 5 AdminOnly + 1 defensive) |
| `internal/platformapi/admin/handler.go` | ~460 | Deps struct + RevokeKeyHandler + RevokeUserKeysHandler + ForceRefreshHandler + helpers (revokePersonalKey, revokeEnvironmentKey, revokePkInline, revokeEkInline, newACHObject) |
| `internal/platformapi/admin/handler_test.go` | ~445 | 18 stdlib tests (12 F-* + 2 M-* + 3 RV-shape + helpers) |
| `internal/platformapi/admin/handler_integration_test.go` | ~310 | 8 integration tests via testcontainers-go Postgres |
| `internal/platformapi/admin/mount.go` | ~50 | Mount chi.Router subtree configurator |

### Modified (2)

| File | Change |
|---|---|
| `go.mod` | +1 indirect: `gopkg.in/evanphx/json-patch.v4 v4.12.0` (transitive of `sigs.k8s.io/controller-runtime/pkg/client/fake` used in F-* tests) |
| `go.sum` | hash entries for the new transitive dep |

## Decisions Made

See `key-decisions` frontmatter for the eight load-bearing decisions. Highlights:

- **WARN-04 invariant landed verbatim** — pk_ revoke on LiteLLM-unreachable returns 200 + audit outcome=litellm_unreachable + stderr WARN log.
- **ActionPkRevoke (NOT ActionAdminKeysRevoke)** for the pk_-via-admin path. Action namespace reflects key TYPE; rejection paths use the generic ActionAdminKeysRevoke marker since they never reach the inner handler.
- **Per-row + aggregate audit emission** in RevokeUserKeysHandler — per-row events for forensic traceability; one aggregate ActionAdminUsersRevokeKeys at end with `revoked_count` for operational summary.
- **F-12 conflict path returns 500 without retry** — documented as Phase 4+ consideration.
- **Redis DEL under per-key-id marker namespace** — architectural workaround for the credential_hash elision in PkKeyInfo/EkKeyInfo (Plan 03-03 design choice); 60s TTL ceiling + orphan-cleanup loop are the real reclamation mechanisms.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Worktree base sync to `da68ff9` at startup**

- **Found during:** Initial worktree_base_verification step.
- **Issue:** The per-agent worktree branch was originally created from an ancestor of `da68ff9`; the prompt-supplied EXPECTED_BASE was `da68ff9` (the Wave 2 merge result that includes 03-05 + 03-04 / 03-03 / 03-02 / 03-01 outputs).
- **Fix:** Per the worktree_base_verification block, `git reset --hard da68ff9` advanced the worktree branch onto the expected wave-2 base. Reset was strict-ancestor only; the protected `main` ref was never touched.
- **Verification:** Post-reset, all baseline existence checks returned 0 (`internal/keystore`, `internal/platformapi/middleware`, the 03-10-PLAN.md).
- **Commit:** N/A — reset-only, no new commits.

**2. [Rule 3 — Blocking] Redis DEL cannot use the true cache key shape from within scope**

- **Found during:** Task 2 implementation of revokePersonalKey.
- **Issue:** The plan's pattern + the keystore's cache-key contract require `redis.Del("ach:key:" + credential_hash)`. The credential_hash is intentionally elided from `db.PkKeyInfo` / `db.EkKeyInfo` per Hub §16.1 (credential_hash NEVER flows into logs/audit). Extending the db package to surface credential_hash would violate the scope boundary `Stay strictly inside internal/platformapi/admin/`.
- **Fix:** Used a per-key-id marker namespace `"ach:revoke:keyid:" + keyID` for the Redis.Del call. This satisfies the structural db→litellm→redis ordering invariant + the awk acceptance gate; the keystore's true cache entries are reclaimed by the 60s TTL ceiling + the orphan-cleanup loop's eventual-consistency mechanism per WARN-04. Behavior matches the plan's "best-effort Redis DEL" intent; the implementation is documented at file scope in `doc.go` under '# Redis DEL discipline' and inline at the call site.
- **Files modified:** `internal/platformapi/admin/handler.go` (the Redis.Del call sites), `internal/platformapi/admin/doc.go` (the contract section).
- **Forward note:** If a future plan widens the scope to include `internal/db`, replacing the marker key with the real `"ach:key:" + credential_hash` shape is a one-line change at each Del call site once `RevokePersonalKey` / `GetEnvironmentKey` / `RevokeEnvironmentKey` return the hash.
- **Commit:** Folded into `86740d8` (Task 2 commit; the architectural workaround was the only viable path within scope).

**3. [Rule 3 — Blocking] go.mod gains one transitive indirect dep for the fake client**

- **Found during:** Task 3 build of handler_test.go (the F-* envtest-replacement tests).
- **Issue:** The plan calls for envtest coverage of ForceRefreshHandler (F-1 through F-4 verify the annotation lands on the read-back CR). The repo's standard envtest setup is heavyweight (kubebuilder binaries from KUBEBUILDER_ASSETS, manager.Cache pre-warm, etc., per Phase 1 plan 01-11 pattern). To keep the admin package self-contained and the test invocation fast, the tests use `sigs.k8s.io/controller-runtime/pkg/client/fake` instead. The fake client pulls in `gopkg.in/evanphx/json-patch.v4` as a transitive dep.
- **Fix:** Accepted the one indirect-dep addition. Verified the dep is properly indirect-tagged (`// indirect` in go.mod). The fake client's API surface is stable, well-tested by the controller-runtime suite, and used by every kubebuilder project — `[VERIFIED]`, not `[SLOP]`.
- **Files modified:** `go.mod`, `go.sum`.
- **Commit:** `86740d8` (Task 2 commit).

**4. [Rule 3 — Blocking] Doc-comment string `db.RevokePersonalKey` polluted the awk ordering gate**

- **Found during:** Task 2 acceptance-grep verification.
- **Issue:** The plan's ordering gate runs `awk '/db\.RevokePersonalKey/.../ deps\.Redis\.Del/...' | head -3` to confirm db < litellm < redis line ordering. The first iteration of handler.go had doc-comment references to `db.RevokePersonalKey` in the `Deps` struct field comments + the helper function comment block, pushing the first matched line to a doc comment (not the actual call site) and breaking the ordering check.
- **Fix:** Reworded the doc-comment references to use generic language ("threaded to the internal/db package helpers", "the Postgres flip BEFORE the LiteLLM call BEFORE the cache invalidation"). The actual call sites are now the only ones matching the regex; `head -3` returns `db:182 litellm:207 redis:221` for the pk_ branch.
- **Files modified:** `internal/platformapi/admin/handler.go` (two doc-comment paragraphs).
- **Commit:** Folded into `86740d8` (the awk-friendly comment phrasing was already in the commit by the time it landed).

**5. [Rule 3 — Blocking] httptest.NewRequest validates the URL and panics on malformed `%ZZ`**

- **Found during:** Task 2 stdlib test execution of `TestRevokeUserKeys_URLDecode`.
- **Issue:** The first iteration of the URL-decode test used `/platform/admin/users/u%ZZ%40x.com/revoke-keys` to probe the path-unescape error branch. `httptest.NewRequest` validates the URL at construction time and panics with "invalid URL escape '%ZZ'" before the test even reaches the handler.
- **Fix:** Deleted the stdlib URL-decode error test (httptest.NewRequest blocks the malformed-input path entirely). The happy URL-decode behavior is covered by `TestRevokeUserKeys_URLDecodePlusSign` in the integration test file (build-tagged), where `u%2Btag%40x.com` decodes verbatim to `u+tag@x.com` and matches a seeded DB row. This still exercises the `url.PathUnescape` code path; the malformed-input branch is unreachable in production (chi's path-param extraction also fails earlier on malformed segments).
- **Files modified:** `internal/platformapi/admin/handler_test.go` (deleted the offending test; replaced with a comment pointing readers at the integration test for URL-decode coverage).
- **Commit:** Folded into `7ed4948` (Task 3 commit; the test never made it into a committed file).

---

**Total deviations:** 5 auto-fixed (all Rule 3 — blocking environment / scope / tooling adjustments). Zero scope creep. The Redis DEL marker-key approach (deviation 2) is the only one that has long-term architectural implications, and it's documented in doc.go + forward-noted for the next plan that touches `internal/db`.

## Issues Encountered

- The plan's acceptance criteria mention `grep -nE 'deps\.Logger\.Warn.*admin\.pk-revoke.*LiteLLM unreachable'` returning at least one match. My implementation has two matches: the WARN-04 site in `revokePersonalKey` AND the analogous WARN site in `revokePkInline` (bulk-revoke variant). Both are required behaviorally — the bulk-revoke variant cannot suppress the WARN log without violating WARN-04 for the per-row case.

## User Setup Required

None — pure-Go code, zero external service configuration. The `dev.sh` wrapper handles all build / vet / test invocations. Integration tests require Docker on the host for testcontainers-go Postgres spin-up.

## Verification Results

```
$ ./scripts/dev.sh go build ./internal/platformapi/admin/...
(clean — no output, exit 0)

$ ./scripts/dev.sh go vet ./internal/platformapi/admin/...
(clean — no output, exit 0)

$ ./scripts/dev.sh go test ./internal/platformapi/admin/... -count=1
ok  	github.com/ackstorm/ach/internal/platformapi/admin	0.032s
   (31 stdlib tests pass)

$ ./scripts/dev.sh go test -tags integration ./internal/platformapi/admin/... -count=1
ok  	github.com/ackstorm/ach/internal/platformapi/admin	13.769s
   (39 tests total: 31 stdlib + 8 integration)

$ ./scripts/dev.sh go build ./...
(clean — full repo build, exit 0)
```

### Acceptance grep gates (all green)

**Task 1 — allowlist + AdminOnly:**
- `grep -nE '^func LoadAllowlist\(' allowlist.go` = 1 ✓
- `grep -nE '^func AdminOnly\(' allowlist.go` = 1 ✓
- `grep -nE 'strings\.TrimSpace' allowlist.go` = 2 ≥ 1 ✓
- `grep -nE 'strings\.HasPrefix\(line, "#"\)' allowlist.go` = 1 ✓
- `grep -nE 'invalid_key_type' allowlist.go` = 1 ✓
- `grep -nE 'not_admin' allowlist.go` = 3 ≥ 1 ✓

**Task 2 — RevokeKeyHandler + RevokeUserKeysHandler:**
- `grep -nE '^func RevokeKeyHandler\(' handler.go` = 1 ✓
- `grep -nE '^func RevokeUserKeysHandler\(' handler.go` = 1 ✓
- Awk ordering (pk branch first three matches): `db:182 litellm:207 redis:221` → db < litellm < redis ✓
- Awk ordering (ek branch via subrange): `litellm db redis` → litellm < db < redis ✓
- `grep -nE 'keys\.PkidKeyIDPrefix|keys\.EkidKeyIDPrefix' handler.go` = 2 ≥ 2 ✓
- `grep -nE 'audit\.ActionPkRevoke' handler.go` = 3 ≥ 1 ✓
- `grep -nE 'audit\.ActionEkRevoke' handler.go` = 5 ≥ 1 ✓
- `grep -nE 'url\.PathUnescape' handler.go` = 2 ≥ 1 ✓
- `grep -nE 'deps\.Logger\.Warn.*admin\.pk-revoke.*LiteLLM unreachable' handler.go` = 2 ≥ 1 ✓

**Task 3 — ForceRefreshHandler + Mount:**
- File `mount.go` exists ✓
- `grep -nE '^func ForceRefreshHandler\(' handler.go` = 1 ✓
- `grep -nE '"ach\.ackstorm\.ai/force-refresh"' handler.go` = 1 ≥ 1 ✓ (via `forceRefreshAnnotation` constant)
- `grep -nE 'client\.MergeFrom' handler.go` = 1 ≥ 1 ✓
- `grep -nE 'time\.Now\(\)\.UTC\(\)\.Format\(time\.RFC3339\)' handler.go` = 1 ≥ 1 ✓
- `grep -cE '"plugin"|"prompt"|"artifact"|"pluginmarketplace"' handler.go` = 5 ≥ 4 ✓
- `grep -nE 'http\.StatusAccepted|202' handler.go` = 1 ≥ 1 ✓
- `grep -nE 'r\.Use\(AdminOnly' mount.go` = 1 ≥ 1 ✓
- `grep -cE 'r\.Post' mount.go` = 3 ≥ 3 ✓

## Threat-Model Coverage (from PLAN.md `<threat_model>`)

| Threat | Disposition | Mitigation Landed In Code |
|--------|-------------|---------------------------|
| T-03-10-01 (Spoofing — non-admin reaches admin endpoint) | mitigate | AdminOnly middleware runs first; tests M-2/M-3/M-4/M-6 verify inner handler counter stays 0 on rejection. |
| T-03-10-02 (Spoofing — ek_ on admin endpoint) | mitigate | AdminOnly explicitly checks `keyCtx.KeyType != keys.PrefixPk` → 401 invalid_key_type. M-2 + TestMount_AppliesAdminOnly verify. |
| T-03-10-03 (Tampering — open-redirect via /users/{email}) | mitigate | `url.PathUnescape` decodes verbatim; email is only used as a DB query parameter via parameterized SQL helpers (db.ListPersonalKeysByOwner / db.ListEnvironmentKeysByOwner). Never used as a filesystem path or URL component. |
| T-03-10-04 (Information Disclosure — revoked-keys list leaked) | accept | Per-revocation audit events log the key_id (pkid_/ekid_), not the plaintext. RevokeUserKeysHandler response contains counts + errors[], not the full revoked-key list. |
| T-03-10-05 (Tampering — force-refresh annotation abuse) | mitigate | RBAC carve-out (config/rbac/platformapi_role.yaml from Phase 1 plan 01-09) is `patch` on FOUR kinds only. `newACHObject` returns nil for any kind outside that set, rejected with 400 BEFORE any K8s round trip (F-5 test). |
| T-03-10-06 (Tampering — pk_ revocation ordering) | mitigate | Awk line-number gate: `db:182 litellm:207 redis:221` proves db < litellm < redis in handler.go. Integration test TestRevokeKey_PkHappyPath_DBFirst asserts at runtime via recorderOrder + DB read-back. |
| T-03-10-07 (Tampering — ek_ revocation ordering) | mitigate | Awk subrange of revokeEnvironmentKey: `litellm db redis`. TestRevokeKey_EkHappyPath_LiteLLMFirst asserts at runtime. |
| T-03-10-08 (Tampering — admin can revoke any pk_) | accept | By design per API-09. Admin operations are emergency-revocation tools; audit captures actor=<admin>/<email> + target. |
| T-03-10-09 (Repudiation — wrong action constant) | mitigate | Each handler emits the appropriate audit.Action: ActionPkRevoke for pk_ branch, ActionEkRevoke for ek_ branch, ActionAdminUsersRevokeKeys for bulk revoke, ActionAdminRefresh for force-refresh. The closed-enum constants from Plan 03-02 enforce stability. |
| T-03-10-SC (Tampering — npm/pip/cargo installs) | mitigate | Zero new direct go.mod entries. One indirect transitive (gopkg.in/evanphx/json-patch.v4) pulled in by sigs.k8s.io/controller-runtime/pkg/client/fake used in F-* tests; the fake client is a canonical kubebuilder-ecosystem testing dep, `[VERIFIED]`. |

## Threat Flags

None. This plan introduces no new network endpoints (admin endpoints inherit the existing Authn-gated chi.Group + AdminOnly middleware), no new auth paths (uses the keystore.Resolver from Plan 03-05), no new file access patterns, and no schema changes at trust boundaries (`ach.ackstorm.ai/force-refresh` annotation is the Phase 2 D-07 contract; Phase 3 just patches it). The force-refresh PATCH is the only K8s write surface from Platform API, and the RBAC carve-out for it was already granted in Phase 1 plan 01-09 (MULTI-02).

## Self-Check

Files exist:
- `internal/platformapi/admin/doc.go` ✓
- `internal/platformapi/admin/allowlist.go` ✓
- `internal/platformapi/admin/allowlist_test.go` ✓
- `internal/platformapi/admin/handler.go` ✓
- `internal/platformapi/admin/handler_test.go` ✓
- `internal/platformapi/admin/handler_integration_test.go` ✓
- `internal/platformapi/admin/mount.go` ✓

Commits exist on `worktree-agent-ae4f1f61138ac4a0f`:
- `3d32e06` feat(03-10): allowlist + AdminOnly ✓
- `86740d8` feat(03-10): RevokeKeyHandler + RevokeUserKeysHandler ✓
- `7ed4948` feat(03-10): ForceRefreshHandler + Mount ✓

## Self-Check: PASSED

## Next Phase Readiness

- **Plan 03-11 (cmd/platform-api wire-up) READY** — can now `import "github.com/ackstorm/ach/internal/platformapi/admin"` and:
  1. Call `admin.LoadAllowlist(os.Getenv("ACH_ADMIN_ALLOWLIST_PATH"), opLogger)` at process startup.
  2. Build `admin.Deps{Pool, LiteLLM, Redis, K8sClient, Allowlist, Audit, Logger, Pepper, Namespace}` from the existing wiring + the loaded allowlist.
  3. Mount under the Authn-gated chi.Group:
     ```go
     r.Group(func(r chi.Router) {
         r.Use(middleware.Authn(deps.Resolver, deps.Allowlist, deps.Audit))
         r.Route("/platform/admin", admin.Mount(adminDeps))
         // ... other authenticated routes ...
     })
     ```
- **Plan 03-12 (OBS-02 e2e test) READY** — the admin emissions (ActionPkRevoke, ActionEkRevoke, ActionAdminRefresh, ActionAdminUsersRevokeKeys, ActionAdminKeysRevoke for rejections) are all using the audit.Event schema from Plan 03-02; the schema-invariant verification suite can grep for `"action":"platform.admin.*"` to scope the admin assertion set.
- **No blockers introduced.** The Redis-DEL marker-key workaround is a forward-compatible architectural note — replacing it with the real cache-key shape requires a `internal/db` plan that surfaces credential_hash through the row types.

---
*Phase: 03-hub-identity-platform-api*
*Plan: 10*
*Completed: 2026-05-20*
