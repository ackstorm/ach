---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 06
subsystem: operator
tags: [go, controller-runtime, pluginmarketplace, three-stage-refresh, marketplace-json, RE2, name-conflict, OP-06, OP-07, OP-08, OP-09, D-07, D-09, D-10, D-12, T-02-06-01, T-02-06-07]

# Dependency graph
requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation/02-02
    provides: internal/sources package (Fetcher interface, SourceSpec, FetchRequest, FetchResult, Err* sentinels, registry.For dispatcher)
  - phase: 02-external-refs-marketplace-operator-reconciliation/02-03
    provides: internal/db.MarketplacePlugin struct + UpsertMarketplacePlugin / ListMarketplacePlugins / DeleteMarketplacePlugin helpers
  - phase: 02-external-refs-marketplace-operator-reconciliation/02-05
    provides: internal/controller/ach/conditions.go (8 closed-set Reason* constants + setExternalRefCondition) + buildSourceSpec / extractAuthSecretRef / requeueDurationFromRefresh / classifyFetchError / OversizeError / FetcherFactory helpers

provides:
  - "internal/controller/ach/marketplace_parse.go — Claude Code marketplace.json wire types (ClaudeCodeMarketplace + Plugin + Source variant) + parseClaudeCodeMarketplace (Stage-1 validator with T-02-06-01 DNS-1123 plugin-name mitigation) + marketplacePluginToSourceSpec (Stage-2 SourceSpec builder, errUnsupportedPluginSource sentinel for npm)"
  - "internal/controller/ach/marketplace_filters.go — compileAnchored (OP-07 ^-prepend, ErrInvalidConfig sentinel) + applyFilters (returns includeMatchedAny for Stage-1 zero-include-match flip)"
  - "internal/controller/ach/marketplace_conflict.go — OP-08 / Hub §12.3 cross-marketplace conflict resolution (Plugin CRD beats marketplace, then alphabetical-lowest metadata.name wins) + listPluginCRNames / listOtherMarketplaceCatalogs informer-cache helpers"
  - "internal/controller/ach/pluginmarketplace_controller.go — Hub §12.4 three-stage refresh lifecycle (Stage-1 fetch+parse+filter+conflict / Stage-2 SERIAL per-plugin materialization / Stage-3 DELETE sweep) + materializeMarketplacePlugin (§10.3 wrapper that applies PluginMaxSizeMiB per T-02-06-07 and writes marketplace_plugins) + formatStage2Message (D-10 first-5+truncate) + classifyFetchErrorMarketplace + markSyncedTrue/False"
  - "conditions.go gains ReasonNameConflict + ReasonUnsupportedPluginSource constants for Plan 02-06's reason vocabulary extensions"
  - "Spec-interpretation choice: cross-marketplace metadata.name loser flips Synced=False reason=NameConflict at CR level; Plugin-CRD-wins drops + per-plugin Stage-2 fetch failures stay informational in status.message with Synced=True"

affects:
  - 02-09 (cmd/operator/main.go wiring): MUST inject PluginMarketplaceReconciler.{DB: dbPool, PluginMaxSizeMiB: pluginMaxSizeMiB, Fetchers: nil} at SetupWithManager time — the cap applies to marketplace-sourced plugins too per T-02-06-07.
  - Phase 3 (Platform API): consumes status.message + status.conditions[Synced] from the §12.4 three-stage refresh; force-refresh annotation path (D-07) wired for Platform API's /admin/refresh patch surface.
  - Phase 5 (Content Service): reads marketplace_plugins.storage_location to serve cached files; Stage-3 DELETE sweep keeps the rows in sync with upstream.

