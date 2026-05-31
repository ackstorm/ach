#!/usr/bin/env bash
# scripts/cluster.sh — e2e cluster lifecycle.
#
# Brings up a kind cluster with ACH's runtime dependencies reconciled:
#   - PostgreSQL (bitnami chart) — operator metadata + platform-api state
#   - Valkey (bitnami chart) — forwarder cache
#   - Dex (dexidp chart) — OIDC issuer for ach-platform-api
#   - BerriAI litellm-helm — upstream LiteLLM gateway
#   - ToolHive operator — vendored MCP servers
#
# The ACH operator + content-service + platform-api + forwarder Deployments
# are deployed by `cluster.sh up` in later phases (currently deferred until
# the helm chart is authored in Phase 11+).

set -euo pipefail

# cluster.sh is internal plumbing invoked by `make cluster-*`, which routes
# through scripts/dev.sh (where helm/kind/kubectl live). Refuse to run on a
# bare host that may lack these tools — that is the half-created-cluster bug.
if [[ "${ACH_IN_DEVTOOLS:-0}" != "1" ]]; then
    echo "scripts/cluster.sh: run via 'make cluster-up' (must be inside devtools), not directly." >&2
    exit 1
fi

CLUSTER_NAME="${CLUSTER_NAME:-ach-e2e}"
KIND_CONFIG="${KIND_CONFIG:-scripts/kind-config.yaml}"

# All cluster bring-up config lives under CLUSTER_DIR, organized by bring-up
# STAGE (mirrors the reconcile_all order):
#   00-namespaces/  kustomize base — the namespace layout (apply -k, pre-helm)
#   01-base/        helm values for the 5 deps (postgres/valkey/dex/litellm/
#                   toolhive) + dex-config.yaml + auth_user_map.py
#   02-ach/         ach.values.yaml (the ach chart) + secrets/ (secretGenerator)
#   03-test-backends/ kustomize base — nginx gateway + ach-mcp-echo +
#                   ach-mock-model (apply -k, post-ach)
#   04-objects/     kustomize base — all non-Environment ACH CRs (apply -k,
#                   post-ach; LiteLLMConnection/plugins/prompts/artifacts/BIPs/
#                   marketplaces) sourced from their real upstreams (option B)
#   05-environment/ kustomize base — the demo Environments LAST (reference 04)
#   06-verify       NOT a directory — the verify_all step that blocks until
#                   every synced object reaches its healthy condition
# Each values file carries its own `chartVersion:` pin (single source of
# truth for chart version + image tag + chart config). cluster.sh ONLY reads
# from these files; changing a chart version or image pin is a YAML edit, not
# a script edit.
CLUSTER_DIR="${CLUSTER_DIR:-test/e2e/cluster}"

# Read the `chartVersion:` line from a values file. Used by reconcile_*
# functions so the chart-version pin and the chart-values themselves
# live in one file. Helm tolerates the unknown top-level key.
chart_version_of() {
  awk '/^chartVersion:/ {print $2; exit}' "$1"
}

# Bitnami pruned all pinned image tags from docker.io/bitnami/* in 2025 and
# moved the snapshots to docker.io/bitnamilegacy/*. The bitnamilegacy/* repo
# overrides are inlined in test/e2e/cluster/01-base/{postgres,valkey,litellm}.values.yaml
# (single source of truth — no env-var indirection from cluster.sh). If
# bitnamilegacy is also pruned, switch to upstream postgres:16-alpine +
# valkey/valkey (TODO.md item 1a option c) or mirror to ghcr.io/ackstorm/mirror/*.

# ach image coordinates used by reconcile_ach. CI builds the image with
# `make build-image IMG=${ACH_IMAGE}` before invoking `cluster.sh up`;
# local developers can override either piece. The default tag `e2e` is
# deliberately not `latest` so a stale cached `latest` cannot mask a
# missing freshly-built image.
ACH_IMAGE_REPO="${ACH_IMAGE_REPO:-ghcr.io/ackstorm/ach}"
ACH_IMAGE_TAG="${ACH_IMAGE_TAG:-e2e}"
ACH_IMAGE="${ACH_IMAGE_REPO}:${ACH_IMAGE_TAG}"

# ach-mcp-echo backend image (issue #35). Built + kind-loaded unconditionally
# by reconcile_ach (e2e always needs it). The tag MUST match what the
# `build-image-mcp-echo` make target produces (ach-mcp-echo:e2e) and what the
# test/e2e/cluster/03-test-backends/ach-mcp-echo.yaml Deployment pulls
# (pullPolicy=IfNotPresent) — kind load is given that exact tag.
MCP_ECHO_IMAGE="${MCP_ECHO_IMAGE:-ach-mcp-echo:e2e}"

