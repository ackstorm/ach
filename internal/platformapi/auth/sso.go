// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// Deps is the auth-package-scoped dependency bag for LoginHandler and
// CallbackHandler. It is DISTINCT from the top-level platformapi server's
// Deps — auth carves out exactly the deps it needs (OIDC provider, OAuth2
// config, LiteLLM client, DB pool, pepper, audit logger, namespace) so the
// SSO handlers stay narrowly coupled.
//
// cmd/platform-api/main.go (Plan 03-11) constructs this Deps and passes it
// to LoginHandler(deps) and CallbackHandler(deps) — both mounted OUTSIDE
// the Authn-gated chi.Group per D-02.
type Deps struct {
	// OIDCProvider is the discovery-derived Dex provider (constructed via
	// oidc.NewProvider at process start; cached JWKS refreshed on
	// signature-validation failure per go-oidc default).
	OIDCProvider *oidc.Provider

	// IDTokenVerifier wraps OIDCProvider.Verifier(&oidc.Config{ClientID})
	// so unit tests can substitute a fake. Production code (Plan 03-11)
	// assigns deps.IDTokenVerifier = deps.OIDCProvider.Verifier(...).
	IDTokenVerifier IDTokenVerifier

	// OAuth2Cfg is the OAuth2 client config — ClientID, ClientSecret,
	// RedirectURL, Scopes, and the Endpoint (auth + token URLs derived from
	// OIDCProvider.Endpoint at process start).
	OAuth2Cfg *oauth2.Config

	// LiteLLM is the (cached) LiteLLM REST client. CallbackHandler uses
	// UserInfoByEmail / UserNew / TeamMemberAdd / KeyGenerate / RevokeKey.
	LiteLLM litellm.Client

	// Pool is the Postgres connection pool. CallbackHandler uses
	// db.InsertPersonalKey to write the new pk_ row.
	Pool *pgxpool.Pool

	// Pepper is the server-side HMAC pepper sourced from
	// ACH_CREDENTIAL_HASH_PEPPER (Phase 1 D-09) — used to derive
	// credential_hash before INSERT.
	Pepper []byte

	// Audit is the *slog.Logger returned by audit.NewLogger (Phase 2 D-17)
	// — the audit=true predicate is already attached.
	Audit *slog.Logger

	// Logger is the operational (NOT audit) logger for non-audit log lines
	// (RevokeKey compensation failures, debugging).
	Logger *slog.Logger

	// Namespace composes the audit `actor` field per Hub §18.3:
	// "<namespace>/<sso-email>". Sourced from POD_NAMESPACE (downward API).
	Namespace string

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
}

// IDTokenVerifier abstracts oidc.IDTokenVerifier so unit tests can
// substitute fakes. The interface mirrors the single method
// CallbackHandler calls; the production *oidc.IDTokenVerifier from
// go-oidc satisfies it by structural typing.
type IDTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)
}

