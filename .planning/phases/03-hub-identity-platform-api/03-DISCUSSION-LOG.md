# Phase 3: Hub Identity & Platform API - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-05-19
**Phase:** 3-hub-identity-platform-api
**Mode:** `/gsd:discuss-phase 3 --auto` — single-pass autonomous resolution. No `AskUserQuestion` calls; Claude selected the recommended option for every question and logged each selection inline.
**Areas discussed:** HTTP server + middleware stack, Dex SSO flow, key resolution + Redis cache, §7.1 pk_ check-and-extend CTE, §8.2 ek_ create transaction shape, asymmetric revocation, hydrate endpoint shape, audit event surface, informer-backed Environment read model, admin allowlist + ConfigMap mount, code structure, LiteLLM client extensions, force-refresh annotation patch.

---

## HTTP server + middleware stack

| Option | Description | Selected |
|--------|-------------|----------|
| `go-chi/chi/v5` | Lightweight router with stdlib-compatible `http.Handler`/`Middleware` signatures; widest familiarity in K8s-controller ecosystem | ✓ |
| `labstack/echo` | Higher-level framework with bespoke `Context` type and per-route middleware; pulls in more transitive deps | |
| `net/http` + `http.ServeMux` only | Smaller surface but Go 1.22's pattern-matched ServeMux still lacks middleware composition + sub-router idioms; would need to hand-roll | |

**Auto-selected:** `go-chi/chi/v5` (recommended default).
**Rationale (auto):** Phase 1's existing `http.ServeMux` stub in `cmd/platform-api/main.go:42` migrates cleanly to `chi.Mux`; zero magic; stdlib-compatible middleware idiom enables direct reuse in Phase 4 (Forwarder) without re-importing a framework.

---

## Dex SSO flow shape

| Option | Description | Selected |
|--------|-------------|----------|
| Stateless OIDC + PKCE; mint pk_ inside callback handler; no server-side session | Each `POST /platform/auth/sso/callback` returns the plaintext exactly once and is over | ✓ |
| Server-side session (Redis-backed) | Callback establishes a session; separate `POST /platform/auth/mint-pk` to issue pk_ | |
| Browser-redirected hosted login page (HTML UI) | Out-of-process Dex flow rendered by ACH | |

**Auto-selected:** Stateless OIDC + PKCE; mint pk_ inside callback; no session.
**Rationale (auto):** Matches CLI Phase 6 `ach login` pattern (open browser → loopback callback → POST code/state to ACH); no second cache surface; the "plaintext exactly once" invariant is naturally enforced by the response itself.

---

## Key resolution + Redis cache

| Option | Description | Selected |
|--------|-------------|----------|
| Cache key = full hex credential_hash, value = JSON KeyInfo, TTL = 60s, single-flight on miss | Mirrors Hub §5.1 / FWD-02 / KEY-04 contract | ✓ |
| Cache key = key_id (pkid_/ekid_), value = credential_hash + KeyInfo | Requires plaintext→hash compute on every hit (no savings) | |
| No Redis; Postgres-only on every request | Misses the 60s TTL ceiling and the cross-component performance contract | |

**Auto-selected:** hex credential_hash key, JSON value, 60s TTL, single-flight.
**Rationale (auto):** Spec mandates a ≤60s Redis TTL ceiling; the keystore package is shared with Phase 4/5 so the cache shape is load-bearing for all three components.

---

## §7.1 pk_ check-and-extend SQL

| Option | Description | Selected |
|--------|-------------|----------|
| Single UPDATE statement with CASE WHEN debounce + WHERE-snapshot + RETURNING | Atomic; no second roundtrip; honors §7.1 wording verbatim | ✓ |
| WITH CTE explicit (`WITH check AS (...) UPDATE ... WHERE key_id IN (...)`) | Equivalent semantically; more verbose; some pgxpool versions had RETURNING-from-CTE oddities | |
| Two-statement: SELECT-then-UPDATE under transaction | Race-prone (statement snapshot is per-statement) and slower; non-conforming to §7.1's "single atomic CTE" wording | |

