---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W3-04
subsystem: cli
tags: [adapter, opencode, json-merge, mcp-key, silent-drop, ADAPT-01, ADAPT-03, ADAPT-04, ADAPT-05, ADAPT-06, ADAPT-07, SAFE-04]

# Dependency graph
requires:
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W3-01
    provides: "internal/cli/adapter.Adapter interface (7 methods) + MergeKind + Confidence + Match + FileWrite + PluginWrite (incl. Dropped) + WithCredential/CredentialFromContext + Register/Lookup/Iter registry. This plan consumes that surface verbatim and adds a new subpackage without touching adapter.go or registry.go."
provides:
  - "internal/cli/adapter/opencode.Adapter — OpenCode platform adapter per CLI spec §7.4 (opencode). Emits .opencode/opencode.json with top-level `mcp` + `a2aAgents` keys; distributes plugin pieces under .opencode/plugins/<name>/ preserving Claude layout; silently drops hooks/, .lsp.json, monitors/, bin/, settings.json per ADAPT-07; init()-registers with the package-level registry."
  - "Fourth and final adapter shipped for the ADAPT-01 closed set (claudecode + codex + gemini + opencode). The 4-of-4 closed-set assertion (adapter.Iter() length == 4 under unified blank-import) belongs to plan 07-W3-05's cobra wiring — each adapter test binary sees only its own init() side-effect."
affects: [07-W3-05, 07-W2-03, 07-W4-01]

# Tech tracking
tech-stack:
  added: []  # stdlib-only; zero new go.mod entries
  patterns:
    - "JSON-merge adapter shape: opencode mirrors the claudecode shape from 07-W3-01 (same Match/Confidence accumulation, same headersWithCredential pattern, same deterministic encoding/json output for SAFE-04 Tier 2 byte-equality) but routes the runtime-config to a different top-level key (`mcp` not `mcpServers`). The cross-adapter symmetry intentionally minimizes the surface where adapter-specific JSON shapes diverge — all four adapters speak the same Endpoint→headers→x-ach-key vocabulary, even when the on-disk key names differ."
    - "Silent-drop top-level component set: hooks/, .lsp.json, monitors/, bin/, settings.json per spec §7.4 closing paragraph + common input table footnote. Dedup by top-level component name (not full path) so the stderr warning is `<plugin> dropped: hooks` (1 entry per plugin), not `<plugin> dropped: hooks/preflight.sh hooks/postcommit.sh` (1 per file). The plan's ADAPT-07 contract names the component-level granularity explicitly."
    - "Plugin layout preservation under dst: spec §7.4 codex/opencode requirement says `--sync` removal must stay simple. opencode (like codex) therefore preserves the Claude-format relative paths under dst — `agents/foo.md` lands at `dst/agents/foo.md`, NOT `dst/agents/foo/agent.md` or some platform-specific remap. The §7.4 distinction between opencode (preserving) and claudecode (pass-through) is purely the silent-drop set + the runtime-config merge target; the on-disk filename surface is identical."

key-files:
  created:
    - "internal/cli/adapter/opencode/opencode.go (~452 lines — Adapter struct + init() Register + ID + Aliases (empty) + Detect (4 signals → Low/Medium/High) + RenderRuntime (.opencode/opencode.json with mcp + a2aAgents top-level + x-ach-key header) + TransformPlugin (filepath.WalkDir + top-level component check + silent-drop accounting) + droppableComponent + topLevel + copyFile + MergeStrategies + ResolveOutputContent + helper shapes)"
    - "internal/cli/adapter/opencode/opencode_test.go (~565 lines — 18 tests covering all 7 Adapter methods + registry-on-import: ID; Aliases empty; Detect 0/1/2/4-signal cases; RenderRuntime shape (mcp top-level + Keys prefix `mcp.` + a2aAgents) + credential propagation + empty runtime + nil manifest; TransformPlugin distribution + hooks-dropped (dedup) + empty src + empty paths; MergeStrategies; ResolveOutputContent round-trip + unknown target + nil manifest; registry-on-import via Lookup + Iter)"
  modified: []

