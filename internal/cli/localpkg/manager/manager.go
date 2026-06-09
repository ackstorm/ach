// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/localpkg/discover"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
	"github.com/ackstorm/ach/internal/cli/skillstage"
	"github.com/ackstorm/ach/internal/contentkit"
	"github.com/ackstorm/ach/internal/gitfetch"
)

// ResolveResult holds the outcome of a Resolve call.
type ResolveResult struct {
	// Name is the plugin/skill name as requested.
	Name string
	// Kind is "plugin" or "skill".
	Kind string
	// ResolvedSHA is the 40-hex commit SHA the resource was resolved from.
	ResolvedSHA string
	// StageDir is the temp directory holding the extracted plugin/skill tree.
	// The CALLER is responsible for removing it (os.RemoveAll) when done.
	StageDir string
}

// schemeFor maps the store's AuthScheme string to a gitfetch.AuthScheme.
// "basic-oauth2" → AuthBasicOAuth2; anything else → AuthBearer.
func schemeFor(s string) gitfetch.AuthScheme {
	if s == "basic-oauth2" {
		return gitfetch.AuthBasicOAuth2
	}
	return gitfetch.AuthBearer
}

// pluginEntryMaxFileBytes caps each regular file when slicing a marketplace
// plugin subtree out of the cached repo tar (mirrors the skill slicer's 64 MiB
// per-file guard).
const pluginEntryMaxFileBytes = 64 << 20

// FetchCache memoizes git fetches within a SINGLE install/update invocation,
// keyed by (url, ref, subtree, scheme). A plugin-marketplace repo cloned once to
// read its marketplace.json is then reused for every plugin installed from it
// (and a skill-marketplace repo likewise across its skills), eliminating the
// per-plugin re-clone — and the per-plugin ls-remote — of the same repo. A nil
// *FetchCache is a valid "no caching" value, so callers that do not batch
// (the plain Resolve entry point, tests) need no special-casing.
type FetchCache struct {
	m map[string]cachedFetchResult
}

type cachedFetchResult struct {
	tar []byte
	sha string
}

// NewFetchCache returns an empty cache scoped to one install/update invocation.
func NewFetchCache() *FetchCache { return &FetchCache{m: map[string]cachedFetchResult{}} }

func fetchCacheKey(spec gitfetch.Spec) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%v", spec.URL, spec.Ref, spec.Subtree, spec.AuthScheme)
}

