# Upstream sync: alitellm-operator → ach

Files ported from `/home/jcm/Projects/alitellm-operator/` that were hardened in ACH
and should be sync-back PR'd to alitellm-operator.

## 2026-05-25 (Task 2.1)

| ach file | alitellm file | Fix |
|---|---|---|
| `Dockerfile.devtools` | `Dockerfile.devtools` | Added `SHELL ["/bin/bash", "-o", "pipefail", "-c"]` to abort on curl-pipe failures |
| `scripts/envtest-assets-path.sh` | `scripts/envtest-assets-path.sh` | LOCALBIN now derives from script location, not `$(pwd)` |
| `scripts/dev.sh` | `scripts/dev.sh` | `getent group docker` fallback — skip `--group-add` when docker group absent |

## 2026-05-25 (Task 3.1) — Makefile port

Ported `Makefile` and `docs/Makefile` verbatim from alitellm with sed renames
(`alitellm-operator` → `ach`, `litellm-devtools` → `ach-devtools`,
`litellm.ackstorm.ai` → `ach.ackstorm.ai`, `litellm-mock` → `ach-mock`).
Added `VERSION ?= dev`, `IMG ?= ghcr.io/ackstorm/ach:$(VERSION)`, and `MODES :=
operator platform-api forwarder content-service migrate` for ach's single-binary
multi-mode service model. Added 6 new waiter targets: `wait-postgres`,
`wait-redis`, `wait-dex`, `wait-platform-api`, `wait-forwarder`,
`wait-content-service`.

| ach file | alitellm file | Follow-up needed |
|---|---|---|
| `Makefile` (lines 53–57) | `Makefile` (lines 47–51) | Comment about `verification/` references "Phase 0 spike (plan 01-01)" — that's alitellm's plan numbering. Re-word for ACH context in a later doc-pass; harmless build-wise. |
| `Makefile` (`wait-litellm`, `pf-litellm`, `logs-litellm`) | same | Refer to upstream BerriAI LiteLLM Deployment in `litellm-system` namespace. Correct for ACH (which also installs litellm-helm), but may want renames to `wait-litellm-upstream` for clarity in a later pass. |
