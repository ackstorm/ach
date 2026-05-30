---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 08
subsystem: deployment-manifests
tags: [kustomize, operator-pod, rwo-pvc, recreate-strategy, init-container, pepper-secret, namespace-scoped, stub-deployments]

# Dependency graph
requires:
  - phase: 01-02
    provides: "CRD bases — config/crd/kustomization.yaml aggregated under config/default/"
  - phase: 01-03
    provides: "Postgres DDL — migrate init container will apply db/migrations against ACH_DB_URL"
  - phase: 01-05
    provides: "Six reconcilers — registered in cmd/operator/main.go run inside the manager container"
  - phase: 01-06
    provides: "Five binaries (cmd/{operator,platform-api,forwarder,content-service,ach}); per-binary env-var inventory table that this plan's env: blocks consume verbatim"
  - phase: 01-07
    provides: "internal/cachefs.EnsureLayout — called by cmd/operator/main.go against /var/cache/ach which this plan provisions via PVC"
  - phase: 01-09
    provides: "Four ServiceAccounts (ach-operator, ach-platform-api, ach-forwarder, ach-content-service) referenced by serviceAccountName in this plan's Deployments"
provides:
  - "config/manager/manager.yaml: Deployment ach-operator with strategy.type=Recreate, 2 containers (manager + content-service), 1 init container (migrations), 1 RWO PVC mount at /var/cache/ach on both app containers, ACH_CREDENTIAL_HASH_PEPPER via secretKeyRef on both containers (zero inline value:), full PodSecurityStandards/restricted compliance"
  - "config/storage/operator_cache_pvc.yaml: ach-operator-cache PVC, accessModes [ReadWriteOnce], 10Gi default, default storageClass"
  - "config/secrets/credential_hash_pepper_secret.yaml: ach-credential-hash-pepper, key `pepper`, literal placeholder 'REPLACE-ME-WITH-RANDOM-32-BYTES-FROM-OPENSSL-RAND-BASE64-32' that Plan 06's cmd/operator/main.go rejects at startup (Plan 11 B2 contract)"
  - "config/secrets/db_url_secret.yaml: ach-db-url, key `url`, dev placeholder for migrate init + manager + platform-api containers"
  - "config/deployments/platform-api_deployment.yaml + forwarder_deployment.yaml: two Phase 1 Hub-stub Deployments with replicas=1, own SAs, full PodSecurityStandards/restricted"
  - "config/default/kustomization.yaml: top-level aggregator with namespace=ach-system; namePrefix dropped to avoid ach-ach-* doubles"
  - "config/default/namespace.yaml: ach-system Namespace (MULTI-01)"
  - "config/manager/kustomization.yaml: untouched (Plan 01 default — single resources line)"
affects:
  - "01-10 (Dockerfiles): MUST ship the migrate companion binary at /migrate inside ach-operator:latest. cmd/migrate/main.go is the explicit follow-up — wraps internal/db.Migrate per D-07. Dockerfile.operator should produce two binaries (operator + migrate) and COPY db/migrations to /db/migrations."
  - "01-11 (verifier): The e2e assertions for Phase 1 SC #2 read this manager.yaml verbatim. The B2 pepper-placeholder e2e gate asserts that applying the literal placeholder Secret to a cluster leaves the operator container in a CrashLoopBackOff with stderr 'credential-hash pepper still carries the placeholder value' (Plan 06 main asserts this; Plan 08 supplies the placeholder text)."
  - "02+ (LiteLLM REST client): Phase 2's plan that swaps NoopClient for the real client touches cmd/operator/main.go only — no manifest changes (env block remains the same)."
  - "07 (Helm chart): Phase 7's chart packages every YAML in this plan verbatim, replacing the placeholder Secret stringData with --set values."

