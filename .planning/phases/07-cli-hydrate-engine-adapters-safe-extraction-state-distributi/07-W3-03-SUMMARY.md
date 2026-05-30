---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W3-03
subsystem: cli
tags: [adapter, gemini-cli, gemini, json-merge, extension-distribution, plugin-write-dropped, silent-drop, hooks-dropped, settings-json, credential-context, ADAPT-01, ADAPT-03, ADAPT-04, ADAPT-05, ADAPT-06, ADAPT-07, SAFE-04, phase-7]

# Dependency graph
requires:
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W3-01
    provides: "adapter.Adapter interface (7 methods) + MergeKind/Confidence enums + Match/FileWrite/PluginWrite (incl. Dropped []string) + WithCredential/CredentialFromContext + Register/Lookup/Iter registry — all consumed verbatim by this adapter."
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-05
    provides: "internal/cli/manifest.Manifest + RuntimeBlock (Models/MCPServers/A2AAgents) + ContentRef (incl. Endpoint) — RenderRuntime consumes m.Runtime.MCPServers[i].Endpoint and m.Runtime.A2AAgents[i].Endpoint for runtime-config URL construction."
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-02
    provides: "internal/cli/state.File — RenderRuntime takes *state.File as the prior-state read input; the gemini adapter does not consult it on the render path but the signature is honored."
provides:
  - "internal/cli/adapter/gemini.Adapter — third of four platform adapters per ADAPT-01"
  - "JSON-merge runtime-config shape (.gemini/settings.json) — mcpServers + a2aAgents maps, MergeDeep, deterministic via lexicographic key sort"
  - "Plugin extension-distribution pattern — .gemini/extensions/<plugin-name>/ + minimal extension.json manifest ({name, version, components}) per the plan's <action> fallback shape"
  - "Silent-drop accounting for hooks/ — PluginWrite.Dropped accumulates 'hooks' exactly once per ADAPT-07 + CONTEXT.md D-08"
affects: [07-W3-05, 07-W2-03, 07-W4-01]

# Tech tracking
tech-stack:
  added: []  # stdlib-only; zero new go.mod entries
  patterns:
    - "Plan-as-canonical-contract: when CLI spec §7.4 + plan must_haves diverge on plugin output root (.gemini/plugins/ vs .gemini/extensions/), the plan wins per the W3-01 precedent ('the plan is the canonical contract for this work'). Future cobra --platform help text reflects the plan's choice."
    - "Per-plugin extension manifest: gemini-cli's TransformPlugin writes an extension.json {name, version, components} alongside the per-component subdirs. The 'components' list is determined by scanning the dst tree (only listing components that actually have at least one file under them) — manifest never claims components the plugin doesn't contribute."
    - "Silent-drop bookkeeping discipline: hooks/ entries are detected at WalkDir time via firstPathComponent classification; the dropped set is a map[string]bool so duplicates don't double-count; PluginWrite.Dropped is always sorted (or nil when empty) for deterministic surface."
    - "Unknown-component silent-drop without bookkeeping: top-level src components not in componentKept AND not in componentDropped are silently dropped WITHOUT being recorded in PluginWrite.Dropped. This keeps the orchestrator's end-of-hydration stderr warning focused on the documented-but-unsupported components (hooks for gemini); unknown components are an upstream protocol violation, not an ADAPT-07 silent-drop."
    - "Best-effort plugin metadata read: TransformPlugin reads .claude-plugin/plugin.json's version field for the extension.json manifest; missing file, malformed JSON, or missing version key all degrade to version='' rather than fail the entire plugin transformation."
    - "Detect() with $HOME isolation in tests: each Detect test uses t.Setenv('HOME', t.TempDir()) to prevent leaking the developer's real ~/.gemini/settings.json into the test (which would inflate Confidence in zero-signal tests). os.UserHomeDir() honors $HOME on POSIX."

