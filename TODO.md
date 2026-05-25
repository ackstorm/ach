# ACH — Post-bootstrap follow-ups

Tracker for non-blocking work after bootstrap release `v0.1.0`. Each item is self-contained, agent-actionable.

---

## 1. Hardening batch (small)

**Scope**: fix the 3 known smoke-verification failures from Phase 16.

**Files**:
- `scripts/cluster.sh` — postgres image tag pin
- `docs/Makefile` — nested-docker mount path translation
- `.golangci.yml` and/or scaffold code — outstanding deprecations

**Acceptance criteria**:
- `./scripts/dev.sh make cluster-up && make cluster-status && make cluster-down` end-to-end PASS with hydrated postgres/valkey/dex/litellm/toolhive
- `./scripts/dev.sh make docs-build` PASS (or document host-only path explicitly + remove from `./scripts/dev.sh` smoke list)
- `./scripts/dev.sh make lint` exit 0 with zero exclusions added beyond the current SA1019 one
- `make pre-push` GREEN with zero warnings (or 1, max — re-evaluate gate 11 self-match exclusions)

**Specific fixes**:

### 1a. postgres bitnami tag pruned upstream
- **Symptom**: kind cluster-up fails because `docker.io/bitnami/postgresql:16.4.0-debian-12-r14` is `NotFound` (Bitnami pruned)
- **Options**: (a) bump to latest published Bitnami tag, (b) mirror to `ghcr.io/ackstorm/mirror/postgresql`, (c) switch to `cloudnative-pg` or upstream postgres image
- **Recommend**: option (b) — Bitnami's image retention policy will keep pruning; mirror once + control rotation
- **Audit also**: valkey, redis, any other Bitnami pins in `scripts/cluster.sh` + `deploy/helm/ach/values.yaml`

### 1b. docs-build nested-docker mount
- **Symptom**: `./scripts/dev.sh make docs-build` invokes a docker container that mounts `$(abspath docs/..)` which resolves to `/workspace` inside devtools, then the mount uses host docker socket → host has no `/workspace`
- **Options**: (a) translate mount path back to host via env var `HOST_PWD`, (b) refuse to run `docs-build` inside devtools and require host invocation, (c) use `docs-build-local` (python3) as devtools fallback
- **Recommend**: option (a) — pass `HOST_PWD=$(pwd)` from `dev.sh` into devtools env; docs-build uses `$(HOST_PWD)` for nested mount

### 1c. lint deprecations
- **Already added**: `SA1019` exclusion for `scheme.Builder` in `api/*`
- **Audit other warnings**: run `./scripts/dev.sh make lint` (full output) and triage anything else introduced by go 1.26 / k8s 0.36 / controller-runtime 0.24.1 churn

---

## 2. Domain port from ach-old

**Scope**: lift business logic from `/home/jcm/Projects/ach-old/` into bootstrap shell.

**Files (read-only sources)**:
- `/home/jcm/Projects/ach-old/api/ach/v1alpha1/*_types.go` — real Spec/Status fields for 6 CRDs
- `/home/jcm/Projects/ach-old/internal/controller/*_controller.go` — reconciler logic
- `/home/jcm/Projects/ach-old/internal/{audit,cachefs,config,connection,credhash,db,keys,keystore,litellm,orphan,platformapi,snapshot,sources}/` — domain packages
- `/home/jcm/Projects/ach-old/cmd/{operator,platform-api,forwarder,content-service,ach,migrate}/main.go` — entrypoint wiring
- `/home/jcm/Projects/ach-old/db/migrations/` — golang-migrate SQL files

