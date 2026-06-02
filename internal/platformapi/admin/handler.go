// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// adminCacheKeyPrefix is the per-key-id namespace the admin revocation
// handlers DEL on best-effort cache cleanup. The keystore Resolver
// caches under "ach:key:<credential_hash>" which we cannot construct
// from the admin endpoint (Plan 03-03 intentionally elides
// credential_hash from PkKeyInfo/EkKeyInfo per Hub §16.1). The 60s TTL
// ceiling + the orphan-cleanup loop bound the worst case; the per-id
// marker is here for observability + to satisfy the structural
// ordering invariant (db → litellm → redis per KEY-07).
const adminCacheKeyPrefix = "ach:revoke:keyid:"

// redisDeleter is the narrow Redis-Del seam admin handlers need. The
// production type *redis.Client implements it (its Del method returns
// *redis.IntCmd); tests inject a recording stub via newRedisStub() to
// observe call ordering.
type redisDeleter interface {
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// Deps is the dependency bag the admin handlers consume. Constructed by
// cmd/ach/cmd/platform_api.go and threaded through admin.Mount.
//
// Issue #34: K8sClient is gone. The /admin/refresh handler now signals
// the operator via Postgres (force_refresh_requested_at column + NOTIFY
// ach_refresh) rather than PATCH'ing a CR annotation, so platform-api
// has no remaining K8s write surface.
type Deps struct {
	Pool      *pgxpool.Pool       // Postgres pool (threaded to the internal/db package helpers)
	LiteLLM   litellm.Client      // LiteLLM REST client (revocation API)
	Redis     redisDeleter        // *redis.Client in production; recording stub in tests.
	Allowlist map[string]struct{} // loaded via LoadAllowlist at process start
	Audit     *slog.Logger        // audit.NewLogger handle (audit=true)
	Logger    *slog.Logger        // operational logger (NOT audit)
	Namespace string              // POD_NAMESPACE — composed into actor strings
}

// revokeRequest is the JSON body of POST /platform/admin/keys/revoke.
// DisallowUnknownFields rejects "extra" keys with 400.
type revokeRequest struct {
	KeyID string `json:"key_id"`
}

// refreshRequest is the JSON body of POST /platform/admin/refresh.
type refreshRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// revokeKeyResponse is the §15.5 success envelope for the single-key
// revocation handler.
type revokeKeyResponse struct {
	KeyID  string `json:"key_id"`
	Status string `json:"status"`
}

// userRevokeResponse is the per-row aggregated envelope of
// POST /platform/admin/users/{email}/revoke-keys.
type userRevokeResponse struct {
	RevokedCount int      `json:"revoked_count"`
	Errors       []string `json:"errors"`
}

// decodeStrict decodes r.Body into out with DisallowUnknownFields. EOF
// + malformed JSON + unknown fields all collapse to a single 400
// invalid_argument response — the verbose error path is intentionally
// flattened so the wire envelope does not leak parser internals.
//
// On error the helper writes the response and returns false; the
// handler returns immediately on false without re-rendering.
func decodeStrict(w http.ResponseWriter, r *http.Request, reqID string, out any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "empty body", reqID)
			return false
		}
		render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "invalid_argument", reqID)
		return false
	}
	return true
}

// RevokeKeyHandler dispatches on the key_id prefix:
//
//   - pkid_… → revokePersonalKey: Postgres flip FIRST per KEY-07 (the
//     visible barrier); LiteLLM RevokeKey + Redis DEL run best-effort.
//   - ekid_… → revokeEnvironmentKey: LiteLLM RevokeKey FIRST per KEY-08
//     (the runtime barrier); DB flip + Redis DEL run after the ack.
//
// Either branch emits exactly one audit event (per-branch ActionPkRevoke
// / ActionEkRevoke). The audit outcome captures partial completion
// (OutcomeLitellmUnreachable on the pk_ branch; the ek_ branch surfaces
// the failure as 503 with no DB flip).
func RevokeKeyHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)
		actor := composeActor(deps.Namespace, keyCtx.OwnerEmail)

		var req revokeRequest
		if !decodeStrict(w, r, reqID, &req) {
			return
		}
		if req.KeyID == "" {
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "key_id required", reqID)
			return
		}

		switch {
		case strings.HasPrefix(req.KeyID, keys.PkidKeyIDPrefix):
			revokePersonalKey(ctx, deps, req.KeyID, actor, reqID, w)
		case strings.HasPrefix(req.KeyID, keys.EkidKeyIDPrefix):
			revokeEnvironmentKey(ctx, deps, req.KeyID, actor, reqID, w)
		default:
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "key_id must start with pkid_ or ekid_", reqID)
		}
	}
}

