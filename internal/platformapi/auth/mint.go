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
	if _, tErr := deps.LiteLLM.CreateTeam(ctx, litellm.NewUserShellRequest(email)); tErr != nil && !litellm.IsDuplicateTeamErr(tErr) {
		return "", zero, mintErr(audit.OutcomeLitellmUnreachable, http.StatusServiceUnavailable,
			"litellm user shell provision failed", "", fmt.Errorf("%w: %v", ErrMintLiteLLM, tErr))
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
