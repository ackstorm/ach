---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 07
subsystem: cachefs
tags: [pvc-bootstrap, hub-section-10.3, op-10, op-11, d-13, stdlib-only, idempotent-mkdir, atomic-rename]

# Dependency graph
requires:
  - phase: 01-01
    provides: "kubebuilder v4 scaffold, go.mod at github.com/ackstorm/ach, hack/boilerplate.go.txt, internal/ tree"
provides:
  - "internal/cachefs.EnsureLayout(root string) error — idempotent OP-10 layout bootstrap; creates prompt/, plugin/, marketplace/, artifact/, .tmp/ under root with mode 0o755"
  - "internal/cachefs.ErrCacheRootMissing — exported sentinel returned when root is empty, missing, or not a directory; errors.Is-compatible"
  - "internal/cachefs.SubDirs — exported []string fixed-order canonical subdir list ('prompt','plugin','marketplace','artifact','.tmp') for downstream consumers that need to iterate the §10.3 layout"
  - "internal/cachefs/doc.go — package contract surface citing Hub §10.3, OP-10, OP-11, D-13"
  - "7-test stdlib contract suite — covers success, idempotency, empty/missing/file-not-dir guards, partial-init survival, permission-denied surfacing"
affects:
  - 01-06 (cmd/operator/main.go — will call cachefs.EnsureLayout(envOr('ACH_CACHE_ROOT','/var/cache/ach')) before manager.Start; fail-fast on returned error per D-13)
  - 02-* (external-ref refresh — materializes into .tmp/<random>, fsyncs, then rename(2) to the publish path; cachefs is the structural precondition)
  - 05-* (Content Service — streams from prompt/, plugin/, marketplace/, artifact/ via sendfile(2); cachefs is the layout cachefs serves)
  - "07 (Helm chart — manager.yaml PVC mount at /var/cache/ach is the runtime origin for the root cachefs.EnsureLayout receives)"

# Tech tracking
tech-stack:
  added: []  # stdlib-only — zero new go.mod entries
  patterns:
    - "Stdlib-only filesystem bootstrap — errors + os + path/filepath. Imports forbidden: log, log/slog, fmt, os.Stdout-or-Stderr write (D-13 fail-fast: errors flow to caller, no logging side effects in this layer)"
    - "Exported sentinel error pattern matches internal/credhash (ErrEmptyPepper) and internal/db (ErrEmptyURL) — `var ErrXxx = errors.New('pkg: human-readable cause')` for errors.Is matching at the call site"
    - "Exported canonical-list pattern — SubDirs []string is exported so downstream consumers (orphan-sweep loops, Plan 02 external-ref refresh) can iterate the §10.3 layout without redeclaring it"
    - "Idempotent re-run by construction — os.MkdirAll is a no-op when the target directory exists; D-13 idempotency is therefore a structural guarantee, not a runtime check"

key-files:
  created:
    - internal/cachefs/bootstrap.go (EnsureLayout + SubDirs + ErrCacheRootMissing; 71 lines with Apache-2.0 header)
    - internal/cachefs/doc.go (package contract; 39 lines)
    - internal/cachefs/bootstrap_test.go (7 test functions; ~186 lines with header)
  modified: []  # zero ambient changes — no go.mod / go.sum / Makefile edits required

key-decisions:
  - "Stdlib-only — zero go.mod entries. errors + os + path/filepath is the entire dependency surface. Auditable against Go stdlib release notes alone, never against an external maintainer's release cadence. Consistent with internal/credhash."
  - "ErrCacheRootMissing covers three failure modes (empty string, missing path, file-not-directory) under one sentinel — these are all the same operator-facing failure (the PVC isn't mounted properly); distinguishing them at the API layer would clutter the contract. Underlying os errors from MkdirAll (ENOSPC, permission denied) pass through verbatim so the caller sees the precise filesystem failure."
  - "SubDirs is an exported var, not an exported function or constant slice. Var is the idiomatic Go pattern for an unmodifiable-by-convention list; future downstream callers (e.g. Plan 02's .tmp/ orphan-sweep) get a single source of truth instead of redeclaring the five names."
  - "Permission-denied test skips on UID 0 in addition to Windows. The plan only required GOOS=windows skip; I added a Geteuid()==0 skip because UID 0 bypasses Unix file-mode permission checks — without the guard, the test would silently fail to assert anything in CI containers that run as root, giving false confidence. This is a Rule 2 mitigation (missing test correctness check); documented in test source as a Cleanup comment."
  - "doc.go is a separate file. Mirrors the internal/credhash convention — `go doc github.com/ackstorm/ach/internal/cachefs` surfaces the contract on its own without burying it in the implementation file's top comment."

