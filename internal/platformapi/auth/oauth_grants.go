// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/redis/go-redis/v9"
)

// GrantReader answers "does email hold a broker grant for store?". The
// production reader is the MCP pods' own cleartext projection
// (mcp-oauth: `oauth:{store}:state:{email}` = {"granted": bool, …}) — one
// GET, no decryption, never written by ACH. email is the lowercased sub;
// the brokers key by the same value (login_hint.sub).
type GrantReader interface {
	Granted(ctx context.Context, email, store string) (bool, error)
}

type redisGrants struct{ rdb *redis.Client }

// NewRedisGrants reads the projection from rdb (ACH_OAUTH_GRANTS_REDIS_URL).
func NewRedisGrants(rdb *redis.Client) GrantReader { return redisGrants{rdb: rdb} }

func (g redisGrants) Granted(ctx context.Context, email, store string) (bool, error) {
	raw, err := g.rdb.Get(ctx, "oauth:"+store+":state:"+email).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var st struct {
		Granted bool `json:"granted"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return false, nil // a malformed projection is "no grant", not an outage
	}
	return st.Granted, nil
}

// MapGrants is the test reader: key = store + "|" + email.
type MapGrants map[string]bool

func (m MapGrants) Granted(_ context.Context, email, store string) (bool, error) {
	return m[store+"|"+email], nil
}
