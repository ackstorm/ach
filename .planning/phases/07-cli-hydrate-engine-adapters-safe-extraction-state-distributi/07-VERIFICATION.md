---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
verified: 2026-05-29T00:00:00Z
status: gaps_found
score: 2/4 must-haves verified
overrides_applied: 0
re_verification: null
gaps:
  - truth: "ach-cli hydrate produces a working configuration for each of the four platforms — adapter dispatch runs and populates platform-specific runtime-config files"
    status: failed
    reason: "Steps 7-10 of the 14-step commit sequence are permanently dead. (1) cmd/ach-cli/cmd/hydrate.go:502 discards both Extractor and AdapterDispatcher from NewWiring via `_, _ = hydrate.NewWiring(...)`. (2) commit.go lines 240-253 show that even if c.extractor/c.adapter were non-nil, the only code executed is `_ = c.extractor` / `_ = c.adapter` — neither ExtractContent() nor Render() is ever called. Opts has no Extractor/AdapterDispatcher fields. The engine cannot write any content or adapter config files in production."
    artifacts:
      - path: "cmd/ach-cli/cmd/hydrate.go"
        issue: "Line 502: `_, _ = hydrate.NewWiring(...)` — both return values discarded. The constructed Extractor and AdapterDispatcher never reach the commit struct."
      - path: "internal/cli/hydrate/commit.go"
        issue: "Lines 240-253: `if c.extractor != nil { _ = c.extractor }` and `if c.adapter != nil { _ = c.adapter }` — neither ExtractContent() nor Render() is invoked. These are W1 stubs with TODO markers referencing W2/W3 wiring that was never completed."
      - path: "internal/cli/hydrate/flags.go"
        issue: "Opts struct has no Extractor or AdapterDispatcher fields — there is no mechanism to inject concrete impls from outside the hydrate package."
    missing:
      - "Add Extractor and AdapterDispatcher fields to hydrate.Opts (or an equivalent injection mechanism)."
      - "Populate these fields in newCommit() from opts."
      - "Replace the `_ = c.extractor` / `_ = c.adapter` stubs in commit.go run() with actual calls to c.extractor.ExtractContent() (per-diffTarget) and c.adapter.Render() after extraction."
      - "Wire diffTargets (step 6 output, currently `_ = diffTargets`) into the step 7 extraction loop."
      - "Populate WrittenFiles / DroppedComponents in Result from extraction and adapter dispatch results."

  - truth: "Credential-bearing adapter config files written at safe permissions (0o600)"
    status: failed
    reason: "state.WriteAtomic unconditionally hardcodes 0o644 (atomic.go:62). The code comment at line 59 explicitly states 'state.json has no secrets — 0644 is the right mode' — but WriteAtomic is also the write path for adapter runtime-config files (.claude/.mcp.json, .codex/config.toml, .gemini/settings.json, .opencode/opencode.json) via adapterDispatcherImpl.Render (wiring.go:230). These files embed plaintext x-ach-key bearer credentials in their headers maps. On any multi-user host, other users can read these files and extract bearer credentials."
    artifacts:
      - path: "internal/cli/state/atomic.go"
        issue: "Line 62: `tmp.Chmod(0o644)` — hardcoded for all WriteAtomic callers. No mode parameter."
      - path: "internal/cli/hydrate/wiring.go"
        issue: "Line 230: `state.WriteAtomic(finalAbs, fw.Content)` — writes credential-bearing adapter config files at 0o644 with no per-caller mode control."
    missing:
      - "Add an os.FileMode parameter to WriteAtomic (or a WriteAtomicWithMode variant)."
      - "Pass 0o600 for credential-bearing files in adapterDispatcherImpl.Render."
      - "Keep 0o644 only for state.json (state.Save caller)."

  - truth: "Auto-claim collision policy correctly identifies adapter-written files as owned on re-hydrate (CollisionOwnedByCurrent)"
    status: failed
    reason: "extract.Classify(finalPath, stateFile) compares entry.Target == finalPath where finalPath is an absolute path (e.g. /workspace/.ach/.claude/.mcp.json) but state.FileEntry.Target is stored workspace-relative (e.g. .claude/.mcp.json). The values never compare equal, so CollisionOwnedByCurrent is never returned for adapter files. Every re-hydrate enters CollisionExistsUnowned, runs Cascade, and — when credentials change between runs — exits with code 7 (CollisionRefuse), refusing to update a file the engine itself wrote."
    artifacts:
      - path: "internal/cli/extract/autoclaim.go"
        issue: "Line 131: `entry.Target == finalPath` — workspace-relative Target compared against absolute finalPath. Paths are structurally incompatible; the equality check always fails for adapter-written files."
    missing:
      - "Normalize both sides of the comparison before comparing: either store absolute paths in FileEntry.Target at write time, or normalize entry.Target to absolute (using achDir as the base) within Classify."
      - "Add a unit test that writes a state.json with a relative Target, calls Classify with the corresponding absolute finalPath, and asserts CollisionOwnedByCurrent is returned."

