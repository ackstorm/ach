// SPDX-License-Identifier: Apache-2.0

// Package render writes the Platform API HTTP response envelope per
// Hub §15.5. Every Phase 3 handler funnels success responses through
// JSON() and error responses through Error() so the wire-format shape
// (Content-Type, status, body envelope) lives in exactly one place.
//
// Hub §15.5 envelope (verbatim):
//
//	Success: <handler-defined JSON body> (list endpoints carry
//	         "next_cursor" — nullable — alongside "items").
//	Error:   { "error": { "code": "<outcome>", "message": "..." },
//	           "request_id": "req_..." }
//
// Every response — success or error — carries Content-Type
// application/json; charset=utf-8.
//
// The error envelope's `code` field uses the same closed vocabulary
// as the audit channel — handler plans pass audit.Outcome* constants
// directly. The package does NOT import internal/audit to avoid a
// circular dependency at the lowest layers; the vocabulary contract
// is enforced by the test file's import of internal/audit
// (TestErrorOutcomeCodeCompatibility) and by handler-plan code review.
//
// Encoder errors on the underlying http.ResponseWriter are swallowed
// (status has already been flushed by WriteHeader). This mirrors the
// internal/litellm/transport.go drainAndClose best-effort idiom.
//
// request_id is read by handlers from ctx.Value (RequestID middleware,
// D-02 step 1) and passed in explicitly so this package stays
// decoupled from chi/context internals.
package render
