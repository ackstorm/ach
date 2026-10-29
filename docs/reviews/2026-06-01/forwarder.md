# Forwarder — Code Quality Review

> **Date:** 2026-06-01 · **Service:** Forwarder · **Packages:** `forwarder + keystore + keys` · **Lines:** 3.0k
> **Findings:** 13 raw → **13 verified** · Read-only review, no code changed.
> **Method:** parallel reviewers per package → adversarial verification (grep/read to refute) → synthesis.
> **Axes:** dead code · complexity · over-engineering · optimization · duplication.
> Part of the [full codebase review](./README.md).

---

# ACH Forwarder — Code Quality Review

## De-duplication note
13 raw findings → 13 distinct issues across 8 units. No same-root collisions found: the two `proxy/handlers.go` duplication findings target different line ranges/concerns (outcome-constant switch vs V1/Gemini handler bodies) and are kept separate. All findings retained.

---

## Duplication

### D1 — caches: LISTEN/NOTIFY + ticker loop duplicated across bipcache & envstore
- **Location:** `internal/forwarder/bipcache/cache.go:106-142`, `internal/forwarder/envstore/store.go:91-120`
- **Problem:** `Cache.Run` and `Store.Run` implement byte-identical initial-Refresh + Listener-Subscribe + 5-min-ticker-select lifecycle; only receiver/log-strings/Refresh-method differ (dup acknowledged in `db/listen.go` doc).
- **Fix:** Extract `db.RunRefreshLoop(ctx, pool, channel, interval, log, refresh func(ctx) error) error`; both `Run`s become one-line delegations.
- **Severity:** low

### D2 — keystore: two near-identical read-through+singleflight Redis cache layers
- **Location:** `internal/keystore/keystore.go:82-166`, `internal/keystore/teamsresolver.go:139-225`
- **Problem:** `redisCachedResolver` and `redisCachedTeamsResolver` share a copy-pasted 5-step GET/unmarshal/singleflight/assert/Set skeleton (incl. identical "malformed → fall through, do NOT DEL" comment).
- **Fix:** Optional — extract generic `cacheRead[T](...)` helper. **Weigh carefully:** the two have *inverted* negative-cache policy (keystore must NOT cache nil per KEY-07/08; teams DOES cache empty at negTTL) — a shared helper risks the security barrier for modest payoff.
- **Severity:** low

### D3 — proxy: `codeMessage` re-switches over string literals duplicating the `outcome*` constants
- **Location:** `internal/forwarder/proxy/handlers.go:152-197`
- **Problem:** `codeMessage` switches on raw literals byte-equal to the `outcome*` constants and re-enumerates `classifyPrecheckErr`'s closed case set — taxonomy maintained in two parallel switches; literals can silently drift from constants.
- **Fix:** Reference the `outcome*` constants; collapse the two switches into one `map[code]struct{status int; msg string}` table.
- **Severity:** low

### D4 — proxy: `HandlerV1` and `HandlerGemini` identical but for one metrics label
- **Location:** `internal/forwarder/proxy/handlers.go:51-68`
- **Problem:** Both handler bodies are byte-identical except the route label (`"/v1"` vs `"/gemini"`) passed to `IncRequests`.
- **Fix:** Extract `taggedPassthrough(deps, routeLabel string) http.HandlerFunc` (mirrors existing `handlerNamed`); define both as one-line wrappers.
- **Severity:** low

### D5 — jwt: `LoadOnce`/`Reload` duplicate next-slot handling + success-log tail
- **Location:** `internal/forwarder/jwt/secret.go:108-128, 164-184`
- **Problem:** After the (legitimately distinct) current-slot error policy, the next-slot resolve/clear branch + trailing `Info` success-log are structurally repeated (~18 lines; the 6-line Info block is byte-identical modulo "loaded"/"reloaded"). Note: next-slot *error* log strings differ deliberately (refuse-to-start vs refuse-to-update), so not "verbatim" as stated.
- **Fix:** Extract private `applyNextSlot(secret)` helper; keep the two entry points as thin wrappers expressing only the current-slot policy.
- **Severity:** low

### D6 — plumbing: byte-identical shutdown block in both `Start` select arms
- **Location:** `internal/forwarder/runnable.go:74-89`
- **Problem:** `ctx.Done()` and `errCh` arms repeat the same 10s-timeout / traffic.Shutdown / health.Shutdown / wg.Wait sequence; only the return value differs.
- **Fix:** Hoist into a `shutdown()` closure; arms become `case <-ctx.Done(): shutdown(); return nil` / `case err := <-errCh: shutdown(); return err`.
- **Severity:** low

---

## Dead code

### X1 — plumbing: five `forwarder.Deps` fields set by cmd but never read by `New()`
- **Location:** `internal/forwarder/server.go:31-47`
- **Problem:** `Pool, Redis, LiteLLM, Pepper, K8sClient` are populated (`forwarder.go:357-361`) but never read; the `K8sClient` "retained for the Secret watcher" doc is stale — that watcher uses `mgr.GetCache()/GetClient()` directly, never `Deps.K8sClient`.
- **Fix:** Remove the five fields + the populating lines + the false `K8sClient` doc justification; re-add when actually wired.
- **Severity:** low

