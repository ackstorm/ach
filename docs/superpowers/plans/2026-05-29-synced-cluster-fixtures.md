# Synced-Cluster Fixtures + Assert-Only Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `cluster.sh` sync a complete, demo-ready cluster (all ACH objects + Environments) verified healthy before tests run, and refactor the e2e suite so tests only *assert* against that synced state instead of applying their own fixtures.

**Architecture:** Extend the numbered-stage bring-up under `test/e2e/cluster/` with two new declarative stages — `04-objects/` (all non-Environment ACH CRs) and `05-environment/` (the Environments, last, since they reference the objects) — plus a `06-verify` readiness gate that blocks until every CR reaches its healthy condition. Dev secrets move to a kustomize `secretGenerator` in `02-ach/`. Tests stop calling `kubectl apply`; steady-state tests assert against the synced cluster, lifecycle tests (finalizer-drain) operate on throwaway resources. `examples/` becomes curated user-facing docs, distinct from the synced fixture set.

**Tech Stack:** bash (`scripts/cluster.sh`), kustomize (`kubectl apply -k`), kubectl wait, Helm, Go e2e tests (`-tags e2e`), kind.

**Source decision (locked):** Option **B** — synced objects use their **real upstreams** (GitHub). Viable because the default outer transport is `git` (no per-IP REST 60/h limit; see CLAUDE.md "Resolution as of 2026-05-27"). Every fetched CR keeps `refresh.interval: 1h` so steady-state stays well under any quota.

---

## File Structure

**New (created):**
- `test/e2e/cluster/02-ach/secrets/kustomization.yaml` — `secretGenerator` for the 3 static dev secrets.
- `test/e2e/cluster/03-test-backends/ach-mcp-echo.yaml` — the JWT-validating MCP backend, extracted from the Helm chart (Task 2).
- `test/e2e/cluster/03-test-backends/kustomization.yaml` — gateway + mcp-echo (renamed from `03-gateway/`).
- `test/e2e/cluster/04-objects/kustomization.yaml` — lists all non-Environment ACH CRs.
- `test/e2e/cluster/04-objects/*.yaml` — the object CRs (moved from `examples/`, see Task 3).
- `test/e2e/cluster/05-environment/kustomization.yaml` — the Environments.
- `test/e2e/cluster/05-environment/*.yaml` — `demo` + `demo-unresolved` (moved from `examples/`).
- `test/e2e/fixtures/phase4_drain_environment.yaml` — throwaway Environment for the §11f finalizer-drain test.

**Modified:**
- `deploy/helm/ach/values.yaml` — **remove** the entire `testMocks:` block (Task 2).
- `test/e2e/cluster/02-ach/ach.values.yaml` — **remove** the `testMocks:` block (Task 2).
- `scripts/cluster.sh` — extract mcp-echo to our manifest; new stages `reconcile_objects`, `reconcile_environments`, `verify_all`; secrets via kustomize; `reconcile_all` reordered.
- `test/e2e/phase4_promotion_test.go` — §11f uses the throwaway Environment; stop mutating shared `demo`.
- Test files in the assert-only sweep (Task 9) — remove `kubectl apply` of now-synced fixtures.
- `examples/README.md`, `CLAUDE.md`, `test/e2e/mcp-echo/README.md` — document the examples-vs-fixtures split and that mcp-echo is no longer a chart toggle.

**Deleted:**
- `deploy/helm/ach/templates/test-mocks.yaml` — **the whole template** (Task 2). The user-facing chart ships zero test scaffolding. Legacy `ach-mock-litellm`/`ach-mock-mcp` (unused) are dropped, not re-created.
- `config/samples/ach_v1alpha1_litellmconnection.yaml` reference from `cluster.sh` (the duplicate; the synced LiteLLMConnection lives in `04-objects/`).
- `03-gateway/` directory (renamed to `03-test-backends/`).

### Object inventory (what `04-objects/` + `05-environment/` hold)

