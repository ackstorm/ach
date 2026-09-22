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

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keycrypt"
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
	// AccessGroupSyncedFromRow derives AccessGroupSynced=True from an
	// already-loaded row (OPT-1): CreateHandler reads terminating off the
	// row's DeletionTimestamp and synced via this method, so the per-call
	// EnvironmentTerminating/EnvironmentAccessGroupSynced SELECTs are no
	// longer on the create path (OPT-1 removed the per-call SELECT helpers entirely).
	AccessGroupSyncedFromRow(row *db.EnvironmentRow) bool
}

// dbOps is the set of internal/db helpers the envkeys handlers call. Same
// rationale as envStore — production wires a thin adapter around the
// db.<helper> functions bound to a pgxpool.Pool; tests inject in-memory
// fakes via dbOps.
type dbOps interface {
	InsertEnvironmentKey(ctx context.Context, row db.EkInsertRow) error
	GetEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	RevokeEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	SuspendEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	ResumeEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	ListKeys(ctx context.Context, f db.KeyListFilter, limit int, cursor string) ([]db.KeyListItem, string, error)
	RevokePersonalKeyByOwner(ctx context.Context, keyID, owner string) (litellmToken *string, err error)
}

// redisOps is the subset of go-redis the envkeys handlers exercise. The
// only call site is the §8.5 RevokeHandler cache invalidation DEL.
type redisOps interface {
	Del(ctx context.Context, key string) error
}

// Deps is the dependency bag every envkeys handler accepts. Plan 03-11's
// server.go constructs it once and passes the same value to each handler
// constructor via MountKeys(deps).
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
	LiteLLM litellm.Client
	DB      dbOps
	Store   envStore
	Redis   redisOps
	Pepper  []byte
	// KeyEncryptionKey is the 32-byte AES-256 DEK sourced from
	// ACH_KEY_ENCRYPTION_KEY (G3) — used to keycrypt.Seal the LiteLLM
	// virtual-key material before INSERT so it is never persisted in
	// cleartext. Required (validated at process start by dekenv.Load).
	KeyEncryptionKey []byte
	Audit            *slog.Logger
	Logger           *slog.Logger
	Namespace        string
	// Issuer is ACH_BASE_URL, stamped as metadata.ach_issuer (see auth.Deps).
	Issuer string
}

// CreateRequest is the POST /platform/keys request body shape (D-16
// idiom — DisallowUnknownFields rejects any extra field with 400
// invalid_argument before the §8.2 flow runs).
type CreateRequest struct {
	Environment string  `json:"environment"`
	Name        string  `json:"name"`
	ExpiresAt   *string `json:"expires_at,omitempty"`

	// Budget optionally caps THIS key's own spend through its
	// "key:<key_id>" tag, independently of the owner's and the
	// Environment's ceilings (LiteLLM blocks on whichever is crossed
	// first). Omit for a key capped only by those two.
	Budget *BudgetRequest `json:"budget,omitempty"`
}

// CreateResponse is the §15.5 success-shape body. Plaintext is returned
// EXACTLY ONCE here and never anywhere else.
type CreateResponse struct {
	KeyID       string  `json:"key_id"`
	Plaintext   string  `json:"plaintext"`
	Environment string  `json:"environment"`
	Name        string  `json:"name"`
	OwnerEmail  string  `json:"owner_email"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at"`
}

// errInvalidArgument is the §15.5 invalid_argument response code. We
// keep it as a string literal (matches the wire format from Hub §15.5)
// rather than promoting it to the audit.Outcome* enum because
// invalid_argument is not a §18.2 audit outcome — it's purely an HTTP
// envelope code for malformed requests.
const codeInvalidArgument = "invalid_argument"

