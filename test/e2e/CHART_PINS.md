# E2E Chart Pins

These versions are consumed by `scripts/cluster.sh` through `test/e2e/values`.

| Component | Chart | Version |
| --- | --- | --- |
| LiteLLM | `oci://docker.litellm.ai/berriai/litellm-helm` | `1.84.0` |
| ToolHive CRDs | `oci://ghcr.io/stacklok/toolhive/toolhive-operator-crds` | `0.0.55` |
| ToolHive operator | `oci://ghcr.io/stacklok/toolhive/toolhive-operator` | `0.5.5` |

LiteLLM image is pinned separately to `ghcr.io/berriai/litellm-database:v1.83.10-stable`.
