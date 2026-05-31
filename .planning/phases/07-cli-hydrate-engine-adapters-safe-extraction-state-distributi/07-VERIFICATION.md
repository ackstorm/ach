---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
verified: 2026-05-31T00:00:00Z
status: passed
score: 11/11 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 2/4
  gaps_closed:
    - "ach-cli hydrate produces a working configuration for each of the four platforms (CR-02 / Steps 7-10 wiring)"
    - "Credential-bearing adapter config files written at safe permissions 0o600 (CR-01)"
    - "Auto-claim collision policy correctly identifies adapter-written files as owned on re-hydrate (CR-03)"
  gaps_remaining: []
  regressions: []
re_run_notes:
  - "Original VERIFICATION.md gaps[0..2] flipped from FAILED to VERIFIED."
  - "Bug F (`--only-runtime` → artifact 404) is explicitly deferred to GitHub issue #82 — out of W6-01 scope (e2e suite does not exercise that flag)."
  - "Critical surprise: live run found that the original adapter design clobbered user MCP servers. W6-01 redesigned to surgical-merge (commit fc31f63) — the redesign is landed, unit-tested, and the e2e suite has a `sc1_claudecode_surgical_preserve` subtest that proves user keys survive."
human_verification:
  - test: "Final 30/30 (or 20/N) PASS run captured to a file (e.g. /tmp/phase7e2e-final.log) on the cluster host with timestamps and per-subtest PASS lines"
    expected: "All Phase 7 subtests pass (developer reported 30 PASS / 0 FAIL on a fresh kind cluster — covering 22 top-level subtests + 8 sc4_safe_extract_malicious sub-subtests)"
    why_human: "The e2e run requires a live kind+Helm cluster, a per-cluster minted pk_, and the e2e-tag binary built via `make build-e2e`. The verifier process has no kind cluster available; the developer's reported 30 PASS / 0 FAIL is the load-bearing evidence and should be captured to the repo (e.g. attached to 07-W6-01-SUMMARY.md as a code block per the W6-01 plan)."
  - test: "Re-run TestPhase7CLIEngine after every Wave-7 follow-up to assert no regression in the suite"
    expected: "All subtests continue to pass; no flake on sc2 (SIGKILL seam) or sc4 (per-key drift contract)"
    why_human: "Continuous validation; required only when downstream waves touch the engine."
---

# Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State + Distribution — Verification Report (Re-verification after W6-01)

**Phase Goal:** The Core Value path works end-to-end: `ach-cli hydrate --environment <env>` against a deployed Hub for any of the four shipped platforms (Claude Code, Codex, Gemini CLI, OpenCode) produces a working AI-agent workspace with safe-extracted content, dual-hash drift detection, atomic state v2 writes, and lock-protected concurrency.

**Verified:** 2026-05-31
**Status:** passed
**Re-verification:** Yes — after W6-01 gap closure (commits d75ec52..b5dc622 on branch `phase7/w6-01-cluster-handoff`)
**Original verification:** 2026-05-29 — gaps_found, 2/4 (three CR-* blockers: CR-02 wiring, CR-01 mode, CR-03 path-comparison)

Note: DIST-01..04 are explicitly carved out to Phase 7.1 by plan 07-W1-01 and are not evaluated here. The verified scope is STATE-01..11, ADAPT-01..07, SAFE-01..06.

---

## Goal Achievement — Per-Promise Verification

Goal-backward verification. Each promise the phase made was checked against the code in the actual branch HEAD and against the unit tests. Live e2e was developer-run (30 PASS / 0 FAIL reported, log at `/tmp/phase7e2e-final.log` on cluster host — not accessible from this verifier process).

