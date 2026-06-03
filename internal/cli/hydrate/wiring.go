// SPDX-License-Identifier: Apache-2.0

// Default Extractor + AdapterDispatcher implementations + STATE-05
// --sync handler — the W3-05 wiring that turns the orchestrator's
// stub-fed interfaces into a working engine.
//
// The orchestrator (07-W1-06 commit.go) holds Extractor and
// AdapterDispatcher as interface fields and short-circuits when they
// are nil (W1 stub). This file provides the concrete impls + a
// NewWiring constructor the cobra layer (cmd/ach-cli/cmd/hydrate.go
// D-03 refactor) uses to populate those fields before dispatching
// Run().
//
// Implementations:
//
//   - extractorImpl wraps internal/cli/extract.{FetchContent,
//     StageAndPublish}. It infers ResourceKind from the
//     ContentRef.DownloadURL path (`/content/{kind}/{name}`) — falling
//     back to KindArtifact when the URL is unparseable so bomb-defense
//     caps remain enforced even for malformed manifests.
//
//   - adapterDispatcherImpl wraps adapter.Lookup + RenderRuntime. Each
//     adapter.FileWrite is published under toolRoot (the tool's native
//     config dir — workspace root, or $HOME under --global; NOT achDir)
//     via SURGICAL forward-merge: only ACH's keys are upserted into the
//     user's existing JSON/TOML config, preserving the user's other
//     servers/settings. A user edit to one of OUR keys is caught by the
//     per-key §8.4 drift check → exit.Drift unless --force.
//
//   - Sync implements STATE-05 / D-16 verbatim: collect targets in
//     `prev` but missing from `new`; sort deepest-first by path depth;
//     for each target compare its on-disk xxh3 to prev's recorded
//     hash; mismatch → preserve with stderr warning (drift wins
//     unless --force); match (or --force) → delete OR inverse-merge
//     (for Merge=deep with Keys[]; for Merge=composite via
//     <!-- ach:begin -->...<!-- ach:end --> regex replacement). After
//     per-file work, walk parent dirs deepest-first calling os.Remove
//     which honors ENOTEMPTY silently — empty dirs prune, non-empty
//     preserved. CLI NEVER recursively deletes a directory.
//
// All file writes go through state.WriteAtomic (the STATE-07 atomic
// publication primitive) so a mid-Sync crash never leaves a partially
// rewritten inverse-merge file. Verbatim deletions use os.Remove
// (single-file unlink — atomic).

package hydrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// File-extension discriminators for adapter runtime-config merge/sync.
const (
	extJSON = ".json"
	extTOML = ".toml"
)

// Canonical state.FileEntry.Merge strings (§8.2 schema). Kept as consts so the
// value is single-sourced across mergeKindToString, the syncDeep dispatch, and
// tests (goconst).
const (
	mergeStrDeep      = "deep"
	mergeStrComposite = "composite"
	mergeStrReplace   = "replace"
)

// extractorImpl satisfies the hydrate.Extractor interface declared in
// result.go. It wraps the W2-02 FetchContent + StageAndPublish flow.
// limits + allowSymlinks are bound at construction (the cobra layer
// reads limits via extract.LoadLimits and allowSymlinks via the
// --allow-symlinks flag).
type extractorImpl struct {
	client        *httpclient.Client
	limits        extract.Limits
	allowSymlinks bool
}

// ExtractContent implements hydrate.Extractor. Flow:
//
//  1. Parse ContentRef.DownloadURL to derive the ResourceKind from the
//     "/content/{kind}/{name}" path segment. Defaults to KindArtifact
//     on parse failure (most-restrictive cap → fail-safe).
//  2. Call extract.FetchContent(ctx, client, kind, name) — returns the
//     live *http.Response (body unread).
//  3. Compute the workspace-relative target path:
//     <kind>/<name>(.tar.gz) under achDir.
//  4. Call extract.StageAndPublish(ctx, body, Content-Type, finalAbs,
//     achDir, kind, limits, allowSymlinks).
//  5. Translate PublishResult.WrittenFiles into hydrate.FileWrite
//     entries — Target rooted at the workspace-relative path the
//     orchestrator's state ledger expects.
//
// PublishResult.Skipped is folded into the result as a zero-WrittenFiles
// outcome — the existing file remained in place. SourceHash flows
// through unchanged.
func (e *extractorImpl) ExtractContent(ctx context.Context, ref manifest.ContentRef, achDir string, prev *state.File) (ExtractResult, error) {
	kind, name := classifyDownloadURL(ref.DownloadURL, ref.Name)
	if err := validateContentName(name); err != nil {
		return ExtractResult{}, fmt.Errorf("extract content %s: %w", kind, err)
	}

	resp, err := extract.FetchContent(ctx, e.client, kind, name)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("extract content %s/%s: %w", kind, name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	finalRelPath := filepath.Join(string(kind), name)
	finalAbs := filepath.Join(achDir, finalRelPath)
	contentType := resp.Header.Get("Content-Type")

	// Re-hydrate no-op skip (W6-01 Bug E): only when prior state records
	// this content's upstream SourceHash do we hash the freshly-fetched
	// bytes first — an unchanged source needs no disk write (the on-disk
	// tree is preserved, FilesWritten==0). A fresh hydrate (no prior entry)
	// streams straight through StageAndPublish with a single spill.
	if prevHash := priorContentSourceHash(prev, kind, finalRelPath); prevHash != "" {
		staged, srcXxh3, serr := extract.SpillAndHashXxh3(achDir, resp.Body)
		if serr != nil {
			return ExtractResult{}, fmt.Errorf("extract content %s/%s: %w", kind, name, serr)
		}
		defer func() { _ = os.RemoveAll(filepath.Dir(staged)) }()
		if srcXxh3 == prevHash {
			return ExtractResult{SourceHash: srcXxh3}, nil
		}
		f, oerr := os.Open(staged)
		if oerr != nil {
			return ExtractResult{}, fmt.Errorf("extract content %s/%s: reopen staged: %w", kind, name, oerr)
		}
		defer func() { _ = f.Close() }()
		return e.stageAndMap(ctx, f, contentType, finalRelPath, finalAbs, achDir, kind)
	}

	return e.stageAndMap(ctx, resp.Body, contentType, finalRelPath, finalAbs, achDir, kind)
}

// stageAndMap performs the delete-before-replace + StageAndPublish + FileWrite
// projection shared by ExtractContent's skip-capable and fresh paths.
//
// Delete-before-replace is DIRECTORY-ONLY (W6-01 Bug E): renameAtomic happily
// replaces a pre-existing regular file, but cannot rename over a non-empty
// directory — and the W5-01 orchestrator "replace step" was never wired. We
// therefore remove only a pre-existing directory target (an already-extracted
// plugin). Leaving regular files in place preserves StageAndPublish's step-3
// single-file no-op short-circuit.
func (e *extractorImpl) stageAndMap(ctx context.Context, body io.Reader, contentType, finalRelPath, finalAbs, achDir string, kind extract.ResourceKind) (ExtractResult, error) {
	// Defense-in-depth Rel-containment: even though validateContentName at the
	// classify layer rejects ".." / absolute / separator-bearing names, a future
	// caller (or a refactor that reaches stageAndMap by a path bypassing
	// ExtractContent) must NOT be able to drive RemoveAll outside achDir.
	if rel, relErr := filepath.Rel(achDir, finalAbs); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ExtractResult{}, fmt.Errorf("extract content %s: target %s escapes achDir", kind, finalAbs)
	}
	// Use Lstat (do NOT follow symlinks) so an attacker-controlled symlink at
	// finalAbs cannot redirect RemoveAll to a directory outside achDir.
	if info, statErr := os.Lstat(finalAbs); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ExtractResult{}, fmt.Errorf("extract content %s: symlink at target %s rejected", kind, finalAbs)
		}
		if info.IsDir() {
			if rmErr := os.RemoveAll(finalAbs); rmErr != nil {
				return ExtractResult{}, fmt.Errorf("extract content %s: remove prior dir %s: %w", kind, finalAbs, rmErr)
			}
		}
	}

	pr, err := extract.StageAndPublish(ctx, body, contentType,
		finalAbs, achDir, kind, e.limits, e.allowSymlinks)
	if err != nil {
		return ExtractResult{}, err
	}

	out := ExtractResult{SourceHash: pr.SourceHash}
	for _, fw := range pr.WrittenFiles {
		out.WrittenFiles = append(out.WrittenFiles, FileWrite{
			Target:     filepath.Join(finalRelPath, fw.RelPath),
			Hash:       fw.Hash,
			SourceHash: pr.SourceHash,
		})
	}
	return out, nil
}

