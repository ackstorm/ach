---
phase: 03-hub-identity-platform-api
plan: 01
subsystem: litellm-client

tags: [litellm, rest-client, user-new, user-info, team-member-add, key-generate, phase-3-d-25, key-10, blk-01, d-13, rel-04, rel-06, stdlib-testing, httptest]

# Dependency graph
requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: "RESTClient + makeRequest + Auth401Error + ErrNotFound + compile-time canary discipline (Plan 02-01 lifted the sister LiteLLM client and widened the Client interface)."
provides:
  - "Four new Client interface methods (UserNew, UserInfoByEmail, TeamMemberAdd, KeyGenerate) — Phase 3 D-25 surface"
  - "Six new request/response types in types.go (UserNewRequest, UserInfo, TeamMember, TeamMemberAddRequest, KeyGenerateRequest, KeyGenerateResponse)"
  - "UserInfo.Teams []string — BLK-01 contract consumed by Plans 03-08/03-09 for §8.2 step-4 team-intersection (KEY-11)"
  - "KEY-10 invariant enforcement at the type level: KeyGenerateRequest.MaxBudget is *float64 with omitempty"
  - "D-13 invariant enforcement at the implementation level: KeyGenerate echoes caller-supplied bearer plaintext (ACH owns the pk_*/ek_* namespace)"
  - "NoopClient stubs returning canned values designed to drive Plan 03-07 SSO unit tests deterministically (UserInfoByEmail → ErrNotFound = first-time-user branch)"
affects: [03-07-sso-callback-handler, 03-08-env-keys-create-handler, 03-09-env-keys-list-handler]

# Tech tracking
tech-stack:
  added: []  # zero new go.mod entries — uses stdlib + existing RESTClient
  patterns:
    - "TDD red-green per task (each task has a test commit followed by an implementation commit)"
    - "Pure REST-client extension via makeRequest (no transport-layer changes)"
    - "Compile-time canary discipline catches downstream consumer drift (var _ Client = (*RESTClient)(nil) / (*NoopClient)(nil))"
    - "url.QueryEscape for query-string email parameters (deterministic across all per-domain helpers)"
    - "Pointer-with-omitempty for tri-state JSON fields (nil = absent, *zero = explicit zero, *value = explicit value)"

key-files:
  created:
    - internal/litellm/users.go
    - internal/litellm/users_test.go
    - internal/litellm/keygen.go
    - internal/litellm/keygen_test.go
  modified:
    - internal/litellm/types.go (appended six Phase 3 request/response types)
    - internal/litellm/client.go (Client interface extended with four signatures)
    - internal/litellm/noop.go (NoopClient extended with four stubs)
    - internal/litellm/client_test.go (added TestPhase3TypesJSON + TestNoopClient_Phase3)
    - internal/connection/client.go (Rule 3 — proxy delegation for four new methods)
    - internal/orphan/runnable_test.go (Rule 3 — fakeLiteLLM stub catch-up)
    - internal/snapshot/snapshot_test.go (Rule 3 — fakeLiteLLM stub catch-up)
    - internal/controller/ach/main_wiring_envtest_test.go (Rule 3 — wiringFakeLiteLLM stub catch-up)

key-decisions:
  - "MaxBudget is *float64 (NOT float64) — pointer-with-omitempty drops the field entirely when nil, enforcing KEY-10 (ACH never sets max_budget) at the type level"
  - "UserInfoByEmail does NOT translate 404 → ErrNotFound — Plan 03-07 SSO handler will branch on strings.Contains(err.Error(), \"404\") (preserves makeRequest 4xx convention; per-domain helper idiom kept narrow)"
  - "NoopClient.UserInfoByEmail returns (nil, ErrNotFound) by default — drives SSO first-time-user branch deterministically; tests that need the existing-user branch construct a *UserInfo directly rather than mutating stub state"
  - "url.QueryEscape (not url.PathEscape) for email query parameter — `+` correctly becomes `%2B` (bare `+` decodes as space per RFC 3986 query semantics; verified by TestUserInfoByEmailEscapesPlus)"
  - "Rule 3 ripple: the Client interface widening forces UserNew/UserInfoByEmail/TeamMemberAdd/KeyGenerate methods onto connection.Client (delegating proxy) + three test fakes — caught by the compile-time canary, fixed inline before the task closed"

patterns-established:
  - "Per-domain helper file follows team.go shape (POST + json.Unmarshal envelope), funneling through makeRequest for REL-04 drain+close + REL-06 *Auth401Error + §9.1 no-body-in-error discipline"
  - "TDD RED commit is build-failure level (referenced type/method literally does not exist) — proves the test is testing the right surface; GREEN commit lands the minimal code to compile and pass"

requirements-completed: [API-02]

# Metrics
duration: 10min
completed: 2026-05-20
---

