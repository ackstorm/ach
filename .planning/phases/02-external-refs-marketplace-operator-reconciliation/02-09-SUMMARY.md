---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 09
subsystem: operator
tags: [wiring, cmd-operator, manager-runnable, litellm-restclient, op-11, op-13, op-15, d-02, d-11, d-13, d-15, d-17, phase-2-final]

# Dependency graph
requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: |
      Plan 02-01 widened litellm.Client + RESTClient (D-11 swap point);
      Plan 02-02 source fetchers behind registry.For (FetcherFactory contract);
      Plan 02-03 db.{Upsert,Get,Delete,Reset}ExternalRef + db.{...}MarketplacePlugins helpers;
      Plan 02-04 cachefs.IsEmpty + cachefs.SweepTmp + audit.NewLogger (D-17);
      Plan 02-05 PluginReconciler/PromptReconciler/ArtifactReconciler + DB / Fetchers / PluginMaxSizeMiB fields;
      Plan 02-06 PluginMarketplaceReconciler + DB / Fetchers / PluginMaxSizeMiB fields;
      Plan 02-07 snapshot.Snapshotter + EnvironmentReconciler.Snapshotter field;
      Plan 02-08 orphan.Runnable + db.ListACHManagedLitellmUsers / db.ListActiveACHKeyIDs.
provides:
  - "Wired cmd/operator/main.go: real litellm.NewRESTClient, three new manager.Runnables (Secret informer pre-warm + snapshot + orphan), audit logger, OP-11 cache-loss recovery, every reconciler injected with Phase 2 fields"
  - "config.MustEnvDurationAtLeast helper (default + 5-min floor enforcement)"
  - "Five new env-var contract knobs at startup: ACH_LITELLM_BASE_URL / ACH_LITELLM_MASTER_KEY (fail-fast), ACH_LITELLM_AUTH_HEADER / ACH_LITELLM_DANGEROUSLY_LOG_BODIES (consumed by RESTClient), ACH_ORPHAN_CLEANUP_INTERVAL (default 1h, floor 5m)"
  - "main_wiring_envtest_test.go: six TestMainWiring_* tests verifying every wiring assumption (injectability, runnable lifecycle, no-op tick, end-to-end Plugin reconcile, OP-11 empty-cache detection, MustEnvDurationAtLeast reachability)"
affects:
  - Phase 3 (Platform API) — pk_/ek_ minting consumes the live LiteLLM REST surface this wires; the audit logger created here is reused for pk_/ek_ lifecycle events
  - Phase 4 (Forwarder) — relies on the wired snapshotter to drive ExecutionResourcesResolved (forwarder Get-by-name from DB; snapshot kept warm by this loop)
  - Phase 5 (Content Service) — consumes the cached files produced by the wired Plugin/Prompt/Artifact reconcilers via the now-active §10.3 refresh

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Plan-driven main.go wire-up: every Phase 1/2 component is wired with explicit comments cross-referencing the originating plan (Plan 02-05 / 02-06 / 02-07 / 02-09 etc.); preserves the audit trail from spec → plan → wired call site"
    - "Order-sensitive Runnable registration: Secret-informer pre-warm → snapshot Runnable → orphan Runnable → reconciler SetupWithManager (controller-runtime starts Runnables in registration order on manager.Start)"
    - "dbPool open reordered ahead of cache-layout bootstrap so the OP-11 cache-loss recovery (db.Reset*) has an open pool before EnsureLayout completes — documented in a code comment to anchor the future maintainer"
    - "Master-key non-leakage discipline: env var read once into a local, passed by-value into the constructor, never appears in any setupLog.* call; T-02-09-01 mitigation is enforced by audit-grep + code-review, not by a runtime redactor"
    - "envtest wiring test pattern: per-test namespace + per-test cacheRoot + sibling reconciler (not registered with the suite manager) — eliminates the cross-test race where the suite reconciler would also reconcile the test CR"
    - "Assignability across packages with unexported function-type fields: orphan.Runnable exposes ListUsers/ListKeyIDs as exported fields whose underlying named types (listUsersFn / listKeyIDsFn) are unexported; external test code assigns unnamed function literals with matching signatures (Go's assignability rule)"

