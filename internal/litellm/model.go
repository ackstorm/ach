// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListModels issues GET /v1/model/info and returns the full registered
// model set. Mirrors the ListMCPServers shape in mcp.go: GET against the
// info endpoint, length-check len(list.Data), return ErrNotFound on
// empty (REL-05).
//
// D-13 (snapshot semantics): the Plan 07 LiteLLM-snapshot Runnable wraps
// errors.Is(err, ErrNotFound) into an empty slice — an Environment that
// lists a model against a LiteLLM with zero models is the empty
// intersection, NOT an error. Direct callers should preserve that
// semantic.
//
// §9.1: only the status code is logged — no response body content.
func (c *RESTClient) ListModels(ctx context.Context) ([]ModelInfoResponse, error) {
	raw, err := c.makeRequest(ctx, "GET", "/v1/model/info", nil)
	if err != nil {
		return nil, err
	}
	var list ModelListResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/model/info: %w", err)
	}
	if len(list.Data) == 0 { // REL-05 length check before indexing
		return nil, ErrNotFound
	}
	return list.Data, nil
}