# Tech tracking
tech-stack:
  added: []  # no new go.mod entries; reuses Plan 02-02 sources + 02-03 db + 02-05 helpers
  patterns:
    - "Marketplace wire format mirrored verbatim from Claude Code (Hub §12.1) — ClaudeCodeMarketplaceSource embeds pointers to existing achv1alpha1.*Source types so no duplication of wire-shape definitions"
    - "Stage discipline: Stage-1 failure aborts before ANY UPSERT/DELETE (no orphan rows); Stage-2 per-plugin failures are recorded in status.message but do NOT abort the stage; Stage-3 DELETE sweep removes rows + files for names absent from current upstream"
    - "Spec-interpretation choice for OP-08: CR-level Synced=False reason=NameConflict ONLY when this marketplace LOST a cross-marketplace name tiebreaker (string prefix match on decision.Reason starting with 'marketplace'); Plugin-CRD-wins drops are informational only"
    - "T-02-06-01 mitigation via DNS-1123 subdomain regex on plugin.Name during parse; bounded names (~253 chars max) keep status.message under Kubernetes' 4096-char limit (T-02-06-08)"
    - "T-02-06-07 mitigation via PluginMaxSizeMiB threading: marketplaceMaterialize reuses the io.LimitReader(body, cap+1) pattern from Plan 02-05 so oversize plugins surface in the same partial-failure path (Synced=True if Stage-1 succeeded, status.message records 'PluginTooLarge')"

key-files:
  created:
    - internal/controller/ach/marketplace_parse.go
    - internal/controller/ach/marketplace_parse_test.go
    - internal/controller/ach/marketplace_filters.go
    - internal/controller/ach/marketplace_filters_test.go
    - internal/controller/ach/marketplace_conflict.go
    - internal/controller/ach/marketplace_conflict_test.go
    - internal/controller/ach/pluginmarketplace_envtest_test.go
  modified:
    - internal/controller/ach/pluginmarketplace_controller.go (Phase 1 finalizer-only stub replaced with §12.4 three-stage Reconcile; struct extended with DB / PluginMaxSizeMiB / Fetchers)
    - internal/controller/ach/conditions.go (added ReasonNameConflict + ReasonUnsupportedPluginSource at the bottom of the closed-enum const block)