// The ek_/pk_ status vocabulary (§7.1). statusActive/statusSuspended/
// statusRevoked are persisted db column values; statusExpired is what
// ListKeys derives for the SQL-level status (never written back);
// statusInvalid is EffectiveState's own derived-only value (never
// persisted, never returned by ListKeys).
const (
	statusActive    = "active"
	statusSuspended = "suspended"
	statusRevoked   = "revoked"
	statusExpired   = "expired"
	statusInvalid   = "invalid"
)

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
//  3. GetEnvironment from the Postgres projection table; absent or terminating → 404.
//  4. AccessGroupSyncedFromRow check; not True → 503 not_ready.
//  5. Team-membership intersection: authorizedTeams ∩ caller teams ≠ ∅;
//     empty → 403 unauthorized_team.
//  6. Idempotent LiteLLM user provision: UserInfoByEmail; on absent run
//     UserNew + TeamMemberAdd(default, user_id, "user").
//  7. Generate server-side plaintext (ek-<64>) + key_id (ekid_<26>);
//     hash plaintext with the pepper; call litellm.KeyGenerate — LiteLLM
//     owns its virtual-key plaintext format (ACH does NOT supply Key);
//     ACH supplies MaxBudget=nil (KEY-10).
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

		// Optional expires_at (D-24, §7.3): RFC3339, must be strictly in the
		// future. ACH enforces it — no LiteLLM Duration is ever set.
		var expiresAt *time.Time
		if req.ExpiresAt != nil && *req.ExpiresAt != "" {
			ts, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				render.Error(w, http.StatusBadRequest, codeInvalidArgument, "expires_at must be RFC3339", reqID)
				return
			}
			if !ts.After(time.Now()) {
				render.Error(w, http.StatusBadRequest, codeInvalidArgument, "expires_at must be in the future", reqID)
				return
			}
			ts = ts.UTC()
			expiresAt = &ts
		}

		if req.Budget != nil && (req.Budget.MaxBudget == nil || *req.Budget.MaxBudget < 0) {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument, "budget.max_budget must be >= 0", reqID)
			return
		}

		cr := &createReq{
			deps: deps, w: w, ctx: ctx, req: req, keyCtx: keyCtx,
			actor: actor, reqID: reqID, expiresAt: expiresAt,
			target: &audit.Target{Kind: "environment", Name: req.Environment},
		}

		// Step 3+4: load env, terminating(404), not-synced(503).
		env, handled := cr.validateAndLoadEnv()
		if handled {
			return
		}
		// Step 5: team-membership intersection.
		if cr.validateTeamMembership(env) {
			return
		}
		// Step 6: idempotent LiteLLM user provision.
		userID, handled := cr.provisionUser()
		if handled {
			return
		}
		// Steps 7+8: mint + insert + success response.
		cr.mintAndInsert(env, userID)
	}
}

// createReq carries the per-request locals shared by CreateHandler's extracted
// steps (CPLX-1). It is constructed once per request after decode+validate and
// threaded through validateAndLoadEnv/validateTeamMembership/provisionUser/
// mintAndInsert; each method reads the fields it needs and writes the terminal
// HTTP response + audit event on its own rejection/success branch.
type createReq struct {
	deps      Deps
	w         http.ResponseWriter
	ctx       context.Context
	req       CreateRequest
	keyCtx    middleware.KeyContext
	actor     string
	reqID     string
	expiresAt *time.Time
	target    *audit.Target
}

// emitInternalError audits + renders the §15.5 500 internal_error envelope
// (DUP-1). Captured ctx/w/deps/actor/reqID/target via the createReq receiver.
func (cr *createReq) emitInternalError(logMsg string, err error) {
	cr.deps.Logger.Error(logMsg, "env", cr.req.Environment, "err", err)
	audit.EmitAudit(cr.ctx, cr.deps.Audit, audit.Event{
		Action: audit.ActionEkCreate, Outcome: audit.OutcomeInternalError,
		Actor: cr.actor, RequestID: cr.reqID, Target: cr.target,
	})
	render.Error(cr.w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", cr.reqID)
}

// emitLitellmError classifies a LiteLLM client error into (status, outcome,
// message) and audits + renders it (DUP-1). The log line carries both the
// owner email and the environment for correlation (the env attribute the
// old KeyGenerate-failure block logged is preserved across all LiteLLM sites).
func (cr *createReq) emitLitellmError(err error, logMsg string) {
	st, oc, msg := classifyLitellmErr(err)
	audit.EmitAudit(cr.ctx, cr.deps.Audit, audit.Event{
		Action: audit.ActionEkCreate, Outcome: oc,
		Actor: cr.actor, RequestID: cr.reqID, Target: cr.target,
	})
	cr.deps.Logger.Error(logMsg, "owner", cr.keyCtx.OwnerEmail, "env", cr.req.Environment, "err", err)
	render.Error(cr.w, st, oc, msg, cr.reqID)
}

// validateAndLoadEnv runs §8.2 steps 3+4: GetEnvironment (Postgres projection
// per issue #34), the env-not-found(404) + terminating(404) + not-synced(503)
// guards. OPT-1: the terminating + AccessGroupSynced predicates are derived
// from the single in-hand env row (DeletionTimestamp + AccessGroupSyncedFromRow)
// instead of two further SELECTs. On any rejection it writes the response +
// audit and returns handled=true.
func (cr *createReq) validateAndLoadEnv() (env *db.EnvironmentRow, handled bool) {
	// db.GetEnvironmentByName returns (nil, nil) on a clean absence, so any
	// non-nil err here is a genuine internal failure.
	env, err := cr.deps.Store.GetEnvironment(cr.ctx, cr.req.Environment)
	if err != nil {
		cr.emitInternalError("envkeys.create: GetEnvironment failed", err)
		return nil, true
	}
	if env == nil {
		audit.EmitAudit(cr.ctx, cr.deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeEnvironmentNotFound,
			Actor:     cr.actor,
			RequestID: cr.reqID,
			Target:    cr.target,
		})
		render.Error(cr.w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "environment not found", cr.reqID)
		return nil, true
	}

	// terminating envs treated as not-found per D-12 step 2.
	if env.DeletionTimestamp != nil {
		audit.EmitAudit(cr.ctx, cr.deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeEnvironmentNotFound,
			Actor:     cr.actor,
			RequestID: cr.reqID,
			Target:    cr.target,
		})
		render.Error(cr.w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound, "environment not found", cr.reqID)
		return nil, true
	}

	// AccessGroupSynced=True per D-12 step 3.
	if !cr.deps.Store.AccessGroupSyncedFromRow(env) {
		audit.EmitAudit(cr.ctx, cr.deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeNotReady,
			Actor:     cr.actor,
			RequestID: cr.reqID,
			Target:    cr.target,
		})
		render.Error(cr.w, http.StatusServiceUnavailable, audit.OutcomeNotReady, "environment access group not yet synced", cr.reqID)
		return nil, true
	}

	return env, false
}

