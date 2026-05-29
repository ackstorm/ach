// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	achteams "github.com/ackstorm/ach/internal/platformapi/teams"
)

// envStore is the read-only Environment-projection seam the envkeys handlers
// consume from internal/platformapi/store.Store. Declared as an interface here
// so tests can inject fakes without standing up a real Postgres (the
// production type *store.Store satisfies it via pointer-receiver methods).
//
// Issue #34: the return type moved from *achv1alpha1.Environment to
// *db.EnvironmentRow when platform-api switched its read path from the
// controller-runtime informer cache to the Postgres projection table.
type envStore interface {
	GetEnvironment(ctx context.Context, name string) (*db.EnvironmentRow, error)
	EnvironmentTerminating(ctx context.Context, name string) (bool, error)
	EnvironmentAccessGroupSynced(ctx context.Context, name string) (bool, error)
}

// dbOps is the set of internal/db helpers the envkeys handlers call. Same
// rationale as envStore — production wires a thin adapter around the
// db.<helper> functions bound to a pgxpool.Pool; tests inject in-memory
// fakes via dbOps.
type dbOps interface {
	InsertEnvironmentKey(ctx context.Context, row db.EkInsertRow) error
	GetEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	RevokeEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	ListEnvironmentKeysByOwner(ctx context.Context, ownerEmail string, limit int, cursor string) ([]db.EkKeyInfo, string, error)
	ListEnvironmentKeysByOwnerWithFilter(ctx context.Context, ownerEmailFilter *string, limit int, cursor string) ([]db.EkKeyInfo, string, error)
}

// redisOps is the subset of go-redis the envkeys handlers exercise. The
// only call site is the §8.5 RevokeHandler cache invalidation DEL.
type redisOps interface {
	Del(ctx context.Context, key string) error
}

// Deps is the dependency bag every envkeys handler accepts. Plan 03-11's
// server.go constructs it once and passes the same value to each handler
// constructor via Mount(deps).
//
// Field semantics:
//
//   - LiteLLM, DB, Store, Redis: small interfaces so handler unit tests
//     can inject fakes without spinning real pgxpool / Redis / envtest /
//     LiteLLM dependencies. Production wires concrete adapters in
//     cmd/platform-api/main.go.
//   - Pepper: HMAC-SHA-256 secret per Hub §16.1; never persisted, never
//     logged. Used in CreateHandler to derive credential_hash from the
//     server-generated plaintext.
//   - Audit: *slog.Logger constructed by audit.NewLogger (the audit=true
//     attribute is already attached). Handlers call audit.EmitAudit on
//     every state-changing branch.
//   - Logger: operational logger (NOT audit). Used for compensation
//     diagnostics and unexpected-error paths.
//   - Namespace: deployment namespace; composed into Actor strings as
//     "<namespace>/<email>" via middleware.ActorFromCtx.
type Deps struct {
	LiteLLM   litellm.Client
	DB        dbOps
	Store     envStore
	Redis     redisOps
	Pepper    []byte
	Audit     *slog.Logger
	Logger    *slog.Logger
	Namespace string
}

// CreateRequest is the POST /platform/env-keys request body shape (D-16
// idiom — DisallowUnknownFields rejects any extra field with 400
// invalid_argument before the §8.2 flow runs).
type CreateRequest struct {
	Environment string `json:"environment"`
	Name        string `json:"name"`
}

// CreateResponse is the §15.5 success-shape body. Plaintext is returned
// EXACTLY ONCE here and never anywhere else.
type CreateResponse struct {
	KeyID       string `json:"key_id"`
	Plaintext   string `json:"plaintext"`
	Environment string `json:"environment"`
	Name        string `json:"name"`
	OwnerEmail  string `json:"owner_email"`
	CreatedAt   string `json:"created_at"`
}

// errInvalidArgument is the §15.5 invalid_argument response code. We
// keep it as a string literal (matches the wire format from Hub §15.5)
// rather than promoting it to the audit.Outcome* enum because
// invalid_argument is not a §18.2 audit outcome — it's purely an HTTP
// envelope code for malformed requests.
const codeInvalidArgument = "invalid_argument"

