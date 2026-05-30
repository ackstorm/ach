---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W1-01
subsystem: cli
tags: [exit-codes, roadmap, requirements, phase-7-foundation, scaffolding]

# Dependency graph
requires:
  - phase: 06-cli-foundation
    provides: "internal/cli/exit Phase 6 const block (OK/General/AuthN/Network/ConfigFile = 0/1/3/6/8) + Code typed-int + CodedError + MapServerError; this plan extends the const block additively without renumber."
provides:
  - "exit.Drift (2), exit.EnvironmentMismatch (4), exit.SchemaMismatch (5), exit.CollisionRefuse (7) constants downstream W1/W2/W3/W4 plans can reference via *exit.CodedError"
  - "Phase 7.1 carve-out in ROADMAP.md: DIST-01..04 + SC#5 lifted off Phase 7 entry; new Phase 7.1 'Distribution polish' entry inserted with Goal/Depends-on/Requirements/Success-Criteria/Plans=TBD; Progress table row added"
  - "REQUIREMENTS.md Traceability rows DIST-01..04 retargeted from Phase 7 to Phase 7.1 (Coverage 126/126 preserved)"
affects: [07-W1-02, 07-W1-03, 07-W1-04, 07-W1-05, 07-W1-06, 07-W2-01, 07-W2-02, 07-W2-03, 07-W3-01, 07-W3-02, 07-W3-03, 07-W3-04, 07-W3-05, 07-W4-01, 07-W4-02, 07.1]

# Tech tracking
tech-stack:
  added: []  # no new deps; stdlib testing only
  patterns:
    - "Phase 7 hydrate-engine errors raised via *exit.CodedError (not via MapServerError HTTP mapping) so a hostile server cannot spoof Drift/EnvironmentMismatch/SchemaMismatch/CollisionRefuse"
    - "Closed Phase 6 set (0/1/3/6/8) preserved verbatim under wire-format-stability promise; new Phase 7 codes (2/4/5/7) slot into existing const block in numeric order"
    - "Test gates pair-wise: TestPhase7Codes asserts the four new codes; TestPhase6CodesUnchanged is the regression gate against accidental renumbering"

key-files:
  created:
    - ".planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W1-01-SUMMARY.md"
  modified:
    - "internal/cli/exit/exit.go (add 4 const + rewrite top-of-file comment; tracked, committed 85bcb95)"
    - "internal/cli/exit/exit_test.go (add TestPhase7Codes + TestPhase6CodesUnchanged; tracked, committed 85bcb95)"
    - ".planning/ROADMAP.md (strip DIST-01..04 from Phase 7 Requirements line + delete SC#5 + insert Phase 7.1 entry + add Progress row; gitignored, no commit)"
    - ".planning/REQUIREMENTS.md (retarget DIST-01..04 Traceability rows to Phase 7.1; gitignored, no commit)"

key-decisions:
  - "Top-of-file comment in internal/cli/exit/exit.go fully rewritten: dropped the 'stay absent here' note and replaced with 'Codes 2/4/5/7 are Phase 7 additions per STATE-02/STATE-03/STATE-04/STATE-09/SAFE-04. Phase 6 set (0/1/3/6/8) is unchanged — every renumber would be a wire-format break.' Documents BOTH the additive nature AND the no-renumber invariant in the same paragraph."
  - "Each new const carries a godoc citing the originating REQ-ID(s) and spec section: Drift→STATE-04 + D-14/D-16; EnvironmentMismatch→STATE-03 + §8.3; SchemaMismatch→STATE-09 §6.2 AND STATE-02 §8.2 (one code, two trigger paths); CollisionRefuse→SAFE-04 + §6.4. Downstream plans grep for these REQ-IDs to confirm wiring."
  - "RED+GREEN collapsed into a single commit (85bcb95) rather than separate test/feat commits because the project's pre-commit hook enforces compile-cleanliness (go vet) and would block a strict-TDD RED commit. Per CLAUDE.md 'never --no-verify', the right resolution is to bundle the test+impl atomically; RED was still verified locally before writing impl (build error: 'undefined: exit.Drift, exit.EnvironmentMismatch, exit.SchemaMismatch, exit.CollisionRefuse')."
  - "Existing exit_test.go (Phase 6) was EXTENDED, not replaced. The plan's <action> said 'Create internal/cli/exit/exit_test.go' but the file already exists with 11 Phase 6 tests; overwriting would have been a Rule 1 bug (lost regression coverage). Phase 7 tests appended after TestMapServerError_504_Network."
  - "Phase 7.1 entry placed AFTER the Phase 7 plan list and BEFORE the '## Progress' section in ROADMAP.md so it slots into the decimal-phase numeric ordering convention documented at the top of ROADMAP.md ('Decimal phases appear between their surrounding integers in numeric order'). The Progress table row (`7.1. Distribution polish ... | 0/TBD | Not started | - |`) is appended after the Phase 7 row."