# Phase 3 Plan 1: LiteLLM Client Extensions (UserNew, UserInfoByEmail, TeamMemberAdd, KeyGenerate)

**Extended `internal/litellm.Client` interface with four Phase 3 D-25 methods plus six request/response types; KEY-10 enforced at the type level via `MaxBudget *float64` with omitempty; D-13 enforced at the implementation level via caller-supplied `Key`-echo contract.**

## Performance

- **Duration:** 10m 51s
- **Started:** 2026-05-20T20:41:48Z
- **Completed:** 2026-05-20T20:52:39Z
- **Tasks:** 3 of 3
- **Files modified:** 9 (5 in internal/litellm/ — 2 created + 3 modified — + 1 created users_test.go + 1 created keygen_test.go in litellm/, 4 Rule 3 ripple targets outside internal/litellm/)

## Accomplishments

- Added six exported types to `internal/litellm/types.go` covering `/user/new`, `/user/info`, `/team/member_add`, `/key/generate` wire shapes (Phase 3 D-25).
- Implemented three RESTClient methods (`UserNew`, `UserInfoByEmail`, `TeamMemberAdd`) in new `internal/litellm/users.go` funneling through existing `makeRequest` — REL-04 / REL-06 / §9.1 discipline inherited verbatim.
- Implemented `KeyGenerate` on RESTClient in new `internal/litellm/keygen.go` mirroring the `CreateTeam` POST + decode shape.
- Widened `Client` interface in `client.go` with all four method signatures; NoopClient gained four canned-value stubs in `noop.go`. Compile-time canaries `var _ Client = (*RESTClient)(nil)` and `var _ Client = (*NoopClient)(nil)` preserved.
- Three TDD red/green cycles executed cleanly — each task ships a failing test commit followed by an implementation commit that makes only those tests pass.

## Task Commits

Each task was executed RED → GREEN per the plan's `tdd="true"` directive:

1. **Task 1: Phase 3 types in types.go**
   - RED: `1f4142e` (`test(03-01): add failing TestPhase3TypesJSON for Phase 3 LiteLLM types`)
   - GREEN: `6be17b4` (`feat(03-01): add Phase 3 LiteLLM request/response types to types.go`)
2. **Task 2: UserNew + UserInfoByEmail + TeamMemberAdd in users.go**
   - RED: `75c1f51` (`test(03-01): add failing users_test.go for UserNew/UserInfoByEmail/TeamMemberAdd`)
   - GREEN: `731d5ef` (`feat(03-01): add UserNew/UserInfoByEmail/TeamMemberAdd on RESTClient`)
3. **Task 3: KeyGenerate + Client interface widening + NoopClient stubs**
   - RED: `de2531d` (`test(03-01): add failing keygen_test.go + TestNoopClient_Phase3`)
   - GREEN core: `d1b7df9` (`feat(03-01): add KeyGenerate + extend Client interface + NoopClient stubs`)
   - Rule 3 ripple: `1edd399` (`fix(03-01): propagate litellm.Client widening through downstream consumers`)

## Files Created/Modified

### Created

- `internal/litellm/users.go` (98 lines) — three RESTClient methods (`UserNew`, `UserInfoByEmail`, `TeamMemberAdd`)
- `internal/litellm/users_test.go` (216 lines) — six tests covering happy/404/escape/4xx/401 paths
- `internal/litellm/keygen.go` (50 lines) — KeyGenerate RESTClient method with D-13 + KEY-10 documentation
- `internal/litellm/keygen_test.go` (119 lines) — three tests covering key-echo / max_budget-omission / 401 propagation
- `.planning/phases/03-hub-identity-platform-api/03-01-SUMMARY.md` (this file)

### Modified

- `internal/litellm/types.go` (+93) — six new structs appended after the Phase 02.2 ListUserKeysResponse
- `internal/litellm/client.go` (+29) — Client interface extended with four method signatures + per-method doc comments
- `internal/litellm/noop.go` (+44) — four NoopClient stubs with canned values; compile-time canary unchanged
- `internal/litellm/client_test.go` (+64) — `TestPhase3TypesJSON` (4 subcases) + `TestNoopClient_Phase3` (4 subcases) + `float64Ptr` helper; added `encoding/json` and `strings` imports
- `internal/connection/client.go` (+39) — four delegation methods through `c.current()` (Rule 3)
- `internal/orphan/runnable_test.go` (+15) — fakeLiteLLM Phase 3 stubs (Rule 3, return-zero)
- `internal/snapshot/snapshot_test.go` (+11) — fakeLiteLLM Phase 3 stubs (Rule 3, return-zero)
- `internal/controller/ach/main_wiring_envtest_test.go` (+11) — wiringFakeLiteLLM Phase 3 stubs (Rule 3, return-zero)

