# ACH — Full Codebase Quality Review (2026-06-01)

> **Scope:** all services, read-only review (no code changed).
> **Method:** per-service multi-agent workflow — parallel reviewers per package →
> adversarial verification of every finding (grep/read to refute) → synthesis.
> Only findings that survived the adversarial pass are recorded.
> **Axes:** dead code · complexity · over-engineering · optimization · duplication
> (correctness/security bugs explicitly out of scope).

This is the index. Detailed findings live in one file per service.

## Per-service reports

| Service | Packages | Lines | Raw → Verified | Report |
|---------|----------|-------|----------------|--------|
| Operator | controller/ach + operator + orphan + snapshot + sources + connection | ~9.9k | 32 → **24** | [operator.md](./operator.md) |
| Platform-API | platformapi | 5.7k | 20 → **19** | [platform-api.md](./platform-api.md) |
| Forwarder | forwarder + keystore + keys | 3.0k | 13 → **13** | [forwarder.md](./forwarder.md) |
| Content-Service | contentservice + cachefs | 1.4k | 12 → **10** | [content-service.md](./content-service.md) |
| CLI (ach-cli) | cli | 9.2k | 28 → **25** | [cli.md](./cli.md) |
| Gateway | gateway | 0.1k | 0 → **0** (clean) | [gateway.md](./gateway.md) |
| Shared-Core | db + litellm + credhash + config + metrics + audit | 6.7k | 25 → **15** | [shared-core.md](./shared-core.md) |
| **Total** | | **~36k** | **130 → 106** | |

24 of 130 raw findings (18%) were rejected during adversarial verification as false positives
(used-via-reflection, build-tag variants, justified polymorphism, superficial-not-real duplication).

## Severity roll-up

- **HIGH (1):** CLI `extract/autoclaim.go` — 343 LOC dead module, security-sensitive, superseded by `hydrate/drift.go`.
- **MEDIUM (~10):** the duplication clusters worth a real refactor (see systemic patterns).
- **LOW (~95):** dead fields/helpers/sentinels, batchable into janitorial commits.

## Four systemic patterns (the real signal)

These recur across services — worth fixing as themes, not one-offs.

### 1. Write-only struct fields "retained for parity/transition"
Present in **every** service that has a `Deps` bag:
- operator DC4/DC5/DC6 · platform-api DC-3/4/5/6 · forwarder X1/X3 · content-service L2/L3/L4 · cli D7/D10/O2/O3
- **Action:** one repo-wide mechanical sweep. Individually trivial; collectively de-clutters every wiring site.

### 2. Per-N copy-paste traps ("add an N → copy M lines")
- **CLI 4 adapters** (claude/codex/gemini/opencode): `copyFile` + `headersWithCredential` + `Detect` + `renderMap` duplicated ×4 — the biggest structural debt found anywhere.
- **Shared-Core 7 db projection tables:** identical origin-gated upsert/notify/classify envelope → `runInTx` + `upsertReturning`.
- **Operator:** 3 content reconcilers + 3 git providers + 6 `SetupWithManager`.

### 3. Generic status / conflict / error writers duplicated
- operator ConflictWithUIRow writer ×6 · platform-api audit+render ~10–14× · content-service audit ×2.

### 4. Dead code orphaned by past migrations
- Shared-Core litellm write-CRUD (11 methods — #34 made operator sole Postgres writer).
- CLI `autoclaim.go` (superseded by `drift.go`).
- Content-Service legacy content-type path.

## One latent bug surfaced (not pure quality)

`ACH_REDIS_DB` parser mismatch (shared-core): platform-api uses `MustEnvIntPositive` (rejects 0)
**and discards the error**, while forwarder/content-service use `EnvIntNonNeg`. An explicit
`ACH_REDIS_DB=0` on platform-api is silently coerced to DB 0 instead of failing fast. Worth a real fix.

## Recommended attack order (highest leverage first)

1. **Delete CLI `autoclaim.go`** (HIGH; −343 LOC; kills drift risk).
2. **CLI adapter consolidation** — U1 (`copyFile`/`headers` → `adapter.go`), then U3 (`detectBySignals`) + U2 (`renderMap[E]`). Kills the "+1 adapter" tax.
3. **Shared-Core `runInTx` + `upsertReturning`** — collapses ~14 near-identical funcs into one bug-fix surface for 7 tables.
4. **Delete Shared-Core litellm write-CRUD** (−11 methods, −120 LOC; #34 orphan).
5. **Operator generics** — `reconcileExternalRefCR[T]` (D2) + `writeConflictWithUIRowStatus[T,PT]` (D1).
6. **Repo-wide write-only-field sweep** (all 5 service `Deps` bags, one commit).
7. **Fix `ACH_REDIS_DB`** parser (latent bug).
