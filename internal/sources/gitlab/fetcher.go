// SPDX-License-Identifier: Apache-2.0

// Package gitlab is the GitLab source fetcher (Hub §10.1).
//
// Behavior (Hub §10.1):
//
//  1. Resolves the commit SHA at HEAD of spec.Ref via go-gitlab's
//     Commits.GetCommit.
//  2. If req.PriorRev equals the resolved SHA, returns
//     FetchResult{NotModified: true} — caller skips re-publish.
//  3. Otherwise downloads the tar.gz archive via a direct authenticated
//     HTTPS GET against /api/v4/projects/<id>/repository/archive.tar.gz
//     (PRIVATE-TOKEN header) and returns it as a streaming body. The
//     SDK's Repositories.Archive is NOT used here because it buffers
//     the entire archive into operator memory before returning, which
//     defeats the SizeCapBytes LimitReader the caller applies in
//     materializeExternalRef (CR-01 / Hub §10.3).
//
// Module note: the historical `github.com/xanzy/go-gitlab` module was
// renamed to `gitlab.com/gitlab-org/api/client-go` in 2025. Plan 02-02
// Task 1 explicitly anticipates the redirect and instructs us to adopt
// the new path. v0.130.1 is the highest version compatible with the
// project's Go 1.23 baseline (v1.46+ require Go 1.24).
package gitlab

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// defaultGitLabHost is the canonical SaaS host when spec.Host is empty.
const defaultGitLabHost = "https://gitlab.com"

// archiveHTTPTimeout bounds the archive-download HTTP request itself.
// The full archive may be large; the SizeCapBytes LimitReader bounds
// post-fetch memory in the caller (external_ref_refresh.materializeExternalRef),
// while this timeout bounds stall behavior on the wire.
const archiveHTTPTimeout = 5 * time.Minute

// Fetcher implements [sources.Fetcher] for GitLabSource.
type Fetcher struct {
	spec       *achv1alpha1.GitLabSource
	httpClient *nethttp.Client
}

