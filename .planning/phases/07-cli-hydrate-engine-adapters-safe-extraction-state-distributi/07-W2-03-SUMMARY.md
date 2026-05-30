---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W2-03
subsystem: cli
tags: [safe-04, auto-claim, collision-cascade, three-tier-lazy, D-17, D-07, exit-7]

# Dependency graph
requires:
  - plan: 07-W1-01
    provides: "exit.CollisionRefuse Code = 7 + *exit.CodedError envelope; consumed by WrapCollisionRefuseError"
  - plan: 07-W1-04
    provides: "internal/cli/hash package — Hash xxh3 contract; informs the comparison-key choice (autoclaim uses crypto/sha256 for the cascade vs xxh3 — see Decisions Made)"
  - plan: 07-W2-01
    provides: "internal/cli/extract package skeleton (doc.go + limits.go + tar.go); autoclaim.go slots in as a new top-level surface alongside Extract"
provides:
  - "extract.Classify(finalPath, *state.File) — three-value collision classifier (None / OwnedByCurrent / ExistsUnowned)"
  - "extract.Cascade(ctx, finalPath, stateEntry, eagerBytes, resolver, sourceFn) — D-17 three-tier lazy byte-compare cascade returning CascadeOutcome{Identical, Tier}"
  - "extract.ContentResolver interface — Tier 2 input shape, satisfied by 07-W3-01 Adapter.ResolveOutputContent (D-07 coupling by interface, not import)"
  - "extract.SourceFn function type — Tier 3 input shape (orchestrator-supplied lazy source.bin read closure)"
  - "extract.CollisionClass enum + CollisionNone / CollisionOwnedByCurrent / CollisionExistsUnowned constants"
  - "extract.WrapCollisionRefuseError(target, tier) — *exit.CodedError builder for orchestrator exit-7 emission"
  - "extract.ErrCascadeNoTier sentinel — orchestrator-bug safety case when all three tiers are nil"
affects:
  - 07-W1-06 (commit.go orchestrator step 8/9 — wires Classify + Cascade at final-rename time)
  - 07-W3-01 (Adapter contract — ResolveOutputContent signature matches ContentResolver)
  - 07-W3-05 (per-adapter orchestrator wiring — supplies SourceFn closures)
  - 07-W4-01 (e2e — exercises auto-claim Identical=true + Identical=false paths)

# Tech tracking
tech-stack:
  added: []  # stdlib only — crypto/sha256, crypto/subtle, context, errors, fmt, os
  patterns:
    - "Three-tier lazy cascade — D-17 ordering: eagerBytes (in-memory) → ContentResolver (adapter-merged) → SourceFn (lazy source). FIRST non-nil tier wins; no fallback retry on mismatch."
    - "D-07 coupling by interface shape, not import: ContentResolver is a single-method interface that 07-W3-01 Adapter.ResolveOutputContent satisfies structurally — the autoclaim package never imports adapter."
    - "Comparison via crypto/sha256 + crypto/subtle.ConstantTimeCompare — chosen over xxh3 because (a) the comparison is one-shot per file at final-rename time (no hot loop), (b) sha256 is forward-compatible with STATE-11 short-circuit logic which keys on canonical content hashes, (c) constant-time compare hardens against side-channel timing leakage on the byte-equality decision."
    - "External _test package (extract_test) on public-API-only consumption — matches the discipline established by W2-01 limits_test + tar_test."
    - "walkAllEntries pointer-slice helper — flattens 5 projection buckets (Prompts / Plugins / Artifacts / RuntimeFiles / Adapter.Files) into one linear scan; pointers (not values) avoid per-entry FileEntry copies."
    - "Iota+1 constant base for CollisionClass — zero-value variables match NO constant, tripping exhaustive switches on uninitialized values rather than silently aliasing to CollisionNone."

