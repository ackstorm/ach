// SPDX-License-Identifier: Apache-2.0

// Package bitbucket is the Bitbucket source fetcher (Hub §10.1).
//
// Behavior (Hub §10.1):
//
//  1. Resolves the commit SHA at HEAD of spec.Ref via Bitbucket Cloud's
//     REST v2.0 `/2.0/repositories/{workspace}/{repo}/commit/{rev}`
//     endpoint (returns `{"hash":"<sha>", ...}`).
//  2. If req.PriorRev equals the resolved SHA, returns
//     FetchResult{NotModified: true} — caller skips re-publish.
//  3. Otherwise downloads the tar.gz archive via
//     `https://bitbucket.org/{workspace}/{repo}/get/{sha}.tar.gz` —
//     plan-sanctioned fallback because go-bitbucket SDK v0.9.81 does
//     not expose a tarball-download method. Bearer token is attached
//     via Authorization header on BOTH requests; never via URL query
//     string (threat T-02-02-02, T-02-02-07).
//
// v1alpha1 supports Bearer Repository Access Tokens only — Bitbucket
// username + app-password auth is deferred to v1beta1 per the
// 02-CONTEXT.md deferred-ideas note.
package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"time"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/cr02validate"
)

// defaultBitbucketAPI is the canonical SaaS API host. Bitbucket Cloud
// only — Bitbucket Server (Stash) is out of scope for v1alpha1.
const (
	defaultBitbucketAPI = "https://api.bitbucket.org"
	defaultBitbucketWeb = "https://bitbucket.org"
	defaultTimeout      = 30 * time.Second
)

// Fetcher implements [sources.Fetcher] for BitbucketSource.
type Fetcher struct {
	spec       *achv1alpha1.BitbucketSource
	httpClient *nethttp.Client

	// cloneURLForTesting overrides the upstream clone URL the
	// git-transport branch uses. Empty in production. Set by tests so
	// the git transport hits a local bare-repo fixture instead of
	// bitbucket.org.
	cloneURLForTesting string
}

// New constructs a Bitbucket source fetcher. Returns ErrUpstreamInvalid
// when spec is nil, or when spec.Workspace/Repo/Ref contains URL-
// structural metacharacters (CR-02). The v1alpha1 CRD enforces only
// MinLength=1 on these fields; this constructor rejects characters that
// could redirect the request to an attacker-controlled host or smuggle
// query parameters past the bearer-in-header discipline (T-02-02-02,
// T-02-02-07). Defense in depth: every URL segment is also url.PathEscape'd
// at Fetch time.
//
// Workspace and Repo are forbidden from containing '/', '?', '#', '\',
// or whitespace — Bitbucket workspaces and repo slugs are flat
// identifiers and never legitimately carry these characters.
//
// Ref is permitted '/' (e.g. "feature/branch" is a valid Bitbucket ref
// shape) but forbidden '?', '#', '\', and whitespace — the ref segment
// goes through url.PathEscape at Fetch time so slashes are escaped to
// %2F when interpolated into the URL.
func New(spec *achv1alpha1.BitbucketSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("bitbucket: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	if err := cr02validate.FlatIdentifier("bitbucket.workspace", spec.Workspace); err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
	}
	if err := cr02validate.FlatIdentifier("bitbucket.repo", spec.Repo); err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
	}
	if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
		return nil, fmt.Errorf("bitbucket: %w", err)
	}
	return &Fetcher{
		spec:       spec,
		httpClient: &nethttp.Client{Timeout: defaultTimeout},
	}, nil
}


