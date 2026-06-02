---
phase: 06-verification
plan: 03
subsystem: testing
tags: [e2e, projection, idempotence, fmt-05, auto-claim, hydrate, kind-cluster, go-test]

# Dependency graph
requires:
  - phase: 06-verification (Plan 02)
    provides: VER-02 e2e harness — projection_helpers_test.go descriptor table + phase7* primitives reused verbatim
  - phase: 02-projection-engine
    provides: route.Project engine + per-adapter ProjectionRules (FMT-05 deterministic sorted-key/stable encode for codex TOML / opencode JSON)
  - phase: 07 (v1.0 CLI engine e2e harness)
    provides: testPhase7BaselineNoOp (state.json sha256 no-op) + SAFE-04 auto-claim cascade primitives
provides:
  - "VER-03 end-to-end gate: per-adapter byte-identical re-hydrate idempotence + auto-claim ownership against the kept kind cluster"
  - "TestProjectionIdempotence (5 adapter subtests) proving the second hydrate is a byte-no-op (projected native files + state.json sha256 unchanged) and auto-claims byte-matching owned files with exit 0"
  - "snapshotProjectedFiles + assertSnapshotsByteIdentical helpers added to the VER-02 harness"
affects: [milestone-completion, sync-drift-detection, future-adapter-additions]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Projected-file byte-snapshot map (path→bytes) captured after hydrate run 1, re-captured after run 2, compared for mutate/drop/spurious-add churn — the FMT-05 determinism proof for the format-converting codex/opencode adapters"
    - "state.json sha256 before/after no-op assertion reused from testPhase7BaselineNoOp, generalized across all five adapters"
    - "Reuse of the descriptor-scoped harness so an empty native dir contributes nothing (guarded by assertProjectedNativeDirs against a vacuous before==after pass)"

key-files:
  created:
    - test/e2e/projection_idempotence_test.go
  modified:
    - test/e2e/projection_helpers_test.go

key-decisions:
  - "Snapshot scope = descriptor nativeDirs only (file-owned projected resources). Co-owned RUNTIME deep-merge files (settings.json / config.toml / opencode.json / .pi/mcp.json) are excluded from the byte-no-op: they carry the live x-ach-key bearer and are the runtime leg's concern; FMT-05's deterministic-encode guarantee is over the projected resource files, which is what the matrix gates. state.json byte-no-op covers the engine-recorded plugin entries separately."
  - "Guarded the idempotence assertion against a vacuous pass: assertProjectedNativeDirs runs before the snapshot and a len(before)==0 fatal trips if no projected files exist — so before==after cannot pass over two empty maps."
  - "Auto-claim is asserted exit-code-based: the second hydrate over byte-matching owned files MUST exit 0 (CollisionOwnedByCurrent / Tier-1 eager match), proving the SAFE-04 auto-claim rather than an exit-7 CollisionRefuse — mirroring testPhase7Sc4AutoClaimMatch + the W6-01 rotated-credential path."

patterns-established:
  - "Per-adapter idempotence matrix mirrors TestProjectionLifecycle's umbrella shape (one t.Run per projectionDescriptor); every subtest re-runs phase7SuiteGuard for clean skip-on-cluster-missing."

requirements-completed: [VER-03]

# Metrics
duration: 38min
completed: 2026-06-02
---

# Phase 06 Plan 03: VER-03 Per-Adapter Re-Hydrate Idempotence + Auto-Claim E2E Matrix Summary

**End-to-end gate proving, for all five adapters against the kept kind cluster, that a second `ach-cli hydrate` over the same demo Environment + workspace is a byte-identical no-op (projected native files unchanged, state.json sha256 unchanged) and auto-claims the byte-matching pre-existing owned files with exit 0 — with the codex (TOML) and opencode (JSON) subtests serving as the load-bearing FMT-05 deterministic-encode proofs.**

## Performance

- **Duration:** ~38 min
- **Tasks:** 2
- **Files modified:** 1 (+1 created)

