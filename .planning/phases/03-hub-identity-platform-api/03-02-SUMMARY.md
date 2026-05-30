---
phase: 03-hub-identity-platform-api
plan: 02
plan_id: 03-02
subsystem: api
tags: [audit, slog, json, http, envelope, hub-spec-18.2, hub-spec-15.5]

# Dependency graph
requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: "internal/audit/handler.go NewLogger (slog JSON sink with audit=true predicate) + internal/orphan.OutcomeRevoked / OutcomeLiteLLMUnreachable (string-value contract Phase 3 preserves)"
provides:
  - "internal/audit package-level Action* constants (9 — Hub §18.2 verbatim)"
  - "internal/audit package-level Outcome* constants (17 — 16 Hub §18.2 + OutcomeStateInvalid per BLK-05)"
  - "internal/audit.Event + Target structs binding Hub §18.2 schema"
  - "internal/audit.EmitAudit(ctx, *slog.Logger, Event) helper consumed by all Phase 3 handlers"
  - "internal/platformapi/render package: JSON() success envelope + Error() §15.5 error envelope"
  - "Cross-package vocabulary contract: render.Error code field accepts audit.Outcome* string values"
affects:
  - "03-07 (SSO callback handler — emits OutcomeStateInvalid, OutcomeDefaultTeamMissing, OutcomeCreated through EmitAudit)"
  - "03-08 (ek_ create/revoke handlers — EmitAudit + render.JSON/Error)"
  - "03-09 (environments list + hydrate handlers — render.JSON with next_cursor; EmitAudit on hydrate)"
  - "03-10 (admin handlers — EmitAudit + render.Error on not_admin)"
  - "03-12 (OBS-02 e2e: schema invariant verification across all handler emissions)"
  - "Phase 5 (Content Service may reuse render package + audit vocabulary)"

# Tech tracking
tech-stack:
  added: []  # stdlib only — log/slog, encoding/json, net/http
  patterns:
    - "Package-level constant enums for closed wire-format vocabularies (Hub §18.2 actions/outcomes)"
    - "Cross-phase string-value equality contract for log-filter continuity (audit.Outcome* == orphan.Outcome*)"
    - "Audit-safety contract: discipline over scrubbing — helper transports raw, callers compose safe events"
    - "Avoid circular imports by keeping cross-package vocabulary as string-literal contracts enforced in tests, not production imports"
    - "Best-effort encoder error swallow on http.ResponseWriter (status already flushed)"

key-files:
  created:
    - "internal/audit/events.go — 9 Action + 17 Outcome constants"
    - "internal/audit/events_test.go — value-stability + cross-phase equality tests"
    - "internal/audit/emit.go — Event/Target structs + EmitAudit helper"
    - "internal/audit/emit_test.go — round-trip behavior tests (6 cases)"
    - "internal/platformapi/render/doc.go — package doc + §15.5 envelope spec"
    - "internal/platformapi/render/json.go — JSON() + Error() writers"
    - "internal/platformapi/render/json_test.go — envelope shape + Content-Type + encoder-error tests"
  modified: []  # zero existing files touched — plan is purely additive

key-decisions:
  - "OutcomeStateInvalid added as Phase-3-internal additive extension to §18.2 per BLK-05 — emitted by SSO callback on cookie-state vs URL-state mismatch or missing URL state. Documented here so a future Hub-spec revision can adopt it."
  - "Production internal/platformapi/render/json.go does NOT import internal/audit — vocabulary contract enforced only in tests + handler-plan code review. Keeps both base-layer packages free of cycles."
  - "Action string used as BOTH slog message AND 'action' top-level attribute. Double-coding matches Phase 2 orphan-loop idiom so both message-based and attribute-based log filters work."
  - "EmitAudit ctx parameter accepted but unused at v0 — reserved for future trace_id/span_id/deadline derivations without breaking call-site signatures."

