---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 01
subsystem: infra
tags: [litellm, rest-client, controller-runtime, redacting-transport, sister-lift]

# Dependency graph
requires:
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: |
      litellm.Client interface (DeleteAccessGroup, DeleteTag) + NoopClient
      implementation typed against the interface; cmd/operator/main.go wires
      NoopClient via dependency injection.
provides:
  - "RESTClient struct + NewRESTClient constructor in internal/litellm/restclient.go"
  - "Widened Client interface (7 methods total) — 2 Phase-1 carry-forwards + 5 new"
  - "ListModels / ListMCPServers / ListA2AAgents — Plan 07 snapshot-Runnable consumers"
  - "ListUserKeys / RevokeKey — Plan 08 orphan-cleanup-Runnable consumers"
  - "DeleteAccessGroup / DeleteTag implementations on RESTClient (§6.5 step 2/3 LiteLLM calls)"
  - "ACH_LITELLM_AUTH_HEADER + ACH_LITELLM_DANGEROUSLY_LOG_BODIES env-var contract"
  - "Redacting RoundTripper (default log payload {method, path, status, latency_ms} — never headers, never bodies)"
  - "ErrNotFound + Auth401Error + litellmErrorEnvelope shared by Plan 02 source-fetcher error classification"
affects:
  - Plan 02-07 (LiteLLM-snapshot manager.Runnable)
  - Plan 02-08 (orphan LiteLLM key cleanup Runnable)
  - Plan 02-09 (cmd/operator/main.go swap NoopClient → NewRESTClient + ACH_LITELLM_* env validation)

# Tech tracking
tech-stack:
  added: []      # No new direct deps; logr already transitive via controller-runtime.
  patterns:
    - "Sister-project verbatim lift with mechanical rename map (LITELLM_OPERATOR_* → ACH_LITELLM_*; Client struct → RESTClient struct)"
    - "Two-implementation Client interface (RESTClient production, NoopClient test/Phase-1 stub) — both anchored by compile-time `var _ Client = (*X)(nil)` assertions"
    - "Redacting RoundTripper as load-bearing §9.1 boundary — no body/header logging unless ACH_LITELLM_DANGEROUSLY_LOG_BODIES=true"
    - "REL-04 drain-and-close discipline (defer drainAndClose every code path) → HTTP keepalive reuse; covered by sister TestMakeRequestDefersDrainAndClose"
    - "REL-05 length-check before indexing on every list-returning helper → ErrNotFound on empty"
    - "REL-06 typed *Auth401Error → caller's §7.7 fast-path resolved via errors.As"
    - "List-methods return ErrNotFound on empty set; D-13 snapshot-Runnable wraps to empty slice (an empty intersection is a real result, not an error)"

key-files:
  created:
    - "internal/litellm/restclient.go (RESTClient struct, NewRESTClient ctor, setAuth, makeRequest, IsAuth401 helper)"
    - "internal/litellm/transport.go (redactingRoundTripper, newHTTPClient, drainAndClose, ACH_LITELLM_DANGEROUSLY_LOG_BODIES)"
    - "internal/litellm/team.go (CreateTeam/UpdateTeam/DeleteTeam/ListTeamsByAlias)"
    - "internal/litellm/model.go (CreateModel/UpdateModel/DeleteModel/GetModelInfo/GetModelInfoByName/ListModels)"
    - "internal/litellm/mcp.go (CreateMCPServer/UpdateMCPServer/DeleteMCPServer/ListMCPServers)"
    - "internal/litellm/agents.go (CreateAgent/UpdateAgent/DeleteAgent/ListAgents/ListA2AAgents)"
    - "internal/litellm/keyinfo.go (ProbeConnection/ListUserKeys/RevokeKey)"
    - "internal/litellm/accessgroups.go (DeleteAccessGroup, DeleteTag) — net-new file for §6.5 step 2/3"
    - "internal/litellm/errors.go (ErrNotFound, Auth401Error, classify, litellmErrorEnvelope, processLitellmError)"
    - "internal/litellm/types.go (lifted wire types + UserKeyInfo + ListUserKeysResponse new for Phase 2)"
    - "internal/litellm/client_test.go, transport_test.go, team_test.go, model_test.go, mcp_test.go, agents_test.go (~1,170 lines lifted)"
  modified:
    - "internal/litellm/client.go (Client interface widened: 2 → 7 methods + var _ Client = (*RESTClient)(nil) assertion)"
    - "internal/litellm/noop.go (extended with 5 log-only stubs)"
    - "internal/litellm/doc.go (rewritten for ACH; ACH_LITELLM_* env-var names; spike-specific paragraphs removed)"
    - "go.mod (go mod tidy demoted onsi/gomega from direct → indirect — only transitively used now)"

