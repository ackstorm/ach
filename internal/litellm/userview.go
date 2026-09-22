// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ackstorm/ach/internal/observability"
)

// UserView issues LiteLLM reads as ONE user's own virtual key over the
// shared RESTClient transport (timeouts, response cap, redaction, error
// mapping all inherited). Measured 2026-09-21 (docs/plans/pk-scope-
// measurement.md): every console read answers scoped to the key's
// user_id. A failure here is never retried with the master key.
type UserView struct {
	c   *RESTClient
	key string
}

// AsUser binds a user's decrypted sk- to the shared transport. The
// client's own master key is untouched.
func (c *RESTClient) AsUser(key string) *UserView { return &UserView{c: c, key: key} }

// ModelGroupInfo is one GET /model_group/info row, allow-listed to the
// public presentation fields (alitellm-auth _project_model_group): no
// litellm_params, no api_base, no credentials.
type ModelGroupInfo struct {
	Name                    string   `json:"name"`
	Providers               []string `json:"providers"`
	Mode                    *string  `json:"mode"`
	MaxInputTokens          *float64 `json:"max_input_tokens"`
	MaxOutputTokens         *float64 `json:"max_output_tokens"`
	InputCostPerToken       *float64 `json:"input_cost_per_token"`
	OutputCostPerToken      *float64 `json:"output_cost_per_token"`
	SupportsVision          bool     `json:"supports_vision"`
	SupportsFunctionCalling bool     `json:"supports_function_calling"`
	SupportsReasoning       bool     `json:"supports_reasoning"`
	SupportsWebSearch       bool     `json:"supports_web_search"`
}