**Auto-selected:** Single UPDATE with CASE WHEN debounce.
**Rationale (auto):** Spec §7.1 explicitly demands a single atomic operation; the form chosen avoids an explicit CTE while preserving snapshot semantics under `READ COMMITTED` (the default).

---

## §8.2 ek_ create transaction shape

| Option | Description | Selected |
|--------|-------------|----------|
| LiteLLM KeyGenerate OUTSIDE tx; INSERT INSIDE tx; on INSERT failure call LiteLLM.RevokeKey to clean up | Aligns with spec ordering; saga-style compensation; deterministic | ✓ |
| INSERT INSIDE tx with deferred constraints; LiteLLM call AFTER COMMIT | Inverts the spec ordering; leaves LiteLLM with a key ACH can't reach if INSERT-rollback fires after the LiteLLM call | |
| LiteLLM + DB writes via 2PC | Postgres 2PC against a non-Postgres resource is unsound; LiteLLM doesn't speak XA | |

**Auto-selected:** LiteLLM OUTSIDE tx; INSERT INSIDE tx; compensation via RevokeKey on insert failure.
**Rationale (auto):** Saga compensation matches §8.2 step ordering; preserves the §7.1/§8.5 asymmetric-revocation analogy (ek_ runtime barrier is LiteLLM).

---

## Asymmetric revocation

| Option | Description | Selected |
|--------|-------------|----------|
| Two distinct handlers, pk_ DB-first / ek_ LiteLLM-first, no shared branching helper | Spec KEY-07/08 mandate the asymmetry; separate code paths keep the ordering reviewable | ✓ |
| Single revoke() with branch on key_type prefix | Branching obscures the ordering; reviewer must mentally trace the asymmetry | |
| Identical order for both (LiteLLM-first or DB-first uniformly) | Violates KEY-07 (pk_) or KEY-08 (ek_); not viable | |

**Auto-selected:** Two distinct handlers, separate ordering.
**Rationale (auto):** Spec is explicit on the asymmetry; the two functions are short and reviewable; the shared bits (Redis invalidate, audit emission) factor into helpers.

---

## Hydrate endpoint shape

| Option | Description | Selected |
|--------|-------------|----------|
| Strict JSON with `DisallowUnknownFields`; runtime+context always present; `[]` when empty; never plaintext | Hub §15.1 / API-04 verbatim | ✓ |
| Loose JSON accepting unknown fields | Increases ambiguity; rejected by API-04 strictness | |
| Conditional response (omit empty blocks) | Forbidden by API-04 "both blocks always present" | |

**Auto-selected:** Strict; `runtime`/`context` always present; `DisallowUnknownFields`.

---

## Audit event surface

| Option | Description | Selected |
|--------|-------------|----------|
| `internal/audit/events.go` with action+outcome constants; `EmitAudit(ctx, Event)` helper; reuse Phase 2 handler | Single source of truth; thin wrapper; no double-handler infra | ✓ |
| Per-handler inline `audit.With(...).Info(...)` calls without shared constants | String drift; review cost on every handler edit | |
| Separate audit package with its own slog handler | Re-implements Phase 2 D-17; doubles the sink config surface | |

**Auto-selected:** Constants + EmitAudit helper; reuse Phase 2 handler.

---

## Informer-backed Environment read model

| Option | Description | Selected |
|--------|-------------|----------|
| `manager.Manager` without controllers, without leader election, with cache only | Sub-ms reads; cache-sync gates readiness (MULTI-03 carry-forward) | ✓ |
| Direct K8s API calls per request | Latency + API-server load; misses the §5.2 "informer-backed views in Phase 3+" contract | |
| External cache layer (e.g. Bloomrpc) | Reinvents controller-runtime's existing primitive | |

