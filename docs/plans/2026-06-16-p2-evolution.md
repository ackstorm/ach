# P2 Evolution Implementation Plan (G2-build, G16, G7-alerts)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver the three P2 ("evolution") gaps from `docs/external-review-resolutions.md`: (G7-alerts) recommended Prometheus alert rules; (G16) content-service HA via an RWX storage-class split into its own Deployment; (G2-build) the UI Objects API + GitOps export + the promotion reconcile that flips a matching `ui` row to `cr`.

**Architecture:** ACH = single `ach` Go binary (cobra-mode-per-subcommand) + separate `ach-cli`; per-mode Helm Deployments (operator, platform-api, forwarder, gateway) sharing one image, content-service today a sidecar in the operator Pod (RWO PVC). Projection tables in Postgres are the read SoT; the operator is the only writer (`origin='cr', locked=true`), origin-gated UPSERTs. Metrics live in `internal/metrics/` (bare registry per service).

**Tech Stack:** Go, chi, pgx, controller-runtime, prometheus/client_golang, Helm, goreleaser, sigs.k8s.io/yaml (already a dep).

---

## Ground-truth corrections (trust THESE over the decisions doc and the frozen spec)

Verified against the working tree 2026-06-16:

1. **G2 — `origin` column value is `'cr'`, NOT the frozen spec's `'kubernetes'`.** Delivered code uses `'ui'` and `'cr'` (migrations 000005/8/9; `db/tx.go:40` `upsertReturning`). Use `'ui'`/`'cr'` throughout; treat the spec's `'kubernetes'` as `'cr'`.
2. **G2 — there is NO `spec_json` column.** The frozen spec (§15.x) assumed a generic `spec_json JSONB`; the delivered tables are **per-field domain-projection columns** (`db/environments.go`, `db/plugins.go`, …). Byte-equivalent promotion (§15.8 step 3) requires a stored canonical spec to compare against. **This is the load-bearing design fork — see "Recommended default to ratify" below.**
3. **G2 — the promote half is genuinely absent.** Confirmed: zero matches for `PromotionMismatch`, origin-flip `UPDATE`, or spec-canonicalization anywhere in Go. Only the block half exists (`ErrOriginConflict` → `ConflictWithUIRow`, `db/errors.go:29`, set in 6 controllers).
4. **G2 — `sigs.k8s.io/yaml v1.6.0` is already a direct dep** (`go.mod:47`); no new YAML serializer needed. CRD Go types live in `api/ach/v1alpha1/`.
5. **G16 — the split is anticipated in-code** at `operator-deployment.yaml:154-156` ("If the storage class is upgraded to RWX … this Pod can later be split"). `ach content-service` is already a standalone cobra mode with no k8s dependency beyond the shared cache dir (`cmd/ach/cmd/content_service.go`). The `ach-content-service` Service exists today but its selector points at the **operator** Pod (`content-service-deployment.yaml:31-38`) — must flip to a `content-service` component when standalone. Gateway already routes `/content/`→`ach-content-service:8082` by DNS (`internal/gateway/routes.go:30`) — no gateway change.
6. **G7-alerts — 3 of 5 alerts are buildable now** (`content_service_requests_total{outcome="stale_cache_expired"}`, `litellm_unreachable_total`, `forwarder_requests_total{key_type="pk"}` all EXIST). 2 are **blocked on P1 metrics** (`operator_external_ref_refresh_total`, `environment_available` — confirmed absent today; land via the P1 code plan tasks 5-7).
7. **No `helm lint`/`promtool` CI gate exists.** Chart validation today is `make helm-sync-check` (CRD drift only). New Helm templates verify via `helm template --set …` rendering + grep assertions.

### Recommended default to ratify (G2) — canonical-spec store

The frozen §15.8 promotion step compares the applied CR's canonicalized `spec` byte-for-byte against the stored row. With per-field projection columns there is nothing to compare against. **Recommended: add a `spec_json JSONB NULL` column to each UI-writable object table**, populated with the canonical (sorted-key JSON) spec on every `origin='ui'` write; NULL for `origin='cr'` rows OR backfilled by the operator on its next reconcile (so promotion can compare). The operator, on CR apply, canonicalizes the CR spec the SAME way and compares to `spec_json`. This is the smallest delta to the existing schema (vs a parallel `ui_objects` table, which would duplicate the projection write path). **This plan assumes that default. If you'd rather use a separate `ui_objects` table, stop and re-plan Phase G2-A.**