// cachedFetch resolves spec to (tar, sha), serving from cache on a (url, ref,
// subtree, scheme) hit. On a miss it resolves the SHA via LsRemote (when not
// pinned) and fetches, then records the result. cache may be nil (no caching).
func cachedFetch(ctx context.Context, cache *FetchCache, spec gitfetch.Spec) (tarBytes []byte, resolvedSHA string, err error) {
	key := fetchCacheKey(spec)
	if cache != nil {
		if r, ok := cache.m[key]; ok {
			return r.tar, r.sha, nil
		}
	}

	if spec.SHA == "" {
		sha, lerr := gitfetch.LsRemote(ctx, spec.URL, spec.Ref, spec.Token, spec.AuthScheme)
		if lerr != nil {
			return nil, "", fmt.Errorf("manager: ls-remote %s %s: %w", spec.URL, spec.Ref, lerr)
		}
		spec.SHA = sha
	}

	f := gitfetch.New(spec)
	res, err := f.Fetch(ctx, gitfetch.Request{})
	if err != nil {
		return nil, "", fmt.Errorf("manager: fetch %s: %w", spec.URL, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("manager: read body %s: %w", spec.URL, err)
	}

	if cache != nil {
		cache.m[key] = cachedFetchResult{tar: body, sha: spec.SHA}
	}
	return body, spec.SHA, nil
}

// cloneRepo fetches the whole repo (no subtree) as a gzipped tar, memoized via
// cache (may be nil). SHA is resolved via LsRemote on a cache miss.
func cloneRepo(ctx context.Context, cloneURL, ref, token string, scheme gitfetch.AuthScheme, cache *FetchCache) (tarBytes []byte, resolvedSHA string, err error) {
	return cachedFetch(ctx, cache, gitfetch.Spec{URL: cloneURL, Ref: ref, Token: token, AuthScheme: scheme})
}

// fetchEntry fetches a specific marketplace entry via its gitfetch.Spec,
// memoized via cache (may be nil).
func fetchEntry(ctx context.Context, spec gitfetch.Spec, cache *FetchCache) (tarBytes []byte, resolvedSHA string, err error) {
	return cachedFetch(ctx, cache, spec)
}

// stageTar extracts tarBytes into a fresh temp directory and returns the
// directory path. The caller owns cleanup via os.RemoveAll.
func stageTar(ctx context.Context, tarBytes []byte, kind extract.ResourceKind) (string, error) {
	dst, err := os.MkdirTemp("", "ach-stage-*")
	if err != nil {
		return "", fmt.Errorf("manager: mkdirtemp stage: %w", err)
	}
	_, err = extract.Extract(ctx, bytes.NewReader(tarBytes), dst, kind, extract.DefaultLimits(), false)
	if err != nil {
		_ = os.RemoveAll(dst)
		return "", fmt.Errorf("manager: extract: %w", err)
	}
	return dst, nil
}

// stageSkillNested extracts a verified skill tar into a RAW temp dir, then
// nests its SKILL.md-bearing content under <stageDir>/skills/<name>/ so the
// adapter's `skills/**/* → .claude/skills/**/*` projection rule (which
// classifies on the FIRST path element) fires at install time. Without this
// nesting a skill staged with SKILL.md at the stage root projects 0 files.
//
// The shared skillstage.Nest performs the same rebase the hydrate engine uses
// (handling the at-most-one REST-archive wrapper dir). The raw extraction dir
// is reclaimed before return (Nest renames the content subtree out of it, so
// ErrNotExist on cleanup is benign). name MUST already be validated as a safe
// path segment by the caller. The returned stageDir is owned by the caller.
func stageSkillNested(ctx context.Context, tarBytes []byte, name string) (string, error) {
	rawDir, err := stageTar(ctx, tarBytes, extract.KindSkill)
	if err != nil {
		return "", err
	}
	defer func() {
		if rmErr := os.RemoveAll(rawDir); rmErr != nil && !os.IsNotExist(rmErr) {
			// Best-effort cleanup of the raw extraction dir; a residual temp dir
			// is harmless and the staged tree is already correct.
			_ = rmErr
		}
	}()

	stageDir, err := os.MkdirTemp("", "ach-skill-*")
	if err != nil {
		return "", fmt.Errorf("manager: mkdirtemp skill stage: %w", err)
	}
	nested, err := skillstage.Nest(stageDir, name, rawDir)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return "", fmt.Errorf("manager: nest skill %q: %w", name, err)
	}
	if !nested {
		_ = os.RemoveAll(stageDir)
		return "", fmt.Errorf("manager: no SKILL.md found in skill %q", name)
	}
	return stageDir, nil
}

// extractFileFromTar returns the bytes of the first entry in the gzipped tar
// whose path matches targetPath OR <archiveRoot>/targetPath (to handle the
// REST-archive wrapper that some git hosts add). Returns an error when not
// found.
func extractFileFromTar(tarBytes []byte, targetPath string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarBytes))
	if err != nil {
		return nil, fmt.Errorf("manager: gzip open for extract: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("manager: tar read for extract: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Normalize the entry name: strip leading "./" and clean.
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		// Match directly or strip a single archive-root wrapper segment.
		if name == targetPath {
			return io.ReadAll(tr)
		}
		// Strip one leading path segment (the REST archive wrapper) and check again.
		if idx := strings.IndexByte(name, '/'); idx >= 0 {
			if name[idx+1:] == targetPath {
				return io.ReadAll(tr)
			}
		}
	}
	return nil, fmt.Errorf("manager: %q not found in tar archive", targetPath)
}

// normSubPath cleans a spec.<git>.path hint to a slash-trimmed relative
// path ("" when empty/root). Mirrors contentkit's internal normSubPath.
func normSubPath(p string) string {
	if p == "" || p == "." || p == "/" {
		return ""
	}
	p = strings.Trim(p, "/")
	if p == "." || p == "" {
		return ""
	}
	return p
}

