// SPDX-License-Identifier: Apache-2.0

// Package pluginpack implements a manifest-aware whitelist filter for
// Plugin-CR-shaped gzipped tarballs (Hub §10.3 plugin lens). The Filter
// entry point takes the raw bytes of an upstream-fetched tar.gz and
// returns a new tar.gz containing ONLY the runtime-relevant subset:
//
//   - `.claude-plugin/plugin.json`            (exact, required)
//   - `LICENSE` / `LICENSE.<ext>`             (root only)
//   - `README.md`                             (root only)
//   - root convention directories             `commands/`, `agents/`,
//     `skills/`, `hooks/`,
//     `mcpServers/`
//   - every parent directory transitively referenced by a
//     `${CLAUDE_PLUGIN_ROOT}/<path>` value in the manifest, plus every
//     bare-relative-path value the manifest's dedicated path fields
//     declare (per the Claude Code plugin manifest schemastore schema —
//     hooks, commands, agents, skills, outputStyles, themes, mcpServers,
//     lspServers, monitors, commands[].source).
//
// Everything else is dropped — multi-runtime mirrors (`.codex/`,
// `.junie/`, `.kiro/`, `.roo/`, `gemini-extension.json`, `AGENTS.md`,
// `GEMINI.md`, nested `plugins/<name>/`, `src/plugins/opencode/`),
// tests, benchmarks, build artifacts, repo metadata (`.gitkeep`), and
// any symlink / device / FIFO tar entry.
//
// Locked decisions (issue #26):
//
//   - Parent-dir transitive inclusion. Per-file precision is brittle
//     because manifests routinely reference a single entry-point in a
//     directory whose sibling files are the entry-point's peers.
//
//   - Missing manifest → strict fail. A Plugin CR is by definition a
//     `.claude-plugin/plugin.json`-driven artifact; if the manifest is
//     absent, the upstream is not a Plugin from this CR's point of
//     view. Filter returns ErrManifestMissing wrapping
//     sources.ErrUpstreamInvalid, which the reconciler's
//     classifyFetchError maps to ReasonUpstreamInvalid.
//
//   - Size cap is the caller's concern. The caller wraps the filtered
//     bytes in io.LimitReader for the staging copy (post-filter cap
//     semantics).
//
// Placeholder scope. The Claude Code plugin manifest schema declares
// multiple placeholder syntaxes; this filter honors only
// `${CLAUDE_PLUGIN_ROOT}/<path>` references. The others are
// intentionally NOT path references inside the tarball:
//
//   - `${CLAUDE_PLUGIN_DATA}` — runtime data dir, lives outside the
//     tarball at consumer time.
//   - `${user_config.*}`      — user-config interpolation, not a path.
//   - `$VAR_NAME` / `${VAR_NAME}` — generic env vars.
//   - `${path}`                — hook-input JSON interpolation, dynamic.
//   - `$ARGUMENTS`             — hook input JSON, dynamic.
//
// The package is kind-agnostic so a future PluginMarketplace
// inner-fetch path can wire the same filter with one line at the
// marketplace controller's per-entry materialize step. The current
// scope is the plain `Plugin` CR path only (issue #26).
package pluginpack

import "errors"

// ErrManifestMissing is returned (wrapping sources.ErrUpstreamInvalid)
// when a Plugin tarball contains no `.claude-plugin/plugin.json` entry.
// Callers can dispatch on this with errors.Is to distinguish
// "malformed plugin" from other UpstreamInvalid causes (gzip header
// rot, traversal-rejection, JSON unmarshal failure).
var ErrManifestMissing = errors.New("pluginpack: plugin.json absent")