# ach-mock — OpenAI chat-completion + a2a echo/capture backend that sits BEHIND
# the real LiteLLM as the model upstream (it is NOT a LiteLLM mock). Built +
# kind-loaded unconditionally by reconcile_ach (e2e always needs it for ek_
# tag-injection asserts). The tag MUST match what `make build-image-mock`
# produces (ach-mock:e2e) and what
# test/e2e/cluster/03-test-backends/ach-mock-model.yaml pulls
# (pullPolicy=IfNotPresent).
MOCK_IMAGE="${MOCK_IMAGE:-ach-mock:e2e}"

usage() {
  cat <<'USAGE' >&2
scripts/cluster.sh — e2e cluster lifecycle.

Usage:
  scripts/cluster.sh up         # create kind + reconcile dependencies + wait Ready (transactional)
  scripts/cluster.sh sync       # reconcile infra/fixtures on an already-up cluster (no recreate)
  scripts/cluster.sh down       # delete kind cluster
  scripts/cluster.sh reset      # down then up (clean recreate)
  scripts/cluster.sh status     # print kubectl + helm state
  scripts/cluster.sh preflight  # check tooling, values files, chart pins, ports
  scripts/cluster.sh wait_ach   # wait for ach Deployments (operator+platform-api+forwarder) Ready
USAGE
  exit 1
}

cmd_up() {
  local created=0
  if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then created=1; fi
  # Keep a half-created cluster on failure by DEFAULT (forensics-friendly: a
  # failed bringup leaves the node + partial state to inspect). Opt into
  # teardown with DELETE_ON_FAILURE=1, and only if THIS run created the
  # cluster — never delete one that already existed before this invocation.
  trap 'rc=$?; if [ "$rc" -ne 0 ] && [ "'"$created"'" = "1" ] && [ "${DELETE_ON_FAILURE:-0}" = "1" ]; then echo "[cluster.sh] up failed (rc=$rc) + DELETE_ON_FAILURE=1 — deleting ${CLUSTER_NAME}" >&2; kind delete cluster --name "${CLUSTER_NAME}" || true; fi' EXIT
  create_cluster; create_namespaces; reconcile_all
  trap - EXIT
}
cmd_sync() {
  # Reconcile infra/fixtures on an EXISTING cluster. A failed sync NEVER
  # deletes the cluster — you are actively iterating on it, so losing it on a
  # transient hiccup would be hostile. Tear it down explicitly with
  # `make cluster-down` (or `make cluster-reset`) when you want a clean slate.
  create_namespaces; reconcile_all
}
cmd_down()  { kind delete cluster --name "${CLUSTER_NAME}" || true; }
cmd_reset() { cmd_down; cmd_up; }
cmd_status(){ print_status; }

