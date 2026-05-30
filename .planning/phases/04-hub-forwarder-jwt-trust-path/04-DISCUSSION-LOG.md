# Phase 4: Hub Forwarder & JWT Trust Path - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-05-26
**Phase:** 4-hub-forwarder-jwt-trust-path
**Areas discussed:** MCP/A2A upstream URL resolution, JWT signing-key Secret shape + library, BIP informer index + DuplicateTarget detection, Team-membership cache for pk_ on /mcp /a2a

---

## MCP/A2A upstream URL resolution

| Option | Description | Selected |
|--------|-------------|----------|
| Hybrid: background poll + miss single-flight | In-proc map refreshed every 30s via ListMCPServers/ListA2AAgents; request hits map. Miss → single-flight LiteLLM lookup, populate, return. | |
| Redis-shared lazy cache | ach:mcpserver:<name> / ach:a2aagent:<name> keys, TTL 60s, lazy populate via single-flight. | |
| Pure background poll (no fallback) | 30s poll fills map. Miss → 503 unknown_resource. | |
| Per-request LiteLLM (no cache) | Every request calls LiteLLM. | |

**User's first response (in Spanish, paraphrased):** "Not sure we have to resolve whether the MCP or A2A exists. We have to validate here, see if we need to generate JWT and forward. Whether it exists or not — is that our job? (I think so)"

**Follow-up question:** Confirm single-upstream model — forwarder forwards to LiteLLM only; LiteLLM owns second-hop to real backends + returns 404 on unknown {name}.

| Follow-up option | Description | Selected |
|---|---|---|
| Yes: forwarder is a thin LiteLLM front | All routes forward to single LITELLM_BASE_URL. Existence of {name} is LiteLLM's problem (404 passes through). ACH only owns: header rewrite, key resolve, ek_ env-ownership pre-check (informer), pk_ team pre-check (LiteLLM teams cache), JWT minting via BIP. | ✓ |
| No: forwarder dispatches per-resource to real backends | ACH knows real MCP/A2A URLs and bypasses LiteLLM second hop. Re-opens cache strategy. | |

**Locked decision:** Single-upstream model. No MCP/A2A URL registry; LiteLLM owns second-hop. The original cache-strategy question is moot. (Captured as D-05 in CONTEXT.md.)

**Notes:** User correctly identified that existence validation can be delegated to LiteLLM. ACH's job at /mcp/{name} + /a2a/{name} is the §5.1 step-4 pre-check (ek_ env-ownership via informer; pk_ team intersection via LiteLLM teams cache) + BIP-driven JWT minting + header strip-rewrite. Backend URL resolution stays with LiteLLM.

---

## JWT signing-key Secret shape + library

### Q1: Secret data layout

| Option | Description | Selected |
|--------|-------------|----------|
| current + next slots | Two fixed slots: current.kid/seed and next.kid/seed (32B Ed25519 seed b64). Rotation: regenerate next → wait ≥24h → promote next→current. JWKS publishes both non-empty slots. | ✓ |
| Single keys.json blob (multi-kid array) | Data key keys.json = JSON array [{kid, seed, alg, status, createdAt}]. Flexible (>2 keys) but v1alpha1 never needs it. | |
| Per-kid data keys | Data keys signing-key-<kid>.seed + active-kid pointer. Easy kubectl inspection. More entries on rotation. | |

### Q2: JWT library choice

| Option | Description | Selected |
|--------|-------------|----------|
| stdlib crypto/ed25519 + hand-rolled JOSE | Header + claims marshalled to JSON, base64url, sign with ed25519.Sign. ~80 LoC. No external dep, minimal attack surface. | |
| github.com/golang-jwt/jwt/v5 | Ubiquitous, supports EdDSA via SigningMethodEdDSA + ed25519.PrivateKey. Sign API trivial. | ✓ |
| github.com/lestrrat-go/jwx/v2 | Richer JWS/JWK/JWKS support; idiomatic JWKS publishing. Heavier dep tree. | |