key-files:
  created:
    - "internal/cli/adapter/gemini/gemini.go (592 lines — Adapter struct + ID/Aliases + Detect (4 signals → Low/Medium/High; includes $HOME global hint) + RenderRuntime (.gemini/settings.json JSON with mcpServers + a2aAgents maps + x-ach-key header) + TransformPlugin (kept: agents/prompts/commands/skills; dropped: hooks → PluginWrite.Dropped; extension.json manifest) + MergeStrategies + ResolveOutputContent (Tier 2 recompute) + init() Register)"
    - "internal/cli/adapter/gemini/gemini_test.go (543 lines — 19 tests: ID; Aliases shape; Detect 0/1/2/4 signals with $HOME isolation; RenderRuntime shape + credential propagation + empty runtime + nil manifest; TransformPlugin extension layout + hooks dropped + no-hooks-Dropped-nil + empty paths; MergeStrategies; ResolveOutputContent round-trip + unknown-target + nil-manifest; registry-on-import via Lookup case-insensitive)"
  modified: []  # adapter.go and registry.go untouched per the parallel-wave invariant

key-decisions:
  - "Plugin output root is .gemini/extensions/<plugin-name>/ per the plan's <must_haves.truths> and <behavior> sections — NOT .gemini/plugins/ as CLI spec §7.2 row 3 lists. The plan is canonical (W3-01 precedent). The cobra layer (W3-05) MUST pass dst='<workspace>/.gemini/extensions' rather than the per-spec '<workspace>/.gemini/plugins'."
  - "Aliases() returns []string{'gemini'} — the plan's <must_haves.truths> pins this single alias. Spec §7.2 row lists [gemini, google-gemini]; the plan trims to one for the parallel-wave contract. If users type 'google-gemini' at the CLI, lookup fails with the closed-set error; this is intentional."
  - "TransformPlugin writes an extension.json {name, version, components} per the plan's <action> fallback when spec §7.4 is silent on Gemini's extension manifest shape. The plugin's version is best-effort-read from .claude-plugin/plugin.json's 'version' field; missing/malformed → version=''. Components list is built from the dst tree (only kept components that have at least one file)."
  - "Silent-drop accounting: hooks/ is detected at WalkDir time by firstPathComponent classification; the dropped set is a map[string]bool so duplicates don't double-count; PluginWrite.Dropped is sorted (or nil when empty) for deterministic surface. Unknown top-level components NOT in componentKept and NOT in componentDropped (e.g. '.lsp.json', 'monitors/', 'bin/') are silently dropped WITHOUT being recorded — the orchestrator warning surface stays focused on hooks specifically (the only documented-but-unsupported component for gemini)."
  - "TransformPlugin signature receives (ctx, src, dst) where src is the staging dir for ONE plugin and dst is the .gemini/extensions root the orchestrator provides. The plugin name is derived from filepath.Base(src) — matches the claudecode pattern. The full destination path becomes dst/<plugin-name>/. ExtractedFiles paths are recorded relative to dst (so the orchestrator's state writer can hash + index without re-deriving the plugin prefix)."
  - "JSON shape for .gemini/settings.json mirrors claudecode .mcp.json: {mcpServers: {<id>: {type: 'http', url: <endpoint>, headers: {x-ach-key: <cred>}}}, a2aAgents: {...}}. The shape is encoded via a struct + map types so encoding/json's lexicographic key sort produces deterministic output (SAFE-04 Tier 2 byte-equality contract). The a2aAgents key is omitted entirely when empty (nil-out-empty + omitempty tag)."
  - "RED+GREEN collapsed into a single per-task commit — same precedent as W3-01 (and W1-01, W1-02 before that). The project's pre-commit hook runs `make unit` over the whole tree; a failing-to-compile RED test would fail go vet and reject the commit. CLAUDE.md forbids `--no-verify`. TDD preserved procedurally: test stubs written first against the unbuilt gemini.Adapter symbols, build failure verified (undefined: gemini.Adapter, settingsJSONPath, etc.), only then impl added, combined diff staged and committed."

patterns-established:
  - "Two-binding plugin output root pattern: for the three non-claudecode adapters, TransformPlugin writes under a platform-specific tree (codex: .codex/plugins/<name>/; gemini-cli: .gemini/extensions/<name>/; opencode: .opencode/plugins/<name>/). Each adapter encodes its prefix as a comment/constant choice for the cobra layer; the adapter itself receives an already-joined dst arg. The cobra layer (W3-05) is responsible for the platform → prefix mapping."
  - "Per-extension manifest write pattern: when a target platform requires a manifest file alongside the distributed pieces (gemini: extension.json), the adapter writes it AT THE END of TransformPlugin after the WalkDir loop. The manifest's contents derive from (a) the plugin's .claude-plugin/plugin.json (name + version) and (b) the dst tree introspection (which components actually got files). This is the right ordering — the manifest reflects what was actually written, not what was intended."

