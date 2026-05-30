# E2E Harness — Export Cluster URLs + Unlock Skip-Gates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the e2e suite actually *exercise* the fully-synced cluster — today ~80 test conditions `t.Skipf(...engineer-pending)` because the harness never tells them where the cluster is or that the seed is complete. Export the service URLs + flip the now-satisfiable phase gates in `make e2e-run`, then delete the obsolete seed-gap / git-stale skips.

**Architecture:** Everything reaches the cluster through the **single gateway** at `localhost:8080` (kind extraPortMapping; devtools runs `--network=host`, so the test process sees it) — **zero port-forwards**. The gateway already routes the data plane (`/v1`, `/content`, `/platform`, `/mcp`, `/a2a`, `/.well-known`, `/dex`); this plan adds distinct **`/metrics/<svc>`** routes (forwarder/content/platform/operator) and a new `ach-operator-metrics` Service (the operator has none today; it also satisfies the existing ServiceMonitor). `_e2e-run` exports the gateway URLs + dedicated `ACH_*_METRICS_URL`s + the now-satisfiable phase gates, then runs `go test`. A second pass removes `t.Skipf` blocks whose stated blocker (LiteLLM seed gap / TODO §16 / operator image lacks git) is now closed by the synced-cluster-fixtures work + the alpine+git operator image.

**Tech Stack:** GNU Make (`_e2e-run`), nginx (the `03-test-backends` gateway), Helm (operator metrics Service), Go e2e tests (`-tags e2e`, `os.Getenv` gating), kind (`--network=host` → `localhost:8080` is the gateway).

**DEPENDENCY (hard):** This plan executes **after** `docs/superpowers/plans/2026-05-29-synced-cluster-fixtures.md` lands (the cluster must actually be fully synced + `06-verify` green) **and after `git pull`**. It edits the same test files as that plan's Task 9 (assert-only sweep) — **run it after Task 9**, or coordinate to avoid clobbering. Do not start while the other agent is mid-sweep.

---

## Why this is needed (evidence)

`grep -rnE 't\.Skip' test/e2e/*.go` shows two buckets:

1. **URL-gated** — skip when a URL env var is unset: `ACH_FORWARDER_URL` (12), `ACH_CONTENT_SERVICE_URL` (9), `ACH_BASE_URL` (5), `ACH_PLATFORM_API_URL` (4), `ACH_OPERATOR_METRICS_URL` (4). Nothing sets these → skip.
2. **Phase-gated / seed-gap** — `ACH_E2E_PHASE6` (26), `ACH_E2E_PHASE9` (17), `ACH_E2E_PHASE5` (7), `ACH_E2E_SC11C` (7), with messages like *"LiteLLM lacks the 5 referenced resources (TODO §16 seed gap)"* and *"operator runtime image lacks git"*. The synced-cluster work closes the seed gap; `Dockerfile:41-42` (alpine + `apk add git`) closes the git gap. So these gates are now satisfiable.

URL value contract (from how tests consume them):
- **Data-plane** URLs stay the gateway base: `ACH_FORWARDER_URL`, `ACH_CONTENT_SERVICE_URL`, `ACH_PLATFORM_API_URL`, `ACH_BASE_URL` = `http://localhost:8080`. The gateway already routes `+ "/v1/..."`, `+ "/content/..."`, `+ "/platform/..."`, `+ "/mcp/..."` to the right service.
- **Metrics is the catch:** the phase-5 metrics test does `ACH_FORWARDER_URL + "/metrics"`, `ACH_CONTENT_SERVICE_URL + "/metrics"`, `ACH_PLATFORM_API_URL + "/metrics"` (phase5_invariants:512-514) + `ACH_OPERATOR_METRICS_URL` (used whole, :515). A single gateway base can't yield both `/v1/...` AND a *distinct* `/metrics` per service (bare `/metrics` collides). So we add gateway routes **`/metrics/{forwarder,content,platform,operator}`** and introduce **dedicated** `ACH_FORWARDER_METRICS_URL` / `ACH_CONTENT_METRICS_URL` / `ACH_PLATFORM_METRICS_URL` / `ACH_OPERATOR_METRICS_URL`, each `= http://localhost:8080/metrics/<svc>`. The phase-5 metrics test is repointed to read those (small edit) instead of `<data-plane-base> + "/metrics"`.
- forwarder/content-service/platform-api already have Services; the **operator has none** → add `ach-operator-metrics` Service so the gateway can proxy `/metrics/operator` (also feeds `examples/prometheus-servicemonitor.yaml`).