// validateTeamMembership runs §8.2 step 5 (D-12 step 4 / WARN-06): the
// authorizedTeams ∩ caller-teams intersection. Imports the shared helper from
// internal/platformapi/teams (Plan 03-05 Task 3). On a LiteLLM lookup error it
// surfaces the classified error; on an empty intersection it emits the 403
// unauthorized_team. Returns handled=true on any rejection.
func (cr *createReq) validateTeamMembership(env *db.EnvironmentRow) (handled bool) {
	callerTeams, err := achteams.LookupCallerTeams(cr.ctx, cr.deps.LiteLLM, cr.keyCtx.OwnerEmail)
	if err != nil {
		cr.emitLitellmError(err, "envkeys.create: team lookup failed")
		return true
	}
	if !achteams.HasIntersect(env.AuthorizedTeams, callerTeams) {
		audit.EmitAudit(cr.ctx, cr.deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeUnauthorizedTeam,
			Actor:     cr.actor,
			RequestID: cr.reqID,
			Target:    cr.target,
		})
		render.Error(cr.w, http.StatusForbidden, audit.OutcomeUnauthorizedTeam, "caller not in any authorized team", cr.reqID)
		return true
	}
	return false
}

// provisionUser runs §8.2 step 6 (D-12 step 5): idempotent LiteLLM user
// provision. We already called UserInfoByEmail above via LookupCallerTeams, but
// LookupCallerTeams swallows ErrNotFound into an empty slice — we can't
// distinguish "absent user" from "user present with empty Teams". A second
// targeted UserInfoByEmail surfaces the explicit 404 branch. This is the
// conservative implementation; Phase 4's cached lookup will collapse the two
// calls. Returns the resolved LiteLLM user_id and handled=true on a hard error.
func (cr *createReq) provisionUser() (userID string, handled bool) {
	userInfo, err := cr.deps.LiteLLM.UserInfoByEmail(cr.ctx, cr.keyCtx.OwnerEmail)
	if err != nil && !litellm.IsNotFound(err) {
		cr.emitLitellmError(err, "envkeys.create: UserInfoByEmail failed")
		return "", true
	}
	if userInfo == nil {
		// First-time user — create + enroll in default team.
		newInfo, err := cr.deps.LiteLLM.UserNew(cr.ctx, &litellm.UserNewRequest{
			UserEmail:     cr.keyCtx.OwnerEmail,
			UserID:        cr.keyCtx.OwnerEmail, // deterministic user_id = email (not a random UUID)
			Teams:         []string{defaultTeam},
			AutoCreateKey: litellm.BoolPtr(false), // no leaked default key; ek_ is minted via /key/generate
		})
		if err != nil {
			if !litellm.IsDuplicateUserErr(err) {
				cr.emitLitellmError(err, "envkeys.create: UserNew failed")
				return "", true
			}
			// Probe false-negative (LiteLLM #36): the user already exists with
			// user_id=email (the value we requested). Recover by using email as
			// the id and continuing.
			cr.deps.Logger.Info("envkeys.create: UserNew duplicate — recovering with user_id=email",
				"email", cr.keyCtx.OwnerEmail)
			newInfo = &litellm.UserInfo{UserID: cr.keyCtx.OwnerEmail, UserEmail: cr.keyCtx.OwnerEmail}
		}
		userInfo = newInfo
		if err := cr.deps.LiteLLM.TeamMemberAdd(cr.ctx, defaultTeam, userInfo.UserID, "user"); err != nil {
			// LiteLLM returns 4xx on duplicate add — caller swallows.
			// Other errors are transient (logged but not fatal: the
			// next call will retry the enrollment).
			cr.deps.Logger.Warn("envkeys.create: TeamMemberAdd error (likely duplicate or transient)",
				"team", defaultTeam, "user", userInfo.UserID, "err", err)
		}
	}
	return userInfo.UserID, false
}

