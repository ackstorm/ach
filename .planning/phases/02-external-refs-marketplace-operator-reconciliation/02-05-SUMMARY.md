---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 05
subsystem: operator
tags: [go, controller-runtime, external-refs, refresh-loop, plugin, prompt, artifact, materialize, rename2, conditional-get, size-cap, OP-03, OP-04, OP-09, OP-12, D-04, D-05, D-06, D-07, D-11, D-12]

# Dependency graph
requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation/02-02
    provides: internal/sources package (Fetcher interface, SourceSpec, FetchRequest, FetchResult, ErrUnauthorized/ErrNotFound/ErrUnreachable/ErrUpstreamInvalid sentinels, registry.For dispatcher)
  - phase: 02-external-refs-marketplace-operator-reconciliation/02-03
    provides: internal/db.ExternalRef struct + UpsertExternalRef / GetExternalRef / DeleteExternalRef helpers; migration 000002 with external_refs.upstream_rev column
  - phase: 02-external-refs-marketplace-operator-reconciliation/02-04
    provides: internal/cachefs.EnsureLayout (used in test fixtures); audit handler (unused this plan); SweepTmp (unused this plan)

provides:
  - "ExternalRefStatus.UpstreamRev field on the shared status surface (Plugin / Prompt / Artifact / PluginMarketplace) — recorded after each successful refresh; observable via kubectl get -o jsonpath='{.status.upstreamRev}'"
  - "internal/controller/ach/conditions.go declaring the 8-reason closed enum for SourceReachable (Synced / Unreachable / Unauthorized / NotFound / UpstreamInvalid / InvalidConfig / PluginTooLarge / StaleCacheExpired) + Phase 1 Initializing carry-forward + setExternalRefCondition wrapper"
  - "internal/controller/ach/external_ref_refresh.go shared §10.3 refresh helper: materializeExternalRef + ExternalRefRefreshDeps + MaterializeResult + OversizeError + classifyFetchError + computeFinalPath + buildSourceSpec + extractAuthSecretRef + requeueDurationFromRefresh + FetcherFactory indirection"
  - "PluginReconciler steady-state: §10.3 refresh with D-12 size-cap enforcement (PluginMaxSizeMiB << 20), D-07 force-refresh annotation removal, OP-12 db.DeleteExternalRef in deletion path"
  - "PromptReconciler steady-state: §10.3 refresh, kind='prompt', no size cap, raw-bytes cache path"
  - "ArtifactReconciler steady-state: §10.3 refresh, kind='artifact', no size cap, spec.Scope drives object vs directory cache path, deletion-path narrowed to recorded StorageLocation when set"
  - "15 envtest test functions (40 RUN entries with subtests): full helper-level coverage + Plugin reconciler happy-path + force-refresh annotation"

affects:
  - 02-06 (PluginMarketplace reconciler): may consume setExternalRefCondition + the 8 Reason* constants for its Synced condition (reasons partially overlap; marketplace adds NameConflict / UnsupportedPluginSource on top)
  - 02-09 (cmd/operator/main.go): MUST inject PluginReconciler.{DB, PluginMaxSizeMiB, Fetchers}, PromptReconciler.{DB, Fetchers}, ArtifactReconciler.{DB, Fetchers} at SetupWithManager time. Production wiring uses dbPool + ACH_PLUGIN_MAX_SIZE_MIB int + registry.For (the default when Fetchers is nil).

