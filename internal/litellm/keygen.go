// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// KeyGenerate issues POST /key/generate.
//
// Phase 3 D-13 contract: ACH generates the bearer plaintext SERVER-SIDE
// (crypto/rand → base32 no-pad → "pk_<26>" or "ek_<26>") and passes it
// via req.Key. LiteLLM stores ACH's plaintext verbatim in its `key`
// column so the LiteLLM virtual key inherits the ACH `pk_*`/`ek_*`
// prefix; the LiteLLM-INTERNAL opaque hex `token` (a different identifier)
// is returned in KeyGenerateResponse.Token and stored by Phase 3 into
// personal_keys.litellm_token / environment_keys.litellm_token
// (Phase 02.2 D-01) for the orphan-cleanup loop (D-16) and revocation
// flows (D-14 / D-15).
//
// KEY-10 invariant: ACH NEVER sets max_budget on first-SSO LiteLLM user
// creation. KeyGenerateRequest.MaxBudget is *float64 with omitempty so
// callers pass nil and the field drops from the wire payload entirely.
// LiteLLM falls back to whatever default the deployer configured server-side.
//
// AccessGroups carries the LiteLLM access-group name list — Phase 3 ek_
// creation passes a single-element slice ([]string{"<environment>"}) so
// LiteLLM applies the access-group budget policy at request time.
//
// Errors propagate verbatim from makeRequest (REL-04 drain+close,
// REL-06 *Auth401Error, §9.1 no-body-in-error).
func (c *RESTClient) KeyGenerate(ctx context.Context, req *KeyGenerateRequest) (*KeyGenerateResponse, error) {
	raw, err := c.makeRequest(ctx, "POST", "/key/generate", req)
	if err != nil {
		return nil, err
	}
	var out KeyGenerateResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode POST /key/generate: %w", err)
	}
	return &out, nil
}
