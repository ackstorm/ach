// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// UpsertTagBudget points the tag at a budget object whose budget_id IS the
// tag name, so /budget/list reads as user:<email> / environment:<env> /
// key:<id> instead of a wall of uuids.
//
// LiteLLM has no upsert on either object, and POST /tag/update cannot bind a
// budget_id at all (500, measured) — so an existing tag holding a different
// budget (LiteLLM auto-creates budgetless tag rows from x-litellm-tags
// traffic) is deleted and recreated. Spend is not lost: enforcement counts
// LiteLLM_DailyTagSpend, which is keyed by tag name in its own table.
//
// §9.1: only the tag name is logged by callers — never a budget amount tied
// to an identity beyond what the caller already holds.
func (c *RESTClient) UpsertTagBudget(ctx context.Context, name string, b TagBudget) error {
	b.BudgetID = name
	// New first: /budget/update on a missing id returns 200 null and does nothing.
	if _, err := c.makeRequest(ctx, "POST", "/budget/new", b); err != nil {
		if _, err := c.makeRequest(ctx, "POST", "/budget/update", b); err != nil {
			return fmt.Errorf("litellm: upsert budget %s: %w", name, err)
		}
	}
	info, err := c.TagInfo(ctx, name)
	if err != nil {
		return fmt.Errorf("litellm: read tag %s: %w", name, err)
	}
	if info != nil && info.Budget != nil && info.Budget.BudgetID == name {
		return nil // already bound
	}
	if info != nil {
		if err := c.DeleteTagByName(ctx, name); err != nil {
			return fmt.Errorf("litellm: rebind tag %s: %w", name, err)
		}
	}
	bind := struct {
		Name     string `json:"name"`
		BudgetID string `json:"budget_id"`
	}{Name: name, BudgetID: name}
	if _, err := c.makeRequest(ctx, "POST", "/tag/new", bind); err != nil {
		return fmt.Errorf("litellm: bind tag %s: %w", name, err)
	}
	return nil
}

// DeleteBudget removes a budget object. A missing budget is success.
func (c *RESTClient) DeleteBudget(ctx context.Context, id string) error {
	if _, err := c.makeRequest(ctx, "POST", "/budget/delete", map[string]string{"id": id}); err != nil {
		if IsHTTPNotFound(err) {
			return nil
		}
		return fmt.Errorf("litellm: delete budget %s: %w", id, err)
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

// DeleteKeyBudget reaps an ek_'s own "key:<id>" tag AND its budget object —
// a tag delete orphans the budget row, so the two always travel together.
// Both halves are attempted even if the first fails, and both are idempotent
// (absent = success). Every caller is a revoke path where the credential is
// already dead, so the error is for logging, never for failing the revoke.
func DeleteKeyBudget(ctx context.Context, c Client, keyID string) error {
	tag := KeyBudgetTag(keyID)
	return errors.Join(c.DeleteTagByName(ctx, tag), c.DeleteBudget(ctx, tag))
}

// UserBudgetTag / EnvironmentBudgetTag / KeyBudgetTag are the three tag
// namespaces ACH stamps (see references/litellm-permission-model.md).
// EnvironmentBudgetTag keeps FWD-06's existing "environment:<name>" name.
func UserBudgetTag(email string) string      { return "user:" + NormalizeEmail(email) }
func EnvironmentBudgetTag(env string) string { return "environment:" + env }
func KeyBudgetTag(keyID string) string       { return "key:" + keyID }
