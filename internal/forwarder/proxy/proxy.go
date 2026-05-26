// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/ackstorm/ach/internal/forwarder/headers"
	"github.com/ackstorm/ach/internal/forwarder/metrics"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// ctxKey is the unexported type used for context keys in this package
// (prevents collisions with foreign packages' context values).
type ctxKey int

const (
	// jwtCtxKey holds the per-request ACH JWT string when a BIP winner
	// opts in on /mcp/{name} or /a2a/{name}. Director reads + writes it
	// to req.Header AFTER headers.StripAndRewrite.
	jwtCtxKey ctxKey = iota + 1
)

// WithJWT stores a minted JWT in the request context for Director to
// attach. Per-route handlers call this BEFORE invoking rp.ServeHTTP.
func WithJWT(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, jwtCtxKey, token)
}

func jwtFromCtx(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(jwtCtxKey).(string)
	return token, ok && token != ""
}

// Deps wires the proxy's static configuration. One *url.URL pre-parsed
// at process start; one shared LiteLLM API key (Hub §9.1 "x-litellm-api-key");
// one structured logger.
type Deps struct {
	LiteLLMUpstream  *url.URL
	LiteLLMSharedKey string
	Logger           *slog.Logger
}

// New constructs the shared *httputil.ReverseProxy. One instance per
// process; all four routes (/v1, /gemini, /mcp, /a2a) share it.
//
// Director ordering per D-05:
//  1. Rewrite scheme + host from deps.LiteLLMUpstream.
//  2. req.URL.Path preserved verbatim (LiteLLM honors the route).
//  3. Strip + rewrite headers (Plan 04-01 — pure function).
//  4. JWT attach LAST — strip has already cleared any client Authorization.
//
// ModifyResponse is intentionally nil for streaming pass-through (D-05).
// Director does NOT touch req.Body to preserve SSE semantics.
func New(deps Deps) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = deps.LiteLLMUpstream.Scheme
			req.URL.Host = deps.LiteLLMUpstream.Host
			// Clear req.Host so Go fills the upstream Host header from
			// URL.Host — never leak client-supplied Host to LiteLLM.
			req.Host = ""

			litellmToken := ""
			if kc, ok := middleware.KeyContextFromCtx(req.Context()); ok && kc.LiteLLMToken != nil {
				litellmToken = *kc.LiteLLMToken
			}
			headers.StripAndRewrite(req.Header, deps.LiteLLMSharedKey, litellmToken)

			// JWT write LAST — strip just cleared any client Authorization.
			if token, present := jwtFromCtx(req.Context()); present {
				req.Header.Set("Authorization", "Bearer "+token)
			}
		},
		ModifyResponse: nil,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Never echo the raw err string — it may contain internal
			// hostnames or upstream metadata. Map to upstream_unreachable
			// + log the err on the server side.
			if deps.Logger != nil {
				deps.Logger.Error("forwarder upstream error",
					slog.String("path", r.URL.Path),
					slog.String("err", err.Error()),
				)
			}
			metrics.IncRequests(routeFor(r.URL.Path), keyTypeFor(r.Context()), "upstream_unreachable")
			metrics.IncLiteLLMUnreachable()
			render.Error(w, http.StatusBadGateway, "upstream_unreachable",
				"upstream connection error",
				middleware.RequestIDFromCtx(r.Context()))
		},
	}
}

// routeFor extracts the top-level route name for metrics labels.
// "/v1/chat/completions" → "/v1"; "/mcp/foo/bar" → "/mcp"; etc.
func routeFor(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1"):
		return "/v1"
	case strings.HasPrefix(path, "/gemini"):
		return "/gemini"
	case strings.HasPrefix(path, "/mcp"):
		return "/mcp"
	case strings.HasPrefix(path, "/a2a"):
		return "/a2a"
	}
	return "unknown"
}

// keyTypeFor maps a KeyContext.KeyType to the Hub §18.5 normative
// metric label-value enum (pk / ek / none).
func keyTypeFor(ctx context.Context) string {
	kc, ok := middleware.KeyContextFromCtx(ctx)
	if !ok {
		return "none"
	}
	switch kc.KeyType {
	case keys.PrefixPk:
		return "pk"
	case keys.PrefixEk:
		return "ek"
	}
	return "none"
}