key-decisions:
  - "Spec-interpretation choice for OP-08 NameConflict CR-flip: only marketplace-loses (decision.Reason starts with 'marketplace') flip Synced=False reason=NameConflict; Plugin-CRD-wins drops stay informational in status.message with Synced=True. This matches Phase 2 SC #3's reading ('the loser reports Synced=False reason=NameConflict') while preserving Phase 2 SC #2's invariant ('Synced=True is preserved when Stage-1 succeeded')."
  - "DNS-1123-subdomain validation on plugin.Name during parseClaudeCodeMarketplace (T-02-06-01 mitigation) is a defense-in-depth on top of filepath.Join's normalization. Rejects '..', '/', leading '.', uppercase, non-printable chars before computeFinalPath sees them. Names are bounded ~253 chars (DNS-1123 max) which also bounds status.message size (T-02-06-08)."
  - "Empty plugins[] array treated as ErrUpstreamInvalid. A well-formed marketplace declares at least one plugin; zero-plugin marketplaces are likely upstream-misconfigurations, not intentional empty catalogs."
  - "Marketplace.json hard cap = 5 MiB via io.LimitReader (T-02-06-03 mitigation). Hub §12.1 expects KiB-to-tens-of-KiB; 5 MiB is generous but bounds memory under adversarial upstream."
  - "PriorRev='' on Stage-1 fetch — Phase 2 does not persist marketplace.json's own UpstreamRev. A future v1beta1 may add an external_refs row keyed by (kind='pluginmarketplace', name=cr.Name) for conditional-GET on the catalog file itself; Phase 2 ships without that optimization because the per-reconcile cost is dominated by Stage-2 anyway."
  - "PriorRev='' on Stage-2 per-plugin fetches — Phase 2 does not maintain per-plugin UpstreamRev for marketplace-sourced plugins. A future v1beta1 may track them via marketplace_plugins.upstream_rev for conditional-GET savings; Phase 2 ships without that optimization to keep the Stage-2 code path identical to the Plugin reconciler's §10.3 shape."
  - "Per-plugin auth Secret = marketplace's auth Secret. v1alpha1 PluginMarketplaceSpec has no per-entry AuthSecretRef; the parser-side ClaudeCodeMarketplacePlugin doesn't carry one either. materializeMarketplacePlugin reuses the marketplace's marketplaceSecret. Hub §12.1 implies the same identity hosts the marketplace.json and the referenced plugin sources, so this is acceptable; v1beta1 may add per-entry auth refs if multi-identity marketplaces emerge."
  - "Stage-3 currentNames set = decisions[i].Kept-only names (not the union including Stage-2 fetch-failures). A Stage-2 fetch failure does NOT remove the prior cached file — failed plugins keep their last-known-good file on disk + DB row intact; only upstream removal triggers the Stage-3 sweep. This matches Phase 2 SC #2 ('one-plugin upstream failure is recorded in status.message and other plugins still succeed') and gives operators time to observe the failure before stale content is dropped."
  - "Stage-3 'cache file remove err' is logged but NOT returned — the DB DeleteMarketplacePlugin is the load-bearing record; an orphaned cached file is benign (next reconcile's rename(2) overwrites it if upstream re-adds the plugin; otherwise it's swept by Plan 02-04's SweepTmp scope which only covers .tmp/, so true orphans persist as 'leaked' bytes — acceptable v1alpha1 tradeoff because PVC sizing already accommodates marketplace_plugins growth)."
  - "Two TestPMR_ functions (TestPMR_Stage3_DeleteSweep + TestPMR_NameConflict_AlphabeticalPriority) skip when r.DB is nil. They require a real Postgres pool because listOtherMarketplaceCatalogs reads marketplace_plugins rows. Pure unit coverage of resolveConflicts in marketplace_conflict_test.go exercises the alphabetical rule deterministically. Integration coverage lands via make test-integration in a future plan."

patterns-established:
  - "Three-stage reconcile discipline: Stage-1 failure aborts BEFORE any DB write (no orphan rows on transient upstream errors); Stage-2 per-plugin failures recorded in status.message via D-10 structured format but do NOT abort the stage; Stage-3 sweep diffs prior-row-set vs current-kept-set"
  - "Spec-interpretation distinction via decision.Reason string prefix: 'marketplace ...' (alphabetical loser → CR-level NameConflict flip) vs 'Plugin CRD ...' (informational only). Future v1beta1 NameConflict subtypes can extend this with additional prefixes without disturbing existing callers."
  - "Per-test marketplaceFakeFactory routes Fetcher dispatch by per-type discriminator key, enabling Stage-1 (catalog fetch) + Stage-2 (per-plugin tarball fetches) to be exercised with distinct fakes in one envtest. keyedFakeFetcher allocates fresh bodies per Fetch() so Eventually() polling tolerates suite-reconciler retries on the same CR."

requirements-completed:
  - OP-06
  - OP-07
  - OP-08

# Metrics
duration: ~15min
completed: 2026-05-17
---

# Phase 02 Plan 06: PluginMarketplace Three-Stage Refresh Summary

**Hub §12.4 three-stage refresh wired into PluginMarketplaceReconciler: Stage-1 fetches + parses + filters marketplace.json (DNS-1123 plugin-name validation + 5 MiB cap), Stage-1.6 runs Plugin-CRD-beats-marketplace + alphabetical-lowest conflict resolution, Stage-2 materializes plugins SERIALLY via the same §10.3 fetch→stage→fsync→rename loop the Plugin reconciler uses (with the same PluginMaxSizeMiB cap per T-02-06-07), Stage-3 DELETE-sweeps vanished names. Per-plugin failures recorded in status.message via D-10 structured format with first-5-verbatim + +M-more truncation; force-refresh annotation cleared on success per D-07.**