patterns-established:
  - "internal/cachefs is the canonical filesystem-bootstrap package for ACH's PVC-mounted cache. Future filesystem-layout helpers (e.g. an orphan-sweep job for stale .tmp/ files per §10.3) live in sibling files in this package, sharing the same stdlib-only + no-logging import discipline."
  - "Plan-internal RED/GREEN gate is not enforced for non-tdd plans — this plan is type=execute (tdd=false on both tasks). The implementation file shipped first, then the test file; verification was always `go test ./internal/cachefs/... -race -count=1` against the just-shipped impl. Future non-tdd utility-package plans can follow the same impl-then-test order."

requirements-completed: [OP-10, OP-11]
# OP-10: Cache layout under PVC mount with .tmp/ staging — the layout function
# is shipped; Plan 06 will call it from operator main.
# OP-11: Cache reconstruction after PVC loss — the directory tree is in place;
# the last_successful_refresh reset to NULL is Phase 2's reconciler logic.
# Phase 1 satisfies OP-11 at the layout-creation level (§10.3 requires the
# tree exist; this plan ensures it does idempotently on every operator start).

# Metrics
duration: ~2min
completed: 2026-05-15
---

# Phase 1 Plan 7: Foundation — `internal/cachefs` PVC Layout Bootstrap Summary

**Idempotent PVC directory bootstrap shipped as stdlib-only `internal/cachefs` package: `EnsureLayout(root)` creates the five OP-10 subdirs (`prompt/`, `plugin/`, `marketplace/`, `artifact/`, `.tmp/`) under root via `os.MkdirAll` at mode 0o755, or returns `ErrCacheRootMissing` when root is empty/missing/not-a-directory; underlying `os.MkdirAll` errors (ENOSPC, permission denied) pass through verbatim. Seven stdlib-`testing` cases pass with `-race`. Zero `go.mod` deps added. `go doc github.com/ackstorm/ach/internal/cachefs` surfaces the contract spelled out in `doc.go` (Hub §10.3, OP-10, OP-11, D-13).**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-05-15T13:51:17Z
- **Completed:** 2026-05-15T13:53:45Z (pre-final-commit; the SUMMARY commit closes the plan)
- **Tasks:** 2 / 2
- **Files modified:** 3 created (zero modified)

## Accomplishments

