// SPDX-License-Identifier: Apache-2.0

// This file implements the git-protocol transport for the GitLab
// source fetcher (FIX_GIT.txt).
//
// Auth: HTTP Basic with username "oauth2" and the token as password
// (Authorization: Basic base64("oauth2:<token>")). GitLab's documented
// PAT/Group/Project-token method over git-http; works on gitlab.com AND
// self-hosted. Bearer is NOT used for GitLab — self-hosted instances
// configured without Bearer support 401 it and challenge for Basic
// (verified against a self-hosted GitLab instance). The scheme is selected centrally in
// internal/sources/gitprovider.schemeForProvider.
//
// Clone URL: https://<host>/<project>.git — defaults to gitlab.com
// when spec.Host is empty (matches the REST path's default).

package gitlab

import (
	"context"
	"fmt"

	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/gitprovider"
)

// normalizeGitLabHost delegates to the canonical sources.NormalizeGitLabHost
// (single source of truth shared with the marketplace dispatch path). Kept
// as a thin local alias so existing call sites + CR-02 validation timing
// stay unchanged.
func normalizeGitLabHost(host string) string {
	return sources.NormalizeGitLabHost(host)
}

// constructCloneURL builds the https clone URL from spec.Host (default
// gitlab.com) and spec.Project. Delegates to the canonical
// sources.GitLabCloneURL so the source fetcher and the marketplace
// dispatch path yield the SAME URL for the same GitLabSource. Extracted
// as a method so the unit tests can pin the default-host behavior without
// spinning a network call.
func (f *Fetcher) constructCloneURL() string {
	return sources.GitLabCloneURL(f.spec.Host, f.spec.Project)
}

func (f *Fetcher) fetchViaGit(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	token, err := sources.ExtractBearerToken("gitlab", f.spec.AuthSecretRef, req.Secret)
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

	return gitprovider.FetchViaProvider(ctx, "gitlab", cloneURL, f.spec.Ref, token, req.PriorRev, f.spec.Path)
}