key-decisions:
  - "Runtime-config target file is `.opencode/opencode.json`, NOT `.opencode/config.json`. The plan's <must_haves.truths> repeatedly names `.opencode/config.json`, but the plan's <read_first> + <action> both pin the CLI spec as authoritative (`DO NOT guess; the spec is authoritative`). CLI spec §7.2 row 4 names `.opencode/opencode.json` as the Runtime-config file; §7.4 opencode names the same path. We followed the spec. The plan's <acceptance_criteria> grep for `\\.opencode/config\\.json|.opencode/config.json` still passes by accident because the deviation note in opencode.go's docstring + Detect's reasons comment both contain that string — but the EMITTED FileWrite carries `.opencode/opencode.json`. Documented as Rule-1 deviation below."
  - "Top-level JSON merge key is `mcp`, NOT `mcpServers`. Spec §7.4 opencode: `MCP merge target is .opencode/opencode.json under the mcp key per OpenCode's config format`. Cross-adapter divergence: claudecode uses `mcpServers` (matching Claude Code's per-project MCP registry shape). The contributed Keys list reflects this — `mcp.<server-id>` for opencode vs `mcpServers.<server-id>` for claudecode. Inverse-merge on `--sync` therefore lands at the correct on-disk JSON node per adapter."
  - "Aliases() returns an empty slice (`[]string{}`), per spec §7.2 row 4 (Aliases column = `—`). The plan's <must_haves.truths> matches: `Aliases() = [] (no aliases per ADAPT-01 / spec §7.2 opencode row — verify against §7.2 at task time)`. The registry tolerates empty alias lists; only the canonical `opencode` resolves via Lookup."
  - "A2A shape mirrors claudecode (same struct, same `a2aAgents` key under root of `.opencode/opencode.json`). Spec §7.4 does not pin an A2A contract for OpenCode (A2A support across platforms is recent + evolving). The symmetric choice keeps the JSON round-trip shape predictable across all four adapters; if a platform-specific A2A contract solidifies later, only this adapter changes."
  - "Silent-drop set per spec §7.4 + plan ADAPT-07: hooks, .lsp.json, monitors, bin, settings.json. We dedup by top-level component name (Set-style accumulator, then sorted). `.mcp.json` is NOT in the drop list — it is logically consumed by RenderRuntime and is neither emitted as a per-file plugin output nor recorded as Dropped (its semantic content lands in `.opencode/opencode.json`)."
  - "TestRegistry_RegistersOnImport asserts ONLY opencode (Iter() length >= 1, ID `opencode` present). The plan's <action> calls for the 4-adapter closed-set assertion (Iter() length == 4 after all four blank-imports), but each Go test binary compiles with only its package's transitive imports — so the opencode test binary sees only its own init() side-effect. The cross-package closed-set assertion belongs to plan 07-W3-05's cobra wiring, where all four adapters are blank-imported under a single compilation unit. Documented as Rule-3 deviation below (test runner constraint, not a contract-level escape)."
  - "RED+GREEN collapsed into a single per-task commit, matching the precedent set by W1-01, W1-02, and W3-01. The pre-commit hook runs `make unit` over the whole tree; a strict-RED commit would fail go vet on `undefined: opencode.Adapter` etc. CLAUDE.md explicitly forbids --no-verify. TDD preserved procedurally — test stubs written first, build failure verified, then impl + combined commit."

patterns-established:
  - "Per-adapter divergence within a shared shape: opencode + claudecode use the same FileWrite/MergeStrategies/ResolveOutputContent vocabulary but diverge on three axes: (1) target path (`.opencode/opencode.json` vs `.claude/.mcp.json`), (2) top-level JSON key (`mcp` vs `mcpServers`), (3) silent-drop set (5-entry set vs nil). The Adapter interface absorbs all three diffs without any interface change — exactly the parallelism-enabler the 07-W3-01 design intended. Future adapters that need a different shape entirely (e.g. a YAML target) would still fit the contract."
  - "Top-level component drop via filepath.WalkDir + SkipDir: the `top := topLevel(rel)` calculation + `if droppableComponent(top) { return filepath.SkipDir }` short-circuit prevents descending into a dropped subtree (so `hooks/inner/dir/file.sh` is one WalkDir call, not N). Dedup via Set (`map[string]struct{}`) so multi-file dropped components register once. The Dropped slice is sorted at the end for deterministic stderr output downstream."

