// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListMCPServers issues GET /v1/mcp/server. LiteLLM returns a bare
// array; we unmarshal into []MCPServerEntry and length-check it per
// REL-05 (Pattern 4 in 01-RESEARCH) → ErrNotFound on empty.
func (c *RESTClient) ListMCPServers(ctx context.Context) ([]MCPServerEntry, error) {
	raw, err := c.makeRequest(ctx, "GET", "/v1/mcp/server", nil)
	if err != nil {
		return nil, err
	}
	var arr []MCPServerEntry
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/mcp/server: %w", err)
	}
	if len(arr) == 0 { // REL-05 length check before indexing
		return nil, ErrNotFound
	}
	return arr, nil
}
