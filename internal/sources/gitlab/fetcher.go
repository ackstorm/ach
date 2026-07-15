// SPDX-License-Identifier: Apache-2.0

// Package gitlab is the GitLab source fetcher (Hub §10.1).
//
// Fetch resolves the commit SHA at HEAD of spec.Ref (git ls-remote), skips
// re-publish via FetchResult{NotModified: true} when req.PriorRev already
// matches, and otherwise streams a git clone tarball for the reconciler to
// materialize under .tmp/<random> / fsync / rename(2) per §10.3. See
// git_transport.go.
package gitlab

import (
	"context"
	"fmt"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/cr02validate"
)

// Fetcher implements [sources.Fetcher] for GitLabSource.
type Fetcher struct {
	spec *achv1alpha1.GitLabSource

	// cloneURLForTesting overrides the upstream clone URL the
	// git-transport branch uses. Empty in production. Set by tests so
	// the git transport hits a local bare-repo fixture instead of
	// gitlab.com.
	cloneURLForTesting string
}

// New constructs a GitLab source fetcher. Returns ErrUpstreamInvalid
// when spec is nil.
func New(spec *achv1alpha1.GitLabSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("gitlab: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	// CR-02 metachar parity with bitbucket: reject URL-structural
	// metacharacters in spec.Host / spec.Project / spec.Ref at
	// construction time so a crafted CR cannot smuggle them into the
	// constructed clone URL or the git subprocess argv. gitlab
	// projects can be deeply nested — allowMultiSegment=true.
	//
	// spec.Host is normalized (case-insensitive scheme strip +
	// trailing-slash strip) BEFORE validation so a legitimate
	// `https://gitlab.example.com` doesn't fail the HostIdentifier
	// '/'-forbidden rule.
	if err := cr02validate.HostIdentifier(normalizeGitLabHost(spec.Host)); err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
	}
	if err := cr02validate.RepoSlashIdentifier("gitlab.project", spec.Project, true); err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
	}
	if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
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
