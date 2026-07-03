// SPDX-License-Identifier: Apache-2.0

package environments

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	"github.com/ackstorm/ach/internal/platformapi/store"
	achteams "github.com/ackstorm/ach/internal/platformapi/teams"
)

// Deps is the dependency bag the chi server (Plan 03-11) constructs and
// hands to ListHandler / GetHandler via the Mount entrypoint. The shape
// mirrors hydrate.Deps (Plan 03-09 sibling) so the wire-up in main.go is
// uniform across the two handler packages.
//
// Field discipline:
//
//   - Store is the informer-backed Environment reader (Plan 03-06). Reads
//     are served from the controller-runtime cache; sub-millisecond.
//   - LiteLLM is the LiteLLM REST client. WARN-06 — used exclusively by
//     internal/platformapi/teams.LookupCallerTeams for the team-intersection
//     filter on non-admin callers; the handler does not call any other
//     LiteLLM method directly.
//   - Audit is the audit slog logger (audit.NewLogger result). Used only on
//     litellm-unreachable / internal_error paths — environment listing is
//     read-only and emits no audit per OBS-01.
type Deps struct {
	Store   *store.Store
	LiteLLM litellm.Client
	Audit   *slog.Logger
}

// ListHandler returns GET /platform/environments. Filters by team
// intersection unless caller is admin; paginates via ?limit + ?cursor.
//
// Error matrix (Hub §15.5):
//
//   - 400 invalid_argument        — bad ?limit / ?cursor
//   - 401 invalid_key_type        — ek_ caller (management endpoint per API-11)
//   - 503 litellm_unreachable     — teams.LookupCallerTeams transport error
//   - 500 internal_error          — Store read failure
//   - 200 OK                       — { items: [<EnvironmentView>], next_cursor: <string or nil> }
func ListHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)

		keyCtx, ok := middleware.KeyContextFromCtx(ctx)
		if !ok {
			// Defensive: Authn middleware must have run before this handler.
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "auth context missing", reqID)
			return
		}

		// Caller-type guard: pk_ only (management endpoint per API-11).
		if keyCtx.KeyType != keys.PrefixPk {
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType,
				"environments endpoints require pk_", reqID)
			return
		}

		// Hub §15.5 wire code is "invalid_argument" — not in the
		// audit.Outcome* closed enum (no dedicated constant), so the
		// literal string is used at the §15.5 envelope. See
		// .planning/phases/03-hub-identity-platform-api/03-CONTEXT.md
		// API-12 for the envelope vocabulary boundary.
		limit, offset, ok := render.PageParams(w, r, reqID)
		if !ok {
			return
		}

		// Determine caller teams (only when non-admin; admin sees all so the
		// LiteLLM call is unnecessary).
		var callerTeams []string
		isAdmin := keyCtx.IsAdmin
		if !isAdmin {
			teams, err := achteams.LookupCallerTeams(ctx, deps.LiteLLM, keyCtx.OwnerEmail)
			if err != nil {
				if deps.Audit != nil {
					audit.EmitAudit(ctx, deps.Audit, audit.Event{
						Action:    "platform.environments.list",
						Outcome:   audit.OutcomeLitellmUnreachable,
						Actor:     middleware.ActorFromCtx(ctx),
						RequestID: reqID,
					})
				}
				render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable,
					"upstream LiteLLM unreachable", reqID)
				return
			}
			callerTeams = teams
		}

		envs, err := deps.Store.ListAuthorizedEnvironments(ctx, callerTeams, isAdmin)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to list environments", reqID)
			return
		}

		page, nextCursor := render.PageWindow(envs, offset, limit)
		items := make([]store.EnvironmentView, 0, len(page))
		for _, row := range page {
			items = append(items, store.RowToView(row))
		}

		render.JSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": nextCursor,
		})
	}
}

// GetHandler returns GET /platform/environments/{name}. Same filtering
// discipline as ListHandler: non-admin callers must intersect at least one
// of the env's authorizedTeams.
//
// Error matrix (Hub §15.5 / API-08):
//
//   - 400 invalid_argument           — empty {name}
//   - 401 invalid_key_type           — ek_ caller
//   - 403 unauthorized_team          — non-admin caller without team intersection
//   - 404 environment_not_found      — env absent
//   - 503 litellm_unreachable        — teams.LookupCallerTeams transport error
//   - 500 internal_error             — Store read failure
//   - 200 OK                          — EnvironmentView
func GetHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)

		keyCtx, ok := middleware.KeyContextFromCtx(ctx)
		if !ok {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "auth context missing", reqID)
			return
		}

		if keyCtx.KeyType != keys.PrefixPk {
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType,
				"environments endpoints require pk_", reqID)
			return
		}

		name := chi.URLParam(r, "name")
		if name == "" {
			render.Error(w, http.StatusBadRequest, "invalid_argument",
				"environment name is required", reqID)
			return
		}

		env, err := deps.Store.GetEnvironment(ctx, name)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read environment", reqID)
			return
		}
		if env == nil {
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound,
				"environment not found", reqID)
			return
		}

		// Admin override BEFORE team lookup — admins see every Environment
		// without the LiteLLM round trip.
		if !keyCtx.IsAdmin {
			teams, err := achteams.LookupCallerTeams(ctx, deps.LiteLLM, keyCtx.OwnerEmail)
			if err != nil {
				if deps.Audit != nil {
					audit.EmitAudit(ctx, deps.Audit, audit.Event{
						Action:    "platform.environments.get",
						Outcome:   audit.OutcomeLitellmUnreachable,
						Actor:     middleware.ActorFromCtx(ctx),
						RequestID: reqID,
						Target:    &audit.Target{Kind: "environment", Name: name},
					})
				}
				render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable,
					"upstream LiteLLM unreachable", reqID)
				return
			}
			if !achteams.HasIntersect(env.AuthorizedTeams, teams) {
				render.Error(w, http.StatusForbidden, audit.OutcomeUnauthorizedTeam,
					"caller is not a member of any authorized team", reqID)
				return
			}
		}

		render.JSON(w, http.StatusOK, store.RowToView(*env))
	}
}
