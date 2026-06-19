// SPDX-License-Identifier: Apache-2.0

// Package platformapi is the chi-based composition root for the ACH
// Platform API HTTP server. It owns two responsibilities:
//
//  1. Router assembly — `New(deps Deps) http.Handler` builds the
//     chi.Mux per D-01 with the middleware chain from Plan 03-05
//     (RequestID → RecoverPanic → AccessLog → ContentTypeJSON) wrapped
//     around the unauthenticated routes (/healthz, /livez, /readyz,
//     /platform/auth/login, /platform/auth/sso/callback) and an
//     Authn-gated subtree for the management endpoints
//     (/platform/hydrate, /platform/env-keys, /platform/keys,
//     /platform/environments, /platform/admin). All routes live under /platform/ per API-01;
//     health probes are the only carve-out.
//
//  2. Manager.Runnable wrap — `NewRunnable(addr, handler, logger)`
//     returns an http.Server tied to the controller-runtime manager's
//     signal context (D-20). Graceful shutdown via
//     http.Server.Shutdown(ctx, 10s) per D-03.
//
// Code structure per D-24:
//
//   - cmd/platform-api/main.go composes the dependency graph and hands
//     a fully-populated Deps to New().
//   - internal/platformapi/server.go owns the chi.Mux constructor.
//   - internal/platformapi/runnable.go owns the *http.Server lifecycle.
//   - internal/platformapi/{auth,envkeys,environments,hydrate,admin}
//     each export their handler/Mount entry points.
//
// API-01 invariant: every route either starts with "/platform/" OR
// matches one of "/healthz", "/livez", "/readyz". NO /v1/... routes.
// Test S-3 in server_test.go enumerates the mux and asserts this
// structurally.
//
// BLK-02: middleware.Authn(deps.Resolver, deps.Allowlist, deps.Audit)
// — the allowlist is passed as a positional arg so KeyContext.IsAdmin
// is populated uniformly for downstream handlers (Plan 03-10 admin
// endpoints, Plan 03-08 envkeys, Plan 03-09 environments/hydrate all
// read keyCtx.IsAdmin directly).
//
// BLK-03: hydrate.Deps.LiteLLM is a first-class field — the hydrate
// handler consumes the LiteLLM client via
// internal/platformapi/teams.LookupCallerTeams per WARN-06.
package platformapi
