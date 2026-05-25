# ACH Bootstrap Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Bootstrap fresh `/home/jcm/Projects/ach/` repo with alitellm-operator-grade scaffolding (kubebuilder v4 multigroup operator + multi-binary services + full release/test/docs pipeline). NO domain logic — only the scaffold.

**Architecture:** Mirror `/home/jcm/Projects/alitellm-operator/` shape. Where ach-old and alitellm-operator differ on scaffolding, **alitellm wins** (per user decision). Domain code lift from `/home/jcm/Projects/ach-old/` is a separate follow-up plan; this plan only stubs the API kinds and binary skeletons so the scaffold compiles green.

**Tech Stack:**
- Go 1.26 + toolchain go1.26.3 (alitellm-aligned, NOT ach-old's 1.23)
- kubebuilder v4.4.0, controller-runtime v0.19.4, k8s 1.31.0
- **Single binary `ach` with cobra subcommands** (operator, platform-api, forwarder, content-service, migrate). Bare `ach` = CLI (Phase 6 surface).
- spf13/cobra v1.x + spf13/viper (if needed for config layering)
- Ginkgo v2 + gomega for envtest + e2e (both projects agree)
- stdlib testing for pure-unit
- golangci-lint v1.62.2 (50+ linters), gitleaks, govulncheck
- distroless/static:nonroot runtime image (single, multi-arch)
- Helm v3 chart (kustomize as source of truth, synced via `make helm-sync`) — N Deployments share the same image, differ only by `args: ["<subcommand>"]`
- mkdocs-material + mike (versioned docs)
- goreleaser v2 (cross-arch amd64+arm64+darwin, cosign keyless OIDC, cyclonedx SBOM) — **1 build, 1 image, 1 manifest list**
- kind v0.25.0 + helm v3.16.3 for local UAT
- All Go/kubectl/kind/helm invocations through `./scripts/dev.sh` container — host has no Go

**Source-of-truth paths (read-only references throughout this plan):**
- alitellm-operator: `/home/jcm/Projects/alitellm-operator/` — scaffolding template
- ach-old: `/home/jcm/Projects/ach-old/` — domain shape (CRD kinds, binary names, kustomize overlays specific to ach)

**Working directory:** `/home/jcm/Projects/ach/` (already a git repo, currently empty except `.claude/` and `docs/plans/`).

**Out of scope (future plans):**
- Reconciler business logic (Environment/Plugin/etc.)
- Platform API endpoint implementations
- DB migration content (only the migrate binary skeleton + migrations dir)
- Real e2e tests with hydrated services
- Goreleaser smoke release

**Definition of done for this plan:**
- `./scripts/dev.sh make help` lists 60+ targets
- `./scripts/dev.sh make fmt vet build` green (produces single `bin/ach`)
- `./scripts/dev.sh make unit` green (zero unit tests, but framework wired)
- `./scripts/dev.sh make envtest-fast` green (zero envtest, but suite scaffold compiles)
- `./scripts/dev.sh make lint` green
- `make pre-push` green on a clean working tree
- `make cluster-up` brings up kind, `make cluster-down` tears it down
- `make docs-build` produces a site/ directory
- Single image builds via `make docker-build IMG=ghcr.io/ackstorm/ach:dev`
- `bin/ach`, `bin/ach operator --help`, `bin/ach platform-api --help`, `bin/ach forwarder --help`, `bin/ach content-service --help`, `bin/ach migrate --help` all print mode-specific help
- CI workflow runs green on a no-op PR
- Helm chart lints via `helm lint deploy/helm/ach`

---

## Phase 1 — Foundation

### Task 1.1: Initialize go.mod and kubebuilder PROJECT

**Files:**
- Create: `go.mod`
- Create: `PROJECT`
- Create: `.gitignore`
- Create: `LICENSE` (Apache-2.0)
- Create: `NOTICE`

**Step 1:** From `/home/jcm/Projects/ach/`, run kubebuilder init in the devtools container. Since we don't have devtools image yet, use a one-shot docker run.

```bash
docker run --rm -v "$PWD:/workspace" -w /workspace -u "$(id -u):$(id -g)" \
  golang:1.26-bookworm bash -c '
    set -e
    go env -w GOFLAGS=-mod=mod
    curl -L -o /tmp/kb https://github.com/kubernetes-sigs/kubebuilder/releases/download/v4.4.0/kubebuilder_linux_amd64
    chmod +x /tmp/kb
    /tmp/kb init --domain ackstorm.ai --repo github.com/ackstorm/ach --project-name ach --plugins go.kubebuilder.io/v4
  '
```

This creates: `PROJECT`, `go.mod`, `go.sum`, `main.go` (we will delete and replace), `Makefile` (we will replace), `Dockerfile` (we will replace), `cmd/main.go`, `api/`, `internal/`, `config/*` overlays, `hack/boilerplate.go.txt`, `.dockerignore`, `.gitignore`, `.golangci.yml`, `README.md`.

**Step 2:** Edit `PROJECT` to enable multigroup. Open and add `multigroup: true` near the top:

```yaml
# (set or add)
multigroup: true
projectName: ach
domain: ackstorm.ai
repo: github.com/ackstorm/ach
layout:
- go.kubebuilder.io/v4
version: "3"
```

**Step 3:** Edit `go.mod` so `go` and `toolchain` lines match alitellm:

```go
module github.com/ackstorm/ach

go 1.26.0

toolchain go1.26.3

godebug default=go1.23
```

(Other require blocks from `kubebuilder init` are kept as-is for now; we will re-pin when adding controller-runtime later.)

**Step 4:** Replace `LICENSE` with Apache-2.0 text (copy from alitellm):

```bash
cp /home/jcm/Projects/alitellm-operator/LICENSE LICENSE
```

**Step 5:** Create `NOTICE` (attribution to bbd-style CI plumbing + alitellm pattern):

```
ACH — Agent Configuration Hub
Copyright (c) 2026 ACKstorm

This product includes software developed by ACKstorm
(https://github.com/ackstorm).

Scaffolding patterns (CI workflows, release pipeline, mkdocs structure,
goreleaser configuration, kustomize→helm sync) were adapted from the
alitellm-operator project (Apache-2.0).
```

**Step 6:** Verify generated `.gitignore` exists. Extend it with alitellm extras:

```
# Append to existing .gitignore
.gocache/
.idea/
.vscode/
.DS_Store
*.swp
.env
*.pem
*.key
*.pfx
credentials.json
kubeconfig
.claude/
cover*.out
testbin/
dist/
site/
```

**Step 7:** Verify scaffold compiles.

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace -u "$(id -u):$(id -g)" golang:1.26-bookworm go build ./...`
Expected: PASS (no output).

**Step 8:** Commit.

```bash
git add -A
git commit -m "chore: kubebuilder v4 init, multigroup, Go 1.26, Apache-2.0"
```

---

### Task 1.2: Relocate cmd/main.go to cmd/ach/main.go (single-binary root)

**Files:**
- Delete: `cmd/main.go` (kubebuilder default)
- Create: `cmd/ach/main.go` (minimal stub — cobra wiring lands in Phase 7)

**Step 1:** Move scaffold main aside.

```bash
git rm cmd/main.go
mkdir -p cmd/ach
```

**Step 2:** Create `cmd/ach/main.go`:

```go
// SPDX-License-Identifier: Apache-2.0
package main

import "fmt"

func main() {
	fmt.Println("ach: not yet implemented")
}
```

**Step 3:** Verify.

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.26-bookworm go build ./cmd/ach`
Expected: PASS.

**Step 4:** Commit.

```bash
git add -A
git commit -m "chore: relocate entrypoint to cmd/ach/ (single-binary)"
```

---

### Task 1.3: Replace hack/boilerplate.go.txt with SPDX-only header

**Files:**
- Modify: `hack/boilerplate.go.txt`

**Step 1:** Overwrite with alitellm's pattern:

```
// SPDX-License-Identifier: Apache-2.0
```

**Step 2:** Commit.

```bash
git add hack/boilerplate.go.txt
git commit -m "chore: SPDX-only boilerplate header"
```

---

## Phase 2 — Devtools Container

### Task 2.1: Port Dockerfile.devtools

**Files:**
- Create: `Dockerfile.devtools`
- Create: `scripts/dev.sh`
- Create: `scripts/envtest-assets-path.sh`

**Step 1:** Copy alitellm devtools image as a starting template.

```bash
cp /home/jcm/Projects/alitellm-operator/Dockerfile.devtools Dockerfile.devtools
cp /home/jcm/Projects/alitellm-operator/scripts/dev.sh scripts/dev.sh
cp /home/jcm/Projects/alitellm-operator/scripts/envtest-assets-path.sh scripts/envtest-assets-path.sh
chmod +x scripts/dev.sh scripts/envtest-assets-path.sh
```

**Step 2:** Rename image tag inside the files: replace `litellm-devtools` → `ach-devtools` everywhere.

```bash
grep -rIl 'litellm-devtools' Dockerfile.devtools scripts/dev.sh | xargs sed -i 's/litellm-devtools/ach-devtools/g'
```

**Step 3:** Verify Dockerfile.devtools references no other litellm-specific paths. Open and inspect — if it COPYs anything project-specific, generalize.

**Step 4:** Build the image once (caches all toolchain).

Run: `docker build -t ach-devtools:latest -f Dockerfile.devtools .`
Expected: PASS, ~5 min cold build.

**Step 5:** Smoke test the wrapper.

Run: `./scripts/dev.sh go version`
Expected: `go version go1.26.3 linux/amd64` (or similar).

Run: `./scripts/dev.sh kubebuilder version`
Expected: prints kubebuilder version line including `v4.4.0`.

**Step 6:** Commit.

```bash
git add Dockerfile.devtools scripts/dev.sh scripts/envtest-assets-path.sh
git commit -m "feat(devtools): containerized Go/kubebuilder/kind/helm toolchain"
```

---

### Task 2.2: Add .gocache to .gitignore (already done) + smoke `go build` via wrapper

**Files:** none new

**Step 1:** Verify.

Run: `./scripts/dev.sh go build ./...`
Expected: PASS, writes to `.gocache/`.

Run: `ls .gocache/`
Expected: shows `build`, `gopath` subdirs.

**Step 2:** No commit (verification only).

---

## Phase 3 — Makefile (core targets)

### Task 3.1: Port full Makefile from alitellm and adapt for ach

**Files:**
- Modify: `Makefile` (replace kubebuilder scaffold default)

**Step 1:** Copy alitellm Makefile as starting point.

```bash
cp /home/jcm/Projects/alitellm-operator/Makefile Makefile
```

**Step 2:** Replace project-specific identifiers (use `sed -i`):

| Find | Replace |
|---|---|
| `alitellm-operator` | `ach` |
| `litellm-devtools` | `ach-devtools` |
| `ghcr.io/ackstorm/alitellm-operator` | `ghcr.io/ackstorm/ach` |
| `litellm.ackstorm.ai` (in CRD ref paths) | `ach.ackstorm.ai` |
| `litellm-mock` (in e2e targets) | `ach-mock` |

```bash
sed -i \
  -e 's|alitellm-operator|ach|g' \
  -e 's|litellm-devtools|ach-devtools|g' \
  -e 's|ghcr.io/ackstorm/ach|ghcr.io/ackstorm/ach|g' \
  -e 's|litellm.ackstorm.ai|ach.ackstorm.ai|g' \
  -e 's|litellm-mock|ach-mock|g' \
  Makefile
```

**Step 3:** Update `wait-litellm`, `wait-toolhive`, `wait-mocks`, `logs-litellm`, `pf-litellm` etc. — these alitellm-specific waiters need ach renames AND additions for ach-old's extra deps (postgres, redis, dex). Add new targets:

```make
.PHONY: wait-postgres
wait-postgres: ## Wait for postgres pod Ready
	kubectl wait --for=condition=Ready pod -l app=postgres -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-redis
wait-redis: ## Wait for redis pod Ready
	kubectl wait --for=condition=Ready pod -l app=redis -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-dex
wait-dex: ## Wait for dex pod Ready
	kubectl wait --for=condition=Ready pod -l app=dex -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-platform-api
wait-platform-api: ## Wait for platform-api Deployment Available
	kubectl rollout status deploy/ach-platform-api -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-forwarder
wait-forwarder: ## Wait for forwarder Deployment Available
	kubectl rollout status deploy/ach-forwarder -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-content-service
wait-content-service: ## Wait for content-service Deployment Available
	kubectl rollout status deploy/ach-content-service -n ach-system --timeout=$(WAIT_TIMEOUT)
```

Replace the operator-specific `wait-operator` body if needed so it waits for `deploy/ach-operator`.

**Step 4:** Single-binary, single-image build. Keep `docker-build` and `docker-push` as inherited from alitellm, but ensure `IMG` defaults to `ghcr.io/ackstorm/ach:$(VERSION)`:

```make
VERSION ?= dev
IMG ?= ghcr.io/ackstorm/ach:$(VERSION)

# MODES is the list of cobra subcommands that ship as long-running services.
# Used by waiters, Helm value generation, and per-mode RBAC tooling — NOT by build.
MODES := operator platform-api forwarder content-service migrate
```

There is exactly ONE image (`ghcr.io/ackstorm/ach`). Per-mode Deployments will reference it with different `args: ["<mode>"]` (Phase 11). No per-mode docker-build targets.

**Step 5:** Strip alitellm-specific test/release targets that don't apply yet:
- Keep: `smoke-idempotency`, `leak-soak` if they reference build tags only; otherwise comment-out and TODO.
- Keep: `e2e-focus`, `e2e-full`, `e2e-keep`, `cluster-*`.
- Remove: any `toolhive`-specific waiters not yet relevant — but ach uses ToolHive too, so KEEP.

**Step 6:** Verify Makefile parses.

Run: `./scripts/dev.sh make help`
Expected: lists ≥60 targets, no parse errors.

**Step 7:** Verify smoke targets.

Run: `./scripts/dev.sh make fmt vet`
Expected: PASS (no output beyond go fmt/vet noise).

**Step 8:** Commit.

```bash
git add Makefile
git commit -m "feat(make): port alitellm Makefile, adapt for multi-binary ach"
```

---

### Task 3.2: Add docs/Makefile (mkdocs sub-makefile)

**Files:**
- Create: `docs/Makefile`
- Create: `docs/.crd-ref-docs.yaml`

**Step 1:** Copy.

```bash
cp /home/jcm/Projects/alitellm-operator/docs/Makefile docs/Makefile
cp /home/jcm/Projects/alitellm-operator/docs/.crd-ref-docs.yaml docs/.crd-ref-docs.yaml
```

**Step 2:** Replace group name `litellm.ackstorm.ai` → `ach.ackstorm.ai` in `.crd-ref-docs.yaml`.

```bash
sed -i 's|litellm.ackstorm.ai|ach.ackstorm.ai|g' docs/.crd-ref-docs.yaml
```

**Step 3:** Commit.

```bash
git add docs/Makefile docs/.crd-ref-docs.yaml
git commit -m "docs: port mkdocs makefile and crd-ref-docs config"
```

---

## Phase 4 — Linting and Security Gates

### Task 4.1: Replace .golangci.yml with alitellm's

**Files:**
- Modify: `.golangci.yml`

**Step 1:** Replace.

```bash
cp /home/jcm/Projects/alitellm-operator/.golangci.yml .golangci.yml
```

**Step 2:** Verify.

Run: `./scripts/dev.sh make lint`
Expected: PASS (codebase is mostly empty so should pass trivially).

**Step 3:** Commit.

```bash
git add .golangci.yml
git commit -m "lint: adopt alitellm golangci config (50+ linters, gosec HIGH)"
```

---

### Task 4.2: Add .gitleaks.toml

**Files:**
- Create: `.gitleaks.toml`

**Step 1:** Copy.

```bash
cp /home/jcm/Projects/alitellm-operator/.gitleaks.toml .gitleaks.toml
```

**Step 2:** Edit allowlists — drop the `spec/litellm_api.json` allowlist (we don't bundle it). Add an allowlist for ach test fixtures, e.g.:

```toml
[[allowlists]]
description = "ACH synthetic test fixtures"
condition = "AND"
regexes = ['''(?i)(test|canary|example|synthetic|fake|dummy)''']
paths = ['''.*_test\.go$''']
```

**Step 3:** Smoke run (requires gitleaks in devtools image — should be there per Dockerfile.devtools).

Run: `./scripts/dev.sh gitleaks detect --no-banner --config .gitleaks.toml`
Expected: `no leaks found`.

**Step 4:** Commit.

```bash
git add .gitleaks.toml
git commit -m "security: add gitleaks config with ACH fixture allowlists"
```

---

### Task 4.3: Port pre-push gate scripts

**Files:**
- Create: `scripts/pre-push-check.sh`
- Create: `scripts/install-hooks.sh`
- Create: `scripts/govulncheck-gate.sh`
- Create: `references/security/govulncheck-acknowledged.md`

**Step 1:** Copy.

```bash
cp /home/jcm/Projects/alitellm-operator/scripts/pre-push-check.sh scripts/pre-push-check.sh
cp /home/jcm/Projects/alitellm-operator/scripts/install-hooks.sh scripts/install-hooks.sh
cp /home/jcm/Projects/alitellm-operator/scripts/govulncheck-gate.sh scripts/govulncheck-gate.sh
chmod +x scripts/pre-push-check.sh scripts/install-hooks.sh scripts/govulncheck-gate.sh
mkdir -p references/security
cp /home/jcm/Projects/alitellm-operator/references/security/govulncheck-acknowledged.md references/security/govulncheck-acknowledged.md
```

**Step 2:** Search and replace project names inside scripts.

```bash
sed -i 's|alitellm-operator|ach|g; s|litellm-devtools|ach-devtools|g' \
  scripts/pre-push-check.sh scripts/install-hooks.sh scripts/govulncheck-gate.sh
```

**Step 3:** Install the hook.

Run: `./scripts/install-hooks.sh`
Expected: `Installed .git/hooks/pre-push`.

**Step 4:** Run gate manually (will partially fail on bare repo — note which gates fail and TODO if they require state not yet present, e.g., upstream origin).

Run: `make pre-push`
Expected: most gates PASS; any gate that requires `origin` may need configuring. Do NOT skip — instead, fix or temporarily stub-allow-empty.

**Step 5:** Commit.

```bash
git add scripts/pre-push-check.sh scripts/install-hooks.sh scripts/govulncheck-gate.sh references/security/govulncheck-acknowledged.md
git commit -m "security: port 15-gate pre-push pipeline + govulncheck"
```

---

## Phase 5 — CRD Scaffold (ach.ackstorm.ai/v1alpha1)

Six kinds, lifted from ach-old PROJECT: `Environment`, `Plugin`, `PluginMarketplace`, `Artifact`, `Prompt`, `BackendIdentityPolicy`.

### Task 5.1: Scaffold 6 CRDs via `kubebuilder create api`

**Files (all auto-generated):**
- Create: `api/ach/v1alpha1/{environment,plugin,pluginmarketplace,artifact,prompt,backendidentitypolicy}_types.go`
- Create: `internal/controller/ach/{environment,plugin,pluginmarketplace,artifact,prompt,backendidentitypolicy}_controller.go`
- Create: `internal/controller/ach/suite_test.go` (auto)
- Create: `config/crd/bases/ach.ackstorm.ai_*.yaml`
- Create: `config/rbac/*.yaml` (additions)
- Create: `config/samples/*.yaml`

**Step 1:** For each Kind, run kubebuilder via devtools wrapper:

```bash
for kind in Environment Plugin PluginMarketplace Artifact Prompt BackendIdentityPolicy; do
  ./scripts/dev.sh kubebuilder create api \
    --group ach --version v1alpha1 --kind "$kind" \
    --resource --controller \
    --force
done
```

**Step 2:** Verify all 6 type files + 6 controllers + suite_test.go created.

Run: `ls api/ach/v1alpha1/ && ls internal/controller/ach/`
Expected: 6 `*_types.go`, 6 `*_controller.go`, 1 `suite_test.go`.

**Step 3:** Generate manifests + DeepCopy.

Run: `./scripts/dev.sh make manifests generate`
Expected: PASS. Creates `config/crd/bases/ach.ackstorm.ai_*.yaml` and `*_deepcopy.go`.

**Step 4:** Verify build.

Run: `./scripts/dev.sh make build`
Expected: PASS — but wait, `make build` builds `cmd/main.go`; that's gone. Edit `build` target to build all 6 binaries, or for now just `./cmd/operator`. Quick fix:

```make
.PHONY: build
build: manifests generate fmt vet
	$(GO_BUILD_PREFIX) go build -o bin/ach-operator ./cmd/operator
```

We'll generalize to all 6 in Task 7.6.

Re-run: `./scripts/dev.sh make build`
Expected: PASS.

**Step 5:** Commit.

```bash
git add -A
git commit -m "feat(api): scaffold 6 ach.ackstorm.ai/v1alpha1 CRDs (empty types)"
```

---

### Task 5.2: Move kubebuilder-generated controllers to canonical paths

Kubebuilder may place controllers under `internal/controller/` when multigroup. Confirm location matches ach-old (`internal/controller/`, not `internal/controller/ach/`). If kubebuilder placed them under `internal/controller/ach/`, that's fine — alitellm uses `internal/controller/` (single group). For ach we keep multigroup convention.

**Step 1:** Inspect.

Run: `ls -R internal/controller/`
Expected: confirms layout.

**Step 2:** No change needed unless layout surprising; commit only if files were moved.

---

## Phase 6 — Kustomize Overlays

### Task 6.1: Add ach-specific overlays from ach-old

**Files (lift from ach-old, adapt):**
- Create: `config/deployments/` (operator + platform-api + forwarder + content-service Deployment manifests)
- Create: `config/dev-postgres/` (Postgres StatefulSet for local dev)
- Create: `config/e2e/` (e2e fixtures: kind, helm values)
- Create: `config/secrets/` (Secret templates — empty defaults, marked with TODO)
- Create: `config/storage/` (PVC for content cache)
- Update: `config/default/kustomization.yaml` to wire new overlays

**Step 1:** Copy ach-old overlay directories.

```bash
cp -r /home/jcm/Projects/ach-old/config/deployments config/deployments
cp -r /home/jcm/Projects/ach-old/config/dev-postgres config/dev-postgres
cp -r /home/jcm/Projects/ach-old/config/e2e config/e2e
cp -r /home/jcm/Projects/ach-old/config/secrets config/secrets
cp -r /home/jcm/Projects/ach-old/config/storage config/storage
```

**Step 2:** Inspect each for absolute paths / project-specific image references and rename where needed (e.g., `ghcr.io/ackstorm/ach-...` to match this repo's image tagging).

```bash
grep -rIl 'ach-old\|ackstorm/ach-old' config/ || true
```

**Step 3:** Wire into `config/default/kustomization.yaml`. Add resources block (only those that should be on by default — likely just `deployments`, defer e2e/dev-postgres):

```yaml
resources:
- ../crd
- ../rbac
- ../manager
- ../deployments
- ../network-policy   # if you want it default
```

**Step 4:** Verify kustomize build.

Run: `./scripts/dev.sh kustomize build config/default > /dev/null`
Expected: PASS, prints valid YAML.

**Step 5:** Commit.

```bash
git add config/
git commit -m "config: port ach-old kustomize overlays (deployments, secrets, e2e, storage)"
```

---

### Task 6.2: Replace samples placeholders with real-ish CR samples

**Files:**
- Modify: `config/samples/*.yaml`

**Step 1:** Each of the 6 CRD samples should have NO `TODO(user)` placeholders. Edit each to a minimal valid example. e.g.:

```yaml
# config/samples/ach_v1alpha1_environment.yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: environment-sample
  namespace: ach-system
spec: {}
```

Repeat for all 6.

**Step 2:** Run samples audit target (port from alitellm Makefile).

Run: `./scripts/dev.sh make samples-audit`
Expected: PASS, zero `TODO(user)` lines.

**Step 3:** Commit.

```bash
git add config/samples/
git commit -m "config: minimal sample CRs (no TODO(user) placeholders)"
```

---

## Phase 7 — Single-binary `ach` with cobra subcommands

Layout:

```
cmd/ach/
  main.go                # tiny — calls cmd.Execute()
  cmd/
    root.go              # cobra root, version, global flags
    operator.go          # `ach operator` — runs controller-runtime manager
    platform_api.go      # `ach platform-api` — runs chi HTTP server
    forwarder.go         # `ach forwarder` — runs MCP/A2A forwarder
    content_service.go   # `ach content-service` — serves artifacts via sendfile
    migrate.go           # `ach migrate` — runs golang-migrate
```

Bare `ach` (no subcommand) prints help — the user-facing CLI surface (hydrate, render, etc.) will be added as additional subcommands in Phase 6 of the post-bootstrap roadmap.

### Task 7.1: Add cobra dep and root command

**Files:**
- Modify: `go.mod`, `go.sum` (add `github.com/spf13/cobra`)
- Modify: `cmd/ach/main.go`
- Create: `cmd/ach/cmd/root.go`

**Step 1:** Add cobra dep.

Run: `./scripts/dev.sh go get github.com/spf13/cobra@latest`
Expected: PASS, go.mod updated.

**Step 2:** Replace `cmd/ach/main.go`:

```go
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"

	"github.com/ackstorm/ach/cmd/ach/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

**Step 3:** Create `cmd/ach/cmd/root.go`:

```go
// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is overridden via -ldflags at build time (see Makefile build target).
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "ach",
	Short: "ACH — Agent Configuration Hub",
	Long: `ach is the unified binary for the ACH control plane and CLI.

Run a long-running service:
  ach operator
  ach platform-api
  ach forwarder
  ach content-service

Run a one-shot job:
  ach migrate

Run as CLI: invoke without a subcommand (CLI surface lands in Phase 6).`,
	Version: Version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("ach %s\n", Version))
}
```

**Step 4:** Verify root prints help.

Run: `./scripts/dev.sh go run ./cmd/ach`
Expected: prints `Usage:` block, no subcommands yet (just `completion`, `help`).

**Step 5:** Commit.

```bash
git add cmd/ach/ go.mod go.sum
git commit -m "feat(cmd): cobra root for single-binary ach"
```

---

### Task 7.2: Add `ach operator` subcommand stub

**Files:**
- Create: `cmd/ach/cmd/operator.go`

```go
// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Run the ACH Kubernetes operator (controller-runtime manager)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("ach operator: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(operatorCmd)
}
```

Verify: `./scripts/dev.sh go run ./cmd/ach operator` → prints stub line.
Commit: `feat(cmd): ach operator subcommand stub`.

---

### Task 7.3: Add `ach platform-api` subcommand stub

**Files:** Create `cmd/ach/cmd/platform_api.go`.

```go
// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var platformAPICmd = &cobra.Command{
	Use:   "platform-api",
	Short: "Run the ACH Platform REST API server",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("ach platform-api: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(platformAPICmd)
}
```

Commit: `feat(cmd): ach platform-api subcommand stub`.

---

### Task 7.4: Add `ach forwarder` subcommand stub

Same pattern. `Use: "forwarder"`. File `cmd/ach/cmd/forwarder.go`.
Commit: `feat(cmd): ach forwarder subcommand stub`.

### Task 7.5: Add `ach content-service` subcommand stub

Same pattern. `Use: "content-service"`. File `cmd/ach/cmd/content_service.go`.
Commit: `feat(cmd): ach content-service subcommand stub`.

### Task 7.6: Add `ach migrate` subcommand stub

Same pattern. `Use: "migrate"`. File `cmd/ach/cmd/migrate.go`.
Commit: `feat(cmd): ach migrate subcommand stub`.

---

### Task 7.7: Wire `make build` for single binary with version ldflags

**Files:** Modify `Makefile`.

Replace whatever earlier `build` target exists with:

```make
.PHONY: build
build: manifests generate fmt vet ## Build single ach binary
	go build \
	  -trimpath \
	  -ldflags="-s -w -X github.com/ackstorm/ach/cmd/ach/cmd.Version=$(VERSION)" \
	  -o bin/ach \
	  ./cmd/ach
