// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// UpsertTagBudget attaches (or re-points) a budget object to the tag.
// LiteLLM has no upsert: POST /tag/new refuses an existing tag, so the
// fallback is POST /tag/update with the same body. Both bodies are
// {"name", "max_budget", "budget_duration"?}.
//
// §9.1: only the tag name is logged by callers — never a budget amount tied
// to an identity beyond what the caller already holds.
func (c *RESTClient) UpsertTagBudget(ctx context.Context, name string, b TagBudget) error {
	body := struct {
		Name string `json:"name"`
		TagBudget
	}{Name: name, TagBudget: b}
	if _, newErr := c.makeRequest(ctx, "POST", "/tag/new", body); newErr == nil {
		return nil
	}
	if _, updErr := c.makeRequest(ctx, "POST", "/tag/update", body); updErr != nil {
		return fmt.Errorf("litellm: upsert tag budget %s: %w", name, updErr)
	}
	return nil
}

// TagInfo returns the tag's spend + budget, or (nil, nil) when LiteLLM does
// not know the tag (no budget configured yet — not an error).
func (c *RESTClient) TagInfo(ctx context.Context, name string) (*TagInfoEntry, error) {
	raw, err := c.makeRequest(ctx, "POST", "/tag/info", map[string][]string{"names": {name}})
	if err != nil {
		return nil, fmt.Errorf("litellm: POST /tag/info %s: %w", name, err)
	}
	var resp map[string]struct {
		Spend  float64    `json:"spend"`
		Budget *TagBudget `json:"litellm_budget_table"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("litellm: decode /tag/info %s: %w", name, err)
	}
	entry, ok := resp[name]
	if !ok {
		return nil, nil
	}
	return &TagInfoEntry{Name: name, Spend: entry.Spend, Budget: entry.Budget}, nil
}

// DeleteTagByName removes the tag (and with it its budget link). Absent is
// success — callers use it on revoke paths that may run twice.
func (c *RESTClient) DeleteTagByName(ctx context.Context, name string) error {
	if _, err := c.makeRequest(ctx, "POST", "/tag/delete", map[string]string{"name": name}); err != nil {
		if IsHTTPNotFound(err) {
			return nil
		}
		return fmt.Errorf("litellm: POST /tag/delete %s: %w", name, err)
	}
	return nil
}

// UserBudgetTag / EnvironmentBudgetTag / KeyBudgetTag are the three tag
// namespaces ACH stamps (see references/litellm-permission-model.md).
// EnvironmentBudgetTag keeps FWD-06's existing "environment:<name>" name.
func UserBudgetTag(email string) string      { return "user:" + NormalizeEmail(email) }
func EnvironmentBudgetTag(env string) string { return "environment:" + env }
func KeyBudgetTag(keyID string) string       { return "key:" + keyID }
