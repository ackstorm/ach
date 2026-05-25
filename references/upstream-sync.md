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

## 2026-05-25 (Task 4.1) — golangci config

Ported `.golangci.yml` verbatim from alitellm (50+ linters, gosec HIGH).

| ach file | alitellm file | Fix |
|---|---|---|
| `test/e2e/e2e_test.go` (kubebuilder scaffold) | n/a — alitellm doesn't carry this scaffold | Added `#nosec G101` directive with justification on `tokenRequestRawString` — gosec G101 false positive (it's a TokenRequest API JSON template, not a credential). If alitellm re-introduces kubebuilder e2e scaffold, same fix applies. |

## 2026-05-25 (Task 4.3) — Pre-push gate pipeline

Ported `scripts/pre-push-check.sh` (15-gate pipeline), `scripts/install-hooks.sh`,
`scripts/govulncheck-gate.sh`, and `references/security/govulncheck-acknowledged.md`
verbatim from alitellm with sed renames (`alitellm-operator` → `ach`,
`litellm-devtools` → `ach-devtools`). Gate 1 (gitleaks) / Gate 2 (trufflehog) /
Gate 13 (govulncheck) / Gate 15 (SPDX) all PASS on bare scaffolding.

| ach file | alitellm file | Fix |
|---|---|---|
| `scripts/pre-push-check.sh` (gate 3 comment) | same | Re-worded the 2MB threshold rationale — removed mention of `spec/litellm_api.json` (alitellm-only file we don't bundle); kept the 2MB threshold as future-proofing. |
