# Platform-API — Code Quality Review

> **Date:** 2026-06-01 · **Service:** Platform-API · **Packages:** `platformapi` · **Lines:** 5.7k
> **Findings:** 20 raw → **19 verified** · Read-only review, no code changed.
> **Method:** parallel reviewers per package → adversarial verification (grep/read to refute) → synthesis.
> **Axes:** dead code · complexity · over-engineering · optimization · duplication.
> Part of the [full codebase review](./README.md).

---

# ACH platform-api — Code Quality Review

## Deduplication note
Three findings describe the **same root** (envkeys `CreateHandler` 3-SELECT redundancy): `envkeys/optimization` and `plumbing/optimization` are duplicates — merged into **OPT-1**. Two findings describe the **same root** (`hasIntersect` 4-copy duplication): `hydrate/duplication` and `environments_teams/duplication` — merged into **DUP-4**. Net: 19 findings → 16 unique.

---

## Optimization

### OPT-1 · `internal/platformapi/envkeys/handler.go:184-259` · low
- **Problem:** `CreateHandler` issues 3 identical `SELECT … FROM environments` for one row — `GetEnvironment` (184), then `EnvironmentTerminating` (210) and `EnvironmentAccessGroupSynced` (236), the latter two re-calling `GetEnvironment` internally (`store/store.go:82,109`), all uncached PK lookups.
- **Fix:** Derive both predicates off the `env` row already held at line 184: `terminating := env.DeletionTimestamp != nil`; add a row-taking synced helper (e.g. `store.AccessGroupSyncedFromRow(env)` wrapping the unexported `decodeAccessGroupSynced`). 3 SELECTs → 1.

---

## Duplication

### DUP-1 · `internal/platformapi/envkeys/handler.go:184-373` · medium
- **Problem:** Internal-error audit+render block copy-pasted 6× and the `classifyLitellmErr` triple 4× inside `CreateHandler` (~10 sites; pattern also recurs in Revoke/List handlers).
- **Fix:** Two handler-bound helpers — `emitInternalError(logMsg, err)` and `emitLitellmError(err, logMsg)` — each doing `Logger.Error` + `EmitAudit` + `render.Error`. Collapses ~10 blocks to one-liners.

### DUP-4 · `hasIntersect` ×4: `hydrate/handler.go:383`, `environments/handler.go:273`, `envkeys/handler.go:537`, `store/store.go:183` · low
- **Problem:** Identical 12-line set-intersection helper duplicated byte-for-byte across 4 packages; comments claim deliberate-for-boundaries, but the primitive is trivial.
- **Fix:** Promote one exported `teams.HasIntersect(a, b []string) bool` (the `achteams`/`teams` package already exists as natural home) and delete the 4 copies. Caveat: `envkeys` does **not** import `store` today (uses a local `envStore` interface), so the home must be `teams`, not `store`, to avoid new coupling.

### DUP-2 · `internal/platformapi/auth/sso.go:276-523` · low
- **Problem:** `CallbackHandler` repeats the audit-emit + render-error failure block ~14×, varying only by (outcome, status, msg, actor, optional keyID); outcome string must be hand-synced between `EmitAudit` and `render.Error` (footgun).
- **Fix:** `func (deps Deps) fail(ctx, w, actor, outcome string, status int, msg, reqID, keyID string)` emitting both; each branch becomes one call. Cuts ~80 lines, threads outcome once.

### DUP-3 · `internal/platformapi/admin/handler.go:159-219 vs 392-416, 231-305 vs 422-447` · low
- **Problem:** pk_/ek_ revoke side-effect sequences (DB flip / LiteLLM RevokeKey / Redis Del / audit) reimplemented near-verbatim in `revokePkInline`/`revokeEkInline` vs the rendering handlers — ordering + audit shape live in 2 places per key type.
- **Fix:** Rendering handlers call the inline helpers and translate returned error to HTTP/audit; keep ordering + audit construction in one place per type. Preserve pk_ WARN-04 (LiteLLM-unreachable = 200, not error).

---

## Complexity

### CPLX-1 · `internal/platformapi/envkeys/handler.go:149-497` · low
- **Problem:** `CreateHandler` is a 349-line single `http.HandlerFunc` closure walking 8 inline steps; bulk is repeated audit boilerplate (flat guard clauses, shallow nesting).
- **Fix:** Apply DUP-1 helpers first; then extract `validateAndLoadEnv` (3a/3b/4), `provisionUser` (6), `mintAndInsert` (7/8) so the handler orchestrates ~6 calls.

### CPLX-2 · `internal/platformapi/auth/sso.go:265-571` · low
- **Problem:** `CallbackHandler` is a ~305-line closure with 14 sequential guard branches + the D-20 Redis-writeback tail; full 8-step OIDC sequence inlined.
- **Fix:** Apply DUP-2 `fail` helper; then extract steps 6-8 (mint pk_/keyid, hash, KeyGenerate, INSERT+compensate, audit+writeback) into `mintAndPersistPK(...) (callbackResponse, error)`, leaving the handler as the linear OIDC orchestrator.

---

## Over-engineering

### OE-1 · `internal/platformapi/admin/handler.go:28-36, 199-201, 293-295, 406-408, 437-439` · low
- **Problem:** Best-effort Redis `DEL` under `adminCacheKeyPrefix` ("ach:revoke:keyid:") in all 4 revoke paths is a guaranteed-miss — nothing SETs/reads that namespace (real key is "ach:key:"+credential_hash); justified only as observability/ordering invariant, exists to satisfy one test. Note: ek_ paths *could* do the real `"ach:key:"+row.CredentialHash` DEL (hash is available there) as `envkeys` does.
- **Fix:** Drop the no-op `Del` (60s TTL already bounds staleness), or wire the real invalidation where `credential_hash` is available; if kept for symmetry, collapse 4 lines into one helper.

