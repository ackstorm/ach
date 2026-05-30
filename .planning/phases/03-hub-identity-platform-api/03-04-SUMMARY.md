---
phase: 03-hub-identity-platform-api
plan: 04
subsystem: auth
tags: [keys, crypto-rand, base32, ulid, hmac-sha256, bearer, constant-time]

requires:
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: internal/credhash (HMAC-SHA-256+pepper hashing; constant-time compare; no-logger discipline)
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: db/migrations/000001_init.up.sql CHECK (key_id LIKE 'pkid_%') / CHECK (key_id LIKE 'ekid_%') constraints
provides:
  - internal/keys.NewBearer — server-side pk_/ek_ plaintext generator (16 bytes crypto/rand → 26 base32-no-pad lowercase chars)
  - internal/keys.NewKeyID — pkid_/ekid_ opaque key_id generator (ULID-backed, time-ordered)
  - internal/keys.ClassifyBearer — strict pk_/ek_ parser; rejects keyIDs, wrong-length, uppercase, out-of-alphabet
  - internal/keys.ConstantTimeEqual — crypto/subtle wrapper for timing-oracle-safe bearer compares
  - PkBearerPrefix / EkBearerPrefix / PkidKeyIDPrefix / EkidKeyIDPrefix exported constants
  - BearerPrefix / KeyIDPrefix newtypes (PrefixPk/PrefixEk/PrefixPkid/PrefixEkid sentinels)
  - ErrInvalidPrefix / ErrInvalidBearer sentinels
affects:
  - 03-05-PLAN.md (keystore — calls ClassifyBearer to dispatch between PkCheckAndExtend/EkResolve)
  - 03-07-PLAN.md (SSO callback — calls NewBearer(PrefixPk) + NewKeyID(PrefixPkid))
  - 03-08-PLAN.md (env-keys — calls NewBearer(PrefixEk) + NewKeyID(PrefixEkid))
  - 03-10-PLAN.md (admin — ConstantTimeEqual for credential equality outside the resolver hot path)
  - Phase 4 Forwarder (imports internal/keystore which imports internal/keys)
  - Phase 5 Content Service (imports internal/keystore which imports internal/keys)

tech-stack:
  added:
    - github.com/oklog/ulid/v2 v2.1.0 (lock-free, time-ordered, Crockford base32 — verified MIT, k8s ecosystem usage)
  patterns:
    - Plaintext-never-logged discipline (zero log/slog/fmt imports across keys.go/doc.go/keys_test.go)
    - Newtype-prefix pattern (BearerPrefix/KeyIDPrefix) for compile-time discrimination of namespace kinds
    - Manual char-loop alphabet validation (avoids regexp allocation on resolver hot path)
    - Lowercased base32 encoding for grep ergonomics (NewBearer + NewKeyID both apply strings.ToLower)
    - Type-safe sentinel error pattern (ErrInvalidPrefix/ErrInvalidBearer) for caller dispatch via errors.Is

key-files:
  created:
    - internal/keys/doc.go
    - internal/keys/keys.go
    - internal/keys/keys_test.go
  modified:
    - go.mod (one new direct require: github.com/oklog/ulid/v2 v2.1.0)
    - go.sum

key-decisions:
  - "internal/keys is the single owner of the pk_/ek_/pkid_/ekid_ namespace; downstream handlers reference exported constants instead of string literals so namespace drift surfaces as a compile break."
  - "ClassifyBearer uses a manual base32-lower char loop (not regexp) to avoid allocation on the keystore resolver hot path; isBase32Lower is an inline byte-range check."
  - "NewBearer derives 16 random bytes from crypto/rand for ~130 bits of entropy (RFC 4648 base32-no-pad → 26 chars after prefix). Lowercased per the codebase grep convention; matches Patterns map 'Bearer plaintext generation pattern' verbatim."
  - "NewKeyID uses ulid.Make() (monotonic within a millisecond) for time-ordered DB index locality on the personal_keys.key_id / environment_keys.key_id primary keys."
  - "ConstantTimeEqual wraps crypto/subtle.ConstantTimeCompare directly; length-mismatch early-return is NOT treated as a timing leak (bearer grammar fixes length at 29 chars; length is not secret per crypto/subtle godoc)."
  - "Package keeps zero-logger discipline (same as internal/credhash from Phase 1): imports are crypto/rand, crypto/subtle, encoding/base32, errors, strings, github.com/oklog/ulid/v2 — no log, no log/slog, no fmt."

