// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/sfdetach"
)

// defaultTTL is the hard 60-second ceiling on every cache entry per Hub
// §5.1 / FWD-02 / KEY-04. NOT a knob — anything longer breaks the
// revocation-propagation guarantee, anything shorter pointlessly raises
// DB pressure.
const defaultTTL = 60 * time.Second

// sfLeaderTimeout bounds the detached singleflight leader DB lookup so a
// per-request cancellation cannot kill the shared flight (finding C1) yet
// the flight can never hang.
const sfLeaderTimeout = 10 * time.Second

// cacheKeyPrefix is the Redis key namespace for cached resolutions.
// Format: "ach:key:" + hex(HMAC-SHA-256(pepper, plaintext)). The bearer
// plaintext NEVER appears in the key (T-03-05-03).
const cacheKeyPrefix = "ach:key:"

// ErrEmptyPepper is returned by NewCachedResolver when the supplied
// pepper is nil or zero-length. Mirrors credhash.ErrEmptyPepper — refuse
// to construct rather than fail at first Resolve.
var ErrEmptyPepper = errors.New("keystore: pepper is empty")

// KeyInfo is the resolver's normalized view of an authenticated bearer.
// Fields are populated from the underlying SQL row (db.PkKeyInfo /
// db.EkKeyInfo) and shared by every downstream consumer of the auth
// path (Phase 3 Platform API handlers, Phase 4 Forwarder, Phase 5
// Content Service).
//
// JSON tags are explicit because KeyInfo is serialized into the Redis
// cache and must round-trip across process restarts; the struct shape is
// part of the cache wire format.
type KeyInfo struct {
	KeyID         string            `json:"key_id"`
	KeyType       keys.BearerPrefix `json:"key_type"`
	OwnerEmail    string            `json:"owner_email"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	Environment   string            `json:"environment,omitempty"`
	LiteLLMUserID *string           `json:"litellm_user_id,omitempty"`
	LiteLLMToken  *string           `json:"litellm_token,omitempty"`
}

// Resolver is the single per-request authentication contract. The Authn
// middleware (Phase 3) and every component that needs to map a bearer
// plaintext to an authenticated identity (Phase 4 Forwarder, Phase 5
// Content Service) types its dependency as Resolver, never as a concrete
// type — production wires NewCachedResolver(NewDBResolver(...), ...).
//
// Resolve semantics:
//
//   - (info != nil, err == nil): bearer is active and authorized; the
//     caller proceeds with the populated KeyInfo.
//   - (info == nil, err == nil): bearer is revoked / expired / unknown
//     (KEY-04 / KEY-06 — three causes indistinguishable). The caller
//     renders 401 expired_or_revoked.
//   - (info == nil, err != nil): infrastructure error (DB / Redis /
//     hash). The caller renders 500 internal_error and emits an audit.
type Resolver interface {
	Resolve(ctx context.Context, plaintext string) (*KeyInfo, error)
}

// redisCachedResolver wraps an inner Resolver (typically the dbResolver)
// with a Redis-backed read-through cache plus a singleflight.Group that
// deduplicates concurrent miss-storms on the same plaintext.
//
// Per D-07 the cache is the only shortcut on the auth-hot-path: on hit
// the entire DB roundtrip + last_used_at debounce UPDATE are skipped.
// The miss path is single-flighted so even a thundering herd of N
// goroutines on the same freshly-rotated bearer results in exactly one
// DB call.
type redisCachedResolver struct {
	inner  Resolver
	redis  *redis.Client
	pepper []byte
	sf     singleflight.Group
	ttl    time.Duration
}

// NewCachedResolver constructs the production redisCachedResolver
// instance. Refuses an empty pepper (D-07: every cache key derives from
// the pepper, so a missing pepper would silently weaken the hash and
// reveal cache lookup paths).
func NewCachedResolver(inner Resolver, redisClient *redis.Client, pepper []byte) (Resolver, error) {
	if len(pepper) == 0 {
		return nil, ErrEmptyPepper
	}
	if inner == nil {
		return nil, errors.New("keystore: nil inner resolver")
	}
	if redisClient == nil {
		return nil, errors.New("keystore: nil redis client")
	}
	return &redisCachedResolver{
		inner:  inner,
		redis:  redisClient,
		pepper: append([]byte(nil), pepper...), // defensive copy
		ttl:    defaultTTL,
	}, nil
}

// Resolve implements the cache → single-flighted-DB lookup flow per D-07.
//
//  1. Hash the plaintext with the configured pepper → cache key.
//  2. GET from Redis; on success deserialize the JSON and return.
//  3. On miss, single-flight the inner.Resolve call (concurrent callers
//     on the same hash join the in-flight result).
//  4. If inner returned a populated *KeyInfo, SET it in Redis with the
//     60-second TTL ceiling (best-effort — log nothing; the next call
//     will simply miss again).
//  5. Return the KeyInfo (or nil on revoked/expired/unknown).
//
// A nil result from inner is NOT cached: caching a "no such key" would
// preserve revoked credentials in the cache window past the immediate
// DEL barrier (KEY-07 / KEY-08). The next call will miss the cache and
// re-check the DB.
func (r *redisCachedResolver) Resolve(ctx context.Context, plaintext string) (*KeyInfo, error) {
	hash, err := credhash.Hash(r.pepper, []byte(plaintext))
	if err != nil {
		return nil, err
	}
	cacheKey := cacheKeyPrefix + hash

	// Cache hit fast path.
	if raw, getErr := r.redis.Get(ctx, cacheKey).Bytes(); getErr == nil {
		var info KeyInfo
		if jsonErr := json.Unmarshal(raw, &info); jsonErr == nil {
			return &info, nil
		}
		// Malformed cache entry — fall through to inner.Resolve as if
		// the cache had missed. We do NOT DEL the bad entry; the next
		// SET on miss overwrites it (and the 60s TTL caps the worst case).
	}

	// Single-flight DB lookup on a detached-but-bounded leader context so
	// one caller's cancellation cannot cascade to live followers (C1).
	info, err := sfdetach.Do(ctx, &r.sf, hash, sfLeaderTimeout,
		func(c context.Context) (*KeyInfo, error) {
			return r.inner.Resolve(c, plaintext)
		})
	if err != nil {
		return nil, err
	}
	if info == nil {
		// Revoked / expired / unknown — propagate (nil, nil) without
		// caching. Caching nil would let a revoked credential survive
		// the cache window past the explicit DEL barrier.
		return nil, nil
	}

	// Populate cache (best-effort; ignore errors).
	if b, marshalErr := json.Marshal(info); marshalErr == nil {
		_ = r.redis.Set(ctx, cacheKey, b, r.ttl).Err()
	}
	return info, nil
}
