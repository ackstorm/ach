#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Verify that dashboard queries track ACH's normalized Prometheus metric names.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
dashboards=("$repo"/deploy/helm/ach/dashboards/*.json)

for dashboard in "${dashboards[@]}"; do
  jq empty "$dashboard"
done

# Bare metric families from the pre-v0.6.22 naming scheme must not reappear.
obsolete='(?<![A-Za-z0-9_])(environment_available|forwarder_[A-Za-z0-9_]+|content_service_[A-Za-z0-9_]+|platform_api_[A-Za-z0-9_]+|key_resolution_cache_[A-Za-z0-9_]+|litellm_unreachable_total|operator_external_ref_refresh_total|router_[A-Za-z0-9_]+|channel_inbound_events_total|engine_[A-Za-z0-9_]+|memory_degraded_total)'
# grep -P, not ripgrep: this runs as `make qa-dashboards` inside the devtools
# container, which ships no rg. GNU grep's -P is PCRE, so the lookbehind holds.
if grep -Pn "$obsolete" "${dashboards[@]}"; then
  echo "obsolete bare metric reference found" >&2
  exit 1
fi

required=(
  ach_environment_available
  ach_forwarder_requests_total
  ach_content_service_requests_total
  ach_platform_api_hydrate_duration_seconds_bucket
  ach_key_resolution_cache_hits_total
  ach_litellm_unreachable_total
  ach_operator_external_ref_refresh_total
  ach_agent_router_backpressure_rejects_total
  ach_agent_channel_inbound_events_total
  ach_agent_engine_launch_failures_total
  ach_agent_memory_degraded_total
)
for metric in "${required[@]}"; do
  grep -qF "$metric" "${dashboards[@]}" || {
    echo "expected metric reference missing: $metric" >&2
    exit 1
  }
done

echo "grafana metric-name check OK (${#dashboards[@]} dashboards)"
