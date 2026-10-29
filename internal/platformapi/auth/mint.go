// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
)

// repairUserShell re-asserts the deny-all sentinels on a user shell that
// already existed. It is the user-side counterpart of the operator's
// ensureShellTeam repair: nothing else ever writes an ach-user-<email> team
// after CreateTeam, so without this a shell that has drifted — by a hand
// edit, a LiteLLM upgrade, or simply by being written by an older ACH — stays
// drifted forever. That is a fail-OPEN state in the general case (an empty
// models or agents list means EVERYTHING), which is why this exists at all;
// migrating the legacy "__deny_all__" sentinel to "no-default-models", so the
// phantom model leaves the person's catalog, is one instance of it.
//
// Ownership: only a team that is ACH-marked OR shell-shaped is touched, the
// same rule ensureShellTeam follows. A same-alias team ACH did not create is
// left alone and logged — an UpdateTeam on it would overwrite a stranger's
// permissions.
//
// Every failure here is swallowed. Repair is opportunistic: the shell already
// exists and already denies, so the fallback is simply "stays as it was", and
// failing the login would deny the person a usable pk_ over a cosmetic fix.
// That is the OPPOSITE disposition from upsertUserBudgetTag's fail-loud read
// (sso.go) and deliberately so: there, a failed read could not tell
// "unbudgeted" from "already budgeted" and either guess did damage; here the
// two outcomes are "repaired" and "unchanged", and unchanged is safe.
func (deps Deps) repairUserShell(ctx context.Context, email, shellID string) {
	info, err := deps.LiteLLM.GetTeamInfo(ctx, shellID)
	if err != nil || info == nil {
		deps.Logger.Warn("mint: user shell read failed; skipping repair", "team", shellID, "err", err)
		return
	}
	if !litellm.IsUserShellManaged(*info, email) && !litellm.IsUserShellShaped(*info, email) {
		deps.Logger.Warn("mint: team is not an ACH user shell; refusing to update a team ACH did not create",
			"team", shellID)
		return
	}
	if !litellm.ShellTeamDrifted(*info, nil) {
		return
	}
	// Metadata travels with every repair (it re-stamps ownership on an
	// adopted shell); Guardrails is an explicit empty slice because a user
	// shell never carries any and the field has no omitempty — nil would
	// marshal as null.
	if _, err := deps.LiteLLM.UpdateTeam(ctx, &litellm.TeamUpdateRequest{
		TeamID:           shellID,
		Models:           []string{litellm.ShellTeamDenyAllModel},
		ObjectPermission: litellm.ShellTeamPermissions(),
		Metadata:         litellm.UserShellMetadata(email),
		Guardrails:       []string{},
	}); err != nil {
		deps.Logger.Warn("mint: user shell repair failed; shell left as it was", "team", shellID, "err", err)
	}
}

// ErrMintLiteLLM marks a LiteLLM-side failure (shell team or key/generate);
// the OAuth token endpoint maps it to 503 temporarily_unavailable.
var ErrMintLiteLLM = errors.New("litellm unreachable during mint")

// MintError carries the audit outcome + HTTP status the SSO callback renders
// for each failure point, so extracting MintPK changed no response.
type MintError struct {
	Outcome string
	Status  int
	Msg     string
	KeyID   string
	Err     error
}

func (e *MintError) Error() string { return e.Msg + ": " + e.Err.Error() }
func (e *MintError) Unwrap() error { return e.Err }

func mintErr(outcome string, status int, msg, keyID string, err error) error {
	return &MintError{Outcome: outcome, Status: status, Msg: msg, KeyID: keyID, Err: err}
}

