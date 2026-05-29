// SPDX-License-Identifier: Apache-2.0

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
	// LiteLLM v1.83 /user/info response top-level `teams` is []object
	// (each entry carries team_id + team_alias + …), not []string.
	// Direct json.Unmarshal into UserInfo blows up with "cannot
	// unmarshal object into Go struct field UserInfo.teams of type
	// string" — the SSO provisionUser path mistakes that for a
	// transport error and surfaces `litellm_unreachable`. Decode into
	// an envelope that pulls team UUIDs out of teams[*].team_id (the
	// shape downstream platformapi/teams/lookup.go consumes).
	var env struct {
		UserID    string `json:"user_id"`
		UserEmail string `json:"user_email"`
		Teams     []struct {
			TeamID    string `json:"team_id"`
			TeamAlias string `json:"team_alias"`
		} `json:"teams,omitempty"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /user/info: %w", err)
	}
	// LiteLLM v1.83 does NOT return 404 for unknown user_email. It
	// returns 200 with the admin placeholder user_id="default_user_id"
	// and a null user_email. Worse, /user/info also returns the
	// placeholder for EXISTING users whose email LiteLLM has on file
	// (the lookup-by-email path is broken upstream). Before giving
	// up and declaring "not found", fall back to /user/list which
	// DOES filter by user_email correctly.
	if env.UserID == "default_user_id" && env.UserEmail == "" {
		listPath := "/user/list?user_email=" + url.QueryEscape(email)
		listRaw, listErr := c.makeRequest(ctx, "GET", listPath, nil)
		if listErr != nil {
			return nil, listErr
		}
		var listEnv struct {
			Users []struct {
				UserID    string   `json:"user_id"`
				UserEmail string   `json:"user_email"`
				Teams     []string `json:"teams,omitempty"`
			} `json:"users"`
		}
		if err := json.Unmarshal(listRaw, &listEnv); err != nil {
			return nil, fmt.Errorf("litellm: decode GET /user/list: %w", err)
		}
		for _, u := range listEnv.Users {
			if u.UserEmail == email {
				idPath := "/user/info?user_id=" + url.QueryEscape(u.UserID)
				idRaw, idErr := c.makeRequest(ctx, "GET", idPath, nil)
				if idErr != nil {
					return &UserInfo{
						UserID:    u.UserID,
						UserEmail: u.UserEmail,
						Teams:     u.Teams,
					}, nil
				}
				// The by-id /user/info call exists ONLY to recover the
				// team-alias mapping. Its user_id is NOT authoritative —
				// LiteLLM v1.83 can return the "default_user_id" placeholder
				// here too (issue #36). Always keep u.UserID from /user/list.
				var idEnv struct {
					Teams []struct {
						TeamID    string `json:"team_id"`
						TeamAlias string `json:"team_alias"`
					} `json:"teams,omitempty"`
				}
				if err := json.Unmarshal(idRaw, &idEnv); err != nil {
					return &UserInfo{
						UserID:    u.UserID,
						UserEmail: u.UserEmail,
						Teams:     u.Teams,
					}, nil
				}
				outTeams := make([]string, 0, len(idEnv.Teams))
				for _, t := range idEnv.Teams {
					if t.TeamAlias != "" {
						outTeams = append(outTeams, t.TeamAlias)
					} else if t.TeamID != "" {
						outTeams = append(outTeams, t.TeamID)
					}
				}
				return &UserInfo{
					UserID:    u.UserID,
					UserEmail: u.UserEmail,
					Teams:     outTeams,
				}, nil
			}
		}
		return nil, ErrNotFound
	}
	out := &UserInfo{
		UserID:    env.UserID,
		UserEmail: env.UserEmail,
		Teams:     make([]string, 0, len(env.Teams)),
	}
	for _, t := range env.Teams {
		if t.TeamAlias != "" {
			out.Teams = append(out.Teams, t.TeamAlias)
		} else if t.TeamID != "" {
			out.Teams = append(out.Teams, t.TeamID)
		}
	}
	return out, nil
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
