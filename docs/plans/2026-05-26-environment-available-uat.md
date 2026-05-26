# Environment.Available=True — UAT validation gate (TODO §16)

**Created:** 2026-05-26
**TODO source:** §16 — validation gate proving Environment.Available=True end-to-end
**Plan type:** UAT / acceptance validation — NOT a feature plan. The Phase B
Ginkgo spec is the only piece of new Go code; everything else is shell
choreography against existing reconciler surface.
**Status when authored:** **BLOCKED** on two upstream plans landing first
(hard gates — see Pre-flight). The plan is ready to execute the moment
those merge.

---

## Why this plan exists

`examples/04-environment-demo.yaml` references 5 LiteLLM-side runtime
names that do NOT exist in a fresh cluster: `gemini.gemini-flash-latest`,
`openai.gpt-5-mini`, `vmcp-dev`, `vmcp-aws`, `test-noop-agent`. Today
LiteLLM is hydrated with only `gpt-3.5-turbo` + `fake-openai-endpoint`,
zero MCP servers, zero A2A agents. Apply the demo CR and you see exactly
what J.6 wired: `ExecutionResourcesResolved=False reason=ResourceUnresolved`
+ placeholder `AccessGroupSynced=Unknown` + placeholder `Available=Unknown`.

That is the **deliberate "unresolved" UAT fixture**. The §16 gate proves
that once §7 (AccessGroupSynced reconciler) and §9 (Available composite
rollup) land, and the 5 LiteLLM resources are seeded, the demo CR
converges to:

```yaml
status:
  conditions:
    - {type: ExecutionResourcesResolved, status: "True",  reason: Resolved}
    - {type: AccessGroupSynced,           status: "True",  reason: Synced}
    - {type: Available,                   status: "True",  reason: AllSubConditionsTrue}
  unresolvedRuntime:
    models: []
    mcpServers: []
    a2aAgents: []
```

This plan delivers:
1. **Phase A** — one-shot manual UAT (`scripts/uat-environment-available.sh`)
   executed by a human against a live kind cluster. Captures evidence
   that §7+§9 work in production wiring.
2. **Phase B** — automated UAT (`test/e2e/phase4_environment_available_test.go`)
   that drives the same flow under Ginkgo with an httptest fake LiteLLM,
   so the gate runs in CI every PR forever.
3. **Phase C** — cleanup verification: `kubectl delete environment demo`
   fires §6.5 `DeleteAccessGroup` + `DeleteTag` against the fake LiteLLM
   call recorder.

---

## Pre-flight — HARD GATES

This plan **cannot execute** until both of the following land on `main`:

### Gate 1: TODO §7 — AccessGroupSynced reconciler

The Environment reconciler must own real access-group binding logic, NOT
the J.6 placeholder. Concretely:
- Reconciler calls LiteLLM's create-or-update-access-group + bind-team
  flow for `spec.authorizedTeams`.
- Emits `AccessGroupSynced=True reason=Synced` on success.
- Emits `AccessGroupSynced=False reason=BindFailed` (or similar) on
  LiteLLM error.
- The `if !hasCondition(...)` placeholder block in
  `internal/controller/ach/environment_controller.go` lines 246-255
  is removed (the real reconciler now writes the condition
  unconditionally).

**Gate marker:** record the commit SHA that lands §7 here before
executing this plan:

```
§7 SHA: __________________________________________ (fill in before Phase A)
```

### Gate 2: TODO §9 — Available composite rollup

The Environment reconciler must compute `Available` as the AND of its
sub-conditions per the Hub §6.6 closed set. Concretely:
- `Available=True reason=AllSubConditionsTrue` when
  `ExecutionResourcesResolved=True` AND `AccessGroupSynced=True` (plus
  any other sub-conditions §9 adds — e.g. `ContentReady` from the
  context.* projection).
- `Available=False reason=<first-failing-sub-condition>` otherwise.
- The `if !hasCondition(...)` placeholder block in
  `internal/controller/ach/environment_controller.go` lines 257-266 is
  removed.