key-files:
  created:
    - internal/controller/ach/main_wiring_envtest_test.go (6 TestMainWiring_* tests + wiringFakeLiteLLM)
    - .planning/phases/02-external-refs-marketplace-operator-reconciliation/02-09-SUMMARY.md (this file)
  modified:
    - internal/config/config.go (added MustEnvDurationAtLeast helper + time import)
    - internal/config/config_test.go (added TestMustEnvDurationAtLeast — 8 table-driven cases)
    - cmd/operator/main.go (Phase 2 wire-up: imports + env-var fail-fast block + OP-11 reset block + RESTClient swap + audit logger + 3× mgr.Add + 5× reconciler field injection)

key-decisions:
  - "Postgres role assignment is deployment-config concern, not runtime code: ach_operator (Hub spec §5.2.4) is selected via the username embedded in ACH_DB_URL — Plan 01-08 manifest / Plan 01-09 RBAC own the credential, not cmd/operator/main.go. Wiring a SET ROLE call at startup would duplicate that contract."
  - "SweepTmp Runnable wiring deferred (not in plan): cachefs.SweepTmp exists (Plan 02-04) but is not registered as a manager.Runnable in Phase 2 — the reconcilers' os.CreateTemp+rename pattern naturally minimizes orphan .tmp files, so the practical impact is low. Documented in a code comment as a future plan candidate."
  - "dbPool open reordered ahead of cache-layout bootstrap (departure from the original main.go layout): the OP-11 reset calls (db.Reset*RefreshOnEmptyCache) need an open pool before they can run, and they must run inside the cache-layout block (after EnsureLayout, before reconciler registration). The alternative — moving the reset out to a separate Runnable — would obscure the OP-11 contract."
  - "End-to-end Plugin reconcile test isolated to a per-test namespace (NOT WatchNamespace): the suite-registered Plugin reconciler from suite_test.go would otherwise see the test CR and race the assertion. Per-test namespace + direct k8sClient (not cached) sidesteps the suite manager's DefaultNamespaces filter."
  - "Master-key value never logged anywhere: the variable is named liteLLMMasterKey, assigned once from config.MustEnvNonEmpty, passed by-value into litellm.NewRESTClient, and never re-read. setupLog.Error references the KEY NAME (\"ACH_LITELLM_MASTER_KEY\") via the MustEnvNonEmpty wrapper, never the VALUE. T-02-09-01 mitigation."
  - "Snapshotter constructed BEFORE reconciler registrations so it is in mgr.Add order BEFORE the reconciler SetupWithManager calls (controller-runtime starts Runnables in registration order). The snapshot's first refresh races the first Environment reconcile; both paths handle cold-start gracefully (snapshotter returns zero-value LiteLLMSnapshot; EnvironmentReconciler reads via Snapshot() and treats empty as 'every spec entry unresolved')."
  - "EnvironmentReconciler.LiteLLM swap from NoopClient → realLiteLLM is now the ONLY swap point: every reconciler types the LiteLLM field as the litellm.Client interface, so adding a future implementation (mock, recording proxy, etc.) is a single-line edit at the construction site."

patterns-established:
  - "Phase 2 final-wire-up convention: each new mgr.Add call is preceded by a Hub-spec-cross-reference comment (D-XX / OP-XX); each reconciler injection block lists Phase 2 fields under a `// Phase 2 (Plan 02-09):` marker"
  - "envtest verification of cmd/operator/main.go wiring assumptions — TestMainWiring_* sibling-reconciler pattern: construct reconciler structs identically to main.go, exercise Reconcile() directly, assert without going through SetupWithManager so the suite manager isn't conflicted"
  - "Fail-fast env-var pattern continued: ACH_LITELLM_* knobs follow the D-08/D-09 pattern (MustEnvNonEmpty + os.Exit(1) on miss). ACH_ORPHAN_CLEANUP_INTERVAL extends the pattern with a minimum-value enforcement via the new MustEnvDurationAtLeast helper"

requirements-completed:
  - OP-03
  - OP-04
  - OP-06
  - OP-07
  - OP-08
  - OP-09
  - OP-11
  - OP-13
  - OP-15