// Resolve clones the repo, locates the named resource via the lens,
// verifies its contents, and extracts it to a fresh temp stage dir.
//
// lens ∈ "plugin-marketplace" | "plugin" | "skill-marketplace" | "skill".
// token may be "" for public repos.
//
// The returned ResolveResult.StageDir is owned by the CALLER; call
// os.RemoveAll(result.StageDir) when done.
func Resolve(ctx context.Context, repo store.RepoEntry, token, name, lens string) (ResolveResult, error) {
	return ResolveWithCache(ctx, repo, token, name, lens, nil)
}

// ResolveWithCache is Resolve with a per-invocation FetchCache so a batch
// install/update fetches each repo at most once (shared across all plugins or
// skills sourced from it). A nil cache disables caching (identical to Resolve).
func ResolveWithCache(ctx context.Context, repo store.RepoEntry, token, name, lens string, cache *FetchCache) (ResolveResult, error) {
	scheme := schemeFor(repo.AuthScheme)
	ref := defaultRef(repo.GitRef)

	switch lens {
	case discover.LensPlugin:
		return resolveDirectPlugin(ctx, repo, token, name, ref, scheme, cache)
	case discover.LensSkill:
		return resolveDirectSkill(ctx, repo, token, name, ref, scheme, cache)
	case discover.LensPluginMarketplace:
		return resolvePluginMarketplace(ctx, repo, token, name, ref, scheme, cache)
	case discover.LensSkillMarketplace:
		return resolveSkillMarketplace(ctx, repo, token, name, ref, scheme, cache)
	default:
		return ResolveResult{}, fmt.Errorf("manager: unknown lens %q (want plugin|skill|plugin-marketplace|skill-marketplace)", lens)
	}
}

// resolveDirectPlugin resolves the "plugin" lens: the whole repo IS the plugin
// and name must equal repo.Name.
func resolveDirectPlugin(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme, cache *FetchCache) (ResolveResult, error) {
	if name != repo.Name {
		return ResolveResult{}, fmt.Errorf("manager: direct-plugin lens requires name == repo.Name (got %q, repo %q)", name, repo.Name)
	}
	tarBytes, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme, cache)
	if err != nil {
		return ResolveResult{}, err
	}
	if err := contentkit.VerifyPluginContents(bytes.NewReader(tarBytes)); err != nil {
		return ResolveResult{}, fmt.Errorf("manager: verify plugin contents: %w", err)
	}
	stageDir, err := stageTar(ctx, tarBytes, extract.KindPlugin)
	if err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{Name: name, Kind: "plugin", ResolvedSHA: sha, StageDir: stageDir}, nil
}

// resolveDirectSkill resolves the "skill" lens: the whole repo IS the skill.
func resolveDirectSkill(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme, cache *FetchCache) (ResolveResult, error) {
	tarBytes, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme, cache)
	if err != nil {
		return ResolveResult{}, err
	}
	if err := contentkit.VerifySkillContents(bytes.NewReader(tarBytes)); err != nil {
		return ResolveResult{}, fmt.Errorf("manager: verify skill contents: %w", err)
	}
	// Guard the requested name as a safe path segment before it is joined into
	// the skills/<name>/ stage path.
	if err := contentkit.ValidateSkillName(name); err != nil {
		return ResolveResult{}, fmt.Errorf("manager: invalid skill name %q: %w", name, err)
	}
	stageDir, err := stageSkillNested(ctx, tarBytes, name)
	if err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{Name: name, Kind: "skill", ResolvedSHA: sha, StageDir: stageDir}, nil
}

