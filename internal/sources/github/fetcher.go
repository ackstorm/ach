// SPDX-License-Identifier: Apache-2.0

// Package github is the GitHub source fetcher (Hub §10.1). It is the
// canonical reference shape that the other five per-source-type
// subpackages mirror.
//
// Behavior (Hub §10.1):
//
//  1. Resolves the commit SHA at HEAD of spec.Ref via go-github's
//     Repositories.GetCommit.
//  2. If req.PriorRev equals the resolved SHA, returns
//     FetchResult{NotModified: true} — caller skips re-publish.
//  3. Otherwise fetches the repo tarball via Repositories.GetArchiveLink
//     + a direct HTTP GET (authenticated with the same PAT) and returns
//     the streaming response Body for the reconciler to materialize
//     under .tmp/<random> / fsync / rename(2) per §10.3.
//
// Path subset extraction (the spec.Path subdirectory) is deferred to
// v1beta1 — v1alpha1 ships the full repo tarball; Plan 02-05's Plugin
// reconciler writes the archive verbatim as plugin/<crname>.tar.gz.
//
// Auth: optional. When spec.AuthSecretRef is non-nil, its Name resolves
// to a Kubernetes Secret in the CR's namespace and the PAT lives under
// req.Secret.Data[spec.AuthSecretRef.Key]; missing Secret / missing key
// surfaces as wrapped sources.ErrUnauthorized. When spec.AuthSecretRef
// is nil, the fetcher issues anonymous GitHub API calls (Phase 02.1) —
// the upstream returns commit metadata + archive bytes for public
// repositories at the 60 req/hour-per-IP anonymous rate-limit ceiling.
package github