// LoginHandler returns the GET /platform/auth/login HTTP handler. It
// implements D-04 step 1:
//
//  1. Generate a 16-byte random `state` and a 32-byte random PKCE
//     `code_verifier` via crypto/rand.
//  2. Compute the S256 `code_challenge` = SHA-256(verifier).
//  3. Set the __Host-ach_sso cookie carrying base64url(state + "|" + verifier).
//  4. Redirect 302 to the Dex authorize URL with code_challenge=S256.
//
// The handler does NOT touch LiteLLM, the DB, the audit logger, or any
// other side-effecting collaborator — it is pure redirect logic.
func LoginHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := middleware.RequestIDFromCtx(r.Context())

		// Step 1: state = 16 random bytes -> base64url-no-pad.
		var stateBytes [16]byte
		if _, err := rand.Read(stateBytes[:]); err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to generate state", reqID)
			return
		}
		state := base64.RawURLEncoding.EncodeToString(stateBytes[:])

		// Step 1b: PKCE verifier = 32 random bytes -> base64url-no-pad.
		// 32 bytes gives 256 bits of entropy (well above the 43..128 char
		// range RFC 7636 §4.1 mandates for code_verifier).
		var verifierBytes [32]byte
		if _, err := rand.Read(verifierBytes[:]); err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to generate verifier", reqID)
			return
		}
		verifier := base64.RawURLEncoding.EncodeToString(verifierBytes[:])

		// Step 2: challenge = base64url(SHA-256(verifier)). Per RFC 7636
		// §4.2, code_challenge_method=S256 implies the verifier is hashed
		// with SHA-256 and the digest is base64url-no-pad-encoded.
		challengeSum := sha256.Sum256([]byte(verifier))
		challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])

		// Step 3: persist (state, verifier) in the __Host-ach_sso cookie.
		setSSOCookie(w, state, verifier)

		// Step 4: build Dex authorize URL with PKCE params and redirect.
		authURL := deps.OAuth2Cfg.AuthCodeURL(state,
			oauth2.SetAuthURLParam("code_challenge", challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)

		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

// callbackInsertPK resolves the DB-insert seam in Deps. If InsertPKFn is
// nil, it falls back to db.InsertPersonalKey on the configured Pool.
// Centralizing the seam keeps CallbackHandler readable.
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

// callbackResponse is the §15.5 SSO callback success body. plaintext is
// the pk_<…> bearer; key_id is the pkid_<…> opaque id. The body is the
// SOLE place plaintext appears (per Hub §16.1 Specifics block).
type callbackResponse struct {
	KeyID      string `json:"key_id"`
	Plaintext  string `json:"plaintext"`
	OwnerEmail string `json:"owner_email"`
}

// pkExpiryWindow is the sliding-window TTL for newly minted pk_ rows.
// Hub §7: 7 days. PkCheckAndExtend (Plan 03-03) extends this on every
// auth call where last_used_at is older than 5 minutes.
const pkExpiryWindow = 7 * 24 * time.Hour

// CallbackHandler returns the GET /platform/auth/sso/callback handler.
// It implements D-04 step 2 (the load-bearing SSO sequence):
//
//  1. Read state cookie + URL ?state= + ?code= — clear cookie immediately
//     after reading (single-use semantics; BLK-05).
//  2. Validate state cookie matches URL state; missing/mismatch →
//     400 invalid_argument + audit outcome=state_invalid.
//  3. Exchange code for token using the PKCE verifier.
//  4. Extract + verify ID token via the OIDC verifier; pull email claim.
//  5. Idempotent LiteLLM user provision (first-time: UserNew + TeamMemberAdd;
//     existing: TeamMemberAdd ALWAYS, per BLK-05 sub-point 3 + D-25).
//  6. Generate pk_ plaintext + pkid_ key_id server-side; HMAC-hash with
//     pepper; KeyGenerate against LiteLLM with caller-supplied Key + MaxBudget=nil.
//  7. INSERT row in personal_keys. On INSERT failure, compensate with
//     LiteLLM RevokeKey (best-effort) and render 500 db_insert_failed.
//  8. Emit audit ActionSSOLogin / OutcomeCreated and return JSON body —
//     the plaintext appears exactly once.
//
// All failure branches emit an audit event with a §18.2 outcome and clear
// the SSO cookie before rendering the error envelope.
func CallbackHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		// Initial actor — no email resolved yet, namespace from deps.
		baseActor := deps.Namespace + "/-"

		// Step 1: read state cookie and clear it BEFORE further processing
		// so single-use semantics hold even on the failure branches (BLK-05).
		state, verifier, cookieErr := readSSOCookie(r)
		clearSSOCookie(w)
		if cookieErr != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeStateInvalid,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusBadRequest, audit.OutcomeStateInvalid,
				"sso state cookie missing or malformed", reqID)
			return
		}

		// Step 2: state-mismatch detection — covers
		//   (a) URL ?state= missing entirely,
		//   (b) URL ?state= empty,
		//   (c) URL ?state= != cookie state.
		// All three branches emit OutcomeStateInvalid (BLK-05).
		urlState := r.URL.Query().Get("state")
		if urlState == "" || urlState != state {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeStateInvalid,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusBadRequest, audit.OutcomeStateInvalid,
				"sso state mismatch or missing", reqID)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusBadRequest, audit.OutcomeInternalError,
				"missing authorization code", reqID)
			return
		}

		// Step 3: exchange the code for the token, passing the PKCE
		// code_verifier so Dex validates the original challenge.
		token, err := deps.OAuth2Cfg.Exchange(ctx, code,
			oauth2.SetAuthURLParam("code_verifier", verifier))
		if err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusBadGateway, audit.OutcomeInternalError,
				"sso code exchange failed", reqID)
			return
		}

		// Step 4a: extract id_token from the token response (OIDC overlay
		// on top of plain OAuth2). Per RFC 6749 §4.1.4 + OIDC §3.1.3.3
		// id_token is returned in the JSON token body, accessible via
		// token.Extra("id_token").
		rawIDToken, ok := token.Extra("id_token").(string)
		if !ok || rawIDToken == "" {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusBadGateway, audit.OutcomeInternalError,
				"sso id_token missing from token response", reqID)
			return
		}

		// Step 4b: validate ID token signature + standard claims (iss,
		// aud, exp, iat, nonce). go-oidc's Verifier refreshes Dex JWKS on
		// signature-validation failure per default config.
		idToken, err := deps.IDTokenVerifier.Verify(ctx, rawIDToken)
		if err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInternalError,
				"sso id_token verification failed", reqID)
			return
		}

		// Step 4c: extract email claim verbatim (Hub §16 DB-05 — no
		// normalization, case-sensitive storage).
		var claims idTokenClaims
		if err := idToken.Claims(&claims); err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"sso id_token claims decode failed", reqID)
			return
		}
		if claims.Email == "" {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     baseActor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusBadRequest, audit.OutcomeInternalError,
				"sso id_token missing email claim", reqID)
			return
		}

		// From here on the resolved actor carries the user's email.
		actor := deps.Namespace + "/" + claims.Email

		// Step 5: idempotent LiteLLM user provision.
		userID, err := provisionUser(ctx, deps, claims.Email)
		if err != nil {
			outcome, status, msg := classifyProvisionError(err)
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   outcome,
				Actor:     actor,
				RequestID: reqID,
			})
			render.Error(w, status, outcome, msg, reqID)
			return
		}

		// Step 6: mint pk_ and pkid_ server-side; hash plaintext.
		plaintext, err := keys.NewBearer(keys.PrefixPk)
		if err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     actor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to mint bearer", reqID)
			return
		}
		keyID, err := keys.NewKeyID(keys.PrefixPkid)
		if err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     actor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to mint key id", reqID)
			return
		}
		credHash, err := credhash.Hash(deps.Pepper, []byte(plaintext))
		if err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeInternalError,
				Actor:     actor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to hash credential", reqID)
			return
		}

		// Step 6b: LiteLLM key registration. ACH does NOT supply
		// req.Key — LiteLLM owns its own virtual-key plaintext format
		// (sk-…) and ACH never persists or forwards it (FIX01 §A.6
		// decision; supersedes the obsolete D-13 "shared plaintext"
		// design). ACH stores only the opaque keyResp.Token, which is
		// the stable LiteLLM-side identifier used for revoke +
		// forwarder attribution. KEY-10 invariant preserved:
		// MaxBudget remains nil.
		keyResp, err := deps.LiteLLM.KeyGenerate(ctx, &litellm.KeyGenerateRequest{
			UserID:    userID,
			MaxBudget: nil,
		})
		if err != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeLitellmUnreachable,
				Actor:     actor,
				RequestID: reqID,
			})
			render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable,
				"litellm key/generate unreachable", reqID)
			return
		}

		// Step 7: INSERT row. On failure compensate by revoking the
		// LiteLLM-side key (best-effort — RevokeKey error is logged but
		// does NOT alter the 500 response).
		expiresAt := deps.callbackNow().Add(pkExpiryWindow)
		row := db.PkInsertRow{
			KeyID:          keyID,
			CredentialHash: credHash,
			OwnerEmail:     claims.Email,
			ExpiresAt:      expiresAt,
			LiteLLMUserID:  &userID,
			LiteLLMToken:   &keyResp.Token,
		}
		if err := deps.callbackInsertPK(ctx, row); err != nil {
			// Compensation: revoke the LiteLLM-side key we just minted.
			// Use a fresh context (the request ctx may already be cancelled
			// when the DB INSERT failed). Best-effort: log on error, do
			// NOT alter the 500 response per D-12 step 7 analog.
			compCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if cleanupErr := deps.LiteLLM.RevokeKey(compCtx, keyResp.Token); cleanupErr != nil {
				deps.Logger.Error("sso.callback: compensation revoke failed",
					"err", cleanupErr, "litellm_token", keyResp.Token)
			}
			cancel()

			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionSSOLogin,
				Outcome:   audit.OutcomeDbInsertFailed,
				Actor:     actor,
				RequestID: reqID,
				KeyID:     keyID,
			})
			render.Error(w, http.StatusInternalServerError, audit.OutcomeDbInsertFailed,
				"failed to persist personal key", reqID)
			return
		}

		// Step 8: emit success audit + render the one-time plaintext.
		audit.EmitAudit(ctx, deps.Audit, audit.Event{
			Action:    audit.ActionSSOLogin,
			Outcome:   audit.OutcomeCreated,
			Actor:     actor,
			RequestID: reqID,
			KeyID:     keyID,
		})
		render.JSON(w, http.StatusOK, callbackResponse{
			KeyID:      keyID,
			Plaintext:  plaintext,
			OwnerEmail: claims.Email,
		})
	}
}

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
		if isLiteLLMNotFound(err) {
			created, createErr := deps.LiteLLM.UserNew(ctx, &litellm.UserNewRequest{
				UserEmail: email,
				Teams:     []string{"default"},
			})
			if createErr != nil {
				return "", &provisionErr{kind: provisionKindLitellm, err: createErr}
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
// on this team" 4xx response. LiteLLM's makeRequest 4xx wrapper formats
// the error string as `litellm: ... status: 400 ... body: ... already
// ...`; the substring check is robust across LiteLLM error-envelope
// shapes.
func isDuplicateAddErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "already") &&
		(strings.Contains(s, "400") || strings.Contains(s, "Bad Request"))
}

// isLiteLLMNotFound reports whether err signals a LiteLLM 404 response.
// Per Plan 03-01 D-25, UserInfoByEmail keeps the generic makeRequest 4xx
// wrapping (i.e. the error string carries "404"); ErrNotFound is also
// checked defensively for forward compatibility.
func isLiteLLMNotFound(err error) bool {
	if errors.Is(err, litellm.ErrNotFound) {
		return true
	}
	// makeRequest formats 4xx as fmt.Errorf("litellm: ... status: 404 ...");
	// the substring check is robust across LiteLLM error-envelope shapes.
	return err != nil && containsCaseInsensitive(err.Error(), "404")
}

// containsCaseInsensitive is a manual substring check — avoids the
// strings.ToLower allocation on the auth hot path.
func containsCaseInsensitive(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			// Treat byte equality strictly — "404" is digits, no case folding needed.
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
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
// HTTP status, and user-visible message. The mapping is intentionally
// narrow: only the kinds provisionUser produces are recognized.
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
