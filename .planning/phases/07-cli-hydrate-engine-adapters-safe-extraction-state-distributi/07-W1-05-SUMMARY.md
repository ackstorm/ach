---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W1-05
subsystem: cli
tags: [hydrate, manifest, decoder, schema-version, content-ref, endpoint, phase-7-foundation]

# Dependency graph
requires:
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    provides: "internal/cli/exit.SchemaMismatch (code 5 from 07-W1-01) — callers map ErrSchemaMismatch to exit.SchemaMismatch via errors.Is"
  - phase: 06-cli-foundation
    provides: "internal/cli/httpclient.Client + DoRaw + *ServerError envelope — manifest.Fetch reuses Client.DoRaw, surfaces *ServerError on non-2xx without re-wrapping"
provides:
  - "internal/cli/manifest package: Manifest, RuntimeBlock, ContextBlock, ContentRef structs with Hub §15.2 JSON shape (schemaVersion + environment + runtime + context arms)"
  - "ContentRef.Endpoint field (json:\"endpoint,omitempty\") that runtime entries (models, mcpServers, a2aAgents) populate — adapters (ADAPT-03 / 07-W3-01..04) consume m.Runtime.MCPServers[i].Endpoint for runtime-config URL construction without re-fetching"
  - "manifest.Decode(io.Reader) (*Manifest, error) — strict json.NewDecoder + DisallowUnknownFields, asserts schemaVersion=='v1alpha1' AND runtime/context non-nil, returns ErrSchemaMismatch via %w on contract violation"
  - "manifest.Fetch(ctx, *httpclient.Client, environment string) (*Manifest, error) — POSTs /platform/hydrate with body {\"environment\": \"<env>\"} (or {} when empty), buffers response, runs Decode"
  - "manifest.ErrSchemaMismatch sentinel — caller layer (07-W1-06) maps to exit.SchemaMismatch (code 5)"
affects: [07-W1-06, 07-W3-01, 07-W3-02, 07-W3-03, 07-W3-04, 07-W4-01]

# Tech tracking
tech-stack:
  added: []  # no new deps; stdlib + existing httpclient only
  patterns:
    - "Strict-decode boundary: DisallowUnknownFields ON inside per-package Decode helpers (mirrors W1-02 state.Decode posture); httpclient.Client.Do path keeps it OFF for additive-server-field tolerance on other endpoints"
    - "Sentinel error + errors.Is mapping: ErrSchemaMismatch is wrapped via fmt.Errorf %w; callers use errors.Is to map to exit code; decode-level errors (malformed JSON, unknown fields) are deliberately NOT ErrSchemaMismatch so the two failure classes stay distinguishable"
    - "Single ContentRef struct serving BOTH runtime (Endpoint) AND context (DownloadURL) — omitempty tags keep encoded output minimal; adapters consume one type without runtime/context type-switching"
    - "DoRaw + buffered Decode instead of typed client.Do(out): manifest needs strict decode but httpclient.Client.Do deliberately omits DisallowUnknownFields for forward-compat on other endpoints; the buffered path keeps Decode unit-testable directly against examples/hydrate.json"

key-files:
  created:
    - "internal/cli/manifest/doc.go (package doc citing STATE-09 + §15.2 + D-09 + ContentRef rationale)"
    - "internal/cli/manifest/manifest.go (Manifest/RuntimeBlock/ContextBlock/ContentRef structs + ErrSchemaMismatch + Decode + Fetch)"
    - "internal/cli/manifest/manifest_test.go (10 tests: golden round-trip, schema mismatch x3, empty-arrays-OK, unknown-field rejection, Fetch POST shape x3, ServerError bubble-up)"
  modified: []

