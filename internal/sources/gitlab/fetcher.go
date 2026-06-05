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
	"bytes"
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
	"github.com/ackstorm/ach/internal/sources/cr02validate"
)

// archiveHTTPTimeout bounds the archive-download HTTP request itself.
// The full archive may be large; the SizeCapBytes LimitReader bounds
// post-fetch memory in the caller (external_ref_refresh.materializeExternalRef),
// while this timeout bounds stall behavior on the wire.
const archiveHTTPTimeout = 5 * time.Minute

// Fetcher implements [sources.Fetcher] for GitLabSource.
type Fetcher struct {
	spec       *achv1alpha1.GitLabSource
	httpClient *nethttp.Client

	// cloneURLForTesting overrides the upstream clone URL the
	// git-transport branch uses. Empty in production. Set by tests so
	// the git transport hits a local bare-repo fixture instead of
	// gitlab.com.
	cloneURLForTesting string
}

// New constructs a GitLab source fetcher. Returns ErrUpstreamInvalid
// when spec is nil.
func New(spec *achv1alpha1.GitLabSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("gitlab: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	// CR-02 metachar parity with bitbucket: reject URL-structural
	// metacharacters in spec.Host / spec.Project / spec.Ref at
	// construction time so a crafted CR cannot smuggle them into the
	// constructed clone URL or the git subprocess argv. gitlab
	// projects can be deeply nested — allowMultiSegment=true.
	//
	// spec.Host is normalized (case-insensitive scheme strip +
	// trailing-slash strip) BEFORE validation so a legitimate
	// `https://gitlab.example.com` doesn't fail the HostIdentifier
	// '/'-forbidden rule.
	if err := cr02validate.HostIdentifier(normalizeGitLabHost(spec.Host)); err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
	}
	if err := cr02validate.RepoSlashIdentifier("gitlab.project", spec.Project, true); err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
	}
	if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
	}
	return &Fetcher{
		spec:       spec,
		httpClient: &nethttp.Client{Timeout: archiveHTTPTimeout},
	}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
//
// Dispatches by spec.Transport (Task 1 / FIX_GIT.txt):
//   - "git"  (default) → fetchViaGit (no per-IP REST rate-limit).
//   - "rest"           → legacy go-gitlab path below.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	if f.resolvedTransport() == "git" {
		return f.fetchViaGit(ctx, req)
	}

	// ───── legacy REST branch ─────
	token, err := sources.ExtractBearerToken("gitlab", f.spec.AuthSecretRef, req.Secret)
	if err != nil {
		return nil, err
	}

	// 2. Resolve host with scheme hardening (S1): force https against the
	//    normalized host so a crafted spec.Host cannot redirect REST/archive
	//    traffic to an attacker scheme/host (e.g. http://169.254.169.254).
	host := f.restBaseURL()

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
	//    T-02-02-02). spec.Path narrowing (when set) is applied to the
	//    StatusOK body via sources.NarrowArchiveSubtree (F1).
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
		// spec.Path narrowing — see github/fetcher.go (F1). The git transport
		// narrows on-disk; the REST archive is the whole repo wrapped under
		// "<repo>-<sha>/", so narrow it here to the same shape when set.
		if f.spec.Path != "" {
			defer sources.DrainAndClose(resp2.Body)
			narrowed, nerr := sources.NarrowArchiveSubtree(resp2.Body, f.spec.Path, sources.DefaultArchiveIngressCap)
			if nerr != nil {
				return nil, fmt.Errorf("gitlab: narrow path %q: %w", f.spec.Path, nerr)
			}
			return &sources.FetchResult{Body: io.NopCloser(bytes.NewReader(narrowed)), UpstreamRev: sha}, nil
		}
		return &sources.FetchResult{
			Body:        resp2.Body,
			UpstreamRev: sha,
			NotModified: false,
		}, nil
	default:
		// Shared ladder for every non-200 archive status; guard preserves
		// the original default arm for the pathological 2xx-other case.
		sources.DrainAndClose(resp2.Body)
		if e := sources.ClassifyHTTPStatus("gitlab", "archive on "+f.spec.Project, resp2.StatusCode); e != nil {
			return nil, e
		}
		return nil, fmt.Errorf("gitlab: archive %d on %s: %w",
			resp2.StatusCode, f.spec.Project, sources.ErrUpstreamInvalid)
	}
}

// classifyGitLabErr maps a go-gitlab SDK error + response into one of
// the [sources] sentinel errors.
func classifyGitLabErr(err error, resp *gogitlab.Response, op string) error {
	if resp == nil || resp.Response == nil {
		return fmt.Errorf("gitlab: %s: %v: %w", op, err, sources.ErrUnreachable)
	}
	if classified := sources.ClassifyHTTPStatus("gitlab", op, resp.StatusCode); classified != nil {
		return classified
	}
	// 2xx-with-err edge: preserve the original default-arm mapping.
	return fmt.Errorf("gitlab: %s %d: %w", op, resp.StatusCode, sources.ErrUpstreamInvalid)
}

// Compile-time assertion that *Fetcher satisfies sources.Fetcher.
var _ sources.Fetcher = (*Fetcher)(nil)