// defaultTeam is the LiteLLM Team alias every first-SSO user gets
// enrolled into per Hub §17 (deployer concern). When LiteLLM rejects
// the TeamMemberAdd because the default Team does not exist, the
// handler emits OutcomeDefaultTeamMissing.
const defaultTeam = "default"

// CreateHandler returns the §8.2 8-step ek_ create handler.
//
// The 8 steps per Hub §8.2 / Plan 03-08 D-12:
//
//  1. Caller-type guard: only pk_ may create ek_; ek_ → 401.
//  2. Strict JSON decode (DisallowUnknownFields) into CreateRequest;
//     missing fields → 400.
//  3. GetEnvironment from the informer cache; absent or terminating → 404.
//  4. EnvironmentAccessGroupSynced check; not True → 503 not_ready.
//  5. Team-membership intersection: authorizedTeams ∩ caller teams ≠ ∅;
//     empty → 403 unauthorized_team.
//  6. Idempotent LiteLLM user provision: UserInfoByEmail; on absent run
//     UserNew + TeamMemberAdd(default, user_id, "user").
//  7. Generate server-side plaintext (ek_<26>) + key_id (ekid_<26>);
//     hash plaintext with the pepper; call litellm.KeyGenerate with the
//     ACH-supplied Key + AccessGroups=[<env>] + MaxBudget=nil (KEY-10).
//  8. INSERT environment_keys row; on PK collision retry once with a
//     new ekid_ (reusing same plaintext + LiteLLM token per WARN-03);
//     on any other failure run the LiteLLM compensation
//     (RevokeKey on the token under context.Background so caller
//     cancellation doesn't interrupt it) and surface 500
//     db_insert_failed.
//
// On success: 200 with CreateResponse (plaintext exactly once); audit
// ActionEkCreate / OutcomeCreated. Plaintext NEVER flows into logs, audit
// records, response headers, or the DB (only the credhash hex persists).
func CreateHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		actor := middleware.ActorFromCtx(ctx)

		// Step 1: caller-type guard (pk_ only per D-12 step 1 / API-11).
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)
		if keyCtx.KeyType != keys.PrefixPk {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeInvalidKeyType,
				Actor:     actor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "ek_ may not create env-keys", reqID)
			return
		}

		// Step 2: strict JSON decode with DisallowUnknownFields (D-16).
		var req CreateRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument, "invalid request body", reqID)
			return
		}
		if req.Environment == "" || req.Name == "" {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument, "environment and name required", reqID)
			return
		}

		// Step 3a: GetEnvironment (Postgres projection per issue #34) — absent → 404.
		// db.GetEnvironmentByName returns (nil, nil) on a clean absence, so
		// any non-nil err here is a genuine internal failure.
		env, err := deps.Store.GetEnvironment(ctx, req.Environment)
		if err != nil {
			deps.Logger.Error("envkeys.create: GetEnvironment failed", "env", req.Environment, "err", err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeInternalError,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if env == nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeEnvironmentNotFound,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "environment not found", reqID)
			return
		}

		// Step 3b: terminating envs treated as not-found per D-12 step 2.
		terminating, err := deps.Store.EnvironmentTerminating(ctx, req.Environment)
		if err != nil {
			deps.Logger.Error("envkeys.create: EnvironmentTerminating failed", "env", req.Environment, "err", err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeInternalError,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if terminating {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeEnvironmentNotFound,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "environment not found", reqID)
			return
		}

		// Step 4: AccessGroupSynced=True per D-12 step 3.
		synced, err := deps.Store.EnvironmentAccessGroupSynced(ctx, req.Environment)
		if err != nil {
			deps.Logger.Error("envkeys.create: EnvironmentAccessGroupSynced failed", "env", req.Environment, "err", err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeInternalError,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if !synced {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeNotReady,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusServiceUnavailable, audit.OutcomeNotReady, "environment access group not yet synced", reqID)
			return
		}

		// Step 5: team-membership intersection per D-12 step 4 / WARN-06.
		// Imports the shared helper from internal/platformapi/teams (Plan
		// 03-05 Task 3). NO inline lookupCallerTeams definition here.
		callerTeams, err := achteams.LookupCallerTeams(ctx, deps.LiteLLM, keyCtx.OwnerEmail)
		if err != nil {
			st, oc, msg := classifyLitellmErr(err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   oc,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			deps.Logger.Error("envkeys.create: team lookup failed", "owner", keyCtx.OwnerEmail, "err", err)
			render.Error(w, st, oc, msg, reqID)
			return
		}
		if !hasIntersect(env.AuthorizedTeams, callerTeams) {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeUnauthorizedTeam,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusForbidden, audit.OutcomeUnauthorizedTeam, "caller not in any authorized team", reqID)
			return
		}

		// Step 6: idempotent LiteLLM user provision per D-12 step 5. We
		// already called UserInfoByEmail above via LookupCallerTeams, but
		// LookupCallerTeams swallows ErrNotFound into an empty slice — we
		// can't distinguish "absent user" from "user present with empty
		// Teams". A second targeted UserInfoByEmail surfaces the explicit
		// 404 branch. This is the conservative implementation; Phase 4's
		// cached lookup will collapse the two calls.
		userInfo, err := deps.LiteLLM.UserInfoByEmail(ctx, keyCtx.OwnerEmail)
		if err != nil && !isNotFound(err) {
			st, oc, msg := classifyLitellmErr(err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   oc,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			deps.Logger.Error("envkeys.create: UserInfoByEmail failed", "owner", keyCtx.OwnerEmail, "err", err)
			render.Error(w, st, oc, msg, reqID)
			return
		}
		if userInfo == nil {
			// First-time user — create + enroll in default team.
			newInfo, err := deps.LiteLLM.UserNew(ctx, &litellm.UserNewRequest{
				UserEmail: keyCtx.OwnerEmail,
				Teams:     []string{defaultTeam},
			})
			if err != nil {
				st, oc, msg := classifyLitellmErr(err)
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action:    audit.ActionEkCreate,
					Outcome:   oc,
					Actor:     actor,
					RequestID: reqID,
					Target:    &audit.Target{Kind: "environment", Name: req.Environment},
				})
				deps.Logger.Error("envkeys.create: UserNew failed", "owner", keyCtx.OwnerEmail, "err", err)
				render.Error(w, st, oc, msg, reqID)
				return
			}
			userInfo = newInfo
			if err := deps.LiteLLM.TeamMemberAdd(ctx, defaultTeam, userInfo.UserID, "user"); err != nil {
				// LiteLLM returns 4xx on duplicate add — caller swallows.
				// Other errors are transient (logged but not fatal: the
				// next call will retry the enrollment).
				deps.Logger.Warn("envkeys.create: TeamMemberAdd error (likely duplicate or transient)",
					"team", defaultTeam, "user", userInfo.UserID, "err", err)
			}
		}

		// Step 7: server-side plaintext + key_id generation per D-13.
		plaintext, err := keys.NewBearer(keys.PrefixEk)
		if err != nil {
			deps.Logger.Error("envkeys.create: NewBearer failed", "err", err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionEkCreate, Outcome: audit.OutcomeInternalError,
				Actor: actor, RequestID: reqID,
				Target: &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		keyID, err := keys.NewKeyID(keys.PrefixEkid)
		if err != nil {
			deps.Logger.Error("envkeys.create: NewKeyID failed", "err", err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionEkCreate, Outcome: audit.OutcomeInternalError,
				Actor: actor, RequestID: reqID,
				Target: &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		credHash, err := credhash.Hash(deps.Pepper, []byte(plaintext))
		if err != nil {
			deps.Logger.Error("envkeys.create: credhash.Hash failed", "err", err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionEkCreate, Outcome: audit.OutcomeInternalError,
				Actor: actor, RequestID: reqID,
				Target: &audit.Target{Kind: "environment", Name: req.Environment},
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}

		// LiteLLM KeyGenerate (D-12 step 6). FIX01 §A.6: do NOT supply
		// req.Key — LiteLLM owns its virtual-key plaintext format
		// (sk-…) and ACH never persists or forwards it. ACH stores
		// only the opaque keyResp.Token used for revoke + forwarder
		// attribution. AccessGroups=[<environment>] +
		// Tags=[<environment>] per §6.3 ek_ Environment tag;
		// MaxBudget=nil per KEY-10.
		keyResp, err := deps.LiteLLM.KeyGenerate(ctx, &litellm.KeyGenerateRequest{
			UserID:       userInfo.UserID,
			MaxBudget:    nil,
			AccessGroups: []string{env.Name},
			Tags:         []string{env.Name},
			Metadata: map[string]string{
				"ach_key_id":      keyID,
				"ach_key_type":    "ek",
				"ach_owner_email": keyCtx.OwnerEmail,
				"ach_environment": env.Name,
			},
		})
		if err != nil {
			st, oc, msg := classifyLitellmErr(err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   oc,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			deps.Logger.Error("envkeys.create: KeyGenerate failed", "env", req.Environment, "err", err)
			render.Error(w, st, oc, msg, reqID)
			return
		}
		llToken := keyResp.Token
		llUserID := userInfo.UserID

		// Step 8: INSERT row with WARN-03 retry policy.
		//
		// Two attempts maximum. Between attempts the LiteLLM key is
		// REUSED (no compensation) — only the ekid_ key_id is regenerated
		// — because credential_hash + plaintext + LiteLLM token are
		// stable. On the second failure OR on a credential_hash collision
		// at any time, run the LiteLLM compensation and surface 500.
		insertRow := db.EkInsertRow{
			KeyID:          keyID,
			CredentialHash: credHash,
			Environment:    req.Environment,
			OwnerEmail:     keyCtx.OwnerEmail,
			Name:           req.Name,
			LiteLLMUserID:  &llUserID,
			LiteLLMToken:   &llToken,
		}
		insertErr := deps.DB.InsertEnvironmentKey(ctx, insertRow)
		if insertErr != nil {
			class := classifyInsertError(insertErr)
			if class == insertErrEkidCollision {
				// Retry once with a fresh ekid_ (same plaintext + LiteLLM key reused).
				newKeyID, kerr := keys.NewKeyID(keys.PrefixEkid)
				if kerr == nil {
					insertRow.KeyID = newKeyID
					if retryErr := deps.DB.InsertEnvironmentKey(ctx, insertRow); retryErr == nil {
						keyID = newKeyID
						insertErr = nil
					} else {
						insertErr = retryErr
					}
				}
			}
		}
		if insertErr != nil {
			// Compensation: RevokeKey under a fresh context so caller
			// cancellation cannot orphan the LiteLLM-side key.
			compCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if cleanupErr := deps.LiteLLM.RevokeKey(compCtx, llToken); cleanupErr != nil {
				deps.Logger.Error("envkeys.create: compensation RevokeKey failed",
					"token", llToken, "err", cleanupErr)
			}
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate,
				Outcome:   audit.OutcomeDbInsertFailed,
				Actor:     actor,
				RequestID: reqID,
				Target:    &audit.Target{Kind: "environment", Name: req.Environment},
			})
			deps.Logger.Error("envkeys.create: InsertEnvironmentKey failed",
				"key_id", keyID, "env", req.Environment, "err", insertErr)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeDbInsertFailed, "db insert failed", reqID)
			return
		}

		// Step 8 success: audit + respond.
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeCreated,
			Actor:     actor,
			RequestID: reqID,
			KeyID:     keyID,
			Target:    &audit.Target{Kind: "environment", Name: req.Environment},
		})
		render.JSON(w, http.StatusOK, CreateResponse{
			KeyID:       keyID,
			Plaintext:   plaintext,
			Environment: req.Environment,
			Name:        req.Name,
			OwnerEmail:  keyCtx.OwnerEmail,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// insertErrClass enumerates the pgx 23505 unique-violation classes the
// CreateHandler retry logic distinguishes (WARN-03).
type insertErrClass int

const (
	insertErrOther insertErrClass = iota
	insertErrEkidCollision
	insertErrCredentialHashCollision
)

// classifyInsertError inspects a db.InsertEnvironmentKey error and
// returns the WARN-03 class. The classifier matches both on pgconn
// error code 23505 + constraint name, AND on a substring match of the
// constraint name in the wrapped error message (because internal/db
// wraps the raw pgconn.PgError via fmt.Errorf %w).
func classifyInsertError(err error) insertErrClass {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return insertErrOther
	}
	if pgErr.Code != "23505" {
		return insertErrOther
	}
	// Constraint names per db/migrations/000001_init.up.sql:
	//   environment_keys_pkey               → PK on key_id  (ekid_ collision)
	//   environment_keys_credential_hash_key → UNIQUE on credential_hash
	switch pgErr.ConstraintName {
	case "environment_keys_pkey":
		return insertErrEkidCollision
	case "environment_keys_credential_hash_key":
		return insertErrCredentialHashCollision
	}
	return insertErrOther
}

// hasIntersect reports whether the two string slices share at least one
// element. Used for the §8.2 step-4 authorizedTeams ∩ callerTeams check.
// Empty slice in either argument short-circuits to false.
func hasIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; ok {
			return true
		}
	}
	return false
}

// --------------------------------------------------------------------------
// RevokeHandler — DELETE /platform/env-keys/{key_id} (§8.5, API-07, D-15)
// --------------------------------------------------------------------------

// RevokeHandler implements Hub §8.5 LiteLLM-first ek_ revocation per D-15.
//
// The §8.5 sequence (KEY-08 — LiteLLM is the load-bearing barrier):
//
//  1. ekid_ prefix gate BEFORE the DB lookup (400 invalid_argument on
//     mismatch; prevents prefix-confusion probes from costing a DB
//     roundtrip — T-03-08-03).
//  2. db.GetEnvironmentKey to capture credential_hash + litellm_token.
//     NO DB UPDATE yet.
//  3. Owner check: non-admin callers may only revoke their own rows
//     (403 not_key_owner); admin may revoke any (T-03-08-04).
//  4. Already-revoked rows (status != 'active') treated as 404 — caller
//     does not need to know whether the key existed-then-revoked or
//     never existed (idempotency without double-emitting audit
//     events).
//  5. **LiteLLM FIRST**: deps.LiteLLM.RevokeKey(ctx, litellm_token).
//     On error → 503 litellm_unreachable + audit; DB row STAYS active
//     so retry retries cleanly. Redis NOT DEL'd. This ordering is the
//     KEY-08 invariant.
//  6. **DB flip**: db.RevokeEnvironmentKey post-LiteLLM-ack. On error
//     → 500 internal_error + audit; the LiteLLM-side key is revoked
//     but the DB row is in a partial state. Operator's orphan-cleanup
//     Runnable will eventually reconcile via ListActiveACHKeyTokens
//     (Phase 02.2 D-02). Redis NOT DEL'd here either — without the
//     DB flip a Redis DEL would let the next resolver populate the
//     cache from the stale 'active' row.
//  7. Redis DEL "ach:key:" + credential_hash (best-effort). On error
//     log a warning; the 60s TTL ceiling caps the worst case.
//  8. 204 No Content (no body); audit ActionEkRevoke / OutcomeRevoked.
//
// pk_-only caller-type guard at the top — ek_ may not revoke env-keys.
func RevokeHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		actor := middleware.ActorFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)

		// Caller-type guard.
		if keyCtx.KeyType != keys.PrefixPk {
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "ek_ may not revoke env-keys", reqID)
			return
		}

		// Step 1: ekid_ prefix gate BEFORE DB lookup (T-03-08-03).
		keyID := chi.URLParam(r, "key_id")
		if !strings.HasPrefix(keyID, keys.EkidKeyIDPrefix) {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"key_id must start with "+keys.EkidKeyIDPrefix, reqID)
			return
		}

		// Step 2: read row to capture credential_hash + litellm_token.
		row, err := deps.DB.GetEnvironmentKey(ctx, keyID)
		if err != nil {
			deps.Logger.Error("envkeys.revoke: GetEnvironmentKey failed", "key_id", keyID, "err", err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionEkRevoke, Outcome: audit.OutcomeInternalError,
				Actor: actor, RequestID: reqID, KeyID: keyID,
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if row == nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionEkRevoke, Outcome: audit.OutcomeEnvironmentNotFound,
				Actor: actor, RequestID: reqID, KeyID: keyID,
			})
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "key not found", reqID)
			return
		}

		// Step 3: owner check.
		if row.OwnerEmail != keyCtx.OwnerEmail && !keyCtx.IsAdmin {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkRevoke,
				Outcome:   audit.OutcomeNotKeyOwner,
				Actor:     actor,
				RequestID: reqID,
				KeyID:     keyID,
				Target:    &audit.Target{Kind: "environment", Name: row.Environment},
			})
			render.Error(w, http.StatusForbidden, audit.OutcomeNotKeyOwner, "caller does not own this key", reqID)
			return
		}

		// Step 4: already-revoked → 404 (idempotency without double audit).
		if row.Status != "active" {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action: audit.ActionEkRevoke, Outcome: audit.OutcomeEnvironmentNotFound,
				Actor: actor, RequestID: reqID, KeyID: keyID,
				Target: &audit.Target{Kind: "environment", Name: row.Environment},
			})
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "key not found", reqID)
			return
		}

		// Step 5: LiteLLM FIRST (KEY-08 invariant). The DB row stays
		// 'active' until LiteLLM acks so a retry from the caller retries
		// cleanly. The literal call site below MUST appear in the
		// source before deps.DB.RevokeEnvironmentKey — the plan's
		// acceptance gate greps line numbers to enforce the ordering.
		var llToken string
		if row.LiteLLMToken != nil {
			llToken = *row.LiteLLMToken
		}
		if err := deps.LiteLLM.RevokeKey(ctx, llToken); err != nil {
			st, oc, msg := classifyLitellmErr(err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkRevoke,
				Outcome:   oc,
				Actor:     actor,
				RequestID: reqID,
				KeyID:     keyID,
				Target:    &audit.Target{Kind: "environment", Name: row.Environment},
			})
			deps.Logger.Error("envkeys.revoke: LiteLLM RevokeKey failed", "token", llToken, "err", err)
			render.Error(w, st, oc, msg, reqID)
			return
		}

		// Step 6: DB flip post-LiteLLM-ack.
		if _, err := deps.DB.RevokeEnvironmentKey(ctx, keyID); err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkRevoke,
				Outcome:   audit.OutcomeInternalError,
				Actor:     actor,
				RequestID: reqID,
				KeyID:     keyID,
				Target:    &audit.Target{Kind: "environment", Name: row.Environment},
			})
			deps.Logger.Error("envkeys.revoke: db.RevokeEnvironmentKey failed (LiteLLM-side already revoked)",
				"key_id", keyID, "err", err)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}

		// Step 7: Redis DEL best-effort. Cache key shape MUST match
		// keystore.cacheKeyPrefix + credential_hash exactly so the
		// keystore.Resolver invalidation takes effect on the next
		// resolve attempt.
		cacheKey := "ach:key:" + row.CredentialHash
		if err := deps.Redis.Del(ctx, cacheKey); err != nil {
			deps.Logger.Warn("envkeys.revoke: Redis DEL failed (60s TTL is the worst case bound)",
				"key", cacheKey, "err", err)
		}

		// Step 8: audit + 204.
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action:    audit.ActionEkRevoke,
			Outcome:   audit.OutcomeRevoked,
			Actor:     actor,
			RequestID: reqID,
			KeyID:     keyID,
			Target:    &audit.Target{Kind: "environment", Name: row.Environment},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// --------------------------------------------------------------------------
