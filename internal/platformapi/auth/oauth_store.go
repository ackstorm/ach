// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// OAuthStore keeps the authorization server's transient state in Redis,
// namespaced "ach:oauth:<kind>:<id>" beside the device-code sessions.
// Kinds: client (DCR registration), pending (an /authorize waiting on Dex),
// code (single use), refresh (rotated on use). A Redis flush logs every
// OAuth client out — they re-run the browser flow. Accepted for v1.
type OAuthStore struct {
	RDB *redis.Client
}

func oauthKey(kind, id string) string { return "ach:oauth:" + kind + ":" + id }

func (s *OAuthStore) Put(ctx context.Context, kind, id string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.RDB.Set(ctx, oauthKey(kind, id), b, ttl).Err()
}

// Get decodes into out; false when absent.
func (s *OAuthStore) Get(ctx context.Context, kind, id string, out any) (bool, error) {
	b, err := s.RDB.Get(ctx, oauthKey(kind, id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, out)
}

// Take is Get + delete, atomically (GETDEL): codes and refresh tokens are
// consumed exactly once whatever the outcome of what follows.
func (s *OAuthStore) Take(ctx context.Context, kind, id string, out any) (bool, error) {
	b, err := s.RDB.GetDel(ctx, oauthKey(kind, id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, out)
}
