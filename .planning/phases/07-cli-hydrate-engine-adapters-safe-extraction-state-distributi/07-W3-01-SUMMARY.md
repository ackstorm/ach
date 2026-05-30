---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W3-01
subsystem: cli
tags: [adapter, registry, claudecode, pass-through, mergekind, plugin-write-dropped, credential-context, phase-7-foundation, ADAPT-01, ADAPT-03, ADAPT-04, ADAPT-05, ADAPT-06, ADAPT-07, SAFE-04]

# Dependency graph
requires:
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-01
    provides: "exit code constants (referenced from CONTEXT for orchestrator wiring; no direct import in this plan — Adapter contract stays import-cycle-free)."
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-02
    provides: "internal/cli/state.File / FileEntry / AdapterSection types — RenderRuntime takes *state.File as the prior-state read input; AdapterSection.Files is the projection target the orchestrator writes after stage+publish."
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-04
    provides: "internal/cli/hash xxh3:<hex> contract — adapter outputs are hashed by the orchestrator (not by the adapter itself), so this plan has no direct import on hash; consumed downstream when adapter outputs flow into FileEntry.Hash / FileEntry.SourceHash."
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-05
    provides: "internal/cli/manifest.Manifest + RuntimeBlock + ContextBlock + ContentRef (incl. ContentRef.Endpoint) — RenderRuntime consumes m.Runtime.MCPServers[i].Endpoint per ADAPT-03 for runtime-config URL construction without re-fetching."
provides:
  - "internal/cli/adapter.Adapter interface (7 methods per CONTEXT.md D-07) + MergeKind enum (MergeDeep / MergeComposite / MergeReplace per spec §7.1 + ADAPT-05) + Confidence enum"
  - "internal/cli/adapter.Match / FileWrite / PluginWrite types — PluginWrite.Dropped []string DECLARED HERE (ADAPT-07) so W3-02/03/04 populate it without racing modifications to adapter.go"
  - "internal/cli/adapter.WithCredential / CredentialFromContext typed-context-key helpers (ADAPT-03) — credentialKey struct{} unexported; adapters never read env vars directly"
  - "internal/cli/adapter.Register / Lookup / Iter registry — case-folded canonical+alias lookup, panic-on-duplicate init-time wiring"
  - "internal/cli/adapter/claudecode.Adapter pass-through reference impl (D-05): RenderRuntime emits .claude/.mcp.json from manifest.Runtime entries; TransformPlugin is a verbatim copy; ResolveOutputContent recomputes the .mcp.json bytes for the SAFE-04 cascade Tier 2"
affects: [07-W3-02, 07-W3-03, 07-W3-04, 07-W3-05, 07-W2-03, 07-W4-01]

# Tech tracking
tech-stack:
  added: []  # stdlib-only; zero new go.mod entries
  patterns:
    - "Typed-int enum discipline (MergeKind, Confidence) — iota+1 so the zero value is invalid; a forgotten Merge field on a FileWrite trips downstream gates rather than silently defaulting"
    - "Context-keyed credential propagation via unexported struct{} key type — namespace-isolated from any string-keyed value; never logged, never exported, accessed only via WithCredential / CredentialFromContext"
    - "Registry as init-side-effect: each adapter subpackage carries init() that calls adapter.Register(&Adapter{}); the cobra layer (plan 07-W3-05) blank-imports each subpackage so registrations fire before main() reaches Lookup/Iter"
    - "Panic-on-duplicate at Register: init-time misconfiguration is a program bug, not recoverable error; the panic surfaces immediately at process start instead of silently shadowing one adapter with another"
    - "Pass-through plugin discipline: claudecode's TransformPlugin walks src and copies regular files verbatim at mode 0644; symlinks/devices/FIFOs are skipped (defense-in-depth — the W2-01 safe-extract layer is the primary filter)"
    - "Deterministic JSON output for ResolveOutputContent contract: encoding/json sorts map keys lexicographically, so the same manifest + credential always yields byte-identical .mcp.json bytes — the SAFE-04 Tier 2 cascade gets a clean byte-equal comparison"

