// SPDX-License-Identifier: Apache-2.0

// Package auth implements the stateless OIDC + PKCE Authorization Code
// flow against Dex (Phase 3 D-04 / D-05 / D-06). Both endpoints
// (LoginHandler, CallbackHandler) are UNAUTHENTICATED (D-02 carve-out) —
// they mount OUTSIDE the Authn-gated chi.Group in cmd/platform-api/main.go.
//
// Stateless: NO server-side session. The __Host-ach_sso cookie carries
// state + PKCE verifier between LoginHandler and CallbackHandler; the
// callback response (the JSON {key_id, plaintext, owner_email}) is the
// SOLE output (the one-time emission per KEY-03 + Hub §16.1 Specifics
// block) and the cookie is cleared. Subsequent requests authenticate via
// the pk_ bearer through the Authn middleware (Plan 03-05).
//
// JSON-only endpoints: GET /platform/auth/login redirects 302 straight to
// Dex; GET /platform/auth/sso/callback returns the JSON response and
// exits. v1alpha1 supports the loopback-callback CLI pattern only — no
// hosted login page (D-05).
//
// Default-team-missing fallback (Hub §17 / API-02): if TeamMemberAdd
// returns any error on the default Team, the handler emits audit
// outcome=default_team_missing and renders 500. ACH does NOT lazily
// create the default Team — that is a fail-loud signal to the deployer.
//
// Plaintext discipline (Hub §16.1, internal/credhash, internal/keys
// docstrings): the bearer plaintext (pk_<26-base32-lower>) appears in
// exactly one place — the response body of the callback. It is NEVER
// logged, NEVER recorded in audit Extra, NEVER persisted in the DB or
// in any cache. The credential_hash (HMAC-SHA-256 with pepper) is the
// only persisted form, and the key.id (pkid_<ulid>) is the only field
// emitted into audit events.
//
// Dex configuration (D-06): the four ACH_DEX_* env vars are REQUIRED at
// process start (validated in cmd/platform-api/main.go per Plan 03-11).
// This package consumes the pre-constructed *oidc.Provider and
// *oauth2.Config via Deps; it does NOT itself read environment.
package auth