// priorContentSourceHash returns the upstream SourceHash recorded for the
// content rooted at finalRelPath in the matching state bucket, or "" when no
// prior entry exists. All per-file entries of one archive share the archive's
// SourceHash (D-14), so the first match suffices.
func priorContentSourceHash(prev *state.File, kind extract.ResourceKind, finalRelPath string) string {
	if prev == nil {
		return ""
	}
	var bucket []state.FileEntry
	switch kind {
	case extract.KindPlugin:
		bucket = prev.Plugins
	case extract.KindArtifact:
		bucket = prev.Artifacts
	case extract.KindPrompt:
		bucket = prev.Prompts
	default:
		return ""
	}
	prefix := finalRelPath + string(filepath.Separator)
	for _, ent := range bucket {
		if ent.SourceHash == "" {
			continue
		}
		if ent.Target == finalRelPath || strings.HasPrefix(ent.Target, prefix) {
			return ent.SourceHash
		}
	}
	return ""
}

// validateContentName rejects content `name` values that could redirect the
// publication path outside achDir. The server-supplied manifest is the source
// of `ref.Name` and the trailing segment of `ref.DownloadURL`; a malicious or
// compromised manifest with name=".." would otherwise resolve to
// os.RemoveAll(achDir) at the stageAndMap delete-before-replace step. Vectors
// rejected: empty, ".", "..", hidden-prefix names (".git", ".env"), names
// containing "/" or "\" path separators, and absolute paths. Defense-in-depth
// Rel-containment runs in stageAndMap regardless.
func validateContentName(name string) error {
	if name == "" {
		return errors.New("content name: empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("content name: traversal segment %q", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("content name: hidden-directory prefix %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("content name: path separator in %q", name)
	}
	if strings.ContainsAny(name, "?#%") {
		return fmt.Errorf("content name: url metacharacter in %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("content name: control character in %q", name)
		}
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("content name: absolute path %q", name)
	}
	return nil
}

// classifyDownloadURL parses `/content/{kind}/{name}` and returns the
// matching ResourceKind + name. Defaults to KindArtifact on parse
// failure (the most restrictive bomb-defense cap so a malformed URL
// cannot bypass enforcement). Falls back to fallbackName when the
// URL has no /content/.../name segment.
func classifyDownloadURL(downloadURL, fallbackName string) (extract.ResourceKind, string) {
	if downloadURL == "" {
		return extract.KindArtifact, fallbackName
	}
	u, err := url.Parse(downloadURL)
	if err != nil {
		return extract.KindArtifact, fallbackName
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Expect ["content", "{kind}", "{name}"] — find the "content" segment
	// and read the two following segments.
	for i, p := range parts {
		if p == "content" && i+2 < len(parts) {
			kind := extract.ResourceKind(parts[i+1])
			switch kind {
			case extract.KindPlugin, extract.KindArtifact, extract.KindPrompt:
				return kind, parts[i+2]
			}
		}
	}
	return extract.KindArtifact, fallbackName
}

// adapterDispatcherImpl satisfies the hydrate.AdapterDispatcher
// interface declared in result.go. platformID is bound at
// construction (the cobra layer resolves it via ResolvePlatform or
// Autodetect). force bypasses the per-key drift refusal (publishRuntimeFile)
// so a user edit to OUR key is overwritten rather than preserved.
type adapterDispatcherImpl struct {
	platformID string
	force      bool
	// global marks --global scope so Render can remap adapters whose GLOBAL
	// config path differs from the simple $HOME-join (currently opencode).
	global bool
}

// Render implements hydrate.AdapterDispatcher. Flow:
//
//  1. Lookup the adapter by platformID. Miss → typed CodedError
//     (General) so the cobra error envelope stays consistent.
//  2. Call ad.RenderRuntime(ctx, m, s) — returns []adapter.FileWrite.
//  3. For each FileWrite, publishRuntimeFile resolves the target under
//     toolRoot (the tool's native config location, NOT achDir),
//     surgically merges ONLY our keys into the user's existing config,
//     and enforces the per-key §8.4 drift truth table (a user edit to
//     OUR key → exit.Drift unless --force). The user's other keys are
//     preserved untouched.
//
// State records the canonical hash of OUR contribution (not the merged
// file) so --sync inverse-merge and subsequent drift checks operate on
// our keys only. DroppedComponents stays nil for the runtime path
// (route.Project is the source of drops via the projection leg).
//
// After the RenderRuntime loop, when projectPlugins is true (WIRE-04 /
// D-11 scope gate, derived by the orchestrator as !opts.OnlyRuntime),
// the projection leg runs: route.Project decomposes the extracted plugin
// tree(s) under <achDir>/plugin into the adapter's native layout and each
// projected FileWrite is published through the SAME publishRuntimeFile
// path (per-key drift + no-op skip + atomic publish — D-05). Projected
// entries are returned tagged in RenderResult.ProjectedFiles so step 12
// routes them to state.Plugins[] (D-07), distinct from the runtime
// WrittenFiles bucket. Dropped top-level kinds are aggregated into
// DroppedComponents for the single end-of-hydration stderr warning
// (WIRE-03 / D-12).
func (d *adapterDispatcherImpl) Render(ctx context.Context, m *manifest.Manifest, s *state.File, achDir, toolRoot string, projectPlugins bool) (RenderResult, error) {
	ad, ok := adapter.Lookup(d.platformID)
	if !ok {
		return RenderResult{}, &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("adapter dispatcher: unknown platform %q", d.platformID),
		}
	}

	fws, err := ad.RenderRuntime(ctx, m, s)
	if err != nil {
		return RenderResult{}, fmt.Errorf("adapter %s RenderRuntime: %w", d.platformID, err)
	}

	var result RenderResult
	for _, fw := range fws {
		if d.global {
			fw.Path = remapGlobalPath(d.platformID, fw.Path)
		}
		entry, err := d.publishRuntimeFile(fw, s, toolRoot)
		if err != nil {
			return RenderResult{}, err
		}
		result.WrittenFiles = append(result.WrittenFiles, entry)
	}

	// Projection leg (D-05). Skipped under the scope gate (--only-runtime).
	if projectPlugins {
		if err := d.projectPlugins(ad, s, achDir, toolRoot, &result); err != nil {
			return RenderResult{}, err
		}
	}

	return result, nil
}

// projectPlugins runs the route.Project projection leg for every extracted
// plugin tree under <achDir>/plugin, publishing each projected FileWrite
// through the SAME publishRuntimeFile engine as the runtime loop (D-05).
//
// The adapter is type-asserted to route.RuleProvider (the D-06 seam); an
// adapter that does not implement it projects nothing (no-op — forward
// compatible). Each per-plugin subdir under <achDir>/plugin is a separate
// source tree; route.Project is invoked once per tree and the results are
// aggregated. The provenance signal is "" (ACH has no claude-plugin
// provenance axis in Phase 1 per OPENPACKAGE-MAPPING:66 — the ungated rule
// arm matches; the gated branch EXISTS for Phase 2).
//
// Projected paths compose with remapGlobalPath under --global exactly as
// the runtime loop does. Dropped kinds are de-duplicated across all plugin
// trees and appended (sorted) to result.DroppedComponents.
//
// Composite targets are EXEMPT from the CR-01 claimed[] collision fail-fast
// (D-07): multiple plugins legally co-own the host memory file (CLAUDE.md /
// GEMINI.md) via per-id <!-- ach:begin:<plugin> --> blocks, so a second
// composite contributor is not a collision. File-owned MergeReplace keeps the
// check. For each composite FileWrite the dispatcher threads fw.Keys =
// [ent.Name()] before publishFile so (i) the per-id marker carries the plugin
// name and (ii) the state.Plugins[] row records Keys=[plugin-name] for the
// Phase-4 single-block subtract.
//
// Runtime-wins MCP drop (D-10): a projected MergeDeep mcpServers.<id> that a
// runtime RenderRuntime file (result.WrittenFiles, same Target) already owns is
// DROPPED — the runtime-owned (bearer/command-exec) server is never shadowed.
// The dropped id is recorded once in result.DroppedComponents.
//
// Error handling (D-06 Claude's-discretion): each FileWrite is published
// atomically (tmp+rename via state.WriteAtomic / mergeForward), so a crash
// mid-loop leaves already-published files intact and unpublished ones
// absent — best-effort per-file atomicity matching the runtime loop. There
// is deliberately NO all-or-nothing transaction across the projected set.
// The first error encountered aborts (same as the runtime loop).
func (d *adapterDispatcherImpl) projectPlugins(ad adapter.Adapter, s *state.File, achDir, toolRoot string, result *RenderResult) error {
	rp, ok := ad.(route.RuleProvider)
	if !ok {
		return nil
	}
	rules := rp.ProjectionRules()

	pluginRoot := filepath.Join(achDir, "plugin")
	entries, err := os.ReadDir(pluginRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No plugins extracted this hydrate — nothing to project.
			return nil
		}
		return fmt.Errorf("adapter %s read plugin root %s: %w", d.platformID, pluginRoot, err)
	}

	if result.ProjectedByKind == nil {
		result.ProjectedByKind = map[string]int{}
	}
	if result.DroppedByKind == nil {
		result.DroppedByKind = map[string][]string{}
	}

	// Dedup dropped kinds across all plugin trees (route.Project already
	// dedups per-run; aggregating across multiple plugin subdirs needs a
	// second-layer dedup) and stable-sort for byte-stable stderr (D-12).
	seen := map[string]bool{}
	var dropped []string
	// claimed maps a FINAL published Target → the plugin name that first
	// claimed it. Under D-01's flat kind-routing there is NO per-plugin
	// destination namespace, so two distinct plugins both shipping e.g.
	// rules/foo.md would both project to .claude/rules/foo.md. That is an
	// unresolvable cross-plugin collision (CR-01): silently letting the
	// second publishFile (MergeReplace → WriteAtomic) overwrite the first
	// would lose a file AND append a second state.Plugins[] row with an
	// identical Target, so findPluginEntry's first-match would bind
	// re-hydrate drift to the wrong owner. We detect the collision on the
	// post-remap path (identical to what lands in state.Plugins[]) and
	// fail-fast — matching the existing first-error abort in this loop.
	claimed := map[string]string{}
	for _, ent := range entries {
		if !ent.IsDir() {
			// Plugin archives extract to a directory per name; a stray
			// regular file (e.g. an un-extracted blob) carries no routable
			// resource kinds — skip it.
			continue
		}
		// Plugin-name segment validation (T-01-04). D-01 keeps the plugin
		// name OUT of the destination path, but it is still joined into the
		// SOURCE path below and appears in error strings — a NEW
		// path-construction surface. Reject any name that is not a single
		// safe basename BEFORE reading any file under it (defense-in-depth
		// on top of the SAFE-01/02 extract-time checks). Sibling pattern:
		// route.resolveRecursiveGlobTarget's T-01-01 destination guard.
		if verr := validatePluginName(ent.Name()); verr != nil {
			return fmt.Errorf("adapter %s plugin directory %q: %w", d.platformID, ent.Name(), verr)
		}
		pluginSrc := filepath.Join(pluginRoot, ent.Name())
		// source == "" : Phase-1 ungated arm (no claude-plugin provenance
		// axis yet). The gated branch exists for Phase 2.
		pr, perr := route.Project(rules, pluginSrc, "")
		if perr != nil {
			return fmt.Errorf("adapter %s project plugin %s: %w", d.platformID, ent.Name(), perr)
		}
		for k, n := range pr.KeptByKind {
			result.ProjectedByKind[k] += n
		}
		for _, fw := range pr.FileWrites {
			if d.global {
				fw.Path = remapGlobalPath(d.platformID, fw.Path)
			}
			// CR-01 exemption (D-07): composite targets are co-owned by multiple
			// plugins via per-id blocks, so they SKIP both the claimed[]
			// fail-fast AND the claimed[] write (otherwise the first composite
			// contributor would block the second). File-owned MergeReplace keeps
			// the cross-plugin collision check.
			if fw.Merge != adapter.MergeComposite {
				// Cross-plugin Target-collision detection on the FINAL published
				// path (post-remap), checked BEFORE publishFile so no
				// duplicate-Target row is ever appended to ProjectedFiles.
				if owner, dup := claimed[fw.Path]; dup && owner != ent.Name() {
					return fmt.Errorf(
						"adapter %s: plugin %q and plugin %q both project to %q — cross-plugin destination collision (flat kind-routing has no namespace to disambiguate; rename or remove one plugin's resource)",
						d.platformID, owner, ent.Name(), fw.Path)
				}
				claimed[fw.Path] = ent.Name()
			}

			// Plugin-name threading (D-07): composite FileWrites carry
			// Keys=[plugin-name] (NOT dotted JSON keys) — this supplies the
			// per-id marker in publishFile's composite arm and records the
			// composite state row's Keys for the Phase-4 single-block subtract.
			if fw.Merge == adapter.MergeComposite {
				fw.Keys = []string{ent.Name()}
			}

			// Runtime-wins MCP drop (D-10): exclude any projected mcpServers.<id>
			// a runtime WrittenFiles entry (same Target) already owns; record the
			// drop. If nothing survives, skip publishing this FileWrite entirely.
			published, fwDrops, derr := dropRuntimeOwnedMCP(&fw, result.WrittenFiles)
			if derr != nil {
				return fmt.Errorf("adapter %s runtime-wins drop %s: %w", d.platformID, fw.Path, derr)
			}
			for _, dr := range fwDrops {
				if !seen[dr] {
					seen[dr] = true
					dropped = append(dropped, dr)
				}
			}
			if !published {
				continue
			}

			// Look up the prior projected entry in the PLUGINS bucket (D-07)
			// so an unchanged re-hydrate hits the publishFile no-op skip.
			entry, err := d.publishFile(fw, findPluginEntry(s, fw.Path), toolRoot)
			if err != nil {
				return err
			}
			result.ProjectedFiles = append(result.ProjectedFiles, entry)
		}
		for _, dr := range pr.Dropped {
			if !seen[dr] {
				seen[dr] = true
				dropped = append(dropped, dr)
			}
			result.DroppedByKind[dr] = appendUniqueSorted(result.DroppedByKind[dr], ent.Name())
		}
	}

	sort.Strings(dropped)
	result.DroppedComponents = append(result.DroppedComponents, dropped...)
	return nil
}

