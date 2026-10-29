// SPDX-License-Identifier: Apache-2.0

// Package keystore owns the per-request authentication resolution path:
// bearer plaintext -> credential_hash -> Redis cache -> Postgres on miss.
//
// Phase 3 + Phase 4 (Forwarder) + Phase 5 (Content Service) all consume
// keystore.Resolver — every cache + dispatch decision lives in one place
// (D-08).
//
// # Cache key shape (D-07)
//
// Hub §5.1 / FWD-02 / KEY-04 mandate that the cache key derive from the
// credential_hash, NEVER from the bearer plaintext:
//
//	"ach:key:" + hex(HMAC-SHA-256(pepper, plaintext))
//
// The bearer plaintext NEVER appears in the cache key or the cache value.
// The cached value is a JSON-encoded KeyInfo (key_id, key_type,
// owner_email, status, expires_at?, environment?, litellm_token?).
//
// # TTL ceiling (D-07)
//
// 60 seconds. NON-CONFIGURABLE. Anything longer breaks the
// revocation-propagation guarantees in Hub §5.1 and FWD-02 — revoke flows
// rely on the cache window being bounded so a stale entry self-expires
// even if the explicit DEL (KEY-07 / KEY-08) is dropped by Redis.
//
// # Single-flight dedup (D-07)
//
// Concurrent miss-storms on the same plaintext (e.g. ten Forwarder
// replicas all racing on a freshly-rotated pk_) collapse to exactly one
// DB roundtrip via golang.org/x/sync/singleflight. The leader populates
// Redis; the other goroutines wake up on the same (any, error) tuple.
//
// # Dispatch (D-08)
//
// dbResolver branches on keys.ClassifyBearer:
//
//   - PrefixPk → db.PkCheckAndExtend (Hub §7.1 atomic sliding-window)
//   - PrefixEk → db.EkResolve (Hub §8.1 debounced last_used_at UPDATE)
//
// Either helper returning (nil, nil) means revoked / expired / unknown —
// keystore propagates (nil, nil) so the caller (typically the Authn
// middleware) renders 401 expired_or_revoked per KEY-04 / KEY-06. The
// three causes are indistinguishable by design — no information leak.
package keystore