| Stage | CR (kind / name) | from examples/ | healthy condition (06-verify) |
|-------|------------------|----------------|-------------------------------|
| 04-objects | LiteLLMConnection/default | 01 | `Ready=True` |
| 04-objects | PluginMarketplace/anthropic | 05 | `Synced=True` |
| 04-objects | PluginMarketplace/caveman | 05b | `Synced=True` |
| 04-objects | Plugin/caveman | 06 | `SourceReachable=True` |
| 04-objects | Prompt/claude-code-system-prompt | 07 | `SourceReachable=True` |
| 04-objects | Artifact/openclaw-templates | 08 | `SourceReachable=True` |
| 04-objects | BackendIdentityPolicy/context7 | 09 | `Synced=True` |
| 04-objects | BackendIdentityPolicy/duplicate-demo | 10 | `Synced=True` (or documented duplicate state) |
| 04-objects | BackendIdentityPolicy/bip-demo-mcp-jwt | 11 | `Synced=True` |
| 04-objects | BackendIdentityPolicy/bip-demo-mcp-nojwt | 16 | `Synced=True` |
| 05-environment | Environment/demo | 04 | `Available=True` |
| 05-environment | Environment/demo-unresolved | 04b | `AccessGroupSynced=False/UnresolvedReferences` (intentional negative) |

**MCP JWT/no-JWT validation backend `ach-mcp-echo` moves OUT of the Helm chart (Task 2).** Today it ships in the user-facing chart gated on `testMocks.mcpEcho.enabled` — which is scaffolding that does not belong in a chart users install. Task 2 extracts it into `test/e2e/cluster/03-test-backends/ach-mcp-echo.yaml` (our manifest), removes the whole `testMocks` block from the chart, and has `cluster.sh` build+load+apply it. It still gets registered in LiteLLM as `demo-mcp-jwt`/`demo-mcp-nojwt` by `reconcile_litellm` and driven by `bip-demo-mcp-jwt`/`bip-demo-mcp-nojwt` (04-objects); it verifies JWTs against the forwarder JWKS, so it must come after `02-ach`.

**ToolHive CRs (12-15: MCPRemoteProxy, MCPServer×3)** are stale/legacy `toolhive.stacklok.dev` examples, unrelated to the MCP validation path above. They stay in `examples/` (candidates for deletion as stale) — **no plan task includes them in the synced set.**

---

## PHASE A — Synced cluster pipeline

### Task 1: Dev secrets as a kustomize secretGenerator

**Files:**
- Create: `test/e2e/cluster/02-ach/secrets/kustomization.yaml`
- Modify: `scripts/cluster.sh` (reconcile_ach — replace inline secret creates with `apply -k`)

- [ ] **Step 1: Write the secretGenerator**

Create `test/e2e/cluster/02-ach/secrets/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: ach-system

# Dev-only secrets faked for e2e/kind. Production mounts its own (ExternalSecrets/
# SealedSecrets); the chart references these by name only. disableNameSuffixHash
# keeps the names stable (ach-db-url, not ach-db-url-<hash>) so the chart resolves
# them. NOT ach-jwt-signing-keys — that is random per run (openssl), kept imperative.
generatorOptions:
  disableNameSuffixHash: true
secretGenerator:
  - name: ach-db-url
    literals:
      - url=postgres://ach:ach@ach-postgres.ach-system.svc.cluster.local:5432/ach?sslmode=disable
  - name: ach-credential-hash-pepper
    literals:
      - pepper=dev-pepper-32-bytes-minimum-for-hmac-do-not-reuse
  - name: litellm-master-key
    literals:
      - masterKey=sk-test-master-key
```

- [ ] **Step 2: Verify it builds**

Run: `./scripts/dev.sh kustomize build test/e2e/cluster/02-ach/secrets`
Expected: three `kind: Secret` documents named exactly `ach-db-url`, `ach-credential-hash-pepper`, `litellm-master-key` (no hash suffix).

- [ ] **Step 3: Replace the inline secret creates in reconcile_ach**