cmd_preflight() {
  echo "== cluster preflight =="
  for t in kind kubectl helm jq openssl; do command -v "$t" >/dev/null 2>&1 && echo "OK   $t" || { echo "FAIL $t MISSING"; exit 1; }; done
  docker info >/dev/null 2>&1 && echo "OK   docker daemon reachable" || { echo "FAIL docker unreachable"; exit 1; }
  test -d "${CLUSTER_DIR}" && echo "OK   values dir ${CLUSTER_DIR}" || { echo "FAIL ${CLUSTER_DIR} missing"; exit 1; }
  for f in postgres valkey dex litellm; do test -f "${CLUSTER_DIR}/01-base/$f.values.yaml" && echo "OK   01-base/$f" || echo "WARN 01-base/$f.values.yaml missing"; done
  echo "OK   chart pins: postgres=$(chart_version_of "${CLUSTER_DIR}/01-base/postgres.values.yaml") litellm=$(chart_version_of "${CLUSTER_DIR}/01-base/litellm.values.yaml")"
  # Ports the kind extraPortMappings expose on the host (gateway 8080).
  for p in 8080; do (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null && { echo "WARN port $p already in use"; exec 3>&- ; } || echo "OK   port $p free"; done
}

create_cluster() {
  if kind get clusters | grep -qx "${CLUSTER_NAME}"; then
    echo "[cluster.sh] kind cluster '${CLUSTER_NAME}' already exists — skipping create"
  else
    echo "[cluster.sh] creating kind cluster '${CLUSTER_NAME}'..."
    kind create cluster \
      --name "${CLUSTER_NAME}" \
      --config "${KIND_CONFIG}" \
      --wait 60s
  fi
  # Sync the devtools-container kubeconfig at .gocache/kube/config from
  # the freshly-bound kind API server port. kind writes the host's
  # ~/.kube/config on cluster create but does NOT touch our container-
  # mounted copy. Every cluster-down/up rolls the random host port,
  # so stale state in .gocache/kube/config silently breaks any
  # `./scripts/dev.sh kubectl ...` call (connect: connection refused
  # on the previous random port). Sync once here keeps both surfaces
  # in lock-step.
  local kube_dir="${GOCACHE_KUBE_DIR:-.gocache/kube}"
  mkdir -p "${kube_dir}"
  # Atomic + validated write. A bare `kind get kubeconfig > config` truncates
  # the file BEFORE kind runs, so a failed read (cluster mid-teardown, kind
  # error) leaves an empty/skeleton config — which makes in-container kubectl
  # silently fall back to its legacy http://localhost:8080 default and 404
  # ("the server could not find the requested resource"). Write to a temp
  # file, require it non-empty, then mv into place.
  if kind get kubeconfig --name "${CLUSTER_NAME}" > "${kube_dir}/config.tmp" 2>/dev/null \
     && [[ -s "${kube_dir}/config.tmp" ]]; then
    mv -f "${kube_dir}/config.tmp" "${kube_dir}/config"
  else
    rm -f "${kube_dir}/config.tmp"
    echo "[cluster.sh] ERROR: 'kind get kubeconfig --name ${CLUSTER_NAME}' produced no output" >&2
    return 1
  fi
}

create_namespaces() {
  # Stage 00: the e2e namespace layout, applied declaratively before any helm
  # install so every chart's target namespace exists. `default` is built-in.
  kubectl apply -k "${CLUSTER_DIR}/00-namespaces"
}

reconcile_postgres() {
  local version; version="$(chart_version_of "${CLUSTER_DIR}/01-base/postgres.values.yaml")"
  echo "[cluster.sh] installing postgres chart ${version}..."
  # Helm release name `ach-postgres` matches postgres.values.yaml's
  # fullnameOverride so every k8s object the chart renders carries that
  # exact name (no `-postgresql` suffix). e2e tests look up
  # svc/ach-postgres and sts/ach-postgres directly.
  helm upgrade --install ach-postgres oci://registry-1.docker.io/bitnamicharts/postgresql \
    --version "${version}" \
    --namespace ach-system \
    --values "${CLUSTER_DIR}/01-base/postgres.values.yaml" \
    --atomic --wait --timeout 5m
}

reconcile_valkey() {
  local version; version="$(chart_version_of "${CLUSTER_DIR}/01-base/valkey.values.yaml")"
  echo "[cluster.sh] installing valkey chart ${version}..."
  helm upgrade --install valkey oci://registry-1.docker.io/bitnamicharts/valkey \
    --version "${version}" \
    --namespace ach-system \
    --values "${CLUSTER_DIR}/01-base/valkey.values.yaml" \
    --atomic --wait --timeout 5m
}

reconcile_dex() {
  local version; version="$(chart_version_of "${CLUSTER_DIR}/01-base/dex.values.yaml")"
  echo "[cluster.sh] installing dex chart ${version}..."
  helm repo add dex https://charts.dexidp.io >/dev/null 2>&1 || true
  helm repo update dex >/dev/null
  # The dex chart expects raw Dex YAML under a top-level `config:` key.
  # test/e2e/cluster/01-base/dex-config.yaml ships with `issuer: http://localhost:5556/dex`
  # for the `docker run` local UAT path documented in its header. For
  # in-cluster Dex, the issuer MUST be the cluster-internal URL the
  # platform-api uses so OIDC discovery's issuer-claim match succeeds
  # (oidc.NewProvider verifies the issuer claim against the URL used to
  # fetch /.well-known/openid-configuration). Sed-rewrite the issuer line
  # at install time so the canonical config stays docker-run-compatible,
  # then concat with the per-component values file so the chartVersion
  # pin still lives in YAML.
  local tmpvals; tmpvals="$(mktemp)"
  {
    cat "${CLUSTER_DIR}/01-base/dex.values.yaml"
    echo ""
    echo "config:"
    sed -e 's|^issuer: http://localhost:5556/dex|issuer: http://dex.dex-system.svc.cluster.local:5556/dex|' \
        -e 's/^/  /' "${CLUSTER_DIR}/01-base/dex-config.yaml"
  } > "${tmpvals}"
  helm upgrade --install dex dex/dex \
    --version "${version}" \
    --namespace dex-system \
    --values "${tmpvals}" \
    --atomic --wait --timeout 3m
  rm -f "${tmpvals}"
}

reconcile_litellm() {
  # Read chartVersion + image tag from the values file (single source of
  # truth — mirrors sister alitellm-operator/scripts/cluster.sh).
  local version image_tag image
  version="$(awk '/^chartVersion:/ {print $2; exit}' "${CLUSTER_DIR}/01-base/litellm.values.yaml")"
  image_tag="$(awk '/^[[:space:]]*tag:/ {gsub(/[[:space:]]+#.*/,""); print $2; exit}' "${CLUSTER_DIR}/01-base/litellm.values.yaml")"
  image="ghcr.io/berriai/litellm-database:${image_tag}"

  echo "[cluster.sh] installing litellm chart ${version} (image: ${image})..."

  # Pre-pull + kind load (sister pattern) — eliminates the ghcr.io
  # round-trip kubelet would otherwise do on every Pod start, including
  # every migrations Job backoff retry. Combined with
  # image.pullPolicy=IfNotPresent in the values file, the chart's
  # PreSync hook reaches the locally-resident image immediately.
  echo "[cluster.sh] pre-pulling ${image} on host..."
  docker pull "${image}"
  echo "[cluster.sh] kind-loading ${image} into ${CLUSTER_NAME}..."
  kind load docker-image "${image}" --name "${CLUSTER_NAME}"

  local tmpdir; tmpdir="$(mktemp -d)"
  # Pre-expand ${tmpdir} into the trap string at set-time (double quotes), NOT
  # at fire-time. The EXIT trap runs in global scope, where this function-local
  # is out of scope; a deferred 'rm -rf "${tmpdir}"' would hit `set -u` and die
  # with "tmpdir: unbound variable" whenever the function fails before the trap
  # is re-armed below (e.g. the helm install fails). Matches the line-259 trap.
  trap "rm -rf '${tmpdir}'" EXIT
  ( cd "${tmpdir}" && helm pull oci://docker.litellm.ai/berriai/litellm-helm --version "${version}" --untar )

  # Create ConfigMap with the custom auth script before installing/upgrading LiteLLM
  echo "[cluster.sh] creating/updating ackstorm-litellm-extras ConfigMap..."
  kubectl -n litellm-system create configmap ackstorm-litellm-extras \
    --from-file=auth_user_map.py="${CLUSTER_DIR}/01-base/auth_user_map.py" \
    -o yaml --dry-run=client | kubectl apply -f -

  helm upgrade --install litellm "${tmpdir}/litellm-helm" \
    --namespace litellm-system \
    --values "${CLUSTER_DIR}/01-base/litellm.values.yaml" \
    --atomic --wait --timeout 5m

  # helm --wait covers Deployment readiness + PreSync hook completion,
  # but a Job stuck in ImagePullBackOff can let helm time out silently
  # while the chart reports STATUS=deployed. Re-verify the migrations
  # Job explicitly so a regression fails loud here (vs producing 500s
  # in the e2e suite minutes later).
  kubectl -n litellm-system wait --for=condition=complete \
    job/litellm-migrations --timeout=180s

  # ─── issue #17: seed LiteLLM with DB-stored Model + MCP + A2A so
  # examples/04-environment-demo.yaml can reach AccessGroupSynced=True
  # end-to-end. These are the canonical names referenced by the demo
  # Environment; examples/04b-environment-unresolved.yaml deliberately
  # references absent names for negative-path coverage.
  #
  # All three calls are idempotent: LiteLLM returns 200 on duplicate
  # name/server_name/agent_name (v1.83.10 behavior). We use a local
  # port-forward so we do not depend on the container having curl in its PATH.
  echo "[cluster.sh] seeding LiteLLM with demo Model + MCP + A2A (issue #17)..."

  # Start a local port-forward to LiteLLM
  kubectl -n litellm-system port-forward svc/litellm 4001:4000 >/dev/null 2>&1 &
  local pf_pid=$!
  trap "kill ${pf_pid} 2>/dev/null || true; rm -rf ${tmpdir}" EXIT

  # Wait for /health/readiness so seed calls don't 503 on a still-starting pod.
  for i in $(seq 1 30); do
    curl -sf http://localhost:4001/health/readiness && break
    sleep 2
  done

  # 1) Seed Model — points at the ach-mock-model echo backend.
  set +e
  local seed_out
  # /model/new ADDS a deployment on every call (it does NOT update an existing
  # one), so re-running cluster-sync accumulates stale demo-model deployments
  # with the old api_base — LiteLLM round-robins onto the broken ones → 404.
  # Make it idempotent: delete every existing demo-model deployment first, then
  # create exactly one pointing at ach-mock-model.
  local mid mk="sk-test-master-key"
  for mid in $(curl -s http://localhost:4001/v1/model/info \
        -H "Authorization: Bearer ${mk}" \
      | jq -r '.data[] | select(.model_name=="demo-model") | .model_info.id'); do
    curl -s -X POST http://localhost:4001/model/delete \
      -H "Authorization: Bearer ${mk}" -H 'Content-Type: application/json' \
      -d "{\"id\":\"${mid}\"}" >/dev/null
  done
  seed_out="$(curl -s -X POST http://localhost:4001/model/new \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "model_name": "demo-model",
        "litellm_params": {
          "model": "openai/demo-model",
          "api_base": "http://ach-mock-model.ach-system.svc/v1",
          "api_key": "sk-mock"
        }
      }' 2>&1)"
  echo "[cluster.sh]   model 'demo-model' → ${seed_out}"

  # 2) Seed the demo Environment's two MCP servers (BIP closed-loop).
  #
  # Both point at the SAME ach-mcp-echo backend; the only difference is the
  # BackendIdentityPolicy applied to each (examples/11 + examples/16):
  #   - demo-mcp-jwt   → BIP forwardIdentityJWT: true  (forwarder mints JWT)
  #   - demo-mcp-nojwt → BIP forwardIdentityJWT: false (no Authorization)
  #
  # extra_headers: ["authorization"] is REQUIRED on BOTH so LiteLLM's MCP
  # gateway propagates whatever Authorization the forwarder sends (a JWT on
  # the jwt route, nothing on the nojwt route) instead of dropping it. The
  # backend runs with ACH_REQUIRE_JWT=false (testMocks.mcpEcho.requireJwt)
  # so the tokenless nojwt route is accepted and recorded jwt_present=false.
  for srv in demo-mcp-jwt demo-mcp-nojwt; do
    seed_out="$(curl -s -X POST http://localhost:4001/v1/mcp/server \
        -H 'Authorization: Bearer sk-test-master-key' \
        -H 'Content-Type: application/json' \
        -d "{
          \"server_name\": \"${srv}\",
          \"transport\": \"http\",
          \"url\": \"http://ach-mcp-echo.ach-system.svc\",
          \"extra_headers\": [\"authorization\"]
        }" 2>&1)"
    echo "[cluster.sh]   mcp server '${srv}' → ${seed_out}"
  done

  # 3) Seed A2A agent.
  seed_out="$(curl -s -X POST http://localhost:4001/v1/agents \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "agent_name": "demo-agent",
        "agent_card_params": {
          "name": "demo-agent",
          "url": "http://ach-mock-a2a.ach-system.svc/"
        }
      }' 2>&1)"
  echo "[cluster.sh]   a2a agent 'demo-agent' → ${seed_out}"
  set -e

  kill "${pf_pid}" 2>/dev/null || true
  rm -rf "${tmpdir}"
  trap - EXIT
}