key-files:
  created:
    - "internal/cli/extract/autoclaim.go (278 lines) — Classify + Cascade + ContentResolver interface + SourceFn type + CollisionClass enum + CascadeOutcome struct + WrapCollisionRefuseError + ErrCascadeNoTier sentinel + walkAllEntries helper"
    - "internal/cli/extract/autoclaim_test.go (492 lines) — 18 external-package unit tests covering: Classify None / OwnedByCurrent / ExistsUnowned / NilStateFile / Adapter-bucket; Cascade Tier-1 match/differ/empty; Tier-2 match/differ/error-wrap; Tier-3 match/differ/error-wrap; tier-ordering laziness (Tier 1 wins over 2; Tier 2 wins over 3); all-nil ErrCascadeNoTier; missing-finalPath error; WrapCollisionRefuseError exit-code-7 invariant + message format"
  modified: []

key-decisions:
  - "Cascade comparison uses crypto/sha256, NOT internal/cli/hash.Hash (xxh3). The plan's `<behavior>` explicitly says sha256 ('cheap and forward-compatible with the STATE-11 short-circuit logic'). xxh3 is faster but unstable as a content fingerprint across go-runtime versions; sha256 lands on the same hash domain STATE-11 already keys on, so the cascade can borrow STATE-11's short-circuit table without a re-hash step in a future plan. Subtle.ConstantTimeCompare adds defense-in-depth against side-channel inference on the bytes-differ outcome."
  - "Cascade does NOT consult stateEntry — the parameter is passed through for orchestrator bookkeeping (07-W1-06 step 8/9 uses it to populate the state.FileEntry on auto-claim) but plays no role in the byte comparison itself. Documenting this in a code comment and using `_ = stateEntry` to silence linters keeps the signature stable across the wiring surface without inviting a future maintainer to accidentally couple Cascade to state internals."
  - "Iota+1 base for CollisionClass constants — the zero value (0) deliberately matches NO constant. A future caller that declares `var c CollisionClass` and forgets to initialize it will trip a switch's default arm rather than silently aliasing to CollisionNone. This is the same defensive discipline `internal/cli/exit.Code` uses for typed exit codes."
  - "walkAllEntries returns []*state.FileEntry (pointers), not []state.FileEntry (values). FileEntry carries a []string Keys field whose copy would allocate per entry across the 5 projection buckets — pointer-flattening keeps the walker zero-alloc on the common case (only the outer slice header is allocated). Order is Prompts → Plugins → Artifacts → RuntimeFiles → Adapter.Files; documented in the helper as non-load-bearing (Classify stops on first match)."
  - "Tier-ordering laziness is enforced structurally: the if/return chain in Cascade returns immediately after the first non-nil tier's check. Tests `TestCascade_TierOrdering_Tier1WinsOverTier2` and `TestCascade_TierOrdering_Tier2WinsOverTier3` use captured-call sentinels (a bool flipped inside the lower-tier closure) to assert the lower tier was NEVER invoked. This is the only way to test laziness without reaching into package internals."
  - "RED+GREEN collapsed into a single atomic commit (eaa3d52) — same Phase 7 friction as W1-01, W1-02, W2-01, W3-01: the project pre-commit hook runs `make pre-commit` (lint-changed + unit) and CLAUDE.md prohibits `--no-verify`. A strict-TDD RED commit with `extract.Classify` undefined would fail `go vet` and be rejected. TDD discipline preserved procedurally: tests were written first; `./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...` confirmed the expected `undefined: extract.Classify, extract.Cascade, ...` compile failure; THEN the implementation was added; THEN tests went GREEN."

patterns-established:
  - "Three-tier lazy cascade (D-17) — applicable to any future surface that must compare on-disk bytes against expected output where input bytes can arrive from (1) memory (2) deterministic re-derivation (3) lazy fetch. Future inverse-merge (--sync per CLI spec §8.5) will follow the same Tier-1/Tier-2/Tier-3 shape."
  - "Interface-coupling-by-shape between packages — ContentResolver lives in extract; the satisfying Adapter.ResolveOutputContent lives in 07-W3-01 adapter package; neither imports the other. This is the load-bearing pattern for closing inter-phase dependency cycles in Phase 7."
  - "Captured-call sentinel for laziness contract tests — passing a `bool` pointer into a fake closure and asserting it was NEVER set is the canonical way to test 'this tier was not consulted' without exposing call counters in production code."