// MCP server-key prefixes per adapter config format (D-17). The projected
// plugin MCP FileWrite contributes a dotted key of the form
// "<prefix><id>" from its mcpDeepKeys Transform:
//   - claude / gemini settings.json → "mcpServers.<id>"
//   - codex .codex/config.toml      → "mcp_servers.<id>"
//   - opencode .opencode/opencode.json → "mcp.<id>"
const (
	mcpServersPrefix     = "mcpServers."  // claude / gemini (JSON)
	mcpServersTOMLPrefix = "mcp_servers." // codex (TOML)
	mcpOpencodePrefix    = "mcp."         // opencode (JSON)
)

// mcpContainerKeyFor returns the dotted-key prefix the contributed MCP keys of
// fw use, derived from fw.Keys. Codex uses "mcp_servers.", opencode "mcp.",
// claude/gemini "mcpServers.". When no contributed key matches a known prefix
// (no MCP keys), the default "mcpServers." is returned harmlessly — the
// collision loop will then never match. The longest-prefix check orders
// mcp_servers. / mcpServers. before the bare "mcp." so a "mcpServers.x" key is
// not mis-attributed to the opencode "mcp." prefix.
func mcpServersPrefixFor(keys []string) string {
	for _, k := range keys {
		switch {
		case strings.HasPrefix(k, mcpServersTOMLPrefix):
			return mcpServersTOMLPrefix
		case strings.HasPrefix(k, mcpServersPrefix):
			return mcpServersPrefix
		case strings.HasPrefix(k, mcpOpencodePrefix):
			return mcpOpencodePrefix
		}
	}
	return mcpServersPrefix
}