reconcile_toolhive() {
  # ToolHive CRDs and operator are on independent version streams (0.0.x
  # vs 0.5.x) — both pins live in test/e2e/cluster/01-base/toolhive.values.yaml.
  local crds_version operator_version
  crds_version="$(awk '/^crdsChartVersion:/ {print $2}' "${CLUSTER_DIR}/01-base/toolhive.values.yaml")"
  operator_version="$(awk '/^operatorChartVersion:/ {print $2}' "${CLUSTER_DIR}/01-base/toolhive.values.yaml")"

  echo "[cluster.sh] installing toolhive-operator-crds @ ${crds_version}..."
  helm upgrade --install toolhive-operator-crds \
    oci://ghcr.io/stacklok/toolhive/toolhive-operator-crds \
    --version "${crds_version}" \
    --atomic --wait --timeout 60s

  echo "[cluster.sh] installing toolhive-operator @ ${operator_version}..."
  helm upgrade --install toolhive-operator \
    oci://ghcr.io/stacklok/toolhive/toolhive-operator \
    --version "${operator_version}" \
    --namespace toolhive-system \
    --atomic --wait --timeout 90s

  # Step 2.5 — add v1beta1 versions to ToolHive CRDs (not yet in
  # published charts). Since we upgraded to 0.28.3, this is natively supported.
  # We can skip applying this fixture now.
  echo "[cluster.sh] toolhive CRD versions: $(kubectl get crd mcpservers.toolhive.stacklok.dev -o jsonpath='{.spec.versions[*].name}' 2>/dev/null || echo 'crd-not-found')"
}

