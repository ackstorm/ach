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
	"github.com/ackstorm/ach/internal/sources/gitprovider"
)

// resolvedTransport returns "git" or "rest". Empty defaults to "git".
func (f *Fetcher) resolvedTransport() string {
	return sources.ResolvedTransport(f.spec.Transport)
}

// normalizeGitLabHost strips any case-variant http:// or https://
// scheme prefix and trailing slashes. Idempotent. Called both at New
// time (so the normalized form is what cr02validate.HostIdentifier
// inspects, since CR-02 rejects '/' in flat identifiers) AND at clone-
// URL construction (defense in depth — and because the spec.Host field
// is preserved verbatim on the Fetcher).
func normalizeGitLabHost(host string) string {
	low := strings.ToLower(host)
	switch {
	case strings.HasPrefix(low, "https://"):
		host = host[len("https://"):]
	case strings.HasPrefix(low, "http://"):
		host = host[len("http://"):]
	}
	return strings.TrimRight(host, "/")
}

// constructCloneURL builds the https clone URL from spec.Host (default
// gitlab.com) and spec.Project. Extracted as a method so the unit tests
// can pin the default-host behavior without spinning a network call.
func (f *Fetcher) constructCloneURL() string {
	host := normalizeGitLabHost(f.spec.Host)
	if host == "" {
		host = "gitlab.com"
	}
	return fmt.Sprintf("https://%s/%s.git", host, f.spec.Project)
}

// restBaseURL builds the REST scheme+host from spec.Host with the same
// hardening as constructCloneURL: normalize (strip any caller-supplied
// scheme + trailing slash), default to gitlab.com, then force https://.
// This closes the REST-path SSRF where a raw "http://<internal>" host
// passed New()-time validation (which strips the scheme before checking)
// but reached WithBaseURL / archiveURL unsanitized (finding S1).
func (f *Fetcher) restBaseURL() string {
	host := normalizeGitLabHost(f.spec.Host)
	if host == "" {
		host = "gitlab.com"
	}
	return "https://" + host
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

	return gitprovider.FetchViaProvider(ctx, "gitlab", cloneURL, f.spec.Ref, token, req.PriorRev)
}
