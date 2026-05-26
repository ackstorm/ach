// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// DeleteAccessGroup issues DELETE /access_group/<name>/delete (Pre-flight
// F1: the prior URL "/access-groups/<name>" was incorrect per LiteLLM
// v1.82.6 OpenAPI). Called from EnvironmentReconciler at Hub §6.5 step 2
// — the runtime barrier. Once the LiteLLM access group named
// <environment> is deleted, every ek_ still bound to the Environment
// fails forwarding at LiteLLM regardless of ACH cache state, which is
// the property the finalizer drain relies on.
//
// §7.7 idempotent-delete contract: makeRequest treats DELETE 404 as
// success, so a re-reconcile after a partially-completed §6.5 sequence
// does NOT generate a spurious error.
func (c *RESTClient) DeleteAccessGroup(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/access_group/"+name+"/delete", nil)
	return err
}

// DeleteTag issues DELETE /tag/<name>. Called from EnvironmentReconciler
// at Hub §6.5 step 3 — clears the budget tag LiteLLM uses for spend
// attribution against the deleted Environment.
//
// Same §7.7 idempotent-delete contract as DeleteAccessGroup.
func (c *RESTClient) DeleteTag(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/tag/"+name, nil)
	return err
}

// CreateAccessGroup issues POST /access_group/new. LiteLLM returns 400
// with body containing "already exists" when the access group is already
// registered; this method translates that into ErrAlreadyExists so
// callers can treat it as the idempotent-success branch.
func (c *RESTClient) CreateAccessGroup(ctx context.Context, name string, modelNames []string) error {
	if name == "" {
		return fmt.Errorf("litellm: CreateAccessGroup: empty name")
	}
	body := &NewAccessGroupRequest{
		AccessGroup: name,
		ModelNames:  modelNames,
	}
	_, err := c.makeRequest(ctx, "POST", "/access_group/new", body)
	if err == nil {
		return nil
	}
	// Peek at upstream message body to detect "already exists" → sentinel.
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		_, msg, _ := processLitellmError(apiErr.Body)
		if strings.Contains(strings.ToLower(msg), "already exists") {
			return ErrAlreadyExists
		}
	}
	return err
}

// BindTeamToAccessGroup grants the named team access to the named access
// group by appending the magic "access_group/<name>" entry to the
// team's models[] array via POST /team/update. Idempotent: if the
// entry is already present in team.models, this is a no-op (no upstream
// call).
//
// Step-by-step:
//
//  1. GET /team/info?team_id=<id> to read current team state.
//  2. Inspect the team's `models` array.
//  3. If "access_group/<name>" is already present, return nil.
//  4. Otherwise POST /team/update with team_id + the appended models[].
func (c *RESTClient) BindTeamToAccessGroup(ctx context.Context, accessGroup, teamID string) error {
	if accessGroup == "" || teamID == "" {
		return fmt.Errorf("litellm: BindTeamToAccessGroup: empty accessGroup or teamID")
	}
	entry := TeamAccessGroupPrefix + accessGroup

	raw, err := c.makeRequest(ctx, "GET", "/team/info?team_id="+teamID, nil)
	if err != nil {
		return fmt.Errorf("litellm: GET /team/info?team_id=%s: %w", teamID, err)
	}
	var info struct {
		TeamInfo TeamListEntry `json:"team_info"`
	}
	if uerr := json.Unmarshal(raw, &info); uerr != nil || info.TeamInfo.TeamID == "" {
		// Some LiteLLM versions return the TeamListEntry directly (no
		// envelope). Fall back to bare-object decode.
		var bare TeamListEntry
		if err2 := json.Unmarshal(raw, &bare); err2 != nil {
			return fmt.Errorf("litellm: decode /team/info: %v (fallback: %w)", uerr, err2)
		}
		info.TeamInfo = bare
	}

	for _, m := range info.TeamInfo.Models {
		if m == entry {
			return nil
		}
	}

	newModels := append([]string{}, info.TeamInfo.Models...)
	newModels = append(newModels, entry)
	upd := &UpdateTeamRequest{
		TeamID: teamID,
		Models: newModels,
	}
	if _, err := c.makeRequest(ctx, "POST", "/team/update", upd); err != nil {
		return fmt.Errorf("litellm: POST /team/update (team_id=%s, add access_group=%s): %w", teamID, accessGroup, err)
	}
	return nil
}

// ListAccessGroupBindings returns the team_ids whose .models array
// contains "access_group/<name>". Used by §7 drift detection.
//
// Wire path: GET /v2/team/list?page=<n>&page_size=200 (no per-access-group
// server-side filter exists on LiteLLM 1.82.6, so we list and filter
// client-side; the operator owns at most O(10s) of teams in production
// per Hub §6.1 — performance is acceptable).
//
// Pagination: iterates pages while page <= TotalPages. Stops at
// maxAccessGroupListPages (50 — generous safety cap; exceeding this is
// a config error, not a correctness condition).
func (c *RESTClient) ListAccessGroupBindings(ctx context.Context, accessGroup string) ([]string, error) {
	if accessGroup == "" {
		return nil, fmt.Errorf("litellm: ListAccessGroupBindings: empty accessGroup")
	}
	entry := TeamAccessGroupPrefix + accessGroup
	var out []string
	for page := 1; page <= maxAccessGroupListPages; page++ {
		path := fmt.Sprintf("/v2/team/list?page=%d&page_size=200", page)
		raw, err := c.makeRequest(ctx, "GET", path, nil)
		if err != nil {
			return nil, fmt.Errorf("litellm: GET %s: %w", path, err)
		}
		var resp TeamListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("litellm: decode %s: %w", path, err)
		}
		for _, t := range resp.Teams {
			for _, m := range t.Models {
				if m == entry {
					out = append(out, t.TeamID)
					break
				}
			}
		}
		if len(resp.Teams) == 0 || page >= resp.TotalPages {
			break
		}
	}
	return out, nil
}

const maxAccessGroupListPages = 50