# Metrics
duration: ~14min
completed: 2026-05-17
---

# Phase 2 Plan 09: Operator Wire-Up Summary

**Final Phase 2 wire-up — composes every Plan 02-02..02-08 product into cmd/operator/main.go: real LiteLLM RESTClient swap, three new manager.Runnables (Secret informer pre-warm + snapshot + orphan), OP-11 cache-loss recovery, audit logger, and Phase 2 field injection across all five external-ref reconcilers.**

## Performance

- **Duration:** ~14 min
- **Started:** 2026-05-17T08:52:08Z
- **Completed:** 2026-05-17T09:06:27Z
- **Tasks:** 3
- **Files modified:** 4 (1 created + 3 modified)

## Accomplishments

- Wired the live LiteLLM `RESTClient` into `EnvironmentReconciler.LiteLLM`, `snapshot.NewSnapshotter`, and `orphan.NewRunnable`. The Phase 1 `NoopClient` no longer ships in cmd/operator/main.go.
- Added `config.MustEnvDurationAtLeast` with 8 table-driven test cases (default / above-min / at-min / below-min / zero / negative / non-parseable / unit-missing); used by `ACH_ORPHAN_CLEANUP_INTERVAL` with default `1h` and minimum `5m` (OP-15 / D-15).
- Wired `cachefs.IsEmpty` + `db.ResetExternalRefRefreshOnEmptyCache` + `db.ResetMarketplacePluginsRefreshOnEmptyCache` on empty-PVC startup so a fresh PVC against a stale DB re-fetches every external reference (OP-11). dbPool open reordered ahead of cache-layout bootstrap so the reset calls have a pool to write through.
- Registered three new `mgr.Add` Runnables in registration order: `corev1.Secret` informer pre-warm (D-11), `snapshot.Snapshotter` (D-13 / OP-13), `orphan.Runnable` (D-15 / D-16 / OP-15).
- Constructed `audit.NewLogger(os.Stdout)` once at startup; passed by-value into `orphan.NewRunnable` as the audit emission destination. Phase 3 will reuse the same logger for pk_/ek_ lifecycle events.
- Injected Phase 2 fields into all four external-ref reconcilers: `Plugin` + `PluginMarketplace` receive `DB / PluginMaxSizeMiB / Fetchers=nil`; `Artifact` + `Prompt` receive `DB / Fetchers=nil`; `Environment` receives `Snapshotter` + the real `LiteLLM`. `BackendIdentityPolicy` is unchanged (Phase 4 territory).
- Added `main_wiring_envtest_test.go` with six `TestMainWiring_*` tests verifying every wiring assumption.
- Full repo build, vet, and test suite (Phase 1 + every Phase 2 plan) all pass after the wire-up.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add config.MustEnvDurationAtLeast helper + unit tests** — `719ad7f` (feat)
2. **Task 2: Wire all Phase 2 components into cmd/operator/main.go** — `3847c2c` (feat)
3. **Task 3: envtest wiring verification — TestMainWiring_*** — `af6c1df` (test)

_Note: SUMMARY commit follows separately._

## Files Created/Modified

