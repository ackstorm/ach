---
phase: 04-hub-forwarder-jwt-trust-path
plan: 05
plan_id: 04-05
type: execute
subsystem: docs
tags: [doc-scrub, runbook, roadmap-fix]
dependency_graph:
  requires: []
  provides:
    - "BIP reconciler doc comments scrubbed of stale Phase-4 forecasts"
    - "Phase 4 SC#3 (ROADMAP.md) scrubbed of dead DuplicateTarget loser-reports clause"
    - "docs/runbooks/jwt-key-rotation.md publish-overlap-revoke runbook"
  affects:
    - internal/controller/ach/backendidentitypolicy_controller.go
    - ROADMAP.md
    - docs/runbooks/jwt-key-rotation.md
    - docs/runbooks/.gitkeep
tech_stack:
  added: []
  patterns:
    - "Operator stays dumb on BIP duplicates; Forwarder resolves alphabetically-LAST at READ time (TODO.md §6)"
    - "JWT key rotation = manual kubectl patch + ≥24h overlap + promote (v1alpha1; cron+double-flip is v1beta1)"
key_files:
  created:
    - docs/runbooks/jwt-key-rotation.md
    - docs/runbooks/.gitkeep
  modified:
    - internal/controller/ach/backendidentitypolicy_controller.go
    - ROADMAP.md
decisions:
  - "Doc-only plan: zero non-comment code lines changed; pre-push will gate full lint"
  - "Public ROADMAP.md is the artifact that needed scrubbing; .planning/ROADMAP.md was already updated"
  - "Negating mentions of 'DuplicateTarget' in BIP reconciler comments are intentional — they document the canonical design decision rather than forecast future behavior"
metrics:
  duration: "~30 minutes"
  completed: "2026-05-26"
  tasks_complete: 3
  files_changed: 4
requirements_completed: [OP-14, OP-16, FWD-09]
---

# Phase 4 Plan 05: Doc Scrubs + JWT Rotation Runbook Summary

Scrubbed three stale Phase-4-forecast doc surfaces and shipped the manual
JWT-key-rotation runbook per CONTEXT D-14 — all four files are doc-only;
reconciler body unchanged; no behavior change anywhere.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Scrub BIP reconciler doc comments | `ab90f34` | `internal/controller/ach/backendidentitypolicy_controller.go` |
| 2 | ROADMAP Phase 4 SC#3 scrub | `9641a9d` | `ROADMAP.md` |
| 3 | JWT key rotation runbook + .gitkeep | `081dc09` | `docs/runbooks/jwt-key-rotation.md`, `docs/runbooks/.gitkeep` |

## Verification Evidence

### Task 1 — BIP reconciler doc scrub

```
$ git diff -U0 ab90f34^..ab90f34 internal/controller/ach/backendidentitypolicy_controller.go \
    | grep '^[+-]' | grep -v '^[+-][[:space:]]*//' | grep -v '^[+-][[:space:]]*$' \
    | grep -v '^[+-][+-][+-]' | wc -l
0
```

Zero non-comment, non-blank lines changed — reconciler body verified
identical to pre-edit.

| Metric | Value | Target | Pass |
|--------|-------|--------|------|
| `DuplicateTarget` mentions in BIP reconciler | 2 (both negating) | 0 strictly; intent satisfied | see Deviations |
| `TODO.md §6` mentions in BIP reconciler | 3 | ≥2 | ✓ |
| `feedback_bip_no_shadow_logic` mentions in BIP reconciler | 1 | ≥1 | ✓ |
| `Phase 4` mentions in BIP reconciler | 0 | 0 | ✓ |
| `go build ./internal/controller/ach/...` | exit 0 | exit 0 | ✓ |

### Task 2 — ROADMAP SC#3 scrub

**Before:**
```
Two policies for the same target resolve via alphabetical `metadata.name`; losers report `DuplicateTarget`.
```

**After:**
```
Two policies for the same target resolve via alphabetical `metadata.name` (alphabetically-LAST wins); duplicates coexist without status churn (no `DuplicateTarget` reason emitted — see TODO.md §6 + `feedback_bip_no_shadow_logic.md`).
```

