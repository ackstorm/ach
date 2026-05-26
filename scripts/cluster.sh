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
    return 0
  fi
  echo "[cluster.sh] creating kind cluster '${CLUSTER_NAME}'..."
  kind create cluster \
    --name "${CLUSTER_NAME}" \
    --config "${KIND_CONFIG}" \
    --wait 60s
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
  helm upgrade --install postgres oci://registry-1.docker.io/bitnamicharts/postgresql \
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
    --from-literal=url="postgres://postgres:achdev@postgres-postgresql.ach-system.svc.cluster.local:5432/ach?sslmode=disable" \
    --dry-run=client -o yaml | kubectl apply -f -

  # extraEnv + chart config live in test/e2e/values/ach.values.yaml.
  # Image coordinates come from the ACH_IMAGE_REPO / ACH_IMAGE_TAG env
  # vars (CI sets them before invoking cluster.sh; default to dev tag
  # `e2e`) so the same chart can target prod registries too.
  local helm_rc=0
  helm upgrade --install ach deploy/helm/ach \
    --namespace ach-system \
    --values "${VALUES_DIR}/ach.values.yaml" \
    --set "image.repo=${ACH_IMAGE_REPO}" \
    --set "image.tag=${ACH_IMAGE_TAG}" \
    --set "image.pullPolicy=IfNotPresent" \
    --wait --timeout 5m || helm_rc=$?
  if [ "${helm_rc}" -ne 0 ]; then
    echo "[cluster.sh] ach helm install failed (rc=${helm_rc}) — dumping pods for forensics:" >&2
    kubectl -n ach-system get pods >&2 || true
    kubectl -n ach-system describe pods >&2 || true
    return "${helm_rc}"
  fi
}

hydrate_all() {
  hydrate_postgres
  hydrate_valkey
  hydrate_dex
  hydrate_litellm
  hydrate_toolhive
  hydrate_ach
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
  *) usage ;;
esac