requirements-completed:
  - ADAPT-01  # 4-adapter compile + alias resolution — gemini-cli (this plan); claude-code shipped in W3-01; codex/opencode in W3-02/04
  - ADAPT-03  # Runtime config rendering — .gemini/settings.json via RenderRuntime + credential context propagation
  - ADAPT-04  # Plugin canonical format = Claude Code — gemini-cli distributes Claude-format pieces into .gemini/extensions/<name>/
  - ADAPT-05  # Merge strategies — MergeDeep for .gemini/settings.json; Keys tracking for inverse-merge during --sync
  - ADAPT-06  # Adapter scope rule — gemini emits ONLY .gemini/-prefixed paths
  - ADAPT-07  # Silent-drop accounting — PluginWrite.Dropped accumulates 'hooks' exactly once per gemini-cli per CONTEXT.md D-08

# Note: ADAPT-01 is now 3-of-4 complete (claude-code from W3-01, gemini-cli
# from this plan, codex/opencode pending in W3-02/W3-04 which run in
# parallel with this plan).

# Metrics
duration: ~20min
completed: 2026-05-29
---

# Phase 7 Plan 07-W3-03: Gemini-cli Platform Adapter Summary

**Stdlib-only platform adapter — third of four per ADAPT-01. Renders .gemini/settings.json (JSON, mcpServers + a2aAgents maps, MergeDeep) and distributes Claude-format plugin pieces into .gemini/extensions/<name>/ with kept components (agents, prompts, commands, skills) verbatim-copied, hooks silently dropped per ADAPT-07, and a per-extension extension.json manifest emitted alongside.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-05-29T15:08:09Z (worktree spawn after HEAD reset)
- **Completed:** 2026-05-29T15:28:25Z
- **Tasks:** 1 (`auto` / `tdd=true`)
- **Files created:** 2 (1 source + 1 test; both tracked)
- **Tracked commits:** 1 (`7127483`)
- **Tests added:** 19, all passing under `-race` (the adapter package's existing 16 registry tests + 18 claudecode tests continue to pass — 53 total in `./internal/cli/adapter/...`)
- **Lines of code:** 1,135 total (592 in gemini.go + 543 in gemini_test.go)

## Accomplishments

- `internal/cli/adapter/gemini/gemini.go` ships the gemini-cli adapter implementing all seven methods of the `adapter.Adapter` interface from W3-01:
  - `ID()` returns `"gemini-cli"` (canonical, matches CLI spec §7.2 row 3).
  - `Aliases()` returns `[]string{"gemini"}` per the plan's `<must_haves.truths>` (trimmed from spec §7.2's `[gemini, google-gemini]` for the parallel-wave contract).
  - `Detect(root)` scans 4 signals: `.gemini/` directory, `.gemini/settings.json` file, `.gemini/extensions/` directory, and `$HOME/.gemini/settings.json` (global-mode hint). Signal count → Low/Medium/High per spec §7.5; empty Match on zero signals so the autodetection layer treats as no-match.
  - `RenderRuntime(ctx, m, _)` emits one `FileWrite` at `.gemini/settings.json`: JSON document with top-level `mcpServers` map + optional `a2aAgents` map (omitted entirely when empty). Each entry's shape is `{type: "http", url: <ContentRef.Endpoint>, headers: {"x-ach-key": <credential>}}`. `Merge=MergeDeep`; `Keys` carries the contributed `mcpServers.<id>` + `a2aAgents.<id>` paths for STATE-02 + ADAPT-05 inverse-merge during `--sync`. Output is deterministic (encoding/json's lexicographic map-key sort) — important for ResolveOutputContent SAFE-04 byte-equality.
  - `TransformPlugin(ctx, src, dst)` walks the src Claude-format plugin tree and distributes pieces under `dst/<filepath.Base(src)>/`. Top-level `agents/`, `prompts/`, `commands/`, `skills/` are verbatim-copied (mode 0644 / 0755). Top-level `hooks/` is silently dropped per ADAPT-07; `PluginWrite.Dropped` accumulates `"hooks"` exactly once via a map-backed set. `.claude-plugin/` metadata is consumed for the per-extension manifest version field; `.mcp.json` is consumed by RenderRuntime (not by TransformPlugin) so it never appears in `ExtractedFiles`. Unknown top-level components (`.lsp.json`, `monitors/`, `bin/`, etc.) are silently dropped WITHOUT being recorded in `Dropped` (the warning surface stays focused on the documented-but-unsupported `hooks/`).
  - After the WalkDir loop, the adapter writes `dst/<plugin>/extension.json` with `{name: <plugin>, version: <from .claude-plugin/plugin.json>, components: [sorted-kept-component-names-actually-written]}`. The components list is derived from the dst tree (only listing components that actually have at least one file), so the manifest never claims components the plugin doesn't contribute.
  - `MergeStrategies()` returns `{".gemini/settings.json": MergeDeep}`.
  - `ResolveOutputContent(ctx, m, target)` for target `".gemini/settings.json"` recomputes the bytes RenderRuntime would emit (Tier 2 cascade). Returns `(nil, nil)` for any other target so the W2-03 cascade Tier 3 source-byte read takes over for per-plugin component files and per-extension `extension.json` files (all pass-through-equivalent).