reconcile_ach() {
  echo "[cluster.sh] reconciling ach (image: ${ACH_IMAGE})..."

  # Build + kind-load the ach image (sister alitellm-operator pattern).
  # `make build-image` is idempotent — Docker layer cache makes repeat
  # runs cheap. Inlining the build here removes the implicit ordering
  # requirement that callers (local dev, CI workflow) must remember to
  # run `make build-image` first.
  echo "[cluster.sh] building ${ACH_IMAGE}..."
  make build-image IMG="${ACH_IMAGE}"
  echo "[cluster.sh] kind load ${ACH_IMAGE} into '${CLUSTER_NAME}'..."
  kind load docker-image "${ACH_IMAGE}" --name "${CLUSTER_NAME}"

  # Build + kind-load the ach-mcp-echo backend image (issue #35). e2e ALWAYS
  # needs this backend — it is applied unconditionally as a stage-03 test
  # backend (test/e2e/cluster/03-test-backends/ach-mcp-echo.yaml), no longer a
  # chart toggle. Inline the build so callers never have to remember a separate
  # `make build-image-mcp-echo` + manual kind load — otherwise the Deployment
  # sits in ImagePullBackOff (the :e2e tag is never pushed to a registry).
  echo "[cluster.sh] building ${MCP_ECHO_IMAGE}..."
  make build-image-mcp-echo
  echo "[cluster.sh] kind load ${MCP_ECHO_IMAGE} into '${CLUSTER_NAME}'..."
  kind load docker-image "${MCP_ECHO_IMAGE}" --name "${CLUSTER_NAME}"

  # Build + kind-load the ach-mock echo/capture backend (ach-mock:e2e) — the
  # OpenAI chat-completion + a2a upstream that sits behind the real LiteLLM.
  # Applied unconditionally as a stage-03 test backend
  # (test/e2e/cluster/03-test-backends/ach-mock-model.yaml), same rationale as
  # mcp-echo above (the :e2e tag is never pushed to a registry).
  echo "[cluster.sh] building ${MOCK_IMAGE}..."
  make build-image-mock
  echo "[cluster.sh] kind load ${MOCK_IMAGE} into '${CLUSTER_NAME}'..."
  kind load docker-image "${MOCK_IMAGE}" --name "${CLUSTER_NAME}"

  # Dev secrets (chart prerequisites) — declarative, applied before helm so the
  # pods find them at boot. ach-jwt-signing-keys stays generated below (random).
  kubectl apply -k "${CLUSTER_DIR}/02-ach/secrets"

  # extraEnv + chart config live in test/e2e/cluster/02-ach/ach.values.yaml.
  # Image coordinates come from the ACH_IMAGE_REPO / ACH_IMAGE_TAG env
  # vars (CI sets them before invoking cluster.sh; default to dev tag
  # `e2e`) so the same chart can target prod registries too.
  #
  # NOTE: --wait is intentionally NOT passed here. The forwarder
  # Deployment depends on LiteLLMConnection/default existing at boot,
  # but the CR is seeded by reconcile_fixtures which runs AFTER this
  # function. If we --wait on helm, we deadlock: reconcile_fixtures
  # never gets to apply the CR. Instead, install without --wait,
  # then run reconcile_fixtures, then explicitly wait for rollouts.
  local helm_rc=0
  # image.rebuildId is set to a per-build timestamp so the
  # ach.ackstorm.ai/rebuild-id pod annotation changes on every run; that
  # makes `helm upgrade` roll EVERY ach Deployment onto the freshly
  # kind-loaded image in a single transaction. Without it, the fixed
  # :e2e tag + pullPolicy=IfNotPresent render a byte-identical spec and
  # Helm leaves the stale pods running (a code change looks deployed but
  # the old container keeps serving). --set-string keeps the numeric
  # timestamp a string so the template's `quote` is well-defined.
  helm upgrade --install ach deploy/helm/ach \
    --namespace ach-system \
    --values "${CLUSTER_DIR}/02-ach/ach.values.yaml" \
    --set "image.repo=${ACH_IMAGE_REPO}" \
    --set "image.tag=${ACH_IMAGE_TAG}" \
    --set "image.pullPolicy=IfNotPresent" \
    --set-string "image.rebuildId=$(date +%s)" || helm_rc=$?
  if [ "${helm_rc}" -ne 0 ]; then
    echo "[cluster.sh] ach helm install failed (rc=${helm_rc}) — dumping pods for forensics:" >&2
    kubectl -n ach-system get pods >&2 || true
    kubectl -n ach-system describe pods >&2 || true
    return "${helm_rc}"
  fi
  # NOTE: the pod roll onto the rebuilt image is driven by image.rebuildId
  # above (the ach.ackstorm.ai/rebuild-id annotation), so the upgrade
  # itself recreates the pods — no separate `kubectl rollout restart`
  # needed. wait_ach (after reconcile_fixtures) blocks on readiness.
}

