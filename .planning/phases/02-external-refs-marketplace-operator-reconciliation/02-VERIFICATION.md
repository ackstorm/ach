---
phase: 02-external-refs-marketplace-operator-reconciliation
verified: 2026-05-18T00:00:00Z
status: human_needed
score: 5/5
overrides_applied: 0
human_verification:
  - test: "End-to-end SC#1 — Plugin github type publishes tar.gz within one reconcile"
    expected: "Creating a Plugin CR with type:github and a branch ref causes plugin/<name>.tar.gz to appear under /var/cache/ach within one reconcile; killing the operator between staging-write and the DB UPDATE leaves no torn-byte file (re-reconcile republishes idempotently)"
    why_human: "Requires a running Kubernetes cluster with a real GitHub-accessible network, the full operator deployed with a real LiteLLM endpoint, and an active PVC — cannot be validated by grep or envtest alone. The code path (materializeExternalRef steps 1-8) is verified programmatically but the full crash-and-recover scenario requires live infrastructure."
  - test: "SC#2 full e2e — PluginMarketplace with a real Claude-Code-shaped upstream file"
    expected: "marketplace/<name>/plugin/<plugin-name>.tar.gz produced for each included plugin; npm source → Synced=False reason=UnsupportedPluginSource without aborting rest; one-plugin failure recorded in status.message while others succeed; vanished names are DELETE-swept"
    why_human: "Envtest coverage exercises each branch in isolation with fake fetchers; end-to-end verification against a real marketplace.json URL requires a running cluster. npm → UnsupportedPluginSource path and DELETE-sweep path are both envtest-verified but live path needs human confirmation."
  - test: "SC#3 live — two PluginMarketplace CRs with same plugin name, alphabetical winner confirmed"
    expected: "Alphabetically-lower CR.Name keeps Synced=True and materializes the plugin; loser gets Synced=False reason=NameConflict; a Plugin CRD with same name beats both"
    why_human: "Logic verified in unit tests (marketplace_conflict_test.go) and envtest (TestPMR_NameConflict_AlphabeticalPriority); live cluster confirmation with a real DB confirms the listOtherMarketplaceCatalogs query path."
  - test: "SC#4 live — oversized plugin cap with a real large archive"
    expected: "ACH_PLUGIN_MAX_SIZE_MIB=0 (or negative) causes operator pod to fail fast; real oversized archive triggers SourceReachable=False reason=PluginTooLarge with no file on disk"
    why_human: "Startup validation is unit-tested (config_test.go). The OversizeError path is tested in TestMaterializeExternalRef_PluginTooLarge with a fake fetcher, but producing a real tarball above the cap in a live deployment requires human confirmation."
  - test: "SC#5 live — ExecutionResourcesResolved with real LiteLLM + orphan-cleanup emitting audit event per revocation"
    expected: "A real Environment with a non-registered model shows status.unresolvedRuntime non-empty and ExecutionResourcesResolved=False reason=ResourceUnresolved; orphan-cleanup loop fires at the configured interval, emits a structured JSON audit event with audit:true per revocation, aborts cleanly when LiteLLM is unreachable"
    why_human: "Snapshot runnable and orphan runnable are unit-tested; environment reconciler ExecutionResourcesResolved logic is envtest-verified. Confirming live audit events on stdout in a real cluster, and the abort-on-unreachable behavior under real LiteLLM outage, requires human verification."
  - test: "Deferred info findings (IN-01, IN-02, IN-03, IN-05) — comment/doc quality"
    expected: "Four remaining deferred info-level findings from 02-REVIEW.md are advisory only (comment wording, %w semantics, no-op doc, test-discipline nit) and do not affect correctness"
    why_human: "These are code-quality/documentation items explicitly deferred by the code review. Human judgment required to decide whether to address them before closing the phase."
---

# Phase 2: External Refs + Marketplace + Operator Reconciliation — Verification Report

**Phase Goal:** The Operator continuously reconciles external content into the cache PVC: it fetches from `github`/`gitlab`/`bitbucket`/`s3`/`gcs`/`http`, publishes via atomic `rename(2)`, runs the three-stage marketplace refresh with anchored RE2 filters, enforces the plugin size cap, queries LiteLLM REST for `ExecutionResourcesResolved`, and reaps orphan LiteLLM keys on a configurable interval.
**Verified:** 2026-05-18T00:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

