// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// CreateAgent issues POST /v1/agents.
func (c *RESTClient) CreateAgent(ctx context.Context, req *AgentConfig) (*AgentEntry, error) {
	raw, err := c.makeRequest(ctx, "POST", "/v1/agents", req)
	if err != nil {
		return nil, err
	}
	var out AgentEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode POST /v1/agents: %w", err)
	}
	return &out, nil
}

// UpdateAgent issues PUT /v1/agents/{agentID}. PUT here IS wholesale-
// replace per spike Probe 7 (the only kind where §5.1 holds empirically
// — see 01-01-SUMMARY.md).
func (c *RESTClient) UpdateAgent(ctx context.Context, agentID string, req *AgentConfig) (*AgentEntry, error) {
	raw, err := c.makeRequest(ctx, "PUT", "/v1/agents/"+agentID, req)
	if err != nil {
		return nil, err
	}
	var out AgentEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("litellm: decode PUT /v1/agents/{id}: %w", err)
	}
	return &out, nil
}

// DeleteAgent issues DELETE /v1/agents/{agentID}.
func (c *RESTClient) DeleteAgent(ctx context.Context, agentID string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/v1/agents/"+agentID, nil)
	return err
}

// ListAgents issues GET /v1/agents?health_check=false. LiteLLM returns
// a bare array; we wrap into AgentListResponse{Data: ...} for length-
// check uniformity per REL-05.
//
// Length-checks len(list.Data) before indexing → ErrNotFound on empty.
func (c *RESTClient) ListAgents(ctx context.Context) ([]AgentEntry, error) {
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

// ListA2AAgents is the ACH-domain wrapper for ListAgents. The wrapper
// name reflects D-13's A2A-agent terminology; the LiteLLM endpoint name
// (/v1/agents) is unchanged. Used by the Plan 07 LiteLLM-snapshot
// Runnable so an Environment's `spec.runtime.a2aAgents` intersection
// against the live LiteLLM registration set drives the
// ExecutionResourcesResolved condition.
func (c *RESTClient) ListA2AAgents(ctx context.Context) ([]AgentEntry, error) {
	return c.ListAgents(ctx)
}