patterns-established:
  - "Hydrate-engine exit codes: *exit.CodedError is the raise mechanism for Drift/EnvironmentMismatch/SchemaMismatch/CollisionRefuse. MapServerError stays HTTP-only (401/403/503/504 → AuthN/General/Network). This separation is the threat-model guarantee against exit-code spoofing by a hostile server (T-06-01-07)."
  - "Phase docs hygiene: Phase 7.1 (and any future decimal carve-out) lifts its Requirements + Success Criteria off the parent Phase N entry — REQUIREMENTS.md Traceability rows for those IDs are retargeted in the same commit/edit. Coverage count is preserved (no IDs added or removed)."

requirements-completed:
  - STATE-02
  - STATE-03
  - STATE-04
  - STATE-09
  - SAFE-04

# Note: these REQ-IDs are PARTIALLY addressed — this plan ships the EXIT-CODE
# surface they emit through. The full STATE-02/03/04/09 + SAFE-04 behavior
# (state.json parser, environment guard, manifest schemaVersion check,
# auto-claim cascade) lands in plans 07-W1-02, 07-W1-05, 07-W1-06, 07-W2-03.
# Listed here because the plan frontmatter `requirements_addressed` claims them.

# Metrics
duration: 13min
completed: 2026-05-29
---

# Phase 7 Plan 07-W1-01: Phase 7.1 carve-out + Phase 7 exit-code surface Summary

**Phase 7.1 distribution carve-out landed in ROADMAP/REQUIREMENTS, and the `internal/cli/exit` package gained four additive constants (Drift=2, EnvironmentMismatch=4, SchemaMismatch=5, CollisionRefuse=7) every downstream Phase 7 plan depends on — Phase 6 codes (0/1/3/6/8) unchanged.**

## Performance

- **Duration:** ~13 min
- **Started:** 2026-05-29T13:36:00Z (worktree spawn)
- **Completed:** 2026-05-29T13:49:00Z
- **Tasks:** 2 (both `auto`/`tdd=true`)
- **Files modified:** 4 (2 tracked + 2 gitignored docs)
- **Tracked commits:** 1 (`85bcb95`)

## Accomplishments

