// SPDX-License-Identifier: Apache-2.0

// Package http is the generic HTTP/HTTPS source fetcher (Hub §10.1).
//
// Importers MUST use the alias `httpsrc` to disambiguate from the
// stdlib net/http package — see internal/sources/registry/registry.go
// for the canonical import line. Inside this file, the stdlib package
// is aliased to `nethttp` for the same reason.
//
// Behavior (Hub §10.1 conditional GET):
//
//  1. New() accepts both http:// and https:// URLs. Plaintext http:// is
//     a DEV/E2E-ONLY concession (G19 decision D): the original HTTPS-only
//     invariant (T-02-02-03) was lifted in Phase 02.1 to admit in-cluster
//     development fixture-servers. Production MUST use https:// by
//     convention — this is NOT machine-enforced here (unlike the CLI Hub
//     URL, which is hard-refused by default per G19 decision B).
//  2. Fetch issues GET against spec.URL. If req.PriorRev is non-empty,
//     it is parsed as "<etag>|<last-modified>" (fetcher-internal
//     convention) and the corresponding If-None-Match and
//     If-Modified-Since headers are attached. Servers ignoring either
//     still honor the other; both are advisory.
//  3. 304 Not Modified → FetchResult{NotModified:true, UpstreamRev:
//     req.PriorRev}; body drained+closed before return.
//  4. 200 OK → FetchResult{Body, UpstreamRev: ETag + "|" + Last-Modified};
//     body ownership transfers to caller.
//  5. 401/403 → ErrUnauthorized; 404 → ErrNotFound; 5xx → ErrUnreachable;
//     other 4xx → ErrUpstreamInvalid. Body drained+closed before return
//     on every non-200 branch.
package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// defaultTimeout bounds the entire request lifecycle. Long-running
// archive downloads inherit the caller's context.Context deadline; this
// timeout is the absolute ceiling.
const defaultTimeout = 30 * time.Second

// Fetcher implements [sources.Fetcher] for HTTPSource.
type Fetcher struct {
	spec       *achv1alpha1.HTTPSource
	httpClient *nethttp.Client
}

// New constructs an HTTP source fetcher. Returns ErrUpstreamInvalid
// wrapping a descriptive message when spec is nil. Accepts both http://
// and https:// URLs — the Phase 02 HTTPS-only invariant (T-02-02-03)
// was lifted in Phase 02.1 to support in-cluster e2e fixture-servers.
func New(spec *achv1alpha1.HTTPSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("http: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	return &Fetcher{
		spec:       spec,
		httpClient: &nethttp.Client{Timeout: defaultTimeout},
	}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	httpReq, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, f.spec.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("http: build request for %q: %v: %w",
			f.spec.URL, err, sources.ErrUpstreamInvalid)
	}

	// Attach auth header if AuthSecretRef is set.
	if f.spec.AuthSecretRef != nil {
		if req.Secret == nil {
			return nil, fmt.Errorf("http: auth secret %q is nil: %w",
				f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
		}
		value := req.Secret.Data[f.spec.AuthSecretRef.HeaderValueKey]
		if len(value) == 0 {
			return nil, fmt.Errorf("http: missing auth secret key %q: %w",
				f.spec.AuthSecretRef.HeaderValueKey, sources.ErrUnauthorized)
		}
		if f.spec.AuthSecretRef.HeaderName == "" {
			return nil, fmt.Errorf("http: spec.AuthSecretRef.HeaderName is empty: %w",
				sources.ErrUpstreamInvalid)
		}
		httpReq.Header.Set(f.spec.AuthSecretRef.HeaderName, string(value))
	}

	// Conditional-GET (Hub §10.1). req.PriorRev encodes both ETag and
	// Last-Modified separated by a literal pipe — fetcher-internal
	// convention. Split into the two components; attach whichever is
	// non-empty.
	if req.PriorRev != "" {
		etag, lastMod := splitPriorRev(req.PriorRev)
		if etag != "" {
			httpReq.Header.Set("If-None-Match", etag)
		}
		if lastMod != "" {
			httpReq.Header.Set("If-Modified-Since", lastMod)
		}
	}

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		// Network / timeout / DNS / TLS — Unreachable.
		return nil, fmt.Errorf("http: GET %q: %v: %w",
			f.spec.URL, err, sources.ErrUnreachable)
	}

	switch {
	case resp.StatusCode == nethttp.StatusNotModified:
		// 304: caller keeps prior cached file unchanged. Preserve the
		// prior UpstreamRev verbatim so callers may write it back.
		sources.DrainAndClose(resp.Body)
		return &sources.FetchResult{
			NotModified: true,
			UpstreamRev: req.PriorRev,
		}, nil

	case resp.StatusCode == nethttp.StatusOK:
		// 200: caller materializes the body. Build the new UpstreamRev
		// from ETag + Last-Modified (preserving the format the
		// conditional-GET branch parses).
		rev := buildRev(resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"))
		return &sources.FetchResult{
			Body:        resp.Body,
			UpstreamRev: rev,
			NotModified: false,
		}, nil

	default:
		// Every non-200/304 status maps via the shared ladder
		// (401/403→Unauthorized, 404→NotFound, ≥500→Unreachable, other
		// 4xx→UpstreamInvalid). The guard preserves the original default
		// arm for the pathological 2xx-other case (e.g. 206), which the
		// ladder would otherwise classify as nil.
		sources.DrainAndClose(resp.Body)
		if e := sources.ClassifyHTTPStatus("http", fmt.Sprintf("GET %q", f.spec.URL), resp.StatusCode); e != nil {
			return nil, e
		}
		return nil, fmt.Errorf("http: GET %q: %d: %w",
			f.spec.URL, resp.StatusCode, sources.ErrUpstreamInvalid)
	}
}

// splitPriorRev parses the "<etag>|<last-modified>" composite the http
// fetcher writes into UpstreamRev. Either half (or both) may be empty;
// callers attach only the non-empty halves to the conditional-GET
// headers.
func splitPriorRev(rev string) (etag, lastMod string) {
	idx := strings.Index(rev, "|")
	if idx < 0 {
		// Legacy / malformed: treat the whole string as the etag.
		return rev, ""
	}
	return rev[:idx], rev[idx+1:]
}

// buildRev composes the UpstreamRev returned to the caller from the
// response's ETag + Last-Modified headers. Either may be empty (servers
// vary); the composite is symmetric with splitPriorRev's parsing.
func buildRev(etag, lastMod string) string {
	return etag + "|" + lastMod
}

// setHTTPClientForTesting is the test-only override that injects an
// alternate http.Client (typically httptest.NewTLSServer().Client()).
// Not part of the public API — tests in the same package call it
// directly on the returned *Fetcher.
func (f *Fetcher) setHTTPClientForTesting(c *nethttp.Client) {
	f.httpClient = c
}

// Compile-time assertion that *Fetcher satisfies sources.Fetcher.
var _ sources.Fetcher = (*Fetcher)(nil)
