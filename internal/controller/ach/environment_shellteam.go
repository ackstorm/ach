// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
)

// ensureShellTeam guarantees the Environment's deny-all shell team exists and
// still carries its sentinels, returning the LiteLLM team id.
//
// The shell is what actually caps an Environment Key: LiteLLM access groups
// only ADD permissions, and a key with no team is fail-open on models
// (references/litellm-permission-model.md §1, §4). The shell holds NO grants —
// the environment's access group is attached to it like any authorized team,
// so the grants stay in exactly one place.
//
// `existingID` is the team id the caller already resolved from its
// ListAllTeams pass ("" ⇒ create). The list response cannot be used to verify
// the sentinels — LiteLLM serialises object_permission as null there — so an
// existing shell costs one extra GET /team/info, and only then.
//
// Edge case: if `spec.authorizedTeams` lists the shell's own alias
// (`ach-env-<name>`) before the shell has ever been created, the caller's
// unresolved-references check (which runs before this function) rejects that
// alias every pass — since ensureShellTeam only runs once resolution is
// clean, the shell is never created and this does NOT self-heal on its own;
// removing the alias lets the shell get created here, and re-adding it
// afterward then resolves normally.
func (r *EnvironmentReconciler) ensureShellTeam(
	ctx context.Context,
	env *achv1alpha1.Environment,
	existingID string,
	logger logr.Logger,
) (string, error) {
	alias := litellm.ShellTeamAlias(env.Name)

	if existingID == "" {
		created, err := r.LiteLLM.CreateTeam(ctx, litellm.NewShellTeamRequest(env.Name))
		if err != nil {
			return "", fmt.Errorf("create shell team %s: %w", alias, err)
		}
		if created == nil || created.TeamID == "" {
			return "", fmt.Errorf("create shell team %s: LiteLLM returned no team_id", alias)
		}
		logger.Info("created deny-all shell team", "alias", alias, "id", created.TeamID)
		return created.TeamID, nil
	}

	info, ierr := r.LiteLLM.GetTeamInfo(ctx, existingID)
	if ierr != nil {
		return "", fmt.Errorf("read shell team %s: %w", alias, ierr)
	}
	if info == nil {
		return existingID, nil
	}

	// Ownership gate (fix: same-alias team adoption). ensureShellTeam used to
	// treat ANY team aliased ach-env-<name> as its own — it would UpdateTeam
	// it (overwriting models/object_permission) and deleteShellTeam would
	// later DeleteTeam it, which CASCADES to that team's keys. That is
	// catastrophic if the alias is ever shared by a team an admin created by
	// hand. Metadata stamped at CreateTeam time (litellm.ShellTeamMetadata)
	// is the proof of ownership; only a marked team is touched.
	//
	// Migration: shells this branch created BEFORE this metadata existed
	// (including every shell already running in the cluster today) carry
	// none, so a strict "managed only" check would strand them as
	// unmanaged forever. Those shells are still unambiguously recognizable
	// by SHAPE — alias ach-env-<env> together with the exact deny-all
	// Models sentinel is not a state an unrelated hand-made team would
	// plausibly land in by chance — so a shell missing ONLY the metadata is
	// adopted: the repair write below re-asserts the sentinels AND stamps
	// the metadata, and every following pass then sees it as managed. A
	// team that is neither marked NOR shell-shaped could be anything, so it
	// is refused outright — loud beats a silent takeover.
	managed := litellm.IsShellTeamManaged(*info, env.Name)
	if !managed && !litellm.IsShellTeamShaped(*info, env.Name) {
		return "", fmt.Errorf(
			"shell team %s (id=%s) is not ACH-managed: refusing to update or delete a team ACH did not create",
			alias, existingID,
		)
	}

	if drifted := litellm.ShellTeamDrifted(*info); drifted || !managed {
		resp, err := r.LiteLLM.UpdateTeam(ctx, &litellm.TeamUpdateRequest{
			TeamID:           existingID,
			Models:           []string{litellm.ShellTeamDenyAllModel},
			ObjectPermission: litellm.ShellTeamPermissions(),
			Metadata:         litellm.ShellTeamMetadata(env.Name),
		})
		if err != nil {
			return "", fmt.Errorf("repair shell team %s: %w", alias, err)
		}
		// POST /team/update returns the applied state inline (unlike the
		// list endpoints) — re-check it instead of trusting the 200. A
		// LiteLLM that accepts the write but does not apply it would
		// otherwise leave the Environment reporting AccessGroupSynced=True
		// over a fail-open shell forever, silently.
		if resp != nil && litellm.ShellTeamDrifted(*resp) {
			return "", fmt.Errorf("repair shell team %s: sentinels still drifted after update", alias)
		}
		if !managed {
			logger.Info("adopted pre-existing shell-shaped team lacking ownership metadata", "alias", alias, "id", existingID)
		} else {
			logger.Info("repaired shell team sentinels", "alias", alias, "id", existingID)
		}
	} else if info.Models == nil && info.ObjectPermission == nil {
		// GetTeamInfo is documented as the one read that always resolves
		// object_permission (references/litellm-permission-model.md §9); a
		// response carrying neither field would mean a LiteLLM version that
		// stopped doing so. ShellTeamDrifted correctly refuses to report
		// that as drift (it can't tell fail-open from unresolved), but with
		// no log line here that silently leaves the shell permanently
		// unverified — this is the one signal an operator would have.
		logger.Info("shell team sentinels unverifiable from read-back; GetTeamInfo returned neither models nor object_permission", "alias", alias, "id", existingID)
	}
	return existingID, nil
}