// ListModelGroups is GET /model_group/info as the user — the key-scoped
// public catalog (NOT /v1/model/info, the admin view). Decoding names only
// the allow-listed fields, so nothing else ever reaches a caller.
func (u *UserView) ListModelGroups(ctx context.Context) ([]ModelGroupInfo, error) {
	raw, err := u.c.makeRequestAs(ctx, u.key, "GET", "/model_group/info", nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []struct {
			ModelGroup              string   `json:"model_group"`
			Providers               []string `json:"providers"`
			Mode                    *string  `json:"mode"`
			MaxInputTokens          *float64 `json:"max_input_tokens"`
			MaxOutputTokens         *float64 `json:"max_output_tokens"`
			InputCostPerToken       *float64 `json:"input_cost_per_token"`
			OutputCostPerToken      *float64 `json:"output_cost_per_token"`
			SupportsVision          bool     `json:"supports_vision"`
			SupportsFunctionCalling bool     `json:"supports_function_calling"`
			SupportsReasoning       bool     `json:"supports_reasoning"`
			SupportsWebSearch       bool     `json:"supports_web_search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /model_group/info: %w", err)
	}
	out := make([]ModelGroupInfo, 0, len(body.Data))
	for _, m := range body.Data {
		providers := m.Providers
		if providers == nil {
			providers = []string{}
		}
		out = append(out, ModelGroupInfo{
			Name: m.ModelGroup, Providers: providers, Mode: m.Mode,
			MaxInputTokens: m.MaxInputTokens, MaxOutputTokens: m.MaxOutputTokens,
			InputCostPerToken: m.InputCostPerToken, OutputCostPerToken: m.OutputCostPerToken,
			SupportsVision: m.SupportsVision, SupportsFunctionCalling: m.SupportsFunctionCalling,
			SupportsReasoning: m.SupportsReasoning, SupportsWebSearch: m.SupportsWebSearch,
		})
	}
	return out, nil
}

// ListMCPServers is GET /v1/mcp/server as the user: a bare array (a
// {"data"|"servers": […]} wrapper tolerated). An empty catalog is an empty
// slice, not ErrNotFound — the console shows "none", not an error.
func (u *UserView) ListMCPServers(ctx context.Context) ([]MCPServerEntry, error) {
	raw, err := u.c.makeRequestAs(ctx, u.key, "GET", "/v1/mcp/server", nil)
	if err != nil {
		return nil, err
	}
	var arr []MCPServerEntry
	if err := decodeBareOrWrapped(raw, &arr, "data", "servers"); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/mcp/server: %w", err)
	}
	return arr, nil
}

// ListA2AAgents is GET /v1/agents?health_check=false as the user. Same
// tolerance as ListMCPServers.
func (u *UserView) ListA2AAgents(ctx context.Context) ([]AgentEntry, error) {
	raw, err := u.c.makeRequestAs(ctx, u.key, "GET", "/v1/agents?health_check=false", nil)
	if err != nil {
		return nil, err
	}
	var arr []AgentEntry
	if err := decodeBareOrWrapped(raw, &arr, "data", "agents"); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/agents: %w", err)
	}
	return arr, nil
}

// dailyActivityPageCap bounds the /user/daily/activity pagination loop —
// far beyond any real console window (a real user daily-activity window
// pages in single digits).
const dailyActivityPageCap = 200

// dailyActivityPage is one page of GET /user/daily/activity. Metadata is
// decoded with json.Number (UseNumber) so mergeDailyActivityMetadata sums
// without float round-trip loss.
type dailyActivityPage struct {
	Results  []json.RawMessage `json:"results"`
	Metadata map[string]any    `json:"metadata"`
}

// mergeDailyActivityMetadata folds one page's metadata into the running
// total: every "total_*" figure is SUMMED across pages (LiteLLM's
// per-response metadata is only a per-page partial — verified live on
// v1.89.2), except "total_pages" itself, which is pagination bookkeeping,
// not a metric. Every other field (has_more, page, page_size, ...) is
// last-page-wins — nothing downstream reads them.
func mergeDailyActivityMetadata(dst, src map[string]any) {
	for k, v := range src {
		if k == "total_pages" || !strings.HasPrefix(k, "total_") {
			dst[k] = v
			continue
		}
		n, ok := v.(json.Number)
		if !ok {
			dst[k] = v
			continue
		}
		f, _ := n.Float64()
		cur := 0.0
		if cv, exists := dst[k]; exists {
			if cn, ok2 := cv.(json.Number); ok2 {
				cur, _ = cn.Float64()
			}
		}
		dst[k] = json.Number(strconv.FormatFloat(cur+f, 'f', -1, 64))
	}
}

// DailyActivity follows GET /user/daily/activity pagination to completion
// (page_size 100, safety cap dailyActivityPageCap pages), concatenating
// results[] and summing every numeric metadata.total_* across pages. The
// window [startDate, endDate] is inclusive on both ends (§10.2 fact 2 —
// unlike SpendLogsV2). No user_id param: the endpoint self-scopes to the
// caller's key (measured 2026-09-21, docs/plans/pk-scope-measurement.md).
func (u *UserView) DailyActivity(ctx context.Context, startDate, endDate string) (observability.DailyActivity, error) {
	merged := observability.DailyActivity{Metadata: map[string]any{}}
	for page := 1; page <= dailyActivityPageCap; page++ {
		q := url.Values{}
		q.Set("start_date", startDate)
		q.Set("end_date", endDate)
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", "100")
		raw, err := u.c.makeRequestAs(ctx, u.key, "GET", "/user/daily/activity?"+q.Encode(), nil)
		if err != nil {
			return observability.DailyActivity{}, err
		}
		var pg dailyActivityPage
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&pg); err != nil {
			return observability.DailyActivity{}, fmt.Errorf("litellm: decode GET /user/daily/activity: %w", err)
		}
		merged.Results = append(merged.Results, pg.Results...)
		mergeDailyActivityMetadata(merged.Metadata, pg.Metadata)
		hasMore, _ := pg.Metadata["has_more"].(bool)
		if !hasMore {
			break
		}
	}
	return merged, nil
}

// spendLogsPage is one page of GET /spend/logs/v2.
type spendLogsPage struct {
	Data       []observability.SpendLogRow `json:"data"`
	TotalPages int                         `json:"total_pages"`
}

// SpendLogsV2 pages GET /spend/logs/v2 NEWEST-first (sort_by=startTime
// desc, page_size 100), stopping at maxPages even when more pages exist —
// truncated reports that case so the caller can flip LatencyContract.Sampled.
// endDateExclusive is EXCLUSIVE on this endpoint (§10.2 fact 2): the caller
// passes last_day + 1. No user_id param (self-scoped, same as
// DailyActivity) — a 401 (role=unknown, §10.2 fact 1) propagates as
// *Auth401Error, never retried with the master key.
func (u *UserView) SpendLogsV2(ctx context.Context, startDate, endDateExclusive string, maxPages int) ([]observability.SpendLogRow, bool, error) {
	var rows []observability.SpendLogRow
	lastTotalPages, fetchedPages := 0, 0
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		q.Set("start_date", startDate)
		q.Set("end_date", endDateExclusive)
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", "100")
		q.Set("sort_by", "startTime")
		q.Set("sort_order", "desc")
		raw, err := u.c.makeRequestAs(ctx, u.key, "GET", "/spend/logs/v2?"+q.Encode(), nil)
		if err != nil {
			return nil, false, err
		}
		var pg spendLogsPage
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, false, fmt.Errorf("litellm: decode GET /spend/logs/v2: %w", err)
		}
		rows = append(rows, pg.Data...)
		lastTotalPages, fetchedPages = pg.TotalPages, page
		if page >= pg.TotalPages {
			break
		}
	}
	return rows, lastTotalPages > fetchedPages, nil
}

// UserInfo is GET /user/info as the user — no user_id param needed, the
// endpoint scopes to the key's own user (measured 2026-09-21).
func (u *UserView) UserInfo(ctx context.Context) (observability.UserInfo, error) {
	raw, err := u.c.makeRequestAs(ctx, u.key, "GET", "/user/info", nil)
	if err != nil {
		return observability.UserInfo{}, err
	}
	var body struct {
		UserInfo struct {
			Spend          *float64 `json:"spend"`
			MaxBudget      *float64 `json:"max_budget"`
			BudgetDuration *string  `json:"budget_duration"`
		} `json:"user_info"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return observability.UserInfo{}, fmt.Errorf("litellm: decode GET /user/info: %w", err)
	}
	return observability.UserInfo{
		Spend: body.UserInfo.Spend, MaxBudget: body.UserInfo.MaxBudget, BudgetDuration: body.UserInfo.BudgetDuration,
	}, nil
}

// teamMemberRow is one team_memberships[] entry of GET /team/info.
type teamMemberRow struct {
	UserID string   `json:"user_id"`
	Spend  *float64 `json:"spend"`
	Budget *struct {
		MaxBudget      *float64 `json:"max_budget"`
		BudgetDuration *string  `json:"budget_duration"`
	} `json:"litellm_budget_table"`
}

// teamInfoMemberships decodes the team_memberships[] LiteLLM returns at the
// TOP level of GET /team/info, a sibling of team_info — not nested under it.
func teamInfoMemberships(raw []byte) ([]teamMemberRow, error) {
	var flat struct {
		TeamMemberships []teamMemberRow `json:"team_memberships"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, err
	}
	return flat.TeamMemberships, nil
}

// TeamMemberBudget is GET /team/info?team_id=<own shell> as the user — the
// team_memberships[] row whose user_id == email, projected to the ENFORCED
// per-member budget. Returns nil (no error) when the caller has no
// membership row, or when the row carries neither a max_budget nor a
// spend figure (both JSON null) — genuinely no budget data to report, as
// opposed to a real zero spend.
func (u *UserView) TeamMemberBudget(ctx context.Context, teamID, email string) (*observability.MemberBudget, error) {
	raw, err := u.c.makeRequestAs(ctx, u.key, "GET", "/team/info?team_id="+url.QueryEscape(teamID), nil)
	if err != nil {
		return nil, err
	}
	members, err := teamInfoMemberships(raw)
	if err != nil {
		return nil, fmt.Errorf("litellm: decode GET /team/info: %w", err)
	}
	for _, m := range members {
		if m.UserID != email {
			continue
		}
		var maxBudget *float64
		var duration *string
		if m.Budget != nil {
			maxBudget, duration = m.Budget.MaxBudget, m.Budget.BudgetDuration
		}
		if maxBudget == nil && m.Spend == nil {
			return nil, nil
		}
		current := 0.0
		if m.Spend != nil {
			current = *m.Spend
		}
		return &observability.MemberBudget{MaxBudget: maxBudget, Current: current, BudgetDuration: duration}, nil
	}
	return nil, nil
}

// decodeBareOrWrapped decodes a bare JSON array, or the first of the named
// keys of a wrapping object, into out.
func decodeBareOrWrapped(raw []byte, out any, keys ...string) error {
	if len(raw) > 0 && raw[0] == '[' {
		return json.Unmarshal(raw, out)
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return err
	}
	for _, k := range keys {
		if v, ok := wrapper[k]; ok {
			return json.Unmarshal(v, out)
		}
	}
	return nil // no known key: empty
}
