---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W5-02
subsystem: cli
tags: [security, file-permissions, credentials, atomic-write, posix-mode, multi-user-host]

# Dependency graph
requires:
  - phase: 07
    provides: state.WriteAtomic publication primitive (STATE-07), adapterDispatcherImpl.Render + Sync inverse-merge sites (07-W3-05 wiring), state.Save (STATE-01)
provides:
  - "WriteAtomic accepts a required os.FileMode mode parameter — per-caller mode policy enforced at the type system"
  - "Adapter runtime-config files (.claude/.mcp.json, .codex/config.toml, .gemini/settings.json, .opencode/opencode.json) written at 0o600 — credential bearer tokens no longer world-readable on multi-user hosts"
  - "state.Save remains the SOLE legitimate 0o644 WriteAtomic caller in the tree (state.json has no secrets per spec §8.2)"
  - "TestWriteAtomic_Mode0600_HonoredOnCredentialFile asserts the 0o600 contract end-to-end (os.Stat assertion)"
affects: [07-W5-03 runtime-mode gate, future credential-bearing writers, multi-user-host operators]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-caller file-mode policy enforced by required signature arg (no default + variant footgun)"
    - "Credential-bearing writes routed through the same atomic primitive as non-secret writes, distinguished only by mode at the call site"

key-files:
  created: []
  modified:
    - internal/cli/state/atomic.go
    - internal/cli/state/state.go
    - internal/cli/state/atomic_test.go
    - internal/cli/hydrate/wiring.go

key-decisions:
  - "Required-mode signature WriteAtomic(path, data, mode os.FileMode) over sibling WriteAtomicWithMode — the compiler refuses to compile a credential-bearing caller that omits the mode arg, closing the silent-regression footgun (T-07-W5-02-05)"
  - "Tasks 1 + 2 folded into a single atomic commit because they form one inseparable security fix: Task 1's signature change breaks the build until Task 2 updates the 4 wiring.go callers — splitting would leave HEAD non-building and break git bisect (Rule 3 deviation: scope of atomic commit follows the build-graph dependency, not the plan task boundary)"
  - "Comment placed at each of the 4 wiring.go 0o600 sites referencing CR-01 + 07-W5-02 + T-07-W5-02-01 so future auditors see the security rationale at the call site, not buried in a downstream summary"

patterns-established:
  - "Pattern: Required-mode publication primitive — when an atomic-write helper is shared between credential-bearing and non-secret callers, the signature MUST take mode as a required arg so the compiler enforces per-caller policy"
  - "Pattern: TOCTOU-free mode publication — Chmod(mode) runs on the temp file BEFORE rename(2); by the instant rename completes the final path is already at the target mode, closing the rename→first-open race window (T-07-W5-02-02)"

requirements-completed: [STATE-07, ADAPT-03]

# Metrics
duration: 4min
completed: 2026-05-29
---

# Phase 07 Plan W5-02: WriteAtomic Required-Mode + Adapter Configs at 0o600 Summary

**WriteAtomic gains a required os.FileMode arg; all four adapter-output writes in wiring.go publish at 0o600 (refuse world-readable for credential-bearing runtime configs per CR-01); state.json continues at 0o644 (no secrets per spec §8.2).**

## Performance

- **Duration:** 4 min
- **Started:** 2026-05-29T18:34:41Z
- **Completed:** 2026-05-29T18:38:41Z
- **Tasks:** 2 (folded into 1 atomic commit — see Deviations)
- **Files modified:** 4

## Accomplishments

- `WriteAtomic(path, data, mode os.FileMode) error` — mode is required at the type system; a future credential-bearing caller cannot silently regress to 0o644 by omitting the arg.
- `internal/cli/hydrate/wiring.go`: all four credential-bearing WriteAtomic call sites pass 0o600 (Render at line 230 + syncComposite at 459 + syncDeepJSON at 517 + syncDeepTOML at 548). Each carries a CR-01 + 07-W5-02 rationale comment.
- `internal/cli/state/state.go`: `Save` passes 0o644 explicitly and is documented as the sole legitimate 0o644 caller (state.json has no secrets per spec §8.2).
- `internal/cli/state/atomic_test.go`: `TestWriteAtomic_Mode0600_HonoredOnCredentialFile` proves the mode arg is honored end-to-end via `os.Stat(path).Mode().Perm() == 0o600` (Windows + root skip). Every other existing test updated to pass 0o644 through the new signature; behavior preserved.
- Doc comment on `WriteAtomic` documents the per-caller-policy posture, the CR-01 rationale, and the TOCTOU-free Chmod-before-rename ordering.