## Performance

- **Duration:** ~15 min (wave-3 executor agent in worktree)
- **Started:** 2026-05-17T08:33:00Z (approximate — agent spawn after wave 2 merge)
- **Completed:** 2026-05-17T08:44:57Z
- **Tasks:** 4 / 4
- **Files created:** 7 (4 task-1 + 2 task-2 + 1 task-4)
- **Files modified:** 2 (conditions.go, pluginmarketplace_controller.go)

## Accomplishments

- **internal/controller/ach/marketplace_parse.go** declares the Claude Code marketplace.json wire types — `ClaudeCodeMarketplace` / `ClaudeCodeMarketplacePlugin` / `ClaudeCodeMarketplaceSource` — and implements `parseClaudeCodeMarketplace` (Stage-1 validator) + `marketplacePluginToSourceSpec` (Stage-2 SourceSpec builder, returns `errUnsupportedPluginSource` sentinel for `npm`). DNS-1123-subdomain validation on plugin names (T-02-06-01 mitigation) rejects path-traversal sequences before they reach `computeFinalPath`.
- **internal/controller/ach/marketplace_filters.go** implements `compileAnchored` (OP-07 `^`-prepend with `ErrInvalidConfig` sentinel for compile failure) + `applyFilters` (returns `includeMatchedAny` so the caller can flip zero-include-match to `ReasonUpstreamInvalid`).
- **internal/controller/ach/marketplace_conflict.go** implements `resolveConflicts` (OP-08 / Hub §12.3 deterministic precedence — Plugin CRD wins absolutely, then alphabetical-lowest marketplace.metadata.name wins) + `listPluginCRNames` / `listOtherMarketplaceCatalogs` informer-cache helpers. `dbPool=nil` returns an empty other-catalogs map for the Phase 1 envtest path.
- **internal/controller/ach/pluginmarketplace_controller.go** replaces the Phase 1 finalizer-only stub with the full §12.4 three-stage lifecycle:
  - **Stage 1** fetches marketplace.json via the resolved Fetcher (5 MiB hard body cap per T-02-06-03), parses + DNS-1123-validates plugin names, compiles + applies RE2 include/exclude filters, runs cross-marketplace conflict resolution. ANY Stage-1 failure flips `Synced=False` with the §12.4 reason and produces ZERO `marketplace_plugins` writes/deletes.
  - **Stage 2** materializes survivors SERIALLY (D-09) via the new `materializeMarketplacePlugin` helper. The helper mirrors §10.3 step-for-step (mkdir → CreateTemp → io.Copy with `io.LimitReader(body, cap+1)` per T-02-06-07 → fsync → close → rename(2) → `UpsertMarketplacePlugin`). Per-plugin failures (Unreachable, Unauthorized, NotFound, UpstreamInvalid, PluginTooLarge, UnsupportedPluginSource) recorded in `status.message` via `formatStage2Message` (D-10 structured one-line summary with first-5-verbatim + `+M more` truncation).
  - **Stage 3** DELETE-sweeps: enumerates `marketplace_plugins` rows for this marketplace, identifies names absent from the current Kept set, `os.Remove`s the cached file + `DeleteMarketplacePlugin`s the row.