# Tech tracking
tech-stack:
  added: []  # no new go.mod entries; reuses Phase 1 + Plans 02-02 / 02-03
  patterns:
    - "Shared steady-state helper pattern: per-kind reconcilers build an ExternalRefRefreshDeps and call one materializeExternalRef → no §10.3 logic duplication across Plugin/Prompt/Artifact"
    - "FetcherFactory indirection: production reconcilers pass nil → defaults to registry.For; envtest injects a fake factory for deterministic §10.3 staging/rename(2)/UPSERT assertions"
    - "Closed-enum reason vocabulary in one file: every reconciler imports Reason* constants; setExternalRefCondition writes them via apimeta.SetStatusCondition"
    - "Staleness escalation (OP-04): transient Unreachable → StaleCacheExpired when now − last_successful_refresh > maxStaleness AND latest fetch failed; terminal reasons (Unauthorized/NotFound/UpstreamInvalid/PluginTooLarge) do NOT escalate"
    - "Crash safety between rename(2) and UPSERT: next reconcile fetches fresh, stages, renames over existing file (atomic per POSIX), UPSERTs — idempotent"
    - "Failure dispatch policy: terminal-upstream / config-derived reasons return RequeueAfter+nil (won't change by retrying); transient errors return the err so controller-runtime workqueue applies exponential backoff"

key-files:
  created:
    - internal/controller/ach/conditions.go
    - internal/controller/ach/external_ref_refresh.go
    - internal/controller/ach/external_ref_refresh_test.go
  modified:
    - api/ach/v1alpha1/external_ref_types.go (added UpstreamRev string field)
    - config/crd/bases/ach.ackstorm.ai_plugins.yaml (regenerated via controller-gen)
    - config/crd/bases/ach.ackstorm.ai_prompts.yaml (regenerated)
    - config/crd/bases/ach.ackstorm.ai_artifacts.yaml (regenerated)
    - config/crd/bases/ach.ackstorm.ai_pluginmarketplaces.yaml (regenerated)
    - internal/controller/ach/plugin_controller.go (steady-state body replaced; struct extended with DB / PluginMaxSizeMiB / Fetchers; deletion path gains DeleteExternalRef)
    - internal/controller/ach/prompt_controller.go (steady-state body replaced; struct extended with DB / Fetchers)
    - internal/controller/ach/artifact_controller.go (steady-state body replaced; struct extended with DB / Fetchers; deletion path narrowed)

key-decisions:
  - "ExternalRefStatus.UpstreamRev is a value-type string, so controller-gen's *out = *in shallow copy at the top of DeepCopyInto already covers it — no zz_generated.deepcopy.go diff. Verified by inspecting the regenerated file."
  - "NOT adding UpstreamRev to a printcolumn. Commit SHAs at 40 chars would force-truncate Age and SourceReachable in kubectl get -o wide output; operators read it via jsonpath when needed."
  - "FetcherFactory is per-reconciler-struct (not a global). Production wires nil → registry.For by default in materializeExternalRef; tests inject per-test. Keeps DI surface minimal and reads-against-cache trivial."
  - "Failure-path retry policy is reason-dependent: PluginTooLarge / Unauthorized / NotFound / UpstreamInvalid return ctrl.Result{RequeueAfter}, nil (no hot-loop on configuration-derived errors); Unreachable / StaleCacheExpired return ctrl.Result{}, result.Err (controller-runtime applies exponential backoff). This is the canonical retry-policy split per Hub §6.6."
  - "ArtifactReconciler deletion-path narrowing: when cr.Status.StorageLocation was populated by a prior Phase 2 reconcile, remove EXACTLY that path. Fall back to Phase 1's try-both-paths sweep only when StorageLocation is empty (which catches CRs that existed before Phase 2 reconcile ran). Avoids unnecessary file-system probes in steady state."
  - "Nil-DB tolerance preserved across all three reconcilers' steady-state branches (GetExternalRef / UpsertExternalRef / DeleteExternalRef all guarded by `if r.DB != nil`). Allows the Phase 1 envtest finalizer tests to stay green without spinning up a Postgres pool. Plan 02-09 wires the real pool from cmd/operator/main.go."
  - "Phase 2 envtest does NOT spin up testcontainers Postgres. The 15 new tests use DB=nil and exercise materializeExternalRef end-to-end via a fakeFetcher — the §10.3 fetch/stage/rename/conditions/annotation paths are fully covered without Docker, keeping `make test` Docker-free per Phase 1 convention. Integration tests against Postgres are Plan 02-03's responsibility."
  - "Test compromise on reconciler races: the suite_test.go registers PluginReconciler WITHOUT a Fetchers factory, so the suite reconciler's calls go through registry.For and immediately fail on the missing auth secret. The two Phase 2 envtest reconciler tests build their OWN PluginReconciler with the fake factory and use Eventually() to assert final-state — the race produces `status update failed` log warnings (k8s 'object modified' conflicts) but tests pass deterministically because the assertion target is the eventual-consistent CR state, not the per-Reconcile outcome."

