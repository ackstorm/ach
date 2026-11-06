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
// namespaced "ach:oauth:<kind>:<id>". Kinds: client (DCR registration),
// pending (an /authorize or device confirmation waiting on Dex), code
// (single use), refresh (rotated on use), rotating (a refresh a grant holds),
// device + device_user (RFC 8628), session_gen (see SessionGen). A Redis flush
// logs every OAuth client out — they re-run the browser flow. Accepted for v1.
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

// PutIfExists overwrites a record only while it still exists (SET XX); false
// when it is gone. A rotation that must not resurrect a record deleted
// meanwhile (EndSessions) uses it.
func (s *OAuthStore) PutIfExists(ctx context.Context, kind, id string, v any, ttl time.Duration) (bool, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return false, err
	}
	return s.RDB.SetXX(ctx, oauthKey(kind, id), b, ttl).Result()
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

// sessionGenKind holds a counter per user, keyed by the lower-cased email.
const sessionGenKind = "session_gen"

// SessionGen reads the user's session generation, 0 when absent, and slides
// its TTL. Every record stamped with a generation is written right after a
// read, so the counter outlives them all: if it expired under a live record it
// would restart at 0 and hand the same numbers out again.
func (s *OAuthStore) SessionGen(ctx context.Context, email string, ttl time.Duration) (int64, error) {
	gen, err := s.RDB.GetEx(ctx, oauthKey(sessionGenKind, email), ttl).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return gen, err
}

// BumpSessionGen moves the user's session generation on.
func (s *OAuthStore) BumpSessionGen(ctx context.Context, email string, ttl time.Duration) error {
	key := oauthKey(sessionGenKind, email)
	_, err := s.RDB.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Incr(ctx, key)
		p.Expire(ctx, key, ttl)
		return nil
	})
	return err
}

// Del removes one record; absent is not an error.
func (s *OAuthStore) Del(ctx context.Context, kind, id string) error {
	return s.RDB.Del(ctx, oauthKey(kind, id)).Err()
}