- `init()` calls `adapter.Register(&Adapter{})` so the cobra layer (plan 07-W3-05) only needs to blank-import the subpackage. The test `TestRegistry_RegistersOnImport` verifies both canonical ID lookup and case-insensitive alias resolution (`"gemini"`, `"GEMINI"`).
- Stdlib-only discipline verified: only `bytes`, `context`, `encoding/json`, `fmt`, `io`, `os`, `path/filepath`, `sort`, `testing` imports plus the in-repo `internal/cli/{adapter,manifest,state}` siblings. Zero new `go.mod` entries.
- All 19 tests pass under `-race`. The whole `./internal/cli/adapter/...` tree (registry + claudecode + gemini) is regression-safe — 53 tests total, no flakes in this package.
- golangci-lint clean on the gemini package after two cosmetic fixes (unused constant `extensionsRoot` removed; deprecated `filepath.HasPrefix` replaced with a separator-aware boundary check).

## Task Commits

| Task | Name | Hash | Files / Lines |
|------|------|------|---------------|
| 1 | gemini-cli adapter — JSON merge + extension distribution | `7127483` | 2 files / 1,135 insertions |

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored at repo level). Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path.

## Files Created/Modified

| Path | Lines | Role |
|------|-------|------|
| `internal/cli/adapter/gemini/gemini.go` | 592 | Adapter impl — 7 methods + init() Register + extension.json manifest writer + componentKept/Dropped classification + best-effort plugin version reader |
| `internal/cli/adapter/gemini/gemini_test.go` | 543 | 19 tests — ID/Aliases/Detect(4 levels)/RenderRuntime(4 scenarios)/TransformPlugin(4 scenarios)/MergeStrategies/ResolveOutputContent(3 scenarios)/registry-on-import |
| **Total** | **1,135** | **2 files** |

## Decisions Made

See `key-decisions` in frontmatter. Summary:

1. **Plugin output root is `.gemini/extensions/<plugin-name>/` per the plan's `<must_haves>`** — NOT `.gemini/plugins/<plugin-name>/` as CLI spec §7.2 lists. The plan is the canonical contract per the W3-01 precedent. The cobra layer (W3-05) MUST pass `dst='<workspace>/.gemini/extensions'`.
2. **`Aliases()` returns `["gemini"]`** per the plan, trimmed from spec §7.2's `[gemini, google-gemini]`. Users who type `google-gemini` at the CLI will fail closed-set lookup; this is intentional.
3. **`TransformPlugin` writes a minimal `extension.json` manifest** when spec §7.4 is silent on Gemini's extension format. Manifest format: `{name, version, components: [actually-written-component-names]}`. Plugin version is best-effort-read from `.claude-plugin/plugin.json`; missing/malformed → `version=""`.
4. **Silent-drop accounting via map-backed set** so duplicate hooks/* entries don't double-count `"hooks"` in `PluginWrite.Dropped`. Unknown top-level components are silently dropped WITHOUT being recorded — the orchestrator's end-of-hydration stderr warning stays focused on documented-but-unsupported components (hooks for gemini).
5. **`ExtractedFiles` paths are relative to `dst`** (not relative to `dst/<plugin>/`) — so the orchestrator's state writer hashes + indexes without re-deriving the plugin prefix. Plugin name is derived from `filepath.Base(src)` (matches the claudecode pattern).
6. **JSON shape for `.gemini/settings.json` mirrors claudecode `.mcp.json`** — `{mcpServers: {...}, a2aAgents: {...}}` with each entry as `{type, url, headers}`. The `a2aAgents` key is omitted entirely when empty (nil-out-empty + `omitempty` tag); the `mcpServers` key always renders (empty-map → `{}`) to keep the JSON shape stable for round-trip diffing.
7. **RED+GREEN collapsed into a single per-task commit** — same precedent as W3-01 / W1-01 / W1-02. Pre-commit hook runs `make unit`; strict RED would fail go vet; CLAUDE.md forbids `--no-verify`. TDD preserved procedurally.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pre-existing timing-sensitive flakes blocked the first commit attempt**

- **Found during:** Task 1 commit (first + second attempts).
- **Issue:** The pre-commit hook runs `make unit` repo-wide. On the first commit attempt, both `TestGet_Singleflight_DedupesConcurrentMisses` in `internal/contentservice/envcache/` AND `TestCachedResolverSingleFlight` + `TestRedisCachedTeamsResolver_SingleFlight` in `internal/keystore/` failed under `-race`. On the second attempt, only `TestGet_Singleflight_DedupesConcurrentMisses` failed. Both failures are unrelated to anything in this plan's scope — same flakes documented in W3-01 SUMMARY's Deviation #1.
- **Verification:** Ran the failing tests in isolation via `./scripts/dev.sh go test -count=3 -run TestGet_Singleflight_DedupesConcurrentMisses ./internal/contentservice/envcache/` (passed clean) and `-run TestCachedResolverSingleFlight ./internal/keystore/` (passed clean). Confirms timing-sensitive flakes, not deterministic regressions.
- **Fix:** Per the SCOPE BOUNDARY rule, re-attempted the commit without modifications. Third attempt passed all gates including the previously-flaky tests. Logged here for future visibility; root-cause fix remains out of scope (and now has hit two consecutive Phase 7 worktree spawns plus W3-01's two — should be tracked as a deferred item).
- **Files modified:** None (workflow retry).
- **Committed in:** `7127483` (Task 1's eventual commit).

**2. [Rule 3 - Workflow] TDD RED+GREEN collapsed into single per-task commit**

- **Found during:** Task 1 RED step.
- **Issue:** The plan's `tdd="true"` attribute mandates separate RED (`test(...)`) and GREEN (`feat(...)`) commits per the executor `<tdd_execution>` block. The project's pre-commit hook runs `make unit` over the whole tree; a failing-to-compile RED commit (test file referencing undefined `gemini.Adapter`, etc.) trips `go vet` and the commit is rejected. CLAUDE.md explicitly forbids `--no-verify`.
- **Fix:** Same resolution as W3-01 / W1-01 / W1-02 precedents. Collapsed RED + GREEN into one atomic commit. TDD preserved procedurally: test stubs written first; build failure verified locally; impl added; combined diff staged and committed.
- **Files modified:** None (workflow trade-off).
- **Verification:** GREEN test run after impl shows all 19 sub-tests passing (`--- PASS: TestGemini_ID` through `--- PASS: TestRegistry_RegistersOnImport`).
- **Committed in:** `7127483`.

**3. [Rule 1 - Bug] Initial gemini.go had an unused constant `extensionsRoot`**

- **Found during:** Task 1 first lint run (`./scripts/dev.sh make lint-changed`).
- **Issue:** I declared `extensionsRoot = ".gemini/extensions"` as a centralized constant intending to use it in TransformPlugin, but the actual implementation receives the extensions root as the orchestrator-provided `dst` argument and never references the constant. golangci-lint's `unused` checker flagged the dead code.
- **Fix:** Removed the constant; documented the `.gemini/extensions` prefix as a comment on `canonicalID` noting that the orchestrator (W3-05) encodes the prefix at the call-site via `dst`.
- **Files modified:** `internal/cli/adapter/gemini/gemini.go` (in-place edit).
- **Verification:** `./scripts/dev.sh make lint-changed` exits 0; all 19 tests still pass.
- **Committed in:** `7127483` (folded into Task 1 commit).

**4. [Rule 1 - Bug] Test code used deprecated `filepath.HasPrefix`**

- **Found during:** Task 1 first lint run (after fix #3).
- **Issue:** The hooks-leak guard in `TestTransformPlugin_ExtensionLayout` used `filepath.HasPrefix(f, filepath.Join("caveman", "hooks"))` to check that no `ExtractedFiles` entries leaked from the hooks directory. golangci-lint's `staticcheck` flagged this: `filepath.HasPrefix has been deprecated since Go 1.0 because it shouldn't be used: HasPrefix does not respect path boundaries and does not ignore case when required (staticcheck SA1019)`.
- **Fix:** Replaced the deprecated call with a separator-aware boundary check: build the prefix as `filepath.Join("caveman", "hooks") + string(filepath.Separator)` and compare via `f == "caveman/hooks"` OR `f[:len(prefix)] == prefix`. This respects the path-separator boundary explicitly (the deprecated call would treat `caveman/hooksXyz` as a hooks match).
- **Files modified:** `internal/cli/adapter/gemini/gemini_test.go` (in-place edit, ~3 lines changed).
- **Verification:** `./scripts/dev.sh make lint-changed` exits 0; tests still pass.
- **Committed in:** `7127483` (folded into Task 1 commit).

---

**Total deviations:** 4 (1 Rule 3 blocking — pre-existing out-of-scope flake retry; 1 Rule 3 workflow — TDD collapse per established precedent; 2 Rule 1 bugs — lint-driven cosmetic fixes).
**Impact on plan:** None on deliverables. All `<acceptance_criteria>` gates pass; all `<verification>` checks pass; all `<success_criteria>` bullets satisfied. The plan's intent (gemini-cli adapter shipping with JSON-merge runtime config + extension distribution + hooks silent-drop) lands exactly as written.

## Threat Flags

None. The gemini adapter introduces:
- No new network endpoints (Adapter contract is pure-function; orchestrator owns HTTP).
- No new auth paths (credential flows in via `context.Context` via `adapter.CredentialFromContext`, never read from env vars or files).
- No new file-access patterns at trust boundaries (`TransformPlugin` reads from a staging dir the orchestrator owns; writes to a destination the orchestrator owns; mode discipline 0644 / 0755; non-regular entries silently skipped).
- No new schema surface beyond `.gemini/settings.json` + `.gemini/extensions/<name>/extension.json` — both files live under the user's own cwd (workspace scope) or `$HOME` (global scope); they are part of the gemini-cli platform's own trust model.

The kept-component filter + best-effort metadata read REDUCE threat surface vs an unbounded pass-through: only known component subdirs are copied to dst; unknown top-level entries are silently dropped (so a maliciously-crafted plugin tarball with a `..` or `/etc/...` top-level can't leak files outside the per-plugin destination — though the W2-01 safe-extract layer is the primary filter, this is defense-in-depth).

## Issues Encountered

- Same pre-existing timing-sensitive flakes documented in W3-01 SUMMARY's Issues section: `TestGet_Singleflight_DedupesConcurrentMisses` (envcache) and `TestCachedResolverSingleFlight` / `TestRedisCachedTeamsResolver_SingleFlight` (keystore). Reproduced twice in this plan's commit cycle; resolved by retry both times. The cumulative count across Phase 7 worktree spawns is now ≥4 — worth tracking as a deferred item, but out of scope for this plan.
- `make lint-changed`'s BASE_REF-vs-HEAD diff strategy only inspects files present in the base ref, so newly-added directories are not implicitly linted on the first commit. The plan's verification step `./scripts/dev.sh make lint-changed` did surface lint errors for the new `internal/cli/adapter/gemini/*.go` files because `lint-changed` widens to whole-package scope when any file in a package is touched (which is the case here — the package is brand new). Both lint errors caught locally before commit.
- `.planning/` is gitignored at the repo level, so this SUMMARY.md is not git-trackable. Per the worktree-mode `<parallel_execution>` block, the SUMMARY survives worktree teardown via the shared main-repo `.planning/` filesystem path. No `docs(...)` follow-up commit is possible without `-f`-style force-stage (which is forbidden).

## User Setup Required

None. All changes are repo-internal Go code. No external services, no secrets, no schema migrations, no new go.mod entries, no CLAUDE.md / docs updates required (the gemini adapter is invisible to user-facing docs until the cobra layer W3-05 lands and surfaces it via `--platform`).

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/adapter/gemini/gemini.go ]                              → FOUND
[ -f internal/cli/adapter/gemini/gemini_test.go ]                         → FOUND

# Gitignored SUMMARY (main-repo .planning/)
[ -f /home/jcm/Projects/ach/.planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W3-03-SUMMARY.md ] → FOUND

# Commit existence
git log --oneline -3 | grep 7127483 → FOUND ("feat(07-W3-03): add gemini-cli platform adapter")

# Plan-level acceptance gates (Task 1, verbatim from plan)
grep -q "package gemini" internal/cli/adapter/gemini/gemini.go                              → OK
grep -q "\"gemini-cli\"" internal/cli/adapter/gemini/gemini.go                              → OK  (canonical ID literal)
grep -q "\"gemini\"" internal/cli/adapter/gemini/gemini.go                                  → OK  (alias literal)
grep -q "adapter.Register" internal/cli/adapter/gemini/gemini.go                            → OK  (init registration)
grep -qE "\\.gemini/settings\\.json|.gemini/settings.json" internal/cli/adapter/gemini/gemini.go → OK
grep -qE "\\.gemini/extensions|.gemini/extensions" internal/cli/adapter/gemini/gemini.go    → OK
gemini_test.go contains TestRenderRuntime_SettingsJsonShape                                 → OK
gemini_test.go contains TestTransformPlugin_Hooks_Dropped                                   → OK
gemini_test.go contains TestTransformPlugin_ExtensionLayout                                 → OK

# Plan-level verification gates
./scripts/dev.sh make unit-pkg PKG=./internal/cli/adapter/...                               → exit 0  (53 tests pass under -race: 19 gemini + 18 claudecode + 16 registry)
./scripts/dev.sh make lint-changed                                                          → exit 0  (lint clean after 2 in-commit fixes)

# Stdlib-only discipline
grep -E '^\s*"(log|log/slog|gopkg\.in/yaml)' internal/cli/adapter/gemini/*.go               → 0      (stdlib-only; no new go.mod entries)

# Parallel-wave invariant
git diff HEAD~1 HEAD --stat -- internal/cli/adapter/adapter.go internal/cli/adapter/registry.go → empty (untouched, as the plan's <parallel_execution> requires)
```

## Self-Check: PASSED

## Next Phase Readiness

- **07-W3-02 (codex):** running in parallel with this plan; no conflict. Both add new subpackages under `internal/cli/adapter/`; neither modifies `adapter.go` or `registry.go`. The parallel-wave invariant holds.
- **07-W3-04 (opencode):** running in parallel with this plan; same posture.
- **07-W3-05 (cobra wiring + autodetection):** depends on all four W3-* plans. Will blank-import `internal/cli/adapter/gemini` so `init()` fires; will call `adapter.Iter()` for autodetection (the gemini adapter's `Detect()` will participate); will call `adapter.Lookup("gemini-cli")` or `adapter.Lookup("gemini")` for explicit `--platform` selection. The cobra layer is also responsible for passing `dst='<workspace>/.gemini/extensions'` (not `'.gemini/plugins'`) to TransformPlugin — the plan deviates from spec §7.2 here, so W3-05 MUST reference this SUMMARY's key-decisions to get the dst path right.
- **07-W2-03 (extract/autoclaim — SAFE-04 cascade):** the gemini adapter's `ResolveOutputContent` is plug-compatible with the Tier 2 ContentResolver contract. For target `.gemini/settings.json` it returns the recomputed bytes; for any other target (per-plugin files under `.gemini/extensions/`, per-extension `extension.json`) it returns `(nil, nil)` so Tier 3 falls through to source-byte read — which is correct because those bytes ARE the canonical bytes (TransformPlugin pass-through for the kept components; deterministic encoding for the extension.json manifest).
- **07-W4-01 (e2e):** the gemini-cli platform path can drive end-to-end against the kept kind cluster as soon as W3-05 lands. The expected behavior: hydrate emits `.gemini/settings.json` with mcpServers + a2aAgents from the demo Environment + extension trees under `.gemini/extensions/caveman/{agents,prompts,commands,skills}/` + an extension.json manifest per extension + a single stderr warning at end of hydration listing `hooks` as a silently-dropped component (CONTEXT.md D-08).

No blockers. The gemini-cli adapter is ready for the cobra integration; only constraint is the plan's `.gemini/extensions/` choice that W3-05 MUST honor.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