- `internal/config/config.go` — Added `MustEnvDurationAtLeast(key, defaultDur, minDur)` helper + `time` import.
- `internal/config/config_test.go` — Added `TestMustEnvDurationAtLeast` table-driven (8 cases).
- `cmd/operator/main.go` — Phase 2 wire-up:
  - **Imports:** `time`, `corev1`, `audit`, `orphan`, `snapshot`.
  - **New env vars:** `ACH_LITELLM_BASE_URL` (fail-fast required), `ACH_LITELLM_MASTER_KEY` (fail-fast required, never logged), `ACH_ORPHAN_CLEANUP_INTERVAL` (default 1h, floor 5m via `MustEnvDurationAtLeast`). `ACH_LITELLM_AUTH_HEADER` + `ACH_LITELLM_DANGEROUSLY_LOG_BODIES` are read internally by `litellm.NewRESTClient` (Plan 02-01 contract).
  - **Reordered:** dbPool open moved ahead of cache-layout bootstrap so the OP-11 reset calls have an open pool.
  - **OP-11 recovery:** `cachefs.IsEmpty(cacheRoot)` → `db.ResetExternalRefRefreshOnEmptyCache` + `db.ResetMarketplacePluginsRefreshOnEmptyCache` + loud-warn `setupLog.Info`.
  - **RESTClient swap:** `litellm.NewNoopClient` replaced with `litellm.NewRESTClient(liteLLMBaseURL, liteLLMMasterKey, ctrl.Log.WithName("litellm"))` — single source of truth for every Phase 2 LiteLLM call.
  - **Audit logger:** `audit.NewLogger(os.Stdout)` constructed once.
  - **Three new mgr.Add Runnables** (registered BEFORE reconciler SetupWithManager, in this order):
    1. `mgr.GetCache().GetInformer(ctx, &corev1.Secret{})` — Secret informer pre-warm (D-11).
    2. `snapshot.NewSnapshotter(realLiteLLM, log)` — LiteLLM resource cache (D-13 / OP-13).
    3. `orphan.NewRunnable(realLiteLLM, dbPool, auditLog, orphanInterval, log)` — orphan-cleanup (D-15 / D-16 / OP-15).
  - **Reconciler field injections** (5 of 6 reconcilers updated; BackendIdentityPolicy unchanged):
    - `EnvironmentReconciler` — `LiteLLM: realLiteLLM`, `Snapshotter: snapshotter`.
    - `PluginReconciler` — `DB: dbPool`, `PluginMaxSizeMiB: pluginMaxSizeMiB`, `Fetchers: nil`.
    - `PluginMarketplaceReconciler` — `DB: dbPool`, `PluginMaxSizeMiB: pluginMaxSizeMiB`, `Fetchers: nil`.
    - `ArtifactReconciler` — `DB: dbPool`, `Fetchers: nil`.
    - `PromptReconciler` — `DB: dbPool`, `Fetchers: nil`.
  - **NOTE comment:** `cachefs.SweepTmp` Runnable wiring is deferred to a future plan (documented inline).
- `internal/controller/ach/main_wiring_envtest_test.go` (NEW) — Six envtests:
  - `TestMainWiring_AllReconcilersInjectable` — every Phase 2 reconciler constructs cleanly with the same field shape as cmd/operator/main.go.
  - `TestMainWiring_Snapshotter_StartAndShutdown` — snapshot Runnable starts, drives a refresh (RefreshedAt non-zero, Models populated), exits cleanly on ctx cancel.
  - `TestMainWiring_OrphanRunnable_EmptyUserSet_NoAuditEvents` — TickOnce against empty user-set is a clean no-op (zero audit bytes, zero ListUserKeys calls).
  - `TestMainWiring_PluginReconciler_EndToEndWithFakeFetcher` — full §10.3 fetch→stage→fsync→rename(2)→status pipeline against a fake fetcher in an isolated namespace; asserts cached file content, status.UpstreamRev, status.StorageLocation, SourceReachable=True reason=Synced, zero orphan .tmp files.
  - `TestMainWiring_CacheEmptyDetection` — mirrors OP-11 startup branch; EnsureLayout → IsEmpty=true; write one file → IsEmpty=false.
  - `TestMainWiring_MustEnvDurationAtLeast_Floor` — duplicate-of-Task-1 coverage verifying the helper is reachable from this package.

## Decisions Made

See `key-decisions` in frontmatter for the full set. Highlights:

- **Postgres role assignment is deployment-config concern, not runtime code.** `ach_operator` (Hub §5.2.4) is selected via the credential embedded in `ACH_DB_URL` — Plan 01-08 manifest / Plan 01-09 RBAC own the credential. No `SET ROLE` is needed in cmd/operator/main.go.
- **SweepTmp Runnable wiring deferred** to a future plan (documented inline in main.go). Practical impact is low because the reconcilers' `os.CreateTemp + rename` pattern minimizes orphan staging files.
- **dbPool open reordered** ahead of the cache-layout bootstrap so the OP-11 reset calls have an open pool. The alternative — making the reset its own Runnable — would obscure the OP-11 contract.
- **End-to-end Plugin reconcile test isolated to a per-test namespace** (NOT WatchNamespace) so the suite-registered Plugin reconciler doesn't race the assertion.
- **Master-key non-leakage discipline** enforced by audit-grep + code-review (T-02-09-01 mitigation): the value appears in exactly two places — the assignment from `MustEnvNonEmpty` and the pass-by-value into `NewRESTClient`.