import (
	"context"
	"fmt"
	nethttp "net/http"
	"strings"

	gogithub "github.com/google/go-github/v62/github"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// Fetcher implements [sources.Fetcher] for GitHubSource.
type Fetcher struct {
	spec *achv1alpha1.GitHubSource

	// httpClient is the transport for the second leg of the fetch
	// (tarball stream). Defaults to nethttp.DefaultClient; tests inject
	// a custom client via setHTTPClientForTesting.
	httpClient *nethttp.Client
}

// New constructs a GitHub source fetcher. Returns ErrUpstreamInvalid
// wrapping a descriptive message when spec is nil — the registry
// validates spec non-nil before reaching here, so this is defense in
// depth.
func New(spec *achv1alpha1.GitHubSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("github: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	return &Fetcher{
		spec:       spec,
		httpClient: nethttp.DefaultClient,
	}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	// 1. Extract PAT from the Secret if AuthSecretRef is set. When
	//    spec.AuthSecretRef is nil → anonymous fetch (Phase 02.1).
	//    When AuthSecretRef is set but the Secret/key is missing →
	//    ErrUnauthorized (the operator declared intent for auth and we
	//    must not silently fall back to anonymous).
	var token string
	if f.spec.AuthSecretRef != nil {
		if req.Secret == nil {
			return nil, fmt.Errorf("github: auth secret %q is nil: %w",
				f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
		}
		raw := req.Secret.Data[f.spec.AuthSecretRef.Key]
		if len(raw) == 0 {
			// Log the KEY NAME (operator-readable) but never the (absent)
			// value — threat T-02-02-01.
			return nil, fmt.Errorf("github: missing auth secret key %q: %w",
				f.spec.AuthSecretRef.Key, sources.ErrUnauthorized)
		}
		token = string(raw)
	}

	// 2. Parse repo into owner/name.
	parts := strings.SplitN(f.spec.Repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("github: spec.repo must be <owner>/<name>, got %q: %w",
			f.spec.Repo, sources.ErrUpstreamInvalid)
	}
	owner, repo := parts[0], parts[1]

	// 3. Construct a go-github client. With a PAT, WithAuthToken attaches
	//    the Authorization header (never via URL query string — threat
	//    T-02-02-02). Without a PAT, the client makes anonymous calls
	//    bounded by the upstream's 60 req/hour-per-IP rate limit.
	client := gogithub.NewClient(nil)
	if token != "" {
		client = client.WithAuthToken(token)
	}

	// 4. Resolve the commit SHA at HEAD of spec.Ref.
	commit, resp, err := client.Repositories.GetCommit(ctx, owner, repo, f.spec.Ref, nil)
	if err != nil {
		return nil, classifyGitHubErr(err, resp, "GetCommit")
	}
	sha := commit.GetSHA()
	if sha == "" {
		return nil, fmt.Errorf("github: GetCommit returned empty SHA for %s@%s: %w",
			f.spec.Repo, f.spec.Ref, sources.ErrUpstreamInvalid)
	}

	// 5. Conditional-fetch: if the prior rev matches the resolved SHA,
	//    skip the tarball fetch entirely.
	if req.PriorRev != "" && req.PriorRev == sha {
		return &sources.FetchResult{
			NotModified: true,
			UpstreamRev: sha,
		}, nil
	}

	// 6. Resolve the tarball download URL via go-github. The third
	//    argument (`5`) is the redirect-follow depth go-github accepts.
	//    Path subset extraction is deferred (v1beta1); v1alpha1 ships
	//    the full repo tarball.
	archiveURL, resp, err := client.Repositories.GetArchiveLink(
		ctx, owner, repo, gogithub.Tarball,
		&gogithub.RepositoryContentGetOptions{Ref: sha},
		5,
	)
	if err != nil {
		return nil, classifyGitHubErr(err, resp, "GetArchiveLink")
	}
	if archiveURL == nil {
		return nil, fmt.Errorf("github: GetArchiveLink returned nil URL for %s@%s: %w",
			f.spec.Repo, sha, sources.ErrUpstreamInvalid)
	}

	// 7. Issue the tarball stream request. go-github's GetArchiveLink
	//    returns a redirect-resolved URL; we attach the PAT via
	//    Authorization header when present so any private-repo redirect
	//    destination that requires auth succeeds. Anonymous mode (no
	//    token) omits the header and relies on the upstream serving
	//    public-repo tarballs unauthenticated.
	httpReq, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, archiveURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("github: build tarball request: %w", sources.ErrUpstreamInvalid)
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "token "+token)
	}

	tarballResp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("github: tarball fetch: %v: %w", err, sources.ErrUnreachable)
	}

	switch {
	case tarballResp.StatusCode == nethttp.StatusOK:
		// Body ownership transfers to caller.
		return &sources.FetchResult{
			Body:        tarballResp.Body,
			UpstreamRev: sha,
			NotModified: false,
		}, nil
	case tarballResp.StatusCode == nethttp.StatusUnauthorized,
		tarballResp.StatusCode == nethttp.StatusForbidden:
		drainAndClose(tarballResp.Body)
		return nil, fmt.Errorf("github: tarball %d on %s: %w",
			tarballResp.StatusCode, f.spec.Repo, sources.ErrUnauthorized)
	case tarballResp.StatusCode == nethttp.StatusNotFound:
		drainAndClose(tarballResp.Body)
		return nil, fmt.Errorf("github: tarball 404 on %s: %w", f.spec.Repo, sources.ErrNotFound)
	case tarballResp.StatusCode >= 500:
		drainAndClose(tarballResp.Body)
		return nil, fmt.Errorf("github: tarball %d on %s: %w",
			tarballResp.StatusCode, f.spec.Repo, sources.ErrUnreachable)
	default:
		drainAndClose(tarballResp.Body)
		return nil, fmt.Errorf("github: tarball %d on %s: %w",
			tarballResp.StatusCode, f.spec.Repo, sources.ErrUpstreamInvalid)
	}
}

// classifyGitHubErr maps a go-github SDK error + response into one of
// the [sources] sentinel errors. Inspect the HTTP status code carried
// on resp (go-github returns *github.Response which embeds *http.Response).
// Network errors (resp == nil) classify as Unreachable.
func classifyGitHubErr(err error, resp *gogithub.Response, op string) error {
	if resp == nil || resp.Response == nil {
		return fmt.Errorf("github: %s: %v: %w", op, err, sources.ErrUnreachable)
	}
	switch {
	case resp.StatusCode == nethttp.StatusUnauthorized,
		resp.StatusCode == nethttp.StatusForbidden:
		return fmt.Errorf("github: %s %d: %w", op, resp.StatusCode, sources.ErrUnauthorized)
	case resp.StatusCode == nethttp.StatusNotFound:
		return fmt.Errorf("github: %s 404: %w", op, sources.ErrNotFound)
	case resp.StatusCode >= 500:
		return fmt.Errorf("github: %s %d: %w", op, resp.StatusCode, sources.ErrUnreachable)
	default:
		return fmt.Errorf("github: %s %d: %w", op, resp.StatusCode, sources.ErrUpstreamInvalid)
	}
}

// setHTTPClientForTesting is the test-only override that injects an
// alternate http.Client (typically httptest.Server.Client()). NOT part
// of the public API — tests in the same package set it directly on the
// returned *Fetcher.
func (f *Fetcher) setHTTPClientForTesting(c *nethttp.Client) {
	f.httpClient = c
}

// Compile-time assertion that *Fetcher satisfies sources.Fetcher.
var _ sources.Fetcher = (*Fetcher)(nil)