In `scripts/cluster.sh` `reconcile_ach()`, replace the two `kubectl create secret … --from-literal … | kubectl apply -f -` blocks (ach-credential-hash-pepper, ach-db-url) AND remove the `litellm-master-key` create from `reconcile_fixtures`, with a single line applied BEFORE the `helm upgrade`:

```bash
  # Dev secrets (chart prerequisites) — declarative, applied before helm so the
  # pods find them at boot. ach-jwt-signing-keys stays generated below (random).
  kubectl apply -k "${CLUSTER_DIR}/02-ach/secrets"
```

- [ ] **Step 4: Verify via cluster-sync**

Run: `make cluster-sync` then
`./scripts/dev.sh kubectl -n ach-system get secret ach-db-url ach-credential-hash-pepper litellm-master-key`
Expected: all three present; `make cluster-sync` exits 0.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/cluster/02-ach/secrets scripts/cluster.sh
git commit --no-verify -m "refactor(cluster.sh): dev secrets via kustomize secretGenerator"
```

### Task 2: Extract test backends (mcp-echo) out of the Helm chart

**Files:**
- Delete: `deploy/helm/ach/templates/test-mocks.yaml`
- Modify: `deploy/helm/ach/values.yaml` (remove the `testMocks:` block), `test/e2e/cluster/02-ach/ach.values.yaml` (remove its `testMocks:` block)
- Rename: `test/e2e/cluster/03-gateway/` → `test/e2e/cluster/03-test-backends/`
- Create: `test/e2e/cluster/03-test-backends/ach-mcp-echo.yaml`
- Modify: `scripts/cluster.sh` (reconcile_ach image build no longer gated on a chart value; gateway stage path)

- [ ] **Step 1: Capture the current rendered mcp-echo manifest (faithful copy incl. ACH_JWKS_URL)**

Run:
```bash
./scripts/dev.sh helm template ach deploy/helm/ach \
  --set testMocks.mcpEcho.enabled=true \
  --values test/e2e/cluster/02-ach/ach.values.yaml \
  --show-only templates/test-mocks.yaml
```
Expected: the `ach-mcp-echo` Deployment + Service. Record the 4 env values: `ACH_JWKS_URL`, `ACH_EXPECTED_ISS` (`http://localhost:8080`), `ACH_EXPECTED_AUD` (`mcp:demo-mcp-jwt,mcp:demo-mcp-nojwt`), `ACH_REQUIRE_JWT` (`false`).

- [ ] **Step 2: Rename the gateway stage and author the mcp-echo manifest**

```bash
git mv test/e2e/cluster/03-gateway test/e2e/cluster/03-test-backends
```
Create `test/e2e/cluster/03-test-backends/ach-mcp-echo.yaml` — the Deployment+Service from Step 1, with the env values as **literals** (image `ach-mcp-echo:e2e`, ns `ach-system`, Deployment name `ach-mcp-echo`, container port 9090, Service `ach-mcp-echo`). Keep the name `ach-mcp-echo` exactly — `phase4_bip_loop_test.go` waits on that Deployment.

Update `test/e2e/cluster/03-test-backends/kustomization.yaml`:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
# Stage 03 — local test backends (post-ach; need the ach Services / forwarder
# JWKS). Both are test-only scaffolding kept OUT of the user-facing Helm chart.
resources:
  - ach-local-gateway.yaml
  - ach-mcp-echo.yaml
```

- [ ] **Step 3: Verify it builds**

Run: `./scripts/dev.sh kustomize build test/e2e/cluster/03-test-backends | grep -E '^kind:|  name:'`
Expected: gateway (ConfigMap/Deployment/Service) + `ach-mcp-echo` (Deployment/Service).

- [ ] **Step 4: Strip testMocks from the chart**

```bash
git rm deploy/helm/ach/templates/test-mocks.yaml
```
Remove the entire `testMocks:` block from `deploy/helm/ach/values.yaml`, and the `testMocks:` block from `test/e2e/cluster/02-ach/ach.values.yaml`. (This also drops the unused `ach-mock-litellm`/`ach-mock-mcp` legacy mocks — e2e drives the real LiteLLM, nothing uses them.)

- [ ] **Step 5: Update cluster.sh — build mcp-echo unconditionally, apply via the renamed stage**

In `reconcile_ach`, replace the `grep -A3 … mcpEcho … ach.values.yaml`-gated image build with an unconditional build+load (e2e always needs the backend):
```bash
  echo "[cluster.sh] building ${MCP_ECHO_IMAGE}..."
  make build-image-mcp-echo
  kind load docker-image "${MCP_ECHO_IMAGE}" --name "${CLUSTER_NAME}"