key-files:
  created:
    - "internal/cli/adapter/doc.go (79 lines — package doc citing ADAPT-01/03/04/05/06/07 + D-05/D-07/D-08/D-09 + credential context-key discipline + SAFE-04 ResolveOutputContent contract)"
    - "internal/cli/adapter/adapter.go (219 lines — Adapter interface + MergeKind/Confidence enums + Match/FileWrite/PluginWrite (incl. Dropped) types + WithCredential/CredentialFromContext helpers + credentialKey unexported type)"
    - "internal/cli/adapter/registry.go (138 lines — Register + Lookup + Iter + resetForTesting; sync.RWMutex-guarded canonical map + alias index; panic on duplicate ID/alias/nil/empty)"
    - "internal/cli/adapter/registry_test.go (245 lines — 16 tests: dup-panic, dup-alias panic, nil/empty rejection, case-insensitive canonical + alias lookup, empty-input rejection, Iter snapshot, credential round-trip, empty-bearer + nil-context defense, flat-namespace alias-vs-canonical-ID collision rejection)"
    - "internal/cli/adapter/claudecode/claudecode.go (260 lines — Adapter struct + ID/Aliases + Detect (4 signals → Low/Medium/High) + RenderRuntime (deterministic .mcp.json with mcpServers + a2aAgents maps + x-ach-key header) + TransformPlugin (filepath.WalkDir pass-through + 0644 mode discipline) + MergeStrategies + ResolveOutputContent (Tier 2 recompute for .mcp.json target; nil-nil for everything else) + init() Register)"
    - "internal/cli/adapter/claudecode/claudecode_test.go (~540 lines after gofmt — 18 tests covering all 7 methods + edge cases: zero/low/medium/high Detect; credential propagation; empty-runtime emits empty mcpServers; nil-manifest error; pass-through file copy with byte-identical content + 0644 mode; empty src / empty paths; ResolveOutputContent round-trip + nil/unknown-target safety; registry-on-import via Lookup)"
  modified: []

key-decisions:
  - "PluginWrite.Dropped []string declared in adapter.go HERE (not in W3-02/03/04) per the plan's <must_haves.truths> + <objective>. This is the structural prerequisite for the three sibling adapter plans to land in parallel — each populates Dropped without ever touching adapter.go. ADAPT-07 silent-drop accounting is therefore wave-level coordinated, not plan-level racing."
  - "Aliases for claude-code = ['claude', 'cc'] per the plan's <must_haves.truths>, NOT spec §7.2's ['claudecode', 'claude']. The plan is the canonical contract for this work; spec §7.2 carries 'claudecode' as the primary alias but the plan deliberately picks the shorter pair (incl. 'cc') for terseness. Aliases are case-folded at Register + Lookup so 'CLAUDE', 'Claude', 'CC' all resolve correctly. Note for W3-05 cobra layer: when stamping `--platform` help text, list canonical-ID + aliases verbatim from a.Aliases() so users see what works."
  - "The on-disk credential discipline in .claude/.mcp.json uses headers map with x-ach-key key. When the credential is empty (offline / unit-test / dry-run that didn't wrap ctx), the header is emitted with empty value. The orchestrator (plan 07-W3-05) is responsible for gating whether to attempt the write at all — claudecode is pure rendering, not credential validation."
  - "claudecode's A2A shape mirrors the MCP server shape (type=http, url, headers) under a parallel 'a2aAgents' top-level key. Spec §7.4 claude-code does NOT pin a fixed A2A shape (Claude Code's A2A support is recent and evolving); we picked the symmetric shape so the JSON round-trip is the same shape on both sides. If the upstream contract solidifies on something else, this adapter is the only impl that needs to change — the interface stays stable."
  - "ResolveOutputContent returns (nil, nil) for any target != .claude/.mcp.json. The SAFE-04 cascade Tier 3 (plan 07-W2-03) then reads source bytes verbatim — which is the right behavior for pass-through plugin files (claudecode's TransformPlugin already emits source bytes verbatim, so the staging dir bytes ARE the canonical bytes). Other targets returning bytes would force the cascade to recompute when source-byte read suffices."
  - "RED+GREEN collapsed into a single per-task commit (same precedent as W1-01 and W1-02). The project's pre-commit hook runs `make unit` over the whole tree; a failing-to-compile RED test would fail go vet and reject the commit. CLAUDE.md explicitly forbids --no-verify. Per the established precedent, TDD discipline is preserved procedurally: test stubs were written first against the (unbuilt) Adapter interface, build failure verified via `./scripts/dev.sh go test ./internal/cli/adapter/...` (undefined: type Adapter, type FileWrite, type PluginWrite, etc.), only then was the impl added, then the combined diff staged and committed."

