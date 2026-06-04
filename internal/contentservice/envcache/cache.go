// SPDX-License-Identifier: Apache-2.0

package envcache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/ackstorm/ach/internal/sfdetach"
)

// envCacheKeyPrefix is the Redis key namespace for cached Environment
// projection rows consumed by the Content Service pipeline. Format:
// "ach:env:" + namespace + "/" + name.
//
// Per D-07 this is a parallel keyspace to the existing
// `ach:key:<credential_hash>` (KeyResolver, Phase 3 D-08) and
// `ach:teams:<owner_email>` (TeamsResolver, Phase 4 D-17) namespaces — the
// prefixes are disjoint and the value shapes (KeyInfo / []string / EnvRow
// JSON) are independently consumed by different code paths, so the cache
// keys cannot collide.
//
// Cache-key SHAPE: the (namespace, name) pair is non-sensitive metadata
// (also visible via `kubectl get environment -A`) so no peppering — see
// threat T-05-03-02 in the 05-03 plan threat model. The prefix is
// unexported because callers MUST type their dependency as the Cache
// interface and never reach for the key shape directly.
const envCacheKeyPrefix = "ach:env:"

// cacheTTL is the hard 60-second ceiling on every cache entry per D-07
// (matches §5.1 cache budget). The TTL is the eventual-consistency
// window: an Operator UPSERT on the `environments` projection row
// (Phase 5 D-14) becomes visible to the Content Service pipeline at
// most 60s later via expiry-driven refresh. We declare this constant
// independently of `internal/keystore.defaultTTL` (which shares the same
// value) to keep the envcache package decoupled from the keystore
// package — both are §5.1 cache-budget consumers but their lifetimes
// could diverge in a future spec revision.
const cacheTTL = 60 * time.Second

// sfLeaderTimeout bounds the detached singleflight loader so one caller's
// cancellation cannot cascade to live followers (C1) yet the flight can
// never hang. See internal/sfdetach for the mechanism.
const sfLeaderTimeout = 10 * time.Second

// EnvRow is the cache payload — the subset of `internal/db.EnvironmentRow`
// (Plan 05-02) that the Content Service pipeline (Plan 05-05) reads on
// every request. Keeping the cache-payload type LOCAL to this package
// isolates the cache wire-format from full schema changes downstream:
// adding a new column to `db.EnvironmentRow` does NOT invalidate Redis
// entries cached under earlier code (the unmarshal would simply ignore
// the new column on read).
//
// Field semantics:
//
//   - AuthorizedTeams: source for the pk_ Team intersection check
//     (CS-03 / D-04 step 4).
//   - ContextPrompts, ContextPlugins, ContextArtifacts, ContextSkills:
//     source for the content allowlist check (CS-04 / D-04 step 5 —
//     cheaper-first ordering).
//
// Only the fields the pipeline reads are cached — the projection PK
// (namespace/name), deletion timestamp, and resource_version are NOT
// stored: staleness gates on the contentRow, not the EnvRow, so caching
// them only bloats the Redis payload.
//
// JSON tags are explicit (not relying on Go field-name defaulting) so
// the wire format is stable across struct-field renames. Dropping fields
// is forward-safe: old cached entries carrying the extra keys still
// deserialize (encoding/json ignores unknown keys; no
// DisallowUnknownFields on the read path), so no cache flush is needed.
type EnvRow struct {
	AuthorizedTeams  []string `json:"authorized_teams"`
	ContextPrompts   []string `json:"context_prompts"`
	ContextPlugins   []string `json:"context_plugins"`
	ContextArtifacts []string `json:"context_artifacts"`
	ContextSkills    []string `json:"context_skills"`
}

// Loader is the function signature for the cache miss path. The
// production wiring (Plan 05-05) injects a closure over
// `db.GetEnvironmentByName` (Plan 05-02). Tests inject a fake.
//
// Semantics (mirror `keystore.TeamsResolver.Resolve` semantics for the
// (nil, nil) "row absent" case):
//
//   - (row, nil) where row != nil: hit. Cache will be hydrated with the
//     JSON-serialized row at 60s TTL.
//   - (nil, nil): the (namespace, name) pair does NOT exist in the
//     projection. Callers (Plan 05-05 / D-04 step 3) map this to
//     `404 environment_not_found`. The cache is NOT populated — a
//     subsequent reconcile may create the row and we want it visible
//     immediately rather than after the negative-TTL.
//   - (nil, err): underlying lookup failed (DB down, context canceled,
//     pgx error). Returned verbatim; cache is NOT populated.
type Loader func(ctx context.Context, ns, name string) (*EnvRow, error)

// Cache is the public interface consumed by the Content Service
// pipeline (Plan 05-05). The pipeline types its dependency as
// `envcache.Cache` and never as the concrete `redisCachedEnvCache` —
// this lets tests substitute an in-memory fake without touching
// production code paths.
//
// Get semantics:
//
//   - (row, nil) row != nil: success (cache hit OR cache miss with
//     loader-hydrated row).
//   - (nil, nil): the (namespace, name) pair does NOT exist. Caller
//     maps to `404 environment_not_found`.
//   - (nil, err): Loader-returned error. Caller maps to `500
//     internal_error` (or `503 stale_cache_expired` if it can identify
//     the DB-down signature). Cache is NOT polluted by errors.
//
// MALFORMED CACHE RULE (D-07): if Redis contains bytes at the expected
// key that cannot be unmarshaled as EnvRow, Get falls through to the
// loader. We do NOT call rdb.Del — the next successful Set overwrites
// the bad entry, and the 60s TTL bounds the worst case. This matches
// `keystore.redisCachedTeamsResolver.Resolve` line-for-line per D-07.
type Cache interface {
	Get(ctx context.Context, ns, name string) (*EnvRow, error)
}