All five success criteria are code-verified at the logic level. Five human-in-the-loop checks are required because they depend on live cluster deployment, real network I/O, or live LiteLLM endpoint behavior.

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Six source fetchers (github/gitlab/bitbucket/s3/gcs/http) fetch upstream archives as streaming io.ReadCloser | VERIFIED | `internal/sources/{github,gitlab,bitbucket,s3,gcs,http}/fetcher.go` all exist and return streaming bodies. GitLab was fixed from SDK-buffering to raw HTTP GET in commit c37c234. Bitbucket URL-escaping fixed in commit 5fa3401. S3 HTTPS enforcement added in commit 6e83226. |
| 2 | Atomic publication via `.tmp/<random>` → fsync → rename(2) → DB UPSERT (§10.3 sequence) | VERIFIED | `internal/controller/ach/external_ref_refresh.go:232-318` implements all 8 steps: CreateTemp in `.tmp/`, io.LimitReader for cap, io.Copy, Sync(), Close(), os.Rename(), then UpsertExternalRef. Crash between rename(2) and UPSERT is benign by design (next reconcile re-publishes idempotently). |
| 3 | Three-stage PluginMarketplace refresh (Stage-1 fail-fast, Stage-2 per-plugin best-effort, Stage-3 DELETE-sweep) | VERIFIED | `internal/controller/ach/pluginmarketplace_controller.go:152-384` implements all three stages. Stage-1 failure aborts before any UPSERT/DELETE (lines 183-209). Stage-2 serial per-plugin with failures collected (lines 252-302). Stage-3 DELETE sweep with fail-loud WR-06 fix (lines 305-348, commit 87ee66e). |
| 4 | Anchored RE2 filters (Operator prepends `^`); compile failure → InvalidConfig; include-zero → UpstreamInvalid; exclude-zero → silent no-op | VERIFIED | `internal/controller/ach/marketplace_filters.go:52-65`: compileAnchored prepends `^` + wraps compile error with ErrInvalidConfig. `pluginmarketplace_controller.go:226-237`: include-zero check (includeListed && !includeMatched). applyFilters WR-05 short-circuit added in commit 90588f4. |
| 5 | Cross-marketplace name conflict: alphabetical priority on metadata.name; Plugin CRD beats marketplace; loser gets Synced=False reason=NameConflict | VERIFIED | `internal/controller/ach/marketplace_conflict.go:93-138`: resolveConflicts sorts contenders and picks contenders[0]. Plugin CRD rule (Rule 1) applies before alphabetical (Rule 2). WR-09 fix (commit e85c40c) adds ReasonPluginCRDPrecedence to distinguish Plugin-CRD-wins from marketplace-loses. |
| 6 | Plugin size cap: ACH_PLUGIN_MAX_SIZE_MIB=0/negative/non-numeric → Operator refuses to start; oversized plugin → SourceReachable=False reason=PluginTooLarge; no cached file produced | VERIFIED | `cmd/operator/main.go:162-165`: MustEnvIntPositive fails on 0/negative/non-numeric. `external_ref_refresh.go:256-270`: io.LimitReader(body, cap+1), overshoot check (n > SizeCapBytes), OversizeError + os.Remove(stagingPath). `classifyFetchError` maps OversizeError → ReasonPluginTooLarge. |
| 7 | LiteLLM snapshot Runnable refreshes Models/MCPServers/A2AAgents into atomic.Pointer every 5 minutes; Environment reconciler reads snapshot lock-free and derives ExecutionResourcesResolved | VERIFIED | `internal/snapshot/snapshot.go`: NewSnapshotter, Start() with initial refresh + 5m ticker, atomic.Pointer[LiteLLMSnapshot] for lock-free reads, Stale flag on unreachable (D-14). `environment_controller.go:190-249`: set-difference for unresolved names, writes UnresolvedRuntime + ExecutionResourcesResolved condition, RequeueAfter 5m. |
| 8 | Orphan LiteLLM key cleanup: configurable interval (default 1h, min 5m floor, refuses below); aborts cleanly on LiteLLM-unreachable; emits audit event per revocation | VERIFIED | `internal/config/config.go:141-157`: MustEnvDurationAtLeast enforces minimum floor. `cmd/operator/main.go:186-188`: calls with 5*time.Minute floor. `internal/orphan/runnable.go:122-237`: Start() tick loop, TickOnce() abort-on-ListUserKeys-failure, per-key RevokeKey + audit event. CR-03 fix (commit 4687a46) removes err.Error() from audit events. |