patterns-established:
  - "Package-level Action*/Outcome* constants live alongside handler.go/doc.go in internal/audit (mirrors internal/litellm split — client.go, errors.go, types.go)"
  - "Test files import sibling packages (internal/orphan, internal/audit) to verify cross-package vocabulary contracts without introducing production cycles"
  - "Best-effort write pattern for HTTP response helpers: w.Header().Set → w.WriteHeader → _ = json.NewEncoder(w).Encode(...)"
  - "Optional Event fields (KeyID, Target, Extra) omitted from slog record when zero/nil — handler authors don't need to construct sentinel placeholders"

requirements-completed: [OBS-01, API-12]

# Metrics
duration: ~22min
completed: 2026-05-20
---

# Phase 3 Plan 02: Audit Event Vocabulary + Platform API Response Envelope Summary

**Foundational stdlib-only substrate for Phase 3 handlers: package-level audit.Action*/Outcome* enum (Hub §18.2 + OutcomeStateInvalid for BLK-05), EmitAudit slog helper bound to the §18.2 schema, and the §15.5 JSON success+error envelope writers — every Phase 3 handler plan (03-07..03-10) imports both.**

## Performance

- **Duration:** ~22 min
- **Started:** 2026-05-20T20:25:00Z (approx)
- **Completed:** 2026-05-20T20:47:39Z
- **Tasks:** 3
- **Files created:** 7
- **Files modified:** 0 (plan is purely additive)
- **Lines added:** 849 (across 7 files)

## Accomplishments

- **audit constant enum** — 9 ActionXxx + 17 OutcomeXxx package-level constants. 16 outcomes are Hub §18.2 verbatim; OutcomeStateInvalid is the Phase-3-internal additive extension per BLK-05 for SSO callback state mismatch / missing URL state. OutcomeRevoked + OutcomeLitellmUnreachable share string values with Phase 2's orphan-cleanup outcomes so a single log-filter predicate matches both eras.
- **audit.EmitAudit helper** — single entrypoint every Phase 3 handler calls. Translates the Event struct (action, outcome, actor, request_id, key.id, target.*, extra) to slog attributes through the Phase 2 NewLogger output. Optional fields (KeyID, Target, Extra) omitted from the record when zero/nil. Discipline-over-scrubbing contract documented but not enforced — callers compose safe events per audit/doc.go.
- **render package** — JSON() and Error() ship the §15.5 envelope writers. Every Platform API response (success or error) carries application/json; charset=utf-8 and the §15.5 body shape. Production code is import-decoupled from internal/audit — the vocabulary contract is enforced by the test file's import (TestErrorOutcomeCodeCompatibility) and by handler-plan code review.

## Task Commits

Each task ran a TDD RED→GREEN cycle.

1. **Task 1: internal/audit/events.go — 9 Action + 17 Outcome constants**
   - RED: `faa1cbb` (test: add failing tests for audit.Action* + audit.Outcome* constants)
   - GREEN: `706609d` (feat: add audit.Action* + audit.Outcome* constants — Hub §18.2 + BLK-05)

2. **Task 2: internal/audit/emit.go — Event + Target + EmitAudit helper**
   - RED: `992f603` (test: add failing tests for audit.EmitAudit helper)
   - GREEN: `bcc89c8` (feat: add audit.Event + audit.Target + audit.EmitAudit helper)

3. **Task 3: internal/platformapi/render — JSON + Error envelope writers**
   - RED: `a8555f7` (test: add failing tests for platformapi/render JSON + Error envelope)
   - GREEN: `60b43c9` (feat: add internal/platformapi/render JSON + Error envelope writers)

No REFACTOR commits — initial implementations passed acceptance criteria + tests on first pass.

## Files Created/Modified

### Created (7)

