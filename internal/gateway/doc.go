// SPDX-License-Identifier: Apache-2.0

// Package gateway implements the `ach gateway` service mode: a thin
// reverse proxy that fronts the four ACH HTTP surfaces behind one
// origin. It wraps net/http/httputil.ReverseProxy (the same primitive
// internal/forwarder/proxy uses) so SSE/streaming flush and hop-by-hop
// header hygiene are handled by the stdlib.
//
// The route table is hardcoded (ServiceRoutes) — prefix -> in-cluster
// Service URL, namespace injected at boot. The gateway is a DUMB router:
// no auth (the forwarder mints per-target JWT; platform-api owns Dex SSO),
// no /metrics (unauthenticated on each service's traffic port — keeping it
// out means the prod Ingress cannot leak it), and no /dex (Dex is reached
// directly via ACH_DEX_ISSUER_URL in prod; proxied by the e2e nginx shim
// in dev). Only /platform, /content, /v1, /gemini, /mcp, /a2a,
// /.well-known, and /agents (per-agent Services, allowlisted via the
// achagents projection — ACH_DB_URL required) are proxied; /healthz
// returns 200 locally for probes.
//
// The gateway is OPTIONAL packaging, not a 6th logic mode: it carries no
// business logic, is toggled by the Helm `gateway.enabled` flag, and
// per-service Ingress is the supported alternative when it is disabled.
package gateway