key-decisions:
  - "Fetch uses DoRaw + buffer + Decode rather than client.Do(out=&Manifest{}). Reason: httpclient.Client.Do does NOT enable DisallowUnknownFields (client.go:127-134 — deliberate forward-compat posture for additive server fields on other endpoints). The manifest contract is strict per §15.2 — unknown fields are a contract violation, not a forward-compat tolerance. Using DoRaw + the package's own Decode helper preserves the typed-strict posture without weakening Do's contract on other call sites."
  - "ContentRef is a SINGLE struct serving both runtime and context entries — runtime populates Endpoint, context populates DownloadURL, both share ID + Name. The omitempty tags keep JSON output minimal and round-trip clean against examples/hydrate.json (context entries decode with Endpoint=\"\", runtime entries with DownloadURL=\"\"). Adapters consume one type without a runtime/context type discriminator."
  - "Unknown-field errors are NOT wrapped in ErrSchemaMismatch. Two distinct failure classes: (a) ErrSchemaMismatch = §15.2 contract violation (schemaVersion drift or missing runtime/context block), maps to exit.SchemaMismatch (5); (b) decode-level errors (malformed JSON, unknown field, type mismatch) = wire-format failure, surfaces with JSON-path-bearing fmt.Errorf message. The 07-W1-06 orchestrator can use errors.Is to discriminate and map accordingly."
  - "RuntimeBlock + ContextBlock are *pointers* in the Manifest struct. Reason: nil-pointer detection is the cheapest way to assert §15.2's always-present-block invariant. Decoder presence of `\"runtime\": null` decodes to nil pointer; absent key also decodes to nil pointer. Empty-arrays-OK (the always-present-with-[] posture) tests the alternate path where the block is non-nil but its slices are length-0."

patterns-established:
  - "Manifest decoder boundary: POST + decode + version-assert ONLY. No scope filtering (--include-runtime / --only-runtime), no state interaction, no file I/O. The hydrate orchestrator (07-W1-06) is the single consumer of Fetch and the single owner of the next layer of policy. This mirrors CONTEXT.md D-09 Claude's Discretion guidance."
  - "Test fixture path discipline: tests reference examples/hydrate.json via the relative path \"../../../examples/hydrate.json\" (3 levels up from internal/cli/manifest/). This mirrors W3-P3 e2e test fixture access and keeps the unit test hermetic — no os.Getenv, no httptest needed for the round-trip."

requirements-completed:
  - STATE-09
  - STATE-11

# Note: STATE-11 (unconditional fetch) is PARTIALLY addressed — this plan
# ships the Fetch helper that unconditionally POSTs /platform/hydrate. The
# orchestrator layer (07-W1-06, commit-sequence step 5) is responsible for
# ensuring this Fetch is called once per `hydrate.Run` regardless of state
# claims, which is the full STATE-11 invariant. STATE-09 (schemaVersion
# strict + runtime/context presence) is fully covered by Decode here.

# Metrics
duration: 25min
completed: 2026-05-29
---

# Phase 7 Plan 07-W1-05: `internal/cli/manifest` POST /platform/hydrate Decoder Summary

**Strict-decode manifest package: schemaVersion=="v1alpha1" + runtime+context non-nil + DisallowUnknownFields + ContentRef.Endpoint round-trip — the single decode chokepoint for the hydrate engine path.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-05-29T13:46:00Z (worktree spawn)
- **Completed:** 2026-05-29T14:11:56Z
- **Tasks:** 1 (`auto` / `tdd=true`)
- **Files created:** 3 (doc.go + manifest.go + manifest_test.go)
- **Tracked commits:** 1 (`367ce8c`)

## Accomplishments