// mintAndInsert runs §8.2 steps 7+8: server-side plaintext + key_id generation
// (D-13), the LiteLLM KeyGenerate with the Enterprise-tags drop-and-retry
// fallback, the INSERT with the WARN-03 ekid_-collision single retry +
// compensation RevokeKey under a fresh context, and the OutcomeCreated success
// audit + 200 CreateResponse. This method writes the terminal response itself.
func (cr *createReq) mintAndInsert(env *db.EnvironmentRow, userID string) {
	deps := cr.deps
	w := cr.w
	ctx := cr.ctx
	reqID := cr.reqID

	// Step 7: server-side plaintext + key_id generation per D-13.
	plaintext, err := keys.NewBearer(keys.PrefixEk)
	if err != nil {
		cr.emitInternalError("envkeys.create: NewBearer failed", err)
		return
	}
	keyID, err := keys.NewKeyID(keys.PrefixEkid)
	if err != nil {
		cr.emitInternalError("envkeys.create: NewKeyID failed", err)
		return
	}
	credHash, err := credhash.Hash(deps.Pepper, []byte(plaintext))
	if err != nil {
		cr.emitInternalError("envkeys.create: credhash.Hash failed", err)
		return
	}

	// The ek_ is capped by the Environment's deny-all shell team and by
	// NOTHING else: no models, no object_permission, no access-group binding.
	// A key with no team is fail-open on models, and a key that is in both a
	// team and an access group trips LiteLLM's agent-collapse bug — see
	// references/litellm-permission-model.md §4 and §7.
	shellAlias := litellm.ShellTeamAlias(env.Name)
	shellTeamID, stErr := achteams.LookupTeamIDByAlias(ctx, deps.LiteLLM, shellAlias)
	if stErr != nil {
		cr.emitLitellmError(stErr, "envkeys.create: shell team lookup failed")
		return
	}
	if shellTeamID == "" {
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeNotReady,
			Actor:     cr.actor,
			RequestID: reqID,
			Target:    cr.target,
		})
		render.Error(w, http.StatusServiceUnavailable, audit.OutcomeNotReady,
			"environment shell team not yet provisioned", reqID)
		return
	}

	// LiteLLM KeyGenerate (D-12 step 6). FIX01 §A.6: do NOT supply
	// req.Key — LiteLLM owns its virtual-key plaintext format
	// (sk-…) and ACH never persists or forwards it. ACH stores
	// only the opaque keyResp.Token used for revoke + forwarder
	// attribution. Tags=[<environment>] per §6.3 ek_ Environment tag;
	// MaxBudget=nil per KEY-10.
	//
	// No TeamMemberAdd needed: LiteLLM only enforces team membership on
	// /key/generate for non-admin callers, and ACH authenticates with the
	// master key (PROXY_ADMIN). See references/litellm-permission-model.md §9.
	//
	// An ek_ is PERPETUAL by default: no LiteLLM Duration is ever set, so the
	// LiteLLM key itself has no expiry. An optional expires_at (D-24, §7.3) is
	// enforced by ACH alone — at resolve time (keystore) and at list time
	// (ListKeys derives status='expired') — never surfaced to LiteLLM. Its
	// lifetime is otherwise the Environment's — it is revoked only by
	// DELETE /platform/keys/{id} or by deleting the Environment (the shell-team
	// delete cascades to its keys). Unlike a pk_ (168h sliding window, re-minted
	// on every SSO login), an ek_ has no renewal path, so an unset expires_at
	// must not silently break long-running agents. See
	// references/litellm-permission-model.md.
	keyReq := &litellm.KeyGenerateRequest{
		UserID:    userID,
		TeamID:    shellTeamID,
		KeyAlias:  keyID, // ekid_… — debug attribution only (not used for lookup)
		MaxBudget: nil,
		Tags:      []string{env.Name},
		Metadata: map[string]string{
			"ach_key_id":      keyID,
			"ach_key_type":    "ek",
			"ach_owner_email": cr.keyCtx.OwnerEmail,
			"ach_environment": env.Name,
			"ach_issuer":      deps.Issuer,
		},
	}
	keyResp, err := deps.LiteLLM.KeyGenerate(ctx, keyReq)
	if err != nil && isEnterpriseTagsRejection(err) {
		// §6.3's `tags` is a LiteLLM Enterprise-only feature; an OSS
		// LiteLLM rejects it with 403 "only available for LiteLLM
		// Enterprise users: tags". Tags are best-effort attribution —
		// the environment is also carried by
		// metadata.ach_environment — so degrade gracefully: drop tags
		// and retry once. On Enterprise the first call succeeds and this
		// retry never fires.
		deps.Logger.Warn("envkeys.create: LiteLLM rejected Enterprise-only tags; retrying without tags",
			"env", cr.req.Environment)
		keyReq.Tags = nil
		keyResp, err = deps.LiteLLM.KeyGenerate(ctx, keyReq)
	}
	if err != nil {
		cr.emitLitellmError(err, "envkeys.create: KeyGenerate failed")
		return
	}
	llToken := keyResp.Token
	llUserID := userID
	// G3: seal the LiteLLM virtual-key material (sk-…) at rest. On seal
	// failure (misconfigured DEK / RNG) minting cannot proceed safely:
	// compensate by revoking the LiteLLM-side key we just minted, then 500.
	// Never log keyResp.Key, the sealed blob, or the DEK.
	llMaterial, err := keycrypt.Seal(deps.KeyEncryptionKey, []byte(keyResp.Key))
	if err != nil {
		compCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if cleanupErr := deps.LiteLLM.RevokeKey(compCtx, llToken); cleanupErr != nil {
			deps.Logger.Error("envkeys.create: compensation RevokeKey failed after seal error",
				"key_id", keyID, "err", cleanupErr)
		}
		cancel()
		cr.emitInternalError("envkeys.create: seal key material failed", err)
		return
	}

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
		Environment:    cr.req.Environment,
		OwnerEmail:     cr.keyCtx.OwnerEmail,
		Name:           cr.req.Name,
		LiteLLMUserID:  &llUserID,
		LiteLLMToken:   &llToken,
		// G3: LiteLLM virtual-key material, encrypted at rest (keycrypt blob).
		LiteLLMKeyMaterial: &llMaterial,
		ExpiresAt:          cr.expiresAt,
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
				"key_id", keyID, "err", cleanupErr)
		}
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action:    audit.ActionEkCreate,
			Outcome:   audit.OutcomeDbInsertFailed,
			Actor:     cr.actor,
			RequestID: reqID,
			Target:    cr.target,
		})
		deps.Logger.Error("envkeys.create: InsertEnvironmentKey failed",
			"key_id", keyID, "env", cr.req.Environment, "err", insertErr)
		render.Error(w, http.StatusInternalServerError, audit.OutcomeDbInsertFailed, "db insert failed", reqID)
		return
	}

	// The key's own ceiling, once the row is committed and the plaintext is
	// in hand — never for a key that failed to mint. The key exists either
	// way, so a refused tag write is a 502, not a silent uncapped key.
	if cr.req.Budget != nil {
		tag := litellm.KeyBudgetTag(keyID)
		if err := deps.LiteLLM.UpsertTagBudget(ctx, tag, cr.req.Budget.tagBudget()); err != nil {
			deps.Logger.Error("envkeys.create: key budget tag", "key_id", keyID, "err", err)
			render.Error(w, http.StatusBadGateway, audit.OutcomeLitellmRejected,
				"key created but its budget could not be set", reqID)
			return
		}
	}

	// Step 8 success: audit + respond.
	audit.EmitAudit(ctx, deps.Audit, audit.Event{
		Action:    audit.ActionEkCreate,
		Outcome:   audit.OutcomeCreated,
		Actor:     cr.actor,
		RequestID: reqID,
		KeyID:     keyID,
		Target:    cr.target,
	})
	var expiresAtResp *string
	if cr.expiresAt != nil {
		s := cr.expiresAt.Format(time.RFC3339)
		expiresAtResp = &s
	}
	render.JSON(w, http.StatusOK, CreateResponse{
		KeyID:       keyID,
		Plaintext:   plaintext,
		Environment: cr.req.Environment,
		Name:        cr.req.Name,
		OwnerEmail:  cr.keyCtx.OwnerEmail,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   expiresAtResp,
	})
}