deferred: []
human_verification:
  - test: "Run `make e2e-full` (or `make cluster-keep` + `make e2e-focus FOCUS=TestPhase7CLIEngine`) against a live kind+Helm cluster with ACH_E2E_PHASE7=1 set after all three gaps above are closed."
    expected: "All 14 subtests pass: 8 sc1_* platform×credential subtests produce actual workspace files (adapter configs + state.json with content entries), sc2 SIGKILL recovery leaves prior state intact, sc3 drift four-outcome table fires the correct exit codes, sc4 safe-extract / bomb / autoclaim subtests pass."
    why_human: "The kind cluster is not running in this session. The e2e test file uses //go:build e2e and requires ACH_E2E_PHASE7_PK + a live Platform API + a live Content Service. All three blockers (CR-02, CR-01, CR-03) must be fixed before the runtime gate can pass."
---

# Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State + Distribution — Verification Report

**Phase Goal:** The Core Value path works end-to-end: `ach-cli hydrate --environment <env>` against a deployed Hub for any of the four shipped platforms (Claude Code, Codex, Gemini CLI, OpenCode) produces a working AI-agent workspace with safe-extracted content, dual-hash drift detection, atomic state v2 writes, and lock-protected concurrency.

**Verified:** 2026-05-29
**Status:** gaps_found
**Re-verification:** No — initial verification

Note: DIST-01..04 are explicitly carved out to Phase 7.1 by plan 07-W1-01 and are not evaluated here. The verified scope is STATE-01..11, ADAPT-01..07, SAFE-01..06.

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `ach-cli hydrate` produces working platform configs for all four adapters | FAILED | Steps 7-10 in commit.go are stubs; ExtractContent() and Render() are never called. CR-02. |
| 2 | Commit sequence runs under advisory flock lock with atomic state v2 write last | VERIFIED | Steps 1-6 and 12-13 are fully implemented. Lock (lock.go), state (state.go), WriteAtomic (atomic.go) all exist and are wired. DryRun gate respected. |
| 3 | Drift detection yields the four §8.4 outcomes; --force overrides; schema mismatches abort correctly | VERIFIED | Differ_ in drift.go implements the four-outcome truth table. step6Diff in commit.go applies scope filter and populates diffTargets. ErrSchemaMismatch exits 5; ErrEnvironmentMismatch exits 4. step3ReadState wires both. NOTE: a v1 state.json with unknown fields returns ErrStateParse (exit 1) rather than ErrSchemaMismatch (exit 5) due to DisallowUnknownFields firing before schemaVersion check (WR-03, warning). |
| 4 | Safe extraction rejects malicious archives unconditionally; modes masked; bomb caps enforced | PARTIALLY VERIFIED | tar.go + stage.go + limits.go implement SAFE-01..06 correctly (substantive ~947 lines of extraction code). However: (a) CR-01 means adapter config files are written at 0o644 (world-readable credentials); (b) CR-03 means collision-owned detection fails for re-hydration. The extraction engine itself is correct but two safety properties fail. |

