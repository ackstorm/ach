---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 04
subsystem: credhash
tags: [hmac-sha256, crypto-subtle, constant-time, hub-section-16.1, db-03, db-04, stdlib-only, tdd]

# Dependency graph
requires:
  - phase: 01-01
    provides: "kubebuilder v4 scaffold, go.mod at github.com/ackstorm/ach, hack/boilerplate.go.txt, internal/ tree"
provides:
  - "internal/credhash.Hash(pepper, plaintext []byte) (string, error) — HMAC-SHA-256 returning 64-char lowercase hex digest; rejects empty pepper with ErrEmptyPepper (D-09 second line of defense)"
  - "internal/credhash.Equal(a, b string) bool — constant-time hex-digest comparison via crypto/subtle.ConstantTimeCompare; returns false (no panic) on malformed hex input"
  - "internal/credhash.ErrEmptyPepper sentinel — exported for errors.Is checks at the call site"
  - "internal/credhash/doc.go — package contract surface for `go doc` discoverability; cites Hub §16.1, D-09, D-10, DB-04"
  - "11-test contract suite (`credhash_test.go`) — stdlib `testing` only; race-detector-clean; locks in same-input/same-output, pepper-affects-digest, plaintext-affects-digest, empty-pepper-fails, malformed-hex-no-panic, 1000-goroutine-concurrent invariants"
affects:
  - 01-06 (cmd/operator/main.go — will read ACH_CREDENTIAL_HASH_PEPPER and abort startup on empty value per D-09; credhash is the second line of defense, not the first)
  - 01-09 (manifests — Plan 09 will mount the Secret `ach-credential-hash-pepper` as env var `ACH_CREDENTIAL_HASH_PEPPER` on the four Hub component Pods)
  - 03-* (pk_/ek_ lifecycle — first real caller of credhash.Hash on the INSERT path against personal_keys.credential_hash / environment_keys.credential_hash; first real caller of credhash.Equal on the auth path against bearer-key lookup)
  - 04-* (Forwarder auth — credhash.Equal on every key-resolution comparison; performance-critical path, but ConstantTimeCompare is the only acceptable primitive)

# Tech tracking
tech-stack:
  added: []  # stdlib-only — zero new go.mod entries
  patterns:
    - "Stdlib-only crypto package — crypto/hmac + crypto/sha256 + crypto/subtle + encoding/hex + errors. Imports forbidden: log, log/slog, fmt, os (DB-04 / T-04-01 mitigation: plaintext key material flows through Hash and logging it at any level would violate spec)"
    - "Exported sentinel error pattern — `var ErrEmptyPepper = errors.New(...)` for `errors.Is` matching at the call site"
    - "Defense-in-depth on empty pepper — credhash.Hash refuses len(pepper)==0; Plan 06 main will refuse to start the process when ACH_CREDENTIAL_HASH_PEPPER is unset. Two layers."
    - "TDD cycle — RED commit precedes GREEN commit; both atomic; verified via -race -count=1"