key-decisions:
  - "Interface-and-struct file split (Option B): client.go owns the Client interface declaration + RESTClient assertion; restclient.go owns the lifted struct + ctor + makeRequest/setAuth. Avoids 280-line single-file blob and keeps the public surface visually obvious."
  - "ListA2AAgents is a thin wrapper over ListAgents — the LiteLLM endpoint name (/v1/agents) is unchanged; only the ACH-domain wrapper name reflects D-13's A2A terminology."
  - "DeleteAccessGroup + DeleteTag live in a new accessgroups.go file (sister project has neither) rather than being squeezed into restclient.go — they share no other surface with restclient.go beyond receiver type."
  - "RevokeKey deliberately does NOT emit audit events; audit emission is the orphan-cleanup-Runnable's responsibility (D-18, Plan 08). Preserves separation of concerns per [feedback_litellm_operator_no_redaction_filter]."
  - "NoopClient list-helpers return (nil, nil) not (nil, ErrNotFound). The Plan 07 snapshotter wraps ErrNotFound → empty slice anyway; (nil, nil) makes NoopClient-driven unit tests behave identically without the wrapping shim."

patterns-established:
  - "Verbatim sister lift + rename map: every new ACH file is a near-byte-identical copy of `../ach_litellm/internal/litellm/<file>` modulo the LITELLM_OPERATOR_* → ACH_LITELLM_* env-var rename and the Client struct → RESTClient struct rename. Plan 02 source-fetchers and other Phase 2 plans MAY rely on this pattern (the lifted errors.go's classify() + processLitellmError() are the canonical error-classification helpers)."
  - "Compile-time interface assertion is the only enforcement against drift: two assertions (one per implementation) sit at the bottom of client.go and noop.go. A future plan adding a third implementation MUST add its own assertion."

requirements-completed:
  - OP-13
  - OP-15

# Metrics
duration: 6min
completed: 2026-05-15
---

# Phase 2 Plan 01: Lift Sister LiteLLM Package into ACH Summary

**Lifted `../ach_litellm/internal/litellm/` (10 source files + 6 test files) into `ach/internal/litellm/` with `LITELLM_OPERATOR_*` → `ACH_LITELLM_*` env-var rename and `Client struct` → `RESTClient struct` rename; widened the Phase 1 `Client` interface from 2 methods to 7 (added `ListModels`, `ListMCPServers`, `ListA2AAgents`, `ListUserKeys`, `RevokeKey`) and extended `NoopClient` with five log-only stubs so the compile-time `var _ Client = (*NoopClient)(nil)` assertion stays green.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-15T18:01:59Z
- **Completed:** 2026-05-15T18:08:11Z
- **Tasks:** 2 (+ 1 chore commit for `go mod tidy`)
- **Files modified:** 19 (3 modified, 16 created — 10 lifted source + 6 lifted test files; accessgroups.go is the only net-new ACH file)

## Accomplishments

