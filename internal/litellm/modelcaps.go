// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ModelCaps is the allow-listed part of a deployment's model_info
// (GET /v2/model/info). nil = LiteLLM did not say.
type ModelCaps struct {
	Mode                    *string  `json:"mode"`
	MaxInputTokens          *float64 `json:"max_input_tokens"`
	MaxOutputTokens         *float64 `json:"max_output_tokens"`
	InputCostPerToken       *float64 `json:"input_cost_per_token"`
	OutputCostPerToken      *float64 `json:"output_cost_per_token"`
	CacheReadInputTokenCost *float64 `json:"cache_read_input_token_cost"`
	SupportsVision          *bool    `json:"supports_vision"`
	SupportsPDFInput        *bool    `json:"supports_pdf_input"`
	SupportsAudioInput      *bool    `json:"supports_audio_input"`
	SupportsVideoInput      *bool    `json:"supports_video_input"`
	SupportsFunctionCalling *bool    `json:"supports_function_calling"`
	SupportsReasoning       *bool    `json:"supports_reasoning"`
}

const maxModelInfoPages = 50 // ponytail: 100 rows/page → 5000 deployments; raise if a proxy outgrows it

// ListDeploymentCapabilities is GET /v2/model/info as ADMIN: model_name →
// capabilities, first deployment of a name wins. It only DESCRIBES models;
// which ones a user may use is decided under the user's key
// (UserView.ListModelGroups) — callers look up only names that returned.
// Decoding names only model_name + model_info, so litellm_params (upstream
// credentials) never enters memory as data.
func (c *RESTClient) ListDeploymentCapabilities(ctx context.Context) (map[string]ModelCaps, error) {
	out := map[string]ModelCaps{}
	for page, pages := 1, 1; page <= min(pages, maxModelInfoPages); page++ {
		raw, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v2/model/info?page=%d&size=100", page), nil)
		if err != nil {
			return nil, err
		}
		var body struct {
			TotalPages int `json:"total_pages"`
			Data       []struct {
				ModelName string    `json:"model_name"`
				ModelInfo ModelCaps `json:"model_info"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("litellm: decode GET /v2/model/info: %w", err)
		}
		pages = max(body.TotalPages, 1)
		for _, d := range body.Data {
			if _, seen := out[d.ModelName]; d.ModelName != "" && !seen {
				out[d.ModelName] = d.ModelInfo
			}
		}
	}
	return out, nil
}

// ModelGroupAliases is router_settings.model_group_alias (alias → target
// group) from GET /get/config/callbacks, as ADMIN. That payload also
// carries callback configuration (secrets): only the alias map is decoded.
// A target is a string or {"model": "<target>", ...}; other shapes drop.
func (c *RESTClient) ModelGroupAliases(ctx context.Context) (map[string]string, error) {
	raw, err := c.makeRequest(ctx, "GET", "/get/config/callbacks", nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		RouterSettings struct {
			ModelGroupAlias map[string]json.RawMessage `json:"model_group_alias"`
		} `json:"router_settings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /get/config/callbacks: %w", err)
	}
	out := map[string]string{}
	for alias, t := range body.RouterSettings.ModelGroupAlias {
		var s string
		var obj struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(t, &s) == nil && s != "" {
			out[alias] = s
		} else if json.Unmarshal(t, &obj) == nil && obj.Model != "" {
			out[alias] = obj.Model
		}
	}
	return out, nil
}
