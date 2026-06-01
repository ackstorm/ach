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

// FetchViaProvider runs the shared git transport flow: ls-remote to
// resolve the SHA, short-circuit on NotModified when priorRev matches,
// then full clone+fetch. provider is the error-prefix literal; cloneURL is
// the per-provider-built https clone URL.
func FetchViaProvider(ctx context.Context, provider, cloneURL, ref, token, priorRev string) (*sources.FetchResult, error) {
	sha, err := gitsrc.LsRemote(ctx, cloneURL, ref, token)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", provider, err)
	}
	if priorRev != "" && priorRev == sha {
		return &sources.FetchResult{NotModified: true, UpstreamRev: sha}, nil
	}
	gitRes, err := gitsrc.New(gitsrc.Spec{
		URL:   cloneURL,
		Ref:   ref,
		SHA:   sha,
		Token: token,
	}).Fetch(ctx, gitsrc.Request{})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", provider, err)
	}
	return &sources.FetchResult{
		Body:        gitRes.Body,
		UpstreamRev: gitRes.UpstreamRev,
	}, nil
}
