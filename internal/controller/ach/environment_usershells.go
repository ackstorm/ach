// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"

	"github.com/ackstorm/ach/internal/litellm"
)

// entitledUserShellIDs returns the team ids of the ach-user-<email> shells for
// every member of the given authorized teams whose shell already exists.
//
// It is how a pk_ reaches its grants: the operator (sole writer of
// assigned_team_ids) attaches each entitled Environment's access group onto the
// member's per-user shell. Membership is read LIVE from GET /team/info (member
// user_id == email — provisionUser), so removing a user from an authorized team
// drops their shell from the next union and detaches the grant.
//
// Only shells present in byAlias (built from the same ListAllTeams pass) are
// returned: absent means the user never minted a pk_ (lazy creation) — adding a
// phantom team_id is pointless and LiteLLM accepts it silently (Hazard 4).
//
// A GetTeamInfo error is returned to the caller, which fails the reconcile:
// never PUT a union missing shells, which would wrongly detach entitled users.
//
// ponytail: one GetTeamInfo per authorized team per reconcile. authorizedTeams
// is 2-3 today; batch or cache if it grows large.
func (r *EnvironmentReconciler) entitledUserShellIDs(
	ctx context.Context,
	authorizedTeamIDs []string,
	byAlias map[string]string,
) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	for _, teamID := range authorizedTeamIDs {
		info, err := r.LiteLLM.GetTeamInfo(ctx, teamID)
		if err != nil {
			return nil, fmt.Errorf("GetTeamInfo(%s): %w", teamID, err)
		}
		if info == nil {
			continue
		}
		for _, email := range info.MemberEmails() {
			shellID, ok := byAlias[litellm.UserShellAlias(email)]
			if !ok || shellID == "" {
				continue // no shell yet (never minted a pk_)
			}
			if _, dup := seen[shellID]; dup {
				continue
			}
			seen[shellID] = struct{}{}
			out = append(out, shellID)
		}
	}
	return out, nil
}