- Phase 1's two-method `Client` interface (`DeleteAccessGroup`, `DeleteTag`) preserved verbatim and widened with the five methods Plan 07 (snapshot Runnable) and Plan 08 (orphan-cleanup Runnable) need: `ListModels`, `ListMCPServers`, `ListA2AAgents`, `ListUserKeys`, `RevokeKey`.
- `RESTClient` concrete struct + `NewRESTClient(endpoint, masterKey, log)` constructor compiled and ready. The constructor reads `ACH_LITELLM_AUTH_HEADER` at construction time (default `Authorization: Bearer`, escape-hatch `x-litellm-api-key`). The redacting RoundTripper reads `ACH_LITELLM_DANGEROUSLY_LOG_BODIES` once at construction (default redaction ON).
- `NoopClient` extended with five log-only stubs (`ListModels`, `ListMCPServers`, `ListA2AAgents` return `nil, nil`; `ListUserKeys` returns `nil, nil`; `RevokeKey` logs and returns `nil`). The Phase 1 `var _ Client = (*NoopClient)(nil)` assertion still holds against the widened interface.
- Zero `LITELLM_OPERATOR_*` literals remain anywhere under `internal/litellm/`. All env-var, const, and docstring references renamed to the `ACH_LITELLM_*` prefix per D-02.
- All lifted sister tests pass under `go test ./internal/litellm/... -count=1` — including the §9.1 redaction canary, REL-04 keepalive-reuse 1000-request leak test, REL-05 length-check matrix, REL-06 401 propagation across every helper, and Pitfall-2 "POST /model/update body-id, never URL-id" path assertion.

## Task Commits

Each task was committed atomically:

1. **Task 1: Lift sister litellm package + rename env vars + introduce RESTClient struct** — `d177f4b` (feat)
2. **Task 2: Widen Client interface, add five new methods on RESTClient, extend NoopClient stubs** — `ee20e8f` (feat)
3. **Chore: go mod tidy after lift** — `9a3f1e7` (chore) — demotes `onsi/gomega` from direct → indirect since no code imports it directly; the Phase 1 e2e suite is build-tag-gated and does not pull it.

**Plan metadata commit:** _(pending after this SUMMARY)_

## Files Created/Modified

**Created (verbatim or near-verbatim lifts from sister, with renames applied):**

- `internal/litellm/restclient.go` (179 lines) — `RESTClient` struct, `NewRESTClient` constructor, `setAuth`, `makeRequest`, `IsAuth401` helper. Lifted from sister `client.go` with `Client` struct → `RESTClient` struct and `NewClient` → `NewRESTClient` renames.
- `internal/litellm/transport.go` (130 lines) — `redactingRoundTripper`, `newHTTPClient`, `drainAndClose`. Verbatim lift with env-var rename.
- `internal/litellm/team.go` (78 lines) — `CreateTeam` / `UpdateTeam` / `DeleteTeam` / `ListTeamsByAlias`. Verbatim lift with receiver rename.
- `internal/litellm/model.go` (166 lines) — `CreateModel` / `UpdateModel` / `DeleteModel` / `GetModelInfo` / `GetModelInfoByName` lifted; **new `ListModels`** (GET `/v1/model/info`, REL-05 length-check, returns `ErrNotFound` on empty per D-13).
- `internal/litellm/mcp.go` (76 lines) — `CreateMCPServer` / `UpdateMCPServer` / `DeleteMCPServer` / `ListMCPServers`. Verbatim lift; `ListMCPServers` already existed in sister.
- `internal/litellm/agents.go` (80 lines) — `CreateAgent` / `UpdateAgent` / `DeleteAgent` / `ListAgents` lifted; **new `ListA2AAgents`** wraps `ListAgents` (LiteLLM endpoint name unchanged; ACH wrapper name reflects A2A domain per D-13).
- `internal/litellm/keyinfo.go` (77 lines) — `ProbeConnection` lifted; **new `ListUserKeys`** (GET `/key/info?user_id=`) and **new `RevokeKey`** (POST `/key/delete` with `{keys:[id]}`) per D-16.
- `internal/litellm/accessgroups.go` (36 lines) — **net-new ACH file**. `DeleteAccessGroup` (DELETE `/access-groups/<name>`) and `DeleteTag` (DELETE `/tags/<name>`) on `*RESTClient`. The sister project has no analog for these endpoints (it manages models / MCPs / agents, not access-groups / budget-tags).
- `internal/litellm/errors.go` (108 lines) — `ErrNotFound`, `Auth401Error`, `ErrorKind` / `classify`, `litellmErrorEnvelope`, `processLitellmError`. Verbatim lift with env-var rename in doc comment.
- `internal/litellm/types.go` (284 lines) — all sister wire types verbatim + **new `UserKeyInfo`** and **new `ListUserKeysResponse`** appended at bottom (D-16 wire shapes).
- `internal/litellm/{client,transport,team,model,mcp,agents}_test.go` (~1,170 lines combined) — verbatim sister tests; `client_test.go` `newTestClient` helper switched to `*RESTClient` / `NewRESTClient` to match the rename.

