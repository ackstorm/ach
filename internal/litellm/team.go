// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

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

// UpdateTeam issues POST /team/update (POST — never the partial-update
// verb; see Pitfall 2).
func (c *RESTClient) UpdateTeam(ctx context.Context, req *UpdateTeamRequest) (*TeamListEntry, error) {
	raw, err := c.makeRequest(ctx, "POST", "/team/update", req)
	if err != nil {
		return nil, err
	}
	var out TeamListEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode POST /team/update: %w", err)
	}
	return &out, nil
}

// DeleteTeam issues POST /team/delete with body {"team_ids": [...]}.
func (c *RESTClient) DeleteTeam(ctx context.Context, teamIDs []string) error {
	_, err := c.makeRequest(ctx, "POST", "/team/delete", &DeleteTeamRequest{TeamIDs: teamIDs})
	return err
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
