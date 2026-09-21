// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
// DB-05 (verbatim, never normalized).
type idTokenClaims struct {
	Email string `json:"email"`
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
				if !isDuplicateAddErr(tmaErr) {
					return "", &provisionErr{kind: provisionKindDefaultTeamMissing, err: tmaErr}
				}
				deps.Logger.Info("sso.callback: TeamMemberAdd duplicate-add swallowed",
					"user_id", created.UserID, "branch", "first-time")
			}
			return created.UserID, nil
		}
		// Transport / non-404 error.
		return "", &provisionErr{kind: provisionKindLitellm, err: err}
	}

	// Existing-user branch. Per BLK-05 sub-point 3 + D-25, ALWAYS call
	// TeamMemberAdd to be idempotent against out-of-band team-membership
	// revocation. Duplicate-add 4xx is the steady-state expected
	// outcome — swallow it. Any other error surfaces as
	// default_team_missing.
	if tmaErr := deps.LiteLLM.TeamMemberAdd(ctx, defaultTeamID, user.UserID, "user"); tmaErr != nil {
		if !isDuplicateAddErr(tmaErr) {
			deps.Logger.Warn("sso.callback: TeamMemberAdd on existing-user path failed",
				"err", tmaErr, "user_id", user.UserID)
			return "", &provisionErr{kind: provisionKindDefaultTeamMissing, err: tmaErr}
		}
		deps.Logger.Info("sso.callback: TeamMemberAdd duplicate-add swallowed",
			"user_id", user.UserID, "branch", "existing-user")
	}
	return user.UserID, nil
}

// isDuplicateAddErr reports whether err signals LiteLLM's "user already
// on this team" response.
//
// LiteLLM v1.83's `POST /team/member_add` returns 400 for the
// duplicate-add case AND the response body is dropped by the
// `internal/litellm/restclient.go` 4xx wrapper (only `litellm: %d on
// POST %s (code=%s)` reaches the caller). Match on path + status
// instead of trying to substring-find "already" / "Bad Request" in a
// body that isn't there.
//
// Our SSO code path is the only caller that issues `POST
// /team/member_add` (Plan 03-07), and we always send a well-formed
// envelope `{team_id, member: {user_id, role}}`. The realistic 400
// causes in production are:
//   - user already on the team (the case we want to swallow)
//   - team_id unknown (would fail earlier at ListTeamsByAlias and
//     surface as default_team_missing, NOT reaching this branch)
//
// So (path + 400) is a sufficient duplicate-add discriminator.
func isDuplicateAddErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "/team/member_add") &&
		(strings.Contains(s, "litellm: 400") || strings.Contains(s, "Bad Request"))
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
