# Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State + Distribution - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-05-29
**Phase:** 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
**Areas discussed:** Phase shape & wave granularity, Adapter depth for codex/gemini/opencode, Engine package layout, Distribution carve-out (DIST-01..04)

---

## Phase shape & wave granularity

### Turn 1: Single phase vs split

| Option | Description | Selected |
|--------|-------------|----------|
| Single Phase 7, multi-wave | Mirror Phase 6 Intermediate A. One phase, ~12-15 plans across 4 waves: W1 engine+state+extract foundation, W2 collision policy + auto-claim, W3 4 adapters parallel, W4 distribution + e2e demo. One verify gate at phase end. | ✓ |
| Split into 7a / 7b / 7c | 7a engine+state+safe-extract, 7b adapters, 7c distribution + Helm + demo. Three verify gates. Cleaner accountability, higher overhead. | |
| Split into 7 + 7-polish | 7 lands engine+state+extract+4 adapters+OCI image (Core Value path). 7.1 polish window for Windows binary, Homebrew tap, Helm chart, InitContainer pattern. Two verify gates. | |

**User's choice:** Single Phase 7, multi-wave
**Notes:** Carries forward Phase 6's "Intermediate A" decision pattern. Discussion immediately moved to wave breakdown specificity rather than re-debating split.

### Turn 2: W1 atomic boundary

| Option | Description | Selected |
|--------|-------------|----------|
| State+lock+atomic-write only | W1 ships internal/cli/{state,lock,hash} + commit-sequence skeleton. NO content fetch, NO extract, NO adapter dispatch in W1. W2 adds extract, W3 adapters, W4 distribution. Tightest foundation. | ✓ |
| Foundation + safe extract bundled in W1 | W1 ships state+lock+commit-sequence + internal/cli/extract. 2 LOC-heavier W1, but W2 lands adapters with extract already proven. | |
| Foundation + claude-code adapter (vertical slice) | W1 ships engine + safe-extract + claude-code (pass-through, simplest adapter). Forces end-to-end CV path to work in W1 against ONE platform. | |

**User's choice:** State+lock+atomic-write only
**Notes:** Establishes the 14-step commit sequence skeleton as the load-bearing W1 foundation. Each subsequent wave slots into specific step IDs without retro-fitting lock semantics.

### Turn 3: hydrate.go evolution strategy

| Option | Description | Selected |
|--------|-------------|----------|
| Refactor cmd/ach-cli/cmd/hydrate.go in-place | Same cmd, same file. Add engine flags. runHydrate dispatches to engine package; falls back to POST+stream via `--raw` flag. Preserves W3-P3 golden-diff. | ✓ |
| Split into `ach-cli hydrate` (engine) + `ach-cli platform-api hydrate` (raw POST) | Move Phase 6 raw-POST behavior to hidden debug subcommand. Breaks golden-diff command line; requires e2e rewrite. | |
| Add `--engine` opt-in flag | Phase 6 POST+stream stays default. `--engine` activates full machinery. Risks two paths drifting. | |

**User's choice:** Refactor cmd/ach-cli/cmd/hydrate.go in-place
**Notes:** Hidden `--raw` flag preserves Phase 6 W3-P3 e2e golden-diff anchor. Engine becomes the new user-facing default.

---

## Adapter depth for codex/gemini/opencode

### Turn 1: Depth across non-claude-code adapters

| Option | Description | Selected |
|--------|-------------|----------|
| Full §7.4 faithful translation | Each non-claude-code adapter implements full §7.4: runtime-config MCP/A2A merge into platform-native TOML/JSON, plugin .mcp.json merge into platform config, agent frontmatter rewriting per platform schema (model/tools/permissions), agent layout transformation. Every contributed top-level key recorded in state.adapter.files[*].keys[]. SC#1 demo-able across all 4 platforms. | ✓ |
| Minimal: runtime-config MCP/A2A merge only | claude-code full pass-through; codex/gemini/opencode merge MCP+A2A into runtime-config but skip frontmatter rewriting & agent layout transformation. Plugin content lands under <ach-dir>/plugins/. v1beta1 owns per-platform plugin transformation. | |
| Tier-2: MCP merge + plugin pass-through copy | codex/gemini/opencode merge runtime-config + copy plugin tree verbatim WITHOUT frontmatter rewriting. Agents present but Claude-format. Middle path. | |

**User's choice:** Full §7.4 faithful translation
**Notes:** Heavier W3 (4-6 plans) accepted as the price for SC#1 demo viability across all 4 platforms. PROJECT.md "Core Value" requires the path to work for all four shipped adapters.

### Turn 2: ADAPT-02 multi-match handling

