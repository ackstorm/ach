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
	"strconv"
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
//     visible barrier); LiteLLM RevokeKey runs best-effort.
//   - ekid_… → revokeEnvironmentKey: LiteLLM RevokeKey FIRST per KEY-08
//     (the runtime barrier); DB flip runs after the ack.
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

// revokePersonalKey is the pk_ rendering handler — DB-first per KEY-07 +
// D-14. The side-effect ordering (Postgres flip FIRST, then best-effort
// LiteLLM RevokeKey) lives in revokePkInline so the single and bulk
// paths share ONE ordering implementation; this handler owns only the
// HTTP rendering + its OWN audit outcome policy.
//
// Per WARN-04: when LiteLLM is unreachable the handler returns 200
// (NOT 503) — the Postgres flip already happened and IS the
// caller-observable revocation barrier; a 503 would mislead the caller
// into retrying (against an already-revoked DB row). A stderr WARN log
// captures the partial-completion signal; the orphan-cleanup loop
// reconciles via ListActiveACHKeyIDs.
//
// Audit (reachability-aware, per D-14): OutcomeLitellmUnreachable when
// the best-effort LiteLLM call failed, else OutcomeRevoked. This is the
// DIVERGENT half of DUP-3 — the bulk caller always emits OutcomeRevoked.
func revokePersonalKey(ctx context.Context, deps Deps, keyID, actor, reqID string, w http.ResponseWriter) {
	litellmOK, err := revokePkInline(ctx, deps, keyID)
	if err != nil {
		if errors.Is(err, errKeyNotActive) {
			// Already revoked, expired, or unknown. Per the helper's
			// (nil,nil) DB contract these three are indistinguishable.
			render.Error(w, http.StatusNotFound, audit.OutcomeExpiredOrRevoked, "key not found or already revoked", reqID)
			return
		}
		// DB failure on the flip itself.
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

	// Audit emission (single event, outcome reflects LiteLLM
	// reachability per D-14).
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

	// Response. 200 in BOTH success and litellm-unreachable per WARN-04:
	// the Postgres flip IS the caller-observable barrier.
	render.JSON(w, http.StatusOK, revokeKeyResponse{KeyID: keyID, Status: "revoked"})
}

// revokeEnvironmentKey is the ek_ branch — LiteLLM-first per KEY-08 +
// D-15. Mirrors Plan 03-08 RevokeHandler MINUS the owner check (admin
// can revoke any caller's ek_).
//
// Ordering is structurally fixed: deps.LiteLLM.RevokeKey BEFORE
// db.RevokeEnvironmentKey.
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

	// Steps 2-3 — LiteLLM-first side effects + audit live in
	// revokeEkInline (shared with the bulk path). It emits its OWN audit:
	// OutcomeLitellmUnreachable on LiteLLM failure (errLitellmUnreachable
	// sentinel) and OutcomeRevoked on success. The ONLY case it does NOT
	// audit is a post-ack DB-flip failure (bare DB error) — the handler
	// owns that OutcomeInternalError emission below, so there is no
	// double-emit.
	if err := revokeEkInline(ctx, deps, row, actor, reqID); err != nil {
		if errors.Is(err, errLitellmUnreachable) {
			// LiteLLM is the load-bearing barrier; failure aborts the
			// revoke + leaves the DB row active for a clean retry (KEY-08).
			// Audit (OutcomeLitellmUnreachable) already emitted by the helper.
			if deps.Logger != nil {
				deps.Logger.Warn("admin.ek-revoke: LiteLLM unreachable; DB row stays active per KEY-08",
					"key_id", keyID, "err", err)
			}
			render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable, "LiteLLM unreachable; retry", reqID)
			return
		}
		// LiteLLM already revoked; DB flip failed. Per KEY-08 surface a
		// 500 — the LiteLLM-side is consistent (revoked); the orphan
		// loop will eventually clean up the DB row on its next tick. The
		// helper did NOT audit this case, so the handler emits it here.
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

	// Success — OutcomeRevoked audit already emitted by revokeEkInline.
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
			// Bulk pk_ audit policy DIVERGES from the single handler:
			// OutcomeRevoked is emitted UNCONDITIONALLY (litellmOK ignored)
			// — the DB flip is the visible barrier and the aggregate event
			// does not surface per-row LiteLLM reachability.
			if _, e := revokePkInline(ctx, deps, pk.KeyID); e != nil {
				errs = append(errs, "pk:"+pk.KeyID+":"+e.Error())
				continue
			}
			if deps.Audit != nil {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionPkRevoke, Outcome: audit.OutcomeRevoked,
					Actor: actor, RequestID: reqID, KeyID: pk.KeyID,
				})
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
			// Bulk ek_ audit: revokeEkInline emits BOTH OutcomeLitellmUnreachable
			// (on LiteLLM fail) and OutcomeRevoked (on success) itself — do NOT add
			// an audit block here; it would double-emit (DUP-3 divergence guard).
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
				Extra:     map[string]string{"revoked_count": strconv.Itoa(revokedCount)},
			})
		}

		render.JSON(w, http.StatusOK, userRevokeResponse{RevokedCount: revokedCount, Errors: errs})
	}
}

