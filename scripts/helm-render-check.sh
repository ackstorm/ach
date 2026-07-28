#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Render-smoke the documented non-default Helm topologies. Catches
# template regressions in paths no e2e cluster exercises (G16 standalone
# content-service, gateway disabled, ingress enabled). Render-only — no
# cluster, no kubectl.
set -euo pipefail

CHART="deploy/helm/ach"
fail() { echo "helm-render-check FAIL: $1" >&2; exit 1; }
render() { helm template ach "$CHART" "$@" 2>&1 || fail "helm template $* exited non-zero"; }

# 1. Default topology: gateway present, no standalone CS Deployment, no Ingress.
out="$(render)"
grep -q "name: ach-gateway" <<<"$out" || fail "default: ach-gateway missing"
grep -q "kind: Ingress" <<<"$out" && fail "default: unexpected Ingress"

# 2. Gateway disabled.
out="$(render --set gateway.enabled=false)"
grep -q "name: ach-gateway" <<<"$out" && fail "gateway.enabled=false: ach-gateway still rendered"

# 3. Ingress enabled.
out="$(render --set ingress.enabled=true)"
grep -q "kind: Ingress" <<<"$out" || fail "ingress.enabled=true: no Ingress rendered"

# 4. G16 standalone content-service (requires RWX cache).
out="$(render --set contentService.standalone=true \
              --set operator.cache.accessMode=ReadWriteMany \
              --set operator.cache.storageClassName=rwx-test)"
grep -q "name: ach-content-service" <<<"$out" || fail "standalone: ach-content-service Deployment missing"

# 5. Grafana dashboards enabled. Off by default, so topologies 1-4 never parse
# past the `if` guard — a break in the template body would reach users untested.
out="$(render --set metrics.dashboards.enabled=true)"
grep -q "name: ach-dashboard-" <<<"$out" || fail "dashboards.enabled=true: no dashboard ConfigMap rendered"
# A label key repeated in metrics.dashboards.labels must merge, not emit a
# duplicate mapping key (which aborts the whole release, not just this object).
render --set metrics.dashboards.enabled=true \
       --set metrics.dashboards.labels.grafana_dashboard=1 >/dev/null

echo "helm-render-check OK (5 topologies)"
