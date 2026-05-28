#!/usr/bin/env bash
# scripts/cluster.sh — e2e cluster lifecycle.
#
# Brings up a kind cluster with ACH's runtime dependencies hydrated:
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

CLUSTER_NAME="${CLUSTER_NAME:-ach-e2e}"
KIND_CONFIG="${KIND_CONFIG:-scripts/kind-config.yaml}"

# Pinned chart versions — bump deliberately. (Phase 11+ will move these to
# test/e2e/values/*.values.yaml + CHART_PINS.md once that infra exists.)
# Per-component values files live under test/e2e/values/ — single source
# of truth for chartVersion + image tag + chart config consumed by every
# hydrate_* function. Mirrors sister alitellm-operator's
# test/e2e/values/ layout. cluster.sh ONLY reads from these files;
# changing a chart version or an image pin is a YAML edit, not a script
# edit.
VALUES_DIR="${VALUES_DIR:-test/e2e/values}"

# Read the `chartVersion:` line from a values file. Used by hydrate_*
# functions so the chart-version pin and the chart-values themselves
# live in one file. Helm tolerates the unknown top-level key.
chart_version_of() {
  awk '/^chartVersion:/ {print $2; exit}' "$1"
}

# Bitnami pruned all pinned image tags from docker.io/bitnami/* in 2025 and
# moved the snapshots to docker.io/bitnamilegacy/*. The bitnamilegacy/* repo
# overrides are inlined in test/e2e/values/{postgres,valkey,litellm}.values.yaml
# (single source of truth — no env-var indirection from cluster.sh). If
# bitnamilegacy is also pruned, switch to upstream postgres:16-alpine +
# valkey/valkey (TODO.md item 1a option c) or mirror to ghcr.io/ackstorm/mirror/*.

# ach image coordinates used by hydrate_ach. CI builds the image with
# `make docker-build IMG=${ACH_IMAGE}` before invoking `cluster.sh up`;
# local developers can override either piece. The default tag `e2e` is
# deliberately not `latest` so a stale cached `latest` cannot mask a
# missing freshly-built image.
ACH_IMAGE_REPO="${ACH_IMAGE_REPO:-ghcr.io/ackstorm/ach}"
ACH_IMAGE_TAG="${ACH_IMAGE_TAG:-e2e}"
ACH_IMAGE="${ACH_IMAGE_REPO}:${ACH_IMAGE_TAG}"

usage() {
  cat <<'USAGE' >&2
scripts/cluster.sh — e2e cluster lifecycle.

Usage:
  scripts/cluster.sh up        # create kind + hydrate dependencies + wait Ready
  scripts/cluster.sh hydrate   # re-apply hydration on an already-up cluster
  scripts/cluster.sh down      # delete kind cluster
  scripts/cluster.sh keep      # same as up but no EXIT trap (local iteration)
  scripts/cluster.sh status    # print kubectl + helm state
  scripts/cluster.sh wait_ach  # wait for ach Deployments (operator+platform-api+forwarder) Ready
USAGE
  exit 1
}

cmd_up()      { create_cluster; create_namespaces; hydrate_all; }
cmd_hydrate() { create_namespaces; hydrate_all; }
cmd_down()    { kind delete cluster --name "${CLUSTER_NAME}" || true; }
cmd_keep()    { cmd_up; }
cmd_status()  { print_status; }

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
  kind get kubeconfig --name "${CLUSTER_NAME}" > "${kube_dir}/config"
}

create_namespaces() {
  local ns
  for ns in default ach-system litellm-system toolhive-system dex-system mocks dev prod; do
    kubectl get ns "${ns}" >/dev/null 2>&1 || kubectl create ns "${ns}"
  done
}

hydrate_postgres() {
  local version; version="$(chart_version_of "${VALUES_DIR}/postgres.values.yaml")"
  echo "[cluster.sh] installing postgres chart ${version}..."
  # Helm release name `ach-postgres` matches postgres.values.yaml's
  # fullnameOverride so every k8s object the chart renders carries that
  # exact name (no `-postgresql` suffix). e2e tests look up
  # svc/ach-postgres and sts/ach-postgres directly.
  helm upgrade --install ach-postgres oci://registry-1.docker.io/bitnamicharts/postgresql \
    --version "${version}" \
    --namespace ach-system \
    --values "${VALUES_DIR}/postgres.values.yaml" \
    --wait --timeout 5m
}

hydrate_valkey() {
  local version; version="$(chart_version_of "${VALUES_DIR}/valkey.values.yaml")"
  echo "[cluster.sh] installing valkey chart ${version}..."
  helm upgrade --install valkey oci://registry-1.docker.io/bitnamicharts/valkey \
    --version "${version}" \
    --namespace ach-system \
    --values "${VALUES_DIR}/valkey.values.yaml" \
    --wait --timeout 5m
}