- `internal/audit/events.go` (107 lines) — Action*/Outcome* constants with file-level extension-policy doc (Hub §18.5 additive-only).
- `internal/audit/events_test.go` (100 lines) — TestEventConstantsAreStable (table-driven over all 26 values) + TestEventConstantsMatchOrphan (cross-phase string-value equality with internal/orphan).
- `internal/audit/emit.go` (133 lines) — Event + Target structs + EmitAudit. ctx-parameter reserved for future trace_id/span_id derivation. Pre-sized attribute slice for the common-case attribute count.
- `internal/audit/emit_test.go` (205 lines) — 6 round-trip tests: basic, target-included, target-omitted, extra-round-trip, empty-key-id-omitted, plaintext-discipline-documented.
- `internal/platformapi/render/doc.go` (46 lines) — package doc with §15.5 envelope spec + the "no audit import in production" contract.
- `internal/platformapi/render/json.go` (76 lines) — JSON() + Error() with shared Content-Type constant.
- `internal/platformapi/render/json_test.go` (182 lines) — 6 tests: success envelope, error envelope, Content-Type before WriteHeader, encoder-error swallow (×2), audit.Outcome* round-trip compatibility.

### Modified (0)

Plan was purely additive. Zero existing files touched.

## Decisions Made

- **OutcomeStateInvalid placement:** added to the same const block as the §18.2 outcomes (not a separate "extensions" block) so handler authors don't have to remember which block a constant lives in. The Phase-3-internal status is signaled by an inline comment on the constant + a section in the file-level docstring. This keeps the API surface flat at the cost of a slightly longer file-level doc — acceptable trade-off given handlers reference these constants frequently.
- **EmitAudit attribute pre-sizing:** the attrs slice is pre-allocated with capacity `8 + 2 + 4 + 2*len(Extra)` to avoid the slow-path append growth on the common case (action+outcome+actor+request_id+key.id+target.kind+target.name + one Extra pair). Micro-optimization but the cost is zero and the audit path is on every state-changing request.
- **Cross-package vocabulary enforcement:** the test file `internal/platformapi/render/json_test.go` is the ONLY place that imports both `internal/audit` and `internal/platformapi/render`. Production code in `render/json.go` deliberately does not — keeps the two base packages cycle-free and lets handler plans wire the audit-enum → render-code parameter at the call site under code review.
- **Action as both msg and attribute:** preserves Phase 2's orphan-loop log-filter compatibility (`grep "operator.orphan-cleanup"` matches the msg; `grep '"action":"operator.orphan-cleanup"'` matches the attr). Costs ~30 bytes per record.

## Deviations from Plan

None — plan executed exactly as written.

All three tasks delivered the constants, structs, helper signatures, file paths, test cases, and acceptance-criteria literals described in `03-02-PLAN.md`. The TDD RED→GREEN gate cycle ran successfully on each task (failing test committed first, implementation second).

## Issues Encountered

- **Worktree branch divergence:** the per-agent worktree branch (`worktree-agent-a7c8d6ddd6eab1b42`) was originally created from commit `e975d28` (before Phase 3 plans were committed to main). A fast-forward merge from main (`a4daf45`) was required at executor startup to bring the Phase 3 plan files + Phase 2 code into the worktree. The merge was fast-forward only (no divergent commits) so no manual conflict resolution was needed; this brings the worktree up to date with main without rewriting history.

## User Setup Required

None — no external service configuration required. The plan ships pure stdlib-only Go code with zero new go.mod entries.

## Verification Results

```
$ ./scripts/dev.sh go build ./...
(clean — no output)

$ ./scripts/dev.sh go test ./internal/audit/... ./internal/platformapi/render/... -count=1
ok  	github.com/ackstorm/ach/internal/audit	0.004s
ok  	github.com/ackstorm/ach/internal/platformapi/render	0.002s

$ ./scripts/dev.sh go vet ./...
(clean — no output)
```

Acceptance-criteria grep checks (all green):