// redisCachedEnvCache is the production Cache implementation. The
// shape MIRRORS `keystore.redisCachedTeamsResolver` verbatim per D-07:
// Redis read-through + singleflight on miss + 60s TTL + best-effort
// writes + fall-through (no DEL) on malformed entries.
//
// Concurrency: `singleflight.Group` is safe for concurrent use; the
// `*redis.Client` is also concurrency-safe by go-redis contract.
type redisCachedEnvCache struct {
	loader Loader
	rdb    *redis.Client
	ttl    time.Duration
	sf     singleflight.Group
}

// Compile-time interface assertion: if a future edit to the Cache
// interface adds or changes a method, the build breaks here until the
// implementation catches up. Mirrors the Phase 3 / Phase 4 canary at
// the bottom of `keystore/keystore.go` and `keystore/teamsresolver.go`.
var _ Cache = (*redisCachedEnvCache)(nil)

// NewCachedEnvCache constructs the production Redis-backed Cache. Refuses
// nil loader or nil redis client — same constructor-time validation idiom
// as `NewCachedTeamsResolver` (Phase 4 D-17) and `NewCachedResolver`
// (Phase 3 D-08), so the wiring layer in
// `cmd/ach/cmd/content_service.go` fails at startup rather than at
// first request.
func NewCachedEnvCache(loader Loader, rdb *redis.Client) (Cache, error) {
	if loader == nil {
		return nil, errors.New("envcache: nil loader")
	}
	if rdb == nil {
		return nil, errors.New("envcache: nil redis client")
	}
	return &redisCachedEnvCache{
		loader: loader,
		rdb:    rdb,
		ttl:    cacheTTL,
	}, nil
}

// Get implements the cache → single-flighted-loader lookup flow per
// D-07. Seven steps:
//
//  1. Compose the cache key as `ach:env:<ns>/<name>`.
//  2. Redis GET; on success, json.Unmarshal into a local EnvRow value.
//     Unmarshal success → return &v. Unmarshal failure → fall through
//     (do NOT DEL the bad key; the next Set overwrites and the 60s TTL
//     bounds the worst case).
//  3. On Redis error (including key-missing redis.Nil): fall through to
//     the loader. Redis errors are best-effort — loud-logging them
//     would amplify cache-down noise.
//  4. Singleflight the loader call: concurrent goroutines on the same
//     key join via singleflight so a thundering herd of N requests collapses
//     to exactly one DB hit (T-05-03-04 mitigation; verified by
//     TestGet_Singleflight_DedupesConcurrentMisses).
//  5. Loader error → return (nil, err). Cache is NOT populated.
//  6. Loader (nil, nil) → return (nil, nil). The row does NOT exist;
//     we do NOT cache the negative answer.
//  7. Loader hit → best-effort `rdb.Set(...)` with cacheTTL; return
//     (row, nil). Cache-write failure is ignored (best-effort).
//
// Empty slices in EnvRow fields ARE cached as `[]` — they encode the
// "Environment exists but its allowlist is empty" state, which is a
// valid (and security-relevant) value distinct from "row missing".
func (r *redisCachedEnvCache) Get(ctx context.Context, ns, name string) (*EnvRow, error) {
	key := cacheKey(ns, name)

	// Cache hit fast path.
	if raw, err := r.rdb.Get(ctx, key).Bytes(); err == nil {
		var v EnvRow
		if jsonErr := json.Unmarshal(raw, &v); jsonErr == nil {
			return &v, nil
		}
		// Malformed cache entry — fall through. Do NOT rdb.Del; the
		// next successful Set overwrites and the 60s TTL bounds the
		// worst case (D-07).
	}

	// Detached-but-bounded leader so one caller's cancellation cannot
	// cascade to live followers (C1).
	row, err := sfdetach.Do(ctx, &r.sf, key, sfLeaderTimeout,
		func(c context.Context) (*EnvRow, error) {
			return r.loader(c, ns, name)
		})
	if err != nil {
		return nil, err
	}
	if row == nil {
		// Loader said "no such row". Do NOT cache the negative answer.
		return nil, nil //nolint:nilnil // (nil,nil) is the "absent" sentinel — see Loader doc.
	}

	// Populate cache (best-effort; ignore errors).
	if b, marshalErr := json.Marshal(row); marshalErr == nil {
		_ = r.rdb.Set(ctx, key, b, r.ttl).Err()
	}
	return row, nil
}

// cacheKey composes the Redis key for the (ns, name) pair. The shape
// is `ach:env:<ns>/<name>` per D-07. We use string concatenation
// rather than fmt.Sprintf for the hot path — Get runs once per
// Content Service request.
func cacheKey(ns, name string) string {
	return envCacheKeyPrefix + ns + "/" + name
}