wait_ach() {
  echo "[cluster.sh] waiting for ach Deployments to be Ready..."
  local rc=0
  # ach-local-gateway is a dev/test add-on applied by hydrate_fixtures(),
  # NOT by the ach Helm install. Treat it as optional: only wait on it when
  # it is actually present, so standalone `make wait-ach` succeeds on a
  # partial bring-up instead of failing with a misleading rollout error.
  # ach-gateway is a core Deployment (gateway.enabled=true) installed by
  # the ach Helm chart; wait on it like the other core ach Deployments.
  local deps=(ach-operator ach-platform-api ach-forwarder ach-gateway)
  if kubectl -n ach-system get deploy/ach-local-gateway >/dev/null 2>&1; then
    deps+=(ach-local-gateway)
  fi
  for d in "${deps[@]}"; do
    kubectl -n ach-system rollout status deploy/"${d}" --timeout=5m || rc=$?
  done
  if [ "${rc}" -ne 0 ]; then
    echo "[cluster.sh] one or more ach Deployments failed to become Ready — dumping pods for forensics:" >&2
    kubectl -n ach-system get pods >&2 || true
    kubectl -n ach-system describe pods >&2 || true
    return "${rc}"
  fi
}

reconcile_fixtures() {
  # JWT signing keys Secret (FWD-09) + the stage-03 test backends.
  #
  # The LiteLLMConnection/default CR + litellm-master-key Secret are NO LONGER
  # seeded here: the Secret is a stage-02 kustomize secretGenerator (reconcile_ach)
  # and the LiteLLMConnection is a stage-04 object (reconcile_objects). The
  # LiteLLM `default` Team is NOT seeded anywhere — the operator's
  # LiteLLMConnection reconciler calls EnsureDefaultTeam(ctx) after a successful
  # probe (idempotent), so production converges without cluster.sh / hand-curl.

  # JWT signing keys Secret (FWD-09). The forwarder refuses-to-start without it.
  # Generated fresh per run — kid is a timestamp so re-running reconciliation
  # produces a new (kid, seed) pair instead of clobbering with a stale value.
  # NOT the same as the test/e2e/fixtures/*UNSAFE* known-plaintext seed: that
  # one lives in `default` for SC#4 JWKS-roundtrip asserts and must never land
  # in `ach-system`.
  if ! kubectl -n ach-system get secret ach-jwt-signing-keys >/dev/null 2>&1; then
    local jwttmp; jwttmp="$(mktemp -d)"
    trap "rm -rf '${jwttmp}'" RETURN
    openssl rand 32 > "${jwttmp}/current.seed"
    printf 'dev-%s' "$(date +%s)" > "${jwttmp}/current.kid"
    kubectl -n ach-system create secret generic ach-jwt-signing-keys \
      --from-file=current.kid="${jwttmp}/current.kid" \
      --from-file=current.seed="${jwttmp}/current.seed"
  else
    echo "[cluster.sh] ach-jwt-signing-keys Secret already present — leaving as-is."
  fi

  # Stage 03 — test backends: nginx gateway + ach-mcp-echo + ach-mock-model
  # (post-ach; the gateway's static proxy_pass upstreams and mcp-echo's JWKS
  # verify both need the ach Services already up).
  echo "[cluster.sh] applying test backends (stage 03)..."
  kubectl apply -k "${CLUSTER_DIR}/03-test-backends"
}