patterns-established:
  - "Adapter contract is import-cycle-free: internal/cli/adapter imports only manifest + state (data types, no I/O). Adapters in subpackages import the parent package + manifest + state. The orchestrator (plan 07-W3-05 cobra layer) is the ONLY place where adapter + extract + hydrate compose. This keeps every adapter unit-testable without spinning up the orchestrator."
  - "Typed-context-key idiom for credential propagation: define unexported `type credentialKey struct{}`; `WithCredential(ctx, v)` does `context.WithValue(ctx, credentialKey{}, v)`; `CredentialFromContext(ctx)` does `ctx.Value(credentialKey{}).(string)` with comma-ok and nil-context defense. This is the pattern future plans should use for any per-request flag that must propagate through Adapter calls without polluting the function-signature space."
  - "Deterministic JSON output for SAFE-04 byte-equality: when the cascade needs to compare two rendered outputs, the rendering MUST be deterministic across invocations. encoding/json's lexicographic map-key sort handles this for free — but only if the adapter does not introduce non-deterministic iteration order via `map[K]V` mutation. claudecode's renderMcpJSON builds the map then encodes it once; iteration order at encode time is sorted; the SAFE-04 cascade gets a clean byte-equal compare."

requirements-completed:
  - ADAPT-01  # 4-adapter compile + alias resolution — claude-code (this plan); codex/gemini-cli/opencode in W3-02/03/04
  - ADAPT-03  # Runtime config rendering — .claude/.mcp.json via RenderRuntime + credential context propagation
  - ADAPT-04  # Plugin canonical format = Claude Code — claudecode pass-through is the canonical reference
  - ADAPT-05  # Merge strategies — MergeDeep for .claude/.mcp.json; Keys tracking for inverse-merge
  - ADAPT-06  # Adapter scope rule — claudecode emits ONLY .claude/-prefixed paths
  - ADAPT-07  # Silent-drop accounting — PluginWrite.Dropped declared here; claudecode returns nil (no drops); siblings populate
  - SAFE-04   # ResolveOutputContent Tier 2 contract — claudecode round-trips its render bytes

# Note: ADAPT-01 is PARTIALLY addressed by this plan — the closed-set
# 4-adapter contract is structurally in place (interface + registry +
# alias resolution), but only 1 of 4 adapters ships here (claude-code).
# Plans 07-W3-02 (codex), 07-W3-03 (gemini-cli), 07-W3-04 (opencode)
# complete the set. Each will add a new subpackage WITHOUT touching
# this plan's adapter.go (the PluginWrite.Dropped + WithCredential
# declarations are the parallelism-enabler).

# Metrics
duration: ~22min
completed: 2026-05-29
---

# Phase 7 Plan 07-W3-01: Adapter Contract + Registry + Claudecode Reference Adapter Summary

**Stdlib-only adapter package: 7-method Adapter interface + typed MergeKind / Confidence enums + PluginWrite.Dropped silent-drop accounting + context-keyed credential propagation + panic-on-duplicate init-registered registry, plus the claudecode pass-through reference impl that emits deterministic .claude/.mcp.json from manifest.Runtime entries — the parallelism-enabler for the three sibling adapter plans (07-W3-02/03/04).**

## Performance

- **Duration:** ~22 min
- **Started:** 2026-05-29T14:34:29Z (worktree spawn after HEAD reset)
- **Completed:** 2026-05-29T14:56:57Z
- **Tasks:** 2 (both `auto` / `tdd=true`)
- **Files created:** 6 (3 source + 1 doc.go + 2 test files; all tracked)
- **Tracked commits:** 2 (`38fa9d1`, `f0741ee`)
- **Tests added:** 34 (16 in registry_test.go + 18 in claudecode_test.go), all passing under `-race`
- **Lines of code:** ~1,484 total (681 in Task 1 + 803 in Task 2)

## Accomplishments

