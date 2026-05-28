// SPDX-License-Identifier: Apache-2.0

// marketplace_dispatch.go owns Stage-2's per-entry fetch path. The
// Claude Code marketplace real schema (TODO §5) replaces the 6-source
// CRD discriminator with three Kinds (git-subdir / url / local-path),
// none of which dispatch through internal/sources/registry.For. All
// three resolve to a git-remote clone via internal/sources/git.
//
// The local-path Kind is special: it points at a subdirectory of the
// MARKETPLACE's OWN repo. We resolve it by reading the marketplace
// CR's spec.<type>.repo/url, building a synthetic git-subdir Spec, and
// calling the same git.Fetcher used by the other two Kinds. This is
// why this function takes the parent PluginMarketplace pointer.

package ach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	sourcesgit "github.com/ackstorm/ach/internal/sources/git"
)

// Kind constants for ClaudeCodeMarketplaceSource. The wire-format
// discriminator values used by UnmarshalJSON + Stage-2 dispatch.
const (
	kindGitSubdir = "git-subdir"
	kindURL       = "url"
	kindGitHub    = "github"
	kindLocalPath = "local-path"
)

// errUnsupportedPluginSource is the typed sentinel
// dispatchMarketplacePlugin returns when the per-entry Kind is empty
// (a wire-format shape we don't model — e.g. {"source":"npm",...}).
// Stage-2 maps to ReasonUnsupportedPluginSource.
var errUnsupportedPluginSource = errors.New("unsupported plugin source")

// gitFetcher is the package-level seam tests inject to bypass the live
// git binary. nil → real git.Fetcher.New is used.
type gitFetcher interface {
	Fetch(ctx context.Context, req sourcesgit.Request) (*sourcesgit.Result, error)
}

// newGitFetcherFn produces a gitFetcher for a Spec. Overridable by tests.
var newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
	return sourcesgit.New(spec)
}

// newResolveHeadSHAFn does a `git ls-remote <url> <ref>` and returns the
// resolved 40-hex commit SHA. Called by dispatchMarketplacePlugin for
// any git-backed entry whose sha field is absent — invoked before
// handing the Spec to git.Fetcher so Fetcher's 40-hex contract is
// satisfied. Generalized from local-path-only in Phase 2 (#15).
//
// Overridable in tests so we don't shell out in unit tests.
var newResolveHeadSHAFn = func(ctx context.Context, url, ref, token string) (string, error) {
	cloneURL := url
	if token != "" && strings.HasPrefix(cloneURL, "https://") {
		cloneURL = "https://" + token + ":x-oauth-basic@" + strings.TrimPrefix(cloneURL, "https://")
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", cloneURL, ref)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ls-remote %s %s: %v: %w", url, ref, err, sources.ErrUpstreamInvalid)
	}
	// Output format: "<40-hex>\t<refname>\n"
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
	if len(parts) != 2 || len(parts[0]) != 40 {
		return "", fmt.Errorf("ls-remote %s %s: unexpected output %q: %w", url, ref, out, sources.ErrUpstreamInvalid)
	}
	return parts[0], nil
}

// dispatchMarketplacePlugin runs the per-entry fetch and returns a
// streaming io.ReadCloser + the UpstreamRev (the resolved SHA).
//
//   - git-subdir / url / github / local-path: build a git.Spec via
//     buildGitSpecForEntry. If Spec.SHA is empty, pre-resolve via
//     newResolveHeadSHAFn (git ls-remote semantics) so the Fetcher's
//     40-hex contract is satisfied. Then Fetch + return the streaming
//     tar.gz body.
//   - "": errUnsupportedPluginSource (Stage-2 maps to
//     ReasonUnsupportedPluginSource).
//
// Sha-optional rationale: the official schema lists ref and sha as both
// optional. Upstream catalogs (Anthropic) ship entries without sha that
// MUST still materialize. The Fetcher itself keeps the 40-hex pin
// contract — we satisfy it by resolving on the marketplace layer.
//
// auth is the marketplace's auth Secret (re-used per the v1alpha1
// design: per-entry auth is NOT yet a wire-format field). May be nil.
func dispatchMarketplacePlugin(
	ctx context.Context,
	mp *achv1alpha1.PluginMarketplace,
	entry ClaudeCodeMarketplacePlugin,
	auth *corev1.Secret,
	cacheRoot string,
) (io.ReadCloser, string, error) {
	spec, err := buildGitSpecForEntry(mp, entry, auth, cacheRoot)
	if err != nil {
		return nil, "", err
	}
	// Pre-resolve ref→sha when the entry didn't pin one. Applies to every
	// git-backed Kind (git-subdir, url, github, local-path). local-path
	// already had this branch — Phase 2 generalizes it to all Kinds the
	// dispatcher returned a Spec for.
	if spec.SHA == "" {
		sha, rerr := newResolveHeadSHAFn(ctx, spec.URL, spec.Ref, spec.Token)
		if rerr != nil {
			return nil, "", rerr
		}
		spec.SHA = sha
	}
	f := newGitFetcherFn(spec)
	res, err := f.Fetch(ctx, sourcesgit.Request{})
	if err != nil {
		return nil, "", err
	}
	return res.Body, res.UpstreamRev, nil
}

