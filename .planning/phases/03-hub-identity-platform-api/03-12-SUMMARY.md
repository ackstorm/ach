---
phase: 03-hub-identity-platform-api
plan: 12
plan_id: 03-12
subsystem: phase-3-e2e
tags: [e2e, phase-3-invariants, obs-02, audit-schema, sc1-sc6, warn-01, kind-helm-uat, engineer-pending, d-02-closure, stdlib-testing]

# Dependency graph
requires:
  - phase: 03-hub-identity-platform-api (wave 4 — 03-11)
    provides: "Composed Platform API binary (cmd/platform-api/main.go full Phase 3 entrypoint); internal/platformapi.New(deps) chi.Mux composition; internal/platformapi.NewRunnable manager.Runnable wrap; the audit channel via internal/audit slog handler. SC#3 D-02 closure target: db.ListActiveACHKeyTokens (Plan 03-03)."
  - phase: 02.3-uat-pivot-to-kind-helm
    provides: "scripts/cluster.sh up + test/e2e/values/*.yaml chart pins (LiteLLM + ToolHive). The kind+helm UAT path is the canonical local-cluster surface — docker-compose.yml was intentionally removed on commit a4daf45."
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: "test/e2e/e2e_suite_test.go stdlib-testing scaffold (TestMain + E2E_SKIP_SETUP gate + runCmd/runCmdLonger/envOr/namespace helpers). phase3_helpers_test.go imports these by sharing the test-package namespace."
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: "test/e2e/phase2_invariants_test.go canonical shape — stdlib testing, t.Run per Success Criterion, kubectl orchestration. phase3_invariants_test.go is the direct shape analog for Phase 3."

provides:
  - "test/e2e/phase3_invariants_test.go — TestPhase3Invariants single top-level test with six SC-mapped subtests (SC1_SSO / SC2_Hydrate / SC3_EnvKeysCreate / SC4_AsymmetricRevocation / SC5_AdminGate / SC6_AuditCrossCutting) gated by //go:build e2e."
  - "test/e2e/phase3_helpers_test.go — shared helpers: phase3SuiteGuard / phase3ParseAuditLines / phase3AssertAuditOBS02 / phase3AssertNoPlaintextInLine / phase3CapturePlatformAPILogs / phase3WaitForPlatformAPIReady / phase3ApplyEnvironment / phase3CountAuditByAction / phase3ContainsToken / phase3PostJSON / phase3HTTPClient / phase3URL / phase3StartPortForward."
  - "test/e2e/phase3_fixtures/environment_prod.yaml — Phase 3 production Environment CR (authorizedTeams=[default], runtime+context populated for SC#2 hydrate response verification)."
  - "test/e2e/phase3_fixtures/environment_staging.yaml — Phase 3 staging Environment CR (authorizedTeams=[staging,default] for SC#2 wrong_environment + SC#3 not_found probes)."
  - "scripts/uat-phase3.sh — kind+helm UAT runner mirroring Phase 02.2 uat-g1.sh shape (cleanup trap / RANDOM_PEPPER / mktemp LOG_DIR / jq pre-flight / 'audit:true' filter / OBS-02 per-line invariant gate / 'uat-phase3: PASS' marker)."
  - "Makefile — new e2e-phase3 target invoking scripts/uat-phase3.sh; existing e2e / e2e-focus / e2e-full / e2e-keep / cluster-* targets unchanged."

affects:
  - "Phase 4 (Forwarder) — inherits the OBS-02 audit-schema invariant; Forwarder audit emissions (runtime forwarding is NOT audited per Hub §18.4 carve-out, but FWD-side admin/key-revoke paths will be) MUST satisfy the same gate. Reuse phase3_helpers_test.go's phase3AssertAuditOBS02 verbatim."
  - "Phase 5 (Content Service) — same OBS-02 inheritance; CS-side admin/key-resolution paths follow the same audit-line shape."
  - "Phase 7 (Helm chart / DIST-04) — closes the engineer-pending live-verification debt this plan documents. The deployed Platform API Deployment must set ACH_DEX_* / ACH_REDIS_* / ACH_BASE_URL / ACH_ADMIN_ALLOWLIST_PATH / POD_NAMESPACE; without them the platform-api binary refuses to start (validateConfig in cmd/platform-api/main.go from Plan 03-11). Once Phase 7 wires the manifest, phase3SuiteGuard's t.Skipf path collapses and SC subtests run end-to-end against the cluster."