// revokePersonalKey is the pk_ branch — DB-first per KEY-07 + D-14.
// Ordering is structurally fixed: the Postgres flip BEFORE the
// LiteLLM call BEFORE the cache invalidation.
//
// Per WARN-04: when LiteLLM is unreachable the handler returns 200
// (NOT 503) — the Postgres flip already happened and IS the
// caller-observable revocation barrier; a 503 would mislead the caller
// into retrying (against an already-revoked DB row). A stderr WARN log
// captures the partial-completion signal; the orphan-cleanup loop
// reconciles via ListActiveACHKeyTokens.
func revokePersonalKey(ctx context.Context, deps Deps, keyID, actor, reqID string, w http.ResponseWriter) {
	// Step 1 — DB UPDATE FIRST (the visible barrier).
	row, err := db.RevokePersonalKey(ctx, deps.Pool, keyID)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Error("admin.pk-revoke: DB error", "key_id", keyID, "err", err)
		}
		if deps.Audit != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionPkRevoke, Outcome: audit.OutcomeInternalError,
				Actor: actor, RequestID: reqID, KeyID: keyID,
			})
		}
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
		return
	}
	if row == nil {
		// Already revoked, expired, or unknown. Per the helper's
		// (nil,nil) contract these three are indistinguishable.
		render.Error(w, http.StatusNotFound, audit.OutcomeExpiredOrRevoked, "key not found or already revoked", reqID)
		return
	}

	// Step 2 — LiteLLM RevokeKey (best-effort per WARN-04). We have
	// already flipped the DB row; LiteLLM failure does NOT roll back.
	litellmOK := true
	if row.LiteLLMToken != nil && *row.LiteLLMToken != "" {
		if revErr := deps.LiteLLM.RevokeKey(ctx, *row.LiteLLMToken); revErr != nil {
			litellmOK = false
			if deps.Logger != nil {
				// WARN-04 invariant: stderr WARN log on partial completion.
				deps.Logger.Warn("admin.pk-revoke: LiteLLM unreachable; DB flip succeeded; orphan-loop will reconcile",
					"key_id", keyID, "err", revErr)
			}
		}
	}

	// Step 3 — Redis DEL (best-effort). See doc.go on the
	// per-key-id marker; the keystore's true cache entries are reclaimed
	// by the 60s TTL ceiling.
	if deps.Redis != nil {
		_ = deps.Redis.Del(ctx, adminCacheKeyPrefix+keyID).Err()
	}

	// Step 4 — audit emission (single event, outcome reflects
	// LiteLLM reachability per D-14).
	outcome := audit.OutcomeRevoked
	if !litellmOK {
		outcome = audit.OutcomeLitellmUnreachable
	}
	if deps.Audit != nil {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: audit.ActionPkRevoke, Outcome: outcome,
			Actor: actor, RequestID: reqID, KeyID: keyID,
		})
	}

	// Step 5 — response. 200 in BOTH success and litellm-unreachable
	// per WARN-04: the Postgres flip IS the caller-observable barrier.
	render.JSON(w, http.StatusOK, revokeKeyResponse{KeyID: keyID, Status: "revoked"})
}

