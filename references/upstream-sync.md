# Upstream sync: alitellm-operator → ach

Files ported from `/home/jcm/Projects/alitellm-operator/` that were hardened in ACH
and should be sync-back PR'd to alitellm-operator.

## 2026-05-25 (Task 2.1)

| ach file | alitellm file | Fix |
|---|---|---|
| `Dockerfile.devtools` | `Dockerfile.devtools` | Added `SHELL ["/bin/bash", "-o", "pipefail", "-c"]` to abort on curl-pipe failures |
| `scripts/envtest-assets-path.sh` | `scripts/envtest-assets-path.sh` | LOCALBIN now derives from script location, not `$(pwd)` (removed 2026-07-13 — zero references) |
| `scripts/dev.sh` | `scripts/dev.sh` | `getent group docker` fallback — skip `--group-add` when docker group absent |

## 2026-05-25 (Task 3.1) — Makefile port

Ported `Makefile` and `docs/Makefile` verbatim from alitellm with sed renames
(`alitellm-operator` → `ach`, `litellm-devtools` → `ach-devtools`,
`litellm.ackstorm.ai` → `ach.ackstorm.ai`). (The e2e capture backend `ach-mock`
is original ackstorm code — `test/e2e/mock/`, three modes `model|mcp|a2a` behind
the real LiteLLM — not a graft, so it is not part of this rename ledger.)
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
| `scripts/pre-push-check.sh` (gate 14 restore) | same | **Real bug**: alitellm uses `SAVED=$(cat go.mod)` + `printf '%s' "$SAVED" > go.mod` to snapshot and restore on tidy drift. Both `$(cat)` and `printf '%s'` strip trailing newlines, so the restore silently corrupts `go.mod` and the next pre-push run sees a phantom `No newline at end of file` diff. ACH fix: snapshot via `cp` into a `mktemp -d` (cleaned via `trap EXIT`), restore via `cp` — byte-for-byte identical. Sync-back PR target. |
| `scripts/pre-push-check.sh` (gate 11 self-match) | same | **Real bug (low)**: gate 11 (urgent TODO markers) `git grep`s the whole tree, which matches the gate script's own descriptive comments naming `DO-NOT-COMMIT` literal. ACH fix: exclude `scripts/pre-push-check.sh` from gate 11 via `':!scripts/pre-push-check.sh'`. Sync-back PR target. |

## 2026-05-25 (Task 5.1) — CRD scaffold side effects

Ran `kubebuilder create api` for 6 CRDs (Environment, Plugin, PluginMarketplace,
Artifact, Prompt, BackendIdentityPolicy). Side effects worth flagging upstream.

| ach file | alitellm file | Follow-up |
|---|---|---|
| `go.mod` (`golang.org/x/net v0.55.0`) | `go.mod` (likely lower) | **Security**: kubebuilder v4.4.0 pulls `controller-runtime v0.24.1` → `golang.org/x/net v0.49.0`, which has open HIGH advisories `GO-2026-5026` + `GO-2026-4918`. Bumped to `v0.55.0` (above both fix versions). Alitellm should run `go get golang.org/x/net@v0.55.0` to clear the same advisories. Sync-back PR target. |
| (kubebuilder v4 quirk) | n/a | `kubebuilder create api` requires `cmd/main.go` to exist at repo root or it refuses to run (cmd-plugin assertion). ACH's single-binary cobra layout puts the entrypoint at `cmd/ach/main.go` instead. Workaround: temporarily stub `cmd/main.go` while running `create api`, then delete. Document for future API additions. Not alitellm-relevant (alitellm uses cmd/main.go canonical layout). |

## ach-old issues (Task 6.1) — overlay port

Lifted `config/{deployments,dev-postgres,e2e,secrets,storage}/` from ach-old.
Issues found in ach-old's overlays (relative to the alitellm scaffold pattern
and to ACH's current single-binary direction). These are ach-old-side; sync-back
target is ach-old, NOT alitellm.