- `internal/cli/adapter/adapter.go` exports the 7-method `Adapter` interface verbatim per CONTEXT.md D-07: `ID() string`, `Aliases() []string`, `Detect(root string) (Match, error)`, `RenderRuntime(ctx, *manifest.Manifest, *state.File) ([]FileWrite, error)`, `TransformPlugin(ctx, src, dst string) (PluginWrite, error)`, `MergeStrategies() map[string]MergeKind`, `ResolveOutputContent(ctx, *manifest.Manifest, target string) ([]byte, error)`.
- `MergeKind` typed-int enum with `MergeDeep` (iota+1), `MergeComposite`, `MergeReplace` per CLI spec §7.1 + ADAPT-05; `Confidence` typed-int enum with `ConfidenceLow` / `ConfidenceMedium` / `ConfidenceHigh` for autodetection ranking. Zero values intentionally invalid so a forgotten field on a FileWrite trips downstream gates.
- `Match` / `FileWrite` / `PluginWrite` structs. `PluginWrite.Dropped []string` is declared HERE in adapter.go so the three sibling adapter plans (07-W3-02 codex, 07-W3-03 gemini, 07-W3-04 opencode) populate it without racing modifications to this file — that is the structural prerequisite for parallel wave-3 execution.
- `WithCredential(ctx, bearer string) context.Context` + `CredentialFromContext(ctx) string` helpers backed by an unexported `credentialKey struct{}` typed context key. Adapters consume the bearer via `CredentialFromContext`; they never read env vars directly. The struct{}-typed key cannot collide with any other package's string-keyed values (namespace-isolated).
- `internal/cli/adapter/registry.go` exports `Register(a Adapter)` (panics on nil / empty ID / duplicate canonical ID / duplicate alias / alias-colliding-with-existing-canonical-ID), `Lookup(id string) (Adapter, bool)` (case-folded canonical + alias resolution), `Iter() []Adapter` (snapshot for autodetection). sync.RWMutex-guarded; init-time-only population in production (the unexported `resetForTesting` is test-only).
- `internal/cli/adapter/doc.go` documents the 4-adapter closed set, the registry init-side-effect pattern, the credential context-key discipline, the SAFE-04 ResolveOutputContent contract, and the ADAPT-06 scope rule.
- `internal/cli/adapter/claudecode/claudecode.go` ships the pass-through reference impl: `ID()` returns `"claude-code"`; `Aliases()` returns `["claude", "cc"]`; `Detect(root)` scans 4 signals and ranks Low/Medium/High by signal count; `RenderRuntime` emits a single `.claude/.mcp.json` FileWrite with `mcpServers` + `a2aAgents` maps (each entry: `type="http"`, `url=ContentRef.Endpoint`, `headers={"x-ach-key": <cred>}`); `TransformPlugin` walks the src tree with `filepath.WalkDir`, copies regular files verbatim at mode 0644 (SAFE-02 mirror), returns `PluginWrite{ExtractedFiles: [...], Dropped: nil}`; `MergeStrategies` returns `{".claude/.mcp.json": MergeDeep}`; `ResolveOutputContent` recomputes the `.mcp.json` bytes for the matched target (Tier 2 cascade) and returns `(nil, nil)` for any other target (Tier 3 source-byte read takes over).
- `init()` in `claudecode.go` calls `adapter.Register(&Adapter{})`; the registry test in the registry package + the `TestRegistry_RegistersOnImport` test in the claudecode package both verify the registration fires correctly.
- 34 tests pass under `-race`: 16 in registry_test.go (Register dup/nil/empty-ID/alias-collision panic; Lookup canonical/alias case-insensitive + unknown + empty input; Iter snapshot; credential round-trip + empty-bearer + nil-context defense; flat-namespace alias-vs-canonical-ID rejection) and 18 in claudecode_test.go (ID; Aliases shape; Detect 0/1/2/3+ signals; RenderRuntime shape + credential propagation + empty-runtime + nil-manifest defense; TransformPlugin pass-through with byte-identical content + 0644 mode + empty-src + empty-paths; MergeStrategies; ResolveOutputContent round-trip + unknown-target + nil-manifest; registry-on-import via Lookup).
- Stdlib-only discipline verified: `grep -E '^\s*"(log|log/slog|gopkg\.in/yaml)' internal/cli/adapter/**/*.go` returns 0 matches. Only stdlib imports: `bytes`, `context`, `encoding/json`, `errors`, `fmt`, `io`, `os`, `path/filepath`, `sort`, `strings`, `sync`, `testing` — plus the in-repo `internal/cli/{adapter,manifest,state}` siblings.
- Zero new `go.mod` entries.
- golangci-lint clean across `./internal/cli/adapter/...` after one gofmt-induced re-format pass (initial doc.go used a `+` mid-list-item that gofmt-doc treated as a literal list marker; rewritten to use `and`).

