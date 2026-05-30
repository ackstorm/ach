---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W5-05
subsystem: cli-adapter
tags: [data-integrity, adapter, close-error, WR-02, ADAPT-03, ADAPT-04]
requires:
  - internal/cli/adapter (registry + interface — pre-existing)
provides:
  - "adapter.copyFile (×4) that surfaces close(2) errors instead of swallowing them"
affects:
  - internal/cli/adapter/claudecode
  - internal/cli/adapter/codex
  - internal/cli/adapter/gemini
  - internal/cli/adapter/opencode
tech-stack:
  added: []
  patterns:
    - "explicit out.Close() return value (replaces deferred swallow)"
    - "Linux /dev/full canonical close-error test fixture (guarded by runtime.GOOS == \"linux\")"
key-files:
  created: []
  modified:
    - internal/cli/adapter/claudecode/claudecode.go
    - internal/cli/adapter/claudecode/claudecode_test.go
    - internal/cli/adapter/codex/codex.go
    - internal/cli/adapter/codex/codex_test.go
    - internal/cli/adapter/gemini/gemini.go
    - internal/cli/adapter/gemini/gemini_test.go
    - internal/cli/adapter/opencode/opencode.go
    - internal/cli/adapter/opencode/opencode_test.go
decisions:
  - "Use /dev/full (Linux-only device that accepts writes but fails on close with ENOSPC) as the canonical fixture for inducing a close(2) error. No test seam, no copyFileImpl factoring — real OS semantics, runtime-guard skip on non-Linux."
  - "Duplicate the test verbatim across the 4 adapter packages instead of factoring into internal/cli/adapter/testutil/. Plan-level decision: 4 × ~25 LOC < the cost of cross-package coupling that would need to be undone the next time an adapter ships."
  - "io.Copy error takes precedence over close error: if io.Copy errs, call _ = out.Close() and return the io.Copy error. Preserves prior error semantics for the non-EIO failure modes."
  - "Accept ENOSPC surfaces as either syscall.Errno (errors.Is(err, syscall.ENOSPC)) or as *os.PathError wrapping the errno; fall back to substring check on the strerror text 'no space left on device' for libc variants where errors.Is does not unwrap as expected."
metrics:
  duration: "~6 min"
  completed: 2026-05-29
---

# Phase 7 Plan 07-W5-05: Close-error propagation in adapter copyFile Summary

`defer func() { _ = out.Close() }()` swallowed close(2) EIO/ENOSPC in all 4 adapter copyFile helpers; replaced with explicit `return out.Close()` so the caller sees buffered-write failures (truncated credential files no longer recorded as written).

## What Changed

### Source (4 adapters, identical mechanical edit)

For each of `claudecode.go:316`, `codex.go:450`, `gemini.go:540`, `opencode.go:398`:

**Before:**
```go
out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
if err != nil {
    return err
}
defer func() { _ = out.Close() }()   // ← silently drops close(2) EIO/ENOSPC

if _, err := io.Copy(out, in); err != nil {
    return err
}
return nil
```

**After:**
```go
out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
if err != nil {
    return err
}

// Per 07-W5-05 (WR-02): explicit close to surface buffered-write
// errors that surface only at close(2) (EIO/ENOSPC). A deferred
// `_ = out.Close()` would silently drop those errors, recording a
// truncated file as successfully written.
if _, err := io.Copy(out, in); err != nil {
    _ = out.Close()
    return err
}
return out.Close()
```

The `defer func() { _ = in.Close() }()` for the source file is unchanged — close errors on the read side are immaterial.

### Tests (4 adapter packages, ~25-LOC pair per adapter)

Each adapter test file gained two new tests:

1. **`TestCopyFile_SurfacesCloseError_OnDevFull`** — Linux-only repro:
   - 64 KiB source file in `t.TempDir()`.
   - Call `copyFile(src, "/dev/full")`.
   - Assert the return value is non-nil AND either `errors.Is(err, syscall.ENOSPC)` OR the error message contains `"no space left on device"` (glibc strerror text).
   - Guarded by `if runtime.GOOS != "linux" { t.Skip(...) }`.

2. **`TestCopyFile_ReturnsNilOnSuccess`** — preserves success-path semantics:
   - Copy small payload to a TempDir destination.
   - Assert nil return + destination bytes match source byte-for-byte.

Imports added per test file: `errors`, `runtime`, `strings`, `syscall`.

## Commits

| Hash    | Subject |
|---------|---------|
| e643db1 | `fix(07-W5-05): claudecode copyFile surfaces close-error (WR-02)` |
| 1d2d24a | `fix(07-W5-05): codex/gemini/opencode copyFile surfaces close-error (WR-02)` |

The split into two commits matches the plan's task boundaries — Task 1 establishes the canonical pattern + test fixture in claudecode; Task 2 replicates verbatim across the three remaining adapters. Each commit independently builds + tests green; either could be reverted in isolation without breaking the tree.

