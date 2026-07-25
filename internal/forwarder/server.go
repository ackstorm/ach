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
}

// New returns the traffic handler — middleware chain + anonymous JWKS +
// authenticated /v1, /v2, /gemini, /mcp/{name}[/*], /a2a/{name}[/*] routes (the
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
		},
		Signer:      deps.Signer,
		BIPResolver: deps.BIPResolver,
		PrecheckDeps: precheck.Deps{
			EnvProvider:   deps.EnvProvider,
			TeamsResolver: deps.TeamsResolver,
		},
		BaseURL: deps.BaseURL,
	}

	r.Group(func(r chi.Router) {
		r.Use(pamw.Authn(deps.Resolver, nil, nil)) // no allowlist, no audit
		r.Handle("/v1/*", proxy.HandlerV1(hdeps))
		r.Handle("/v2/*", proxy.HandlerV2(hdeps))
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
