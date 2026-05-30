---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W3-02
subsystem: cli
tags: [adapter, registry, codex, toml, frontmatter-rewrite, silent-drop, plugin-distribution, mergekind, credential-context, ADAPT-01, ADAPT-03, ADAPT-04, ADAPT-05, ADAPT-06, ADAPT-07, SAFE-04]

# Dependency graph
requires:
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W3-01
    provides: "internal/cli/adapter.Adapter interface + MergeKind/Confidence enums + FileWrite + PluginWrite (incl. Dropped []string DECLARED in W3-01) + WithCredential/CredentialFromContext + Register/Lookup/Iter registry + claudecode reference shape. This plan consumes all of these unchanged — adapter.go was not modified."
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-02
    provides: "internal/cli/state.File / FileEntry / AdapterSection types — RenderRuntime takes *state.File as the prior-state read input (currently ignored by codex per the pass-through reading; consumed in W3-05 orchestrator wiring)."
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-05
    provides: "internal/cli/manifest.Manifest + RuntimeBlock + ContextBlock + ContentRef.Endpoint — RenderRuntime consumes m.Runtime.MCPServers[i].Endpoint and m.Runtime.A2AAgents[i].Endpoint per ADAPT-03 for runtime-config URL construction without re-fetching."
provides:
  - "internal/cli/adapter/codex.Adapter — second of four Adapter impls per ADAPT-01; first NON-pass-through reference (TOML target + frontmatter rewrite + silent-drop accounting)"
  - "github.com/BurntSushi/toml v1.6.0 as the first new go.mod direct dep at the codex plan boundary (matches PLAN.md decision)"
  - "codex.canonicalID = \"codex\"; codex.Aliases() = [\"codex-cli\"] — registered in the adapter package via init()"
  - "Frontmatter rewrite primitive: rewriteAgentFrontmatter / rewriteFrontmatterLines — line-by-line YAML frontmatter rewrite that preserves CRLF + body bytes verbatim, suitable as the shape for the gemini-cli / opencode siblings if they need analogous transformations"
  - "Silent-drop accumulator pattern: droppedSet helper used to populate PluginWrite.Dropped with deduplicated top-level component names per ADAPT-07"
  - "Detect's global-mode hint pattern: $HOME/.<platform>/ contributes a Low-confidence signal even with no local artifacts — a reusable shape for the three sibling adapters"
affects: [07-W3-03, 07-W3-04, 07-W3-05, 07-W2-03, 07-W4-01]

# Tech tracking
tech-stack:
  added:
    - "github.com/BurntSushi/toml v1.6.0 — TOML 1.0 encoder/decoder used by RenderRuntime + the test suite's round-trip decode. First go.mod direct entry in the adapter wave; the plan deliberately placed it at the codex boundary so the W3-01 foundation stays stdlib-only and the gemini/opencode siblings (JSON targets) don't need it."
  patterns:
    - "TOML deterministic emission via field-tagged map values — BurntSushi/toml sorts map keys lexicographically, so the same manifest + credential always yields byte-identical .codex/config.toml bytes for the SAFE-04 cascade Tier 2 byte-equal compare. Same posture as encoding/json with sorted maps in claudecode."
    - "Frontmatter rewrite as line-level not YAML-AST — the rewrite preserves CRLF line endings + indentation + comment bytes verbatim. A YAML-AST round-trip would have re-emitted with the canonical-form output of whichever encoder was in scope, losing byte-for-byte stability. The W2-03 cascade's drift detection compares xxh3 hashes; byte-identity is the contract."
    - "Top-level-component classification driven by filepath.ToSlash + strings.Split — the silent-drop discipline lives in a single droppedSet helper + a single map lookup. ADAPT-07 accumulation is bounded by the four top-level entries the Claude plugin layout currently defines (commands, hooks, .mcp.json, .claude-plugin), not by tree depth."
    - "Global-mode Detect hint via os.UserHomeDir — Detect supports the spec §7.3 -g/--global flow by checking $HOME/.codex/ on top of the local cwd. The check guards against double-counting when root == \$HOME (the local check already covered it). HOME unset is silent-skip, defensive against init-only environments."
    - "HOME spoofed in tests via t.Setenv — every Detect test calls t.Setenv(\"HOME\", t.TempDir()) so the global-mode hint is deterministic across CI hosts (the bare-metal runner's $HOME/.codex/ would otherwise leak into the test's signal count). t.Setenv auto-restores on test cleanup, so no cross-test pollution."

