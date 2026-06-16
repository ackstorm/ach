// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// TokenRequest is the wire body for POST /platform/auth/cli/token.
// Strict-decoded with DisallowUnknownFields per Pattern P8.
type TokenRequest struct {
	SessionID string `json:"session_id"`
}

// TokenResponse is the 200 success body returned once the SSO
// round-trip has landed the pk_ in Redis. Plaintext appears EXACTLY
// ONCE — here — per Hub §16.1 (Pattern S5).
type TokenResponse struct {
	KeyID      string `json:"key_id"`
	Plaintext  string `json:"plaintext"`
	OwnerEmail string `json:"owner_email"`
}

// TokenPendingResponse is the 202 pending body returned while the
// session's KeyID is still empty (sentinel). The CLI polls until it
// flips to 200 or 404.
type TokenPendingResponse struct {
	Status string `json:"status"`
}

// TokenHandler returns POST /platform/auth/cli/token. The handler:
//
//  1. Reads request_id from ctx (RequestID middleware).
//  2. Strict-decodes TokenRequest; missing/empty session_id → 400
//     invalid_argument.
//  3. Peeks (non-destructive) the session at "ach:cli-session:<id>".
//     Absent → 404 session_not_found; corrupt JSON / transport
//     error → 500 internal_error.
//  4. If sess.KeyID == "" (sentinel): re-Put with deps.sessionTTL to
//     refresh the TTL across polls; render 202 TokenPendingResponse.
//  5. If sess.KeyID != "" (completed): Consume (GETDEL) the session
//     to enforce one-shot semantics; emit one platform.cli.login
//     audit event with key.id + actor=<ns>/<owner_email>; render 200
//     TokenResponse. The pk_ plaintext is in the response body ONLY
//     — never in audit, never in operational logs (Pattern S5).
func TokenHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)

		// Strict decode. Empty body → 400.
		if r.Body == nil || r.ContentLength == 0 {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"missing request body", reqID)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var req TokenRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"invalid request body", reqID)
			return
		}
		if req.SessionID == "" {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"session_id required", reqID)
			return
		}

		// Peek (non-destructive) — pending sentinels MUST survive
		// successive polls until the Dex round-trip lands the pk_.
		sess, _, err := Peek(ctx, deps.Redis, req.SessionID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				render.Error(w, http.StatusNotFound, codeSessionNotFound,
					"session not found", reqID)
				return
			}
			// ErrCorruptSession OR transport error.
			deps.Logger.Error("cli.token: Peek failed",
				"err", err, "request_id", reqID)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read session", reqID)
			return
		}

		// Pending sentinel branch: re-Put to refresh TTL across polls,
		// then 202.
		if sess.KeyID == "" {
			if perr := Put(ctx, deps.Redis, req.SessionID, *sess, deps.sessionTTL()); perr != nil {
				deps.Logger.Error("cli.token: pending Put refresh failed",
					"err", perr, "request_id", reqID)
				// Non-fatal — the cached sentinel still has SOME TTL
				// (the previous Put). 202 stays correct; surfacing 500
				// would mask the pending state from the polling CLI.
			}
			render.JSON(w, http.StatusAccepted, TokenPendingResponse{Status: "pending"})
			return
		}

		// Completed branch: Consume (GETDEL) for one-shot semantics.
		// The Peek↔Consume gap is a TOCTOU but harmless — the only
		// state that can change in between is "completed → consumed
		// by a racing poll", in which case Consume returns
		// ErrNotFound and we surface 404 (the racing poll already
		// got the pk_).
		consumed, err := Consume(ctx, deps.Redis, req.SessionID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				render.Error(w, http.StatusNotFound, codeSessionNotFound,
					"session not found", reqID)
				return
			}
			deps.Logger.Error("cli.token: Consume failed",
				"err", err, "request_id", reqID)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to consume session", reqID)
			return
		}

		// Audit emission per D-19 / Pattern S5: action=
		// platform.cli.login, actor="<ns>/<owner_email>",
		// key.id=pkid_…, request_id. The pk_ plaintext NEVER appears.
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action:    audit.ActionCliLogin,
			Outcome:   audit.OutcomeCreated,
			Actor:     deps.Namespace + "/" + consumed.OwnerEmail,
			RequestID: reqID,
			KeyID:     consumed.KeyID,
		})
		// G7: platform_api_login_total{outcome="created"} on CLI login
		// completion (the /token poll that consumes the minted pk_).
		if deps.Metrics != nil {
			deps.Metrics.Login.WithLabelValues(audit.OutcomeCreated).Inc()
		}

		render.JSON(w, http.StatusOK, TokenResponse{
			KeyID:      consumed.KeyID,
			Plaintext:  consumed.Plaintext,
			OwnerEmail: consumed.OwnerEmail,
		})
	}
}
