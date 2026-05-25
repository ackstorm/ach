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
POSTGRES_CHART_VERSION="${POSTGRES_CHART_VERSION:-15.5.38}"
VALKEY_CHART_VERSION="${VALKEY_CHART_VERSION:-2.4.1}"
DEX_CHART_VERSION="${DEX_CHART_VERSION:-0.19.1}"

# Bitnami pruned all pinned image tags from docker.io/bitnami/* in 2025 and
# moved the snapshots to docker.io/bitnamilegacy/* (newer Bitnami charts pin
# tag: latest, a moving target). We keep our pinned chart versions for
# reproducibility but redirect the image registry to bitnamilegacy/* which
# still serves the exact tags the charts reference. If bitnamilegacy is also
# pruned in the future, switch to upstream postgres:16-alpine + valkey/valkey
# (TODO: mirror to ghcr.io/ackstorm/mirror/* — see TODO.md item 1a option c).
BITNAMI_IMAGE_REGISTRY="${BITNAMI_IMAGE_REGISTRY:-docker.io}"
BITNAMI_IMAGE_REPO_PREFIX="${BITNAMI_IMAGE_REPO_PREFIX:-bitnamilegacy}"

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
  echo "[cluster.sh] installing postgres @ ${POSTGRES_CHART_VERSION}..."
  # Override each sub-image's repository to redirect bitnami/* → bitnamilegacy/*
  # (see BITNAMI_IMAGE_REPO_PREFIX comment above). global.imageRegistry on its
  # own is insufficient — the bitnamilegacy snapshots live under a sibling
  # repo path, not a sibling registry. global.security.allowInsecureImages
  # disables the chart's hard-coded "approved containers" guard that aborts
  # the install when image repositories differ from the bundled defaults.
  helm upgrade --install postgres oci://registry-1.docker.io/bitnamicharts/postgresql \
    --version "${POSTGRES_CHART_VERSION}" \
    --namespace ach-system \
    --set auth.postgresPassword=achdev \
    --set auth.database=ach \
    --set primary.persistence.size=1Gi \
    --set "global.imageRegistry=${BITNAMI_IMAGE_REGISTRY}" \
    --set "global.security.allowInsecureImages=true" \
    --set "image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/postgresql" \
    --set "volumePermissions.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/os-shell" \
    --set "metrics.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/postgres-exporter" \
    --wait --timeout 5m
}

hydrate_valkey() {
  echo "[cluster.sh] installing valkey @ ${VALKEY_CHART_VERSION}..."
  # Same Bitnami pruning workaround as postgres — see hydrate_postgres comment.
  helm upgrade --install valkey oci://registry-1.docker.io/bitnamicharts/valkey \
    --version "${VALKEY_CHART_VERSION}" \
    --namespace ach-system \
    --set auth.enabled=false \
    --set primary.persistence.size=1Gi \
    --set "global.imageRegistry=${BITNAMI_IMAGE_REGISTRY}" \
    --set "global.security.allowInsecureImages=true" \
    --set "image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/valkey" \
    --set "sentinel.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/valkey-sentinel" \
    --set "metrics.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/redis-exporter" \
    --set "volumePermissions.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/os-shell" \
    --set "kubectl.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/kubectl" \
    --wait --timeout 5m
}

hydrate_dex() {
  echo "[cluster.sh] installing dex @ ${DEX_CHART_VERSION}..."
  # Dex chart is hosted on the dexidp helm repo. Add repo idempotently.
  helm repo add dex https://charts.dexidp.io >/dev/null 2>&1 || true
  helm repo update dex >/dev/null
  # The dex chart expects raw Dex YAML under a top-level `config:` key in
  # values.yaml (see https://github.com/dexidp/helm-charts). Our
  # scripts/dex-config.yaml stays as standalone raw Dex YAML so engineers
  # can also stand it up with `docker run dex serve /etc/dex/config.yaml`
  # (see the comment block at the top of that file). Wrap it for helm here.
  local tmpvals
  tmpvals="$(mktemp)"
  {
    echo "config:"
    sed -e 's/^/  /' scripts/dex-config.yaml
  } > "${tmpvals}"
  helm upgrade --install dex dex/dex \
    --version "${DEX_CHART_VERSION}" \
    --namespace dex-system \
    --values "${tmpvals}" \
    --wait --timeout 3m
  rm -f "${tmpvals}"
}

hydrate_litellm() {
  echo "[cluster.sh] installing upstream litellm (latest pinned chart)..."
  # Pull the litellm-helm OCI chart into a tmpdir and install. No values
  # file yet — uses chart defaults. Phase 11+ will wire test/e2e/values/.
  #
  # LiteLLM bundles bitnami/postgresql + bitnami/redis as subcharts; both
  # hit the same docker.io/bitnami/* pruning that broke our top-level
  # postgres/valkey installs (see hydrate_postgres). Override each
  # subchart's image.repository the same way using the chart-prefix
  # syntax `postgresql.<key>` / `redis.<key>`.
  local tmpdir; tmpdir="$(mktemp -d)"
  trap 'rm -rf "${tmpdir}"' EXIT
  ( cd "${tmpdir}" && helm pull oci://docker.litellm.ai/berriai/litellm-helm --untar )
  helm upgrade --install litellm "${tmpdir}/litellm-helm" \
    --namespace litellm-system \
    --set masterkey=sk-1234 \
    --set "global.imageRegistry=${BITNAMI_IMAGE_REGISTRY}" \
    --set "global.security.allowInsecureImages=true" \
    --set "postgresql.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/postgresql" \
    --set "postgresql.volumePermissions.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/os-shell" \
    --set "postgresql.metrics.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/postgres-exporter" \
    --set "redis.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/redis" \
    --set "redis.sentinel.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/redis-sentinel" \
    --set "redis.metrics.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/redis-exporter" \
    --set "redis.volumePermissions.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/os-shell" \
    --set "redis.kubectl.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/kubectl" \
    --set "redis.sysctl.image.repository=${BITNAMI_IMAGE_REPO_PREFIX}/os-shell" \
    --wait --timeout 4m || {
      echo "[cluster.sh] WARN: litellm helm install failed — continuing (see Phase 11+ for full hydration)" >&2
    }
  rm -rf "${tmpdir}"
  trap - EXIT
}

hydrate_toolhive() {
  echo "[cluster.sh] installing toolhive-operator-crds..."
  helm upgrade --install toolhive-operator-crds \
    oci://ghcr.io/stacklok/toolhive/toolhive-operator-crds \
    --wait --timeout 60s || {
      echo "[cluster.sh] WARN: toolhive CRDs install failed — continuing" >&2
    }

  echo "[cluster.sh] installing toolhive-operator..."
  helm upgrade --install toolhive-operator \
    oci://ghcr.io/stacklok/toolhive/toolhive-operator \
    --namespace toolhive-system \
    --wait --timeout 90s || {
      echo "[cluster.sh] WARN: toolhive operator install failed — continuing" >&2
    }
}

hydrate_all() {
  hydrate_postgres
  hydrate_valkey
  hydrate_dex
  hydrate_litellm
  hydrate_toolhive
}

print_status() {
  # All probes are informational — never fail the status command.
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
  return 0
}

case "${1:-}" in
  up|hydrate|down|keep|status) "cmd_${1}" ;;
  *) usage ;;
esac