**Score:** 2/4 truths fully verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/cli/hydrate/commit.go` | 14-step commit sequence orchestrator | STUB (steps 7-10) | Steps 1-6 and 12-13 implemented; steps 7-10 are `_ = c.extractor` / `_ = c.adapter` no-ops with TODO markers. |
| `internal/cli/hydrate/wiring.go` | Default Extractor + AdapterDispatcher implementations | ORPHANED | NewWiring(), extractorImpl, adapterDispatcherImpl, Sync() all exist and are substantive (684 lines). BUT the cobra layer discards the result and the commit struct never receives them. |
| `internal/cli/hydrate/flags.go` | Opts with Extractor/AdapterDispatcher injection fields | MISSING WIRING | Opts struct exists but has no Extractor/AdapterDispatcher fields. There is no field on the struct through which impls can be injected. |
| `cmd/ach-cli/cmd/hydrate.go` | runHydrateEngine wiring NewWiring → hydrate.Run | STUB | Line 502: `_, _ = hydrate.NewWiring(...)`. Wiring constructed then immediately discarded. |
| `internal/cli/extract/tar.go` | Safe tar extraction with SAFE-01..06 policy | VERIFIED | 378 lines; all rejection types implemented (abs path, `..`, symlinks, hardlinks, devices, FIFOs, PAX injection, bomb caps). |
| `internal/cli/extract/stage.go` | Per-resource staging + atomic publication | VERIFIED | 420 lines; StageAndPublish implements streaming spill + sha256 short-circuit + per-resource atomic rename. |
| `internal/cli/extract/limits.go` | Bomb-defense caps (SAFE-03) | VERIFIED | ACH_MAX_EXTRACTED_PLUGIN_MIB, ACH_MAX_EXTRACTED_ARTIFACT_MIB, ACH_MAX_ARCHIVE_ENTRIES enforced. |
| `internal/cli/extract/autoclaim.go` | Auto-claim collision policy (SAFE-04) | STUB | Classify() has path-comparison mismatch (CR-03): entry.Target (relative) vs finalPath (absolute) never equal. CollisionOwnedByCurrent unreachable for adapter files on re-hydrate. |
| `internal/cli/state/atomic.go` | WriteAtomic with secure file permissions | STUB | Exists and implements atomic write correctly. BUT hardcodes 0o644 (CR-01) — credential-bearing adapter config files written world-readable. |
| `internal/cli/state/state.go` | State schema v2 Load/Save | VERIFIED | Load/Save/WriteAtomic implemented. DisallowUnknownFields + schemaVersion=="2" check present. NOTE: WR-03 — DisallowUnknownFields fires before schemaVersion check for v1 files. |
| `internal/cli/lock/lock_unix.go` | POSIX flock LOCK_EX implementation | VERIFIED | Acquire/Release with AcquireFailFast/AcquireWait/AcquireWithTimeout modes. ErrLockContended and ErrLockTimeout sentinels. |
| `internal/cli/hash/xxh3.go` | xxh3 hash wrapper | VERIFIED | Hash(io.Reader) and HashBytes([]byte) returning "xxh3:<32hex>" format. |
| `internal/cli/manifest/manifest.go` | Manifest decoder + schemaVersion assertion | VERIFIED | ErrSchemaMismatch on non-"v1alpha1", missing runtime/context blocks. Fetch(ctx, client, env) implemented. |
| `internal/cli/adapter/claudecode/claudecode.go` | Claude Code adapter | VERIFIED (code); ORPHANED (wiring) | RenderRuntime(), TransformPlugin(), ResolveOutputContent() all implemented (366 lines). Never called in production due to CR-02. |
| `internal/cli/adapter/codex/codex.go` | Codex adapter | VERIFIED (code); ORPHANED (wiring) | All three methods implemented (724 lines). Never called in production due to CR-02. |
| `internal/cli/adapter/gemini/gemini.go` | Gemini CLI adapter | VERIFIED (code); ORPHANED (wiring) | All three methods implemented (592 lines). Never called in production due to CR-02. |
| `internal/cli/adapter/opencode/opencode.go` | OpenCode adapter | VERIFIED (code); ORPHANED (wiring) | All three methods implemented (452 lines). Never called in production due to CR-02. |
| `internal/cli/hydrate/drift.go` | §8.4 four-outcome drift truth table | VERIFIED | Differ_/NewDiffer() implements NoOp/UpstreamOnly/LocalOnly/Conflict outcomes. ShouldExit2/WrapDriftError implemented. |
| `internal/cli/hydrate/autodetect.go` | Platform autodetection (ADAPT-02) | VERIFIED | Autodetect() and ResolvePlatform() implemented. |
| `internal/cli/adapter/registry.go` | Adapter registry + Lookup | VERIFIED | Register/Lookup with alias resolution. WR-05 (O(n) scan) is a warning, not a blocker. |
| `cmd/ach-cli/cmd/adapters_register.go` | Blank-import adapter registration | VERIFIED | All four adapters blank-imported for init() side-effects. |
| `internal/cli/exit/exit.go` | Phase 7 exit constants | VERIFIED | Drift=2, EnvironmentMismatch=4, SchemaMismatch=5, CollisionRefuse=7 all declared. |
| `test/e2e/cli_hydrate_engine_test.go` | Phase 7 e2e suite | COMPILED; RUNTIME UNVERIFIED | 1,168 lines; //go:build e2e; TestPhase7CLIEngine umbrella with 14 subtests. ACH_E2E_PHASE7 guard. Files compile but cluster not running — runtime pass not verified. |
| `test/e2e/phase7_helpers_test.go` | Phase 7 e2e helpers | COMPILED; RUNTIME UNVERIFIED | phase7SuiteGuard, phase7RunAchCli, phase7SeedXdgConfig, etc. Compiles clean. |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `cmd/ach-cli/cmd/hydrate.go:runHydrateEngine` | `internal/cli/hydrate/commit.c.extractor` | `hydrate.Opts.Extractor` field | NOT_WIRED | Opts has no Extractor field. `_, _ = NewWiring(...)` discards both impls at line 502. |
| `cmd/ach-cli/cmd/hydrate.go:runHydrateEngine` | `internal/cli/hydrate/commit.c.adapter` | `hydrate.Opts.AdapterDispatcher` field | NOT_WIRED | Same as above. AdapterDispatcher never reaches commit. |
| `internal/cli/hydrate/commit.go:run()` | `internal/cli/hydrate/wiring.go:extractorImpl.ExtractContent()` | `c.extractor.ExtractContent()` call | NOT_WIRED | Lines 240-244: `if c.extractor != nil { _ = c.extractor }` — no method call. |
| `internal/cli/hydrate/commit.go:run()` | `internal/cli/hydrate/wiring.go:adapterDispatcherImpl.Render()` | `c.adapter.Render()` call | NOT_WIRED | Lines 249-253: `if c.adapter != nil { _ = c.adapter }` — no method call. |
| `internal/cli/hydrate/commit.go:step6Diff()` | steps 7-10 extractor calls | `diffTargets` slice | NOT_WIRED | `_ = diffTargets // W1: not consumed further; W2 wires.` at line 230. |
| `internal/cli/hydrate/wiring.go:adapterDispatcherImpl.Render()` | `internal/cli/state/atomic.go:WriteAtomic` | file permission parameter | PARTIAL | Call exists at line 230 but uses 0o644 for credential-bearing files (CR-01). |
| `internal/cli/extract/autoclaim.go:Classify()` | state.FileEntry.Target | path equality check | BROKEN | Absolute vs relative comparison — CollisionOwnedByCurrent unreachable (CR-03). |
| `internal/cli/hydrate/commit.go:step1Lock()` | `internal/cli/lock/lock_unix.go:Acquire()` | `c.locker.Acquire(ctx, mode, timeout)` | WIRED | Lock properly acquired with mode dispatch (FailFast/Wait/WithTimeout). |
| `internal/cli/hydrate/commit.go:step12WriteState()` | `internal/cli/state/state.go:Save()` | `c.stateStore.Save()` | WIRED | Atomic state write is wired and conditional on DryRun. |
| `internal/cli/hydrate/commit.go:step5Manifest()` | `internal/cli/manifest/manifest.go:Fetch()` | `c.fetcher(ctx, environment)` | WIRED | manifestFetcher closure over manifest.Fetch is constructed in newCommit(). |