### OE-2 · `internal/platformapi/auth/sso.go:722-747` · low
- **Problem:** `containsCaseInsensitive` is a 26-line manual double-loop doing strict byte equality (NOT case-insensitive, per its own comment); single caller passes literal `"404"`; the "avoids ToLower alloc" rationale is moot (no lowering; `strings.Contains` allocates nothing).
- **Fix:** Replace call with `strings.Contains(err.Error(), "404")` (strings already imported) and delete the function.

### OE-3 · `internal/platformapi/admin/handler.go:550-573` · low
- **Problem:** Hand-rolled 24-line `itoa` reimplements `strconv.Itoa` for one audit Extra value; sibling `environments/handler.go` already imports `strconv.Itoa`.
- **Fix:** Delete `itoa`, use `strconv.Itoa(revokedCount)` at handler.go:379, import `strconv`.

---

## Dead code

### DC-1 · `internal/platformapi/server.go:92-107` · low
- **Problem:** Orphaned doc-comment narrates an `envKeysStoreAdapter` type that exists nowhere, mislocates `envkeysDBAdapter` ("inline below" — actually in `adapters.go:19`), and references a nonexistent `adminDB`.
- **Fix:** Delete the comment block; optionally fold a one-liner next to `envkeysDBAdapter` in `adapters.go`.

### DC-2 · `internal/platformapi/middleware/chi_compat.go:1-37` · low
- **Problem:** Entire file is vestigial — pins the chi import + compile-asserts middleware signatures, both now redundant (`server.go` uses `chi.NewRouter()`/`chi.Router` directly and `r.Use(pamw.RequestID/ContentTypeJSON)` type-checks at call site); comments admit Plan 03-06 (shipped) supersedes it.
- **Fix:** Delete the file entirely.

### DC-3 · `internal/platformapi/auth/sso.go:41-50` · low
- **Problem:** `Deps.OIDCProvider *oidc.Provider` is write-only — verification runs through `IDTokenVerifier`; provider threaded through two Deps structs for a nonexistent consumer.
- **Fix:** Drop `OIDCProvider` from `auth.Deps` + assignments at `server.go:129` and `platform_api.go:253`.

### DC-4 · `internal/platformapi/admin/handler.go:60` · low
- **Problem:** `Deps.Pepper []byte` never read in admin package (comment admits "compositional parity"); dead plumbing across 3 files.
- **Fix:** Drop `Pepper` from `admin.Deps` + assignment at `server.go:205`.

### DC-5 · `internal/platformapi/hydrate/handler.go:48,50` · low
- **Problem:** `Deps.Allowlist` + `Deps.Namespace` never read (docs say "retained for parity/symmetry"); admin status comes from `keyCtx.IsAdmin`, Store is namespace-scoped.
- **Fix:** Drop both fields + assignments at `server.go:169,171`.

### DC-6 · `internal/platformapi/environments/handler.go:54,56` · low
- **Problem:** `Deps.Allowlist` + `Deps.Namespace` write-only (same parity rationale as DC-5); populated at `server.go:192,194`, never read.
- **Fix:** Drop both fields + the `server.go:192,194` assignments.

### DC-7 · `internal/platformapi/envkeys/handler.go:506,525-530` · low
- **Problem:** `insertErrCredentialHashCollision` class computed by `classifyInsertError` but the sole caller branches only on `insertErrEkidCollision`; a credential-hash collision falls through identically to `insertErrOther` — the const + switch case are unreachable-behavior dead code.
- **Fix:** Fold case 525-529 into `default`/`return insertErrOther` and drop the const — OR wire the caller to skip compensation on hash collision if that was the intent.

---

## Top 5 highest-leverage cleanups

1. **DUP-1** (`envkeys/handler.go` ~10 copy-pasted audit+render blocks) — only **medium** finding; two small helpers kill the largest copy-paste hazard AND directly unlock CPLX-1's extraction. Highest leverage: one change, two findings resolved.
2. **DUP-2 + CPLX-2** (`sso.go` `CallbackHandler`) — the `fail` helper removes ~80 lines and the outcome-must-match-twice footgun in an **audit/security path**, then enables the `mintAndPersistPK` extraction. One helper, two findings.
3. **DUP-4** (`hasIntersect` ×4 → `teams.HasIntersect`) — kills duplication across 4 packages in one move; pure trivial helper, lowest risk, repo-wide hygiene win (mind the `envkeys`→`store` coupling caveat: home is `teams`).
4. **DC-2 + DC-1** (`chi_compat.go` deletion + `server.go` orphan comment) — delete a whole vestigial file plus a misleading comment that actively misdescribes the codebase; zero-risk, removes reader-confusion debt.
5. **OPT-1** (`envkeys` 3→1 SELECTs) — the only behavioral/perf finding; small surgical change (read predicates off the in-hand `env` row), trims 2 DB round-trips per ek_ create. Low impact but the only one that changes runtime behavior.

Batch the 6 write-only `Deps`-field removals (DC-3/4/5/6 + OPT-1's neighbors) as a single mechanical sweep — individually trivial, collectively they de-clutter the Deps bags and `server.go` wire-up in one commit.