patterns-established:
  - "Shared steady-state helper for like-shaped reconcilers (Plugin/Prompt/Artifact). When the next plan adds Marketplace (02-06), its Stage-2 per-plugin path can reuse materializeExternalRef per-plugin or factor a similar helper from it."
  - "OversizeError + classifyFetchError typed-error switch pattern: future reasons (e.g. ContentLength/Checksum mismatch in a hypothetical v1beta1) add a new typed error + an errors.As branch in classifyFetchError without disturbing any callers."
  - "Reconciler reads PriorRev once per Reconcile via GetExternalRef → passes to materializeExternalRef.PriorRev → fetcher uses for conditional-GET. The single-read-per-Reconcile cadence keeps DB load proportional to the reconcile rate."

requirements-completed:
  - OP-03
  - OP-04
  - OP-09
  - OP-12

# Metrics
duration: ~35min
completed: 2026-05-17
---

# Phase 02 Plan 05: Plugin/Prompt/Artifact Steady-State Refresh Summary

**Phase 2 §10.3 refresh loop (fetch → stage → fsync → rename(2) → DB UPSERT) wired into Plugin / Prompt / Artifact reconcilers via a single shared materializeExternalRef helper. D-12 plugin size cap enforced via io.LimitReader before rename(2); D-07 force-refresh annotation cleared on success; OP-12 deletion drops the DB row in sync with the cached file.**

## Performance