key-files:
  created:
    - "internal/cli/adapter/codex/codex.go (724 lines after gofmt — Adapter struct + ID/Aliases + Detect (4-signal accumulation incl. global-mode hint) + RenderRuntime (TOML emission with [mcp_servers.<id>] + [a2a_agents.<id>] tables; credential propagation via ctx) + TransformPlugin (verbatim copy + agents frontmatter rewrite + ADAPT-07 silent-drop accumulation) + writeAgentWithFrontmatterRewrite + rewriteAgentFrontmatter + rewriteFrontmatterLines + rewriteFrontmatterLine + startsWithFrontmatterFence + findFrontmatterFences + MergeStrategies + ResolveOutputContent + init() Register call)"
    - "internal/cli/adapter/codex/codex_test.go (614 lines after gofmt — 18 tests covering all 7 methods + edge cases)"
  modified:
    - "go.mod (BurntSushi/toml v1.6.0 promoted from indirect → direct dep)"
    - "go.sum (BurntSushi/toml checksum + downstream Go module resolution updates from go mod tidy)"

key-decisions:
  - "Aliases() returns single-element [\"codex-cli\"] not the spec §7.2 pair [\"codex-cli\", \"openai-codex\"]. The PLAN.md must_haves.truths is the canonical contract for this work; the spec is an authoritative reference for the platform identity but the plan picks the shorter alias set to match the same parallelism-enabling discipline as claudecode's [\"claude\", \"cc\"]. The flat-namespace registry rejection at Register would also reject \"openai-codex\" if a later W3 plan or user override added it — the right place to extend is the §13 user-overrideable platform table, not this adapter's literal."
  - "Frontmatter rewrite scope at v1alpha1 = `tools:` → `allowed_tools:` ONLY (single top-level key). The spec §7.4 codex section describes a wider rewrite map (capitalize tool names, model shortname → full model id, permissions: object → permissionMode: string), but those value-level rewrites need a YAML-AST round-trip which would break byte-for-byte hash stability (sourceHash semantics in W1-04 + D-14). The plan behavior pins the explicit single-key rewrite for v1alpha1; the rest is reserved for a future release once D-14's dual-hash discipline lands. The code comment documents the full mapping table citing spec §7.4 so the future contributor sees the shape."
  - "Frontmatter rewrite is line-level (not YAML-AST). The rewrite preserves CRLF line endings + indentation + comment bytes verbatim. A YAML-AST encoder would re-emit with the canonical-form output of whichever encoder was in scope, losing byte-for-byte stability. The W2-03 cascade's drift detection compares xxh3 hashes; byte-identity is the contract. Line-level rewrite trades expressiveness for stability and is the right tier for the current rewrite map (one top-level key)."
  - "src/.mcp.json is consumed at runtime-config rendering, NOT by TransformPlugin and NOT in PluginWrite.Dropped. The plan behavior makes this distinction explicit: .mcp.json IS consumed, just at a different layer (the orchestrator + runtime-config bridge), so it should not show up as a 'silently dropped' surface in the stderr warning. The test asserts .mcp.json is NOT copied to dst, NOT in ExtractedFiles, and NOT in Dropped — all three negatives encode the discipline."
  - "Global-mode Detect signal contributes Low confidence even with zero local signals. The plan behavior explicitly calls out the global-mode discovery path (-g / ACH_GLOBAL=1): a workspace with no .codex/ artifacts can still resolve to codex if $HOME/.codex/ is present. This means a fresh project cwd + a long-time codex user's $HOME/.codex/ resolves correctly under autodetection. The autodetection layer in W3-05 will use this signal to disambiguate during the cobra dispatch flow."
  - "RED + GREEN collapsed into a single commit — same precedent as W1-01, W1-02, W3-01 (see W3-01-SUMMARY Deviation #2). The project's pre-commit hook runs `make unit` repo-wide; a strict-RED commit referencing undefined symbols would trip go vet and the commit would be rejected. CLAUDE.md forbids --no-verify. TDD preserved procedurally: test stubs written first against the unbuilt Adapter shape, mentally verified that they would reference undefined symbols, then production code added in the same uncommitted working set, then unit + lint gates run + clean, then the combined diff staged + committed."

