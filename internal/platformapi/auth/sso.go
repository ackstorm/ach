// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// Deps is the auth-package-scoped dependency bag behind the OAuth AS: the
// Dex leg (ID-token verifier + OAuth2 config), LiteLLM provisioning, the
// pk_ mint (pool, pepper, DEK). DISTINCT from the top-level platformapi
// server's Deps.
type Deps struct {
	// IDTokenVerifier wraps oidc.Provider.Verifier(&oidc.Config{ClientID})
	// so unit tests can substitute a fake. Production code (Plan 03-11)
	// assigns deps.IDTokenVerifier = oidcProvider.Verifier(...).
	IDTokenVerifier IDTokenVerifier

	// OAuth2Cfg is the Dex client config — ClientID, ClientSecret, Scopes,
	// Endpoint. No RedirectURL: the AS derives its own callback from the
	// issuer (oauth_authorize.go dexConfig).
	OAuth2Cfg *oauth2.Config

	// LiteLLM is the (cached) LiteLLM REST client: UserInfoByEmail / UserNew
	// / TeamMemberAdd (provisionUser), CreateTeam / KeyGenerate / RevokeKey
	// (MintPK).
	LiteLLM litellm.Client

	// UserBudget is the per-user default spend ceiling SEEDED onto the
	// "user:<email>" tag at provision time (chart: platformApi.userDefaults).
	// nil = no default configured; ACH then reads and writes no tag and the
	// user is uncapped. Applied only to a tag that has no budget yet, so an
	// admin's later PATCH survives every subsequent login. Budgets live ONLY on tags — never on a LiteLLM team or user
	// object (a user-object budget is not enforced; measured 2026-09-22).
	UserBudget *litellm.TagBudget

	// Pool is the Postgres connection pool (pk_ rows, consent BIP lookup).
	Pool *pgxpool.Pool

	// Pepper is the server-side HMAC pepper sourced from
	// ACH_CREDENTIAL_HASH_PEPPER (Phase 1 D-09) — used to derive
	// credential_hash before INSERT.
	Pepper []byte

	// KeyEncryptionKey is the 32-byte AES-256 DEK sourced from
	// ACH_KEY_ENCRYPTION_KEY (G3) — used to keycrypt.Seal the LiteLLM
	// virtual-key material before INSERT so it is never persisted in
	// cleartext. Required (validated at process start by dekenv.Load).
	KeyEncryptionKey []byte

	// Audit is the *slog.Logger returned by audit.NewLogger (Phase 2 D-17)
	// — the audit=true predicate is already attached.
	Audit *slog.Logger

	// Logger is the operational (NOT audit) logger for non-audit log lines
	// (RevokeKey compensation failures, debugging).
	Logger *slog.Logger

	// Namespace composes the audit `actor` field per Hub §18.3:
	// "<namespace>/<sso-email>". Sourced from POD_NAMESPACE (downward API).
	Namespace string
	// Issuer is ACH_BASE_URL, stamped on every minted LiteLLM key as
	// metadata.ach_issuer: the orphan reaper revokes only its own release's.
	Issuer string

	// InsertPKFn is the DB-insert seam. Production wiring sets it to a
	// closure around db.InsertPersonalKey(ctx, deps.Pool, row); unit tests
	// substitute an in-memory writer to avoid the testcontainers + Postgres
	// dependency in the handler-test suite. nil means "use the Pool with
	// db.InsertPersonalKey" at runtime — see callbackInsertPK below.
	InsertPKFn func(ctx context.Context, row db.PkInsertRow) error

	// NowFn returns the current time. Production wiring leaves it nil
	// (defaulting to time.Now); tests inject a fixed clock so the
	// expires_at column value is deterministic.
	NowFn func() time.Time

	// InsecureCookie, when true, drops the __Host- prefix and Secure flag
	// from the AS binding cookie so a plain-http deployment (dev/e2e,
	// ACH_BASE_URL=http://…) can complete the round-trip. DERIVED from the
	// ACH_BASE_URL scheme in cmd/ach/cmd/platform_api.go.
	InsecureCookie bool
}

// IDTokenVerifier abstracts oidc.IDTokenVerifier so unit tests can
// substitute fakes; the production *oidc.IDTokenVerifier satisfies it.
type IDTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)
}

// callbackInsertPK resolves the DB-insert seam in Deps. If InsertPKFn is
// nil, it falls back to db.InsertPersonalKey on the configured Pool.
func (deps Deps) callbackInsertPK(ctx context.Context, row db.PkInsertRow) error {
	if deps.InsertPKFn != nil {
		return deps.InsertPKFn(ctx, row)
	}
	return db.InsertPersonalKey(ctx, deps.Pool, row)
}

// callbackNow resolves the clock seam in Deps. Defaults to time.Now.
func (deps Deps) callbackNow() time.Time {
	if deps.NowFn != nil {
		return deps.NowFn()
	}
	return time.Now()
}