- Phase 7.1 "Distribution polish" entry exists in `ROADMAP.md` between the Phase 7 block and the Progress section, with Goal sentence + Depends-on Phase 7 + Requirements DIST-01..04 + 5 numbered Success Criteria mirroring the old SC#5 wording (OCI image, 5 binaries, brew install, Helm chart deploy, InitContainer pattern) + Plans: TBD + UI hint: no.
- Phase 7 entry no longer references DIST-01..04 in its `**Requirements**:` line (24 REQ-IDs remaining: STATE-01..11 + ADAPT-01..07 + SAFE-01..06) and no longer carries SC#5 (4 numbered Success Criteria remaining).
- Progress table row appended: `7.1. Distribution polish ... | 0/TBD | Not started | - |`.
- `REQUIREMENTS.md` Traceability rows for DIST-01..04 read `Phase 7.1` (not `Phase 7`). Coverage line still reads `126 of 126 v1alpha1 REQ-IDs mapped` (no IDs added, no IDs lost).
- `internal/cli/exit/exit.go` exports 4 new constants in numeric order: `Drift Code = 2`, `EnvironmentMismatch Code = 4`, `SchemaMismatch Code = 5`, `CollisionRefuse Code = 7`. Each carries a godoc citing the originating REQ-ID and spec section.
- Top-of-file package comment rewritten: drops the prior "stay absent here" note (which was Phase 6's forward reference to this work) and replaces it with the additive-no-renumber invariant statement.
- Phase 6 constants (OK=0, General=1, AuthN=3, Network=6, ConfigFile=8) preserved verbatim — same values, same names, identical placement in the const block.
- `internal/cli/exit/exit_test.go` gained `TestPhase7Codes` (asserts Drift/EnvironmentMismatch/SchemaMismatch/CollisionRefuse values) and `TestPhase6CodesUnchanged` (regression gate against accidental Phase 6 renumber). 14 total tests pass; SPDX header on first line; no testify; stdlib only.
- `MapServerError`, `CodedError` struct, `Error()` / `Unwrap()` methods, `Code` typed-int — all unchanged.

## Task Commits

Each task was committed atomically:

1. **Task 1: ROADMAP + REQUIREMENTS refresh — carve out Phase 7.1 (DIST-01..04, SC#5)** — _no git commit_ (`.planning/` is gitignored; SDK `commit` returned `committed:false` with reason `paths are ignored`). File edits landed on disk at `/home/jcm/Projects/ach/.planning/ROADMAP.md` + `/home/jcm/Projects/ach/.planning/REQUIREMENTS.md`. This is the intentional `skipped_gitignored` success path per executor SDK envelope.
2. **Task 2: Phase 7 exit codes — additive constants in `internal/cli/exit/exit.go`** — `85bcb95` (`feat`). Combines test+impl in one atomic commit (see Decisions Made → "RED+GREEN collapsed").

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored). No `docs(...)` follow-up commit possible without `-f`-style force-stage, which is forbidden.

_TDD note: the plan's `tdd="true"` attribute was honored procedurally (RED test written + verified failing-to-compile, then GREEN impl + tests passing) but the RED and GREEN commits were collapsed into one because the project pre-commit hook enforces `go vet` cleanliness and would reject the RED commit. CLAUDE.md prohibits `--no-verify`._

## Files Created/Modified

- `internal/cli/exit/exit.go` — Rewrote the package doc comment (lines 3-7) to reflect the additive-no-renumber invariant; inserted 4 new const blocks with godoc into the existing const block in numeric order (Drift between General and AuthN; EnvironmentMismatch between AuthN and SchemaMismatch; SchemaMismatch between EnvironmentMismatch and Network; CollisionRefuse between Network and ConfigFile); updated the const block's header comment from "Phase 6 subset" to "Phase 6 ships codes 0/1/3/6/8; Phase 7 adds 2/4/5/7 (additive, no renumber)." MapServerError, CodedError, Code typed-int — untouched.
- `internal/cli/exit/exit_test.go` — Appended `TestPhase7Codes` (table-driven, expected-int literals so a future renumber of either side trips the gate) and `TestPhase6CodesUnchanged` (Phase 6 regression gate). The pre-existing `TestExitCodeConstants` already covered the Phase 6 values; the new named gate makes the regression intent explicit in `go test -v` output. Existing 12 Phase 6 tests left in place.
- `.planning/ROADMAP.md` (gitignored) — Phase 7 `**Requirements**:` line stripped of `, DIST-01, DIST-02, DIST-03, DIST-04`. Phase 7 Success Criteria SC#5 (the "Distribution artifacts are publishable…" paragraph) deleted. New `### Phase 7.1: Distribution polish (windows binary, Homebrew tap, Helm chart polish, K8s InitContainer pattern)` heading + body inserted between the Phase 7 plan list (after "**UI hint**: no") and the `## Progress` section. New `| 7.1. Distribution polish (...) | 0/TBD | Not started | - |` row appended to the Progress table.
- `.planning/REQUIREMENTS.md` (gitignored) — Traceability table rows `DIST-01`, `DIST-02`, `DIST-03`, `DIST-04` changed from `| Phase 7 | TBD |` to `| Phase 7.1 | TBD |`. Coverage line at line 211 (`**Coverage:** 126 of 126 v1alpha1 REQ-IDs mapped to exactly one phase. No orphans, no duplicates.`) unchanged.

## Decisions Made

See `key-decisions` in frontmatter. Summary:
- RED+GREEN merged into one commit (`85bcb95`) because the pre-commit hook blocks RED commits that fail `go vet`. CLAUDE.md forbids `--no-verify`. TDD discipline preserved procedurally (RED verified locally before GREEN).
- The existing `exit_test.go` was extended (Phase 7 tests appended) rather than replaced — the plan's `<action>` literally said "Create internal/cli/exit/exit_test.go" but the file already exists from Phase 6.
- The top-of-file comment in `exit.go` was fully rewritten, not patched in place, because the prior comment included the forward reference "stay absent here" which became false once the constants were added.
- SchemaMismatch (5) was given a single godoc citing BOTH STATE-09 (manifest schemaVersion != "v1alpha1") AND STATE-02 (state.schemaVersion != "2") because the plan's `<behavior>` block explicitly designates one code with two trigger paths.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Plan `<verify>` automated gate uses `grep -c` where `grep -oc | wc -l` was intended**
- **Found during:** Task 1 verification
- **Issue:** The plan's automated verify line reads `grep -c "DIST-0[1-4]" .planning/ROADMAP.md | grep -v "^#" | (read n; test "$n" -ge 4)`. `grep -c` counts matching LINES, not occurrences; a single Requirements line with all four DIST IDs counts as 1, not 4. The semantic intent (all four IDs present somewhere in the ROADMAP entry) is satisfied — `grep -o "DIST-0[1-4]" | wc -l` returns 4 — but the literal `-ge 4` test as authored cannot pass when the four IDs live on one line.
- **Fix:** Validated against the plan's `<acceptance_criteria>` block instead (which is the canonical specification), all six bullets of which pass:
  - "Phase 7 **Requirements**: line does NOT contain DIST-01..04" → OK
  - "ROADMAP.md contains heading '### Phase 7.1: Distribution polish'" → OK
  - "Phase 7 Success Criteria list contains exactly 4 numbered items" → OK (count = 4)
  - "REQUIREMENTS.md rows matching `^\| DIST-0[1-4] \| Phase 7.1 \|` count = 4" → OK
  - "REQUIREMENTS.md Coverage line still reads '126 of 126'" → OK
  - "`git diff --stat` shows two files modified" → N/A (both files gitignored)
- **Files modified:** N/A — verify-command issue, no code change required
- **Verification:** All acceptance criteria validated independently via grep/wc
- **Committed in:** N/A

**2. [Rule 3 - Blocking] Pre-commit hook (`make pre-commit`) blocks the strict-TDD RED commit**
- **Found during:** Task 2 RED step
- **Issue:** The plan's `tdd="true"` attribute combined with the `<tdd_execution>` step in the executor system prompt mandates a separate RED commit (`test(...): add failing test for [feature]`) before the GREEN impl commit. But this project's `pre-commit` git hook runs `make pre-commit` which includes `go vet` over the entire tree; a failing-to-compile RED test trips the vet gate and the commit is rejected. CLAUDE.md explicitly forbids `--no-verify` as a workaround ("If a gate fails, fix the root cause — never `--no-verify` or otherwise bypass"). The orchestrator prompt does not surface `workflow.worktree_skip_hooks=true`, so silent bypass is also forbidden per the executor's `<parallel_execution>` block.
- **Fix:** Collapsed RED + GREEN into a single atomic commit (`85bcb95`). TDD discipline preserved procedurally: RED test was written first and verified locally via `./scripts/dev.sh make unit-pkg PKG=./internal/cli/exit/...` to produce the expected build failure (`undefined: exit.Drift, exit.EnvironmentMismatch, exit.SchemaMismatch, exit.CollisionRefuse`); only then was the impl added; only then was the combined diff staged and committed.
- **Files modified:** None (workflow change, not a code/test change)
- **Verification:** GREEN test run after impl shows `--- PASS: TestPhase7Codes` and `--- PASS: TestPhase6CodesUnchanged` alongside the 12 pre-existing Phase 6 tests
- **Committed in:** `85bcb95` (the combined test+impl commit itself)

**3. [Rule 1 - Bug] Initial Edit/Write calls targeted main-repo `/home/jcm/Projects/ach/internal/cli/exit/exit_test.go` instead of worktree's `internal/cli/exit/exit_test.go`**
- **Found during:** Task 2 RED step
- **Issue:** First attempt at appending the Phase 7 tests used the absolute path `/home/jcm/Projects/ach/internal/cli/exit/exit_test.go`, which resolves to the **main repo's** copy, not the worktree's. The two are independent inodes (the worktree has its own checkout of tracked files). After the edit, `./scripts/dev.sh make unit-pkg` ran inside the worktree, found NO Phase 7 tests in the worktree's `internal/cli/exit/exit_test.go`, and all 12 pre-existing tests passed silently — so the RED step appeared to pass when it should have failed. This is exactly the absolute-path safety hazard documented in `agents/gsd-executor.md` step 0b ("absolute paths constructed from prior `pwd` output … will resolve to the **main repo**, not the worktree — silently writing files to the wrong location").
- **Fix:** Reverted the misplaced main-repo edit with `git checkout -- internal/cli/exit/exit_test.go` (executed inside `/home/jcm/Projects/ach`). Re-derived the correct absolute path from the worktree-local `git rev-parse --show-toplevel` (`/home/jcm/Projects/ach/.claude/worktrees/agent-a92df1052ac45f085`), verified containment via the boundary-safe `[[ "$ABS_PATH" != "$WT_ROOT" && "$ABS_PATH" != "$WT_ROOT/"* ]]` check, then Edited the worktree copy. Re-ran the unit tests; this time the build correctly failed with the expected `undefined: exit.Drift` errors, confirming RED state.
- **Files modified:** `/home/jcm/Projects/ach/internal/cli/exit/exit_test.go` (transiently — reverted to pre-edit state); `internal/cli/exit/exit_test.go` (worktree relative path — the correct target, retained and committed).
- **Verification:** `stat` confirmed independent inodes between main-repo and worktree copies; `git diff --stat` in worktree after revert + correct edit showed only the worktree's `internal/cli/exit/exit_test.go` modified.
- **Committed in:** `85bcb95` (Task 2 commit) — only the worktree's correctly-edited file is in the commit

---

**Total deviations:** 3 auto-fixed (2 Rule 1 bugs, 1 Rule 3 blocking-workflow)
**Impact on plan:** All three deviations were workflow/tooling friction, not scope creep. The plan's intent (Phase 7.1 carve-out + 4 new exit codes) was delivered verbatim. The plan's verify command typo is harmless (acceptance criteria are the canonical spec); the TDD-collapse is procedural (RED→GREEN discipline preserved); the path mishap was caught and corrected before commit.

## Issues Encountered

- `.planning/` is gitignored at the repo level (line 48 of `.gitignore`), so the Task 1 file edits do not produce a git commit — they live on disk only at `/home/jcm/Projects/ach/.planning/{ROADMAP,REQUIREMENTS}.md`. This is the documented "intentional success path" per the executor SDK envelope (`{committed:false, reason: 'paths are ignored'}`). The CLAUDE.md "Documentation hygiene" rule still holds: docs were updated at the moment of the code change, just not in a git-trackable commit. Downstream phases that need to know about Phase 7.1 read the in-tree `.planning/` files directly.
- The worktree initially had no `.planning/` directory (because `.planning/` is gitignored and worktrees only carry tracked content). Edits to `.planning/ROADMAP.md` and `.planning/REQUIREMENTS.md` therefore landed in the main repo's shared filesystem-level `.planning/`, NOT in any worktree-local copy. This is correct behavior — the shared `.planning/` IS the source of truth across worktrees — but worth flagging so a future executor doesn't look in the worktree for those files.
- Running `go mod tidy` (via the devtools container) was necessary mid-flow because intermediate `make unit-pkg` runs polluted `go.sum` with transitive checksums; `go mod tidy` cleaned this back to the pre-existing baseline.

## User Setup Required

None — no external service configuration required. All changes are repo-internal (Go code + planning docs).

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/exit/exit.go ]            → FOUND
[ -f internal/cli/exit/exit_test.go ]       → FOUND