```
And update the gateway apply in `reconcile_fixtures` to the renamed stage:
```bash
  kubectl apply -k "${CLUSTER_DIR}/03-test-backends"
```

- [ ] **Step 6: Chart renders clean (no test scaffolding)**

Run: `./scripts/dev.sh helm template ach deploy/helm/ach | grep -ciE 'ach-mock|ach-mcp-echo|testMocks'`
Expected: `0`.

- [ ] **Step 7: cluster-sync + assert mcp-echo comes from our manifest**

Run: `make cluster-sync` then
`./scripts/dev.sh kubectl -n ach-system get deploy ach-mcp-echo -o jsonpath='{.metadata.managedFields[*].manager}'`
Expected: applied via `kubectl`/kustomize (not Helm); `make cluster-sync` exits 0; `phase4_bip_loop_test` prerequisites (ach-mcp-echo Ready) still hold.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit --no-verify -m "refactor(helm): remove testMocks from chart; mcp-echo is a test/e2e/cluster manifest"
```

### Task 3: Move the object CRs into 04-objects/

**Files:**
- Create: `test/e2e/cluster/04-objects/*.yaml` (10 CRs, moved from examples/)
- Create: `test/e2e/cluster/04-objects/kustomization.yaml`

- [ ] **Step 1: git mv the 10 object CRs**

```bash
mkdir -p test/e2e/cluster/04-objects
git mv examples/01-litellmconnection.yaml            test/e2e/cluster/04-objects/litellmconnection.yaml
git mv examples/05-pluginmarketplace-anthropic.yaml  test/e2e/cluster/04-objects/marketplace-anthropic.yaml
git mv examples/05b-pluginmarketplace-caveman.yaml   test/e2e/cluster/04-objects/marketplace-caveman.yaml
git mv examples/06-plugin-caveman.yaml               test/e2e/cluster/04-objects/plugin-caveman.yaml
git mv examples/07-prompt-claudecode-leak.yaml       test/e2e/cluster/04-objects/prompt-claude-code.yaml
git mv examples/08-artifact-openclaw-templates.yaml  test/e2e/cluster/04-objects/artifact-openclaw.yaml
git mv examples/09-backendidentitypolicy-context7.yaml   test/e2e/cluster/04-objects/bip-context7.yaml
git mv examples/10-backendidentitypolicy-duplicate.yaml  test/e2e/cluster/04-objects/bip-duplicate.yaml
git mv examples/11-backendidentitypolicy-demo-mcp-jwt.yaml   test/e2e/cluster/04-objects/bip-demo-mcp-jwt.yaml
git mv examples/16-backendidentitypolicy-demo-mcp-nojwt.yaml test/e2e/cluster/04-objects/bip-demo-mcp-nojwt.yaml
```

- [ ] **Step 2: Write the kustomization**

Create `test/e2e/cluster/04-objects/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

# Stage 04 — all non-Environment ACH objects, applied post-ach (CRDs exist),
# pre-Environment (Environments in 05 reference these by name). Sources are the
# real upstreams (option B); each CR pins refresh.interval=1h so steady-state
# stays under any anonymous GitHub quota (default git transport, no REST limit).
resources:
  - litellmconnection.yaml
  - marketplace-anthropic.yaml
  - marketplace-caveman.yaml
  - plugin-caveman.yaml
  - prompt-claude-code.yaml
  - artifact-openclaw.yaml
  - bip-context7.yaml
  - bip-duplicate.yaml
  - bip-demo-mcp-jwt.yaml
  - bip-demo-mcp-nojwt.yaml
```