requirements-completed:
  - SAFE-04

# Note: SAFE-04 is FULLY addressed at the auto-claim cascade layer. The
# orchestrator wiring (Classify+Cascade call sites + --force gate +
# state.FileEntry append on auto-claim) lands in 07-W1-06 commit step 8/9
# and 07-W3-05 per-adapter wiring; the structural cascade machinery is
# complete here.

# Metrics
duration: ~20min
completed: 2026-05-29
---

# Phase 7 Plan 07-W2-03: SAFE-04 Auto-Claim Three-Tier Lazy Cascade Summary

**Shipped `internal/cli/extract/autoclaim.go`, the SAFE-04 auto-claim collision policy: `Classify` maps a final-rename target to one of three CollisionClasses by walking every state.File projection bucket; `Cascade` implements the D-17 three-tier lazy byte comparison (eager → adapter resolver → lazy source) with first-non-nil-tier-wins ordering and constant-time SHA-256 equality; `WrapCollisionRefuseError` produces the `*exit.CodedError{Code: exit.CollisionRefuse}` the orchestrator returns when bytes differ and `--force` is not in effect.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-05-29T15:07:17Z (worktree spawn)
- **Completed:** 2026-05-29T15:27:23Z (Task 1 commit eaa3d52)
- **Tasks:** 1 (`auto`/`tdd=true`)
- **Files created:** 2 (`autoclaim.go` 278 lines + `autoclaim_test.go` 492 lines, total 770 LOC)
- **Files modified:** 0 (sibling W2-02 owns stage/fetch; W2-01 owns tar/limits/doc; W2-03 is purely additive)
- **Tracked commits:** 1 (`eaa3d52`)
- **Tests:** 18 new unit tests passing (`TestClassify_*` × 5, `TestCascade_*` × 12, `TestWrapCollisionRefuseError_*` × 2; one of the Cascade group covers the ErrCascadeNoTier safety case); 25 pre-existing extract tests still green (no regression)
- **Lint:** clean (`./scripts/dev.sh make lint-changed` exit 0)

## Accomplishments

- **`Classify(finalPath, *state.File) (CollisionClass, error)`** — three-value classifier per SAFE-04:
  - File absent → `CollisionNone`
  - File present + referenced by ANY FileEntry across Prompts/Plugins/Artifacts/RuntimeFiles/Adapter.Files → `CollisionOwnedByCurrent`
  - File present + NOT referenced → `CollisionExistsUnowned` (triggers Cascade)
  - `os.Stat` errors other than IsNotExist are wrapped and returned (caller maps to exit 1)
- **`Cascade(ctx, finalPath, stateEntry, eagerBytes, resolver, sourceFn) (CascadeOutcome, error)`** — D-17 three-tier lazy comparison:
  - Reads `finalPath` once as the comparison anchor (sha256 of on-disk bytes)
  - Tier 1: if `eagerBytes != nil` → sha256-compare and return with `Tier=1`. Empty `[]byte{}` is still a valid Tier-1 anchor (distinct from nil).
  - Tier 2: if `resolver != nil` → call `resolver.Resolve(ctx, target)`, sha256-compare, return with `Tier=2`
  - Tier 3: if `sourceFn != nil` → call `sourceFn(ctx, target)`, sha256-compare, return with `Tier=3`
  - All three nil → return `ErrCascadeNoTier` (orchestrator-bug safety case)
  - First non-nil tier WINS; no retry on mismatch. Laziness contract: lower tiers are NEVER consulted when a higher one is supplied.