# Gitignored doc edits (main-repo .planning/)
[ -f /home/jcm/Projects/ach/.planning/ROADMAP.md ]      → FOUND
[ -f /home/jcm/Projects/ach/.planning/REQUIREMENTS.md ] → FOUND

# Commit existence
git log --oneline --all | grep 85bcb95 → FOUND ("feat(07-W1-01): add Phase 7 exit codes ...")

# Plan-level verification gates
grep -E "^\s+(Drift|EnvironmentMismatch|SchemaMismatch|CollisionRefuse) Code = [0-9]" internal/cli/exit/exit.go | wc -l → 4   (expected 4)
grep -E "^\s+(OK|General|AuthN|Network|ConfigFile) Code = [0-9]" internal/cli/exit/exit.go | wc -l → 5  (expected 5)
grep -q "^### Phase 7.1: Distribution polish" .planning/ROADMAP.md → OK
grep -E "^\| DIST-0[1-4] \| Phase 7.1 \|" .planning/REQUIREMENTS.md | wc -l → 4  (expected 4)
grep -q "Coverage:\*\* 126 of 126" .planning/REQUIREMENTS.md → OK
./scripts/dev.sh make unit-pkg PKG=./internal/cli/exit/... → exit 0 (14 tests pass, including TestPhase7Codes + TestPhase6CodesUnchanged)
./scripts/dev.sh make lint → exit 0 (full lint sweep clean)
```

## Self-Check: PASSED

## Next Phase Readiness

- Wave-1 plans 07-W1-02 (state), 07-W1-03 (lock), 07-W1-04 (hash), 07-W1-05 (manifest), 07-W1-06 (hydrate skeleton) can all now `import "github.com/ackstorm/ach/internal/cli/exit"` and reference `exit.Drift` / `exit.EnvironmentMismatch` / `exit.SchemaMismatch` / `exit.CollisionRefuse` without further edits to the exit package.
- Phase 7.1 entry exists in ROADMAP.md, so subsequent Phase 7 commits that touch ROADMAP.md (e.g. progress updates, plan-completion ticks) will not need a side-quest carve-out — the boundary is already drawn. This eliminates the mid-phase ROADMAP-churn rebase hazard the plan's `<objective>` flagged.
- No blockers or concerns for downstream plans. The Plans column for Phase 7.1 reads `TBD` — researcher/planner ownership for Phase 7.1 has not been assigned yet, but that is correctly out-of-scope for this plan.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
