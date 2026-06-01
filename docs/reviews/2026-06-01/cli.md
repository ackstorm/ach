# CLI (ach-cli) — Code Quality Review

> **Date:** 2026-06-01 · **Service:** CLI (ach-cli) · **Packages:** `cli` · **Lines:** 9.2k
> **Findings:** 28 raw → **25 verified** · Read-only review, no code changed.
> **Method:** parallel reviewers per package → adversarial verification (grep/read to refute) → synthesis.
> **Axes:** dead code · complexity · over-engineering · optimization · duplication.
> Part of the [full codebase review](./README.md).

---

# ACH ach-cli — Code Quality Review

Findings deduped (the four-adapter copy-paste cluster spanned `adapters_a` + `adapters_b`; merged into single entries). Grouped by category, ordered by severity.

## Dead Code

| # | Location | Problem | Fix | Sev |
|---|----------|---------|-----|-----|
| D1 | `extract/autoclaim.go:1-344` (+ `autoclaim_test.go`) | Entire file (Classify/Cascade/CollisionClass/ContentResolver/…) dead — superseded by `hydrate/drift.go`; zero production callers | Delete `autoclaim.go` + its test; gate the 3-tier cascade behind a tracked issue if still planned | **high** |
| D2 | adapter.go + all 4 adapters: `ResolveOutputContent` (adapter.go:174-181; impls in each) | SAFE-04 Tier-2 method implemented 4× but its only consumer (`extract.Cascade`/`ContentResolver`) has no production wiring | Confirm SAFE-04 scope; drop method + 4 impls + dead Cascade chain, or wire a real `ContentResolver` | low |
| D3 | adapter.go:167-172 + 4 impls: `MergeStrategies()` | No production caller — `FileWrite.Merge` is the real source of truth; returns same MergeKind a second time | Drop `MergeStrategies` from interface + 4 impls + tests; derive from `RenderRuntime` if ever needed | low |
| D4 | `synthetic/synthetic.go:113-121,272` | `allowedInSyntheticGates` map never read; kept alive by `_ = …` no-op; duplicates `readOnlyGatesRejectingEnvKey` keys | Delete map + no-op line; allow-set is implicit fallthrough already | low |
| D5 | `render/render.go:247-259` `FormatIdentity` | Exported, dead (test-only caller); `whoami.go:265` carries byte-identical inline `formatIdentityBlock` | Pick one: delete `FormatIdentity`, or finish 06-03 and have `whoami` consume it | low |
| D6 | `config/config.go:71-75` `ErrFileMode` | Exported sentinel, zero references; speculative "reserved for future strict-mode" — no path emits it | Delete; reintroduce in the commit that adds the strict-mode flag (YAGNI) | low |
| D7 | `extract/tar.go:56-60,203` `Result.EntriesParsed` | Written every run, read nowhere (prod/unit/e2e) | Drop field + assignment; keep `entriesSeen` local for the cap check | low |
| D8 | `extract/tar.go:285-291,159` `writeRegular(_ *Result)` | 4th param blank-named, never used; caller already folds results into `res` itself | Remove `_ *Result` param + `&res` arg | low |
| D9 | `hydrate/commit.go:748-755` | Two self-justifying `var _` assertions (`http.MethodPost`, `io.Writer`) keep `net/http`+`io` imports alive for nothing | Delete both `var _` lines + the two imports (keep `io/fs`); pure build no-op | low |
| D10 | `hydrate/commit.go:43,169` `commit.differ` | Field assigned `NewDiffer()` but never read; real compare uses a fresh `NewDiffer()` in `wiring.go:416` — DI seam bypassed | Wire compare through `c.differ`, or delete field + construction and let wiring own `NewDiffer()` | low |
| D11 | `extract/autoclaim.go:96-101` `CascadeOutcome.Error` | Never set/read; self-described speculative future-proofing (folds under D1) | Removed with D1; else drop the field | low |

## Duplication

