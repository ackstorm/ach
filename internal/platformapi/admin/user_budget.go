// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/envkeys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// UserBudgetHandler serves PATCH /platform/admin/users/{email}/budget:
// replace one person's whole-footprint spend ceiling. The ceiling lives on
// the LiteLLM "user:<email>" tag the forwarder stamps on every authenticated
// request that person makes, so it caps their pk_ AND every ek_ they own,
// across Environments (references/litellm-permission-model.md §15). ACH
// stores no budget of its own; the change takes effect within LiteLLM's tag
// cache (~10s), not instantly.
//
// Before this route the only ACH-side lever was the chart-wide default
// (ACH_USER_MAX_BUDGET), applied at provision — it caps new users and cannot
// retune an existing one.
//
// The body, its one rule (max_budget present and >= 0; 0 is a real "refuse
// everything" ceiling) and the 400 envelope are the ek_ route's, verbatim
// via envkeys.BudgetFromRequest.
//
// A user LiteLLM has never seen is NOT an error: UpsertTagBudget creates the
// budget object and binds the tag. Tag rows are ACH metadata that LiteLLM
// also auto-creates from traffic, so their presence says nothing about
// whether the person exists — gating on it would only mean an admin cannot
// cap someone until after their first request.
//
// Admin gating is the subtree's AdminOnly middleware (401 invalid_key_type
// for an ek_, 403 not_admin otherwise), which emits its own denial audit.
func UserBudgetHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)
		actor := composeActor(deps.Namespace, keyCtx.OwnerEmail)

		// Same decode as the revoke-keys route: `u%40x.com` → `u@x.com`
		// (chi also routes an unescaped '@' fine — both spellings arrive here).
		email, err := url.PathUnescape(chi.URLParam(r, "email"))
		if err != nil || email == "" {
			emitUserBudget(ctx, deps, audit.OutcomeInvalidKeyFormat, actor, reqID, email)
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "invalid email path parameter", reqID)
			return
		}
		req, ok := envkeys.BudgetFromRequest(w, r, reqID)
		if !ok {
			emitUserBudget(ctx, deps, audit.OutcomeInvalidKeyFormat, actor, reqID, email)
			return
		}
		// litellm.UserBudgetTag owns the tag name (it normalizes the email);
		// never hand-roll "user:" + email.
		tag := litellm.UserBudgetTag(email)
		if err := deps.LiteLLM.UpsertTagBudget(ctx, tag, req.TagBudget()); err != nil {
			if deps.Logger != nil {
				deps.Logger.Error("admin.user-budget: upsert tag budget", "tag", tag, "err", err)
			}
			st, oc, msg := envkeys.ClassifyLitellmErr(err)
			emitUserBudget(ctx, deps, oc, actor, reqID, email)
			render.Error(w, st, oc, msg, reqID)
			return
		}
		emitUserBudget(ctx, deps, audit.OutcomeUpdated, actor, reqID, email)
		w.WriteHeader(http.StatusNoContent)
	}
}

// emitUserBudget writes the one audit event this route emits per request —
// success and refusal alike, so an auditor sees who retuned whose ceiling
// and who tried to. The amount is never logged (§9.1); the target is.
func emitUserBudget(ctx context.Context, deps Deps, outcome, actor, reqID, email string) {
	if deps.Audit == nil {
		return
	}
	audit.EmitAudit(ctx, deps.Audit, audit.Event{
		Action: audit.ActionAdminUserBudget, Outcome: outcome,
		Actor: actor, RequestID: reqID,
		Target: &audit.Target{Kind: "user", Name: email},
	})
}
