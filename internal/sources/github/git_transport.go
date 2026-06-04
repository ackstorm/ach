// SPDX-License-Identifier: Apache-2.0

// This file implements the git-protocol transport for the GitHub
// source fetcher (FIX_GIT.txt).
//
// The per-provider clone-URL build + spec validation stay here; the
// shared ls-remote → NotModified → clone tail lives in
// internal/sources/gitprovider.FetchViaProvider.
//
// The token (when present) reaches git via http.extraHeader; never URL.
// See internal/sources/git for the engine.

package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/gitprovider"
)

// resolvedTransport returns "git" or "rest". Empty defaults to "git"
// (matches the kubebuilder default; defends against a CR submitted
// before kube-apiserver defaulting applies — e.g. an envtest scenario
// that builds the CR struct in Go and bypasses admission).
func (f *Fetcher) resolvedTransport() string {
	return sources.ResolvedTransport(f.spec.Transport)
}

func (f *Fetcher) fetchViaGit(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	token, err := sources.ExtractBearerToken("github", f.spec.AuthSecretRef, req.Secret)
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
		cloneURL = sources.GitHubCloneURL(f.spec.Repo)
	}

	return gitprovider.FetchViaProvider(ctx, "github", cloneURL, f.spec.Ref, token, req.PriorRev)
}