**Gate marker:**

```
§9 SHA: __________________________________________ (fill in before Phase A)
```

### Pre-flight verification

Before running any task in this plan, confirm both SHAs exist and the
placeholder blocks are gone:

```bash
cd /home/jcm/Projects/ach
git log --oneline --grep='AccessGroupSynced' | head -3
git log --oneline --grep='composite\|Available rollup' | head -3
grep -A2 'TODO §7' internal/controller/ach/environment_controller.go
grep -A2 'TODO §9' internal/controller/ach/environment_controller.go
```

**Expected:** the two commits exist; the two `TODO §7` / `TODO §9`
hasCondition guards are gone (replaced by real reconcile logic).

If either gate is still open, STOP and re-route to the upstream plan.
Running this UAT against the J.6 placeholders proves nothing.

---

## Architecture

Two execution modes against the same reconciler logic:

```
┌──────────────────────────┐         ┌────────────────────────────┐
│ Phase A — manual UAT     │         │ Phase B — automated UAT    │
│ scripts/uat-environment- │         │ test/e2e/phase4_env_       │
│ available.sh             │         │ available_test.go          │
├──────────────────────────┤         ├────────────────────────────┤
│ Real kind cluster        │         │ Real kind cluster          │
│ (cluster-up hydrates     │         │ (E2E_SKIP_SETUP=1 reuses)  │
│  Postgres, Valkey, Dex,  │         │                            │
│  LiteLLM, ach modes)     │         │                            │
│                          │         │                            │
│ Real BerriAI LiteLLM     │   vs.   │ httptest fake LiteLLM      │
│ on localhost:4001        │         │ exposed via NodePort or    │
│ (curl admin-API to seed  │         │ kubectl port-forward       │
│  5 resources)            │         │ (returns 200 for the 5     │
│                          │         │  names; records calls for  │
│                          │         │  Phase C assertions)       │
│                          │         │                            │
│ Forces snapshot refresh  │         │ Same touch-annotation path │
│ via touch annotation on  │         │ or seeds + waits 5min      │
│ the Environment CR (or   │         │ (FAST=1 cuts to 30s by     │
│ waits ≤5 min)            │         │  reading Snapshotter test  │
│                          │         │  hook)                     │
│                          │         │                            │
│ Captures Environment     │         │ Asserts 3 conditions True  │
│ YAML for human review    │         │ + unresolvedRuntime empty  │
└──────────────────────────┘         └────────────────────────────┘
```

Both modes hit the same `EnvironmentReconciler.Reconcile` path — Phase A
proves the live wiring is correct; Phase B proves the contract stays
green as the codebase evolves.

---

## Tech stack

- Bash (Phase A script — kubectl + curl + yq).
- Go stdlib `testing` + `httptest` (Phase B — follows `test/e2e/*` idiom
  per Phase 02.3 decision; **NOT Ginkgo** despite the task prompt's
  Ginkgo phrasing — the suite is stdlib, see
  `feedback_023_tier_framework_rejected`).
- Existing `test/e2e/utils/` + `test/e2e/mock/` helpers — no new
  dependencies.
- Existing Makefile targets: `cluster-up`, `wait-cr-ready`,
  `operator-redeploy`, `e2e-focus`.

---

## Test surface

This plan has THREE assertable surfaces:

1. **Phase A evidence** — `scripts/uat-environment-available.sh` prints
   `PASS` and saves the converged Environment YAML to
   `/tmp/ach-uat-environment-available.yaml`. A human pastes the result
   into the PR description.
2. **Phase B CI gate** — `make e2e-focus FOCUS=TestEnvironmentAvailable`
   passes in CI for every PR touching `internal/controller/ach/`,
   `internal/litellm/`, `internal/snapshot/`, or `examples/`.
3. **Phase C delete-path assertion** — Phase B's fake LiteLLM records
   `DeleteAccessGroup("demo")` + `DeleteTag("demo")` after
   `kubectl delete environment demo` returns.

---

## Tasks