// deleteShellTeam removes the Environment's shell team. Idempotent: an absent
// alias is success. MUST run AFTER the environment's ek_ keys were revoked
// individually — deleting the team first leaves its keys serving traffic for
// ~60s with no route to revoke them (references/litellm-permission-model.md §8).
func (r *EnvironmentReconciler) deleteShellTeam(
	ctx context.Context,
	env *achv1alpha1.Environment,
	logger logr.Logger,
) error {
	alias := litellm.ShellTeamAlias(env.Name)
	teams, err := r.LiteLLM.ListTeamsByAlias(ctx, alias)
	if err != nil {
		return fmt.Errorf("lookup shell team %s: %w", alias, err)
	}
	for _, t := range teams {
		if t.TeamID == "" {
			continue
		}
		// DeleteTeam CASCADES to the team's keys (see the doc comment on
		// litellm.RESTClient.DeleteTeam) — refuse to delete a same-alias
		// team that isn't proven ACH-managed. Unlike ensureShellTeam, there
		// is no shape-based adoption fallback here: by the time an
		// Environment is deleted, ensureShellTeam has run at least once
		// during its lifetime and already stamped the metadata onto any
		// adoptable shell, so an unmarked team seen here is genuinely
		// unrecognized rather than mid-migration.
		if !litellm.IsShellTeamManaged(t, env.Name) {
			logger.Info("skipping delete: team is not ACH-managed for this environment",
				"alias", alias, "id", t.TeamID)
			continue
		}
		if derr := r.LiteLLM.DeleteTeam(ctx, t.TeamID); derr != nil {
			return fmt.Errorf("delete shell team %s (id=%s): %w", alias, t.TeamID, derr)
		}
		logger.Info("deleted shell team", "alias", alias, "id", t.TeamID)
	}
	return nil
}

// revokeEnvironmentKeys revokes every ACTIVE ek_ of this Environment in
// LiteLLM, before anything else in the deletion sequence.
//
// Order is a correctness property, not bookkeeping: deleting the shell team
// (or the access group) first leaves recently-used keys serving traffic until
// LiteLLM's key cache expires (~60s), and inside that window key/delete,
// key/block and key/update all return 404 — the key cannot be revoked by any
// route. Revoking first makes it immediate and verifiable.
//
// A confirmed 404 here (key already gone from LiteLLM, e.g. deleted
// out-of-band) is logged and skipped so a stale row cannot wedge the finalizer
// forever. ANY other error aborts the deletion and requeues: reporting a
// revocation that did not happen is the one outcome that must never occur.
func (r *EnvironmentReconciler) revokeEnvironmentKeys(
	ctx context.Context,
	env *achv1alpha1.Environment,
	logger logr.Logger,
) error {
	if r.DB == nil {
		return nil
	}
	// ponytail: ONE pass over active rows, unlike the sibling drainEkRows
	// loop below in the deletion sequence (a key can be INSERTed while
	// deletion is in progress here too). A key inserted after this SELECT
	// gets its DB row unconditionally flipped to 'revoked' by drainEkRows
	// later, but is never RevokeKey'd in LiteLLM by THIS function — it is
	// cleaned up only by the shell-team delete cascade (with LiteLLM's
	// ~60s key-cache window) or by the orphan reconciler afterwards. Upgrade
	// to a drainEkRows-style reselect-until-converged loop here if that
	// window ever proves too wide in practice; the revoke MUST still run
	// (and re-run) strictly before DeleteAccessGroup/deleteShellTeam, so
	// don't just move this pass later in the sequence instead.
	rows, err := r.DB.Query(ctx,
		`SELECT key_id, litellm_token FROM environment_keys
		  WHERE environment=$1 AND status='active' AND litellm_token IS NOT NULL`,
		env.Name,
	)
	if err != nil {
		return classifyDrainErr("ek_ revoke SELECT", err)
	}
	type ekRow struct{ keyID, token string }
	var pending []ekRow
	for rows.Next() {
		var e ekRow
		if scanErr := rows.Scan(&e.keyID, &e.token); scanErr != nil {
			rows.Close()
			return classifyDrainErr("ek_ revoke scan", scanErr)
		}
		pending = append(pending, e)
	}
	rows.Close()
	if rerr := rows.Err(); rerr != nil {
		return classifyDrainErr("ek_ revoke SELECT", rerr)
	}

	revoked := 0
	for _, e := range pending {
		if rvErr := r.LiteLLM.RevokeKey(ctx, e.token); rvErr != nil {
			if litellm.IsHTTPNotFound(rvErr) {
				logger.Info("ek_ absent in LiteLLM at revoke time; skipping",
					"env", env.Name, "key_id", e.keyID)
				continue
			}
			return fmt.Errorf("revoke ek_ %s: %w", e.keyID, rvErr)
		}
		revoked++
	}
	logger.Info("revoked environment keys in LiteLLM",
		"env", env.Name, "revoked", revoked, "total", len(pending))
	return nil
}

// shellTeamFailed is the closed-set condition for a shell team that could not
// be provisioned or repaired. It rides AccessGroupSynced because the shell is
// part of the same LiteLLM write: without it, ek_ minting is unsafe, so the
// Environment must not report Available.
func shellTeamFailed(env *achv1alpha1.Environment, err error) metav1.Condition {
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionFalse,
		Reason:             "ShellTeamFailed",
		Message:            fmt.Sprintf("LiteLLM shell team %s: %v", litellm.ShellTeamAlias(env.Name), err),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}