// buildGitSpecForEntry maps a parsed entry into a git.Spec. Pulls the
// per-entry token from the marketplace's auth Secret when present.
func buildGitSpecForEntry(
	mp *achv1alpha1.PluginMarketplace,
	entry ClaudeCodeMarketplacePlugin,
	auth *corev1.Secret,
	cacheRoot string,
) (sourcesgit.Spec, error) {
	token := extractTokenFromSecret(auth)
	switch entry.Source.Kind {
	case kindGitSubdir:
		return sourcesgit.Spec{
			URL:       entry.Source.URL,
			Ref:       defaultRef(entry.Source.Ref),
			SHA:       entry.Source.SHA,
			Subtree:   entry.Source.Path,
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case kindURL:
		// url+path collapse: when path is non-empty the entry behaves
		// like git-subdir (upstream-drift ack — see marketplace_parse.go
		// header). Empty path → whole-worktree tar.
		return sourcesgit.Spec{
			URL:       entry.Source.URL,
			Ref:       defaultRef(entry.Source.Ref),
			SHA:       entry.Source.SHA,
			Subtree:   entry.Source.Path,
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case kindGitHub:
		return sourcesgit.Spec{
			URL:       "https://github.com/" + entry.Source.Repo + ".git",
			Ref:       defaultRef(entry.Source.Ref),
			SHA:       entry.Source.SHA,
			Subtree:   "", // github Kind has no path → whole-worktree
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case kindLocalPath:
		// Resolve the marketplace's own repo URL + Ref.
		url, ref, err := marketplaceOwnRepo(mp)
		if err != nil {
			return sourcesgit.Spec{}, err
		}
		return sourcesgit.Spec{
			URL:       url,
			Ref:       ref,
			SHA:       "", // resolved by caller via newResolveHeadSHAFn
			Subtree:   entry.Source.Path,
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case "":
		return sourcesgit.Spec{}, errUnsupportedPluginSource
	default:
		return sourcesgit.Spec{}, fmt.Errorf("plugin %q: unknown source Kind %q: %w",
			truncateErrField(entry.Name), entry.Source.Kind, sources.ErrUpstreamInvalid)
	}
}

// marketplaceOwnRepo returns the (URL, Ref) of the marketplace's own
// upstream repo, derived from spec.<type>. Only github / gitlab /
// bitbucket carry a repo identity; s3 / gcs / http do not — for those
// types, local-path entries are unsupported and return an explicit
// error.
func marketplaceOwnRepo(mp *achv1alpha1.PluginMarketplace) (string, string, error) {
	switch mp.Spec.Type {
	case "github":
		if mp.Spec.GitHub == nil {
			return "", "", fmt.Errorf("github marketplace missing spec.github: %w", sources.ErrUpstreamInvalid)
		}
		return "https://github.com/" + mp.Spec.GitHub.Repo + ".git", defaultRef(mp.Spec.GitHub.Ref), nil
	case "gitlab":
		if mp.Spec.GitLab == nil {
			return "", "", fmt.Errorf("gitlab marketplace missing spec.gitlab: %w", sources.ErrUpstreamInvalid)
		}
		host := mp.Spec.GitLab.Host
		if host == "" {
			host = "https://gitlab.com"
		}
		return host + "/" + mp.Spec.GitLab.Project + ".git", defaultRef(mp.Spec.GitLab.Ref), nil
	case "bitbucket":
		if mp.Spec.Bitbucket == nil {
			return "", "", fmt.Errorf("bitbucket marketplace missing spec.bitbucket: %w", sources.ErrUpstreamInvalid)
		}
		return "https://bitbucket.org/" + mp.Spec.Bitbucket.Workspace + "/" + mp.Spec.Bitbucket.Repo + ".git",
			defaultRef(mp.Spec.Bitbucket.Ref), nil
	default:
		return "", "", fmt.Errorf("local-path entries unsupported for marketplace type %q: %w",
			mp.Spec.Type, sources.ErrUpstreamInvalid)
	}
}

// defaultRef returns "main" when ref is empty — a marketplace fixture
// may omit Ref to mean "follow main".
func defaultRef(ref string) string {
	if ref == "" {
		return "main"
	}
	return ref
}

// extractTokenFromSecret peeks at the first non-empty Secret value as
// the bearer/PAT token. Phase 2 plugin entries don't carry their own
// AuthSecretRef; they re-use the marketplace's. A future v1beta1 may
// surface per-entry auth (TODO §3) — at that point this extraction
// becomes keyed by an entry-specific Secret key.
func extractTokenFromSecret(s *corev1.Secret) string {
	if s == nil {
		return ""
	}
	for _, v := range s.Data {
		if len(v) > 0 {
			return string(v)
		}
	}
	return ""
}
