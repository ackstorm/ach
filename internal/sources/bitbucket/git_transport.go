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
	"github.com/ackstorm/ach/internal/sources/gitprovider"
)

// resolvedTransport returns "git" or "rest". Empty defaults to "git".
func (f *Fetcher) resolvedTransport() string {
	return sources.ResolvedTransport(f.spec.Transport)
}

// constructCloneURL builds the https clone URL. Workspace + Repo are
// already validated by New (CR-02 / validateFlatIdentifier rejects
// URL-structural metacharacters), so direct interpolation is safe.
func (f *Fetcher) constructCloneURL() string {
	return fmt.Sprintf("https://bitbucket.org/%s/%s.git", f.spec.Workspace, f.spec.Repo)
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

	return gitprovider.FetchViaProvider(ctx, "bitbucket", cloneURL, f.spec.Ref, token, req.PriorRev)
}
