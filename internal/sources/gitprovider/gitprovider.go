// SPDX-License-Identifier: Apache-2.0

// Package gitprovider holds the shared git-transport flow used by the
// github / gitlab / bitbucket source fetchers. It lives in its own leaf
// package (not the parent internal/sources) because it depends on
// internal/sources/git, which itself imports internal/sources — putting
// FetchViaProvider in the parent would create an import cycle.
package gitprovider

import (
	"context"
	"fmt"

	"github.com/ackstorm/ach/internal/sources"
	gitsrc "github.com/ackstorm/ach/internal/sources/git"
)

// schemeForProvider maps a git provider literal to the HTTP auth scheme
// its git-smart-http endpoint expects. GitLab (gitlab.com AND self-hosted)
// authenticates with HTTP Basic "oauth2:<token>" — its documented PAT
// git-http method and the only one self-hosted instances that reject
// Bearer will accept. github.com and bitbucket.org keep Bearer.
func schemeForProvider(provider string) gitsrc.AuthScheme {
	if provider == "gitlab" {
		return gitsrc.AuthBasicOAuth2
	}
	return gitsrc.AuthBearer
}

// FetchViaProvider runs the shared git transport flow: ls-remote to
// resolve the SHA, short-circuit on NotModified when priorRev matches,
// then full clone+fetch. provider is the error-prefix literal; cloneURL is
// the per-provider-built https clone URL.
func FetchViaProvider(ctx context.Context, provider, cloneURL, ref, token, priorRev string) (*sources.FetchResult, error) {
	scheme := schemeForProvider(provider)
	sha, err := gitsrc.LsRemote(ctx, cloneURL, ref, token, scheme)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", provider, err)
	}
	if priorRev != "" && priorRev == sha {
		return &sources.FetchResult{NotModified: true, UpstreamRev: sha}, nil
	}
	gitRes, err := gitsrc.New(gitsrc.Spec{
		URL:        cloneURL,
		Ref:        ref,
		SHA:        sha,
		Token:      token,
		AuthScheme: scheme,
	}).Fetch(ctx, gitsrc.Request{})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", provider, err)
	}
	return &sources.FetchResult{
		Body:        gitRes.Body,
		UpstreamRev: gitRes.UpstreamRev,
	}, nil
}