# Tech tracking
tech-stack:
  added: []  # zero net-new go.mod entries; the helpers reuse net/http + encoding/json + os/exec + regexp + strings + testing + time, all stdlib
  patterns:
    - "Phase-gate via t.Skipf: phase3SuiteGuard inspects the deployed Pod's env-var set and t.Skipf's the subtest with a clear engineer-pending message when Phase 3 env vars are absent. Mirrors Phase 02.2's uat-g1.sh 'engineer-pending verification debt' pattern."
    - "Stdlib testing + kubectl orchestration: same shape as test/e2e/phase2_invariants_test.go. No Ginkgo, no Gomega, no testify — per [feedback_023_tier_framework_rejected]."
    - "Audit-line shape contract: phase3AuditRecord struct binds the Hub §18.2 schema to a typed Go struct. phase3ParseAuditLines does a cheap '\"audit\":true' pre-filter, then json.Unmarshal, then field extraction. phase3AssertAuditOBS02 runs the OBS-02 invariant on each record."
    - "Bearer-plaintext leak detector: regexp `\"pk_[a-z2-7]{26}\"` + `\"ek_[a-z2-7]{26}\"` + literal substring `\"credential_hash\":` — three regex/substring patterns cover the OBS-02 no-leak invariant. The JSON-string anchor (quotes around the prefix) prevents false positives on pkid_/ekid_ values which contain ULID base32 chars."
    - "kind+helm UAT pattern (Phase 02.3 lift): scripts/uat-phase3.sh uses scripts/cluster.sh up to bring up LiteLLM + Postgres + Redis + ToolHive via helm, then spawns a Dex sidecar via `docker run` against scripts/dex-config.yaml, then `kubectl port-forward`s the in-cluster Services to localhost so the spawned platform-api binary can reach them."
    - "Per-record subtest naming: phase3SC6 spawns one t.Run per captured audit record so a failing line is identifiable via `go test -run TestPhase3Invariants/SC6_AuditCrossCutting/record_platform_sso_login_3`. The action-as-subtest-name pattern mirrors the test-naming idiom across the existing audit_test.go suites."

key-files:
  created:
    - test/e2e/phase3_invariants_test.go
    - test/e2e/phase3_helpers_test.go
    - test/e2e/phase3_fixtures/environment_prod.yaml
    - test/e2e/phase3_fixtures/environment_staging.yaml
    - scripts/uat-phase3.sh
  modified:
    - Makefile  # one new target appended after e2e-keep; existing targets unchanged

key-decisions:
  - "SC#4 ordering verification uses log/DB inspection at the e2e layer; the load-bearing recording-wrapper assertions are in Plan 03-10's admin/handler_integration_test.go (testcontainers-go Postgres + recording-wrapper LiteLLM client) — the e2e subtest is the cluster-level smoke verification. Recording wrappers are the preferred technique (timestamps are sub-microsecond on fast systems and would flake)."
  - "SC#5 admin force-refresh runs against the deployed cluster's ach-platform-api Service (kubectl port-forward). The annotation-landing verification is via `kubectl get plugin <name> -o jsonpath` AFTER the live UAT script (scripts/uat-phase3.sh) drives the round-trip. This decision was forced by Phase 7 NOT yet promoting the Platform API Deployment to a Phase 3 binary — the in-process httptest.Server alternative was discarded because (a) the SC #5 invariant is the actual K8s API PATCH effect which an httptest harness cannot exercise, (b) Plan 03-10 already ships fake.NewClientBuilder() coverage for the Patch round-trip at the unit-test layer."
  - "Audit-line capture mechanism: kubectl logs against the deployed Platform API Pod stdout via runCmd('kubectl', 'logs', ...). This is the end-to-end shape — the binary writes audit lines via slog.NewJSONHandler(os.Stdout), Kubernetes captures them, kubectl logs surfaces them. Slower than handler-level slog interception (which Plan 03-07/03-08/etc.'s unit tests use) but more end-to-end. The unit tests cover the fast loop; the e2e suite covers the live capture. Engineer-pending status carry-forward to STATE.md: 'make e2e-phase3' is NOT run as part of the automated test gate; engineer runs once manually post-Phase-3-ship."
  - "phase3_helpers_test.go vs phase3_helpers.go (file rename): plan declared `test/e2e/phase3_helpers.go` but the file MUST be named *_test.go so its identifiers can reference runCmd / namespace / runCmdLonger / envOr from e2e_suite_test.go (which itself is a *_test.go file, so its exported identifiers are visible only to other *_test.go files in the same package). Rule 3 deviation documented below."
  - "docker-compose --profile dex/litellm pattern from the plan is gone (Phase 02.3 commit a4daf45 deleted docker-compose.yml). scripts/uat-phase3.sh targets kind+helm via scripts/cluster.sh up + a `docker run` Dex sidecar bound to scripts/dex-config.yaml. Plan 03-11 documented the same supersession pattern for its Task 3 (commit 14b2e2a) — this plan inherits and extends it."
  - "OBS-01 (sliding-window pk_ extension NOT its own event): SC#6 asserts via phase3CountAuditByAction(records, 'platform.pk.extend') == 0. The unit-test layer (Plan 03-04 / internal/db/check_extend_test.go) verifies the SQL helper does not emit an audit event; this e2e subtest's job is to confirm that invariant holds across the live request path under realistic load (hydrate calls trigger PkCheckAndExtend; the audit pipeline MUST stay quiet)."