## Verification Evidence

```
./scripts/dev.sh go build ./internal/litellm/...    → exit 0
./scripts/dev.sh go vet   ./internal/litellm/...    → exit 0
./scripts/dev.sh go test  ./internal/litellm/...    → ok 0.74s
./scripts/dev.sh go build ./...                     → exit 0
./scripts/dev.sh go vet   ./...                     → exit 0
./scripts/dev.sh go test  ./internal/litellm/... ./internal/connection/... \
                          ./internal/orphan/... ./internal/snapshot/...    → ok
```

Test-function PASS roster (the new Phase 3 surface):

- `TestPhase3TypesJSON` (4 subcases)
- `TestUserNewHappyPath`
- `TestUserInfoByEmailNotFound`
- `TestUserInfoByEmailHappyPath`
- `TestUserInfoByEmailEscapesPlus`
- `TestTeamMemberAddHappyPath`
- `TestTeamMemberAddDuplicate4xx`
- `TestUserHelpers401Propagation`
- `TestKeyGenerateEchoesCallerSuppliedKey`
- `TestKeyGenerateOmitsMaxBudgetWhenNil`
- `TestKeyGenerate401Propagation`
- `TestNoopClient_Phase3` (4 subcases)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Downstream consumers required interface-widening propagation**

- **Found during:** Task 3 (after extending the `Client` interface).
- **Issue:** The compile-time canary `var _ litellm.Client = (*connection.Client)(nil)` (and three test fakes' analogous canaries) broke when `KeyGenerate`/`UserNew`/`UserInfoByEmail`/`TeamMemberAdd` were added to the `Client` interface. The plan's success criterion stated "No file outside `internal/litellm/` is modified," but the canary discipline established in Phase 02-01 (which Plan 03-01 explicitly preserves) FORCES the proxy/fake catch-up — otherwise the build breaks and the plan cannot complete.
- **Fix:** Added the four method delegations on `connection.Client` (real proxy — routes through `c.current()` like every existing method) and four return-zero stubs on `orphan/runnable_test.go::fakeLiteLLM`, `snapshot/snapshot_test.go::fakeLiteLLM`, `controller/ach/main_wiring_envtest_test.go::wiringFakeLiteLLM`. None of these tests exercise the new SSO/env-keys methods, so the zero-value returns are behaviorally inert.
- **Files modified:** `internal/connection/client.go`, `internal/orphan/runnable_test.go`, `internal/snapshot/snapshot_test.go`, `internal/controller/ach/main_wiring_envtest_test.go`
- **Commit:** `1edd399`
- **Plan-text update needed:** The Plan 03-01 success criterion "No file outside `internal/litellm/` is modified" is incompatible with the Plan 02-01 compile-time canary discipline whenever the interface widens. Future plans that widen `litellm.Client` should expect the same ripple by default.

**2. [Plan-AC nit] Pre-existing `MaxBudget *float64` lines on team types**

- **Found during:** Task 1 (acceptance criterion check).
- **Issue:** Task 1 AC requires `grep -nE 'MaxBudget \*float64' internal/litellm/types.go` to output "exactly one match." In reality the worktree (post-Phase-02-01 sister lift) already had `MaxBudget *float64` on `NewTeamRequest` (line 99) and `UpdateTeamRequest` (line 116). The new `KeyGenerateRequest.MaxBudget *float64` adds a third match (line 365).
- **Fix:** No code change — the semantic intent of the AC (KeyGenerateRequest uses *float64) is satisfied; the strict grep count is satisfied IF the AC is read as "exactly one *new* match for KEY-10." Documented here so the verifier doesn't blink at the literal `wc -l` mismatch.
- **Files modified:** None — pure documentation deviation.

## Worktree Note

This plan was executed in a Claude Code worktree that was created from a commit 123 ahead of the worktree-branch base. The worktree was fast-forwarded to main at the start of execution (zero divergent commits — strict ancestor relationship). All Phase 03 plan artifacts (PLAN.md, CONTEXT.md, PATTERNS.md) were committed on main by `ecb3617` and are now available on the worktree branch.

## Self-Check: PASSED

- File `internal/litellm/users.go` exists: FOUND
- File `internal/litellm/users_test.go` exists: FOUND
- File `internal/litellm/keygen.go` exists: FOUND
- File `internal/litellm/keygen_test.go` exists: FOUND
- Commit `6be17b4`: FOUND (Task 1 GREEN)
- Commit `731d5ef`: FOUND (Task 2 GREEN)
- Commit `d1b7df9`: FOUND (Task 3 GREEN core)
- Commit `1edd399`: FOUND (Task 3 Rule 3 ripple)
- All Phase 3 test functions PASS under `go test ./internal/litellm/...`
- `go build ./...` and `go vet ./...` both exit 0