- **conditions.go** gains `ReasonNameConflict` + `ReasonUnsupportedPluginSource` at the end of the closed-enum block (Plan 02-05 already declared the 8 SourceReachable reasons in the same file; Plan 02-06's two reasons are marketplace-specific).
- **Spec-interpretation choice** (documented in the Reconcile body): a marketplace whose name LOST the cross-marketplace tiebreaker flips `Synced=False reason=NameConflict` at the CR level; Plugin-CRD-wins drops + per-plugin Stage-2 fetch failures stay informational in `status.message` with `Synced=True`. The distinction is made by string-prefixing `decision.Reason` (`"marketplace ..."` → CR-flip; `"Plugin CRD ..."` → informational).
- **D-07 force-refresh annotation** cleared via the same `r.Update` after the status PATCH, matching the Plugin reconciler's pattern.
- **Deletion path preserved** — Phase 1's `os.RemoveAll(marketplace/<name>/)` stays, with a `marketplace_plugins` row sweep added before finalizer removal (OP-12 parity with the Plugin reconciler's `DeleteExternalRef` on delete).
- **34 unit tests across 4 _test files PASS** (19 from Task 1 + 8 from Task 2 + 11 TestPMR_ envtest functions, 2 of which integration-skip without r.DB). Phase 1 finalizer test (`TestPluginMarketplaceFinalizerAddRemove`) still green.

## Task Commits

Each task was committed atomically on `worktree-agent-a5c9f5b067e4952a4`:

1. **Task 1: marketplace_parse.go + marketplace_filters.go** — `08486e1` (feat)
2. **Task 2: marketplace_conflict.go (cross-marketplace name resolution)** — `81ebc01` (feat)
3. **Task 3: PluginMarketplaceReconciler three-stage refresh** — `70aa4eb` (feat)
4. **Task 4: envtest coverage (11 TestPMR_ functions)** — `5750219` (test)

_SUMMARY.md commit follows this list; STATE.md / ROADMAP.md updates are the orchestrator's responsibility after wave 3 merges._

## Files Created/Modified

### Created (7)

- `internal/controller/ach/marketplace_parse.go` (~220 lines) — ClaudeCodeMarketplace types, parseClaudeCodeMarketplace, marketplacePluginToSourceSpec, errUnsupportedPluginSource sentinel, DNS-1123 plugin-name validator.
- `internal/controller/ach/marketplace_parse_test.go` (~190 lines) — 10 unit tests covering valid parse, malformed JSON, zero plugins, unknown type, npm-kept, missing subobject, plugin-name traversal rejection, uppercase rejection, npm→errUnsupportedPluginSource, github SourceSpec preservation.
- `internal/controller/ach/marketplace_filters.go` (~115 lines) — ErrInvalidConfig sentinel, compileAnchored (^-prepend), applyFilters with includeMatchedAny flag, matchAny helper.
- `internal/controller/ach/marketplace_filters_test.go` (~165 lines) — 9 unit tests covering anchored compile, invalid pattern, empty input, include-some/none, exclude, include+exclude composition, neither-set vacuous-match, ^-anchor blocks substring match.
- `internal/controller/ach/marketplace_conflict.go` (~190 lines) — ConflictDecision type, resolveConflicts (Plugin-CRD-wins + alphabetical-lowest), listPluginCRNames / listOtherMarketplaceCatalogs informer helpers.
- `internal/controller/ach/marketplace_conflict_test.go` (~180 lines) — 8 unit tests: Plugin-CRD-wins, alphabetical-win, alphabetical-lose, no-conflict, triple-tie (3 calls), empty input, Plugin-CRD-beats-alphabetical, input-order preservation.
- `internal/controller/ach/pluginmarketplace_envtest_test.go` (~700 lines) — marketplaceFakeFactory with per-type-discriminator dispatch, keyedFakeFetcher with fresh-body-per-Fetch, ensureSecret helper, applyMarketplaceCR helper, drainReconcileUntil polling helper, 11 TestPMR_ functions (9 enabled + 2 integration-skipped).

### Modified (2)

- `internal/controller/ach/pluginmarketplace_controller.go` (~510 lines net diff, mostly added) — struct extended with DB / PluginMaxSizeMiB / Fetchers; deletion path adds DB row sweep; steady-state replaced with §12.4 three-stage Reconcile + materializeMarketplacePlugin + formatStage2Message + classifyFetchErrorMarketplace + markSyncedTrue / markSyncedFalse helpers; D-07 annotation handling; 5 MiB body cap on marketplace.json; force-refresh annotation removal.
- `internal/controller/ach/conditions.go` (+15 lines) — ReasonNameConflict + ReasonUnsupportedPluginSource constants added inside the closed-enum block at the bottom of the file; Phase 1 comment block updated to mention these are Plan 02-06's additions.

