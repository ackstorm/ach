---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W2-01
subsystem: cli
tags: [safe-extract, tar, gzip, bomb-defense, symlink-policy, SAFE-01, SAFE-02, SAFE-03, SAFE-06, phase-7-foundation]

# Dependency graph
requires:
  - plan: 07-W1-01
    provides: "Phase 7 exit code constants (Drift / EnvironmentMismatch / SchemaMismatch / CollisionRefuse) — extract returns sentinel errors that the W2-02 staging layer maps to *exit.CodedError via these constants"
  - plan: 07-W1-04
    provides: "internal/cli/hash package — Hash(io.Reader) (string, error) producing canonical 'xxh3:<32hex>' digests; consumed by extract for FileWrite.Hash on every materialized regular file"
provides:
  - "internal/cli/extract package — Extract(ctx, gzReader, dst, kind, limits, allowSymlinks) (Result, error), the single safe-extraction entry point in Phase 7"
  - "Limits struct + DefaultLimits + LoadLimits env-var parser (ACH_MAX_EXTRACTED_PLUGIN_MIB / ACH_MAX_EXTRACTED_ARTIFACT_MIB / ACH_MAX_ARCHIVE_ENTRIES) with the operator-side ACH_PLUGIN_MAX_SIZE_MIB validation discipline"
  - "ResourceKind enum (KindPlugin / KindArtifact / KindPrompt) — exhaustive for switch statements downstream"
  - "Sentinel errors: ErrUnsafeTarEntry (SAFE-01), ErrBombCapExceeded (SAFE-03), ErrTooManyEntries (SAFE-03 entry-count axis)"
  - "test/fixtures/malicious-archives — Go package emitting 8 deterministic .tar.gz fixtures via BuildAll(dir), one per SAFE-01 rejection class; reusable from the W4 e2e suite"