---

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `commit.go:run()` | `diffTargets` from step6Diff | manifest.Context/Runtime | Yes (real manifest parsed from Hub) | DISCONNECTED — `_ = diffTargets`, never passed to extraction |
| `commit.go:run()` | `existingState` | state.Load | Yes (reads real state.json) | WIRED for steps 3-4, 12; DISCONNECTED for steps 7-10 |
| `wiring.go:adapterDispatcherImpl.Render()` | `fws` from ad.RenderRuntime | manifest + adapter logic | Yes (real adapter output) | HOLLOW_PROP — Render() is substantive but never called |

---

### Behavioral Spot-Checks

Step 7b skipped: the kind cluster is not running. Probing `ach-cli hydrate` against a live Hub is required for runtime verification but not possible in this session.

---

### Probe Execution

Step 7c skipped: no `scripts/*/tests/probe-*.sh` files declared for Phase 7. Runtime e2e gate requires the kind cluster.

---

### Requirements Coverage

Phase 7 owns STATE-01..11, ADAPT-01..07, SAFE-01..06 (DIST-01..04 deferred to Phase 7.1).

| Requirement | Description (abbreviated) | Status | Evidence |
|-------------|---------------------------|--------|----------|
| STATE-01 | State file at `<ach-dir>/state.json`; ResolvePath | VERIFIED | state/path.go:ResolvePath(); commit.go step 0/1 |
| STATE-02 | State schema v2 with xxh3 hashes | VERIFIED | state/state.go File struct, hash/xxh3.go |
| STATE-03 | Same-ach-dir different-Environment guard → exit 4 | VERIFIED | state/guard.go:GuardEnvironment(), commit.go step3; exit.EnvironmentMismatch=4 |
| STATE-04 | Drift detection four-outcome truth table | VERIFIED (code); NOT EXERCISED IN PRODUCTION | drift.go:Differ_.Compare(); step6Diff populates targets; but targets are discarded — drift outcomes never drive file overwrites |
| STATE-05 | --sync deepest-first inverse-merge deletion | VERIFIED (code); NOT WIRED | wiring.go:Sync() substantive (200+ lines); commit.go step 11 is `c.maybeKill(11)` with no Sync() call |
| STATE-06 | flock LOCK_EX advisory lock | VERIFIED | lock_unix.go:Acquire()/Release(); commit.go:step1Lock() |
| STATE-07 | Atomic state write: tmp→fsync→rename→fsync(parent) | VERIFIED | state/atomic.go:WriteAtomic(); wired via state.Save() in step12 |
| STATE-08 | §6.7 14-step commit sequence end-to-end | PARTIALLY VERIFIED | Steps 1-6, 12-13 wired; steps 7-11 are no-ops (W1 stubs never replaced) |
| STATE-09 | Manifest schemaVersion=="v1alpha1" abort → exit 5 | VERIFIED | manifest.go:ErrSchemaMismatch; commit.go:step5Manifest() maps to exit.SchemaMismatch |
| STATE-10 | --include-runtime / --only-runtime scope filter | VERIFIED (code only) | step6Diff() correctly filters; but diffTargets is discarded so filter has no observable effect |
| STATE-11 | Unconditional fetch; sha256 short-circuit on disk write | VERIFIED (code only) | extract/fetch.go:FetchContent() + stage.go:StageAndPublish() implement this; but FetchContent() is never called (CR-02) |
| ADAPT-01 | Four adapters compiled in; detection, aliases, registry | VERIFIED | All four init() register calls; adapters_register.go blank-imports; Lookup() functional |
| ADAPT-02 | Platform autodetection on zero/one/multi match | VERIFIED | hydrate/autodetect.go:Autodetect() + ResolvePlatform() |
| ADAPT-03 | Runtime rendered into platform-native config | FAILED | RenderRuntime() exists in all four adapters but is never called (CR-02) |
| ADAPT-04 | Plugin canonical wire format + per-adapter distribution | FAILED | TransformPlugin() exists in all four adapters but is never called (CR-02) |
| ADAPT-05 | Merge strategies: deep/composite/replace | FAILED | Merge logic coded in adapters; never invoked (CR-02) |
| ADAPT-06 | Adapter scope rule (.claude/, .codex/, etc.) | FAILED | Scope logic coded in adapters; never invoked (CR-02) |
| ADAPT-07 | Silently drop untranslatable components; record in Dropped | FAILED | Drop accumulation coded in adapters; never invoked (CR-02) |
| SAFE-01 | Reject abs paths, `..`, symlinks, hardlinks, devices, FIFOs, PAX injection | VERIFIED | extract/tar.go:Extract() implements all rejections |
| SAFE-02 | Mode masked to 0755; setuid/setgid/sticky/group-write/world-write stripped | VERIFIED | extract/tar.go mode masking logic present |
| SAFE-03 | Bomb caps enforced per-resource; partial output discarded | VERIFIED | extract/limits.go + tar.go ErrBombCapExceeded; stage.go staging dir removed on error |
| SAFE-04 | Auto-claim collision policy | FAILED | autoclaim.Classify() has path-comparison mismatch (CR-03); CollisionOwnedByCurrent unreachable for adapter files |
| SAFE-05 | Per-resource atomic publication via rename(2) | VERIFIED | extract/stage.go:StageAndPublish() uses os.Rename for final publication |
| SAFE-06 | Streaming gzip; never fully buffered | VERIFIED | extract/tar.go + stage.go stream via io.Copy; source.bin spilled to disk |

