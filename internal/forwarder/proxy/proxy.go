// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

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
	// to req.Header AFTER stripAndRewrite.
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

	// BaseURL is ACH's own public base URL (ACH_BASE_URL) — the same value
	// used as the JWT "iss" claim. ModifyResponse stamps its scheme+authority
	// onto the resource_metadata pointer in outbound 401/403 auth challenges
	// (issue #177, see challenge.go). Empty or unparseable disables the
	// rewrite; it is NOT required for any other part of the proxy.
	BaseURL string

	// GeminiStripModelPrefixes are vendor prefixes removed from the model
	// name on the /gemini route only (chart forwarder.gemini.
	// stripModelPrefixes -> ACH_GEMINI_STRIP_MODEL_PREFIXES), in declared
	// order, first match wins. It exists because LiteLLM registers a model
	// as "<vendor>.<model>" while a native Gemini client addresses it by
	// its bare Google name; the caller may send either.
	//
	// /v1 deliberately has no equivalent: there the model travels in the
	// JSON body, and the Director never touches req.Body (D-05 streaming).
	// Use LiteLLM's own model_group_alias for that surface.
	GeminiStripModelPrefixes []string
}

// New constructs the shared *httputil.ReverseProxy. One instance per
// process; all routes (/v1, /gemini, /mcp, /a2a) share it.
//
// Director ordering per D-05:
//  1. Rewrite scheme + host from deps.LiteLLMUpstream.
//  2. req.URL.Path preserved verbatim (LiteLLM honors the route).
//  3. Strip + rewrite headers (Plan 04-01 — pure function).
//  4. JWT attach LAST — strip has already cleared any client Authorization.
//
// ModifyResponse is HEADER-ONLY (issue #177): it rewrites the
// resource_metadata pointer in a 401/403 WWW-Authenticate challenge so it names
// ACH rather than LiteLLM's own front door. It runs BEFORE the body is copied
// and buffers nothing, so streaming pass-through (D-05) is unaffected — the
// constraint that comment always encoded is that the BODY is never touched, and
// it still is not. Director likewise does NOT touch req.Body, preserving SSE
// semantics. With an empty/unparseable BaseURL the hook is nil, exactly as before.
func New(deps Deps) *httputil.ReverseProxy {
	publicBase := parsePublicBase(deps.BaseURL)
	var modifyResponse func(*http.Response) error
	if publicBase != nil {
		modifyResponse = func(resp *http.Response) error {
			rewriteChallengeHost(resp, publicBase)
			return nil
		}
	}
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = deps.LiteLLMUpstream.Scheme
			req.URL.Host = deps.LiteLLMUpstream.Host
			// req.Host stays what the client dialled: LiteLLM composes its
			// redirects (/ → /ui/) from Host and ignores X-Forwarded-Host.

			// TESTING-PHASE (reverts FIX01 §A.6 / D-13): forward the CALLER's
			// own LiteLLM virtual key as x-litellm-api-key (1:1 identity). The
			// master key is no longer sent; x-litellm-key-id delegation is gone.
			// G3: the KeyContext carries the material SEALED at rest — decrypt it
			// once here. A nil/empty material (or a decrypt failure) writes an
			// empty header (no fallback) — keys minted before migration 000014,
			// or under a different DEK, fail upstream by design.
			material := ""
			if raw, ok := middleware.RawLiteLLMKeyFromCtx(req.Context()); ok {
				// The caller presented a raw LiteLLM key: no ACH identity, no
				// sealed material — LiteLLM authenticates the key itself.
				material = raw
			} else if kc, ok := middleware.KeyContextFromCtx(req.Context()); ok && kc.LiteLLMKeyMaterial != nil {
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
			stripAndRewrite(req.Header, material)
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
				// Model-name rewrite (path only — the body is never read).
				// Anchored after "/models/" so a configured "gemini/" can
				// never match ACH's own /gemini route prefix.
				if p := stripGeminiModelPrefix(req.URL.Path, deps.GeminiStripModelPrefixes); p != req.URL.Path {
					req.URL.Path = p
					req.URL.RawPath = ""
				}
			}

			// Budget tags: one stamping point for every family, so /mcp,
			// /a2a and /v2/model/info are attributed like /v1 (FWD-06's
			// body injection only ever reached /v1 + /gemini).
			stampTags(req)

			// JWT write LAST — overwrites whatever Authorization the client
			// sent: on /mcp + /a2a the per-target ACH JWT is the credential.
			if token, present := jwtFromCtx(req.Context()); present {
				req.Header.Set("Authorization", "Bearer "+token)
			}
		},
		ModifyResponse: modifyResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Client went away mid-proxy (SSE stream on /mcp or /v1 closed
			// by the caller): the inbound request context is canceled, NOT
			// an upstream fault. Nothing to write, no error to log, no
			// litellm_unreachable metric — the nginx "499" convention.
			if errors.Is(err, context.Canceled) {
				return
			}
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
// "/v1/chat/completions" → "/v1";
// "/mcp/foo/bar" → "/mcp"; etc.
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