| ach-old file | ach file | Issue / Fix |
|---|---|---|
| `config/deployments/platform-api_deployment.yaml` | `config/deployments/platform-api_deployment.yaml` | **Adapted**: ach-old ships `image: ach-platform-api:latest` + `command: [/platform-api]` — a separate-image-per-service layout. ACH is single-binary (`ach` + cobra subcommand). Rewrote to `image: ghcr.io/ackstorm/ach:dev` + `args: [platform-api]`, removed `command:` so the Dockerfile ENTRYPOINT `/ach` runs. |
| `config/deployments/forwarder_deployment.yaml` | `config/deployments/forwarder_deployment.yaml` | Same single-binary rewrite as platform-api: `image: ach-forwarder:latest` → `ghcr.io/ackstorm/ach:dev`, drop `command: [/forwarder]`, add `args: [forwarder]`. |
| `config/deployments/kustomization.yaml` comment | same | Comment claims content-service is "co-located with the operator container in config/manager/manager.yaml". True in ach-old; in ACH, `config/manager/manager.yaml` is still the kubebuilder scaffold (single container, name `manager`, image `controller:latest`). The co-located 2-container Pod (operator + content-service + cache PVC) is deferred until manager.yaml is ported. **Follow-up plan needed**: port ach-old's `config/manager/manager.yaml` (init container `migrations`, container `manager` with single-binary args `[operator]`, container `content-service` with args `[content-service]`, shared `ach-operator-cache` PVC, `strategy: Recreate` for RWO). |
| `config/default/kustomization.yaml` (kubebuilder default) | same (ACH inherits scaffold) | **Quirk worth flagging**: ach-old explicitly REMOVED kubebuilder's default `namePrefix: ach-` because source manifest names already start with `ach-` (e.g. `ach-platform-api`, `ach-forwarder`, `ach-operator`), so the prefix produces double-prefixed `ach-ach-*` names. ACH currently still carries `namePrefix: ach-` from the kubebuilder scaffold — the build is functional but `kustomize build config/default` emits `ach-ach-platform-api`, `ach-ach-forwarder`. Decide later: either drop `namePrefix` (ach-old strategy) or rename source manifests to bare `platform-api` / `forwarder` and let `namePrefix` add `ach-`. **Out of scope for 6.1** — affects rbac + manager too. |
| `config/e2e/kustomization.yaml` Patch 1 target | same | Patch 1 targets `Deployment name: ach-operator` (the ach-old operator Deployment in `config/manager/manager.yaml`). ACH's current `config/manager/manager.yaml` uses kubebuilder name `controller-manager` (becomes `ach-controller-manager` after namePrefix). The patch silently no-ops today — `kustomize build config/e2e` succeeds but `ACH_ORPHAN_CLEANUP_INTERVAL` env var is NOT injected. **Follow-up**: when ach-old's manager.yaml is ported (see row above), retarget Patch 1 to whatever the operator Deployment is named. |
| `config/e2e/kustomization.yaml` images block | same | **Adapted**: ach-old retags `ach-operator → ach-operator:e2e`. Changed to `ghcr.io/ackstorm/ach → ghcr.io/ackstorm/ach:e2e` for single-binary alignment. |
| `config/storage/operator_cache_pvc.yaml` | same (copied as-is) | Cache PVC `ach-operator-cache` is RWO and meant to be mounted by both the operator container and the co-located content-service container in `config/manager/manager.yaml`. Until manager.yaml is ported, this PVC is unused. Intentionally NOT wired into `config/default` resources for the same reason. |
| `config/secrets/credential_hash_pepper_secret.yaml` | same | Ships `REPLACE-ME-WITH-RANDOM-32-BYTES-FROM-OPENSSL-RAND-BASE64-32`. ach-old comment says the operator binary refuses to start when this placeholder is present (`strings.HasPrefix(pepper, "REPLACE-ME-WITH-RANDOM-")`). Same contract carries forward to ACH single-binary `operator` subcommand. Not a bug — intentional fail-closed. |

## 2026-05-25 (Task 9.1) — Envtest runner

Ported `scripts/run-envtest-packages.sh` verbatim from alitellm with sed renames.
Adjusted Makefile envtest-race / envtest-fast / unit targets to drop the
`./internal/toolhive/...` package (ach has no toolhive integration; only
`./internal/controller/...`). Single-package envtest-fast run on the
auto-generated `internal/controller/ach/suite_test.go` PASS (~6s).

## 2026-05-25 (Task 9.2) — e2e suite scaffold