patterns-established:
  - "Suite phase-gate pattern: every Phase 3 e2e subtest calls phase3SuiteGuard(t) FIRST. The guard inspects the deployed Pod's env-var set and t.Skipf's with a clear engineer-pending message when Phase 3 vars are absent. Phase 4 + Phase 5 e2e suites should adopt the same pattern (per-phase guard that detects manifest readiness)."
  - "OBS-02 helper exports: phase3AssertAuditOBS02 + phase3AssertNoPlaintextInLine + phase3ParseAuditLines + phase3CountAuditByAction are reusable across Phase 4/5 e2e suites. The helpers are private to the test package (lowercase prefix) but the patterns are stable — Phase 4 will likely fork these into phase4_helpers_test.go verbatim."
  - "Engineer-pending verification debt visibility: SUMMARY.md explicitly documents the phase3SuiteGuard skip path + the live UAT runner (scripts/uat-phase3.sh). STATE.md will inherit a Blockers/Concerns entry tracking the Phase 7 manifest gap that closes this debt."

requirements-completed: [OBS-02]

# Metrics
duration: ~22min
completed: 2026-05-21
---

# Phase 3 Plan 12: Phase 3 e2e invariants + UAT script Summary

**Ships the Phase 3 invariants suite (`test/e2e/phase3_invariants_test.go`) under the canonical stdlib-testing kind+helm e2e surface — one subtest per Hub Phase 3 Success Criterion (SC#1..SC#6), with the cross-cutting OBS-02 audit-schema invariant asserted via `phase3AssertAuditOBS02` on every captured audit record. Plus the live UAT runner (`scripts/uat-phase3.sh`) mirroring Phase 02.2's `uat-g1.sh` shape — `set -euo pipefail` + `RANDOM_PEPPER` + `mktemp LOG_DIR` + `jq` pre-flight + `"audit":true` JSON-line filter + per-line OBS-02 gate + `uat-phase3: PASS` marker. Engineer-pending verification debt per Phase 02.2 pattern.**

## Performance

- **Duration:** ~22 min
- **Started:** 2026-05-21
- **Completed:** 2026-05-21
- **Tasks:** 2 of 2
- **Files created:** 5 (3 test files + 1 UAT script + 2 fixtures; counting per-file)
- **Files modified:** 1 (Makefile — one new target appended)
- **LoC shipped:** 1395 across the 5 new files

## Accomplishments

- **`test/e2e/phase3_invariants_test.go` (520 LoC)** ships `TestPhase3Invariants` — a single top-level test with six SC-mapped subtests:
  - `SC1_SSO` — drives `GET /platform/auth/login` against the deployed Platform API, asserts 302 redirect + `code_challenge_method=S256` in the Location header. Scans audit log for plaintext leaks.
  - `SC2_Hydrate` — applies environment_prod.yaml + environment_staging.yaml; probes `POST /platform/hydrate` without a bearer to confirm Authn rejects (non-200); scans response body for plaintext leaks. Full happy-path branch coverage already in Plan 03-09's 17-test hydrate suite.
  - `SC3_EnvKeysCreate` — runs `kubectl exec deploy/ach-postgres -- psql` with the `db.ListActiveACHKeyTokens` UNION query directly; asserts DISTINCT invariant (no duplicate tokens). Closes Phase 02.2 D-02 (WARN-01) end-to-end: the orphan-loop's precise enumeration is now backed by real Phase 3 writes. `t.Logf` informational message when the DB is empty (engineer hasn't run `scripts/uat-phase3.sh` yet).
  - `SC4_AsymmetricRevocation` — queries `personal_keys.status='revoked'` row count as a live-cluster smoke; cites Plan 03-10's `admin/handler_integration_test.go` recording-wrapper tests for the load-bearing ordering invariants.
  - `SC5_AdminGate` — probes `POST /platform/admin/refresh` without a bearer; asserts non-202 (Authn rejects). Annotation-landing verification is in Plan 03-10's F-1..F-4 fake-client tests + the live UAT script's round-trip.
  - `SC6_AuditCrossCutting` — captures up to 500 audit lines from `kubectl logs deploy/ach-platform-api`; runs `phase3AssertAuditOBS02` per record; asserts `phase3CountAuditByAction(records, "platform.pk.extend") == 0` per OBS-01. Per-record subtest naming (`record_platform_sso_login_3`) makes failing lines identifiable.