reconcile_objects() {
  # Stage 04 — all non-Environment ACH objects (CRDs from the 02 chart exist now;
  # Environments in 05 reference these by name). Includes LiteLLMConnection/default.
  echo "[cluster.sh] applying objects (stage 04)..."
  kubectl apply -k "${CLUSTER_DIR}/04-objects"
}

reconcile_environments() {
  # Stage 05 — Environments last (reference the 04 objects + LiteLLM-seeded
  # models/mcp/agents). demo → Available=True; demo-unresolved → intentional
  # AccessGroupSynced=False/UnresolvedReferences negative path.
  echo "[cluster.sh] applying Environments (stage 05)..."
  kubectl apply -k "${CLUSTER_DIR}/05-environment"
}

# Wait for a BackendIdentityPolicy to be reconciled. The BIP controller emits
# NO positive condition on the happy path (TODO.md §6 / OP-16 keep the closed
# condition set minimal) — `status.observedGeneration == metadata.generation`
# is itself the "reconciled OK" signal; the only condition it ever writes is
# Synced=False/ConflictWithUIRow. So we cannot `kubectl wait --for=condition`;
# instead bound-wait on observedGeneration matching the live generation.
wait_bip_reconciled() {
  local name="$1" to="$2" gen
  gen="$(kubectl -n ach-system get backendidentitypolicy "${name}" -o jsonpath='{.metadata.generation}')"
  kubectl -n ach-system wait --for=jsonpath="{.status.observedGeneration}=${gen}" \
    --timeout="${to}" backendidentitypolicy/"${name}"
}

