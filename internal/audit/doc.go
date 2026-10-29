// SPDX-License-Identifier: Apache-2.0

// Package audit is the operator's dedicated structured-audit log surface.
//
// The single exported constructor (NewLogger) wraps a stdlib JSON
// slog.Handler with a top-level audit=true attribute (D-17). Phase 2
// emits only Hub §18.4 orphan-cleanup revocation events through this
// logger (D-18); Phase 3 will expand to pk_/ek_ create/revoke,
// Environment lifecycle, hydrate, and admin operations per §18.2.
//
// Forbidden imports inside this package: fmt, log, sigs.k8s.io/...,
// k8s.io/... — the package is stdlib-only by design (mirroring
// internal/credhash's no-logger discipline). Callers compose
// audit-safe events; this package transports them.
//
// Output destination is the caller's choice via io.Writer. Production
// wires os.Stdout in cmd/operator/main.go; tests wire bytes.Buffer for
// line-by-line assertions.
//
// Audit-safety contract: the handler emits records raw — it does NOT
// scrub plaintext, credential material, or response bodies. The
// [feedback_litellm_operator_no_redaction_filter] memory pattern
// applies — discipline over scrubbing. Callers MUST NOT pass
// credential plaintext, credential_hash values, response bodies,
// raw error strings, or other sensitive payloads as attribute
// values. Phase 2's only emitter (Plan 02-08 orphan cleanup) emits:
//
//	{target.kind, target.name (when applicable), outcome, user_id}
//
// A future request_id field is reserved for Phase 3 Platform API
// events and is intentionally absent in Phase 2's emitter shape
// (the orphan tick is not request-scoped). Raw err.Error() strings
// are forbidden as attribute values — they were previously included
// on the litellm_unreachable and revoke_failed paths but removed
// (CR-03 / 02-REVIEW) because no-scrubbing + the underlying %w
// wrapping in litellm.RESTClient.makeRequest cannot guarantee the
// wire-format string is body-free across Go runtime versions.
// Diagnostic detail belongs in the operational log, not the audit
// channel.
package audit
