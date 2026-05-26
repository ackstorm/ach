// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/ackstorm/ach/internal/litellm"
)

// teamsCacheKeyPrefix is the Redis key namespace for cached LiteLLM
// team-membership resolutions. Format: "ach:teams:" + ownerEmail.
//
// Per D-17 this is a parallel keyspace to the existing
// `ach:key:<credential_hash>` namespace used by KeyResolver — the cache
// keys cannot collide because the prefixes are disjoint and the value
// shapes (KeyInfo JSON vs []string JSON) are independently consumed.
//
// Cache-key SHAPE difference from KeyResolver: the bearer plaintext is
// secret material so the KeyResolver hashes it; the SSO ownerEmail is
// PII but not credential material, so we use it verbatim. NO peppering.
const teamsCacheKeyPrefix = "ach:teams:"

// TeamsResolver returns the LiteLLM team IDs for an SSO user.
//
// Two consumers (Phase 4 and Phase 5) type their dependency as
// TeamsResolver and never as a concrete type:
//
//   - Forwarder pk_ pre-check (FWD-03 / D-16) intersects the returned
//     team list with the union of `Environment.spec.authorizedTeams[]`
//     across Environments hosting the requested resource. Empty
//     intersection → 403 unauthorized_team.
//   - Phase 5 Content Service (CS-04) reuses this resolver verbatim for
//     the same `pk_` Team-intersection check at the `/content/`
//     surface.
//
// Resolve semantics:
//
//   - (teams, nil), len(teams) ≥ 0: caller proceeds with the list.
//     An empty slice IS a valid LiteLLM answer ("user has no teams") —
//     it is cached, NOT a sentinel for unknown.
//   - (nil, err) or (nil, non-nil-err): LiteLLM unreachable / transport
//     failure. Forwarder maps to 503 litellm_unreachable per FWD-03 /
//     SC#2 (fail-closed); cache is NOT populated.
type TeamsResolver interface {
	Resolve(ctx context.Context, ownerEmail string) ([]string, error)
}

// liteLLMTeamsResolver is the base resolver wired to a litellm.Client.
//
// It is the inner Resolver wrapped by NewCachedTeamsResolver in
// production wiring. Plan 04-08 (cmd wiring) instantiates the chain
// via NewCachedTeamsResolver(NewLiteLLMTeamsResolver(litellmClient), rdb).
type liteLLMTeamsResolver struct {
	ll litellm.Client
}

// NewLiteLLMTeamsResolver constructs the production base resolver.
// Returns an error on nil litellm.Client — mirrors the Phase 3
// NewDBResolver / NewCachedResolver constructor-time guard idiom so
// the wiring layer in cmd/ach/cmd/forwarder.go fails at startup
// rather than at first request.
func NewLiteLLMTeamsResolver(ll litellm.Client) (TeamsResolver, error) {
	if ll == nil {
		return nil, errors.New("keystore: nil litellm client")
	}
	return &liteLLMTeamsResolver{ll: ll}, nil
}

// Resolve calls litellm.Client.UserInfoByEmail and unwraps the Teams
// slice. Per D-17 / plan 04-03 contract:
//
//   - litellm.ErrNotFound (LiteLLM-side 404 on /user/info) → returns
//     ([]string{}, nil). The user has no team membership; this is NOT
//     an error — the Forwarder pk_ pre-check will reject with 403
//     unauthorized_team upstream.
//   - info == nil OR len(info.Teams) == 0 → ([]string{}, nil). Same
//     reasoning; the call succeeded but LiteLLM has no teams for this
//     user.
//   - Any other error → (nil, err). The cached wrapper propagates
//     without writing to Redis; the caller renders 503
//     litellm_unreachable.
func (r *liteLLMTeamsResolver) Resolve(ctx context.Context, email string) ([]string, error) {
	info, err := r.ll.UserInfoByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, litellm.ErrNotFound) {
			return []string{}, nil
		}
		return nil, err
	}
	if info == nil || len(info.Teams) == 0 {
		return []string{}, nil
	}
	return info.Teams, nil
}