- **`ContentResolver` interface** — single-method `Resolve(ctx, target) ([]byte, error)`. Matches the 07-W3-01 `Adapter.ResolveOutputContent` signature, enabling D-07 coupling by interface shape without an inter-package import.
- **`SourceFn` function type** — `func(ctx, target) ([]byte, error)`. Caller (orchestrator) supplies a closure that reads `source.bin` from the per-resource staging dir.
- **`CascadeOutcome` struct** — `{Identical bool, Tier int, Error error}`. Tier is set even on mismatch so `--verbose` output stays consistent across both outcomes. `Error` is reserved for batch-outcome use; `Cascade` itself returns errors via the second return value, never embedded.
- **`WrapCollisionRefuseError(target, tier) error`** — builds `*exit.CodedError{Code: exit.CollisionRefuse, Msg: "collision refused at <target> (cascade tier <N>)"}`. The orchestrator returns this when `Identical=false && !force`; `errors.As` in `cmd/ach-cli/main.go` pulls out Code=7 for `os.Exit`.
- **`ErrCascadeNoTier` sentinel** — `errors.New("autoclaim: no tier supplied — orchestrator bug")`. Loud failure mode for an orchestrator wiring bug; never surfaces in normal runs because the orchestrator's contract guarantees ≥1 tier per call.
- **`walkAllEntries(*state.File) []*state.FileEntry`** — pointer-slice helper flattening all 5 projection buckets. Order is Prompts → Plugins → Artifacts → RuntimeFiles → Adapter.Files; documented as non-load-bearing (Classify stops on first match). Pointer return avoids FileEntry copies.

## Task Commits

| Task | Description | Commit | Files |
|------|-------------|--------|-------|
| 1 | CollisionClass + Classify + Cascade three-tier lazy + WrapCollisionRefuseError | `eaa3d52` | `internal/cli/extract/autoclaim.go`, `internal/cli/extract/autoclaim_test.go` |

**Plan metadata commit:** N/A — `.planning/` is gitignored at repo level; the SDK `commit` would return `skipped_gitignored`. Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY.md lives in the main-repo `.planning/` filesystem path (`/home/jcm/Projects/ach/.planning/...`) and is NOT staged or committed from the worktree.

_TDD note: `tdd="true"` honored procedurally — tests were authored before the implementation file, the expected `undefined: extract.Classify, extract.Cascade, ...` compile failure was reproduced via `./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...`, then the implementation was added, then tests went GREEN. The RED and GREEN commits collapsed per the established Phase 7 convention (see W1-01 / W1-02 / W2-01 / W3-01 SUMMARYs)._

## Files Created/Modified

### `internal/cli/extract/autoclaim.go` (created, 278 lines)

SPDX header → package extract → imports `context`, `crypto/sha256`, `crypto/subtle`, `errors`, `fmt`, `os`, `github.com/ackstorm/ach/internal/cli/exit`, `github.com/ackstorm/ach/internal/cli/state`.

Surfaces (in declaration order):
- `CollisionClass int` (typed enum with iota+1 base)
- `CollisionNone` / `CollisionOwnedByCurrent` / `CollisionExistsUnowned` constants with full godoc
- `ContentResolver` interface — single-method `Resolve(ctx, target) ([]byte, error)`
- `SourceFn func(ctx, target) ([]byte, error)` type
- `CascadeOutcome struct {Identical bool; Tier int; Error error}`
- `ErrCascadeNoTier` sentinel
- `Classify(finalPath, *state.File) (CollisionClass, error)`
- `walkAllEntries(*state.File) []*state.FileEntry` (unexported helper)
- `Cascade(ctx, finalPath, stateEntry, eagerBytes, resolver, sourceFn) (CascadeOutcome, error)` — if/return chain, one branch per tier, ErrCascadeNoTier fall-through
- `WrapCollisionRefuseError(target, tier) error` — *exit.CodedError builder

Every type, function, and constant carries a godoc comment citing the originating REQ-ID (SAFE-04), spec section (CLI spec §6.4), and applicable PRD decision (D-07 / D-17).

### `internal/cli/extract/autoclaim_test.go` (created, 492 lines)

SPDX header → package `extract_test` (external; public-API-only consumption per W2-01 discipline).

