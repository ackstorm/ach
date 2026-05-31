// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// newReverseProxy builds a single-host reverse proxy to target. It mirrors
// internal/forwarder/proxy: clear req.Host so Go fills the upstream Host
// from target.Host (never leak the client Host), and force immediate flush
// (FlushInterval = -1) so SSE (/v1) and streamable-http (/mcp) chunks pass
// through without buffering — the Go equivalent of nginx proxy_buffering off.
//
// NewSingleHostReverseProxy's Director already rewrites scheme+host and
// preserves req.URL.Path verbatim (target has no base path), so /v1/chat/...
// reaches the upstream unchanged.
func newReverseProxy(target *url.URL, logger *slog.Logger) *httputil.ReverseProxy {
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1

	orig := rp.Director
	rp.Director = func(req *http.Request) {
		orig(req)
		req.Host = ""
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if logger != nil {
			logger.Error("gateway upstream error",
				slog.String("path", r.URL.Path),
				slog.String("upstream", target.String()),
				slog.String("err", err.Error()),
			)
		}
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	return rp
}
