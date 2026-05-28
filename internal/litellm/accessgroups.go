// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
)

// CreateAccessGroup issues POST /v1/access_group. Returns the
// AccessGroupResponse (UUID, name, current bindings). Replaces the
// legacy POST /access_group/new flow whose validator rejected empty
// model_names; the /v1 endpoint accepts an empty-resource creation.
//
// The reconciler is expected to call GetAccessGroupByName first and only
// POST when the result is nil — `already exists` semantics are owned at
// the controller layer, not here (per issue-17 plan §4 decision:
// list-first-then-create).
func (c *RESTClient) CreateAccessGroup(ctx context.Context, req AccessGroupCreateRequest) (*AccessGroupResponse, error) {
	if req.AccessGroupName == "" {
		return nil, fmt.Errorf("litellm: CreateAccessGroup: empty access_group_name")
	}
	raw, err := c.makeRequest(ctx, "POST", "/v1/access_group", req)
	if err != nil {
		return nil, fmt.Errorf("litellm: POST /v1/access_group (name=%s): %w", req.AccessGroupName, err)
	}
	var resp AccessGroupResponse
	if uerr := json.Unmarshal(raw, &resp); uerr != nil {
		return nil, fmt.Errorf("litellm: decode POST /v1/access_group: %w", uerr)
	}
	return &resp, nil
}

// GetAccessGroupByName performs GET /v1/access_group and returns the
// matching entry by access_group_name. (nil, nil) when not found — the
// reconciler treats this as "needs POST to create".
//
// Decision (issue #17): no status field stores the UUID; we resolve by
// name on every reconcile. The N here is small (O(10s) of access groups
// in production per Hub §6.1), so a single list call per reconcile is
// acceptable.
func (c *RESTClient) GetAccessGroupByName(ctx context.Context, name string) (*AccessGroupResponse, error) {
	if name == "" {
		return nil, fmt.Errorf("litellm: GetAccessGroupByName: empty name")
	}
	raw, err := c.makeRequest(ctx, "GET", "/v1/access_group", nil)
	if err != nil {
		return nil, fmt.Errorf("litellm: GET /v1/access_group: %w", err)
	}
	var list []AccessGroupResponse
	if uerr := json.Unmarshal(raw, &list); uerr != nil {
		return nil, fmt.Errorf("litellm: decode GET /v1/access_group: %w", uerr)
	}
	for i := range list {
		if list[i].AccessGroupName == name {
			out := list[i]
			return &out, nil
		}
	}
	return nil, nil
}

// UpdateAccessGroup issues PUT /v1/access_group/{id}. Used by the
// reconciler's drift-correction branch when GetAccessGroupByName found
// an existing group with diverged bindings.
func (c *RESTClient) UpdateAccessGroup(ctx context.Context, id string, req AccessGroupUpdateRequest) (*AccessGroupResponse, error) {
	if id == "" {
		return nil, fmt.Errorf("litellm: UpdateAccessGroup: empty id")
	}
	raw, err := c.makeRequest(ctx, "PUT", "/v1/access_group/"+id, req)
	if err != nil {
		return nil, fmt.Errorf("litellm: PUT /v1/access_group/%s: %w", id, err)
	}
	var resp AccessGroupResponse
	if uerr := json.Unmarshal(raw, &resp); uerr != nil {
		return nil, fmt.Errorf("litellm: decode PUT /v1/access_group/%s: %w", id, uerr)
	}
	return &resp, nil
}

// DeleteAccessGroupByID issues DELETE /v1/access_group/{id}. The
// underlying makeRequest treats 404 as success per the existing §7.7
// idempotent-delete contract, so a re-reconcile after a partially-
// completed §6.5 sequence does NOT spurious-error.
func (c *RESTClient) DeleteAccessGroupByID(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("litellm: DeleteAccessGroupByID: empty id")
	}
	_, err := c.makeRequest(ctx, "DELETE", "/v1/access_group/"+id, nil)
	return err
}

// DeleteAccessGroup is the high-level helper the §6.5 finalizer path
// calls. It list-by-name → DELETE-by-id, treating absent-name as the
// idempotent-success branch (matches the §7.7 contract). The Environment
// reconciler's existing r.LiteLLM.DeleteAccessGroup(ctx, env.Name) call
// site does not change.
func (c *RESTClient) DeleteAccessGroup(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("litellm: DeleteAccessGroup: empty name")
	}
	found, err := c.GetAccessGroupByName(ctx, name)
	if err != nil {
		return fmt.Errorf("litellm: DeleteAccessGroup(%s) lookup: %w", name, err)
	}
	if found == nil {
		return nil // already gone — idempotent
	}
	if derr := c.DeleteAccessGroupByID(ctx, found.AccessGroupID); derr != nil {
		return fmt.Errorf("litellm: DeleteAccessGroup(%s, id=%s): %w", name, found.AccessGroupID, derr)
	}
	return nil
}

// DeleteTag is preserved verbatim from the legacy file — §6.5 step 3
// "delete tag" is orthogonal to the access-group migration.
func (c *RESTClient) DeleteTag(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/tag/"+name, nil)
	return err
}

// Removed in issue #17:
//   - BindTeamToAccessGroup (use AccessGroupCreateRequest.AssignedTeamIDs)
//   - ListAccessGroupBindings (use AccessGroupResponse.AssignedTeamIDs)
