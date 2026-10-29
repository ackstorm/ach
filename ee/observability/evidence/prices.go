// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"

	"github.com/ackstorm/ach/internal/litellm"
)

// PriceCatalog is the admin LiteLLM surface the list prices come from.
type PriceCatalog interface {
	ListDeploymentCapabilities(ctx context.Context) (map[string]litellm.ModelCaps, error)
	ModelGroupAliases(ctx context.Context) (map[string]string, error)
}

// LiteLLMPrices resolves list prices by the public alias a caller used. An
// alias such as ackstorm.smart has no deployment of its own: its price is the
// one of the group it points at (router_settings.model_group_alias).
//
// ponytail: two admin reads per cost request, no cache; cache them (like
// opencode's capsCache) if the cost page is polled.
type LiteLLMPrices struct{ Catalog PriceCatalog }

func (p LiteLLMPrices) Prices(ctx context.Context) (map[string]ModelPrice, error) {
	caps, err := p.Catalog.ListDeploymentCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	aliases, err := p.Catalog.ModelGroupAliases(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ModelPrice, len(caps)+len(aliases))
	for name, c := range caps {
		out[name] = ModelPrice{Input: c.InputCostPerToken, CacheRead: c.CacheReadInputTokenCost}
	}
	for alias, target := range aliases {
		if c, ok := caps[target]; ok {
			if _, own := caps[alias]; !own {
				out[alias] = ModelPrice{Input: c.InputCostPerToken, CacheRead: c.CacheReadInputTokenCost}
			}
		}
	}
	return out, nil
}