// MintPK is steps 6–7 of the SSO callback as a pure function: mint pk_ +
// pkid_, hash, ensure the per-user shell team, KeyGenerate, seal, INSERT
// with LiteLLM compensation on failure. purpose is 'cli' or 'oauth'
// (migration 000020). The plaintext is returned for the CLI/console paths;
// the OAuth token endpoint discards it — the row is reached by owner_email.
func (deps Deps) MintPK(ctx context.Context, email, userID, purpose string) (string, db.PkInsertRow, error) {
	var zero db.PkInsertRow
	internal := func(msg string, err error) error {
		return mintErr(audit.OutcomeInternalError, http.StatusInternalServerError, msg, "", err)
	}
	plaintext, err := keys.NewBearer(keys.PrefixPk)
	if err != nil {
		return "", zero, internal("failed to mint bearer", err)
	}
	keyID, err := keys.NewKeyID(keys.PrefixPkid)
	if err != nil {
		return "", zero, internal("failed to mint key id", err)
	}
	credHash, err := credhash.Hash(deps.Pepper, []byte(plaintext))
	if err != nil {
		return "", zero, internal("failed to hash credential", err)
	}

	// Cap the pk_ with the caller's per-user deny-all shell team. A key with
	// no live team is fail-open on models AND agents (measured; the exact hole
	// this change closes). The shell MUST exist before KeyGenerate — LiteLLM
	// silently accepts a nonexistent team_id and mints a fail-open key
	// (Hazard 4). team_id == alias, so a 400 "already exists" means the shell
	// is already there with the id we know: success.
	shellID := litellm.UserShellAlias(email)
	if _, tErr := deps.LiteLLM.CreateTeam(ctx, litellm.NewUserShellRequest(email)); tErr != nil {
		if !litellm.IsDuplicateTeamErr(tErr) {
			return "", zero, mintErr(audit.OutcomeLitellmUnreachable, http.StatusServiceUnavailable,
				"litellm user shell provision failed", "", fmt.Errorf("%w: %v", ErrMintLiteLLM, tErr))
		}
		deps.repairUserShell(ctx, email, shellID)
	}

	// LiteLLM key registration. ACH does NOT supply req.Key — LiteLLM owns
	// its own virtual-key plaintext format (sk-…); ACH stores only the opaque
	// keyResp.Token for revoke + attribution and the sealed material (G3).
	// KEY-10 invariant preserved: MaxBudget remains nil.
	keyResp, err := deps.LiteLLM.KeyGenerate(ctx, &litellm.KeyGenerateRequest{
		UserID:    userID,
		KeyAlias:  keyID,                          // pkid_… — debug attribution only (not used for lookup)
		TeamID:    shellID,                        // per-user deny-all shell (grants attach via the operator)
		Duration:  durationString(pkExpiryWindow), // LiteLLM key expires with the ACH row
		MaxBudget: nil,
		Metadata: map[string]string{
			"ach_key_id":      keyID,
			"ach_key_type":    "pk",
			"ach_owner_email": email,
			"ach_purpose":     purpose,
			"ach_issuer":      deps.Issuer,
		},
	})
	if err != nil {
		return "", zero, mintErr(audit.OutcomeLitellmUnreachable, http.StatusServiceUnavailable,
			"litellm key/generate unreachable", "", fmt.Errorf("%w: %v", ErrMintLiteLLM, err))
	}

	// Compensation: revoke the LiteLLM-side key we just minted. Fresh
	// context (the request ctx may already be cancelled). Best-effort.
	compensate := func(where string) {
		compCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if cleanupErr := deps.LiteLLM.RevokeKey(compCtx, keyResp.Token); cleanupErr != nil {
			deps.Logger.Error("mint: compensation revoke failed after "+where,
				"err", cleanupErr, "key_id", keyID)
		}
	}
	sealedMaterial, err := keycrypt.Seal(deps.KeyEncryptionKey, []byte(keyResp.Key))
	if err != nil {
		compensate("seal error")
		return "", zero, mintErr(audit.OutcomeInternalError, http.StatusInternalServerError,
			"failed to seal personal key material", keyID, err)
	}
	row := db.PkInsertRow{
		KeyID:              keyID,
		CredentialHash:     credHash,
		OwnerEmail:         email,
		ExpiresAt:          deps.callbackNow().Add(pkExpiryWindow),
		LiteLLMUserID:      &userID,
		LiteLLMToken:       &keyResp.Token,
		LiteLLMKeyMaterial: &sealedMaterial, // G3: the forwarder decrypts on use
		Purpose:            purpose,
	}
	if err := deps.callbackInsertPK(ctx, row); err != nil {
		compensate("insert error")
		return "", zero, mintErr(audit.OutcomeDbInsertFailed, http.StatusInternalServerError,
			"failed to persist personal key", keyID, err)
	}
	return plaintext, row, nil
}