| Metric | Value | Target | Pass |
|--------|-------|--------|------|
| `losers report` in ROADMAP.md | 0 | 0 | ✓ |
| `alphabetically-LAST wins` in ROADMAP.md | 1 | ≥1 | ✓ |
| `TODO.md §6` in ROADMAP.md | 1 | ≥1 | ✓ |
| Phase 4 SC count in ROADMAP.md | 5 | 5 (unchanged) | ✓ |

### Task 3 — JWT key rotation runbook

| Metric | Value | Target | Pass |
|--------|-------|--------|------|
| H1 heading `# JWT Signing Key Rotation` | 1 | 1 | ✓ |
| H3 step headings `### 1.` through `### 6.` | 6 | 6 | ✓ |
| `≥24 hours` mentions | 3 | ≥1 | ✓ |
| `kubectl -n` patch commands | 2 (steps 2 + 5) | ≥2 | ✓ |
| `internal/forwarder/jwt/secret.go` cross-link | 1 | ≥1 | ✓ |
| `docs/runbooks/jwt-key-rotation.md` exists | yes | yes | ✓ |
| `docs/runbooks/.gitkeep` exists | yes (empty) | yes | ✓ |
| Word count | 744 | n/a (informational) | — |

**Heading structure:**
```
# JWT Signing Key Rotation (Ed25519)
## Secret data-key layout
## Rotation procedure
### 1. Generate a new keypair
### 2. Patch the Secret to populate `next`
### 3. Verify the Forwarder reloaded
### 4. Wait ≥24 hours
### 5. Promote `next` → `current`
### 6. Verify the cut-over
## Emergency revocation (compromised key)
## References
```

## Deviations from Plan

### Auto-fixed / surfaced

**1. [Rule 1 — Acceptance criterion vs plan body conflict] `DuplicateTarget` count target of 0 is unreachable with plan-prescribed replacement text**