**Modified:**

- `internal/litellm/client.go` (104 lines, was 62) — `Client` interface widened from 2 to 7 methods; `var _ Client = (*RESTClient)(nil)` compile-time assertion appended.
- `internal/litellm/noop.go` (104 lines, was 67) — 5 new log-only stubs (`ListModels`, `ListMCPServers`, `ListA2AAgents`, `ListUserKeys`, `RevokeKey`). Existing `var _ Client = (*NoopClient)(nil)` assertion preserved.
- `internal/litellm/doc.go` (58 lines, was 44) — rewritten: ACH_LITELLM_* env-var names documented, Phase 1 spike-specific paragraphs removed, contract for the two-implementation `Client` interface spelled out.
- `go.mod` — `onsi/gomega` demoted from direct → indirect (it was a Phase 1 direct dep but no code currently imports it; the e2e suite is build-tag-gated).

## Decisions Made

- **Interface-and-struct file split (Option B from the plan).** `client.go` keeps the `Client` interface declaration + the `var _ Client = (*RESTClient)(nil)` assertion; the lifted `RESTClient` struct + constructor + private helpers live in `restclient.go`. Keeps the public surface visually obvious: one file per "what consumers should look at" (the interface) and one file per "the production implementation".
- **`ListA2AAgents` wraps `ListAgents`.** Sister's `ListAgents` already covers `GET /v1/agents?health_check=false`; ACH's wrapper name reflects D-13's A2A-agent terminology. The single-line wrapper preserves the LiteLLM-endpoint name on the wire while exposing the ACH-domain name to reconcilers.
- **`DeleteAccessGroup` + `DeleteTag` get their own file (`accessgroups.go`).** Sister has no analog (it does not manage LiteLLM access-groups or budget-tags); rather than squeezing them into `restclient.go`, a new file makes them easy to find when the §6.5 deletion sequence is read by reviewers.
- **`RevokeKey` does NOT emit audit events.** Per D-18 the audit emission lives at the orphan-cleanup Runnable layer (Plan 08), so `Client.RevokeKey` stays purely an HTTP-call helper. Matches the [feedback_litellm_operator_no_redaction_filter] discipline: the client surface neither scrubs nor produces audit payloads.
- **NoopClient list-helpers return `(nil, nil)`, not `(nil, ErrNotFound)`.** The Plan 07 snapshotter wraps `ErrNotFound` → empty slice anyway; `(nil, nil)` makes NoopClient-driven unit tests behave identically without the wrapper. The RESTClient helpers DO return `ErrNotFound` per D-13.

## Deviations from Plan

None - plan executed exactly as written.

The plan explicitly anticipated that `DeleteAccessGroup` and `DeleteTag` would need to be added on `*RESTClient` because the sister project has no analog ("if the sister lift does NOT include them verbatim, ADD them in this task"). They were added in `accessgroups.go`; this is planned scope, not a deviation.