## Decisions Made

- **OP-08 NameConflict CR-flip spec-interpretation:** Only marketplace-loses (decision.Reason starts with `"marketplace "`) flip Synced=False reason=NameConflict at the CR level. Plugin-CRD-wins drops and per-plugin Stage-2 fetch failures stay informational in status.message with Synced=True. This matches Phase 2 SC #3's reading while preserving Phase 2 SC #2's invariant.
- **DNS-1123-subdomain on plugin.Name** (T-02-06-01 mitigation) is defense-in-depth on top of filepath.Join's normalization. Names are bounded ~253 chars which also caps status.message contributions (T-02-06-08).
- **Empty `plugins[]` array treated as ErrUpstreamInvalid.** A well-formed marketplace declares at least one plugin; zero-plugin marketplaces are likely upstream misconfigurations.
- **Marketplace.json hard cap = 5 MiB** via io.LimitReader (T-02-06-03 mitigation). Hub §12.1 expects KiB-to-tens-of-KiB; 5 MiB is generous but bounds memory under adversarial upstream.
- **PriorRev='' on Stage-1 + Stage-2 fetches** — Phase 2 does NOT persist marketplace.json's own UpstreamRev or per-plugin UpstreamRev for marketplace-sourced plugins. Conditional-GET optimization deferred to v1beta1.
- **Per-plugin auth Secret = marketplace's auth Secret.** v1alpha1 PluginMarketplaceSpec has no per-entry AuthSecretRef and the parser-side ClaudeCodeMarketplacePlugin doesn't carry one either. Reusing the marketplace's secret is acceptable because Hub §12.1 implies the same identity hosts both the catalog and the referenced sources.
- **Stage-3 currentNames = Kept-only names** (not the union including Stage-2 failures). A Stage-2 fetch failure does NOT remove the prior cached file — failed plugins keep their last-known-good content on disk + in DB. Only upstream removal triggers the sweep.
- **Stage-3 cache-file-remove err is LOGGED but not RETURNED.** The DB DeleteMarketplacePlugin is the load-bearing record; an orphaned cached file is benign (next reconcile's rename(2) overwrites it if upstream re-adds the plugin; otherwise it's leaked bytes — acceptable v1alpha1 tradeoff).
- **Two TestPMR_ functions skip without r.DB.** TestPMR_Stage3_DeleteSweep + TestPMR_NameConflict_AlphabeticalPriority require a real Postgres pool. Pure unit coverage of resolveConflicts in marketplace_conflict_test.go exercises the alphabetical rule deterministically; the full reconciler integration lands via make test-integration in a future plan.

## Deviations from Plan

**None — plan executed exactly as written.**

The plan's `<action>` blocks were followed precisely. Notes worth recording for the reviewer:

1. The plan's Task 4 enumeration listed 10 TestPMR_ functions; the implementation has 11 (added `TestPMR_Stage2_PluginTooLarge` to exercise the T-02-06-07 mitigation explicitly per the plan's threat-model section). The verify `grep -c "TestPMR_" ... ; # expect 10` now reports 11, which exceeds the requirement.
2. The plan's Task 3 spec-interpretation note "Update Task 3 accordingly when running this task. Document the spec-interpretation choice in code comment." was followed: the Reconcile body now flips Synced=False reason=NameConflict when ANY decision was a marketplace-loser (string-prefix match on `"marketplace "`), distinguishing this from Plugin-CRD-wins (string-prefix `"Plugin CRD "` → informational only).
3. The plan's `<files>` field lists `internal/controller/ach/conditions.go` as modified; this plan added the two marketplace-specific reasons exactly as specified.
4. `cachefs.EnsureLayout` (Phase 1) already creates `marketplace/` + `.tmp/` at the cache-root level; the per-marketplace `marketplace/<name>/plugin/` subdirectory is created by `materializeMarketplacePlugin` via `os.MkdirAll` before each rename. The envtest's `newCacheRoot` helper invokes `EnsureLayout` so the suite + per-test cache roots match the deployed shape.