### Sequencing

- **G7-alerts** depends on P1 metrics (code-plan tasks 5-7) for 2 of 5 alerts → do the 3 ready alerts anytime; gate the 2 blocked alerts behind P1 landing.
- **G16** is independent — can run anytime.
- **G2-build** is a multi-week epic — independent of G16/G7 but the largest. Do it last or in its own worktree.

**Verify commands (host has no Go — via make):** `make test-unit-pkg PKG=…`, `make test-integration` (Docker Postgres, `-tags=integration`), `make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS=…`, `make qa-lint-changed`, `make helm-sync-check`, `make e2e-focus RUN='…'`. Helm rendering: `./scripts/dev.sh helm template ach deploy/helm/ach --set …`.

---

# Part 1 — G7-alerts: Prometheus alert rules

## Task 1: PrometheusRule Helm template + values toggle (3 ready alerts)

**Files:**
- Create: `deploy/helm/ach/templates/prometheusrule.yaml`
- Create: `examples/prometheus-alertrules.yaml`
- Modify: `deploy/helm/ach/values.yaml` (add `metrics.prometheusRule` block ~line 270, mirroring `metrics.serviceMonitor`)

**Step 1: Add the values block**

In `values.yaml` under `metrics:` (alongside `serviceMonitor`):

```yaml
  prometheusRule:
    enabled: false
    labels: {}
    # Alerts targeting P1 metrics are gated; enable once those metrics ship.
    includeP1Alerts: false
```

**Step 2: Write the failing render assertion**