The `go mod tidy` cleanup (removed `onsi/gomega` direct dep) was explicit in the plan's action: "Run `./scripts/dev.sh go mod tidy` to pull any sister-side dependencies not yet in go.mod". The lift introduced no new direct deps (logr was already transitive via controller-runtime), and tidy correctly demoted the e2e suite's unused gomega line. Committed as a separate chore commit for clarity.

## Issues Encountered

None.

The single mechanical pitfall (a `sed` invocation lost a single space between `*RESTClient)` and the method name in `model.go`) was caught immediately by `go build`, fixed with a follow-up `sed -i 's/func (c \*RESTClient)\([A-Z]\)/func (c *RESTClient) \1/g'`, and built cleanly on the next iteration. The fix landed before Task 1 was committed.

## User Setup Required

None - no external service configuration required by this plan. Plan 02-09 (the cmd/operator/main.go wiring) will introduce the `ACH_LITELLM_BASE_URL` + `ACH_LITELLM_MASTER_KEY` startup env-var contract; this plan only adds the package-level `ACH_LITELLM_AUTH_HEADER` and `ACH_LITELLM_DANGEROUSLY_LOG_BODIES` constants (consumed at NewRESTClient construction time, not at process startup).

## Next Phase Readiness

- **Plan 02-02 (source-type fetchers):** the lifted `errors.go` (with `Auth401Error`, `classify`, `processLitellmError`) is the canonical error-classification source for Plan 02's six new source-type fetchers. The `drainAndClose` helper in `transport.go` is the canonical REL-04 pattern fetchers reuse (or copy verbatim).
- **Plan 02-07 (LiteLLM-snapshot Runnable):** the widened `Client` interface exposes `ListModels` / `ListMCPServers` / `ListA2AAgents` as required. The Snapshotter wraps `errors.Is(err, ErrNotFound)` → empty slice per D-13.
- **Plan 02-08 (orphan-cleanup Runnable):** the widened `Client` interface exposes `ListUserKeys` + `RevokeKey` as required. Audit emission is the Runnable's responsibility (not RevokeKey's).
- **Plan 02-09 (cmd/operator/main.go wiring):** swap `litellm.NewNoopClient(...)` → `litellm.NewRESTClient(baseURL, masterKey, log)`; add fail-fast `MustEnvNonEmpty` checks for `ACH_LITELLM_BASE_URL` + `ACH_LITELLM_MASTER_KEY`; replace `_ = noopLiteLLM` plumbing with `realLiteLLM` injection. Reconcilers do NOT change — they still type their dependency as `litellm.Client`.
- **Threat surface:** no new network endpoints, auth paths, file access, or schema changes introduced by this plan. Plan 02's threat-model T-02-01 (master-key leakage via redacting transport) and T-02-02 (NewRESTClient masterKey logging discipline) are both satisfied by the verbatim sister lift; both are enforceable by `grep -n "Header\|Authorization\|masterKey\|MasterKey" internal/litellm/{transport,restclient}.go`.

## Self-Check: PASSED

Verified after writing this SUMMARY:

- `internal/litellm/restclient.go` exists (179 lines).
- `internal/litellm/accessgroups.go` exists (36 lines).
- `internal/litellm/client.go` widened (104 lines, declares 7 interface methods + 1 RESTClient assertion).
- `internal/litellm/noop.go` extended (104 lines, declares 7 NoopClient methods + assertion).
- Commits exist: `d177f4b` (Task 1), `ee20e8f` (Task 2), `9a3f1e7` (chore: go mod tidy).
- `go build ./...` → exit 0.
- `go test ./internal/litellm/... -count=1` → ok (sister tests pass under the rename).
- `grep -rn 'LITELLM_OPERATOR_' internal/litellm/` → exit 1 (no matches).

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Plan: 01*
*Completed: 2026-05-15*
