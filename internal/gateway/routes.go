// SPDX-License-Identifier: Apache-2.0

package gateway

import "fmt"

// Route maps an incoming path prefix to an upstream base URL (no path).
// The prefix MUST end in "/" so net/http.ServeMux treats it as a subtree
// match (longest-prefix wins).
type Route struct {
	Prefix   string
	Upstream string
}

// ServiceRoutes returns the production route table for the given
// namespace. Upstreams are in-cluster Service DNS names. The forwarder
// owns four route families (/v1, /gemini, /mcp, /a2a) plus /.well-known
// (JWKS); platform-api owns /platform; content-service owns /content.
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
	}
}