```

**Step 1:** Verify.

Run: `./scripts/dev.sh make build`
Expected: PASS, produces `bin/ach`.

Run: `./bin/ach --version`
Expected: `ach dev`.

Run: `./bin/ach --help`
Expected: shows all 5 subcommands (operator, platform-api, forwarder, content-service, migrate).

**Step 2:** Commit.

```bash
git add Makefile
git commit -m "feat(make): single-binary build with version ldflags"
```

---

## Phase 8 — Single Dockerfile (single-binary image)

### Task 8.1: Author one multi-stage Dockerfile for the `ach` binary

**Files:**
- Create: `Dockerfile`

**Step 1:** Write `Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION} AS builder
WORKDIR /workspace

# Cache modules
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags="-s -w -X github.com/ackstorm/ach/cmd/ach/cmd.Version=${VERSION}" \
      -o /out/ach \
      ./cmd/ach

# Runtime
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/ach /ach
USER 65532:65532
ENTRYPOINT ["/ach"]
# No CMD — caller (Pod spec / `docker run`) supplies subcommand:
#   docker run --rm ghcr.io/ackstorm/ach:dev operator
#   docker run --rm ghcr.io/ackstorm/ach:dev platform-api
```

**Step 2:** Build and smoke-test.

Run: `docker build --build-arg VERSION=dev -t ghcr.io/ackstorm/ach:dev .`
Expected: PASS.

Run: `docker run --rm ghcr.io/ackstorm/ach:dev --version`
Expected: `ach dev`.

Run: `docker run --rm ghcr.io/ackstorm/ach:dev operator`
Expected: prints `ach operator: not yet implemented`.

Run: `docker run --rm ghcr.io/ackstorm/ach:dev platform-api`
Expected: prints `ach platform-api: not yet implemented`.

**Step 3:** Wire `make docker-build` to use this one Dockerfile and pass `VERSION`:

```make
.PHONY: docker-build
docker-build: ## Build single ach image
	$(CONTAINER_TOOL) build --build-arg VERSION=$(VERSION) -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push ach image
	$(CONTAINER_TOOL) push $(IMG)