- **Duration:** ~35 min (wave-2 executor agent in worktree)
- **Started:** 2026-05-17T07:46:30Z (approximate — agent spawned after wave 1 merge)
- **Completed:** 2026-05-17T08:22:06Z
- **Tasks:** 5 / 5
- **Files created:** 3 (conditions.go, external_ref_refresh.go, external_ref_refresh_test.go)
- **Files modified:** 8 (1 types file + 4 regen'd CRD YAMLs + 3 reconciler files)

## Accomplishments

- **ExternalRefStatus.UpstreamRev** lands on the shared status surface used by Plugin / Prompt / Artifact / PluginMarketplace. The four CRD YAMLs regenerated cleanly via `make manifests generate`; no zz_generated.deepcopy.go diff because the field is a value-type string already covered by `*out = *in`.
- **internal/controller/ach/conditions.go** declares the closed 8-reason enum (Hub §6.6) + `setExternalRefCondition` wrapper around `apimeta.SetStatusCondition`. Reason constants for Phase 2: `ReasonSynced` / `ReasonUnreachable` / `ReasonUnauthorized` / `ReasonNotFound` / `ReasonUpstreamInvalid` / `ReasonInvalidConfig` / `ReasonPluginTooLarge` / `ReasonStaleCacheExpired` + Phase 1 `ReasonInitializing` carry-forward.
- **internal/controller/ach/external_ref_refresh.go** implements `materializeExternalRef` end-to-end: auth Secret resolve (D-11 informer cache) → source dispatch via registry.For (overridable through `FetcherFactory` for tests) → conditional-GET shortcut → `.tmp/stg-<random>` staging via `os.CreateTemp` → optional `io.LimitReader(body, max+1)` cap (D-12) → fsync → atomic `os.Rename` → `db.UpsertExternalRef`. Crash-safe per §10.3: a death between rename and UPSERT is benign because the next reconcile re-stages, re-renames over the existing file, and re-UPSERTs.
- **classifyFetchError** maps wrapped sources sentinels + OversizeError → Hub §6.6 reasons; staleness escalation (OP-04) flips Unreachable → StaleCacheExpired when prior successful refresh exists AND `now − lastRefresh > maxStaleness`. Terminal/configuration-derived reasons (Unauthorized/NotFound/UpstreamInvalid/PluginTooLarge) do NOT escalate even when the window is exhausted.
- **PluginReconciler steady-state** delegates to `materializeExternalRef` with `SizeCapBytes = int64(PluginMaxSizeMiB) << 20`. Failure dispatch policy: PluginTooLarge / Unauthorized / NotFound / UpstreamInvalid return `ctrl.Result{RequeueAfter: …}, nil` (no hot-loop on configuration-derived errors); transient Unreachable / StaleCacheExpired return `ctrl.Result{}, result.Err` (controller-runtime workqueue applies exponential backoff). D-07 annotation removed in a second `r.Update` after the status PATCH.
- **PromptReconciler steady-state** mirrors Plugin with kind="prompt", `SizeCapBytes=0`, cache path `prompt/<name>` (raw bytes). Deletion-path adds `db.DeleteExternalRef` before RemoveFinalizer (OP-12 parity with Plugin).
- **ArtifactReconciler steady-state** mirrors Plugin with kind="artifact", `SizeCapBytes=0`, cache path driven by `spec.Scope` (object → `artifact/<name>`; directory → `artifact/<name>.tar.gz`). Deletion-path NARROWS to `cr.Status.StorageLocation` when populated, falling back to Phase 1's "try both paths" sweep for backwards compatibility.
- **15 envtest test functions** (40 RUN entries with subtests) cover all helpers + materializeExternalRef + Plugin reconciler success/force-refresh — all 40 PASS in 9.8 seconds.

## Task Commits

Each task was committed atomically on `worktree-agent-ad0ff939059f2eaa7`:

1. **Task 1: ExternalRefStatus.UpstreamRev field + CRD regen** — `e13c52e` (feat)
2. **Task 2: external_ref_refresh.go + conditions.go** — `4347c92` (feat)
3. **Task 3: PluginReconciler steady-state via materializeExternalRef** — `a12fa23` (feat)
4. **Task 4: Prompt + Artifact reconcilers refactor** — `fc4b8ae` (feat)
5. **Task 5: envtest coverage (15 test funcs / 40 RUN entries)** — `22e1ed8` (test)

_Plan metadata commit (SUMMARY.md) follows this commit; STATE/ROADMAP updates are the orchestrator's responsibility after the wave merges._

## Files Created/Modified

### Created (3)

- `internal/controller/ach/conditions.go` (~115 lines) — 8 Reason* constants + `setExternalRefCondition` helper.
- `internal/controller/ach/external_ref_refresh.go` (~470 lines) — `FetcherFactory` type + `OversizeError` struct + `ExternalRefRefreshDeps` / `MaterializeResult` structs + `materializeExternalRef` (8-step §10.3 implementation) + `classifyFetchError` (sentinel switch + OP-04 escalation) + `computeFinalPath` + `buildSourceSpec` + `extractAuthSecretRef` + `requeueDurationFromRefresh`.
- `internal/controller/ach/external_ref_refresh_test.go` (~630 lines) — fakeFetcher + fakeFactory + newCacheRoot helper + 15 test functions (TestComputeFinalPath, TestClassifyFetchError_AllReasons with 6 subtests, TestClassifyFetchError_StalenessEscalation, TestClassifyFetchError_NoEscalationForUnauthorized, TestBuildSourceSpec_Github, TestExtractAuthSecretRef, six TestMaterializeExternalRef_* tests, TestPluginReconciler_SteadyState_Success, TestPluginReconciler_ForceRefreshAnnotation_Cleared).

### Modified (8)

- `api/ach/v1alpha1/external_ref_types.go` — appended UpstreamRev string field after LastSuccessfulRefresh in ExternalRefStatus.
- `config/crd/bases/ach.ackstorm.ai_plugins.yaml` — controller-gen added `upstreamRev: type: string` under `properties.status.properties`.
- `config/crd/bases/ach.ackstorm.ai_prompts.yaml` — same.
- `config/crd/bases/ach.ackstorm.ai_artifacts.yaml` — same.
- `config/crd/bases/ach.ackstorm.ai_pluginmarketplaces.yaml` — same.
- `internal/controller/ach/plugin_controller.go` — struct extended with DB / PluginMaxSizeMiB / Fetchers; deletion path adds DeleteExternalRef; steady-state branch replaced with §10.3 refresh + D-07 annotation handling; writeStatus helper removed (its functionality is inline in setExternalRefCondition + r.Status().Update).
- `internal/controller/ach/prompt_controller.go` — same shape as Plugin minus PluginMaxSizeMiB; kind="prompt"; no size cap.
- `internal/controller/ach/artifact_controller.go` — same shape as Prompt; kind="artifact"; spec.Scope drives computeFinalPath; deletion-path narrowed.

## Decisions Made

- **No zz_generated.deepcopy.go diff for UpstreamRev.** controller-gen's `*out = *in` shallow copy at the top of `DeepCopyInto(*ExternalRefStatus)` already covers value-type fields. Verified by inspecting the regenerated file. The plan's `<files>` list includes `zz_generated.deepcopy.go` but no actual change was required — declaring this explicitly in case a reviewer checks the diff.
- **No printcolumn for UpstreamRev.** kubectl get -o wide is bounded to ~80 chars and a 40-char SHA would force-truncate Age and SourceReachable. Operators access UpstreamRev via `kubectl get plugin <name> -o jsonpath='{.status.upstreamRev}'` when needed.
- **FetcherFactory lives on the per-reconciler struct, not as a global.** Production wires `nil` and `materializeExternalRef` falls back to `registry.For`; tests inject a fake on a per-test `PluginReconciler`. This keeps the DI surface minimal and the suite_test.go scaffold unchanged (the suite reconciler still uses `registry.For` because Phase 1 tests pre-date the field).
- **Failure-path retry policy is reason-dependent.** PluginTooLarge / Unauthorized / NotFound / UpstreamInvalid won't change by retrying immediately, so the reconciler returns `RequeueAfter+nil` to avoid hot-looping. Transient Unreachable / StaleCacheExpired return the err so controller-runtime's workqueue applies exponential backoff. This matches the canonical Hub §6.6 retry-policy split.
- **ArtifactReconciler deletion-path narrowing.** When `cr.Status.StorageLocation` was populated by a prior Phase 2 reconcile, the deletion code removes EXACTLY that path. The Phase 1 "try-both-paths" sweep stays as a fallback for CRs that existed before Phase 2 reconcile ran (e.g. immediately after upgrading from Phase 1). Avoids unnecessary file-system probes in steady state.
- **Nil-DB tolerance.** All three reconcilers' steady-state branches guard `GetExternalRef` / `UpsertExternalRef` / `DeleteExternalRef` behind `if r.DB != nil`. The Phase 1 envtest finalizer test (PluginReconciler with no DB) stays green; Plan 02-09 will wire the real pool.
- **Phase 2 envtest is Docker-free.** The 15 new tests use `DB=nil` and exercise `materializeExternalRef` end-to-end via a fakeFetcher. Integration tests against Postgres are Plan 02-03's responsibility (`make test-integration` with `//go:build integration` tag). `make test` stays Docker-free per Phase 1 convention.
- **Test compromise on suite-reconciler races.** The suite_test.go registers PluginReconciler WITHOUT a Fetchers factory; its calls hit registry.For and fail on the missing auth Secret. The two Phase 2 envtest reconciler tests build their OWN PluginReconciler with the fake factory and use `Eventually()` to assert final state. The race produces `status update failed` log warnings (k8s "object modified" conflicts) which are real, expected, and benign — both reconcilers eventually converge on the assertion target.

## Deviations from Plan

**None — plan executed exactly as written.**

The plan's `<action>` blocks were followed precisely. Notes worth recording for the reviewer:

1. The `<files>` list in Task 1 includes `api/ach/v1alpha1/zz_generated.deepcopy.go`, but `make generate` produced no diff to that file because UpstreamRev is a value-type string already covered by the existing `*out = *in` shallow copy. The file was inspected post-regen to confirm.
2. The plan's Task 3 says "Preserve the Phase 1 writeStatus helper as a no-op delete-it path …. Decision: remove it; the deletion path no longer writes status." Followed: writeStatus is fully removed; the deletion path is structured-log only (`logger.Info("§10.3 cleanup complete; finalizer removed", ...)` — no status condition is written during deletion).
3. The plan's Task 5 envtest enumeration includes `TestMaterializeExternalRef_FetchError_Unauthorized`, `_Unreachable`, `_PluginTooLarge`, `_NotModified`, `_MissingSecret`, `_Success_StagesAndRenames` — all landed. The plan does not explicitly require a `TestMaterializeExternalRef_RenameFailure` test (rename(2) failure requires either a read-only FS or a no-perm dir, which is awkward in envtest). The error-path coverage is satisfied by `TestMaterializeExternalRef_FetchError_*` plus the rename(2) wrapping in code being a one-line `fmt.Errorf("§10.3 rename(2): %w", err)` covered by inspection.

## Issues Encountered

- **ctrl.Request shim compile error.** Initial draft of the test file used a tiny `ctrlRequest` type alias to avoid importing `ctrl` at file scope. `ctrl.Request` is a struct with `NamespacedName types.NamespacedName` as a regular field (not embedded), so the alias didn't satisfy the `Reconcile(ctx, req ctrl.Request)` signature. Fixed by importing `ctrl "sigs.k8s.io/controller-runtime"` and using `ctrl.Request` directly. No commit churn — the fix landed inline before the Task 5 commit.
- **status update failed log warnings during reconciler tests.** Expected: the suite-registered reconciler and the per-test reconciler race on `r.Status().Update`. Both reconcilers retry on the k8s "object modified" conflict; the per-test assertion via `Eventually()` waits for the eventual-consistent state. Tests pass deterministically. The log noise is acceptable for envtest and disappears in production (only one reconciler instance per Plugin CRD).

## User Setup Required

None — no external service configuration required by Plan 02-05. The plan's `user_setup` frontmatter is `[]`. The new reconciler-struct fields (DB, PluginMaxSizeMiB, Fetchers) are injected by Plan 02-09 at cmd/operator/main.go's `SetupWithManager` call sites.

## Next Plan Readiness

- **Plan 02-06 (PluginMarketplace reconciler):** can import the 8 Reason* constants from conditions.go for its Synced condition (it will additionally need NameConflict + UnsupportedPluginSource constants for marketplace-specific outcomes — likely declared in a marketplace-local conditions extension). May optionally factor a per-plugin `materializeOnePlugin` helper from this plan's materializeExternalRef pattern.
- **Plan 02-09 (cmd/operator/main.go):** MUST inject the three new reconciler-struct fields at SetupWithManager time:
  ```go
  if err = (&achcontroller.PluginReconciler{
      Client:           mgr.GetClient(),
      Scheme:           mgr.GetScheme(),
      Namespace:        watchNS,
      Log:              ctrl.Log.WithName("controller").WithName("Plugin"),
      CacheRoot:        cacheRoot,
      DB:               dbPool,                  // Plan 02-09 (NEW)
      PluginMaxSizeMiB: pluginMaxSizeMiB,        // Plan 02-09 (NEW; from ACH_PLUGIN_MAX_SIZE_MIB)
      Fetchers:         nil,                     // nil → registry.For
  }).SetupWithManager(mgr); err != nil { ... }
  // Same DB + Fetchers fields for PromptReconciler and ArtifactReconciler;
  // they do NOT carry PluginMaxSizeMiB.
  ```
- **Phase 5 (Content Service):** can read `external_refs.upstream_rev` to compare against future Hub-side fetches for stale-check logic. The DB column landed in Plan 02-03 migration 000002.
- **Force-refresh annotation contract** (Hub §15.5): operator removes annotation in same UPDATE as last_successful_refresh — Plan 02-09's Platform API will be the first writer of `ach.ackstorm.ai/force-refresh` once it ships.

## Threat Model Coverage

All seven threats from the plan's `<threat_model>` section have implementation hooks:

- **T-02-05-01** (rename-mid-crash torn-byte) — `accept`: POSIX rename(2) on the same FS is atomic; only orphan staging files appear, swept by Plan 02-04's SweepTmp Runnable.
- **T-02-05-02** (between-rename-and-UPSERT crash) — `mitigate`: documented in materializeExternalRef step-8 godoc; idempotent re-reconcile.
- **T-02-05-03** (oversize bypass via Content-Length lie) — `mitigate`: io.LimitReader(body, max+1) is the load-bearing barrier; `TestMaterializeExternalRef_PluginTooLarge` asserts the 100-byte body + 10-byte cap path.
- **T-02-05-04** (Secret echo in status.message) — `mitigate`: OversizeError.Error() formats `staged N bytes exceeds … cap of M bytes`; Secret values never enter the message stream.
- **T-02-05-05** (adversarial low-interval hammering) — `accept`: CRD-03/04 enforce interval ≤ maxStaleness; conditional-GET minimizes upstream traffic.
- **T-02-05-06** (unauthorized force-refresh annotation) — `accept`: MULTI-02 RBAC + Platform API admin-allowlist (Plan 03-*) enforce the write path.
- **T-02-05-07** (DB error wrap leaking storage path) — `accept`: cache layout is public per Hub §10.3.

## Self-Check: PASSED

Verified after writing this SUMMARY:

Commits exist on `worktree-agent-ad0ff939059f2eaa7`:
- `e13c52e` (Task 1): FOUND
- `4347c92` (Task 2): FOUND
- `a12fa23` (Task 3): FOUND
- `fc4b8ae` (Task 4): FOUND
- `22e1ed8` (Task 5): FOUND

Key files exist:
- `internal/controller/ach/conditions.go`: FOUND
- `internal/controller/ach/external_ref_refresh.go`: FOUND
- `internal/controller/ach/external_ref_refresh_test.go`: FOUND
- `api/ach/v1alpha1/external_ref_types.go` carries UpstreamRev: FOUND (grep `UpstreamRev string`)
- 4 CRD YAMLs include `upstreamRev:`: FOUND (1 line each)

Build + test gates:
- `go build ./...`: clean (whole module)
- `go vet ./internal/controller/ach/...`: clean
- `go test ./internal/controller/ach/... -count=1`: 40 RUN entries, ALL PASS in 9.8s (Phase 1 finalizer + CEL admission + Phase 2 helpers + Phase 2 Plugin reconciler)
- `grep -c "io.LimitReader" internal/controller/ach/external_ref_refresh.go`: 3 (1 in code + 2 in comments — satisfies "at least 1")
- `grep -c "os.Rename" internal/controller/ach/external_ref_refresh.go`: 1 (exactly 1 — single atomic publish point)
- `grep -c "UpsertExternalRef|DeleteExternalRef" internal/controller/ach/*.go`: 11 across 4 files (6+1+3+1) — exceeds the required ≥4

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Plan: 02-05*
*Completed: 2026-05-17*
