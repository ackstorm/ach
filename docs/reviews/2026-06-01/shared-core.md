# Shared-Core — Code Quality Review

> **Date:** 2026-06-01 · **Service:** Shared-Core · **Packages:** `db + litellm + credhash + config + metrics + audit` · **Lines:** 6.7k
> **Findings:** 25 raw → **15 verified** · Read-only review, no code changed.
> **Method:** parallel reviewers per package → adversarial verification (grep/read to refute) → synthesis.
> **Axes:** dead code · complexity · over-engineering · optimization · duplication.
> Part of the [full codebase review](./README.md).

---

## Code-quality findings — ACH shared libs

### dead_code

#### HIGH
_none_

#### MEDIUM

**litellm write-CRUD surface unused** — `internal/litellm/{model,mcp,agents}.go` (model.go:18-160, mcp.go:18-47, agents.go:12-43)
Create/Update/Delete for Model/MCP/Agent (11 methods) have zero callers outside the package; not in the `Client` interface — spike-era write plumbing the Postgres-as-SoT migration (#34) orphaned.
Fix: delete the 11 write methods + their tests; keep only the live `List*` read paths.
Severity: medium

**litellm request types backing dead methods** — `internal/litellm/types.go` (33-54, 73-75, 96-116, 139-199, 224-239)
`Deployment/updateDeployment/ModelDeleteRequest/UpdateTeamRequest/DeleteTeamRequest/MCPServerRequest/MCPServerUpdateRequest/AgentConfig` (~120 lines, MCP structs ~26 fields each) feed only the dead write methods.
Fix: remove with the methods; keep `NewTeamRequest` (reached via `EnsureDefaultTeam`).
Severity: medium

**BIP pool-form delete funcs unreferenced** — `internal/db/backend_identity_policies.go` (233 `SoftDeleteBIP`, 255 `DeleteBIP`)
Both have zero callers anywhere (incl. tests); production drains via `SoftDeleteBIPTx`, no BIP hard-delete path exists. Sibling pool-forms all have real callers.
Fix: delete both (keep `softDeleteBIPSQL`, used by the Tx form), or add a regression test if a hard-delete path is planned.
Severity: medium

#### LOW

**`UpdateDeployment` exported alias unused** — `internal/litellm/types.go:51-54`
`type UpdateDeployment = updateDeployment` has zero references; the aspirational doc-comment promises callers that never arrived (`UpdateModel` exposes the lowercase type directly).
Fix: delete the alias (underlying `updateDeployment` stays live until `UpdateModel` is removed).
Severity: low

**10 `Extra`/`Raw` `json:"-"` overflow fields dead** — `internal/litellm/types.go` (30, 64, 93, 110, 127, 167, 198, 215, 238, 250)
Every `Extra`/`Raw` field is `json:"-"` (so it cannot even capture overflow) and is never read or assigned — pure reader noise.
Fix: drop them; real forward-compat capture needs an embedded `json.RawMessage` + custom `UnmarshalJSON`, not `json:"-"`.
Severity: low

**`ErrAlreadyExists` sentinel never used** — `internal/litellm/errors.go:18-22`
Documented as returned by Create-shaped methods, but no method returns it and nothing `errors.Is` against it; real "already exists" detection lives at the controller layer via `APIError` string-match.
Fix: remove it, or wire it into the Create methods if sentinel detection is wanted.
Severity: low

**`ErrorKind`/`classify()` test-only** — `internal/litellm/errors.go:69-96`
`ErrorKind` + `KindTransient/KindPermanent/KindAuth401` + `classify(status)` have no production caller; `makeRequest` (restclient.go:138-161) re-implements the same status switch inline.
Fix: delete them + the test, or have `makeRequest` call `classify()` so the logic lives in one place.
Severity: low

**`IsAuth401` helper test-only** — `internal/litellm/restclient.go:164-173`
Wraps `errors.As(&Auth401Error)` but no production caller; the two real 401 sites (envkeys handler, litellmconnection controller) call `errors.As` directly.
Fix: drop it, or adopt it at the two hand-rolled `errors.As` sites.
Severity: low

**`credhash.Equal` test-only** — `internal/credhash/credhash.go:40-62`
Constant-time hex compare with no production caller (hash matching is an indexed SQL lookup, the hash _is_ the key); parallel `keys.ConstantTimeEqual` is likewise test-only.
Fix: remove `Equal` + tests, or document the intended call site; if kept, consolidate with `keys.ConstantTimeEqual`.
Severity: low

### duplication

#### HIGH
_none_

#### MEDIUM

**7 byte-identical origin-gated UPSERT wrappers** — `internal/db/{environments,plugins,prompts,artifacts,backend_identity_policies,marketplace_plugins,external_refs}.go` (pool-form + tx-form per file; e.g. environments.go:118-165)
All 7 projection tables ship the same two-function upsert shape: pool-form `begin/defer Rollback/call upsertXTx/transient-classify/commit` + tx-form `QueryRow.Scan(&pk)/ErrNoRows→ErrOriginConflict/transient-classify/wrap`; only the SQL const, field list, and ident args differ. ~14 funcs collapse to 2 helpers + 7 call sites (with_tx_notify.go:67-120 adds 7 more pass-throughs).
Fix: extract `runInTx(ctx, pool, fn)` (unify with `WithTxNotify`) + `upsertReturning(ctx, q, sql, ident, args...)`; keep per-file SQL consts + Row structs.
Severity: medium

#### LOW

**`ListPersonalKeysByOwner` re-hand-codes the pagination engine** — `internal/db/personal_keys.go:183-249`
Re-implements the `clampLimit/decodeCursor/with-without-cursor SQL/limit+1 peek/scan loop/nextCursor-trim` machinery already factored into `listEnvironmentKeys` (environment_keys.go:144-213); ~30 lines diverging only by table/columns/Scan target (`PkKeyInfo` vs `EkKeyInfo`).
Fix: generic `listKeys[T any](..., scanFn)` paginator so all key-list funcs become thin wrappers (needs Go generics + per-type scan callback; engines differ in query-shape count — 2 vs 4 — so not a trivial lift).
Severity: low

**`List*` row-scan loops repeat the same streaming idiom** — `internal/db/{environments,backend_identity_policies,marketplace_plugins}.go` (ListEnvironments 213-255, ListBIPsByTarget 144-183, ListAllBIPs 187-223, ListMarketplacePlugins 116-153)
All four repeat `Query/transient-guard/defer Close/for rows.Next{Scan;append}/rows.Err-guard`; `ListBIPsByTarget` and `ListAllBIPs` are near-verbatim (byte-identical 11-field scan blocks), and the BIP scan-target list is duplicated 3x (also `GetBIPByName` 125-128).
Fix: private `scanBIPRows(rows) ([]BIPRow, error)` collapses the BIP pair + the WHERE-only delta; cross-table dedup is bounded by differing row types, leave inline.
Severity: low

**`MustEnvIntPositive` vs `EnvIntNonNeg` + wrong call site for `ACH_REDIS_DB`** — `internal/config/config.go:90-134`
The two parsers are identical but for the `<=0` vs `<0` check; platform-api (`platform_api.go:128`) uses the 0-rejecting `MustEnvIntPositive` for `ACH_REDIS_DB` **and discards the error**, while forwarder/content-service use `EnvIntNonNeg` — so an explicit `ACH_REDIS_DB=0` on platform-api is silently coerced via the `(0,err)` path, diverging from the two already-fixed services.
Fix: switch `platform_api.go:128` to `EnvIntNonNeg` and stop discarding the error (or collapse both into `envIntAtLeast(key, fallback, min)`).
Severity: low

### over_engineering

#### HIGH
_none_

#### MEDIUM
_none_

#### LOW

**11 exported `UpsertXTx`/`SoftDeleteXTx` pass-throughs** — `internal/db/with_tx_notify.go:72-120`
For Environment/Plugin/Prompt/Artifact/ExternalRef/Marketplace, an exported `UpsertXTx` whose entire body is `return upsertXTx(...)` mirrors each unexported twin — a redundant re-export layer the BIP file proves unnecessary (it exports `UpsertBIPTx` directly with no twin).
Fix: adopt the BIP pattern — rename each unexported `upsertXTx` to the exported form in its own file, have the pool-form call it, delete the pass-throughs (leaving only `WithTxNotify`).
Severity: low

**Two public functions for one collector** — `internal/metrics/shared.go:27-54`
`MustRegisterLitellmUnreachable(*prometheus.Registry)` is a one-line forwarder to `...On(prometheus.Registerer)`; since `*Registry` already satisfies `Registerer`, all four call sites work through a single `Registerer`-typed func.
Fix: keep one `MustRegisterLitellmUnreachable(reg prometheus.Registerer)`; drop the wrapper + its duplicate doc block.
Severity: low

---

### Top 5 highest-leverage cleanups

1. **Delete the litellm write-CRUD surface** (model/mcp/agents.go, 11 methods) **+ its request types** (types.go ~120 lines) **+ tests** — one coherent removal of the #34-orphaned write plumbing; largest dead-code mass, two findings fall together.
2. **Extract `runInTx` + `upsertReturning` helpers** for the 7 projection UPSERTs — collapses ~14 near-identical funcs (the transient/ErrNoRows classification is copy-pasted 7×, so any bug-fix today touches 7 sites).
3. **Delete dead BIP pool-forms `SoftDeleteBIP` + `DeleteBIP`** — zero callers, no hard-delete path; removes drift risk against the live `SoftDeleteBIPTx`.
4. **Fix `ACH_REDIS_DB` parser mismatch on platform-api** — only finding with a (latent) behavioral edge: malformed `ACH_REDIS_DB` is silently coerced to DB 0 instead of failing fast like the other two services; switch to `EnvIntNonNeg`, stop discarding the error.
5. **Adopt the BIP Tx-form pattern + drop the 11 `with_tx_notify.go` pass-throughs** — pairs naturally with #2 (both touch the projection-writer envelope), removing the redundant re-export layer and unifying the tx helper surface.
