// SPDX-License-Identifier: Apache-2.0

package connection

import (
	"context"
	"errors"
	"fmt"

	"github.com/ackstorm/ach/internal/litellm"
)

var ErrNotReady = errors.New("litellm connection not ready")

// Client delegates litellm.Client calls to the current ready connection.
type Client struct {
	cache CacheReader
}

func NewClient(cache CacheReader) *Client {
	return &Client{cache: cache}
}

func (c *Client) current() (litellm.Client, error) {
	snap := c.cache.Snapshot()
	if !snap.Ready || snap.Client == nil {
		reason := snap.Reason
		if reason == "" {
			reason = "Connecting"
		}
		return nil, fmt.Errorf("%w: %s", ErrNotReady, reason)
	}
	return snap.Client, nil
}

func (c *Client) DeleteAccessGroup(ctx context.Context, name string) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.DeleteAccessGroup(ctx, name)
}

func (c *Client) DeleteTag(ctx context.Context, name string) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.DeleteTag(ctx, name)
}

func (c *Client) ListModels(ctx context.Context) ([]litellm.ModelInfoResponse, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.ListModels(ctx)
}

func (c *Client) ListMCPServers(ctx context.Context) ([]litellm.MCPServerEntry, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.ListMCPServers(ctx)
}

func (c *Client) ListA2AAgents(ctx context.Context) ([]litellm.AgentEntry, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.ListA2AAgents(ctx)
}

func (c *Client) ListUserKeys(ctx context.Context, userID string) ([]litellm.UserKeyInfo, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.ListUserKeys(ctx, userID)
}

func (c *Client) RevokeKey(ctx context.Context, keyID string) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.RevokeKey(ctx, keyID)
}

// Phase 3 Plan 03-01 delegations. The connection.Client proxy must
// satisfy the widened litellm.Client interface; failing to forward any
// new method here would break the compile-time canary at the bottom of
// this file.

func (c *Client) UserNew(ctx context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.UserNew(ctx, req)
}

func (c *Client) UserInfoByEmail(ctx context.Context, email string) (*litellm.UserInfo, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.UserInfoByEmail(ctx, email)
}

func (c *Client) TeamMemberAdd(ctx context.Context, teamID, userID, role string) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.TeamMemberAdd(ctx, teamID, userID, role)
}

func (c *Client) KeyGenerate(ctx context.Context, req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.KeyGenerate(ctx, req)
}

func (c *Client) ListTeamsByAlias(ctx context.Context, alias string) ([]litellm.TeamListEntry, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.ListTeamsByAlias(ctx, alias)
}

func (c *Client) ListAllTeams(ctx context.Context) ([]litellm.TeamListEntry, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.ListAllTeams(ctx)
}

func (c *Client) EnsureDefaultTeam(ctx context.Context) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.EnsureDefaultTeam(ctx)
}

// CreateAccessGroup proxies to the underlying RESTClient. See
// internal/litellm/accessgroups.go for semantics.
func (c *Client) CreateAccessGroup(ctx context.Context, req litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.CreateAccessGroup(ctx, req)
}

func (c *Client) GetAccessGroupByName(ctx context.Context, name string) (*litellm.AccessGroupResponse, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.GetAccessGroupByName(ctx, name)
}

func (c *Client) UpdateAccessGroup(ctx context.Context, id string, req litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	client, err := c.current()
	if err != nil {
		return nil, err
	}
	return client.UpdateAccessGroup(ctx, id, req)
}

func (c *Client) DeleteAccessGroupByID(ctx context.Context, id string) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.DeleteAccessGroupByID(ctx, id)
}

var _ litellm.Client = (*Client)(nil)