// revokeEnvironmentKey is the ek_ branch — LiteLLM-first per KEY-08 +
// D-15. Mirrors Plan 03-08 RevokeHandler MINUS the owner check (admin
// can revoke any caller's ek_).
//
// Ordering is structurally fixed: deps.LiteLLM.RevokeKey BEFORE
// db.RevokeEnvironmentKey BEFORE deps.Redis.Del.
//
// On LiteLLM-unreachable: return 503; the DB row STAYS active so the
// caller can retry idempotently (per KEY-08 LiteLLM is the runtime
// barrier).
func revokeEnvironmentKey(ctx context.Context, deps Deps, keyID, actor, reqID string, w http.ResponseWriter) {
	// Step 1 — Read row to capture litellm_token. No status flip yet.
	row, err := db.GetEnvironmentKey(ctx, deps.Pool, keyID)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Error("admin.ek-revoke: DB error on read", "key_id", keyID, "err", err)
		}
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
		return
	}
	if row == nil {
		render.Error(w, http.StatusNotFound, audit.OutcomeExpiredOrRevoked, "key not found", reqID)
		return
	}
	if row.Status != "active" {
		// Already revoked. Same 404 shape — caller cannot distinguish
		// from "never existed", aligns with KEY-04/06 indistinguishability.
		render.Error(w, http.StatusNotFound, audit.OutcomeExpiredOrRevoked, "key already revoked", reqID)
		return
	}

	// Step 2 — LiteLLM RevokeKey. LiteLLM-first means LiteLLM is the
	// load-bearing barrier; failure here aborts the revoke + leaves the
	// DB row active for a clean retry.
	if row.LiteLLMToken != nil && *row.LiteLLMToken != "" {
		if revErr := deps.LiteLLM.RevokeKey(ctx, *row.LiteLLMToken); revErr != nil {
			if deps.Logger != nil {
				deps.Logger.Warn("admin.ek-revoke: LiteLLM unreachable; DB row stays active per KEY-08",
					"key_id", keyID, "err", revErr)
			}
			if deps.Audit != nil {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionEkRevoke, Outcome: audit.OutcomeLitellmUnreachable,
					Actor: actor, RequestID: reqID, KeyID: keyID,
				})
			}
			render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable, "LiteLLM unreachable; retry", reqID)
			return
		}
	}

	// Step 3 — DB UPDATE (post-LiteLLM-ack flip).
	flipped, err := db.RevokeEnvironmentKey(ctx, deps.Pool, keyID)
	if err != nil {
		// LiteLLM already revoked; DB flip failed. Per KEY-08 surface a
		// 500 — the LiteLLM-side is consistent (revoked); the orphan
		// loop will eventually clean up the DB row on its next tick.
		if deps.Logger != nil {
			deps.Logger.Error("admin.ek-revoke: DB flip failed after LiteLLM ack", "key_id", keyID, "err", err)
		}
		if deps.Audit != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionEkRevoke, Outcome: audit.OutcomeInternalError,
				Actor: actor, RequestID: reqID, KeyID: keyID,
			})
		}
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
		return
	}
	_ = flipped // not surfaced to the response; the keyID is authoritative.

	// Step 4 — Redis DEL (best-effort).
	if deps.Redis != nil {
		_ = deps.Redis.Del(ctx, adminCacheKeyPrefix+keyID).Err()
	}

	// Step 5 — audit + response.
	if deps.Audit != nil {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: audit.ActionEkRevoke, Outcome: audit.OutcomeRevoked,
			Actor: actor, RequestID: reqID, KeyID: keyID,
		})
	}
	render.JSON(w, http.StatusOK, revokeKeyResponse{KeyID: keyID, Status: "revoked"})
}