Add a check (a shell assertion in the plan's verification, since Helm has no unit-test harness here): rendering with `--set metrics.prometheusRule.enabled=true` must emit a `kind: PrometheusRule` with the 3 ready alerts and NOT the 2 P1 alerts (because `includeP1Alerts=false`).

Run: `./scripts/dev.sh helm template ach deploy/helm/ach --set metrics.prometheusRule.enabled=true | grep -c 'kind: PrometheusRule'`
Expected: `0` (template doesn't exist yet) — this is the failing state.

**Step 3: Create the template**

`deploy/helm/ach/templates/prometheusrule.yaml`:

```yaml
{{- if .Values.metrics.prometheusRule.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: {{ include "ach.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "ach.commonLabels" . | nindent 4 }}
    {{- with .Values.metrics.prometheusRule.labels }}{{ toYaml . | nindent 4 }}{{- end }}
spec:
  groups:
    - name: ach.rules
      rules:
        - alert: ACHLiteLLMUnreachable
          expr: increase(litellm_unreachable_total[5m]) > 0
          for: 2m
          labels: {severity: critical}
          annotations:
            summary: "LiteLLM unreachable from ACH component {{`{{ $labels.caller }}`}}"
            description: "litellm_unreachable_total{caller=\"{{`{{ $labels.caller }}`}}\"} is incrementing."
        - alert: ACHContentStaleCacheExpired
          expr: rate(content_service_requests_total{outcome="stale_cache_expired"}[5m]) > 0
          for: 5m
          labels: {severity: warning}
          annotations:
            summary: "ACH content cache stale-expired for kind {{`{{ $labels.kind }}`}}"
            description: "Content service serving stale_cache_expired — upstream fetch not refreshing."
        - alert: ACHPersonalKeyOnRuntimeRoute
          expr: rate(forwarder_requests_total{key_type="pk", outcome="forwarded"}[5m]) > 0
          for: 0m
          labels: {severity: info}
          annotations:
            summary: "pk_ used on runtime route {{`{{ $labels.route }}`}}"
            description: "pk_ is not Environment-scoped; use ek_ for Environment-bound workloads/CI/agents (advisory; pk_ runtime is permanently allowed)."
        {{- if .Values.metrics.prometheusRule.includeP1Alerts }}
        - alert: ACHExternalRefRefreshFailed
          expr: rate(operator_external_ref_refresh_total{result=~"error|UpstreamInvalid|Unauthorized|NotFound"}[10m]) > 0
          for: 10m
          labels: {severity: warning}
          annotations:
            summary: "ACH operator failing to refresh {{`{{ $labels.kind }}`}} from {{`{{ $labels.type }}`}}"
        - alert: ACHEnvironmentUnavailable
          expr: environment_available == 0
          for: 5m
          labels: {severity: critical}
          annotations:
            summary: "ACH Environment {{`{{ $labels.name }}`}} is not available"
            description: "environment_available == 0 for 5m — hydrate will fail. Check: kubectl describe environment {{`{{ $labels.name }}`}}."
        {{- end }}
{{- end }}
```

(Use the existing `ach.fullname`/`ach.commonLabels` helpers — confirm their names in `_helpers.tpl`; `ach.commonLabels` exists. If `ach.fullname` is absent, use the literal `ach` name as the ServiceMonitor template does.)

**Step 4: Verify rendering**

Run: `./scripts/dev.sh helm template ach deploy/helm/ach --set metrics.prometheusRule.enabled=true | grep -c 'kind: PrometheusRule'`
Expected: `1`. Then with `--set …includeP1Alerts=true` confirm `ACHEnvironmentUnavailable` appears; without it, it does not.

**Step 5: Create the standalone example**

`examples/prometheus-alertrules.yaml`: the same `PrometheusRule` (all 5 rules, with a comment that the last 2 require the P1 metrics). Add a row in `examples/README.md`.

**Step 6: Commit**

```bash
git add deploy/helm/ach/templates/prometheusrule.yaml deploy/helm/ach/values.yaml examples/prometheus-alertrules.yaml examples/README.md
git commit -m "feat(helm): add PrometheusRule with ACH alert rules (G7-alerts)"
```

**Step 7 (blocked-on-P1): flip the default once metrics land**

After P1 code-plan tasks 5-7 ship `operator_external_ref_refresh_total` + `environment_available`, change `includeP1Alerts` default to `true` and update the carried-forward note in `docs/external-review-resolutions.md`.

---

# Part 2 — G16: content-service HA via RWX split

## Task 2: values + PVC parameterization

**Files:**
- Modify: `deploy/helm/ach/values.yaml` (`contentService` block ~line 175; `operator` block ~line 63)
- Modify: `deploy/helm/ach/templates/operator-deployment.yaml:22` (PVC accessModes + storageClassName)

**Step 1: Add the values keys**

`contentService`:
```yaml
contentService:
  enabled: true
  standalone: false      # G16: when true, run as its own Deployment (needs RWX cache)
  replicas: 1
  resources: {}
  args: ["content-service"]
```
`operator` (new `cache` sub-key):
```yaml
operator:
  ...
  cache:
    size: 10Gi
    accessMode: ReadWriteOnce      # set ReadWriteMany when contentService.standalone=true
    storageClassName: ""           # e.g. efs-sc / nfs-client / cephfs
```

**Step 2: Parameterize the PVC**

In `operator-deployment.yaml` PVC spec (lines 12-25):

```yaml
spec:
  accessModes:
    - {{ .Values.operator.cache.accessMode | default "ReadWriteOnce" }}
  {{- with .Values.operator.cache.storageClassName }}
  storageClassName: {{ . }}
  {{- end }}
  resources:
    requests:
      storage: {{ .Values.operator.cache.size | default "10Gi" }}
```

(Migrate the existing `.Values.operator.cacheSize` reference → `.Values.operator.cache.size`; keep a `default "10Gi"`.)

**Step 3: Verify both render modes**

Run: `./scripts/dev.sh helm template ach deploy/helm/ach | grep -A2 accessModes` → `ReadWriteOnce`.
Run: `… --set operator.cache.accessMode=ReadWriteMany --set operator.cache.storageClassName=efs-sc | grep -A3 accessModes` → `ReadWriteMany` + `storageClassName: efs-sc`.

**Step 4: Commit**

```bash
git add deploy/helm/ach/values.yaml deploy/helm/ach/templates/operator-deployment.yaml
git commit -m "feat(helm): parameterize operator cache PVC accessMode/storageClass (G16)"
```

## Task 3: gate the sidecar on `not standalone`

**Files:**
- Modify: `deploy/helm/ach/templates/operator-deployment.yaml:149` (sidecar guard)

**Step 1: Tighten the sidecar condition**

Change the sidecar guard (line 149) from `{{- if .Values.contentService.enabled }}` to:

```yaml
        {{- if and .Values.contentService.enabled (not .Values.contentService.standalone) }}
```

**Step 2: Verify**

Run: `./scripts/dev.sh helm template ach deploy/helm/ach --set contentService.standalone=true | grep -c 'name: content-service'`
Expected: the sidecar container is ABSENT from the operator Pod (count drops vs default render).

**Step 3: Commit**

```bash
git add deploy/helm/ach/templates/operator-deployment.yaml
git commit -m "feat(helm): drop content-service sidecar when standalone (G16)"
```

## Task 4: standalone content-service Deployment + SA + RBAC + Service selector

**Files:**
- Modify: `deploy/helm/ach/templates/content-service-deployment.yaml` (grow a Deployment + SA; conditional Service selector)
- Modify: `deploy/helm/ach/templates/operator-rbac.yaml` (or new `content-service-rbac.yaml` for the standalone SA)

**Step 1: Add the standalone Deployment**

In `content-service-deployment.yaml`, gate a new block on `{{- if and .Values.contentService.enabled .Values.contentService.standalone }}`: a `ServiceAccount/ach-content-service`, and a `Deployment/ach-content-service` mirroring `platform-api-deployment.yaml` but: `replicas: {{ .Values.contentService.replicas }}`, pod label `app.kubernetes.io/component: content-service`, container port `cs-http: 8082`, the same env block as the current sidecar (`ACH_CACHE_ROOT=/var/cache/ach`, `CONTENT_SERVICE_HEALTH_BIND_ADDRESS=:8082`, `ach.litellmConnectionEnv`, DB/Redis/pepper env), a `cache` volume from PVC `ach-operator-cache` mounted readOnly at `/var/cache/ach`, liveness/readiness on `/healthz:8082`, security context (`runAsNonRoot`, `readOnlyRootFilesystem`, `fsGroup: 65532`, `capabilities.drop:[ALL]`). Add the Prometheus pod-scrape annotation (mirror forwarder/platform-api).

**Step 2: Conditional Service selector**

Make the `ach-content-service` Service `selector.app.kubernetes.io/component` resolve to `content-service` when standalone, else `operator`:

```yaml
  selector:
    app.kubernetes.io/name: ach
    app.kubernetes.io/component: {{ if .Values.contentService.standalone }}content-service{{ else }}operator{{ end }}
```

**Step 3: Standalone RBAC**

When standalone, the new SA needs only the tight RBAC content-service actually uses (it reads Postgres/Redis, NOT the k8s API — per `content_service.go` header). Add a minimal Role/RoleBinding (or no API-server RBAC at all if it truly only needs Postgres). Confirm by reading `content_service.go` what k8s verbs (if any) it issues; default to no CRD list/watch.

**Step 4: Verify both modes render coherently**

Run: `./scripts/dev.sh helm template ach deploy/helm/ach --set contentService.standalone=true | grep -E 'kind: Deployment|component: content-service'`
Expected: a `Deployment` named `ach-content-service` with `component: content-service`, and the Service selector points to it.
Run default render: the Service still selects `operator` and no standalone Deployment renders.

**Step 5: Commit**

```bash
git add deploy/helm/ach/templates/content-service-deployment.yaml deploy/helm/ach/templates/*rbac.yaml
git commit -m "feat(helm): standalone content-service Deployment for RWX split (G16)"
```

## Task 5: e2e + docs for the split

**Files:**
- Modify: `references/troubleshooting.md` or `references/repo-layout.md` (document the HA limitation + the standalone toggle)
- Modify: `deploy/helm/ach/values.yaml` comments

**Step 1: e2e smoke (optional, RWX needed)**

The kind e2e cluster's default storage class is RWO; a true standalone run needs an RWX provisioner (NFS subdir provisioner). If feasible, add an e2e variant `contentService.standalone=true` with an RWX class and assert `make wait-content-service` + a `content fetch` succeed. If not feasible in kind, document that standalone is validated by `helm template` + manual RWX cluster testing only.

Run: `make e2e-focus RUN='TestContent'` (sidecar mode regression — confirm default path still green).

**Step 2: Document the v1alpha1 limitation + toggle**

Add a note: default = single-replica content path co-located with the operator (content availability tied to the operator Pod); set `contentService.standalone=true` + an RWX `operator.cache.storageClassName` to split content-service into its own N-replica Deployment.

**Step 3: Commit**

```bash
git add references/ deploy/helm/ach/values.yaml
git commit -m "docs(g16): document content-service HA split + RWX toggle (G16)"
```

---

# Part 3 — G2-build: UI Objects API + GitOps export + promotion

> This is a multi-week epic with four phases. Each phase is independently committable. Phase A (schema/db) is the foundation; do not start B/C/D until A is green. **Confirm the `spec_json` design default (above) before Phase A.**

## Phase G2-A — schema + DB helpers

## Task 6: add `spec_json` + UI-write DB helpers

**Files:**
- Create: `db/migrations/0000NN_ui_objects_spec_json.up.sql` / `.down.sql` (next migration number)
- Create: `internal/db/ui_objects.go`
- Test: `internal/db/ui_objects_test.go` (`//go:build integration`)

**Step 1: Write the failing integration test**

In `ui_objects_test.go`, `TestInsertUIObject_RoundTrip`: insert a `ui` environment via `InsertUIObject(ctx, pool, "environment", ns, name, specJSON)`; read it back; assert `origin='ui'`, `locked=false`, `spec_json` equals the canonical input. Add `TestPromoteIfMatch_MatchFlipsOrigin` and `TestPromoteIfMatch_MismatchReturnsErr`.

**Step 2: Run to verify it fails**

Run: `make test-integration` (or `go test -tags=integration ./internal/db/...`)
Expected: FAIL — migration + helpers absent.

**Step 3: Migration**

Add `spec_json JSONB NULL` to each UI-writable object table (`environments, plugins, prompts, artifacts, backend_identity_policies, skills, …` — match the set that already carries `origin`/`locked`). Down-migration drops the column.

**Step 4: DB helpers**

`internal/db/ui_objects.go`: `InsertUIObject` / `UpdateUIObject` / `DeleteUIObject` (drain) writing `origin='ui', locked=false, spec_json=$canonical` (+ the projected columns derived from the spec, OR a NULL projection that a projector backfills — pick the projection approach to match how reads work). `PromoteIfMatch(ctx, tx, kind, ns, name, canonicalSpec) (bool, error)`: `UPDATE <table> SET origin='cr', locked=TRUE WHERE origin='ui' AND namespace=$ AND name=$ AND spec_json=$canonical RETURNING name` → `true` on a row, `false` (no match → caller emits `PromotionMismatch`) on `ErrNoRows`. Add a `canonicalizeSpec([]byte) []byte` helper (sorted-key JSON via `encoding/json` round-trip or `sigs.k8s.io/yaml`→JSON) used by BOTH write and promotion so comparisons are stable.

**Step 5: Run to verify it passes**

Run: `make test-integration`
Expected: PASS

**Step 6: Commit**

```bash
git add db/migrations/ internal/db/ui_objects.go internal/db/ui_objects_test.go
git commit -m "feat(db): spec_json column + UI-object write/promote helpers (G2)"
```

## Phase G2-B — UI Objects API routes

## Task 7: `/platform/objects/<kind>` CRUD + `ui_writes_disabled` flag

**Files:**
- Create: `internal/platformapi/objects/{mount,handler}.go`
- Modify: `internal/platformapi/server.go:137` (mount inside the authed group, with an AdminOnly-style gate)
- Test: `internal/platformapi/objects/handler_test.go` (httptest + fake store)

**Context:** mirror the admin inventory handler pattern (`admin/inventory/handler.go` generic `listHandler` + `paginate`, JSON envelope `{items, next_cursor}`). Frozen §15.7 endpoint table: `GET /<kind>`, `GET /<kind>/<name>`, `POST /<kind>`, `PATCH /<kind>/<name>` (JSON Merge), `DELETE /<kind>/<name>`, `GET /<kind>/<name>/yaml`. Error codes: `403 immutable_via_ui` (PATCH/DELETE on a `cr` row), `409 conflict_with_kubernetes_object` (POST over an existing `cr` row), `403 ui_writes_disabled` (env `ACH_DISABLE_UI_WRITES=true`).

**Step 1: Write failing handler tests**

`TestPostObject_CreatesUIRow`, `TestPatchObject_ImmutableViaUI_403` (target is `cr`), `TestPostObject_ConflictWithKubernetes_409`, `TestUIWritesDisabled_403`, `TestGetObject_ReturnsSpecStatus`. Use a fake store implementing the objects interface; no Postgres.

**Step 2: Run to verify they fail**

Run: `make test-unit-pkg PKG=./internal/platformapi/objects/...`
Expected: FAIL — package absent.

**Step 3: Implement the routes**

`objects/mount.go` `Mount(deps)` registering the 6 routes; `handler.go` wiring to the Task-6 DB helpers. Respect `ACH_DISABLE_UI_WRITES`. Validate `<kind>` against the allowed UI-writable set. POST/PATCH validate the body against the kind's spec (reuse `api/v1alpha1` types for unmarshal + CEL-equivalent checks where cheap). Mount in `server.go` inside the authed `r.Group` (line 137), behind an admin-or-writer gate.

**Step 4: Run to verify they pass**

Run: `make test-unit-pkg PKG=./internal/platformapi/objects/...`
Expected: PASS

**Step 5: Lint + commit**

```bash
make qa-lint-changed
git add internal/platformapi/objects/ internal/platformapi/server.go
git commit -m "feat(platform-api): UI Objects API CRUD routes (G2)"
```

## Phase G2-C — GitOps YAML export

## Task 8: `GET /platform/objects/<kind>/<name>/yaml`

**Files:**
- Create: `internal/platformapi/objects/export.go`
- Test: `internal/platformapi/objects/export_test.go`

**Context:** frozen §15.8 — canonical K8s manifest with ONLY `apiVersion`, `kind`, `metadata.name`, `metadata.namespace`, `spec`; NO `status`/`uid`/`resourceVersion`/`creationTimestamp`/`generation`/`managedFields`/ACH annotations; deterministic byte-identical output (load-bearing for promotion). `Content-Type: application/yaml`.

**Step 1: Write the failing test**

`TestExportYAML_Canonical`: export a known environment row; assert the body equals a golden YAML (apiVersion/kind/metadata{name,namespace}/spec only), and that two exports are byte-identical. Assert `status` and `uid` are absent.

**Step 2: Run to verify it fails**

Run: `make test-unit-pkg PKG=./internal/platformapi/objects/...`
Expected: FAIL.

**Step 3: Implement**

`export.go`: reconstruct a minimal struct `{TypeMeta{APIVersion:"ach.ackstorm.ai/v1alpha1", Kind:<Kind>}, ObjectMeta{Name, Namespace}, Spec:<typed spec>}` from the projection columns (or the `spec_json`), marshal with `sigs.k8s.io/yaml.Marshal` (deterministic). Do NOT marshal the full k8s object (it carries status/managedFields). Set `Content-Type: application/yaml`.

**Step 4: Run to verify it passes**

Run: `make test-unit-pkg PKG=./internal/platformapi/objects/...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/platformapi/objects/export.go internal/platformapi/objects/export_test.go
git commit -m "feat(platform-api): canonical GitOps YAML export (G2)"
```

## Phase G2-D — promotion reconcile

## Task 9: promote-on-match + `PromotionMismatch` condition

**Files:**
- Modify: each writer controller (`environment_controller.go`, `external_ref_driver.go` (plugin/prompt/artifact/skill), `backendidentitypolicy_controller.go`, `litellmconnection_controller.go`) — call `PromoteIfMatch` before the block path
- Modify: `internal/controller/ach/conditions.go` (add `ReasonPromotionMismatch`)
- Test: `internal/controller/ach/*_test.go` (envtest) + the db integration tests from Task 6

**Context:** today, when the operator UPSERT hits a `ui` row it returns `ErrOriginConflict` → `ConflictWithUIRow` (block). New behavior: first attempt `PromoteIfMatch` (canonical spec equality). Match → row flips `origin='ui'`→`'cr'`, `locked=TRUE`, primary key `id` preserved, normal projection continues (`Synced=True`). Mismatch → `Synced=False, reason=PromotionMismatch` + a k8s Event with the diff summary; the UI row keeps serving (do NOT overwrite).

**Step 1: Add the reason constant**

In `conditions.go` (near `ReasonConflictWithUIRow`):

```go
// ReasonPromotionMismatch: a CR was applied matching a ui row by (name,ns) but
// its canonical spec differs from the row's spec_json; the operator refuses to
// sync and the ui row keeps serving (frozen §15.8 step 5; G2).
const ReasonPromotionMismatch = "PromotionMismatch"
```

**Step 2: Write the failing envtest**

`TestPromotion_SpecMatchFlipsOrigin`: seed a `ui` row (via the test DB) with spec S; apply a CR with the same canonical spec S; reconcile; assert the row is now `origin='cr', locked=true`, same `id`, and the CR has `Synced=True`. `TestPromotion_SpecMismatch_PromotionMismatch`: seed `ui` row spec S; apply CR with spec S′≠S; assert row stays `origin='ui'` and CR has `Synced=False/PromotionMismatch`. (These need a DB-backed envtest — the current envtest runs `DB: nil`; add a Postgres-backed controller test variant or assert at the `PromoteIfMatch` integration level + a unit test of the controller's branch with a fake store.)

**Step 3: Run to verify it fails**

Run: `make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestPromotion`
Expected: FAIL.

**Step 4: Wire promotion into each controller's UPSERT path**

Where each controller currently maps `ErrOriginConflict`→`ConflictWithUIRow`: first call `PromoteIfMatch(ctx, tx, kind, ns, name, canonicalSpec(cr.Spec))`. On `true`, proceed with the normal `origin='cr'` projection (now unblocked). On `false`, set `Synced=False/PromotionMismatch` + `r.Recorder.Event(cr, Warning, "PromotionMismatch", <diff summary>)` and requeue. Keep `ConflictWithUIRow` ONLY for the genuine non-promotable case (e.g. a `ui` row that the CR is not trying to promote — but with promotion wired, the live path becomes match/mismatch; `ConflictWithUIRow` remains the fallback). Reuse the same `canonicalizeSpec` from Task 6 so spec equality is symmetric.

**Step 5: Run to verify it passes**

Run: `make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestPromotion` then `make test-integration`
Expected: PASS

**Step 6: Lint + commit**

```bash
make qa-lint-changed
git add internal/controller/ach/ internal/db/
git commit -m "feat(operator): promotion reconcile (ui->cr) + PromotionMismatch (G2)"
```

## Task 10: docs + spec flip for G2

**Files:**
- Modify: `references/troubleshooting.md` (the dormant `ConflictWithUIRow` entry from the P1 docs plan → now reachable via the UI path; add `PromotionMismatch`)
- Modify: `CLAUDE.md` (architecture — note the UI write path is now live)
- Modify: `internal/controller/ach/conditions.go` (remove the "dormant/reserved" comment added in the P1 docs plan)

**Step 1: Update the docs**

Reverse the P1-docs "dormant/reserved" framing for G2 now that the UI path ships: document `POST/PATCH/DELETE /platform/objects/<kind>`, `GET …/yaml`, the promotion flow, and `PromotionMismatch`. Add a troubleshooting entry for `Synced=False/PromotionMismatch` (resolution: align the CR spec to the UI row, or delete the UI row).

**Step 2: Commit**

```bash
make qa-lint-changed
git add references/ CLAUDE.md internal/controller/ach/conditions.go
git commit -m "docs(g2): document live UI Objects API + promotion (G2)"
```

**Spec note (external `/home/jcm/Projects/ach-spec/`):** flip `ach-spec-final-delivered.md` §15.7/§15.8 + the `PromotionMismatch` condition row from NOT IMPLEMENTED → delivered. Reconcile the `origin='kubernetes'` vs delivered `'cr'` naming and the `spec_json` reconstruction note.

---

## Final gate

```bash
make test-unit
make test-integration         # G2 db helpers
make test-envtest             # G2 promotion, G16 unaffected
make qa-lint
./scripts/dev.sh helm template ach deploy/helm/ach --set metrics.prometheusRule.enabled=true --set contentService.standalone=true --set operator.cache.accessMode=ReadWriteMany   # G7 + G16 render
make e2e-full                 # cross-service — local-only gate (G2 routes, G16 sidecar regression)
```

Never push a change touching `internal/controller|platformapi|forwarder|contentservice/`, `api/v1alpha1/`, `deploy/helm/ach/`, or `test/e2e/` without confirming E2E green. Pre-push 18-gate hook must pass.