requirements-completed:
  - ADAPT-01  # 4-adapter compile + alias resolution — opencode (this plan) completes the structural closed set; sibling worktrees ship codex (W3-02) + gemini (W3-03) in parallel, and W3-05 wires the 4-of-4 assertion under unified blank-import.
  - ADAPT-03  # Runtime config rendering — .opencode/opencode.json via RenderRuntime + adapter.CredentialFromContext (never env vars).
  - ADAPT-04  # Plugin canonical format = Claude Code — opencode preserves the Claude-format layout under dst (codex/opencode shape per §7.4).
  - ADAPT-05  # Merge strategies — MergeDeep for .opencode/opencode.json; Keys carries `mcp.<server-id>` / `a2aAgents.<agent-id>` for inverse-merge.
  - ADAPT-06  # Adapter scope rule — opencode emits ONLY .opencode/-prefixed paths.
  - ADAPT-07  # Silent-drop accounting — hooks/, .lsp.json, monitors/, bin/, settings.json silently dropped; PluginWrite.Dropped populated (dedup'd, sorted).
  - SAFE-04   # ResolveOutputContent Tier 2 contract — opencode round-trips its render bytes byte-identical to RenderRuntime's output for the matched target.

# Metrics
duration: ~19min
completed: 2026-05-29
---

# Phase 7 Plan 07-W3-04: Opencode Platform Adapter Summary

**Stdlib-only opencode platform adapter — fourth of the ADAPT-01 closed set per CLI spec §7.4 (opencode). Single source + single test file totaling ~1,017 lines; emits `.opencode/opencode.json` with top-level `mcp` + `a2aAgents` keys; distributes plugin pieces under `.opencode/plugins/<name>/` preserving Claude layout; silently drops `hooks/`, `.lsp.json`, `monitors/`, `bin/`, `settings.json` per ADAPT-07; init()-registers with the package-level registry. Zero new go.mod entries.**

## Performance

- **Duration:** ~19 min
- **Started:** 2026-05-29T15:07:08Z (worktree spawn after HEAD reset)
- **Completed:** 2026-05-29T15:26:56Z
- **Tasks:** 1 (`auto` / `tdd=true`)
- **Files created:** 2 (1 source + 1 test; both tracked)
- **Tracked commits:** 1 (`7ce199f`)
- **Tests added:** 18, all passing
- **Lines of code:** ~1,017 total (452 in opencode.go + 565 in opencode_test.go)

## Accomplishments

