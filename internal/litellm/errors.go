// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrNotFound is returned by list-style helpers when the LiteLLM response
// envelope is non-nil but its Data slice is empty. Callers use
// errors.Is(err, ErrNotFound) to distinguish "list endpoint returned 200
// with empty data" from "HTTP error".
var ErrNotFound = errors.New("litellm: not found")

// APIError is the typed error returned by makeRequest for 4xx (non-401)
// and 5xx responses. Exposes the response Body so callers that need to
// peek at the LiteLLM error message (e.g. CreateAccessGroup detecting
// "already exists") can do so via errors.As. The Error() string keeps
// the legacy "litellm: <status> on <method> <path> (code=<code>)" format
// for back-compat with string-matching callers (auth/sso.go).
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Code       string
	Body       []byte
	Transient  bool
}

// Error implements the error interface. Body content intentionally
// excluded per §9.1 (no body content in error strings).
func (e *APIError) Error() string {
	if e.Transient {
		return fmt.Sprintf("litellm: %d on %s %s (code=%s, transient)", e.StatusCode, e.Method, e.Path, e.Code)
	}
	return fmt.Sprintf("litellm: %d on %s %s (code=%s)", e.StatusCode, e.Method, e.Path, e.Code)
}

// IsHTTPNotFound reports whether err is an *APIError carrying HTTP 404.
// A 404 is the LiteLLM signal that the addressed resource does not exist —
// on POST /key/delete it means the virtual key is already gone. Callers that
// treat delete-of-absent as idempotent success (the ek_ revoke path) use this
// to distinguish a confirmed not-found from every other 4xx/5xx, so only a
// genuine "already gone" bypasses the LiteLLM-first revoke barrier (KEY-08).
func IsHTTPNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// Auth401Error is the typed error returned by Client.makeRequest when
// LiteLLM responds with HTTP 401. The reconciler's §7.7 fast-path uses
// errors.As(err, &auth401) to detect it and trigger cache-invalidate +
// re-probe enqueue + Ready=False, reason=LiteLLMUnavailable.
//
// The Body field carries the raw response body for diagnostic logging
// (only emitted when ACH_LITELLM_DANGEROUSLY_LOG_BODIES=true).
// Default code paths never log Body.
type Auth401Error struct {
	Path string
	Body []byte
}

// Error implements the error interface. The message intentionally omits
// the response body to honor §9.1: no body content in error strings,
// because controller-runtime emits errors as Events / status condition
// messages where they would be persisted in cluster-readable state.
func (e *Auth401Error) Error() string {
	return fmt.Sprintf("litellm: 401 unauthorized on %s", e.Path)
}

// litellmErrorEnvelope mirrors the LiteLLM 1.83.10 error response shape
// (uniform across all 14 authenticated endpoints — verified by spike
// Probe 8; recorded literally in 01-01-SUMMARY.md):
//
//	{"error":{"message":"...","type":"...","param":null|"...","code":"401"}}
type litellmErrorEnvelope struct {
	Error struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Param   json.RawMessage `json:"param"`
		Code    string          `json:"code"`
	} `json:"error"`
}

// processLitellmError parses the {error: {message, type, param, code}}
// envelope LiteLLM returns on every non-2xx response. On unmarshal
// failure it returns the raw body capped at 512 bytes and kind="unparsed"
// so the caller still has something to log without spraying possibly
// large response bodies into error strings.
//
// Derivative work from bbdsoftware/litellm-operator (Apache-2.0; NOTICE).
func processLitellmError(body []byte) (kind, message, code string) {
	var env litellmErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Error.Code == "" && env.Error.Message == "" {
		cap := body
		if len(cap) > 512 {
			cap = cap[:512]
		}
		return "unparsed", string(cap), ""
	}
	return env.Error.Type, env.Error.Message, env.Error.Code
}
