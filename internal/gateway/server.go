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
// and everything under it, longest-match wins. Unmatched paths (e.g.
// /metrics, /) fall through to the built-in 404 — the gateway never
// fabricates a "/" catch-all, so it cannot accidentally proxy /metrics.
//
// Returns an error if any route Upstream fails to parse (refuse-to-start).
func Handler(routes []Route, logger *slog.Logger) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	for _, r := range routes {
		target, err := url.Parse(r.Upstream)
		if err != nil {
			return nil, err
		}
		mux.Handle(r.Prefix, newReverseProxy(target, logger))
	}
	return mux, nil
}