**Requirements closure check:** All 25 Phase 7 requirement IDs (STATE-01..11, ADAPT-01..07, SAFE-01..06) are accounted for. DIST-01..04 are correctly assigned to Phase 7.1 and are not evaluated.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/cli/hydrate/commit.go` | 241-244 | `_ = c.extractor` W1 stub with TODO | BLOCKER | Steps 7-10 permanently no-op; content fetch/extraction/adapter dispatch never execute |
| `internal/cli/hydrate/commit.go` | 249-253 | `_ = c.adapter` W1 stub with TODO | BLOCKER | Adapter dispatch never executes |
| `cmd/ach-cli/cmd/hydrate.go` | 502 | `_, _ = hydrate.NewWiring(...)` — both returns discarded | BLOCKER | Extractor + AdapterDispatcher permanently nil in production |
| `internal/cli/hydrate/commit.go` | 230 | `_ = diffTargets // W1: not consumed` | BLOCKER | step6Diff output (scope-filtered content targets) never drives extraction |
| `internal/cli/state/atomic.go` | 62 | `tmp.Chmod(0o644)` hardcoded | BLOCKER | Credential-bearing adapter config files world-readable on multi-user hosts |
| `internal/cli/extract/autoclaim.go` | 131 | `entry.Target == finalPath` (relative vs absolute) | BLOCKER | CollisionOwnedByCurrent never fires for re-hydrate of adapter files |
| `internal/cli/hydrate/commit.go` | 178-182 | `ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP` read in production | WARNING | SIGKILL injection seam active in release binary; any user can crash mid-hydrate |
| `internal/cli/state/state.go` | 103-114 | `DisallowUnknownFields` before schemaVersion check | WARNING | v1 state.json returns exit 1 (ErrStateParse) instead of exit 5 (ErrSchemaMismatch); `--force` bypass does not work |
| All four adapters | ~327, ~457, ~547, ~408 | `defer func() { _ = out.Close() }()` swallowing close error | WARNING | Last-flush I/O errors on credential-bearing files silently lost |
| `internal/cli/hydrate/commit.go` | 256 | Step 11 Sync() never called | WARNING | `--sync` flag accepted but has no effect; inverse-merge deletion never runs |