## Deviations from Plan

None — plan executed exactly as written.

Three minor adjustments noted (all already documented in the plan text):

1. The plan's "Change 6" suggested importing only `"github.com/ackstorm/ach/internal/db"` ("already imported in Phase 1 — verify"). Verified — the import was already present from Phase 1.
2. The plan's "Change 3" called out the dbPool reorder explicitly; implemented as instructed.
3. The plan's Task 3 sub-bullet "1. TestMainWiring_AllReconcilersInjectable" notes: "skip the actual call to avoid duplicate watches against the envtest manager which already has Phase 1 reconcilers registered — verify by inspecting the assembled struct only." Implemented exactly that way.

**Total deviations:** 0 auto-fixed.
**Impact on plan:** None — the plan was tight; execution matched.

## Issues Encountered

- **Stray `operator` binary in working tree** during Task 2 verification (left by `go build ./cmd/operator/...`). Removed before the Task 2 commit so the commit was minimal (cmd/operator/main.go only). Production builds always use `make build` which writes to `bin/` (already in .gitignore). Not a deviation — pure local-tooling artifact.

## Phase 2 Complete

All nine subsidiary plans land:

1. `02-01` — Lifted `litellm.RESTClient` from sister project; widened `Client` interface.
2. `02-02` — Six source fetchers (github, gitlab, bitbucket, s3, gcs, http) behind `sources.Fetcher` / `registry.For`.
3. `02-03` — Postgres helpers for `external_refs` + `marketplace_plugins` (UPSERT/GET/DELETE/Reset + force-refresh column).
4. `02-04` — `cachefs.SweepTmp` + `cachefs.IsEmpty` + `audit.NewLogger` (D-17 audit=true predicate).
5. `02-05` — `PluginReconciler` / `PromptReconciler` / `ArtifactReconciler` Phase 2 refresh loop via `materializeExternalRef`.
6. `02-06` — `PluginMarketplaceReconciler` three-stage refresh; RE2 filters; cross-marketplace name conflict.
7. `02-07` — `snapshot.Snapshotter` + `EnvironmentReconciler.ExecutionResourcesResolved`.
8. `02-08` — `orphan.Runnable` per Hub §18.4 (orphan LiteLLM key cleanup).
9. `02-09` (this plan) — Final wire-up in `cmd/operator/main.go` + envtest wiring verification.

The Phase 2 SC #1–5 enumerated in `.planning/ROADMAP.md` are now collectively achievable by the running Operator:

- SC #1 — external-ref refresh loop ships per OP-03/OP-04.
- SC #2 — `PluginMarketplace` three-stage refresh ships per OP-06/OP-07/OP-08; per-plugin failure handling per D-10.
- SC #3 — `ExecutionResourcesResolved` ships per OP-13.
- SC #4 — orphan LiteLLM key cleanup ships per OP-15.
- SC #5 — Operator boots with Phase 2 env vars set, all runnables registered, all reconcilers wired.

## User Setup Required

External services (LiteLLM Proxy, Postgres) require manual deployment configuration. The plan's `user_setup` block enumerates the new env vars and their sources:

- **ACH_LITELLM_BASE_URL** (required) — set in Operator Pod env block (Plan 01-08); example: `http://litellm.ach-system.svc.cluster.local:4000`.
- **ACH_LITELLM_MASTER_KEY** (required) — Kubernetes Secret `ach-litellm-master-key.value` mounted via envFrom on the Operator container. Generate with `openssl rand -hex 32` → set as litellm-proxy master key in LiteLLM deployment + matching ACH Secret.
- **ACH_LITELLM_AUTH_HEADER** (optional, default `Authorization: Bearer`) — override to `x-litellm-api-key` only if LiteLLM 1.83.11+ behavior changes (Plan 02-01 escape hatch).
- **ACH_LITELLM_DANGEROUSLY_LOG_BODIES** (optional, default redaction-on) — set to `true` ONLY for local-dev debugging.
- **ACH_ORPHAN_CLEANUP_INTERVAL** (optional, default `1h`) — Go time.Duration format. Operator refuses to start when `0`, negative, non-parseable, or `< 5m` (D-15 floor).