**Locked decisions:** Secret = current+next slots (D-10). Library = golang-jwt/jwt/v5 for signing (D-11). JWKS marshalling = hand-rolled JSON (D-12) since fixed-format JWKs don't justify the jwx dep.

**Notes:** Current+next slots gives the simplest mental model for manual rotation (publish-overlap-revoke). JWKS hand-roll keeps the public-facing serialization in plain Go for review surface.

---

## BIP informer index + DuplicateTarget detection

### Q1: Forwarder BIP lookup strategy

| Option | Description | Selected |
|--------|-------------|----------|
| controller-runtime IndexField | Register IndexField on target/<kind>/<name> at manager setup. cache.List with MatchingFields returns matches; sort by metadata.name; first wins. | ✓ (rule flipped to alpha-LAST per follow-up) |
| In-proc map maintained by event handlers | Watch BIPs via informer; OnAdd/OnUpdate/OnDelete rebuild internal map[kind/name][]bipName. O(1) but bespoke concurrency surface. | |
| List-and-filter via informer cache | cache.List + Go filter. O(N), N small. | |

### Q2: Operator DuplicateTarget detection trigger

| Option | Description | Selected |
|--------|-------------|----------|
| Cross-enqueue on any BIP change | Watches() with EnqueueRequestsFromMapFunc. Each reconcile lists peers + computes winner + writes Synced=True/False. | (initial pick, then superseded by TODO.md §6) |
| Re-reconcile all BIPs on any change | Simpler trigger. | |
| Periodic full sweep | 60s scan. | |

**User's response (annotated):** "ok, but I changed my mind, now is: alpha-last wins" + "option 1: Cross-enqueue on any BIP change, but read TODO.md as I have some changes to do in BIP that should be taken into consideration"

**TODO.md §6 (read after user pointer):** Authoritative design decision (2026-05-26):
- NO DuplicateTarget reconciler.
- NO Synced status churn.
- NO shadow flip.
- Forwarder resolves alphabetical-LAST winner from spec via informer.
- Operators rename CRs (`zz-` suffix) to flip precedence.
- Memory ref: `[[feedback_bip_no_shadow_logic.md]]`.

**Locked decisions:**
- D-09: Forwarder uses controller-runtime IndexField on (target.kind, target.name); sort by metadata.name ASC; take Items[len-1] (alpha-LAST). Q2 superseded — no Operator DuplicateTarget reconciler.
- D-19: NO Operator-side BIP duplicate logic in Phase 4 or any future phase.
- Phase 1 BIP-reconciler stub doc comment (lines 17–25 + 80–82) is stale and gets scrubbed during this phase.
- ROADMAP Phase 4 SC#3 "losers report DuplicateTarget" clause is stale; planner patches the ROADMAP in this phase.
- ach-old's BIP DuplicateTarget reconciler is SKIPPED when porting per TODO.md §2 step 5.

---

## Team-membership cache for pk_ on /mcp /a2a

| Option | Description | Selected |
|--------|-------------|----------|
| Redis, separate keyspace, new keystore.TeamsResolver | ach:teams:<owner_email> → JSON []string, TTL 60s, single-flight on miss. New TeamsResolver in internal/keystore. Phase 5 reuses. Uniform cross-replica view. | ✓ |
| Extend keystore.KeyInfo with teams[] | Resolve teams inside pk_ key resolution; cache as part of ach:key:<hash>. Forces Phase 3 KeyInfo contract revision. | |
| In-proc LRU per forwarder replica | hashicorp/golang-lru. No Redis hop. Per-replica drift during TTL. | |
| Per-request LiteLLM (no cache) | Every pk_ /mcp /a2a request hits /user/info. Likely violates §5.1 cache budget. | |