### Task 0 — Pre-flight gate confirmation [BLOCKING]

Run the Pre-flight verification block above. Record both SHAs in the
gate-marker placeholders. If either is missing, STOP.

**Verify:**
```bash
cd /home/jcm/Projects/ach
grep -E 'TODO §[79]' internal/controller/ach/environment_controller.go
```
**Expected:** no output (placeholders removed by §7 + §9).

---

## Phase A — Manual UAT (~15 min, one-shot)

### Task A.1 — Author `scripts/uat-environment-available.sh` [NEW]

A single bash script that:
1. Asserts cluster is up (`kubectl get nodes` returns ≥1 Ready node).
2. Asserts the 4 sub-modes are Ready (`make wait-operator`,
   `wait-platform-api`, `wait-postgres`, `wait-dex` — `make wait-redis`
   if it exists). The Snapshotter runs in-process inside the operator
   so no separate Deployment wait is needed.
3. Applies `examples/04-environment-demo.yaml` (idempotent — uses
   `kubectl apply`).
4. Waits ≤30s for `AccessGroupSynced=True`:
   ```bash
   make wait-cr-ready KIND=environment NAME=demo NS=ach-system \
     CONDITION=AccessGroupSynced WAIT_TIMEOUT=30s
   ```
   If `wait-cr-ready` doesn't accept a `CONDITION=` override today,
   add it (single-line `--for=condition=$(CONDITION)` substitution).