// resolvePluginMarketplace resolves the "plugin-marketplace" lens: parse the
// repo's marketplace.json, locate the named plugin entry, fetch + verify it.
func resolvePluginMarketplace(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme, cache *FetchCache) (ResolveResult, error) {
	repoTar, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme, cache)
	if err != nil {
		return ResolveResult{}, err
	}

	// Extract marketplace.json from the cloned repo tar.
	mktJSON, err := extractFileFromTar(repoTar, ".claude-plugin/marketplace.json")
	if err != nil {
		return ResolveResult{}, fmt.Errorf("manager: extract marketplace.json from repo: %w", err)
	}

	mkt, err := contentkit.ParseClaudeCodeMarketplace(mktJSON)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("manager: parse marketplace.json: %w", err)
	}

	// Find the requested plugin entry.
	var found *contentkit.ClaudeCodeMarketplacePlugin
	for i := range mkt.Plugins {
		if mkt.Plugins[i].Name == name {
			found = &mkt.Plugins[i]
			break
		}
	}
	if found == nil {
		return ResolveResult{}, fmt.Errorf("manager: plugin %q not found in marketplace", name)
	}

	entrySpec, err := BuildEntrySpec(found.Source, repo.CloneURL, ref, token, scheme)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("manager: build entry spec for %q: %w", name, err)
	}

	// When the entry is a subdir of the marketplace's OWN repo (a local-path
	// entry, or a git-subdir/url whose URL is the marketplace repo), the plugin
	// is already inside the repoTar we cloned to read marketplace.json — slice it
	// out in-memory instead of cloning the whole repo again per plugin (mirrors
	// resolveSkillMarketplace). gitfetch's subtree fetch re-roots the subtree to
	// the archive root, so SliceSubtree(keepLeafDir=false) produces the same
	// shape. External-repo entries (github, or a different URL) still fetch their
	// own repo (cached). archiveRoot is "" — a git-clone tar is rooted at /.
	var entryTar []byte
	var entrySHA string
	switch sub := path.Clean(entrySpec.Subtree); {
	case entrySpec.URL == repo.CloneURL && (sub == "." || sub == "/"):
		// The marketplace repo ROOT itself is the plugin (a single-plugin
		// marketplace whose entry source is ".") — the cloned repoTar IS the
		// plugin. Serve it directly (no slice, no re-fetch).
		entryTar, entrySHA = repoTar, sha
	case entrySpec.URL == repo.CloneURL:
		// A subdir of the marketplace's own repo: slice the plugin out of the
		// already-cloned repoTar — no second network fetch.
		entryTar, err = contentkit.SliceSubtree(repoTar, "", sub, pluginEntryMaxFileBytes, false)
		if err != nil {
			return ResolveResult{}, fmt.Errorf("manager: slice plugin subtree %q: %w", name, err)
		}
		entrySHA = sha // the plugin subdir is at the marketplace repo's SHA
	default:
		// External-repo entry (github, or a different URL): fetch its own repo.
		entryTar, entrySHA, err = fetchEntry(ctx, entrySpec, cache)
		if err != nil {
			return ResolveResult{}, fmt.Errorf("manager: fetch marketplace entry %q: %w", name, err)
		}
	}

	if err := contentkit.VerifyPluginContents(bytes.NewReader(entryTar)); err != nil {
		return ResolveResult{}, fmt.Errorf("manager: verify plugin contents for %q: %w", name, err)
	}

	stageDir, err := stageTar(ctx, entryTar, extract.KindPlugin)
	if err != nil {
		return ResolveResult{}, err
	}

	// Use the entry's resolved SHA (the plugin entry's repo); fall back to
	// the marketplace repo's SHA for local-path entries.
	resolvedSHA := entrySHA
	if resolvedSHA == "" {
		resolvedSHA = sha
	}

	return ResolveResult{Name: name, Kind: "plugin", ResolvedSHA: resolvedSHA, StageDir: stageDir}, nil
}

