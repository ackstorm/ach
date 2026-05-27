// SPDX-License-Identifier: Apache-2.0

// This file implements the git-protocol transport for the GitLab
// source fetcher (FIX_GIT.txt).
//
// Auth: same Bearer <token> shape that the legacy REST path's
// PRIVATE-TOKEN header maps to on GitLab's git endpoints (PAT and
// Project/Group Access Tokens supported via Bearer on git over HTTPS
// since GitLab 15.x).
//
// Clone URL: https://<host>/<project>.git — defaults to gitlab.com
// when spec.Host is empty (matches the REST path's default).

package gitlab

import (
	"context"
	"fmt"
	"strings"

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

// constructCloneURL builds the https clone URL from spec.Host (default
// gitlab.com) and spec.Project. Extracted as a method so the unit tests
// can pin the default-host behavior without spinning a network call.
func (f *Fetcher) constructCloneURL() string {
	host := f.spec.Host
	if host == "" {
		host = "gitlab.com"
	}
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")
	return fmt.Sprintf("https://%s/%s.git", host, f.spec.Project)
}

func (f *Fetcher) fetchViaGit(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	token, err := f.extractToken(req)
	if err != nil {
		return nil, err
	}

	if f.spec.Project == "" {
		return nil, fmt.Errorf("gitlab: spec.project required: %w", sources.ErrUpstreamInvalid)
	}

	cloneURL := f.cloneURLForTesting
	if cloneURL == "" {
		cloneURL = f.constructCloneURL()
	}

	sha, err := gitsrc.LsRemote(ctx, cloneURL, f.spec.Ref, token)
	if err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
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
		return nil, fmt.Errorf("gitlab: %w", err)
	}
	return &sources.FetchResult{
		Body:        gitRes.Body,
		UpstreamRev: gitRes.UpstreamRev,
	}, nil
}
