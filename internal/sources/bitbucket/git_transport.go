// SPDX-License-Identifier: Apache-2.0

// This file implements the git-protocol transport for the Bitbucket
// source fetcher — the only transport (git, since v0.2.0).
//
// Bitbucket Cloud only — Bitbucket Server (Stash) remains out of
// scope for v1alpha1.
//
// Auth: Bearer <token> — Repository Access Token, sent via
// http.extraHeader (never the URL).
//
// Clone URL: https://bitbucket.org/<workspace>/<repo>.git — host fixed
// (no per-CR override).

package bitbucket

import (
	"context"

	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/gitprovider"
)

// constructCloneURL builds the https clone URL. Workspace + Repo are
// already validated by New (CR-02 / validateFlatIdentifier rejects
// URL-structural metacharacters), so direct interpolation is safe.
// Delegates to the canonical sources.BitbucketCloneURL.
func (f *Fetcher) constructCloneURL() string {
	return sources.BitbucketCloneURL(f.spec.Workspace, f.spec.Repo)
}

func (f *Fetcher) fetchViaGit(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	token, err := sources.ExtractBearerToken("bitbucket", f.spec.AuthSecretRef, req.Secret)
	if err != nil {
		return nil, err
	}

	cloneURL := f.cloneURLForTesting
	if cloneURL == "" {
		cloneURL = f.constructCloneURL()
	}

	return gitprovider.FetchViaProvider(ctx, "bitbucket", cloneURL, f.spec.Ref, token, req.PriorRev, f.spec.Path)
}
