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
	"github.com/ackstorm/ach/internal/keycrypt"
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
// at process start; one structured logger. The per-request LiteLLM auth key
// (the caller's own virtual key) is sourced from the KeyContext in Director —
// TESTING-PHASE (reverts FIX01 §A.6 / D-13), the shared master key is no longer
// part of the forward path.
type Deps struct {
	LiteLLMUpstream *url.URL
	Logger          *slog.Logger

	// KeyEncryptionKey is the 32-byte AES-256 DEK (ACH_KEY_ENCRYPTION_KEY,
	// G3). The KeyContext carries the LiteLLM virtual-key material SEALED at
	// rest (keycrypt blob); Director decrypts it once per request before
	// forwarding. Required (validated at process start by dekenv.Load).
	KeyEncryptionKey []byte
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

			// TESTING-PHASE (reverts FIX01 §A.6 / D-13): forward the CALLER's
			// own LiteLLM virtual key as x-litellm-api-key (1:1 identity). The
			// master key is no longer sent; x-litellm-key-id delegation is gone.
			// G3: the KeyContext carries the material SEALED at rest — decrypt it
			// once here. A nil/empty material (or a decrypt failure) writes an
			// empty header (no fallback) — keys minted before migration 000014,
			// or under a different DEK, fail upstream by design.
			material := ""
			if kc, ok := middleware.KeyContextFromCtx(req.Context()); ok && kc.LiteLLMKeyMaterial != nil {
				pt, err := keycrypt.Open(deps.KeyEncryptionKey, *kc.LiteLLMKeyMaterial)
				if err != nil {
					// Wrong DEK / legacy plaintext row / corruption: forward no
					// key — upstream 401, by design. Never log the material.
					if deps.Logger != nil {
						deps.Logger.Warn("key material decrypt failed", slog.String("err", err.Error()))
					}
				} else {
					material = string(pt)
				}
			}
			headers.StripAndRewrite(req.Header, material)
			// MCP route only: LiteLLM's MCP key parser (user_api_key_auth_mcp.py)
			// requires a "Bearer " prefix; /v1, /gemini, /a2a take the bare value.
			if routeFor(req.URL.Path) == "/mcp" {
				// Collapse to the bare /mcp/<server> form. LiteLLM v1.87.1's MCP
				// gateway grants non-admin virtual keys ONLY on the exact
				// single-segment route (mcp_inference_routes lists
				// "/mcp/{subpath}"); a trailing slash or deeper subpath falls
				// through to the proxy-admin-only check → 500. Since we forward
				// the caller's own non-admin key (never the master), the upstream
				// path must be the single segment the gateway accepts.
				req.URL.Path = mcpServerPath(req.URL.Path)
				req.URL.RawPath = ""
				req.Header.Set("X-Litellm-Api-Key", "Bearer "+material)
			}
			// Gemini route only: LiteLLM's native Google AI Studio passthrough
			// authenticates the virtual key ONLY via the x-goog-api-key header
			// (or ?key= query param) — it does NOT read x-litellm-api-key (that
			// is the /v1 OpenAI-compat proxy). Sending x-litellm-api-key here
			// yields LiteLLM's "Virtual Key expected ... 'sk-'" 401. Move the
			// caller's key to the header the gemini gateway reads and drop the
			// ignored x-litellm-api-key so exactly one auth header is sent.
			if routeFor(req.URL.Path) == "/gemini" {
				req.Header.Del("X-Litellm-Api-Key")
				req.Header.Set("X-Goog-Api-Key", material)
			}

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

// mcpServerPath collapses any "/mcp/<server>/..." or "/mcp/<server>/" to the
// bare single-segment "/mcp/<server>" form LiteLLM's MCP gateway accepts for
// non-admin virtual keys (see Director). "/mcp" and "/mcp/" (no server) are
// returned unchanged — there is no segment to normalize.
func mcpServerPath(path string) string {
	rest := strings.TrimPrefix(path, "/mcp/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return path
	}
	return "/mcp/" + rest
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
