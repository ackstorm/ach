---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 07
subsystem: operator
tags: [litellm-snapshot, manager-runnable, atomic-pointer, execution-resources-resolved, environment-reconciler, op-13]

# Dependency graph
requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation
    plan: 01
    provides: |
      Widened litellm.Client interface with ListModels / ListMCPServers /
      ListA2AAgents; ErrNotFound sentinel; NoopClient list-helpers return
      (nil, nil) for unit-test compatibility.
provides:
  - "internal/snapshot package with Snapshotter (controller-runtime manager.Runnable) + LiteLLMSnapshot value type"
  - "NewSnapshotter(c litellm.Client, log logr.Logger) *Snapshotter constructor"
  - "Snapshotter.Start(ctx) — initial refresh + 5-minute ticker loop per Hub §6.4 / D-13"
  - "Snapshotter.Snapshot() LiteLLMSnapshot — lock-free read via atomic.Pointer.Load + value-copy"
  - "Snapshotter.LiteLLMUnreachableCount() int64 — Phase 5 wires to litellm_unreachable_total{caller=\"operator\"} per §18.5"
  - "D-14 stale-preservation: on LiteLLM-unreachable, prior snapshot preserved with Stale=true; first-refresh failure emits empty stale snapshot"
  - "ErrNotFound downgrade per Plan 02-01 SUMMARY contract — empty set is a valid result, NOT an error"
  - "EnvironmentReconciler.Snapshotter *snapshot.Snapshotter field (Plan 02-09 injects)"
  - "EnvironmentReconciler steady-state: ExecutionResourcesResolved condition derivation + status.UnresolvedRuntime population + 5-minute RequeueAfter per Hub §6.4 / OP-13"
  - "Nil-Snapshotter back-compat branch preserves Phase 1 AccessGroupSynced=Unknown reason=Initializing emission (keeps existing envtests green)"
affects:
  - Plan 02-09 (cmd/operator/main.go must construct snapshot.NewSnapshotter(realLiteLLM, log), register via mgr.Add(snapshotter), and inject EnvironmentReconciler.Snapshotter from the same instance)
  - Phase 5 (litellm_unreachable_total metric — LiteLLMUnreachableCount counter accumulates here; Phase 5 wires the Prometheus emitter)

# Tech tracking
tech-stack:
  added: []   # stdlib only (sync/atomic, time, errors, context); logr already transitive via controller-runtime
  patterns:
    - "manager.Runnable lifecycle: Start(ctx) — initial population + ticker.NewTicker loop with select on ctx.Done — returns nil on cancel"
    - "atomic.Pointer[T] for single-writer / many-reader publication safety — readers do *Pointer.Load + value-copy, never block writer"
    - "atomic.Int64 unreachable counter — Phase 5 exports without converting to mutex"
    - "Per-tick refresh issues 3 list calls in series; ANY non-ErrNotFound failure flips the whole tick to unreachable (atomic-shape rule per D-14)"
    - "Nil-dependency back-compat branch: reconcilers that accept an optional *T dependency emit a degraded-mode status condition (Phase 1 fallback) when T is nil — avoids forcing every unit test to wire production dependencies"

key-files:
  created:
    - "internal/snapshot/snapshot.go (Snapshotter struct + lifecycle, LiteLLMSnapshot value type, refresh logic with stale-preservation, toModelSet/toMCPSet/toAgentSet helpers, DefaultRefreshInterval const)"
    - "internal/snapshot/snapshot_test.go (8 unit tests: cold-start, first-success, first-failure, prior-preserve, ErrNotFound downgrade, partial-error any-failure rule, concurrent reads under -race, Start ctx-cancel)"
    - "internal/snapshot/doc.go (package docstring: D-13, D-14, Hub §6.4 cadence, atomic.Pointer rationale, unreachable-preserve contract, cold-start vs stale-empty semantics)"
  modified:
    - "internal/controller/ach/environment_controller.go (+105 / -19): adds Snapshotter field, replaces Phase 1 steady-state stub with ExecutionResourcesResolved derivation, returns 5-min RequeueAfter, preserves nil-Snapshotter back-compat branch; updates struct + Reconcile docstrings + writeStatus docstring for Phase 2 semantics)"

