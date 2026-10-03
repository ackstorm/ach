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

# 6. Sandboxed-agent harness identity. Off by default — preserved optional external
# agentSandbox chart infrastructure (SA + sandboxclaims Role), uncoupled from the
# workspace-v1 ACHAgent runtime (only the legacy pruneLegacySandbox cleanup, not this
# chart surface, knows about agent-sandbox GVKs).
out="$(render --set agentSandbox.enabled=true)"
grep -q "name: ach-sandboxed-agent" <<<"$out" || fail "agentSandbox.enabled=true: ServiceAccount/Role missing"
grep -q "sandboxclaims" <<<"$out" || fail "agentSandbox.enabled=true: sandboxclaims rule missing"

# 7. Default runtime Storage: no bucket configured, no broker/CA settings anywhere (contract
# §11 scope reset — removed, not deferred), and the optional credentials env is absent.
out="$(render)"
grep -q "ACH_RUNTIME_BROKER\|ACH_RUNTIME_CA" <<<"$out" && fail "default: unexpected runtime broker/CA settings"
grep -q "ACH_STORAGE_S3_CREDENTIALS_SECRET" <<<"$out" && fail "default: unexpected credentials env with no credentialsSecretName set"
grep -q "name: ACH_STORAGE_S3_BUCKET" <<<"$out" || fail "default: ACH_STORAGE_S3_BUCKET env missing"
grep -A1 "name: ACH_STORAGE_S3_BUCKET" <<<"$out" | grep -q 'value: ""' || fail "default: ACH_STORAGE_S3_BUCKET must default empty"

# 8. Configured runtime Storage S3 — five operator env names/quoted values, no broker/CA, no
# credential key/value injected into the operator (the chart only ever carries the Secret
# NAME, never bytes).
out="$(render --set runtime.storage.s3.bucket=runtime-bucket \
              --set runtime.storage.s3.region=us-east-1 \
              --set runtime.storage.s3.endpointUrl=http://seaweedfs:8333 \
              --set runtime.storage.s3.prefix=tenant/runtime \
              --set runtime.storage.s3.credentialsSecretName=runtime-s3)"
grep -q "ACH_RUNTIME_BROKER\|ACH_RUNTIME_CA" <<<"$out" && fail "configured: unexpected runtime broker/CA settings"
grep -A1 "name: ACH_STORAGE_S3_BUCKET" <<<"$out" | grep -q 'value: "runtime-bucket"' || fail "configured: ACH_STORAGE_S3_BUCKET value wrong"
grep -A1 "name: ACH_STORAGE_S3_REGION" <<<"$out" | grep -q 'value: "us-east-1"' || fail "configured: ACH_STORAGE_S3_REGION value wrong"
grep -A1 "name: ACH_STORAGE_S3_ENDPOINT_URL" <<<"$out" | grep -q 'value: "http://seaweedfs:8333"' || fail "configured: ACH_STORAGE_S3_ENDPOINT_URL value wrong"
grep -A1 "name: ACH_STORAGE_S3_PREFIX" <<<"$out" | grep -q 'value: "tenant/runtime"' || fail "configured: ACH_STORAGE_S3_PREFIX value wrong"
grep -A1 "name: ACH_STORAGE_S3_CREDENTIALS_SECRET" <<<"$out" | grep -q 'value: "runtime-s3"' || fail "configured: ACH_STORAGE_S3_CREDENTIALS_SECRET value wrong"
grep -qE "AWS_ACCESS_KEY_ID|AWS_SECRET_ACCESS_KEY|AWS_SESSION_TOKEN" <<<"$out" && fail "configured: AWS credential keys must never be injected into the operator itself"

echo "helm-render-check OK (8 topologies)"