| Option | Description | Selected |
|--------|-------------|----------|
| Exit 1 with list (spec verbatim) | Multi-match → exit 1 listing matches + prompt to pass --platform. No silent priority ordering. | ✓ |
| Precedence list with stderr warning | Multi-match → pick by ordered list (claude-code > codex > gemini-cli > opencode) with stderr warning. Reduces friction; introduces hidden ordering. | |
| Run each matched platform (multi-hydrate) | Multi-match → dispatch adapter run for each matched platform in sequence, sharing same staged content. Maximal usefulness; widens commit-sequence scope; risks confusing failure modes. | |

**User's choice:** Exit 1 with list (spec verbatim)
**Notes:** User-confronts-the-ambiguity model preferred over silent priority ordering or implicit multi-hydrate scope.

### Turn 3: Adapter contract shape

| Option | Description | Selected |
|--------|-------------|----------|
| Single Adapter interface, 4 impls under adapter/<id>/ | `type Adapter interface { ID(), Aliases(), Detect(), RenderRuntime(), TransformPlugin(), MergeStrategies() }`. Registry in adapter/registry.go via init() side-effect. | ✓ |
| Struct-with-funcs, no interface | `type Adapter struct { ID, Aliases, DetectFn, ... }`. 4 instances at package init. Fewer indirection; harder to test/mock. | |
| Sub-typed: ContextAdapter + RuntimeAdapter | Two interfaces separating runtime-config and context responsibilities. Clean separation; doubles boilerplate per adapter. | |

**User's choice:** Single Adapter interface, 4 impls under adapter/<id>/
**Notes:** Single interface keeps the 4 adapter subpackages structurally identical; the registry indirection enables autodetection iteration. `ResolveOutputContent` added to the interface to feed the auto-claim three-tier cascade (D-17).

---

## Engine package layout

### Turn 1: Package shape

| Option | Description | Selected |
|--------|-------------|----------|
| Layered: hydrate/ core + 4 siblings | `internal/cli/hydrate/` owns commit-sequence + orchestration. Siblings `internal/cli/{state,extract,adapter,lock}/`. Adapter subpackages under adapter/{claudecode,codex,gemini,opencode}/. Mirrors existing internal/cli/ flat layout. | ✓ |
| Monolith hydrate package | Everything as files inside `internal/cli/hydrate/`: lock.go, state.go, drift.go, extract.go, adapter.go. Adapter subpackages nested. Fewer imports, harder to unit-test in isolation. | |
| Flat under internal/cli/ | Everything as siblings under internal/cli/: hydrate, state, extract, adapter, lock, drift. No nesting. Wide top-level surface. | |

**User's choice:** Layered: hydrate/ core + 4 siblings
**Notes:** Each sibling unit-testable without the engine. The layered shape preserves existing internal/cli/ flat layout (config, devicecode, exit, httpclient, render, synthetic) and stays consistent with the stdlib-only utility-package pattern.

### Turn 2: Cross-cutting library choices

| Option | Description | Selected |
|--------|-------------|----------|
| stdlib tar + zeebo/xxh3 + --raw flag | (a) zeebo/xxh3 pure-Go xxh3, no cgo. (b) Go stdlib archive/tar + compress/gzip with hand-rolled §6.4 safety checks (stdlib-preference per Phase 1/6). (c) Hidden `--raw` flag bypasses engine and runs Phase 6 POST+stream behavior; W3-P3 test passes `--raw`. Engine default for users. | ✓ |
| stdlib tar + cespare/xxhash backfill | Use cespare/xxhash/v2 (xxh64) already in go.mod for hashing. Simpler dep graph; spec deviation must be flagged. | |
| moby/moby chrootarchive + zeebo/xxh3 + rewrite golden test | Use moby chrootarchive for proven tar-safety. Adds large dep. Rewrites W3-P3 golden-diff to compare state.json + adapter outputs instead of raw manifest stdout. | |

**User's choice:** stdlib tar + zeebo/xxh3 + --raw flag
**Notes:** Single new go.mod entry (`github.com/zeebo/xxh3`). Stdlib tar with hand-rolled safety checks aligned with Phase 1/6 conventions. `--raw` flag is the explicit affordance that decouples engine evolution from the W3-P3 golden-diff e2e anchor.

---

## Distribution carve-out (DIST-01..04)

### Turn 1: What lands in Phase 7 vs polish