patterns-established:
  - "Adapter contract is import-cycle-free: internal/cli/adapter/codex imports the parent adapter package + manifest + state + a single third-party (BurntSushi/toml). The orchestrator (plan 07-W3-05 cobra layer) is the only place where adapter + extract + hydrate compose. codex stays unit-testable without the orchestrator — every test in codex_test.go uses t.TempDir + httptest-free seed data."
  - "TOML deterministic emission via field-tagged map values — BurntSushi/toml's source-declaration ordering of struct fields + lexicographic map-key sort gives byte-identical output across invocations with the same input. The codex.configTOMLShape struct is the contract: top-level mcp_servers + a2a_agents keys in fixed source order; per-id maps sorted lex; per-id table fields in source-declared order (url, headers, transport)."
  - "Silent-drop accumulator type droppedSet — encapsulates the 'add once, return sorted slice' discipline. Future adapters (gemini-cli, opencode) can copy the helper verbatim if their silent-drop sets differ; or the helper can be promoted to the parent adapter package if it stabilizes across all three (deferred — premature abstraction until the gemini + opencode siblings are written)."
  - "Per-test HOME spoofing via t.Setenv(\"HOME\", t.TempDir()) — the right discipline for any adapter Detect whose signal set includes a $HOME-rooted hint. Same posture is reusable verbatim in the gemini + opencode test suites if they pick up similar global-mode signals."
  - "Frontmatter rewrite primitive (rewriteFrontmatterLines) — line-level rewrite that preserves CRLF + indentation + non-rewritten bytes verbatim. Extracted as a function so the gemini + opencode siblings can reuse the helper shape if their rewrite maps are non-empty (currently they're TBD per their respective spec sections)."

requirements-completed:
  - ADAPT-01  # 4-adapter compile + alias resolution — codex (this plan); claude-code already in W3-01; gemini-cli + opencode in W3-03 / W3-04
  - ADAPT-03  # Runtime config rendering — .codex/config.toml via RenderRuntime + credential context propagation
  - ADAPT-04  # Plugin canonical format = Claude Code — codex consumes Claude-format src trees and transforms to codex layout (frontmatter rewrite + silent-drop accounting)
  - ADAPT-05  # Merge strategies — MergeDeep for .codex/config.toml; Keys[] prefixed mcp_servers./a2a_agents. for inverse-merge
  - ADAPT-06  # Adapter scope rule — codex emits ONLY .codex/-prefixed paths (configTOMLPath = ".codex/config.toml"; TransformPlugin writes under dst owned by orchestrator)
  - ADAPT-07  # Silent-drop accounting — commands/ + hooks/ silently dropped; PluginWrite.Dropped populated and surfaced via droppedSet
  - SAFE-04   # ResolveOutputContent Tier 2 contract — codex round-trips its TOML render bytes for the matched target; nil/nil for everything else

# Note: ADAPT-01 closed-set status — claude-code (W3-01) + codex (W3-02)
# of the 4 v1alpha1 adapters now ship. gemini-cli (W3-03) and opencode
# (W3-04) land in parallel against the same adapter.go shape; this
# plan does NOT close ADAPT-01 by itself.

# Metrics
duration: ~20min
completed: 2026-05-29
---

# Phase 7 Plan 07-W3-02: Codex Platform Adapter Summary