## Task Commits

Each task was committed atomically with full pre-commit gate (lint-changed + make unit):

1. **Task 1: adapter contract + registry + credential helpers** — `38fa9d1` (`feat`). 4 files / 681 insertions. First commit attempt tripped a pre-existing flake in `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` under `-race` (identical to the W1-02 SUMMARY's noted flake — out of scope per SCOPE BOUNDARY rule). Re-attempt landed clean.
2. **Task 2: claudecode pass-through reference adapter** — `f0741ee` (`feat`). 2 files / 803 insertions. Single attempt passed all gates.

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored at repo level). Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path.

## Files Created/Modified

| Path | Lines | Role |
|------|-------|------|
| `internal/cli/adapter/doc.go` | 79 | Package doc — 4-adapter closed set, ADAPT-01/03/04/05/06/07 citations, credential discipline |
| `internal/cli/adapter/adapter.go` | 219 | Adapter interface + MergeKind/Confidence enums + Match/FileWrite/PluginWrite (incl. Dropped) + WithCredential/CredentialFromContext + credentialKey |
| `internal/cli/adapter/registry.go` | 138 | Register + Lookup + Iter; sync.RWMutex-guarded; panic-on-duplicate |
| `internal/cli/adapter/registry_test.go` | 245 | 16 tests — registry behavior + credential helpers |
| `internal/cli/adapter/claudecode/claudecode.go` | 260 | Pass-through reference Adapter — 7 methods + init() Register |
| `internal/cli/adapter/claudecode/claudecode_test.go` | ~540 (after gofmt) | 18 tests — ID/Aliases/Detect/RenderRuntime/TransformPlugin/MergeStrategies/ResolveOutputContent/registration |
| **Total** | **~1,484** | **6 files** |

## Decisions Made

See `key-decisions` in frontmatter. Summary:

1. **`PluginWrite.Dropped` declared in this plan, not W3-02/03/04.** This is the parallelism-enabler per the plan's `<must_haves.truths>`: the three sibling adapter plans land independently, each populating the field, no plan touches adapter.go after this one.
2. **Aliases are `["claude", "cc"]`** per the plan contract, NOT spec §7.2's `["claudecode", "claude"]`. The plan is canonical; future cobra `--platform` help text should reflect this.
3. **Credential discipline:** `WithCredential` / `CredentialFromContext` with unexported `credentialKey struct{}` typed key. Adapters never read env vars. Orchestrator wraps ctx once before invoking RenderRuntime.
4. **A2A shape mirrors MCP shape** under a parallel `a2aAgents` key, because spec §7.4 claude-code does not pin a fixed A2A contract. Future upstream evolution is contained inside the claudecode impl.
5. **`ResolveOutputContent` returns `(nil, nil)` for non-runtime targets** so the SAFE-04 Tier 3 source-byte read takes over for pass-through plugin files — those bytes ARE the canonical bytes (claudecode's TransformPlugin is byte-identical to the staging dir).
6. **RED+GREEN collapsed into single per-task commits** — same precedent as W1-01 and W1-02. The pre-commit hook runs `make unit`; a strict-RED commit would fail go vet; CLAUDE.md forbids `--no-verify`. TDD preserved procedurally (test stubs first, undefined-symbol build failure verified, then impl + combined commit).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pre-existing flake in `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` blocked the first commit of Task 1**

- **Found during:** Task 1 commit (first attempt)
- **Issue:** The pre-commit hook runs `make unit` repo-wide. On the first commit attempt, `TestGet_Singleflight_DedupesConcurrentMisses` in `internal/contentservice/envcache/` failed under `-race`. The failure is unrelated to anything in this plan's scope.
- **Verification:** Ran the failing test in isolation via `./scripts/dev.sh go test -run TestGet_Singleflight_DedupesConcurrentMisses -count=3 ./internal/contentservice/envcache/` — passed cleanly. Confirms identical timing-sensitive flake noted in `07-W1-02-SUMMARY.md` Deviation #1.
- **Fix:** Per the SCOPE BOUNDARY rule, re-attempted the commit with no modifications. Second attempt passed all gates including the previously-flaky test. Logged here for future visibility; root-cause fix is out of scope for this plan.
- **Files modified:** None (workflow retry).
- **Committed in:** `38fa9d1` (Task 1's eventual commit).

**2. [Rule 3 - Workflow] TDD RED+GREEN collapsed into single per-task commits**

- **Found during:** Both Task 1 and Task 2 RED steps.
- **Issue:** The plan's `tdd="true"` attribute mandates separate RED (`test(...)`) and GREEN (`feat(...)`) commits per the executor `<tdd_execution>` block. The project's `pre-commit` hook runs `make unit` over the whole tree; a failing-to-compile RED commit (test files referencing undefined `adapter.Adapter`, `adapter.WithCredential`, etc.) trips `go vet` and the commit is rejected. CLAUDE.md explicitly forbids `--no-verify`.
- **Fix:** Same resolution as the W1-01 and W1-02 precedents (documented in their respective SUMMARY files). Collapsed RED + GREEN into one atomic commit per task. TDD preserved procedurally: test stubs written first; build failure verified locally (`undefined: adapter.Adapter`, etc.); impl files written; combined diff staged and committed.
- **Files modified:** None (workflow trade-off).
- **Verification:** Both tasks' GREEN test runs after impl show all sub-tests passing (`--- PASS: TestRegister_Duplicate_Panics` through `--- PASS: TestRegistry_RegistersOnImport`).
- **Committed in:** `38fa9d1` (Task 1) and `f0741ee` (Task 2).

**3. [Rule 1 - Bug] Initial `doc.go` tripped gofmt-doc's interpretation of `+ spec §7.5` as a literal list-item marker**

- **Found during:** Task 1 first lint run (`./bin/golangci-lint run ./internal/cli/adapter/...`).
- **Issue:** The initial doc.go contained the bullet:
  ```
  //   - Autodetection logic (zero/one/multi-match outcomes per ADAPT-02
  //     + spec §7.5) lives in the cobra layer at plan 07-W3-05; this
  //     ...
  ```
  gofmt's doc-comment heuristic saw the leading `+` on the continuation line and tried to rewrite the indentation, producing a diff against the gofmt-canonical form. Lint reported `File is not gofmt-ed with -s`.
- **Fix:** Replaced `+ spec §7.5` with `and spec §7.5` (and a similar replacement two lines down where `+ state alone` became `and state alone`). gofmt and lint now pass clean.
- **Files modified:** `internal/cli/adapter/doc.go` (in-place edit, pre-commit).
- **Verification:** `./scripts/dev.sh bash -c "cd /workspace && gofmt -s -d internal/cli/adapter/"` returns no diff; `./bin/golangci-lint run ./internal/cli/adapter/...` exits 0.
- **Committed in:** `38fa9d1` (folded into Task 1 commit).

**4. [Rule 1 - Bug] claudecode_test.go alignment-spacing-induced gofmt diff**

- **Found during:** Task 2 first lint run.
- **Issue:** A test map literal used multiple spaces to align the values column visually; gofmt's `-s` simplifier collapsed the alignment. Same `File is not gofmt-ed with -s` lint failure.
- **Fix:** Ran `./scripts/dev.sh bash -c "cd /workspace && gofmt -s -w internal/cli/adapter/claudecode/"` to apply the canonical formatting. The map literal is now single-space-aligned.
- **Files modified:** `internal/cli/adapter/claudecode/claudecode_test.go` (in-place rewrite by gofmt).
- **Verification:** `./bin/golangci-lint run ./internal/cli/adapter/...` exits 0; tests still pass.
- **Committed in:** `f0741ee` (folded into Task 2 commit).

---

**Total deviations:** 4 (1 Rule 3 blocking — pre-existing out-of-scope flake; 1 Rule 3 workflow — TDD collapse per established precedent; 2 Rule 1 bugs — gofmt cosmetic fixes caught at lint).
**Impact on plan:** None on deliverables. All `<acceptance_criteria>` gates pass for both tasks; all `<verification>` checks pass; all `<success_criteria>` bullets satisfied. The plan's intent (the adapter contract + claudecode pass-through reference + PluginWrite.Dropped + credential helpers — the parallelism-enabler for W3-02/03/04) lands exactly as written.

## Threat Flags

None. The adapter package introduces:
- No new network endpoints (Adapter contract is pure-function; orchestrator owns HTTP).
- No new auth paths (credential flows in via context.Context, never read from env vars or files).
- No new file-access patterns at trust boundaries (TransformPlugin reads from a staging dir the orchestrator owns; writes to a destination the orchestrator owns; mode discipline forced to 0644 / 0755).
- No new schema surface beyond `.claude/.mcp.json` — already part of the claude-code platform's own trust model (the user's own cwd).

The pass-through write discipline REDUCES threat surface vs ad-hoc copy: every file gets a 0644 mode, every non-regular entry is silently skipped (defense-in-depth against any future W2 safe-extract regression).

## Issues Encountered

- `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` is a pre-existing flake (also documented in 07-W1-02 SUMMARY). Reproduced once on the first Task 1 commit attempt; resolved by retry. Out of scope for this plan but worth flagging — the flake has now hit two consecutive Phase 7 worktree spawns, so it should be tracked as a deferred item.
- The `make lint-changed` target's BASE_REF-vs-HEAD diff strategy does NOT see newly-added directories (it only lints files present in the base ref). To verify lint on this plan's new files, ran `./scripts/dev.sh bash -c "cd /workspace && ./bin/golangci-lint run ./internal/cli/adapter/..."` directly. The pre-commit hook's full `make lint` sweep would catch the new files on a future commit cycle; for this plan's atomic commits, the explicit per-package run is the safety net.
- `.planning/` is gitignored at the repo level, so this SUMMARY.md is not git-trackable. Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path. No `docs(...)` follow-up commit is possible without `-f`-style force-stage (which is forbidden).

## User Setup Required

None. All changes are repo-internal Go code. No external services, no secrets, no schema migrations, no new go.mod entries.

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/adapter/doc.go ]                                  → FOUND
[ -f internal/cli/adapter/adapter.go ]                              → FOUND
[ -f internal/cli/adapter/registry.go ]                             → FOUND
[ -f internal/cli/adapter/registry_test.go ]                        → FOUND
[ -f internal/cli/adapter/claudecode/claudecode.go ]                → FOUND
[ -f internal/cli/adapter/claudecode/claudecode_test.go ]           → FOUND