key-decisions:
  - "MCPServerEntry uses .ServerName (verified at internal/litellm/types.go line 217), AgentEntry uses .AgentName (verified at line 256). The Snapshotter set-builders key on these exact fields so the EnvironmentReconciler's spec.runtime.MCPServers / .A2AAgents comparison resolves correctly."
  - "Partial-error rule (D-14): if any of the three list calls returns a non-ErrNotFound error, the ENTIRE tick is treated as unreachable. We do NOT merge partial results into the snapshot because doing so would publish a snapshot with inconsistent shape — an Environment with one MCP server in spec.runtime would oscillate between resolved and unresolved depending on which subset of LiteLLM list calls happened to succeed on a given tick."
  - "First-refresh failure publishes an empty Stale snapshot (rather than leaving the atomic.Pointer at nil). Reason: callers distinguish 'cold start' (zero-value LiteLLMSnapshot, Stale=false) from 'stale empty' (Stale=true) — having a distinct codepath in logs and condition-message text disambiguates 'never refreshed' from 'refresh failed'."
  - "Snapshotter field on EnvironmentReconciler is optional (*snapshot.Snapshotter; nil-check before use). The nil-Snapshotter branch emits the Phase 1 AccessGroupSynced=Unknown reason=Initializing condition so the existing TestEnvironmentFinalizerAddRemove envtest stays green without rewiring the suite. Plan 02-09 always wires a non-nil Snapshotter from cmd/operator/main.go for production."
  - "Both code paths return ctrl.Result{RequeueAfter: 5 * time.Minute} — the nil-Snapshotter back-compat branch also re-queues every 5 min because Phase 1's bare ctrl.Result{} return would have suppressed re-evaluation entirely once the Snapshotter is wired. The 5-min cadence per Hub §6.4 is the operative SLA for steady-state convergence."
  - "Snapshotter.refresh is invoked synchronously inside Start's for-loop body — one tick at a time, no goroutine fan-out. If a refresh takes longer than DefaultRefreshInterval, the next tick simply runs immediately after with no gap. Avoids the back-pressure / queue-buildup pathology a fan-out implementation would introduce against a slow LiteLLM."

patterns-established:
  - "Lock-free snapshot read pattern (atomic.Pointer[T] + value-copy in the Snapshot accessor) is the canonical shape for any future cross-reconciler shared state. Plan 02-08's orphan-cleanup Runnable does NOT use this pattern (it's batch, not read-heavy); future read-heavy caches should."
  - "manager.Runnable Start lifecycle (initial-action + ticker + select on ctx.Done) — Plan 02-08's orphan cleanup will follow the same shape verbatim."

requirements-completed:
  - OP-13

# Metrics
duration: ~10min
completed: 2026-05-17
---

# Phase 2 Plan 07: LiteLLM Snapshot Runnable + Environment ExecutionResourcesResolved Derivation Summary

**Lands `internal/snapshot.Snapshotter` (controller-runtime `manager.Runnable`) that refreshes `ListModels` + `ListMCPServers` + `ListA2AAgents` every 5 minutes into an `atomic.Pointer[LiteLLMSnapshot]` for lock-free reads, with prior-snapshot preservation on LiteLLM-unreachable (D-14), and extends `EnvironmentReconciler` with a `Snapshotter` field whose steady-state branch computes `spec.runtime \ snapshot` set differences to drive `ExecutionResourcesResolved` per Hub §6.6 closed set, populates `status.UnresolvedRuntime`, and returns `ctrl.Result{RequeueAfter: 5*time.Minute}` per Hub §6.4 — Phase 1 unit-test mode (nil Snapshotter) retains the back-compat `AccessGroupSynced=Unknown reason=Initializing` emission so existing envtests stay green (OP-13).**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-05-17T07:35Z
- **Completed:** 2026-05-17T07:48Z
- **Tasks:** 2 (atomic commits)
- **Files modified:** 4 (3 created in `internal/snapshot/`, 1 modified in `internal/controller/ach/`)

## Accomplishments