| Option | Description | Selected |
|--------|-------------|----------|
| All 4 DIST reqs in Phase 7 | W4 ships Windows binary, Homebrew tap, Helm chart polish, InitContainer pattern. 4-5 plans. Phase 7 verifier covers all SCs. | |
| Engine+adapters Phase 7, distribution all in 7.1 | Phase 7 closes when SC#1-4 pass (engine + adapters + safe extract + state). DIST-01..04 deferred to Phase 7.1 polish window. Faster Phase 7 close. | ✓ |
| Phase 7 = OCI + Linux/Darwin only, slip Windows + Homebrew + InitContainer to 7.1 | Phase 7 ships engine + adapters + 4 binaries + OCI + Helm chart polish. Slip Windows + Homebrew + InitContainer to 7.1. | |

**User's choice:** Engine+adapters Phase 7, distribution all in 7.1
**Notes:** New Phase 7.1 inserted to ROADMAP. SC#5 slides to Phase 7.1. Phase 7's success criteria narrow to SC#1-4. Windows lock impl (`LockFileEx`) bundled with Windows binary in Phase 7.1.

### Turn 2: Phase 7 close criteria + Phase 7.1 boundary

| Option | Description | Selected |
|--------|-------------|----------|
| Phase 7 closes when SC#1-4 pass on linux-amd64 dev kind | Phase 7 closes on SC#1-4 via test/e2e/cli_hydrate_engine_test.go on the kept kind cluster. Phase 7.1 owns Windows + Homebrew + Helm polish + InitContainer + SC#5. ROADMAP updated to insert Phase 7.1 entry. | ✓ |
| Phase 7 also keeps DIST-01 (OCI image) | Phase 7 retains DIST-01 since OCI image already builds. Phase 7.1 covers DIST-02/03/04. | |
| Phase 7 keeps DIST-01 + DIST-04 (OCI + Helm) | Phase 7 retains OCI + Helm chart values surface. Phase 7.1 owns DIST-02 (Windows + Homebrew) + DIST-03 (InitContainer). | |

**User's choice:** Phase 7 closes when SC#1-4 pass on linux-amd64 dev kind
**Notes:** Clean break — even the already-existing OCI + Helm move to Phase 7.1 polish for SC#5 verification. ROADMAP refresh + Phase 7.1 insertion happens in the planner's first Phase 7 commit (Documentation hygiene rule per CLAUDE.md).

---

## Claude's Discretion

- Exact wave-to-plan mapping (W1/W2/W3/W4 plan counts) — planner finalizes.
- Lock file lease + advisory semantics on NFS / overlayfs — planner picks documentation strategy.
- Whether `RenderRuntime` returns `[]FileWrite` (staged-then-renamed) or writes directly — `[]FileWrite` recommended for testability.
- Concrete `xxh3:<hex>` format — recommend `xxh3:` + lowercase hex of the 16-byte output.
- Test fixture layout for malicious-archive scenarios (SAFE-01) — recommend `test/fixtures/malicious-archives/*.tar.gz` + a Go generator script.
- `internal/cli/manifest/` package boundary — recommend strict POST + decode + version assert only.
- Bomb-cap env-var validation: recommend rejecting non-numeric / zero / negative values at hydrate-start with exit 1.
- Platform autodetection `Detect()` signatures — recommend `(root string) → (Match{ID, Confidence, Reasons}, error)`.

## Deferred Ideas

- **Phase 7.1 (NEW)** — DIST-01..04 + SC#5: Windows binary + `LockFileEx` lock impl + Homebrew tap publish + Helm chart polish + K8s InitContainer pattern.
- **Sandboxed in-tree symlink resolution via `openat2(RESOLVE_BENEATH)`** (CLI spec §13) — `--allow-symlinks` is the v1alpha1 affordance.
- **`custom` CLI platform adapter** (CLI spec §7.6, §13) — Phase 7 ships hardcoded 4 adapters; user-overrideable platform table is v1beta1.
- **Declarative transformation DSL for plugin adapters** (CLI spec §13) — Phase 7 ships imperative per-adapter; DSL is v1beta1.
- **Template rendering on artifacts** (CLI spec §13) — Hub serves opaque bytes / `.tar.gz` in v1alpha1.
- **`ach hook emit`** (CLI spec §13) — out-of-scope per Hub v1alpha1.
- **Offline `ach status`** (CLI spec §13) — every server-bearing subcommand requires connectivity.
- **OS keyring integration** (CLI spec §13) — pk_/ek_ in plaintext config per Phase 6 D-04.
- **Resumable downloads / Conditional GET / HTTP `Range`** (Hub §15.6, §20 + CLI §13).
- **`ach-cli env-keys rotate`** (CLI spec §13) — revoke + create flow not in v1alpha1.
- **Workforce SSO multiplexing** (CLI spec §13).
- **Deployment discovery** (CLI spec §13).