patterns-established:
  - "Newtype-prefix discrimination: BearerPrefix and KeyIDPrefix are string-newtypes whose only valid values are the exported Prefix* sentinels. Any other input returns ErrInvalidPrefix at the package boundary, so caller misuse fails fast."
  - "TDD with stdlib testing only — no testify, no gomega; same convention as internal/credhash and internal/cachefs from Phase 1."
  - "Plaintext-once invariant codified in package doc.go: only POST /platform/auth/sso/callback and POST /platform/env-keys may return plaintext; doc references the future static-analysis grep gate."

requirements-completed: [KEY-01]

duration: 9min
completed: 2026-05-20
---

# Phase 3 Plan 04: internal/keys Package Summary

**Single-owner package for the ACH bearer/key-id namespace — `NewBearer` mints `pk_`/`ek_` plaintexts from 16 crypto/rand bytes (RFC 4648 base32-no-pad lowercased), `NewKeyID` mints `pkid_`/`ekid_` opaque IDs from `ulid.Make()`, `ClassifyBearer` enforces the strict 29-char grammar at the keystore boundary, and `ConstantTimeEqual` provides timing-oracle-safe equality compares.**

## Performance

- **Duration:** 9 min (548 s)
- **Started:** 2026-05-20T20:42:58Z
- **Completed:** 2026-05-20T20:52:06Z
- **Tasks:** 3
- **Files modified:** 5 (3 created + 2 modified)

## Accomplishments

