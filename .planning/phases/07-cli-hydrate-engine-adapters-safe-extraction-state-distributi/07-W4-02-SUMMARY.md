---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W4-02
subsystem: docs
tags: [cli, hydrate, docs, claude-md, e2e, golden-diff, raw, state-schema, environment-guard]

# Dependency graph
requires:
  - phase: 07-W3-05
    provides: "cmd/ach-cli/cmd/hydrate.go D-03 refactor — engine-default + hidden --raw fallback registered via MarkHidden. This plan trusts that --raw is available on the binary the e2e test invokes."
  - phase: 07-W4-01
    provides: "test/e2e/cli_hydrate_engine_test.go (TestPhase7CLIEngine) — the new engine-path e2e umbrella that exercises the schemaVersion + Environment-guard surfaces. This plan does NOT re-test those surfaces (07-W4-01 already does) — it only updates the W3-P3 anchor in cli_login_hydrate_test.go so the engine-default binary does not regress the byte-for-byte golden-diff."
provides:
  - "test/e2e/cli_login_hydrate_test.go: W3-P3 hydrate_golden_diff subtest now passes --raw, preserving the Phase 6 POST+stream byte-for-byte stdout contract under the Phase 7 engine-default ach-cli (D-21)."
  - "CLAUDE.md: existing \"Hydrate output != examples/hydrate.json\" failure-mode entry updated — symptom block uses --raw, remedy block opens with a Phase-7-default paragraph that documents the engine-vs-stdout split and points at the W3-P3 caller as the load-bearing --raw user."
  - "CLAUDE.md: NEW failure-mode entry — \"ach-cli hydrate exits 5 with state: schemaVersion != \\\"2\\\"\" — documents the v1alpha1 state.json clean-break per spec §8.2 / D-13 / STATE-02, with two recoveries (delete-stale, --force) and the no-files-written invariant."
  - "CLAUDE.md: NEW failure-mode entry — \"ach-cli hydrate exits 4 with state: same <ach-dir> bound to a different Environment\" — documents the STATE-03 / spec §8.3 workspace-scope guard, three recoveries (cd new workspace, --global, --force), and the workspace-only firing posture."
affects: [07-W4-03, future-phase-7.1]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Documentation-hygiene closure: same-commit-with-code-change. The engine-default behavior shipped via 07-W3-05 + the new exit codes (4, 5) exposed in 07-W1-01 are now reflected in CLAUDE.md so that downstream agents debugging Phase 7 hydrate failures see the new posture immediately."
    - "E2E anchor preservation via hidden-flag short-circuit. The Phase 6 byte-for-byte golden-diff against examples/hydrate.json is preserved unchanged at the bytes layer by routing the W3-P3 caller through --raw (D-04), which short-circuits before any engine call. The change is exactly one --raw argument added — no helper, no second pathway."

key-files:
  created: []
  modified:
    - "test/e2e/cli_login_hydrate_test.go (W3-P3 hydrate_golden_diff subtest: append \"--raw\" to phase6RunAch args slice with a D-21 reference comment; no other subtests touched)"
    - "CLAUDE.md (Common failure modes section: existing Hydrate-golden entry updated + 2 new entries inserted between \"Code change rebuilt but the old container keeps serving\" and \"## Repository-specific patterns\")"

key-decisions:
  - "--raw belongs ONLY on the W3-P3 golden-diff subtest. The other Phase 6 e2e subtests (login_device_code, whoami_verify_pk, env_list, env_keys_create) exercise non-engine surfaces that do not intersect the engine refactor — adding --raw to them would be either incoherent (--raw is hydrate-only) or a no-op. The grep-based AC (\"1 occurrence in args slice\") enforces this scope."
  - "The existing Hydrate-golden CLAUDE.md entry's symptom command was updated from `./bin/ach-cli hydrate --environment demo > /tmp/hydrate-test.json` to `./bin/ach-cli hydrate --raw --environment demo > /tmp/hydrate-test.json`. Without --raw, the engine-default binary writes to disk and emits a stderr summary — the > /tmp/hydrate-test.json redirect would capture an empty stdout and the diff would fail with a confusing \"file is empty\" symptom. Updating the command keeps the failure-mode entry truthful as a copy-pasteable repro."
  - "The two new failure-mode entries cite exit codes 5 and 4 explicitly (\"exit status 5\" / \"exit status 4\" + plain \"exit 5\" / \"exit 4\") so the AC grep + downstream agents both find them. STATE-02 / STATE-03 + spec §8.2 / §8.3 + D-13 / D-04 are cited inline to anchor the entries to the planning corpus."
  - "Both new failure-mode entries document THREE recoveries each (or two for the schemaVersion entry — deleting the stale file is the canonical recovery, --force is the escape hatch). The pattern matches the Forwarder Pod CrashLoopBackOff entry, which documents both the dev-seed and the prod-provisioning recovery — explicit recoveries beat a single \"use --force\" hand-wave."

patterns-established:
  - "Pattern: E2E anchor preservation via single-flag invariant. When a downstream phase changes the default behavior of a CLI command, the preserved-byte-for-byte e2e anchor passes a feature flag (here: --raw, registered + hidden via cmd.Flags().MarkHidden) rather than spawning a parallel test file. The pattern minimizes the cross-phase diff to ONE added argument."
  - "Pattern: failure-mode entries as load-bearing repro scripts. CLAUDE.md \"Common failure modes\" entries are not narrative — they are copy-pasteable bash blocks that reproduce the symptom + remedy. Updating an entry to reflect a default-behavior change (here: prepending --raw to the symptom command) keeps the entry truthful and useful. Stale repros are worse than no repros."

requirements-completed: [STATE-02, STATE-03, STATE-09]

# Metrics
duration: ~20min
completed: 2026-05-29
---

