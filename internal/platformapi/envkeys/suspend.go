// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	achteams "github.com/ackstorm/ach/internal/platformapi/teams"
)

// SuspendHandler / ResumeHandler: POST /platform/keys/{key_id}/{suspend,resume}.
// Suspension lives in ACH only (§8.2): the backing LiteLLM key is untouched,
// the DB row flips, the resolver cache entry is DEL'd; admission of new
// requests is denied within the 60 s cache ceiling. Resume needs the owner
// to still hold Environment access (§11) and refuses Expired/Revoked (409).

func SuspendHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		row, ok := deps.loadOwnedEk(w, r, audit.ActionEkSuspend)
		if !ok {
			return
		}
		reqID := middleware.RequestIDFromCtx(r.Context())
		switch {
		case row.Status == statusRevoked:
			render.Error(w, http.StatusConflict, "key_revoked", "a revoked key cannot be suspended", reqID)
			return
		case expired(row):
			render.Error(w, http.StatusConflict, "key_expired", "an expired key cannot be suspended", reqID)
			return
		case row.Status == statusSuspended:
			w.WriteHeader(http.StatusNoContent) // idempotent
			return
		}
		flipped, err := deps.DB.SuspendEnvironmentKey(r.Context(), row.KeyID)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if flipped == nil { // lost a race: re-read and answer for the state we found
			deps.conflictFor(w, r, row.KeyID)
			return
		}
		deps.invalidate(r.Context(), row.CredentialHash)
		audit.EmitAudit(r.Context(), deps.Audit, audit.Event{Action: audit.ActionEkSuspend, Outcome: audit.OutcomeSuspended,
			Actor: middleware.ActorFromCtx(r.Context()), RequestID: reqID, KeyID: row.KeyID,
			Target: &audit.Target{Kind: "environment", Name: row.Environment}})
		w.WriteHeader(http.StatusNoContent)
	}
}

func ResumeHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		row, ok := deps.loadOwnedEk(w, r, audit.ActionEkResume)
		if !ok {
			return
		}
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		switch {
		case row.Status == statusRevoked:
			render.Error(w, http.StatusConflict, "key_revoked", "a revoked key cannot be resumed", reqID)
			return
		case expired(row):
			render.Error(w, http.StatusConflict, "key_expired", "an expired key cannot be resumed", reqID)
			return
		case row.Status == statusActive:
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Current access is required to resume (§11): the same rule as create.
		env, err := deps.Store.GetEnvironment(ctx, row.Environment)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if env == nil || env.DeletionTimestamp != nil {
			render.Error(w, http.StatusForbidden, audit.OutcomeUnauthorizedTeam, "environment is not available", reqID)
			return
		}
		kc, _ := middleware.KeyContextFromCtx(ctx)
		if !kc.IsAdmin {
			teams, err := achteams.LookupCallerTeams(ctx, deps.LiteLLM, row.OwnerEmail)
			if err != nil {
				render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable, "upstream LiteLLM unreachable", reqID)
				return
			}
			if !achteams.HasIntersect(env.AuthorizedTeams, teams) {
				render.Error(w, http.StatusForbidden, audit.OutcomeUnauthorizedTeam, "owner no longer has access to this environment", reqID)
				return
			}
		}
		flipped, err := deps.DB.ResumeEnvironmentKey(ctx, row.KeyID)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if flipped == nil {
			deps.conflictFor(w, r, row.KeyID) // revoked/expired meanwhile: 409, never a resurrection
			return
		}
		deps.invalidate(ctx, row.CredentialHash)
		audit.EmitAudit(ctx, deps.Audit, audit.Event{Action: audit.ActionEkResume, Outcome: audit.OutcomeResumed,
			Actor: middleware.ActorFromCtx(ctx), RequestID: reqID, KeyID: row.KeyID,
			Target: &audit.Target{Kind: "environment", Name: row.Environment}})
		w.WriteHeader(http.StatusNoContent)
	}
}

// expired reports whether an ek_ row's optional expires_at has passed.
func expired(row *db.EkKeyInfo) bool {
	return row.ExpiresAt != nil && !time.Now().Before(*row.ExpiresAt)
}

// loadOwnedEk: pk_ only, ekid_ prefix gate, read, owner-or-admin — the
// shared head of revoke/suspend/resume (mirrors the former
// revokeEnvironmentKey steps 1-3, generalized with the caller's audit
// Action so each route's rejection events carry their own action).
func (deps Deps) loadOwnedEk(w http.ResponseWriter, r *http.Request, action string) (*db.EkKeyInfo, bool) {
	ctx := r.Context()
	reqID := middleware.RequestIDFromCtx(ctx)
	actor := middleware.ActorFromCtx(ctx)
	keyCtx, _ := middleware.KeyContextFromCtx(ctx)

	// Caller-type guard.
	if keyCtx.KeyType != keys.PrefixPk {
		render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "ek_ may not manage env-keys", reqID)
		return nil, false
	}

	// ekid_ prefix gate BEFORE the DB lookup (T-03-08-03).
	keyID := chi.URLParam(r, "key_id")
	if !strings.HasPrefix(keyID, keys.EkidKeyIDPrefix) {
		render.Error(w, http.StatusBadRequest, codeInvalidArgument,
			"key_id must start with "+keys.EkidKeyIDPrefix, reqID)
		return nil, false
	}

	row, err := deps.DB.GetEnvironmentKey(ctx, keyID)
	if err != nil {
		deps.Logger.Error("envkeys: GetEnvironmentKey failed", "key_id", keyID, "err", err)
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: action, Outcome: audit.OutcomeInternalError,
			Actor: actor, RequestID: reqID, KeyID: keyID,
		})
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
		return nil, false
	}
	if row == nil {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: action, Outcome: audit.OutcomeEnvironmentNotFound,
			Actor: actor, RequestID: reqID, KeyID: keyID,
		})
		render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "key not found", reqID)
		return nil, false
	}

	if row.OwnerEmail != keyCtx.OwnerEmail && !keyCtx.IsAdmin {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: action, Outcome: audit.OutcomeNotKeyOwner,
			Actor: actor, RequestID: reqID, KeyID: keyID,
			Target: &audit.Target{Kind: "environment", Name: row.Environment},
		})
		render.Error(w, http.StatusForbidden, audit.OutcomeNotKeyOwner, "caller does not own this key", reqID)
		return nil, false
	}

	return row, true
}

// conflictFor re-reads after a zero-row UPDATE and maps the state found.
func (deps Deps) conflictFor(w http.ResponseWriter, r *http.Request, keyID string) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	row, err := deps.DB.GetEnvironmentKey(r.Context(), keyID)
	switch {
	case err != nil || row == nil:
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
	case row.Status == statusRevoked:
		render.Error(w, http.StatusConflict, "key_revoked", "key was revoked", reqID)
	case expired(row):
		render.Error(w, http.StatusConflict, "key_expired", "key has expired", reqID)
	default:
		w.WriteHeader(http.StatusNoContent) // already in the requested state
	}
}

// invalidate DELs the keystore resolver cache entry for a credential hash
// (§8.4/§8.2). Best-effort: the 60 s cache TTL bounds the worst case.
func (deps Deps) invalidate(ctx context.Context, credentialHash string) {
	if err := deps.Redis.Del(ctx, "ach:key:"+credentialHash); err != nil {
		deps.Logger.Warn("envkeys: Redis DEL failed (60s TTL is the worst case bound)", "err", err)
	}
}
