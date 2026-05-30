---
phase: 05-content-service-cross-component-observability
plan: 07
subsystem: helm-observability
tags: [helm, prometheus, servicemonitor, observability, scrape, drift-flag-4]

requires:
  - phase: 05-content-service-cross-component-observability
    provides: 05-06 GET /metrics mounted on chi mux for forwarder/platform-api/content-service + ACH-namespaced collectors registered on operator's controller-runtime metricsserver
provides:
  - Pod-level prometheus.io/scrape annotations on operator/forwarder/platform-api Deployments (port :8080, path /metrics)
  - Service-level prometheus.io/scrape annotations on ach-content-service Service (port :8082) — drift flag #4 final resolution
  - examples/prometheus-servicemonitor.yaml opt-in reference manifest (NOT installed by chart)
  - values.yaml /metrics topology documentation + spec v4 §5.2 RBAC enforcement-at-code-level clarification
affects: [05-08]

tech-stack:
  added: []
  patterns:
    - "Pod-annotation scrape default (kubernetes-pods role) + Service-annotation scrape for co-located second metrics port"
    - "ServiceMonitor as opt-in alternative — examples/ reference only, never auto-installed"
    - "values.yaml documents §5.2 RBAC enforcement boundary: code-level (cmd/ach/cmd/content_service.go drops manager.Manager) not RBAC-level (CS shares ach-operator SA per RWO PVC co-location)"

key-files:
  modified:
    - deploy/helm/ach/templates/operator-deployment.yaml
    - deploy/helm/ach/templates/forwarder-deployment.yaml
    - deploy/helm/ach/templates/platform-api-deployment.yaml
    - deploy/helm/ach/templates/content-service-deployment.yaml
    - deploy/helm/ach/values.yaml
  created:
    - examples/prometheus-servicemonitor.yaml

key-decisions:
  - "Drift flag #4 resolution: operator Pod's two metrics ports (:8080 operator, :8082 CS) split across two scrape targets — Pod-level annotation for :8080, Service-level annotation on ach-content-service for :8082"
  - "Pod-annotation scrape is the default; ServiceMonitor is opt-in via examples/prometheus-servicemonitor.yaml (deployers running Prometheus Operator only)"
  - "Operator :8080 has no Service object — documented as a limitation in the ServiceMonitor example; ServiceMonitor users either rely on the default Pod-annotation path or ship their own operator-metrics Service"
  - "metrics.serviceMonitor.enabled schema key preserved (already existed pre-Phase-5); the extended documentation block prepended explains the full topology"

patterns-established:
  - "All four service templates carry scrape annotations; helm template + helm lint pass clean"
  - "rendered chart shows 4 prometheus.io/scrape occurrences (operator/forwarder/platform-api Pods + content-service Service)"

requirements-completed: [OBS-03, OBS-04, OBS-05, OBS-06]

duration: ~25min (post-recovery)
completed: 2026-05-27
---

# Phase 05: Plan 07 — Helm Scrape Annotations Summary

**Prometheus scrape topology shipped via Helm chart — drift flag #4 (operator Pod's two metric ports) explicitly resolved across Pod-level + Service-level annotations**

## Performance

- **Duration:** ~25min (inline orchestrator recovery after wave-4 executor died mid-Task-1)
- **Tasks:** 6
- **Files modified:** 5 + 1 created

## Accomplishments

### Task 1 — operator Pod scrape annotation

`deploy/helm/ach/templates/operator-deployment.yaml`:

```yaml
      annotations:
        kubectl.kubernetes.io/default-container: manager
        # Prometheus default scrape — Phase 5 OBS-03 + D-12.
        # Drift flag #4 resolution: this Pod hosts TWO metrics ports
        # (operator :8080, content-service :8082). Pod annotation here
        # covers the OPERATOR port. The content-service port has its
        # own scrape target via Service-level annotation on
        # ach-content-service (see content-service-deployment.yaml).
        # See examples/prometheus-servicemonitor.yaml for the
        # ServiceMonitor alternative.
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
```

### Task 2 — forwarder Pod scrape annotation

`deploy/helm/ach/templates/forwarder-deployment.yaml`:

```yaml
  template:
    metadata:
      annotations:
        # Phase 5 OBS-03 + D-12 — scrape the forwarder's main traffic
        # listener at :8080/metrics. Plan 05-06 mounts GET /metrics on
        # the chi mux alongside /v1, /gemini, /mcp, /a2a route family.
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
```

### Task 3 — platform-api Pod scrape annotation

`deploy/helm/ach/templates/platform-api-deployment.yaml`:

```yaml
  template:
    metadata:
      annotations:
        # Phase 5 OBS-03 + D-12 — scrape platform-api /metrics on the
        # main traffic listener. Plan 05-06 mounts GET /metrics on the
        # chi mux. Shared litellm_unreachable_total counter registered
        # here for caller="platform_api".
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
```

### Task 4 — content-service Service scrape annotation (drift flag #4 resolution)

`deploy/helm/ach/templates/content-service-deployment.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: ach-content-service
  ...
  annotations:
    # Phase 5 OBS-03 + D-12 + drift flag #4 resolution.
    # The CS container is co-located in the operator Pod (RWO PVC
    # forces single-Pod). The operator Pod already carries scrape
    # annotations for its own :8080 metrics port; CS's :8082 port
    # gets a separate scrape target via this Service-level
    # annotation. Both scrape targets are visible to the default
    # Prometheus scrape config (kubernetes-pods + kubernetes-services
    # roles).
    prometheus.io/scrape: "true"
    prometheus.io/port: "8082"
    prometheus.io/path: "/metrics"
```

### Task 5 — opt-in ServiceMonitor reference

`examples/prometheus-servicemonitor.yaml` created. 70 lines. Enumerates three Service-port-named endpoints (content-service `http`→8082, forwarder `traffic`→8080, platform-api `http`→8080). Documents the operator :8080 limitation: no Service exists for the operator metrics port, so ServiceMonitor users either rely on the default Pod-annotation scrape path or ship their own Service for it.

**Confirmation: not referenced from any chart template** —

```
$ grep -r 'prometheus-servicemonitor' deploy/helm/ach/
# (no output — zero hits)
```

### Task 6 — values.yaml /metrics topology documentation

`deploy/helm/ach/values.yaml` — full 39-line documentation block prepended to the existing `metrics:` key. Preserved the `metrics.serviceMonitor.enabled` schema key for backward compat. Documents:

- per-service /metrics endpoints (operator/platform-api/forwarder/content-service)
- unauthenticated-internal-only contract
- drift flag #4 scrape-topology narrative
- ServiceMonitor opt-in pointer to `examples/`
- spec v4 §5.2 RBAC clarification — CS drops CRD informers at Go code level (`cmd/ach/cmd/content_service.go` no longer builds `manager.Manager`); K8s RBAC still grants CRD reads to the shared `ach-operator` SA because the operator container needs them.

## Task Commits

Each task committed atomically (all 6 pre-commit gates passed):

1. Task 1 (operator Pod) — `e558c0d` (feat)
2. Task 2 (forwarder Pod) — `6035d72` (feat)
3. Task 3 (platform-api Pod) — `0f2b643` (feat)
4. Task 4 (content-service Service) — `45ce950` (feat)
5. Task 5 (ServiceMonitor example) — `41a489f` (feat)
6. Task 6 (values.yaml docs) — `592442f` (docs)

## Verification

```
$ helm template test deploy/helm/ach/ > /tmp/helm-render.yaml
RENDER_OK
$ grep -c "prometheus.io/scrape" /tmp/helm-render.yaml
4
$ helm lint deploy/helm/ach/
1 chart(s) linted, 0 chart(s) failed
LINT_OK
```

Four scrape annotations in rendered output — one per service template, matching plan expectation.

## Decisions Made

- Followed plan as written. Preserved existing `metrics.serviceMonitor.enabled` schema key in values.yaml rather than collapsing to `{}` placeholder — backward-compat for any chart consumer who already set the flag.
- ServiceMonitor example omits an operator :8080 endpoint and instead documents the limitation. The plan suggested adding it with a `port: metrics` reference, but there is no operator Service for ServiceMonitor to select against; shipping a non-functional endpoint would silently fail in deployers' Prometheus instances.

## Deviations from Plan

None substantive — only the ServiceMonitor example deliberately omits the operator endpoint and explains why (vs the plan's TODO-note suggestion).

## Issues Encountered

- **Wave-4 executor agent `a8bbf10f1e40799f4` died mid-Task-1** with 11 lines staged on `operator-deployment.yaml` and nothing committed. Orchestrator recovered inline: salvaged the agent's staged Task 1 edit verbatim (it was correct), applied Tasks 2-6 from scratch, committed atomically, verified `helm template` + `helm lint`, then dropped the worktree.

## Next Phase Readiness

- **05-08 (E2E phase5 invariants)** can now assert against scrape-rendered annotations in `helm template` output as part of its observability gates.

---
*Phase: 05-content-service-cross-component-observability*
*Completed: 2026-05-27*