// errKeyNotActive is returned by revokePkInline when the pk_ DB row was
// nil (already revoked, expired, or unknown — indistinguishable per the
// helper's (nil,nil) contract). Its Error() string ("not_active")
// matches the pre-DUP-3 inline value so the bulk aggregate `errors`
// entry is byte-for-byte unchanged.
var errKeyNotActive = errors.New("not_active")

// errLitellmUnreachable is a sentinel for classification only. It is
// returned by revokeEkInline wrapped around the underlying transport
// error so callers can errors.Is() it (→ 503 in the single handler)
// WITHOUT re-emitting audit. Its Error() string is TRANSPARENT — it
// returns the wrapped error's message verbatim — so the bulk aggregate
// `errors` entry is byte-for-byte unchanged from the pre-DUP-3 path
// (which collected the bare transport error).
var errLitellmUnreachable = errors.New("litellm_unreachable")

// litellmUnreachableErr wraps the transport error from a failed LiteLLM
// RevokeKey. It is errors.Is(errLitellmUnreachable) for classification,
// errors.Is(wrapped) for the underlying cause, and its Error() forwards
// the wrapped message verbatim (no "litellm_unreachable: " prefix) to
// preserve the bulk path's collected error string.
type litellmUnreachableErr struct{ wrapped error }

func (e litellmUnreachableErr) Error() string { return e.wrapped.Error() }

// Unwrap exposes the underlying transport error for errors.As / further unwrapping.
func (e litellmUnreachableErr) Unwrap() error { return e.wrapped }
func (e litellmUnreachableErr) Is(target error) bool {
	return target == errLitellmUnreachable
}

// revokePkInline performs the pk_ revoke side-effects in WARN-04 order:
// DB flip first (the visible barrier), then best-effort LiteLLM RevokeKey.
// Returns (litellmOK, err): err is non-nil only for DB failure / not_active
// (errKeyNotActive); LiteLLM-unreachable is reported via litellmOK=false
// (WARN-04: NOT an error).
//
// It does NOT emit audit — the caller emits with ITS OWN outcome policy so
// the divergent single-vs-bulk audit semantics are preserved verbatim:
// the single rendering handler is reachability-aware (OutcomeLitellmUnreachable
// when !litellmOK, else OutcomeRevoked); the bulk caller emits OutcomeRevoked
// UNCONDITIONALLY (litellmOK ignored). Because audit moved to the callers,
// the helper no longer needs actor/reqID.
func revokePkInline(ctx context.Context, deps Deps, keyID string) (litellmOK bool, err error) {
	row, err := db.RevokePersonalKey(ctx, deps.Pool, keyID)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, errKeyNotActive
	}
	litellmOK = true
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
	return litellmOK, nil
}