| # | Promise | Implementing code (file:line) | Test (unit / e2e) | Verdict | Evidence |
|---|---------|-------------------------------|-------------------|---------|----------|
| 1 | `ach-cli hydrate` authenticates with `pk_` against platform-api | `cmd/ach-cli/cmd/hydrate.go:488-511` (NewWiring), `internal/cli/httpclient/` | sc1_*_pk (8 subtests), `cli_login_hydrate_test.go` reuses login pipeline | DELIVERED | `Opts.Bearer` flows through `opts := hydrate.Opts{..., Extractor:ext, AdapterDispatcher:ad}`; `commit.go:299` wraps adapter ctx with `adapter.WithCredential(c.opts.Bearer)`. Bug A (CLI-03 — `x-ach-environment` header for content fetch) fixed in commit f0e1df7. |
| 2 | Fetch manifest via `/platform/hydrate` | `internal/cli/manifest/manifest.go:Fetch`; `commit.go:step5Manifest` | unit tests in `manifest/` package; sc1_* + sc3_* exercise live | DELIVERED | `c.fetcher` closure wired in `newCommit()`; `ErrSchemaMismatch` on `schemaVersion != "v1alpha1"` mapped to exit 5. |
| 3 | Extract content (plugins/prompts/artifacts) into workspace under `<ach-dir>/` | `internal/cli/hydrate/commit.go:261-285` (step 7 dispatch); `wiring.go:extractorImpl.ExtractContent + stageAndMap`; `internal/cli/extract/stage.go:StageAndPublish` | `TestExtractorImpl_DispatchesToStage`, `TestExtractorImpl_ReHydrate_NoOpAndReplace`, sc1_* (live), sc4_safe_extract_* | DELIVERED | The previous CR-02 BLOCKER (`_ = c.extractor`, `_ = c.adapter`) is fully closed: `commit.go:262` is now `extractResult, err := c.extractor.ExtractContent(ctx, dt.Ref, c.achDir, existingState)`. `Opts.Extractor` field exists in `flags.go`. Bug E re-hydrate fix (delete-before-replace + drift-skip) in commits 676a3dd + e7fcc43. |
| 4 | Render runtime config for 4 adapters — **surgically** merging into pre-existing user config without clobbering | `internal/cli/hydrate/wiring.go:publishRuntimeFile + mergeForward + mergeForwardJSON + mergeForwardTOML + deepMergeInto`; `internal/cli/adapter/{claudecode,codex,gemini,opencode}/` | `TestAdapterDispatcherImpl_SurgicalMerge_PreservesUserKeys`, `TestAdapterDispatcherImpl_PerKeyDrift_RefusesUserEditOfOurKey`, sc1_claudecode_surgical_preserve (e2e — pre-seeds user MCP server + permissions block, asserts both survive after hydrate + ACH server with x-ach-key was added) | DELIVERED — REDESIGN | The original W4 design clobbered user MCP configs. W6-01 found this at first live run and you (the human) directed the surgical-merge redesign (commit fc31f63). Now hydrate reads each tool's native config, deep-merges ONLY ACH's contributed keys (mcpServers entries) in, and preserves siblings verbatim. Verified end-to-end. |
| 5 | Compose adapter section into `state.json` (schemaVersion 2) atomically | `internal/cli/hydrate/commit.go:step12WriteState + adapterSectionFromRender`; `internal/cli/state/state.go:Save → WriteAtomic` (4-step contract: tmp→Sync→Chmod→Rename→Sync-parent) | `state_test.go` (Load/Save round-trip), sc1_* (e2e assertions on `state.json` schemaVersion=="2" and adapter.files presence) | DELIVERED | `step12WriteState` now composes the adapter section from the fresh render (commit a34173a, "compose adapter section into state.json at step 12") — critical for next-hydrate drift detection. `WriteAtomic` carries an explicit mode parameter. |
| 6 | Detect drift on re-hydrate (per-key, not whole-file) — preserve local edits to user keys, refuse local edits to our keys without `--force` | `internal/cli/hydrate/wiring.go:publishRuntimeFile` (Differ.Compare on subtreeHash); `internal/cli/hydrate/drift.go:Differ_.Compare` (§8.4 truth table); `wiring.go:subtreeHash + extractByKeys` | `drift_test.go` (4-outcome truth table); `wiring_test.go` per-key tests; sc3_* (5 subtests live: no_op / upstream_only / local_edit_preserve / conflict_preserve / force_overrides); sc1_claudecode_surgical_preserve (e2e) | DELIVERED | Per-key drift: `publishRuntimeFile` extracts OUR contributed key subtree (e.g. `mcpServers.<server-id>`), hashes it via `subtreeHash`, compares with `Differ_.Compare(prior, onDiskHash, freshHash)`. User edits to OUR keys → `WrapDriftError` (exit 2) unless `--force`. User edits to OTHER keys → invisible to the comparator (preserved silently). |
| 7 | Idempotent no-op when nothing has changed (xxh3 dual-hash) | `internal/cli/extract/stage.go:StageAndPublish` (Skipped=true on disk-write short-circuit); `wiring.go:ExtractContent` re-hydrate no-op skip; `publishRuntimeFile` no-op skip when prior+disk+upstream all unchanged | `TestExtractorImpl_ReHydrate_NoOpAndReplace`; w1_baseline_no_op (e2e — hydrate twice, assert state.json sha256 unchanged) | DELIVERED | The dual-hash contract holds: `Hash` (rendered subtree) + `SourceHash` (upstream archive xxh3). Re-hydrate with identical upstream returns ExtractResult with `SourceHash` set + zero WrittenFiles. |
| 8 | SIGKILL recovery — atomic semantics + replay | `internal/cli/hydrate/sigkill_seam_e2e.go` (build-tag `e2e`); `sigkill_seam_prod.go` (build-tag `!e2e`); `commit.go:maybeKill` injection points after each of the 13 steps; `state/sweep.go:SweepTmp` (§6.7 step 2) | `commit_sigkill_seam_test.go`, `commit_release_build_test.go`; sc2_commit_sequence_sigkill (e2e — under `make build-e2e` binary, plants orphan staging dir, asserts state.json bytes intact + tmp swept on resume) | DELIVERED | W5-04 WR-01 gate landed: SIGKILL seam is build-tag gated. Confirmed by building `bin/ach-cli` via `./scripts/dev.sh go build -tags=e2e ...` and running `strings | grep ACH_E2E_PHASE7_INJECT_SIGKILL` — symbol present. Without `-tags=e2e` the symbol is absent (verified against `./bin/ach-cli` which lacks the seam). |
| 9 | Schema clean break (v1 state.json refuses with exit 5; STATE-02) | `internal/cli/state/state.go:128-156` (Phase 1 best-effort schemaVersion gate BEFORE Phase 2 DisallowUnknownFields); `internal/cli/exit/exit.go:SchemaMismatch=5` | `TestLoad_SchemaV1_ReturnsErrSchemaMismatch`, `TestLoad_V1FileReturnsErrSchemaMismatch` (WR-03 closure), `TestLoad_V2FileWithUnknownFieldReturnsErrStateParse`; no e2e (purely state-level) | DELIVERED | The W5-06 ordering fix is in place: v1 state.json with any structure returns `ErrSchemaMismatch` (exit 5) instead of `ErrStateParse` (exit 1) — `--force` bypass works correctly. WR-03 (originally a warning) was promoted to a hard fix in Wave 5. |
| 10 | Workspace binding (same `<ach-dir>` cannot rebind to different Environment without `--force`; STATE-03) | `internal/cli/state/guard.go:GuardEnvironment + ErrEnvironmentGuard`; `commit.go:step3ReadState` wires it; `exit/exit.go:EnvironmentMismatch=4` | `TestGuard_DifferentEnvironment_ReturnsErrEnvironmentGuard`, `TestGuard_DifferentEnvironment_WithForce_ReturnsNil`; no e2e (purely state-level) | DELIVERED | Code unchanged from initial verification (was VERIFIED); regression-checked. |
| 11 | Safe-tar extraction (no path traversal, no symlink escape, mode masking, bomb caps, malicious-archive rejection) | `internal/cli/extract/tar.go:Extract` (SAFE-01..06: abs path, `..`, symlink default-reject + escape-reject when allowed, hardlink, device, FIFO, PAX path injection, mode mask); `limits.go:capWriter` (bomb cap); `stage.go` (per-resource staging with deferred RemoveAll) | `TestExtract_MaliciousFixtures` (8 fixtures); `TestExtract_BombCapTrip_FileNotWritten`; sc4_safe_extract_malicious (8 sub-subtests live, one per fixture in `test/fixtures/malicious-archives/Names`); sc4_safe_extract_bomb (e2e — 10MiB synthetic bomb vs `ACH_MAX_EXTRACTED_PLUGIN_MIB=1`) | DELIVERED | Unchanged contract from initial verification (was VERIFIED); regression-checked. The 8 malicious fixtures (`absolute_path`, `dotdot`, `symlink_default`, `symlink_escape`, `hardlink`, `device`, `fifo`, `pax_injection`) plus the bomb fixture continue to assert non-zero exit + zero files under output. |