# Phase 07 Plan W4-02: CLI Engine Anchor + Failure-Mode Docs Summary

**E2E byte-equal golden-diff anchor preserved via `--raw` on the W3-P3 hydrate subtest + CLAUDE.md gains two new Phase-7 failure-mode entries (exit 5 schemaVersion, exit 4 Environment guard) + existing Hydrate-golden entry updated to make `--raw` explicit**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-05-29T18:30:00Z (worktree spawn)
- **Completed:** 2026-05-29T18:50:00Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- **D-21 anchor preserved.** The W3-P3 hydrate_golden_diff subtest in `test/e2e/cli_login_hydrate_test.go` now invokes `ach-cli hydrate --environment demo --no-warnings --raw`. The hidden `--raw` flag (D-04 / 07-W3-05) short-circuits before any engine call and emits the Phase 6 POST+stream response body verbatim — `examples/hydrate.json` continues to byte-diff cleanly under the engine-default binary. Single-line behavior change (one `"--raw"` argument added) with a D-21 reference comment.
- **CLAUDE.md docs-hygiene closed for Phase 7 hydrate exit codes.** Two new `### ❌ ... ✅ ...` failure-mode entries added under `## Common failure modes`:
  - "ach-cli hydrate exits 5 with state: schemaVersion != \"2\"" — STATE-02 / spec §8.2 / D-13. Two recoveries (delete stale state, `--force` escape hatch). Notes the no-files-written invariant (gate fires BEFORE manifest fetch).
  - "ach-cli hydrate exits 4 with state: same <ach-dir> bound to a different Environment" — STATE-03 / spec §8.3. Three recoveries (cd new workspace, `--global`, `--force`). Notes the workspace-scope-only firing posture.
- **Existing Hydrate-golden entry updated.** The symptom command now includes `--raw` (engine-default no longer prints the manifest to stdout). The remedy block opens with a Phase-7-default paragraph explaining the engine-vs-stdout split and naming the W3-P3 e2e as the load-bearing `--raw` caller. The existing two-step normalization fallback paragraphs are preserved verbatim — they still apply for cross-cluster base-URL diffs against the `--raw` path.

## Task Commits

Each task was committed atomically:

1. **Task 1: test/e2e/cli_login_hydrate_test.go — add --raw to W3-P3 hydrate invocation** — `e9300af` (test)
2. **Task 2: CLAUDE.md — add schemaVersion + Environment-guard failure-mode entries + update --raw entry** — `de1dc38` (docs)

## Files Created/Modified

- `test/e2e/cli_login_hydrate_test.go` — W3-P3 hydrate_golden_diff subtest: `--raw` appended to the `phase6RunAch` args slice; comment block above the call references D-21 + explains why the flag is needed. Other Phase 6 subtests unchanged.
- `CLAUDE.md` — `## Common failure modes` section: existing Hydrate-golden entry updated (symptom + ✅ remedy lead paragraph); two new entries inserted before `## Repository-specific patterns`. +92 / -2 lines net per the commit diff.

## Decisions Made

- **--raw scope = W3-P3 only.** The plan's AC allows 1–2 occurrences of `"--raw"`; we shipped exactly 1 (in the args slice). No other Phase 6 subtest needs it — they cover non-hydrate surfaces.
- **Hydrate-golden symptom command updated to include --raw.** Without `--raw`, the engine-default `./bin/ach-cli hydrate --environment demo > /tmp/hydrate-test.json` would capture empty stdout (engine writes to disk + summary on stderr) and the failure-mode entry would document an impossible repro. Keeping the repro pasteable beats keeping the wording untouched.
- **Three explicit recoveries documented per new entry** (or two for the schemaVersion-only entry). Pattern matches the Forwarder-CrashLoopBackOff entry that documents both dev-seed and prod-provisioning recoveries — explicit recoveries beat a single `--force` hand-wave.

## Deviations from Plan

None - plan executed exactly as written. Both tasks landed as specified in the plan's `<behavior>` and `<action>` blocks; all acceptance criteria pass per the verification grep set.

## Issues Encountered

None. The plan was a documentation-hygiene closure for two anchors that already exist on disk (the `--raw` flag in 07-W3-05, the exit codes 4/5 in 07-W1-01) — the task was reflecting them in the load-bearing e2e test + the load-bearing CLAUDE.md reference.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 7 W4 (Wave 4) docs-hygiene loop closed for the new hydrate exit codes. The remaining W4 work is plan-level verification (07-W4-03 or equivalent, if planned) — this plan does not gate it.
- Phase 7.1 (distribution polish — Windows binary, Homebrew, Helm polish, InitContainer runbook) inherits the engine-default behavior + the CLAUDE.md failure-mode entries as load-bearing context. No additional CLAUDE.md updates expected from 7.1 for these exit codes.
- The `--raw` invariant is now documented in BOTH the test (as a load-bearing comment with D-21 reference) and CLAUDE.md (as the explicit short-circuit explanation). Future re-captures of `examples/hydrate.json` will discover both surfaces.

## Self-Check: PASSED

- File `test/e2e/cli_login_hydrate_test.go`: FOUND (modified, --raw present)
- File `CLAUDE.md`: FOUND (modified, all 7 grep AC pass: schemaVersion, same <ach-dir>, exit 5, exit 4, --raw, STATE-02, STATE-03; code-fences balanced at 68 even)
- Commit `e9300af` (test(07-W4-02): pass --raw to W3-P3 hydrate golden-diff (D-21)): FOUND
- Commit `de1dc38` (docs(07-W4-02): document Phase 7 hydrate failure modes in CLAUDE.md): FOUND
- Verification gate `./scripts/dev.sh go build -tags=e2e ./test/e2e/...`: EXIT 0

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