// revokeEkInline runs the LiteLLM-first ek_ revocation side-effects and
// emits audit for the cases it covers: OutcomeLitellmUnreachable when
// LiteLLM RevokeKey fails (returns errLitellmUnreachable wrapping the
// transport error; the row stays active per KEY-08 so the admin can
// retry) and OutcomeRevoked on success. It does NOT audit a post-ack
// DB-flip failure — it returns the bare DB error and the caller owns
// that OutcomeInternalError emission (so the single rendering handler
// can distinguish 503 vs 500 without a double-emit).
func revokeEkInline(ctx context.Context, deps Deps, row *db.EkKeyInfo, actor, reqID string) error {
	if row.LiteLLMToken != nil && *row.LiteLLMToken != "" {
		if revErr := deps.LiteLLM.RevokeKey(ctx, *row.LiteLLMToken); revErr != nil {
			if deps.Audit != nil {
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action: audit.ActionEkRevoke, Outcome: audit.OutcomeLitellmUnreachable,
					Actor: actor, RequestID: reqID, KeyID: row.KeyID,
				})
			}
			return litellmUnreachableErr{wrapped: revErr}
		}
	}
	if _, err := db.RevokeEnvironmentKey(ctx, deps.Pool, row.KeyID); err != nil {
		return err
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

// --------------------------------------------------------------------------
// ListKeysHandler — GET /platform/admin/keys
// --------------------------------------------------------------------------

// adminKeyListItem is the secret-free wire projection of one key returned by
// ListKeysHandler. It mirrors keyListItemWire in the envkeys package to avoid
// a cross-package import between sibling packages; the ~15 lines are duplicated
// here intentionally (both packages own their own HTTP surface).
type adminKeyListItem struct {
	KeyID       string  `json:"key_id"`
	Type        string  `json:"type"`
	OwnerEmail  string  `json:"owner_email"`
	Environment string  `json:"environment,omitempty"`
	Name        string  `json:"name,omitempty"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at,omitempty"`
	RevokedAt   *string `json:"revoked_at,omitempty"`
}

// adminDefaultLimit is the default page size for ListKeysHandler.
const adminDefaultLimit = 100

// adminMaxLimit is the hard cap for ListKeysHandler's ?limit parameter.
const adminMaxLimit = 500

// parseAdminLimit parses the ?limit query string into an integer between 1
// and adminMaxLimit, defaulting to adminDefaultLimit on empty input.
func parseAdminLimit(raw string) int {
	if raw == "" {
		return adminDefaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return adminDefaultLimit
	}
	if n > adminMaxLimit {
		return adminMaxLimit
	}
	return n
}

// normalizeAdminKeyType maps the ?type query value to a valid filter string.
// Unknown values normalize to "" (no filter).
func normalizeAdminKeyType(v string) string {
	switch v {
	case "pk", "ek":
		return v
	default:
		return ""
	}
}

// normalizeAdminKeyStatus maps the ?status query value to a valid filter string.
// Unknown values normalize to "" (no filter).
func normalizeAdminKeyStatus(v string) string {
	switch v {
	case "active", "revoked", "expired":
		return v
	default:
		return ""
	}
}

// writeAdminKeyListJSON encodes items + next_cursor as the paginated list envelope.
func writeAdminKeyListJSON(w http.ResponseWriter, items []db.KeyListItem, next string) {
	out := make([]adminKeyListItem, 0, len(items))
	for _, it := range items {
		row := adminKeyListItem{
			KeyID:      it.KeyID,
			Type:       it.Type,
			OwnerEmail: it.OwnerEmail,
			Status:     it.Status,
			CreatedAt:  it.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
		if it.Environment != nil {
			row.Environment = *it.Environment
		}
		if it.Name != nil {
			row.Name = *it.Name
		}
		if it.LastUsedAt != nil {
			s := it.LastUsedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			row.LastUsedAt = &s
		}
		if it.RevokedAt != nil {
			s := it.RevokedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			row.RevokedAt = &s
		}
		out = append(out, row)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "next_cursor": next})
}

// ListKeysHandler serves GET /platform/admin/keys — lists all keys across all
// owners, optionally narrowed by ?owner_email. Gated by AdminOnly middleware.
// Supports the same ?type, ?status, ?environment, ?limit, ?cursor filters as
// the caller-scoped GET /platform/keys (Task 3), plus ?owner_email=<email>.
func ListKeysHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var owner *string
		if v := q.Get("owner_email"); v != "" {
			owner = &v
		}
		f := db.KeyListFilter{
			OwnerEmail:  owner,
			Type:        normalizeAdminKeyType(q.Get("type")),
			Status:      normalizeAdminKeyStatus(q.Get("status")),
			Environment: q.Get("environment"),
		}
		limit := parseAdminLimit(q.Get("limit"))
		items, next, err := db.ListKeys(r.Context(), deps.Pool, f, limit, q.Get("cursor"))
		if err != nil {
			reqID := middleware.RequestIDFromCtx(r.Context())
			render.Error(w, http.StatusInternalServerError, "internal", "list keys failed", reqID)
			return
		}
		writeAdminKeyListJSON(w, items, next)
	}
}

// isRefreshableKind gates the kinds Platform API may force-refresh.
// Unknown kind resolves to a 400 at the bad-request gate before any DB
// round trip — same closed set as the pre-issue-34 newACHObject helper.
// skill / skillmarketplace joined the set in G8 (db.SetForceRefresh routes
// them to external_refs / skill_marketplace_skills respectively).
func isRefreshableKind(kind string) bool {
	switch kind {
	case "plugin", "prompt", "artifact", "pluginmarketplace", "skill", "skillmarketplace":
		return true
	}
	return false
}
