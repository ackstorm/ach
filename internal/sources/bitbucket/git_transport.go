// SPDX-License-Identifier: Apache-2.0

// This file implements the git-protocol transport for the Bitbucket
// source fetcher (FIX_GIT.txt).
//
// Bitbucket Cloud only — Bitbucket Server (Stash) remains out of
// scope for v1alpha1.
//
// Auth: Bearer <token> — same Repository Access Token shape the
// legacy REST path already uses on /2.0/repositories/{ws}/{repo}/commit
// and on /{ws}/{repo}/get/<sha>.tar.gz.
//
// Clone URL: https://bitbucket.org/<workspace>/<repo>.git — host fixed
// (no per-CR override; matches the legacy REST defaults).

package bitbucket

import (
	"context"
	"fmt"

	"github.com/ackstorm/ach/internal/sources"
	gitsrc "github.com/ackstorm/ach/internal/sources/git"
)

// resolvedTransport returns "git" or "rest". Empty defaults to "git".
func (f *Fetcher) resolvedTransport() string {
	if f.spec.Transport == "rest" {
		return "rest"
	}
	return "git"
}

// constructCloneURL builds the https clone URL. Workspace + Repo are
// already validated by New (CR-02 / validateFlatIdentifier rejects
// URL-structural metacharacters), so direct interpolation is safe.
func (f *Fetcher) constructCloneURL() string {
	return fmt.Sprintf("https://bitbucket.org/%s/%s.git", f.spec.Workspace, f.spec.Repo)
}

func (f *Fetcher) fetchViaGit(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	token, err := f.extractToken(req)
	if err != nil {
		return nil, err
	}

	cloneURL := f.cloneURLForTesting
	if cloneURL == "" {
		cloneURL = f.constructCloneURL()
	}

	sha, err := gitsrc.LsRemote(ctx, cloneURL, f.spec.Ref, token)
	if err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
	}

	if req.PriorRev != "" && req.PriorRev == sha {
		return &sources.FetchResult{NotModified: true, UpstreamRev: sha}, nil
	}

	gitRes, err := gitsrc.New(gitsrc.Spec{
		URL:   cloneURL,
		Ref:   f.spec.Ref,
		SHA:   sha,
		Token: token,
	}).Fetch(ctx, gitsrc.Request{})
	if err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
	}
	return &sources.FetchResult{
		Body:        gitRes.Body,
		UpstreamRev: gitRes.UpstreamRev,
	}, nil
}