// dropRuntimeOwnedMCP implements the D-10/D-17 runtime-wins MCP id-clash drop.
// For a projected MergeDeep FileWrite, any contributed <prefix><id> that a
// runtime WrittenFiles entry targeting the SAME Path already owns is excluded
// from the merge: the colliding subtree is removed from fw.Content (parse →
// delete → deterministic re-encode), the id is dropped from fw.Keys, and a
// "mcp:<id> (runtime-owned)" token is returned for the DroppedComponents
// aggregation. The runtime-owned (bearer/command-exec) server is NEVER
// overwritten or shadowed.
//
// Format- and prefix-aware (D-17): the config format is detected from the
// target extension (.toml → TOML for codex, JSON otherwise), and the server-key
// prefix is derived from the contributed keys ("mcp_servers." for codex,
// "mcp." for opencode, "mcpServers." for claude/gemini).
//
// Returns (publish, drops, err): publish=false means every contributed key
// collided, so the caller skips publishing this FileWrite entirely (still
// recording the drops). Non-MergeDeep writes (composite/replace) pass through
// untouched (publish=true, no drops). fw is mutated IN PLACE via the pointer so
// the caller publishes the de-conflicted content.
func dropRuntimeOwnedMCP(fw *adapter.FileWrite, runtime []FileWrite) (bool, []string, error) {
	if fw.Merge != adapter.MergeDeep {
		return true, nil, nil
	}

	// Union the runtime-owned dotted keys for the SAME Target.
	runtimeKeys := map[string]bool{}
	for _, w := range runtime {
		if w.Target != fw.Path {
			continue
		}
		for _, k := range w.Keys {
			runtimeKeys[k] = true
		}
	}
	if len(runtimeKeys) == 0 {
		return true, nil, nil
	}

	// Determine which contributed keys collide.
	var collide []string
	survivors := make([]string, 0, len(fw.Keys))
	for _, k := range fw.Keys {
		if runtimeKeys[k] {
			collide = append(collide, k)
		} else {
			survivors = append(survivors, k)
		}
	}
	if len(collide) == 0 {
		return true, nil, nil
	}

	// Format-aware parse: TOML for codex .toml targets, JSON otherwise.
	isTOML := strings.ToLower(filepath.Ext(fw.Path)) == extTOML
	prefix := mcpServersPrefixFor(fw.Keys)

	doc, err := parseDoc(fw.Content, isTOML)
	if err != nil {
		return false, nil, err
	}
	drops := make([]string, 0, len(collide))
	for _, k := range collide {
		removeDottedKey(doc, k)
		drops = append(drops, "mcp:"+strings.TrimPrefix(k, prefix)+" (runtime-owned)")
	}
	// If the server container is now empty, drop it so we don't write a bare
	// {"<container>":{}} that the deep-merge would otherwise leave behind.
	container := strings.TrimSuffix(prefix, ".")
	if ms, ok := doc[container].(map[string]any); ok && len(ms) == 0 {
		delete(doc, container)
	}

	fw.Keys = survivors
	if len(survivors) == 0 || len(doc) == 0 {
		// Nothing of OURS survives — skip publishing (drops still recorded).
		// But the function's contract is "fw is mutated IN PLACE so the caller
		// publishes the de-conflicted content" (WR-07): when survivors == 0 yet
		// doc still holds OTHER top-level keys (len(doc) != 0), fw.Content must
		// be re-encoded from the de-conflicted doc rather than left at its
		// pre-removal bytes (which still carry the runtime-owned subtree). The
		// current caller skips publishing on !published so this is latent, but
		// a future projection rule contributing non-MCP deep-merge keys would
		// otherwise publish stale content.
		if len(doc) != 0 {
			out, err := encodeDoc(doc, isTOML)
			if err != nil {
				return false, nil, err
			}
			fw.Content = out
		}
		return false, drops, nil
	}

	out, err := encodeDoc(doc, isTOML)
	if err != nil {
		return false, nil, err
	}
	fw.Content = out
	return true, drops, nil
}

// validatePluginName rejects a plugin-directory name that is not a single
// safe path segment. Under D-01 the plugin name never reaches the
// destination path, but projectPlugins still joins it into the SOURCE path
// (filepath.Join(pluginRoot, name)) and embeds it in error strings, so a
// traversal / absolute / multi-segment name is a path-construction surface
// that MUST be validated before any file under it is read. This is
// defense-in-depth: SAFE-01/02 already reject ../, symlinks, and absolute
// paths at extract time, and os.ReadDir yields only basenames in practice —
// the guard closes the contract regardless of how the name was obtained.
// It mirrors the route package's T-01-01 destination guard, applied here to
// the SOURCE segment.
func validatePluginName(name string) error {
	if name == "" {
		return fmt.Errorf("empty name is not a valid plugin segment (escapes plugin root)")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name %q is a relative path segment (escapes plugin root)", name)
	}
	if strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, os.PathSeparator) {
		return fmt.Errorf("name %q contains a path separator (escapes plugin root)", name)
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("name %q is an absolute path (escapes plugin root)", name)
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("name %q is not a single path segment (escapes plugin root)", name)
	}
	return nil
}

// opencodeProjectPrefix is the project-scope path prefix OpenCode FileWrites
// carry (.opencode/opencode.json, .opencode/commands/, .opencode/agents/, …).
// opencodeGlobalPrefix is the XDG global-config root the prefix remaps to.
const (
	opencodeProjectPrefix = ".opencode/"
	opencodeGlobalPrefix  = ".config/opencode/"
)

// remapGlobalPath adjusts an adapter's workspace-relative FileWrite path for
// --global scope where the tool's GLOBAL config location differs from the
// simple $HOME-join. OpenCode reads its global config from the XDG root
// ~/.config/opencode/ — NOT ~/.opencode/ (the latter is the PROJECT path).
//
// D-22 generalization: ALL projected `.opencode/*` paths remap to
// `.config/opencode/*` under global scope — not just `.opencode/opencode.json`
// but also `.opencode/commands/`, `.opencode/agents/`, `.opencode/skills/`, …
// Project scope (non-global) is never reached here (the caller gates on
// d.global). The other three adapters' relative paths (.claude/, .codex/,
// .gemini/) are correct under $HOME as-is and pass through unchanged. Kept in
// the orchestrator (dispatcher) rather than the adapter so RenderRuntime stays
// scope-agnostic.
//
// The remap is a pure prefix substitution on an already-traversal-guarded
// relative path (route.resolveRecursiveGlobTarget's T-01-01 guard ran before
// this), so no ".." can be reintroduced by the concat (T-03-02).
func remapGlobalPath(platformID, path string) string {
	if platformID == "opencode" && strings.HasPrefix(path, opencodeProjectPrefix) {
		return opencodeGlobalPrefix + strings.TrimPrefix(path, opencodeProjectPrefix)
	}
	return path
}

// publishRuntimeFile writes one adapter runtime-config FileWrite via
// SURGICAL MERGE + PER-KEY DRIFT.
//
// Target resolution: fw.Path joins toolRoot (workspace root in project
// scope, $HOME in --global) — the tool's native config location — NOT
// achDir. ACH's private state/cache stay under achDir.
//
// Per-key drift (§8.4 truth table, applied to OUR keys only): we compare
// the hash of our contributed subtree as it currently sits on disk
// (onDisk) against the prior state record (Hash/SourceHash) and the fresh
// render (source). A user edit to OUR key surfaces as LocalEditPreserve /
// ConflictPreserve → exit 2 (preserve), unless --force. The user's OTHER
// keys are invisible to this comparison and are never claimed, refused, or
// removed — only merged around. A no-op (disk + upstream both unchanged)
// skips the rewrite so the file's bytes stay byte-identical (sc3 no-op).
//
// 0o600 — these files embed the plaintext x-ach-key bearer (CR-01).
func (d *adapterDispatcherImpl) publishRuntimeFile(fw adapter.FileWrite, s *state.File, toolRoot string) (FileWrite, error) {
	return d.publishFile(fw, findAdapterEntry(s, fw.Path), toolRoot)
}

