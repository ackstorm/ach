---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W1-04
subsystem: cli
tags: [xxh3, hashing, state, drift-detection, content-addressed-integrity, phase-7-foundation, D-10, D-14, STATE-02]

# Dependency graph
requires:
  - phase: 06-cli-foundation
    provides: internal/credhash analog (tight stdlib-shaped Hash() wrapper discipline)
  - plan: 07-W1-01
    provides: Phase 7 exit code constants (Drift / EnvironmentMismatch / SchemaMismatch / CollisionRefuse)
provides:
  - internal/cli/hash package — xxh3 wrapper exposing Hash(io.Reader) + HashBytes([]byte)
  - Canonical "xxh3:<32-char-lowercase-hex>" string contract used by state.FileEntry.hash + state.FileEntry.sourceHash
  - Streaming + non-streaming forms that produce byte-identical output for the same input
  - Concurrent-safe pure-function discipline (1000-goroutine -race test guards against future regressions)
affects:
  - 07-W1-02 (state.go marshaler — consumes hash.Hash for FileEntry.hash + sourceHash)
  - 07-W2-01 (extract/tar.go — consumes hash.Hash to compute the bytes-written digest)
  - 07-W2-02 (extract/stage.go — consumes hash.Hash on the staged source stream)
  - 07-W3-01..05 (adapter/* — adapters synthesizing files via HashBytes on rendered output)
  - 07-W3-06 (manifest.go decoder — consumes Hash for parsed Manifest payload integrity checks)

# Tech tracking
tech-stack:
  added:
    - github.com/zeebo/xxh3 v1.1.0 (promoted from transitive to direct require — D-10 mandate)
  patterns:
    - "Single-primitive wrapper package (analog: internal/credhash) — stdlib + one external dep, tight Hash() API"
    - "Streaming + non-streaming dual surface (Hash(io.Reader) vs HashBytes([]byte)) with consistency guarantee"
    - "Format invariant tests (regexp on output shape) over hard-coded vector tests — survives library upgrades, doesn't lie about output"

key-files:
  created:
    - internal/cli/hash/doc.go — package doc citing CLI spec §8.2 + D-10 + D-14 + STATE-02 + non-cryptographic posture
    - internal/cli/hash/xxh3.go — Hash(io.Reader) + HashBytes([]byte) implementations
    - internal/cli/hash/xxh3_test.go — 11 tests covering format invariant, consistency, determinism, concurrency, I/O error surfacing
  modified:
    - go.mod — promoted github.com/zeebo/xxh3 from transitive (go.sum-only via go-redis/v9) to direct require + added klauspost/cpuid/v2 indirect (xxh3 transitive)
    - go.sum — added github.com/zeebo/assert v1.3.0 entry that xxh3 test infrastructure pulls in

key-decisions:
  - "Hex encoding via xxh3.Uint128.Bytes() — the library exposes a built-in big-endian Hi||Lo [16]byte serializer; using it is shorter than manual encoding/binary.BigEndian.PutUint64 calls and removes a potential off-by-one"
  - "Format invariant tests rather than hard-coded vector tests — the plan's <behavior> explicitly forbids inventing vectors, and the load-bearing properties (prefix + 32 hex chars + Hash↔HashBytes consistency + determinism) are checkable without external vectors"
  - "Error wrapping via fmt.Errorf with %w — the wrapper's I/O error path needs to be errors.Is-able by the state engine for drift-detection error classification"
  - "External test package (hash_test) — mirrors internal/credhash_test discipline; keeps the public API surface honest"

patterns-established:
  - "Pattern A: Stdlib + one-named-external Hash wrapper — Hash(io.Reader)(string, error) + HashBytes([]byte)string with shared canonical output format; consumed by 07-W1-02 (state) and 07-W2-* (extract)"
  - "Pattern B: Non-cryptographic posture documentation in doc.go — explicit anti-pattern callout (never use for credentials or signatures) per CLAUDE.md documentation-hygiene discipline"
  - "Pattern C: Concurrent-safety test under -race for any 'pure function' wrapper — 1000-goroutine pattern lifted from internal/credhash_test.go"

requirements-completed:
  - STATE-02

# Metrics
duration: ~22min
completed: 2026-05-29
---

# Phase 07 Plan 07-W1-04: internal/cli/hash xxh3 wrapper Summary

**xxh3 content-addressed integrity wrapper producing "xxh3:<32hex>" strings for state.FileEntry.hash + sourceHash drift detection, with streaming Hash(io.Reader) and in-memory HashBytes([]byte) forms backed by github.com/zeebo/xxh3.**

## Performance

- **Duration:** ~22 min
- **Started:** 2026-05-29 (worktree spawn time)
- **Completed:** 2026-05-29
- **Tasks:** 1
- **Files modified:** 5 (3 created, 2 modified)

## Accomplishments

- Shipped the single canonical hash primitive every downstream Phase 7 package (state, extract, adapter, manifest) will consume to compute `xxh3:<hex>` digests
- Locked the output-format contract: `xxh3:` prefix + 32-char lowercase hex (16-byte big-endian Hi||Lo encoding via xxh3.Uint128.Bytes)
- Established the streaming/in-memory consistency guarantee: `Hash(bytes.NewReader(b)) == HashBytes(b)` byte-for-byte — verified by the 1MiB random-buffer test and the "abc" consistency test
- Verified concurrent-safety: 1000-goroutine `go test -race` passes without data races (xxh3.New per call holds all state; the package surface is genuinely stateless)
- Documented the non-cryptographic posture explicitly in doc.go — `internal/cli/hash` is for content-addressed integrity (drift detection, bit-rot guard) only; credentials remain on `internal/credhash` (HMAC-SHA-256) and signatures on `crypto/ed25519`

## Task Commits

Each task was committed atomically:

1. **Task 1: xxh3 wrapper with streaming Hash + non-streaming HashBytes** — `4a37932` (feat)

_TDD discipline note: per the project's pre-commit hook (which runs `go vet ./...` as part of `make unit`), a pure-RED commit containing only the test file fails the hook because `go vet` cannot resolve `hash.Hash` / `hash.HashBytes` symbols. RED was nevertheless executed conceptually: the test file was written and saved first, then `./scripts/dev.sh go test ./internal/cli/hash/...` was run and confirmed to fail with `undefined: hash.Hash` (build failure), then `doc.go` and `xxh3.go` were added to make tests green. The pre-commit hook then accepted the combined RED+GREEN landing as a single `feat(...)` commit. The user's CLAUDE.md rule "never use --no-verify" was honored — no hook was bypassed._

## Files Created/Modified

- `internal/cli/hash/doc.go` — Package doc documenting the contract (Hash + HashBytes shape, xxh3 prefix + 32 hex chars), discipline (stdlib + zeebo/xxh3 only, no log/log/slog/os), and non-cryptographic posture (never use for credentials)
- `internal/cli/hash/xxh3.go` — Hash(io.Reader) (string, error) streams via xxh3.New + io.Copy; HashBytes([]byte) string uses xxh3.Hash128 directly; both share the `prefix + hex.EncodeToString(Uint128.Bytes()[:])` encoding
- `internal/cli/hash/xxh3_test.go` — 11 test cases: empty/abc/1MiB-random format & consistency, deterministic across calls (both forms), concurrent-safe under 1000 goroutines, distinct inputs produce distinct digests, I/O errors surfaced as non-nil error + empty string
- `go.mod` — promoted `github.com/zeebo/xxh3 v1.1.0` from transitive (go.sum-only via go-redis/v9) to direct require; added `klauspost/cpuid/v2 v2.2.10` as indirect (xxh3's CPU-feature-detect dep)
- `go.sum` — added `github.com/zeebo/assert v1.3.0` entry (xxh3 test-infrastructure transitive)

## Decisions Made

- **Encode via `xxh3.Uint128.Bytes()` rather than manual `encoding/binary.BigEndian.PutUint64`** — the xxh3 library exposes a built-in canonical big-endian Hi||Lo serializer for Uint128. Using it is one line instead of four, removes a potential off-by-one in manual packing, and tracks the library's intent (the Bytes() method is documented as "canonical form").
- **Format-invariant tests over hard-coded vector tests** — the plan's `<behavior>` block explicitly says "DO NOT invent vectors". The load-bearing properties for the state engine are (a) format shape `xxh3:[0-9a-f]{32}$`, (b) `Hash(bytes.NewReader(b)) == HashBytes(b)`, (c) determinism across runs, (d) distinct inputs → distinct digests. All four are checkable without external vectors and survive library upgrades that change bit-exact output.
- **Error wrapping via `fmt.Errorf(..., %w, err)`** — the I/O error path needs to be `errors.Is`-able by the state engine, which will classify drift-detection failures by error kind. The doc.go's "no fmt" prohibition was meant to bar `fmt.Println`-style I/O; error-formatting via fmt is the Go idiom and is preserved here.
- **External test package (`package hash_test`)** — matches `internal/credhash_test` discipline. Keeps the public API surface honest (no access to unexported helpers from tests).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Promoted github.com/zeebo/xxh3 from transitive to direct require in go.mod**

- **Found during:** Task 1 (first `./scripts/dev.sh go test ./internal/cli/hash/...` run after adding the import)
- **Issue:** The plan's acceptance criterion at line 92 reads `git diff --stat go.mod | wc -l | grep -q "^0$"` — i.e. "go.mod must NOT change after this task." The planner verified `github.com/zeebo/xxh3 v1.1.0` is in `go.sum:395-396` and concluded no go.mod entry would be needed. That verification missed the distinction between transitive (go.sum-only via go-redis/v9) and direct (require block in go.mod). When this package directly imports `github.com/zeebo/xxh3`, `go mod tidy` correctly promotes it to a direct `require` line in go.mod and pulls in xxh3's CPU-feature-detect transitive dep (`klauspost/cpuid/v2`) as a new indirect. This is mandatory Go modules semantics — there is no flag or config that would leave go.mod stale; the 15-gate pre-push enforces `go mod tidy` cleanliness, so the alternative is a permanently broken pre-push.
- **Fix:** Ran `./scripts/dev.sh go mod tidy` and accepted the resulting 2-line go.mod delta:
  - `+ github.com/zeebo/xxh3 v1.1.0` (direct require — was previously transitive)
  - `+ github.com/klauspost/cpuid/v2 v2.2.10 // indirect` (xxh3 transitive — new)
  - `go.sum`: `+ github.com/zeebo/assert v1.3.0` (xxh3 test-infrastructure transitive)
- **Files modified:** go.mod, go.sum
- **Verification:** `./scripts/dev.sh go build ./...` clean across the entire tree; `./scripts/dev.sh make unit-pkg PKG=./internal/cli/hash/...` passes; `./scripts/dev.sh go mod tidy` is a no-op afterward (stable state).
- **Committed in:** `4a37932` (Task 1 commit — go.mod + go.sum included)

The spirit of the plan's "go.mod NOT modified" check (D-10 verification that xxh3 is already in the dependency closure and no NEW library is being introduced) is preserved: `zeebo/xxh3 v1.1.0` was already locked at this version in go.sum before this plan, so this change does not introduce a new third-party library — it merely upgrades its dependency edge from transitive to direct. The CLAUDE.md govulncheck-acknowledged list does not need updating; xxh3 is pure Go with no known advisories.

---

**Total deviations:** 1 auto-fixed (1 blocking — Go modules semantics correction)
**Impact on plan:** The deviation tightens (does not loosen) the dependency posture — xxh3 becomes an explicit direct dependency, surfacing it on `go mod graph` and dependency audits where it was previously hidden behind go-redis/v9. No scope creep, no new third-party libraries introduced.

## Issues Encountered

- **TDD RED commit blocked by pre-commit hook.** The first attempt at landing a pure RED commit (test file only, no implementation) failed `make unit`'s `go vet ./...` step with "undefined: hash.Hash". This is fundamental to Go: a test file referencing a yet-unimplemented public symbol cannot vet. Resolved by combining RED and GREEN into a single `feat(...)` commit per the gsd-executor "one commit per task" protocol — the user's CLAUDE.md rule "never use --no-verify" was honored. TDD discipline was preserved at the conceptual level (tests written first, RED phase failure observed before adding implementation). This pattern is reusable for future Go TDD tasks under the project's pre-commit posture.

## User Setup Required

None — pure code-level package, no external service or secret required.

## Next Phase Readiness

- **07-W1-02 (state.go marshaler) unblocked** — can now `import "github.com/ackstorm/ach/internal/cli/hash"` and call `hash.Hash(io.Reader)` or `hash.HashBytes([]byte)` to compute `FileEntry.hash` and `FileEntry.sourceHash` per D-14 dual-hash discipline.
- **07-W2-* (extract pipeline) unblocked** — the per-staged-file digest hop (compute hash of bytes written to disk) and the per-source-stream digest hop (compute hash of upstream bytes pre-transformation) both use this package.
- **07-W3-* (adapters) unblocked** — adapter outputs (`RenderRuntime` results) hash via `hash.HashBytes` before staging.

No blockers for downstream waves. The format contract `xxh3:<32 lowercase hex>` is locked and tested.

## Self-Check: PASSED

- internal/cli/hash/doc.go — FOUND in worktree commit `4a37932`
- internal/cli/hash/xxh3.go — FOUND in worktree commit `4a37932`
- internal/cli/hash/xxh3_test.go — FOUND in worktree commit `4a37932`
- Commit `4a37932` — FOUND in `git log --oneline --all`
- go.mod + go.sum modifications — FOUND in worktree commit `4a37932`
- SUMMARY.md — written to `/home/jcm/Projects/ach/.planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W1-04-SUMMARY.md` (in main repo; `.planning/` is gitignored inside the worktree per `parallel_execution` discipline)

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