// redisCachedTeamsResolver wraps an inner TeamsResolver (typically the
// liteLLMTeamsResolver) with a Redis read-through cache plus a
// singleflight.Group that deduplicates concurrent miss-storms on the
// same ownerEmail.
//
// Per D-17 the cache is the only shortcut on the pk_ pre-check path:
// on hit the entire LiteLLM /user/info roundtrip is skipped. The miss
// path is single-flighted so even a thundering herd of N goroutines on
// the same email collapses to exactly one upstream call (T-04-03-04
// LiteLLM-unreachable storm mitigation; verified by
// TestRedisCachedTeamsResolver_SingleFlight).
//
// Empty-slice results ARE cached: `[]string{}` is a valid LiteLLM
// answer ("user has no teams") and indistinguishable on the wire from
// a JSON `[]`. Caching it bounds the LiteLLM round-trip cost for
// no-team SSO users to once per 60s. (T-04-03-05 empty-team-list
// confusion mitigation.)
type redisCachedTeamsResolver struct {
	base TeamsResolver
	rdb  *redis.Client
	sf   singleflight.Group
	ttl  time.Duration
}

// NewCachedTeamsResolver constructs the production redisCachedTeamsResolver.
// Refuses nil base or nil redis client — same constructor-time validation
// idiom as NewCachedResolver (Phase 3 D-08).
func NewCachedTeamsResolver(base TeamsResolver, rdb *redis.Client) (TeamsResolver, error) {
	if base == nil {
		return nil, errors.New("keystore: nil base teams resolver")
	}
	if rdb == nil {
		return nil, errors.New("keystore: nil redis client")
	}
	return &redisCachedTeamsResolver{
		base: base,
		rdb:  rdb,
		ttl:  defaultTTL, // 60s — same Hub §5.1 ceiling as KeyResolver
	}, nil
}

// Resolve implements the cache → single-flighted-base lookup flow per
// D-17:
//
//  1. GET from Redis under "ach:teams:<email>"; on success deserialize
//     the JSON []string and return.
//  2. On miss, single-flight the base.Resolve call (concurrent callers
//     on the same email join the in-flight result).
//  3. On base success populate Redis with the JSON-encoded result and
//     the 60s TTL ceiling (best-effort — log nothing; the next call
//     will simply miss again).
//  4. On base error propagate verbatim; cache is NOT written (a
//     LiteLLM-unreachable failure would otherwise mask itself for 60s).
//
// Empty-slice results follow the same path as populated slices — they
// ARE cached. The cache wire format for an empty result is `[]`, never
// `null`; a nil slice from the base is normalized to `[]string{}` before
// marshaling to ensure that invariant.
func (r *redisCachedTeamsResolver) Resolve(ctx context.Context, email string) ([]string, error) {
	key := teamsCacheKeyPrefix + email

	// Cache hit fast path.
	if raw, err := r.rdb.Get(ctx, key).Bytes(); err == nil {
		var teams []string
		if jsonErr := json.Unmarshal(raw, &teams); jsonErr == nil {
			if teams == nil {
				teams = []string{}
			}
			return teams, nil
		}
		// Malformed cache entry — fall through to base. Do NOT DEL;
		// the next SET overwrites and the 60s TTL caps the worst case.
	}

	// Single-flight base lookup. The leader holds the call; concurrent
	// callers on the same email join via singleflight.Do.
	v, sfErr, _ := r.sf.Do(email, func() (any, error) {
		return r.base.Resolve(ctx, email)
	})
	if sfErr != nil {
		// Propagate base error WITHOUT caching — see Resolve doc.
		return nil, sfErr
	}
	teams, _ := v.([]string)
	if teams == nil {
		teams = []string{}
	}

	// Populate cache (best-effort; ignore errors).
	if b, marshalErr := json.Marshal(teams); marshalErr == nil {
		_ = r.rdb.Set(ctx, key, b, r.ttl).Err()
	}
	return teams, nil
}

// Compile-time interface assertions: both implementations satisfy
// TeamsResolver. If a future edit to the TeamsResolver interface adds
// or changes a method, the build breaks here until both types catch up.
//
// Mirrors the Phase 3 `var _ Resolver = (*xxxResolver)(nil)` canary at
// the bottom of keystore.go / dbresolver.go.
var (
	_ TeamsResolver = (*liteLLMTeamsResolver)(nil)
	_ TeamsResolver = (*redisCachedTeamsResolver)(nil)
)
