// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"io"
	"log/slog"
)

// NewLogger returns a *slog.Logger whose every record carries
// audit=true at the top level. The returned logger is the canonical
// Phase 2 audit emitter (Hub §18.2, §18.4); Phase 3 will reuse the
// same constructor for pk_/ek_ lifecycle and admin-operation events.
//
// The handler writes JSON to w. Production callers pass os.Stdout
// so Kubernetes log collection (fluent-bit / Loki / etc.) picks up
// audit records alongside operational logs; downstream pipelines
// filter via the audit=true predicate to route to an audit sink
// without ACH owning a second log destination.
//
// The handler emits records raw — it does NOT scrub plaintext, body
// content, or header values. Audit-safety is the CALLER's
// responsibility (compose events with target.kind / target.name /
// outcome / request_id only; NEVER include credential plaintext,
// credential_hash, or response bodies). This mirrors the
// [feedback_litellm_operator_no_redaction_filter] memory pattern.
//
// Level is fixed at slog.LevelInfo: Debug records are silently
// dropped (audit is a high-signal channel, not a debug stream).
func NewLogger(w io.Writer) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return slog.New(h).With(slog.Bool("audit", true))
}