// resolveSkillMarketplace resolves the "skill-marketplace" lens: tree-walk the
// repo for the named skill, slice its subtree, verify it.
func resolveSkillMarketplace(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme, cache *FetchCache) (ResolveResult, error) {
	repoTar, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme, cache)
	if err != nil {
		return ResolveResult{}, err
	}

	archiveRoot, skills, err := contentkit.DiscoverSkillsInTree(repoTar, repo.SkillsRootHint)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("manager: discover skills: %w", err)
	}

	// Find the requested skill.
	var foundSkill *contentkit.DiscoveredSkill
	for i := range skills {
		if skills[i].Name == name {
			foundSkill = &skills[i]
			break
		}
	}
	if foundSkill == nil {
		return ResolveResult{}, fmt.Errorf("manager: skill %q not found in skill marketplace", name)
	}

	// SliceSkillSubtree's subtreePath must be relative to archiveRoot
	// (already stripped). It is: subPath joined with the skill's Dir.
	// DiscoverSkillsInTree strips archiveRoot + subPath internally; the
	// returned Dir is the bare skill directory name under the skills-root.
	// SliceSkillSubtree expects subtreePath = "<subPath>/<dir>" (relative
	// to archiveRoot) so it can do its prefix match correctly.
	subPath := normSubPath(repo.SkillsRootHint)
	var subtreePath string
	if subPath == "" {
		subtreePath = foundSkill.Dir
	} else {
		subtreePath = path.Join(subPath, foundSkill.Dir)
	}

	sliced, err := contentkit.SliceSkillSubtree(repoTar, archiveRoot, subtreePath)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("manager: slice skill subtree for %q: %w", name, err)
	}

	if err := contentkit.VerifySkillContents(bytes.NewReader(sliced)); err != nil {
		return ResolveResult{}, fmt.Errorf("manager: verify skill contents for %q: %w", name, err)
	}

	// Guard the requested name as a safe path segment before it is joined into
	// the skills/<name>/ stage path. (DiscoverSkillsInTree already validated
	// foundSkill.Name; this closes the contract at the join site.)
	if err := contentkit.ValidateSkillName(name); err != nil {
		return ResolveResult{}, fmt.Errorf("manager: invalid skill name %q: %w", name, err)
	}
	stageDir, err := stageSkillNested(ctx, sliced, name)
	if err != nil {
		return ResolveResult{}, err
	}

	return ResolveResult{Name: name, Kind: "skill", ResolvedSHA: sha, StageDir: stageDir}, nil
}

// PlannedWrite is one file write planned by Project.
type PlannedWrite struct {
	Path    string
	Content []byte
	Merge   adapter.MergeKind
	Keys    []string
}

// Project runs route.Project over the staged tree for one adapter ID
// (canonical, e.g. "claude-code") and returns the planned file writes.
// The adapter must be registered (via blank-import of its subpackage) and
// implement route.RuleProvider. Returns an empty slice (no error) when the
// adapter has no projection rules.
func Project(stageDir, adapterID string) ([]PlannedWrite, error) {
	ad, ok := adapter.Lookup(adapterID)
	if !ok {
		return nil, fmt.Errorf("manager: adapter %q not found (did you blank-import its package?)", adapterID)
	}

	rp, ok := ad.(route.RuleProvider)
	if !ok {
		// Adapter exists but has no projection rules — not an error.
		return nil, nil
	}

	pr, err := route.Project(rp.ProjectionRules(), stageDir, "")
	if err != nil {
		return nil, fmt.Errorf("manager: project: %w", err)
	}

	out := make([]PlannedWrite, 0, len(pr.FileWrites))
	for _, fw := range pr.FileWrites {
		// Containment (local-installer policy): write nothing outside the
		// target adapter's own dot-dir. Every legitimate adapter destination
		// is under a dot-dir (.claude/, .codex/, .gemini/, .opencode/, .pi/,
		// .agents/); the escaping rules are the AGENTS.md→CLAUDE.md /
		// →GEMINI.md composites that land a loose file in the PROJECT ROOT.
		// `ach-cli plugin/skill install` must not touch the user's root files
		// (their own CLAUDE.md/README.md/etc.), so drop any project-root
		// destination here — EXCEPT .mcp.json, which is Claude Code's per-project
		// MCP registry (the file claude actually reads for plugin MCP servers);
		// a plugin that ships MCP is useless without it. This is the local
		// installer only — governed `env hydrate` (internal/cli/hydrate) keeps
		// the shared rules intact.
		if path.Dir(fw.Path) == "." && path.Base(fw.Path) != ".mcp.json" {
			continue
		}
		out = append(out, PlannedWrite{
			Path:    fw.Path,
			Content: fw.Content,
			Merge:   fw.Merge,
			Keys:    fw.Keys,
		})
	}
	return out, nil
}