## Issues Encountered

- **Duplicate `pluginFailure` type declaration.** Initial draft of `pluginmarketplace_controller.go` declared `pluginFailure` both inside the Reconcile body (local type) AND at package level (for `formatStage2Message`'s standalone testability). The compiler caught the double-declaration; resolved by removing the local type and using the package-level one.
- **`status update failed` warnings during envtest reconciler tests.** Expected: the suite-registered PluginMarketplaceReconciler (with Fetchers=nil → registry.For) races with our per-test reconciler (with the fake factory). Both write status; controller-runtime reports `"object has been modified"` conflicts. The tests pass deterministically because `drainReconcileUntil` waits for the eventually-consistent state. Same pattern documented in Plan 02-05 SUMMARY.
- **Subtree dir `marketplace/<name>/plugin/` did not exist at first rename(2).** `cachefs.EnsureLayout` creates only `marketplace/` at the root. Fixed by adding `os.MkdirAll(finalDir, 0o755)` inside `materializeMarketplacePlugin` before `CreateTemp` (the .tmp dir is shared at cache-root level; only the FINAL parent dir needs creating).

## User Setup Required

None — no external service configuration required by Plan 02-06. The plan's `user_setup` frontmatter is `[]`. The new reconciler-struct fields (DB, PluginMaxSizeMiB, Fetchers) are injected by Plan 02-09 at cmd/operator/main.go's `SetupWithManager` call site.

## Next Plan Readiness

- **Plan 02-09 (cmd/operator/main.go):** MUST inject the three new reconciler-struct fields at SetupWithManager time:
  ```go
  if err = (&achcontroller.PluginMarketplaceReconciler{
      Client:           mgr.GetClient(),
      Scheme:           mgr.GetScheme(),
      Namespace:        watchNS,
      Log:              ctrl.Log.WithName("controller").WithName("PluginMarketplace"),
      CacheRoot:        cacheRoot,
      DB:               dbPool,            // Plan 02-09 (NEW)
      PluginMaxSizeMiB: pluginMaxSizeMiB,  // Plan 02-09 (NEW; T-02-06-07 mitigation)
      Fetchers:         nil,               // nil → registry.For
  }).SetupWithManager(mgr); err != nil { ... }
  ```
  The PluginMaxSizeMiB injection is REQUIRED for the T-02-06-07 mitigation to take effect in production (otherwise marketplace-sourced plugins observe an infinite cap).
- **Plan 02-08 (orphan-cleanup loop)** is independent of this plan; no shared surface beyond conditions.go's `setExternalRefCondition` helper which orphan-cleanup does not consume.
- **Phase 5 (Content Service):** reads marketplace_plugins.storage_location to serve cached files; Stage-3 DELETE sweep keeps the rows in sync with upstream.
- **Phase 3 (Platform API):** Plan 02-06's `D-07` force-refresh annotation clearance is the consumer side of Platform API's `/platform/admin/refresh` patch surface. PluginMarketplace CRs annotated with `ach.ackstorm.ai/force-refresh: <RFC3339-ts>` will reconcile on the next event delivery; the annotation is removed in the same UPDATE as the success status PATCH.

## Threat Model Coverage

All eight threats from the plan's `<threat_model>` section have implementation hooks:

- **T-02-06-01** (path-traversal in plugin names) — `mitigate`: DNS-1123 subdomain validation in parseClaudeCodeMarketplace rejects `..`, `/`, leading `.`, uppercase, non-printable chars before computeFinalPath. `TestParseClaudeCodeMarketplace_PluginNameTraversalRejected` + `TestParseClaudeCodeMarketplace_PluginNameUppercaseRejected` cover the surface.
- **T-02-06-02** (adversarial 10k-plugin marketplace) — `accept`: D-09 chose serial materialization; Hub §12.4 expects tens of plugins v1alpha1 scale. Bounded-parallel deferred to v1beta1.
- **T-02-06-03** (5 MiB body echo in status.message) — `mitigate`: parseClaudeCodeMarketplace's error wraps short prefixes + json.Unmarshal's bounded err string; 5 MiB io.LimitReader on marketplace.json body bounds memory. `TestPMR_Stage1_ParseFails` asserts message contains "parse" without raw body bytes.
- **T-02-06-04** (RE2 catastrophic backtracking) — `accept`: Go's regexp is RE2 (linear-time by design).
- **T-02-06-05** (recursive marketplace.type=marketplace) — `accept`: parser rejects unknown source.types; marketplace recursion is not in the Claude Code wire format.
- **T-02-06-06** (concurrent reconcile race) — `accept`: single-replica Operator + controller-runtime workqueue + serial Stage-2 ensures no two concurrent reconciles for the same PluginMarketplace CR.
- **T-02-06-07** (uncapped marketplace plugin tarball) — `mitigate`: PluginMaxSizeMiB threaded into PluginMarketplaceReconciler struct + materializeMarketplacePlugin uses io.LimitReader(body, cap+1); overshoot returns OversizeError → classifyFetchErrorMarketplace → ReasonPluginTooLarge in the partial-failure list. `TestPMR_Stage2_PluginTooLarge` covers the path.
- **T-02-06-08** (status.message echoes adversarial plugin names) — `accept`: T-02-06-01's DNS-1123 validation bounds names to ~253 chars and excludes control chars; formatStage2Message's first-5+truncate keeps the message ≤ ~500 chars typical.

## Self-Check: PASSED

Verified after writing this SUMMARY:

Commits exist on `worktree-agent-a5c9f5b067e4952a4`:
- `08486e1` (Task 1): FOUND
- `81ebc01` (Task 2): FOUND
- `70aa4eb` (Task 3): FOUND
- `5750219` (Task 4): FOUND

Key files exist:
- `internal/controller/ach/marketplace_parse.go`: FOUND
- `internal/controller/ach/marketplace_filters.go`: FOUND
- `internal/controller/ach/marketplace_conflict.go`: FOUND
- `internal/controller/ach/pluginmarketplace_envtest_test.go`: FOUND
- `internal/controller/ach/pluginmarketplace_controller.go` carries the three-stage refresh: FOUND (grep "Stage 1\|Stage 2\|Stage 3" → 6 hits)
- `internal/controller/ach/conditions.go` carries ReasonNameConflict + ReasonUnsupportedPluginSource: FOUND

Build + test gates:
- `./scripts/dev.sh go build ./...`: clean (whole module)
- `./scripts/dev.sh go test ./internal/controller/ach/... -count=1`: ALL PASS in ~14s (Phase 1 finalizer + CEL admission + Plan 02-05 helpers + Plan 02-06 helpers + 9 TestPMR_ enabled + 2 integration-skipped)
- `grep -c "Stage 1\|Stage 2\|Stage 3" internal/controller/ach/pluginmarketplace_controller.go`: 6 (≥3 required)
- `grep -c "stage-2:\|plugin(s) failed" internal/controller/ach/pluginmarketplace_controller.go`: 2 (≥2 required)
- `grep -c "PluginMaxSizeMiB" internal/controller/ach/pluginmarketplace_controller.go`: 4 (≥1 required)
- `grep -c "TestPMR_" internal/controller/ach/pluginmarketplace_envtest_test.go`: 11 (≥10 required)

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Plan: 02-06*
*Completed: 2026-05-17*