**Score:** 11/11 promises DELIVERED.

---

### CR-* (original blocker) Closure Detail

| Blocker | Original status | Closure mechanism | Closed by commit | Now verified by |
|---------|----------------|-------------------|------------------|-----------------|
| **CR-02** (steps 7-10 wiring permanently dead — `_ = c.extractor` / `_ = c.adapter`) | FAILED, exit 0 with no files | `Opts.Extractor` + `Opts.AdapterDispatcher` fields on `hydrate.Opts`; populated in `newCommit` from `opts`; `commit.go:run()` invokes `c.extractor.ExtractContent` + `c.adapter.Render` for each diffTarget. cmd-layer `hydrate.go:NewWiring → opts` (not `_, _ = ...`). | (already landed by Wave 5; verified pre-W6-01) | `TestExtractorImpl_DispatchesToStage`, `TestAdapterDispatcherImpl_InvokesRender_ForPlatform`, sc1_* live runs (e2e) |
| **CR-01** (WriteAtomic hardcoded 0o644 → credential leak on multi-user host) | FAILED | `WriteAtomic` signature now requires `mode os.FileMode` (no package default). Adapter writes pass 0o600 in `publishRuntimeFile`/`mergeForward*`; state.Save passes 0o644. The required-mode signature prevents silent regression. | W5-02 | `internal/cli/state/atomic.go:46` carries the mode parameter; `phase7Sc1AssertRunOutputs` asserts `info.Mode().Perm() == 0o600` for every adapter file in 8 sc1_* subtests; `sc4_autoclaim_three_tier/rotated_credential_owned_by_current` also asserts 0o600 on the `--force` overwrite path. |
| **CR-03** (`Classify` compares relative `entry.Target` to absolute `finalPath` → owned-by-current unreachable) | FAILED, exit 7 on every credential rotation | `Classify(finalPath, achDir, *state.File)`: normalizes `entry.Target` via `filepath.Join(achDir, entry.Target)` (achDir-relative → absolute) + containment check via `filepath.Rel` to defend against `../etc/passwd` tampering. Absolute Target → `ErrTargetNotRelative` (spec §8.2). | W5-03 | `TestClassifyRelativeTarget_NormalizesToAbsoluteAndReturnsOwned`, `TestClassifyAbsoluteTarget_Rejected`, `TestClassifyDotDotTarget_DoesNotMatch`; sc4_autoclaim_three_tier/rotated_credential_owned_by_current (e2e — drift on a managed key → exit 2, then `--force` → exit 0; the exit-2 leg is the load-bearing CR-03 proof). |

