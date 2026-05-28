// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// codeInvalidArgument is the wire-format error code for malformed
// request bodies — mirrors the constant defined in
// internal/platformapi/envkeys/handler.go (Pattern P8). Kept package-
// local here rather than promoted to render/ because it is the only
// 4xx code the cli package emits (along with audit.OutcomeInternalError
// for 5xx).
const codeInvalidArgument = "invalid_argument"

// codeSessionNotFound is the 404 code per D-02. Also covers what the
// spec earmarks as 410 session_expired — the Redis side cannot tell
// TTL-bust from never-existed (the pragmatic alias is documented in
// 06-CONTEXT.md D-02 / planner discretion).
const codeSessionNotFound = "session_not_found"

// InitResponse is the §15.5 body returned by POST /platform/auth/cli/init
// per D-02 wire shape. expires_in is the session TTL in whole seconds;
// poll_interval is the recommended cadence between successive /token
// POSTs.
type InitResponse struct {
	SessionID       string `json:"session_id"`
	VerificationURL string `json:"verification_url"`
	PollInterval    int    `json:"poll_interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// InitHandler returns POST /platform/auth/cli/init. The handler:
//
//  1. Reads the request_id from ctx (RequestID middleware).
//  2. Strict-decodes the request body — empty body and `{}` both
//     accepted; unknown fields → 400 invalid_argument (Pattern P8).
//  3. Mints a fresh session_id (32-char base64url; 192 bits entropy).
//  4. Writes a pending-sentinel Session{CreatedAt} at
//     "ach:cli-session:<id>" via cli.Put (TTL = deps.sessionTTL).
//  5. Composes verification_url = deps.BaseURL +
//     "/platform/auth/login?session_id=<id>".
//  6. Renders 200 InitResponse.
//
// NO audit emission on init — D-19 reserves the platform.cli.login
// event for the /token success path (the actual pk_ exchange).
func InitHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)

		// Strict decode: accept empty body (io.EOF) AND `{}`; reject
		// unknown fields. Mirrors envkeys.CreateHandler Step 2.
		if r.Body != nil {
			defer func() { _ = r.Body.Close() }()
		}
		if r.Body != nil && r.ContentLength != 0 {
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			var stub struct{}
			if err := dec.Decode(&stub); err != nil && !errors.Is(err, io.EOF) {
				render.Error(w, http.StatusBadRequest, codeInvalidArgument,
					"invalid request body", reqID)
				return
			}
		}

		sessionID, err := NewSessionID()
		if err != nil {
			deps.Logger.Error("cli.init: NewSessionID failed", "err", err, "request_id", reqID)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to mint session id", reqID)
			return
		}

		ttl := deps.sessionTTL()
		sentinel := Session{
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := Put(ctx, deps.Redis, sessionID, sentinel, ttl); err != nil {
			deps.Logger.Error("cli.init: Put pending sentinel failed",
				"err", err, "request_id", reqID)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to record session", reqID)
			return
		}

		render.JSON(w, http.StatusOK, InitResponse{
			SessionID:       sessionID,
			VerificationURL: deps.BaseURL + "/platform/auth/login?session_id=" + sessionID,
			PollInterval:    int(deps.pollInterval().Seconds()),
			ExpiresIn:       int(ttl.Seconds()),
		})
	}
}
