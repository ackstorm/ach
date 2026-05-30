---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W5-06
subsystem: cli-state-parser
tags: [WR-03, STATE-02, exit-code-contract, recovery-path, refactor, tests]
requires: [internal/cli/state/state.go, internal/cli/state/state_test.go]
provides: [two-phase state.Load — schemaVersion gate before DisallowUnknownFields decode]
affects: [internal/cli/hydrate/commit.go (caller — no code change; behavior unchanged on happy path, --force recovery now reaches v1 files)]
tech-stack:
  added: []
  patterns: [two-phase parse — best-effort schemaVersion gate before strict-decode]
key-files:
  created: []
  modified:
    - internal/cli/state/state.go
    - internal/cli/state/state_test.go
decisions:
  - "WR-03 closure: best-effort json.Unmarshal of `struct{ SchemaVersion string }` runs BEFORE dec.DisallowUnknownFields(). Non-\"2\" → ErrSchemaMismatch (exit 5, --force overrides at caller). v2-with-unknown-field still → ErrStateParse (exit 1, no --force escape — correctness-preserving)."
  - "Final post-strict-decode schemaVersion check preserved verbatim — belt-and-braces against phase-1 false negative when JSON is malformed outside the schemaVersion field."
  - "No new sentinels introduced; ErrSchemaMismatch / ErrStateParse / ErrInvalidPath surface preserved. commit.go:step3ReadState requires no change — its existing errors.Is(err, state.ErrSchemaMismatch) gate now fires for v1 files."
metrics:
  duration: ~2.5 min
  completed: 2026-05-29
  tasks: 2
  files_modified: 2
  files_created: 0
  lines_added_approx: 95
  lines_removed_approx: 13
---

# Phase 07 Plan 07-W5-06: state.Load two-phase parse (WR-03 closure) Summary

Reordered `internal/cli/state/state.go:Load` so a best-effort schemaVersion gate runs BEFORE the strict `DisallowUnknownFields` decode. A v1 state.json carrying the removed `contentHashes` field now returns `ErrSchemaMismatch` (exit 5, `--force` overrides at the caller) instead of `ErrStateParse` (exit 1, no `--force` escape). Two new unit tests pin both arms of the new ordering against future regression.

## Objective Recap

WR-03 in 07-REVIEW.md flagged that the CLAUDE.md "schemaVersion != \"2\"" failure-mode entry (added by 07-W4-02) advertised `--force` as the recovery path for a stale on-disk state file. The advertisement was a lie for any v1 file with legacy fields: `dec.DisallowUnknownFields()` ran first, the `contentHashes` field tripped it, and the caller saw `ErrStateParse` — which `commit.go:step3ReadState` does NOT bypass with `--force` (only `ErrSchemaMismatch` is gated). The user ended up in a state with no documented recovery path beyond manually deleting `<ach-dir>/state.json`.

The fix is a small reordering: a best-effort `json.Unmarshal` into a `struct{ SchemaVersion string }` checks the version BEFORE the strict decode runs. Non-`"2"` short-circuits to `ErrSchemaMismatch` (exit 5, `--force` overrides). v2 files continue to flow through the strict decode unchanged — an unknown field in a current-version state.json is still a `ErrStateParse` (exit 1, no `--force` escape) because that arm is a bug, not a user-recoverable migration.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Reorder Load to check schemaVersion before DisallowUnknownFields | `81dae0a` | `internal/cli/state/state.go` |
| 2 | Add unit tests for the new ordering | `154ebaf` | `internal/cli/state/state_test.go` |

## Implementation Notes

**Task 1 — Two-phase parse in `Load`:**

Inserted a phase-1 gate immediately after `os.ReadFile` and before `json.NewDecoder`:

```go
var sv struct {
    SchemaVersion string `json:"schemaVersion"`
}
_ = json.Unmarshal(raw, &sv)
if sv.SchemaVersion != "" && sv.SchemaVersion != "2" {
    return nil, fmt.Errorf("%w: got %q, want \"2\"", ErrSchemaMismatch, sv.SchemaVersion)
}
```

The Unmarshal error is intentionally ignored — phase 2's strict decode is the authoritative parse, and we only need a probable schemaVersion value here to make the right routing decision. The phase-2 strict-decode block is preserved verbatim, including the final `f.SchemaVersion != "2"` check (belt-and-braces against a phase-1 false negative where the JSON is malformed outside the schemaVersion field and the best-effort Unmarshal leaves sv zero-valued).

The `Load` godoc was rewritten to document the two-phase ordering, explaining the load-bearing user-facing contract: phase 1 is the `--force` recovery branch for legacy on-disk files; phase 2 is the corruption / forward-compat-drift branch with no `--force` escape (correctness-preserving).

**Task 2 — Two new unit tests:**