Replaced kubebuilder's e2e scaffold (`test/e2e/e2e_suite_test.go`,
`test/e2e/e2e_test.go`, `test/utils/utils.go`) with alitellm's pattern
(`test/e2e/suite_test.go`, `test/e2e/utils/{forensics,secret_cr}.go`).
Rewrote `forensics.go` for ACH context: operator deploy name `ach-operator`
in namespace `ach-system`, CRs list updated to ACH kinds
(environments, plugins, pluginmarketplaces, artifacts, prompts,
backendidentitypolicies), prefix `e2e-` instead of `tier2-`. All files
gated with `//go:build e2e`. `go build -tags=e2e ./test/e2e/...` PASS.
(`test/e2e/utils` removed 2026-07-13 — zero importers; `cluster.sh` dumps
forensics inline.)

## 2026-05-25 (Task 10.1) — Cluster scripts

Ported `scripts/{cluster,collect-forensics}.sh`, `scripts/kind-config.yaml`
from alitellm; lifted `scripts/dex-config.yaml` from ach-old.

| ach file | Source | Adaptation |
|---|---|---|
| `scripts/cluster.sh` | alitellm | **Rewritten** — alitellm cluster.sh depends on `test/e2e/{values,fixtures,charts}/` and `deploy/helm/ach/` that don't yet exist in ach (those land Phase 11+). Replaced `install_{litellm,toolhive,mocks,operator}` with `hydrate_{postgres,valkey,dex,litellm,toolhive}` — each helm-installs an external runtime dep. Postgres + Valkey from bitnami OCI charts (pinned). Dex from dexidp helm repo (uses `scripts/dex-config.yaml`). LiteLLM from BerriAI OCI chart (pulled to tmpdir, untar, install). ToolHive from stacklok OCI charts. Hydration wrapped in `|| true` warn-then-continue (best-effort until Phase 11+ supplies values files). Cluster name `ach-e2e`. |
| `scripts/cluster.sh print_status` | alitellm | **Fixed bug**: alitellm's `print_status` runs `kubectl get ns default litellm-system ...` which exits non-zero if any listed namespace doesn't exist (e.g. before hydration). Combined with `set -e`, status aborts with `Error 1`. Fix: add `set +e` at top of `print_status` + `return 0` at end — status is purely informational, must never fail. Sync-back PR candidate. |
| `scripts/collect-forensics.sh` | alitellm | Adapted for ach: operator deploy `ach-operator` in `ach-system`, added platform-api + forwarder deploy log dumps, CR list updated to ach kinds, event collection extended to include `ach-system` namespace. (removed 2026-07-13 — zero references) |
| `scripts/kind-config.yaml` | alitellm | Cluster name `alitellm-operator-test` → `ach-e2e`. |
| `scripts/dex-config.yaml` | ach-old | Copied verbatim — OIDC config for `ach-platform-api`. |

Verified: `make cluster-up` (empty cluster) → `make cluster-status` →
`make cluster-down` round trip PASS. Full hydration execution (postgres,
valkey, dex, litellm, toolhive helm installs) deferred to Phase 16 final
verification — hydrate_* functions are syntactically valid (`bash -n` PASS).

## 2026-05-25 (Task 11.1) — Helm chart port

Ported `deploy/helm/alitellm-operator/` → `deploy/helm/ach/` with sed renames.
Updated `Chart.yaml` version to `0.0.1` (ach baseline). Rewrote `values.yaml`
for the single-binary multi-component design: one `image:` reference plus
per-mode toggles (`operator`, `platformApi`, `forwarder`, `contentService`,
`migrate`), each carrying `args:` to select the cobra subcommand. External
postgres + redis assumed (`{postgres,redis}.external: true`). The `crds.yaml`
+ `NOTES.txt` templates were ported as-is from alitellm; `scripts/helm-inject-crd-annotation.py`
was ported with sed renames. The grafted `install.yaml` veneer template and
its generator `scripts/kustomize-to-helm.sh` were **retired** once the chart
moved to hand-authored per-mode templates (see note below).