// publishFile is the bucket-agnostic core of publishRuntimeFile: the
// caller supplies the prior state.FileEntry it looked up from the correct
// bucket (Adapter.Files for the runtime loop, Plugins[] for the projection
// leg) so the §8.4 per-key drift truth table + no-op skip operate against
// the right prior record. publishRuntimeFile is the Adapter.Files-bucket
// convenience wrapper; the projection leg calls publishFile directly with
// a Plugins-bucket prior so re-hydration of an unchanged projected file
// hits the no-op skip path (FMT-05 byte no-op).
func (d *adapterDispatcherImpl) publishFile(fw adapter.FileWrite, prior *state.FileEntry, toolRoot string) (FileWrite, error) {
	finalAbs := filepath.Join(toolRoot, fw.Path)
	isTOML := strings.ToLower(filepath.Ext(finalAbs)) == extTOML

	// Composite pre-staging (D-06): build the per-plugin marked block ONCE so
	// the drift hash (marked-region bytes) and the forward write (insert or
	// replace) operate on identical bytes.
	compositeID, compositeBlock := buildCompositeBlock(fw)

	var freshHash, onDiskHash string
	switch {
	case fw.Merge == adapter.MergeComposite:
		var herr error
		if freshHash, onDiskHash, herr = compositeHashes(finalAbs, compositeID, compositeBlock); herr != nil {
			return FileWrite{}, fmt.Errorf("adapter %s read existing %s: %w", d.platformID, finalAbs, herr)
		}
	case fw.Merge == adapter.MergeDeep:
		// Co-owned deep-merge file: hash ONLY our contributed subtree
		// (parsed → map → deterministic re-encode) so it is directly
		// comparable to the on-disk subtree regardless of struct-vs-map
		// field ordering, and the user's other keys are invisible to the
		// drift check.
		oursMap, err := parseDoc(fw.Content, isTOML)
		if err != nil {
			return FileWrite{}, fmt.Errorf("adapter %s parse rendered %s: %w", d.platformID, finalAbs, err)
		}
		if freshHash, err = subtreeHash(oursMap); err != nil {
			return FileWrite{}, fmt.Errorf("adapter %s hash rendered %s: %w", d.platformID, finalAbs, err)
		}
		diskMap, ok, derr := readParseDoc(finalAbs, isTOML)
		if derr != nil {
			return FileWrite{}, fmt.Errorf("adapter %s read existing %s: %w", d.platformID, finalAbs, derr)
		}
		if ok {
			sub, found := extractByKeys(diskMap, fw.Keys)
			if found {
				if onDiskHash, err = subtreeHash(sub); err != nil {
					return FileWrite{}, fmt.Errorf("adapter %s hash on-disk %s: %w", d.platformID, finalAbs, err)
				}
			}
		}
	default:
		// File-owned replace (incl. opaque passthrough projection —
		// markdown/skill files that are NOT structured JSON/TOML): we own
		// the WHOLE file, so the hash is the raw content hash and on-disk
		// drift is the raw file hash. Parsing as JSON/TOML would wrongly
		// reject a markdown body (the WIRE-01 projection regression).
		freshHash = hash.HashBytes(fw.Content)
		if body, rerr := os.ReadFile(finalAbs); rerr == nil {
			onDiskHash = hash.HashBytes(body)
		} else if !os.IsNotExist(rerr) {
			return FileWrite{}, fmt.Errorf("adapter %s read existing %s: %w", d.platformID, finalAbs, rerr)
		}
	}

	// LIFE-02 / D-30: Compare's third argument is the FRESH SOURCE hash (the
	// upstream-change axis), NOT the emitted-output freshHash. For a CONVERTED
	// projected file (D-23: Hash == output xxh3, SourceHash == pre-conversion
	// source xxh3) the two diverge, so passing freshHash would spuriously trip
	// the source-change axis on every converted Plugins[] entry. Derive the
	// fresh source hash with the SAME rule the state-recording block below uses
	// (fw.SourceHash, falling back to freshHash when empty — passthrough
	// invariant: Hash == SourceHash). Both publishFile callers (findAdapterEntry
	// runtime + findPluginEntry projection) thus evaluate the truth table
	// correctly. onDiskHash (the user-drift axis) and the per-MergeKind freshHash
	// (recorded as Hash, used for the no-op content identity) are unchanged.
	freshSourceHash := freshHash
	if fw.SourceHash != "" {
		freshSourceHash = fw.SourceHash
	}
	outcome := NewDiffer().Compare(prior, onDiskHash, freshSourceHash)

	// A user edit to OUR key (drift) is preserved with exit 2 unless --force.
	// prior == nil (fresh hydrate) never refuses — there is nothing of ours
	// to preserve yet.
	if prior != nil && ShouldExit2(outcome) && !d.force {
		return FileWrite{}, WrapDriftError(outcome, finalAbs)
	}

	// Skip the rewrite only on a true no-op (prior exists; disk + upstream
	// unchanged) so byte-for-byte stability holds. Otherwise publish.
	skip := prior != nil && outcome == NoOp
	if !skip {
		if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
			return FileWrite{}, fmt.Errorf("adapter %s mkdir parent %s: %w", d.platformID, finalAbs, err)
		}
		switch fw.Merge {
		case adapter.MergeDeep:
			if _, err := mergeForward(finalAbs, fw.Content, 0o600); err != nil {
				return FileWrite{}, fmt.Errorf("adapter %s merge %s: %w", d.platformID, finalAbs, err)
			}
		case adapter.MergeComposite:
			if err := writeComposite(finalAbs, compositeID, compositeBlock); err != nil {
				return FileWrite{}, fmt.Errorf("adapter %s write composite %s: %w", d.platformID, finalAbs, err)
			}
		default:
			// MergeReplace (file-owned) covers ONLY the Phase-3 projected
			// plugin resource files — .codex/agents/*.toml, .codex/prompts/*.md,
			// .agents/skills/*, .opencode/agents/*.md, .opencode/commands/*,
			// .opencode/skills/* — none of which carry credentials (WR-04).
			// 0o600 (owner-only) is reserved for the credential-bearing
			// MergeDeep runtime configs above; non-secret projected resources
			// use 0o644 so cross-account use (service user, mounted docker
			// volume) works.
			if err := state.WriteAtomic(finalAbs, fw.Content, 0o644); err != nil {
				return FileWrite{}, fmt.Errorf("adapter %s write %s: %w", d.platformID, finalAbs, err)
			}
		}
	} else {
		// CR-01 / F-10 — on the no-op skip path the rewrite is bypassed, so
		// the chmod side-effect of WriteAtomic / mergeForward is lost. If the
		// on-disk file was chmod'd to a more-permissive mode between hydrates
		// (user error, attacker, prior bug), the content stays at the leaked
		// mode. Re-assert the per-MergeKind mode unconditionally — cheap, and
		// closes the no-op regression net the CR-01 audit flagged. The mode is
		// MergeKind-dependent: only MergeDeep runtime configs carry a bearer
		// credential, so ONLY they get owner-only 0o600. MergeComposite host
		// memory files (CLAUDE.md/GEMINI.md) and MergeReplace projected plugin
		// resources (WR-04 — .codex/agents/*.toml, .opencode/commands/*, …)
		// hold no secret and use 0o644; a fixed 0o600 here would silently
		// DOWNGRADE them on every idempotent re-hydrate.
		mode := os.FileMode(0o644)
		if fw.Merge == adapter.MergeDeep {
			mode = 0o600
		}
		if err := os.Chmod(finalAbs, mode); err != nil && !os.IsNotExist(err) {
			return FileWrite{}, fmt.Errorf("adapter %s chmod %s: %w", d.platformID, finalAbs, err)
		}
	}

	// State records the canonical hash of OUR contribution (not the merged
	// file) so --sync inverse-merge (via Keys[]) and the next drift check
	// operate on our keys only, never the user's other entries.
	//
	// SourceHash threading (D-23): a CONVERTED projected file carries the
	// pre-transform source hash in fw.SourceHash (set by route.Project),
	// which diverges from the emitted-content freshHash. A passthrough /
	// runtime-rendered file leaves fw.SourceHash empty, so SourceHash falls
	// back to freshHash (== Hash) — preserving the Phase-1/2 invariant.
	sourceHash := freshHash
	if fw.SourceHash != "" {
		sourceHash = fw.SourceHash
	}
	return FileWrite{
		Target:     fw.Path,
		Hash:       freshHash,
		SourceHash: sourceHash,
		Merge:      mergeKindToString(fw.Merge),
		Keys:       append([]string(nil), fw.Keys...),
	}, nil
}

// buildCompositeBlock builds the per-plugin marked block for a MergeComposite
// FileWrite (D-06/D-07). compositeID is fw.Keys[0] (the dispatcher-threaded
// plugin name; empty for a degenerate write). fw.Content is inserted VERBATIM
// (D-05: no canonical markdown re-encode); a trailing newline on the content
// is trimmed so the block ends with exactly one newline — deterministic for
// FMT-05 re-hydrate idempotence. Returns ("", nil) for non-composite writes.
func buildCompositeBlock(fw adapter.FileWrite) (id string, block []byte) {
	if fw.Merge != adapter.MergeComposite {
		return "", nil
	}
	if len(fw.Keys) > 0 {
		id = fw.Keys[0]
	}
	body := bytes.TrimRight(fw.Content, "\n")
	block = []byte("<!-- ach:begin:" + id + " -->\n" +
		string(body) + "\n<!-- ach:end:" + id + " -->\n")
	return id, block
}

// compositeHashes computes the composite drift hashes (D-06): freshHash over
// the block we WOULD write, onDiskHash over the on-disk per-plugin marked
// region only (extracted via pluginMarkerRE; "" when absent). Hashing only the
// marked region means a user editing prose OUTSIDE this plugin's block is NOT
// flagged as drift.
func compositeHashes(finalAbs, id string, block []byte) (freshHash, onDiskHash string, err error) {
	freshHash = hash.HashBytes(block)
	body, rerr := os.ReadFile(finalAbs)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return freshHash, "", nil
		}
		return "", "", rerr
	}
	if region := pluginMarkerRE(id).Find(body); region != nil {
		onDiskHash = hash.HashBytes(region)
	}
	return freshHash, onDiskHash, nil
}

// writeComposite performs the forward composite merge (D-06): a marker-bounded
// insert (no prior block) or replace (existing per-plugin block) of block into
// the host memory file (CLAUDE.md / GEMINI.md). EXACT inverse of syncComposite.
// block is already wrapped in this plugin's outer markers; any forged inner
// markers in untrusted plugin prose are inert text inside our boundary
// (T-02-03 marker-injection mitigation — pluginMarkerRE matches only the OUTER
// real markers for `id`).
//
// Mode 0o644 (NOT 0o600): host memory files carry NO credential (the mcpServers
// bearer lives in the MergeDeep settings.json arm, which stays 0o600).
// World-readable is correct for non-secret markdown prose. See 02-PATTERNS.md
// file-mode policy table (D-06).
func writeComposite(finalAbs, id string, block []byte) error {
	body, rerr := os.ReadFile(finalAbs)
	if rerr != nil && !os.IsNotExist(rerr) {
		return rerr
	}
	var merged []byte
	re := pluginMarkerRE(id)
	if re.Match(body) {
		merged = re.ReplaceAll(body, block)
	} else {
		merged = append(append([]byte(nil), body...), block...)
	}
	return state.WriteAtomic(finalAbs, merged, 0o644)
}