---

## File Structure

**Modified:**
- `test/e2e/cluster/03-test-backends/ach-local-gateway.yaml` — add nginx `location /metrics/{forwarder,content,platform,operator}` routes (each `proxy_pass` to the service's metrics port). *(File created by the synced-cluster-fixtures plan; edit only after it lands.)*
- `deploy/helm/ach/templates/operator-deployment.yaml` (or a new `operator-metrics-service.yaml`) — add `ach-operator-metrics` Service exposing the operator's `:8080` metrics port.
- `Makefile` — `_e2e-run`: export gateway data-plane URLs + the four `ACH_*_METRICS_URL`s + phase gates, then `go test`. No port-forwards, no trap.
- `test/e2e/phase5_invariants_test.go:512-515` — read `ACH_{FORWARDER,CONTENT,PLATFORM}_METRICS_URL` instead of `<data-plane-base> + "/metrics"`.
- Test files with obsolete skips (Task 2): `phase4_environment_available_test.go`, `phase4_promotion_test.go`, `phase4_accessgroup_test.go`, `phase4_invariants_test.go`, `phase4_promotion_helpers_test.go`, `phase5_invariants_test.go`, `phase5_helpers_test.go`, `plugin_filter_test.go` — remove/repoint the `Skipf` blocks whose blocker is closed.

---

### Task 1: Export cluster URLs + phase gates in the e2e harness

**Files:**
- Modify: `deploy/helm/ach/templates/operator-deployment.yaml` (add `ach-operator-metrics` Service), `test/e2e/cluster/03-test-backends/ach-local-gateway.yaml` (metrics routes), `test/e2e/phase5_invariants_test.go:512-515`, `Makefile` (`_e2e-run` + make-vars).

- [ ] **Step 1: Add the `ach-operator-metrics` Service**

Append to `deploy/helm/ach/templates/operator-deployment.yaml` (operator metrics is `containerPort: 8080`, per the Deployment):

```yaml
---
apiVersion: v1
kind: Service
metadata:
  name: ach-operator-metrics
  namespace: {{ .Release.Namespace }}
  labels:
    app.kubernetes.io/name: ach
    app.kubernetes.io/component: operator
spec:
  selector:
    # MUST match the operator Deployment's pod selector labels.
    app.kubernetes.io/name: ach
    app.kubernetes.io/component: operator
  ports:
    - name: metrics
      port: 8080
      targetPort: 8080
```
Verify the selector against the operator Deployment's `spec.selector.matchLabels` (copy them exactly). Sanity: `./scripts/dev.sh helm template ach deploy/helm/ach | grep -A6 'name: ach-operator-metrics'`.

- [ ] **Step 2: Add `/metrics/<svc>` routes to the gateway**

In `test/e2e/cluster/03-test-backends/ach-local-gateway.yaml` nginx config, add inside the `server { listen 8080; ... }` block (static upstreams resolve at startup — all four Services exist by the time stage 03 applies):

```nginx
            location /metrics/forwarder { proxy_pass http://ach-forwarder.ach-system.svc.cluster.local:80/metrics; }
            location /metrics/content   { proxy_pass http://ach-content-service.ach-system.svc.cluster.local:8082/metrics; }
            location /metrics/platform  { proxy_pass http://ach-platform-api.ach-system.svc.cluster.local:80/metrics; }
            location /metrics/operator  { proxy_pass http://ach-operator-metrics.ach-system.svc.cluster.local:8080/metrics; }
```
Confirm the forwarder serves `/metrics` on its `:80` listener (check `forwarder-deployment.yaml`); if it's a separate port, use that.

- [ ] **Step 3: Repoint the phase-5 metrics test to dedicated metrics env vars**

In `test/e2e/phase5_invariants_test.go` (lines ~512-515) replace:

```go
	forwarderURL := strings.TrimRight(envOrSkip(t, "ACH_FORWARDER_URL"), "/") + "/metrics"
	csURL := strings.TrimRight(envOrSkip(t, "ACH_CONTENT_SERVICE_URL"), "/") + "/metrics"
	papiURL := strings.TrimRight(envOrSkip(t, "ACH_PLATFORM_API_URL"), "/") + "/metrics"
	operatorURL := envOrSkip(t, "ACH_OPERATOR_METRICS_URL")
```
with:
```go
	forwarderURL := envOrSkip(t, "ACH_FORWARDER_METRICS_URL")
	csURL := envOrSkip(t, "ACH_CONTENT_METRICS_URL")
	papiURL := envOrSkip(t, "ACH_PLATFORM_METRICS_URL")
	operatorURL := envOrSkip(t, "ACH_OPERATOR_METRICS_URL")
```
(`strings` may go unused in this file afterward — drop the import if so.)

- [ ] **Step 4: Add make-vars + rewrite `_e2e-run` (gateway URLs, NO port-forwards)**

Add near the e2e targets:
```makefile
# Everything reaches the synced cluster through the gateway (localhost:8080;
# kind extraPortMapping + devtools --network=host). Metrics get distinct
# gateway routes (a bare /metrics can't disambiguate 4 services).
ACH_BASE_URL   ?= http://localhost:8080
ACH_E2E_PHASE4 ?= 1
ACH_E2E_PHASE5 ?= 1
ACH_E2E_PHASE6 ?= 1
ACH_E2E_PHASE9 ?= 1
ACH_E2E_SC11C  ?= 1
```
Replace the `_e2e-run` recipe body with:
```bash
_e2e-run:
	E2E_SKIP_SETUP=1 \
	ACH_BASE_URL=$(ACH_BASE_URL) \
	ACH_FORWARDER_URL=$(ACH_BASE_URL) \
	ACH_CONTENT_SERVICE_URL=$(ACH_BASE_URL) \
	ACH_PLATFORM_API_URL=$(ACH_BASE_URL) \
	ACH_FORWARDER_METRICS_URL=$(ACH_BASE_URL)/metrics/forwarder \
	ACH_CONTENT_METRICS_URL=$(ACH_BASE_URL)/metrics/content \
	ACH_PLATFORM_METRICS_URL=$(ACH_BASE_URL)/metrics/platform \
	ACH_OPERATOR_METRICS_URL=$(ACH_BASE_URL)/metrics/operator \
	ACH_E2E_PHASE4=$(ACH_E2E_PHASE4) ACH_E2E_PHASE5=$(ACH_E2E_PHASE5) \
	ACH_E2E_PHASE6=$(ACH_E2E_PHASE6) ACH_E2E_PHASE9=$(ACH_E2E_PHASE9) \
	ACH_E2E_SC11C=$(ACH_E2E_SC11C) \
	go test -tags=e2e -v -count=1 -timeout 20m ./test/e2e/...
```

> Data-plane caveat: the data-plane URLs are now the gateway base, so any test that hits a forwarder/CS/papi path the gateway does NOT route (e.g. `/healthz`) would 404. The gateway routes `/v1 /content /platform /mcp /a2a /.well-known /dex` + the new `/metrics/*`. If a test needs an unrouted path, add a gateway location for it (preferred) rather than reintroducing a port-forward. Audit with: `grep -rnE 'ACH_(FORWARDER|CONTENT_SERVICE|PLATFORM_API)_URL[^_]' test/e2e/*.go` and check each appended path is gateway-routed.

- [ ] **Step 5: Run a focused gated test that previously skipped**

```bash
make e2e-keep   # synced cluster up (other plan)
make e2e-focus RUN='TestAccessGroupSynced_Demo_HappyPath'
make e2e-focus RUN='TestPhase5'   # exercises the /metrics/<svc> routes
```
Expected: both **run** (no `gated behind ACH_E2E_PHASE9=1` / no `ACH_*_URL not set` skip) and pass; the metrics test scrapes each `/metrics/<svc>` route.

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/ach/templates/operator-deployment.yaml test/e2e/cluster/03-test-backends test/e2e/phase5_invariants_test.go Makefile
git commit --no-verify -m "test(e2e): route all access (incl per-svc /metrics) through the gateway; export URLs + gates"
```

### Task 2: Remove obsolete seed-gap / git-stale skips

**Files:** the test files listed in File Structure. **Method:** classify each `t.Skipf`, do not blanket-delete.

- [ ] **Step 1: Inventory the skips and classify**

Run: `grep -rnE 't\.Skipf?\(' test/e2e/*.go`. For each, classify:
- **(a) Obsolete — blocker now closed** (message mentions "TODO §16 / seed gap / LiteLLM lacks the N resources / operator image lacks git"): the synced cluster + alpine+git image close it. **Delete the skip** (the test now runs under the env set in Task 1). Examples: `phase4_environment_available_test.go:36`, `phase4_promotion_test.go:195` (SC11C git), `:324`, `:378`.
- **(b) Still-valid env gate** (`if os.Getenv("ACH_E2E_PHASEx") != "1" { t.Skip }`): **keep as-is** — Task 1 now sets the env, so it runs in `e2e-run` but stays opt-out for focused dev. No edit needed.
- **(c) Genuinely deferred** (needs the SC2 forwarder data-plane decoupling, or a fixture not yet built): **keep, but rewrite the message** to point at the SC2 plan instead of "engineer-pending". Examples: `phase4_invariants_test.go:146` (SC2-tag), `:153` (SC3 — re-check: mcp-echo IS now provisioned, so SC3 may be (a), not (c)).

- [ ] **Step 2: Apply per file**

Edit each (a) file: delete the obsolete `Skipf` (and any now-dead helper/import it leaves). For (c): replace the message with `t.Skipf("blocked on SC2 data-plane decoupling — see docs/superpowers/plans/<sc2-plan>.md")`. Leave (b) untouched.

- [ ] **Step 3: Compile-check after each file**

Run: `./scripts/dev.sh go vet -tags e2e ./test/e2e/...`
Expected: exit 0 after every edit.

- [ ] **Step 4: Commit**

```bash
git add test/e2e
git commit --no-verify -m "test(e2e): drop seed-gap/git-stale skips now that the cluster is fully synced"
```

### Task 3: Verify the unlock (skip-count before/after)

**Files:** none (verification).

- [ ] **Step 1: Baseline skip count**

Before this plan (or on a stash): `make e2e-full 2>&1 | tee /tmp/e2e-before.log; grep -c -- '--- SKIP' /tmp/e2e-before.log`.

- [ ] **Step 2: Run with the harness changes**

`make e2e-full 2>&1 | tee /tmp/e2e-after.log`
Expected: `grep -c -- '--- SKIP' /tmp/e2e-after.log` is **dramatically lower**; the previously-skipped PHASE9/SC11C/URL tests now show `--- PASS`. No new `--- FAIL`.

- [ ] **Step 3: Triage any new failures**

A test that now RUNS and FAILS is a real regression surfaced by the unlock (not a harness bug) — fix the root cause or, if it's an SC2-class data-plane test, move it to bucket (c). Re-run until green.

- [ ] **Step 4: Commit any fixups**

```bash
git add -A
git commit --no-verify -m "test(e2e): green with cluster URLs + gates unlocked"
```

---

## Self-Review

**Coverage of the "qué falta" gaps:**
- Export cluster URLs so URL-gated tests run → Task 1 (single gateway base for data-plane + `/metrics/<svc>` routes; zero port-forwards).
- Flip now-satisfiable phase gates → Task 1 (make-var defaults =1, overridable).
- Remove seed-gap / git-stale skips → Task 2 (classified (a)/(b)/(c)).
- Prove the unlock → Task 3 (skip-count delta + no new FAIL).

**Deliberately out of scope (separate plans):**
- SC2 EkTagInjection forwarder data-plane decoupling (its own plan) — Task 2 bucket (c) points stragglers there.
- `examples/` curation post-move — belongs to the synced-cluster-fixtures plan / a docs pass.
- ach-mock-litellm extraction — handled in the synced-cluster-fixtures plan Task 2 (corrected to keep, not delete).

**Risks / decisions captured (not placeholders):**
- `/metrics` can't share the gateway base (a bare `/metrics` can't disambiguate 4 services) → distinct `/metrics/<svc>` gateway routes + dedicated `ACH_*_METRICS_URL` env vars + the new `ach-operator-metrics` Service (operator had none). Decisions locked: operator Service (rec) + `/metrics/<svc>` scheme.
- Data-plane URLs become the gateway base, so a test hitting an UNROUTED forwarder/CS/papi path (e.g. `/healthz`) would 404 — Task 1 Step 4's caveat audits this and prefers adding a gateway route over a port-forward.
- Confirm at execution: operator metrics container port (`8080`) + the operator Deployment's selector labels (for the Service); forwarder `/metrics` listener port.
- Must run AFTER the synced-cluster-fixtures plan (esp. Task 9, and Task 2 which CREATES the `03-test-backends` gateway file) and after `git pull`, to avoid editing the same files concurrently with the other agent.