## Task Commits

Tasks 1 + 2 folded into one atomic commit because Task 1's signature change breaks the build at HEAD until Task 2 updates all four callers in `internal/cli/hydrate/wiring.go`. Splitting would leave the first commit non-building and break `git bisect`. See Deviations below.

1. **Task 1 + Task 2 combined: WriteAtomic gains mode arg; adapter configs at 0o600 (CR-01)** — `70f33c0` (`fix`)

**Plan metadata commit:** (created after this SUMMARY)

## Files Created/Modified

- `internal/cli/state/atomic.go` — `WriteAtomic` signature gains `mode os.FileMode`; Chmod uses `mode` instead of the literal `0o644`; doc comment documents the per-caller-policy posture + CR-01 rationale + TOCTOU-free ordering.
- `internal/cli/state/state.go` — `Save` passes `0o644` explicitly; godoc documents Save as the SOLE legitimate 0o644 WriteAtomic caller.
- `internal/cli/state/atomic_test.go` — Every existing test updated to pass `0o644` through the new signature (5 sites); new `TestWriteAtomic_Mode0600_HonoredOnCredentialFile` asserts 0o600 via `os.Stat`.
- `internal/cli/hydrate/wiring.go` — All four `state.WriteAtomic` call sites pass `0o600` (Render line 230, syncComposite line 459, syncDeepJSON line 517, syncDeepTOML line 548). Each carries a CR-01 comment.

## Decisions Made

- **Required-mode signature over sibling WriteAtomicWithMode (Option (a) vs (b) in the plan).** Rationale per plan `<objective>`: the audit cost of preventing future credential leaks outweighs the one-time churn of updating six call sites. The compiler enforces per-caller mode policy; there is no way to "forget" the mode arg and silently regress to a world-readable default. Doc comment + verbose state.Save godoc lock the contract.
- **Mitigation of T-07-W5-02-05 (state.json mode regression via a future mistake).** The required-mode signature is the load-bearing barrier — a future writer that omits the mode arg fails to compile. Documented in the atomic.go doc comment so a future reader understands why the signature is what it is.
- **Mitigation of T-07-W5-02-02 (TOCTOU between rename and next caller's open).** Documented in the Chmod-block comment: the temp file is chmod'd to `mode` BEFORE rename(2); by the instant rename completes the final path is already at the target mode. No window between rename and the first reader where the mode is wrong.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Tasks 1 + 2 folded into a single atomic commit**
- **Found during:** Task 1, when first attempt at staged commit triggered the pre-commit hook's `lint-changed` gate.
- **Issue:** The plan instructs the executor to "Commit each task atomically." Committing Task 1 alone leaves the tree in a non-building state at HEAD — `internal/cli/hydrate/wiring.go` still has four 2-arg calls to `state.WriteAtomic`, but the new 3-arg required-mode signature in `internal/cli/state/atomic.go` would make those calls fail typecheck. The pre-commit hook (lint-changed + unit) refuses the commit; the only way to land Task 1 alone would be `--no-verify`, which CLAUDE.md explicitly forbids ("If a gate fails, fix the root cause — never `--no-verify` or otherwise bypass.").
- **Fix:** Completed both tasks' edits before staging; made one atomic commit (`70f33c0`) covering all four modified files. The commit message documents both tasks explicitly so the historical record preserves the plan's task boundaries even though they land in one git commit.
- **Files modified:** internal/cli/state/atomic.go, internal/cli/state/state.go, internal/cli/state/atomic_test.go, internal/cli/hydrate/wiring.go (all 4 files in the combined commit).
- **Verification:** `git log --oneline -1` shows the commit; `./scripts/dev.sh make unit-pkg PKG=./internal/cli/state/...` PASS; `./scripts/dev.sh make unit-pkg PKG=./internal/cli/hydrate/...` PASS; `./scripts/dev.sh go build ./...` clean; `./scripts/dev.sh make lint-changed` exit 0. No file deletions in the commit (`git diff --diff-filter=D --name-only HEAD~1 HEAD` empty).
- **Committed in:** `70f33c0` (the single atomic commit).

---

**Total deviations:** 1 auto-fixed (Rule 3 — blocking: tree must stay buildable at every commit per CLAUDE.md pre-commit gate posture).
**Impact on plan:** Zero scope creep. All plan-specified source edits landed verbatim; only the commit-granularity boundary changed. Task 1's acceptance criteria (new signature, mode-honored test, 0o644 caller updates) AND Task 2's acceptance criteria (4×0o600 in wiring.go, CR-01 rationale comment) both pass simultaneously against the single commit.

## Issues Encountered

- **Plan's acceptance criterion regex (`grep -nE 'WriteAtomic\\([^,]+,[^,]+\\)' internal/cli/state/*.go` "returns no matches") is a heuristic, not a strict gate.** The regex matches 3-arg calls when the inner args contain parentheses (`buf.Bytes()`, `[]byte(\`{}\`)`) because `[^,]+\\)` greedy-matches across the inner `)`. The source-of-truth check is the function signature itself (mandatory mode) plus the Go compile gate, which rejects every 2-arg call. Verified by inspecting `grep -oE 'WriteAtomic\\([^)]*\\)'` output: every call has 3 args. No 2-arg call remains anywhere in the tree.