**Per-mode Deployments authored; kustomize→Helm veneer retired**: the deferred
follow-up landed. The chart now ships hand-authored per-mode templates
(`operator-deployment.yaml`, `platform-api-deployment.yaml`,
`forwarder-deployment.yaml`, `content-service-deployment.yaml`,
`migrate-job.yaml`, plus per-mode RBAC), each gated by its values.yaml toggle.
The grafted single-Deployment `install.yaml` veneer and `kustomize-to-helm.sh`
were removed: the chart no longer derives any Deployment from kustomize, so
`make helm-sync` now only syncs CRDs into `deploy/helm/ach/crd-sources/` (via
`scripts/helm-inject-crd-annotation.py`). `make build-installer` remains as a
standalone kustomize install bundle (`kubectl apply -f dist/install.yaml`),
independent of the chart.

| ach file | alitellm file | Fix / Adaptation |
|---|---|---|
| `Makefile` (build-installer) | same | Originally fed the now-retired `kustomize-to-helm.sh` veneer; `build-installer` is now a standalone kustomize install bundle (`kubectl apply -f dist/install.yaml`). The `kustomize edit set image controller=controller:latest` pin is a no-op (config/manager already pins `ghcr.io/ackstorm/ach`), kept for kubebuilder-convention parity. |
| `config/manager/manager.yaml` (command/args) | n/a (kubebuilder default) | Adapted to single-binary cobra: `command: [/manager]` → `command: [/ach]`, prepended `operator` to `args`. The Dockerfile ENTRYPOINT already produces `/ach`; this aligns the kustomize source with the actual binary path. |
| `deploy/helm/ach/values.yaml` | `deploy/helm/alitellm-operator/values.yaml` | Rewritten: dropped `safetyRelistInterval` (alitellm-specific knob, has no ACH analog); added `operator`, `platformApi`, `forwarder`, `contentService`, `migrate` per-mode blocks with `{enabled, replicas, resources, args}` shape; added `postgres.external` + `redis.external` flags; kept `installCRDs`, `image`, `watchNamespace`, `toolhive`, `metrics.serviceMonitor`, `extraEnv` from alitellm verbatim. |
| `deploy/helm/ach/Chart.yaml` | same | Reset version `0.4.7` → `0.0.1` and description from "LiteLLM operator …" to "ACH — Agent Capability Hub. Kubernetes operator + platform API + forwarder + content service for declarative agent configuration management." |

## 2026-05-25 (Task 11.2) — deploy/kustomize snapshot

Ported `deploy/kustomize/` + `scripts/render-deploy-kustomize-rbac.sh` from
alitellm with sed renames. Bumped image newTag to `v0.0.1`.

| ach file | alitellm file | Fix / Adaptation |
|---|---|---|
| `config/rbac/toolhive_clusterrole.yaml` | same | **Added new file**: ACH's RBAC dir didn't carry the ToolHive ClusterRole. The render script's `RBAC_SOURCES` list expects it (operator-runtime subset). Ported alitellm's content with SA name rewrite (`alitellm-operator` → `ach`) and ClusterRole/ClusterRoleBinding rename (`alitellm-operator-toolhive-reader` → `ach-toolhive-reader`). |
| `deploy/kustomize/kustomization.yaml` | same | Image newTag `v0.4.7` → `v0.0.1` to match Chart.yaml baseline. |

## 2026-05-25 (Phase 14 — release tooling)