# Tech tracking
tech-stack:
  added: []  # zero new go.mod entries — this plan ships YAML only
  patterns:
    - "Operator + Content Service Pod co-location (Hub §5.1 Pod boundary): two app containers + one init container + one RWO PVC + strategy.type=Recreate inside a single Deployment. Sister project ach_litellm ships a single-container Deployment; ACH's two-container shape is documented inline with YAML comments referencing OP-05 / SC #2 so future readers can trace the why."
    - "Pepper Secret single-source-of-truth across all four Hub components (D-15): manager + content-service + platform-api + forwarder all reference Secret/ach-credential-hash-pepper via valueFrom.secretKeyRef even when the Phase 1 binary doesn't read the value. Helm chart in Phase 7 inherits this manifest set unchanged. Inline YAML comment 'Phase 5+: real Content Service hashes ek_ credentials' adjacent to the content-service env entry signals the intent so future readers don't delete it."
    - "Pepper placeholder discipline: Secret manifest stringData contains the literal text 'REPLACE-ME-WITH-RANDOM-...' that cmd/operator/main.go matches via strings.HasPrefix at startup. Annotation ach.ackstorm.ai/placeholder=true on both Secret manifests, plus ach.ackstorm.ai/replace-before-prod with operator-readable replacement instructions. Pairs the runtime gate (Plan 06) with the manifest text (this plan) under one convention."
    - "Migrate init container per D-07: same Operator image, /migrate entrypoint, args -path /db/migrations -database $(ACH_DB_URL) up. D-08 fail-fast — non-zero exit → Init:Error → app containers never start. Plan 10's Dockerfile.operator owns producing the /migrate binary alongside /operator."
    - "namePrefix dropped from config/default/kustomization.yaml: Plan 09 SAs + Plan 08 Secrets/PVC/Deployments already encode `ach-` in their resource names. A second prefix would produce ach-ach-* doubles. Kustomize namespace directive alone (without namePrefix) correctly renames system → ach-system on the Namespace resource itself and on every namespaced reference."
    - "PodSecurityStandards/restricted across every container in every Deployment: runAsNonRoot=true, allowPrivilegeEscalation=false, capabilities.drop=[ALL], readOnlyRootFilesystem=true, Pod-level seccompProfile.type=RuntimeDefault. Matches sister project plus the additional readOnlyRootFilesystem hardening; cmd/operator/main.go's slog writes to stdout so a read-only root is compatible."

key-files:
  created:
    - config/storage/operator_cache_pvc.yaml (RWO PVC, 10Gi, default storageClass; 30 lines)
    - config/storage/kustomization.yaml (single resource list; 5 lines)
    - config/secrets/credential_hash_pepper_secret.yaml (Opaque, key pepper, literal placeholder; 33 lines)
    - config/secrets/db_url_secret.yaml (Opaque, key url, dev placeholder; 28 lines)
    - config/secrets/kustomization.yaml (two-resource list; 6 lines)
    - config/deployments/platform-api_deployment.yaml (stub Deployment, SA ach-platform-api, :8081; 91 lines)
    - config/deployments/forwarder_deployment.yaml (stub Deployment, SA ach-forwarder, :8081; 84 lines)
    - config/deployments/kustomization.yaml (two-resource list + comment about co-located content-service; 9 lines)
    - config/default/namespace.yaml (Namespace `system` renamed to `ach-system` by kustomize; 14 lines)
  modified:
    - config/manager/manager.yaml (kubebuilder-generated single-container → Phase 1 two-container Pod with init container + PVC + Secret env refs; +209/-75 lines)
    - config/default/kustomization.yaml (kubebuilder default with cert-manager / metrics scaffolding → Phase 1 aggregator listing 7 resource directories; +29/-209 lines)