# Gitignored SUMMARY (main-repo .planning/)
[ -f /home/jcm/Projects/ach/.planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W3-01-SUMMARY.md ] → FOUND

# Commit existence
git log --oneline -5 | grep 38fa9d1 → FOUND ("feat(07-W3-01): add adapter contract + registry + credential helpers")
git log --oneline -5 | grep f0741ee → FOUND ("feat(07-W3-01): add claudecode pass-through reference adapter")

# Plan-level acceptance gates (Task 1)
grep -q "type Adapter interface" internal/cli/adapter/adapter.go                                  → OK
grep -E "ID\(\) string|Aliases\(\) \[\]string|Detect\(|RenderRuntime\(|TransformPlugin\(|MergeStrategies\(|ResolveOutputContent\(" internal/cli/adapter/adapter.go | wc -l → 8 (≥7)
grep -E "MergeDeep|MergeComposite|MergeReplace" internal/cli/adapter/adapter.go | wc -l           → 9 (all three constants present multiple times: const decl + doc + usage)
grep -q "type PluginWrite struct" internal/cli/adapter/adapter.go                                 → OK
grep -q "Dropped *\[\]string" internal/cli/adapter/adapter.go                                     → OK  (ADAPT-07 silent-drop)
grep -q "func WithCredential" internal/cli/adapter/adapter.go                                     → OK
grep -q "func CredentialFromContext" internal/cli/adapter/adapter.go                              → OK
grep -q "func Register" internal/cli/adapter/registry.go                                          → OK
grep -q "func Lookup" internal/cli/adapter/registry.go                                            → OK
grep -q "func Iter" internal/cli/adapter/registry.go                                              → OK
registry_test.go contains TestRegister_Duplicate_Panics                                           → OK
registry_test.go contains TestLookup_ByAlias_CaseInsensitive                                      → OK
registry_test.go contains TestWithCredential_RoundTrips                                           → OK

