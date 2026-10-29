// SPDX-License-Identifier: Apache-2.0

// Package envcache is the Phase 5 D-07 Redis-backed read-through cache
// for the `environments` projection row (Plan 05-02 / D-13). The
// Content Service pipeline (Plan 05-05) consumes this on every request
// to look up `authorized_teams[]` + `context_<prompts|plugins|artifacts>[]`
// without hitting Postgres per call.
//
// # Why a dedicated cache package
//
// Spec v4 §5.2 (line 13) reverses the v3 informer model: Platform API,
// Forwarder, and Content Service no longer hold informers over ACH
// CRDs; they read CRD spec/status from Postgres. The Operator is the
// sole K8s watcher and writes a Postgres "projection" row in the same
// transaction as the K8s state change. The Content Service then needs
// a per-request lookup against `environments` — and a 60s in-memory
// cache is the cheapest way to keep the inner-loop cost off the
// Postgres connection pool. D-07 isolates that cache in its own
// package so the pipeline (Plan 05-05) just calls
// `envCache.Get(ctx, ns, name)` and stays decoupled from the redis
// client + singleflight wiring.
//
// # Cache shape
//
//   - Key: `ach:env:<namespace>/<name>` (e.g. `ach:env:ach-system/prod`).
//     Parallel keyspace to `ach:key:<credential_hash>` (KeyResolver,
//     Phase 3 D-08) and `ach:teams:<owner_email>` (TeamsResolver,
//     Phase 4 D-17); prefixes are disjoint.
//   - Value: JSON-serialized EnvRow (see cache.go). EnvRow is a
//     deliberate SUBSET of the full `db.EnvironmentRow` — only the
//     fields the Content Service pipeline reads. Keeping the
//     cache-payload type local to this package isolates the wire
//     format from full schema changes downstream.
//   - TTL: 60 seconds (matches §5.1 cache budget). This is the
//     eventual-consistency window: an Operator UPSERT becomes visible
//     to Content Service at most 60s later via expiry-driven refresh.
//     There is NO explicit invalidation on Operator write — the spec
//     accepts the 60s convergence window per CS-03 cache budget.
//
// # Concurrency
//
// Cache misses are deduplicated via `golang.org/x/sync/singleflight`:
// concurrent goroutines requesting the same (namespace, name) collapse
// to exactly one Loader call (T-05-03-04 thundering-herd mitigation).
// This mirrors the keystore.redisCachedTeamsResolver pattern verbatim
// per D-07.
//
// # Best-effort writes, malformed-cache fall-through
//
// Redis writes are best-effort: an `rdb.Set` failure is silently
// ignored (the next request will simply miss again and try to
// re-hydrate). Malformed cache entries (json.Unmarshal failure) fall
// through to the Loader, and the resulting hit overwrites the bad
// entry via the next Set — we do NOT call rdb.Del. This matches the
// keystore.redisCachedTeamsResolver behavior exactly and avoids the
// extra Redis round-trip a defensive DEL would cost on every read.
//
// # See also
//
//   - Plan 05-03 PLAN.md — D-07 decision text + threat register.
//   - internal/keystore/teamsresolver.go — the line-for-line analog
//     this package mirrors.
//   - internal/keystore/keystore.go — defaultTTL constant pattern.
package envcache
