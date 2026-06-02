// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// defaultTeamAlias is the canonical LiteLLM team alias every SSO-
// provisioned user is enrolled into. The literal "default" is the
// production value today; TODO §15 tracks making this configurable
// per-deployment (so a deployer can pick "engineering", "tenant-a",
// etc.). Both EnsureDefaultTeam and the SSO handler reference this
// constant so they stay in lockstep.
const defaultTeamAlias = "default"

// EnsureDefaultTeam guarantees LiteLLM has at least one Team with
// alias=defaultTeamAlias. Idempotent — list-first via
// ListTeamsByAlias, only POST /team/new on empty. Called by the
// LiteLLMConnection reconciler after a successful probe so the
// operator-side bootstrap converges without deployer intervention.
//
// Returns nil if the team already exists OR was just created. Returns
// wrapped error on LiteLLM unreachable / 5xx / unauthorized — caller
// logs and continues.
func (c *RESTClient) EnsureDefaultTeam(ctx context.Context) error {
	existing, err := c.ListTeamsByAlias(ctx, defaultTeamAlias)
	if err != nil {
		return fmt.Errorf("ensure default team: list by alias %q: %w", defaultTeamAlias, err)
	}
	if len(existing) > 0 {
		return nil
	}
	if _, err := c.CreateTeam(ctx, &NewTeamRequest{TeamAlias: defaultTeamAlias}); err != nil {
		return fmt.Errorf("ensure default team: create %q: %w", defaultTeamAlias, err)
	}
	return nil
}

// CreateTeam issues POST /team/new.
func (c *RESTClient) CreateTeam(ctx context.Context, req *NewTeamRequest) (*TeamListEntry, error) {
	raw, err := c.makeRequest(ctx, "POST", "/team/new", req)
	if err != nil {
		return nil, err
	}
	var out TeamListEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode POST /team/new: %w", err)
	}
	return &out, nil
}

// ListTeamsByAlias issues GET /v2/team/list?team_alias=<alias>&page_size=100
// and returns ONLY teams whose TeamAlias exactly matches the requested
// alias. LiteLLM's server-side filter is partial (substring) per spec
// §6.7; the operator must apply an exact-match filter client-side to
// preserve the "operator never touches names it did not declare" invariant.
//
// Returns a (possibly empty) slice. Empty is NOT ErrNotFound — callers
// decide whether absence is a soft success (e.g. "create a new team") or
// an error.
func (c *RESTClient) ListTeamsByAlias(ctx context.Context, alias string) ([]TeamListEntry, error) {
	path := "/v2/team/list?team_alias=" + url.QueryEscape(alias) + "&page_size=100"
	raw, err := c.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var list TeamListResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /v2/team/list: %w", err)
	}
	// Client-side exact-match filter (§6.7).
	out := make([]TeamListEntry, 0, len(list.Teams))
	for _, t := range list.Teams {
		if t.TeamAlias == alias {
			out = append(out, t)
		}
	}
	return out, nil
}