verify_all() {
  # Stage 06 — block until every synced object reaches its healthy state. This
  # is the "everything is OK before we run tests" gate the e2e suite relies on
  # (tests assert, they do not apply). VERIFY_TIMEOUT default 300s per resource.
  #
  # Excluded from the happy-state gate on purpose (intentional negative/edge
  # fixtures): backendidentitypolicy/zz-bip-context7-jwt-off (duplicate-PK
  # demo) and environment/demo-unresolved (UnresolvedReferences). The Phase 5
  # *-invalid fixtures AND the Phase 02 SC#3 loser pluginmarketplace/conflict-mkt-b
  # ARE gated below — on their EXPECTED failure state (SourceReachable=False /
  # Synced=False) — so "everything is in its known state" still holds.
  local to="${VERIFY_TIMEOUT:-300s}"
  echo "[cluster.sh] verifying all synced objects healthy (stage 06)..."
  # Test backends (stage 03) up before asserting the JWT/MCP + capture paths.
  kubectl -n ach-system rollout status deploy/ach-mcp-echo     --timeout="${to}"
  kubectl -n ach-system rollout status deploy/ach-mock-model   --timeout="${to}"
  kubectl -n ach-system rollout status deploy/ach-mock-a2a     --timeout="${to}"
  kubectl -n ach-system wait --for=condition=Ready           --timeout="${to}" litellmconnection/default
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="${to}" plugin/caveman
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="${to}" prompt/claude-code-system-prompt
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="${to}" artifact/openclaw-templates
  kubectl -n ach-system wait --for=condition=Synced          --timeout="${to}" pluginmarketplace/anthropic-code
  kubectl -n ach-system wait --for=condition=Synced          --timeout="${to}" pluginmarketplace/caveman
  # Phase 02 SC#3 alphabetical name-conflict pair (both filter the real
  # anthropic catalogue to `feature-dev`): conflict-mkt-a is the alphabetical
  # winner (Synced=True); conflict-mkt-b is the loser, gated on its EXPECTED
  # failure state (Synced=False reason=NameConflict). Their 1m refresh.interval
  # makes the loser converge well inside this timeout even if it wins the
  # initial apply-race.
  kubectl -n ach-system wait --for=condition=Synced          --timeout="${to}" pluginmarketplace/conflict-mkt-a
  kubectl -n ach-system wait --for=condition=Synced=false    --timeout="${to}" pluginmarketplace/conflict-mkt-b
  for b in bip-context7-jwt-on bip-demo-mcp-jwt bip-demo-mcp-nojwt; do
    wait_bip_reconciled "${b}" "${to}"
  done
  kubectl -n ach-system wait --for=condition=Available       --timeout="${to}" environment/demo
  # Phase 5 content-service exercise matrix — valid half healthy.
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="${to}" plugin/plugin-valid
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="${to}" prompt/prompt-valid
  kubectl -n ach-system wait --for=condition=SourceReachable --timeout="${to}" artifact/artifact-valid
  kubectl -n ach-system wait --for=condition=Available       --timeout="${to}" environment/env-valid
  # Phase 5 SC2 unauthorized_team negative fixture. authorizedTeams names a
  # sentinel team absent from LiteLLM → AccessGroupSynced=False → Available=False
  # BY DESIGN, so it is NOT gated on Available. ExecutionResourcesResolved=True
  # is the gate: the operator sets it in the same reconcile that writes the
  # projection row the content-service reads, so once it is True the
  # authorized_teams=[sentinel] row exists and the UnauthorizedTeam case can
  # assert 403. (Mirrors the demo-unresolved exclusion idiom for not-Available
  # fixtures, but keeps a positive wait so the e2e suite never races the row.)
  kubectl -n ach-system wait --for=condition=ExecutionResourcesResolved --timeout="${to}" environment/env-team-denied
  # Phase 5 invalid half — gate on the EXPECTED FAILURE state (the operator
  # has fetched + failed). kubectl wait supports condition=<type>=false.
  kubectl -n ach-system wait --for=condition=SourceReachable=false --timeout="${to}" plugin/plugin-invalid
  kubectl -n ach-system wait --for=condition=SourceReachable=false --timeout="${to}" prompt/prompt-invalid
  kubectl -n ach-system wait --for=condition=SourceReachable=false --timeout="${to}" artifact/artifact-invalid
  echo "[cluster.sh] all synced objects healthy."
}

reconcile_all() {
  reconcile_postgres
  reconcile_valkey
  reconcile_dex
  reconcile_litellm
  reconcile_toolhive
  reconcile_ach          # operator chart + secrets (Task 1) + build/load mcp-echo + mock-model + mock-a2a (Task 2)
  reconcile_fixtures     # jwt keys + test backends (gateway + mcp-echo + mock-model + mock-a2a, stage 03)
  reconcile_objects      # stage 04
  reconcile_environments # stage 05
  wait_ach
  verify_all             # stage 06 (Task 6)
}

print_status() (
  # Subshell scope so `set +e` does not leak to the calling shell. Bash
  # function bodies share the caller's shell options; `return 0` only
  # masks the function's exit status, it does NOT restore errexit. After
  # `cmd_status` runs, the rest of cluster.sh (and any caller that sources
  # it) would inherit the relaxed errexit. Subshell form `(...)` scopes
  # the option change automatically — set +e dies with the subshell.
  set +e
  echo "== kind clusters =="
  kind get clusters
  echo
  echo "== nodes =="
  kubectl get nodes
  echo
  echo "== namespaces (e2e layout) =="
  kubectl get ns default ach-system litellm-system toolhive-system dex-system mocks dev prod 2>/dev/null
  echo
  echo "== reconcile =="
  helm ls -A
  echo
  echo "== ach-system pods =="
  kubectl -n ach-system get pods 2>/dev/null
  echo
  echo "== litellm-system pods =="
  kubectl -n litellm-system get pods 2>/dev/null
)

case "${1:-}" in
  up|sync|down|reset|status|preflight) "cmd_${1}" ;;
  wait_ach) wait_ach ;;
  verify_all) verify_all ;;
  *) usage ;;
esac
