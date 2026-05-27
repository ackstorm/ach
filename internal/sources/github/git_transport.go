// SPDX-License-Identifier: Apache-2.0

// This file implements the git-protocol transport for the GitHub
// source fetcher (FIX_GIT.txt).
//
// Composition:
//   1. gitsrc.LsRemote(url, ref, token) → SHA  (cheap; no clone).
//   2. gitsrc.Fetcher{URL, Ref, SHA, Token}.Fetch() → tarball.
//
// The token (when present) reaches git via http.extraHeader; never URL.
// See internal/sources/git for the engine.

package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
	gitsrc "github.com/ackstorm/ach/internal/sources/git"
)

// resolvedTransport returns "git" or "rest". Empty defaults to "git"
// (matches the kubebuilder default; defends against a CR submitted
// before kube-apiserver defaulting applies — e.g. an envtest scenario
// that builds the CR struct in Go and bypasses admission).
func (f *Fetcher) resolvedTransport() string {
	if f.spec.Transport == "rest" {
		return "rest"
	}
	return "git"
}

func (f *Fetcher) fetchViaGit(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	token, err := f.extractToken(req)
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(f.spec.Repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("github: spec.repo must be <owner>/<name>, got %q: %w",
			f.spec.Repo, sources.ErrUpstreamInvalid)
	}

	cloneURL := f.cloneURLForTesting
	if cloneURL == "" {
		cloneURL = fmt.Sprintf("https://github.com/%s/%s.git", parts[0], parts[1])
	}

	sha, err := gitsrc.LsRemote(ctx, cloneURL, f.spec.Ref, token)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
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
		return nil, fmt.Errorf("github: %w", err)
	}
	return &sources.FetchResult{
		Body:        gitRes.Body,
		UpstreamRev: gitRes.UpstreamRev,
	}, nil
}
