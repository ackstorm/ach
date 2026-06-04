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

// litellmTeamListPageSize is the page size ListAllTeams requests. LiteLLM's
// /v2/team/list rejects oversized page_size values with 422 (the deployed
// build 422s page_size=500 — #113 regression, 2026-06-05: a 422 → makeRequest
// 4xx → APIError → teams.LookupCallerTeams (nil,err) → hydrate 503
// litellm_unreachable, breaking every hydrate/env-keys request). 100 is the
// value ListTeamsByAlias already uses successfully, so it is the safe ceiling.
const litellmTeamListPageSize = 100

// listAllTeamsPageCap bounds the pagination loop so a malformed total_pages or
// a non-advancing endpoint cannot spin forever. 100 pages × 100/page = 10k
// teams — far beyond any real ACH-owned (single-tenant) LiteLLM.
const listAllTeamsPageCap = 100

// ListAllTeams pages GET /v2/team/list?page=<n>&page_size=100 and returns every
// team. No client-side filter (cf. ListTeamsByAlias). Empty slice is not an
// error. Pages until total_pages is exhausted so alias resolution is complete:
// a single page_size=500 request is rejected 422 by the deployed LiteLLM
// (#113), and silent truncation would drop alias resolutions and re-introduce
// the team false-negative this path closes (cross-AI review MED finding
// 2026-06-04 — now resolved by real pagination rather than a WARN). A
// pathological total_pages is bounded by listAllTeamsPageCap (logged WARN).
func (c *RESTClient) ListAllTeams(ctx context.Context) ([]TeamListEntry, error) {
	var all []TeamListEntry
	for page := 1; ; page++ {
		path := fmt.Sprintf("/v2/team/list?page=%d&page_size=%d", page, litellmTeamListPageSize)
		raw, err := c.makeRequest(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		var list TeamListResponse
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("litellm: decode GET /v2/team/list: %w", err)
		}
		all = append(all, list.Teams...)
		// Stop once the last page is consumed. The empty-page guard covers an
		// unreliable total_pages (stop rather than loop to the cap on dupes).
		if list.TotalPages <= page || len(list.Teams) == 0 {
			break
		}
		if page >= listAllTeamsPageCap {
			// logr has no native WARN level; the package logs via c.log.Info.
			c.log.Info("WARN: litellm team list exceeds page cap; alias resolution may be incomplete — pagination stopped early",
				"total_pages", list.TotalPages, "page_cap", listAllTeamsPageCap, "total", list.Total)
			break
		}
	}
	return all, nil
}