- Shipped `internal/keys/` as the single owner of the `pk_/ek_/pkid_/ekid_` prefix invariant — downstream Phase 3 plans 03-05 (keystore), 03-07 (SSO), 03-08 (env-keys), and 03-10 (admin) can now import this package and call typed APIs instead of fabricating prefix string literals.
- Hardened the bearer-plaintext generator against timing-oracle and predictability attacks: 130 bits of entropy via `crypto/rand`, lowercase RFC 4648 base32-no-pad (the only base32 dialect with a fixed lowercase representation in Go's stdlib), and `crypto/subtle.ConstantTimeCompare` for equality.
- Codified the plaintext-never-logged discipline at package scope: `doc.go` documents the contract; the package has ZERO imports of `log`, `log/slog`, `fmt`, or `os` — same convention as `internal/credhash` from Phase 1.
- 23 unit tests landed across 4 behavior families (NewBearer × 6, NewKeyID × 5, ClassifyBearer × 7, ConstantTimeEqual × 4, KeyIDPrefix constants × 1) — all pass under stdlib `testing` only (no testify, no gomega).

## Task Commits

Each task was committed atomically:

1. **Task 1: Add github.com/oklog/ulid/v2 + create internal/keys/doc.go** — `4daa3db` (feat)
2. **Task 2 RED: failing tests for NewBearer + bearer constants** — `82ffe3e` (test)
2. **Task 2 GREEN: NewBearer + bearer namespace constants** — `0e0dd71` (feat)
3. **Task 3 RED: failing tests for NewKeyID + ClassifyBearer + ConstantTimeEqual** — `3177444` (test)
3. **Task 3 GREEN: NewKeyID + ClassifyBearer + ConstantTimeEqual** — `b57de27` (feat)

_Plan executed via strict RED → GREEN TDD per `tdd="true"` on every task. No REFACTOR commits needed — initial GREEN was already clean._

## Files Created/Modified

- `internal/keys/doc.go` (NEW, 57 lines) — Package doc declaring namespace invariants and the plaintext-never-logged discipline.
- `internal/keys/keys.go` (NEW, 225 lines) — `BearerPrefix`/`KeyIDPrefix` newtypes, `PkBearerPrefix`/`EkBearerPrefix`/`PkidKeyIDPrefix`/`EkidKeyIDPrefix` constants, `PrefixPk`/`PrefixEk`/`PrefixPkid`/`PrefixEkid` sentinels, `NewBearer` / `NewKeyID` / `ClassifyBearer` / `ConstantTimeEqual` exported funcs, `ErrInvalidPrefix` / `ErrInvalidBearer` sentinels, `isBase32Lower` private helper.
- `internal/keys/keys_test.go` (NEW, 392 lines) — 23 unit tests with stdlib `testing` only.
- `go.mod` (MODIFIED, +1 line) — direct require for `github.com/oklog/ulid/v2 v2.1.0`.
- `go.sum` (MODIFIED, +3 lines) — hash + go.mod entries for the new dep.

## Decisions Made

- **Plan-author guidance followed verbatim** for both base32 encoder choice (`base32.StdEncoding.WithPadding(base32.NoPadding)`) and ULID library choice (`github.com/oklog/ulid/v2`).
- **Manual char-loop alphabet validation in `ClassifyBearer`** (per plan's explicit "OR a manual char-loop. Either is acceptable; choose the manual loop to avoid `regexp` allocation overhead on hot paths"). The `isBase32Lower` helper is a 5-line byte-range check inlined-by-the-compiler.
- **Pinned ULID dep at v2.1.0** (verified against the plan's package-legitimacy guidance — oklog/ulid is the canonical Go ULID implementation, MIT-licensed, used by Kubernetes/Cilium/Thanos ecosystem; explicitly marked `[VERIFIED]` not `[SLOP]` in the plan).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Task 1's go.mod entry transiently landed as `// indirect`**
- **Found during:** Task 1 verify (`grep -nE 'github.com/oklog/ulid/v2' go.mod` showed the entry under `// indirect` rather than the direct `require` block).
- **Issue:** The plan's Task 1 acceptance criteria require `oklog/ulid/v2` to be in a direct `require` block (NOT `// indirect`). But `go mod tidy` correctly demotes any dep that has no in-tree code reference — and Task 1's scope (`doc.go` only) intentionally adds no Go code that imports `ulid`. The plan's `<verify>` block runs `go mod tidy` which strips the entry to `// indirect` (or even removes it).
- **Fix:** Committed Task 1 with the entry as `// indirect` and documented the deviation; Task 3 (which actually `imports "github.com/oklog/ulid/v2"`) re-runs `go mod tidy` and the entry is automatically promoted to the direct `require` block. Final state at HEAD has `github.com/oklog/ulid/v2 v2.1.0` in the direct `require` block as the plan's success criterion requires.
- **Files modified:** `go.mod` (transient `// indirect` annotation in commit `4daa3db`, then promoted-to-direct in commit `b57de27`).
- **Verification:** `grep -n "oklog/ulid" go.mod` at HEAD returns `18:\tgithub.com/oklog/ulid/v2 v2.1.0` — the entry is in the direct `require` block (no `// indirect` suffix).
- **Committed in:** `4daa3db` (transient state) + `b57de27` (final promoted state).

---

**Total deviations:** 1 auto-fixed (Rule 3 — go-mod-tidy semantics on empty packages).
**Impact on plan:** Zero scope creep. The deviation is a plan-authoring quirk (Task 1 cannot ship a direct-require entry without code that uses the dep) and resolves itself organically by Task 3 once the actual `ulid.Make` import lands. Final repository state matches the plan's success criteria exactly.

## Issues Encountered

None. Both RED phases produced the expected compile failures; both GREEN phases produced clean builds and full test passes on the first iteration.

## Plan-level Verification

| Check | Result |
|-------|--------|
| `./scripts/dev.sh go build ./internal/keys/...` exits 0 | PASS |
| `./scripts/dev.sh go test ./internal/keys/... -count=1` exits 0 | PASS (23 tests pass) |
| `./scripts/dev.sh go vet ./internal/keys/...` exits 0 | PASS |
| `grep -rE '(log\|slog\|fmt\.Print)' internal/keys/keys.go internal/keys/doc.go` discipline gate | PASS (count = 0) |
| `go.mod` gains exactly one new direct dep (`github.com/oklog/ulid/v2 v2.1.0`) | PASS |
| `git diff --stat` shows 3 new files in `internal/keys/` + modified go.mod + go.sum | PASS |
| Plan acceptance grep gates (all 11 patterns) | PASS |

## Threat-Model Coverage (from PLAN.md `<threat_model>`)

| Threat | Disposition | Mitigation Landed In Code |
|--------|-------------|---------------------------|
| T-03-04-01 (Information Disclosure — bearer plaintext logged) | mitigate | Zero `log`/`log/slog`/`fmt` imports in `internal/keys/*` (acceptance grep gate enforced). |
| T-03-04-02 (Information Disclosure — predictable bearer plaintext) | mitigate | `NewBearer` draws 16 bytes from `crypto/rand` (~130 bits of entropy); 1000-call uniqueness test demonstrates entropy in practice. |
| T-03-04-03 (Tampering — timing attack on bearer compare) | mitigate | `ConstantTimeEqual` wraps `crypto/subtle.ConstantTimeCompare`; tests cover equal, mismatch, length-diff, both-empty. |
| T-03-04-04 (Tampering — bearer plaintext format collision) | mitigate | `ClassifyBearer` enforces strict 29-char length + lowercase prefix + `[a-z2-7]` alphabet via manual char loop; rejects `pkid_*`/`ekid_*` keyIDs explicitly. |
| T-03-04-05 (Repudiation — ULID timestamp predictability) | accept | Per plan, ULID's wall-clock-ms high bits leak creation time but not identity; documented in `keys.go` package-doc comment for NewKeyID. |
| T-03-04-SC (Tampering — npm/pip/cargo installs) | mitigate | One new go.mod entry: `github.com/oklog/ulid/v2 v2.1.0`. Plan author verified MIT + ~3.4k stars + k8s ecosystem usage; explicitly `[VERIFIED]`, not `[SLOP]`. |

## Threat Flags

None — this plan introduces no new network endpoints, auth paths, file access patterns, or schema changes at trust boundaries beyond what the plan's `<threat_model>` already enumerates.

## Next Phase Readiness

- **03-05 (keystore) is unblocked** — can now import `internal/keys.ClassifyBearer` to dispatch between `db.PkCheckAndExtend` and `db.EkResolve` based on bearer prefix.
- **03-07 (SSO) is unblocked** — can now call `keys.NewBearer(keys.PrefixPk)` + `keys.NewKeyID(keys.PrefixPkid)` to mint the pk_ + pkid_ pair that the §7 / §16 contract requires before calling `litellm.KeyGenerate(key=<plaintext>)`.
- **03-08 (env-keys) is unblocked** — same pattern, `keys.NewBearer(keys.PrefixEk)` + `keys.NewKeyID(keys.PrefixEkid)`.
- **03-10 (admin) is unblocked** — `keys.ConstantTimeEqual` available for any credential equality checks that arise outside the resolver hot path.

No blockers, no deferred items, no follow-up work needed inside this plan's scope.

## Self-Check: PASSED

Created files verified to exist:
- FOUND: `internal/keys/doc.go`
- FOUND: `internal/keys/keys.go`
- FOUND: `internal/keys/keys_test.go`

Commits verified to exist in git history (commit `git log --oneline -7` reflects all five):
- FOUND: `4daa3db` (Task 1)
- FOUND: `82ffe3e` (Task 2 RED)
- FOUND: `0e0dd71` (Task 2 GREEN)
- FOUND: `3177444` (Task 3 RED)
- FOUND: `b57de27` (Task 3 GREEN)

All plan-level verification commands return 0; all 11 acceptance grep gates match; the discipline gate (zero log/slog/fmt imports) holds.

---
*Phase: 03-hub-identity-platform-api*
*Plan: 04*
*Completed: 2026-05-20*