5. Seeds the 5 LiteLLM resources via admin API. The exact endpoint set
   from TODO §16:
   ```bash
   BASE=http://localhost:4001
   KEY="sk-test-master-key"   # the cluster.sh hydrate_litellm default

   # 2 models
   curl -fsS -X POST $BASE/model/new -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' \
     -d '{"model_name":"gemini.gemini-flash-latest","litellm_params":{"model":"gemini/gemini-flash-latest"}}'
   curl -fsS -X POST $BASE/model/new -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' \
     -d '{"model_name":"openai.gpt-5-mini","litellm_params":{"model":"openai/gpt-5-mini"}}'
   # 2 MCP servers
   curl -fsS -X POST $BASE/v1/mcp/server -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' \
     -d '{"server_name":"vmcp-dev","url":"http://localhost:9100/mcp","transport":"sse"}'
   curl -fsS -X POST $BASE/v1/mcp/server -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' \
     -d '{"server_name":"vmcp-aws","url":"http://localhost:9101/mcp","transport":"sse"}'
   # 1 A2A agent
   curl -fsS -X POST $BASE/v1/agents -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' \
     -d '{"agent_name":"test-noop-agent","url":"http://localhost:9200/a2a"}'
   ```
   Each curl uses `-fsS` so HTTP non-2xx aborts the script with the
   server's error body. The fake upstream URLs (`localhost:9100/9101/
   9200`) are deliberately bogus — LiteLLM accepts the registration;
   the operator's Snapshotter only reads the name set, never proxies a
   request to those URLs.
6. Force a snapshot refresh — the Snapshotter polls every 5 min by
   default, so the script either:
   - (a) waits ≤5 min via `make wait-cr-ready KIND=environment NAME=demo
     NS=ach-system CONDITION=Available WAIT_TIMEOUT=320s` (use 320s so
     a freshly-ticked snapshot has slack), OR
   - (b) annotates the CR to force an event-driven reconcile:
     ```bash
     kubectl annotate environment demo -n ach-system \
       ach.ackstorm.ai/touch="$(date -u +%FT%TZ)" --overwrite
     ```
     Then waits ≤30s. NOTE: touch-annotation only triggers a reconcile,
     NOT a fresh snapshot poll — if §7/§9 reconciler reads
     `r.Snapshotter.Snapshot()` (lock-free cached pointer), the stale
     snapshot will still report unresolved. Use path (a) by default; if
     §7/§9 land a `Snapshotter.Touch()` hook that forces a poll,
     reference it here.
7. Captures evidence:
   ```bash
   kubectl get environment demo -n ach-system -o yaml \
     > /tmp/ach-uat-environment-available.yaml
   yq '.status.conditions[] | select(.type | test("Available|AccessGroupSynced|ExecutionResourcesResolved")) | "\(.type)=\(.status) reason=\(.reason)"' \
     /tmp/ach-uat-environment-available.yaml
   ```
   **Expected output (exactly):**
   ```
   AccessGroupSynced=True reason=Synced
   Available=True reason=AllSubConditionsTrue
   ExecutionResourcesResolved=True reason=Resolved
   ```
   And:
   ```bash
   yq '.status.unresolvedRuntime' /tmp/ach-uat-environment-available.yaml
   ```
   **Expected:** `null` OR `{models: null, mcpServers: null, a2aAgents: null}`
   (empty slices are omitempty-marshaled). The script accepts either.
8. Prints `PASS` and exits 0. On any assertion failure prints `FAIL:
   <reason>` to stderr and exits 1.

**File:** `scripts/uat-environment-available.sh` (new — `chmod +x` after
write).

**Verify the script itself with shellcheck:**
```bash
./scripts/dev.sh shellcheck scripts/uat-environment-available.sh
```
**Expected:** no output (clean).

**Commit:** `test(uat): scripts/uat-environment-available.sh — §16 manual gate`

### Task A.2 — Execute the manual UAT against live kind [HUMAN]

```bash
cd /home/jcm/Projects/ach
make cluster-up                        # ~3 min cold; ~30s warm
./scripts/uat-environment-available.sh # ~30s if snapshot ticks fast
```

**Expected last line:** `PASS`.

**Evidence:** paste the contents of `/tmp/ach-uat-environment-available.yaml`
(or just the `status.conditions` + `status.unresolvedRuntime` excerpts)
into the PR description for this plan's execution. The PR reviewer
should see the three True conditions verbatim.

If FAIL — common diagnoses:
- `ExecutionResourcesResolved=False reason=ResourceUnresolved` after
  5 min: LiteLLM seeding failed (check `kubectl logs -n ach-system
  deploy/ach-operator | grep snapshot`); rerun the curls.
- `AccessGroupSynced=False`: §7 reconciler hit a LiteLLM error
  (check operator logs for the bind-team call). Not a UAT bug — file
  back to §7.
- `Available=False reason=PendingSubConditions`: §9 reconciler ran
  before §7 produced a True condition. Should self-heal on the next
  requeue (≤5 min); if it doesn't, §9 has a watch-dependency bug.

### Task A.3 — Tear down and confirm §6.5 drain fires [HUMAN]

```bash
kubectl delete environment demo -n ach-system
# Watch operator logs for the §6.5 drain trio:
kubectl logs -n ach-system deploy/ach-operator -f | grep -E '§6.5|access.group|tag|finalizer'
```

**Expected log lines (in order, within ~5s of the delete):**
```
DELETE LiteLLM access group {"name":"demo"}
DELETE LiteLLM tag {"name":"demo"}
§6.5 drain complete; finalizer removed {"env":"demo"}
```

The actual log shape depends on whether `internal/litellm/RESTClient`
emits structured slog fields — adjust the grep if the real lines look
different. The point of the assertion is: drain runs, both LiteLLM
DELETEs happen, finalizer comes off, CR disappears.

Confirm CR is gone:
```bash
kubectl get environment demo -n ach-system 2>&1
```
**Expected:** `Error from server (NotFound): environments.ach.ackstorm.ai "demo" not found`.

---

## Phase B — Automated UAT (Ginkgo→stdlib testing) (~3 hours)

### Task B.1 — Add a fake LiteLLM helper to `test/e2e/utils/` [NEW]

Build an httptest.Server that:
- Accepts `POST /model/new`, `POST /v1/mcp/server`, `POST /v1/agents`,
  `POST /access-groups`, `POST /tags`, `DELETE /access-groups/<name>`,
  `DELETE /tags/<name>`.
- Maintains an in-memory registry of registered names per kind.
- Serves `GET /models`, `GET /v1/mcp/server`, `GET /v1/agents` — the
  three endpoints `snapshot.Snapshotter` polls — returning the
  registered names in the shape `internal/litellm` expects.
- Records every DELETE call into an accessor:
  `(f *FakeLiteLLM) DeletedAccessGroups() []string` and
  `(f *FakeLiteLLM) DeletedTags() []string`.

**File:** `test/e2e/utils/fake_litellm.go`

```go
//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Package utils — httptest fake LiteLLM for §16 phase4 automated UAT.
//
// Implements the minimum surface the Snapshotter polls (GET /models,
// /v1/mcp/server, /v1/agents) plus the admin-API endpoints the manual
// UAT script POSTs to. DELETE calls into access-groups + tags are
// recorded for the §6.5 drain-verification assertion in Phase C.
package utils