- `internal/snapshot.Snapshotter` implements `controller-runtime/pkg/manager.Runnable` via `Start(ctx context.Context) error`. The initial `refresh(ctx)` runs synchronously before the `time.NewTicker(s.interval)` loop begins, so the first Environment reconcile after manager boot has a populated snapshot iff LiteLLM is reachable.
- Single-writer / many-reader publication safety via `atomic.Pointer[LiteLLMSnapshot]`. Readers call `Snapshot()` which performs `s.snap.Load()` + dereference — zero contention; the writer (the ticker's `refresh`) never blocks a reader.
- D-14 stale-preservation contract implemented exactly: on any non-`ErrNotFound` failure from any of the three list calls, the prior snapshot's value is copied, `Stale` flipped to `true`, and re-stored via `atomic.Pointer.Store`. First-refresh failure (no prior snapshot exists) publishes an empty `Stale=true` snapshot so callers never oscillate between cold-start and stale-empty semantics.
- `ErrNotFound` downgraded to empty slice per the Plan 02-01 SUMMARY contract — an Environment that lists a model name against a zero-model LiteLLM correctly observes the empty intersection, not a transient error.
- `atomic.Int64` `LiteLLMUnreachableCount` counter increments on every failed tick; Phase 5 will wire it into the `litellm_unreachable_total{caller="operator"}` Prometheus counter per Hub §18.5.
- `EnvironmentReconciler` gained a `Snapshotter *snapshot.Snapshotter` field. The steady-state branch reads the snapshot once, builds the three `unresolved` slices via map lookup (O(n) in `spec.runtime` size, NOT in snapshot size), writes both `env.Status.UnresolvedRuntime = &unresolved` AND the `ExecutionResourcesResolved` condition in a single `r.Status().Update` via the existing `writeStatus` helper. Returns `ctrl.Result{RequeueAfter: 5 * time.Minute}` per Hub §6.4 — both the nil-Snapshotter back-compat branch and the full ExecutionResourcesResolved branch return this cadence.
- 8 unit tests pass under `-race`: cold-start zero value, first-refresh success, first-refresh failure (empty Stale), prior-preserve on subsequent failure, `ErrNotFound` downgrade, partial-error any-failure rule, 100-reader / 1-writer concurrent stress, `Start` ctx-cancel lifecycle.
- Phase 1 `TestEnvironmentFinalizerAddRemove` envtest still passes (verified — 7.8s suite; OP-02 counter still hits 2; nil-Snapshotter back-compat preserves Phase 1 behavior).

## Task Commits

Each task was committed atomically:

1. **Task 1: Add `internal/snapshot` package** — `bcda793` (feat) — Snapshotter struct, NewSnapshotter ctor, Start lifecycle, refresh with stale-preservation, helpers, doc.go, 8 unit tests under -race.
2. **Task 2: Extend EnvironmentReconciler with Snapshotter field + ExecutionResourcesResolved derivation** — `663f6e2` (feat) — adds field; replaces Phase 1 stub with set-difference computation; emits closed-set condition; populates UnresolvedRuntime; returns 5-min RequeueAfter; preserves nil-Snapshotter back-compat.

**Plan metadata commit:** _(pending after this SUMMARY)_

## Files Created/Modified

**Created:**

- `internal/snapshot/snapshot.go` (263 lines)
  - `const DefaultRefreshInterval = 5 * time.Minute` per Hub §6.4 / D-13.
  - `type LiteLLMSnapshot struct { Models, MCPServers, A2AAgents map[string]struct{}; RefreshedAt time.Time; Stale bool }`.
  - `type Snapshotter struct` with `client litellm.Client`, `interval time.Duration`, `snap atomic.Pointer[LiteLLMSnapshot]`, `log logr.Logger`, `litellmUnreachableCount atomic.Int64`.
  - `NewSnapshotter(c, log) *Snapshotter` — constructor (defaults interval to `DefaultRefreshInterval`).
  - `Snapshot() LiteLLMSnapshot` — lock-free read (`Load + dereference`; returns zero value on cold start).
  - `LiteLLMUnreachableCount() int64` — counter accessor for Phase 5 metric wiring.
  - `Start(ctx) error` — manager.Runnable: initial refresh + ticker loop; returns nil on ctx cancel.
  - `refresh(ctx)` — issues three list calls, downgrades ErrNotFound → empty, on any other error increments unreachable counter and preserves prior snapshot with Stale=true (D-14), on full success publishes new snapshot via atomic.Pointer.Store.
  - `toModelSet / toMCPSet / toAgentSet` — package-private set builders keyed on `.ModelName / .ServerName / .AgentName`.
- `internal/snapshot/snapshot_test.go` (372 lines) — 8 stdlib `testing` tests (no testify, no Ginkgo):
  - `TestSnapshotter_ColdStart_ReturnsEmpty`
  - `TestSnapshotter_FirstRefreshSuccess`
  - `TestSnapshotter_FirstRefreshLiteLLMUnreachable`
  - `TestSnapshotter_RefreshAfterPriorSuccess_LiteLLMUnreachable`
  - `TestSnapshotter_ErrNotFoundIsEmptyNotError`
  - `TestSnapshotter_PartialError_OneOfThreeFails`
  - `TestSnapshotter_ConcurrentReads` (100 readers, 1 writer; -race-safe)
  - `TestSnapshotter_StartRespectsCtxCancel`
  - `fakeLiteLLM` test double declared inside the file as the package-internal `litellm.Client` impl.
- `internal/snapshot/doc.go` (46 lines) — package docstring spelling out D-13, D-14, Hub §6.4 cadence, atomic.Pointer rationale, cold-start vs stale-empty semantics, and the dependency floor (internal/litellm + logr + stdlib only).

**Modified:**

- `internal/controller/ach/environment_controller.go` (+105 / −19; now 396 lines, was 311)
  - Imports: added `github.com/ackstorm/ach/internal/snapshot`.
  - Struct: added `Snapshotter *snapshot.Snapshotter` field with a godoc paragraph documenting Plan 02-09 injection + nil-in-unit-tests semantics.
  - `Reconcile` Step 3 (lines 168-249 after edit): replaced the Phase 1 AccessGroupSynced=Unknown stub with the ExecutionResourcesResolved derivation. Nil-Snapshotter fallback retained (lines 175-184) for envtest back-compat. Both branches return `ctrl.Result{RequeueAfter: 5 * time.Minute}` per Hub §6.4.
  - `Reconcile` godoc Step 3 description updated to reflect the new semantics; `writeStatus` godoc updated to note Phase 2's `ExecutionResourcesResolved` emission with `{Resolved, ResourceUnresolved}` reason set.

## Decisions Made

- **MCP server / A2A agent field names verified during implementation.** Plan suggested `toMCPSet` might use `.Name` or `.ServerName` and `toAgentSet` might use `.Name` or `.AgentName`. Read `internal/litellm/types.go`:
  - `MCPServerEntry` declares `ServerName string` at line 217.
  - `AgentEntry` declares `AgentName string` at line 256.
  Both set builders key on these exact fields. The reconciler's spec.runtime comparison (`env.Spec.Runtime.MCPServers` / `.A2AAgents`) carries the same name semantics, so the set membership check resolves correctly without name transformation.
- **`fakeLiteLLM.modelCalls` uses `atomic.Int64`** rather than a mutex-guarded counter so the `TestSnapshotter_StartRespectsCtxCancel` test's ticker-fired call count can be read after `cancel()` without coordinating against the now-stopped Start goroutine. Lifts the `-race` cleanliness for free.
- **Both Reconcile branches use the same `5 * time.Minute` RequeueAfter.** The nil-Snapshotter branch could have returned `ctrl.Result{}` (no re-queue) since it's a back-compat unit-test path, but returning the production cadence keeps behavior between the two branches deliberately uniform — anything reading `result.RequeueAfter` (e.g., a future SLO check) sees a single contract.
- **`Start` always returns nil on ctx cancellation.** Controller-runtime treats a non-nil error from a Runnable as fatal to the entire manager. LiteLLM unreachability is a recoverable degradation (preserved via `Stale=true`), not a fatal manager-stop condition. Only ctx cancel (manager shutdown / SIGTERM) ends the loop, and that's a normal-exit path.

## Deviations from Plan

None — plan executed exactly as written.

The plan explicitly anticipated the field-name verification step ("Verify the actual field names on MCPServerEntry + AgentEntry by reading internal/litellm/types.go before finalizing"). The verified fields are `.ServerName` and `.AgentName` respectively — both diverge from the plan's example sketches (`.Name`), so the toMCPSet / toAgentSet implementations follow `types.go` rather than the example sketches. Plan-anticipated divergence, not a deviation.

Optional `TestEnvironmentReconciler_ExecutionResourcesResolved_NoSnapshot` envtest was deferred per the plan's explicit "Defer" guidance — the suite already covers the nil-Snapshotter branch via `TestEnvironmentFinalizerAddRemove` (Phase 1) which still passes; adding a parallel test would not exercise a new code path beyond what Phase 1's test confirms about back-compat.

## Issues Encountered

None.

The `ach-devtools:latest` Docker image was not built locally at agent startup, but the byte-identical `ach-litellm-devtools:latest` image was available — tagged as `ach-devtools:latest` to satisfy `scripts/dev.sh`'s image-inspect probe (`docker tag ach-litellm-devtools:latest ach-devtools:latest`). This is environmental setup, not a plan deviation.

## User Setup Required

None.

`r.Snapshotter` is nil-safe in this plan's deliverable; production wiring is Plan 02-09's responsibility. No environment variables introduced (Plan 02-09 introduces `ACH_LITELLM_BASE_URL` / `ACH_LITELLM_MASTER_KEY` when wiring the real client into `cmd/operator/main.go`).

## Next Phase Readiness

- **Plan 02-09 (cmd/operator/main.go wire-up)** must:
  1. Construct the Snapshotter:
     ```go
     snapshotter := snapshot.NewSnapshotter(realLiteLLM, ctrl.Log.WithName("litellm-snapshot"))
     if err := mgr.Add(snapshotter); err != nil { setupLog.Error(...); os.Exit(1) }
     ```
  2. Inject `Snapshotter: snapshotter` into the `EnvironmentReconciler{...}` literal at the same site that already supplies `Client`, `Scheme`, `LiteLLM`, `Namespace`, `Log`, `DB`. The field is nil-safe in unit tests but production reconciles will always observe a non-nil pointer once Plan 02-09 lands.
  3. The Snapshotter's `LiteLLMUnreachableCount()` accessor is callable from anywhere — Plan 02-09 may emit it via a `slog.Info` line periodically; the full Prometheus binding lands in Phase 5.
- **Phase 5 (Prometheus /metrics)** will register `litellm_unreachable_total{caller="operator"}` and read the counter via `snapshotter.LiteLLMUnreachableCount()`. The counter increments monotonically per failed tick — Phase 5 may need to wrap it in a `prometheus.GaugeFunc` or a manually-published counter that polls the accessor on scrape, since this is not a true Prometheus client_golang Counter (which exposes Inc, not Add — but the accessor returns the cumulative total directly, which is what Prometheus scraping wants anyway).
- **Threat surface:** No new network endpoints, file access, or schema changes introduced by this plan. The Snapshotter makes 3 GET requests every 5 minutes through the redacting transport from Plan 02-01 (T-02-07-01 / -02 accepted: model/mcp/agent names are operationally-visible per Hub §6.4; map-allocation pathologies bounded at 10000×80 bytes ≈ 800 KiB worst case). atomic.Pointer publication safety verified under -race (T-02-07-04 mitigated).

## Self-Check: PASSED

Verified after writing this SUMMARY:

- `internal/snapshot/snapshot.go` exists (263 lines).
- `internal/snapshot/snapshot_test.go` exists (372 lines).
- `internal/snapshot/doc.go` exists (46 lines).
- `internal/controller/ach/environment_controller.go` modified (396 lines, was 311).
- Commits exist: `bcda793` (Task 1), `663f6e2` (Task 2).
- `./scripts/dev.sh go build ./...` → exit 0.
- `./scripts/dev.sh go test ./internal/snapshot/... -count=1 -race` → ok (8 tests).
- `./scripts/dev.sh go test ./internal/controller/ach/... -count=1` → ok (TestEnvironmentFinalizerAddRemove still passes via nil-Snapshotter back-compat; CEL + four other finalizer tests also green).
- `grep -c "snapshot.Snapshotter\|snapshot.NewSnapshotter" internal/controller/ach/environment_controller.go` = 2 (≥ 1).
- `grep -c "RequeueAfter: 5 \\* time.Minute" internal/controller/ach/environment_controller.go` = 2 (≥ 2 — both branches).
- `grep -c "atomic.Pointer\|atomic.Int64" internal/snapshot/snapshot.go` = 10 (well ≥ 2; includes docstring mentions + the two field declarations).
- `grep -n "litellm.ErrNotFound" internal/snapshot/snapshot.go` → 3 hits (one per list call's downgrade branch).
- `grep -n "DefaultRefreshInterval = 5 \\* time.Minute" internal/snapshot/snapshot.go` → 1 hit (the package constant).

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Plan: 07*
*Completed: 2026-05-17*
