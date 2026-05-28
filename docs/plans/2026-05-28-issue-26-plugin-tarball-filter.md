# Plugin Tarball Content Filter — Implementation Plan (issue #26)

**Issue:** https://github.com/ackstorm/ach/issues/26

**Goal:** Strip multi-runtime noise, tests, repo metadata, and unrelated mirrors from the cached Plugin tarball so downstream `${CLAUDE_PLUGIN_ROOT}` consumers see only runtime-relevant files. Target ≥60% size reduction (caveman: 200K / 209 entries → <80K / <100 entries).

**Architecture:** A new pure-logic package `internal/sources/pluginpack` exposes a kind-agnostic `Filter(in []byte) (out []byte, error)` entry point that walks a gzipped Plugin tarball, locates `.claude-plugin/plugin.json`, computes the manifest-driven include set (whitelist edges + parent-dir transitive of every `${CLAUDE_PLUGIN_ROOT}/<path>` reference), and emits a fresh gzipped tar containing only that subset. The Plugin-CR reconciler hooks into the existing §10.3 single-funnel (`materializeExternalRef`) at a new Step 5.5, guarded by `deps.Kind == "plugin"` so Prompt and Artifact CR paths are byte-identical to pre-issue-26 behavior.

## Problem statement

`Plugin/caveman` (resolving `JuliusBrussee/caveman@main`) currently materializes a cached artifact `<CacheRoot>/plugin/<name>.tar.gz` containing the WHOLE upstream repo: tests, benchmarks, docs, build artifacts, installer JS, repo metadata, and — significantly — packaging for OTHER runtimes (`.codex/`, `.codex-plugin/`, `.junie/`, `.kiro/`, `.roo/`, `gemini-extension.json`, `src/plugins/opencode/`, `AGENTS.md`, `GEMINI.md`, nested `plugins/<name>/` Codex-style mirrors). Live cluster baseline (2026-05-28): 200K / 209 entries; ~70-80% is non-runtime noise.

The CR kind is the contract:

- `Plugin` CR → read ONLY `<plugin_root>/.claude-plugin/plugin.json`. Ignore `marketplace.json` even if it co-exists.
- `PluginMarketplace` CR → read ONLY `<root>/.claude-plugin/marketplace.json`. Ignore loose `plugin.json` files. **Out of scope for this task** (tracked as a follow-up).

## Locked decisions

### Missing-manifest failure mode — Strict fail

When `<plugin_root>/.claude-plugin/plugin.json` is absent at fetch time, the reconciler surfaces `Synced=False reason=UpstreamInvalid` (mapped from `sources.ErrUpstreamInvalid` via the typed `pluginpack.ErrManifestMissing` sentinel; the existing `classifyFetchError` already maps wrapped `ErrUpstreamInvalid` to `ReasonUpstreamInvalid`).

**Why:** the Plugin CR lens contract IS "read plugin.json". An absent manifest means a malformed plugin from this lens's point of view. Loud failure beats silently shipping a whitelist-only cache that downstream consumers cannot use because no manifest means no entry-point references. This closes a pre-existing latent gap (`verifyPluginManifest` is wired only on the marketplace path — the bare Plugin path previously went green against manifest-less repos).

### Manifest reference granularity — Parent dir, transitively