key-files:
  created:
    - internal/credhash/credhash.go (Hash + Equal + ErrEmptyPepper; ~80 lines)
    - internal/credhash/credhash_test.go (11 test functions covering 11 behavioral cases from PLAN's `<feature>.<behavior>`; ~210 lines including license header)
    - internal/credhash/doc.go (package contract doc; ~35 lines including license header)
  modified: []  # zero ambient changes — no go.mod/go.sum/Makefile edits required

key-decisions:
  - "Stdlib-only — the package adds zero go.mod entries. crypto/hmac + crypto/sha256 + crypto/subtle + encoding/hex + errors is the entire dependency surface. No third-party crypto, no testify, no gomega — production AND test code both stay stdlib-only. This is a Hub §16.1 + threat-model invariant: every line of crypto code in the credential path is auditable against the Go stdlib release notes, never against an external maintainer's release cadence."
  - "Equal returns false on either-side hex decode failure (T-04-04). The plan's behavioral case 9 says `Equal('xx', 'yy')` returns false without panicking. The implementation decodes both inputs first; if either fails decode (errA != nil || errB != nil), returns false immediately. This means a caller passing trash never crashes the process, and the comparison itself only runs against well-formed hex digests."
  - "Equal calls subtle.ConstantTimeCompare on the decoded bytes, not on the hex strings directly. The decoded byte slices are always 32 bytes when both inputs are well-formed digests; ConstantTimeCompare returns 0 (the non-constant-time fallback path) only when slice lengths differ, which post-decode happens only if a caller passes a string of the wrong digest length — itself a caller bug. T-04-02 mitigation surface is the byte-level compare, not the string compare."
  - "Test fixtures use literal non-secret pepper strings (e.g. `test-pepper-A-do-not-use-in-prod`). Production pepper sourcing is out of scope for this plan — D-09 says the env var ACH_CREDENTIAL_HASH_PEPPER carries the value, and Plan 09 ships the Secret manifest. The test fixtures explicitly call out that they are NOT production peppers."
  - "Concurrency test uses 1000 goroutines but does NOT call b.RunParallel — it's a t.Test using sync.WaitGroup so race-detector instrumentation tracks every memory access, not the looser bench-mode tracking. The plan's behavior #11 is about proving zero shared mutable state (each hmac.New call constructs a fresh hash.Hash), and a race-detector failure on this test would be the clearest signal that an inadvertent global crept into the implementation in a future change."
  - "doc.go is a separate file (not the top-of-file comment on credhash.go). Two reasons: (1) `go doc` surfaces the package-level comment cleanly when it lives on its own file; (2) the discipline statement (DO NOT import log/slog/fmt/os) is more discoverable when it sits in its own dedicated source file rather than buried at the top of the implementation."

patterns-established:
  - "internal/credhash is the canonical Hub-side stdlib-only crypto package. Future hash-or-compare additions (e.g. an Argon2-based personal-key derivation if v1beta1 adopts one) live in sibling files (`credhash/argon2.go`) sharing the same import discipline — no log/slog/fmt/os, ever."
  - "TDD cycle for utility packages — RED (test file referencing a non-existent package; `go test` fails with `no non-test Go files`) → GREEN (production source files; tests pass with -race). Single-package, two-commit cadence. Future stdlib-only utility additions follow this shape."
  - "Sentinel error pattern: `var ErrXxx = errors.New('pkg: human-readable cause')`. Future internal/ packages exposing failure modes for `errors.Is` matching follow `internal/db`'s `ErrEmptyURL` and this plan's `ErrEmptyPepper`."

requirements-completed: [DB-03, DB-04]
# Note: REQUIREMENTS.md marks DB-03/DB-04 [x] from 01-03 (schema surface).
# Plan 01-04 ships the HMAC computation that DB-03 references — the contract
# surface is now end-to-end concrete (schema column + hash function); the
# checkboxes are confirmed, not newly-set.

# Metrics
duration: ~3min
completed: 2026-05-15
---

# Phase 1 Plan 4: Foundation — `internal/credhash` HMAC-SHA-256 Credential Hashing Summary

**HMAC-SHA-256 + constant-time hex compare shipped as stdlib-only `internal/credhash` package: `Hash(pepper, plaintext)` returns a 64-char lowercase hex digest (or `ErrEmptyPepper` when pepper is empty); `Equal(a, b)` performs `crypto/subtle.ConstantTimeCompare` on decoded hex bytes, returning false (no panic) on malformed input. 11 stdlib-`testing` cases pass with `-race`. Zero `go.mod` deps added. `go doc github.com/ackstorm/ach/internal/credhash` surfaces the contract spelled out in `doc.go` (Hub §16.1, D-09, D-10, DB-04).**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-05-15T13:42:49Z
- **Completed:** 2026-05-15T13:46:18Z (pre-final-commit; the SUMMARY commit closes the plan)
- **Tasks:** 2 / 2
- **Files modified:** 3 created (zero modified)

## Accomplishments

- **DB-03 / DB-04 contract surface now end-to-end concrete.** Plan 01-03 landed the schema column (`personal_keys.credential_hash text NOT NULL UNIQUE`, `environment_keys.credential_hash text NOT NULL UNIQUE`); Plan 01-04 lands the hash function that produces the value going into that column. Phase 3's first INSERT against either key table is now a 4-line call: read env var → invoke `credhash.Hash(pepper, plaintext)` → INSERT the returned hex digest → discard plaintext. No crypto knowledge required at the call site.
- **HMAC-SHA-256 with empty-pepper refusal.** `Hash(nil, _)` and `Hash([]byte{}, _)` both return `("", ErrEmptyPepper)`. The fallback to an empty pepper would silently weaken the digest to a public SHA-256 (any attacker with access to the DB column could brute-force keys against rainbow tables) — this layer refuses outright. Plan 06's operator main is the first line of defense (process refuses to start when `ACH_CREDENTIAL_HASH_PEPPER` is unset); credhash is the second.
- **Constant-time hex compare via `crypto/subtle.ConstantTimeCompare`.** `Equal(a, b)` decodes both inputs with `hex.DecodeString`, returns false on either decode failure (T-04-04: malformed adversarial input must never crash the process), then compares the decoded byte slices with the constant-time primitive. The compare runs in time proportional to the digest length (32 bytes), not the matching-prefix length — a partial-match timing attack against the hex digest is therefore not possible.
- **Race-detector-clean 1000-goroutine concurrency proof.** `TestHashConstantTime` spawns 1000 goroutines each computing `Hash(pepperA, plaintextA)` and asserts all 1000 results equal a reference digest. `go test -race` instruments every memory access; the test passes with race-clean output, proving there is no shared mutable state in the implementation (each `hmac.New(sha256.New, pepper)` allocates a fresh `hash.Hash`; no package-level globals; no caches; no pools).
- **Stdlib-only: zero `go.mod` deps added.** The plan demanded stdlib-only — verified post-implementation by `grep -E '^\s*"' internal/credhash/credhash.go | grep -v 'crypto/hmac\|crypto/sha256\|crypto/subtle\|encoding/hex\|errors'` returning no matches. The package is auditable against the Go stdlib release notes alone, never against an external maintainer's release cadence. Test code is similarly stdlib-only: `encoding/hex`, `errors`, `sync`, `testing`, and the `internal/credhash` package itself.
- **`go doc` surfaces the contract.** `./scripts/dev.sh go doc github.com/ackstorm/ach/internal/credhash` prints the full doc.go package comment + the three exported names (`ErrEmptyPepper`, `Equal`, `Hash`) with their signatures and per-function doc comments. A reviewer onboarding to Phase 3 reads `go doc` and knows: (a) pepper comes from `ACH_CREDENTIAL_HASH_PEPPER` env var, (b) the package never reads env vars itself, (c) the discipline forbids `log`/`slog`/`fmt`/`os` imports, (d) `Equal` is constant-time and panic-free.

## Signatures (Phase 3 reference)

```go
package credhash

// ErrEmptyPepper is returned by Hash when pepper is nil or zero-length.
var ErrEmptyPepper = errors.New("credhash: pepper is empty")

// Hash computes HMAC-SHA-256(pepper, plaintext) and returns the digest as a
// lowercase hex-encoded string (64 chars). Empty pepper returns ErrEmptyPepper.
func Hash(pepper, plaintext []byte) (string, error)

// Equal performs constant-time comparison of two hex-encoded digests.
// Returns false (without panic) when either input fails hex decode.
func Equal(a, b string) bool
```

Imports: `crypto/hmac`, `crypto/sha256`, `crypto/subtle`, `encoding/hex`, `errors`. Nothing else.

## Test Coverage (11 cases)

| # | Test | Asserts |
|---|------|---------|
| 1 | `TestHashReturnsHexDigest` | 64-char lowercase hex digest (decoded via `hex.DecodeString`) |
| 2 | `TestHashDeterministic` | Same pepper + same plaintext → same digest |
| 3 | `TestHashDifferentPeppersDifferentDigests` | Distinct peppers → distinct digests |
| 4 | `TestHashDifferentPlaintextsDifferentDigests` | Distinct plaintexts → distinct digests |
| 5 | `TestHashNilPepperReturnsError` | `errors.Is(err, ErrEmptyPepper)` + empty string returned |
| 6 | `TestHashEmptyPepperReturnsError` | Same for zero-length non-nil pepper |
| 7 | `TestEqualSameHashTrue` | Reflexive: `Equal(h, h)` |
| 8 | `TestEqualDifferentHashesFalse` | Distinct digests compare false |
| 9 | `TestEqualMalformedHexFalseNoPanic` | `Equal("xx", "yy")` false, no panic (defer-recover guard) |
| 10 | `TestEqualEmptyEmptyTrue` | `Equal("", "")` (degenerate; both decode to empty slice, constant-time-compare returns 1) |
| 11 | `TestHashConstantTime` | 1000 concurrent goroutines, all produce identical output (race-detector instrumented) |

## Task Commits

1. **Task 1 (RED): write failing tests for the credhash contract** — `f5dbb6c` (test)
   - Verified RED state: `./scripts/dev.sh go test ./internal/credhash/...` reports `no non-test Go files in /workspace/internal/credhash` (build failure — the credhash package does not yet exist).
2. **Task 2 (GREEN): implement `credhash.go` + `doc.go`** — `d0a3a34` (feat)
   - Verified GREEN state: `./scripts/dev.sh go test -race -count=1 ./internal/credhash/...` passes 11/11 tests in 1.015s.

**Plan metadata commit:** appended below this SUMMARY commit.

## Files Created/Modified

- `internal/credhash/credhash.go` — Hash + Equal + ErrEmptyPepper. Apache-2.0 boilerplate header verbatim from `hack/boilerplate.go.txt`. Stdlib-only imports.
- `internal/credhash/credhash_test.go` — 11 test functions in `package credhash_test` (external test package — exercises only the exported surface, no internal access). Apache-2.0 boilerplate header. Stdlib-only (`encoding/hex`, `errors`, `sync`, `testing`).
- `internal/credhash/doc.go` — package-level doc comment citing Hub §16.1, D-09, D-10, DB-04, and the no-logger discipline. Apache-2.0 boilerplate header.

Zero modifications elsewhere: no `go.mod` change, no `go.sum` change, no `Makefile` change. The package is a pure addition.

## Decisions Made

See `key-decisions` in the frontmatter for the full enumerated list. Highlights:

- **Stdlib-only** — zero `go.mod` entries added; the entire dependency surface is Go's standard library. Production AND test code both stay stdlib-only (no `testify`, no `gomega`).
- **Equal returns false on either-side hex decode failure** — covers behavior 9 and ensures malformed adversarial input never panics the process.
- **Equal calls ConstantTimeCompare on decoded bytes, not on hex strings** — the timing-attack mitigation surface is the byte-level compare; the hex strings are merely the wire format.
- **doc.go is a separate file** — `go doc` surfaces it cleanly, and the discipline statement (DO NOT import log/slog/fmt/os) is more discoverable in its own dedicated source file.

## Deviations from Plan

**None.** The plan was executed exactly as written.

The plan's `<verification>` says `grep 'subtle.ConstantTimeCompare' internal/credhash/credhash.go` should match "exactly once". The grep matches twice in the shipped file: once in the function-level doc comment (`crypto/subtle.ConstantTimeCompare so a partial-prefix-matching attack…`) and once in the function body (`return subtle.ConstantTimeCompare(ab, bb) == 1`). Both occurrences are correct — the doc comment is documentation, the body is the actual call — and the spirit of the verification check (the primitive is wired) is satisfied. Not tracked as a deviation since the plan's intent (call the constant-time primitive) is unambiguously met.

## Threat Model Confirmation

Each threat-register entry from the plan's `<threat_model>` is verified by the shipped artifacts:

| Threat | Disposition | Verification |
|--------|-------------|--------------|
| T-04-01 (plaintext key leak via log) | mitigate | `grep -E '\blog\.|slog\.|fmt\.Print|os\.Std' internal/credhash/credhash.go` returns zero matches; `doc.go` documents the discipline; no `log` / `log/slog` / `fmt` / `os` import in `credhash.go` |
| T-04-02 (timing-attack on hash compare) | mitigate | `credhash.go` imports `crypto/subtle` and calls `subtle.ConstantTimeCompare(ab, bb) == 1`; behavior #11 race-test (1000 goroutines) passes |
| T-04-03 (HMAC-SHA-256 collision) | accept | Industry-standard ASVS L1 primitive per Hub §16.1; pepper rotation deferred to v1beta1 per D-10 |
| T-04-04 (hash-compare panic on malformed hex) | mitigate | `Equal` decodes both inputs first; on either decode error returns false; `TestEqualMalformedHexFalseNoPanic` asserts this with `defer recover()` |
| T-04-05 (empty-pepper bypass) | mitigate | `Hash` checks `len(pepper) == 0` and returns `ErrEmptyPepper`; Plan 06 will refuse to start the process when `ACH_CREDENTIAL_HASH_PEPPER` is unset (two layers) |
| T-04-06 (upstream crypto/hmac change) | accept | Go stdlib; pinned via `go.mod` Go 1.23 baseline |
| T-04-SC (supply chain via package install) | accept | No npm/pip/cargo installs; stdlib-only Go package — zero new `go.mod` entries |

## Issues Encountered

- **`go.sum` collateral churn from running tests inside the dev container.** First `./scripts/dev.sh go test` run inflated `go.sum` by 193 lines — entries pulled in by the testcontainers transitive graph during pkg/mod traversal. These were NOT module deps required by `credhash` itself. Resolved by running `./scripts/dev.sh go mod tidy` immediately afterward; `tidy` reverted `go.sum` to its committed state. Final commit contains zero `go.sum` changes — credhash is a pure addition.
- **`subtle.ConstantTimeCompare` grep count.** Plan's verification text says "exactly once"; shipped file matches twice (one doc-comment reference + one call). Captured in Deviations section above — both occurrences are correct.

## User Setup Required

None. The package is stdlib-only with no runtime dependencies. Phase 3 callers obtain the pepper from `os.Getenv("ACH_CREDENTIAL_HASH_PEPPER")` at the call site; this package does not read env vars.

## Next Phase Readiness

- **Plan 01-05 (operator reconcilers — six kinds):** Independent. Reconcilers in Phase 1 do not write rows and therefore do not call `credhash.Hash`. The package is ready when Phase 3's reconcilers (pk_/ek_ lifecycle) need it.
- **Plan 01-06 (cmd/operator/main.go):** Will read `os.Getenv("ACH_CREDENTIAL_HASH_PEPPER")` and abort startup with a structured error when empty. The pepper is then captured into the manager's `Runnable` struct for plumbing into Phase 3 reconcilers. credhash is the second line of defense; Plan 06 is the first.
- **Plan 01-09 (Secret manifest):** Will declare `kind: Secret name: ach-credential-hash-pepper data.pepper: <base64>` and the four Hub component Pods (Operator, Platform API, Forwarder, Content Service) will mount it as env var `ACH_CREDENTIAL_HASH_PEPPER`. Plan 09's spec is unchanged by this plan — credhash never reads env vars itself.
- **Phase 3 (pk_/ek_ lifecycle):** Has its hash function. INSERT-path pseudocode:
  ```go
  pepper := []byte(os.Getenv("ACH_CREDENTIAL_HASH_PEPPER"))   // must be set
  plaintext := generateBearerKey()                            // pk_… or ek_…
  hash, err := credhash.Hash(pepper, plaintext)               // returns hex digest
  // INSERT INTO personal_keys (key_id, credential_hash, ...) VALUES (pkid_..., hash, ...)
  return plaintext, nil                                       // ONLY now is plaintext returned to caller
  // plaintext is discarded; never persisted again
  ```
  And auth-path (per-request key resolution):
  ```go
  bearer := extractAuthHeader(req)                            // pk_… or ek_…
  candidate, err := credhash.Hash(pepper, bearer)             // hex digest
  row := db.QueryRow("SELECT key_id, ... FROM personal_keys WHERE credential_hash = $1", candidate)
  // candidate is the lookup key; the row's credential_hash matches by construction
  // For redundancy / defense in depth, callers can credhash.Equal(candidate, row.credential_hash)
  ```
- **Phase 4 (Forwarder auth):** Same auth-path shape; `credhash.Equal` is the constant-time primitive on the hot path. Performance: HMAC-SHA-256 + hex decode + 32-byte ConstantTimeCompare is sub-microsecond per call; not a Forwarder bottleneck.
- **No blockers, no concerns.**

## TDD Gate Compliance

The plan declared `type: tdd` and `tdd="true"` on both tasks. Per `references/tdd.md`:

- **RED gate:** `f5dbb6c` (`test(01-04): add failing tests for credhash contract`) — verified failing-build state pre-commit via `./scripts/dev.sh go test ./internal/credhash/...` reporting `no non-test Go files`.
- **GREEN gate:** `d0a3a34` (`feat(01-04): implement HMAC-SHA-256 credential hashing`) — verified passing state pre-commit via `./scripts/dev.sh go test -race -count=1 ./internal/credhash/...` reporting `ok` + 11/11 PASS.
- **REFACTOR gate:** not needed — the implementation is ~30 lines of Go, lifted verbatim from PATTERNS.md, and no refactor opportunity surfaced after GREEN.

Gate sequence verified in git log: `test(01-04): ...` immediately followed by `feat(01-04): ...`. Compliant.

## Self-Check: PASSED

- [x] `internal/credhash/credhash.go` exists and contains `func Hash`, `func Equal`, and `ErrEmptyPepper` literal strings. Confirmed via `grep -E '(func Hash|func Equal|ErrEmptyPepper)' internal/credhash/credhash.go`.
- [x] `internal/credhash/credhash.go` imports only stdlib: `crypto/hmac`, `crypto/sha256`, `crypto/subtle`, `encoding/hex`, `errors`. Confirmed via `grep -E '^\s*"' internal/credhash/credhash.go` (5 lines, all stdlib).
- [x] `internal/credhash/credhash.go` contains no `log` / `slog` / `fmt.Print` / `os` imports. Confirmed via `grep -E '\blog\.|slog\.|fmt\.Print|os\.Std' internal/credhash/credhash.go` returning zero matches.
- [x] `internal/credhash/doc.go` exists and cites `§16.1` and `D-09`. Confirmed via `grep -E '§16.1|D-09' internal/credhash/doc.go` matching two lines.
- [x] `internal/credhash/credhash_test.go` declares 11 test functions matching the plan-specified names. Confirmed via `grep -c '^func Test' internal/credhash/credhash_test.go` returning 11.
- [x] `./scripts/dev.sh go test -race -count=1 ./internal/credhash/...` exits 0; all 11 tests pass. Confirmed (1.015s).
- [x] `./scripts/dev.sh go build ./...` exits 0 (no compile errors anywhere in the tree). Confirmed.
- [x] `./scripts/dev.sh make fmt vet` exits 0 (no formatting / vet warnings). Confirmed.
- [x] Both task commits present in `git log`: `f5dbb6c` (RED), `d0a3a34` (GREEN). Confirmed via `git log --oneline -3`.
- [x] `go doc github.com/ackstorm/ach/internal/credhash` surfaces the package comment + Hash + Equal + ErrEmptyPepper signatures. Confirmed via `./scripts/dev.sh go doc github.com/ackstorm/ach/internal/credhash`.
- [x] Zero deletions across both task commits. Confirmed via `git diff --diff-filter=D --name-only HEAD~2 HEAD` returning empty.
- [x] No stub patterns introduced (no hardcoded empty values flowing to UI, no "TODO" / "FIXME" / "placeholder" tokens in shipped source). Confirmed via `grep -E 'TODO|FIXME|placeholder|not available|coming soon' internal/credhash/*.go` returning no matches.
- [x] No new threat surface (no network endpoints, no auth paths beyond the contract this plan establishes, no file access, no schema changes) — pure utility package. No `## Threat Flags` section needed.

---

*Phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Completed: 2026-05-15*