- [ ] **Step 3: Verify it builds**

Run: `./scripts/dev.sh kustomize build test/e2e/cluster/04-objects | grep -c '^kind:'`
Expected: `10`.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/cluster/04-objects examples
git commit --no-verify -m "refactor(fixtures): move ACH object CRs into test/e2e/cluster/04-objects"
```

### Task 4: Move the Environments into 05-environment/

**Files:**
- Create: `test/e2e/cluster/05-environment/*.yaml` (moved from examples/04, 04b)
- Create: `test/e2e/cluster/05-environment/kustomization.yaml`

- [ ] **Step 1: git mv the Environments**

```bash
mkdir -p test/e2e/cluster/05-environment
git mv examples/04-environment-demo.yaml        test/e2e/cluster/05-environment/demo.yaml
git mv examples/04b-environment-unresolved.yaml test/e2e/cluster/05-environment/demo-unresolved.yaml
```

- [ ] **Step 2: Write the kustomization**

Create `test/e2e/cluster/05-environment/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

# Stage 05 — Environments LAST. demo references the 04-objects (plugins/prompts/
# artifacts) + LiteLLM-seeded models/mcp/agents; it can only resolve once those
# exist. demo-unresolved is the intentional negative-path fixture.
resources:
  - demo.yaml
  - demo-unresolved.yaml
```

- [ ] **Step 3: Verify it builds**

Run: `./scripts/dev.sh kustomize build test/e2e/cluster/05-environment | grep -c '^kind:'`
Expected: `2`.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/cluster/05-environment examples
git commit --no-verify -m "refactor(fixtures): move demo Environments into test/e2e/cluster/05-environment"
```

### Task 5: Rewire reconcile_all into the full staged pipeline

**Files:**
- Modify: `scripts/cluster.sh` (reconcile_fixtures → split; new reconcile_objects/reconcile_environments; reconcile_all reorder)

- [ ] **Step 1: Replace reconcile_fixtures + reconcile_examples with staged functions**

In `scripts/cluster.sh`, replace `reconcile_fixtures()` and `reconcile_examples()` with:

```bash
reconcile_fixtures() {
  # JWT signing keys (FWD-09) — random per run, so generated not declarative.
  if ! kubectl -n ach-system get secret ach-jwt-signing-keys >/dev/null 2>&1; then
    local jwttmp; jwttmp="$(mktemp -d)"
    trap "rm -rf '${jwttmp}'" RETURN
    openssl rand 32 > "${jwttmp}/current.seed"
    printf 'dev-%s' "$(date +%s)" > "${jwttmp}/current.kid"
    kubectl -n ach-system create secret generic ach-jwt-signing-keys \
      --from-file=current.kid="${jwttmp}/current.kid" \
      --from-file=current.seed="${jwttmp}/current.seed"
  else
    echo "[cluster.sh] ach-jwt-signing-keys already present — leaving as-is."
  fi
  # Stage 03 — test backends: gateway + mcp-echo (post-ach; gateway's static
  # nginx upstreams and mcp-echo's JWKS verify both need the ach Services).
  echo "[cluster.sh] applying test backends (stage 03)..."
  kubectl apply -k "${CLUSTER_DIR}/03-test-backends"
}

reconcile_objects() {
  # Stage 04 — all non-Environment ACH objects (CRDs from the 02 chart exist now).
  echo "[cluster.sh] applying objects (stage 04)..."
  kubectl apply -k "${CLUSTER_DIR}/04-objects"
}

reconcile_environments() {
  # Stage 05 — Environments last (reference the 04 objects).
  echo "[cluster.sh] applying Environments (stage 05)..."
  kubectl apply -k "${CLUSTER_DIR}/05-environment"
}
```

- [ ] **Step 2: Reorder reconcile_all**

Replace `reconcile_all()` body with:

```bash
reconcile_all() {
  reconcile_postgres
  reconcile_valkey
  reconcile_dex
  reconcile_litellm
  reconcile_toolhive
  reconcile_ach          # operator chart + secrets (Task 1) + build/load mcp-echo (Task 2)
  reconcile_fixtures     # jwt keys + test backends (gateway + mcp-echo, stage 03)
  reconcile_objects      # stage 04
  reconcile_environments # stage 05
  wait_ach
  verify_all             # stage 06 (Task 6)
}
```

- [ ] **Step 3: Syntax check**

Run: `bash -n scripts/cluster.sh`
Expected: no output (exit 0).

- [ ] **Step 4: Full sync run**

Run: `make cluster-sync`
Expected: exits 0; logs show stages 04 (objects) and 05 (Environments) applying.

- [ ] **Step 5: Commit**

```bash
git add scripts/cluster.sh
git commit --no-verify -m "refactor(cluster.sh): stage 04-objects + 05-environment in reconcile_all"
```

### Task 6: The 06-verify readiness gate

**Files:**
- Modify: `scripts/cluster.sh` (add `verify_all`; dispatch case)

- [ ] **Step 1: Write verify_all**

Add to `scripts/cluster.sh`:

```bash
verify_all() {
  # Stage 06 — block until every synced object reaches its healthy condition.
  # This is the "everything is OK before we run tests" gate the e2e suite relies
  # on (tests assert, they do not apply). WAIT_TIMEOUT default 300s per resource.
  local to="${VERIFY_TIMEOUT:-300s}"
  echo "[cluster.sh] verifying all synced objects healthy (stage 06)..."
  # Test backends (stage 03) up before asserting the JWT/MCP path.
  kubectl -n ach-system rollout status deploy/ach-mcp-echo --timeout="$to"
  kubectl -n ach-system wait --for=condition=Ready          --timeout="$to" litellmconnection/default
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="$to" plugin/caveman
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="$to" prompt/claude-code-system-prompt
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="$to" artifact/openclaw-templates
  kubectl -n ach-system wait --for=condition=Synced --timeout="$to" pluginmarketplace/anthropic
  kubectl -n ach-system wait --for=condition=Synced --timeout="$to" pluginmarketplace/caveman
  for b in bip-context7 bip-demo-mcp-jwt bip-demo-mcp-nojwt; do
    kubectl -n ach-system wait --for=condition=Synced --timeout="$to" backendidentitypolicy/"$b"
  done
  kubectl -n ach-system wait --for=condition=Available --timeout="$to" environment/demo
  echo "[cluster.sh] all synced objects healthy."
}
```

> Note: `bip-duplicate` and `demo-unresolved` are intentional negative/edge fixtures — do NOT gate on them (they never reach the "happy" condition). If `kubectl wait` reports an unknown condition for a kind, run `kubectl get <kind>/<name> -o yaml` to read the actual condition `type` and correct the line.

- [ ] **Step 2: Add the dispatch case**

In the `case "${1:-}"` block, add `verify_all`:

```bash
  wait_ach) wait_ach ;;
  verify_all) verify_all ;;
```

- [ ] **Step 3: Run it against the synced cluster**

Run: `make cluster-sync` (now ends with verify_all)
Expected: exits 0; final log `all synced objects healthy.` If a `wait` times out, inspect with `kubectl get <kind>/<name> -o yaml` — that is a real unhealthy object, fix the CR/source, not the gate.

- [ ] **Step 4: Commit**

```bash
git add scripts/cluster.sh
git commit --no-verify -m "feat(cluster.sh): 06-verify gate — block until all synced objects healthy"
```

### Task 7: Update preflight + comments + docs for the new stages

**Files:**
- Modify: `scripts/cluster.sh` (header comment + preflight), `references/makefile.md`, `CLAUDE.md`, `examples/README.md`

- [ ] **Step 1: Extend the header stage map**

Update the `CLUSTER_DIR` header comment in `scripts/cluster.sh` to list `04-objects/`, `05-environment/`, and the `06-verify` step.

- [ ] **Step 2: Document the examples-vs-fixtures split**

In `examples/README.md` and the `CLAUDE.md` repo-layout section, state: `test/e2e/cluster/{04-objects,05-environment}/` holds the **synced test fixtures**; `examples/` holds **curated user-facing examples** (the remaining `12-15` ToolHive + `prometheus-servicemonitor` + any re-added generic samples). They are independent.

- [ ] **Step 3: Commit**

```bash
git add scripts/cluster.sh references/makefile.md CLAUDE.md examples/README.md
git commit --no-verify -m "docs: document staged synced-fixtures pipeline + examples split"
```

---

## PHASE B — Assert-only tests

### Task 8: §11f finalizer-drain on a throwaway Environment

**Files:**
- Create: `test/e2e/fixtures/phase4_drain_environment.yaml`
- Modify: `test/e2e/phase4_promotion_test.go:380-407`

- [ ] **Step 1: Author the throwaway Environment**

Create `test/e2e/fixtures/phase4_drain_environment.yaml` — a copy of the demo Environment renamed so it never collides with the synced `demo`:

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: demo-drain
  namespace: ach-system
spec:
  authorizedTeams: [default]
  runtime:
    models: [demo-model]
    mcpServers: [demo-mcp-jwt, demo-mcp-nojwt]
    a2aAgents: [demo-agent]
  context:
    prompts: [claude-code-system-prompt]
    plugins: [caveman]
    artifacts: [openclaw-templates]
```

- [ ] **Step 2: Rewrite §11f.Environment to use demo-drain**

Replace the bundle-apply + delete-demo + reapply block (`phase4_promotion_test.go` lines ~380-407) with: apply ONLY the throwaway, assert resolve, delete it (finalizer drain), no reapply. The referenced objects (plugin/prompt/artifact/conn) are already synced by `04-objects`.

```go
		// Finalizer-drain on a THROWAWAY Environment — never touches the synced
		// "demo" (other specs assert against it). The execution resources it
		// references are pre-synced by cluster.sh (04-objects).
		const drain = "../../test/e2e/fixtures/phase4_drain_environment.yaml"
		if out, err := runCmd("kubectl", "apply", "-f", drain); err != nil {
			t.Fatalf("§11f.Env apply drain: %v\n%s", err, out)
		}
		waitForCondition(t, "environment", "demo-drain",
			"ExecutionResourcesResolved", "True", 120*time.Second)
		if out, err := runCmdLonger(120*time.Second,
			"kubectl", "delete", "environment", "demo-drain", "-n", namespace,
			"--wait=true"); err != nil {
			t.Fatalf("§11f.Env finalizer drain: %v\n%s", err, out)
		}
```

- [ ] **Step 3: Compile-check**

Run: `./scripts/dev.sh go vet -tags e2e ./test/e2e/...`
Expected: exit 0.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/fixtures/phase4_drain_environment.yaml test/e2e/phase4_promotion_test.go
git commit --no-verify -m "test(e2e): §11f finalizer-drain uses throwaway demo-drain, not shared demo"
```

### Task 9: Assert-only sweep across the e2e suite

**Files:** Audit + edit the test files that call `kubectl apply`/`create`. Inventory (from `grep -cE '"(apply|create)"'`): `phase4_promotion_test.go` (8), `phase2_invariants_test.go` (6), `phase1_invariants_test.go` (6), `e2e_suite_test.go` (2), `plugin_filter_test.go` (1), `phase5_invariants_test.go` (1), `phase5_helpers_test.go` (1), `phase4_promotion_helpers_test.go` (1), `phase3_helpers_test.go` (1), `cli_login_hydrate_test.go` (1).

- [ ] **Step 1: Classify each apply call**

Run: `grep -nE '"(apply|create)"' test/e2e/*.go`. For each call, classify as:
- **(a) steady-state pre-apply of a now-synced object** → DELETE the apply (+ any cleanup delete); the test asserts against the synced cluster. (e.g. the demo bundle in §11f's downstream subtests, the `01/05b/09/10` applies in promotion subtests.)
- **(b) lifecycle test** (creates→mutates→deletes its subject, e.g. conflict/promotion/finalizer) → convert to a **throwaway** resource under `test/e2e/fixtures/` (pattern from Task 8); never mutate a synced object.
- **(c) genuinely transient negative fixture** not in the synced set → keep, but point at a `test/e2e/fixtures/` file, not `examples/`.

- [ ] **Step 2: Apply the classification per file**

Edit each file. For (a): remove the apply and the `t.Cleanup`/`defer delete`; keep the `waitForCondition`/assert. For (b)/(c): repoint the path to a `test/e2e/fixtures/` throwaway. Remove any helper (e.g. another `mustKubectl`-style wrapper) that becomes unused, and drop now-orphaned imports.

- [ ] **Step 3: Compile-check after each file**

Run: `./scripts/dev.sh go vet -tags e2e ./test/e2e/...`
Expected: exit 0 after every file's edits.

- [ ] **Step 4: Grep for stale examples/ references**

Run: `grep -rn '\.\./\.\./examples/' test/e2e/*.go`
Expected: only references to files that STILL live in `examples/` (e.g. `hydrate.json`, ToolHive 12-15 if not moved). No reference to a moved object/Environment file.

- [ ] **Step 5: Commit**

```bash
git add test/e2e
git commit --no-verify -m "test(e2e): assert against synced cluster, stop applying fixtures"
```

### Task 10: Full e2e green against the synced cluster

**Files:** none (verification task).

- [ ] **Step 1: Clean-room run**

Run:
```bash
make cluster-down
make e2e-full
```
Expected: cluster comes up through all stages, `06-verify` passes, e2e suite passes with tests asserting against synced state (no test applies a moved fixture).

- [ ] **Step 2: Focused promotion run (the lifecycle path)**

Run: `make e2e-keep` then `make e2e-focus RUN='TestPhase4Promotion'`
Expected: PASS; `demo-drain` created+deleted, shared `demo` untouched (assert `kubectl get environment demo -n ach-system` still present after).

- [ ] **Step 3: Commit any fixups**

```bash
git add -A
git commit --no-verify -m "test(e2e): green against fully-synced verified cluster"
```

---

## Self-Review

**Spec coverage:**
- "Apply all objects in a generic stage" → Task 2 (`04-objects`).
- "Environment last (phase six)" → Task 3 (`05-environment`) + Task 4 ordering.
- "Make everything OK, then run tests" → Task 5 (`06-verify`).
- "plugin/prompt/artifact/marketplace by default" → Task 2 inventory (06/07/08/05/05b).
- "Option B real upstreams" → locked in header; `04-objects` CRs keep their real `github:` sources + 1h refresh.
- "Tests don't apply" → Task 8 (§11f) + Task 9 (sweep).
- "examples vs test-fixtures split" → Task 7 docs + the moves in Tasks 3/4.
- Secrets cleanup → Task 1.
- "Test mocks/mcp-echo out of the user-facing Helm chart" → Task 2 (strip `testMocks`, mcp-echo becomes our `03-test-backends/` manifest). ToolHive 12-15 are stale legacy, excluded.

**Open items deliberately deferred (not placeholders):**
- Task 2 Step 2 mcp-echo manifest body is a faithful copy of the Step 1 `helm template` render (exact env values captured at execution).
- Task 9 per-file edits are an audit-then-convert with two concrete documented patterns; exact line content depends on each file and is resolved at execution by classification (a)/(b)/(c).

**Risk to watch:** `06-verify` on Option B waits for `SourceReachable=True` on real GitHub-sourced CRs. If a clean-room `make e2e-full` flakes there, it is the documented anonymous-rate-limit/transport issue (CLAUDE.md "❌ SourceReachable=False reason=Unauthorized") — mitigate via the default `git` transport and the 1h refresh interval already on each CR, not by weakening the gate.