// extractToken returns the bearer token to send to Bitbucket, or empty
// for anonymous fetch. Shared by both transport branches.
//
// Semantics (post Task 1 CRD shape relaxation):
//
//   - spec.AuthSecretRef == nil          → anonymous (token="").
//   - spec.AuthSecretRef != nil,
//     req.Secret == nil                  → ErrUnauthorized.
//   - key resolves from f.spec.AuthSecretRef.Key, or — when empty —
//     achv1alpha1.DefaultAuthSecretKey("bitbucket") == "BITBUCKET_TOKEN"
//     so `kubectl create secret generic foo --from-literal=BITBUCKET_TOKEN=…`
//     works zero-config.
//   - resolved key missing from Secret.Data → ErrUnauthorized with the
//     key NAME in the message (never the absent value — T-02-02-01).
func (f *Fetcher) extractToken(req sources.FetchRequest) (string, error) {
	if f.spec.AuthSecretRef == nil {
		return "", nil
	}
	if req.Secret == nil {
		return "", fmt.Errorf("bitbucket: auth secret %q is nil: %w",
			f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
	}
	key := f.spec.AuthSecretRef.Key
	if key == "" {
		key = achv1alpha1.DefaultAuthSecretKey("bitbucket")
	}
	raw := req.Secret.Data[key]
	if len(raw) == 0 {
		return "", fmt.Errorf("bitbucket: missing auth secret key %q: %w",
			key, sources.ErrUnauthorized)
	}
	return string(raw), nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
//
// Dispatches by spec.Transport (Task 1 / FIX_GIT.txt):
//   - "git"  (default) → fetchViaGit (no per-IP REST rate-limit).
//   - "rest"           → legacy two-step REST + tarball-web path below.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	if f.resolvedTransport() == "git" {
		return f.fetchViaGit(ctx, req)
	}

	// ───── legacy REST branch ─────
	token, err := f.extractToken(req)
	if err != nil {
		return nil, err
	}

	// 2. Resolve commit SHA via Bitbucket Cloud REST API.
	//    Every CR-provided segment is url.PathEscape'd (CR-02). The
	//    constructor rejected '/', '?', '#', and whitespace in
	//    Workspace/Repo and '?', '#', whitespace in Ref already; the
	//    escape here is defense-in-depth against future spec changes
	//    that legitimately allow more characters.
	commitURL := fmt.Sprintf("%s/2.0/repositories/%s/%s/commit/%s",
		defaultBitbucketAPI,
		url.PathEscape(f.spec.Workspace),
		url.PathEscape(f.spec.Repo),
		url.PathEscape(f.spec.Ref))
	sha, err := f.resolveCommitSHA(ctx, commitURL, token)
	if err != nil {
		return nil, err
	}
	if sha == "" {
		return nil, fmt.Errorf("bitbucket: empty SHA for %s/%s@%s: %w",
			f.spec.Workspace, f.spec.Repo, f.spec.Ref, sources.ErrUpstreamInvalid)
	}

	// 3. Conditional-fetch.
	if req.PriorRev != "" && req.PriorRev == sha {
		return &sources.FetchResult{
			NotModified: true,
			UpstreamRev: sha,
		}, nil
	}

	// 4. Download the tar.gz archive via the plan-sanctioned web URL
	//    (T-02-02-07: bearer attached via Authorization header).
	//    Workspace/Repo are url.PathEscape'd (CR-02 / T-02-02-02);
	//    sha is the SHA returned by the prior commit-resolution step
	//    and is therefore trusted, but escape is defensive.
	//    Path subset extraction (spec.Path) is deferred to v1beta1.
	archiveURL := fmt.Sprintf("%s/%s/%s/get/%s.tar.gz",
		defaultBitbucketWeb,
		url.PathEscape(f.spec.Workspace),
		url.PathEscape(f.spec.Repo),
		url.PathEscape(sha))
	httpReq, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, archiveURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bitbucket: build archive request: %v: %w",
			err, sources.ErrUpstreamInvalid)
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bitbucket: archive GET: %v: %w", err, sources.ErrUnreachable)
	}
	switch {
	case resp.StatusCode == nethttp.StatusOK:
		return &sources.FetchResult{
			Body:        resp.Body,
			UpstreamRev: sha,
			NotModified: false,
		}, nil
	case resp.StatusCode == nethttp.StatusUnauthorized,
		resp.StatusCode == nethttp.StatusForbidden:
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("bitbucket: archive %d on %s/%s: %w",
			resp.StatusCode, f.spec.Workspace, f.spec.Repo, sources.ErrUnauthorized)
	case resp.StatusCode == nethttp.StatusNotFound:
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("bitbucket: archive 404 on %s/%s@%s: %w",
			f.spec.Workspace, f.spec.Repo, sha, sources.ErrNotFound)
	case resp.StatusCode >= 500:
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("bitbucket: archive %d on %s/%s: %w",
			resp.StatusCode, f.spec.Workspace, f.spec.Repo, sources.ErrUnreachable)
	default:
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("bitbucket: archive %d on %s/%s: %w",
			resp.StatusCode, f.spec.Workspace, f.spec.Repo, sources.ErrUpstreamInvalid)
	}
}

// resolveCommitSHA performs the REST GET to Bitbucket Cloud's commit
// endpoint and extracts the commit hash. Maps status codes to sentinels
// via the same scheme as Fetch.
func (f *Fetcher) resolveCommitSHA(ctx context.Context, url, token string) (string, error) {
	httpReq, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("bitbucket: build commit request: %v: %w",
			err, sources.ErrUpstreamInvalid)
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("bitbucket: commit GET: %v: %w", err, sources.ErrUnreachable)
	}
	defer drainAndClose(resp.Body)

	switch {
	case resp.StatusCode == nethttp.StatusOK:
		// Parse {"hash": "<sha>", ...}.
		var body struct {
			Hash string `json:"hash"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("bitbucket: parse commit response: %v: %w",
				err, sources.ErrUpstreamInvalid)
		}
		return body.Hash, nil
	case resp.StatusCode == nethttp.StatusUnauthorized,
		resp.StatusCode == nethttp.StatusForbidden:
		return "", fmt.Errorf("bitbucket: commit %d: %w",
			resp.StatusCode, sources.ErrUnauthorized)
	case resp.StatusCode == nethttp.StatusNotFound:
		return "", fmt.Errorf("bitbucket: commit 404 on %s/%s@%s: %w",
			f.spec.Workspace, f.spec.Repo, f.spec.Ref, sources.ErrNotFound)
	case resp.StatusCode >= 500:
		return "", fmt.Errorf("bitbucket: commit %d: %w",
			resp.StatusCode, sources.ErrUnreachable)
	default:
		return "", fmt.Errorf("bitbucket: commit %d: %w",
			resp.StatusCode, sources.ErrUpstreamInvalid)
	}
}

// drainAndClose is the REL-04 helper.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

// setHTTPClientForTesting is the test-only override.
func (f *Fetcher) setHTTPClientForTesting(c *nethttp.Client) {
	f.httpClient = c
}

// Compile-time assertion that *Fetcher satisfies sources.Fetcher.
var _ sources.Fetcher = (*Fetcher)(nil)