hydrate_dex() {
  local version; version="$(chart_version_of "${VALUES_DIR}/dex.values.yaml")"
  echo "[cluster.sh] installing dex chart ${version}..."
  helm repo add dex https://charts.dexidp.io >/dev/null 2>&1 || true
  helm repo update dex >/dev/null
  # The dex chart expects raw Dex YAML under a top-level `config:` key.
  # scripts/dex-config.yaml ships with `issuer: http://localhost:5556/dex`
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
    cat "${VALUES_DIR}/dex.values.yaml"
    echo ""
    echo "config:"
    sed -e 's|^issuer: http://localhost:5556/dex|issuer: http://dex.dex-system.svc.cluster.local:5556/dex|' \
        -e 's/^/  /' scripts/dex-config.yaml
  } > "${tmpvals}"
  helm upgrade --install dex dex/dex \
    --version "${version}" \
    --namespace dex-system \
    --values "${tmpvals}" \
    --wait --timeout 3m
  rm -f "${tmpvals}"
}

hydrate_litellm() {
  # Read chartVersion + image tag from the values file (single source of
  # truth — mirrors sister alitellm-operator/scripts/cluster.sh).
  local version image_tag image
  version="$(awk '/^chartVersion:/ {print $2; exit}' "${VALUES_DIR}/litellm.values.yaml")"
  image_tag="$(awk '/^[[:space:]]*tag:/ {gsub(/[[:space:]]+#.*/,""); print $2; exit}' "${VALUES_DIR}/litellm.values.yaml")"
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
  trap 'rm -rf "${tmpdir}"' EXIT
  ( cd "${tmpdir}" && helm pull oci://docker.litellm.ai/berriai/litellm-helm --version "${version}" --untar )
  helm upgrade --install litellm "${tmpdir}/litellm-helm" \
    --namespace litellm-system \
    --values "${VALUES_DIR}/litellm.values.yaml" \
    --wait --timeout 5m

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
  # name/server_name/agent_name (v1.83.10 behavior). We exec curl
  # through the LiteLLM Pod's runtime image (alpine ships curl) so
  # we don't need a port-forward.
  echo "[cluster.sh] seeding LiteLLM with demo Model + MCP + A2A (issue #17)..."

  # Wait for /health/readiness so seed calls don't 503 on a still-starting pod.
  kubectl -n litellm-system exec deploy/litellm -c litellm -- \
    sh -c 'for i in $(seq 1 30); do curl -sf http://localhost:4000/health/readiness && exit 0; sleep 2; done; exit 1'

  # 1) Seed Model — points at LiteLLM's openai mock backend, no upstream creds needed.
  set +e
  local seed_out
  seed_out="$(kubectl -n litellm-system exec deploy/litellm -c litellm -- \
    curl -s -X POST http://localhost:4000/model/new \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "model_name": "demo-model",
        "litellm_params": {
          "model": "openai/demo-model",
          "api_base": "http://localhost:4000/mock",
          "api_key": "sk-mock"
        }
      }' 2>&1)"
  echo "[cluster.sh]   model 'demo-model' → ${seed_out}"

  # 2) Seed MCP server.
  seed_out="$(kubectl -n litellm-system exec deploy/litellm -c litellm -- \
    curl -s -X POST http://localhost:4000/v1/mcp/server \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "server_name": "demo-mcp",
        "transport": "http",
        "url": "http://localhost:4000/mock-mcp"
      }' 2>&1)"
  echo "[cluster.sh]   mcp server 'demo-mcp' → ${seed_out}"

  # 3) Seed A2A agent.
  seed_out="$(kubectl -n litellm-system exec deploy/litellm -c litellm -- \
    curl -s -X POST http://localhost:4000/v1/agents \
      -H 'Authorization: Bearer sk-test-master-key' \
      -H 'Content-Type: application/json' \
      -d '{
        "agent_name": "demo-agent",
        "agent_card_params": {
          "name": "demo-agent",
          "url": "http://localhost:4000/mock-agent"
        }
      }' 2>&1)"
  echo "[cluster.sh]   a2a agent 'demo-agent' → ${seed_out}"
  set -e

  rm -rf "${tmpdir}"
  trap - EXIT
}

