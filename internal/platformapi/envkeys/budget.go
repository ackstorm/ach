// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"encoding/json"
	"net/http"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// BudgetRequest is the spend ceiling a caller sets on one ek_, at create
// time (CreateRequest.Budget) or later through PATCH. MaxBudget is a
// pointer so an omitted field is told apart from an explicit 0 (which is a
// legitimate ceiling: refuse everything).
type BudgetRequest struct {
	MaxBudget      *float64 `json:"max_budget"`
	BudgetDuration string   `json:"budget_duration,omitempty"`
}

// tagBudget converts a validated request into the LiteLLM wire shape.
// BudgetDuration is passed through verbatim — LiteLLM validates the format.
func (b BudgetRequest) tagBudget() litellm.TagBudget {
	return litellm.TagBudget{MaxBudget: *b.MaxBudget, BudgetDuration: b.BudgetDuration}
}

// decodeBudget reads a BudgetRequest and enforces the one rule ACH owns:
// max_budget present and >= 0.
func decodeBudget(r *http.Request) (BudgetRequest, bool) {
	var req BudgetRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, false
	}
	if req.MaxBudget == nil || *req.MaxBudget < 0 {
		return req, false
	}
	return req, true
}

// BudgetHandler serves PATCH /platform/keys/{key_id}/budget: replace this
// ek_'s own spend ceiling. Owner-scoped like suspend/resume (loadOwnedEk).
// The ceiling lives on the LiteLLM "key:<id>" tag — ACH stores no budget of
// its own — so the change takes effect within LiteLLM's tag cache (~10s),
// not instantly. 400 invalid_argument on a negative or missing max_budget;
// 404 on an unknown key; 409 on a revoked one (a revoked key has no tag).
func BudgetHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		row, ok := deps.loadOwnedEk(w, r, audit.ActionEkBudget)
		if !ok {
			return
		}
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		if row.Status == statusRevoked {
			render.Error(w, http.StatusConflict, "key_revoked", "a revoked key has no budget", reqID)
			return
		}
		req, valid := decodeBudget(r)
		if !valid {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument, "max_budget required and must be >= 0", reqID)
			return
		}
		tag := litellm.KeyBudgetTag(row.KeyID)
		if err := deps.LiteLLM.UpsertTagBudget(ctx, tag, req.tagBudget()); err != nil {
			deps.Logger.Error("envkeys.budget: key budget tag", "key_id", row.KeyID, "err", err)
			st, oc, msg := classifyLitellmErr(err)
			render.Error(w, st, oc, msg, reqID)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