**Debt marker gate:** The TODO markers at `commit.go` lines 241 and 250 are unresolved and do not reference formal issue/PR numbers. They reference W2/W3 wave labels from the planning corpus, not auditable follow-up work. These are BLOCKERS under the debt marker gate rule.

---

### Human Verification Required

#### 1. Runtime E2E Validation (after gap closure)

**Test:** After fixing CR-02, CR-01, CR-03: run `make cluster-keep` then `ACH_E2E_PHASE7=1 ACH_E2E_PHASE7_PK=<pk_> ACH_E2E_PHASE7_BASE_URL=http://localhost:8080 ./scripts/dev.sh make e2e-focus FOCUS=TestPhase7CLIEngine`

**Expected:** All 14 subtests in `TestPhase7CLIEngine` pass: 8 sc1_* subtests produce actual workspace files for all four platforms (`.claude/.mcp.json`, `.codex/config.toml`, `.gemini/settings.json`, `.opencode/opencode.json`) with valid bearer headers, sc2 SIGKILL recovery leaves prior state.json intact, sc3 drift subtests exit with the correct codes (0/0/2/2), sc4 rejects malicious archives and bomb tarballs with non-zero exit and no partial output.

**Why human:** Kind cluster not running in this session. All three critical blockers must be resolved before this test is meaningful. CR-02 means sc1_* will fail to produce any files even against a live Hub.