hydrate_toolhive() {
  # ToolHive CRDs and operator are on independent version streams (0.0.x
  # vs 0.5.x) — both pins live in test/e2e/values/toolhive.values.yaml.
  local crds_version operator_version
  crds_version="$(awk '/^crdsChartVersion:/ {print $2}' "${VALUES_DIR}/toolhive.values.yaml")"
  operator_version="$(awk '/^operatorChartVersion:/ {print $2}' "${VALUES_DIR}/toolhive.values.yaml")"

  echo "[cluster.sh] installing toolhive-operator-crds @ ${crds_version}..."
  helm upgrade --install toolhive-operator-crds \
    oci://ghcr.io/stacklok/toolhive/toolhive-operator-crds \
    --version "${crds_version}" \
    --wait --timeout 60s

  echo "[cluster.sh] installing toolhive-operator @ ${operator_version}..."
  helm upgrade --install toolhive-operator \
    oci://ghcr.io/stacklok/toolhive/toolhive-operator \
    --version "${operator_version}" \
    --namespace toolhive-system \
    --wait --timeout 90s

  # Step 2.5 — add v1beta1 versions to ToolHive CRDs (not yet in
  # published charts). The OCI chart above ships only v1alpha1. This
  # fixture (vendored from stacklok/toolhive v0.28.0 @ 748a64228710...)
  # adds v1beta1 served=true, storage=false to MCPServer + VirtualMCPServer
  # so dual-vintage informers can register against both. v1alpha1 stays
  # storage=true (preserved). kubectl apply replaces the OCI chart's CRD
  # with the multi-version fixture; safe in ephemeral kind. Idempotent.
  echo "[cluster.sh] adding v1beta1 CRD versions (toolhive dual-vintage fixture)..."
  kubectl apply --server-side --force-conflicts \
    --field-manager=ach-cluster-bootstrap \
    -f test/e2e/fixtures/toolhive-v1beta1-crds.yaml
  echo "[cluster.sh] toolhive CRD versions after fixture: $(kubectl get crd mcpservers.toolhive.stacklok.dev -o jsonpath='{.spec.versions[*].name}' 2>/dev/null || echo 'crd-not-found')"
}

hydrate_ach() {
  echo "[cluster.sh] hydrating ach (image: ${ACH_IMAGE})..."

  # Build + kind-load the ach image (sister alitellm-operator pattern).
  # `make docker-build` is idempotent — Docker layer cache makes repeat
  # runs cheap. Inlining the build here removes the implicit ordering
  # requirement that callers (local dev, CI workflow) must remember to
  # run `make docker-build` first.
  echo "[cluster.sh] building ${ACH_IMAGE}..."
  make docker-build IMG="${ACH_IMAGE}"
  echo "[cluster.sh] kind load ${ACH_IMAGE} into '${CLUSTER_NAME}'..."
  kind load docker-image "${ACH_IMAGE}" --name "${CLUSTER_NAME}"

  # Required Secrets. Plain --from-literal because dev/e2e accepts dev
  # values; production deployments mount their own Secrets (the chart
  # references them by name only). Idempotent via dry-run-pipe-apply.
  kubectl -n ach-system create secret generic ach-credential-hash-pepper \
    --from-literal=pepper="dev-pepper-32-bytes-minimum-for-hmac-do-not-reuse" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n ach-system create secret generic ach-db-url \
    --from-literal=url="postgres://ach:ach@ach-postgres.ach-system.svc.cluster.local:5432/ach?sslmode=disable" \
    --dry-run=client -o yaml | kubectl apply -f -

  # extraEnv + chart config live in test/e2e/values/ach.values.yaml.
  # Image coordinates come from the ACH_IMAGE_REPO / ACH_IMAGE_TAG env
  # vars (CI sets them before invoking cluster.sh; default to dev tag
  # `e2e`) so the same chart can target prod registries too.
  #
  # NOTE: --wait is intentionally NOT passed here. The forwarder
  # Deployment depends on LiteLLMConnection/default existing at boot,
  # but the CR is seeded by hydrate_fixtures which runs AFTER this
  # function. If we --wait on helm, we deadlock: hydrate_fixtures
  # never gets to apply the CR. Instead, install without --wait,
  # then run hydrate_fixtures, then explicitly wait for rollouts.
  local helm_rc=0
  helm upgrade --install ach deploy/helm/ach \
    --namespace ach-system \
    --values "${VALUES_DIR}/ach.values.yaml" \
    --set "image.repo=${ACH_IMAGE_REPO}" \
    --set "image.tag=${ACH_IMAGE_TAG}" \
    --set "image.pullPolicy=IfNotPresent" || helm_rc=$?
  if [ "${helm_rc}" -ne 0 ]; then
    echo "[cluster.sh] ach helm install failed (rc=${helm_rc}) — dumping pods for forensics:" >&2
    kubectl -n ach-system get pods >&2 || true
    kubectl -n ach-system describe pods >&2 || true
    return "${helm_rc}"
  fi
}

