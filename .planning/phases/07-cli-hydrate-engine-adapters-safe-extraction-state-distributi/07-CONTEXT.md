# Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State - Context

**Gathered:** 2026-05-29
**Status:** Ready for planning
**Mode:** `/gsd-discuss-phase 7` (interactive — 4 selected areas: phase shape, adapter depth, engine layout, distribution carve-out)

<domain>
## Phase Boundary

Phase 7 builds the `ach-cli hydrate` **engine** — the layer between Phase 6's `POST /platform/hydrate` surface and the user's filesystem. It implements the Core Value path end-to-end against all four shipped platform adapters with safe-extracted content, dual-hash drift detection, atomic state v2 writes, and lock-protected concurrency. **Distribution (DIST-01..04) is carved out to a new Phase 7.1.**

### In scope (Phase 7)

- **23 of 28 requirements**: STATE-01..11 (state file + lock + atomic commit + drift + scope flags) + ADAPT-01..07 (4 platform adapters + autodetection + merge strategies) + SAFE-01..06 (tar safety + bomb defense + auto-claim + per-resource atomic publication).
- **4 of 5 success criteria**: SC#1 (4-platform hydrate works against demo Environment) + SC#2 (14-step commit sequence under `flock`) + SC#3 (drift detection: 4 outcomes + schema/Env aborts) + SC#4 (safe extraction + size caps + auto-claim).
- Refactor `cmd/ach-cli/cmd/hydrate.go` in place to add the engine flags (`--include-runtime`, `--only-runtime`, `--sync`, `--force`, `--dry-run`, `--wait`, `--lock-timeout`, `--output`, `--allow-symlinks`, `--platform`, `--global`) and an opt-in `--raw` flag that preserves Phase 6 POST+stream behavior for the W3-P3 e2e golden-diff anchor.
- New engine packages: `internal/cli/{hydrate,state,extract,adapter,lock}/` with adapter subpackages `internal/cli/adapter/{claudecode,codex,gemini,opencode}/`.
- New e2e test: `test/e2e/cli_hydrate_engine_test.go` driving the 4-platform Core Value path against the kept kind cluster on `linux-amd64`.

### Out of scope — moved to Phase 7.1 (Distribution polish)

- **DIST-01..04 + SC#5**. Windows binary in goreleaser, Homebrew tap publish, Helm chart polish (rebuildId surface review, README install snippet, values-md), K8s InitContainer pattern (`deploy/initcontainer-example/` + `docs/runbooks/k8s-initcontainer.md`).
- The Windows-only `LockFileEx` shim in `internal/cli/lock/` belongs to Phase 7.1 with the Windows binary; Phase 7 ships `lock_unix.go` only.
- Note: ROADMAP.md must grow a Phase 7.1 entry and SC#5 must slide out of Phase 7's success criteria (planner does this in the same commit that lands the Phase 7 ROADMAP refresh, per CLAUDE.md "Documentation hygiene").

### Out of scope (permanent, per spec §13 + PROJECT.md)

- **`custom` CLI platform adapter** — four shipped adapters cover immediate consumers.
- **Declarative transformation DSL for plugin adapters** — imperative in each adapter for v1alpha1.
- **Template rendering on artifacts** — Hub serves opaque bytes / `.tar.gz`.
- **OS keyring integration** — pk_/ek_ in plaintext config per Phase 6 D-04.
- **State.json v1 migration** — clean break; CLI rejects `schemaVersion != "2"` with exit 5 (no v1 reader code).
- **User-overrideable platform table** — hardcoded 4 platforms.
- **`ach hook emit`** — Hub v1alpha1 omits hooks.
- **Sandboxed in-tree symlink resolution via `openat2(RESOLVE_BENEATH)`** — `--allow-symlinks` is the v1alpha1 mechanism; v1beta1 backlog.
- **Resumable downloads / Conditional GET / `Range`** — full-body fetch per Hub spec §15.6.

</domain>

<decisions>
## Implementation Decisions

### Phase shape