affects:
  - 07-W2-02 (extract/stage.go — wraps Extract behind the per-resource staging dir + atomic rename publication)
  - 07-W2-03 (extract/autoclaim.go — feeds bytes-on-disk hashes from extract.Result.WrittenFiles into the three-tier collision cascade)
  - 07-W3-01..05 (adapters — consume Limits + ResourceKind via the engine's pre-stage prep)
  - 07-W4-01 (e2e cli_hydrate_engine_test.go — sc4_safe_extract_malicious imports maliciousfixtures.BuildAll into a temp dst)

# Tech tracking
tech-stack:
  added: []  # stdlib only — archive/tar + compress/gzip already in std
  patterns:
    - "Hand-rolled SAFE-01..06 tar policy over stdlib archive/tar + compress/gzip (D-11) — no third-party tar library introduced"
    - "capWriter pattern (io.Writer wrapper with per-entry byte counter) intercepts writes BEFORE bytes hit the underlying file, making SAFE-03 bomb-defense ordering structurally observable"
    - "O_EXCL on extracted regular files refuses to overwrite anything — the W2-02 staging layer guarantees fresh dst so existing-file means malicious-archive trying to clobber"
    - "Raw-byte tar-header builder for the pax_injection fixture — stdlib tar.Writer refuses TypeXHeader on manual writes AND silently deduplicates structured PAXRecords['path'] against USTAR-fitting Name; only a hand-crafted 512-byte header reproduces the real wire-format attack"
    - "External _test package (extract_test) consuming the public API only — keeps the contract honest, matches internal/credhash_test + internal/cli/hash_test discipline"

key-files:
  created:
    - "internal/cli/extract/doc.go — package contract citing SAFE-01..06, D-11, D-12, CLAUDE.md 'Decompression-bomb caps' failure mode, and the count-as-stream bomb-defense-ordering rule"
    - "internal/cli/extract/limits.go — ResourceKind enum + Limits struct + DefaultLimits + LoadLimits + MaxBytesForKind helper"
    - "internal/cli/extract/limits_test.go — 9 unit tests (defaults, every rejection class, MiB→bytes, per-kind routing, DefaultLimits literal-D12 invariant)"
    - "internal/cli/extract/tar.go — Extract + capWriter + checkSafeRel + maskMode + writeRegular + writeSymlink + paxInjectedPath; sentinel errors"
    - "internal/cli/extract/tar_test.go — 16 unit tests (SAFE-01 fixture iteration, happy-path, mode-mask, bomb-cap-trip-file-not-written, too-many-entries, symlink-in-tree, symlink-escape-rejected, KindPrompt no-cap, gzip reader error)"
    - "test/fixtures/malicious-archives/fixtures.go — maliciousfixtures.BuildAll(dir) + Names + raw-byte PAX builder"
    - "test/fixtures/malicious-archives/generator/main.go — thin wrapper around BuildAll for manual fixture materialization on disk"
    - "test/fixtures/malicious-archives/README.md — fixture table, programmatic usage, manual-regen command, references"
  modified: []

key-decisions:
  - "Re-hash freshly-written file via os.Open + hash.Hash AFTER io.Copy completes, rather than teeing through io.MultiWriter during write. Trade-off: extra disk read per file, but the io.Copy path stays tight (single writer through capWriter, no extra concurrency surface), and on staged dirs (RWO PVC / local SSD) the re-read is cache-hot. The streaming-tee alternative would require hash.New as io.Writer which the W1-04 surface deliberately doesn't expose (Hash takes io.Reader so the contract is unambiguous about source-vs-sink); rewriting hash to expose a Writer just to save one read is an over-fit."
  - "capWriter checks 'written + len(p) > maxBytes' BEFORE the underlying Write — guaranteeing that on bomb-cap trip, ZERO bytes from the offending entry land on disk. The TestExtract_BombCapTrip_FileNotWritten test asserts the file does not exist (errors.Is fs.ErrNotExist) after the cap fires, which is the strong invariant. The alternative (post-write check with truncate-on-overage) leaves a window where partial bytes are observable to a concurrent scanner."
  - "tar.TypeRegA case removed in favor of TypeReg-only switch arm — staticcheck SA1019 deprecation. archive/tar.Reader normalizes pre-USTAR null-byte typeflag entries to TypeReg internally before returning, so the case was redundant anyway."
  - "Symlink target-escape check uses LEXICAL resolution (filepath.Clean of Linkname relative to symlink's parent dir), NOT filepath.EvalSymlinks. EvalSymlinks would follow real-fs links the caller cannot have planted yet at extraction time, AND any pre-existing symlink in dst (impossible in W2-02 fresh staging but possible in misuse) could change semantics. Lexical check is closed-form against the wire-format-only target."
  - "pax_injection fixture is the ONE fixture that bypasses tar.Writer and writes raw 512-byte headers. Reason: stdlib tar.Writer (a) returns 'cannot manually encode TypeXHeader' on manual WriteHeader calls, and (b) when a regular entry carries PAXRecords['path']='/etc/escape' but Name fits USTAR (≤100 chars), the writer silently drops the path record as redundant — so neither approach can reproduce the real wire-format attack. The 512-byte header layout (ustar magic, octal-encoded numeric fields, checksum = sum of bytes with checksum field treated as 8 spaces) is straight POSIX.1-2001."
  - "Tests build fixtures into t.TempDir() instead of committing .tar.gz blobs. Keeps the tree lean, no large binaries in `git log`, and a refactor of fixtures.go cannot silently leave stale fixtures behind."

patterns-established:
  - "Pattern A: capWriter — io.Writer wrapper with cumulative byte counter that fails BEFORE the underlying writer is touched. Apply to any streaming pipeline where SAFE-style per-entry caps must be observable BEFORE bytes hit the destination."
  - "Pattern B: Raw-byte tar-header builder for adversarial wire-format tests. Apply to any future fixture that the stdlib tar.Writer deduplicates or refuses (TypeGNULongName, TypeGNULongLink, sparse headers, custom magic strings)."
  - "Pattern C: External _test package (extract_test) on public-API-only consumption. Apply to every new internal/cli/<pkg>/ in Phase 7+ — keeps the contract honest, matches established credhash + hash discipline."

requirements-completed:
  - SAFE-01
  - SAFE-02
  - SAFE-03
  - SAFE-06

# Note: SAFE-01..03 + SAFE-06 are FULLY addressed in their per-extract semantics
# (rejection set, mode masking, bomb cap, streaming gzip). The W2-02 staging
# layer adds the "partial output discarded" cross-resource invariant for SAFE-03;
# the W2-03 autoclaim layer adds SAFE-04 (CollisionRefuse) and SAFE-05 (tmp sweep).

# Metrics
duration: ~23min
completed: 2026-05-29
---

# Phase 7 Plan 07-W2-01: internal/cli/extract — Safe Tar Policy + Bomb Defense Summary

**Shipped `internal/cli/extract`, the single safe-extraction surface in Phase 7: SAFE-01..06 hand-rolled over stdlib `archive/tar` + `compress/gzip`, with the bomb-defense byte counter (`capWriter`) intercepting writes BEFORE bytes hit disk and a deterministic Go fixture package (`maliciousfixtures.BuildAll`) emitting 8 `.tar.gz` exemplars covering every SAFE-01 rejection class.**

## Performance

- **Duration:** ~23 min
- **Started:** 2026-05-29T14:34:32Z (worktree spawn)
- **Completed:** 2026-05-29 (Task 2 commit)
- **Tasks:** 2 (both `auto`/`tdd=true`)
- **Files created:** 8 (3 source + 2 test + doc.go + fixtures.go + generator/main.go + README.md)
- **Files modified:** 0
- **Tracked commits:** 2 (`d76d08d`, `c24b806`)
- **Tests:** 25 unit tests passing (`./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...`)
- **Lint:** clean (`./bin/golangci-lint run ./internal/cli/extract/... ./test/fixtures/malicious-archives/...`)

## Accomplishments

- `internal/cli/extract.Extract(ctx, gzReader, dst, kind, limits, allowSymlinks) (Result, error)` is the package's single public extraction entry point.
- Tar policy unconditionally rejects: absolute paths (`/etc/passwd`), `..` segments, paths normalized outside `dst`, hardlinks (`TypeLink`), device files (`TypeChar` / `TypeBlock`), FIFOs (`TypeFifo`), sockets / any unknown typeflag, and pax-extended-header path injections (`TypeXHeader` / `TypeXGlobalHeader` whose embedded `path` fails the same SAFE-01 path-safety check).
- Symlinks rejected by default; `allowSymlinks=true` admits ONLY in-tree-resolved symlinks (the resolved target MUST live inside `dst`). Out-of-tree symlinks remain rejected even with the opt-in.
- File modes masked to `mode & 0o0755`; setuid/setgid/sticky/group-write/world-write unconditionally stripped per SAFE-02. Directory modes forced to `0o0755`. Mtime/atime NOT preserved.
- `Limits.MaxBytesForKind(kind)` enforced per-entry as bytes stream via `capWriter` — the cap-trip happens BEFORE the underlying writer is touched per CLAUDE.md "Decompression-bomb caps" failure-mode discipline. `Limits.MaxEntries` enforced BEFORE reading the offending entry's body. Partial output removed on failure.
- Gzip streamed via `compress/gzip.NewReader` wrapping `archive/tar.NewReader` — the archive is NEVER fully buffered (SAFE-06 structurally enforced by the stream-through-stream architecture).
- `limits.go` reads three env vars (`ACH_MAX_EXTRACTED_PLUGIN_MIB` default 200, `ACH_MAX_EXTRACTED_ARTIFACT_MIB` default 500, `ACH_MAX_ARCHIVE_ENTRIES` default 65536), rejects zero/negative/non-numeric values with a clear error citing the offending var name. Matches the operator-side `ACH_PLUGIN_MAX_SIZE_MIB` validation discipline.
- `test/fixtures/malicious-archives/fixtures.go` exports `BuildAll(dir) (map[string]string, error)` materializing 8 deterministic `.tar.gz` files into the supplied directory — one per SAFE-01 class (absolute, dotdot, symlink default, symlink escape, hardlink, device, fifo, pax-injection). All headers carry `Uid=0/Gid=0/ModTime=time.Unix(0,0).UTC()` so the byte output is reproducible across runs.
- `test/fixtures/malicious-archives/generator/main.go` is a thin wrapper that calls `BuildAll` and prints paths — used for manual fixture inspection / external-verifier debugging.
- 25 unit tests: 9 limits tests + 16 tar tests. The bomb-cap test asserts BOTH the error AND `errors.Is(stat, fs.ErrNotExist)` on the would-be-written path (SAFE-03 ordering structurally observable).

## Task Commits

Each task was committed atomically:

1. **Task 1: Limits env-var parser** — `d76d08d` (`feat`). doc.go + limits.go + limits_test.go. 9 tests pass.
2. **Task 2: tar.go safe-extract policy + SAFE-01 fixtures** — `c24b806` (`feat`). tar.go + tar_test.go + fixtures.go + generator/main.go + README.md. Combined RED/GREEN (see Deviations).

**Plan metadata commit:** N/A — `.planning/` is gitignored at repo level; the SDK `commit` would return `skipped_gitignored`. Per executor-prompt parallel_execution discipline, the SUMMARY.md lives in the main-repo `.planning/` directory and is NOT staged or committed from the worktree.

## Files Created/Modified

### `internal/cli/extract/doc.go` (created)

Package doc citing the load-bearing surface:
- Contract: Extract signature + gzip/tar streaming discipline
- SAFE-01 unconditional rejection set + symlink default-reject + allow-symlinks-in-tree-only
- SAFE-02 mode mask + dir mode + no mtime preservation
- SAFE-03 per-entry byte counter ordering + entry-count check ordering
- SAFE-06 streaming guarantee
- LoadLimits env-var contract + reject-zero/negative/non-numeric
- D-11 stdlib-only discipline; D-12 default values + env-var names
- References: CLI spec §6.4, PRD D-11/D-12, SAFE-01..06, CLAUDE.md "Decompression-bomb caps"

### `internal/cli/extract/limits.go` (created)

- `ResourceKind` typed string + `KindPlugin` / `KindArtifact` / `KindPrompt` constants (values match CLI spec §10 lowercase resource identifiers; doubles as URL path segments for the Content Service consumer)
- `Limits{MaxExtractedPluginBytes int64, MaxExtractedArtifactBytes int64, MaxEntries int}` — byte caps stored as int64 bytes (MiB resolved at LoadLimits) so the streaming counter compares against a single typed scalar
- `(Limits).MaxBytesForKind(kind) int64` — returns 0 for KindPrompt (no cap; prompts are opaque single files)
- `DefaultLimits() Limits` — returns the literal D-12 values (200 / 500 / 65536) for the dry-run path
- `LoadLimits() (Limits, error)` — reads env vars; empty/unset uses default; zero/negative/non-numeric returns an error citing the offending var name
- `loadPositiveInt(key, fallback)` helper kept local (does not pull `internal/config` which is controller-runtime-adjacent)

### `internal/cli/extract/limits_test.go` (created)

9 tests, stdlib `testing` + `t.Setenv` only:
- `TestLoadLimits_Defaults_NoEnvSet` — defaults match D-12 (200 MiB / 500 MiB / 65536)
- `TestLoadLimits_RejectsZero` — `ACH_MAX_EXTRACTED_PLUGIN_MIB=0` errors with var name
- `TestLoadLimits_RejectsNegative` — `ACH_MAX_ARCHIVE_ENTRIES=-1` errors with var name
- `TestLoadLimits_RejectsNonNumeric` — `ACH_MAX_EXTRACTED_ARTIFACT_MIB=abc` errors with var name
- `TestLoadLimits_MiBToBytes` — 10 MiB → 10×1024×1024 bytes
- `TestMaxBytesForKind_Plugin` / `_Artifact` / `_Prompt` — per-kind routing (Prompt returns 0)
- `TestDefaultLimits_LiteralD12` — DefaultLimits() unchanged across refactors

### `internal/cli/extract/tar.go` (created)

- `ErrUnsafeTarEntry` (SAFE-01) / `ErrBombCapExceeded` (SAFE-03 byte-cap) / `ErrTooManyEntries` (SAFE-03 entry-count) sentinel errors
- `FileWrite{RelPath, Hash, Mode}` + `Result{WrittenFiles, BytesWritten, EntriesParsed}` types
- `Extract` flow: gzip.NewReader → tar.NewReader → for-each-entry loop with entry-count check, PAX path injection check, hdr.Name path-safety check (checkSafeRel), typeflag switch (TypeDir → forced 0755 MkdirAll; TypeReg → writeRegular; TypeSymlink → gated by allowSymlinks; TypeXHeader/TypeXGlobalHeader → no-op continue; TypeLink/TypeChar/TypeBlock/TypeFifo → unconditional reject)
- `checkSafeRel` — strict path validation (reject empty, `/`-prefix, IsAbs, `..` segment, resolved-path-escapes-dst with trailing-separator boundary safety)
- `maskMode` — `mode & 0o0755`
- `capWriter` — io.Writer wrapper that returns `ErrBombCapExceeded` BEFORE the underlying writer is touched
- `writeRegular` — O_EXCL|O_CREATE|O_TRUNC open + io.Copy through capWriter + close + re-hash via `internal/cli/hash.Hash`. Partial output removed on any failure (os.Remove).
- `writeSymlink` — LEXICAL target resolution (filepath.Clean of Linkname relative to symlink's parent dir); reject absolute Linkname; reject resolved-target escapes; create via os.Symlink (NOT os.Link)
- `paxInjectedPath` — defensive PAXRecords["path"] readout for global PAX headers that the Reader hasn't merged yet

### `internal/cli/extract/tar_test.go` (created)

16 tests (external `extract_test` package), stdlib `testing` + `archive/tar` + `compress/gzip` + the in-package fixtures helper:
- `TestExtract_MaliciousFixtures` — 10 sub-tests iterating the SAFE-01 fixture set with both allowSymlinks=false and =true permutations; every one asserts `errors.Is(err, ErrUnsafeTarEntry)`
- `TestExtract_HappyPath_OneFile` — "x.txt" → bytes equal input, mode masked, RelPath = "x.txt", hash has "xxh3:" prefix
- `TestExtract_ModeMasked` — input mode `0o4755` → on-disk mode `0o0755` (setuid stripped per SAFE-02)
- `TestExtract_BombCapTrip_FileNotWritten` — 1 KiB body under 512 B cap → ErrBombCapExceeded + `errors.Is(stat, fs.ErrNotExist)` on the would-be-written path (SAFE-03 ordering)
- `TestExtract_TooManyEntries` — 4 entries under MaxEntries=3 → ErrTooManyEntries
- `TestExtract_SymlinkAllowed_InTree` — allowSymlinks=true + in-tree Linkname → admitted; Lstat confirms ModeSymlink
- `TestExtract_SymlinkAllowed_Escape_Rejected` — allowSymlinks=true + `../../etc/passwd` Linkname → ErrUnsafeTarEntry
- `TestExtract_PromptKind_NoLimit` — KindPrompt with 1 MiB body under 1-byte plugin/artifact caps → succeeds; BytesWritten = 1 MiB
- `TestExtract_GzipReaderError` — non-gzip stream → error returned (no silent partial-extract)

### `test/fixtures/malicious-archives/fixtures.go` (created)

`maliciousfixtures.BuildAll(dir)` materializes 8 deterministic `.tar.gz` files. Helper-driven: `detEntry()` returns a baseline header with fixed Uid/Gid/ModTime; per-fixture builders override Name/Typeflag/Linkname/PaxHeaders. The pax_injection fixture uses hand-rolled 512-byte ustar headers because stdlib `tar.Writer` (a) refuses TypeXHeader on manual writes, (b) silently dedups structured `PAXRecords['path']` against USTAR-fitting Name.

### `test/fixtures/malicious-archives/generator/main.go` (created)

Thin wrapper around `BuildAll` for manual fixture materialization. `./scripts/dev.sh go run ./test/fixtures/malicious-archives/generator <dir>` writes the 8 fixtures to `<dir>` and prints the paths.

### `test/fixtures/malicious-archives/README.md` (created)

Fixture table (filename → SAFE-01 class → header detail), programmatic-use example (BuildAll into t.TempDir), manual-regen command, references to CLI spec §6.4 + D-11 + SAFE-01..06.

## Decisions Made

See `key-decisions` in frontmatter. Summary:
- Re-hash freshly-written file (extra disk read) rather than streaming-tee through io.MultiWriter — keeps the write path tight and the W1-04 hash contract honest (Hash takes io.Reader exclusively).
- `capWriter` rejects writes BEFORE touching the underlying file — TestExtract_BombCapTrip_FileNotWritten asserts the file does not exist (fs.ErrNotExist), making SAFE-03 ordering structurally observable.
- `tar.TypeRegA` case removed (staticcheck SA1019); archive/tar normalizes null-byte typeflag to TypeReg internally so the case was redundant.
- Symlink target-escape uses LEXICAL `filepath.Clean(filepath.Join(...))`, NOT `filepath.EvalSymlinks` — wire-format-only check, no real-fs follow.
- pax_injection fixture is the only one using raw 512-byte ustar headers — stdlib tar.Writer cannot reproduce the real wire-format attack.
- Tests build fixtures into `t.TempDir()` (no committed `.tar.gz` blobs in the repo).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 — Bug] Initial pax_injection fixture used `tar.Writer.WriteHeader` with `PAXRecords["path"]="/etc/escape"` + `Format: FormatPAX` — Go's tar.Writer silently drops the record**

- **Found during:** Task 2 first test run
- **Issue:** The first cut of `buildPaxInjection` in `fixtures.go` constructed a regular entry with `Name = "innocent.txt"` and `PAXRecords = {"path": "/etc/escape"}`. When read back by `tar.Reader`, the merged Name was still "innocent.txt" — the Writer dedup'd the PAX record because the Name already fit USTAR (≤100 chars). My Extract correctly accepted "innocent.txt" as safe (it IS safe), and the test failed because no rejection occurred.
- **Investigation:** Reproduced via a 30-line scratch program in `.gocache/scratch/` (cleaned up before commit) — confirmed Go's tar.Writer drops `path` PAX records when redundant against a USTAR-fitting Name. Even with `Format: FormatPAX` explicitly set, the writer's PAX emission code-path checks for value-equality before emitting.
- **Fix (Rule 1 bug — pax_injection fixture didn't test what it claimed to test):** Rewrote `buildPaxInjection` to hand-roll raw 512-byte ustar tar headers (TypeXHeader + body containing `len path=/etc/escape\n`, followed by a regular TypeReg "innocent.txt" entry). Added `paxRecord`, `writeRawTarHeader`, `copyOctalNul`, `padTo512` helpers + `tarTypeReg`/`tarTypeXHeader` constants. After the fix, `tar.Reader` returns the merged header with `Name = "/etc/escape"`, which `checkSafeRel` correctly rejects as an absolute path → `ErrUnsafeTarEntry`.
- **Files modified:** `test/fixtures/malicious-archives/fixtures.go` (rewrote buildPaxInjection + added 4 helpers + 2 constants)
- **Verification:** `TestExtract_MaliciousFixtures/pax_injection` now PASSes with `errors.Is(err, ErrUnsafeTarEntry)` true. All 16 tar tests + 9 limits tests green.
- **Committed in:** `c24b806` (Task 2 commit — both buildPaxInjection broken-then-fixed in same atomic change, no intermediate broken state landed)

**2. [Rule 1 — Bug] `tar.TypeRegA` constant deprecation tripped golangci-lint SA1019**

- **Found during:** Task 2 lint pass
- **Issue:** First cut of `tar.go` had `case tar.TypeReg, tar.TypeRegA:` for the regular-file arm. `tar.TypeRegA` ('\x00') has been deprecated since Go 1.11 in favor of `tar.TypeReg` — archive/tar.Reader normalizes the null-byte typeflag to TypeReg before returning, so the case was always redundant. golangci-lint's staticcheck SA1019 flagged it.
- **Fix:** Removed `tar.TypeRegA` from the case label; added an explanatory comment noting the Reader does the normalization internally.
- **Files modified:** `internal/cli/extract/tar.go` (one-line case-label edit + 4 lines of comment)
- **Verification:** `./bin/golangci-lint run ./internal/cli/extract/...` clean; tests still 25/25 green.
- **Committed in:** `c24b806` (Task 2 — caught during the pre-commit lint pass, fixed before commit landed)

**3. [Rule 3 — Blocking] TDD RED commit collapsed into GREEN per project convention**

- **Found during:** Task 1 + Task 2 commit steps
- **Issue:** Both tasks were `tdd="true"` in the plan; the standard executor flow expects a separate `test(...)` RED commit before the `feat(...)` GREEN commit. But the project's `pre-commit` hook runs `make unit` which includes `go vet` over the entire tree; a failing-to-compile RED test (referencing yet-unimplemented `extract.Limits`, `extract.Extract`, etc.) trips the vet gate. CLAUDE.md explicitly forbids `--no-verify` ("If a gate fails, fix the root cause — never `--no-verify` or otherwise bypass"). Same pattern as 07-W1-01 and 07-W1-04 Summaries.
- **Fix:** Collapsed RED + GREEN into a single atomic `feat(...)` commit per task. TDD discipline preserved procedurally: in each task I wrote `*_test.go` first, then ran `./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...` and confirmed the expected build failure (`undefined: extract.Limits`, then later `undefined: extract.Extract`), THEN wrote the implementation, THEN re-ran the tests to GREEN.
- **Files modified:** None (workflow change, not code change)
- **Verification:** Each task's tests are present in the same commit as the implementation, and the unit-test run after the commit returns 0 with the expected number of tests passing.
- **Committed in:** N/A (workflow discipline, not a file change)

---

**Total deviations:** 3 auto-fixed (2 Rule 1 bugs caught pre-commit; 1 Rule 3 blocking-workflow per project convention)
**Impact on plan:** Plan intent delivered verbatim. The pax_injection fix tightens (does not loosen) the SAFE-01 coverage — the fixture now actually exercises the wire-format attack the spec calls out, not a structurally-clean Header that tar.Writer optimizes away.

## Issues Encountered

- **`./scripts/dev.sh make lint-changed` does not include untracked-but-unstaged files.** It diffs against `origin/main` via `git diff --name-only`, so a brand-new package (untracked directory) is invisible. Worked around by running `./bin/golangci-lint run ./internal/cli/extract/... ./test/fixtures/malicious-archives/...` directly inside the devtools container. This was the path that surfaced the SA1019 TypeRegA deprecation (Deviation 2).
- **Debug scratch files in `.gocache/scratch/` — cleaned up before committing.** Used during pax_injection investigation; the directory was created once, used to reproduce the tar.Writer deduplication behavior, then `rm -rf`'d before staging. `.gocache/` is already in `.gitignore` (under the standard Go-cache pattern) so no accidental leakage risk; cleanup was a hygiene step.

## User Setup Required

None — no external service configuration. All changes are repo-internal Go code + test fixtures.

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/extract/doc.go ]                                    → FOUND
[ -f internal/cli/extract/limits.go ]                                 → FOUND
[ -f internal/cli/extract/limits_test.go ]                            → FOUND
[ -f internal/cli/extract/tar.go ]                                    → FOUND
[ -f internal/cli/extract/tar_test.go ]                               → FOUND
[ -f test/fixtures/malicious-archives/fixtures.go ]                   → FOUND
[ -f test/fixtures/malicious-archives/generator/main.go ]             → FOUND
[ -f test/fixtures/malicious-archives/README.md ]                     → FOUND

# Commits exist
git log --oneline | grep d76d08d                                       → FOUND ("feat(07-W2-01): add internal/cli/extract limits + package doc")
git log --oneline | grep c24b806                                       → FOUND ("feat(07-W2-01): add safe-tar Extract + SAFE-01 fixture set")

# Plan-level verification gates
grep -q "type Limits struct" internal/cli/extract/limits.go            → OK
grep -q "func LoadLimits" internal/cli/extract/limits.go               → OK
grep -qE "ACH_MAX_EXTRACTED_PLUGIN_MIB|ACH_MAX_EXTRACTED_ARTIFACT_MIB|ACH_MAX_ARCHIVE_ENTRIES" internal/cli/extract/limits.go → OK
grep -qE "200|500|65536" internal/cli/extract/limits.go                → OK
grep -q "func Extract" internal/cli/extract/tar.go                     → OK
grep -E "ErrUnsafeTarEntry|ErrBombCapExceeded|ErrTooManyEntries" internal/cli/extract/tar.go | grep -c errors.New → 3
grep -cE "TypeLink|TypeChar|TypeBlock|TypeFifo|TypeXHeader" internal/cli/extract/tar.go → 4 (all SAFE-01 classes referenced across the typeflag-switch arms)
grep -q "allowSymlinks" internal/cli/extract/tar.go                    → OK
grep -q "compress/gzip" internal/cli/extract/tar.go                    → OK
grep -q "archive/tar" internal/cli/extract/tar.go                      → OK
grep -qE "TestExtract_BombCapTrip_FileNotWritten|TestExtract_ModeMasked" internal/cli/extract/tar_test.go → OK

# Behavior gates
./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...          → exit 0, 25 tests pass
./scripts/dev.sh bash -c "./bin/golangci-lint run ./internal/cli/extract/... ./test/fixtures/malicious-archives/..." → clean
./scripts/dev.sh go vet ./internal/cli/extract/... ./test/fixtures/malicious-archives/... → clean
```

## Self-Check: PASSED

## Next Phase Readiness

- **07-W2-02 (staging) unblocked** — can now `import "github.com/ackstorm/ach/internal/cli/extract"` and call `extract.Extract(ctx, gzReader, stagingDir, kind, limits, allowSymlinks)` to materialize a resource into a fresh staging directory. The staging layer wraps Extract with the per-resource tmp dir + atomic rename publication per SAFE-05.
- **07-W2-03 (autoclaim) unblocked** — can consume `extract.Result.WrittenFiles[i].Hash` (canonical "xxh3:" digests) as the "bytes-on-disk" input to the three-tier collision cascade (eager → adapter → lazy source).
- **07-W3-01..05 (adapters) unblocked** — `extract.Limits` + `extract.ResourceKind` are stable; adapter pre-stage prep can construct/route them without further extract-package edits.
- **07-W4-01 (e2e) unblocked** — the e2e safe-extract subtest can `import maliciousfixtures` and call `BuildAll(t.TempDir())` to feed the full SAFE-01 fixture set through the engine.

No blockers for downstream waves. The `internal/cli/extract` surface is closed: Extract is the only public entry point, and the SAFE-01..06 policy is structurally enforced (no exposed knob that could weaken the policy by accident).

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
