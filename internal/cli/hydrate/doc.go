// SPDX-License-Identifier: Apache-2.0

// Package hydrate is the engine-side 14-step commit-sequence orchestrator
// behind `ach-cli hydrate` (CLI spec §6.7). It composes the W1 atomic
// boundary primitives (state + lock + manifest + hash) into a single
// fail-fast pipeline that materializes a workspace from the
// platform-api's POST /platform/hydrate response.
//
// # W1 atomic boundary (D-02)
//
// This package ships the orchestrator skeleton and the FULLY-implemented
// stages 1-6 (lock → tmp sweep → state read+guard → reconcile vs disk →
// manifest fetch → scope-aware diff) plus the §8.4 drift four-outcome
// truth table. Stages 7-11 (content fetch / safe extract / hash classify
// / adapter dispatch / sync inverse-merge) are interface-shaped STUBS
// in W1; concrete implementations land in:
//
//   - 07-W2-01/02 — Extractor (safe tar policy + auto-claim cascade)
//   - 07-W3-01..05 — AdapterDispatcher (4 platform adapters + registry)
//
// Stage 12 (atomic state write) reuses state.WriteAtomic verbatim;
// stage 13 (cleanup tmp) reuses state.SweepTmp. Stage 14 is the
// implicit return.
//
// # 14-step commit sequence (CLI spec §6.7)
//
//  1. lock        — flock(LOCK_EX) on <ach-dir>/lock (mode dispatch:
//     FailFast / Wait / WithTimeout per opts.Wait + opts.LockTimeout).
//  2. sweep-tmp   — unconditional state.SweepTmp(achDir). Errors swallowed.
//     Then dropLegacyPluginCache: best-effort RemoveAll of the pre-ephemeral
//     persistent <achDir>/plugin projection-cache (context scope, !DryRun).
//  3. read-state  — state.Load + state.GuardEnvironment. Schema mismatch
//     → exit 5 unless --force; environment mismatch → exit 4 unless --force.
//  4. reconcile   — silent prune of state entries whose target is missing
//     on disk (STATE-04 "tracked file missing → silently pruned").
//  5. manifest    — POST /platform/hydrate via manifest.Fetch; schema
//     mismatch → exit 5.
//  6. diff        — scope-aware iteration honoring STATE-10
//     (--include-runtime / --only-runtime) — out-of-scope state slices
//     remain UNTOUCHED.
//  7. fetch       — STATE-11 unconditional GET of every downloadUrl.
//     W1 STUB — concrete impl lands in 07-W2.
//  8. extract     — safe tar policy + bomb defense + auto-claim. Plugins
//     extract to the per-run ephemeral pluginStageRoot (<achDir>/tmp, swept
//     at steps 2 + 13) — NO persistent plugin cache; prompts/artifacts keep
//     their <achDir>/<kind> deliverable path (CLI §6.4).
//     W1 STUB — 07-W2.
//  9. hash        — xxh3 of bytes written vs upstream-source; feed
//     drift.Compare for STATE-04 four-outcome classification.
//  10. adapter     — per-platform runtime + plugin transformation. The plugin
//     projection leg reads the ephemeral pluginStageRoot, so a plugin removed
//     from the Environment can never linger on disk and be re-projected (the
//     cross-plugin destination-collision bug class).
//     W1 STUB — 07-W3.
//  11. sync        — opts.Sync inverse-merge deletion (STATE-05).
//     W1 STUB — 07-W2/W3.
//  12. write-state — state.Save (= state.WriteAtomic, STATE-07 four-step
//     tmp + fsync(fd) + rename + fsync(parent_dir)). Last barrier before
//     successful return. Skipped when opts.DryRun.
//  13. cleanup    — state.SweepTmp again. Errors swallowed.
//  14. return     — implicit. opts.Verbose summarizes Result counts to
//     opts.Stderr (CONTEXT.md Integration Points).
//
// # Requirements addressed
//
//   - STATE-01 — <ach-dir>/state.json resolved per workspace vs global.
//   - STATE-03 — same-<ach-dir>-different-Environment guard (exit 4).
//   - STATE-04 — §8.4 drift four-outcome truth table (drift.go).
//   - STATE-07 — state.WriteAtomic at step 12.
//   - STATE-08 — manifest fetch unconditional (STATE-11 sibling).
//   - STATE-09 — manifest schemaVersion=="v1alpha1" (exit 5).
//   - STATE-10 — scope filter via --include-runtime / --only-runtime.
//   - STATE-11 — GET unconditional; disk-write short-circuit lives in
//     07-W2 extract path, NOT here.
//
// # Test seam discipline (07-PATTERNS.md)
//
// Every stage-7-onward stage reaches through an interface field on the
// commit struct so W1 unit tests can swap fakes:
//
//   - stateStore  (StateStore)        — Load / Save / GuardEnvironment
//   - locker      (lock.Locker)       — Acquire(ctx, mode, timeout)
//   - fetcher     (manifestFetcher)   — defaults to manifest.Fetch
//   - extractor   (Extractor)         — nil in W1; 07-W2-01 supplies
//   - adapter     (AdapterDispatcher) — nil in W1; 07-W3-05 supplies
//   - differ      (Differ)            — drift.NewDiffer concrete here
//
// Concrete extractor + adapter are wired in by the caller layer
// (07-W3-05 cobra adaptation of cmd/ach-cli/cmd/hydrate.go) without
// touching commit.go's step methods.
//
// # TEST-ONLY: ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP
//
// commit.go honors a single test-only environment variable consumed by
// 07-W4-01 sc2_commit_sequence_sigkill. Setting
// ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP=<N> (where N is a step
// number 1..13) causes the orchestrator to invoke killFn(N) immediately
// after stepN returns and BEFORE stepN+1 runs. The production killFn
// calls syscall.Kill(os.Getpid(), syscall.SIGKILL) — the process dies
// before the next step, giving the e2e a deterministic kill point
// without the timing flake `timeout --signal=KILL 0.5s` would produce
// (D-22 close-criterion alignment).
//
// The env-var is read once at newCommit() entry via
// readSigkillSeamFromEnv; unset/empty/unparsable → the seam is
// disabled (killFn never called, no syscall, no overhead on the
// production path).
//
// Compiled in ONLY under -tags=e2e; release builds receive a no-op
// stub via sigkill_seam_prod.go (WR-01 production safety fix — the
// env-var literal is not present in release-binary strings, closing
// the code-injection vector for a misconfigured parent process or
// hostile env that would otherwise crash ach-cli mid-hydrate).
//
// TODO(post-Phase-7-close): remove this seam once SC#2 stabilizes via a
// less invasive mechanism. The TODO marker is duplicated above the
// commit-struct field declaration so a grep for "post-Phase-7-close"
// finds both touch points.
//
// # Package layout (D-09)
//
//   - doc.go     — this file (package contract).
//   - flags.go   — Opts struct (every engine flag from D-03).
//   - result.go  — Result struct + Extractor / AdapterDispatcher /
//     Differ / StateStore interfaces.
//   - commit.go  — Run entry point + 14-step orchestrator + SIGKILL seam.
//   - drift.go   — STATE-04 four-outcome truth table + WrapDriftError.
//
// Sibling packages this package imports:
//
//   - internal/cli/state    — File / Load / Save / GuardEnvironment /
//     SweepTmp / ResolvePath / WriteAtomic / sentinel errors.
//   - internal/cli/lock     — Locker / Lease / AcquireMode / sentinel errors.
//   - internal/cli/hash     — Hash / HashBytes (xxh3 wrapper).
//   - internal/cli/manifest — Manifest / Fetch / ErrSchemaMismatch.
//   - internal/cli/exit     — CodedError (Drift / EnvironmentMismatch /
//     SchemaMismatch).
//   - internal/cli/httpclient — Client (for manifest.Fetch).
package hydrate