- **D-01:** **Single Phase 7 with 4 waves, ~12-15 plans** — Intermediate A mirror of Phase 6. Wave breakdown:
  - **W1 — Foundation (state + lock + atomic write)**: `internal/cli/{state,lock,hash}/` + `internal/cli/hydrate/commit.go` skeleton (14-step orchestrator with stub stages). NO content fetch, NO extract, NO adapter dispatch in W1. State.json v2 marshal+atomic-write+flock-protected sweep+read closed; schema-version-mismatch (exit 5), same-`<ach-dir>` different-Environment (exit 4), schema-`!= "2"` (exit 5) all enforced at W1 close.
  - **W2 — Safe extract + collision policy**: `internal/cli/extract/` (tar safety per spec §6.4, bomb defense via `ACH_MAX_EXTRACTED_PLUGIN_MIB` / `ACH_MAX_EXTRACTED_ARTIFACT_MIB` / `ACH_MAX_ARCHIVE_ENTRIES` envs, stream gzip + per-resource staging + atomic per-resource publication) + three-tier auto-claim lazy cascade (eager → adapter `resolveOutputContent()` → lazy source-file read).
  - **W3 — 4 adapters in parallel**: `internal/cli/adapter/{claudecode,codex,gemini,opencode}/` + registry + autodetection. Each adapter implements the full §7.4 contract.
  - **W4 — E2E demo + verifier**: `test/e2e/cli_hydrate_engine_test.go` driving 4 platforms × {pk_, ek_} on the kept kind cluster. ROADMAP refresh (Phase 7.1 insert + SC#5 slide).
- **D-02:** **W1 atomic boundary = state + lock + atomic-write ONLY.** No content fetch, no extract, no adapter dispatch in W1. Each subsequent wave slots into specific commit-sequence step IDs (W2: steps 1-2 sweep + 7-9 fetch/extract/hash; W3: step 10 adapter run; W4: end-to-end). Tightest foundation; downstream waves do not need to retro-fit lock semantics.

### `ach-cli hydrate` evolution

- **D-03:** **Refactor `cmd/ach-cli/cmd/hydrate.go` in place.** Same command, same file. Engine flags added: `--include-runtime` / `--only-runtime` / `--sync` / `--force` / `--dry-run` / `--wait` / `--lock-timeout <dur>` / `--output <dir>` / `--allow-symlinks` / `--platform <id>` / `--global`. `runHydrate` dispatches to `internal/cli/hydrate.Run(ctx, opts)` for the engine path.
- **D-04:** **Hidden `--raw` flag preserves Phase 6 POST+stream behavior** for the W3-P3 e2e golden-diff anchor (`test/e2e/cli_login_hydrate_test.go`). The W3-P3 test is updated to pass `--raw`; engine path becomes the new user-facing default. No silent dual-path drift — `--raw` short-circuits before any engine call.

### Adapter depth

- **D-05:** **Full §7.4 faithful translation across all 4 adapters.** claude-code = pass-through (locked PROJECT.md). codex/gemini-cli/opencode each implement: runtime-config MCP/A2A merge into platform-native TOML/JSON, plugin `.mcp.json` merge into platform config, agent frontmatter rewriting per platform schema (`model`/`tools`/`permissions`), plugin layout transformation (e.g. codex `.codex/{prompts,agents,skills}/<name>/`). Every contributed top-level key recorded in `state.adapter.files[*].keys[]` for precise `--sync` inverse-merge. SC#1 demo-able across all 4 platforms.
- **D-06:** **Autodetection per ADAPT-02 verbatim — multi-match exits 1 with list.** When `--platform` omitted AND `ACH_PLATFORM` unset, scan cwd (workspace) or `$HOME` (global) per adapter's `Detect()`. Zero matches → exit 1 with prompt; one match → `Detected platform: <id>` to stderr; multi-match → exit 1 listing matches + prompt to pass `--platform`. NO silent priority ordering. User confronts the ambiguity.
- **D-07:** **Adapter contract = single `Adapter` interface, 4 impls under `internal/cli/adapter/<id>/`.** Interface in `internal/cli/adapter/adapter.go`:
  ```go
  type Adapter interface {
      ID() string
      Aliases() []string
      Detect(root string) (Match, error)
      RenderRuntime(ctx context.Context, m *manifest.Manifest, s *state.File) ([]FileWrite, error)
      TransformPlugin(ctx context.Context, src, dst string) (PluginWrite, error)
      MergeStrategies() map[string]MergeKind
      ResolveOutputContent(ctx context.Context, m *manifest.Manifest, target string) ([]byte, error)
  }
  ```
  Registry in `internal/cli/adapter/registry.go` via init() side-effect registration; CLI iterates registered adapters for autodetection. Each adapter ships its own `*_test.go` with table-driven scenarios.
- **D-08:** **ADAPT-07 silent-drop semantics.** Adapter components a platform cannot meaningfully translate (e.g. `hooks/` for Codex) are silently dropped from output; adapter accumulates dropped names and the orchestrator emits a single stderr warning at end of hydration. Exit code unchanged.

### Engine package layout

- **D-09:** **Layered: `internal/cli/hydrate/` core + 4 siblings.** Final shape:
  - `internal/cli/hydrate/` — `commit.go` (14-step orchestrator), `flags.go`, `result.go`. Imports state/extract/adapter/lock.
  - `internal/cli/state/` — state.json v2 marshaling, atomic write (tmp → `fsync(fd)` → `rename(2)` → `fsync(parent_dir)`), schema-version gate, same-`<ach-dir>` different-Environment guard, tmp-sweep, missing-file silent-prune.
  - `internal/cli/extract/` — safe tar policy, gzip stream, bomb defense, per-resource staging, three-tier auto-claim lazy cascade.
  - `internal/cli/adapter/` — `adapter.go` interface + registry + 4 subpackages.
  - `internal/cli/lock/` — `flock(LOCK_EX)` POSIX impl + interface. Windows `LockFileEx` impl is Phase 7.1.
  - `internal/cli/hash/` — xxh3 wrapper (`zeebo/xxh3`) used by state + extract for `hash` / `sourceHash`.
  - `internal/cli/manifest/` — `POST /platform/hydrate` decoder + schema-version assertion + `runtime`/`context` presence assertion (exit 5). Reuses `httpclient.Client.DoRaw` from Phase 6.

  This mirrors existing `internal/cli/` flat layout (config, devicecode, exit, httpclient, render, synthetic) and keeps each sibling unit-testable without the engine.

### Cross-cutting library choices

- **D-10:** **xxh3 via `github.com/zeebo/xxh3`** — pure-Go, no cgo, MIT-licensed. `cespare/xxhash/v2` (xxh64) already in go.mod is INSUFFICIENT — spec §8.2 mandates xxh3 prefix. New `internal/cli/hash/` package wraps it as `Hash(io.Reader) (string, error)` returning `xxh3:<hex>` strings.
- **D-11:** **Go stdlib `archive/tar` + `compress/gzip`** for safe extract. Hand-rolled §6.4 safety checks (reject `..`/absolute/symlinks/hardlinks/devices/FIFOs/sockets/pax-extended-path-injection, mask modes to `mode & 0755`, strip setuid/setgid/sticky/group-write/world-write). Consistent with the stdlib-preference set in Phase 1/6 (`internal/credhash`, `internal/cachefs`, `internal/cli/config` are all stdlib-only).
- **D-12:** **Decompression-bomb caps via env vars** per SAFE-03: `ACH_MAX_EXTRACTED_PLUGIN_MIB` (default 200), `ACH_MAX_EXTRACTED_ARTIFACT_MIB` (default 500), `ACH_MAX_ARCHIVE_ENTRIES` (default 65536). Read once at hydrate-start; exceeding any limit aborts THAT resource before writing the offending entry; partial output discarded; other resources continue.

### State schema + drift

- **D-13:** **State.json v2 clean break** per spec §8.2. CLI rejects `schemaVersion != "2"` with exit 5 unless `--force`. NO v1 reader code shipped; the only known-prior schema is the Phase 6 W3-P3 hand-test which doesn't write state.json. Clean greenfield.
- **D-14:** **Dual hash discipline** — every state entry carries `hash` (xxh3 of bytes written on disk) AND `sourceHash` (xxh3 of upstream bytes pre-transformation). For pass-through resources (prompts, scope-object artifacts) `hash == sourceHash`. For adapter-merged files (`.claude/.mcp.json`, `.codex/config.toml`) `hash != sourceHash`. Drift detection (§8.4) compares all four data points: `state.hash`, `state.sourceHash`, fresh-on-disk hash, fresh-staged-source hash. Four outcomes per the truth table — no-op / upstream-only-overwrite / local-edit-preserve (exit 2) / conflict-preserve (exit 2).
- **D-15:** **Hydrate fetch is unconditional** per STATE-11. Every `GET <downloadUrl>` runs even when state claims upstream is unchanged; the disk-write step is short-circuited only when freshly-downloaded `sha256` matches on-disk `sha256`. `--only-runtime` skips the GETs entirely (context out of scope).

### `--sync` inverse-merge semantics

- **D-16:** **Per spec §8.5 verbatim.** `--sync` deletion is deepest-first; on-disk-hash mismatch preserves the file with stderr warning (drift wins; user edits sacred). For entries with `merge` + `keys[]`: inverse-merge — remove only the listed top-level keys via deep-merge inverse for JSON/TOML, OR replace `<!-- ach:begin -->...<!-- ach:end -->` block content for `composite` markdown. Empty-dir pruning via `rmdir(2)` honoring `ENOTEMPTY` silently — CLI NEVER recursively deletes a directory.

### Auto-claim collision policy

- **D-17:** **Three-tier lazy cascade per SAFE-04.** Final-rename collision classifier returns one of `none` / `owned-by-current` / `exists-unowned`. For `exists-unowned`:
  1. **Eager** — if staged content is already in memory (typical for prompt / scope-object artifact), compare directly.
  2. **Lazy transform** — for adapter-written merged files, invoke adapter `ResolveOutputContent()` to compute what would be written, then compare.
  3. **Lazy source read** — for pass-through, read source file from staging on demand and compare.

  Identical bytes → auto-claim into state on commit. Different bytes → exit 7 with refusal, unless `--force`. Adapter contract REQUIRES `ResolveOutputContent` (D-07) precisely to feed this cascade.

### Lock + concurrency

- **D-18:** **`flock(LOCK_EX)` POSIX impl in `internal/cli/lock/lock_unix.go`** with `//go:build !windows` build tag. Path: `<ach-dir>/lock` (workspace) or `~/.ach/<environment>/lock` (global). Acquired at hydrate-start (before manifest fetch); released by kernel on process exit including SIGKILL. Contention: fail-fast exit 1 default; `--wait` blocks; `--lock-timeout <dur>` caps the wait. Windows `LockFileEx` impl belongs to Phase 7.1 with the Windows binary — Phase 7 file layout includes the build-tagged Unix file only.
- **D-19:** **Lock module is interface-based** so the W1 test surface can swap a `noopLocker` for unit tests. Concrete impl in `lock_unix.go`; interface in `lock.go` (no build tag). Phase 7.1 adds `lock_windows.go` without touching W1 code.

### Test infrastructure

- **D-20:** **`test/e2e/cli_hydrate_engine_test.go` (new) drives 4-platform Core Value path** against the kept kind cluster (per CLAUDE.md dev loop — `make cluster-keep`). Subtests:
  - W1 baseline: hydrate-twice no-op (second invocation produces zero writes; same state hash).
  - SC#1 per platform: 4 subtests (claude-code, codex, gemini-cli, opencode) × {pk_, ek_}: hydrate against demo Environment + assert runtime-config rendered with `baseUrl` + MCP/A2A endpoints + `x-ach-key` header attachment.
  - SC#2 commit sequence: induce SIGKILL between steps 9 and 12; assert prior state intact + `<ach-dir>/tmp/` swept on resume.
  - SC#3 drift: four-outcome truth table (no-op, upstream-only, local-edit-preserve exit 2, conflict-preserve exit 2) + `--force` overrides.
  - SC#4 safe extract: malicious-archive fixture rejection (absolute path, `..`, symlink default, hardlink, device, FIFO, pax-extended-path-injection) + bomb-cap-exceeded test + auto-claim three-tier cascade.
- **D-21:** **W3-P3 e2e golden-diff test (`test/e2e/cli_login_hydrate_test.go`) preserved via `--raw`.** Update the existing test to pass `--raw` to `ach-cli hydrate`; engine path becomes user-facing default; raw remains the byte-equal anchor against `examples/hydrate.json`.

### Phase 7 close criteria + Phase 7.1 boundary

- **D-22:** **Phase 7 closes when SC#1-4 pass on `linux-amd64` against the dev kind cluster.** SC#5 (distribution artifacts publishable) slides to Phase 7.1. The Phase 7 verifier asserts:
  1. `cli_hydrate_engine_test.go` green for all 8 platform×key combos.
  2. SC#2 commit-sequence subtest green (crash-recovery semantics).
  3. SC#3 drift four-outcome truth-table green.
  4. SC#4 safe-extract malicious-archive + bomb + auto-claim green.
  5. `--raw` golden-diff still green.
- **D-23:** **Phase 7.1 (NEW)** owns: goreleaser `windows-amd64` build for `ach-cli` + `internal/cli/lock/lock_windows.go` (`LockFileEx`) + Homebrew tap publish (goreleaser `homebrew_cask` or `brews` block targeting `ackstorm/homebrew-tap`) + Helm chart polish (`rebuildId` knob doc, values surface review per `deploy/helm/ach/values.yaml`, README install snippet, `helm install` runbook) + InitContainer pattern (`deploy/initcontainer-example/` sample manifest + `docs/runbooks/k8s-initcontainer.md`) + SC#5 verifier (publishable artifacts: OCI image runs with no preset config, 5 binaries downloadable, `brew install ackstorm/tap/ach` succeeds, Helm chart end-to-end deploy). ROADMAP.md must grow a Phase 7.1 entry; that work happens in the planner's first Phase 7 commit (Documentation hygiene rule).

### Claude's Discretion (planner picks)

- Exact wave-to-plan mapping (W1 → N plans, W2 → M plans, etc.) — planner finalizes.
- Lock file lease + advisory semantics on NFS / overlayfs — planner picks documentation strategy.
- Whether `RenderRuntime` returns `[]FileWrite` (staged-then-renamed) or writes directly — `[]FileWrite` recommended for testability; planner finalizes.
- Concrete `xxh3:<hex>` format — recommend `xxh3:` + lowercase hex of the 16-byte output; planner finalizes.
- Test fixture layout for malicious-archive scenarios (SAFE-01) — recommend `test/fixtures/malicious-archives/*.tar.gz` + a Go generator script; planner picks.
- `internal/cli/manifest/` package boundary — recommend strict POST + decode + version assert only; do not bleed scope-filter logic into it (that belongs to hydrate orchestrator).
- Bomb-cap env-var validation: recommend rejecting non-numeric / zero / negative values at hydrate-start with exit 1 (consistent with `ACH_PLUGIN_MAX_SIZE_MIB` operator startup discipline established Phase 1).
- Platform autodetection `Detect()` signatures — recommend (root string) → (Match{ID, Confidence, Reasons}, error); planner finalizes.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### CLI spec (authoritative for §6, §7, §8, §9)

- `spec/ach_cli_spec_v20260515_FINALv4.md` §6 — Hydration Flow (the entire section is the engine contract)
  - §6.1 Credential resolution (Phase 6 already handles via `cmd/ach-cli/cmd/hydrate.go`; preserved by engine path)
  - §6.2 Manifest fetch — `schemaVersion == "v1alpha1"` strict; both `runtime` + `context` required; exit 5 otherwise
  - §6.3 Diff computation — three-state machine + scope-selection table + `--dry-run` limitation
  - §6.4 Content download + safe extraction — tar safety table + path/mode/ownership rules + bomb defense + auto-claim three-tier cascade
  - §6.5 Platform adapter application — narrow remit + scope-gating table
  - §6.6 pk_ warning (Phase 6 implements; engine preserves)
  - §6.7 Concurrency lock + 14-step commit sequence + crash-recovery semantics table
- `spec/ach_cli_spec_v20260515_FINALv4.md` §7 — Platform Adapters
  - §7.1 Merge strategies (deep / composite / replace)
  - §7.2 Per-adapter `detection`/`aliases`/runtime-config files
  - §7.3 `<ach-dir>` resolution (workspace vs global)
  - §7.4 Per-platform adapter behavior (claude-code pass-through; codex/gemini/opencode transformations)
  - §7.5 Autodetection algorithm
- `spec/ach_cli_spec_v20260515_FINALv4.md` §8 — Local State
  - §8.1 State file location + `<ach-dir>` semantics
  - §8.2 Schema v2 — `{target, hash, sourceHash, merge?, keys?}` + xxh3 + `schemaVersion: "2"`
  - §8.3 Same-`<ach-dir>` different-Environment guard
  - §8.4 Drift four-outcome truth table
  - §8.5 `--sync` deepest-first + inverse-merge for `merge`+`keys[]` entries
  - §8.7 Atomic state write (tmp → `fsync(fd)` → `rename(2)` → `fsync(parent_dir)`)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §9.3 — Exit code matrix (Phase 7 adds 2/4/5/7 to the Phase 6 0/1/3/6/8 set)
- `spec/ach_cli_spec_v20260515_FINALv4.md` §13 — v1beta1 backlog (confirms what Phase 7 is NOT)

### Hub spec (referenced for endpoint contracts Phase 7 consumes)

- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.1 — `POST /platform/hydrate` (manifest source)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.2 — manifest schema (`schemaVersion: "v1alpha1"`, `runtime`/`context` shape)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.3 — pk_ warning text (mirror of CLI §6.6)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §15.6 — `GET /content/{kind}/{name}` contract (content download source for §6.4)
- `spec/ach_hub_spec_v20260515_FINALv4.md` §10.2 — content "trusted administrative" qualifier (motivation for safe-extract policy)

### Project planning

- `.planning/ROADMAP.md` §"Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State + Distribution" — current entry; planner refreshes to slide DIST-* + SC#5 into a new Phase 7.1 entry
- `.planning/REQUIREMENTS.md` STATE-01..11 + ADAPT-01..07 + SAFE-01..06 — testable acceptance criteria for everything in Phase 7 scope
- `.planning/REQUIREMENTS.md` DIST-01..04 — moves to Phase 7.1 (planner reshuffles Traceability table)
- `.planning/PROJECT.md` "Core Value" + "Out of Scope" — Phase 7 finalizes Core Value; permanent boundaries respected
- `.planning/phases/05-content-service-cross-component-observability/05-CONTEXT.md` — Content Service serves the `/content/{kind}/{name}` endpoint Phase 7 consumes
- `.planning/phases/06-cli-foundation/06-CONTEXT.md` — Phase 6 D-09 (surface-only hydrate) is the seam Phase 7 expands; D-04 (config schema), D-11 (mutex creds), D-12 (--environment required for pk_), D-15 (exit code matrix 0/1/3/6/8) are preserved verbatim and extended

### Existing code surfaces Phase 7 wires into

- `cmd/ach-cli/cmd/hydrate.go` — D-03 refactors in place; engine flags slot in; `--raw` short-circuits to existing POST+stream
- `cmd/ach-cli/cmd/hydrate_test.go` — extend with engine-flag test scenarios
- `cmd/ach-cli/cmd/root.go` — Version ldflag injection point; no shape change
- `internal/cli/httpclient/` — Phase 6 D-04. Engine reuses `Client.DoRaw` for `POST /platform/hydrate` and `GET /content/...`
- `internal/cli/config/` — Phase 6 D-04. Engine reuses `Path` + `Load` + `ResolveActive` for `<ach-dir>` resolution + base-URL discovery
- `internal/cli/exit/` — Phase 6 D-13. Engine adds exit codes 2 (drift), 4 (Environment mismatch), 5 (schema mismatch), 7 (collision)
- `internal/cli/synthetic/` — Phase 6 D-08 + 06-07. Engine respects synthetic gating verbatim; `--global` flag interaction documented
- `internal/keys/` — `pk_` / `ek_` classify (Phase 6 reuse); engine reuses for `--environment` gating
- `examples/hydrate.json` — golden artifact (preserved by `--raw` flag; W3-P3 e2e updated to pass `--raw`)

### Phase 7.1 inputs (DO NOT read for Phase 7 planning; read for Phase 7.1)

- `.goreleaser.yml` — existing 4-platform binary builds + OCI image. Phase 7.1 adds `windows` to `goos:` for `ach-cli` build, adds homebrew block.
- `.goreleaser.prerelease.yml` + `.goreleaser.snapshot.yml` — release-channel companions
- `deploy/helm/ach/` — full chart structure; values surface review + README install snippet land in Phase 7.1
- `Dockerfile.goreleaser` — OCI image source

### Toolchain + dev loop (CLAUDE.md, MANDATORY)

- `CLAUDE.md` §"Toolchain — host has NO Go" — every `go`/`make` prefixed `./scripts/dev.sh`
- `CLAUDE.md` §"Test phases" — `unit`, `envtest-run`, `e2e-focus`, `e2e-full` taxonomy
- `CLAUDE.md` §"E2E debug loop" — kept-cluster iteration pattern for D-20
- `CLAUDE.md` §"Common failure modes" — `Hydrate output ≠ examples/hydrate.json` entry already documents the `--raw` invariant Phase 7 introduces (planner updates entry to reference `--raw` flag)
- `CLAUDE.md` §"Publication" — 17-gate pre-push; SPDX header per `*.go`; govulncheck ack-list discipline
- `CLAUDE.md` §"Repository-specific patterns" — single-binary cobra layout, per-mode service Deployments, BIP+Environment forwarder read-path

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets

- **`cmd/ach-cli/cmd/hydrate.go`** (Phase 6 D-09) — surface-only command Phase 7 extends. `runHydrate` flow (mutex creds → synthetic gate → resolve bearer → classify prefix → emit warnings → POST+stream) preserved by `--raw`; engine path is a parallel dispatch under D-03.
- **`internal/cli/httpclient.Client.DoRaw`** (Phase 6) — returns `*http.Response` without decoding; engine reuses for both `POST /platform/hydrate` (manifest decode) and `GET /content/...` (stream to staging). NO new HTTP client.
- **`internal/cli/config.{Path,Load,ResolveActive}`** (Phase 6 D-04) — resolves active deployment + base URL. Engine reuses verbatim for `<ach-dir>` parent resolution and base-URL discovery.
- **`internal/cli/exit.CodedError`** (Phase 6) — engine raises exit codes 2/4/5/7 via the same envelope `cmd/ach-cli/main.go` already maps.
- **`internal/cli/synthetic.GuardCommand` + `GateHydrate`** (Phase 6 06-07) — engine path runs the same gate; `--global` flag does NOT cross the synthetic boundary (workspace scope only in synthetic per the InitContainer pattern Phase 7.1 documents).
- **`internal/keys.ClassifyBearer`** (Phase 3 / Phase 6 reuse) — `pk_` vs `ek_` discrimination drives `--environment` gating + `x-ach-environment` header attachment.
- **`net/http/httptest`** — hermetic adapter unit tests can spin up a fake `/content/...` server returning known archives. Same pattern as Phase 6 httpclient tests.
- **Existing `examples/` CR fixtures** (Phase 02) — demo Environment fixture (`examples/04-environment-demo.yaml`) is what the W4 e2e test's `--environment demo` resolves against. No new fixtures unless the four-platform demo needs additional Plugins/Prompts/Artifacts.

### Established Patterns

- **Stdlib-only utility packages** — `internal/credhash`, `internal/cachefs`, `internal/cli/config` are zero-dep. Phase 7 honors this for `internal/cli/state`, `internal/cli/lock`, `internal/cli/extract`. Only new dep: `github.com/zeebo/xxh3` (D-10).
- **TDD discipline (red → green → refactor)** — Phase 3/4/5/6 set the precedent; every engine package lands with `*_test.go` first.
- **SPDX-only license headers** — every `*.go` outside `vendor/`/`zz_generated*`/`mock_*` starts with `// SPDX-License-Identifier: Apache-2.0`. Pre-push gate enforces.
- **Exit-code dispatch via `internal/cli/exit`** — adapters and engine raise `*exit.CodedError`; `cmd/ach-cli/main.go` already maps. Phase 7 extends the constant set (2/4/5/7 added).
- **Single-binary cobra layout** — CLAUDE.md §"Repository-specific patterns" locks this. All Phase 7 work goes under `cmd/ach-cli/cmd/<verb>.go` + `internal/cli/`; NEVER as a second `cmd/<x>/main.go` tree.
- **Devtools container toolchain** — `./scripts/dev.sh go test ./internal/cli/hydrate/...`, `./scripts/dev.sh make e2e-focus FOCUS=TestCLIHydrateEngine`. Host has no Go (CLAUDE.md §"Toolchain").

### Integration Points

- **`cmd/ach-cli/cmd/hydrate.go` refactor** (D-03) — engine flag wiring + dispatch to `internal/cli/hydrate.Run(ctx, Opts)`. `--raw` short-circuits to the Phase 6 POST+stream path. NO breaking change to existing cobra command name / short / long.
- **Engine entry point** — `internal/cli/hydrate.Run(ctx, opts) (Result, error)` is the single public function the cobra layer calls. Opts struct carries all the engine flags (`Environment`, `Platform`, `Global`, `IncludeRuntime`, `OnlyRuntime`, `Sync`, `Force`, `DryRun`, `AllowSymlinks`, `Output`, `Wait`, `LockTimeout`). Result carries summary for `--verbose` stderr output.
- **Adapter registry** — `internal/cli/adapter/registry.go` `Register(Adapter)` + `Lookup(id) (Adapter, bool)` + `Iter() []Adapter` (for autodetection). Each adapter subpackage `init()` registers itself.
- **State file location resolution** — `internal/cli/state.ResolvePath(opts) (string, error)` returns `<ach-dir>/state.json`. Workspace: `<cwd>/.ach/state.json`. Global (`--global`): `~/.ach/<environment>/state.json`. Engine calls this once at hydrate-start.
- **Lock path resolution** — `internal/cli/lock.Path(achDir string) string` returns `<ach-dir>/lock`. Same `<ach-dir>` as state.
- **Content Service consumption** — `GET <baseUrl>/content/{kind}/{name}` per Hub §15.6. Engine streams response body via `io.Copy` into `<ach-dir>/tmp/<rand>/<resource>(.tar.gz|raw)`. `Content-Type: application/gzip` triggers extract; any other type is verbatim write.
- **CLAUDE.md "Common failure modes"** — three entries Phase 7 touches:
  1. `Hydrate output ≠ examples/hydrate.json` (already documents `--raw` invariant; planner updates wording to make `--raw` explicit)
  2. New entry: state.json schemaVersion mismatch — exit 5, no files written, `--force` overrides
  3. New entry: same-`<ach-dir>` different-Environment — exit 4, `--force` overrides (workspace scope only)
- **`test/e2e/cli_login_hydrate_test.go`** — Phase 6 W3-P3 golden-diff test updated to pass `--raw` (D-21).

</code_context>

<specifics>
## Specific Ideas

- **Core Value demo**: After Phase 7 closes, `ach-cli login` + `ach-cli hydrate --environment demo --platform <one of 4>` produces a working AI agent workspace for the chosen platform. The Phase 7 verifier runs all 8 combos (4 platforms × {pk_, ek_}). This is the headline outcome of the milestone.
- **`--raw` golden-diff preservation**: The W3-P3 e2e test that runs `ach-cli hydrate --environment demo > hydrate.json` byte-diffs vs `examples/hydrate.json` stays GREEN throughout Phase 7. The single CLI change is appending `--raw` to the invocation; the engine path is the new default for actual users.
- **Adapter parallelism in W3**: The 4 adapter subpackages are independent — each is a separate plan in W3. Plans 7-W3-{01,02,03,04} (claude-code, codex, gemini-cli, opencode) can land in parallel. claude-code lands first as the pass-through reference; the other three reference its `Adapter` impl for shape.
- **Bomb defense ordering**: Bomb caps trip BEFORE writing the offending entry — the implementation must count uncompressed bytes per-entry as it streams, NOT after the full archive is materialized. This is a recurring rust/go tar-safety gotcha worth flagging to the W2 planner.
- **Auto-claim three-tier cascade adapter coupling**: `Adapter.ResolveOutputContent` is the lazy-tier-2 hook (D-07, D-17). Every adapter MUST implement it; for adapters whose output is byte-equal to a single source resource (pass-through claude-code prompts/artifacts), the implementation returns the source bytes verbatim.
- **Phase 7.1 ROADMAP refresh**: When the planner ships Plan 07-W1-01 (or the first commit that touches ROADMAP.md), update the Phase 7 entry to drop DIST-* + SC#5 and insert a new Phase 7.1 entry per D-23. Documentation hygiene rule per CLAUDE.md.

</specifics>

<deferred>
## Deferred Ideas

- **Phase 7.1 (NEW)** — DIST-01..04 + SC#5: goreleaser `windows-amd64` build for `ach-cli`, `internal/cli/lock/lock_windows.go` (`LockFileEx`), Homebrew tap publish via goreleaser, Helm chart polish + README install snippet + runbook, K8s InitContainer sample manifest + runbook. Independent phase, dependent on Phase 7 close (engine + adapters must work on `linux-amd64` first).
- **Sandboxed in-tree symlink resolution via `openat2(RESOLVE_BENEATH)`** (CLI spec §13) — Phase 7 ships `--allow-symlinks` as the explicit opt-in. The `openat2`-based safe-by-default path is v1beta1.
- **`custom` CLI platform adapter** (CLI spec §7.6, §13) — Phase 7 ships hardcoded 4 adapters; user-overrideable platform table is v1beta1.
- **Declarative transformation DSL for plugin adapters** (CLI spec §13) — Phase 7 ships imperative per-adapter; DSL is v1beta1.
- **Template rendering on artifacts** (CLI spec §13) — Hub serves opaque bytes / `.tar.gz` in v1alpha1.
- **`ach hook emit`** (CLI spec §13) — out-of-scope per Hub v1alpha1.
- **Offline `ach status`** (CLI spec §13) — every server-bearing subcommand requires connectivity.
- **OS keyring integration** (CLI spec §13) — pk_/ek_ in plaintext config per Phase 6 D-04.
- **Resumable downloads / Conditional GET / HTTP `Range`** (Hub §15.6, §20 + CLI §13) — full-body fetch in v1alpha1.
- **`ach-cli env-keys rotate`** (CLI spec §13) — revoke + create flow not in v1alpha1.
- **Workforce SSO multiplexing** (CLI spec §13) — single Dex flow in v1alpha1.
- **Deployment discovery** (CLI spec §13) — `~/.config/ach/config.yaml` registry only.

</deferred>

---

*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Context gathered: 2026-05-29*
