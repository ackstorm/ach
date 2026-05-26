// SPDX-License-Identifier: Apache-2.0

// Package middleware ships the Platform API's chi-compatible middleware
// chain. Every middleware function satisfies chi's canonical
// `func(http.Handler) http.Handler` signature (D-01).
//
// Order (outer -> inner) per D-02:
//
//	RequestID -> RecoverPanic -> AccessLog -> ContentTypeJSON -> Authn -> handler
//
// Each layer's contract:
//
//   - RequestID — generates "req_<ulid>" server-side ALWAYS (never trusts
//     a caller-supplied X-Request-Id; T-03-05-06). Sets the
//     X-Request-Id response header and stores the id in ctx.
//
//   - RecoverPanic — wraps inner handlers so a panic in any handler
//     becomes a 500 internal_error envelope + an audit emission.
//     Captures the panic value to the operational logger; never echoes
//     it to the client.
//
//   - AccessLog — emits {method, path, status, latency_ms, request_id}
//     exclusively (T-03-05-01 / FWD-11). The x-ach-key header is
//     NEVER logged (Authn discards it; AccessLog never reads it; the
//     standard slog handler never iterates headers).
//
//   - ContentTypeJSON — sets `application/json; charset=utf-8` on every
//     response unless the inner handler has already set Content-Type
//     (idempotent — preserves SSO 302 redirects and Content-Type-aware
//     handlers).
//
//   - Authn — the load-bearing middleware. Reads x-ach-key from
//     r.Header, calls keystore.Resolver.Resolve, and on success
//     DISCARDS the plaintext (r.Header.Del per D-19) before injecting a
//     populated KeyContext into ctx. Inner handlers see only the
//     KeyContext, never the raw bearer.
//
// Unauthenticated endpoints (D-02 carve-out: /healthz, /livez, /readyz,
// /platform/auth/login, /platform/auth/sso/callback) MUST be mounted
// OUTSIDE the Authn-gated chi.Group — Authn rejects requests with no
// x-ach-key header as 401 missing_key.
//
// # KeyContext propagation (D-19 / BLK-02)
//
// Authn calls WithKeyContext to attach a read-only KeyContext to the
// request's context.Context. KeyContext.IsAdmin is populated by Authn
// against the allowlist parameter: pk_ callers whose OwnerEmail appears
// in the allowlist receive IsAdmin=true; ek_ callers ALWAYS receive
// IsAdmin=false (admin endpoints reject ek_ upstream regardless).
//
// Downstream handlers retrieve the context via KeyContextFromCtx(ctx)
// — returns the zero-value KeyContext and ok=false on contexts that did
// not pass through Authn.
package middleware