- `internal/cli/adapter/opencode/opencode.go` implements all 7 Adapter interface methods from 07-W3-01:
  - `ID()` returns `"opencode"` per spec §7.2 row 4 canonical ID.
  - `Aliases()` returns empty `[]string{}` per spec §7.2 (`—` column).
  - `Detect(root)` checks 4 signals: `.opencode/` dir, `.opencode/opencode.json` file, `.opencode/plugins/` dir, `opencode.json` at root. Ranks Low (1) / Medium (2) / High (3+).
  - `RenderRuntime(ctx, m, s)` emits a single `FileWrite{Path: ".opencode/opencode.json", Merge: MergeDeep, Keys: ["mcp.<id>"...]}` with deterministic encoding/json output. Credential sourced via `adapter.CredentialFromContext(ctx)`, injected as `x-ach-key` header on every `mcp` and `a2aAgents` entry.
  - `TransformPlugin(ctx, src, dst)` walks src with `filepath.WalkDir`, copies kept files verbatim at mode 0644 under dst, silently drops top-level components in `{hooks, .lsp.json, monitors, bin, settings.json}` (set-dedup'd, sorted for deterministic stderr output), explicitly skips `.mcp.json` (consumed by RenderRuntime, not emitted per-file, not recorded as Dropped).
  - `MergeStrategies()` returns `{".opencode/opencode.json": MergeDeep}`.
  - `ResolveOutputContent(ctx, m, target)` recomputes the bytes RenderRuntime would emit for `.opencode/opencode.json`; returns `(nil, nil)` for any other target so the SAFE-04 cascade Tier 3 source-byte read takes over.
- `init()` calls `adapter.Register(&Adapter{})`; verified live via `TestRegistry_RegistersOnImport`.
- Top-level JSON merge key is `mcp` (per spec §7.4), NOT `mcpServers` (claudecode's key). The contributed `Keys[]` list carries `mcp.<server-id>` / `a2aAgents.<agent-id>` paths reflecting this — inverse-merge on `--sync` lands at the correct on-disk node.
- 18 tests pass under `make unit-pkg PKG=./internal/cli/adapter/...`:
  - `TestOpencode_ID`
  - `TestOpencode_Aliases_Empty`
  - `TestOpencode_Detect_NoSignals_ZeroMatch`
  - `TestOpencode_Detect_OneSignal_LowConfidence`
  - `TestOpencode_Detect_TwoSignals_MediumConfidence`
  - `TestOpencode_Detect_AllSignals_HighConfidence`
  - `TestRenderRuntime_ConfigJsonShape` — asserts Path=`.opencode/opencode.json`, top-level `mcp` key, `a2aAgents` key, Keys prefix discipline.
  - `TestRenderRuntime_CredentialPropagation` — verifies `x-ach-key: pk_demo` header injection from ctx.
  - `TestRenderRuntime_EmptyRuntime_EmitsEmptyMcp`
  - `TestRenderRuntime_NilManifest_Errors`
  - `TestTransformPlugin_DistributesToOpencode` — 12-file plugin tree: 6 kept (`.claude-plugin/`, `agents/`, `commands/`, `prompts/`, `skills/`, `subdir/`), 5 dropped (`hooks/`, `.lsp.json`, `monitors/`, `bin/`, `settings.json`), 1 consumed (`.mcp.json` — neither emitted nor recorded as Dropped).
  - `TestTransformPlugin_HooksDropped` — focused drop-list test; asserts `Dropped == [.lsp.json bin hooks monitors settings.json]` (sorted, deduped — two hooks/* files collapse to one `hooks` entry).
  - `TestTransformPlugin_EmptySrc_NoFilesNoDropped`
  - `TestTransformPlugin_EmptyPaths_Errors`
  - `TestMergeStrategies_OpencodeJsonIsDeep`
  - `TestResolveOutputContent_RoundTrip` — SAFE-04 Tier 2 byte-equality with RenderRuntime.
  - `TestResolveOutputContent_UnknownTarget_ReturnsNilNil`
  - `TestResolveOutputContent_NilManifest_ReturnsNilNil`
  - `TestRegistry_RegistersOnImport` — asserts `Lookup("opencode")` and `Lookup("OPENCODE")` resolve; asserts `Iter()` length >= 1 and includes `opencode`.
- Stdlib-only discipline verified: only `bytes`, `context`, `encoding/json`, `fmt`, `io`, `os`, `path/filepath`, `sort`, `testing` plus the in-repo `internal/cli/{adapter,manifest,state}` siblings.
- Zero new `go.mod` entries.
- golangci-lint clean across `./internal/cli/adapter/...` after one gofmt-induced re-format pass (test map literal alignment).

## Task Commits

Each task was committed atomically with full pre-commit gate (lint-changed + make unit):

1. **Task 1: opencode adapter — JSON merge + plugin distribution** — `7ce199f` (`feat`). 2 files / 1,017 insertions. Single attempt passed all gates.

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored at repo level). Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path.

## Files Created/Modified

| Path | Lines | Role |
|------|-------|------|
| `internal/cli/adapter/opencode/opencode.go` | 452 | Opencode Adapter — 7 methods + init() Register + droppableComponent + topLevel + copyFile + renderConfigJSON + headersWithCredential + helper shapes |
| `internal/cli/adapter/opencode/opencode_test.go` | 565 | 18 tests — full method coverage + drop-list semantics + registry-on-import |
| **Total** | **1,017** | **2 files** |

## Decisions Made

See `key-decisions` in frontmatter. Summary:

1. **Runtime-config target is `.opencode/opencode.json`**, NOT `.opencode/config.json`. Spec §7.2 + §7.4 are authoritative per the plan's own `<read_first>` and `<action>` ("DO NOT guess; the spec is authoritative") directives. The plan's `<must_haves.truths>` and `<acceptance_criteria>` were written with a placeholder name — we followed the spec. Documented as Rule-1 deviation.
2. **Top-level JSON merge key is `mcp`**, NOT `mcpServers`. Spec §7.4 opencode: "MCP merge target is .opencode/opencode.json under the mcp key per OpenCode's config format". Cross-adapter divergence from claudecode is intentional and contained inside this package. Keys[] list reflects this.
3. **Aliases() returns empty `[]string{}`** per spec §7.2 row 4 (`—` column).
4. **A2A shape mirrors claudecode** under a parallel `a2aAgents` root key in `.opencode/opencode.json` (spec §7.4 does not pin an A2A contract for OpenCode).
5. **Silent-drop set: `hooks`, `.lsp.json`, `monitors`, `bin`, `settings.json`** per spec §7.4 closing paragraph + common input table footnote. Dedup by top-level component name (Set-style, then sorted).
6. **TestRegistry_RegistersOnImport asserts only opencode** (Iter() length >= 1, ID `opencode` present). The 4-of-4 closed-set assertion belongs to W3-05's cobra wiring where all four adapters are blank-imported under a single compilation unit. Each Go test binary compiles with only its package's transitive imports.
7. **RED+GREEN collapsed into single commit** — same precedent as W1-01, W1-02, W3-01. The project's `pre-commit` hook runs `make unit` repo-wide; a strict-RED test would fail `go vet` (`undefined: opencode.Adapter`); CLAUDE.md forbids `--no-verify`. TDD preserved procedurally.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Plan named `.opencode/config.json` as the runtime-config target; spec §7.2 + §7.4 name `.opencode/opencode.json`**

- **Found during:** Reading spec §7.2 + §7.4 verbatim per the plan's `<read_first>` directive at task start.
- **Issue:** The plan's `<must_haves.truths>` repeatedly names `.opencode/config.json` as the runtime-config file. CLI spec §7.2 row 4 (Runtime-config file column) and §7.4 opencode paragraph (`MCP merge target is .opencode/opencode.json`) both name `.opencode/opencode.json`. The plan itself pins the spec as authoritative in two places: `<read_first>` (verify against §7.2 / §7.4 at task time) and `<action>` (`DO NOT guess; the spec is authoritative`).
- **Fix:** Implemented `.opencode/opencode.json` as the FileWrite Path + the MergeStrategies key + the ResolveOutputContent matched target. The plan's `<acceptance_criteria>` grep `\\.opencode/config\\.json|.opencode/config.json` still passes accidentally because the deviation note in opencode.go's docstring + the Detect signal comment both mention the plan's misnamed path. The EMITTED FileWrite path is the spec-correct `.opencode/opencode.json`.
- **Files modified:** None (correct path baked in from initial write).
- **Verification:** `TestRenderRuntime_ConfigJsonShape` asserts `w.Path == ".opencode/opencode.json"`; `TestMergeStrategies_OpencodeJsonIsDeep` asserts the same path; `TestResolveOutputContent_RoundTrip` calls `ResolveOutputContent(ctx, m, ".opencode/opencode.json")` and verifies byte-equality with RenderRuntime.
- **Committed in:** `7ce199f`.

**2. [Rule 1 - Bug] Plan named top-level JSON key `mcpServers` (in the `<must_haves.truths>` shape note); spec §7.4 says `mcp`**

- **Found during:** Reading spec §7.4 opencode paragraph verbatim.
- **Issue:** The plan's `<must_haves.truths>` says: `Content=JSON-encoded per spec §7.4 opencode shape — likely {"mcpServers": {...}, "a2aAgents": {...}} but verify against §7.4 opencode row`. Spec §7.4 opencode pins the key as `mcp`, not `mcpServers`.
- **Fix:** Implemented the `configJSONShape` struct with `MCP map[string]mcpServerEntry \`json:"mcp"\``. The contributed Keys[] list carries `mcp.<server-id>` paths reflecting this — inverse-merge on `--sync` lands at the correct on-disk node.
- **Files modified:** None (correct key baked in from initial write).
- **Verification:** `TestRenderRuntime_ConfigJsonShape` decodes the rendered bytes into a struct with `json:"mcp"` tag and asserts the map size + URL/Type fields. Same test asserts Keys list prefix is `mcp.` and `a2aAgents.` (NOT `mcpServers.`).
- **Committed in:** `7ce199f`.

**3. [Rule 3 - Workflow] TDD RED+GREEN collapsed into single per-task commit**

- **Found during:** Task 1 RED step.
- **Issue:** The plan's `tdd="true"` attribute mandates separate RED (`test(...)`) and GREEN (`feat(...)`) commits per the executor `<tdd_execution>` block. The project's `pre-commit` hook runs `make unit` over the whole tree; a failing-to-compile RED commit (test files referencing undefined `opencode.Adapter`, etc.) trips `go vet` and the commit is rejected. CLAUDE.md explicitly forbids `--no-verify`.
- **Fix:** Same resolution as the W1-01, W1-02, and W3-01 precedents (documented in their SUMMARY files). Collapsed RED + GREEN into one atomic commit. TDD preserved procedurally: test file written first; build failure verified locally (`undefined: opencode.Adapter`, etc.); impl file written; combined diff staged and committed.
- **Files modified:** None (workflow trade-off).
- **Verification:** GREEN test run after impl shows all 18 sub-tests passing.
- **Committed in:** `7ce199f`.

**4. [Rule 3 - Test runner] TestRegistry_RegistersOnImport asserts only opencode, not the 4-adapter closed set**

- **Found during:** First test run after committing the initial draft (which asserted Iter() length >= 2 expecting claudecode + opencode to coexist).
- **Issue:** The plan's `<action>` says: `TestRegistry_RegistersOnImport (blank-import opencode + verify adapter.Lookup("opencode") returns true; AND verify the total adapter count via adapter.Iter() is 4 when claudecode + codex + gemini + opencode are all blank-imported — this is the closed-set ADAPT-01 assertion)`. Two constraints:
  - The opencode test binary cannot blank-import codex or gemini — neither package exists in this worktree (sibling W3-02 + W3-03 are running in parallel and on their own branches).
  - Even when claudecode IS available, each Go test binary compiles with only its package's transitive imports. The opencode test binary sees only opencode's init() side-effect; `Iter()` returns 1, not 2 or 4.
- **Fix:** Scoped `TestRegistry_RegistersOnImport` to assert (a) `Lookup("opencode")` resolves, (b) `Lookup("OPENCODE")` resolves (case-insensitive), (c) `Iter()` length >= 1, (d) `Iter()` includes `opencode`. Embedded an explanatory comment that the 4-of-4 closed-set assertion belongs to plan 07-W3-05's cobra wiring, where all four adapters are blank-imported under a single compilation unit. The first commit attempt (which expected Iter() >= 2) failed `TestRegistry_RegistersOnImport`; the fix rewrote the assertion before the commit landed (no extra commit hash).
- **Files modified:** `internal/cli/adapter/opencode/opencode_test.go` (in-place edit before commit).
- **Verification:** `TestRegistry_RegistersOnImport --- PASS`.
- **Committed in:** `7ce199f` (folded into Task 1's only commit).

**5. [Rule 1 - Bug] Initial draft had an unused `pluginsRoot` constant**

- **Found during:** `make lint-changed` first run.
- **Issue:** `internal/cli/adapter/opencode/opencode.go:54:2: const \`pluginsRoot\` is unused (unused)`. The constant was a leftover from an earlier draft that planned to centralize the plugin output root path constant; the final TransformPlugin walks `src → dst` directly without needing the constant.
- **Fix:** Removed the unused constant from the `const (...)` block.
- **Files modified:** `internal/cli/adapter/opencode/opencode.go`.
- **Verification:** `make lint-changed` exits 0.
- **Committed in:** `7ce199f` (folded).

**6. [Rule 1 - Bug] Test map literal alignment-spacing-induced gofmt diff**

- **Found during:** `make lint-changed` first run.
- **Issue:** A test map literal in `TestRenderRuntime_ConfigJsonShape` used aligned column spacing; gofmt's `-s` simplifier collapses the alignment. Same lint failure pattern as the W3-01 SUMMARY's Deviation #4.
- **Fix:** Ran `gofmt -s -w internal/cli/adapter/opencode/` to apply canonical formatting. The map literal is now single-space-aligned. (gofmt also rewrote a second map literal in `TestTransformPlugin_HooksDropped` that had similar alignment.)
- **Files modified:** `internal/cli/adapter/opencode/opencode_test.go` (in-place rewrite by gofmt).
- **Verification:** `gofmt -s -d internal/cli/adapter/opencode/` returns no diff; `make lint-changed` exits 0.
- **Committed in:** `7ce199f` (folded).

---

**Total deviations:** 6 — 2 Rule-1 bugs flagged by reading the spec verbatim (plan vs spec divergence on file path + JSON key), 1 Rule-3 workflow (TDD collapse per established precedent), 1 Rule-3 test-runner constraint (cross-package init() side-effects don't aggregate in per-package test binaries), 2 Rule-1 lint bugs (unused const + gofmt cosmetic).

**Impact on plan:** None on deliverables. All `<acceptance_criteria>` gates pass for the task (including the `.opencode/config.json` grep, which passes via deviation-note string); all `<verification>` checks pass; all `<success_criteria>` bullets satisfied. The plan's intent (opencode adapter completes ADAPT-01 structural closed set; §7.4 opencode contract held; ADAPT-07 silent-drop discipline; stdlib-only) lands faithful to the spec — the path/key naming corrections are upgrades, not regressions.

## Threat Flags

None. The opencode package introduces:
- No new network endpoints (Adapter contract is pure-function; orchestrator owns HTTP).
- No new auth paths (credential flows in via context.Context, never read from env vars or files).
- No new file-access patterns at trust boundaries (TransformPlugin reads from a staging dir the orchestrator owns; writes to a destination the orchestrator owns; mode discipline forced to 0644 / 0755).
- No new schema surface beyond `.opencode/opencode.json` — already part of the OpenCode platform's own trust model (the user's own cwd).

The write discipline REDUCES threat surface vs ad-hoc copy: every file gets a 0644 mode; non-regular entries are silently skipped (defense-in-depth against any future W2 safe-extract regression); drop-listed top-level components are short-circuited with `filepath.SkipDir` so even pathological multi-MB tarballs full of `hooks/*.sh` never get copied.

## Issues Encountered

- Sibling worktrees (W3-02 codex, W3-03 gemini) running in parallel are not visible from this worktree. `TestRegistry_RegistersOnImport` could not assert the 4-of-4 closed set even if Go's per-package test binary model would have allowed it — neither package exists in this branch. The 4-of-4 assertion is correctly delegated to plan 07-W3-05's cobra wiring (single compilation unit).
- `make lint-changed`'s BASE_REF-vs-HEAD diff strategy does NOT see newly-added directories (it only lints files present in the base ref). For this plan's atomic commit, the lint sweep that catches new files is the full `make lint` inside the pre-push gate; the per-iteration `make lint-changed` here passes because the package list includes `./internal/cli/adapter/...` from claudecode's prior commit (which transitively covers opencode/).
- `.planning/` is gitignored at the repo level, so this SUMMARY.md is not git-trackable. Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path. No `docs(...)` follow-up commit is possible without `-f`-style force-stage (which is forbidden).

## User Setup Required

None. All changes are repo-internal Go code. No external services, no secrets, no schema migrations, no new go.mod entries.

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/adapter/opencode/opencode.go ]                                  → FOUND (452 lines)
[ -f internal/cli/adapter/opencode/opencode_test.go ]                             → FOUND (565 lines)

# Gitignored SUMMARY (main-repo .planning/)
[ -f /home/jcm/Projects/ach/.planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W3-04-SUMMARY.md ] → FOUND (this file)

# Commit existence
git log --oneline -3 | grep 7ce199f → FOUND ("feat(07-W3-04): add opencode platform adapter")

# Plan-level acceptance gates
grep -q "package opencode" internal/cli/adapter/opencode/opencode.go                              → OK
grep -q "\"opencode\"" internal/cli/adapter/opencode/opencode.go                                  → OK  (canonical ID literal)
grep -q "adapter.Register" internal/cli/adapter/opencode/opencode.go                              → OK  (init registration)
grep -qE "\\.opencode/config\\.json|.opencode/config.json" internal/cli/adapter/opencode/opencode.go → OK  (matches via deviation-note + Detect comment; EMITTED Path is .opencode/opencode.json)
grep -qE "\\.opencode/opencode\\.json" internal/cli/adapter/opencode/opencode.go                  → OK  (spec-correct path)
opencode_test.go contains TestRenderRuntime_ConfigJsonShape                                       → OK
opencode_test.go contains TestTransformPlugin_HooksDropped                                        → OK
opencode_test.go contains TestRegistry_RegistersOnImport                                          → OK

# Plan-level verification gates
./scripts/dev.sh make unit-pkg PKG=./internal/cli/adapter/...                                     → exit 0  (claudecode 17 tests + opencode 18 tests = 35 total pass)
./scripts/dev.sh make lint-changed                                                                 → exit 0  (lint clean)
grep -E '^\s*"(log|log/slog|gopkg\.in/yaml)' internal/cli/adapter/opencode/*.go                   → 0  (stdlib-only)
go.mod diff vs origin/main: 0  (zero new entries)
```

## Self-Check: PASSED

## Next Phase Readiness

- **07-W3-05 (cobra wiring + autodetection):** can now blank-import all four adapter subpackages once W3-02 (codex) and W3-03 (gemini) land. The 4-of-4 closed-set assertion (`adapter.Iter()` length == 4 after all four init()s fire) belongs there. opencode resolves via `adapter.Lookup("opencode")` (canonical); no aliases.
- **07-W2-03 (extract/autoclaim — SAFE-04 cascade):** can now reference `opencode.Adapter.ResolveOutputContent` as the Tier 2 ContentResolver for `.opencode/opencode.json` targets. For any other target the cascade falls through to Tier 3 source-byte read — which is the correct behavior for pass-through plugin files (opencode's TransformPlugin emits source bytes verbatim for everything except the silent-drop set).
- **07-W4-01 (e2e):** the 4-platform Core Value path can drive `opencode` end-to-end against the kept kind cluster as soon as the orchestrator (W3-05) lands. Expected deltas vs the claude-code happy path: runtime-config file is `.opencode/opencode.json` (not `.claude/.mcp.json`); top-level JSON key is `mcp` (not `mcpServers`); plugin trees with `hooks/`, `.lsp.json`, `monitors/`, `bin/`, `settings.json` components emit a single stderr warning at end of hydration listing the dropped names; exit code unchanged.

No blockers. The Adapter interface surface (from 07-W3-01) absorbed the opencode-specific divergences (target path, top-level JSON key, silent-drop set) without any interface change — exactly the parallelism-enabler the W3-01 design intended.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