### X2 — keystore: `KeyInfo.Status` is write-only dead state on the cache wire format
- **Location:** `internal/keystore/keystore.go:47, 129, 152`
- **Problem:** Hardcoded to `"active"` in both lookups (SQL already filters `status='active'`), never read for branching, dropped by `middleware.KeyContext` — yet JSON-serialized into every Redis cache entry.
- **Fix:** Remove the `Status` field + both `Status: "active"` assignments + json tag; adjust test fixtures.
- **Severity:** low

### X3 — precheck: `Deps.Namespace` is write-only — set in production, never read
- **Location:** `internal/forwarder/precheck/check.go:32-42`
- **Problem:** Populated at `server.go:75` and in tests but never read in-package (`check/checkEk/checkPk` use only `EnvProvider`/`TeamsResolver`); the "left for log fields" doc is unmet — the package has no log statements.
- **Fix:** Drop the field + the `Namespace: deps.Namespace` line; add back when a logger genuinely lands.
- **Severity:** low

### X4 — caches: unreachable `m == nil` guards in `Resolve`/`Get`/`List`
- **Location:** `internal/forwarder/bipcache/cache.go:67-69`, `internal/forwarder/envstore/store.go:48-51, 63-66`
- **Problem:** `New` always `Store`s a non-nil map and `Refresh` likewise; `atomic.Pointer.Load()` can't return nil post-construction, so the three nil guards are unreachable (empty-map case handled separately).
- **Fix:** Drop the three checks and dereference `Load()` directly; if desired keep one guard behind a documented invariant.
- **Severity:** low

### X5 — keys: `ConstantTimeEqual` has zero production callers (YAGNI helper)
- **Location:** `internal/keys/keys.go:196-211`
- **Problem:** Thin wrapper over `subtle.ConstantTimeCompare`, referenced only by its own 4 tests; doc concedes it's for a "webhook-signature parity in future plans" feature that doesn't exist. Every real compare site calls `subtle.ConstantTimeCompare` directly.
- **Fix:** Delete the helper + its tests; re-add with a real consumer when the webhook feature lands.
- **Severity:** low

---

## Over-engineering

### O1 — jwt: `LoadCurrent`/`LoadNext` exported only for same-package use
- **Location:** `internal/forwarder/jwt/signer.go:134-153`
- **Problem:** Both exported solely for the same-package `SecretLoader` (not interface methods; `*signerSlot` arg is unexported so external calls are impossible), forcing two `//nolint:revive` directives + apologetic "package-private by convention" doc paragraphs.
- **Fix:** Rename to unexported `loadCurrent`/`loadNext`; delete both `//nolint:revive` and the apologia; keep one sentence on the atomic-publication contract.
- **Severity:** low

---

## Optimization

### P1 — headers: `hopByHop` set rebuilt on every request in the proxy hot path
- **Location:** `internal/forwarder/headers/strip.go:87-91`
- **Problem:** `StripAndRewrite` (per-request Director on `/v1`,`/gemini`,`/mcp`,`/a2a`) allocs a fresh map + 8 inserts from the immutable `hopByHop` slice on every call.
- **Fix:** Replace `hopByHop []string` with package-level `var hopByHopSet = map[string]struct{}{...}` (or build once in `init()`); look up `hopByHopSet[k]` directly.
- **Severity:** low

---

## Top 5 highest-leverage cleanups (ranked)

1. **X1 — five dead `forwarder.Deps` fields + stale K8sClient doc** (`server.go:31-47`). Highest leverage: removes 5 unused fields, a misleading doc claim, and the only reason `pgxpool`/`redis`/`litellm`/`client` imports stay alive in the wiring — pure subtraction, zero behavior risk.
2. **D1 — caches LISTEN/NOTIFY+ticker loop** (`bipcache`/`envstore`). One `db.RunRefreshLoop` helper kills ~26 dup lines across 2 sites and future-proofs jitter/coalescing/metrics changes against single-site drift; the dup is already self-acknowledged in `db/listen.go`.
3. **X2 — `KeyInfo.Status` write-only on the auth cache wire format** (`keystore.go`). Removes dead state that's JSON-serialized into *every* Redis cache entry on the auth hot path — slim cache values + delete a misleading invariant carrier.
4. **D3 — `codeMessage` literals vs `outcome*` constants** (`proxy/handlers.go`). Binds the error taxonomy to the constant set (compiler-checked) and collapses two parallel switches into one table — kills a real silent-drift risk on user-facing error responses.
5. **P1 — `hopByHop` set rebuilt per request** (`headers/strip.go`). Only finding on the throughput-critical path; one-line move to package-level set eliminates a per-request map alloc + 8 inserts on every forwarded request.

> Deliberately ranked **below** the cleanups: **D2** (generic cache helper) — the inverted nil/negative-cache security policy (KEY-07/08 vs T-04-03-05) makes consolidation higher-risk than its low payoff justifies; leave as-is or refactor only with explicit security review.