Inline test fakes (defined in this file, no testify):
- `fakeResolver struct {result []byte; err error}` with `Resolve(ctx, target)` method
- `fakeResolverCaptured struct {result []byte; err error; called *bool}` with `Resolve` flipping the pointer — used for laziness-ordering tests
- `fakeSourceFn(result, err) extract.SourceFn` returns a closure
- `contains(haystack, needle) bool` — stdlib-only substring check for message-format assertions

Test groups (18 tests total):
- **Classify** (5 tests): `_NoFile_None`, `_FileInState_OwnedByCurrent` (Prompts bucket), `_FileNotInState_ExistsUnowned`, `_NilStateFile_ExistsUnowned`, `_AdapterFile_OwnedByCurrent` (Adapter.Files bucket — exercises the otherwise-unreached arm of walkAllEntries)
- **Cascade Tier 1** (3 tests): `_Tier1_Match_Identical`, `_Tier1_Differ_NotIdentical`, `_Tier1_EmptyBytes_AnchorsTier1` (empty `[]byte{}` is a valid anchor, NOT nil — laziness contract edge case)
- **Cascade Tier 2** (3 tests): `_Tier2_ResolverInvoked` (match), `_Tier2_ResolverDiffers_NotIdentical`, `_Tier2_ResolverError_Wrapped` (errors.Is unwraps the sentinel through the fmt.Errorf wrap chain)
- **Cascade Tier 3** (3 tests): `_Tier3_SourceFnInvoked` (match), `_Tier3_SourceFnDiffers_NotIdentical`, `_Tier3_SourceFnError_Wrapped`
- **Cascade ordering** (2 tests): `_TierOrdering_Tier1WinsOverTier2` (resolver NEVER called when eagerBytes supplied), `_TierOrdering_Tier2WinsOverTier3` (sourceFn NEVER called when resolver supplied)
- **Cascade error paths** (2 tests): `_AllNil_ReturnsError` (ErrCascadeNoTier via errors.Is), `_FinalPathMissing_Errors`
- **WrapCollisionRefuseError** (2 tests): `_HasExitCode7` (errors.As → *exit.CodedError → Code == exit.CollisionRefuse AND == 7 literally — the literal-7 assertion is the regression gate against accidental exit-code renumber), `_MsgCitesTargetAndTier` (loose contains-check on path + tier digits)

## Decisions Made

See `key-decisions` in frontmatter. Summary:

- **sha256 not xxh3 for Cascade compares** — plan's `<behavior>` explicitly says crypto/sha256, AND it lands on the same hash domain STATE-11 short-circuit logic will key on. xxh3 is faster but the cascade is one-shot per file at final-rename time (no hot loop), so the speed delta is irrelevant. `crypto/subtle.ConstantTimeCompare` adds side-channel hardening on the bytes-differ outcome.
- **stateEntry passed through but unused inside Cascade** — keeps the signature stable for the 07-W1-06 orchestrator wiring point, where the caller will use stateEntry to populate the auto-claimed state.FileEntry. Documented with `_ = stateEntry` to silence the unused-arg lint.
- **iota+1 base for CollisionClass** — zero value matches no constant, so an uninitialized variable trips an exhaustive switch rather than silently aliasing to CollisionNone.
- **walkAllEntries returns pointers** — FileEntry contains a []string Keys slice; pointer-flattening keeps the helper zero-alloc beyond the outer slice header.
- **Laziness contract is structurally enforced** — if/return chain in Cascade returns on the first non-nil tier; tests use captured-call bool pointers to assert lower tiers are NEVER invoked.
- **RED+GREEN collapsed** — same Phase 7 friction (pre-commit hook runs `go vet`; CLAUDE.md forbids `--no-verify`).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Workflow] TDD RED+GREEN collapsed into a single atomic commit per Phase 7 convention**

