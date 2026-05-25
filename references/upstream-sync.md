# Upstream sync: alitellm-operator → ach

Files ported from `/home/jcm/Projects/alitellm-operator/` that were hardened in ACH
and should be sync-back PR'd to alitellm-operator.

## 2026-05-25 (Task 2.1)

| ach file | alitellm file | Fix |
|---|---|---|
| `Dockerfile.devtools` | `Dockerfile.devtools` | Added `SHELL ["/bin/bash", "-o", "pipefail", "-c"]` to abort on curl-pipe failures |
| `scripts/envtest-assets-path.sh` | `scripts/envtest-assets-path.sh` | LOCALBIN now derives from script location, not `$(pwd)` |
| `scripts/dev.sh` | `scripts/dev.sh` | `getent group docker` fallback — skip `--group-add` when docker group absent |