key-decisions:
  - "namePrefix dropped from config/default/kustomization.yaml. The plan's example showed `namePrefix: ach-` but Plan 09's SAs (`ach-operator`, `ach-platform-api`, etc.) and Plan 08's PVC/Secrets/Deployments already encode `ach-` in their resource names. Stacking the prefix produced ach-ach-* doubles; dropping it preserves the names consistently. The namespace directive alone (`namespace: ach-system`) is enough to scope everything correctly. Rule 3 inline fix per <deviation_rules> — keeps `kustomize build` output coherent without surfacing as a checkpoint."
  - "Init container `migrations` reads /db/migrations directly out of the operator image — no emptyDir volume. The plan-text hedged here ('drop db-migrations volume; the migrate init container mounts no volume — it reads /db/migrations straight out of its own image'). Picked the no-volume path: simpler manifest, fewer moving parts, matches D-07's 'same Operator image' invariant. Plan 10's Dockerfile.operator owns COPYing db/migrations to /db/migrations."
  - "Content Service container co-located in the Operator Pod (NOT a separate Deployment under config/deployments/). config/deployments/kustomization.yaml has an explicit comment explaining why: Hub §5.1 Pod boundary places Operator + Content Service together so they share the RWO PVC. Adding a separate ach-content-service Deployment would create a second RWO claim on the same PVC, which is the exact deadlock OP-05's Recreate strategy prevents."
  - "The Content Service container in manager.yaml mounts the pepper Secret even though the Phase 1 stub does not read it. D-15 ('every component-that-may-hash references the Secret') means the Phase 7 Helm chart inherits a complete manifest set without needing to add the env var when Phase 5 promotes the Content Service to real hashing surface. Inline YAML comment 'Phase 5+: real Content Service hashes ek_ credentials' is the future-reader signal."
  - "Forwarder Deployment does NOT reference ACH_DB_URL. Forwarder Phase 1 stub doesn't open a DB connection; Phase 4 will add Postgres+Redis access (and the env block can be extended then without touching this Plan 08 manifest). Adding the unused env now would still work but creates a startup dependency on the ach-db-url Secret — better to keep stub Pods self-sufficient so they can come up even before Plan 03's DB is provisioned."
  - "metrics_service.yaml + manager_metrics_patch.yaml + cert_metrics_manager_patch.yaml left in config/default/ as orphan files (no longer referenced in config/default/kustomization.yaml). Logged in .planning/.../deferred-items.md for Phase 5 (OBS-03..06) to either re-wire or remove. Deleting them is out of Plan 01-08 scope."

patterns-established:
  - "Phase 1 manifest layout: config/{crd,rbac,secrets,storage,manager,deployments}/ each with its own kustomization.yaml; config/default/kustomization.yaml aggregates all six (plus the Namespace) into one deployable bundle. Phase 7 Helm chart will rebase off config/default/."
  - "Per-component Deployment shape: replicas=1 Phase 1 baseline (Phase 3/4 can scale); own SA from Plan 09; env block: pepper Secret + DB URL Secret (where applicable) + ACH_NAMESPACE downward API + per-binary BIND_ADDRESS knob; healthz/readyz probes on the binary's bound port; PodSecurityStandards/restricted security context."
  - "Pepper Secret invariant: ALL components reference Secret/ach-credential-hash-pepper via secretKeyRef, never via inline `value:`. Plan 11 e2e grep gate: `grep -A1 'name: ACH_CREDENTIAL_HASH_PEPPER' config/**/*.yaml | grep -cE '^\\s*value:'` returns 0."

requirements-completed: [OP-02, OP-05, OP-10]

# Metrics
duration: ~7min
completed: 2026-05-15
---

# Phase 1 Plan 8: Operator + Content Service Pod, RWO PVC, Stub Deployments Summary