## User Setup Required

None — no external service configuration. This is a server-side / local file mode hardening.

A targeted runtime spot-check (NOT required for CI, captured here for the post-fix runtime gate in W5-03's must_haves): on a Linux host, after a successful `ach-cli hydrate --environment demo --platform claude-code`, `stat -c '%a %n' .ach/.claude/.mcp.json` should now return `600 .ach/.claude/.mcp.json` (was `644`).

## Threat Surface (no new flags)

This plan **shrinks** existing threat surface (T-07-W5-02-01) by closing the credential-disclosure vector at 0o644 on multi-user hosts. No new endpoints, auth paths, file access patterns, or schema changes at trust boundaries. The atomic-write primitive itself is unchanged in observable behavior except that callers now choose the mode. No threat flags to surface.

## Next Phase Readiness

- W5-03 can run its post-fix runtime gate (the `stat -c '%a %n'` assertion against a kind-cluster hydration) against a tree where the engine produces 0o600 credential files end-to-end.
- The next plan in the W5 wave depends on this commit being on `main`; sequential serial wave invariant honored.

## Self-Check: PASSED

- `[ -f internal/cli/state/atomic.go ]` → FOUND
- `[ -f internal/cli/state/state.go ]` → FOUND
- `[ -f internal/cli/state/atomic_test.go ]` → FOUND
- `[ -f internal/cli/hydrate/wiring.go ]` → FOUND
- `git log --oneline --all | grep -q "70f33c0"` → FOUND
- `grep -c "state.WriteAtomic(.*, 0o600)" internal/cli/hydrate/wiring.go` → 4 (expected 4)
- `grep -n "func WriteAtomic(path string, data \\[\\]byte, mode os.FileMode)" internal/cli/state/atomic.go` → FOUND (line 46)
- `grep -n "tmp.Chmod(0o644)" internal/cli/state/atomic.go` → NO MATCH (literal removed, as required)
- `grep -n "tmp.Chmod(mode)" internal/cli/state/atomic.go` → FOUND (line 81)
- `grep -n "WriteAtomic(path, buf.Bytes(), 0o644)" internal/cli/state/state.go` → FOUND (line 144)
- `grep -n "TestWriteAtomic_Mode0600_HonoredOnCredentialFile" internal/cli/state/atomic_test.go` → FOUND
- `./scripts/dev.sh make unit-pkg PKG=./internal/cli/state/...` → PASS (all 6 WriteAtomic tests including the new 0o600 assertion)
- `./scripts/dev.sh make unit-pkg PKG=./internal/cli/hydrate/...` → PASS (no regressions from wiring.go signature update)
- `./scripts/dev.sh go build ./...` → clean
- `./scripts/dev.sh make lint-changed` → exit 0

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