- `TestLoad_V1FileReturnsErrSchemaMismatch`: writes `{"schemaVersion":"1","environment":"demo","contentHashes":{"foo":"bar"}}` to a t.TempDir state.json, calls `state.Load`, asserts `errors.Is(err, state.ErrSchemaMismatch)` AND `!errors.Is(err, state.ErrStateParse)` AND `f == nil`. Comment references WR-03 and the caller-side `--force` recovery contract in `commit.go:step3ReadState`.
- `TestLoad_V2FileWithUnknownFieldReturnsErrStateParse`: writes `{"schemaVersion":"2","environment":"demo","futureField":"x"}`, asserts `errors.Is(err, state.ErrStateParse)` AND `!errors.Is(err, state.ErrSchemaMismatch)` AND `f == nil`. Comment documents that v2-with-unknown is NOT user-recoverable; `--force` has no special handling for this arm.

Existing tests (`TestLoad_AbsentFile_ReturnsNilNil`, `TestLoad_SchemaV1_ReturnsErrSchemaMismatch`, `TestLoad_SchemaV2_RoundTrip`, `TestLoad_UnknownField_ReturnsErrStateParse`, `TestLoad_CorruptJSON_ReturnsErrStateParse`) all continue to pass — the reorder does not break the happy path nor the legitimate corruption arm.

Total: 29 tests in the state package after the addition (27 → 29), all green under `-race`.

## Verification

| Check | Result |
|-------|--------|
| `./scripts/dev.sh make unit-pkg PKG=./internal/cli/state/...` | PASS (29 tests, including 2 new) |
| `./scripts/dev.sh go vet ./internal/cli/state/...` | PASS |
| `./scripts/dev.sh go build ./...` | PASS (no errors across whole tree) |
| `./scripts/dev.sh make lint-changed` | PASS (clean) |
| Pre-commit hook (lint-changed + make unit) | PASS on both commits |

The pre-commit hook ran the FULL `make unit` sweep (not just the state package) on each commit and exited zero — confirming the reorder does not regress any caller (manifest, hydrate, adapter, exit packages all clean).

## Deviations from Plan

None — plan executed exactly as written. Both tasks landed in clean atomic commits with the per-task `refactor(...)` / `test(...)` conventional-commit prefixes, no `--no-verify` bypass, pre-commit hooks green on both commits.

The plan called out that `commit.go:step3ReadState` requires no change — confirmed during read_first; the existing `errors.Is(err, state.ErrSchemaMismatch)` gate now fires for v1 files as intended.

## Integration Verification (documented for reviewer)

After this plan lands AND W5-01 lands (CR-02 wiring; ALREADY LANDED in commit 22 from earlier wave), the `--force` recovery path for a v1 state.json file works end-to-end:

```
$ ach-cli hydrate --environment demo
fatal: state.json schema mismatch: state: schemaVersion != "2": got "1", want "2" (use --force to overwrite)
exit status 5

$ ach-cli hydrate --environment demo --force
warning: --force bypassing state.json schemaVersion mismatch (state: schemaVersion != "2": got "1", want "2"); state will be rewritten on commit
... (hydrate proceeds, state.json rewritten to v2 shape on Save)
```

This is the contract the CLAUDE.md "schemaVersion != \"2\"" failure-mode entry (W4-02) advertises. The advertisement is no longer a promise the code doesn't keep.

## Self-Check: PASSED

Files modified exist:
- `/home/jcm/Projects/ach/internal/cli/state/state.go` — phase-1 schemaVersion gate present (line 134, `var sv struct {`; line 137, `json.Unmarshal(raw, &sv)`; line 138, `sv.SchemaVersion != "" && sv.SchemaVersion != "2"`); strict decode preserved (line 149, `dec.DisallowUnknownFields()`); two-phase godoc present (lines 86-122).
- `/home/jcm/Projects/ach/internal/cli/state/state_test.go` — both new tests present (line 117, `TestLoad_V1FileReturnsErrSchemaMismatch`; line 147, `TestLoad_V2FileWithUnknownFieldReturnsErrStateParse`).

Commits exist:
- `81dae0a` refactor(07-W5-06): reorder state.Load to check schemaVersion before strict decode
- `154ebaf` test(07-W5-06): pin two-phase state.Load ordering (v1→ErrSchemaMismatch, v2+unknown→ErrStateParse)

Acceptance criteria from PLAN.md all satisfied:
- ✓ v1 file → `ErrSchemaMismatch` (NOT `ErrStateParse`)
- ✓ v2 file with unknown field → `ErrStateParse` (NOT `ErrSchemaMismatch`)
- ✓ `./scripts/dev.sh make unit-pkg PKG=./internal/cli/state/...` exits 0
- ✓ `./scripts/dev.sh go build ./...` exits 0
- ✓ Two new tests in `state_test.go`
- ✓ `Load` godoc rewritten to document two-phase ordering
- ✓ No change to `commit.go:step3ReadState`
- ✓ No new sentinel errors introduced
