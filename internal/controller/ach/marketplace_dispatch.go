// SPDX-License-Identifier: Apache-2.0

// marketplace_dispatch.go owns Stage-2's per-entry fetch path. The
// Claude Code marketplace real schema defines four plugin-source Kinds
// (git-subdir / url / github / local-path), none of which dispatch
// through internal/sources/registry.For. All four resolve to a
// git-remote clone via internal/gitfetch.
//
// The local-path Kind is special: it points at a subdirectory of the
// MARKETPLACE's OWN repo. We resolve it by reading the marketplace
// CR's spec.<type>.repo/url, building a synthetic git-subdir Spec, and
// calling the same gitfetch.Fetcher used by the other three Kinds. This is
// why this function takes the parent PluginMarketplace pointer.

package ach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	sourcesgit "github.com/ackstorm/ach/internal/gitfetch"
	"github.com/ackstorm/ach/internal/sources"
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
// git binary. nil → real gitfetch.Fetcher.New is used.
type gitFetcher interface {
	Fetch(ctx context.Context, req sourcesgit.Request) (*sourcesgit.Result, error)
}

// newGitFetcherFn produces a gitFetcher for a Spec. Overridable by tests.
var newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
	return sourcesgit.New(spec)
}

// newResolveHeadSHAFn resolves ref→sha for sha-less marketplace entries
// before handing the Spec to gitfetch.Fetcher (the Fetcher's 40-hex contract
// requires a pinned SHA). Delegates to gitfetch.LsRemote which already
// implements namespace-scoped ref disambiguation
// (refs/heads/<ref>+refs/tags/<ref> with branch-preferred resolution) —
// critical because Stage-2 fetched the wrong tree on real repos where
// a branch named "main" and another like "daisy/caffeinate/main" both
// existed, and the dispatcher's own ls-remote wrapper picked the
// alphabetically first match.
//
// Overridable in tests so we don't shell out in unit tests.
var newResolveHeadSHAFn = func(ctx context.Context, url, ref, token string, scheme sourcesgit.AuthScheme) (string, error) {
	sha, err := sourcesgit.LsRemote(ctx, url, ref, token, scheme)
	if err != nil {
		return "", fmt.Errorf("ls-remote %s %s: %w", url, ref, err)
	}
	return sha, nil
}

// dispatchMarketplacePlugin runs the per-entry fetch and returns a
// streaming io.ReadCloser + the UpstreamRev (the resolved SHA).
//
//   - git-subdir / url / github / local-path: build a gitfetch.Spec via
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
		sha, rerr := newResolveHeadSHAFn(ctx, spec.URL, spec.Ref, spec.Token, spec.AuthScheme)
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

// buildGitSpecForEntry maps a parsed entry into a gitfetch.Spec. Pulls the
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
		canonURL, host, cerr := sources.CanonicalCloneURL(entry.Source.URL)
		if cerr != nil {
			return sourcesgit.Spec{}, cerr
		}
		return sourcesgit.Spec{
			URL:        canonURL,
			Ref:        defaultRef(entry.Source.Ref),
			SHA:        entry.Source.SHA,
			Subtree:    entry.Source.Path,
			Token:      tokenForHost(mp, host, token),
			CacheRoot:  cacheRoot,
			AuthScheme: schemeForHost(mp, host),
		}, nil
	case kindURL:
		// url+path collapse: when path is non-empty the entry behaves
		// like git-subdir (upstream-drift ack — see marketplace_parse.go
		// header). Empty path → whole-worktree tar.
		canonURL, host, cerr := sources.CanonicalCloneURL(entry.Source.URL)
		if cerr != nil {
			return sourcesgit.Spec{}, cerr
		}
		return sourcesgit.Spec{
			URL:        canonURL,
			Ref:        defaultRef(entry.Source.Ref),
			SHA:        entry.Source.SHA,
			Subtree:    entry.Source.Path,
			Token:      tokenForHost(mp, host, token),
			CacheRoot:  cacheRoot,
			AuthScheme: schemeForHost(mp, host),
		}, nil
	case kindGitHub:
		ghURL := sources.GitHubCloneURL(entry.Source.Repo)
		return sourcesgit.Spec{
			URL:        ghURL,
			Ref:        defaultRef(entry.Source.Ref),
			SHA:        entry.Source.SHA,
			Subtree:    "", // github Kind has no path → whole-worktree
			Token:      tokenForHost(mp, "github.com", token),
			CacheRoot:  cacheRoot,
			AuthScheme: schemeForHost(mp, "github.com"), // GitHubCloneURL is always github.com
		}, nil
	case kindLocalPath:
		// Resolve the marketplace's own repo URL + Ref.
		url, ref, err := marketplaceOwnRepo(mp)
		if err != nil {
			return sourcesgit.Spec{}, err
		}
		_, host, cerr := sources.CanonicalCloneURL(url)
		if cerr != nil {
			return sourcesgit.Spec{}, cerr
		}
		return sourcesgit.Spec{
			URL:        url,
			Ref:        ref,
			SHA:        "", // resolved by caller via newResolveHeadSHAFn
			Subtree:    entry.Source.Path,
			Token:      tokenForHost(mp, host, token),
			CacheRoot:  cacheRoot,
			AuthScheme: schemeForHost(mp, host),
		}, nil
	case "":
		return sourcesgit.Spec{}, errUnsupportedPluginSource
	default:
		return sourcesgit.Spec{}, fmt.Errorf("plugin %q: unknown source Kind %q: %w",
			truncateErrField(entry.Name), entry.Source.Kind, sources.ErrUpstreamInvalid)
	}
}