**Targets in /home/jcm/Projects/ach/**:
- `api/ach/v1alpha1/*_types.go` — replace placeholder `Foo string` fields with real Spec/Status
- `internal/controller/ach/*_controller.go` — real Reconcile logic
- `internal/{audit,cachefs,config,connection,credhash,db,keys,keystore,litellm,orphan,platformapi,snapshot,sources}/` — port packages
- `cmd/ach/cmd/{operator,platform_api,forwarder,content_service,migrate}.go` — wire real subcommands (replace stub Println with manager.Start / chi.Serve / etc.)
- `db/migrations/` — SQL files

**Order**:
1. CRD types (smallest unit, unblocks DeepCopy regen)
2. `internal/db/` (Postgres models + queries) — needed by everything else
3. `internal/keystore/` + `internal/keys/` + `internal/credhash/` — auth foundations
4. `internal/connection/` + `internal/litellm/` — LiteLLM client
5. `internal/controller/ach/` reconcilers
6. `cmd/ach/cmd/operator.go` — wire manager.Start
7. `internal/platformapi/` + `cmd/ach/cmd/platform_api.go` — REST surface
8. `internal/sources/` + `internal/cachefs/` + `cmd/ach/cmd/content_service.go` — artifact streaming
9. `cmd/ach/cmd/forwarder.go` — MCP/A2A forwarding
10. `cmd/ach/cmd/migrate.go` + `db/migrations/` — DB schema

**Acceptance**: `./scripts/dev.sh make envtest-fast` + `./scripts/dev.sh make e2e-full` green with real reconcilers. UAT-Phase3-style scenario from `ach-old/scripts/uat-phase3.sh` passes.

**Caveats**:
- ach-old uses Ginkgo for some tests; we have Ginkgo on envtest+e2e — should slot in cleanly
- ach-old has `internal/toolhive/` references that conflicted with envtest-fast (removed in T9.1) — restore if porting toolhive integration
- Single-binary cobra layout: ach-old's `cmd/operator/main.go` content goes into `cmd/ach/cmd/operator.go` `RunE` (not a separate main)

---

## 3. Multi-component Helm templates

**Scope**: current `deploy/helm/ach/templates/install.yaml` renders a single Deployment (operator-only baseline inherited from alitellm). `values.yaml` already exposes 5 per-mode toggles (operator, platformApi, forwarder, contentService, migrate). Templates need to consume them.

**Files**:
- `deploy/helm/ach/templates/operator-deployment.yaml` (new)
- `deploy/helm/ach/templates/platform-api-deployment.yaml` (new)
- `deploy/helm/ach/templates/forwarder-deployment.yaml` (new)
- `deploy/helm/ach/templates/content-service-deployment.yaml` (new)
- `deploy/helm/ach/templates/migrate-job.yaml` (new — Job, not Deployment)
- `deploy/helm/ach/templates/_helpers.tpl` (extend with per-mode labels/serviceAccount selectors)
- `deploy/helm/ach/templates/{service,rbac,sa}-*.yaml` per-mode resources
- Remove or refactor: `deploy/helm/ach/templates/install.yaml` (monolith from alitellm)

**Pattern**: each Deployment template references `{{ .Values.image.repo }}:{{ .Values.image.tag }}` (single image) with `args: {{ .Values.<mode>.args | toYaml }}` (cobra subcommand). Gated by `{{- if .Values.<mode>.enabled }}`.

**Sync source-of-truth**: `make helm-sync` regenerates from kustomize. Either (a) author Helm templates directly and skip kustomize-to-helm, or (b) extend `config/deployments/` kustomize bases for all 5 modes and let `kustomize-to-helm.sh` regenerate.

**Recommend**: (a) — single-binary makes per-mode templates trivial; keep kustomize for k8s-native users; Helm chart authored independently.

**Acceptance**:
- `helm template deploy/helm/ach --set operator.enabled=true --set platformApi.enabled=true` renders 2 Deployments
- `helm template deploy/helm/ach --set migrate.enabled=true` renders 1 Job + the default Deployments
- `helm lint deploy/helm/ach` PASS
- Install into kind cluster end-to-end via `helm install ach deploy/helm/ach --namespace ach-system --create-namespace`

---

## 4. Sync-back PR to alitellm-operator ✅ STARTED

**Scope**: open a PR against `/home/jcm/Projects/alitellm-operator/` containing the real-bug fixes + hardening improvements ACH developed during bootstrap.

**Briefing files staged at**: `/home/jcm/Projects/alitellm-operator/SYNC-FROM-ACH/`

See `/home/jcm/Projects/alitellm-operator/SYNC-FROM-ACH/README.md` for the agent prompt + per-fix diffs.

**Acceptance**:
- Branch on alitellm-operator: `sync-from-ach-2026-05-25`
- Single commit per fix (or one bundled commit per category)
- PR description references ACH commit SHAs
- All fixes pass alitellm's own `make pre-push` + CI

---

## Cross-cutting tech debt (deferred)

- **Goreleaser `dockers_v2` migration** — current configs emit deprecation warnings; future maintenance task
- **kustomize `namePrefix: ach-` collision** with already-prefixed source manifests (would produce `ach-ach-platform-api`) — drop or rename sources; cross-cuts rbac + manager
- **`config/manager/manager.yaml`** still single-container kubebuilder scaffold; ach-old has 2-container Pod (operator + content-service + shared RWO PVC). Port when content-service domain is wired
- **e2e Patch 1 silent no-op** in `config/e2e/kustomization.yaml` (targets nonexistent `ach-operator` Deployment) — fix when manager.yaml is ported
- **Bitnami image-retention exposure** broader audit — any other pinned bitnami images in scripts/values
- **Bootstrap tag** never created — Phase 16 reported 3 smoke failures so `bootstrap-complete` was not tagged. Once item 1 closes, tag retroactively at the post-fix commit
- **Pre-push gate 11 self-match warnings** — references/upstream-sync.md descriptive text contains `DO-NOT-COMMIT`/`DO NOT COMMIT` literals; gate exclusion needs widening or doc-text rewording
