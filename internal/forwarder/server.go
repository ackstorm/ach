// SPDX-License-Identifier: Apache-2.0

package forwarder

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/forwarder/proxy"
	"github.com/ackstorm/ach/internal/keystore"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/go-chi/chi/v5"
)

// Deps wires the forwarder traffic handler's runtime dependencies.
//
// BIPResolver and EnvProvider are Postgres-backed caches (issue #34 C1
// + C2) that replaced the controller-runtime informers — the traffic
// path no longer reads from the cached k8s client.
type Deps struct {
	// Identity is the identity profile: no Environments, no policies —
	// /mcp and /a2a forward the resolved identity like the catch-all does
	// (no precheck, no BIP JWT). BIPResolver/EnvProvider/TeamsResolver are nil.
	Identity      bool
	BIPResolver   proxy.BIPResolver
	EnvProvider   precheck.EnvProvider
	Resolver      keystore.Resolver
	TeamsResolver keystore.TeamsResolver
	Signer        jwt.Signer
	Logger        *slog.Logger
	BaseURL       string
	// KeyEncryptionKey is the 32-byte AES-256 DEK (ACH_KEY_ENCRYPTION_KEY,
	// G3); the proxy Director decrypts the sealed LiteLLM key material per
	// request before forwarding. Required (validated at process start).
	KeyEncryptionKey []byte
	LiteLLMUpstream  *url.URL
	// AuthnOptions: the RFC 9728 challenge on 401 + the declared credential headers.
	AuthnOptions pamw.AuthnOptions
}

// New returns the traffic handler — middleware chain + anonymous JWKS +
// authenticated /v1, /gemini, /mcp/{name}[/*], /a2a/{name}[/*] routes (the
// bare and subpath forms are both registered so a slash-less MCP endpoint
// resolves — see the route block below).
// D-02 middleware chain: RequestID → RecoverPanic → AccessLog → Authn
// (per-route bypass for JWKS).
func New(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(pamw.RequestID)
	r.Use(pamw.RecoverPanic(deps.Logger, nil)) // nil audit per OBS-01
	r.Use(pamw.AccessLog(deps.Logger))         // redacts x-ach-key per FWD-11

	// Anonymous JWKS — OUTSIDE the Authn group per D-02.
	r.Get("/.well-known/jwks.json", jwt.JWKSHandler(deps.Signer))

	// Authenticated subtree.
	hdeps := proxy.HandlerDeps{
		Deps: proxy.Deps{
			LiteLLMUpstream:  deps.LiteLLMUpstream,
			Logger:           deps.Logger,
			KeyEncryptionKey: deps.KeyEncryptionKey,
			BaseURL:          deps.BaseURL,
		},
		Signer:      deps.Signer,
		BIPResolver: deps.BIPResolver,
		PrecheckDeps: precheck.Deps{
			EnvProvider:   deps.EnvProvider,
			TeamsResolver: deps.TeamsResolver,
		},
		BaseURL:  deps.BaseURL,
		Identity: deps.Identity,
	}

	// OAuth discovery, ANONYMOUS by design (a client fetches these precisely
	// because it holds no credential yet): the RFC 8414 AS document (the AS
	// itself lives in platform-api; the gateway routes /.well-known/ here)
	// and the RFC 9728 protected-resource document for the API root and for
	// every /mcp/<name> + /a2a/<name>. ACH composes both — LiteLLM's PRM
	// document is no longer relayed, so the client is sent to ACH's
	// authorization server, never LiteLLM's.
	wk := proxy.WellKnownHandler(deps.BaseURL)
	r.Handle("/.well-known/oauth-authorization-server", wk)
	r.Handle("/.well-known/oauth-protected-resource", wk)
	r.Handle("/.well-known/oauth-protected-resource/*", wk)

	r.Group(func(r chi.Router) {
		r.Use(pamw.Authn(deps.Resolver, nil, nil, deps.AuthnOptions)) // no allowlist, no audit
		r.Handle("/v1/*", proxy.HandlerV1(hdeps))
		r.Handle("/gemini/*", proxy.HandlerGemini(hdeps))
		// Both the bare "/mcp/{name}" and the subpath "/mcp/{name}/*" forms
		// are registered: chi (no RedirectSlashes here) does NOT match a
		// trailing-slash-less path against "/{name}/*", and the canonical MCP
		// endpoint LiteLLM serves — the one hydrate writes into runtime config
		// (platformapi/hydrate/handler.go) — is the bare "/mcp/<name>" form.
		// Without the bare route a client POSTing "/mcp/<name>" 404s at the
		// router before precheck/proxy ever run. Same for /a2a.
		r.Handle("/mcp/{name}", proxy.HandlerMCP(hdeps))
		r.Handle("/mcp/{name}/*", proxy.HandlerMCP(hdeps))
		r.Handle("/a2a/{name}", proxy.HandlerA2A(hdeps))
		r.Handle("/a2a/{name}/*", proxy.HandlerA2A(hdeps))
	})

	// Catch-all: everything else LiteLLM serves (/ui, /sso, /key/*, /model/*,
	// /anthropic/*, /health, …) when ACH fronts the whole API host. The
	// credential is optional here — an ACH credential is resolved and
	// rewritten, a raw key passes, none is forwarded anonymously and LiteLLM
	// decides. A present-but-invalid credential is still a 401.
	r.Group(func(r chi.Router) {
		opts := deps.AuthnOptions
		opts.Optional = true
		opts.Challenge = nil
		r.Use(pamw.Authn(deps.Resolver, nil, nil, opts))
		r.Handle("/*", proxy.HandlerPassthrough(hdeps))
	})

	return r
}

// NewHealthHandler returns the health handler with /healthz, /livez, /readyz.
// /readyz gates on mgrCacheSync() AND signer.Loaded() per D-Discretion.
// /healthz + /livez always return 200 (process up).
func NewHealthHandler(signer jwt.Signer, mgrCacheSync func(context.Context) bool) http.Handler {
	mux := http.NewServeMux()
	live := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("/healthz", live)
	mux.HandleFunc("/livez", live)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		ready := signer != nil && signer.Loaded()
		if ready && mgrCacheSync != nil && !mgrCacheSync(ctx) {
			ready = false
		}
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