| # | Location | Problem | Fix | Sev |
|---|----------|---------|-----|-----|
| U1 | all 4 adapters: `copyFile` + `headersWithCredential` (claudecode 328-349/229-233; opencode 398-422/226-230; codex 450/264; gemini 540/252) | Both helpers copy-pasted verbatim ×4 (opencode's `copyFile` adds one `MkdirAll`); single bug-fix surface for write-close/credential discipline | Hoist into `adapter.go` as `CopyFile`/`HeadersWithCredential`; make `CopyFile` always `MkdirAll` (superset) | **medium** |
| U2 | all 4 adapters: `render*`/`RenderRuntime` pipelines (codex 218-296; gemini 200-284; claudecode 172-260; opencode 169-259) | 4 near-identical decode→loop→sort→encode pipelines diverging only by key-prefix + encoder; "+1 adapter = copy 80 lines" trap | Generic `renderMap[E]` driver (entryFn, prefixes, encodeFn) in adapter pkg; per-adapter keeps shape struct + prefixes only | low |
| U3 | all 4 adapters: `Detect()` scaffold (claudecode 92-128; opencode 87-123; codex/gemini) | Identical signal-count→`>=3 High/==2 Med/else Low` switch + zero-signal early return ×4; only the (path,reason) pairs differ | Shared `detectBySignals(root, []signalCheck) Match` in adapter pkg; each Detect = a signal table | low |
| U4 | 3 JSON adapters: `mcpServerEntry`/`a2aAgentEntry` (claudecode 135-150; opencode 130-146; gemini 170-184) | 6 byte-identical `{Type,URL,Headers}` struct decls; `a2aAgentEntry` even mirrors `mcpServerEntry` within each file | One exported `adapter.ServerEntry`; delete the per-adapter pairs | low |
| U5 | codex/gemini/opencode: first-path-component + drop-set (codex 388-389/307-322; gemini 493-505/354; opencode 387-394/306) | 3 divergent impls of "top path component"; 3 drop-set mechanisms (codex's `droppedSet` struct vs siblings' inline maps) | Hoist one `firstPathComponent` + tiny ordered set; delete codex's `droppedSet` in favor of `map[string]struct{}` | low |
| U6 | `hydrate/wiring.go:501-556,937-1001` | 4 decode-modify-encode roundtrips (`mergeForward{JSON,TOML}` / `syncDeep{JSON,TOML}`) repeat the JSON/TOML encoder block verbatim | Add `encodeDoc(m,isTOML)` + shared roundtrip skeleton; extractable piece is the tree-wide indented-JSON encoder | low |
| U7 | `hydrate/wiring.go:1068-1117` `walkEntries` vs `walkEntriesTagged` | Same 5-bucket flatten twice; `walkEntries` has one caller (Sync keep-set) needing only `.Target` | Delete `walkEntries`; Sync loop reads `walkEntriesTagged(...).Entry.Target` | low |
| U8 | `render/render.go:212-242` `FormatEnvDescribe` | Runtime + Context tabwriter blocks = 6 copies of one `KIND\t%s\t%s\t%s` loop body | Extract `writeKindRows(tw, kind, rows)`; feed the 6 (kind, rows, last-field) tuples | low |
| U9 | `devicecode/client.go:224-246` `decodeServerError` | Verbatim copy of `httpclient.decodeServerError`; forced only by the original being unexported | Export `httpclient.DecodeServerError(resp)`; call from devicecode, delete the copy | low |

## Over-Engineering

| # | Location | Problem | Fix | Sev |
|---|----------|---------|-----|-----|
| O1 | adapter.go:40-73 + 4 adapters: `Confidence` enum + `Match.Reasons` | Graded Low/Med/High + Reasons computed everywhere; sole consumer `Autodetect` only checks `Confidence==0` (presence) and never reads Reasons | Collapse Detect to detected-bool/single state; drop `Reasons` unless multi-match error surfaces it | low |
| O2 | `hydrate/result.go:45,91-95` + commit.go:318 `DroppedComponents` | Full pipeline (2 fields + append + warning contract) but no producer or consumer wired; `TransformPlugin` drops never reach it | Keep only if `TransformPlugin` wiring is imminent; else drop fields + append | low |
| O3 | `hydrate/wiring.go:483-495` `mergeForward` return bytes | Returns `([]byte,error)` "for hashing/state"; sole caller discards `_`; hash comes from `freshHash` instead | Change `mergeForward{,JSON,TOML}` to return just `error` | low |
| O4 | `extract/stage.go:62,227` `subtle.ConstantTimeCompare` | Constant-time compare on non-secret content SHA-256 digests (decides only a rename-skip); no timing oracle exists | Replace with `bytes.Equal`; drop `crypto/subtle` import — clearer, no security loss | low |

## Top 5 Highest-Leverage Cleanups

1. **D1 — Delete `extract/autoclaim.go` (343 LOC + test).** Only `high` finding: an entire security-sensitive module reachable only from its own test, superseded by `drift.go`. Biggest single LOC removal, kills a path that can silently drift from the live one.
2. **U1 — Hoist `copyFile` + `headersWithCredential` into `adapter.go`.** Only `medium`: collapses ×4 verbatim copies (plus their ×4 duplicated test pairs) into one bug-fix surface for write-close/credential discipline. Lowest-risk consolidation (shared home already exists).
3. **D2 + D3 — Drop `ResolveOutputContent` and `MergeStrategies` from the Adapter interface (+ 4 impls each).** Two dead interface methods × 4 adapters shrink the adapter contract to what's actually wired; D2 also retires the dead Cascade/ContentResolver chain alongside D1.
4. **U3 + U2 — Shared `detectBySignals` + generic `renderMap` driver.** Removes the two structural "copy ~40–80 lines to add a 5th adapter" traps; each adapter collapses to a signal table + shape struct, eliminating the largest ongoing per-adapter maintenance cost.
5. **O1 — Collapse `Confidence`/`Reasons` to a detected-bool.** Removes computed-but-unconsumed ranking machinery across all 4 adapters; pairs naturally with U3 (the same Detect rewrite), so doing them together is near-free.