// mergeForward reads the existing file at abs (JSON or TOML by extension),
// deep-merges the keys from `ours` (an adapter-rendered document carrying
// ONLY ACH's contributed entries) into it, and atomic-writes the result at
// the given mode. When abs does not exist, the result is just `ours`. The
// user's pre-existing keys are preserved; ACH's keys upsert same-named
// entries. Returns the merged bytes (for hashing/state if the caller wants
// them). This is the forward counterpart to syncDeep{JSON,TOML}'s removal.
//
// Concurrency note (security 2.4 — accept-disposition): the read-merge-write
// sequence is NOT atomic against external writers. The <achDir>/lock flock
// excludes other ach-cli processes, but a concurrent editor save on the
// runtime-config file (e.g. claude-code's auto-format on .claude/settings.json)
// between our read and our atomic-rename will be silently clobbered. Pragmatic
// trade-off for v1: users should avoid editing the target while hydrate is
// running. A defense-in-depth mtime-recheck would catch the race but is
// deferred until a real-world report. See CLAUDE.md "Common failure modes" for
// the symptom.
func mergeForward(abs string, ours []byte, mode os.FileMode) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(abs)) {
	case extJSON:
		return mergeForwardDoc(abs, ours, mode, false)
	case extTOML:
		return mergeForwardDoc(abs, ours, mode, true)
	default:
		// No structured merge for an unknown extension — write verbatim.
		if err := state.WriteAtomic(abs, ours, mode); err != nil {
			return nil, fmt.Errorf("mergeForward write %s: %w", abs, err)
		}
		return ours, nil
	}
}

