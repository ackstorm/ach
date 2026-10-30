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

# 4b. Classifier: off by default; on, its secrets come only from its own Secret.
grep -q "name: ach-classifier" <<<"$(render)" && fail "default: unexpected ach-classifier"
out="$(render --set enterprise.enabled=true --set classifier.enabled=true --set classifier.secretName=cls \
              --set classifier.clickhouseUrl=http://ch:8123 --set classifier.modelUrl=http://gw/v1)"
grep -q "name: ach-classifier" <<<"$out" || fail "classifier.enabled=true: ach-classifier Deployment missing"
grep -q "ACH_CLASSIFIER_LANGFUSE_URL" <<<"$out" && fail "classifier: Langfuse URL rendered without langfuseUrl (must be a dry run)"
# 4c. Classifier taxonomy: built-in unless set; set, a ConfigMap, its env and a restart checksum.
grep -q "ach-classifier-taxonomy" <<<"$out" && fail "classifier: taxonomy ConfigMap without classifier.taxonomy"
out="$(render --set enterprise.enabled=true --set classifier.enabled=true --set classifier.secretName=cls \
              --set classifier.clickhouseUrl=http://ch:8123 --set classifier.modelUrl=http://gw/v1 \
              --set 'classifier.taxonomy.categories[0].id=ops.fleet' \
              --set 'classifier.taxonomy.categories[0].fits=Routes')"
grep -q "name: ach-classifier-taxonomy" <<<"$out" || fail "classifier.taxonomy: ConfigMap missing"
grep -q "ACH_CLASSIFIER_TAXONOMY_FILE" <<<"$out" || fail "classifier.taxonomy: env missing"
grep -q "ach.ackstorm.ai/taxonomy-checksum" <<<"$out" || fail "classifier.taxonomy: restart checksum missing"

# 5. Grafana dashboards enabled. Off by default, so topologies 1-4 never parse
# past the `if` guard — a break in the template body would reach users untested.
out="$(render --set metrics.dashboards.enabled=true)"
grep -q "name: ach-dashboard-" <<<"$out" || fail "dashboards.enabled=true: no dashboard ConfigMap rendered"
# A label key repeated in metrics.dashboards.labels must merge, not emit a
# duplicate mapping key (which aborts the whole release, not just this object).
render --set metrics.dashboards.enabled=true \
       --set metrics.dashboards.labels.grafana_dashboard=1 >/dev/null

# 6. Enterprise: off by default, the flag reaches platform-api, and Enterprise
# values render nothing while it is off.
grep -A1 "name: ACH_ENTERPRISE" <<<"$(render)" | grep -q 'value: "false"' || fail "default: ACH_ENTERPRISE must be \"false\""
grep -A1 "name: ACH_ENTERPRISE" <<<"$(render --set enterprise.enabled=true)" | grep -q 'value: "true"' || fail "enterprise.enabled=true: ACH_ENTERPRISE not \"true\""
out="$(render --set enterprise.enabled=true --set classifier.enabled=true --set classifier.secretName=cls \
              --set classifier.clickhouseUrl=http://ch:8123 --set classifier.modelUrl=http://gw/v1)"
awk -v RS='\n---\n' '/kind: Deployment/ && /name: ach-classifier/' <<<"$out" \
  | grep -A1 "name: ACH_ENTERPRISE" | grep -q 'value: "true"' || fail "enterprise.enabled=true: classifier Deployment lacks ACH_ENTERPRISE \"true\""
out="$(render --set classifier.enabled=true --set classifier.secretName=cls \
              --set classifier.clickhouseUrl=http://ch:8123 --set classifier.modelUrl=http://gw/v1 \
              --set 'classifier.taxonomy.categories[0].id=ops.fleet' --set 'classifier.taxonomy.categories[0].fits=Routes' \
              --set platformApi.observability.clickhouseUrl=http://ch:8123 --set platformApi.observability.secretName=obs \
              --set 'platformApi.observability.projectIds[0]=p1' \
              --set platformApi.observability.thanosUrl=http://thanos)"
grep -qE "name: ach-classifier|ACH_CLASSIFIER_TAXONOMY_FILE|ACH_OBSERVABILITY_(THANOS|CLICKHOUSE)_URL" <<<"$out" \
  && fail "enterprise.enabled=false: Enterprise values rendered"
out="$(render --set enterprise.enabled=true \
              --set platformApi.observability.clickhouseUrl=http://ch:8123 --set platformApi.observability.secretName=obs \
              --set 'platformApi.observability.projectIds[0]=p1')"
grep -q "name: ACH_OBSERVABILITY_CLICKHOUSE_URL" <<<"$out" \
  || fail "enterprise.enabled=true: ACH_OBSERVABILITY_CLICKHOUSE_URL not rendered"

# 7. Key exchange: platform-api may call TokenReview and nothing else.
grep -q "name: system:auth-delegator" <<<"$(render --show-only templates/platform-api-rbac.yaml)" \
  || fail "default: platform-api lacks the system:auth-delegator binding (TokenReview)"
grep -A1 "name: ACH_KEY_EXCHANGE_TOKEN_TTL" <<<"$(render)" | grep -q 'value: "15m"' \
  || fail "default: ACH_KEY_EXCHANGE_TOKEN_TTL must be \"15m\""

echo "helm-render-check OK (7 topologies)"