# Plan-level acceptance gates (Task 2)
grep -q "package claudecode" internal/cli/adapter/claudecode/claudecode.go                        → OK
grep -q "ID() string" internal/cli/adapter/claudecode/claudecode.go                               → OK
grep -q "claude-code" internal/cli/adapter/claudecode/claudecode.go                               → OK  (canonical ID literal)
grep -E "\"claude\"|\"cc\"" internal/cli/adapter/claudecode/claudecode.go                         → OK  (alias literals)
grep -q "adapter.Register" internal/cli/adapter/claudecode/claudecode.go                          → OK  (init registration)
grep -q "adapter.CredentialFromContext" internal/cli/adapter/claudecode/claudecode.go             → OK  (ADAPT-03 ctx-keyed credential)
grep -qE "\\.claude/\\.mcp\\.json|.claude/.mcp.json" internal/cli/adapter/claudecode/claudecode.go → OK
grep -q "func.*RenderRuntime" internal/cli/adapter/claudecode/claudecode.go                       → OK
grep -q "func.*ResolveOutputContent" internal/cli/adapter/claudecode/claudecode.go                → OK
claudecode_test.go contains TestRenderRuntime_EmitsMcpJson                                        → OK
claudecode_test.go contains TestClaudeCode_Detect_AllSignals_HighConfidence                       → OK
claudecode_test.go contains TestResolveOutputContent_McpJson_RoundTrips                           → OK
claudecode_test.go contains TestRenderRuntime_CredentialPropagation                               → OK

# Plan-level verification gates
./scripts/dev.sh make unit-pkg PKG=./internal/cli/adapter/...                                     → exit 0  (34 tests pass under -race)
./scripts/dev.sh bash -c "cd /workspace && ./bin/golangci-lint run ./internal/cli/adapter/..."    → exit 0  (lint clean)
grep -E '^\s*"(log|log/slog|gopkg\.in/yaml)' internal/cli/adapter/**/*.go                         → 0  (stdlib-only)
```

## Self-Check: PASSED

## Next Phase Readiness

- **07-W3-02 (codex):** can land in parallel. References `adapter.Adapter` interface + `adapter.WithCredential` / `CredentialFromContext` + `adapter.PluginWrite.Dropped` — all declared here. Adds a new subpackage at `internal/cli/adapter/codex/` with `init() → adapter.Register(&Adapter{})`. Will introduce `github.com/BurntSushi/toml` as the first new go.mod entry in the adapter wave.
- **07-W3-03 (gemini-cli):** can land in parallel. Stdlib-only (encoding/json reuse). Adds `internal/cli/adapter/gemini/`.
- **07-W3-04 (opencode):** can land in parallel. Stdlib-only. Adds `internal/cli/adapter/opencode/`.
- **07-W3-05 (cobra wiring + autodetection):** depends on all four W3-* plans. Will blank-import each subpackage to fire its `init()`; will call `adapter.Iter()` for autodetection; will call `adapter.Lookup(--platform)` for explicit selection; will call `adapter.WithCredential` to wrap the bearer before invoking `RenderRuntime`.
- **07-W2-03 (extract/autoclaim — SAFE-04 cascade):** can now reference `adapter.Adapter.ResolveOutputContent` as the Tier 2 ContentResolver. Claudecode's `ResolveOutputContent` returns the bytes RenderRuntime would emit for `.claude/.mcp.json` and `(nil, nil)` for any other target — the cascade falls through to Tier 3 source-byte read for pass-through plugin files. The other three adapters (W3-02/03/04) will recompute their own merged-target bytes for their target paths (`.codex/config.toml`, `.gemini/settings.json`, `.opencode/opencode.json`).
- **07-W4-01 (e2e):** the 4-platform Core Value path can drive `claude-code` end-to-end against the kept kind cluster as soon as the orchestrator (W3-05) lands. The pass-through reference is the canonical happy path — every other adapter's e2e is a delta against this baseline.

No blockers. The Adapter interface surface, the PluginWrite.Dropped field, the credential helpers, and the registry contract are stable — the three sibling adapter plans can ship without touching `adapter.go` or `registry.go`.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