// insertErrClass enumerates the pgx 23505 unique-violation classes the
// CreateHandler retry logic distinguishes (WARN-03).
type insertErrClass int

const (
	insertErrOther insertErrClass = iota
	insertErrEkidCollision
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
	//   environment_keys_pkey → PK on key_id (ekid_ collision). A
	//   credential_hash UNIQUE violation is not distinguished — it falls
	//   through to the generic compensation path identically to any other
	//   unique violation.
	if pgErr.ConstraintName == "environment_keys_pkey" {
		return insertErrEkidCollision
	}
	return insertErrOther
}

// --------------------------------------------------------------------------
// RevokeHandler — DELETE /platform/keys/{key_id} (§8.5, API-07, D-15)
// --------------------------------------------------------------------------

// RevokeHandler implements Hub §8.5 LiteLLM-first ek_ revocation per D-15.
//
// The §8.5 sequence (KEY-08 — LiteLLM is the load-bearing barrier):
//
//  1. ekid_ prefix gate BEFORE the DB lookup (400 invalid_argument on
//     mismatch; prevents prefix-confusion probes from costing a DB
//     roundtrip — T-03-08-03).
//
//  2. db.GetEnvironmentKey to capture credential_hash + litellm_token.
//     NO DB UPDATE yet.
//
//  3. Owner check: non-admin callers may only revoke their own rows
//     (403 not_key_owner); admin may revoke any (T-03-08-04).
//
//     Steps 1-3 are the shared loadOwnedEk head (also used by
//     SuspendHandler/ResumeHandler).
//
//  4. Revoke is valid from ANY non-revoked state (active, suspended, or
//     an active row past its expires_at — D-11/§8.4): only an already
//     'revoked' row short-circuits, as an idempotent 204 with no LiteLLM
//     call and no second state change or audit emission.
//
//  5. **LiteLLM FIRST**: deps.LiteLLM.RevokeKey(ctx, litellm_token).
//     On error → 503 litellm_unreachable + audit; DB row STAYS active
//     so retry retries cleanly. Redis NOT DEL'd. This ordering is the
//     KEY-08 invariant. EXCEPTION: a 404 (LiteLLM has no such key — it is
//     already gone) is idempotent success, NOT a failure: the barrier's
//     goal is already met, so the flow proceeds to step 6. This recovers a
//     row stranded by an out-of-band LiteLLM key delete. Every other error
//     still fails closed.
//
//  6. **DB flip**: db.RevokeEnvironmentKey post-LiteLLM-ack. On error
//     → 500 internal_error + audit; the LiteLLM-side key is revoked
//     but the DB row is in a partial state. Operator's orphan-cleanup
//     Runnable will eventually reconcile via ListManagedACHKeyIDs
//     (Phase 02.2 D-02). Redis NOT DEL'd here either — without the
//     DB flip a Redis DEL would let the next resolver populate the
//     cache from the stale 'active' row.
//
//  7. Redis DEL "ach:key:" + credential_hash (best-effort). On error
//     log a warning; the 60s TTL ceiling caps the worst case.
//
//  8. 204 No Content (no body); audit ActionEkRevoke / OutcomeRevoked.
//
// revokeEnvironmentKey is the ekid_ branch of the unified DELETE
// /platform/keys/{key_id}. LiteLLM-FIRST (KEY-08): the deps.LiteLLM.RevokeKey
// call MUST stay textually before deps.DB.RevokeEnvironmentKey. Returns 204.
func revokeEnvironmentKey(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		actor := middleware.ActorFromCtx(ctx)

		// Steps 1-3 (caller-type guard, ekid_ prefix gate, read + owner
		// check) are shared with SuspendHandler/ResumeHandler via loadOwnedEk.
		row, ok := deps.loadOwnedEk(w, r, audit.ActionEkRevoke)
		if !ok {
			return
		}
		keyID := row.KeyID

		// D-11: already revoked is an idempotent success — no LiteLLM call,
		// no second state change, no double audit. RevokeEnvironmentKey now
		// flips ANY non-revoked status (active/suspended, expired-or-not),
		// so this is the only terminal state left to special-case.
		if row.Status == statusRevoked {
			w.WriteHeader(http.StatusNoContent)
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
			// A 404 means LiteLLM no longer has this virtual key — it is
			// already gone, so the LiteLLM-first barrier's goal (kill the
			// upstream credential before ACH forgets it) is ALREADY met.
			// Treat it as idempotent success and fall through to the DB
			// flip. This recovers a row an operator stranded by deleting
			// the LiteLLM key out-of-band (direct /key/delete or the UI),
			// which otherwise leaves the ek_ un-revokable via the CLI.
			// Any OTHER error leaves the upstream state UNKNOWN → fail
			// closed (row stays 'active', caller retries cleanly).
			if !litellm.IsHTTPNotFound(err) {
				st, oc, msg := classifyLitellmErr(err)
				audit.EmitAudit(ctx, deps.Audit, audit.Event{
					Action:    audit.ActionEkRevoke,
					Outcome:   oc,
					Actor:     actor,
					RequestID: reqID,
					KeyID:     keyID,
					Target:    &audit.Target{Kind: "environment", Name: row.Environment},
				})
				deps.Logger.Error("envkeys.revoke: LiteLLM RevokeKey failed", "key_id", keyID, "err", err)
				render.Error(w, st, oc, msg, reqID)
				return
			}
			deps.Logger.Info("envkeys.revoke: LiteLLM key already absent (404); proceeding to DB flip idempotently",
				"key_id", keyID)
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

		// Step 6b: the key's own budget tag + budget object are dead weight
		// once the key is gone. Both are idempotent and neither may fail the
		// revoke — the credential is already dead, which is what matters.
		tag := litellm.KeyBudgetTag(keyID)
		if err := deps.LiteLLM.DeleteTagByName(ctx, tag); err != nil {
			deps.Logger.Error("envkeys.revoke: DeleteTagByName", "key_id", keyID, "err", err)
		}
		if err := deps.LiteLLM.DeleteBudget(ctx, tag); err != nil {
			deps.Logger.Error("envkeys.revoke: DeleteBudget", "key_id", keyID, "err", err)
		}

		// Step 7: Redis DEL best-effort (cache key shape must match
		// keystore.cacheKeyPrefix + credential_hash exactly).
		deps.invalidate(ctx, row.CredentialHash)

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
// Shared read types — pagination helpers used by ListAllHandler.
// --------------------------------------------------------------------------

// defaultListLimit + maxListLimit clamp the ?limit query parameter per
// §15.5 pagination contract. Mirrors the db.clampLimit invariant from
// Plan 03-03 (default 100, hard cap 500).
const (
	defaultListLimit = 100
	maxListLimit     = 500
)

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

// --------------------------------------------------------------------------
// ListAllHandler — GET /platform/keys (caller's own pk_ + ek_ keys)
// --------------------------------------------------------------------------

// ListAllHandler serves GET /platform/keys — the caller's own pk_ + ek_ keys.
// owner_email is ALWAYS forced to the authenticated caller; ?owner_email is
// intentionally NOT honored (admins use /platform/admin/keys).
func ListAllHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := middleware.RequestIDFromCtx(r.Context())
		keyCtx, ok := middleware.KeyContextFromCtx(r.Context())
		if !ok {
			render.Error(w, http.StatusUnauthorized, "unauthenticated", "missing key context", reqID)
			return
		}
		q := r.URL.Query()
		limit, err := parseLimit(q.Get("limit"))
		if err != nil {
			render.Error(w, http.StatusBadRequest, codeInvalidArgument, err.Error(), reqID)
			return
		}
		cursor := q.Get("cursor")

		statusRaw := q.Get("status")
		switch statusRaw {
		case "", statusActive, statusSuspended, statusRevoked, statusExpired, statusInvalid:
		default:
			render.Error(w, http.StatusBadRequest, codeInvalidArgument, "invalid status filter", reqID)
			return
		}

		owner := keyCtx.OwnerEmail // ALWAYS caller-scoped on this route
		f := db.KeyListFilter{
			OwnerEmail:  &owner,
			Type:        normalizeKeyType(q.Get("type")),
			Status:      normalizeKeyStatus(statusRaw),
			Environment: q.Get("environment"),
		}
		items, next, err := deps.DB.ListKeys(r.Context(), f, limit, cursor)
		if err != nil {
			deps.Logger.Error("envkeys.listall: ListKeys failed", "err", err)
			render.Error(w, http.StatusInternalServerError, "internal", "list keys failed", reqID)
			return
		}

		// D-30: Invalid is derived, once per owner (this route is always
		// caller-scoped) and once per distinct Environment.
		verdicts := deps.accessByEnvironment(r.Context(), keyCtx, items)
		wantStatus := statusRaw
		out := make([]render.KeyListRow, 0, len(items))
		for _, it := range items {
			status, reasons := EffectiveState(it, verdicts[deref(it.Environment)])
			// The wire contract is "?status= filters on the EFFECTIVE state"
			// for all five values, not just 'invalid': the SQL-level filter
			// (normalizeKeyStatus) only narrows what it safely can — it lets
			// every 'active' row through unfiltered since some of those may
			// derive 'invalid' here — so the final say is always this
			// post-derivation check.
			if wantStatus != "" && status != wantStatus {
				continue
			}
			out = append(out, render.KeyRow(it, status, reasons))
		}
		render.KeyList(w, out, next)
	}
}