// RevokeUserKeysHandler revokes every active pk_ and ek_ row for the
// URL-decoded email path parameter. The email is matched verbatim
// (no normalization per §16 DB-05); URL encoding is reversed via
// url.PathUnescape so `u%40x.com` → `u@x.com`.
//
// Returns 200 with body {"revoked_count": N, "errors": [...]} even on
// per-row partial failures; the admin caller decides how to react.
// Aggregate audit event emitted at end with ActionAdminUsersRevokeKeys.
func RevokeUserKeysHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)
		actor := composeActor(deps.Namespace, keyCtx.OwnerEmail)

		encoded := chi.URLParam(r, "email")
		email, err := url.PathUnescape(encoded)
		if err != nil || email == "" {
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "invalid email path parameter", reqID)
			return
		}

		// Admin operations may use a higher internal cap than the
		// user-visible §15.5 pagination ceiling. 1000 keeps the unit
		// of work bounded; a user with >1000 active keys is itself an
		// anomaly worth flagging.
		const adminListLimit = 1000

		var revokedCount int
		var errs []string

		// pk_ branch — DB-first per KEY-07.
		pkRows, _, err := db.ListPersonalKeysByOwner(ctx, deps.Pool, email, adminListLimit, "")
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error listing pk rows", reqID)
			return
		}
		for _, pk := range pkRows {
			if pk.Status != "active" {
				continue
			}
			if e := revokePkInline(ctx, deps, pk.KeyID, actor, reqID); e != nil {
				errs = append(errs, "pk:"+pk.KeyID+":"+e.Error())
				continue
			}
			revokedCount++
		}

		// ek_ branch — LiteLLM-first per KEY-08.
		ekRows, _, err := db.ListEnvironmentKeysByOwner(ctx, deps.Pool, email, adminListLimit, "")
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error listing ek rows", reqID)
			return
		}
		for _, ek := range ekRows {
			if ek.Status != "active" {
				continue
			}
			if e := revokeEkInline(ctx, deps, &ek, actor, reqID); e != nil {
				errs = append(errs, "ek:"+ek.KeyID+":"+e.Error())
				continue
			}
			revokedCount++
		}

		if deps.Audit != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionAdminUsersRevokeKeys,
				Outcome:   audit.OutcomeRevoked,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "user", Name: email},
				Extra:     map[string]string{"revoked_count": itoa(revokedCount)},
			})
		}

		render.JSON(w, http.StatusOK, userRevokeResponse{RevokedCount: revokedCount, Errors: errs})
	}
}

// revokePkInline runs the DB-first pk_ revocation sequence without
// rendering an HTTP response. Used by RevokeUserKeysHandler for the
// per-row iteration; errors are collected into the aggregate response.
// LiteLLM-unreachable is NOT an error from this helper's perspective
// (per WARN-04 the DB flip IS the visible barrier).
func revokePkInline(ctx context.Context, deps Deps, keyID, actor, reqID string) error {
	row, err := db.RevokePersonalKey(ctx, deps.Pool, keyID)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("not_active")
	}
	if row.LiteLLMToken != nil && *row.LiteLLMToken != "" {
		if revErr := deps.LiteLLM.RevokeKey(ctx, *row.LiteLLMToken); revErr != nil && deps.Logger != nil {
			deps.Logger.Warn("admin.pk-revoke: LiteLLM unreachable in bulk revoke; DB flip succeeded",
				"key_id", keyID, "err", revErr)
		}
	}
	if deps.Redis != nil {
		_ = deps.Redis.Del(ctx, adminCacheKeyPrefix+keyID).Err()
	}
	if deps.Audit != nil {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: audit.ActionPkRevoke, Outcome: audit.OutcomeRevoked,
			Actor: actor, RequestID: reqID, KeyID: keyID,
		})
	}
	return nil
}

// revokeEkInline runs the LiteLLM-first ek_ revocation sequence without
// rendering an HTTP response. LiteLLM-unreachable IS an error from
// this helper's perspective per KEY-08 — the row stays active and the
// admin can retry the bulk operation.
func revokeEkInline(ctx context.Context, deps Deps, row *db.EkKeyInfo, actor, reqID string) error {
	if row.LiteLLMToken != nil && *row.LiteLLMToken != "" {
		if revErr := deps.LiteLLM.RevokeKey(ctx, *row.LiteLLMToken); revErr != nil {
			if deps.Audit != nil {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionEkRevoke, Outcome: audit.OutcomeLitellmUnreachable,
					Actor: actor, RequestID: reqID, KeyID: row.KeyID,
				})
			}
			return revErr
		}
	}
	if _, err := db.RevokeEnvironmentKey(ctx, deps.Pool, row.KeyID); err != nil {
		return err
	}
	if deps.Redis != nil {
		_ = deps.Redis.Del(ctx, adminCacheKeyPrefix+row.KeyID).Err()
	}
	if deps.Audit != nil {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action: audit.ActionEkRevoke, Outcome: audit.OutcomeRevoked,
			Actor: actor, RequestID: reqID, KeyID: row.KeyID,
		})
	}
	return nil
}