## Accomplishments
- Extended `test/e2e/projection_helpers_test.go` (the VER-02 06-02 harness) with two new `//go:build e2e` helpers: `snapshotProjectedFiles` (walks the descriptor's nativeDirs → workspace-relative path→bytes map) and `assertSnapshotsByteIdentical` (fails on a byte-diff, a dropped file, OR a spurious new file — churn in either direction). The 06-02 descriptor table and all five existing `assert*` helpers are intact (extended, not recreated).
- Added `test/e2e/projection_idempotence_test.go`: `TestProjectionIdempotence` umbrella with one subtest per canonical adapter id, driving the two-hydrate byte-no-op + auto-claim proof against the cluster.
- Proved the matrix GREEN end-to-end: 5/5 subtests PASS (claude-code 14.0s, codex 13.7s, gemini-cli 14.6s, opencode 14.2s, pimono 17.8s) against the kept cluster using a real demo `pk_` minted via the device-code + Dex mockCallback SSO flow.
- The codex + opencode subtests are explicitly tagged FMT-05 determinism proofs: their projected output passes through a TOML / JSON re-encode, so a non-deterministic map-iteration-order encode would surface as a run1≠run2 byte diff right there.

## Task Commits

Each task was committed atomically:

1. **Task 1: projected-file snapshot + byte-identical helpers** - `ce915bd` (test)
2. **Task 2: per-adapter re-hydrate idempotence + auto-claim e2e matrix** - `867d9ca` (feat)

## Files Created/Modified
- `test/e2e/projection_helpers_test.go` - MODIFIED. Added `bytes` import + `snapshotProjectedFiles` and `assertSnapshotsByteIdentical` (both `//go:build e2e`, package e2e), reusing the 06-02 `projectionDescriptor` table. The 06-02 descriptor table + assertions untouched.
- `test/e2e/projection_idempotence_test.go` - NEW. `//go:build e2e`, package e2e. `TestProjectionIdempotence` (5 per-adapter subtests), each asserting (a) second-hydrate exit 0 auto-claim, (b) `assertSnapshotsByteIdentical` over projected files, (c) state.json sha256 unchanged.

## Decisions Made
- **Snapshot scope = file-owned projected resources, not co-owned runtime files.** FMT-05's deterministic-encode guarantee that VER-03 gates is over the projected resource files; the co-owned runtime deep-merge files carry the live bearer and are the runtime leg's domain. state.json's byte-no-op covers the engine-recorded plugin entries separately, so the projection idempotence contract is fully gated without snapshotting credential-bearing files.
- **Vacuous-pass guard.** `assertProjectedNativeDirs` runs before the snapshot and a `len(before)==0` fatal trips when no projected files exist — so the byte-identical assertion cannot pass over two empty maps.
- **Auto-claim asserted by exit code.** The second hydrate over byte-matching owned files MUST exit 0 (CollisionOwnedByCurrent / Tier-1 eager match), proving the SAFE-04 auto-claim rather than an exit-7 collision refusal.

## Deviations from Plan

None - plan executed exactly as written. Both tasks' acceptance criteria met on the first verification pass; no Rule 1-4 deviations were needed.

## Issues Encountered
- **No host Go toolchain** (per CLAUDE.md): all `go vet`, `build-e2e`, the pk_ mint, and the e2e run were routed through `./scripts/dev.sh`.
- **pk_ acquisition + cluster-setup gating.** `make e2e-focus` does not forward `ACH_E2E_PHASE7_PK` into the devtools container, and the suite `TestMain` defaults to a `kind load ach-operator:latest` setup that fails against the already-up shared cluster. Resolved (matching 06-02's path) by minting a real demo pk_ via the device-code + Dex mockCallback SSO flow against `localhost:8080`, then running `./scripts/dev.sh env E2E_SKIP_SETUP=1 ACH_E2E_PHASE7=1 ACH_E2E_PHASE7_PK=<pk_> go test -tags=e2e -run TestProjectionIdempotence ./test/e2e/`. `E2E_SKIP_SETUP=1` makes `TestMain` reuse the orchestrator-owned cluster rather than re-creating it. The transient mint helper was placed under `hack/mintpk/`, run once, and removed before any commit (never tracked).

## Known Stubs
None — test-only additions; no product code changed, no UI/data-wiring stubs.

## Threat Flags
None new. The matrix only READS the `demo` Environment and writes to per-test `phase7Workspace` temp dirs — no `test/e2e/cluster/` synced fixture modified (T-06-06 mitigated). The snapshot byte-maps are held in-test only and never logged on success; failure messages would print diffs but the bearer is the live demo pk_ flow value, not a committed secret (T-06-07 mitigated). No package installs (T-06-SC accept).

## Verification
- `make e2e-focus RUN='TestProjectionIdempotence'` (equivalent: `./scripts/dev.sh env E2E_SKIP_SETUP=1 ACH_E2E_PHASE7=1 ACH_E2E_PHASE7_PK=<pk_> go test -tags=e2e -run TestProjectionIdempotence -v ./test/e2e/`) → `--- PASS: TestProjectionIdempotence (74.34s)` with all five subtests (claude-code, codex, gemini-cli, opencode, pimono) PASS against the kept cluster.
- `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...` → exit 0 (both after Task 1 and after Task 2).
- `git diff --diff-filter=D` across both commits → no deletions; no `test/e2e/cluster/` fixture touched; only the two intended test files changed.

## Next Phase Readiness
- VER-03 satisfied: re-hydrate is a byte-identical no-op (idempotence + auto-claim ownership) for every adapter, including the format-converting codex/opencode adapters whose conversions FMT-05 guarantees are deterministic. This is the precondition for trustworthy `--sync` drift detection across all five adapters.
- Test-only additions plus a harness extension; no service code touched, so no cluster image rebuild was needed.

## Self-Check: PASSED
- FOUND: test/e2e/projection_idempotence_test.go
- FOUND: test/e2e/projection_helpers_test.go (modified — snapshot helpers added)
- FOUND commit: ce915bd (Task 1)
- FOUND commit: 867d9ca (Task 2)

---
*Phase: 06-verification*
*Completed: 2026-06-02*