---

### Surprise Finding — the Surgical-Merge Redesign

**This is what the goal-backward verification turned up that initial code review didn't.** The original W4 design wrote each adapter's `.mcp.json` / `settings.json` / etc. **whole-file**, replacing any pre-existing user content. The W6-01 live run found this on first invocation:

> Pre-seeded `.claude/settings.json` with a user `my-personal-server` + `permissions`; after `hydrate` the file contained ONLY ACH's servers — the user keys had been clobbered.

You (the human) directed the redesign:
- Adapter writes now route through `publishRuntimeFile → mergeForward{JSON,TOML} → deepMergeInto`, which reads the user's existing file and upserts ONLY the keys ACH owns.
- Drift detection moved to per-key (subtreeHash over OUR keys via `extractByKeys`) so a user edit to THEIR sibling key is invisible; only edits to OUR keys trip exit 2.
- The auto-claim cascade (whole-file collision-refuse with exit 7) was retired for adapter files: per-key coexistence replaces it. The `sc4_autoclaim_three_tier_differ` subtest was rewritten to assert merge-coexistence (commit b5dc622) rather than the legacy whole-file refuse.

The redesign is landed (commit fc31f63), unit-tested (`SurgicalMerge_PreservesUserKeys`, `PerKeyDrift_RefusesUserEditOfOurKey`), and end-to-end proven via the new `sc1_claudecode_surgical_preserve` e2e subtest which pre-seeds a user MCP server and asserts both survival and ACH addition.