```

Confirm `make docker-build IMG=ghcr.io/ackstorm/ach:dev` PASS.

**Step 4:** Commit.

```bash
git add Dockerfile Makefile
git commit -m "feat(docker): single multi-stage distroless image, mode via cobra subcommand"
```

---

## Phase 9 — Test Scaffold

### Task 9.1: Port run-envtest-packages.sh

**Files:**
- Create: `scripts/run-envtest-packages.sh`

```bash
cp /home/jcm/Projects/alitellm-operator/scripts/run-envtest-packages.sh scripts/run-envtest-packages.sh
chmod +x scripts/run-envtest-packages.sh
```

Verify: `./scripts/dev.sh make envtest-fast`
Expected: PASS — runs zero tests but exits 0 (no `*_test.go` matching envtest selector yet, or runs the auto-generated suite_test.go from kubebuilder which spins envtest and exits clean).

Commit: `test: concurrent envtest runner from alitellm`.

### Task 9.2: Port e2e suite scaffold

**Files:**
- Create: `test/e2e/suite_test.go` (Ginkgo bootstrap)
- Create: `test/e2e/utils/` directory

**Step 1:** Copy alitellm's suite_test.go + utils package as a starting point, renaming references.

```bash
mkdir -p test/e2e/utils
cp /home/jcm/Projects/alitellm-operator/test/e2e/suite_test.go test/e2e/suite_test.go
cp -r /home/jcm/Projects/alitellm-operator/test/e2e/utils/* test/e2e/utils/
sed -i 's|alitellm-operator|ach|g; s|litellm.ackstorm.ai|ach.ackstorm.ai|g' \
  test/e2e/suite_test.go test/e2e/utils/*.go
```

**Step 2:** Confirm `//go:build e2e` tag at top of every file.

**Step 3:** Verify build (with tag).

Run: `./scripts/dev.sh go build -tags=e2e ./test/e2e/...`
Expected: PASS.

**Step 4:** Commit.

```bash
git add test/e2e/
git commit -m "test(e2e): Ginkgo suite bootstrap + utils"
```

---

### Task 9.3: Stub test/e2e/mock/ image source (for future mock litellm/dex)

**Files:**
- Create: `test/e2e/mock/main.go` (placeholder)
- Create: `test/e2e/mock/Dockerfile`

```go
// SPDX-License-Identifier: Apache-2.0
package main

import "fmt"

func main() {
	fmt.Println("ach-mock: not yet implemented")
}
```

`test/e2e/mock/Dockerfile`:

```dockerfile
FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ach-mock ./test/e2e/mock

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/ach-mock /ach-mock
ENTRYPOINT ["/ach-mock"]
```

Verify: `docker build -f test/e2e/mock/Dockerfile -t ach-mock:e2e .` PASS.

Commit: `test(e2e): mock service stub + Dockerfile`.

---

## Phase 10 — Kind Cluster Scripts

### Task 10.1: Port cluster.sh + kind-config.yaml

**Files:**
- Create: `scripts/cluster.sh`
- Create: `scripts/kind-config.yaml`
- Create: `scripts/dex-config.yaml` (lifted from ach-old)
- Create: `scripts/collect-forensics.sh`

**Step 1:**

```bash
cp /home/jcm/Projects/alitellm-operator/scripts/cluster.sh scripts/cluster.sh
cp /home/jcm/Projects/alitellm-operator/scripts/kind-config.yaml scripts/kind-config.yaml
cp /home/jcm/Projects/alitellm-operator/scripts/collect-forensics.sh scripts/collect-forensics.sh
cp /home/jcm/Projects/ach-old/scripts/dex-config.yaml scripts/dex-config.yaml
chmod +x scripts/cluster.sh scripts/collect-forensics.sh
```

**Step 2:** Rename references inside cluster.sh:
- `litellm` → `ach`
- `litellm-devtools` → `ach-devtools`
- Cluster name: pick `ach-operator-e2e` (matches ach-old) or `ach-e2e` — be consistent. Use `ach-e2e`.

**Step 3:** Extend `cluster.sh hydrate` step to install ach's local deps:
- BerriAI/litellm-helm (alitellm dep — keep)
- ToolHive (alitellm dep — keep)
- bitnami/postgresql (ach dep)
- bitnami/redis or bitnami/valkey (ach dep)
- dex (ach dep — using scripts/dex-config.yaml)

**Step 4:** Test cluster lifecycle.

Run: `./scripts/dev.sh make cluster-up`
Expected: PASS, kind cluster `ach-e2e` running.

Run: `./scripts/dev.sh make cluster-status`
Expected: shows ready nodes + hydrated workloads.

Run: `./scripts/dev.sh make cluster-down`
Expected: PASS, cluster deleted.

**Step 5:** Commit.

```bash
git add scripts/cluster.sh scripts/kind-config.yaml scripts/dex-config.yaml scripts/collect-forensics.sh
git commit -m "feat(scripts): kind cluster lifecycle + dex/postgres/redis hydration"
```

---

## Phase 11 — Helm Chart

### Task 11.1: Scaffold deploy/helm/ach/ multi-component chart

**Files:**
- Create: `deploy/helm/ach/Chart.yaml`
- Create: `deploy/helm/ach/values.yaml`
- Create: `deploy/helm/ach/templates/install.yaml` (placeholder — synced from kustomize via `make helm-sync`)
- Create: `deploy/helm/ach/templates/crds.yaml`
- Create: `deploy/helm/ach/crd-sources/` (copy of `config/crd/bases/`)

**Step 1:** Copy alitellm chart as template.

```bash
cp -r /home/jcm/Projects/alitellm-operator/deploy/helm/alitellm-operator deploy/helm/ach
```

**Step 2:** Rename references inside (sed -i across all files in deploy/helm/ach):

```bash
grep -rIl 'alitellm-operator\|litellm.ackstorm.ai' deploy/helm/ach \
  | xargs sed -i 's|alitellm-operator|ach|g; s|litellm.ackstorm.ai|ach.ackstorm.ai|g'
```

**Step 3:** Edit `values.yaml`. Single `image:` block (all modes share it); per-mode toggle + optional override for replicas/resources:

```yaml
# Single image — every mode runs the same binary, differs only by args.
image:
  repo: ghcr.io/ackstorm/ach
  tag: v0.0.1
  pullPolicy: IfNotPresent

installCRDs: true
watchNamespace: ""

# Per-mode toggles. `args` defaults to [<mode>] but is overridable.
operator:
  enabled: true
  replicas: 1
  resources: {}
  args: ["operator"]
platformApi:
  enabled: true
  replicas: 1
  resources: {}
  args: ["platform-api"]
forwarder:
  enabled: true
  replicas: 1
  resources: {}
  args: ["forwarder"]
contentService:
  enabled: true
  replicas: 1
  resources: {}
  args: ["content-service"]
migrate:
  enabled: true              # rendered as a Job (not Deployment)
  args: ["migrate"]

postgres:
  external: true             # users provide their own DSN via Secret
redis:
  external: true
toolhive:
  enabled: true
metrics:
  serviceMonitor:
    enabled: false
extraEnv: []
```

**Step 3b:** Templates pattern — each enabled mode renders a Deployment (or Job for migrate) that references `{{ .Values.image.repo }}:{{ .Values.image.tag }}` and sets `args: {{ .Values.operator.args | toYaml }}` etc. Single ServiceAccount + RBAC scoped to operator mode; platform-api/forwarder/content-service get their own minimal ServiceAccounts (no cluster-wide watch perms).

**Step 4:** Sync CRDs into `crd-sources/`.

```bash
cp -f config/crd/bases/*.yaml deploy/helm/ach/crd-sources/
```

**Step 5:** Port `scripts/kustomize-to-helm.sh` and `scripts/helm-inject-crd-annotation.py`.

```bash
cp /home/jcm/Projects/alitellm-operator/scripts/kustomize-to-helm.sh scripts/kustomize-to-helm.sh
cp /home/jcm/Projects/alitellm-operator/scripts/helm-inject-crd-annotation.py scripts/helm-inject-crd-annotation.py
chmod +x scripts/kustomize-to-helm.sh
sed -i 's|alitellm-operator|ach|g' scripts/kustomize-to-helm.sh scripts/helm-inject-crd-annotation.py
```

**Step 6:** Run `make helm-sync`.

Run: `./scripts/dev.sh make helm-sync`
Expected: PASS, regenerates `deploy/helm/ach/templates/install.yaml` from kustomize output.

**Step 7:** Lint the chart.

Run: `./scripts/dev.sh helm lint deploy/helm/ach`
Expected: PASS (0 errors, warnings ok).

**Step 8:** Commit.

```bash
git add deploy/ scripts/kustomize-to-helm.sh scripts/helm-inject-crd-annotation.py
git commit -m "feat(helm): multi-component ach chart, kustomize→helm sync"
```

---

### Task 11.2: Port deploy/kustomize/ snapshot

**Files:**
- Create: `deploy/kustomize/kustomization.yaml`
- Create: `deploy/kustomize/manager-rbac.yaml`
- Create: `scripts/render-deploy-kustomize-rbac.sh`

```bash
cp -r /home/jcm/Projects/alitellm-operator/deploy/kustomize deploy/kustomize
cp /home/jcm/Projects/alitellm-operator/scripts/render-deploy-kustomize-rbac.sh scripts/render-deploy-kustomize-rbac.sh
chmod +x scripts/render-deploy-kustomize-rbac.sh
sed -i 's|alitellm-operator|ach|g' \
  deploy/kustomize/kustomization.yaml deploy/kustomize/manager-rbac.yaml \
  scripts/render-deploy-kustomize-rbac.sh
```

Verify: `./scripts/dev.sh make deploy-kustomize-sync-check` PASS.
Commit: `feat(deploy): kustomize snapshot + sync script`.

---

## Phase 12 — CI/CD Workflow (minimum)

### Task 12.1: Port ci.yml

**Files:**
- Create: `.github/workflows/ci.yml`

```bash
mkdir -p .github/workflows
cp /home/jcm/Projects/alitellm-operator/.github/workflows/ci.yml .github/workflows/ci.yml
sed -i 's|alitellm-operator|ach|g; s|litellm-devtools|ach-devtools|g' .github/workflows/ci.yml
```

**Step 2:** Adjust the e2e job timeout if needed (ach has more deps, may need 40m).

**Step 3:** Commit (workflow not running locally — will validate on first push).

```bash
git add .github/workflows/ci.yml
git commit -m "ci: port ci.yml (lint, unit, envtest, security, e2e)"
```

---

### Task 12.2: Port govulncheck.yml

**Files:**
- Create: `.github/workflows/govulncheck.yml`

```bash
cp /home/jcm/Projects/alitellm-operator/.github/workflows/govulncheck.yml .github/workflows/govulncheck.yml
sed -i 's|alitellm-operator|ach|g' .github/workflows/govulncheck.yml
```

Commit: `ci: nightly govulncheck workflow`.

### Task 12.3: Port pr-labeler.yml (and labeler config)

```bash
cp /home/jcm/Projects/alitellm-operator/.github/workflows/pr-labeler.yml .github/workflows/pr-labeler.yml
# also copy labeler config if present
if [ -f /home/jcm/Projects/alitellm-operator/.github/labeler.yml ]; then
  cp /home/jcm/Projects/alitellm-operator/.github/labeler.yml .github/labeler.yml
fi
```

Commit: `ci: PR auto-labeler`.

---

## Phase 13 — mkdocs Site

### Task 13.1: Bootstrap mkdocs scaffold

**Files:**
- Create: `mkdocs.yml`
- Create: `docs/index.md`
- Create: `docs/getting-started/installation.md`
- Create: `docs/getting-started/quickstart.md`
- Create: `docs/user-guide/index.md`
- Create: `docs/developer-guide/architecture.md`
- Create: `docs/developer-guide/development.md`
- Create: `docs/developer-guide/release-process.md`
- Create: `docs/api-reference/.gitkeep`

**Step 1:** Copy alitellm mkdocs.yml.

```bash
cp /home/jcm/Projects/alitellm-operator/mkdocs.yml mkdocs.yml
sed -i 's|alitellm-operator|ach|g; s|litellm.ackstorm.ai|ach.ackstorm.ai|g' mkdocs.yml
```

**Step 2:** Replace `nav:` entries with ach-relevant tree (operator + platform-api + CLI sections).

**Step 3:** Seed minimal docs pages — each is one paragraph stub with `TODO: fill`. Example `docs/index.md`:

```markdown
# ACH — Agent Configuration Hub

ACH is the control plane for managing AI agent configurations across environments.

> **Status:** scaffolding bootstrap — implementation in progress.

See [Quickstart](getting-started/quickstart.md) once available.
```

**Step 4:** Generate the CRD reference (may be empty since types have no fields yet — that's fine).

Run: `./scripts/dev.sh make gen-crd-ref-docs`
Expected: PASS, writes `docs/api-reference/ach.ackstorm.ai.md`.

**Step 5:** Build docs.

Run: `./scripts/dev.sh make docs-build`
Expected: PASS, writes `site/`.

**Step 6:** Commit.

```bash
git add mkdocs.yml docs/
git commit -m "docs: mkdocs-material scaffold + crd reference seed"
```

---

### Task 13.2: Port docs.yml workflow

**Files:**
- Create: `.github/workflows/docs.yml`

```bash
cp /home/jcm/Projects/alitellm-operator/.github/workflows/docs.yml .github/workflows/docs.yml
sed -i 's|alitellm-operator|ach|g' .github/workflows/docs.yml
```

Commit: `ci: docs build + mike gh-pages publish`.

---

## Phase 14 — Release Pipeline (goreleaser)

### Task 14.1: Port goreleaser configs and adapt for multi-binary

**Files:**
- Create: `.goreleaser.yml`
- Create: `.goreleaser.prerelease.yml`
- Create: `.goreleaser.snapshot.yml`
- Create: `Dockerfile.goreleaser`

**Step 1:** Copy.

```bash
cp /home/jcm/Projects/alitellm-operator/.goreleaser.yml .goreleaser.yml
cp /home/jcm/Projects/alitellm-operator/.goreleaser.prerelease.yml .goreleaser.prerelease.yml
cp /home/jcm/Projects/alitellm-operator/.goreleaser.snapshot.yml .goreleaser.snapshot.yml
cp /home/jcm/Projects/alitellm-operator/Dockerfile.goreleaser Dockerfile.goreleaser
```

**Step 2:** Edit each `.goreleaser*.yml`:
- Project name: `ach`
- `builds:` block — **single build** of `./cmd/ach`, both server and CLI use cases:

```yaml
builds:
  - id: ach
    main: ./cmd/ach
    binary: ach
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags:
      - -s -w
      - -X github.com/ackstorm/ach/cmd/ach/cmd.Version={{.Version}}
```

- `dockers:` block — **single image** per arch, referencing the root `Dockerfile`:

```yaml
dockers:
  - id: ach-amd64
    ids: [ach]
    image_templates:
      - "ghcr.io/ackstorm/ach:{{ .Version }}-amd64"
    dockerfile: Dockerfile
    use: buildx
    goos: linux
    goarch: amd64
    build_flag_templates:
      - "--platform=linux/amd64"
      - "--build-arg=VERSION={{ .Version }}"
  - id: ach-arm64
    ids: [ach]
    image_templates:
      - "ghcr.io/ackstorm/ach:{{ .Version }}-arm64"
    dockerfile: Dockerfile
    use: buildx
    goos: linux
    goarch: arm64
    build_flag_templates:
      - "--platform=linux/arm64"
      - "--build-arg=VERSION={{ .Version }}"
```

- `docker_manifests:` block — **one manifest list** combining the two arches:

```yaml
docker_manifests:
  - name_template: "ghcr.io/ackstorm/ach:{{ .Version }}"
    image_templates:
      - "ghcr.io/ackstorm/ach:{{ .Version }}-amd64"
      - "ghcr.io/ackstorm/ach:{{ .Version }}-arm64"
  - name_template: "ghcr.io/ackstorm/ach:latest"
    image_templates:
      - "ghcr.io/ackstorm/ach:{{ .Version }}-amd64"
      - "ghcr.io/ackstorm/ach:{{ .Version }}-arm64"
    skip_push: "{{ if .Prerelease }}true{{ else }}false{{ end }}"
```

- `signs:` and `docker_signs:` — keep cosign keyless OIDC, single image to sign.
- `sboms:` — keep cyclonedx-gomod, single SBOM.
- `archives:` — produce per-OS/arch tarballs of the `ach` binary for CLI distribution (Homebrew tap, GitHub Releases).

**Step 3:** Validate config without releasing.

Run: `./scripts/dev.sh goreleaser check`
Expected: PASS.

Run: `./scripts/dev.sh goreleaser build --snapshot --clean --single-target`
Expected: PASS, produces `dist/` with one-arch binaries.

**Step 4:** Commit.

```bash
git add .goreleaser.yml .goreleaser.prerelease.yml .goreleaser.snapshot.yml Dockerfile.goreleaser
git commit -m "release: goreleaser multi-binary configs (6 services × 2 arches)"
```

---

### Task 14.2: Add release.yml workflow + bump target

**Files:**
- Create: `.github/workflows/release.yml`
- Modify: `Makefile` — `bump` and `release` targets

**Step 1:** Copy workflow.

```bash
cp /home/jcm/Projects/alitellm-operator/.github/workflows/release.yml .github/workflows/release.yml
sed -i 's|alitellm-operator|ach|g' .github/workflows/release.yml
```

**Step 2:** Edit `Makefile` `bump` target. Single image → fewer manifests to update:

```make
.PHONY: bump
bump: ## Bump VERSION across all manifests (used by release.yml)
	@test -n "$(VERSION)" || (echo "VERSION required" && exit 1)
	sed -i 's|^version: .*|version: $(VERSION)|' deploy/helm/ach/Chart.yaml
	sed -i 's|^appVersion: .*|appVersion: v$(VERSION)|' deploy/helm/ach/Chart.yaml
	sed -i 's|  tag: v.*|  tag: v$(VERSION)|' deploy/helm/ach/values.yaml
	sed -i 's|newTag:.*|newTag: v$(VERSION)|' config/manager/kustomization.yaml || true
	sed -i 's|newTag:.*|newTag: v$(VERSION)|' deploy/kustomize/kustomization.yaml || true
```

Only ONE image newTag to bump (was 6).

**Step 3:** Commit.

```bash
git add .github/workflows/release.yml Makefile
git commit -m "release: commit-msg-driven release.yml + bump target (multi-binary)"
```

---

### Task 14.3: Port nightly.yml (defer if not needed yet)

**Files:**
- Create: `.github/workflows/nightly.yml`

```bash
cp /home/jcm/Projects/alitellm-operator/.github/workflows/nightly.yml .github/workflows/nightly.yml
sed -i 's|alitellm-operator|ach|g' .github/workflows/nightly.yml
```

Commit: `ci: nightly smoke-idempotency + leak-soak`.

---

## Phase 15 — Top-level Documentation

### Task 15.1: README.md

**Files:** Replace stub `README.md`.

```markdown
# ACH — Agent Configuration Hub

Multi-service Kubernetes control plane for managing AI agent configurations:
operator + platform API + forwarder + content service + CLI.

## Quick links

- [Documentation](https://ackstorm.github.io/ach/)
- [Installation](https://ackstorm.github.io/ach/getting-started/installation/)
- [Architecture](https://ackstorm.github.io/ach/developer-guide/architecture/)
- [Release process](https://ackstorm.github.io/ach/developer-guide/release-process/)
- [CONTRIBUTING](CONTRIBUTING.md)
- [SECURITY](SECURITY.md)
- [MAINTAINERS](MAINTAINERS.md)
- [CHANGELOG](CHANGELOG.md)

## License

Apache-2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
```

Commit: `docs: README with project intro and quick links`.

### Task 15.2: CHANGELOG.md

```markdown
# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Initial scaffolding (kubebuilder v4, multi-binary, Helm chart, CI/CD, mkdocs, goreleaser).
```

Commit: `docs: CHANGELOG bootstrap`.

### Task 15.3: SECURITY.md

Copy from alitellm:

```bash
cp /home/jcm/Projects/alitellm-operator/SECURITY.md SECURITY.md
sed -i 's|alitellm-operator|ach|g' SECURITY.md
```

Commit: `docs: SECURITY disclosure policy`.

### Task 15.4: MAINTAINERS.md

```markdown
# Maintainers

- Juan Carlos Moreno (@jcm) — juancarlos.moreno@ackstorm.com

## Decision making

Contact a maintainer to discuss changes.
```

Commit: `docs: MAINTAINERS list`.

### Task 15.5: CONTRIBUTING.md

```bash
cp /home/jcm/Projects/alitellm-operator/CONTRIBUTING.md CONTRIBUTING.md
sed -i 's|alitellm-operator|ach|g' CONTRIBUTING.md
```

Commit: `docs: CONTRIBUTING guidelines`.

### Task 15.6: ROADMAP.md (port ach-old's roadmap, adapt heading)

```bash
cp /home/jcm/Projects/ach-old/.planning/ROADMAP.md ROADMAP.md
```

Trim or annotate as needed.
Commit: `docs: ROADMAP from ach-old planning`.

### Task 15.7: CLAUDE.md (project-specific instructions)

**Files:** Create `CLAUDE.md` in repo root.

Take alitellm's `CLAUDE.md` as the template, edit:
- Project name → ach
- Binary list → 6 binaries
- Wait targets → include `wait-postgres`, `wait-redis`, `wait-dex`, `wait-platform-api`, `wait-forwarder`, `wait-content-service`
- Failure modes — adapt to ach domain (e.g., no enterprise-only LiteLLM fields, valid Postgres/Redis URLs in dev hydration)

```bash
cp /home/jcm/Projects/alitellm-operator/CLAUDE.md CLAUDE.md
# then hand-edit for ach specifics — see notes above
```

Commit: `docs: CLAUDE.md with ach-specific dev conventions`.

### Task 15.8: PUBLISH.md (release runbook)

```bash
cp /home/jcm/Projects/alitellm-operator/PUBLISH.md PUBLISH.md
sed -i 's|alitellm-operator|ach|g' PUBLISH.md
```

Commit: `docs: PUBLISH release runbook`.

---

## Phase 16 — End-to-end Smoke Verification

### Task 16.1: Full bootstrap smoke run

**Files:** none modified — verification only.

Run sequentially:

```bash
./scripts/dev.sh make help                        # ≥60 targets
./scripts/dev.sh make manifests generate fmt vet
./scripts/dev.sh make build                       # single bin/ach
./bin/ach --version                               # ach dev
./bin/ach operator                                # stub line
./bin/ach platform-api                            # stub line
./bin/ach forwarder                               # stub line
./bin/ach content-service                         # stub line
./bin/ach migrate                                 # stub line
./scripts/dev.sh make unit                        # zero tests but PASS
./scripts/dev.sh make envtest-fast                # zero envtest but PASS
./scripts/dev.sh make lint                        # PASS
./scripts/dev.sh make samples-audit               # PASS
make pre-push                                     # PASS (host-only)
./scripts/dev.sh make helm-sync-check             # PASS (no drift)
./scripts/dev.sh make deploy-kustomize-sync-check # PASS
./scripts/dev.sh helm lint deploy/helm/ach        # PASS
./scripts/dev.sh make docs-build                  # PASS, site/ produced
./scripts/dev.sh goreleaser check                 # PASS
docker build --build-arg VERSION=smoke -t ghcr.io/ackstorm/ach:smoke .
docker run --rm ghcr.io/ackstorm/ach:smoke --version       # ach smoke
docker run --rm ghcr.io/ackstorm/ach:smoke operator        # stub line
docker run --rm ghcr.io/ackstorm/ach:smoke platform-api    # stub line
./scripts/dev.sh make cluster-up && ./scripts/dev.sh make cluster-status && ./scripts/dev.sh make cluster-down
```

Expected: every command PASSes (zero non-zero exits, no manual intervention).

**Step 2:** Tag the bootstrap checkpoint.

```bash
git tag -a bootstrap-complete -m "ACH scaffolding bootstrap complete — alitellm-parity"
```

(Do NOT push tag yet — that becomes part of the first real release flow.)

---

## Post-bootstrap follow-up plans (NOT in this plan)

These are separate plans to write after bootstrap green:

1. **Domain types lift** — populate the 6 CRD `*_types.go` files with real spec/status from `ach-old/api/ach/v1alpha1/`.
2. **Reconciler port** — copy `internal/controller/*` and supporting packages (`audit`, `cachefs`, `connection`, `credhash`, `db`, `keys`, `keystore`, `litellm`, `orphan`, `snapshot`, `sources`) from ach-old.
3. **Platform API port** — `internal/platformapi/*` from ach-old, including chi routes, auth, env keys, hydrate, render, store, teams.
4. **Forwarder + content-service implementation**.
5. **Migrate binary + db/migrations** — port from `ach-old/db/migrations/`.
6. **E2E phase invariants** — adapt ach-old's `test/e2e/phase{1,2,3}_*` tests to the new repo layout.
7. **Spec migration** — move `ach-old/spec/` documents into `docs/` and `references/` as appropriate; freeze and version-tag.
8. **First real release** — `chore(release): v0.1.0` to trigger release.yml end-to-end.

---

## Cross-references

- **alitellm-operator** scaffolding: `/home/jcm/Projects/alitellm-operator/`
- **ach-old** domain code (read-only, do not write here): `/home/jcm/Projects/ach-old/`
- **Sister Go project** (operator-side patterns for ACH Hub): `/home/jcm/Projects/ach_litellm/` (per memory `[[reference_ach_litellm_sister_project]]`)
- **LiteLLM autoconfig** (operator predecessor): `/home/jcm/Projects/mcp/litellm-autoconfig/` (per memory `[[reference_litellm_autoconfig_predecessor]]`)

## Notes / Decisions log

- **Single binary `ach` with cobra subcommands** (decided 2026-05-25). 1 image, 1 SBOM, 1 cosign sig, simpler goreleaser/Makefile/Helm. Trade-offs: larger binary (~120MB), per-mode RBAC needs explicit subdivision, no per-mode independent hotfix. Per-mode Deployments share image, differ only by `args: ["<mode>"]`.
- **Go version**: 1.26 (alitellm) — NOT ach-old's 1.23.
- **Test framework**: Ginkgo v2 for envtest + e2e (both alitellm and ach-old agree). Stdlib `testing` for pure-unit.
- **Local UAT**: kind + helm install (per memory `[[feedback_local_uat_kind_helm]]`). NOT docker-compose (superseded per `[[feedback_local_uat_docker_compose]]`).
- **Helm source-of-truth**: kustomize → helm via `make helm-sync` (alitellm pattern). Helm chart is veneer; kustomize is canonical.
- **Release trigger**: commit message `chore(release): vX.Y.Z` on main; tag created LAST by release.yml (so failures leave no orphan tag).
- **Multi-binary release**: 6 services × 2 arches (amd64+arm64) via goreleaser `builds:` array.
- **Naked polling loops banned** in scripts and tests — use `make wait-*` targets or bounded `timeout` + `grep -m1` patterns (per global CLAUDE.md).
- **Per `[[feedback_litellm_operator_pass_through]]`**: spec.params pass-through with no validation/defaults at operator side — applies to ACH's BackendIdentityPolicy and similar resources later.