**Score:** 5/5 success criteria satisfied (8/8 observable truths verified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/sources/github/fetcher.go` | GitHub streaming fetcher | VERIFIED | Exists; returns streaming body via go-github + HTTP GET |
| `internal/sources/gitlab/fetcher.go` | GitLab streaming fetcher | VERIFIED | Exists; uses raw HTTP GET after CR-01 fix (commit c37c234), not SDK buffering |
| `internal/sources/bitbucket/fetcher.go` | Bitbucket fetcher | VERIFIED | Exists; URL segments are url.PathEscape'd after CR-02 fix (commit 5fa3401) |
| `internal/sources/s3/fetcher.go` | S3 fetcher | VERIFIED | Exists; HTTPS enforcement at construction after WR-01 fix (commit 6e83226) |
| `internal/sources/gcs/fetcher.go` | GCS fetcher | VERIFIED | Exists; streaming via cloud.google.com/go/storage |
| `internal/sources/http/fetcher.go` | HTTPS-only (LIFTED 2026-05-18 (Phase 02.1 commit 45b7558)) conditional-GET fetcher | VERIFIED | Exists; refuses non-https:// at construction |
| `internal/controller/ach/external_ref_refresh.go` | §10.3 shared materializeExternalRef | VERIFIED | 470 lines; all 8 §10.3 steps implemented |
| `internal/controller/ach/plugin_controller.go` | Plugin steady-state reconciler | VERIFIED | Calls materializeExternalRef; injects SizeCapBytes; WR-02 fix applied |
| `internal/controller/ach/pluginmarketplace_controller.go` | Three-stage marketplace reconciler | VERIFIED | 634 lines; all three stages plus markSyncedTrue/False helpers |
| `internal/controller/ach/marketplace_conflict.go` | Cross-marketplace name conflict resolver | VERIFIED | resolveConflicts with Plugin-CRD-beats-all + alphabetical tiebreaker |
| `internal/controller/ach/marketplace_filters.go` | Anchored RE2 include/exclude filters | VERIFIED | compileAnchored prepends `^`; applyFilters with WR-05 short-circuit |
| `internal/controller/ach/marketplace_parse.go` | Claude Code marketplace.json parser | VERIFIED | Parses plugins array; surfaces errUnsupportedPluginSource for npm |
| `internal/controller/ach/conditions.go` | Reason constants + setExternalRefCondition | VERIFIED | Closed enum including ReasonPluginCRDPrecedence (WR-09 addition) |
| `internal/controller/ach/environment_controller.go` | ExecutionResourcesResolved logic | VERIFIED | Reads Snapshotter, computes set difference, writes condition + UnresolvedRuntime |
| `internal/snapshot/snapshot.go` | LiteLLM snapshot Runnable | VERIFIED | atomic.Pointer, 5m ticker, Stale flag, litellmUnreachableCount hook |
| `internal/orphan/runnable.go` | Orphan-cleanup Runnable | VERIFIED | OrphanAgeFloor, abort-on-unreachable, per-key revoke + audit; CR-03 fix applied |
| `internal/audit/handler.go` | Audit logger with audit:true | VERIFIED | NewLogger wraps slog.JSONHandler with slog.Bool("audit", true) |
| `internal/db/external_refs.go` | UpsertExternalRef / GetExternalRef / Reset / Delete | VERIFIED | Parameterized SQL, transient-class detection, force_refresh_requested_at NULL on UPSERT |
| `internal/db/marketplace_plugins.go` | UpsertMarketplacePlugin / List / Delete / Reset | VERIFIED | ResetMarketplacePluginsRefreshOnEmptyCache confirmed present |
| `internal/config/config.go` | MustEnvIntPositive + MustEnvDurationAtLeast | VERIFIED | Both helpers present; MustEnvDurationAtLeast enforces minimum floor |
| `internal/cachefs/sweep.go` | IsEmpty + SweepTmp | VERIFIED | IsEmpty recursively checks subtrees after WR-08 fix (commit 39155bb); SweepTmp exists but is NOT registered as a Runnable (explicitly deferred per main.go comment) |
| `cmd/operator/main.go` | Full operator wiring | VERIFIED | NoopClient→RESTClient swap; snapshotter.Add; orphanRunnable.Add; Secret informer pre-warm; OP-11 empty-PVC recovery; all 6 reconcilers injected with Phase 2 fields |
| `db/migrations/000002_phase2.up.sql` | Phase 2 schema additions | VERIFIED | Adds litellm_user_id, upstream_rev, force_refresh_requested_at columns |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `plugin_controller.go` | `materializeExternalRef` | `external_ref_refresh.go` | WIRED | materializeExternalRef called with full ExternalRefRefreshDeps including SizeCapBytes |
| `pluginmarketplace_controller.go` | `resolveConflicts` | `marketplace_conflict.go` | WIRED | Stage-1.d calls resolveConflicts; decisions slice drives Stage-2 |
| `pluginmarketplace_controller.go` | `applyFilters` | `marketplace_filters.go` | WIRED | compileAnchored + applyFilters called in Stage-1.c |
| `environment_controller.go` | `snapshot.Snapshotter` | `r.Snapshotter.Snapshot()` | WIRED | Snapshotter field injected from main.go; Snapshot() called in steady-state |
| `cmd/operator/main.go` | `snapshot.NewSnapshotter` | `mgr.Add(snapshotter)` | WIRED | Line 375-378; registered before reconcilers |
| `cmd/operator/main.go` | `orphan.NewRunnable` | `mgr.Add(orphanRunnable)` | WIRED | Line 382-387; wired with realLiteLLM + dbPool + auditLog + orphanInterval |
| `cmd/operator/main.go` | `litellm.NewRESTClient` | `realLiteLLM` variable | WIRED | Line 257-258; injected into EnvironmentReconciler + orphan Runnable |
| `orphan.Runnable` | `audit.NewLogger` | `r.Audit.Info(...)` | WIRED | audit.NewLogger(os.Stdout) passed as auditLog to NewRunnable; used in TickOnce |
| `cmd/operator/main.go` | `cachefs.IsEmpty` → `db.ResetExternalRefRefreshOnEmptyCache` | OP-11 recovery | WIRED | Lines 232-246: IsEmpty check + Reset on both tables when cache empty |
| `materializeExternalRef` | `db.UpsertExternalRef` | `deps.DB` non-nil guard | WIRED | Line 301-315 in external_ref_refresh.go |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `environment_controller.go` | `snap.Models/MCPServers/A2AAgents` | `snapshot.Snapshotter.Snapshot()` | Yes — when LiteLLM reachable; Stale flag preserved on unreachable | FLOWING |
| `pluginmarketplace_controller.go` | `decisions[]` | `resolveConflicts()` over filtered marketplace plugins | Yes — driven by Stage-1 upstream parse | FLOWING |
| `plugin_controller.go` | `cr.Status.StorageLocation` | `materializeExternalRef` → `computeFinalPath` + rename(2) | Yes — populated after successful §10.3 pipeline | FLOWING |
| `orphan/runnable.go` | `keys []litellm.KeyInfo` | `r.Client.ListUserKeys(ctx, uid)` | Yes — live LiteLLM REST call | FLOWING |

### Behavioral Spot-Checks

Step 7b is SKIPPED for this phase: the operator requires a live Kubernetes cluster + real LiteLLM endpoint. No runnable entry points without external dependencies.

### Probe Execution

No `scripts/*/tests/probe-*.sh` files found. No probes declared in PLAN/SUMMARY files. SKIPPED.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|------------|------------|-------------|--------|----------|
| OP-03 | 02-05 | External-reference refresh §10.3 sequence | SATISFIED | `materializeExternalRef` steps 1-8; plugin_controller / prompt_controller / artifact_controller all call it |
| OP-06 | 02-06 | PluginMarketplace three-stage refresh | SATISFIED | `pluginmarketplace_controller.go` three-stage implementation; Stage-1 fail-fast, Stage-2 best-effort, Stage-3 DELETE |
| OP-07 | 02-06 | RE2 anchored include/exclude filters | SATISFIED | `marketplace_filters.go` compileAnchored + applyFilters; InvalidConfig on compile fail; UpstreamInvalid on include-zero |
| OP-08 | 02-06 | Cross-marketplace name conflict resolution | SATISFIED | `marketplace_conflict.go` resolveConflicts; Plugin CRD beats all; alphabetical tiebreaker; NameConflict reason on loser |
| OP-09 | 02-05 / 02-09 | Plugin size cap enforcement | SATISFIED | MustEnvIntPositive fail-fast; io.LimitReader + OversizeError; PluginTooLarge reason; no oversized file at cache path |
| OP-13 | 02-07 | ExecutionResourcesResolved via LiteLLM REST | SATISFIED | `snapshot.go` Runnable + `environment_controller.go` steady-state; UnresolvedRuntime; 5m RequeueAfter |
| OP-15 | 02-08 | Orphan LiteLLM key cleanup Runnable | SATISFIED | `orphan/runnable.go`; 5m floor via MustEnvDurationAtLeast; OrphanAgeFloor; audit event per revocation; abort-on-unreachable |

**Note on checkbox state in REQUIREMENTS.md:** OP-03, OP-06, OP-07, OP-08, OP-09 still show `[ ]` (unchecked) while OP-13 and OP-15 show `[x]`. This is a documentation artifact — the REQUIREMENTS.md checkbox was not updated after the phase completed. The implementation evidence confirms all seven requirements are satisfied. Recommend updating the checkboxes as a follow-up documentation task.

### Anti-Patterns Found

Anti-pattern scan of Phase 2 modified files:

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/operator/main.go` | 389-393 | `SweepTmp Runnable is deferred` (comment) | INFO | SweepTmp function exists in cachefs/sweep.go but is not registered as a manager Runnable. The main.go comment explicitly documents this as a deliberate deferral: "NOTE: cachefs.SweepTmp Runnable is deferred — the .tmp/ sweep cadence (hourly per Hub §10.3) is a discretion item; the reconcilers' os.CreateTemp+rename pattern naturally minimizes orphan .tmp files." Not a blocker — deferred item with documented rationale. |
| `.planning/REQUIREMENTS.md` | 236-248 | `TBD` in Plan column for all Phase 2 req IDs | INFO | The traceability table shows Plan column as "TBD" for all Phase 2 requirement IDs. The checkbox state for OP-03/06/07/08/09 is `[ ]` even though the implementation is complete. Documentation debt, not a code issue. |
| `internal/controller/ach/pluginmarketplace_controller.go` | 295-307 | IN-01: comment contradicts code | INFO | Deferred per 02-REVIEW.md — comment wording nit, no behavior impact. |
| `internal/litellm/restclient.go` | 135-138 | IN-02: `%w` wrap comment misleading | INFO | Deferred per 02-REVIEW.md — comment vs `%w` semantics; no runtime impact. |
| `internal/litellm/transport.go` | 60-72 | IN-03: `path` in error log includes query params | INFO | Deferred per 02-REVIEW.md — documented for future maintainers; no security impact. |
| `internal/controller/ach/marketplace_filters_test.go` | 150-162 | IN-05: test discipline nit | INFO | Deferred per 02-REVIEW.md — test asserts behavior not compiled pattern; no test correctness issue. |

**No TBD/FIXME/XXX/debt markers** found in Phase 2 source files. The four deferred info findings are in the 02-REVIEW.md itself and are explicitly documented as advisory-only.

### Human Verification Required

#### 1. End-to-End SC#1 — github Plugin publish and crash recovery

**Test:** In a running cluster: create a Plugin CR with `type: github`, a valid repo, and a branch ref. Observe the reconcile log. After one reconcile, check the PVC for `plugin/<name>.tar.gz`. Then kill the operator pod immediately after the `os.CreateTemp` call completes but before the DB row UPDATE (requires a debugger breakpoint or manual timing). Restart the operator and verify: no torn-byte file exists and the plugin is re-published idempotently on the next reconcile.
**Expected:** `plugin/<name>.tar.gz` appears within one reconcile cycle; crash-and-recover leaves no partial file and the next reconcile publishes the correct archive.
**Why human:** Requires a live Kubernetes cluster with GitHub network access, a real PVC, and deliberate pod-kill timing to exercise the crash-recovery invariant.

#### 2. End-to-End SC#2 — PluginMarketplace with real upstream

**Test:** Point a PluginMarketplace CR at a real Claude-Code-shaped marketplace.json URL. Include one plugin with `type: npm` in the upstream. Introduce one plugin with an unreachable tarball URL. Observe status.message and cache files. Then delete a plugin entry from the upstream (simulating vanishment) and trigger a re-reconcile.
**Expected:** Valid plugins produce `marketplace/<name>/plugin/<plugin-name>.tar.gz`. npm plugin → `UnsupportedPluginSource` in `status.message` without aborting. Unreachable plugin recorded in `status.message`. Vanished plugin's cache file and DB row deleted in Stage-3.
**Why human:** Requires a real network-accessible marketplace.json URL and an intentionally broken plugin tarball URL.

#### 3. End-to-End SC#3 — Two marketplaces, alphabetical winner

**Test:** Create two PluginMarketplace CRs (e.g. `alpha-mkt` and `beta-mkt`) both exposing a plugin with the same name. Also create a Plugin CRD with that same name. Trigger reconciles on all three.
**Expected:** `alpha-mkt` (alphabetically lower) keeps `Synced=True` and materializes the plugin. `beta-mkt` gets `Synced=False, reason=NameConflict`. The Plugin CRD entry takes precedence over both marketplace entries.
**Why human:** The listOtherMarketplaceCatalogs DB query path (reading prior marketplace_plugins rows) requires a live DB with real populated rows from prior reconcile cycles.

#### 4. End-to-End SC#4 — Operator startup validation and size cap live

**Test:** Set `ACH_PLUGIN_MAX_SIZE_MIB=0` (or `-1`, or `abc`) and observe the operator pod. Then set a valid value but point a Plugin at a tarball that exceeds the cap. Observe status conditions and PVC contents.
**Expected:** Operator pod exits 1 immediately on misconfigured cap. Oversized plugin gets `SourceReachable=False, reason=PluginTooLarge`; no `.tar.gz` appears in `plugin/` on the PVC.
**Why human:** Startup validation requires a real pod restart. Oversized plugin testing requires a real archive > the cap limit.

#### 5. End-to-End SC#5 — ExecutionResourcesResolved + orphan cleanup audit events

**Test:** Create an Environment referencing a model name not registered in LiteLLM. Inspect `kubectl get environment <name> -o yaml` status. Then intentionally create an orphan LiteLLM key (a key that exists in LiteLLM but is absent from ACH's active-key DB rows and is >10 minutes old). Wait for the orphan-cleanup interval. Inspect Operator stdout logs for `"audit":true` events. Then make LiteLLM unreachable and observe the cleanup loop abort.
**Expected:** `status.unresolvedRuntime` contains the non-registered model; `ExecutionResourcesResolved=False, reason=ResourceUnresolved`. Audit log shows `{"audit":true, "action":"operator.orphan-cleanup", "target.kind":"litellm_key", "outcome":"success"}` per revocation. On LiteLLM-unreachable, the tick aborts and logs `outcome=litellm_unreachable` without crashing the manager.
**Why human:** Requires a live LiteLLM instance, real key lifecycle, and observing stdout JSON audit events from the running pod.

#### 6. Deferred info findings (IN-01, IN-02, IN-03, IN-05) — advisory

**Test:** Review the four deferred findings in 02-REVIEW.md: comment wording at `pluginmarketplace_controller.go:295-307`, %w semantics at `restclient.go:135-138`, path-in-error-log at `transport.go:60-72`, test pattern assertion at `marketplace_filters_test.go:150-162`.
**Expected:** Developer decides whether to address in a follow-up or defer to next phase.
**Why human:** These are code-quality/documentation judgment calls, not automated correctness checks.

### Gaps Summary

No automated gaps. All five success criteria are verified in code against the codebase. The six items above require human/live-environment testing.

**Notable observation:** REQUIREMENTS.md checkboxes for OP-03, OP-06, OP-07, OP-08, OP-09 are still `[ ]` (unchecked) while the code satisfies them. This is a documentation-only discrepancy and not a code gap. Recommend updating as a housekeeping step.

**SweepTmp runnable deferral:** The `.tmp/` orphan-sweep Runnable (Hub §10.3) is implemented as a function (`cachefs.SweepTmp`) but is not registered in the manager. The main.go comment explicitly documents this as a discretion deferral. This is not a blocker — the create+rename pattern leaves minimal orphan .tmp files in the happy path.

---

_Verified: 2026-05-18T00:00:00Z_
_Verifier: Claude (gsd-verifier)_