---

### Gaps Summary

Three blockers prevent Phase 7 goal achievement. They are related — CR-02 is the root cause that makes the engine ship no content, and CR-01/CR-03 are two other security/correctness properties that fail independently.

**Root cause (CR-02):** The W1-06 plan deliberately left steps 7-10 of the commit sequence as stubs with TODO markers pointing at W2/W3 for completion. The W3-05 plan created `wiring.go` with working `extractorImpl` and `adapterDispatcherImpl` concrete types but omitted the injection mechanism: (a) `hydrate.Opts` has no Extractor/AdapterDispatcher fields, (b) `cmd/ach-cli/cmd/hydrate.go` discards the constructed impls with `_, _ = ...`, and (c) `commit.go`'s run() method only executes `_ = c.extractor` / `_ = c.adapter` even if they were non-nil. The engine contains all the right code but it is never connected. An `ach-cli hydrate` run acquires the lock, reads/writes state.json, and fetches the manifest — but never downloads any content file and never writes any adapter config. The workspace receives a valid state.json but zero actual content.

**CR-01** (WriteAtomic at 0o644): Once CR-02 is fixed and adapter config files are actually written, they will contain plaintext `x-ach-key` bearer credentials accessible to all local users on multi-user hosts.

**CR-03** (Classify path mismatch): Once CR-02 is fixed, every re-hydrate of adapter-written files will enter the `CollisionExistsUnowned` path, run the three-tier cascade, and refuse with exit 7 when credentials change (which is the normal rotation case). The user is stuck at exit 7 with no recovery except `--force`.

The fix scope is narrow: add two fields to `hydrate.Opts`, populate them in `newCommit()`, replace the two stub blocks in `commit.go:run()` with real method calls, add a mode parameter to `WriteAtomic`, and normalize the path comparison in `Classify`. The adapters, extractor, and all supporting packages are production-quality and do not need changes.

---

_Verified: 2026-05-29_
_Verifier: Claude (gsd-verifier)_