**Auto-selected:** controller-runtime manager.Manager cache-only.

---

## Admin allowlist + ConfigMap mount

| Option | Description | Selected |
|--------|-------------|----------|
| Read once at process start; absent file → empty allowlist + WARN; restart to update | Hub AC18 / AC24 verbatim | ✓ |
| K8s ConfigMap watch + hot-reload | Forbidden by AC18 "restart required to update" | |
| Refuse to start when allowlist file missing | Treats empty allowlist as a bug; deployer choice (zero admins) is legitimate | |

**Auto-selected:** Read once at start; fail-closed; restart to update.

---

## Code structure

| Option | Description | Selected |
|--------|-------------|----------|
| `internal/platformapi/` with subpackages per endpoint group (auth, envkeys, environments, hydrate, admin) + `store/` reader + `render/` helpers | Mirrors Phase 1/2 `internal/controller/` per-kind layout; reviewer-friendly | ✓ |
| Flat `internal/platformapi/` with handler files per endpoint | Smaller package count but ~12+ files at one level | |
| Single `internal/server.go` god-package | Hard to test; hard to enforce per-group invariants | |

**Auto-selected:** Subpackages per endpoint group.

---

## LiteLLM client extensions

| Option | Description | Selected |
|--------|-------------|----------|
| Add `UserNew`, `UserInfoByEmail`, `TeamMemberAdd`, `KeyGenerate` to `internal/litellm/Client`; NoopClient stubs them; compile-time canary | Mirrors Phase 2 D-01 lift convention; closes Phase 02.2 D-02 prerequisite | ✓ |
| Subordinate `internal/litellm/sso/` sub-package | Premature partitioning; LiteLLM client is one logical surface | |
| Inline calls in handler code | Loses NoopClient testability; couples handlers to wire format | |

**Auto-selected:** Extend existing `internal/litellm` package; NoopClient stubs.

---

## Force-refresh annotation patch

| Option | Description | Selected |
|--------|-------------|----------|
| Typed controller-runtime client + `client.MergeFrom(orig)` JSON merge-patch | Type-safe; idiomatic; reuses informer-cache client | ✓ |
| Raw `kubectl`-style PATCH via REST client | Bypasses informer cache; loses type-checking | |
| Strategic merge patch | Not supported on CRDs (no strategic merge metadata) | |

**Auto-selected:** Typed client + MergeFrom JSON merge-patch.

---

## Claude's Discretion

Items where the workflow logged the choice but the alternatives were ergonomic / mechanical rather than spec-shaping:

- **`request_id` ULID source** — `github.com/oklog/ulid/v2` (lock-free, time-ordered, base32-encoded).
- **Bearer plaintext** generated via `crypto/rand` + `base32.StdEncoding.WithPadding(NoPadding)` (16 bytes → 26 chars after the `pk_`/`ek_` prefix).
- **OIDC JWKS cache** TTL 24h with on-signature-failure refresh (go-oidc default pattern).
- **Test infrastructure** — Ginkgo + Gomega + envtest from Phase 1; testcontainers-go for Postgres + Redis; `httptest.Server` for Dex + LiteLLM mocks.
- **Counter-hook stubs inline; full Prometheus emitter Phase 5** — Phase 2 convention.
- **No new DB migration** — Phase 02.2's `000003_litellm_token` already added the columns Phase 3 needs.

## Deferred Ideas

Captured in CONTEXT.md `<deferred>` section. Highlights:

- Hosted login web page (v1beta1).
- Cookie-backed user session (rejected — D-04 is stateless).
- K8s ConfigMap watch for admin allowlist hot-reload (forbidden by AC18).
- HA Platform API multi-replica leader election (out of scope; informer-only manager is multi-replica-compatible already).
- Engineer-pending: `scripts/uat-g1.sh` against live LiteLLM v1.83.10 (Phase 02.2 verification debt, NOT a Phase 3 task).
