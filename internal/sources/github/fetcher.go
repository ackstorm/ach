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
// Path subset extraction honors spec.Path (F1): a directory path narrows to
// that subtree's contents (re-rooted), a single-file path returns that file's
// raw bytes. Narrowing happens on-disk for the git transport (git.tarSubtree)
// and via sources.NarrowArchiveSubtree for the legacy REST transport. Empty
// path → full repo tarball. (PluginMarketplace strips path before fetch — its
// marketplace.json discovery walks the whole repo.)
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
	"bytes"
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	neturl "net/url"
	"strings"

	gogithub "github.com/google/go-github/v62/github"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/cr02validate"
)

// Fetcher implements [sources.Fetcher] for GitHubSource.
type Fetcher struct {
	spec *achv1alpha1.GitHubSource

	// httpClient is the transport for the second leg of the legacy REST
	// fetch path (tarball stream). Defaults to nethttp.DefaultClient;
	// tests inject a custom client via setHTTPClientForTesting.
	httpClient *nethttp.Client

	// cloneURLForTesting overrides the upstream clone URL the
	// git-transport branch uses. Empty in production. Set by tests so
	// the git transport hits a local bare-repo fixture instead of
	// github.com.
	cloneURLForTesting string

	// apiBaseURLForTesting overrides the go-github REST client BaseURL.
	// Empty in production (defaults to api.github.com). Set by tests so
	// the legacy REST branch hits a local httptest server instead of the
	// real api.github.com — keeping unit tests offline + deterministic
	// (mirrors cloneURLForTesting for the git branch).
	apiBaseURLForTesting string
}

// New constructs a GitHub source fetcher. Returns ErrUpstreamInvalid
// wrapping a descriptive message when spec is nil — the registry
// validates spec non-nil before reaching here, so this is defense in
// depth.
func New(spec *achv1alpha1.GitHubSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("github: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	// CR-02 metachar parity with bitbucket: reject URL-structural
	// metacharacters in spec.Repo / spec.Ref at construction time so a
	// crafted CR cannot smuggle them into the constructed clone URL or
	// the git subprocess argv. github repos are exactly <owner>/<name>
	// — allowMultiSegment=false.
	if err := cr02validate.RepoSlashIdentifier("github.repo", spec.Repo, false); err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	return &Fetcher{
		spec:       spec,
		httpClient: nethttp.DefaultClient,
	}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
//
// Dispatches by spec.Transport (Task 1 / FIX_GIT.txt):
//   - "git"  (default) → fetchViaGit (no per-IP REST rate-limit).
//   - "rest"           → legacy go-github path below (escape hatch;
//     will be removed one release after the git
//     transport is observed clean).
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	if f.resolvedTransport() == "git" {
		return f.fetchViaGit(ctx, req)
	}

	// ───── legacy REST branch ─────
	token, err := sources.ExtractBearerToken("github", f.spec.AuthSecretRef, req.Secret)
	if err != nil {
		return nil, err
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
	// Test-only: redirect the REST client at a local httptest server.
	if f.apiBaseURLForTesting != "" {
		base, perr := neturl.Parse(f.apiBaseURLForTesting)
		if perr != nil {
			return nil, fmt.Errorf("github: parse test base URL %q: %w", f.apiBaseURLForTesting, sources.ErrUpstreamInvalid)
		}
		client.BaseURL = base
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
	//    The full repo archive streams below; spec.Path narrowing (when set)
	//    is applied to the StatusOK body via sources.NarrowArchiveSubtree (F1).
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
		// spec.Path narrowing: the git transport narrows on-disk, but the REST
		// archive is the whole repo wrapped under "<repo>-<sha>/", so narrow it
		// here to the same shape (rooted at the subtree's contents) when a path
		// is set — keeping the fetcher's output contract transport-agnostic (F1).
		if f.spec.Path != "" {
			defer sources.DrainAndClose(tarballResp.Body)
			narrowed, nerr := sources.NarrowArchiveSubtree(tarballResp.Body, f.spec.Path, sources.DefaultArchiveIngressCap)
			if nerr != nil {
				return nil, fmt.Errorf("github: narrow path %q: %w", f.spec.Path, nerr)
			}
			return &sources.FetchResult{Body: io.NopCloser(bytes.NewReader(narrowed)), UpstreamRev: sha}, nil
		}
		// Body ownership transfers to caller.
		return &sources.FetchResult{
			Body:        tarballResp.Body,
			UpstreamRev: sha,
			NotModified: false,
		}, nil
	default:
		// Shared ladder for every non-200 tarball status; guard preserves
		// the original default arm for the pathological 2xx-other case.
		sources.DrainAndClose(tarballResp.Body)
		if e := sources.ClassifyHTTPStatus("github", "tarball on "+f.spec.Repo, tarballResp.StatusCode); e != nil {
			return nil, e
		}
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
	if classified := sources.ClassifyHTTPStatus("github", op, resp.StatusCode); classified != nil {
		return classified
	}
	// 2xx-with-err edge: ClassifyHTTPStatus returns nil for 2xx, but an
	// SDK error paired with a 2xx still means the response was unusable —
	// preserve the original default-arm UpstreamInvalid mapping.
	return fmt.Errorf("github: %s %d: %w", op, resp.StatusCode, sources.ErrUpstreamInvalid)
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