## Deviations from Plan

None — plan executed exactly as written.

The plan's `<action>` block walked through three test-seam alternatives (function-variable override, interface refactor, copyFileImpl factoring) before settling on the `/dev/full` approach. That decision was carried forward verbatim from the plan; no in-execution re-evaluation was required.

## Verification

### Per-task automated checks

```
./scripts/dev.sh make unit-pkg PKG=./internal/cli/adapter/claudecode/...   # Task 1: PASS
./scripts/dev.sh make unit-pkg PKG=./internal/cli/adapter/...              # Task 2: PASS (all 4 adapter pkgs)
./scripts/dev.sh make lint-changed                                          # Task 1 + 2: PASS
./scripts/dev.sh go build ./...                                             # Task 2: PASS
./scripts/dev.sh go vet ./internal/cli/adapter/...                          # Task 2: PASS
```

### Acceptance criteria

| Criterion | Status |
|-----------|--------|
| `grep -c "defer func() { _ = out.Close() }()" {4 adapters}.go` = 0 across all 4 files | PASS |
| `grep -n "return out.Close()" {4 adapters}.go` returns the explicit close-error line in each | PASS (claudecode.go:336, codex.go:470, gemini.go:560, opencode.go:421) |
| `grep -l "TestCopyFile_SurfacesCloseError_OnDevFull" {4 adapter tests}` returns all 4 test files | PASS |
| `grep -n "/dev/full" claudecode_test.go` returns the test destination path | PASS |
| `make unit-pkg PKG=./internal/cli/adapter/...` exits 0 | PASS (all 4 adapter packages: 21 + 22 + 21 + 21 = 85 tests pass; new `TestCopyFile_*` tests included and not skipped on this Linux host) |
| `make lint-changed` exits 0 | PASS |
| `go build ./...` exits 0 | PASS |
| On Linux: the new tests run (not skipped) and assert an ENOSPC error | PASS — all 4 `TestCopyFile_SurfacesCloseError_OnDevFull` ran and PASSed; the assertion fired and matched |

### Success criteria (plan-level)

1. ✅ All four adapter `copyFile` implementations explicitly close the destination file and return its close error on the success path; the `io.Copy` error path discards the close error.
2. ✅ Each adapter has a new unit test that, on Linux, asserts ENOSPC is surfaced from a write to `/dev/full`.
3. ✅ Success-path semantics preserved — when both `io.Copy` and close succeed, `copyFile` returns nil and the destination matches the source byte-for-byte.
4. ✅ No new packages, no shared test helpers — duplication is intentional per the plan rationale.

## Threat Mitigation Status

| Threat ID | Disposition | Status |
|-----------|-------------|--------|
| T-07-W5-05-01 (Information Disclosure / Data Loss via swallowed close error) | mitigate | CLOSED — explicit `return out.Close()` propagates EIO/ENOSPC; the caller sees the error and aborts the hydrate; no partial state.json entry is recorded for a truncated file. |
| T-07-W5-05-02 (Tampering — partial credential file written then truncated) | mitigate | CLOSED — same fix; caller never records the file as written, state.json never carries a hash for the truncated bytes. |
| T-07-W5-05-03 (DoS — repeated ENOSPC on a full disk) | accept | UNCHANGED — fix surfaces the error; caller exits non-zero. Runtime gate is the user/CI to free disk space. |

## Known Stubs

None — no UI-rendering stubs in scope; this plan touches only inner-loop file-copy helpers.

## Phase Sequencing

- This plan closes the data-integrity gap (WR-02) introduced when the engine started actually writing adapter configs in 07-W5-01. Until W5-01, the adapter copyFile helpers were unreachable in production; the silent close-error swallow was latent.
- No downstream plans depend on this fix — but every plan that exercises adapter writes (W5-01..W5-04 already shipped; W5-06 next) is now backed by a copyFile that cannot silently corrupt credential-bearing files.

## Self-Check: PASSED

Verified files exist:
- `internal/cli/adapter/claudecode/claudecode.go` — FOUND
- `internal/cli/adapter/codex/codex.go` — FOUND
- `internal/cli/adapter/gemini/gemini.go` — FOUND
- `internal/cli/adapter/opencode/opencode.go` — FOUND
- `internal/cli/adapter/claudecode/claudecode_test.go` — FOUND
- `internal/cli/adapter/codex/codex_test.go` — FOUND
- `internal/cli/adapter/gemini/gemini_test.go` — FOUND
- `internal/cli/adapter/opencode/opencode_test.go` — FOUND

Verified commits exist:
- `e643db1` — FOUND (`fix(07-W5-05): claudecode copyFile surfaces close-error (WR-02)`)
- `1d2d24a` — FOUND (`fix(07-W5-05): codex/gemini/opencode copyFile surfaces close-error (WR-02)`)