Deployment-layer note: LiteLLM must be reachable from the ACH namespace before the Operator starts; otherwise the initial Snapshotter refresh logs `litellm_unreachable_total++` and the orphan Runnable runs no-op ticks until LiteLLM is reachable. This is not blocking — the manager probes still pass — but it degrades `ExecutionResourcesResolved` until LiteLLM comes up.

## Next Phase Readiness

- Phase 2 complete; the live Operator can be deployed against a real LiteLLM + Postgres + kind cluster and will execute the full §10.3 refresh loop, drive `ExecutionResourcesResolved` per OP-13, and run the orphan-cleanup ticker per OP-15.
- Phase 3 (Platform API + Dex SSO) can now begin: it consumes the wired `audit.NewLogger` for pk_/ek_ lifecycle events, the wired `db.ResetExternalRefRefreshOnEmptyCache` for the force-refresh-via-column model, and the live LiteLLM resource cache for `Environment.status.AccessGroupSynced` writes.
- Phase 4 (Forwarder) can also begin: it relies on the snapshotter to drive `ExecutionResourcesResolved` (forwarder reads from `<kind>_objects.status_json` only, but the snapshot must stay warm or `Environment` status goes stale).
- Phase 5 (Content Service) can also begin: it streams the cached files produced by the now-active Plugin/Prompt/Artifact reconcilers.

**Blockers carried forward:** Phase 1 manifest gap (`config/secrets/db_url_secret.yaml` points at a non-existent `ach-postgres` service). Resolution is Phase 7 (Helm chart) or a `config/dev-postgres/` overlay — not a Phase 2 concern.

## Self-Check

Verified before SUMMARY commit:

- `internal/config/config.go` contains `func MustEnvDurationAtLeast` — **FOUND**.
- `internal/config/config_test.go` contains `TestMustEnvDurationAtLeast` with 8 cases — **FOUND** (all 8 pass: `--- PASS: TestMustEnvDurationAtLeast (0.00s)` + 8 subtests).
- `cmd/operator/main.go` contains `litellm.NewRESTClient` — **FOUND** (line 271).
- `cmd/operator/main.go` does NOT contain `litellm.NewNoopClient` — **CONFIRMED ABSENT** (NoopClient lives in internal/litellm only; the test suite still uses it via suite_test.go).
- `cmd/operator/main.go` contains `snapshot.NewSnapshotter` / `orphan.NewRunnable` / `audit.NewLogger` / `cachefs.IsEmpty` — **ALL FOUND** (lines 389 / 396 / 277 / 246).
- `cmd/operator/main.go` contains 3 new env vars — **FOUND** (`ACH_LITELLM_BASE_URL` line 188, `ACH_LITELLM_MASTER_KEY` line 193, `ACH_ORPHAN_CLEANUP_INTERVAL` line 200).
- `cmd/operator/main.go` master-key references — only assignment (line 193) and pass-by-value into NewRESTClient (line 271); no log call carries the value (T-02-09-01 mitigation).
- `internal/controller/ach/main_wiring_envtest_test.go` contains 6 `^func TestMainWiring_*` definitions — **FOUND** (all 6 pass).
- Phase 1 envtests still pass: full `./internal/controller/ach/...` suite returns `ok` (14.243s).
- Full repo `go test ./... -count=1` returns `ok` for every package; `go vet ./...` is clean; `go build ./...` succeeds.
- All three task commits present in `git log --oneline 1a791d7..HEAD`: `af6c1df` (Task 3) / `3847c2c` (Task 2) / `719ad7f` (Task 1).

## Self-Check: PASSED

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Plan: 09 (final wire-up)*
*Completed: 2026-05-17*
