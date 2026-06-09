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

// cloneRepo fetches the whole repo (no subtree) as a gzipped tar and
// returns the bytes. SHA is resolved via LsRemote.
func cloneRepo(ctx context.Context, cloneURL, ref, token string, scheme gitfetch.AuthScheme) (tarBytes []byte, resolvedSHA string, err error) {
	sha, err := gitfetch.LsRemote(ctx, cloneURL, ref, token, scheme)
	if err != nil {
		return nil, "", fmt.Errorf("manager: ls-remote %s %s: %w", cloneURL, ref, err)
	}

	spec := gitfetch.Spec{
		URL:        cloneURL,
		Ref:        ref,
		SHA:        sha,
		Token:      token,
		AuthScheme: scheme,
	}
	f := gitfetch.New(spec)
	res, err := f.Fetch(ctx, gitfetch.Request{})
	if err != nil {
		return nil, "", fmt.Errorf("manager: fetch %s: %w", cloneURL, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("manager: read clone body: %w", err)
	}
	return body, sha, nil
}

// fetchEntry fetches a specific entry using a gitfetch.Spec (for marketplace
// entries). If spec.SHA is empty, resolves it via LsRemote first.
func fetchEntry(ctx context.Context, spec gitfetch.Spec) (tarBytes []byte, resolvedSHA string, err error) {
	if spec.SHA == "" {
		sha, err := gitfetch.LsRemote(ctx, spec.URL, spec.Ref, spec.Token, spec.AuthScheme)
		if err != nil {
			return nil, "", fmt.Errorf("manager: ls-remote entry %s %s: %w", spec.URL, spec.Ref, err)
		}
		spec.SHA = sha
	}

	f := gitfetch.New(spec)
	res, err := f.Fetch(ctx, gitfetch.Request{})
	if err != nil {
		return nil, "", fmt.Errorf("manager: fetch entry %s: %w", spec.URL, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("manager: read entry body: %w", err)
	}
	return body, spec.SHA, nil
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
	scheme := schemeFor(repo.AuthScheme)
	ref := defaultRef(repo.GitRef)

	switch lens {
	case discover.LensPlugin:
		return resolveDirectPlugin(ctx, repo, token, name, ref, scheme)
	case discover.LensSkill:
		return resolveDirectSkill(ctx, repo, token, name, ref, scheme)
	case discover.LensPluginMarketplace:
		return resolvePluginMarketplace(ctx, repo, token, name, ref, scheme)
	case discover.LensSkillMarketplace:
		return resolveSkillMarketplace(ctx, repo, token, name, ref, scheme)
	default:
		return ResolveResult{}, fmt.Errorf("manager: unknown lens %q (want plugin|skill|plugin-marketplace|skill-marketplace)", lens)
	}
}

// resolveDirectPlugin resolves the "plugin" lens: the whole repo IS the plugin
// and name must equal repo.Name.
func resolveDirectPlugin(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme) (ResolveResult, error) {
	if name != repo.Name {
		return ResolveResult{}, fmt.Errorf("manager: direct-plugin lens requires name == repo.Name (got %q, repo %q)", name, repo.Name)
	}
	tarBytes, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme)
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
func resolveDirectSkill(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme) (ResolveResult, error) {
	tarBytes, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme)
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
func resolvePluginMarketplace(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme) (ResolveResult, error) {
	repoTar, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme)
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

	entryTar, entrySHA, err := fetchEntry(ctx, entrySpec)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("manager: fetch marketplace entry %q: %w", name, err)
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
func resolveSkillMarketplace(ctx context.Context, repo store.RepoEntry, token, name, ref string, scheme gitfetch.AuthScheme) (ResolveResult, error) {
	repoTar, sha, err := cloneRepo(ctx, repo.CloneURL, ref, token, scheme)
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
		// .agents/); the only escaping rules are the AGENTS.md→CLAUDE.md /
		// →GEMINI.md composites that land a loose file in the PROJECT ROOT.
		// `ach-cli plugin/skill install` must not touch the user's root files
		// (their own CLAUDE.md/README.md/etc.), so drop any project-root
		// destination here. This is the local installer only — governed
		// `env hydrate` (internal/cli/hydrate) keeps the shared rules intact.
		if path.Dir(fw.Path) == "." {
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
