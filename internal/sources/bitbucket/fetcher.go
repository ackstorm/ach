// SPDX-License-Identifier: Apache-2.0

// Package bitbucket is the Bitbucket source fetcher (Hub §10.1).
//
// v1alpha1 supports Bearer Repository Access Tokens only — Bitbucket
// username + app-password auth is deferred to v1beta1 per the
// 02-CONTEXT.md deferred-ideas note.
package bitbucket

import (
	"context"
	"fmt"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/cr02validate"
)

// Fetcher implements [sources.Fetcher] for BitbucketSource.
type Fetcher struct {
	spec *achv1alpha1.BitbucketSource

	// cloneURLForTesting overrides the upstream clone URL the
	// git-transport branch uses. Empty in production. Set by tests so
	// the git transport hits a local bare-repo fixture instead of
	// bitbucket.org.
	cloneURLForTesting string
}

// New constructs a Bitbucket source fetcher. Returns ErrUpstreamInvalid
// when spec is nil, or when spec.Workspace/Repo/Ref contains URL-
// structural metacharacters (CR-02). The v1alpha1 CRD enforces only
// MinLength=1 on these fields; this constructor rejects characters that
// could redirect the request to an attacker-controlled host or smuggle
// query parameters past the bearer-in-header discipline (T-02-02-02,
// T-02-02-07).
//
// Workspace and Repo are forbidden from containing '/', '?', '#', '\',
// or whitespace — Bitbucket workspaces and repo slugs are flat
// identifiers and never legitimately carry these characters.
//
// Ref is permitted '/' (e.g. "feature/branch" is a valid Bitbucket ref
// shape) but forbidden '?', '#', '\', and whitespace.
func New(spec *achv1alpha1.BitbucketSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("bitbucket: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	if err := cr02validate.FlatIdentifier("bitbucket.workspace", spec.Workspace); err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
	}
	if err := cr02validate.FlatIdentifier("bitbucket.repo", spec.Repo); err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
	}
	if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
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