wait_ach() {
  echo "[cluster.sh] waiting for ach Deployments to be Ready..."
  local rc=0
  for d in ach-operator ach-platform-api ach-forwarder; do
    kubectl -n ach-system rollout status deploy/"${d}" --timeout=5m || rc=$?
  done
  if [ "${rc}" -ne 0 ]; then
    echo "[cluster.sh] one or more ach Deployments failed to become Ready — dumping pods for forensics:" >&2
    kubectl -n ach-system get pods >&2 || true
    kubectl -n ach-system describe pods >&2 || true
    return "${rc}"
  fi
}

hydrate_fixtures() {
  # Seed cluster-scoped CRs the e2e suite needs to assert ACH end-to-end:
  #   - litellm-master-key Secret: the LiteLLMConnection operator uses
  #     to authenticate against the LiteLLM upstream during reconcile +
  #     §6.5 finalizer drain. Value must match LiteLLM chart's
  #     `masterkey` (test/e2e/values/litellm.values.yaml).
  #   - LiteLLMConnection/default: the CR that wires the operator's
  #     litellm-rest client to the in-cluster LiteLLM Service. Without
  #     it, every Environment finalizer drain stalls on
  #     "§6.5 step 2 DeleteAccessGroup: litellm connection not ready".
  #
  # The LiteLLM `default` Team is NOT seeded here. The operator's
  # LiteLLMConnection reconciler calls EnsureDefaultTeam(ctx) after a
  # successful probe (idempotent — list-first, create-on-empty). That
  # way production deployments converge without cluster.sh / hand-curl.
  echo "[cluster.sh] seeding e2e fixtures (litellm-master-key + LiteLLMConnection + JWT signing keys)..."
  kubectl -n ach-system create secret generic litellm-master-key \
    --from-literal=masterKey="sk-test-master-key" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl apply -f config/samples/ach_v1alpha1_litellmconnection.yaml

  # JWT signing keys Secret (FWD-09). The forwarder refuses-to-start
  # without it. Generated fresh per `cluster.sh up` invocation — kid is
  # a timestamp so re-running hydration produces a new (kid, seed) pair
  # instead of clobbering with a stale value. NOT the same as the
  # test/e2e/fixtures/*UNSAFE* known-plaintext seed: that one lives in
  # `default` for SC#4 JWKS-roundtrip asserts and must never land in
  # `ach-system`.
  if ! kubectl -n ach-system get secret ach-jwt-signing-keys >/dev/null 2>&1; then
    local jwttmp; jwttmp="$(mktemp -d)"
    openssl rand 32 > "${jwttmp}/current.seed"
    printf 'dev-%s' "$(date +%s)" > "${jwttmp}/current.kid"
    kubectl -n ach-system create secret generic ach-jwt-signing-keys \
      --from-file=current.kid="${jwttmp}/current.kid" \
      --from-file=current.seed="${jwttmp}/current.seed"
    rm -rf "${jwttmp}"
  else
    echo "[cluster.sh] ach-jwt-signing-keys Secret already present — leaving as-is."
  fi
}

hydrate_examples() {
  # Apply the issue #17 demo Environments so a fresh `cluster.sh up` lands
  # the cluster in a state the AccessGroupSynced contract can be eyeballed
  # against without any further `kubectl apply`:
  #   - examples/04-environment-demo.yaml      → AccessGroupSynced=True
  #   - examples/04b-environment-unresolved.yaml → AccessGroupSynced=False
  #     reason=UnresolvedReferences
  # Both are namespaced to ach-system. Applied after wait_ach so the
  # operator is Ready to reconcile on first observation.
  echo "[cluster.sh] applying issue #17 demo Environments..."
  kubectl apply -f examples/04-environment-demo.yaml
  kubectl apply -f examples/04b-environment-unresolved.yaml
}

hydrate_all() {
  hydrate_postgres
  hydrate_valkey
  hydrate_dex
  hydrate_litellm
  hydrate_toolhive
  hydrate_ach
  hydrate_fixtures
  wait_ach
  hydrate_examples
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
  echo "== hydration =="
  helm ls -A
  echo
  echo "== ach-system pods =="
  kubectl -n ach-system get pods 2>/dev/null
  echo
  echo "== litellm-system pods =="
  kubectl -n litellm-system get pods 2>/dev/null
)

case "${1:-}" in
  up|hydrate|down|keep|status) "cmd_${1}" ;;
  wait_ach) wait_ach ;;
  *) usage ;;
esac