// idTokenClaims is the minimal subset of the Dex ID-token payload ACH
// reads. The email claim is the SSO-resolved user identity per Hub §16
// DB-05 (verbatim, never normalized); name (the `profile` scope) is display
// only — the console header — and may be empty.
type idTokenClaims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// pkExpiryWindow is the sliding-window TTL for newly minted pk_ rows.
// Hub §7: 7 days. PkCheckAndExtend (Plan 03-03) extends this on every
// auth call where last_used_at is older than 5 minutes, and
// keystore.NewLiteLLMPkExtendHook mirrors each slide onto the LiteLLM key —
// so all three must use the one canonical value.
const pkExpiryWindow = db.PkSlidingWindow

// durationString renders a Go duration as a LiteLLM key duration ("168h").
// LiteLLM accepts an <int><unit> string; hours dodge day-unit differences.
func durationString(d time.Duration) string { return fmt.Sprintf("%dh", int(d.Hours())) }

// provisionUser implements D-04 step 5 + BLK-05 sub-point 3 + D-25
// idempotent LiteLLM user provisioning:
//
//   - UserInfoByEmail(email) → if "not found" (litellm.ErrNotFound OR
//     error string carries "404"), call UserNew(email, teams=["default"])
//     followed by TeamMemberAdd("default", user_id, "user").
//   - Otherwise, the user exists — but per BLK-05 sub-point 3 + D-25 we
//     STILL call TeamMemberAdd("default", user_id, "user") to maintain
//     idempotency against out-of-band Team-membership revocation.
//     Duplicate-add 4xx from LiteLLM is swallowed (interpreted as
//     "membership already present" — desired state).
//
// Returns the resolved LiteLLM user_id on success. Failure cases are
// classified by classifyProvisionError into one of:
//   - audit.OutcomeDefaultTeamMissing (TeamMemberAdd error after
//     successful or skipped UserNew — Hub §17 / API-02 fail-loud)
//   - audit.OutcomeLitellmUnreachable (UserNew or UserInfoByEmail error
//     other than "not found")
//   - audit.OutcomeInternalError (genuinely unexpected)
func provisionUser(ctx context.Context, deps Deps, email string) (string, error) {
	// Resolve the LiteLLM-side team_id for the "default" alias up front.
	// LiteLLM team_id is a UUID auto-assigned at team creation; ACH must
	// look it up by alias rather than hard-coding the literal string
	// "default" (which only happens to work when the deployer pre-seeds
	// LiteLLM with team_id="default", a brittle setup quirk).
	defaultTeams, ltErr := deps.LiteLLM.ListTeamsByAlias(ctx, "default")
	if ltErr != nil {
		return "", &provisionErr{kind: provisionKindLitellm, err: ltErr}
	}
	if len(defaultTeams) == 0 {
		return "", &provisionErr{
			kind: provisionKindDefaultTeamMissing,
			err:  errors.New("LiteLLM has no team with alias 'default'"),
		}
	}
	defaultTeamID := defaultTeams[0].TeamID

	user, err := deps.LiteLLM.UserInfoByEmail(ctx, email)
	if err != nil {
		// 404 → first-time SSO path.
		if litellm.IsNotFound(err) {
			created, createErr := deps.LiteLLM.UserNew(ctx, &litellm.UserNewRequest{
				UserEmail:     email,
				UserID:        email, // deterministic LiteLLM user_id = email (not a random UUID)
				Teams:         []string{"default"},
				AutoCreateKey: litellm.BoolPtr(false), // no leaked default key; pk_ is minted via /key/generate
			})
			if createErr != nil {
				if !litellm.IsDuplicateUserErr(createErr) {
					return "", &provisionErr{kind: provisionKindLitellm, err: createErr}
				}
				// Probe false-negative (LiteLLM #36): the user already exists
				// with user_id=email (the value we requested). Recover by using
				// email as the id and continuing to TeamMemberAdd.
				deps.Logger.Info("sso.callback: UserNew duplicate — recovering with user_id=email",
					"email", email)
				created = &litellm.UserInfo{UserID: email, UserEmail: email}
			}
			// TeamMemberAdd: D-04 step 5 mandates this. LiteLLM v1.83
			// already enrolls the user in `teams:[…]` during UserNew, so
			// this call typically hits a 400 "already added" — swallow
			// that case (desired state). Other errors (genuine
			// team-missing, transport) still surface as
			// default_team_missing per Hub §17 / API-02.
			if tmaErr := deps.LiteLLM.TeamMemberAdd(ctx, defaultTeamID, created.UserID, "user"); tmaErr != nil {
				if !litellm.IsHTTPBadRequest(tmaErr) {
					return "", &provisionErr{kind: provisionKindDefaultTeamMissing, err: tmaErr}
				}
				deps.Logger.Info("sso.callback: TeamMemberAdd duplicate-add swallowed",
					"user_id", created.UserID, "branch", "first-time")
			}
			if budgetErr := upsertUserBudgetTag(ctx, deps, email); budgetErr != nil {
				return "", budgetErr
			}
			return created.UserID, nil
		}
		// Transport / non-404 error.
		return "", &provisionErr{kind: provisionKindLitellm, err: err}
	}

	// Existing-user branch. Per BLK-05 sub-point 3 + D-25, ALWAYS call
	// TeamMemberAdd to be idempotent against out-of-band team-membership
	// revocation. Duplicate-add is the steady-state outcome here — every
	// login after the first — so getting its classification wrong breaks
	// login for everyone EXCEPT new users, and the retry loop is
	// unwinnable (the member is still a member next time).
	//
	// Any 400 is that outcome. We never parse LiteLLM's prose for it: the
	// 4xx wrapper drops the body anyway (§9.1), and the wording has moved
	// across versions. The other conceivable 400 here — an unknown team_id
	// — cannot reach this branch, because ListTeamsByAlias resolved the
	// team above and already returned default_team_missing if it was gone.
	// A 404 or a transport error still surfaces as default_team_missing.
	if tmaErr := deps.LiteLLM.TeamMemberAdd(ctx, defaultTeamID, user.UserID, "user"); tmaErr != nil {
		if !litellm.IsHTTPBadRequest(tmaErr) {
			deps.Logger.Warn("sso.callback: TeamMemberAdd on existing-user path failed",
				"err", tmaErr, "user_id", user.UserID)
			return "", &provisionErr{kind: provisionKindDefaultTeamMissing, err: tmaErr}
		}
		deps.Logger.Info("sso.callback: TeamMemberAdd duplicate-add swallowed",
			"user_id", user.UserID, "branch", "existing-user")
	}
	if budgetErr := upsertUserBudgetTag(ctx, deps, email); budgetErr != nil {
		return "", budgetErr
	}
	return user.UserID, nil
}