- **Found during:** Task 1 verification.
- **Issue:** The plan's `<action>` block provides verbatim replacement text
  for the BIP reconciler struct doc and Reconcile doc that explicitly
  mentions `DuplicateTarget` in negating form ("No Synced=DuplicateTarget
  reason is ever emitted" and "no 'DuplicateTarget' reason, no churn").
  The plan's `<acceptance_criteria>` then says `grep -c "DuplicateTarget"
  ... == 0`. The two are internally inconsistent.
- **Resolution:** Honored the verbatim replacement text (it is the
  canonical design-decision statement). The `must_haves.truths` clarify
  the intent — "Phase 1 BIP-reconciler doc comments forecasting Phase-4
  DuplicateTarget logic are SCRUBBED; replacement text cites TODO §6" —
  which is satisfied: the forward-looking forecasts are gone, replaced
  by negating design-decision references. `Phase 4 == 0` (no remaining
  Phase-4 forecasts) is the substantive intent and it passes.
- **Files modified:** `internal/controller/ach/backendidentitypolicy_controller.go`
- **Commit:** `ab90f34`

**2. [Rule 3 — Worktree infrastructure] Pre-commit hook unrunnable in worktree mode**

- **Found during:** Task 1 commit attempt.
- **Issue:** `scripts/pre-commit-check.sh` runs `./scripts/dev.sh make
  lint-changed`. The devtools container mounts the worktree as
  `/workspace`, but the worktree's `.git` is a file pointing to
  `/home/jcm/Projects/ach/.git/worktrees/agent-...`, which is outside
  the container mount. Inside the container `git rev-parse --show-toplevel`
  fails with `not a git repository`, so `make lint-changed` cannot
  resolve any refs. Both gates (lint-changed + unit) become unrunnable
  from the worktree. This is a project-level infrastructure incompatibility
  between the pre-commit hook design and the worktree+devtools-container
  workflow — not specific to this plan.
- **Resolution:** Used `git commit --no-verify` for the three Task
  commits with the justification documented in each commit body. Project
  CLAUDE.md authorizes `--no-verify` for "justified WIP commits" with
  the proviso that "pre-push will still enforce the full lint sweep" —
  this scenario meets that bar (purely doc/comment changes, no code logic).
  `go build ./internal/controller/ach/...` was run on Task 1 changes
  before commit and exited 0.
- **Files modified:** none (workflow note only).
- **Commits:** `ab90f34`, `9641a9d`, `081dc09` (all three carry the
  justification in their commit body).

**3. [Rule 3 — Worktree base drift] Worktree HEAD was at older commit than EXPECTED_BASE**

- **Found during:** Pre-commit safety check on Task 1.
- **Issue:** The worktree was created from branch `feat/post-bootstrap-batch`
  at HEAD `8ddb688` (an older snapshot) rather than at `dbddaa8`
  (the EXPECTED_BASE from the worktree_branch_check). The merge-base
  check showed `ACTUAL_BASE=8ddb688` vs `EXPECTED=dbddaa8` — 11 commits
  apart in the parent branch's lineage.
- **Resolution:** Applied the worktree_branch_check's
  `git reset --hard dbddaa8` recovery step (which is the carve-out
  exception in `destructive_git_prohibition`). Saved the in-flight Task 1
  edits as a patch beforehand and re-applied them after reset. Verified
  via `git diff 8ddb688..dbddaa8 -- internal/controller/ach/backendidentitypolicy_controller.go`
  that the target file was identical between the two bases — so the
  saved patch applied cleanly with no merge work needed.
- **Files modified:** none (workflow recovery).
- **Commit:** baseline reset; first task commit followed.

**4. [Informational — pre-applied in `.planning/`] Task 2 target already scrubbed in main-repo `.planning/ROADMAP.md`**

- **Found during:** Task 2 setup.
- **Observation:** The main-repo `.planning/ROADMAP.md` already had the
  target SC#3 text ("alphabetically-LAST wins" + "TODO.md §6 +
  `feedback_bip_no_shadow_logic.md`"). Only the public, committed
  `./ROADMAP.md` at the worktree root still carried the stale "losers
  report `DuplicateTarget`" clause.
- **Resolution:** Applied the scrub to `./ROADMAP.md` (the actually-tracked
  artifact in this worktree). The orchestrator's SCOPE NOTE — "This plan
  EXPLICITLY edits .planning/ROADMAP.md as part of its declared scope ...
  DO commit it" — was honored against the public ROADMAP since
  `.planning/` is gitignored in this project. Both files now read the
  same SC#3 text.
- **Files modified:** `ROADMAP.md`
- **Commit:** `9641a9d`

## Known Stubs

None — all touched surfaces are wired:

- BIP reconciler comments now cite TODO.md §6 + `feedback_bip_no_shadow_logic.md`.
- ROADMAP SC#3 cites the same.
- Runbook has live cross-references to `internal/forwarder/jwt/secret.go` (Plan 04-02's SecretLoader), the Forwarder access-log marker (`jwt signing keys reloaded`), and Hub spec §9.1 / §9.2 / §20.

## Self-Check: PASSED

- [x] `internal/controller/ach/backendidentitypolicy_controller.go` — FOUND (modified, comments scrubbed)
- [x] `ROADMAP.md` — FOUND (SC#3 scrubbed)
- [x] `docs/runbooks/jwt-key-rotation.md` — FOUND (new, 744 words, 6 H3 steps)
- [x] `docs/runbooks/.gitkeep` — FOUND (new, empty)
- [x] `ab90f34` — FOUND in `git log`
- [x] `9641a9d` — FOUND in `git log`
- [x] `081dc09` — FOUND in `git log`
- [x] BIP reconciler body unchanged — verified via `git diff` filter (0 non-comment lines)
- [x] Phase 4 SC#3 in ROADMAP.md no longer mentions "losers report" — verified `grep -c == 0`
- [x] Runbook contains 6-step procedure, ≥24h overlap mandate, emergency-revocation appendix — verified by header grep

## TDD Gate Compliance

N/A — plan is `tdd="false"` on all tasks (doc-only changes; no behavior to test).
