// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/json"
	"net/http"
)

// contentType is the Hub §15.5-mandated wire content type for every
// Platform API response — success or error, every status code.
const contentType = "application/json; charset=utf-8"

// JSON writes a success-side response envelope: sets the
// application/json; charset=utf-8 Content-Type header, flushes the
// status code, and JSON-encodes body to the writer.
//
// body is any value json.Marshal accepts. List endpoints typically
// pass map[string]any{"items": [...], "next_cursor": <opaque-or-nil>}
// per §15.5; single-resource endpoints pass a typed struct or map.
//
// Encoder write errors are deliberately swallowed (`_ =`): by the
// time the encoder fails, WriteHeader has already flushed the status
// and there is no clean recovery path. Mirrors the
// internal/litellm/transport.go best-effort write idiom.
//
// Callers MUST set the status before any other writer mutation;
// modifying headers AFTER calling JSON has no effect on the response.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Error writes the §15.5 error envelope:
//
//	{ "error": { "code": "<code>", "message": "<message>" },
//	  "request_id": "<requestID>" }
//
// code is one of the audit.Outcome* constants (closed vocabulary
// shared with the audit channel). message is a hard-coded handler
// string from the §15.5 outcome→message table — it MUST NOT echo raw
// upstream errors, stack traces, or user-controlled input (T-03-02-02
// in the plan's threat register).
//
// requestID is the "req_<ulid>" value the RequestID middleware
// generated and stored in context.Context (D-02 step 1); the caller
// reads it from ctx and passes it in explicitly so this package stays
// chi-independent.
//
// Encoder write errors are swallowed (same rationale as JSON).
func Error(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
		"request_id": requestID,
	})
}
