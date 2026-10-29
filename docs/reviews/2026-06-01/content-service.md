# Content-Service — Code Quality Review

> **Date:** 2026-06-01 · **Service:** Content-Service · **Packages:** `contentservice + cachefs` · **Lines:** 1.4k
> **Findings:** 12 raw → **10 verified** · Read-only review, no code changed.
> **Method:** parallel reviewers per package → adversarial verification (grep/read to refute) → synthesis.
> **Axes:** dead code · complexity · over-engineering · optimization · duplication.
> Part of the [full codebase review](./README.md).

---

# Content-Service Code-Quality Review

## Dead Code

### M1 — `ContentTypeForFile` + `contentTypeMarkdown` superseded
- **Location:** `internal/contentservice/content_type.go:7-55`
- **Problem:** Exported `ContentTypeForFile` + `contentTypeMarkdown` are referenced only by tests; production Content-Type policy is `contentTypeFor` in `pipeline.go:185`, and the `text/markdown` fallback is stale per CS-06 (octet-stream default).
- **Fix:** Delete `ContentTypeForFile`, `contentTypeMarkdown`, and `content_type_test.go`; keep the still-live `kind*`/`contentTypeGzip`/`contentTypeOctet`/`gzipSuffix` constants.
- **Severity:** medium

### L1 — `cachefs.SweepTmp` unwired
- **Location:** `internal/cachefs/sweep.go:117-157`
- **Problem:** `SweepTmp` (§10.3 orphan-staging sweep) has no production caller — the "operator hourly Runnable" its doc names was never wired; only its 3 tests call it.
- **Fix:** Either wire the hourly Runnable in `cmd/ach/cmd/operator.go` via `mgr.Add`, or delete `SweepTmp` + its 3 tests + the `maxAge` knob. Decide now.
- **Severity:** low

### L2 — `PromptContentTypeLookup` type + `Deps.PromptContentTypeFn` field
- **Location:** `internal/contentservice/handler.go:45-54, 79-97`
- **Problem:** Transition-only type/field "RETAINED until Plan 05-06"; that wiring already landed (full `Deps` built in `content_service.go`, field never set or read) — pure dead weight + stale doc.
- **Fix:** Remove the `PromptContentTypeLookup` type, the `PromptContentTypeFn` field, and the "Deprecated transition-only fields" doc block.
- **Severity:** low

### L3 — `resolvedRow.AuditKind` / `AuditName` write-only
- **Location:** `internal/contentservice/pipeline.go:55-56, 165-166`
- **Problem:** Both fields are populated but never read; `serve()` emits the success audit from its own local `kind`/`name`, so the struct doc claiming they feed the audit event is stale.
- **Fix:** Drop `AuditKind`/`AuditName` from `resolvedRow` and stop populating them.
- **Severity:** low

### L4 — `EnvRow.{Namespace,Name,DeletionTimestamp,ResourceVersion}` write-only
- **Location:** `internal/contentservice/envcache/cache.go:72-79`
- **Problem:** Four of `EnvRow`'s 8 fields have zero production reads (only test fixtures); the "logging/debug convenience" + "CS-09 draining" doc rationale is false — staleness gates on `contentRow`, not these.
- **Fix:** Drop the four fields (and loader assignments) to shrink the cached JSON, or replace the doc rationale with an honest "reserved/unused" note.
- **Severity:** low

## Duplication

### L5 — Success-path audit `Event` duplicates `writeError`'s block
- **Location:** `internal/contentservice/handler.go:169-177`
- **Problem:** The forwarded-success `audit.Event` is field-for-field identical (5 of 6 fields) to the denial-path one in `errors.go:107-114`; an audit-schema change needs two edits.
- **Fix:** Extract `func (d Deps) emitAudit(ctx, kind, name, outcome, info)` and call from both `serve()` and `writeError`.
- **Severity:** low

### L6 — Hand-rolled `contains()` duplicates `strings.Contains`
- **Location:** `internal/contentservice/envcache/cache_test.go:316-323`
- **Problem:** Test-local `contains()` reimplements substring search though `strings` is already imported and used (`strings.Contains` at line 207) in the same file.
- **Fix:** Delete `contains` and replace its two call sites with `strings.Contains(err.Error(), want)`.
- **Severity:** low

## Complexity

### L7 — Hand-rolled `subtreeHasFile` recursion duplicates `filepath.WalkDir`
- **Location:** `internal/cachefs/sweep.go:92-115`
- **Problem:** Manual recursive walk reimplements `filepath.WalkDir` traversal; `fs.SkipAll` (Go 1.20+, module targets 1.26) makes the "stop at first regular file" early-exit a short callback.
- **Fix:** Replace with `filepath.WalkDir` returning `fs.SkipAll` on the first `!d.IsDir()` entry; wire the callback `err` into the existing "any error ⇒ populated" classification.
- **Severity:** low

### L8 — Redundant two-step assignment in `resolveEnv` ek_ branch
- **Location:** `internal/contentservice/authz.go:134-141`
- **Problem:** After the line-135 guard, `headerEnv` is provably empty or equal to `info.Environment`, so the conditional `if headerEnv != "" { resolved = headerEnv }` is a no-op that obscures the invariant.
- **Fix:** Drop lines 139-141; keep only `resolved = info.Environment`.
- **Severity:** low

## Over-engineering

### L9 — Doc + race-test scaffolding for a non-existent caller
- **Location:** `internal/cachefs/sweep.go:117-129`
- **Problem:** `SweepTmp`'s doc and `TestSweepTmp_RaceTolerance`'s WaitGroup/goroutine model a "concurrent reconciler rename(2)" against a function no reconciler runs — effort spent on an unreachable path (consequence of L1).
- **Fix:** Resolve L1 first; if `SweepTmp` is wired the comments become true, if deleted the test vanishes.
- **Severity:** low

---

## Top 5 highest-leverage cleanups

1. **M1** — `internal/contentservice/content_type.go:7-55`: delete `ContentTypeForFile` + `contentTypeMarkdown` + `content_type_test.go`; collapses two parallel Content-Type implementations into one and removes a stale `text/markdown` branch. *(only medium; largest surface + stale-behavior risk.)*
2. **L1** — `internal/cachefs/sweep.go:117-157`: wire-or-delete `SweepTmp`; forces a decision that also resolves L9, and surfaces a latent operational gap (orphan `.tmp/` files never swept).
3. **L2** — `internal/contentservice/handler.go:45-54,79-97`: remove the completed-transition `PromptContentTypeLookup` type/field/doc — clean public-`Deps` surface, zero risk.
4. **L5** — `internal/contentservice/handler.go:169-177`: extract `emitAudit` so audit-schema changes are single-site — the only finding that reduces future-edit fan-out.
5. **L4** — `internal/contentservice/envcache/cache.go:72-79`: drop or honestly re-document the 4 write-only `EnvRow` fields — trims the per-write Redis payload and removes a misleading doc rationale.
