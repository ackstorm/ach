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

// Upstreams are the base URLs (no path) of the services the gateway fronts.
type Upstreams struct {
	PlatformAPI    string
	Forwarder      string
	ContentService string
}

// ServiceUpstreams returns the in-cluster Service DNS upstreams for the
// given namespace.
func ServiceUpstreams(namespace string) Upstreams {
	svc := func(name string, port int) string {
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", name, namespace, port)
	}
	return Upstreams{
		PlatformAPI:    svc("ach-platform-api", 80),
		Forwarder:      svc("ach-forwarder", 80),
		ContentService: svc("ach-content-service", 8082),
	}
}

// ServiceRoutes returns the production route table over the given
// upstreams. The forwarder
// owns four route families (/v1, /gemini, /mcp, /a2a) plus /.well-known
// (JWKS + the RFC 9728 protected-resource document, both anonymous; except
// /.well-known/opencode, platform-api's OpenCode install manifest) and
// the single owned /v2/model/info (ach-agent pricing); platform-api owns
// /platform and "/" (the console, D-26); content-service owns /content.
//
// Deliberately absent: /metrics (unauthenticated per service — never
// front it) and /dex (browser reaches Dex via ACH_DEX_ISSUER_URL in
// prod; the e2e nginx shim proxies it in dev).
func ServiceRoutes(u Upstreams) []Route {
	forwarder := u.Forwarder
	return []Route{
		{Prefix: "/platform/", Upstream: u.PlatformAPI},
		{Prefix: "/content/", Upstream: u.ContentService},
		{Prefix: "/v1/", Upstream: forwarder},
		{Prefix: "/gemini/", Upstream: forwarder},
		{Prefix: "/mcp/", Upstream: forwarder},
		{Prefix: "/a2a/", Upstream: forwarder},
		// OpenCode's well-known manifest is platform-api's (it knows the
		// provider name); every other /.well-known document is the forwarder's.
		{Prefix: "/.well-known/opencode", Upstream: u.PlatformAPI},
		{Prefix: "/.well-known/", Upstream: forwarder},
		// The one owned route outside the families: ach-agent prices its
		// usage at GET /v2/model/info with its ek_ (exact path, no subtree —
		// a trailing slash or any other /v2 path falls to "/" below).
		{Prefix: "/v2/model/info", Upstream: forwarder},
		// The console (D-26): platform-api serves the embedded SPA at "/"
		// with an SPA fallback.
		// Nothing LiteLLM serves is reachable here — /ui, /key/*, /health…
		// live on LiteLLM's own host (D-18). net/http's mux keeps the longer
		// prefixes above (and /healthz, /metrics) ahead of it.
		{Prefix: "/", Upstream: u.PlatformAPI},
	}
}
