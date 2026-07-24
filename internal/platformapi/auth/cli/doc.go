// SPDX-License-Identifier: Apache-2.0

// Package cli ships the server-side device-code endpoints that back
// `ach login`'s polling flow (Phase 6 D-02, D-19, D-20). Two HTTP
// handlers + a Redis-backed session helper:
//
//   - InitHandler (POST /platform/auth/cli/init) — anonymously mints a
//     32-char base64url session_id, writes a pending-sentinel Session
//     to Redis at "ach:cli-session:<id>" with TTL ~5 minutes, and
//     returns {session_id, verification_url, poll_interval, expires_in}.
//     verification_url is the existing /platform/auth/login route
//     parametrized with ?session_id=<id>.
//
//   - TokenHandler (POST /platform/auth/cli/token) — given a body
//     {session_id}, peeks the stored Session. If the sentinel is still
//     in place (KeyID == ""), re-puts it with a fresh TTL and returns
//     202 {status:"pending"}; if the pk_ has been written (KeyID set),
//     it Consumes (GETDEL) the entry and returns 200 {key_id,
//     plaintext, owner_email}, emitting a single platform.cli.login
//     audit event. A second /token call after a 200 (or for an unknown
//     id) returns 404 session_not_found.
//
// The D-20 surgical extension to sso.CallbackHandler (in the sibling
// internal/platformapi/auth package) writes the freshly minted pk_
// payload to "ach:cli-session:<id>" via cli.Put when the inbound
// callback URL packs a session_id into the OAuth2 state — and renders
// a friendly browser-side HTML "you may now close this page" page
// instead of the legacy JSON body. Absence-of-session-id preserves the
// pre-Phase-6 JSON response so test/e2e/phase3_invariants browser-
// driven assertions continue to pass.
//
// Audit-safety (Hub §16.1, §15.4 trust-artifact contract, Pattern S5):
// the pk_ plaintext appears EXACTLY ONCE in the /token success
// response body. It MUST NOT flow through Deps.Logger (operational
// log) or Deps.Audit (structured audit log) — only the resolved
// key_id (pkid_…) and owner_email appear in the audit event. The
// helper transports values raw — discipline lives at the handler
// site, not in scrubbing.
package cli