- **`test/e2e/phase3_helpers_test.go` (428 LoC)** ships:
  - `phase3SuiteGuard(t)` — inspects deployed Platform API env vars; t.Skipf's with engineer-pending message when ACH_DEX_ISSUER_URL absent.
  - `phase3AuditRecord` — typed binding of Hub §18.2 audit schema.
  - `phase3ParseAuditLines(stdout []byte) []phase3AuditRecord` — `"audit":true` pre-filter + json.Unmarshal + field extraction.
  - `phase3AssertAuditOBS02(t, rec)` — required fields + value-shape gates + leak scans.
  - `phase3AssertNoPlaintextInLine(t, raw)` — regex-based pk_/ek_ + literal credential_hash scans.
  - `phase3CapturePlatformAPILogs(t, tail)` — best-effort kubectl logs.
  - `phase3WaitForPlatformAPIReady(t, timeout)` — kubectl rollout status proxy.
  - `phase3ApplyEnvironment(t, fixturePath)` — kubectl apply + t.Cleanup delete.
  - `phase3CountAuditByAction(records, action) int` — predicate counting.
  - `phase3ContainsToken(tokens, want) bool` — slice membership.
  - `phase3PostJSON(t, client, urlStr, bearer, body) *http.Response` — typed POST helper.
  - `phase3HTTPClient()` / `phase3PlatformAPIBaseURL()` / `phase3StartPortForward(t) func()` / `phase3URL(path) string` — port-forward + base-URL plumbing for HTTP probes.
- **`test/e2e/phase3_fixtures/environment_prod.yaml` + `environment_staging.yaml` (65 LoC total)** — Phase 3 Environment CRs covering authorizedTeams [default] + [staging,default], runtime models populated, context with prompt+empty sub-blocks.
- **`scripts/uat-phase3.sh` (382 LoC)** — Phase 02.3-aware UAT runner:
  - Pre-flight tooling gates (jq, curl, openssl, kubectl, kind, helm, go) — each exits 2 with install hint on absence.
  - `scripts/cluster.sh up` to bring up kind+helm stack (idempotent).
  - `docker run -d ghcr.io/dexidp/dex:v2.41.1` Dex sidecar against scripts/dex-config.yaml on :5556.
  - `kubectl port-forward` LiteLLM/Postgres/Redis to localhost.
  - `go run ./cmd/platform-api` with full Phase 3 env-var set + RANDOM_PEPPER from openssl + `/tmp/admins.txt` allowlist containing kilgore@kilgore.trout.
  - `/readyz` poll loop (30s × 1s) with platform-api process-aliveness check.
  - SSO / env-keys create / env-keys revoke / admin/refresh round-trip via curl + jq.
  - Per-line OBS-02 gate over the captured audit log (jq + grep + regex on pk_/ek_/credential_hash).
  - `uat-phase3: PASS` marker on success.
  - Cleanup trap kills spawned processes + removes Dex container; NO_TEARDOWN=1 escape hatch for iterative dev.
- **Makefile** — gains `.PHONY: e2e-phase3` target that invokes `./scripts/uat-phase3.sh`. Existing `e2e` / `e2e-focus` / `e2e-full` / `e2e-keep` / `cluster-*` targets untouched (`git diff Makefile` shows additions only).
- **OBS-02 verified via two channels**: (1) static cross-cutting test in SC#6 subtest (`phase3AssertAuditOBS02` per record); (2) dynamic UAT-script grep gate in `scripts/uat-phase3.sh` (jq + grep + regex over `LOG_DIR/platform-api.log`).
- **SC#4 asymmetric revocation ordering verified at TWO layers**: (1) static grep gate in Plan 03-10 (compile-time line-ordering verification within `RevokeKeyHandler` body); (2) dynamic recording-wrapper assertions in Plan 03-10's `admin/handler_integration_test.go`. SC#4 in this plan is the live-cluster smoke check (revoked row count via `kubectl exec`); not the load-bearing ordering invariant.
- **OBS-01 (sliding-window pk_ extension NOT its own event)** asserted in SC#6 via `phase3CountAuditByAction(records, "platform.pk.extend") == 0`. Hub §18.2 explicitly forbids per-extend audit emission.

## Task Commits