- `internal/cli/manifest/manifest.go` exports the `Manifest`, `RuntimeBlock`, `ContextBlock`, and `ContentRef` structs matching Hub spec §15.2 verbatim: `schemaVersion: "v1alpha1"`, `environment`, three runtime arms (`models`, `mcpServers`, `a2aAgents`), three context arms (`prompts`, `plugins`, `artifacts`).
- `ContentRef` carries `Endpoint string \`json:"endpoint,omitempty"\`` — runtime entries (every model/MCP-server/A2A-agent in `examples/hydrate.json`) round-trip the `endpoint` URL through Decode → re-marshal without loss. Adapters in 07-W3-01..04 will consume `m.Runtime.MCPServers[i].Endpoint` directly per ADAPT-03 without re-fetching the manifest.
- `manifest.Decode(io.Reader) (*Manifest, error)` uses `json.NewDecoder` + `DisallowUnknownFields()` and asserts the three §15.2 invariants in order: `SchemaVersion == "v1alpha1"`, `Runtime != nil`, `Context != nil`. Any of the three fails to ErrSchemaMismatch via `%w` wrap.
- `manifest.ErrSchemaMismatch` is the package sentinel — callers (07-W1-06) use `errors.Is(err, manifest.ErrSchemaMismatch)` to map to `exit.SchemaMismatch` (code 5, from 07-W1-01).
- `manifest.Fetch(ctx, *httpclient.Client, environment string) (*Manifest, error)` POSTs `/platform/hydrate` with body `{"environment": "<env>"}` (or `{}` when empty), buffers the response body, runs `Decode` on it. Non-2xx responses bubble up as `*httpclient.ServerError` via DoRaw.
- 10 tests pass (`./scripts/dev.sh make unit-pkg PKG=./internal/cli/manifest/...` exit 0):
  - `TestDecode_GoldenHydrate` — round-trips `examples/hydrate.json` and asserts every ContentRef.Endpoint and ContentRef.DownloadURL is non-empty on the appropriate arm.
  - `TestDecode_SchemaV2_ReturnsErrSchemaMismatch` — `schemaVersion: "v2"` → `errors.Is(err, ErrSchemaMismatch)`.
  - `TestDecode_NilRuntime_ReturnsErrSchemaMismatch` — `"runtime": null` → ErrSchemaMismatch.
  - `TestDecode_NilContext_ReturnsErrSchemaMismatch` — `"context": null` → ErrSchemaMismatch.
  - `TestDecode_EmptyRuntimeArrays_OK` — runtime/context blocks present with `[]` slices is valid (always-present-with-[] posture per §15.2).
  - `TestDecode_UnknownField_Rejects` — bogus top-level field is a decode error AND `errors.Is(err, ErrSchemaMismatch) == false` (two failure classes stay distinct).
  - `TestFetch_PostShape_BuildsCorrectBody` — `httptest.NewServer` sees POST + `/platform/hydrate` + body `{environment: demo}`.
  - `TestFetch_EmptyEnvironment_SendsEmptyObject` — env=="" path sends literal `{}`.
  - `TestFetch_EnvironmentWithEscapes_Encodes` — JSON-escape regression guard.
  - `TestFetch_ServerError_BubblesUp` — 401 surfaces as `*httpclient.ServerError` via `errors.As`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Manifest type + Decode + Fetch + schema assertion + ContentRef.Endpoint + DisallowUnknownFields** — `367ce8c` (`feat`). Combines test + implementation in one atomic commit (mirrors W1-01 collapse rationale — pre-commit hook enforces `go vet`/lint cleanliness, blocks strict RED commits; TDD discipline preserved procedurally by writing the test stubs first, then watching them fail to compile, then adding impl, then watching them pass).

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored). No `docs(...)` follow-up commit possible without `-f`-style force-stage, which is forbidden.

_TDD note: the plan's `tdd="true"` attribute was honored procedurally (test file written referencing yet-undefined symbols, verified compile-failure as the RED state, then impl added to flip to GREEN). RED and GREEN landed in one commit per the project's pre-commit gate posture._

## Files Created/Modified

- `internal/cli/manifest/doc.go` (new) — Package doc citing STATE-09 + Hub §15.2 + CLI §6.2 + D-09 boundary discipline; documents the ContentRef single-struct rationale (runtime entries populate Endpoint, context entries populate DownloadURL, both share ID+Name); documents the DisallowUnknownFields strict-shape posture mirroring W1-02 state.Decode.
- `internal/cli/manifest/manifest.go` (new) — `schemaV1Alpha1` const, `ContentRef` / `RuntimeBlock` / `ContextBlock` / `Manifest` structs with the json tags called out in the plan, `ErrSchemaMismatch` sentinel, `Decode(io.Reader)` strict-decode helper, `Fetch(ctx, *httpclient.Client, environment string)` wrapper. ~120 LOC.
- `internal/cli/manifest/manifest_test.go` (new) — 10 stdlib `testing` tests; no testify; uses `os.ReadFile("../../../examples/hydrate.json")` for the round-trip; uses `httptest.NewServer` for the four `Fetch` tests. ~280 LOC.

## Decisions Made

