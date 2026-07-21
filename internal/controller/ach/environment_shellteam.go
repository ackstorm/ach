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
	if info != nil && litellm.ShellTeamDrifted(*info) {
		if _, err := r.LiteLLM.UpdateTeam(ctx, &litellm.TeamUpdateRequest{
			TeamID:           existingID,
			Models:           []string{litellm.ShellTeamDenyAllModel},
			ObjectPermission: litellm.ShellTeamPermissions(),
		}); err != nil {
			return "", fmt.Errorf("repair shell team %s: %w", alias, err)
		}
		logger.Info("repaired shell team sentinels", "alias", alias, "id", existingID)
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
		if derr := r.LiteLLM.DeleteTeam(ctx, t.TeamID); derr != nil {
			return fmt.Errorf("delete shell team %s (id=%s): %w", alias, t.TeamID, derr)
		}
		logger.Info("deleted shell team", "alias", alias, "id", t.TeamID)
	}
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
