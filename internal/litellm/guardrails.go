// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// guardrailListPaths are the two endpoints that together enumerate the proxy's
// guardrails. NEITHER is a superset of the other:
//
//	GET /guardrails/list     -> config-file guardrails only
//	GET /v2/guardrails/list  -> DB-registry guardrails only
//
// Measured against api.ackstorm.ai (LiteLLM v1.93.0) on 2026-07-28: the config
// list returned {"guardrails": []} while v2 returned both live guardrails, so
// querying only the documented /guardrails/list would have found nothing.
var guardrailListPaths = []string{"/guardrails/list", "/v2/guardrails/list"}

// ListGuardrails returns the union of both guardrail list endpoints, deduped by
// guardrail_name.
//
// COLLISION RULE. The two endpoints are disjoint in the deployment we could
// measure (config list empty, everything in the DB registry — G8), so a name
// present in both is unmeasured, not impossible: nothing stops an admin from
// registering a DB guardrail whose name matches a config-file one. Membership
// is unaffected either way — the name exists, so any Environment referencing it
// resolves. Only the DISPLAY attributes can disagree.
//
// So: identity is first-occurrence-wins (config before DB, fixed order), and if
// a later occurrence disagrees on mode or default_on the entry is flagged
// Ambiguous and its attributes are dropped downstream. The catalog then shows
// the name with a blank MODE rather than one of two values picked by coin flip.
// Do NOT "fix" this by asserting a precedence we have not measured, and do NOT
// fail the call — a display ambiguity must never block ek_ provisioning.
//
// Failure policy is deliberately strict: ONLY a 404 degrades an endpoint to
// "absent on this LiteLLM". Every other outcome — transport error, 5xx, 403,
// malformed body — fails the whole call.
//
// This matters because the Snapshotter treats a returned slice as the complete,
// authoritative set: it publishes it and calls ReplaceRuntimeCatalog, which
// tombstones every catalog row it did not see. Returning a partial union on a
// transient 500 would therefore mark live guardrails "missing" and flip
// Environments referencing them to unresolved — blocking ek_ minting on a blip.
// A hard error instead preserves the prior snapshot with Stale=true.
//
// An empty union returns ErrNotFound per REL-05, which the Snapshotter
// downgrades to an empty set.
func (c *RESTClient) ListGuardrails(ctx context.Context) ([]GuardrailEntry, error) {
	seen := make(map[string]int) // guardrail_name -> index in out
	out := []GuardrailEntry{}

	for _, path := range guardrailListPaths {
		raw, err := c.makeRequest(ctx, "GET", path, nil)
		if err != nil {
			if IsHTTPNotFound(err) {
				continue // route absent on this LiteLLM — the only tolerated miss
			}
			return nil, fmt.Errorf("litellm: GET %s: %w", path, err)
		}
		var resp guardrailListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("litellm: decode GET %s: %w", path, err)
		}
		for _, g := range resp.Guardrails {
			if g.GuardrailName == "" {
				continue
			}
			if i, dup := seen[g.GuardrailName]; dup {
				// Same name from the other endpoint. Keep the first entry's
				// identity; only flag it if the attributes actually disagree.
				if !slices.Equal(out[i].Mode, g.LiteLLMParams.Mode) ||
					out[i].DefaultOn != g.LiteLLMParams.DefaultOn {
					out[i].Ambiguous = true
				}
				continue
			}
			seen[g.GuardrailName] = len(out)
			out = append(out, GuardrailEntry{
				GuardrailID:   g.GuardrailID,
				GuardrailName: g.GuardrailName,
				Mode:          g.LiteLLMParams.Mode,
				DefaultOn:     g.LiteLLMParams.DefaultOn,
			})
		}
	}

	if len(out) == 0 { // REL-05 length check
		return nil, ErrNotFound
	}
	return out, nil
}
