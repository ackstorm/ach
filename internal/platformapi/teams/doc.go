// SPDX-License-Identifier: Apache-2.0

// Package teams ships the canonical team-membership lookup helper
// consumed by the Phase 3 envkeys (Plan 03-08) and hydrate (Plan 03-09)
// handlers per WARN-06.
//
// The Phase 3 implementation calls LiteLLM directly on every invocation
// — no caching. The Phase 4 Forwarder will replace the body with a
// Redis-cached variant sharing the keystore 60s TTL (Hub §5.1 / FWD-02
// — "the cached Team-membership lookup, ≤60s TTL — same cache the
// Forwarder uses in Phase 4").
//
// LookupCallerTeams is intentionally a free function (not a struct
// method) so consumers import shape stays
// `teams.LookupCallerTeams(ctx, ll, email)`. Phase 4 may refactor to a
// struct + cache; callers will pick up the signature change at build
// time via the existing import alias.
//
// # Semantics
//
//   - LiteLLM returns ErrNotFound (HTTP 404): caller has no LiteLLM
//     user yet → return ([], nil). Downstream team-intersection
//     treats this as zero intersection → 403 unauthorized_team for
//     protected paths.
//   - LiteLLM returns transport / 5xx error: return (nil, err). The
//     handler emits 503 litellm_unreachable.
//   - LiteLLM returns 200 with `teams` array: return ([...team names],
//     nil). A nil or empty `teams` field is normalized to a zero-length
//     slice so downstream `hasIntersect` always sees a real slice.
package teams