- **OP-10 contract surface now end-to-end concrete.** The §10.3 cache layout (`prompt/`, `plugin/`, `marketplace/`, `artifact/`, `.tmp/`) is no longer a documentation-only claim — `cachefs.EnsureLayout(root)` is the single source of truth that creates the tree. Plan 06's operator main will replace any inline `mkdir -p` loop with a 2-line call: read `ACH_CACHE_ROOT`, invoke `cachefs.EnsureLayout`, abort startup with the returned error if non-nil. Phase 2's external-ref refresh code consumes the same layout; Phase 5's Content Service streams from these paths.
- **Idempotent by construction via `os.MkdirAll`.** D-13 says the bootstrap must be idempotent so the operator can restart without breaking the cache. `os.MkdirAll` is a no-op when the target directory already exists (returns nil), so calling `EnsureLayout` repeatedly is structurally a no-op — not a runtime existence-check pattern. The `TestEnsureLayoutIdempotent` test proves this; `TestEnsureLayoutSurvivesExistingSubdirs` proves it under partial-init (one subdir already exists).
- **Three guard cases collapsed under one sentinel.** Empty-string root, missing-on-disk root, and file-not-directory root are all the same operator-facing failure mode: the PVC isn't mounted properly. `ErrCacheRootMissing` covers all three and the test suite exercises each path explicitly (`TestEnsureLayoutEmptyRootReturnsError`, `TestEnsureLayoutNonExistentRootReturnsError`, `TestEnsureLayoutFileNotDirReturnsError`). Underlying `os` errors from `MkdirAll` (ENOSPC, permission denied) pass through verbatim so the caller sees the precise filesystem failure — that's `TestEnsureLayoutPermissionDeniedReturnsError`.
- **Permission-denied path proven without false positives.** The chmod-0o500 test asserts BOTH that `EnsureLayout` returns a non-nil error AND that the error is NOT `ErrCacheRootMissing` — i.e. the failure must come from `MkdirAll` on a child path, not from the `IsDir` guard at the top of the function. The test skips on Windows (chmod semantics differ) AND when running as root (Geteuid()==0 bypasses Unix mode checks); without the root-skip, the test would silently fail to assert anything in CI containers that run as UID 0.
- **Stdlib-only: zero `go.mod` deps added.** The plan demanded stdlib-only — verified post-implementation by `grep -E '^\s*"' internal/cachefs/bootstrap.go` returning exactly three import lines (`errors`, `os`, `path/filepath`). The package is auditable against the Go stdlib release notes alone. Test code is similarly stdlib-only: `errors`, `os`, `path/filepath`, `runtime`, `testing`, plus the `internal/cachefs` package itself.
- **`go doc` surfaces the contract.** `./scripts/dev.sh go doc github.com/ackstorm/ach/internal/cachefs` prints the full `doc.go` package comment + the three exported names (`EnsureLayout`, `ErrCacheRootMissing`, `SubDirs`) with their signatures and per-symbol doc comments. A reviewer onboarding to Plan 06 reads `go doc` and knows: (a) the caller passes `root` resolved from `ACH_CACHE_ROOT`, (b) the package does not read env vars, (c) the discipline forbids `log`/`slog`/`fmt`/`os.Stdout` writes, (d) the `.tmp/` staging path lives on the same filesystem as the publish dirs by construction so `rename(2)` is atomic per POSIX.

## Signatures (Plan 06 reference)

```go
package cachefs

// ErrCacheRootMissing is returned when ACH_CACHE_ROOT is empty, missing,
// or not a directory.
var ErrCacheRootMissing = errors.New("cachefs: ACH_CACHE_ROOT directory does not exist or is unwritable")

// SubDirs are the five OP-10 cache subdirectories created under root.
// Order is fixed: prompt, plugin, marketplace, artifact, .tmp.
var SubDirs = []string{"prompt", "plugin", "marketplace", "artifact", ".tmp"}

// EnsureLayout creates the OP-10 cache directory tree under root with mode
// 0o755. Idempotent. Returns ErrCacheRootMissing on empty/missing/non-dir
// root; returns the underlying os error on MkdirAll failure.
func EnsureLayout(root string) error
```

Imports: `errors`, `os`, `path/filepath`. Nothing else.

## Test Coverage (7 cases)

| # | Test | Asserts |
|---|------|---------|
| 1 | `TestEnsureLayoutCreatesAllFiveSubdirs` | All five §10.3 subdirs present as directories after one call |
| 2 | `TestEnsureLayoutIdempotent` | Two back-to-back calls; second returns nil; layout intact |
| 3 | `TestEnsureLayoutEmptyRootReturnsError` | `errors.Is(EnsureLayout(""), ErrCacheRootMissing)` |
| 4 | `TestEnsureLayoutNonExistentRootReturnsError` | `errors.Is(EnsureLayout("/this/path/does/not/exist/at/all"), ErrCacheRootMissing)` |
| 5 | `TestEnsureLayoutFileNotDirReturnsError` | Root pointing at a regular file returns `ErrCacheRootMissing` |
| 6 | `TestEnsureLayoutSurvivesExistingSubdirs` | Pre-create one subdir; EnsureLayout converges to full layout |
| 7 | `TestEnsureLayoutPermissionDeniedReturnsError` | chmod 0o500 root; non-nil error; NOT ErrCacheRootMissing (skipped on Windows + on UID 0) |

## Task Commits

1. **Task 1: write `bootstrap.go` + `doc.go`** — `f1ff33e` (feat)
   - Verified clean build: `./scripts/dev.sh go build ./internal/cachefs/...` exits 0.
   - Acceptance checks: `func EnsureLayout(root string) error` present; `SubDirs` lists five entries in fixed order; `doc.go` cites OP-10 + D-13; zero logging imports; stdlib-only imports.