See `key-decisions` in frontmatter. Summary:
- **DoRaw + buffered Decode chosen over typed `client.Do(out=&Manifest{})`** — preserves the strict-decode posture without weakening `httpclient.Client.Do` (which intentionally tolerates additive server fields on other endpoints per its lines 127-134 comment).
- **Single ContentRef struct serves both runtime and context** — runtime entries populate Endpoint, context entries populate DownloadURL; omitempty keeps encoded output minimal. Adapters get one type to consume.
- **Unknown-field errors are NOT ErrSchemaMismatch** — two distinct failure classes (contract violation vs wire-format failure), kept distinguishable for the orchestrator's error mapping in 07-W1-06.
- **RuntimeBlock + ContextBlock are pointers** — nil-pointer detection is the cheapest §15.2 always-present-block guard. The empty-arrays-OK test covers the alternate "block present, slices empty" path.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pre-commit hook (`make pre-commit`) blocks the strict-TDD RED commit**
- **Found during:** Task 1 RED step
- **Issue:** The plan's `tdd="true"` attribute combined with the `<tdd_execution>` step in the executor system prompt mandates a separate RED commit (`test(...): add failing test for [feature]`) before the GREEN impl commit. But this project's `pre-commit` git hook runs `make pre-commit` which includes `go vet` + `golangci-lint` over the touched packages; a failing-to-compile RED test would trip the vet gate and the commit would be rejected. CLAUDE.md explicitly forbids `--no-verify` as a workaround. This is the SAME deviation W1-01 documented; same resolution applies.
- **Fix:** Collapsed RED + GREEN into a single atomic commit (`367ce8c`). TDD discipline preserved procedurally: the test file was written first referencing yet-undefined symbols (`manifest.ErrSchemaMismatch`, `manifest.Decode`, `manifest.Fetch`, `manifest.Manifest`, `manifest.ContentRef`); the impl was then added to satisfy those references; the combined diff was staged and committed atomically.
- **Files modified:** None (workflow change, not a code/test change)
- **Verification:** Final `./scripts/dev.sh make unit-pkg PKG=./internal/cli/manifest/...` exits 0 with all 10 tests passing.
- **Committed in:** `367ce8c` (the combined test+impl commit itself)

**2. [Rule 1 - Specification gap] Plan's literal grep gates `Endpoint string` and `MCPServers[0].Endpoint != ""` miss due to gofmt alignment + idiomatic Go assertion grammar**
- **Found during:** Task 1 acceptance-criteria verification
- **Issue:** The plan's `<acceptance_criteria>` includes two literal greps:
  - `grep -q "Endpoint string" internal/cli/manifest/manifest.go` — gofmt aligns field declarations within a struct, producing `Endpoint    string` (multiple spaces), so the literal single-space grep fails.
  - `MCPServers[0].Endpoint != ""` (implicitly an assertion grammar) — the idiomatic Go test pattern checks `== ""` and emits `t.Error` (negative-form assertion), not `!= ""` with `t.Fatal`. My test file uses the idiomatic form.
- **Fix:** No code change — the substantive requirements ARE met: the `Endpoint` field exists as a `string` with the correct json tag (verified via `grep -E "^\\s+Endpoint\\s+string" internal/cli/manifest/manifest.go`); the round-trip test DOES assert non-empty Endpoint on `MCPServers[0]` (lines 72-73 of manifest_test.go), just via the idiomatic `if … == "" { t.Error(…) }` form. Plan acceptance criteria are spec drift, not code drift.
- **Files modified:** None
- **Verification:** Semantic re-checks via regex grep pass; the round-trip test in fact asserts the literal expected URL `http://localhost:8080/mcp/demo-mcp-jwt` for `MCPServers[0].Endpoint` (stronger than the plan's bare non-empty check).
- **Committed in:** N/A

---

**Total deviations:** 2 auto-fixed (1 Rule 3 blocking-workflow, 1 Rule 1 spec gap)
**Impact on plan:** Both deviations were workflow/specification drift, not scope creep. The plan's intent (manifest decoder + Fetch + schema assertion + ContentRef.Endpoint + DisallowUnknownFields) was delivered verbatim. The TDD collapse is procedural (same as W1-01); the grep-gate misses are gofmt-alignment + idiomatic-Go artifacts. Acceptance criteria semantically pass.

## Issues Encountered

- `make lint-changed` defaults to `BASE_REF=origin/main` and only lints packages with COMMITTED diffs vs that ref. Uncommitted new packages don't appear in its file-walk output. Worked around by invoking `./bin/golangci-lint` directly on the new package (`./scripts/dev.sh ./bin/golangci-lint run ./internal/cli/manifest/...` exits 0) before staging. Post-commit, `make lint-changed` picks up the package automatically.
- `.planning/` is gitignored at the repo level. The SUMMARY.md is written to the main-repo's `.planning/phases/07-…/07-W1-05-SUMMARY.md` (the shared filesystem-level planning tree), NOT to a worktree-local copy. This is correct behavior per executor system-prompt guidance: "Worktree mode commits SUMMARY.md and REQUIREMENTS.md only. `.planning/` is gitignored — SUMMARY.md lives in main repo. Still write & save it per spec." Followed verbatim.

## User Setup Required

None — no external service configuration required. All changes are repo-internal (Go code).

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/manifest/doc.go ]           → FOUND
[ -f internal/cli/manifest/manifest.go ]      → FOUND
[ -f internal/cli/manifest/manifest_test.go ] → FOUND

