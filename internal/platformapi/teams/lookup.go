// SPDX-License-Identifier: Apache-2.0

package teams

import (
	"context"

	"github.com/ackstorm/ach/internal/litellm"
)

// LookupCallerTeams returns the LiteLLM-side team memberships for the
// caller identified by their owner email, expressed as BOTH the raw
// LiteLLM team IDs and their resolved aliases. UserInfoByEmail yields the
// team IDs (`team-platform`, `6a31a295-…`); `authorized_teams` (the env
// projection) stores aliases (`default`, `run`, …), so returning only the
// IDs makes HasIntersect false-negative every non-admin member of an
// authorized team (incident 2026-06-04). This helper round-trips
// ListAllTeams to build an id→alias map and returns both forms so the
// intersection matches whichever identifier the env authorized by. The
// ListAllTeams round-trip is also the natural seam for the Phase-4 Redis
// cache (60s TTL, same cache the keystore.Resolver uses) — Phase 3 calls
// LiteLLM on every request.
//
// Semantics:
//
//   - LiteLLM returns 404 (errors.Is(err, litellm.ErrNotFound) OR the
//     wrapped error string contains "404"): caller has no LiteLLM user
//     yet → return (empty slice, nil). Downstream team-intersection
//     treats this as zero intersection → 403 unauthorized_team for
//     protected paths.
//   - LiteLLM returns transport / 5xx error from UserInfoByEmail or
//     ListAllTeams: return (nil, err). The handler emits 503
//     litellm_unreachable.
//   - LiteLLM returns 200 with `teams` array: return a de-duplicated set
//     of {id, alias} per caller team (empty aliases skipped), nil. A nil
//     or empty `teams` field is normalized to a zero-length slice so
//     downstream `HasIntersect` consumers always see a real slice.
//
// The dual-branch 404 detection (errors.Is + string-match) reflects
// Plan 03-01's UserInfoByEmail design decision: that helper does NOT
// translate 404 → ErrNotFound at the type level (kept the makeRequest
// 4xx convention narrow). Phase 4 Forwarder may tighten this contract.
func LookupCallerTeams(ctx context.Context, ll litellm.Client, email string) ([]string, error) {
	info, err := ll.UserInfoByEmail(ctx, email)
	if err != nil {
		if litellm.IsNotFound(err) {
			return []string{}, nil
		}
		return nil, err
	}
	if info == nil || len(info.Teams) == 0 {
		return []string{}, nil
	}

	// info.Teams are LiteLLM team IDs; authorized_teams stores aliases.
	// Resolve each ID to its alias and return BOTH so HasIntersect matches
	// regardless of which identifier the env authorized by (incident
	// 2026-06-04: a member of team alias "default" was denied because the
	// raw team-id string never equalled the alias string).
	teams, err := ll.ListAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	aliasByID := make(map[string]string, len(teams))
	for _, t := range teams {
		if t.TeamAlias != "" {
			aliasByID[t.TeamID] = t.TeamAlias
		}
	}

	out := make([]string, 0, len(info.Teams)*2)
	seen := make(map[string]struct{}, len(info.Teams)*2)
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, id := range info.Teams {
		add(id)            // raw id (matches authorized_teams that hold ids)
		add(aliasByID[id]) // resolved alias (matches the common alias case)
	}
	return out, nil
}

// LookupTeamIDByAlias resolves a LiteLLM team alias to its team id via one
// ListAllTeams round-trip. Returns ("", nil) when no team carries the alias —
// absence is a caller decision (the ek_ create path turns it into 503
// not_ready), not an error.
//
// First-wins on duplicate aliases, matching the Environment reconciler.
func LookupTeamIDByAlias(ctx context.Context, ll litellm.Client, alias string) (string, error) {
	if alias == "" {
		return "", nil
	}
	teams, err := ll.ListAllTeams(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range teams {
		if t.TeamAlias == alias && t.TeamID != "" {
			return t.TeamID, nil
		}
	}
	return "", nil
}
