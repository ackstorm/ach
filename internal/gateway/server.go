// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"log/slog"
	"net/http"
	"net/url"
)

// Handler builds the gateway's http.Handler: a net/http.ServeMux mapping
// each route prefix to its reverse proxy, plus a local /healthz returning
// 200 (used by the pod readiness/liveness probes — no upstream dependency,
// so the gateway reports Ready as soon as it is serving).
//
// ServeMux subtree semantics: a pattern ending in "/" matches that prefix
// and everything under it, longest-match wins. The route table ends in
// "/" -> platform-api (the console, D-26), and platform-api serves its
// own Prometheus handler on that same port, so /metrics and /metrics/
// are pinned to 404 here — they must never leave the cluster through the
// public Ingress.
//
// When resolver is non-nil, the gateway also serves the /agents/ subtree
// (delivery to per-agent Services). In production the resolver is always
// set (ACH_DB_URL is required); nil is a test-only convenience.
//
// Returns an error if any route Upstream fails to parse (refuse-to-start).
func Handler(routes []Route, resolver UpstreamResolver, logger *slog.Logger) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/metrics", http.NotFound)
	mux.HandleFunc("/metrics/", http.NotFound)
	if resolver != nil {
		mux.Handle("/agents/", newAgentsHandler(resolver, logger))
	}
	for _, r := range routes {
		target, err := url.Parse(r.Upstream)
		if err != nil {
			return nil, err
		}
		mux.Handle(r.Prefix, newReverseProxy(target, logger))
	}
	return mux, nil
}