- **Found during:** Task 1 commit step
- **Issue:** The plan's `tdd="true"` attribute combined with the executor system prompt's `<tdd_execution>` step mandates separate RED (`test(...)`) and GREEN (`feat(...)`) commits. The project's `pre-commit` hook runs `make pre-commit` = `lint-changed + unit`; a failing-to-compile RED commit (test files referencing undefined `extract.Classify`, `extract.Cascade`, etc.) trips `go vet` and the commit is rejected. CLAUDE.md explicitly forbids `--no-verify`. Same pattern as 07-W1-01, 07-W1-02, 07-W2-01, 07-W3-01 SUMMARYs.
- **Fix:** Collapsed RED + GREEN into a single atomic `feat(...)` commit. TDD discipline preserved procedurally: the test file was authored first; `./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...` confirmed the expected `undefined: extract.Classify, extract.Cascade, extract.ContentResolver, extract.SourceFn, extract.CollisionClass, ...` build failure; THEN the implementation file was authored; THEN unit tests went 18-of-18 green.
- **Files modified:** None (workflow change, not a code change).
- **Verification:** `./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/... → exit 0`; 18 new tests pass alongside the 25 pre-existing extract tests; `./scripts/dev.sh make lint-changed → exit 0`.
- **Committed in:** `eaa3d52` (the atomic Task 1 commit).

**2. [Rule 3 - Workflow] Pre-existing concurrent-singleflight test flakies blocked the first two commit attempts**

- **Found during:** Task 1 first + second commit attempts
- **Issue:** The pre-commit hook runs `make unit` over the entire repo. First commit attempt: `TestGet_Singleflight_DedupesConcurrentMisses` (`internal/contentservice/envcache`) AND `TestCachedResolverSingleFlight` (`internal/keystore`) failed under the parallel race-test loadout. Second attempt: `TestRedisCachedTeamsResolver_SingleFlight` failed. All three are timing-sensitive singleflight dedup assertions in packages this plan does NOT touch. Reproduced on the clean base via `git stash && ./scripts/dev.sh make unit` (failures occurred BEFORE my changes were staged), confirming pre-existing flakiness. Same pattern documented in W1-02 SUMMARY ("Pre-existing flake in `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` blocked the first commit") and W3-01 SUMMARY ("Re-attempt landed clean").
- **Fix:** Per the SCOPE BOUNDARY rule ("only auto-fix issues DIRECTLY caused by the current task's changes; pre-existing warnings, linting errors, or failures in unrelated files are out of scope"), did NOT modify the flaky tests or their source. Re-attempted the commit; third attempt landed clean. The flakes are logged here for verifier visibility — fixing them is a separate work item that should target the singleflight dedup goroutine-scheduling races.
- **Files modified:** None (workflow retry, not a code/test change).
- **Verification:** Third commit attempt exit 0; final `make unit` run inside the hook printed `OK unit tests passed` and the commit landed (`eaa3d52`).
- **Committed in:** N/A — workflow retry, not a file change.

---

**Total deviations:** 2 (both Rule 3 — pre-existing workflow friction documented in prior Phase 7 SUMMARYs)
**Impact on plan:** Plan intent delivered verbatim. No scope creep, no contract changes. The pre-existing flakes are NOT a regression introduced by this plan (reproduced on the clean base before my changes were staged).

## Issues Encountered

- **Pre-existing concurrent-singleflight test flakies under parallel `make unit`** — three different tests (`TestGet_Singleflight_DedupesConcurrentMisses`, `TestCachedResolverSingleFlight`, `TestRedisCachedTeamsResolver_SingleFlight`) tripped across three commit attempts. All are in unrelated packages (`internal/contentservice/envcache`, `internal/keystore`). Documented in W1-02 SUMMARY as pre-existing. Worth a dedicated future plan to harden the singleflight dedup assertions (sync barriers, deterministic scheduling, or removing the assert entirely).
- **`make lint-changed` skips brand-new files (untracked relative to origin/main)** — the target diffs `--name-only` against `origin/main`, so packages that did not previously exist are partially invisible. autoclaim.go and autoclaim_test.go live INSIDE an existing package (`internal/cli/extract/...`) so the package-level lint did pick them up — confirmed by checking the output included `./internal/cli/extract/...`. No workaround needed for this plan.
- **`.planning/` is gitignored** — the SUMMARY.md lives in the main-repo `.planning/` filesystem path and survives the worktree teardown. No `docs(...)` follow-up commit possible without `-f`-style force-stage (forbidden).