// deref returns the empty string for a nil *string, and the pointed-to
// value otherwise.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// accessByEnvironment resolves the owner's current Environment access
// (D-30) once per distinct environment among the given items: the owner's
// teams (one TeamsResolver lookup) intersected with each distinct
// Environment's authorizedTeams. The verdict is about the key OWNER's
// access — this route is always caller-scoped (owner == caller) and there
// is no admin bypass: an admin's own ek_ derives Invalid exactly like
// anyone else's once its owner loses Environment access. A TeamsResolver
// failure marks every ek_ environment unverified rather than invalid
// (§7.2) — it must never write a state, only annotate the list.
func (deps Deps) accessByEnvironment(ctx context.Context, kc middleware.KeyContext, items []db.KeyListItem) map[string]accessVerdict {
	out := map[string]accessVerdict{}
	var teams []string
	looked := false
	for _, it := range items {
		if it.Type != "ek" || it.Environment == nil {
			continue
		}
		env := *it.Environment
		if _, done := out[env]; done {
			continue
		}
		if !looked {
			looked = true
			t, err := achteams.LookupCallerTeams(ctx, deps.LiteLLM, kc.OwnerEmail)
			if err != nil {
				for _, it2 := range items { // every env unverified
					if it2.Environment != nil {
						out[*it2.Environment] = accessUnverified
					}
				}
				return out
			}
			teams = t
		}
		row, err := deps.Store.GetEnvironment(ctx, env)
		switch {
		case err != nil:
			out[env] = accessUnverified
		case row == nil || row.DeletionTimestamp != nil:
			out[env] = accessLost
		case achteams.HasIntersect(row.AuthorizedTeams, teams):
			out[env] = accessGranted
		default:
			out[env] = accessLost
		}
	}
	return out
}

