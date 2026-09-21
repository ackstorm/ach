// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
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