// (full implementation omitted from this plan — write per the contract
// above; mirror the curl payload shapes from Task A.1 verbatim, and
// the GET response shapes from internal/litellm/{model,mcp,agents}.go)
```

The exact JSON-decode/encode shapes come from the real
`internal/litellm/*.go` clients — read those for the wire contract.
Test the fake in isolation first:

```bash
./scripts/dev.sh go test ./test/e2e/utils/ -run TestFakeLiteLLM -v
```

The unit test (`test/e2e/utils/fake_litellm_test.go`, no `e2e` build tag
on the test or with it — author's choice; if no-tag, ensure it doesn't
import e2e-tagged symbols) should:
- POST a model, GET /models, assert the name is in the response.
- DELETE an access group, assert `DeletedAccessGroups()` records it.

**Verify:**
```bash
./scripts/dev.sh go test -tags=e2e ./test/e2e/utils/...
```

**Commit:** `test(e2e): utils — fake LiteLLM for §16 phase4`

### Task B.2 — Write `test/e2e/phase4_environment_available_test.go` [NEW]

**File:** `test/e2e/phase4_environment_available_test.go`

```go
//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 — Environment.Available=True end-to-end (TODO §16).
//
// Stdlib testing per the Phase 02.3 decision (no Ginkgo). The test
// drives the Phase 3 helper pattern (phase3SuiteGuard etc.) adapted
// to Phase 4 — guards that the §7 + §9 reconcilers are wired (gate
// the test on a probe of the Environment CR's condition set), then
// exercises the converge path.
//
// Cross-references TODO §11 (e2e UAT promotion) — this file is the
// canonical "phase4" slot in the suite.

package e2e

import (
    "testing"
    "time"

    "github.com/ackstorm/ach/test/e2e/utils"
)

func TestEnvironmentAvailable(t *testing.T) {
    // Subtests:
    t.Run("AccessGroupSyncedTrueOnApply", testEnvAccessGroupSyncedTrue)
    t.Run("ResolvesWhenAllNamesSeeded",   testEnvResolvesWhenSeeded)
    t.Run("AvailableTrueAfterRollup",     testEnvAvailableTrueAfterRollup)
    t.Run("DeleteFiresSixFiveDrain",      testEnvDeleteFiresDrain)
}
```

Each subtest:

**`testEnvAccessGroupSyncedTrue`** — apply the demo CR, wait ≤30s for
`AccessGroupSynced=True reason=Synced`. Asserts §7 wiring works.

**`testEnvResolvesWhenSeeded`** — start fake LiteLLM, swap the
operator's LITELLM_URL env to point at it (via patching the Deployment
+ `operator-redeploy`, OR by configuring the cluster's LiteLLM Service
to ExternalName-alias the fake — pick whichever is least invasive given
the §7/§9 wiring). POST the 5 names to the fake. Trigger snapshot
refresh (annotate Environment with `ach.ackstorm.ai/touch=` OR sleep
6 min — the assertion uses `wait-cr-ready` with `WAIT_TIMEOUT=350s`).
Assert `ExecutionResourcesResolved=True reason=Resolved` and
`unresolvedRuntime` is empty.

**`testEnvAvailableTrueAfterRollup`** — after the previous subtest's
state is in place, assert `Available=True reason=AllSubConditionsTrue`.
This is the §9 contract test.

**`testEnvDeleteFiresDrain`** — `kubectl delete environment demo`,
wait for the CR to disappear, assert
`fake.DeletedAccessGroups()` contains `"demo"` and `fake.DeletedTags()`
contains `"demo"`. This is the §6.5 contract test.

**Wiring the fake into the cluster** — the operator reads LiteLLM URL
from a Helm value (`litellm.url` or similar). Two viable approaches:

- **(A) ExternalName Service swap** — leave operator's LITELLM_URL
  pointing at `http://litellm.ach-system.svc:4000`, but patch that
  Service to ExternalName-alias the fake's host:port (the fake runs on
  the test process, exposed to the kind cluster via
  `host.docker.internal` or a kind extraMounts socket).
- **(B) Patch + restart the operator** — `kubectl set env
  deploy/ach-operator -n ach-system LITELLM_URL=http://host.docker.internal:<fake-port>`
  then `kubectl rollout restart`. Higher latency per test run (~15s for
  the restart) but no Service surgery.

**Pick (B)** — simpler, isolates the test to a single Deployment
mutation, restorable via `kubectl rollout undo`. The latency hit
(~15s) is acceptable for a once-per-PR phase4 gate.

t.Cleanup registers:
- `kubectl delete environment demo --ignore-not-found`
- `kubectl rollout undo deploy/ach-operator -n ach-system`
- fake.Close()

**Verify:**
```bash
./scripts/dev.sh make e2e-keep                 # cluster up, kept
./scripts/dev.sh make e2e-focus FOCUS=TestEnvironmentAvailable
```

**Expected:** all 4 subtests PASS, total runtime ≤8 min (the snapshot
wait dominates if path (a) is used in `testEnvResolvesWhenSeeded`; ≤3
min if path (b) touch-annotation is supported).

**Commit:** `test(e2e): phase4 — TestEnvironmentAvailable (§16 gate)`

### Task B.3 — Document the snapshot-poll forcing mechanism [DECISION]

If §7/§9 land a programmatic way to force a Snapshotter tick (e.g.
`r.Snapshotter.Refresh(ctx)` or a touch-annotation handler that calls
it), use that and document the call site. Otherwise, the phase4 test
spends ~5 min on the snapshot-tick wait.

Worth a separate ~30 min spike if the test time hits 8+ min, OR if §9
already needs a Refresh hook for its own event flow. Defer to §9's
author; this plan does NOT block on it.

If the spike is taken, add a new task `B.3a` here updating the test to
use the hook; until then leave Task B.2 as-is.

### Task B.4 — Wire phase4 into CI [NEW]

`make e2e-full` runs the entire suite — TestEnvironmentAvailable will
join it automatically once the test file lands (the build tag is the
gate, and TestMain already handles cluster setup).

Confirm with one PR-shaped CI run:
```bash
make e2e-full
```
**Expected:** TestEnvironmentAvailable appears in `go test -v` output
between phase3 tests and the suite tail, all subtests PASS.

No CI workflow file edit needed. If the suite grows past the workflow
timeout (currently the e2e job has a `timeout-minutes:` — check
`.github/workflows/ci.yml`), bump it by ~10 min.

**Commit:** none (no file changes — CI integration is implicit).

---

## Phase C — Cleanup verification (covered by Task B.2's
`testEnvDeleteFiresDrain` subtest)

No new tasks — the §6.5 drain assertion lives inside Phase B's last
subtest, against the fake's call recorder. Phase A's manual Task A.3
covers the same path against real LiteLLM (which has the operations as
no-ops if those routes return 404 — that's fine, the operator just
needs to issue the DELETE).

---

## Task summary

| # | Task | Status | Type |
|---|------|--------|------|
| 0 | Pre-flight gate confirmation (§7 SHA + §9 SHA) | **BLOCKING** | gate |
| A.1 | Author `scripts/uat-environment-available.sh` | **NEW** | script |
| A.2 | Execute manual UAT against live kind | **NEW** | human/evidence |
| A.3 | Tear down, confirm §6.5 drain fires (manual) | **NEW** | human/evidence |
| B.1 | Fake LiteLLM helper `test/e2e/utils/fake_litellm.go` | **NEW** | Go code |
| B.2 | `test/e2e/phase4_environment_available_test.go` | **NEW** | Go code |
| B.3 | Snapshot-poll forcing mechanism (DECISION/spike) | optional | decision |
| B.4 | Wire phase4 into CI (implicit via build tag) | **NEW** | verification |

**Total:** 8 tasks (1 gate, 1 optional spike, 6 actionable).

**Critical path:** 0 → A.1 → A.2 → A.3 → B.1 → B.2 → B.4.
B.3 is parallel-safe at any time.

**Estimated time:**
- Phase A: ~20 min (script ~10 min, run ~10 min including the snapshot
  wait if path (a) is used).
- Phase B: ~3 hours (fake LiteLLM ~90 min, phase4 test file ~90 min).
- Total: ~3.5 hours of focused work.

---

## Cross-plan references

- **BLOCKED ON §7** (AccessGroupSynced reconciler). Hard gate — Task 0.
- **BLOCKED ON §9** (Available composite rollup). Hard gate — Task 0.
- **COMPLEMENTS §11** (e2e UAT promotion). The phase4 test slot lives
  in the same `test/e2e/` suite §11 manages.
- **SUPERSEDED-BY §10** (CLI). When `ach login` + `ach hydrate` land,
  the manual `scripts/uat-environment-available.sh` becomes redundant —
  the same flow drives through the CLI. Delete A.1's script at that
  point and replace it with a one-liner `ach env apply demo &&
  ach env wait --for=available demo`. The Phase B Ginkgo (stdlib) test
  in B.2 stays — it's the per-PR CI gate either way.
- **OPERATIONALLY ADJACENT to §3** (Helm multi-component plan). When
  that plan lands the `wait-content-service` target, this plan benefits
  for free in Task A.1.

---

## Final acceptance

The plan is COMPLETE when:

1. `scripts/uat-environment-available.sh` exists, is executable, and
   exits 0 against a fresh `make cluster-up` cluster with the §7+§9
   binary running.
2. `/tmp/ach-uat-environment-available.yaml` shows the three True
   conditions verbatim, and the YAML is pasted into the merge PR's
   description as evidence.
3. `make e2e-focus FOCUS=TestEnvironmentAvailable` passes locally.
4. `make e2e-full` passes locally and on the PR's CI run.
5. CLAUDE.md's "test phases" / "e2e debug loop" section gets one new
   bullet under e2e that says "phase4 covers §16 — Environment converges
   to Available=True once §7+§9 reconcilers report green." (Single-line
   doc-hygiene fix per CLAUDE.md's "Documentation hygiene" rule.)
6. `TODO §16` line in `/home/jcm/Projects/ach/TODO` (or the actual TODO
   file location — `/home/jcm/Projects/ach/TODO` per the working tree
   snapshot) is removed (TODO is the inbox; §16 graduates into
   `docs/plans/2026-05-26-environment-available-uat.md`).

---

## Commit cadence

One commit per actionable task:
1. `test(uat): scripts/uat-environment-available.sh — §16 manual gate` (A.1)
2. (no commit — A.2 + A.3 are human-execution + evidence capture)
3. `test(e2e): utils — fake LiteLLM for §16 phase4` (B.1)
4. `test(e2e): phase4 — TestEnvironmentAvailable (§16 gate)` (B.2)
5. `docs(claude): note phase4 §16 gate in e2e test phases` (acceptance #5)
6. `docs(todo): graduate §16 to executed plan` (acceptance #6)

After all six land, the demo CR's promise — "this is what an
ACH Environment looks like when everything works" — becomes a CI-enforced
invariant. Any future regression that breaks the converge path fails
phase4 on the offending PR, not in production six months later.