## User Setup Required

None. All changes are repo-internal Go code. No external services, no secrets, no schema migrations, no CRD changes.

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/extract/autoclaim.go ]            → FOUND
[ -f internal/cli/extract/autoclaim_test.go ]       → FOUND

# Sibling W2-02 / W2-01 files untouched
git diff --name-only HEAD~1 HEAD | sort
  → internal/cli/extract/autoclaim.go
  → internal/cli/extract/autoclaim_test.go
  (no stage.go / fetch.go / tar.go / limits.go / doc.go — clean scope)

# Commit existence
git log --oneline | grep eaa3d52  → FOUND ("feat(07-W2-03): add SAFE-04 auto-claim three-tier lazy cascade")

# Plan acceptance-criteria gates
grep -q "type CollisionClass" internal/cli/extract/autoclaim.go                              → OK
grep -cE "^\s+(CollisionNone|CollisionOwnedByCurrent|CollisionExistsUnowned)" internal/cli/extract/autoclaim.go  → 3
grep -q "type ContentResolver interface" internal/cli/extract/autoclaim.go                   → OK
grep -q "type SourceFn func" internal/cli/extract/autoclaim.go                               → OK
grep -q "func Classify" internal/cli/extract/autoclaim.go                                    → OK
grep -q "func Cascade" internal/cli/extract/autoclaim.go                                     → OK
grep -q "exit.CollisionRefuse" internal/cli/extract/autoclaim.go                             → OK
grep -E "crypto/sha256|crypto/subtle" internal/cli/extract/autoclaim.go                      → both imports present
grep -cE "TestCascade_Tier1|TestCascade_Tier2|TestCascade_Tier3|TestClassify.*Owned|TestWrapCollisionRefuseError" internal/cli/extract/autoclaim_test.go → 13
head -1 internal/cli/extract/autoclaim.go                                                    → "// SPDX-License-Identifier: Apache-2.0"
head -1 internal/cli/extract/autoclaim_test.go                                               → "// SPDX-License-Identifier: Apache-2.0"

# Behavior gates
./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...  → exit 0 (18 new + 25 existing = 43 tests pass)
./scripts/dev.sh make lint-changed                              → exit 0 (clean)
```

## Self-Check: PASSED

## Next Phase Readiness

- **07-W1-06 (commit.go orchestrator step 8/9) unblocked** — can now `import "github.com/ackstorm/ach/internal/cli/extract"` and call `extract.Classify(finalPath, stateFile)` then `extract.Cascade(ctx, finalPath, stateEntry, eagerBytes, resolver, sourceFn)` at final-rename time. On `Identical=true` → auto-claim into state; on `Identical=false && !force` → return `extract.WrapCollisionRefuseError(target, tier)` for exit-7 emission.
- **07-W3-01 (Adapter contract) unblocked** — the `ContentResolver` interface in autoclaim.go is the load-bearing shape `Adapter.ResolveOutputContent` must satisfy. D-07 coupling closes structurally without a cross-package import.
- **07-W3-05 (per-adapter orchestrator wiring) unblocked** — the wiring can construct `SourceFn` closures around the per-resource staging-dir path and pass them to `extract.Cascade` from the commit-step loop.
- **07-W4-01 (e2e) unblocked** — the e2e suite can exercise Identical=true / Identical=false / --force / all three tiers via the same public Cascade entry point.

No blockers for downstream waves. The `internal/cli/extract` surface is closed at autoclaim.go: Classify and Cascade are the only public auto-claim entry points, and the SAFE-04 three-tier discipline is structurally enforced (no exposed knob that could weaken the policy by accident).

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
