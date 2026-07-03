// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// UpstreamResolver maps (namespace, agentName) → upstream base URL for a
// routable webhook agent. Satisfied by *agentstore.Store.
type UpstreamResolver interface {
	Upstream(ns, name string) (string, bool)
}

// newHookHandler serves POST-able webhook delivery under the /hook/ subtree.
// The public URL is /hook/{ns}/{name}/channels/{channel}/events; the gateway
// strips the /hook/{ns}/{name} prefix and reverse-proxies the remainder
// (/channels/{channel}/events) to the agent's Service. The projection is the
// allowlist — an unknown (ns, name) is a 404, never a proxy to an arbitrary
// in-cluster Service. No auth, no header rewrite; the harness verifies HMAC.
func newHookHandler(resolver UpstreamResolver, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/hook/")
		ns, after, ok := strings.Cut(rest, "/")
		if !ok || ns == "" {
			http.NotFound(w, r)
			return
		}
		name, remainder, ok := strings.Cut(after, "/")
		if !ok || name == "" {
			http.NotFound(w, r)
			return
		}
		upstream, ok := resolver.Upstream(ns, name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		target, err := url.Parse(upstream)
		if err != nil {
			if logger != nil {
				logger.Error("hook: bad upstream URL", slog.String("upstream", upstream), slog.String("err", err.Error()))
			}
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		// Forward the post-prefix path verbatim to the harness's native route.
		r.URL.Path = "/" + remainder
		// ponytail: builds a ReverseProxy per request; cache per-upstream if hook QPS ever matters.
		newReverseProxy(target, logger).ServeHTTP(w, r)
	}
}