// upsertUserBudgetTag seeds the configured default ceiling onto the caller's
// own LiteLLM tag ("user:<email>"). That tag is stamped on every forwarded
// request the person makes, so ONE budget covers their pk_ and every ek_ they
// own, across Environments. Both provisionUser branches call it.
//
// The default is a STARTING value, not a per-login reassertion: it is applied
// only when the tag carries no budget object, so an admin's
// PATCH /platform/admin/users/{email}/budget survives the person's next
// login. The corollary is that changing the chart default no longer retunes
// people who have already been seeded — that is the admin route's job.
//
// "No budget" is BOTH shapes: no tag at all (TagInfo → nil) and a tag with no
// budget object, which is what LiteLLM auto-creates the first time the name
// appears in x-litellm-tags (references/litellm-permission-model.md §15).
// Only a tag that already has a budget is left alone.
//
// No default configured (nil) means no read, no tag and no cap. Both the read
// and the write are fail-loud: an uncapped user is a governance hole, and a
// failed read cannot tell "unbudgeted" from "already budgeted" — guessing
// either way is worse than failing the login.
func upsertUserBudgetTag(ctx context.Context, deps Deps, email string) error {
	if deps.UserBudget == nil {
		return nil
	}
	tag := litellm.UserBudgetTag(email)
	info, err := deps.LiteLLM.TagInfo(ctx, tag)
	if err != nil {
		deps.Logger.Error("provision: read user budget tag", "tag", tag, "err", err)
		return &provisionErr{kind: provisionKindLitellm, err: err}
	}
	if info != nil && info.Budget != nil {
		return nil // already capped — never clobber an admin's value
	}
	if err := deps.LiteLLM.UpsertTagBudget(ctx, tag, *deps.UserBudget); err != nil {
		deps.Logger.Error("provision: write user budget tag", "tag", tag, "err", err)
		return &provisionErr{kind: provisionKindLitellm, err: err}
	}
	return nil
}

// provisionKind is the failure-classification used by classifyProvisionError.
type provisionKind int

const (
	provisionKindUnknown provisionKind = iota
	provisionKindLitellm
	provisionKindDefaultTeamMissing
)

// provisionErr is the typed error returned by provisionUser so the
// caller can map kinds → audit outcomes / HTTP statuses without
// string-matching upstream library errors.
type provisionErr struct {
	kind provisionKind
	err  error
}

func (e *provisionErr) Error() string {
	if e == nil || e.err == nil {
		return "sso: provision failed"
	}
	return "sso: provision failed: " + e.err.Error()
}

func (e *provisionErr) Unwrap() error { return e.err }

// classifyProvisionError maps a provisionUser error into the audit outcome,
// HTTP status, and the message the browser page shows — default-team-missing
// stays fail-loud for the deployer. Only the kinds provisionUser produces
// are recognized.
func classifyProvisionError(err error) (outcome string, status int, msg string) {
	var pe *provisionErr
	if errors.As(err, &pe) {
		switch pe.kind {
		case provisionKindDefaultTeamMissing:
			return audit.OutcomeDefaultTeamMissing, http.StatusInternalServerError,
				"default team unreachable; deployer must create the default Team in LiteLLM"
		case provisionKindLitellm:
			return audit.OutcomeLitellmUnreachable, http.StatusServiceUnavailable,
				"litellm user provisioning unreachable"
		}
	}
	return audit.OutcomeInternalError, http.StatusInternalServerError, "sso provision failed"
}
