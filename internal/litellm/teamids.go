// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"fmt"
)

// ResolveTeamIDs maps team aliases to LiteLLM team_ids, in order. LiteLLM
// auto-assigns a UUID at team creation, so callers must look the id up by
// alias rather than assume it equals the alias. An alias that names no team
// wraps ErrNotFound; any other error is the transport's, unchanged.
func ResolveTeamIDs(ctx context.Context, c Client, aliases []string) ([]string, error) {
	ids := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		teams, err := c.ListTeamsByAlias(ctx, alias)
		if err != nil {
			return nil, err
		}
		if len(teams) == 0 {
			return nil, fmt.Errorf("team alias %q: %w", alias, ErrNotFound)
		}
		ids = append(ids, teams[0].TeamID)
	}
	return ids, nil
}
