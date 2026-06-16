// SPDX-License-Identifier: Apache-2.0

// context.go carries the G20 request-scoped forensics/governance metadata
// (source IP, user-agent, route, environment) on context.Context so EmitAudit
// can attach it to EVERY audit record without each of the ~45 handler call
// sites threading the values by hand. Middleware (which already imports this
// package) populates the metadata once per request via WithRequestMeta; a
// handler that knows a more specific value (e.g. hydrate's requested
// environment) still sets the typed Event field, which takes precedence.

package audit

import (
	"context"

	"github.com/go-chi/chi/v5"
)

// RequestMeta is the request-scoped metadata EmitAudit falls back to when the
// corresponding Event field is empty. All fields are optional.
type RequestMeta struct {
	SourceIP    string // client RemoteAddr, preferring the first X-Forwarded-For hop
	UserAgent   string // User-Agent header
	Route       string // chi route pattern (e.g. "/platform/hydrate")
	Environment string // the key-bound Environment resolved at Authn time
}

// requestMetaKey is the private context key for RequestMeta. Private so
// external packages cannot inject or read it (Go context-key convention).
type requestMetaKey struct{}

// WithRequestMeta merges meta onto any RequestMeta already on ctx: each
// non-empty field in meta overwrites the existing value, empty fields leave
// the prior value intact. This lets the outermost middleware set IP/UA and a
// later middleware (Authn) add Environment without clobbering.
func WithRequestMeta(ctx context.Context, meta RequestMeta) context.Context {
	cur := requestMetaFromCtx(ctx)
	if meta.SourceIP != "" {
		cur.SourceIP = meta.SourceIP
	}
	if meta.UserAgent != "" {
		cur.UserAgent = meta.UserAgent
	}
	if meta.Route != "" {
		cur.Route = meta.Route
	}
	if meta.Environment != "" {
		cur.Environment = meta.Environment
	}
	return context.WithValue(ctx, requestMetaKey{}, cur)
}

// requestMetaFromCtx returns the RequestMeta stored on ctx, or a zero value.
func requestMetaFromCtx(ctx context.Context) RequestMeta {
	v, _ := ctx.Value(requestMetaKey{}).(RequestMeta)
	return v
}

// routePatternFromCtx returns the chi route pattern matched for the request,
// or "" when ctx is not a chi request (content-service direct handlers,
// unit tests). Read at emit time — by then chi has fully populated the
// RouteContext, so the pattern is complete.
func routePatternFromCtx(ctx context.Context) string {
	if rc := chi.RouteContext(ctx); rc != nil {
		return rc.RoutePattern()
	}
	return ""
}