// ListHandler — GET /platform/env-keys (API-06)
// --------------------------------------------------------------------------

// EkRowView is the read-only JSON projection of an environment_keys row
// returned by ListHandler + GetHandler. Plaintext and credential_hash
// MUST NOT appear here — only stable identifiers and metadata. litellm_*
// columns are also omitted: they're internal cross-references for the
// orphan-cleanup loop and have no consumer outside the Hub.
type EkRowView struct {
	KeyID       string  `json:"key_id"`
	Environment string  `json:"environment"`
	Name        string  `json:"name"`
	OwnerEmail  string  `json:"owner_email"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at,omitempty"`
	RevokedAt   *string `json:"revoked_at,omitempty"`
}

// ListResponse is the §15.5 paginated list envelope.
type ListResponse struct {
	Items      []EkRowView `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// defaultListLimit + maxListLimit clamp the ?limit query parameter per
// §15.5 pagination contract. Mirrors the db.clampLimit invariant from
// Plan 03-03 (default 100, hard cap 500).
const (
	defaultListLimit = 100
	maxListLimit     = 500
)

// ListHandler returns the GET /platform/env-keys list handler.
//
// Caller-scoped non-admin: each pk_ caller sees only their own env-keys.
// Admin override: pk_ callers with KeyContext.IsAdmin=true see every row
// in the deployment, optionally narrowed by ?owner_email=<email>.
// Non-admin callers passing ?owner_email are rejected with 400
// invalid_argument (no silent fallback to caller-scoped — explicit
// rejection prevents UI bugs that quietly leak the wrong scope).
//
// ek_ callers receive 401 invalid_key_type (management endpoint per
// API-11). pk_ only.
func ListHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)

		// Caller-type guard.
		if keyCtx.KeyType != keys.PrefixPk {
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "ek_ may not list env-keys", reqID)
			return
		}

		// Parse query parameters.
		q := r.URL.Query()
		limit, err := parseLimit(q.Get("limit"))
		if err != nil {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument, err.Error(), reqID)
			return
		}
		cursor := q.Get("cursor")
		ownerFilter := q.Get("owner_email")

		// Non-admin callers may NOT supply ?owner_email — explicit
		// rejection per the plan (T-03-08-05 mitigation; prevents
		// scope-mixing UI bugs).
		if ownerFilter != "" && !keyCtx.IsAdmin {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"owner_email filter requires admin privileges", reqID)
			return
		}

		// Dispatch to the appropriate DB helper.
		var rows []db.EkKeyInfo
		var nextCursor string
		if keyCtx.IsAdmin {
			var filter *string
			if ownerFilter != "" {
				filter = &ownerFilter
			}
			rows, nextCursor, err = deps.DB.ListEnvironmentKeysByOwnerWithFilter(ctx, filter, limit, cursor)
		} else {
			rows, nextCursor, err = deps.DB.ListEnvironmentKeysByOwner(ctx, keyCtx.OwnerEmail, limit, cursor)
		}
		if err != nil {
			deps.Logger.Error("envkeys.list: DB query failed", "err", err)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}

		render.JSON(w, http.StatusOK, ListResponse{
			Items:      mapEkRows(rows),
			NextCursor: nextCursor,
		})
	}
}

// parseLimit parses the ?limit query string into an integer between 1
// and maxListLimit, defaulting to defaultListLimit on empty input. A
// non-integer value or out-of-range value returns an error suitable for
// 400 invalid_argument.
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultListLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if n < 1 {
		return 0, errors.New("limit must be >= 1")
	}
	if n > maxListLimit {
		return 0, errors.New("limit must be <= 500")
	}
	return n, nil
}

// mapEkRows projects a slice of db.EkKeyInfo rows to the read-only
// EkRowView wire shape; plaintext, credential_hash, and litellm_* fields
// are NEVER carried into the response.
func mapEkRows(rows []db.EkKeyInfo) []EkRowView {
	out := make([]EkRowView, 0, len(rows))
	for _, r := range rows {
		v := EkRowView{
			KeyID:       r.KeyID,
			Environment: r.Environment,
			Name:        r.Name,
			OwnerEmail:  r.OwnerEmail,
			Status:      r.Status,
			CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
		}
		if r.LastUsedAt != nil {
			t := r.LastUsedAt.UTC().Format(time.RFC3339)
			v.LastUsedAt = &t
		}
		if r.RevokedAt != nil {
			t := r.RevokedAt.UTC().Format(time.RFC3339)
			v.RevokedAt = &t
		}
		out = append(out, v)
	}
	return out
}

// --------------------------------------------------------------------------
// GetHandler — GET /platform/env-keys/{key_id} (API-07 single-row read)
// --------------------------------------------------------------------------

// GetHandler returns the GET /platform/env-keys/{key_id} handler.
//
// Strict ekid_ prefix gate runs BEFORE the DB lookup so a caller probing
// with a pkid_/ek_/pk_/random string never costs a DB roundtrip (T-03-08-03
// mitigation). Owner check: non-admin callers may only read their own
// rows; admin may read any.
//
// Response shape uses EkRowView so plaintext + credential_hash + litellm_*
// fields are excluded by construction.
func GetHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		actor := middleware.ActorFromCtx(ctx)
		keyCtx, _ := middleware.KeyContextFromCtx(ctx)

		// Caller-type guard.
		if keyCtx.KeyType != keys.PrefixPk {
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "ek_ may not read env-keys", reqID)
			return
		}

		// Prefix gate BEFORE any DB roundtrip.
		keyID := chi.URLParam(r, "key_id")
		if !strings.HasPrefix(keyID, keys.EkidKeyIDPrefix) {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument,
				"key_id must start with "+keys.EkidKeyIDPrefix, reqID)
			return
		}

		// DB lookup.
		row, err := deps.DB.GetEnvironmentKey(ctx, keyID)
		if err != nil {
			deps.Logger.Error("envkeys.get: GetEnvironmentKey failed", "key_id", keyID, "err", err)
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
			return
		}
		if row == nil {
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "key not found", reqID)
			return
		}

		// Owner check.
		if row.OwnerEmail != keyCtx.OwnerEmail && !keyCtx.IsAdmin {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionEkCreate, // reuse closest action for read denial; alternative new action not in §18.2 enum
				Outcome:   audit.OutcomeNotKeyOwner,
				Actor:     actor,
				RequestID: reqID,
				KeyID:     keyID,
			})
			render.Error(w, http.StatusForbidden, audit.OutcomeNotKeyOwner, "caller does not own this key", reqID)
			return
		}

		render.JSON(w, http.StatusOK, mapEkRow(row))
	}
}

// mapEkRow is the single-row analog of mapEkRows.
func mapEkRow(r *db.EkKeyInfo) EkRowView {
	v := EkRowView{
		KeyID:       r.KeyID,
		Environment: r.Environment,
		Name:        r.Name,
		OwnerEmail:  r.OwnerEmail,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.LastUsedAt != nil {
		t := r.LastUsedAt.UTC().Format(time.RFC3339)
		v.LastUsedAt = &t
	}
	if r.RevokedAt != nil {
		t := r.RevokedAt.UTC().Format(time.RFC3339)
		v.RevokedAt = &t
	}
	return v
}

// isNotFound checks if a LiteLLM-side error represents a 404 (user-absent
// signal). Mirrors the dual-branch detection in
// internal/platformapi/teams.LookupCallerTeams (Plan 03-05 Task 3): the
// typed ErrNotFound sentinel OR a substring match on "404" in the wrapped
// makeRequest error string. Phase 4 may tighten this contract.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, litellm.ErrNotFound) {
		return true
	}
	return strings.Contains(err.Error(), "404")
}

// classifyLitellmErr maps a LiteLLM client error to the (HTTP status,
// audit outcome, client message) triple the envkeys handlers should
// surface. An upstream 4xx — a typed *litellm.APIError with a 4xx status,
// or a *litellm.Auth401Error — means LiteLLM answered and REFUSED (bad
// master key, validation, permission), which is a 502 Bad Gateway +
// litellm_rejected, NOT the 503 litellm_unreachable that a connectivity
// or 5xx failure warrants. Distinguishing the two stops the operator from
// chasing a phantom outage when the real cause is a config/auth rejection.
func classifyLitellmErr(err error) (status int, outcome, message string) {
	var apiErr *litellm.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return http.StatusBadGateway, audit.OutcomeLitellmRejected, "litellm rejected the request"
	}
	var auth401 *litellm.Auth401Error
	if errors.As(err, &auth401) {
		return http.StatusBadGateway, audit.OutcomeLitellmRejected, "litellm rejected the request"
	}
	return http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable, "litellm unreachable"
}
