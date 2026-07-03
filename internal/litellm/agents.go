// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListA2AAgents issues GET /v1/agents?health_check=false. The name reflects
// D-13's A2A-agent terminology; the LiteLLM endpoint name (/v1/agents) is
// unchanged. LiteLLM returns a bare array; we wrap into
// AgentListResponse{Data: ...} for length-check uniformity per REL-05
// (ErrNotFound on empty, length-checked before indexing). Used by the Plan 07
// LiteLLM-snapshot Runnable so an Environment's `spec.runtime.a2aAgents`
// intersection against the live registration set drives the
// ExecutionResourcesResolved condition.
func (c *RESTClient) ListA2AAgents(ctx context.Context) ([]AgentEntry, error) {
	raw, err := c.makeRequest(ctx, "GET", "/v1/agents?health_check=false", nil)
	if err != nil {
		return nil, err
	}
	var arr []AgentEntry
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/agents: %w", err)
	}
	list := AgentListResponse{Data: arr}
	if len(list.Data) == 0 { // REL-05 length check before indexing
		return nil, ErrNotFound
	}
	return list.Data, nil
}
