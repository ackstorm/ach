// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// UserNew issues POST /user/new. The req body is the union of fields ACH
// ever populates (email, optional user_id, optional teams). LiteLLM
// returns the canonical UserInfo on success; this method decodes that
// into *UserInfo so callers (Phase 3 SSO handler, Plan 03-07) can read
// the LiteLLM-assigned user_id without a second round-trip.
//
// Per KEY-10: ACH NEVER sets max_budget on first-SSO LiteLLM user
// creation — UserNewRequest has no max_budget field by design. If a future
// plan needs to override the deployer's LiteLLM defaults, extend the
// struct (and revisit KEY-10).
//
// Phase 3 D-25 contract. Errors propagate verbatim from makeRequest:
//   - 401  → *Auth401Error (errors.As resolvable; REL-06)
//   - 4xx  → fmt.Errorf with status + LiteLLM error code (NEVER body — §9.1)
//   - 5xx  → transient fmt.Errorf
func (c *RESTClient) UserNew(ctx context.Context, req *UserNewRequest) (*UserInfo, error) {
	raw, err := c.makeRequest(ctx, "POST", "/user/new", req)
	if err != nil {
		return nil, err
	}
	var out UserInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode POST /user/new: %w", err)
	}
	return &out, nil
}

// UserInfoByEmail issues GET /user/info?user_email=<url-escaped email>.
// Returns *UserInfo on success.
//
// 404 handling: this method does NOT translate 404 → ErrNotFound. Phase 3
// D-25 keeps the makeRequest 4xx convention (generic error string carrying
// the status code) so the SSO handler (Plan 03-07) can branch with
// strings.Contains(err.Error(), "404") in line with the "user not found —
// fall through to UserNew" first-time-SSO path. This keeps the type-level
// surface narrow and matches the existing per-domain helper idiom in
// keyinfo.go / team.go.
//
// The email value MUST be url-escaped before being placed into the query
// string. url.QueryEscape preserves `+` as `%2B` (correct — bare `+`
// decodes as space per RFC 3986 query semantics) and encodes `@` as
// `%40` (LiteLLM tolerates either form but ACH uses QueryEscape for
// determinism across all per-domain helpers).
func (c *RESTClient) UserInfoByEmail(ctx context.Context, email string) (*UserInfo, error) {
	path := "/user/info?user_email=" + url.QueryEscape(email)
	raw, err := c.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out UserInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /user/info: %w", err)
	}
	return &out, nil
}

// TeamMemberAdd issues POST /team/member_add with a nested {"member": {...}}
// body shape (LiteLLM v1.83.10 contract).
//
// The role parameter is passed through verbatim to LiteLLM; Phase 3
// callers (Plan 03-07 SSO handler) pass "user" for the §8.1 default-team
// enrollment, and Plan 03-08 may pass other roles depending on §8.2
// membership semantics.
//
// Idempotency: LiteLLM treats "add a user already on the team" as a 4xx.
// This method does NOT swallow that — the caller decides (Plan 03-07 uses
// errors.As on Auth401Error first, then strings.Contains("400") /
// strings.Contains("already") to detect already-a-member; the duplicate-add
// path is logged but does NOT propagate as failure).
//
// Per Phase 3 D-25 the success path returns nil (the LiteLLM response
// body — typically the updated team object — is discarded; callers that
// need post-add team state should call ListTeamsByAlias).
func (c *RESTClient) TeamMemberAdd(ctx context.Context, teamID, userID, role string) error {
	body := &TeamMemberAddRequest{
		TeamID: teamID,
		Member: TeamMember{UserID: userID, Role: role},
	}
	_, err := c.makeRequest(ctx, "POST", "/team/member_add", body)
	return err
}
