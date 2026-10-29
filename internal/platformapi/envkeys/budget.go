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

// budgetInvalidMsg is the single 400 message for the single rule, shared by
// the create path and PATCH so the two can never drift.
const budgetInvalidMsg = "max_budget required and must be >= 0"

// valid is the one rule ACH owns: max_budget present and >= 0. 0 is a real
// ceiling (refuse everything once any spend lands), which is why MaxBudget
// is a pointer. Everything else (budget_duration's format) is LiteLLM's.
func (b *BudgetRequest) valid() bool {
	return b != nil && b.MaxBudget != nil && *b.MaxBudget >= 0
}

// tagBudget converts a validated request into the LiteLLM wire shape.
// BudgetDuration is passed through verbatim — LiteLLM validates the format.
func (b BudgetRequest) TagBudget() litellm.TagBudget {
	return litellm.TagBudget{MaxBudget: *b.MaxBudget, BudgetDuration: b.BudgetDuration}
}

// BudgetFromRequest decodes and validates a budget body, rendering the
// shared 400 invalid_argument envelope itself on refusal. Both PATCH-budget
// routes go through it — this ek_ one and the admin per-user one
// (PATCH /platform/admin/users/{email}/budget) — so the request shape, the
// one rule and the error envelope can never drift apart.
func BudgetFromRequest(w http.ResponseWriter, r *http.Request, reqID string) (BudgetRequest, bool) {
	req, ok := decodeBudget(r)
	if !ok {
		render.Error(w, http.StatusBadRequest, codeInvalidArgument, budgetInvalidMsg, reqID)
	}
	return req, ok
}

// decodeBudget reads a BudgetRequest and validates it.
func decodeBudget(r *http.Request) (BudgetRequest, bool) {
	var req BudgetRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, false
	}
	return req, req.valid()
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
		req, valid := BudgetFromRequest(w, r, reqID)
		if !valid {
			return
		}
		tag := litellm.KeyBudgetTag(row.KeyID)
		if err := deps.LiteLLM.UpsertTagBudget(ctx, tag, req.TagBudget()); err != nil {
			deps.Logger.Error("envkeys.budget: key budget tag", "key_id", row.KeyID, "err", err)
			st, oc, msg := ClassifyLitellmErr(err)
			render.Error(w, st, oc, msg, reqID)
			return
		}
		// A raised ceiling is exactly the governance mutation an audit log
		// exists for — every sibling verb on /platform/keys emits on success.
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: audit.ActionEkBudget, Outcome: audit.OutcomeUpdated,
			Actor: middleware.ActorFromCtx(ctx), RequestID: reqID, KeyID: row.KeyID,
			Target: &audit.Target{Kind: "environment", Name: row.Environment},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
