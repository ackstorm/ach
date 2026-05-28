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
	"github.com/ackstorm/ach/internal/litellm"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Deps wires the forwarder traffic handler's runtime dependencies.
//
// BIPResolver and EnvProvider are Postgres-backed caches (issue #34 C1
// + C2) that replaced the controller-runtime informers. K8sClient is
// retained ONLY for the ach-jwt-signing-keys Secret hot-reload watcher
// wired in cmd/ach/cmd/forwarder.go — the traffic path no longer reads
// from the cached k8s client.
type Deps struct {
	Pool             *pgxpool.Pool
	Redis            *redis.Client
	LiteLLM          litellm.Client
	Pepper           []byte
	K8sClient        client.Client
	BIPResolver      proxy.BIPResolver
	Resolver         keystore.Resolver
	TeamsResolver    keystore.TeamsResolver
	Signer           jwt.Signer
	Logger           *slog.Logger
	BaseURL          string
	Namespace        string
	LiteLLMUpstream  *url.URL
	LiteLLMMasterKey string
}

// New returns the traffic handler — middleware chain + anonymous JWKS +
// authenticated /v1, /gemini, /mcp/{name}/*, /a2a/{name}/* routes.
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
			LiteLLMMasterKey: deps.LiteLLMMasterKey,
			Logger:           deps.Logger,
		},
		Signer:      deps.Signer,
		BIPResolver: deps.BIPResolver,
		PrecheckDeps: precheck.Deps{
			K8sClient:     deps.K8sClient,
			TeamsResolver: deps.TeamsResolver,
			Namespace:     deps.Namespace,
		},
		BaseURL:   deps.BaseURL,
		Namespace: deps.Namespace,
	}

	r.Group(func(r chi.Router) {
		r.Use(pamw.Authn(deps.Resolver, nil, nil)) // no allowlist, no audit
		r.Handle("/v1/*", proxy.HandlerV1(hdeps))
		r.Handle("/gemini/*", proxy.HandlerGemini(hdeps))
		r.Handle("/mcp/{name}/*", proxy.HandlerMCP(hdeps))
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