- `grep -cE '^\tAction[A-Z][a-zA-Z]+\s*=' internal/audit/events.go` = 9 ✓
- `grep -cE '^\tOutcome[A-Z][a-zA-Z]+\s*=' internal/audit/events.go` = 17 ✓
- `grep -nE 'OutcomeStateInvalid\s*=\s*"state_invalid"' internal/audit/events.go` = 1 match ✓
- `grep -nE 'OutcomeRevoked\s*=\s*"revoked"' internal/audit/events.go` = 1 match ✓
- `grep -nE 'OutcomeLitellmUnreachable\s*=\s*"litellm_unreachable"' internal/audit/events.go` = 1 match ✓
- `grep -nE 'type Event struct' internal/audit/emit.go` = 1 match ✓
- `grep -nE 'type Target struct' internal/audit/emit.go` = 1 match ✓
- `grep -nE '^func EmitAudit\(ctx context\.Context, logger \*slog\.Logger, e Event\)' internal/audit/emit.go` = 1 match ✓
- `grep -nE 'logger\.Info\(e\.Action' internal/audit/emit.go` = 1 match ✓
- `grep -nE '^func JSON\(w http\.ResponseWriter, status int, body any\)' internal/platformapi/render/json.go` = 1 match ✓
- `grep -nE '^func Error\(w http\.ResponseWriter, status int, code, message, requestID string\)' internal/platformapi/render/json.go` = 1 match ✓
- `grep -nE 'application/json; charset=utf-8' internal/platformapi/render/json.go` ≥ 1 match ✓
- `grep -c 'internal/audit' internal/platformapi/render/json.go` = 0 (production cycle-free) ✓
- `grep -rn 'audit\.Outcome\|audit\.Action' internal/audit/events_test.go internal/audit/emit_test.go internal/platformapi/render/json_test.go | wc -l` = 47 (≥ 5 required, cross-pkg usage proven) ✓

## TDD Gate Compliance

All three tasks completed the RED→GREEN gate sequence in git history:

| Task | RED commit | GREEN commit | Sequence verified |
|------|-----------|-------------|-------------------|
| 1 (events.go) | `faa1cbb` test | `706609d` feat | ✓ test before feat |
| 2 (emit.go) | `992f603` test | `bcc89c8` feat | ✓ test before feat |
| 3 (render) | `a8555f7` test | `60b43c9` feat | ✓ test before feat |

No REFACTOR commits — implementations were minimal and passed acceptance on first pass.

## Next Phase Readiness

Plans 03-07 (SSO callback), 03-08 (ek_ create/revoke), 03-09 (environments + hydrate), 03-10 (admin) can now:

- `import "github.com/ackstorm/ach/internal/audit"` and call `audit.EmitAudit(ctx, deps.Audit, audit.Event{Action: audit.ActionXxx, Outcome: audit.OutcomeXxx, Actor: ..., RequestID: ..., KeyID: ..., Target: ...})` without further setup.
- `import "github.com/ackstorm/ach/internal/platformapi/render"` and call `render.JSON(w, 200, body)` for success or `render.Error(w, status, audit.OutcomeXxx, message, requestID)` for errors.
- Rely on the cross-phase string-value contract so a downstream log filter like `audit=true outcome=revoked` matches BOTH Phase 2 orphan revocations AND Phase 3 ek_/pk_ revoke handler emissions.

No blockers for Wave 2+ of Phase 3. The OutcomeStateInvalid extension is in place for Plan 03-07's SSO callback (per BLK-05 commit on tests 3 / 3b / 3c).

## Self-Check

Files exist:

- `internal/audit/events.go` ✓
- `internal/audit/events_test.go` ✓
- `internal/audit/emit.go` ✓
- `internal/audit/emit_test.go` ✓
- `internal/platformapi/render/doc.go` ✓
- `internal/platformapi/render/json.go` ✓
- `internal/platformapi/render/json_test.go` ✓

Commits exist on `worktree-agent-a7c8d6ddd6eab1b42`:

- `faa1cbb` test(03-02): events RED ✓
- `706609d` feat(03-02): events GREEN ✓
- `992f603` test(03-02): emit RED ✓
- `bcc89c8` feat(03-02): emit GREEN ✓
- `a8555f7` test(03-02): render RED ✓
- `60b43c9` feat(03-02): render GREEN ✓

## Self-Check: PASSED

---
*Phase: 03-hub-identity-platform-api*
*Plan: 02*
*Completed: 2026-05-20*