For each `${CLAUDE_PLUGIN_ROOT}/<path>` reference (and each bare-relative-path value in the manifest's dedicated path fields), the filter includes the path's parent directory and EVERYTHING under it (recursive).

**Why:** matches the issue text ("transitively include their parent dirs"). Robust against the common pattern of a manifest referencing an entry-point that imports peer modules in the same directory — e.g. caveman's manifest references `src/hooks/caveman-activate.js`, but the sibling `src/hooks/caveman-mode-tracker.js` is also a hook and likely a runtime dependency. Per-file precision is brittle for almost no storage savings.

### Size-cap semantics — Filtered output only

The existing `ACH_PLUGIN_MAX_SIZE_MIB` cap continues to apply during the staging copy AFTER the filter has run — i.e. against the filtered tarball bytes that will land in `<CacheRoot>/plugin/<name>.tar.gz`.

**Why:** the cap is a user-visible "what will my cache hold" budget; raw pre-filter clone bytes are already capped at `gitDefaultMaxCloneBytes` (512 MiB) inside the git engine, so defense-in-depth is preserved. Capping the filtered output is the semantic the user actually understands.

## Implementation surface

| Path | Role |
|------|------|
| `internal/sources/pluginpack/doc.go` | Package doc; exports `ErrManifestMissing` sentinel. |
| `internal/sources/pluginpack/filter.go` | `Filter(in []byte) (out []byte, error)` entry point; gzip walk; emit-pass; explicit TypeDir entries. |
| `internal/sources/pluginpack/manifest.go` | `parsePluginJSON` + `extractReferences`: recursive JSON walk, `${CLAUDE_PLUGIN_ROOT}/<path>` regex, bare-relative-path heuristic, path-traversal rejection. |
| `internal/sources/pluginpack/filter_test.go` | 10 table-driven unit tests (caveman happy path, missing manifest, traversal, symlink/device drop, explicit dirs, JSON null/numeric, whitelist edges, `./`-prefix normalization, corrupt gzip, output validity). |
| `internal/controller/ach/external_ref_refresh.go` | Step 5.5 wired between Step 5 staging-init and Step 6 size-cap copy, gated `if deps.Kind == "plugin"`. |
| `internal/controller/ach/external_ref_refresh_test.go` | Envtest fixtures updated: pure state-machine tests use `Kind="prompt"`/`"artifact"`; Plugin reconciler tests fed `minimalPluginTarGz(t)` so the filter accepts the body. |
| `internal/controller/ach/main_wiring_envtest_test.go` | End-to-end wiring test fed `minimalPluginTarGz`; byte-equality assertion on cached body dropped (filter rewrites it). |

## Whitelist edges

The filter INCLUDES the following entries (and drops everything else):

- `.claude-plugin/plugin.json` (exact, required)
- `LICENSE` / `LICENSE.<ext>` (root only)
- `LICENSE-<suffix>` (e.g. `LICENSE-MIT`, root only)
- `README.md` (exact, root only)
- Root convention directories: `commands/`, `agents/`, `skills/`, `hooks/`, `mcpServers/`
- Every parent directory transitively referenced by a `${CLAUDE_PLUGIN_ROOT}/<path>` value in the manifest
- Bare-relative-path values in the manifest's dedicated path fields (`commands`, `agents`, `skills`, `hooks`, `mcpServers`, `outputStyles`, `themes`, `lspServers`, `monitors`)

The filter DROPS:

- `.gitkeep` and other VCS metadata
- Multi-runtime mirrors: `.codex/`, `.junie/`, `.kiro/`, `.roo/`, `gemini-extension.json`, `AGENTS.md`, `GEMINI.md`, nested `plugins/<name>/`, `src/plugins/opencode/`
- Tests / benchmarks (when not referenced by the manifest)
- Symlink, device, and FIFO tar entries
- `..`-bearing entries

Placeholder scope: the filter honors only `${CLAUDE_PLUGIN_ROOT}/<path>` references. The other Claude Code manifest placeholders (`${CLAUDE_PLUGIN_DATA}`, `${user_config.*}`, `$VAR_NAME`, `${path}`, `$ARGUMENTS`) either point outside the tarball, are not paths, or are dynamic — see the package doc comment.

## Out of scope

- **PluginMarketplace inner-fetch reuse.** The `pluginpack.Filter` signature is kind-agnostic so a future marketplace inner-fetch path is a one-line wire-up. Generalizing the API surface (a `BodyTransform` field on `ExternalRefRefreshDeps`) is the follow-up task's scope. A `TODO(#26-followup)` marker is left in `external_ref_refresh.go`.
- **`NotModified` (304) path re-filter for pre-filter caches.** Plugin CRs whose cache pre-dates this change keep their unfiltered tarball until the next non-304 reconcile re-fetches and re-filters. Users wanting immediate filtering can apply `ach.ackstorm.ai/force-refresh`. No migration logic in this task.

## Acceptance criteria (mapped to issue #26)

| AC | Description | Verification |
|----|-------------|--------------|
| AC1 | Cached Plugin tarball contains ONLY the whitelist + manifest-referenced parent dirs | `tar tzf /var/cache/ach/plugin/caveman.tar.gz` enumerates only whitelisted entries |
| AC2 | Multi-runtime noise dropped | grep for `.codex`, `.junie`, `AGENTS.md`, `GEMINI.md`, `plugins/caveman/`, etc. → all absent |
| AC3 | ≥60% size reduction for caveman (200K → <80K, 209 entries → <100) | `stat -c '%s'` and `tar tzf | wc -l` on the cached file |
| AC4 | `SourceReachable=True/Synced` and `Synced=True/Synced` conditions preserved after the filter lands | `kubectl get plugin/caveman -o jsonpath='{.status.conditions[*]}'` |
| AC5 | Manifest-less Plugin repos surface `Synced=False reason=UpstreamInvalid` (closes the pre-existing latent gap) | Unit test `TestFilter_MissingManifest_StrictFail` (in addition to runtime behavior) |

## Verification recipe

```bash
# 1. Unit tests
./scripts/dev.sh go test -count=1 ./internal/sources/pluginpack/...

# 2. Envtest (controller wiring)
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestMaterializeExternalRef
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestPluginReconciler
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestMainWiring_PluginReconciler_EndToEndWithFakeFetcher

# 3. Live cluster (kind)
./scripts/dev.sh make operator-redeploy
./scripts/dev.sh kubectl -n ach-system annotate plugin/caveman ach.ackstorm.ai/force-refresh="$(date +%s)" --overwrite
./scripts/dev.sh kubectl -n ach-system wait \
  --for=jsonpath='{.status.conditions[?(@.type=="Synced")].status}'=True \
  plugin/caveman --timeout=300s

# 4. Inspect the filtered cache
./scripts/dev.sh kubectl -n ach-system exec deploy/ach-operator -c content-service -- \
  tar tzf /var/cache/ach/plugin/caveman.tar.gz | sort
./scripts/dev.sh kubectl -n ach-system exec deploy/ach-operator -c content-service -- \
  stat -c '%s' /var/cache/ach/plugin/caveman.tar.gz

# 5. Conditions
./scripts/dev.sh kubectl -n ach-system get plugin/caveman \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status}/{.reason}{"\n"}{end}'
```

## Risks / Rollback

| Risk | Likelihood | Detection | Rollback |
|------|------------|-----------|----------|
| Bare-relative-path heuristic misses a schema-declared path field, dropping a runtime file | Medium | Unit `TestFilter_CavemanShape_HappyPath` fails; live grep for an expected file fails | Revert `internal/sources/pluginpack/manifest.go`; widen the heuristic; add regression test. Hook wiring stays in place. |
| Plugin CRs against manifest-less repos that previously went green now surface `Synced=False reason=UpstreamInvalid` | Confirmed (by design) | `kubectl get plugin -A -o jsonpath` survey for `Synced=False` after rollout | Strict improvement — fix the upstream repo. If a fast revert is required for external reasons, delete the `if deps.Kind == "plugin"` block in `external_ref_refresh.go`; the package stays present and dormant. |
| Forwarder / CLI consumer breaks on explicit directory entries the filter emits | Low | Forwarder logs / `ach hydrate` end-to-end smoke fails | Drop the explicit dir-entry emission in `filter.go` (one constant + one branch); regenerate; redeploy. Filter signature unchanged. |
| Re-emit loses a header field a downstream consumer cared about (mtime, mode) | Low | Downstream error referring to mode/mtime/owner | Add the missing field to the fresh-header allocation in `filter.go`; regression test. |
| `io.ReadAll(result.Body)` in `materializeExternalRef` blows memory for an unexpectedly large Plugin tarball | Low (the git engine already buffers the whole tarball before returning the body; `gitDefaultMaxCloneBytes=512 MiB` caps the worst case) | Operator OOM during plugin reconcile | Tighten `ACH_PLUGIN_MAX_SIZE_MIB` in Helm values; or refactor `Filter` to a streaming `(io.Reader) io.Reader` signature in a follow-up. |

**Clean-revert path:** `git revert <merge commit>` reverts cleanly because the only edits to existing files are (a) the Step 5.5 block in `external_ref_refresh.go`, (b) test fixture updates in `external_ref_refresh_test.go` and `main_wiring_envtest_test.go`, and (c) this docs page. The new `internal/sources/pluginpack` directory is purely additive — deleting it has no compile-time dependents outside the Step 5.5 import.

## Cross-references

- The internal planning workspace for this quick task lives at `.planning/quick/260528-dmz-plugin-tarball-content-filter-issue-26/` (gitignored). This doc is the canonical, committed record.
- Upstream schema reference: https://www.schemastore.org/claude-code-plugin-manifest.json
- Sister task (deferred): apply `pluginpack.Filter` from the `PluginMarketplace` inner-fetch path once the marketplace lens is ready to consume the same filter.