// normalizeKeyType maps the ?type query value to a valid filter string.
// Unknown values are silently normalized to "" (no filter) rather than 400
// to mirror the existing ListAllHandler convention.
func normalizeKeyType(v string) string {
	switch v {
	case "pk", "ek":
		return v
	default:
		return ""
	}
}

// normalizeKeyStatus maps the ?status query value into the SQL-level filter
// string ListKeys understands. "invalid" is derived client-side post-query
// (ListAllHandler filters on EffectiveState's output after the fact) so it
// maps to "" here — passing it to SQL would filter out every row.
func normalizeKeyStatus(v string) string {
	switch v {
	case statusActive, statusRevoked, statusExpired, statusSuspended:
		return v
	default:
		return ""
	}
}

// classifyLitellmErr maps a LiteLLM client error to the (HTTP status,
// audit outcome, client message) triple the envkeys handlers should
// surface. An upstream 4xx — a typed *litellm.APIError with a 4xx status,
// or a *litellm.Auth401Error — means LiteLLM answered and REFUSED (bad
// master key, validation, permission), which is a 502 Bad Gateway +
// litellm_rejected, NOT the 503 litellm_unreachable that a connectivity
// or 5xx failure warrants. Distinguishing the two stops the operator from
// chasing a phantom outage when the real cause is a config/auth rejection.
// isEnterpriseTagsRejection reports whether err is the LiteLLM OSS 403 that
// rejects the Enterprise-only `tags` field on POST /key/generate. The
// upstream body reads "This feature is only available for LiteLLM Enterprise
// users: tags". Detection drives the drop-tags-and-retry degradation in the
// env-keys create path — tags are best-effort attribution, so a missing
// Enterprise license must not block key minting on an OSS deployment.
func isEnterpriseTagsRejection(err error) bool {
	var apiErr *litellm.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		return false
	}
	return strings.Contains(string(apiErr.Body), "LiteLLM Enterprise")
}

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
