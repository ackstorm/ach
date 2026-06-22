// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// codeCannotRevokeActiveKey is the wire error code returned when the caller
// attempts to revoke the key they are currently authenticating with, without
// the force=true override.
const codeCannotRevokeActiveKey = "cannot_revoke_active_key"

// RevokePersonalHandler handles DELETE /platform/keys/{key_id}.
//
// Owner-scoped (NOT admin-gated): the handler revokes only the caller's own
// pk_ key. The DB function (db.RevokePersonalKeyByOwner) enforces ownership;
// any zero-row result (wrong owner, absent key, already revoked) maps to 404
// key_not_found — no existence leak.
//
// Active-key guard: if the target key_id equals the caller's own authenticating
// key id AND the request does not carry ?force=true, 409 is returned so the
// caller must explicitly confirm they want to invalidate their current session.
//
// DB-first ordering (KEY-07): the DB row is flipped before the best-effort
// LiteLLM RevokeKey call. LiteLLM-unreachable still returns 200 — the DB flip
// is the caller-observable revocation barrier.
func RevokePersonalHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		actor := middleware.ActorFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)

		keyID := chi.URLParam(r, "key_id")

		// ekid_ prefix → tell the caller to use the ek_ revoke endpoint.
		if strings.HasPrefix(keyID, keys.EkidKeyIDPrefix) {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"use DELETE /platform/env-keys/{key_id} to revoke ek_ keys", reqID)
			return
		}

		// pkid_ prefix guard — reject anything that is not a pk_ key ID.
		if !strings.HasPrefix(keyID, keys.PkidKeyIDPrefix) {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"key_id must start with "+keys.PkidKeyIDPrefix, reqID)
			return
		}

		// Active-key guard: prevent accidental self-lockout. keyCtx.KeyID is
		// the resolved key id of the bearer used for this request (set by the
		// Authn middleware via middleware.WithKeyContext). If the target is the
		// caller's own active key, require ?force=true.
		if keyCtx.KeyID == keyID && r.URL.Query().Get("force") != "true" {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionPkRevoke, Outcome: codeCannotRevokeActiveKey,
				Actor: actor, RequestID: reqID, KeyID: keyID,
			})
			render.Error(w, http.StatusConflict, codeCannotRevokeActiveKey,
				"cannot revoke the key currently used to authenticate; pass ?force=true to override", reqID)
			return
		}

		// DB-first revocation (KEY-07): flip DB row with owner check enforced
		// by the DB function. ErrKeyNotFoundOrNotOwner → 404 (no existence leak).
		litellmToken, err := deps.DB.RevokePersonalKeyByOwner(ctx, keyID, keyCtx.OwnerEmail)
		if err != nil {
			if errors.Is(err, db.ErrKeyNotFoundOrNotOwner) {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionPkRevoke, Outcome: audit.OutcomeNotKeyOwner,
					Actor: actor, RequestID: reqID, KeyID: keyID,
				})
				render.Error(w, http.StatusNotFound, "key_not_found",
					"key not found or not owned by caller", reqID)
				return
			}
			if deps.Logger != nil {
				deps.Logger.Error("envkeys.revoke-personal: DB error", "key_id", keyID, "err", err)
			}
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionPkRevoke, Outcome: audit.OutcomeInternalError,
				Actor: actor, RequestID: reqID, KeyID: keyID,
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}

		// Best-effort LiteLLM delete (WARN-04): DB flip already happened and IS
		// the caller-observable revocation barrier. LiteLLM-unreachable → 200,
		// outcome OutcomeLitellmUnreachable. Orphan-cleanup loop reconciles.
		litellmOK := true
		if litellmToken != nil && *litellmToken != "" {
			if revErr := deps.LiteLLM.RevokeKey(ctx, *litellmToken); revErr != nil {
				litellmOK = false
				if deps.Logger != nil {
					deps.Logger.Warn("envkeys.revoke-personal: LiteLLM unreachable; DB flip succeeded; orphan-loop will reconcile",
						"key_id", keyID, "err", revErr)
				}
			}
		}

		outcome := audit.OutcomeRevoked
		if !litellmOK {
			outcome = audit.OutcomeLitellmUnreachable
		}
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: audit.ActionPkRevoke, Outcome: outcome,
			Actor: actor, RequestID: reqID, KeyID: keyID,
		})

		render.JSON(w, http.StatusOK, map[string]string{"key_id": keyID, "status": "revoked"})
	}
}
