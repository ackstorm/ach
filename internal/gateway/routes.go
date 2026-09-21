// SPDX-License-Identifier: Apache-2.0

package gateway

import "fmt"

// Route maps an incoming path prefix to an upstream base URL (no path).
// A prefix ending in "/" is a net/http.ServeMux subtree match
// (longest-prefix wins); one without is an exact path (/v2/model/info).
type Route struct {
	Prefix   string
	Upstream string
}

// ServiceRoutes returns the production route table for the given
// namespace. Upstreams are in-cluster Service DNS names. The forwarder
// owns four route families (/v1, /gemini, /mcp, /a2a) plus /.well-known
// (JWKS + the RFC 9728 protected-resource document, both anonymous) and
// the single owned /v2/model/info (ach-agent pricing); platform-api owns
// /platform and "/" (the console, D-26); content-service owns /content.
//
// Deliberately absent: /metrics (unauthenticated per service — never
// front it) and /dex (browser reaches Dex via ACH_DEX_ISSUER_URL in
// prod; the e2e nginx shim proxies it in dev).
func ServiceRoutes(namespace string) []Route {
	svc := func(name string, port int) string {
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", name, namespace, port)
	}
	forwarder := svc("ach-forwarder", 80)
	return []Route{
		{Prefix: "/platform/", Upstream: svc("ach-platform-api", 80)},
		{Prefix: "/content/", Upstream: svc("ach-content-service", 8082)},
		{Prefix: "/v1/", Upstream: forwarder},
		{Prefix: "/gemini/", Upstream: forwarder},
		{Prefix: "/mcp/", Upstream: forwarder},
		{Prefix: "/a2a/", Upstream: forwarder},
		{Prefix: "/.well-known/", Upstream: forwarder},
		// The one owned route outside the families: ach-agent prices its
		// usage at GET /v2/model/info with its ek_ (exact path, no subtree —
		// a trailing slash or any other /v2 path falls to "/" below).
		{Prefix: "/v2/model/info", Upstream: forwarder},
		// The console (D-26): platform-api serves the embedded SPA at "/"
		// with an SPA fallback (and /openwork + /api/den when the Den is on).
		// Nothing LiteLLM serves is reachable here — /ui, /key/*, /health…
		// live on LiteLLM's own host (D-18). net/http's mux keeps the longer
		// prefixes above (and /agents/, /healthz, /metrics) ahead of it.
		{Prefix: "/", Upstream: svc("ach-platform-api", 80)},
	}
}
