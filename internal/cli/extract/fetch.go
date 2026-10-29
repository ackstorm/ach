// SPDX-License-Identifier: Apache-2.0

package extract

import (
	"context"
	"net/http"
	"net/url"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// contentPath builds the escaped Content Service path. Both segments are
// PathEscaped (defense-in-depth: the server already escapes, but a name
// carrying URL metacharacters must never bleed into the query/path here —
// finding S2).
func contentPath(kind ResourceKind, name string) string {
	return "/content/" + url.PathEscape(string(kind)) + "/" + url.PathEscape(name)
}

// FetchContent issues an UNCONDITIONAL GET against the Content Service
// for the supplied (kind, name) and returns the live *http.Response
// with body unread.
//
// D-15 / STATE-11 invariant — the fetch is ALWAYS performed; the
// orchestrator (07-W1-06 step 7) MUST NOT gate this call on prior
// state. The disk-write short-circuit that compares sha256(freshly
// downloaded) vs sha256(existing-on-disk) lives entirely in
// StageAndPublish, not here. No If-None-Match, no If-Modified-Since,
// no Range — Phase 7 takes the simple unconditional path per spec
// §15.6 (the response is a full body, Content-Length-known, no
// resumable / conditional behavior).
//
// Headers — credentialing is handled by httpclient.Client
// (`x-ach-key`); for pk_ requests the caller layer (07-W3-05 cobra
// wiring) sets client.ExtraHeaders["x-ach-environment"] per CLI-03 so
// the Content Service can resolve the right Environment scope.
// FetchContent itself is credential-agnostic — it just composes the
// path.
//
// Body lifecycle — the caller owns resp.Body and MUST Close it
// (typically after StageAndPublish has streamed/spilled it).
// Non-2xx responses are converted to *httpclient.ServerError by
// DoRaw; resp is nil in that case.
//
// References:
//
//   - CLI spec §15.6 (Content Service GET /content/{kind}/{name})
//   - CLI PRD D-15 (unconditional fetch + disk-write short-circuit lives downstream)
//   - STATE-11 (fetch-unconditional invariant)
//   - 07-PATTERNS.md "httpclient.Client.DoRaw for content download"
func FetchContent(
	ctx context.Context,
	client *httpclient.Client,
	kind ResourceKind,
	name string,
) (*http.Response, error) {
	return client.DoRaw(ctx, http.MethodGet, contentPath(kind, name), nil)
}