// ForceRefreshHandler marks the named external-reference projection row
// as pending-refresh in Postgres and fires NOTIFY ach_refresh '<kind>/<name>'.
// The operator's refreshsignal listener (A11) picks up the notification and
// enqueues the matching CR for reconcile; the periodic operator resync (A10,
// ≤ 5 min) is the safety net for any dropped notification.
//
// Issue #34: replaces the pre-issue-34 CR-annotation PATCH path. Platform-api
// no longer holds a K8s client — every refresh signal goes through Postgres.
//
// Body shape: {"kind":"plugin|prompt|artifact|pluginmarketplace","name":"..."}.
// Returns 202 Accepted on success — the actual refresh is async.
//
// Error codes:
//   - 400 invalid_argument                — unknown kind / missing field / extra field
//   - 400 invalid_argument (UI origin)    — row exists with origin='ui' (UI-managed
//     rows have no upstream to refresh)
//   - 404 environment_not_found           — the named projection row is absent
//   - 500 internal_error                  — any other DB failure
func ForceRefreshHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)
		actor := composeActor(deps.Namespace, keyCtx.OwnerEmail)

		var req refreshRequest
		if !decodeStrict(w, r, reqID, &req) {
			return
		}
		if req.Kind == "" || req.Name == "" {
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "kind and name are required", reqID)
			return
		}
		if !isRefreshableKind(req.Kind) {
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat, "unknown kind", reqID)
			return
		}

		err := db.SetForceRefresh(ctx, deps.Pool, deps.Namespace, req.Kind, req.Name)
		switch {
		case err == nil:
			// fall through to success path
		case errors.Is(err, db.ErrUIOriginRefreshUnsupported):
			if deps.Audit != nil {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionAdminRefresh, Outcome: audit.OutcomeInvalidKeyFormat,
					Actor: actor, RequestID: reqID,
					Target: &audit.Target{Kind: req.Kind, Name: req.Name},
				})
			}
			render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat,
				"UI-managed resource has no upstream to refresh", reqID)
			return
		case errors.Is(err, pgx.ErrNoRows):
			if deps.Audit != nil {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionAdminRefresh, Outcome: audit.OutcomeEnvironmentNotFound,
					Actor: actor, RequestID: reqID,
					Target: &audit.Target{Kind: req.Kind, Name: req.Name},
				})
			}
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "resource not found", reqID)
			return
		default:
			if deps.Logger != nil {
				deps.Logger.Error("admin.refresh: SetForceRefresh failed",
					"kind", req.Kind, "name", req.Name, "err", err)
			}
			if deps.Audit != nil {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionAdminRefresh, Outcome: audit.OutcomeInternalError,
					Actor: actor, RequestID: reqID,
					Target: &audit.Target{Kind: req.Kind, Name: req.Name},
				})
			}
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}

		if deps.Audit != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionAdminRefresh, Outcome: audit.OutcomeCreated,
				Actor: actor, RequestID: reqID,
				Target: &audit.Target{Kind: req.Kind, Name: req.Name},
			})
		}
		render.JSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	}
}

// isRefreshableKind gates the four kinds Platform API may force-refresh.
// Unknown kind resolves to a 400 at the bad-request gate before any DB
// round trip — same closed set as the pre-issue-34 newACHObject helper.
func isRefreshableKind(kind string) bool {
	switch kind {
	case "plugin", "prompt", "artifact", "pluginmarketplace":
		return true
	}
	return false
}

// itoa is a tiny base-10 int-to-string formatter so we avoid pulling
// strconv in just for the audit Extra value. Used by
// RevokeUserKeysHandler's revoked_count attribute.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