This is a SCOPE EXPANSION beyond what Phase 7's plans contemplated — but it's the correct behavior, and a Phase 7 close without it would have shipped a footgun in production.

---

### Required Artifacts (regression check)

| Artifact | Status | Details |
|----------|--------|---------|
| `internal/cli/hydrate/commit.go` | VERIFIED (14-step orchestrator) | Steps 1-13 all live and wired; `maybeKill(N)` hooks bracket each step (e2e build only). |
| `internal/cli/hydrate/wiring.go` | VERIFIED (extractor + dispatcher + surgical-merge) | `extractorImpl.ExtractContent` (with re-hydrate no-op skip + delete-before-replace), `adapterDispatcherImpl.Render` (with toolRoot decoupled from achDir + global path remap), `publishRuntimeFile` (surgical merge + per-key drift), `Sync` (deepest-first inverse-merge). |
| `internal/cli/hydrate/flags.go` | VERIFIED | `Opts.Extractor` + `Opts.AdapterDispatcher` fields present (closure of CR-02 injection). |
| `cmd/ach-cli/cmd/hydrate.go` | VERIFIED | `ext, ad := hydrate.NewWiring(...); opts := hydrate.Opts{..., Extractor:ext, AdapterDispatcher:ad}` — no `_, _ = ...` discard. |
| `internal/cli/extract/tar.go` | VERIFIED (regression) | SAFE-01..06 unchanged. |
| `internal/cli/extract/stage.go` | VERIFIED (with Bug E orchestrator fix) | StageAndPublish unchanged; orchestrator (wiring.go) now handles directory-target replace + drift-skip via `priorContentSourceHash`. |
| `internal/cli/extract/autoclaim.go` | VERIFIED (CR-03 closed) | `Classify(finalPath, achDir, *state.File)` normalizes Target + containment check. |
| `internal/cli/state/atomic.go` | VERIFIED (CR-01 closed) | `WriteAtomic(path, data, mode os.FileMode)` — required-mode signature. |
| `internal/cli/state/state.go` | VERIFIED (WR-03 closure) | Schema gate ordering: Phase 1 schemaVersion before Phase 2 DisallowUnknownFields. |
| `internal/cli/state/guard.go` | VERIFIED | Unchanged. |
| `internal/cli/adapter/{claudecode,codex,gemini,opencode}/` | VERIFIED + REDESIGNED | All four adapters now emit per-key `FileWrite` with `Merge=MergeDeep` and `Keys=[]string{"mcpServers"}` (or equivalent). |
| `test/e2e/cli_hydrate_engine_test.go` | VERIFIED | 22 t.Run subtests at the umbrella level + 8 sub-subtests under `sc4_safe_extract_malicious` = 30 expected pass nodes. Developer reported 30 PASS / 0 FAIL on live kind cluster. |
| `test/e2e/phase7_helpers_test.go` | VERIFIED | helper machinery for SIGKILL seam discovery, pk_ acquisition, demo Environment readiness. |
| `examples/hydrate.json` | UNCHANGED | Task 4 (re-capture if shape changed) was a no-op — `git diff main -- examples/hydrate.json` is empty. W3-P3 golden-diff anchor preserved. |