// New constructs a GitLab source fetcher. Returns ErrUpstreamInvalid
// when spec is nil.
func New(spec *achv1alpha1.GitLabSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("gitlab: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	return &Fetcher{
		spec:       spec,
		httpClient: &nethttp.Client{Timeout: archiveHTTPTimeout},
	}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	// 1. Extract PAT from Secret if AuthSecretRef is set. Phase 02.1
	//    permits anonymous mode (spec.AuthSecretRef == nil) — go-gitlab
	//    is constructed with an empty token and the PRIVATE-TOKEN header
	//    is omitted on the raw archive download. Public projects on
	//    gitlab.com SaaS respond to anonymous callers; private projects
	//    return 401/404, which surfaces as ErrUnauthorized/ErrNotFound
	//    through the classifier.
	var token string
	if f.spec.AuthSecretRef != nil {
		if req.Secret == nil {
			return nil, fmt.Errorf("gitlab: auth secret %q is nil: %w",
				f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
		}
		raw := req.Secret.Data[f.spec.AuthSecretRef.Key]
		if len(raw) == 0 {
			return nil, fmt.Errorf("gitlab: missing auth secret key %q: %w",
				f.spec.AuthSecretRef.Key, sources.ErrUnauthorized)
		}
		token = string(raw)
	}

	// 2. Resolve host (default to gitlab.com SaaS when spec.Host is empty).
	host := f.spec.Host
	if host == "" {
		host = defaultGitLabHost
	}

	// 3. Construct go-gitlab client. Token (when set) is attached to every
	//    request via Authorization header; NEVER via URL query string
	//    (threat T-02-02-02). With empty token the SDK still constructs;
	//    requests go out without auth headers.
	client, err := gogitlab.NewClient(token,
		gogitlab.WithBaseURL(strings.TrimRight(host, "/")+"/api/v4"))
	if err != nil {
		return nil, fmt.Errorf("gitlab: NewClient: %v: %w", err, sources.ErrUpstreamInvalid)
	}

	// 4. Resolve commit SHA at HEAD of spec.Ref.
	commit, resp, err := client.Commits.GetCommit(f.spec.Project, f.spec.Ref, nil,
		gogitlab.WithContext(ctx))
	if err != nil {
		return nil, classifyGitLabErr(err, resp, "GetCommit")
	}
	if commit == nil || commit.ID == "" {
		return nil, fmt.Errorf("gitlab: GetCommit returned empty SHA for %s@%s: %w",
			f.spec.Project, f.spec.Ref, sources.ErrUpstreamInvalid)
	}
	sha := commit.ID

	// 5. Conditional-fetch.
	if req.PriorRev != "" && req.PriorRev == sha {
		return &sources.FetchResult{
			NotModified: true,
			UpstreamRev: sha,
		}, nil
	}

	// 6. Fetch archive via raw HTTP GET so the response body streams.
	//    The go-gitlab SDK's Repositories.Archive buffers the entire
	//    archive into operator memory before returning, which defeats
	//    the caller's io.LimitReader-based size cap (CR-01 / Hub §10.3,
	//    CLAUDE.md "Content Service streams via sendfile(2) and never
	//    buffers a full body" — the same discipline applies operator-
	//    side because the operator is the load-bearing single-replica
	//    process). PRIVATE-TOKEN goes in the header (never the URL —
	//    T-02-02-02). Path subset extraction (spec.Path) is deferred to
	//    v1beta1.
	archiveURL := fmt.Sprintf("%s/api/v4/projects/%s/repository/archive.tar.gz?sha=%s",
		strings.TrimRight(host, "/"),
		url.PathEscape(f.spec.Project),
		url.QueryEscape(sha),
	)
	httpReq, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, archiveURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gitlab: build archive request: %v: %w",
			err, sources.ErrUpstreamInvalid)
	}
	if token != "" {
		httpReq.Header.Set("PRIVATE-TOKEN", token)
	}
	resp2, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gitlab: archive GET: %v: %w", err, sources.ErrUnreachable)
	}
	switch {
	case resp2.StatusCode == nethttp.StatusOK:
		return &sources.FetchResult{
			Body:        resp2.Body,
			UpstreamRev: sha,
			NotModified: false,
		}, nil
	case resp2.StatusCode == nethttp.StatusUnauthorized,
		resp2.StatusCode == nethttp.StatusForbidden:
		drainAndClose(resp2.Body)
		return nil, fmt.Errorf("gitlab: archive %d on %s: %w",
			resp2.StatusCode, f.spec.Project, sources.ErrUnauthorized)
	case resp2.StatusCode == nethttp.StatusNotFound:
		drainAndClose(resp2.Body)
		return nil, fmt.Errorf("gitlab: archive 404 on %s@%s: %w",
			f.spec.Project, sha, sources.ErrNotFound)
	case resp2.StatusCode >= 500:
		drainAndClose(resp2.Body)
		return nil, fmt.Errorf("gitlab: archive %d on %s: %w",
			resp2.StatusCode, f.spec.Project, sources.ErrUnreachable)
	default:
		drainAndClose(resp2.Body)
		return nil, fmt.Errorf("gitlab: archive %d on %s: %w",
			resp2.StatusCode, f.spec.Project, sources.ErrUpstreamInvalid)
	}
}

// drainAndClose is the REL-04 helper — guarantees the OS-level connection
// is released to the http.Transport's pool after a non-2xx response. Used
// only on error paths; success paths hand the unread Body to the caller.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

// classifyGitLabErr maps a go-gitlab SDK error + response into one of
// the [sources] sentinel errors.
func classifyGitLabErr(err error, resp *gogitlab.Response, op string) error {
	if resp == nil || resp.Response == nil {
		return fmt.Errorf("gitlab: %s: %v: %w", op, err, sources.ErrUnreachable)
	}
	switch {
	case resp.StatusCode == nethttp.StatusUnauthorized,
		resp.StatusCode == nethttp.StatusForbidden:
		return fmt.Errorf("gitlab: %s %d: %w", op, resp.StatusCode, sources.ErrUnauthorized)
	case resp.StatusCode == nethttp.StatusNotFound:
		return fmt.Errorf("gitlab: %s 404: %w", op, sources.ErrNotFound)
	case resp.StatusCode >= 500:
		return fmt.Errorf("gitlab: %s %d: %w", op, resp.StatusCode, sources.ErrUnreachable)
	default:
		return fmt.Errorf("gitlab: %s %d: %w", op, resp.StatusCode, sources.ErrUpstreamInvalid)
	}
}

// Compile-time assertion that *Fetcher satisfies sources.Fetcher.
var _ sources.Fetcher = (*Fetcher)(nil)