**TOML-targeted codex Adapter impl that ships the second of four v1alpha1 platform adapters: .codex/config.toml RenderRuntime emits [mcp_servers.<id>] + [a2a_agents.<id>] tables driven by manifest.Runtime entries with credential-context propagation; TransformPlugin distributes Claude-format plugin pieces under dst preserving the Claude layout while rewriting agents/*.md YAML frontmatter (`tools:` → `allowed_tools:` per spec §7.4) and silently dropping commands/ + hooks/ into PluginWrite.Dropped; first new go.mod direct dep (github.com/BurntSushi/toml v1.6.0) lands at the codex plan boundary as designed.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-05-29T15:08:11Z (worktree spawn after HEAD reset)
- **Completed:** 2026-05-29T15:28:52Z
- **Tasks:** 1 (single `auto` / `tdd=true`)
- **Files created:** 2 (1 source + 1 test under internal/cli/adapter/codex/)
- **Files modified:** 2 (go.mod + go.sum)
- **Tracked commits:** 1 (`13d36cb`)
- **Tests added:** 18, all passing (no -race in unit-pkg's default config)
- **Lines of code:** ~1,338 total (724 in codex.go + 614 in codex_test.go after gofmt)

## Accomplishments

- `internal/cli/adapter/codex/codex.go` ships the codex platform Adapter impl: `ID()` returns `"codex"`; `Aliases()` returns `["codex-cli"]`; `Detect(root)` accumulates 4 signals (`.codex/`, `.codex/config.toml`, `.codex/agents/`, `$HOME/.codex/` global-mode hint) into Low/Medium/High confidence and accumulates human-readable reasons.
- `RenderRuntime` emits a single `.codex/config.toml` `FileWrite` containing `[mcp_servers.<id>]` + `[a2a_agents.<id>]` tables built from `m.Runtime.MCPServers` / `m.Runtime.A2AAgents`; each table carries `url` (from `ContentRef.Endpoint`), `headers` (with `x-ach-key` set from `adapter.CredentialFromContext(ctx)`), and `transport: "http"`. Merge classification is `MergeDeep`; `Keys[]` is prefixed `mcp_servers.<id>` / `a2a_agents.<id>` so STATE-05 inverse-merge can target the right TOML subtable (ADAPT-05).
- `TransformPlugin` walks the src tree via `filepath.WalkDir`; routes each entry to one of four paths:
  1. **Silent-drop**: src top-level `commands/` and `hooks/` (per ADAPT-07 + plan behavior) — `filepath.SkipDir`, accumulated into `PluginWrite.Dropped` via the `droppedSet` helper.
  2. **Runtime-bridge consumed**: src root `.mcp.json` — NOT copied (consumed at runtime-config rendering layer) AND NOT in `Dropped` (it IS consumed, just at a different layer).
  3. **Frontmatter rewrite**: src `agents/*.md` — opens file, rewrites YAML frontmatter (`tools:` → `allowed_tools:` per spec §7.4), preserves body bytes + indentation + line endings verbatim, writes to dst at 0644.
  4. **Verbatim pass-through**: every other regular file (prompts, skills, `.claude-plugin/plugin.json`, arbitrary nested files) — `io.Copy` at 0644.
- `MergeStrategies()` returns `{".codex/config.toml": MergeDeep}`.
- `ResolveOutputContent(ctx, m, target)` recomputes the `.codex/config.toml` bytes via `renderConfigTOML` (SAFE-04 cascade Tier 2 contract); returns `(nil, nil)` for any other target (cascade falls through to Tier 3 source-byte read).
- `init()` calls `adapter.Register(&Adapter{})`; the `TestRegistry_RegistersOnImport` test verifies the registration fires correctly.
- 18 tests pass:
  - `TestCodex_ID`, `TestCodex_Aliases`
  - `TestCodex_Detect_NoCodexDir_Zero` (HOME spoofed → zero signals → empty Match)
  - `TestCodex_Detect_NoCodexDir_Low_GlobalHint` (HOME with `.codex/` → Low confidence even with no local artifacts)
  - `TestCodex_Detect_FullArtifacts_High` (HOME spoofed + 3 local signals → High)
  - `TestCodex_Detect_TwoSignals_Medium`
  - `TestRenderRuntime_TomlShape` (BurntSushi/toml round-trip decode → assert `mcp_servers.demo-mcp-jwt.url`, `transport`, key counts, Keys[] prefix discipline)
  - `TestRenderRuntime_CredentialPropagation` (`adapter.WithCredential` → headers carry `x-ach-key`)
  - `TestRenderRuntime_EmptyRuntime_EmitsEmptyTables`
  - `TestRenderRuntime_NilManifest_Errors`
  - `TestTransformPlugin_DistributesPrompts` (full 7-file plugin tree → 4 extracted, 2 in Dropped, .mcp.json neither copied nor dropped, dropped dirs NOT on disk, every extracted file byte-identical + mode 0644)
  - `TestTransformPlugin_FrontmatterRewrite_AgentsKeys` (`tools:` → `allowed_tools:`; pass-through keys + body + nested values intact)
  - `TestTransformPlugin_FrontmatterRewrite_NoFrontmatter_VerbatimCopy`
  - `TestTransformPlugin_EmptySrc_NoFiles`
  - `TestTransformPlugin_EmptyPaths_Errors`
  - `TestMergeStrategies_ConfigTomlIsDeep`
  - `TestResolveOutputContent_TomlRoundTrips` (bytes byte-equal `RenderRuntime` output)
  - `TestResolveOutputContent_UnknownTarget_ReturnsNilNil`
  - `TestResolveOutputContent_NilManifest_ReturnsNilNil`
  - `TestRegistry_RegistersOnImport`
- `go.mod` / `go.sum` updated: `github.com/BurntSushi/toml v1.6.0` is now a direct dep. `go mod tidy` ran clean (no drift). This is the first new direct go.mod entry in the adapter wave, intentionally landed at the codex plan boundary per the plan's <objective>.
- `golangci-lint` clean across `./internal/cli/adapter/...` after one `gofmt -s -w` pass on the codex sources (initial inline doc comment used a `+` mid-list-item that gofmt-doc rewrote to use the canonical numbered-list indentation — same cosmetic gofmt rewrite as W3-01 Deviation #3).
- Pre-commit `make unit` repo-wide passed on the second attempt; the first attempt tripped a transient `internal/keystore.TestCachedResolverSingleFlight` flake (out of scope per SCOPE BOUNDARY; resolved by retry — see Deviation #1 below).

## Task Commits

1. **Task 1: codex adapter** — `13d36cb` (`feat`). 4 files / 1,341 insertions: `internal/cli/adapter/codex/codex.go`, `internal/cli/adapter/codex/codex_test.go`, `go.mod`, `go.sum`. Pre-commit gate fired twice; second attempt clean.

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored at repo level) per the same posture as 07-W3-01. The SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path.

## Files Created/Modified

| Path | Lines | Role |
|------|-------|------|
| `internal/cli/adapter/codex/codex.go` | 724 | Adapter impl — 7 methods + 7 helpers + 4 types + 1 silent-drop map + init() Register |
| `internal/cli/adapter/codex/codex_test.go` | 614 | 18 tests covering all 7 methods + edge cases |
| `go.mod` | +2 (delta) | BurntSushi/toml v1.6.0 promoted from indirect → direct |
| `go.sum` | +2 (delta) | BurntSushi/toml checksum + downstream module updates from go mod tidy |
| **Total new lines** | **~1,338** | **2 source/test files; 2 manifest files updated** |

## Decisions Made

See `key-decisions` in frontmatter. Summary:

1. **Aliases = `["codex-cli"]`** (single element) per plan must_haves contract; not the spec §7.2 pair `["codex-cli", "openai-codex"]`. Matches the parallelism-enabling discipline from claudecode.
2. **Frontmatter rewrite scope at v1alpha1 = `tools:` → `allowed_tools:` ONLY.** The spec §7.4 codex section describes a wider rewrite map (tool capitalization, model shortname → full id, permissions → permissionMode); those value-level rewrites need a YAML-AST round-trip which would break byte-for-byte hash stability under D-14's dual-hash discipline. Reserved for a future release once D-14 lands.
3. **Frontmatter rewrite is line-level, not YAML-AST.** Preserves CRLF + indentation + comment bytes verbatim. Trades expressiveness for byte-identity, which is the W2-03 cascade's drift-detection contract.
4. **src/.mcp.json is consumed at runtime-config rendering, NOT by TransformPlugin and NOT in Dropped.** The plan distinguishes "silently dropped" (commands, hooks) from "consumed at a different layer" (.mcp.json). The test asserts all three negatives (not copied to dst, not in ExtractedFiles, not in Dropped).
5. **Global-mode Detect signal contributes Low confidence even with zero local signals.** Supports the -g flow per spec §7.3: a workspace with no .codex/ artifacts can still resolve to codex if `$HOME/.codex/` is present.
6. **RED + GREEN collapsed into a single commit** — same precedent as W1-01, W1-02, W3-01 (Deviation #2). The pre-commit hook runs `make unit` repo-wide; CLAUDE.md forbids `--no-verify`. TDD preserved procedurally.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pre-existing flake in `internal/keystore.TestCachedResolverSingleFlight` blocked the first commit attempt**

- **Found during:** Task 1 commit (first attempt). The pre-commit hook runs `make unit` repo-wide; on the first attempt, `TestCachedResolverSingleFlight` in `internal/keystore/` failed. The failure is unrelated to anything in this plan's scope (codex adapter has no keystore dependency).
- **Verification:** Ran the failing test in isolation via `./scripts/dev.sh go test -run TestCachedResolverSingleFlight -count=3 ./internal/keystore/` — passed cleanly on all 3 runs. Confirms timing-sensitive flake parallel to the W1-02 / W3-01 `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` issue (both are `singleflight` tests; same likely root cause).
- **Fix:** Per the SCOPE BOUNDARY rule + W3-01 Deviation #1 precedent, re-attempted the commit with no modifications. Second attempt passed all gates. Logged here for future visibility; root-cause fix is out of scope.
- **Files modified:** None (workflow retry).
- **Committed in:** `13d36cb` (Task 1's eventual commit).

**2. [Rule 3 - Workflow] TDD RED+GREEN collapsed into a single commit**

- **Found during:** Task 1 RED step.
- **Issue:** The plan's `tdd="true"` attribute mandates separate RED (`test(...)`) and GREEN (`feat(...)`) commits per the executor `<tdd_execution>` block. The project's pre-commit hook runs `make unit` over the whole tree; a failing-to-compile RED commit (test files referencing undefined `codex.Adapter`) trips `go vet` and the commit is rejected. CLAUDE.md explicitly forbids `--no-verify`.
- **Fix:** Same resolution as the W1-01 / W1-02 / W3-01 precedents (documented in their respective SUMMARY files). Collapsed RED + GREEN into one atomic commit. TDD preserved procedurally: test file written first with named tests + struct shapes referencing the to-be-built `codex.Adapter`; production code written in the same uncommitted working set; tests + lint gates verified locally before staging.
- **Files modified:** None (workflow trade-off).
- **Verification:** Final test run shows all 18 codex tests + 18 claudecode tests + 16 registry tests passing.
- **Committed in:** `13d36cb` (folded into Task 1 commit).

**3. [Rule 1 - Bug] Initial codex.go + codex_test.go tripped gofmt rewrites**

- **Found during:** Task 1 first `make lint-changed` run.
- **Issue:** Two distinct cosmetic gofmt issues:
  - The codex.go package doc-comment numbered list (`1. ADAPT-07 silent-drop:` / `2. Frontmatter rewrite for src/agents/<name>.md:`) used 7-space continuation indent. gofmt-doc rewrote to 5-space + flatter indent.
  - The codex_test.go test data map literal used multiple spaces to visually align the `// silent-dropped` comments. gofmt `-s` collapsed the alignment.
- **Fix:** Ran `./scripts/dev.sh bash -c "cd /workspace && gofmt -s -w internal/cli/adapter/codex/"`. lint and unit then ran clean.
- **Files modified:** Both codex.go and codex_test.go (in-place gofmt rewrites). Same cosmetic class as W3-01 Deviation #3 and #4.
- **Verification:** `./scripts/dev.sh ./bin/golangci-lint run ./internal/cli/adapter/...` exits 0; all 18 tests still pass post-rewrite.
- **Committed in:** `13d36cb` (folded into Task 1 commit).

**4. [Rule 3 - Blocking] `make lint-changed` skips newly-added directories**

- **Found during:** Task 1 first lint sweep.
- **Issue:** `make lint-changed`'s BASE_REF-vs-HEAD diff strategy only sees files present in the base ref; `internal/cli/adapter/codex/` is brand-new and doesn't appear in origin/main, so `lint-changed` silently skips it (only `./internal/cli/adapter/claudecode/...` and the other older dirs are linted). Same issue noted in W3-01 SUMMARY's Issues Encountered section.
- **Fix:** Ran `./scripts/dev.sh ./bin/golangci-lint run ./internal/cli/adapter/...` directly to verify lint on the new files. The pre-push gate's full `make lint` sweep WOULD catch the new files on push; for the per-plan acceptance test, the explicit per-package run is the right safety net.
- **Files modified:** None (workflow gap, not a code issue).
- **Verification:** Explicit golangci-lint run exits 0.

---

**Total deviations:** 4 (1 Rule 3 blocking — pre-existing out-of-scope flake; 1 Rule 3 workflow — TDD collapse per established precedent; 1 Rule 1 bug — gofmt cosmetic; 1 Rule 3 blocking — lint-changed dir-newness gap).
**Impact on plan:** None on deliverables. All `<acceptance_criteria>` gates pass; all `<verification>` checks pass; all `<success_criteria>` bullets satisfied. The plan's intent (the second adapter + first non-pass-through reference, with the BurntSushi/toml dep landing at the codex boundary as designed, and adapter.go untouched to preserve W3-01's declaration ownership) lands exactly as written.

## Authentication Gates

None. The codex adapter is pure-Go logic with no network, no secret, no external service. The credential propagation pathway (`adapter.CredentialFromContext`) is the only credential surface; it's wrapped via `adapter.WithCredential(ctx, "pk_demo")` in the credential-propagation test.

## Threat Flags

None. The codex adapter introduces:
- No new network endpoints (Adapter contract is pure-function; orchestrator owns HTTP).
- No new auth paths (credential flows in via context.Context, never read from env vars or files).
- No new file-access patterns at trust boundaries (TransformPlugin reads from a staging dir the orchestrator owns; writes to a destination the orchestrator owns; mode discipline forced to 0644 / 0755).
- No new schema surface beyond `.codex/config.toml` — already part of the codex platform's own trust model (the user's own cwd).

The 0644 / 0755 mode discipline + the regular-file-only filter in TransformPlugin REDUCE threat surface vs ad-hoc copy: every file gets a canonical mode, every non-regular entry is silently skipped (defense-in-depth against any future W2 safe-extract regression).

The ADAPT-07 silent-drop discipline is a feature, not a threat: source-tree components codex cannot meaningfully translate (commands, hooks) are explicitly dropped + accumulated for end-of-hydration reporting. The user sees the drop list; nothing slips into the codex output unannounced.

## Known Stubs

None. The codex adapter ships a fully-functional impl of every contract method:
- `RenderRuntime` produces real TOML bytes (not a placeholder)
- `TransformPlugin` does the full distribution + frontmatter rewrite + silent-drop (not a stub)
- `MergeStrategies` returns the documented map (not empty)
- `ResolveOutputContent` round-trips the actual render bytes (not nil)

The frontmatter rewrite map is intentionally narrowed at v1alpha1 to `tools:` → `allowed_tools:` only; the full spec §7.4 mapping (model shortname expansion, permissions object → permissionMode string) is **deferred to a future release** with the rationale documented in the code comment + key-decisions frontmatter above. This is a deliberate scope decision, not a stub — the test suite asserts the v1alpha1 rewrite shape and other keys pass through verbatim.

## Issues Encountered

- `internal/keystore.TestCachedResolverSingleFlight` is a pre-existing transient flake (parallel to W1-02 / W3-01's `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` — both `singleflight` tests). Reproduced once on the first Task 1 commit attempt; resolved by retry. Out of scope for this plan; the flake has now hit multiple consecutive Phase 7 worktree spawns and should be tracked as a deferred item.
- `make lint-changed`'s BASE_REF-vs-HEAD diff strategy does NOT see newly-added directories (it only lints files present in the base ref). To verify lint on this plan's new files, ran `./scripts/dev.sh ./bin/golangci-lint run ./internal/cli/adapter/...` directly. The pre-push gate's full `make lint` sweep will catch the new files at push time; for per-plan atomic commits, the explicit per-package run is the safety net.
- `.planning/` is gitignored at the repo level, so this SUMMARY.md is not git-trackable. Per the same posture as W3-01, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path; no `docs(...)` follow-up commit is possible without `-f`-style force-stage (forbidden).

## User Setup Required

None. All changes are repo-internal Go code + a single new go.mod direct dep (`github.com/BurntSushi/toml v1.6.0`). No external services, no secrets, no schema migrations. The dep download fired during `go get` + `go mod tidy` ran clean inside the devtools container.

## Self-Check

```
# Worktree file existence
[ -f internal/cli/adapter/codex/codex.go ]            → FOUND (724 lines)
[ -f internal/cli/adapter/codex/codex_test.go ]       → FOUND (614 lines)

# Gitignored SUMMARY (main-repo .planning/)
[ -f /home/jcm/Projects/ach/.planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W3-02-SUMMARY.md ] → FOUND

# Commit existence
git log --oneline -3 | grep 13d36cb → FOUND ("feat(07-W3-02): add codex adapter — TOML merge + plugin distribution + frontmatter rewrite")

# Plan-level acceptance gates
grep -q "package codex" internal/cli/adapter/codex/codex.go                                           → OK
grep -q "\"codex\"" internal/cli/adapter/codex/codex.go                                               → OK  (canonical ID literal)
grep -q "\"codex-cli\"" internal/cli/adapter/codex/codex.go                                           → OK  (alias literal)
grep -q "adapter.Register" internal/cli/adapter/codex/codex.go                                        → OK  (init registration)
grep -q "adapter.CredentialFromContext" internal/cli/adapter/codex/codex.go                           → OK  (ADAPT-03 ctx-keyed credential)
grep -q "BurntSushi/toml" internal/cli/adapter/codex/codex.go                                         → OK  (TOML encoder import)
grep -q ".codex/config.toml" internal/cli/adapter/codex/codex.go                                      → OK  (runtime-config target literal)
grep -q "Dropped" internal/cli/adapter/codex/codex.go                                                 → OK  (PluginWrite.Dropped consumed)
grep -q "BurntSushi/toml" go.mod                                                                      → OK  (direct dep)
git status --porcelain | grep -q " M internal/cli/adapter/adapter.go"                                 → NOT FOUND (adapter.go untouched — W3-01 boundary preserved)

# Test name presence in codex_test.go
func TestRenderRuntime_TomlShape                                                                      → OK
func TestTransformPlugin_DistributesPrompts                                                           → OK
func TestTransformPlugin_FrontmatterRewrite_AgentsKeys                                                → OK
func TestRenderRuntime_CredentialPropagation                                                          → OK

# Plan-level verification gates
./scripts/dev.sh make unit-pkg PKG=./internal/cli/adapter/...                                         → exit 0  (18 codex tests + 18 claudecode tests + 16 registry tests all pass)
./scripts/dev.sh ./bin/golangci-lint run ./internal/cli/adapter/...                                   → exit 0  (lint clean)
```

## Self-Check: PASSED

## Next Phase Readiness

- **07-W3-03 (gemini-cli):** can land in parallel. References `adapter.Adapter` interface + `adapter.WithCredential` / `CredentialFromContext` + `adapter.PluginWrite.Dropped` — all declared in W3-01, unchanged by this plan. Will add `internal/cli/adapter/gemini/`. Runtime-config target is `.gemini/settings.json` (JSON); no new go.mod entry — encoding/json is stdlib. Plugin transformation per spec §7.4 gemini-cli: same shape as codex but MCP merge target is `mcpServers` key in JSON; agent frontmatter rewriting follows Gemini's schema; `.lsp.json`, `hooks/`, `monitors/`, `bin/` silently dropped.
- **07-W3-04 (opencode):** can land in parallel. Same shape as gemini-cli but target is `.opencode/opencode.json` under the `mcp` key per OpenCode's config format; agent frontmatter rewriting follows OpenCode's schema.
- **07-W3-05 (cobra wiring + autodetection):** depends on all four W3-* plans landing. Will blank-import each subpackage (`_ "github.com/ackstorm/ach/internal/cli/adapter/{claudecode,codex,gemini,opencode}"`) to fire each `init()`; will call `adapter.Iter()` for autodetection; will call `adapter.Lookup(--platform)` for explicit selection; will call `adapter.WithCredential` to wrap the bearer before invoking `RenderRuntime`.
- **07-W2-03 (extract/autoclaim — SAFE-04 cascade):** codex's `ResolveOutputContent` now provides the Tier 2 ContentResolver for the `.codex/config.toml` target; `(nil, nil)` for everything else (Tier 3 source-byte read takes over). The other two non-claude adapters (W3-03/W3-04) will follow the same posture for their `.gemini/settings.json` and `.opencode/opencode.json` targets.
- **07-W4-01 (e2e):** the 4-platform Core Value path can drive `codex` end-to-end against the kept kind cluster as soon as the orchestrator (W3-05) lands. The codex adapter's TOML emission + plugin distribution + silent-drop accounting are the canonical non-pass-through reference; gemini-cli + opencode e2e tests will be deltas against this baseline (plus claudecode pass-through).

No blockers. The codex adapter is structurally complete; W3-03 and W3-04 can ship in parallel without touching this plan's surface.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