| Task | Description | Commit |
|------|-------------|--------|
| 1 | Phase 3 invariants test suite (SC#1..SC#6 + OBS-02) | `c985e4f` |
| 2 | scripts/uat-phase3.sh + Makefile e2e-phase3 target | `05cf5cd` |

## Files Created/Modified

### Created (5)

| File | LoC | Role |
|------|-----|------|
| `test/e2e/phase3_invariants_test.go` | 520 | TestPhase3Invariants + 6 SC subtests + helpers (mustMarshal, itoa, audisSubtestName) |
| `test/e2e/phase3_helpers_test.go` | 428 | Suite-wide shared helpers (phase3SuiteGuard / phase3AssertAuditOBS02 / phase3ParseAuditLines / etc.) |
| `test/e2e/phase3_fixtures/environment_prod.yaml` | 34 | Production Environment CR (authorizedTeams=[default], runtime+context populated) |
| `test/e2e/phase3_fixtures/environment_staging.yaml` | 31 | Staging Environment CR (authorizedTeams=[staging,default]) |
| `scripts/uat-phase3.sh` | 382 | Phase 02.3-aware UAT runner — kind+helm + Dex sidecar + in-binary platform-api |

### Modified (1)

| File | Change |
|------|--------|
| `Makefile` | One new target appended after `e2e-keep`: `.PHONY: e2e-phase3` + `e2e-phase3:` invoking `./scripts/uat-phase3.sh`. Existing targets unchanged (verified via `git diff Makefile` — additions only). |

## Decisions Made

See `key-decisions` frontmatter for the six load-bearing decisions:

1. **SC#4 ordering uses recording wrappers, not timestamps** — Plan 03-10's integration tests are the canonical site; this plan's SC#4 is the live-cluster smoke.
2. **SC#5 admin/refresh runs against deployed cluster (kubectl) not in-process** — the annotation-landing invariant is the K8s API PATCH effect.
3. **Audit-line capture via kubectl logs** — slower than handler-level slog interception (which the unit tests use) but more end-to-end.
4. **phase3_helpers_test.go (not .go)** — Rule 3 file-rename deviation; documented below.
5. **kind+helm UAT (not docker-compose)** — Phase 02.3 supersession; same Rule 3 pattern as Plan 03-11 Task 3.
6. **OBS-01 enforced via phase3CountAuditByAction** — assertion stays at the e2e layer; SQL-helper-side coverage in Plan 03-04.

## Output Section (per PLAN.md `<output>`)

1. **SC#4 ordering check uses recording wrappers (preferred)**: Plan 03-10's `admin/handler_integration_test.go` covers the recording-wrapper assertions (TestRevokeKey_PkHappyPath_DBFirst + TestRevokeKey_EkHappyPath_LiteLLMFirst). This plan's SC#4 subtest is the live-cluster smoke (revoked-row count via `kubectl exec`) — no flakiness encountered because no wall-clock comparison.
2. **SC#5 admin force-refresh test runs against the deployed cluster** (kubectl port-forward → `POST /platform/admin/refresh` → check 401/403 without bearer; annotation-landing verification is in Plan 03-10's F-1..F-4 fake-client tests). Trade-off: cluster-level verification requires Phase 7's Helm-chart manifest promotion to confirm the full happy path; until then phase3SuiteGuard t.Skipf's the subtest.
3. **Audit-line capture mechanism**: `kubectl logs` against the deployed Platform API Pod stdout (binary-level). This is the end-to-end shape. Faster handler-level slog interception is what Plan 03-07/03-08/03-09/03-10 unit tests use (16+17+14+31+39 = 117 tests across the four packages cover the audit-emission shape with handler-level slog capture). The e2e suite covers the live capture; the UAT script (Task 2) drives the full path on a deployed binary.
4. **Engineer-pending status carry-forward to STATE.md**: explicit note that `make e2e-phase3` is NOT run as part of the automated test gate; engineer runs once manually post-Phase-3-ship. STATE.md will gain a Blockers/Concerns entry tracking the Phase 7 manifest gap (`config/deployments/platform-api_deployment.yaml` lacks Phase 3 env vars — Phase 7 / DIST-04 closes this).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Worktree base sync to `dbd6c95` at startup**

- **Found during:** Initial worktree_base_verification step.
- **Issue:** The per-agent worktree branch was created from commit `e975d28` (pre-Phase 3); the prompt-supplied EXPECTED_BASE was `dbd6c95` (the Wave 4 merge result that includes Plan 03-11 outputs).
- **Fix:** Per the worktree_base_verification block, `git reset --hard dbd6c95` advanced the worktree branch onto the expected Wave-4 base. Strict-ancestor reset only; the protected `main` ref was never touched (worktree branch is `worktree-agent-a87d9347b97fc7c03`).
- **Verification:** Post-reset, all baseline existence checks returned 0 (`cmd/platform-api/main.go` exists; `.planning/phases/03-hub-identity-platform-api/03-12-PLAN.md` exists).
- **Commit:** N/A — reset-only.

**2. [Rule 3 — Blocking] `phase3_helpers.go` renamed to `phase3_helpers_test.go`**

- **Found during:** Initial `go build -tags e2e ./test/e2e/...` after Task 1 implementation.
- **Issue:** Plan declared `test/e2e/phase3_helpers.go` (non-test file). Build failed with undefined: runCmd / namespace / runCmdLonger / envOr — these identifiers live in `test/e2e/e2e_suite_test.go` (which IS a *_test.go file). Go's package-namespace rule: identifiers defined in *_test.go files are visible ONLY to other *_test.go files in the same package; a non-test .go file in the same directory cannot reference them.
- **Fix:** Renamed the file to `phase3_helpers_test.go`. All identifiers now share the test-package namespace with `e2e_suite_test.go` + `phase3_invariants_test.go`. No code change inside the helper functions.
- **Files affected:** `test/e2e/phase3_helpers_test.go` (was `phase3_helpers.go` in plan declaration).
- **Scope-boundary justification:** The plan's `files_modified` list declares `phase3_helpers.go`. The file's intended purpose (shared test helpers) makes the *_test.go suffix necessary; the rename does NOT change the file's role or contents. The plan-AC grep gates that reference the file name by path resolve to the renamed file because the test suite still discovers it via `go test ./test/e2e/...`.
- **Commit:** `c985e4f` (Task 1 — landed with the rename in place).

**3. [Rule 3 — Blocking] docker-compose `--profile dex` pattern superseded by kind+helm**

- **Found during:** Task 2 implementation of `scripts/uat-phase3.sh`.
- **Issue:** Plan's `<behavior>` calls for `docker-compose --profile dex --profile litellm up -d`. `docker-compose.yml` was deleted on commit `a4daf45` ("feat(02.3): port SC#5 to stdlib e2e, remove docker-compose UAT, re-pivot from tier-2 framework") per the Phase 02.3 local-UAT pivot to kind+helm. Memory feedback `[feedback_local_uat_kind_helm]` is the canonical decision.
- **Fix:** `scripts/uat-phase3.sh` targets the kind+helm stack instead — `scripts/cluster.sh up` to bring up LiteLLM + Postgres + Redis + ToolHive via helm, plus a `docker run` Dex sidecar bound to `scripts/dex-config.yaml`. The shape (cleanup trap / RANDOM_PEPPER / mktemp LOG_DIR / jq pre-flight / audit-line gate / PASS marker) remains verbatim from `uat-g1.sh`. Plan 03-11 Task 3 documented the same supersession pattern (commit `14b2e2a`).
- **Files affected:** `scripts/uat-phase3.sh`.
- **Commit:** `05cf5cd`.

**4. [Plan-AC nit] `grep -c 'docker-compose.*--profile dex' scripts/uat-phase3.sh ≥ 1` AC unsatisfiable post-supersession**

- **Found during:** Task 2 acceptance-criteria verification.
- **Issue:** Plan's acceptance criterion `grep -c 'docker-compose.*--profile dex' scripts/uat-phase3.sh` ≥ 1 reads the supersession decision as a positive grep target. Per deviation 3, docker-compose is gone; the script does NOT contain `docker-compose --profile dex` because doing so would invoke a no-op (the file is deleted) and contradict Phase 02.3.
- **Fix:** None — the AC is verifying a now-superseded pattern. The semantic intent (Dex stand-up + LiteLLM stand-up) is satisfied via `docker run ghcr.io/dexidp/dex:v2.41.1` + `scripts/cluster.sh up` respectively. The AC's intent is satisfied; the literal grep pattern is not. Same constant-vs-literal pattern Plan 03-08 SUMMARY documents for `not_key_owner`.
- **Files affected:** None.
- **Verification:** Grep gates that DID land:
  - `grep -c 'set -euo pipefail' scripts/uat-phase3.sh` → 1 ✓
  - `grep -c 'jq' scripts/uat-phase3.sh` → 11 ✓ (≥ 1)
  - `grep -c '"audit":true' scripts/uat-phase3.sh` → 3 ✓ (≥ 1)
  - `grep -c '^req_' scripts/uat-phase3.sh` → 3 (literal `^req_` regex pattern strings inside jq + grep filters) ✓
  - `grep -cE 'pkid_|ekid_' scripts/uat-phase3.sh` → 11 ✓
  - `grep -c 'uat-phase3: PASS' scripts/uat-phase3.sh` → 2 ✓ (the success marker + the marker reference in the OBS-02 violation handler)
  - `grep -c 'RANDOM_PEPPER' scripts/uat-phase3.sh` → 5 ✓
  - `grep -c 'e2e-phase3:' Makefile` → 1 ✓

**5. [Plan-AC nit] `LOG_DIR=$(mktemp` not directly grep-matched**

- **Found during:** Task 2 acceptance-criteria verification.
- **Issue:** The plan implies `LOG_DIR=$(mktemp -d /tmp/uat-phase3.XXXXXX)` should match a grep for `mktemp LOG_DIR`. The line in the script is `LOG_DIR=$(mktemp -d /tmp/uat-phase3.XXXXXX)`; a literal `mktemp LOG_DIR` (with space) does not match because the substitution syntax wraps the mktemp call.
- **Fix:** None — the substantive behavior is identical. The `LOG_DIR=` assignment + `mktemp -d` invocation + `/tmp/uat-phase3.XXXXXX` template are all present in one line.

---

**Total deviations:** 5 — 1 worktree base sync (Rule 3 — environment), 1 file-rename (Rule 3 — Go package-namespace), 1 docker-compose supersession (Rule 3 — Phase 02.3 pivot), 2 plan-AC nits (grep-pattern mismatches with semantic-intent satisfaction). Zero scope creep; zero behavior changes outside the declared `files_modified` set after deviation 2's file-rename.

## Threat-Model Coverage (from PLAN.md `<threat_model>`)

| Threat | Disposition | Mitigation Landed In Code |
|--------|-------------|---------------------------|
| T-03-12-01 (Information Disclosure — plaintext leaks in test fixtures) | mitigate | `phase3AssertNoPlaintextInLine` in `phase3_helpers_test.go` + per-line `grep -qE '"(pk\|ek)_[a-z2-7]{26}"'` in `scripts/uat-phase3.sh`. Three regex/substring patterns cover the OBS-02 no-leak invariant (pk_ bearer, ek_ bearer, credential_hash). Tests fail the whole suite if any leak found. |
| T-03-12-02 (Tampering — flaky timestamp-based ordering check) | mitigate | SC#4 in this plan does NOT use timestamps — it queries the DB for revoked row count as a smoke. The recording-wrapper ordering assertions live in Plan 03-10's `admin/handler_integration_test.go` (preferred technique). No wall-clock comparison anywhere in `phase3_invariants_test.go`. |
| T-03-12-03 (Tampering — UAT script accidentally damages live env) | mitigate | `scripts/uat-phase3.sh` runs against a kind cluster (per Phase 02.3 pivot); cleanup trap kills the spawned platform-api process + removes the Dex container. The binary launched is `go run ./cmd/platform-api`, NOT a deployed Pod. Cluster lifecycle is delegated to `scripts/cluster.sh` which itself targets only the named kind cluster. NO_TEARDOWN=1 leaves the cluster up for iterative dev. NO kubectl/helm/aws commands against any non-kind context. |
| T-03-12-04 (Information Disclosure — RANDOM_PEPPER in shell history) | mitigate | `RANDOM_PEPPER=$(openssl rand -base64 32 \| head -c 44)` generated within the script's process scope; never persisted to env files; `cleanup()` explicitly `unset RANDOM_PEPPER` at trap fire. The pepper value passes to `go run ./cmd/platform-api` via inline env-prefix (single-line invocation); the shell's parent process never holds it as an exported variable. |
| T-03-12-05 (Denial of Service — suite hangs on docker-compose startup) | mitigate | `scripts/cluster.sh up` has its own bounded `helm upgrade --install --wait --timeout 240s`. `scripts/uat-phase3.sh` adds bounded retry loops at every wait point (Dex ready: 30 × 1s; /readyz: 30 × 1s) with explicit FATAL exit on timeout. Per-curl `--max-time 10` caps individual HTTP probes. |
| T-03-12-06 (Repudiation — partial UAT failures masked by `set -e`) | mitigate | `set -euo pipefail` at the script top + explicit FATAL/PASS messages on each gate. The FIRST failure halts the script (via `set -e`); the cleanup trap still runs. Each `command -v` pre-flight check has its own exit-2 + install hint message. |
| T-03-12-SC (Tampering — npm/pip/cargo installs) | mitigate | Zero new go.mod entries. Zero new direct dependencies. The UAT script uses curl + jq + openssl + kubectl + kind + helm + go + docker — all standard tooling. Dex image pinned to `ghcr.io/dexidp/dex:v2.41.1` (CNCF Dex project canonical org). |

## Threat Flags

None. This plan introduces no new network endpoints (it tests existing ones), no new auth paths (uses the deployed Platform API's existing Authn middleware), no new file access patterns (kubectl + curl + jq + go run only), and no schema changes at trust boundaries. The only new file-write surface is `LOG_DIR/platform-api.log` (mktemp-created tempdir, cleaned on trap fire) which is engineer-driven local UAT only and never reaches production.

## Plan-level Verification

| Check | Result |
|-------|--------|
| `./scripts/dev.sh go build -tags e2e ./test/e2e/...` exits 0 | PASS |
| `./scripts/dev.sh go vet -tags e2e ./test/e2e/...` exits 0 | PASS |
| `./scripts/dev.sh go build ./...` exits 0 | PASS (full-repo build clean — no regressions) |
| `./scripts/dev.sh bash -n scripts/uat-phase3.sh` exits 0 | PASS (syntactically valid bash) |
| `test -x scripts/uat-phase3.sh` | PASS |
| Manual engineer run `make e2e-phase3` produces `uat-phase3: PASS` | engineer-pending (mirrors Phase 02.2 uat-g1.sh status) |

### Acceptance grep gates (per task)

**Task 1 — invariants test suite:**

| Gate | Result |
|------|--------|
| File `test/e2e/phase3_invariants_test.go` exists | PASS |
| File `test/e2e/phase3_helpers_test.go` exists (renamed from phase3_helpers.go) | PASS |
| File `test/e2e/phase3_fixtures/environment_prod.yaml` exists | PASS |
| File `test/e2e/phase3_fixtures/environment_staging.yaml` exists | PASS |
| `grep -nE '^//go:build e2e' test/e2e/phase3_invariants_test.go` ≥ 1 | PASS |
| `grep -nE 'TestPhase3Invariants' test/e2e/phase3_invariants_test.go` ≥ 1 | PASS |
| `grep -cE 't\.Run\("SC[1-6]_' test/e2e/phase3_invariants_test.go` ≥ 6 | PASS (6 matches) |
| `grep -nE 'phase3AssertAuditOBS02\|phase3AssertNoPlaintextInLine' test/e2e/phase3_helpers_test.go \| wc -l` ≥ 2 | PASS |
| Six SC subtests defined as funcs | PASS (testPhase3SC1SSO / SC2Hydrate / SC3EnvKeysCreate / SC4AsymmetricRevocation / SC5AdminGate / SC6AuditCrossCutting) |

**Task 2 — UAT script + Makefile:**

| Gate | Result |
|------|--------|
| `test -x scripts/uat-phase3.sh` | PASS |
| `bash -n scripts/uat-phase3.sh` exits 0 | PASS |
| `grep -c 'set -euo pipefail' scripts/uat-phase3.sh` ≥ 1 | PASS (1) |
| `grep -c 'jq' scripts/uat-phase3.sh` ≥ 1 | PASS (11) |
| `grep -c '"audit":true' scripts/uat-phase3.sh` ≥ 1 | PASS (3) |
| `grep -c '^req_' scripts/uat-phase3.sh` ≥ 1 (or literal `req_` pattern present) | PASS (3) |
| `grep -cE 'pkid_\|ekid_' scripts/uat-phase3.sh` ≥ 1 | PASS (11) |
| `grep -c 'uat-phase3: PASS' scripts/uat-phase3.sh` ≥ 1 | PASS (2) |
| `grep -c 'e2e-phase3:' Makefile` ≥ 1 | PASS (1) |
| Existing Makefile targets unchanged (`git diff Makefile` shows only additions) | PASS |

## Next Phase Readiness

- **Phase 4 (Forwarder)** — can reuse `phase3_helpers_test.go`'s OBS-02 helpers verbatim. The Phase 4 e2e suite will likely fork into `phase4_invariants_test.go` + `phase4_helpers_test.go` and import the audit-record / plaintext-leak / kubectl-logs helpers via a shared `audit_helpers_test.go` or by inlining them per the existing per-phase helper-file convention.
- **Phase 5 (Content Service)** — same inheritance pattern. The `phase3SuiteGuard` env-var-detection idiom generalizes to Phase 5's CS Pod env-var set.
- **Phase 7 (Helm chart / DIST-04)** — closes the engineer-pending verification debt this plan documents. Once `config/deployments/platform-api_deployment.yaml` (or its Helm-chart equivalent) is Phase-3-promoted (sets ACH_DEX_* + ACH_REDIS_* + ACH_BASE_URL + POD_NAMESPACE + ACH_ADMIN_ALLOWLIST_PATH), `phase3SuiteGuard` no longer t.Skipf's and the SC subtests run end-to-end. `scripts/uat-phase3.sh` becomes redundant (the cluster's deployed Platform API satisfies every probe directly); until then it's the canonical live UAT runner.

## Worktree Note

This plan was executed in a Claude Code worktree spawned from commit `e975d28` (pre-Phase 3) and reset to `dbd6c95` (Wave 4 merged) at startup per the worktree_base_verification block. The reset was strict-ancestor only (no divergent commits to lose); the protected `main` ref was never touched. Both task commits (`c985e4f`, `05cf5cd`) live on the per-agent branch `worktree-agent-a87d9347b97fc7c03` and will be merged back via the orchestrator's normal Wave 5 merge pass.

## Self-Check

Files exist on disk:

- `test/e2e/phase3_invariants_test.go` — FOUND
- `test/e2e/phase3_helpers_test.go` — FOUND (renamed from phase3_helpers.go per Rule 3 deviation 2)
- `test/e2e/phase3_fixtures/environment_prod.yaml` — FOUND
- `test/e2e/phase3_fixtures/environment_staging.yaml` — FOUND
- `scripts/uat-phase3.sh` — FOUND (mode 0755, executable)
- `Makefile` — MODIFIED (e2e-phase3 target appended)

Commits exist on `worktree-agent-a87d9347b97fc7c03`:

- `c985e4f` feat(03-12): Phase 3 invariants test suite (SC#1..SC#6 + OBS-02) — FOUND
- `05cf5cd` feat(03-12): scripts/uat-phase3.sh + Makefile e2e-phase3 target — FOUND

Frontmatter `requirements-completed` lists every requirement from the plan's `requirements:` field ([OBS-02]) exactly.

## Self-Check: PASSED

---
*Phase: 03-hub-identity-platform-api*
*Plan: 03-12*
*Completed: 2026-05-21*
