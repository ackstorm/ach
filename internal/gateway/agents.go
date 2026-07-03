// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// UpstreamResolver maps (namespace, serviceName) → upstream base URL for a
// routable agent Service. Satisfied by *agentstore.Store.
type UpstreamResolver interface {
	Upstream(ns, serviceName string) (string, bool)
}

// newAgentsHandler reverse-proxies /agents/{ns}/{service}/… to the agent's
// Service, where {service} is the Service name (e.g. achagent-gh). The
// gateway strips the /agents/{ns}/{service} prefix and forwards whatever
// follows verbatim — the tail is the harness's contract (webhook channels,
// a2a, anything), not the gateway's. A missing tail forwards as "/". The
// projection is the allowlist — an unknown (ns, service) is a 404, never a
// proxy to an arbitrary in-cluster Service. No auth, no header rewrite.
func newAgentsHandler(resolver UpstreamResolver, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/agents/")
		ns, after, ok := strings.Cut(rest, "/")
		if !ok || ns == "" {
			http.NotFound(w, r)
			return
		}
		service, remainder, _ := strings.Cut(after, "/")
		if service == "" {
			http.NotFound(w, r)
			return
		}
		upstream, ok := resolver.Upstream(ns, service)
		if !ok {
			http.NotFound(w, r)
			return
		}
		target, err := url.Parse(upstream)
		if err != nil {
			if logger != nil {
				logger.Error("agents: bad upstream URL", slog.String("upstream", upstream), slog.String("err", err.Error()))
			}
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		// Forward the post-prefix path verbatim; empty tail becomes "/".
		r.URL.Path = "/" + remainder
		// ponytail: builds a ReverseProxy per request; cache per-upstream if agent QPS ever matters.
		newReverseProxy(target, logger).ServeHTTP(w, r)
	}
}