**Locked decisions:**
- D-17: New `internal/keystore.TeamsResolver` + `redisCachedTeamsResolver` mirroring Phase 3 D-08 pattern. Key shape `ach:teams:<owner_email>`. TTL 60s. Single-flight via `golang.org/x/sync/singleflight`. Miss path calls `litellm.UserInfoByEmail(owner_email)` (already shipped Phase 3; returns `UserInfo.Teams[]`).
- Fail-closed on LiteLLM unreachable: propagate error; caller returns 503 litellm_unreachable.
- Phase 5 Content Service reuses verbatim for CS-04 `pk_` Team-intersection check.

---

## Claude's Discretion

Items where the spec + Phase 3 patterns determined the answer without needing to ask:

- HTTP server bind topology — two ports (`:8080` traffic+JWKS, `:8081` health) per CLAUDE.md probe convention.
- Middleware chain — mirrors Phase 3 D-02 with per-route Authn bypass for JWKS + health.
- Reverse-proxy implementation — `httputil.ReverseProxy` with custom Director (D-05). Path preserved verbatim.
- Header strip/write contract — blacklist mode (D-06, D-07); allowlist-mode would break legitimate User-Agent/Accept/etc. pass-through.
- JWT claims — locked verbatim by Hub §9.1 (no `jti`, exp=iat+120s, aud=<kind>:<name>).
- `kid` format — `ach-jwt-<26-char-base32-ulid>` (consistent with Phase 3 ULID idiom).
- Manager.Manager — informer-only, no leader election, namespace-scoped (Phase 3 D-20 idiom).
- RBAC — new namespace-scoped Role + RoleBinding for forwarder SA; least-privilege Secret access via `resourceNames`.
- Audit on runtime path — none (OBS-01 forbids).
- Counter hooks inline; full Prometheus emitter Phase 5 (Phase 3 carry-forward pattern).
- JWKS endpoint anonymously accessible, `Cache-Control: public, max-age=3600`, `application/jwk-set+json`.
- Secret mount as volume (NOT env var) — Ed25519 seed is binary; env-var would require base64 indirection.
- `WriteTimeout: 0` on traffic listener for SSE pass-through.
- `httputil.ReverseProxy` default `X-Forwarded-For` behavior preserved.
- Test plan — unit (headers, JWT, JWKS, BIP) + envtest (IndexField, Secret reload, Environment informer) + integration (testcontainers Postgres+Redis + httptest LiteLLM mock) + E2E (kind+Helm).
- New direct dep: `github.com/golang-jwt/jwt/v5`. Reuses existing redis/v9, singleflight, chi/v5, oklog/ulid/v2.
- Helm chart `forwarder-deployment.yaml` shipped in Phase 4 (TODO.md §3 follow-up rolled into this phase so SC#1–#5 can be E2E-verified).

## Deferred Ideas

See `<deferred>` section in `04-CONTEXT.md`. Key items:

- Operator-side `Synced=DuplicateTarget` — PERMANENTLY DROPPED per TODO.md §6.
- JWT `jti` + replay window — Hub §20 v1beta1.
- Dual-key acceptance window for `ACH_LITELLM_SHARED_KEY` — Hub §20 v1beta1.
- Automated key rotation — v1beta1.
- HA Forwarder formal multi-replica testing — deferred unless issue surfaces.
- CORS on JWKS — revisit only if browser-side verifier appears.
- Hot reload of LiteLLM base/shared key — restart-on-change.

## Spec-revision flags for planner

- ROADMAP Phase 4 SC#3 — stale "losers report DuplicateTarget" clause; scrub in this phase.
- Hub spec §9.3 — same scrub per TODO.md §6 cleanup task.
- Phase 1 BIP-reconciler `internal/controller/ach/backendidentitypolicy_controller.go` doc comment lines 17–25 + 80–82 — scrub forecasting Phase-4 DuplicateTarget logic that won't ship.
- ach-old BIP DuplicateTarget reconciler — SKIP when porting per TODO.md §2 step 5.