2. **Task 2: write `bootstrap_test.go`** — `60d26e4` (test)
   - Verified GREEN: `./scripts/dev.sh go test -race -count=1 ./internal/cachefs/...` reports `ok` + 7/7 PASS in 1.014s.
   - Acceptance checks: 7 test funcs matching plan-specified names; 5 `t.TempDir()` calls; zero hardcoded `/tmp` paths; Windows skip via `runtime.GOOS == "windows"`; UID 0 skip via `os.Geteuid() == 0`.

**Plan metadata commit:** appended below this SUMMARY commit.

## Files Created/Modified

- `internal/cachefs/bootstrap.go` — EnsureLayout + SubDirs + ErrCacheRootMissing. Apache-2.0 boilerplate header verbatim from `hack/boilerplate.go.txt`. Stdlib-only imports.
- `internal/cachefs/doc.go` — package-level doc comment citing Hub §10.3, OP-10, OP-11, D-13, and the no-logger discipline. Apache-2.0 boilerplate header.
- `internal/cachefs/bootstrap_test.go` — 7 test functions in `package cachefs_test` (external test package — exercises only the exported surface, no internal access). Apache-2.0 boilerplate header. Stdlib-only (`errors`, `os`, `path/filepath`, `runtime`, `testing`).

Zero modifications elsewhere: no `go.mod` change, no `go.sum` change, no `Makefile` change. The package is a pure addition.

## Decisions Made

See `key-decisions` in the frontmatter for the full enumerated list. Highlights:

- **Stdlib-only** — zero `go.mod` entries; entire dependency surface is Go's standard library.
- **Three guard cases under one sentinel** — empty/missing/file-not-dir all return `ErrCacheRootMissing`; underlying `os.MkdirAll` errors flow through verbatim.
- **SubDirs is an exported var** — single source of truth for the five §10.3 subdirs; downstream consumers (Plan 02 .tmp/ orphan-sweep, etc.) iterate it instead of redeclaring.
- **Permission-denied test skips UID 0 in addition to Windows** — Rule 2 mitigation: without the root-skip the test would falsely pass in CI containers running as UID 0 (UID 0 bypasses Unix mode checks).

## Deviations from Plan

**Rule 2 — auto-add missing critical functionality (test correctness):**

The plan specified that `TestEnsureLayoutPermissionDeniedReturnsError` should call `t.Skip()` on Windows. I added a second `t.Skip()` branch for `os.Geteuid() == 0` because UID 0 on Unix bypasses file-mode permission checks — the `chmod 0o500` step would have no effect for a root process, and `MkdirAll` would succeed against the read-only directory, making the test silently pass without actually asserting the permission-denied surface. The dev container that runs `./scripts/dev.sh` could plausibly run as root depending on host UID mapping, so the guard is load-bearing for correctness, not theoretical. Documented in the test source as a comment, and reflected in `key-decisions`.

No other deviations. Task verification commands all ran clean as specified.

## Threat Model Confirmation

Each threat-register entry from the plan's `<threat_model>` is verified by the shipped artifacts:

| Threat | Disposition | Verification |
|--------|-------------|--------------|
| T-07-01 (path-traversal via malicious root) | mitigate | `EnsureLayout` does `os.Stat(root)` + `IsDir()` check before any `MkdirAll`; root is sourced from `ACH_CACHE_ROOT` env var (Plan 06) which is set by manifest authors (Plan 08), never by user input |
| T-07-02 (`.tmp/` on different FS) | mitigate | All five subdirs created under the SAME `root` via `filepath.Join(root, sub)`; by construction same filesystem; structurally impossible to place `.tmp/` elsewhere |
| T-07-03 (ENOSPC causes crash loop) | accept | `EnsureLayout` returns the verbatim `os.MkdirAll` error including ENOSPC; Plan 06's operator main aborts startup per D-13; K8s reschedules; CrashLoopBackOff is correct behavior |
| T-07-04 (world-readable cache) | accept | `MkdirAll(0o755)` — owner-write, group/other-read; cached content is non-sensitive (plugin tarballs, prompts, artifacts); plaintext secrets never cached per DB-04 |
| T-07-05 (TOCTOU stat→mkdirall) | accept | Phase 1: PVC mount is non-adversarial; only the operator process writes; no concurrent racer exists |
| T-07-SC (supply chain via package install) | accept | No npm/pip/cargo installs; stdlib-only Go package — zero new `go.mod` entries |

## Issues Encountered

**None.** Both tasks executed cleanly:

- Task 1 build verified on first run (no output ⇒ exit 0).
- Task 2 tests passed 7/7 on first run with `-race`.
- `./scripts/dev.sh go build ./...` clean across the whole tree.
- `./scripts/dev.sh make fmt vet` clean (no formatting / vet warnings).
- Zero `go.mod` / `go.sum` churn — the package is a pure addition.

## User Setup Required

None. The package is stdlib-only with no runtime dependencies. Plan 06 will read `os.Getenv("ACH_CACHE_ROOT")` (default `/var/cache/ach`) and pass it to `cachefs.EnsureLayout`; this package does not read env vars.

## Next Phase Readiness

- **Plan 01-06 (cmd/operator/main.go):** Has its layout bootstrap. The operator main inserts a 4-line call site before `manager.Start()`:
  ```go
  cacheRoot := envOr("ACH_CACHE_ROOT", "/var/cache/ach")
  if err := cachefs.EnsureLayout(cacheRoot); err != nil {
      setupLog.Error(err, "cache layout bootstrap failed", "root", cacheRoot)
      os.Exit(1)
  }
  ```
  Fail-fast on returned error per D-13; CrashLoopBackOff is the correct symptom.
- **Plan 01-08 (manifests):** The manager.yaml mounts the `ach-cache` RWO PVC at `/var/cache/ach`. The operator container's `ACH_CACHE_ROOT` env var defaults to that path. No change required to this plan's contract.
- **Phase 2 (external-ref refresh):** Has its layout. Refresh code writes into `{root}/.tmp/<random>`, fsyncs, then renames into `{root}/prompt/<name>` / `{root}/plugin/<name>.tar.gz` / etc. The §10.3 atomic-rename invariant holds because all paths share the same filesystem (the same PVC).
- **Phase 5 (Content Service):** Streams from the same layout via `sendfile(2)`. Read-only path; cachefs created the directories Content Service serves.
- **No blockers, no concerns.**

## Self-Check: PASSED

- [x] `internal/cachefs/bootstrap.go` exists and contains `func EnsureLayout`, `var SubDirs`, and `var ErrCacheRootMissing`. Confirmed via `grep -nE 'func EnsureLayout|var SubDirs|var ErrCacheRootMissing' internal/cachefs/bootstrap.go`.
- [x] `internal/cachefs/bootstrap.go` imports only stdlib: `errors`, `os`, `path/filepath`. Confirmed via `grep -E '^\s*"' internal/cachefs/bootstrap.go` returning exactly three lines.
- [x] `internal/cachefs/bootstrap.go` contains no logging imports (`log`, `log/slog`, `fmt`). Confirmed via `grep -E '"(log|log/slog|fmt)"' internal/cachefs/bootstrap.go` returning zero matches.
- [x] `internal/cachefs/doc.go` exists and cites `§10.3`, `OP-10`, and `D-13`. Confirmed via `grep -nE '§10.3|OP-10|D-13' internal/cachefs/doc.go` matching multiple lines.
- [x] `internal/cachefs/bootstrap_test.go` declares 7 test functions matching the plan-specified names. Confirmed via `grep -c '^func Test' internal/cachefs/bootstrap_test.go` returning 7.
- [x] `./scripts/dev.sh go test -race -count=1 ./internal/cachefs/...` exits 0; all 7 tests pass. Confirmed (1.014s).
- [x] `./scripts/dev.sh go build ./...` exits 0. Confirmed.
- [x] `./scripts/dev.sh make fmt vet` exits 0. Confirmed.
- [x] Both task commits present in `git log`: `f1ff33e` (feat), `60d26e4` (test). Confirmed via `git log --oneline -3`.
- [x] Zero deletions across both task commits. Confirmed via `git diff --diff-filter=D --name-only HEAD~2 HEAD` returning empty.
- [x] No stub patterns introduced (no hardcoded empty values flowing to UI, no `TODO` / `FIXME` / `placeholder` markers in shipped source). Confirmed via `grep -E 'TODO|FIXME|placeholder|not available|coming soon' internal/cachefs/*.go` returning no matches.
- [x] No new threat surface beyond plan's `<threat_model>` (no network endpoints, no auth paths, no schema changes; filesystem access is the plan's documented surface). No `## Threat Flags` section needed.

---

*Phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Completed: 2026-05-15*
