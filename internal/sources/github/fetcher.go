// SPDX-License-Identifier: Apache-2.0

// Package github is the GitHub source fetcher (Hub §10.1). It is the
// canonical reference shape that the other five per-source-type
// subpackages mirror.
//
// Fetch resolves the commit SHA at HEAD of spec.Ref (git ls-remote), skips
// re-publish via FetchResult{NotModified: true} when req.PriorRev already
// matches, and otherwise streams a git clone tarball for the reconciler to
// materialize under .tmp/<random> / fsync / rename(2) per §10.3. See
// git_transport.go.
//
// Path subset extraction honors spec.Path (F1): a directory path narrows to
// that subtree's contents (re-rooted), a single-file path returns that file's
// raw bytes. Narrowing happens on-disk (git.tarSubtree). Empty path → full
// repo tarball. (PluginMarketplace strips path before fetch — its
// marketplace.json discovery walks the whole repo.)
//
// Auth: optional. When spec.AuthSecretRef is non-nil, its Name resolves
// to a Kubernetes Secret in the CR's namespace and the PAT lives under
// req.Secret.Data[spec.AuthSecretRef.Key]; missing Secret / missing key
// surfaces as wrapped sources.ErrUnauthorized. When spec.AuthSecretRef
// is nil, the fetch is anonymous (Phase 02.1) — supported only for
// public repositories.
package github

import (
	"context"
	"fmt"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/cr02validate"
)

// Fetcher implements [sources.Fetcher] for GitHubSource.
type Fetcher struct {
	spec *achv1alpha1.GitHubSource

	// cloneURLForTesting overrides the upstream clone URL the
	// git-transport branch uses. Empty in production. Set by tests so
	// the git transport hits a local bare-repo fixture instead of
	// github.com.
	cloneURLForTesting string
}

// New constructs a GitHub source fetcher. Returns ErrUpstreamInvalid
// wrapping a descriptive message when spec is nil — the registry
// validates spec non-nil before reaching here, so this is defense in
// depth.
func New(spec *achv1alpha1.GitHubSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("github: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	// CR-02 metachar parity with bitbucket: reject URL-structural
	// metacharacters in spec.Repo / spec.Ref at construction time so a
	// crafted CR cannot smuggle them into the constructed clone URL or
	// the git subprocess argv. github repos are exactly <owner>/<name>
	// — allowMultiSegment=false.
	if err := cr02validate.RepoSlashIdentifier("github.repo", spec.Repo, false); err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	return &Fetcher{
		spec: spec,
	}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	return f.fetchViaGit(ctx, req)
}

// Compile-time assertion that *Fetcher satisfies sources.Fetcher.
var _ sources.Fetcher = (*Fetcher)(nil)