// schemeForHost picks the git HTTP auth scheme for a marketplace plugin
// entry given the entry's clone-URL host. A GitLab-typed marketplace
// authenticates clones to its OWN GitLab host with HTTP Basic
// "oauth2:<token>" (the only scheme self-hosted GitLab honors; Bearer
// 401s). A host that doesn't match (e.g. a github Kind inside a gitlab
// marketplace) and every non-gitlab marketplace keep Bearer. host is the
// lowercased host from sources.CanonicalCloneURL; comparison uses the same
// sources.NormalizeGitLabHost as marketplaceOwnRepo so a bare or scheme-
// prefixed spec.gitlab.host both match.
func schemeForHost(mp *achv1alpha1.PluginMarketplace, host string) sourcesgit.AuthScheme {
	if mp.Spec.Type != "gitlab" || mp.Spec.GitLab == nil {
		return sourcesgit.AuthBearer
	}
	want := sources.NormalizeGitLabHost(mp.Spec.GitLab.Host)
	if want == "" {
		want = "gitlab.com"
	}
	if strings.EqualFold(host, want) {
		return sourcesgit.AuthBasicOAuth2
	}
	return sourcesgit.AuthBearer
}

// ownHostOf returns the marketplace's canonical upstream host (the host of
// marketplaceOwnRepo's clone URL), or "" when the marketplace type has no
// git own-host (s3 / gcs / http) or its repo identity is malformed.
func ownHostOf(mp *achv1alpha1.PluginMarketplace) string {
	ownURL, _, err := marketplaceOwnRepo(mp)
	if err != nil {
		return ""
	}
	_, host, err := sources.CanonicalCloneURL(ownURL)
	if err != nil {
		return ""
	}
	return host
}

// tokenForHost returns the marketplace auth token ONLY when the entry's
// canonical clone-URL host matches the marketplace's OWN upstream host. A
// marketplace auth Secret is scoped to its own provider host; attaching it
// to a foreign host (e.g. a github.com plugin entry inside a gitlab
// marketplace) leaks a wrong-provider credential AND 401s where an anonymous
// clone of a public repo would succeed (the GitLab oauth2 PAT sent as a
// github Bearer is rejected). Foreign-host entries therefore clone
// anonymously until per-entry auth lands (see extractTokenFromSecret TODO).
// host is the lowercased host from sources.CanonicalCloneURL; the comparison
// is the same own-host check schemeForHost makes for the GitLab scheme, but
// applied to EVERY marketplace type (a github/bitbucket/http marketplace
// likewise must not lend its token to a foreign-host entry).
func tokenForHost(mp *achv1alpha1.PluginMarketplace, host, token string) string {
	if own := ownHostOf(mp); own != "" && strings.EqualFold(host, own) {
		return token
	}
	return ""
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
		return sources.GitHubCloneURL(mp.Spec.GitHub.Repo), defaultRef(mp.Spec.GitHub.Ref), nil
	case "gitlab":
		if mp.Spec.GitLab == nil {
			return "", "", fmt.Errorf("gitlab marketplace missing spec.gitlab: %w", sources.ErrUpstreamInvalid)
		}
		return sources.GitLabCloneURL(mp.Spec.GitLab.Host, mp.Spec.GitLab.Project), defaultRef(mp.Spec.GitLab.Ref), nil
	case "bitbucket":
		if mp.Spec.Bitbucket == nil {
			return "", "", fmt.Errorf("bitbucket marketplace missing spec.bitbucket: %w", sources.ErrUpstreamInvalid)
		}
		return sources.BitbucketCloneURL(mp.Spec.Bitbucket.Workspace, mp.Spec.Bitbucket.Repo),
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