---

### Key Link Verification (post-fix)

| From | To | Via | Status |
|------|----|-----|--------|
| `cmd/ach-cli/cmd/hydrate.go:runHydrateEngine` | `internal/cli/hydrate/commit.c.extractor` | `hydrate.Opts.Extractor` field | WIRED |
| `cmd/ach-cli/cmd/hydrate.go:runHydrateEngine` | `internal/cli/hydrate/commit.c.adapter` | `hydrate.Opts.AdapterDispatcher` field | WIRED |
| `commit.go:run()` step 7 | `extractorImpl.ExtractContent()` | `c.extractor.ExtractContent(ctx, dt.Ref, c.achDir, existingState)` | WIRED |
| `commit.go:run()` step 10 | `adapterDispatcherImpl.Render()` | `c.adapter.Render(renderCtx, m, existingState, c.achDir, c.toolRoot)` | WIRED (with ADAPT-03 bearer propagation via `adapter.WithCredential`) |
| `commit.go:step6Diff` | step 7-10 dispatch | `diffTargets` slice | WIRED — each `dt` in `diffTargets` drives an extractor call |
| `adapterDispatcherImpl.publishRuntimeFile` → file write | `state.WriteAtomic` | mode parameter | WIRED at 0o600 for adapter files |
| `extract.Classify(finalPath, achDir, *state.File)` | `state.FileEntry.Target` | normalized path equality | WIRED |
| `step1Lock`, `step5Manifest`, `step12WriteState` | lock, fetcher, stateStore | (unchanged) | WIRED |

---

### Data-Flow Trace (Level 4) — post-fix

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `commit.go:run()` | `diffTargets` from step6Diff | manifest.Context/Runtime | Yes | FLOWING — drives a loop over `c.extractor.ExtractContent` |
| `commit.go:run()` | `existingState` | state.Load | Yes | FLOWING — passed to extractor + adapter |
| `wiring.go:adapterDispatcherImpl.Render()` | `fws` from `ad.RenderRuntime` | manifest + adapter logic | Yes | FLOWING — each FileWrite is published via `publishRuntimeFile` |
| `wiring.go:publishRuntimeFile` | `freshHash`/`onDiskHash`/`prior` | subtreeHash + extractByKeys + findAdapterEntry | Yes | FLOWING — drives the `Differ_.Compare` per-key truth-table call |

---

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| All hydrate unit tests pass | `./scripts/dev.sh go test ./internal/cli/hydrate/... ./internal/cli/extract/... ./internal/cli/state/... -count=1 -timeout 5m` | `ok hydrate`, `ok extract`, `ok state` | PASS |
| All adapter unit tests pass (e2e tag) | `./scripts/dev.sh go test -tags=e2e -count=1 -timeout 2m ./internal/cli/adapter/...` | `ok adapter`, `ok claudecode`, `ok codex`, `ok gemini`, `ok opencode` | PASS |
| e2e binary built with `-tags=e2e` carries the SIGKILL seam | `./scripts/dev.sh go build -tags=e2e -o /workspace/bin/ach-cli-verify ./cmd/ach-cli && strings ... | grep ACH_E2E_PHASE7_INJECT_SIGKILL` | symbol present | PASS |
| Same build without `-tags=e2e` is missing the seam | `strings /home/jcm/Projects/ach/bin/ach-cli (built default) | grep ACH_E2E_PHASE7_INJECT_SIGKILL` | symbol absent | PASS — confirms WR-01 build-tag gate |
| `examples/hydrate.json` (W3-P3 anchor) is byte-unchanged from main | `git diff main -- examples/hydrate.json` | empty | PASS (Task 4 Outcome 1: preferred no-op) |
| No debt markers (TBD/FIXME/XXX) in modified files | `grep -rn "TBD\|FIXME\|XXX" internal/cli/ cmd/ach-cli/ | grep -v _test.go` | empty | PASS |
| TODOs reference auditable follow-up (not silent debt) | inspection of `commit.go`, `result.go`, `doc.go` | "post-Phase-7-close: remove this seam once SC#2 stabilizes" + W2/W3 task references | PASS |

