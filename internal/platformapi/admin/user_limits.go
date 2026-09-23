// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

type limitsRequest struct {
	MaxKeys *int `json:"max_keys"`
}

const limitsInvalidMsg = "max_keys required and must be >= 0"

// UserLimitsHandler serves PATCH /platform/admin/users/{email}/limits.
// The ceiling lives in Postgres because ACH enforces it on its own mint path.
func UserLimitsHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)
		actor := composeActor(deps.Namespace, keyCtx.OwnerEmail)

		email, err := url.PathUnescape(chi.URLParam(r, "email"))
		if err != nil || email == "" {
			emitUserLimits(ctx, deps, audit.OutcomeInvalidKeyFormat, actor, reqID, email)
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "invalid email path parameter", reqID)
			return
		}

		var req limitsRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if decErr := dec.Decode(&req); decErr != nil || req.MaxKeys == nil || *req.MaxKeys < 0 {
			emitUserLimits(ctx, deps, audit.OutcomeInvalidKeyFormat, actor, reqID, email)
			render.Error(w, http.StatusBadRequest, "invalid_argument", limitsInvalidMsg, reqID)
			return
		}

		if err := deps.SetMaxKeys(ctx, email, *req.MaxKeys); err != nil {
			if deps.Logger != nil {
				deps.Logger.Error("admin.user-limits: set max keys", "email", email, "err", err)
			}
			emitUserLimits(ctx, deps, audit.OutcomeInternalError, actor, reqID, email)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		emitUserLimits(ctx, deps, audit.OutcomeUpdated, actor, reqID, email)
		w.WriteHeader(http.StatusNoContent)
	}
}

func emitUserLimits(ctx context.Context, deps Deps, outcome, actor, reqID, email string) {
	if deps.Audit == nil {
		return
	}
	audit.EmitAudit(ctx, deps.Audit, audit.Event{
		Action: audit.ActionAdminUserLimits, Outcome: outcome,
		Actor: actor, RequestID: reqID,
		Target: &audit.Target{Kind: "user", Name: email},
	})
}