| ach file | alitellm file | Fix / Adaptation |
|---|---|---|
| `.goreleaser.yml`, `.goreleaser.prerelease.yml`, `.goreleaser.snapshot.yml` | same | **Rewritten for single-binary**: alitellm carried a single `manager` build pointing at `./cmd/main.go` producing `alitellm-operator`. ach builds `./cmd/ach` → `ach`. Added `darwin` to `goos:` so the CLI ships for macOS too (alitellm was Linux-only because it's a server). Switched ldflag from `-X github.com/ackstorm/alitellm-operator/internal/identity.Version=...` to `-X github.com/ackstorm/ach/cmd/ach/cmd.Version=...` to match the actual `Version` symbol in `cmd/ach/cmd/root.go`. Dropped `-X main.{version,commit,date,buildType}` because the ach root.go does not consume them. Dockers/manifests rewritten to `ghcr.io/ackstorm/ach:<version>` (no `{{ .Env.GITHUB_REPOSITORY_OWNER }}` indirection — owner is fixed) and `--build-arg=VERSION=...` added since the goreleaser Dockerfile now COPYs the prebuilt binary (the build-arg is consumed for label injection only). |
| `Dockerfile.goreleaser` | same | Renamed `alitellm-operator` → `ach`; ENTRYPOINT `/ach`. |
| `.github/workflows/release.yml` | same | Sed rename `alitellm-operator` → `ach`. Paths in the bot-bump step (`deploy/helm/ach/{Chart,values}.yaml`, kustomize files) now match ach's structure verbatim (no further adaptation needed — kubebuilder-default layout). |
| `.github/workflows/nightly.yml` | same | Sed rename `alitellm-operator` → `ach`. |
| `Makefile` (`bump` target) | n/a | Already present in ach (added during Phase 11). Confirmed matches release.yml expectations. |

## 2026-05-25 (Phase 15 — project docs)

| ach file | alitellm file | Fix / Adaptation |
|---|---|---|
| `README.md` | n/a | Replaced kubebuilder stub with concise ach intro + quick-link table. |
| `CHANGELOG.md` | n/a | Bootstrap entry for v0.0.1 scaffolding. |
| `SECURITY.md`, `CONTRIBUTING.md`, `PUBLISH.md`, `CLAUDE.md` | same | Sed rename `alitellm-operator` → `ach`. CLAUDE.md adapted to ach domain (waiters expanded to include `wait-postgres`, `wait-redis`, `wait-dex`, `wait-platform-api`, `wait-forwarder`, `wait-content-service`; single-binary `ach <mode>` invocation pattern noted). |
| `MAINTAINERS.md` | n/a | Single maintainer entry (jcm). |
| `ROADMAP.md` | `ach-old/.planning/ROADMAP.md` | Copied verbatim from ach-old plan numbering. **Doc-polish TODO**: trim ach-old plan-numbering / phase content to focus on user-visible roadmap items; the current copy still references internal plan IDs (e.g. "Phase 1: Foundation"), which are not meaningful to external readers. |

## 2026-05-25 (TODO item 1 — post-bootstrap hardening, branch `fix/post-bootstrap-hardening`)

| ach file | alitellm file | Fix / Sync-back |
|---|---|---|
| `scripts/cluster.sh` (hydrate_postgres, hydrate_valkey, hydrate_litellm) | n/a — alitellm cluster.sh does NOT install bitnami postgres/valkey/litellm | Bitnami pruned `docker.io/bitnami/*` pinned tags in 2025; moved snapshots to `docker.io/bitnamilegacy/*`. Override `global.imageRegistry` + each sub-image's `repository` to redirect, and set `global.security.allowInsecureImages=true` to bypass the chart's hard-coded "approved containers" guard. **No alitellm sync needed** — alitellm doesn't hydrate these. If alitellm later adds postgres/valkey/litellm helm hydration, apply the same workaround. |
| `scripts/cluster.sh` (hydrate_dex) | n/a — alitellm doesn't install dex | The dexidp helm chart expects raw Dex YAML under a top-level `config:` key in values.yaml. The script was passing `scripts/dex-config.yaml` (raw Dex YAML) as `--values` directly, producing an "invalid Config: no issuer specified" CrashLoopBackOff. Wrap the file in-place at install time so `scripts/dex-config.yaml` can stay docker-run-compatible. No alitellm sync. |
| `scripts/dev.sh` + `docs/Makefile` | `scripts/dev.sh` + `docs/Makefile` (same paths) | `./scripts/dev.sh make docs-build` silently produced empty `site/` because the inner `docker run -v $(abspath docs/..):/docs` resolved `$(abspath)` to `/workspace` (cwd inside devtools) but used the host docker socket. Export `HOST_PWD=$WORKSPACE` from dev.sh; honor it in `docs/Makefile` via `DOCS_HOST_ROOT ?= $(if $(HOST_PWD),$(HOST_PWD),$(abspath ...))`. **Sync-back PR target** — see `SYNC-FROM-ACH/05-docs-build-nested-mount.md` in alitellm. |
| `.golangci.yml` | n/a — cosmetic only | Annotated exclude-rules with single-line justifications, dropped redundant `lll` entry from the SA1019 rule. No behavior change. No alitellm sync. |