**Phase 1 deployment topology landed: `config/manager/manager.yaml` declares the Operator Pod with two containers (manager + content-service), one init container (migrations), one RWO PVC mounted at `/var/cache/ach`, and `strategy: Recreate` per OP-05. `config/storage/operator_cache_pvc.yaml` provisions `ach-operator-cache` with `accessModes: [ReadWriteOnce]`. `config/secrets/{credential_hash_pepper,db_url}_secret.yaml` ship the development placeholder Secrets; the pepper carries the literal `REPLACE-ME-WITH-RANDOM-...` text that Plan 06's operator main rejects at startup (Plan 11 B2 contract). `config/deployments/{platform-api,forwarder}_deployment.yaml` ship the two Hub-stub Pods. `config/default/kustomization.yaml` aggregates the full set: 3 Deployments + 1 PVC + 1 Namespace + 6 CRDs + 2 Secrets + 4 SAs + 4 Roles + 4 RoleBindings. `./scripts/dev.sh kustomize build config/default/` exits 0. Zero inline `value:` for `ACH_CREDENTIAL_HASH_PEPPER` anywhere in the rendered manifest set. Phase 1 SC #2 (`kubectl describe pod` shows two ready containers + one PVC) is reachable from this manifest set.**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-05-15T14:44:22Z
- **Completed:** 2026-05-15

## Env-Var → Secret Key Mappings (for Plan 10's Dockerfile reference and Plan 11's e2e assertions)

The complete wire-up of every env var across all four Phase 1 Hub components:

### Operator Pod (`config/manager/manager.yaml`)

| Container | Env Var | Source | Secret/Field |
|-----------|---------|--------|--------------|
| init `migrations` | `ACH_DB_URL` | secretKeyRef | `ach-db-url` → key `url` |
| `manager` | `ACH_CREDENTIAL_HASH_PEPPER` | secretKeyRef | `ach-credential-hash-pepper` → key `pepper` |
| `manager` | `ACH_DB_URL` | secretKeyRef | `ach-db-url` → key `url` |
| `manager` | `ACH_NAMESPACE` | fieldRef (downward API) | `metadata.namespace` |
| `manager` | `ACH_CACHE_ROOT` | inline `value:` | `/var/cache/ach` |
| `manager` | `ACH_PLUGIN_MAX_SIZE_MIB` | inline `value:` | `"50"` |
| `content-service` | `ACH_CREDENTIAL_HASH_PEPPER` | secretKeyRef | `ach-credential-hash-pepper` → key `pepper` |
| `content-service` | `ACH_CACHE_ROOT` | inline `value:` | `/var/cache/ach` |
| `content-service` | `ACH_NAMESPACE` | fieldRef | `metadata.namespace` |
| `content-service` | `CONTENT_SERVICE_HEALTH_BIND_ADDRESS` | inline `value:` | `":8082"` |

### Platform API Pod (`config/deployments/platform-api_deployment.yaml`)

| Env Var | Source | Secret/Field |
|---------|--------|--------------|
| `ACH_CREDENTIAL_HASH_PEPPER` | secretKeyRef | `ach-credential-hash-pepper` → key `pepper` |
| `ACH_DB_URL` | secretKeyRef | `ach-db-url` → key `url` |
| `ACH_NAMESPACE` | fieldRef | `metadata.namespace` |
| `PLATFORM_API_HEALTH_BIND_ADDRESS` | inline `value:` | `":8081"` |

### Forwarder Pod (`config/deployments/forwarder_deployment.yaml`)

| Env Var | Source | Secret/Field |
|---------|--------|--------------|
| `ACH_CREDENTIAL_HASH_PEPPER` | secretKeyRef | `ach-credential-hash-pepper` → key `pepper` |
| `ACH_NAMESPACE` | fieldRef | `metadata.namespace` |
| `FORWARDER_HEALTH_BIND_ADDRESS` | inline `value:` | `":8081"` |

**Phase 4 expansion (forwarder):** adds `ACH_DB_URL`, `ACH_REDIS_URL`, and signing-key references. Phase 4's plan touches only the forwarder Deployment; nothing else.

## Follow-up Required from Plan 10 (Dockerfiles)

Plan 10 (Dockerfiles + container build) MUST satisfy this manifest set:

1. **`ach-operator:latest`** image MUST produce two binaries at the image root:
   - `/operator` (the Phase 1 controller-manager from `cmd/operator/main.go`)
   - `/migrate` (a thin wrapper around `internal/db.Migrate(ctx, url, path, "up")` — Plan 10 ships `cmd/migrate/main.go` so this entrypoint exists)
2. **`ach-operator:latest`** image MUST `COPY db/migrations /db/migrations` so the init container's `-path /db/migrations` argument resolves.
3. **`ach-content-service:latest`** image MUST produce `/content-service` at the image root (Plan 06's `cmd/content-service/main.go`).
4. **`ach-platform-api:latest`** image MUST produce `/platform-api` at the image root.
5. **`ach-forwarder:latest`** image MUST produce `/forwarder` at the image root.
6. All five images MUST run as non-root with a read-only root filesystem (the manifests enforce `runAsNonRoot: true, readOnlyRootFilesystem: true`).
7. All five images MUST listen on the documented ports (operator: 8080 metrics + 8081 healthz/readyz; platform-api: 8081 healthz; forwarder: 8081 healthz; content-service: 8082 healthz).

## Task Commits

Each task committed atomically:

1. **Task 1: Operator + Content Service Pod with RWO PVC + migrate init** — `e16ecc2` (feat)
2. **Task 2: RWO PVC for cache layout + ACH namespace manifest** — `ae51498` (feat)
3. **Task 3: development pepper + DB-URL Secret manifests (placeholders)** — `8fc5b46` (feat)
4. **Task 4: Platform API + Forwarder stub Deployments (own Pods)** — `cf76b71` (feat)
5. **Task 5: aggregate full Phase 1 manifest set in config/default/** — `d818f37` (feat)

## Verification Outcomes

- `./scripts/dev.sh kustomize build config/manager/` — exit 0; emits the Operator + Content Service Pod.
- `./scripts/dev.sh kustomize build config/storage/` — exit 0; emits the RWO PVC.
- `./scripts/dev.sh kustomize build config/secrets/` — exit 0; emits both placeholder Secrets.
- `./scripts/dev.sh kustomize build config/deployments/` — exit 0; emits both stub Deployments.
- `./scripts/dev.sh kustomize build config/default/` — exit 0; emits the full Phase 1 deployable bundle.
- Resource counts (rendered top-level output):
  - 3 Deployments (ach-operator, ach-platform-api, ach-forwarder)
  - 1 PVC (ach-operator-cache)
  - 1 Namespace (ach-system)
  - 6 CRDs (Plan 02)
  - 2 Secrets (ach-credential-hash-pepper, ach-db-url)
  - 4 SAs + 4 Roles + 4 RoleBindings (Plan 09)
- `grep -A1 'name: ACH_CREDENTIAL_HASH_PEPPER' <(./scripts/dev.sh kustomize build config/default/) | grep -cE '^\s*value:'` → `0` (Plan 11 e2e contract).
- `grep -A1 'strategy:' config/manager/manager.yaml | grep -c 'type: Recreate'` → `1` (OP-05).
- `grep -c 'mountPath: /var/cache/ach' config/manager/manager.yaml` → `2` (both app containers; init does not mount).
- `grep -q 'claimName: ach-operator-cache' config/manager/manager.yaml` → match.
- `./scripts/dev.sh go build ./...` → exit 0 (unchanged — Plan 08 ships YAML only).
- `./scripts/dev.sh make fmt vet` → exit 0.

## Deviations from Plan

### Auto-fixed issues

**1. [Rule 3 — Blocking] namePrefix would have produced ach-ach-* doubles**
- **Found during:** Task 5 (verification step).
- **Issue:** The plan-text example for `config/default/kustomization.yaml` included `namePrefix: ach-`. Plan 09's SAs (`ach-operator`, `ach-platform-api`, etc.) and Plan 08's PVC/Secrets/Deployments already encode `ach-` in their resource names. Stacking the prefix rendered `ach-ach-operator`, `ach-ach-platform-api`, `ach-ach-operator-cache`, etc., across all 18 generated names — visibly incoherent and would have broken any external reference using the un-prefixed names.
- **Fix:** Dropped `namePrefix:` from `config/default/kustomization.yaml`. The `namespace: ach-system` directive alone correctly renames the Namespace resource itself AND every namespaced reference. Resource names remain `ach-operator`, `ach-operator-cache`, `ach-credential-hash-pepper`, etc. — exactly what Plan 09 / Plan 06 / Plan 11 already reference.
- **Files modified:** config/default/kustomization.yaml.
- **Commit:** `d818f37`.

### Deferred items (out-of-scope, logged for follow-up)

- `config/default/cert_metrics_manager_patch.yaml`, `manager_metrics_patch.yaml`, `metrics_service.yaml` — orphaned kubebuilder scaffolding no longer referenced by the new `config/default/kustomization.yaml`. Phase 5 (OBS-03..06) will either re-wire or remove them. Logged in `.planning/phases/01-foundation-crds-db-schema-operator-skeleton-multi-tenancy/deferred-items.md`.

## Notes for Plan 11 (Verifier)

The Plan 11 e2e assertions touching this plan's manifests:

1. **SC #2 — Pod has two ready containers + one PVC.** Apply `config/default/` via kustomize, wait for the `ach-operator` Pod, run `kubectl describe pod` and assert exactly two `Containers:` blocks marked `Ready: True` plus exactly one `Volumes:` entry of type `PersistentVolumeClaim` with `ClaimName: ach-operator-cache`. The init container `migrations` must appear under `Init Containers:` with `State: Terminated, Reason: Completed`.
2. **SC #4 — Postgres tables present after init container ran + pepper outside DB.** After the init container succeeds, `psql $ACH_DB_URL -c '\dt'` must list the four tables from Plan 03 / Hub §16. `psql $ACH_DB_URL -c 'SELECT 1 FROM information_schema.columns WHERE column_name = ''pepper'''` must return 0 rows (pepper lives in K8s Secret, not in DB).
3. **B2 — operator rejects literal placeholder pepper.** Apply `config/secrets/credential_hash_pepper_secret.yaml` verbatim (placeholder Secret). The `ach-operator` Pod's `manager` container must enter `CrashLoopBackOff` with stderr containing the phrase `"credential-hash pepper still carries the placeholder value"` (per Plan 06 SUMMARY's identity grep gate).
4. **OP-05 — strategy is Recreate.** `kubectl get deployment ach-operator -o jsonpath='{.spec.strategy.type}'` returns `Recreate`. A second `kubectl apply` with a `metadata.labels` patch should NOT trigger a rolling update (the old Pod terminates before the new Pod starts).
5. **No inline `value:` for pepper.** `grep -A1 'name: ACH_CREDENTIAL_HASH_PEPPER' <(./scripts/dev.sh kustomize build config/default/) | grep -cE '^\s*value:'` returns `0` exactly.

## Self-Check: PASSED

Files claimed in this summary:

- `config/manager/manager.yaml` — FOUND
- `config/storage/operator_cache_pvc.yaml` — FOUND
- `config/storage/kustomization.yaml` — FOUND
- `config/secrets/credential_hash_pepper_secret.yaml` — FOUND
- `config/secrets/db_url_secret.yaml` — FOUND
- `config/secrets/kustomization.yaml` — FOUND
- `config/deployments/platform-api_deployment.yaml` — FOUND
- `config/deployments/forwarder_deployment.yaml` — FOUND
- `config/deployments/kustomization.yaml` — FOUND
- `config/default/namespace.yaml` — FOUND
- `config/default/kustomization.yaml` — FOUND (modified)

Commits claimed:

- `e16ecc2` — FOUND
- `ae51498` — FOUND
- `8fc5b46` — FOUND
- `cf76b71` — FOUND
- `d818f37` — FOUND