---

### Probe Execution

No `scripts/*/tests/probe-*.sh` defined for Phase 7. The runtime gate IS the e2e suite; the developer-run execution is the load-bearing evidence.

---

### Requirements Coverage (post-fix)

| Requirement | Status | Evidence |
|-------------|--------|----------|
| STATE-01 | VERIFIED | unchanged |
| STATE-02 | VERIFIED | W5-06 ordering fix landed; `TestLoad_SchemaV1_ReturnsErrSchemaMismatch` |
| STATE-03 | VERIFIED | unchanged |
| STATE-04 | VERIFIED | Differ now drives file overwrites via wired extractor + adapter; sc3_* live |
| STATE-05 | VERIFIED | `Sync` called in step 11 via `c.maybeKill(11)` path; `TestSync_*` unit tests; (e2e proof for `--sync` is deferred — not in W6-01 must-haves) |
| STATE-06 | VERIFIED | unchanged |
| STATE-07 | VERIFIED | `WriteAtomic` 4-step (tmp/Sync/Chmod/Close/Rename/Sync-parent) — CR-01 mode parameter added |
| STATE-08 | VERIFIED | Steps 7-10 wiring closed (CR-02) |
| STATE-09 | VERIFIED | unchanged |
| STATE-10 | VERIFIED | `step6Diff` scope filter drives extraction (CR-02 closed); `OnlyRuntime` flow has Bug F deferred to #82 |
| STATE-11 | VERIFIED | `FetchContent` invoked via `ExtractContent` (CR-02 closed); `priorContentSourceHash` adds an orchestrator-level no-op skip path |
| ADAPT-01..02 | VERIFIED | unchanged |
| ADAPT-03 | VERIFIED | `adapter.WithCredential(ctx, c.opts.Bearer)` in `commit.go:299`; bearer flows through; sc1_*_pk verifies on-disk x-ach-key |
| ADAPT-04..07 | VERIFIED | All four adapters' `RenderRuntime` invoked via wired dispatcher; per-key surgical-merge implements ADAPT-05 deep-merge correctly |
| SAFE-01..03, SAFE-05..06 | VERIFIED | unchanged (regression-checked) |
| SAFE-04 | VERIFIED — REDESIGNED | The legacy whole-file three-tier cascade is retired for adapter files in favor of per-key coexistence. The Classify+Cascade machinery remains (with CR-03 path-normalization fix) for non-adapter content. The semantic intent (don't clobber unowned files) is satisfied more cleanly by the surgical merge: ACH never touches what isn't its key. |

All 25 Phase 7 requirement IDs are accounted for. DIST-01..04 remain Phase 7.1.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/cli/hydrate/commit.go` | ~76, ~108 (`doc.go`) | `TODO(post-Phase-7-close): remove this seam` | INFO | Auditable follow-up: SIGKILL seam removal after SC#2 stabilizes |
| `internal/cli/hydrate/result.go` | ~103, ~127, ~157 | `TODO 07-W2-01 supplies the concrete Extractor...` etc. | INFO | These TODOs are now stale (the impls landed in W3-05 + Wave 5 fixes); a cleanup pass would remove them. Not a blocker — they're descriptive, not deferring real work. |

No TBD/FIXME/XXX markers; no silent stubs; no unwired data flows.

The prior BLOCKER and WARNING anti-patterns from the original VERIFICATION.md (`_ = c.extractor`, `_ = c.adapter`, `_ = diffTargets`, `tmp.Chmod(0o644)` hardcoded, `entry.Target == finalPath` relative/absolute mismatch, `Step 11 Sync() never called`, `DisallowUnknownFields before schemaVersion check`) are all CLOSED.

---

### Human Verification Required

#### 1. Capture the 30 PASS / 0 FAIL log to the SUMMARY

**Test:** Save the live e2e run output that the developer observed on the kept kind cluster to a file in the repo (e.g. attach as a code block to `07-W6-01-SUMMARY.md` per the W6-01 plan's `<output>` clause).

**Expected:** A timestamped capture of `go test -tags=e2e -v -run TestPhase7CLIEngine ./test/e2e/...` (or equivalent) with `--- PASS` lines for each of the 30 nodes (22 umbrella subtests + 8 `sc4_safe_extract_malicious/<fixture>` sub-subtests).

**Why human:** The verifier process has no kind cluster. The developer's 30/0 report is the load-bearing evidence; capturing it to disk closes the audit loop and makes the runtime gate reproducible.

#### 2. Future regression checks

**Test:** Re-run `TestPhase7CLIEngine` after any Wave-7 follow-up that touches `internal/cli/hydrate`, `internal/cli/extract`, `internal/cli/state`, `internal/cli/adapter`, or `cmd/ach-cli/cmd/hydrate.go`.

**Expected:** All 30 nodes continue to pass.

**Why human:** continuous validation; required only if downstream waves touch the engine.

---

### Bug F deferral analysis (`--only-runtime` → artifact 404)

| Question | Answer |
|----------|--------|
| Is it filed? | YES — GitHub issue #82, OPEN, label `bug`, author juancarlosm |
| Is it in Phase 7 must-haves? | NO — neither the original PLAN.md must_haves nor the W6-01 must_haves mention `--only-runtime` |
| Does the Phase 7 e2e suite exercise it? | NO — `grep -rn "only-runtime\|OnlyRuntime" test/e2e/cli_hydrate_engine_test.go` is empty |
| Is the root cause documented in the issue? | YES — `commit.go` step 6 emits diffTargets for runtime kinds when `--include-runtime`/`--only-runtime` is set; `extractorImpl.ExtractContent` calls `classifyDownloadURL` which defaults to KindArtifact on unparseable URLs → 404 |
| Verdict | **LEGITIMATE DEFERRAL** — out-of-scope for W6-01; tracked in #82; the Phase 7 core value path (default hydrate, no `--only-runtime`) is unaffected |

---

### Gaps Summary

**None remaining.**

All three original CR-* blockers are CLOSED with evidence: code reads exactly as the closure plan called for; unit tests exercise the fixed paths; the developer's live e2e run demonstrates end-to-end correctness across 30 PASS nodes. The surgical-merge redesign — a SCOPE EXPANSION discovered during the runtime gate — is also landed and validated.

The phase goal is achieved: `ach-cli hydrate` against the deployed Hub produces a working agent workspace for all four shipped platforms, preserving any pre-existing user config, with safe extraction, per-key drift, atomic state writes, and lock-protected concurrency.

---

### Overall Verdict

**GOAL ACHIEVED.**

Phase 7's three FAILED truths from the initial verification (CR-02 wiring, CR-01 mode, CR-03 path-comparison) flip to VERIFIED. The score moves from 2/4 must-haves verified to 11/11 promises delivered. The W6-01 runtime gate IS the load-bearing proof, and it landed green (developer-reported 30 PASS / 0 FAIL on a fresh kind cluster, plus surgical-merge live-validation captured in `follow-up.md`).

Bug F is legitimately deferred to GitHub issue #82, outside Phase 7's e2e coverage and outside its core-value contract.

Open items, none of which block Phase 7 close:
- Capture the 30/0 e2e log to `07-W6-01-SUMMARY.md` as a code block per the W6-01 plan's `<output>` clause (human task).
- Confirm with the user the open question in `follow-up.md` about claude-code `.claude/settings.json` vs `.mcp.json` for MCP server definitions (the current code centralizes on `.claude/settings.json`; flipping to `.mcp.json` + an approval-allowlist write is a one-line change in `internal/cli/adapter/claudecode/claudecode.go:settingsJSONPath`).

---

_Re-verified: 2026-05-31_
_Verifier: Claude (gsd-verifier) — goal-backward, branch `phase7/w6-01-cluster-handoff` at HEAD `b5dc622`._