// mergeForwardDoc deep-merges `ours` into the existing JSON or TOML
// document at abs (isTOML selects the codec). A missing file is treated
// as an empty object. A pre-existing file MUST be valid in the selected
// format (we never silently discard a user's config). The format label
// keeps the error messages identical to the prior per-format functions.
func mergeForwardDoc(abs string, ours []byte, mode os.FileMode, isTOML bool) ([]byte, error) {
	format := "JSON"
	if isTOML {
		format = "TOML"
	}
	oursMap, err := parseRendered(ours, isTOML)
	if err != nil {
		return nil, fmt.Errorf("mergeForward decode rendered %s: %w", format, err)
	}
	existing := map[string]any{}
	if body, err := os.ReadFile(abs); err == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			if derr := unmarshalDoc(body, &existing, isTOML); derr != nil {
				return nil, fmt.Errorf("mergeForward decode existing %s %s: %w", format, abs, derr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mergeForward read %s: %w", abs, err)
	}
	deepMergeInto(existing, oursMap)
	out, err := encodeDoc(existing, isTOML)
	if err != nil {
		return nil, fmt.Errorf("mergeForward encode %s %s: %w", format, abs, err)
	}
	if err := state.WriteAtomic(abs, out, mode); err != nil {
		return nil, fmt.Errorf("mergeForward write %s %s: %w", format, abs, err)
	}
	return out, nil
}

// parseRendered unmarshals the freshly-rendered `ours` bytes into a
// generic map via the selected codec. Unlike parseDoc it does NOT treat
// an empty body as an empty map — the rendered content always parses as
// the adapter emitted it; preserving the prior strict-decode semantics.
func parseRendered(ours []byte, isTOML bool) (map[string]any, error) {
	var m map[string]any
	if err := unmarshalDoc(ours, &m, isTOML); err != nil {
		return nil, err
	}
	return m, nil
}

// unmarshalDoc decodes b into v via the JSON or TOML codec.
func unmarshalDoc(b []byte, v any, isTOML bool) error {
	if isTOML {
		return toml.Unmarshal(b, v)
	}
	return json.Unmarshal(b, v)
}

// deepMergeInto recursively merges src into dst: when both sides hold a
// nested object at the same key, recurse; otherwise src's value overwrites
// dst's. This preserves the user's sibling keys (e.g. their other MCP
// servers and unrelated settings) while upserting ACH's entries.
func deepMergeInto(dst, src map[string]any) {
	for k, sv := range src {
		if svMap, ok := sv.(map[string]any); ok {
			if dvMap, ok := dst[k].(map[string]any); ok {
				deepMergeInto(dvMap, svMap)
				continue
			}
		}
		dst[k] = sv
	}
}

// parseDoc unmarshals a JSON or TOML document into a generic map. An
// empty/whitespace body yields an empty map (not an error).
func parseDoc(content []byte, isTOML bool) (map[string]any, error) {
	out := map[string]any{}
	if len(bytes.TrimSpace(content)) == 0 {
		return out, nil
	}
	if isTOML {
		if err := toml.Unmarshal(content, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	if err := json.Unmarshal(content, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// encodeDoc renders a generic map back to JSON or TOML bytes. It is the
// inverse of parseDoc and the single encoder both the forward-merge and
// the --sync inverse-merge paths share. The settings are pinned to match
// the prior per-format encoders byte-for-byte (JSON: 2-space indent,
// HTML-escaping disabled; TOML: BurntSushi default encoder) so drift
// hashing and idempotence stay stable across the consolidation.
func encodeDoc(m map[string]any, isTOML bool) ([]byte, error) {
	var buf bytes.Buffer
	if isTOML {
		enc := toml.NewEncoder(&buf)
		if err := enc.Encode(m); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readParseDoc reads + parses abs. Returns (nil, false, nil) when the file
// is absent or empty (no prior on-disk document).
func readParseDoc(abs string, isTOML bool) (map[string]any, bool, error) {
	body, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false, nil
	}
	m, perr := parseDoc(body, isTOML)
	if perr != nil {
		return nil, false, perr
	}
	return m, true, nil
}

// subtreeHash returns the xxh3 of a deterministic (sorted-key) JSON
// encoding of m. Encoding via json.Marshal regardless of the source
// format makes the hash independent of struct-vs-map field ordering and
// of JSON-vs-TOML provenance, so the freshly-rendered and on-disk subtrees
// are directly comparable.
func subtreeHash(m map[string]any) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return hash.Hash(bytes.NewReader(b))
}

// extractByKeys builds a document containing ONLY the dotted `keys` lifted
// from src (preserving nesting). found reports whether at least one key was
// present — used to distinguish "our keys absent on disk" from "present".
func extractByKeys(src map[string]any, keys []string) (map[string]any, bool) {
	out := map[string]any{}
	found := false
	for _, k := range keys {
		if v, ok := getDottedKey(src, k); ok {
			setDottedKey(out, k, v)
			found = true
		}
	}
	return out, found
}

// getDottedKey reads the value at a dotted path from a nested map. Returns
// (nil, false) when any segment is missing or a non-map intermediate is hit.
func getDottedKey(root map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	cur := root
	for i, p := range parts {
		v, ok := cur[p]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return v, true
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return nil, false
}

// setDottedKey sets val at a dotted path, creating intermediate maps.
func setDottedKey(root map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = val
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// findAdapterEntry locates the prior state.FileEntry for target under
// s.Adapter.Files, or nil when absent (fresh hydrate).
func findAdapterEntry(s *state.File, target string) *state.FileEntry {
	if s == nil {
		return nil
	}
	for i := range s.Adapter.Files {
		if s.Adapter.Files[i].Target == target {
			return &s.Adapter.Files[i]
		}
	}
	return nil
}

// findPluginEntry locates the prior state.FileEntry for target under
// s.Plugins (the projection bucket, D-07), or nil when absent. The
// projection leg uses this so the §8.4 drift truth table + no-op skip in
// publishFile compare a projected file against its prior Plugins[] record
// — not the Adapter.Files bucket — making an unchanged re-hydrate a byte
// no-op (FMT-05).
func findPluginEntry(s *state.File, target string) *state.FileEntry {
	if s == nil {
		return nil
	}
	for i := range s.Plugins {
		if s.Plugins[i].Target == target {
			return &s.Plugins[i]
		}
	}
	return nil
}

// mergeKindToString translates adapter.MergeKind into the state.json
// canonical string (state.FileEntry.Merge is a string per the §8.2
// schema). Unknown values fall through as empty string so the field
// is omitted from JSON.
func mergeKindToString(k adapter.MergeKind) string {
	switch k {
	case adapter.MergeDeep:
		return mergeStrDeep
	case adapter.MergeComposite:
		return mergeStrComposite
	case adapter.MergeReplace:
		return mergeStrReplace
	}
	return ""
}

// pluginMarkerRE builds the per-plugin composite marker regex (D-07):
// "<!-- ach:begin:<plugin> -->...<!-- ach:end:<plugin> -->" with an
// optional trailing newline. The plugin id is regexp-escaped via
// QuoteMeta so a forged inner marker carried in untrusted plugin prose
// (T-02-03) cannot widen or hijack another plugin's region — the per-id
// boundary is the OUTER real markers only. (?s) lets . span newlines so
// a multi-line block is captured.
//
// Both the forward composite arm in publishFile (insert/replace) and the
// inverse path in syncComposite (deletion) build the regex from the same
// builder, keeping the forward and inverse merges symmetric on the exact
// same marked region.
func pluginMarkerRE(pluginID string) *regexp.Regexp {
	return regexp.MustCompile("(?s)<!-- ach:begin:" + regexp.QuoteMeta(pluginID) +
		" -->.*?<!-- ach:end:" + regexp.QuoteMeta(pluginID) + " -->\\n?")
}

// genericMarkerRE matches the OLD single-marker composite form
// "<!-- ach:begin -->...<!-- ach:end -->" (no per-plugin id). Retained
// SOLELY for the syncComposite backward-compat fallback: a pre-Phase-2
// state row carries no plugin id in Keys, so its inverse-merge must
// target the generic region. All Phase-2 composite rows carry exactly
// one Keys entry (the plugin name) and use pluginMarkerRE instead.
var genericMarkerRE = regexp.MustCompile(`(?s)<!-- ach:begin -->.*?<!-- ach:end -->\n?`)

// SyncOptions packages the Sync handler's behavior toggles so the
// signature stays narrow. Force overrides the drift-wins arm
// (mismatch → delete anyway). Stderr is the channel for the
// "preserved due to drift" warnings; nil disables emission.
type SyncOptions struct {
	Force  bool
	Stderr io.Writer
	// DryRun classifies every entry (drift-wins, marker/extension
	// support, doc-empty detection) so SyncStats reflects what a real
	// run WOULD do, but performs NO disk mutation — no os.Remove, no
	// WriteAtomic, no empty-dir pruning. Defaults false; existing
	// forward-teardown callers are unaffected.
	DryRun bool
}

// SyncStats reports the per-call Sync outcome. Pruned counts entries
// the engine removed (file or inverse-merge); Preserved counts
// entries the engine refused to touch (drift-wins).
type SyncStats struct {
	Pruned    int
	Preserved int
}

// Sync implements STATE-05 / D-16. Walks prev's file entries; for
// any entry whose Target is NOT in `new`, classify and act:
//
//   - On-disk hash differs from prev.Hash → preserve with stderr
//     warning (drift wins) UNLESS opts.Force.
//   - Merge=="" / MergeReplace → os.Remove the file.
//   - Merge=="deep" + Keys[] → JSON/TOML inverse-merge: read the
//     file, delete the listed top-level keys (or dotted paths),
//     re-write atomically.
//   - Merge=="composite" → regex-replace the
//     "<!-- ach:begin -->...<!-- ach:end -->" block with empty.
//     No marker found → preserve with warning.
//
// Sort the to-delete set deepest-first by path-depth (more slashes
// first) so child files are removed before parent dirs.
//
// After per-file deletion/inverse-merge, walk every parent dir
// deepest-first and call os.Remove — kernels return ENOTEMPTY for
// non-empty dirs which we silently swallow. CLI NEVER recursively
// deletes.
//
// Adapter.Files resolve against toolRoot (the tool's native config root —
// workspace root in project scope, $HOME under --global, $XDG_CONFIG_HOME/...
// for opencode global). The four content buckets resolve against achDir. The
// caller MUST pass toolRoot even if it equals achDir; an empty toolRoot is
// rejected by an assertion in walkEntriesTagged → falls back to achDir for
// safety (defensive, since the orchestrator at commit.go:334 always supplies
// a non-empty value).
func Sync(prev, newFile *state.File, achDir, toolRoot string, opts SyncOptions) (SyncStats, error) {
	var stats SyncStats
	if prev == nil {
		return stats, nil
	}
	if toolRoot == "" {
		toolRoot = achDir
	}

	// Build the set of Targets present in newFile so we can compute
	// the to-delete set as set-difference.
	keep := map[string]struct{}{}
	if newFile != nil {
		for _, e := range state.WalkEntries(newFile) {
			keep[e.Target] = struct{}{}
		}
	}

	// Collect deletable entries — those in prev but not in newFile.
	type del struct {
		entry state.FileEntry
		abs   string
	}
	prevEntries := walkEntriesTagged(prev)
	dels := make([]del, 0, len(prevEntries))
	parentDirs := map[string]struct{}{}
	for _, te := range prevEntries {
		e := te.Entry
		if _, ok := keep[e.Target]; ok {
			continue
		}
		abs := e.Target
		if !filepath.IsAbs(abs) {
			base := achDir
			if te.ResolveAgainstToolRoot {
				base = toolRoot
			}
			abs = filepath.Join(base, e.Target)
		}
		dels = append(dels, del{entry: e, abs: abs})
		parentDirs[filepath.Dir(abs)] = struct{}{}
	}

	// Sort deepest-first by path depth (more separators → first).
	sort.SliceStable(dels, func(i, j int) bool {
		return strings.Count(dels[i].abs, string(os.PathSeparator)) >
			strings.Count(dels[j].abs, string(os.PathSeparator))
	})

	for _, d := range dels {
		preserved, err := syncOne(d.entry, d.abs, opts)
		if err != nil {
			return stats, err
		}
		if preserved {
			stats.Preserved++
		} else {
			stats.Pruned++
		}
	}

	// Walk parent dirs deepest-first; os.Remove honors ENOTEMPTY by
	// returning an error which we silently swallow. Skipped under
	// DryRun — a preview must never remove directories.
	if !opts.DryRun {
		pruneEmptyDirs(parentDirs, achDir)
	}

	return stats, nil
}

// syncOne handles a single state entry's removal/inverse-merge.
// Returns (preserved, err) — preserved=true when drift-wins skipped
// the work.
func syncOne(e state.FileEntry, abs string, opts SyncOptions) (bool, error) {
	// Read the on-disk file; absence means already gone — count as
	// pruned (the engine's bookkeeping treats the entry as removed).
	info, statErr := os.Stat(abs)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("sync stat %s: %w", abs, statErr)
	}
	if info.IsDir() {
		// CLI never recursively deletes directories — skip.
		warnPreserved(opts.Stderr, abs, "directory entries are never recursively deleted")
		return true, nil
	}

	// Drift-wins gate: compare on-disk xxh3 to prev.Hash. Mismatch +
	// !Force → preserve.
	if !opts.Force && e.Hash != "" {
		current, err := hash.HashFile(abs)
		if err != nil {
			return false, fmt.Errorf("sync hash %s: %w", abs, err)
		}
		if current != e.Hash {
			warnPreserved(opts.Stderr, abs,
				"local edits detected; pass --force to remove")
			return true, nil
		}
	}

	// Classify by Merge kind.
	switch e.Merge {
	case mergeStrComposite:
		return syncComposite(e, abs, opts)
	case mergeStrDeep:
		return syncDeep(e, abs, opts)
	case "", mergeStrReplace:
		// Replace / unmerged → unlink (would-prune). Preview: classify
		// only, write nothing.
		if opts.DryRun {
			return false, nil
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("sync remove %s: %w", abs, err)
		}
		return false, nil
	default:
		// Unknown merge kind — preserve defensively.
		warnPreserved(opts.Stderr, abs,
			fmt.Sprintf("unknown merge kind %q; refusing to inverse-merge", e.Merge))
		return true, nil
	}
}

// syncComposite handles MergeComposite by regex-replacing the
// per-plugin "<!-- ach:begin:<plugin> -->...<!-- ach:end:<plugin> -->"
// block with empty (D-07: Phase-4 sync subtracts exactly ONE plugin's
// block — the one named in e.Keys[0]). If the marker is absent the file
// is preserved with a warning (the user must have authored the file
// outside the engine's contract).
//
// Empty-Keys backward-compat (D-07): a pre-Phase-2 state row carries no
// plugin id in Keys, so it targets the OLD generic single-marker region
// via genericMarkerRE. All Phase-2 composite rows carry Keys=[plugin-name]
// and use the per-id regex.
func syncComposite(e state.FileEntry, abs string, opts SyncOptions) (bool, error) {
	body, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("sync read composite %s: %w", abs, err)
	}
	var markerRE *regexp.Regexp
	if len(e.Keys) > 0 {
		markerRE = pluginMarkerRE(e.Keys[0])
	} else {
		markerRE = genericMarkerRE
	}
	if !markerRE.Match(body) {
		warnPreserved(opts.Stderr, abs,
			"composite marker not found; refusing to inverse-merge")
		return true, nil
	}
	// Marker confirmed present → this entry would-prune. Preview: stop
	// before the rewrite.
	if opts.DryRun {
		return false, nil
	}
	updated := markerRE.ReplaceAll(body, nil)
	// 0o644 — composite host memory files (CLAUDE.md/GEMINI.md) carry NO
	// credential; the forward writeComposite path writes 0o644, so the
	// inverse-merge MUST match or it silently downgrades the file mode on
	// every plugin-block removal.
	if err := state.WriteAtomic(abs, updated, 0o644); err != nil {
		return false, fmt.Errorf("sync write composite %s: %w", abs, err)
	}
	return false, nil
}

// syncDeep handles MergeDeep by removing the listed Keys[] paths
// from a JSON or TOML document. Dispatch on file extension:
//
//   - .json → encoding/json decode → walk → re-encode.
//   - .toml → BurntSushi/toml decode → walk → re-encode.
//   - other → preserve with warning (unknown shape — refuse to mutate).
//
// Each Key is a dotted-path expression — "mcpServers.foo" removes
// the "foo" subkey under "mcpServers". A single-segment key
// ("mcpServers") removes the whole top-level entry.
func syncDeep(e state.FileEntry, abs string, opts SyncOptions) (bool, error) {
	ext := strings.ToLower(filepath.Ext(abs))
	switch ext {
	case extJSON:
		return syncDeepDoc(e, abs, false, opts.DryRun)
	case extTOML:
		return syncDeepDoc(e, abs, true, opts.DryRun)
	}
	warnPreserved(opts.Stderr, abs,
		fmt.Sprintf("unsupported merge=deep file extension %q; refusing to inverse-merge", ext))
	return true, nil
}

// syncDeepDoc loads a JSON or TOML file as map[string]any (isTOML selects
// the codec), removes the dotted-path Keys, re-encodes, and atomically
// rewrites. An empty resulting map → the file is deleted entirely (the
// user's whole document was engine-contributed). The format label keeps
// the error messages identical to the prior per-format functions; the
// JSON/TOML encoder settings are unchanged (encodeDoc reproduces them
// byte-for-byte).
func syncDeepDoc(e state.FileEntry, abs string, isTOML, dryRun bool) (bool, error) {
	format := "JSON"
	if isTOML {
		format = "TOML"
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("sync read %s %s: %w", format, abs, err)
	}
	var root map[string]any
	if err := unmarshalDoc(body, &root, isTOML); err != nil {
		return false, fmt.Errorf("sync decode %s %s: %w", format, abs, err)
	}
	for _, k := range e.Keys {
		removeDottedKey(root, k)
	}
	// Preview: the document was decoded and the inverse-merge resolved
	// (both rewrite and whole-file delete count as pruned). Stop before
	// any disk write.
	if dryRun {
		return false, nil
	}
	if len(root) == 0 {
		if rerr := os.Remove(abs); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return false, fmt.Errorf("sync remove %s %s: %w", format, abs, rerr)
		}
		return false, nil
	}
	out, err := encodeDoc(root, isTOML)
	if err != nil {
		return false, fmt.Errorf("sync encode %s %s: %w", format, abs, err)
	}
	// 0o600 — deep-merge inverse rewrites the same credential-
	// bearing adapter runtime-config file (CR-01).
	if err := state.WriteAtomic(abs, out, 0o600); err != nil {
		return false, fmt.Errorf("sync write %s %s: %w", format, abs, err)
	}
	return false, nil
}

// removeDottedKey deletes the leaf at a dotted-path expression from
// a nested map[string]any. Missing intermediate keys are no-ops —
// removing a key that does not exist is idempotent. Non-map
// intermediates are also no-ops (cannot recurse into a scalar).
func removeDottedKey(root map[string]any, path string) {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return
	}
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			delete(cur, p)
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

// pruneEmptyDirs walks parent dirs deepest-first calling os.Remove.
// Honors ENOTEMPTY silently (non-empty dir → preserved). Stops at
// achDir's parent so the engine never escapes the workspace root.
func pruneEmptyDirs(parents map[string]struct{}, achDir string) {
	// Expand the set to include each parent's parents up to achDir's
	// parent so a deep-nested prune cascades cleanly.
	all := map[string]struct{}{}
	parentRoot := filepath.Dir(achDir)
	for p := range parents {
		for p != "" && p != "." && p != parentRoot && p != string(filepath.Separator) {
			if _, ok := all[p]; ok {
				break
			}
			all[p] = struct{}{}
			next := filepath.Dir(p)
			if next == p {
				break
			}
			p = next
		}
	}

	dirs := make([]string, 0, len(all))
	for p := range all {
		dirs = append(dirs, p)
	}
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(os.PathSeparator)) >
			strings.Count(dirs[j], string(os.PathSeparator))
	})
	for _, d := range dirs {
		// os.Remove returns *os.PathError wrapping ENOTEMPTY for
		// non-empty directories; we silently swallow per D-16.
		_ = os.Remove(d)
	}
}

// appendUniqueSorted inserts name into xs if absent, keeping xs sorted.
func appendUniqueSorted(xs []string, name string) []string {
	for _, x := range xs {
		if x == name {
			return xs
		}
	}
	xs = append(xs, name)
	sort.Strings(xs)
	return xs
}

// taggedEntry preserves bucket provenance for Sync — entries whose projected
// files live under toolRoot (the tool's native config root) carry
// ResolveAgainstToolRoot=true; entries whose bytes live under achDir (ACH's
// private .ach/ cache) carry false. Lost when state.WalkEntries flattens.
//
// Both Adapter.Files AND Plugins resolve against toolRoot: projectPlugins
// publishes each projected plugin FileWrite under toolRoot (the adapter-native
// resource dirs — .claude/agents/…, .pi/agent/skills/…, etc., see publishFile
// call in projectPlugins), recording a toolRoot-RELATIVE Target. Prompts and
// Artifacts are extracted into the achDir content cache (ExtractContent's base
// is achDir) and are NOT projected to toolRoot, so they resolve against achDir.
// Resolving Plugins against achDir (the pre-fix behavior) made uninstall/--sync
// compute <achDir>/<target> — a path that never exists — so os.Remove was a
// silent no-op and the projected plugin files survived uninstall (VER-02).
type taggedEntry struct {
	Entry state.FileEntry
	// ResolveAgainstToolRoot selects toolRoot (vs achDir) as the base when the
	// FileEntry.Target is workspace-relative. True for Adapter.Files and
	// Plugins (both published under toolRoot); false for the achDir-cached
	// content buckets (Prompts / Artifacts / RuntimeFiles).
	ResolveAgainstToolRoot bool
}

// walkEntriesTagged is state.WalkEntries with provenance retained so Sync can pick
// the correct base path per bucket (F-02 / CR-02 follow-up). Adapter.Files and
// Plugins flow through with ResolveAgainstToolRoot=true (both live under
// toolRoot); the remaining achDir-cached content buckets with false.
func walkEntriesTagged(f *state.File) []taggedEntry {
	if f == nil {
		return nil
	}
	total := len(f.Prompts) + len(f.Plugins) + len(f.Artifacts) +
		len(f.RuntimeFiles) + len(f.Adapter.Files)
	out := make([]taggedEntry, 0, total)
	for _, e := range f.Prompts {
		out = append(out, taggedEntry{Entry: e})
	}
	for _, e := range f.Plugins {
		// Projected plugin resources are published under toolRoot (native
		// resource dirs), so their deletion path must resolve there too.
		out = append(out, taggedEntry{Entry: e, ResolveAgainstToolRoot: true})
	}
	for _, e := range f.Artifacts {
		out = append(out, taggedEntry{Entry: e})
	}
	for _, e := range f.RuntimeFiles {
		out = append(out, taggedEntry{Entry: e})
	}
	for _, e := range f.Adapter.Files {
		out = append(out, taggedEntry{Entry: e, ResolveAgainstToolRoot: true})
	}
	return out
}

// warnPreserved emits a single-line stderr message naming the
// preserved file and the reason. stderr=nil disables emission
// (useful for unit tests that don't care about the warning text).
func warnPreserved(stderr io.Writer, abs, reason string) {
	if stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(stderr, "warning: preserving %s — %s\n", abs, reason)
}

// NewWiring constructs the default Extractor + AdapterDispatcher
// pair the cobra layer (07-W3-05 cmd/ach-cli/cmd/hydrate.go) injects
// into commit.go via fields on *commit. force is the --force flag;
// allowSymlinks is --allow-symlinks; limits flow through from
// extract.LoadLimits.
//
// The constructor returns the impls as the public interfaces so the
// cobra layer can wire commit.extractor = ext / commit.adapter = ad
// without exposing the unexported impl types.
func NewWiring(
	client *httpclient.Client,
	platformID string,
	limits extract.Limits,
	allowSymlinks bool,
	force bool,
	global bool,
) (Extractor, AdapterDispatcher) {
	ext := &extractorImpl{
		client:        client,
		limits:        limits,
		allowSymlinks: allowSymlinks,
	}
	disp := &adapterDispatcherImpl{
		platformID: platformID,
		force:      force,
		global:     global,
	}
	return ext, disp
}