# Commit existence
git log --oneline --all | grep 367ce8c → FOUND ("feat(07-W1-05): add internal/cli/manifest decoder + fetch for POST /platform/hydrate")

# Plan-level verification gates (semantic where the literal greps drift on gofmt/Go idioms)
grep -q "type Manifest struct" internal/cli/manifest/manifest.go        → OK
grep -q "var ErrSchemaMismatch" internal/cli/manifest/manifest.go       → OK
grep -q "func Decode" internal/cli/manifest/manifest.go                 → OK
grep -q "func Fetch" internal/cli/manifest/manifest.go                  → OK
grep -q "v1alpha1" internal/cli/manifest/manifest.go                    → OK
grep -q "DisallowUnknownFields" internal/cli/manifest/manifest.go       → OK
grep -E "^\s+Endpoint\s+string" internal/cli/manifest/manifest.go       → OK (gofmt aligns "Endpoint    string"; semantic match)
grep -q 'json:"endpoint,omitempty"' internal/cli/manifest/manifest.go   → OK
grep -q "examples/hydrate.json" internal/cli/manifest/manifest_test.go  → OK
grep -q "TestDecode_GoldenHydrate" internal/cli/manifest/manifest_test.go            → OK
grep -q "TestDecode_SchemaV2_ReturnsErrSchemaMismatch" …                              → OK
grep -q "TestDecode_NilRuntime_ReturnsErrSchemaMismatch" …                            → OK
grep -q "TestDecode_UnknownField_Rejects" …                                           → OK
# MCPServers[0].Endpoint non-empty assertion (idiomatic Go == "" + t.Error form):
grep -E 'MCPServers\[0\]\.Endpoint == ""' internal/cli/manifest/manifest_test.go     → OK (negative-form non-empty assertion)
./scripts/dev.sh make unit-pkg PKG=./internal/cli/manifest/... → exit 0 (10 tests pass)
./scripts/dev.sh ./bin/golangci-lint run ./internal/cli/manifest/... → exit 0
```

## Self-Check: PASSED

## Next Phase Readiness

- Wave-1 plan 07-W1-06 (commit-sequence orchestrator skeleton) can now `import "github.com/ackstorm/ach/internal/cli/manifest"` and call `manifest.Fetch(ctx, client, env)` in commit-sequence step 5. The returned `*Manifest` carries runtime + context arms ready for the orchestrator's diff-compute step (W1-06).
- Wave-3 adapter plans 07-W3-01..04 can `import` the same package and consume `m.Runtime.MCPServers[i].Endpoint` for runtime-config URL construction (ADAPT-03), and `m.Context.Plugins[i].DownloadURL` for content fetch in the plugin-transform path.
- 07-W1-06 orchestrator MUST map `errors.Is(err, manifest.ErrSchemaMismatch)` → `&exit.CodedError{Code: exit.SchemaMismatch, ...}` (5). The error-mapping helper in 07-W1-06 should also pass non-`ErrSchemaMismatch` decode-level errors through as `exit.General` (1), since they are wire-format failures not schema-contract violations.
- No blockers or concerns for downstream plans.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
