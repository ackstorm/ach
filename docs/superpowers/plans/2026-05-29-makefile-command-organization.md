# Deterministic `make`-only Command Organization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `make` the single, deterministic interface for every developer workflow (cluster, test, e2e, build, qa, docs), so the host needs only `docker` and a command can never half-mutate state on the wrong execution context.

**Architecture:** Hybrid model. `scripts/dev.sh` and `scripts/cluster.sh` become internal plumbing. Public `make` targets opt in — via an explicit `container_target` macro — to auto-route into the devtools container (where the Go toolchain + helm/kind/kubectl live); host+docker gates stay on host. A two-level `make doctor` / `make doctor-cluster` preflight verifies the environment before mutation. Cluster lifecycle becomes transactional. Renamed targets are propagated to `.github/workflows/*` in lockstep (no back-compat aliases).

**Tech Stack:** GNU Make, Bash, Docker, kind, Helm, Kubernetes, Go (two binaries: `ach` services + `ach-cli`), GitHub Actions.

---

## Background facts (verified in-repo on 2026-05-29)

- Host PATH has: `kind`, `kubectl`, `docker`, `jq`, `openssl`, `python3`. Host is MISSING: `go`, `helm`, `yq`, `gitleaks`, `trufflehog`. The devtools container has the full Go toolchain + `helm`/`kind`/`kubectl`.
- `scripts/dev.sh` runs the devtools container with `--network=host`, mounts the repo at `/workspace`, mounts `/var/run/docker.sock`, and sets `KUBECONFIG=/workspace/.gocache/kube/config` (a host-side path, bind-mounted). So `kind` inside the container drives the host docker daemon and writes the kubeconfig to `./.gocache/kube/config` on the host filesystem.
- Two Go binaries exist after the CLI split: `bin/ach` (`./cmd/ach`, services) and `bin/ach-cli` (`./cmd/ach-cli`, user CLI).
- `make` targets currently used by CI (must be renamed in lockstep):
  - `.github/workflows/ci.yml:47` `./scripts/dev.sh make lint`
  - `.github/workflows/ci.yml:60` `./scripts/dev.sh make unit`
  - `.github/workflows/ci.yml:82` `./scripts/dev.sh make envtest-fast`
  - `.github/workflows/ci.yml:112` `./scripts/dev.sh make security`
  - `.github/workflows/ci.yml:156` `./scripts/dev.sh make cluster-up`
  - `.github/workflows/ci.yml:159` `./scripts/dev.sh make e2e`
  - `.github/workflows/ci.yml:167` `./scripts/dev.sh make cluster-down`
  - `.github/workflows/gotests.yml:36` `make test`
  - `.github/workflows/nightly.yml:38` `make smoke-idempotency-long`
  - `.github/workflows/nightly.yml:61` `make leak-soak`
  - `.github/workflows/nightly.yml:85` `./scripts/dev.sh make fuzz-long`
  - `.github/workflows/docs.yml:39` `make docs`
  - `.github/workflows/release.yml:80` `make unit`
  - `.github/workflows/release.yml:81` `make envtest-fast`
  - `.github/workflows/release.yml:104` `make bump VERSION=$VERSION`
  - `.github/workflows/release.yml:161` `make generate manifests`
- ach-old stale targets (verified unused in any workflow → safe to delete): `operator-redeploy`, `watch-crs`, `pf-litellm`, `pf-openai-mock`, `pf-kubeai-mock`, `logs-mocks`, `mock-mode`, `docker-load`.

## Final command vocabulary (the contract)

| Family | Targets |
|---|---|
| `cluster-` | `cluster-up` · `cluster-down` · `cluster-reset` · `cluster-sync` · `cluster-status` · `cluster-image-load` |
| `test-` | `test-full` · `test-unit` · `test-envtest` · `test-envtest-fast` · `test-integration` · `test-smoke-idempotency` · `test-smoke-idempotency-long` · `test-leak-soak` · `test-unit-pkg PKG=…` · `test-envtest-pkg PKG=…` |
| `e2e-` | `e2e-full` · `e2e-keep` · `e2e-run` · `e2e-focus RUN=… / FOCUS=…` |
| `build-` | `build-all` · `build-server` · `build-cli` · `build-image` · `build-image-mock` · `build-image-mcp-echo` |
| `gen-` | `gen-code` · `gen-manifests` · `gen-crd-ref-docs` |
| `qa-` | `qa-lint` · `qa-lint-fix` · `qa-lint-changed` · `qa-lint-config` · `qa-security` · `qa-fuzz-short` · `qa-fuzz-long` |
| `wait-` | `wait-operator` · `wait-platform-api` · `wait-forwarder` · `wait-content-service` · `wait-mcp-echo` · `wait-postgres` · `wait-redis` · `wait-dex` · `wait-litellm` · `wait-ach` · `wait-cr-ready` · `wait-container` |
| `logs-` | `logs-operator` · `logs-platform-api` · `logs-forwarder` · `logs-litellm` |
| `release-` | `release-bump` · `release-cut` |
| `docs-` | `docs-build` · `docs-serve` · `docs-gen-crd` |
| Gates (no prefix) | `pre-commit` · `pre-push` · `verify` · `hooks` |
| Dev | `doctor` · `doctor-cluster` · `shell` |

Rename map for CI: `lint`→`qa-lint`, `unit`→`test-unit`, `envtest-fast`→`test-envtest-fast`, `security`→`qa-security`, `e2e`→`e2e-run`, `test`→`test-full`, `smoke-idempotency-long`→`test-smoke-idempotency-long`, `leak-soak`→`test-leak-soak`, `fuzz-long`→`qa-fuzz-long`, `docs`→`docs-build`, `bump`→`release-bump`, `generate`→`gen-code`, `manifests`→`gen-manifests`, `docker-build`→`build-image`. (`cluster-up`/`cluster-down` keep their names.)

## File structure (what each touched file owns)

- `scripts/dev.sh` — the ONLY tool boundary. Adds: `ACH_IN_DEVTOOLS` recursion guard, docker preflight, passes `ACH_IN_DEVTOOLS=1` into the container.
- `Makefile` — public/private target split, `container_target` macro, vocabulary renames, `doctor`/`doctor-cluster`/`shell`, deletion of ach-old targets.
- `scripts/cluster.sh` — refuses to run outside devtools; transactional `cmd_up`/`cmd_sync`/`cmd_down`/`cmd_reset`; a `preflight` subcommand backing `doctor-cluster`. (mcp-echo build+load already landed in commit `7be51b5`.)
- `Dockerfile.devtools` — reconcile envtest version pins with `go.mod`.
- `deploy/helm/ach/templates/test-mocks.yaml` — add `rebuild-id` annotation to the two mock Deployments (hand-written template; NOT regenerated by `helm-sync`).
- `scripts/pre-push-check.sh` — pin scanner image digests; `fmt-check` not `fmt`; restore `go mod tidy` drift on every exit path.
- `.github/workflows/{ci,gotests,nightly,docs,release}.yml` — target renames.
- `references/makefile.md` — new authoritative command reference (table + 3-context model + kubectl note).
- `CLAUDE.md`, `test/e2e/README.md`, `examples/README.md` — reconcile to the new vocabulary.

---

## Phase 1 — Foundation (recursion guard + doctor). No behavior change to existing targets.

### Task 1: `dev.sh` recursion guard + docker preflight

**Files:**
- Modify: `scripts/dev.sh`

- [ ] **Step 1: Add the guard + preflight near the top**

In `scripts/dev.sh`, immediately after the `WORKSPACE="..."` line (line ~26, just after `IMAGE=...`), insert:

```bash
# If we are already inside the devtools container, run the command directly.
# Auto-wrapping make targets (Makefile `container_target` macro) re-invoke
# ./scripts/dev.sh from within; this prevents nested containers.
if [[ "${ACH_IN_DEVTOOLS:-0}" == "1" ]]; then
    exec "${@:-bash}"
fi

# Host requirement: docker only. Fail fast with a clear message rather than
# letting a downstream `kind`/`helm` call die cryptically.
if ! docker info >/dev/null 2>&1; then
    echo "scripts/dev.sh: docker daemon not reachable — is Docker running and is your user in the docker group?" >&2
    exit 1
fi
```

- [ ] **Step 2: Pass the flag into the container**

In the `exec docker run --rm ...` block, add this line alongside the other `-e` flags (e.g. right after `-e HOME=/workspace/.gocache \`):

```bash
    -e ACH_IN_DEVTOOLS=1 \
```

- [ ] **Step 3: Verify the guard short-circuits (no nested container)**

Run: `ACH_IN_DEVTOOLS=1 ./scripts/dev.sh echo nested-ok`
Expected: prints `nested-ok` immediately (no "building image" / no `docker run`).

- [ ] **Step 4: Verify normal path still enters the container**

Run: `./scripts/dev.sh bash -c 'echo $ACH_IN_DEVTOOLS'`
Expected: prints `1` (the var is set inside the container).

- [ ] **Step 5: Commit**

```bash
git add scripts/dev.sh
git commit -m "feat(dev.sh): add ACH_IN_DEVTOOLS recursion guard + docker preflight"
```

### Task 2: Makefile `container_target` macro + `doctor` / `doctor-cluster` / `shell`

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add the routing macro**

After the `CONTAINER_TOOL ?= docker` block (around line 24) add:

```makefile
# --- execution-context routing (explicit opt-in; NO magic-by-prefix) -------
# container_target re-runs a PRIVATE target ($1, conventionally _name) inside
# the devtools container, unless we are already inside it. Each public target
# that needs the Go/helm toolchain calls this explicitly, so `make help` stays
# honest and a future host-only target is never auto-wrapped by accident.
ACH_IN_DEVTOOLS ?= 0
define container_target
	@if [ "$(ACH_IN_DEVTOOLS)" = "1" ]; then \
		$(MAKE) --no-print-directory $(1); \
	else \
		./scripts/dev.sh $(MAKE) --no-print-directory $(1); \
	fi
endef
```

- [ ] **Step 2: Add a `##@ Diagnostics` section with `doctor`, `doctor-cluster`, `shell`**

Add near the top of the `##@ Development` section:

```makefile
##@ Diagnostics

.PHONY: doctor
doctor: ## Fast local preflight: docker, devtools image, socket, cache paths, in-container tools, kubeconfig (if present). No network.
	@echo "== ach doctor (fast) =="
	@docker info >/dev/null 2>&1 && echo "OK   docker daemon reachable" || { echo "FAIL docker daemon unreachable"; exit 1; }
	@test -S /var/run/docker.sock && echo "OK   /var/run/docker.sock present" || echo "WARN /var/run/docker.sock not a socket on host"
	@docker image inspect ach-devtools:latest >/dev/null 2>&1 && echo "OK   ach-devtools:latest present" || echo "WARN ach-devtools:latest absent (built on first ./scripts/dev.sh use)"
	@for d in .gocache/gopath .gocache/build .gocache/envtest .gocache/kube; do test -d "$$d" && echo "OK   $$d" || echo "WARN $$d missing (created on first dev.sh run)"; done
	@./scripts/dev.sh bash -c 'for t in go helm kind kubectl golangci-lint controller-gen setup-envtest; do command -v $$t >/dev/null 2>&1 && echo "OK   (container) $$t" || echo "FAIL (container) $$t MISSING"; done'
	@test -f .gocache/kube/config && echo "OK   kubeconfig present (.gocache/kube/config)" || echo "INFO no kubeconfig yet (run make cluster-up)"

.PHONY: doctor-cluster
doctor-cluster: ## Deep cluster preflight: free ports, values files, chart pins, helm template, image pull/build, kind→docker socket. Touches network.
	$(call container_target,_doctor-cluster)
_doctor-cluster:
	bash scripts/cluster.sh preflight

.PHONY: shell
shell: ## Interactive shell inside the devtools container.
	./scripts/dev.sh bash
```

- [ ] **Step 3: Verify `make doctor` runs and reports tool status**

Run: `make doctor`
Expected: a list of `OK`/`WARN`/`INFO` lines; the in-container tool block prints `OK (container) helm`, `OK (container) kind`, etc. Exit code 0 when docker is up.

- [ ] **Step 4: Verify `make shell` opens the container**

Run: `echo 'whoami; helm version --short' | make shell` (or `make shell` then type the commands)
Expected: runs inside the container; `helm version --short` prints a version.

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "feat(make): add container_target macro + doctor/doctor-cluster/shell"
```

---

## Phase 2 — Public/private split + vocabulary renames (single coherent rename).

> This is one large, mechanical task. Do it in one commit so the Makefile is never in a half-renamed state. The `_name` private targets hold the real recipes; public targets are thin wrappers.

### Task 3: Rename + split test / qa / gen / e2e / build / cluster families

**Files:**
- Modify: `Makefile`
- Modify: `scripts/cluster.sh` (internal `make` calls it shells out to)

- [ ] **Step 1: Rename code-gen targets and update all prerequisites**

Rename target definitions: `manifests:` → `gen-manifests:`, `generate:` → `gen-code:`. Then update EVERY prerequisite reference across the Makefile (`build`, `run`, `envtest-race`, `smoke-idempotency`, `smoke-idempotency-long`, `leak-soak`, `build-installer` all list `manifests generate`). Replace each `manifests generate` / `manifests` / `generate` prerequisite token with `gen-manifests` / `gen-code`. Keep the `gen-crd-ref-docs` name (already in `docs/Makefile`; alias the existing `crd-ref-docs` rendering target — see Task 12-adjacent docs Makefile if needed).

- [ ] **Step 2: Split the test family**

Replace the current `test`, `test-all`, `unit`, `envtest-run`, `envtest-race`, `envtest-fast`, `unit-pkg`, `envtest-pkg`, `test-integration`, `smoke-idempotency`, `smoke-idempotency-long`, `leak-soak` targets with public wrappers + private recipes. Public:

```makefile
.PHONY: test-full
test-full: ## All non-cluster tests (unit + envtest, race-enabled).
	$(call container_target,_test-full)
_test-full: _test-unit _test-envtest

.PHONY: test-unit
test-unit: ## Pure-logic unit tests (~10s warm).
	$(call container_target,_test-unit)
_test-unit: fmt vet
	go test -v -race -shuffle=on -count=1 \
		$$(go list ./... | grep -v -E "/internal/controller|/test/e2e") \
		-coverprofile cover-unit.out

.PHONY: test-envtest
test-envtest: ## Controller envtest with -race (CI gate, ~7m).
	$(call container_target,_test-envtest)
_test-envtest: gen-manifests gen-code fmt vet setup-envtest
	@KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)"; \
	export KUBEBUILDER_ASSETS; \
	scripts/run-envtest-packages.sh --race --timeout 15m --coverprofile cover-envtest.out -- ./internal/controller/...

.PHONY: test-envtest-fast
test-envtest-fast: ## Controller envtest WITHOUT -race (dev loop, ~3m).
	$(call container_target,_test-envtest-fast)
_test-envtest-fast: setup-envtest
	@KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)"; \
	export KUBEBUILDER_ASSETS; \
	scripts/run-envtest-packages.sh --timeout 10m -- ./internal/controller/...

.PHONY: test-integration
test-integration: ## Integration tests (build tag: integration; testcontainers).
	$(call container_target,_test-integration)
_test-integration:
	go test -tags=integration -count=1 -timeout=15m ./...

.PHONY: test-unit-pkg
test-unit-pkg: ## Unit tests for one package. Usage: make test-unit-pkg PKG=./internal/litellm/...
	$(call container_target,_test-unit-pkg)
_test-unit-pkg:
	@test -n "$(PKG)" || (echo "ERROR: PKG=... required" >&2; exit 1)
	go test -v -race -count=1 $(PKG)

.PHONY: test-envtest-pkg
test-envtest-pkg: ## envtest for one package. Usage: make test-envtest-pkg PKG=./internal/controller/... [FOCUS=TestX] [TIMEOUT=10m]
	$(call container_target,_test-envtest-pkg)
_test-envtest-pkg: setup-envtest
	@test -n "$(PKG)" || (echo "ERROR: PKG=... required" >&2; exit 1)
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		script -q /dev/null -c "go test -v -count=1 -timeout $(or $(TIMEOUT),10m) $(if $(FOCUS),-run $(FOCUS),) $(PKG)"

.PHONY: test-smoke-idempotency
test-smoke-idempotency: ## Accelerated AC-R1 idempotency smoke (10s window).
	$(call container_target,_test-smoke-idempotency)
_test-smoke-idempotency: gen-manifests gen-code fmt vet setup-envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -count=1 -timeout 60s -run TestIdempotencyNoMutationSteadyState ./internal/controller/...

.PHONY: test-smoke-idempotency-long
test-smoke-idempotency-long: ## Real 35-min AC-R1 idempotency test (nightly).
	$(call container_target,_test-smoke-idempotency-long)
_test-smoke-idempotency-long: gen-manifests gen-code fmt vet setup-envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -count=1 -timeout 40m -tags=longidempotency -run TestIdempotency35MinReal ./internal/controller/...

.PHONY: test-leak-soak
test-leak-soak: ## REL-03 1000-reconcile leak harness (nightly).
	$(call container_target,_test-leak-soak)
_test-leak-soak: gen-manifests gen-code fmt vet setup-envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -count=1 -timeout 5m -tags=longidempotency -run TestLeakHarness_1000Reconciles ./internal/controller/...
```

- [ ] **Step 3: Rename the qa family** (`lint*`, `security`, `fuzz-*`)

```makefile
.PHONY: qa-lint
qa-lint: ## golangci-lint full sweep.
	$(call container_target,_qa-lint)
_qa-lint: golangci-lint
	$(GOLANGCI_LINT) run

.PHONY: qa-lint-fix
qa-lint-fix: ## golangci-lint with --fix.
	$(call container_target,_qa-lint-fix)
_qa-lint-fix: golangci-lint
	$(GOLANGCI_LINT) run --fix

.PHONY: qa-lint-config
qa-lint-config: ## Verify golangci-lint config.
	$(call container_target,_qa-lint-config)
_qa-lint-config: golangci-lint
	$(GOLANGCI_LINT) config verify

.PHONY: qa-lint-changed
qa-lint-changed: ## Lint only packages touched vs BASE_REF (default origin/main).
	$(call container_target,_qa-lint-changed)
_qa-lint-changed: golangci-lint
	@BASE=$${BASE_REF:-origin/main}; \
	if ! git rev-parse --verify "$$BASE" >/dev/null 2>&1; then \
		BASE=main; \
		git rev-parse --verify "$$BASE" >/dev/null 2>&1 || { \
			echo "ERROR: neither origin/main nor main exists; pass BASE_REF=<ref>" >&2; exit 1; }; \
	fi; \
	CHANGED=$$(git diff --name-only "$$BASE...HEAD" -- '*.go' \
		| xargs -r -n1 dirname | sort -u | sed 's|^|./|; s|$$|/...|'); \
	if [ -z "$$CHANGED" ]; then echo "No Go changes vs $$BASE"; \
	else echo "Linting (vs $$BASE): $$CHANGED"; $(GOLANGCI_LINT) run $$CHANGED; fi

.PHONY: qa-security
qa-security: ## gosec (via lint) + govulncheck + fuzz-short.
	$(call container_target,_qa-security)
_qa-security: _qa-lint
	bash scripts/govulncheck-gate.sh
	$(MAKE) _qa-fuzz-short

.PHONY: qa-fuzz-short
qa-fuzz-short: ## Go fuzz targets, 60s budget each (CI cadence).
	$(call container_target,_qa-fuzz-short)
_qa-fuzz-short:
	@if [ -d ./internal/substitution ]; then go test -run='^$$' -fuzz=FuzzSubstitute -fuzztime=$(FUZZ_TIME_SHORT) ./internal/substitution/...; else echo "qa-fuzz-short: skip — ./internal/substitution absent"; fi
	@if [ -d ./internal/normalize    ]; then go test -run='^$$' -fuzz=FuzzNormalize  -fuzztime=$(FUZZ_TIME_SHORT) ./internal/normalize/...;    else echo "qa-fuzz-short: skip — ./internal/normalize absent";    fi

.PHONY: qa-fuzz-long
qa-fuzz-long: ## Go fuzz targets, 10-minute budget each (nightly).
	$(call container_target,_qa-fuzz-long)
_qa-fuzz-long:
	@if [ -d ./internal/substitution ]; then go test -run='^$$' -fuzz=FuzzSubstitute -fuzztime=$(FUZZ_TIME_LONG)  ./internal/substitution/...; else echo "qa-fuzz-long: skip — ./internal/substitution absent";  fi
	@if [ -d ./internal/normalize    ]; then go test -run='^$$' -fuzz=FuzzNormalize  -fuzztime=$(FUZZ_TIME_LONG)  ./internal/normalize/...;    else echo "qa-fuzz-long: skip — ./internal/normalize absent";     fi
```

- [ ] **Step 4: Rename the build family (two binaries) + images**

```makefile
.PHONY: build-all
build-all: ## Build both binaries (ach services + ach-cli).
	$(call container_target,_build-all)
_build-all: _build-server _build-cli

.PHONY: build-server
build-server: ## Build bin/ach (services: operator/platform-api/forwarder/content-service/migrate).
	$(call container_target,_build-server)
_build-server: gen-manifests gen-code fmt vet
	go build -trimpath -ldflags="-s -w -X github.com/ackstorm/ach/cmd/ach/cmd.Version=$(VERSION)" -o bin/ach ./cmd/ach

.PHONY: build-cli
build-cli: ## Build bin/ach-cli (user CLI).
	$(call container_target,_build-cli)
_build-cli:
	go build -trimpath -ldflags="-s -w -X github.com/ackstorm/ach/cmd/ach-cli/cmd.Version=$(VERSION)" -o bin/ach-cli ./cmd/ach-cli

.PHONY: build-image
build-image: ## Build the ach services container image. Usage: make build-image IMG=ach:e2e
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: build-image-mock
build-image-mock: ## Build the ach-mock:e2e image (LiteLLM-shaped chat-completion mock).
	$(CONTAINER_TOOL) build -t ach-mock:e2e -f test/e2e/mock/Dockerfile test/e2e/mock/

.PHONY: build-image-mcp-echo
build-image-mcp-echo: ## Build the ach-mcp-echo:e2e image (JWT-verifying MCP backend, issue #35).
	$(CONTAINER_TOOL) build -t ach-mcp-echo:e2e -f test/e2e/mcp-echo/Dockerfile .
```

`build-image*` stay host targets (plain `docker build`; host has docker). Do NOT wrap them in `container_target`.

- [ ] **Step 5: Rename cluster family + add `cluster-reset` + `cluster-image-load`**

```makefile
.PHONY: cluster-up cluster-down cluster-reset cluster-sync cluster-status cluster-image-load
cluster-up: ## Bring up canonical kind cluster + hydration (transactional).
	$(call container_target,_cluster-up)
_cluster-up:
	bash scripts/cluster.sh up
cluster-down: ## Tear down canonical kind cluster.
	$(call container_target,_cluster-down)
_cluster-down:
	bash scripts/cluster.sh down
cluster-reset: ## Tear down then bring up a clean cluster.
	$(call container_target,_cluster-reset)
_cluster-reset:
	bash scripts/cluster.sh reset
cluster-sync: ## Reconcile infra/fixtures on a RUNNING cluster (does NOT recreate).
	$(call container_target,_cluster-sync)
_cluster-sync:
	bash scripts/cluster.sh sync
cluster-status: ## Print status of hydration fixtures.
	$(call container_target,_cluster-status)
_cluster-status:
	bash scripts/cluster.sh status
cluster-image-load: ## Build + kind-load the ach image. Usage: make cluster-image-load IMG=ach:e2e
	$(call container_target,_cluster-image-load)
_cluster-image-load: _build-image
	kind load docker-image $(IMG) --name $${KIND_CLUSTER:-ach-e2e}
```

(Note: `_build-image` is not defined as private since `build-image` is host-only; in `_cluster-image-load` — which runs inside the container — call `$(MAKE) build-image` directly instead. Replace the `_cluster-image-load:` prerequisite with a recipe line `$(MAKE) build-image` then `kind load ...`.)

- [ ] **Step 6: Rename e2e family + fix `e2e-focus` double-wrap**

```makefile
.PHONY: e2e-run e2e-focus e2e-full e2e-keep
e2e-run: ## Run e2e suite against an already-up cluster.
	$(call container_target,_e2e-run)
_e2e-run:
	E2E_SKIP_SETUP=1 go test -tags=e2e -v -count=1 -timeout 15m ./test/e2e/...

e2e-focus: ## Focused subtest. RUN='TestX/Sub' (stdlib) OR FOCUS='ginkgo it'.
	$(call container_target,_e2e-focus)
_e2e-focus:
	@test -n "$(RUN)$(FOCUS)" || { echo "ERROR: pass RUN=<go-test -run> OR FOCUS=<ginkgo it>" >&2; exit 1; }
	E2E_SKIP_SETUP=1 go test -tags=e2e -v -count=1 -timeout 5m \
		$(if $(RUN),-run "$(RUN)") ./test/e2e/... \
		$(if $(FOCUS),-args -ginkgo.focus="$(FOCUS)")

e2e-full: ## cluster-up → e2e-run → cluster-down (trap-guaranteed teardown).
	@bash -c '\
	  set -e; \
	  trap "$(MAKE) cluster-down || true" EXIT; \
	  $(MAKE) cluster-up; \
	  $(MAKE) e2e-run'

e2e-keep: ## cluster-up (kept) → e2e-run (no teardown — local iteration).
	$(MAKE) cluster-up
	$(MAKE) e2e-run
```

`e2e-full`/`e2e-keep` are host-level orchestrators that call the auto-wrapping `cluster-*` and `e2e-run` targets — they themselves are NOT wrapped (avoids the old split-brain). The `_e2e-run`/`_e2e-focus` recipes no longer contain `./scripts/dev.sh` (the wrap now happens once, at the public target), fixing the double-nesting.

- [ ] **Step 7: Rename release + keep gates**

```makefile
.PHONY: release-bump
release-bump: ## Internal: bump version across manifests (used by release.yml).
	# (body unchanged from old `bump` target)

.PHONY: release-cut
release-cut: ## Cut a release: empty chore(release) commit, pre-push, push. Usage: make release-cut VERSION=X.Y.Z
	# (body unchanged from old `release` target, but its internal `$(MAKE) pre-push` stays)
```

Update the old `release` target's internal reference if it called `make bump` → `make release-bump`.

- [ ] **Step 8: Update internal `make` calls inside `scripts/cluster.sh`**

In `scripts/cluster.sh` `hydrate_ach`: `make docker-build IMG="${ACH_IMAGE}"` → `make build-image IMG="${ACH_IMAGE}"`. The mcp-echo block (added in `7be51b5`): `make e2e-mcp-echo-build` → `make build-image-mcp-echo`. If any function builds the litellm mock, `make e2e-mock-build` → `make build-image-mock`.

- [ ] **Step 9: Verify the graph resolves (dry-run every public target)**

Run:
```bash
for t in build-all build-server build-cli build-image cluster-up cluster-down cluster-reset cluster-sync cluster-status cluster-image-load \
         test-full test-unit test-envtest test-envtest-fast test-integration test-smoke-idempotency test-leak-soak \
         e2e-run e2e-focus e2e-full e2e-keep qa-lint qa-security qa-fuzz-short gen-code gen-manifests release-bump; do \
  make -n $$t >/dev/null 2>&1 && echo "OK   $$t" || echo "FAIL $$t"; done
```
Expected: every line `OK`. (For `test-unit-pkg`/`test-envtest-pkg`/`e2e-focus` that require args, `make -n` may print the ERROR guard — that is acceptable; confirm no "No rule to make target" errors.)

- [ ] **Step 10: Verify a real build works end-to-end**

Run: `make build-all`
Expected: `bin/ach` and `bin/ach-cli` both produced (runs inside devtools via the macro). `ls -la bin/ach bin/ach-cli` shows both.

- [ ] **Step 11: Commit**

```bash
git add Makefile scripts/cluster.sh
git commit -m "refactor(make): public/private split + vocabulary rename (test-/qa-/gen-/build-/cluster-/e2e-)"
```

---

## Phase 3 — Harden `cluster.sh` (refuse-outside-devtools + transactional).

### Task 4: `cluster.sh` devtools guard, transactional lifecycle, `preflight` subcommand

**Files:**
- Modify: `scripts/cluster.sh`

- [ ] **Step 1: Refuse to run outside devtools**

After `set -euo pipefail` (line 16) insert:

```bash
# cluster.sh is internal plumbing invoked by `make cluster-*`, which routes
# through scripts/dev.sh (where helm/kind/kubectl live). Refuse to run on a
# bare host that may lack these tools — that is the half-created-cluster bug.
if [[ "${ACH_IN_DEVTOOLS:-0}" != "1" ]]; then
    echo "scripts/cluster.sh: run via 'make cluster-up' (must be inside devtools), not directly." >&2
    exit 1
fi
```

- [ ] **Step 2: Make `cmd_up` transactional + add `cmd_sync` / `cmd_reset`**

Replace the `cmd_up`/`cmd_hydrate`/`cmd_down`/`cmd_keep` lines (≈68-72) with:

```bash
cmd_up() {
  local created=0
  if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then created=1; fi
  # Delete a half-created cluster on failure, but ONLY if this run created it
  # and the caller did not opt out (KEEP_ON_FAILURE=1).
  trap 'rc=$?; if [ "$rc" -ne 0 ] && [ "'"$created"'" = "1" ] && [ "${KEEP_ON_FAILURE:-0}" != "1" ]; then echo "[cluster.sh] up failed (rc=$rc) — deleting half-created ${CLUSTER_NAME}" >&2; kind delete cluster --name "${CLUSTER_NAME}" || true; fi' EXIT
  create_cluster; create_namespaces; hydrate_all
  trap - EXIT
}
cmd_sync() {
  # Reconcile infra/fixtures on an EXISTING cluster. Never deletes it unless
  # the caller explicitly opts in with RESET_ON_FAILURE=1.
  trap 'rc=$?; if [ "$rc" -ne 0 ] && [ "${RESET_ON_FAILURE:-0}" = "1" ]; then echo "[cluster.sh] sync failed (rc=$rc) + RESET_ON_FAILURE=1 — deleting ${CLUSTER_NAME}" >&2; kind delete cluster --name "${CLUSTER_NAME}" || true; fi' EXIT
  create_namespaces; hydrate_all
  trap - EXIT
}
cmd_down()  { kind delete cluster --name "${CLUSTER_NAME}" || true; }
cmd_reset() { cmd_down; cmd_up; }
cmd_status(){ print_status; }
```

- [ ] **Step 3: Add a `preflight` function (backs `make doctor-cluster`)**

Add near the other `cmd_*` definitions:

```bash
cmd_preflight() {
  echo "== cluster preflight =="
  for t in kind kubectl helm jq openssl; do command -v "$t" >/dev/null 2>&1 && echo "OK   $t" || { echo "FAIL $t MISSING"; exit 1; }; done
  docker info >/dev/null 2>&1 && echo "OK   docker daemon reachable" || { echo "FAIL docker unreachable"; exit 1; }
  test -d "${VALUES_DIR}" && echo "OK   values dir ${VALUES_DIR}" || { echo "FAIL ${VALUES_DIR} missing"; exit 1; }
  for f in postgres valkey dex litellm; do test -f "${VALUES_DIR}/$f.values.yaml" && echo "OK   values/$f" || echo "WARN values/$f.values.yaml missing"; done
  echo "OK   chart pins: postgres=$(chart_version_of "${VALUES_DIR}/postgres.values.yaml") litellm=$(chart_version_of "${VALUES_DIR}/litellm.values.yaml")"
  # Ports the kind extraPortMappings expose on the host (gateway 8080).
  for p in 8080; do (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null && { echo "WARN port $p already in use"; exec 3>&- ; } || echo "OK   port $p free"; done
}
```

- [ ] **Step 4: Update the dispatch**

Replace the `case "${1:-}"` block (≈493-497) with:

```bash
case "${1:-}" in
  up|sync|down|reset|status|preflight) "cmd_${1}" ;;
  wait_ach) wait_ach ;;
  *) usage ;;
esac
```

Update `usage()` to list `up | sync | down | reset | status | preflight`.

- [ ] **Step 5: Add `--atomic --wait` to non-deadlocking Helm installs**

In each `hydrate_postgres`/`hydrate_valkey`/`hydrate_dex`/`hydrate_litellm`/`hydrate_toolhive` `helm upgrade --install` invocation, add `--atomic --wait --timeout 5m`. Do NOT add it to `hydrate_ach` (the file already documents the LiteLLMConnection boot-order deadlock — leave that one as-is with its explicit post-install `wait_ach`).

- [ ] **Step 6: Verify the guard blocks host execution**

Run: `bash scripts/cluster.sh status`
Expected: FAIL message "run via 'make cluster-up' (must be inside devtools)", exit 1.

- [ ] **Step 7: Verify `make doctor-cluster` runs the preflight**

Run: `make doctor-cluster`
Expected: `OK kind`, `OK helm`, `OK docker daemon reachable`, chart pins line. Exit 0.

- [ ] **Step 8: Verify a real transactional cluster bring-up**

Run: `make cluster-reset` then `make cluster-status`
Expected: cluster recreated; status prints `ach-system` pods Running (operator, platform-api, forwarder, mcp-echo, postgres, valkey).

- [ ] **Step 9: Commit**

```bash
git add scripts/cluster.sh
git commit -m "feat(cluster.sh): devtools guard + transactional up/sync/reset + preflight"
```

---

## Phase 4 — Correctness fixes folded in.

### Task 5: `rebuild-id` annotation on the two test-mock Deployments

**Files:**
- Modify: `deploy/helm/ach/templates/test-mocks.yaml`

- [ ] **Step 1: Add the annotation to `ach-mock-litellm` podTemplate**

In `deploy/helm/ach/templates/test-mocks.yaml`, the `ach-mock-litellm` Deployment's `template.metadata:` (≈line 23) currently has only `labels:`. Insert an `annotations:` block before `labels:`:

```yaml
    metadata:
      annotations:
        {{- with .Values.image.rebuildId }}
        ach.ackstorm.ai/rebuild-id: {{ . | quote }}  # per-rebuild roll trigger (cluster.sh sets $(date +%s)); empty in prod
        {{- end }}
      labels:
        {{- include "ach.commonLabels" . | nindent 8 }}
        app.kubernetes.io/component: mock-litellm
```

- [ ] **Step 2: Add the annotation to `ach-mcp-echo` podTemplate**

For the `ach-mcp-echo` Deployment's `template.metadata:` (≈line 179), same insertion:

```yaml
    metadata:
      annotations:
        {{- with .Values.image.rebuildId }}
        ach.ackstorm.ai/rebuild-id: {{ . | quote }}  # per-rebuild roll trigger (cluster.sh sets $(date +%s)); empty in prod
        {{- end }}
      labels:
        {{- include "ach.commonLabels" . | nindent 8 }}
        app.kubernetes.io/component: mock-mcp-echo
```

- [ ] **Step 3: Verify the template renders the annotation when rebuildId is set**

Run:
```bash
make shell
helm template ach deploy/helm/ach --set testMocks.enabled=true --set testMocks.mcpEcho.enabled=true --set-string image.rebuildId=12345 \
  | grep -B2 -A1 'ach.ackstorm.ai/rebuild-id'
```
Expected: TWO matches under the mock Deployments (plus the three already on operator/platform-api/forwarder), each rendering `ach.ackstorm.ai/rebuild-id: "12345"`.

- [ ] **Step 4: Verify it is omitted when rebuildId is empty (prod safety)**

Run: `helm template ach deploy/helm/ach --set testMocks.enabled=true | grep -c 'rebuild-id' || true`
Expected: `0` (annotation absent when `image.rebuildId=""`).

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/ach/templates/test-mocks.yaml
git commit -m "fix(helm): roll test-mock pods per rebuild via image.rebuildId annotation"
```

### Task 6: Reconcile envtest version pins with `go.mod`

**Files:**
- Modify: `Dockerfile.devtools`

- [ ] **Step 1: Confirm the target versions from `go.mod`**

Run: `grep -E 'k8s.io/api |sigs.k8s.io/controller-runtime ' go.mod`
Expected: `k8s.io/api v0.36.1` and `sigs.k8s.io/controller-runtime v0.24.1`. So `ENVTEST_K8S_VERSION` should be `1.36` and `setup-envtest` should track `release-0.24`.

- [ ] **Step 2: Update the ARGs in `Dockerfile.devtools`**

Change lines 20-21:

```dockerfile
ARG SETUP_ENVTEST_VERSION=release-0.24
ARG ENVTEST_K8S_VERSION=1.36.0
```

- [ ] **Step 3: Rebuild the devtools image and verify envtest assets resolve**

Run:
```bash
ACH_DEVTOOLS_REBUILD=1 ./scripts/dev.sh bash -c 'setup-envtest list | grep -E "1\.36" && echo OK-1.36'
```
Expected: `OK-1.36` (a 1.36.x asset bundle is available/installed).

- [ ] **Step 4: Verify envtest still runs green against the new assets**

Run: `make test-envtest-fast`
Expected: PASS (controller envtest suite green with the 1.36 control-plane binaries).

- [ ] **Step 5: Commit**

```bash
git add Dockerfile.devtools
git commit -m "fix(devtools): pin envtest k8s assets to 1.36 to match go.mod (k8s v0.36.1)"
```

---

## Phase 5 — Gates become check-only + pinned.

### Task 7: `pre-push-check.sh` — pin scanners, fmt-check, tidy restore-on-every-exit

**Files:**
- Modify: `scripts/pre-push-check.sh`
- Modify: `Makefile` (add `fmt-check`)

- [ ] **Step 1: Pin the scanner images by digest**

Replace the mutable tags (lines 76, 86). Pin to the digests currently in use (resolve once with `docker buildx imagetools inspect zricethezav/gitleaks:v8.21.2` / `trufflesecurity/trufflehog:3.95.3` and paste the `@sha256:...`):

```bash
# line ~76
if docker run --rm -v "$REPO_ROOT:/repo:ro" zricethezav/gitleaks:v8.21.2@sha256:<DIGEST> \
# line ~86
if docker run --rm -v "$REPO_ROOT:/pwd:ro" trufflesecurity/trufflehog:3.95.3@sha256:<DIGEST> \
```

(Record the chosen versions in `references/security/` per the repo's pinning convention.)

- [ ] **Step 2: Make `go mod tidy` restore on EVERY exit path**

The current restore (lines 262-263) only runs in the drift branch. Wrap the snapshot+restore in a `trap` so a non-zero `tidy` also restores. After the snapshot `cp` (lines 253-254) add:

```bash
restore_gomod() { [[ -f "$SNAP_DIR/go.mod" ]] && cp "$SNAP_DIR/go.mod" go.mod; [[ -f "$SNAP_DIR/go.sum" ]] && cp "$SNAP_DIR/go.sum" go.sum; }
trap restore_gomod RETURN EXIT
```

(Remove the now-redundant inline restore in the drift branch; the trap covers both branches.)

- [ ] **Step 3: Add a `fmt-check` target (no mutation)**

In the Makefile, add:

```makefile
.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-clean (no mutation).
	$(call container_target,_fmt-check)
_fmt-check:
	@out=$$(gofmt -l $$(git ls-files '*.go' | grep -v -E 'zz_generated|/vendor/')); \
	if [ -n "$$out" ]; then echo "Not gofmt-clean:"; echo "$$out"; exit 1; fi; \
	echo "OK gofmt-clean"
```

Replace the `fmt vet` prerequisites on validation paths (`_test-unit`, `_qa-lint`) with `fmt-check vet` so gates never rewrite the tree. (Leave `fmt` on `_build-server` — building locally may legitimately format.)

- [ ] **Step 4: Verify gates are non-mutating**

Run: `git stash --include-untracked >/dev/null 2>&1 || true; make fmt-check; git status --porcelain`
Expected: `make fmt-check` prints `OK gofmt-clean`; `git status --porcelain` shows no new modifications introduced by the check.

- [ ] **Step 5: Commit**

```bash
git add scripts/pre-push-check.sh Makefile
git commit -m "fix(gates): pin scanner digests, restore go.mod on every exit, add fmt-check"
```

---

## Phase 6 — Delete ach-old targets + fix topology + update workflows.

### Task 8: Remove stale ach-old targets + fix `wait-*`/`logs-*` topology

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Delete the ach-old targets**

Remove these target blocks entirely: `operator-redeploy`, `watch-crs`, `pf-litellm`, `pf-openai-mock`, `pf-kubeai-mock`, `logs-mocks`, `mock-mode`, `docker-load`. (Verified unused in `.github/workflows/`.)

- [ ] **Step 2: Fix the surviving `logs-*` to the current topology**

```makefile
.PHONY: logs-operator logs-platform-api logs-forwarder logs-litellm
logs-operator:     ## Tail operator logs.
	kubectl -n ach-system logs -f --timestamps deploy/ach-operator
logs-platform-api: ## Tail platform-api logs.
	kubectl -n ach-system logs -f --timestamps deploy/ach-platform-api
logs-forwarder:    ## Tail forwarder logs.
	kubectl -n ach-system logs -f --timestamps deploy/ach-forwarder
logs-litellm:      ## Tail LiteLLM logs.
	kubectl -n litellm-system logs -f --timestamps deploy/litellm
```

(These are host targets: they use host `kubectl`, which the dev reads from `.gocache/kube/config` — see docs. Leave them unwrapped.)

- [ ] **Step 3: Verify no dangling references to deleted targets remain**

Run: `grep -nE 'operator-redeploy|watch-crs|pf-(litellm|openai|kubeai)|logs-mocks|mock-mode|docker-load' Makefile scripts/ .github/ || echo "OK none"`
Expected: `OK none`.

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "chore(make): delete ach-old targets, fix logs-* to ach-system topology"
```

### Task 9: Update GitHub Actions workflows to the renamed targets

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/gotests.yml`
- Modify: `.github/workflows/nightly.yml`
- Modify: `.github/workflows/docs.yml`
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: `ci.yml`**

- `:47` `./scripts/dev.sh make lint` → `make qa-lint`
- `:60` `./scripts/dev.sh make unit` → `make test-unit`
- `:82` `./scripts/dev.sh make envtest-fast` → `make test-envtest-fast`
- `:112` `./scripts/dev.sh make security` → `make qa-security`
- `:156` `./scripts/dev.sh make cluster-up` → `make cluster-up`
- `:159` `./scripts/dev.sh make e2e` → `make e2e-run`
- `:167` `./scripts/dev.sh make cluster-down` → `make cluster-down`

(The leading `./scripts/dev.sh` can be dropped because the targets now auto-wrap; keeping it is also safe via the `ACH_IN_DEVTOOLS` guard. Drop it for clarity. Update the surrounding comment lines 144/149/152/158 that mention `make e2e`/`make cluster-up`.)

- [ ] **Step 2: `gotests.yml`**

- `:36` `make test` → `make test-full`

- [ ] **Step 3: `nightly.yml`**

- `:38` `make smoke-idempotency-long` → `make test-smoke-idempotency-long`
- `:61` `make leak-soak` → `make test-leak-soak`
- `:85` `./scripts/dev.sh make fuzz-long` → `make qa-fuzz-long`

- [ ] **Step 4: `docs.yml`**

- `:39` `make docs` → `make docs-build`

- [ ] **Step 5: `release.yml`**

- `:80` `make unit` → `make test-unit`
- `:81` `make envtest-fast` → `make test-envtest-fast`
- `:104` `make bump VERSION=$VERSION` → `make release-bump VERSION=$VERSION`
- `:161` `make generate manifests` → `make gen-code gen-manifests`
- Update the header comments (`:11`, `:12`) that reference `make test` / `make bump`.

- [ ] **Step 6: Verify every workflow references only existing targets**

Run:
```bash
for t in $(grep -rhoE 'make [a-z][a-z0-9_-]+' .github/workflows/ | sed 's/make //' | sort -u); do \
  make -n "$t" >/dev/null 2>&1 && echo "OK   $t" || echo "FAIL $t (no such target)"; done
```
Expected: every line `OK` (args-required targets that print their ERROR guard are acceptable; "No rule to make target" is a failure).

- [ ] **Step 7: Commit**

```bash
git add .github/workflows/
git commit -m "ci: update workflows to renamed make targets (qa-/test-/gen-/build-/e2e-run)"
```

---

## Phase 7 — Documentation (single source of truth).

### Task 10: Create `references/makefile.md`

**Files:**
- Create: `references/makefile.md`

- [ ] **Step 1: Write the reference doc**

Create `references/makefile.md` with: (a) the 3-context model table (A container / B host+docker / C k8s infra); (b) the full command table from this plan's "Final command vocabulary", one row per target with a one-line description; (c) the kubectl/kubeconfig debug note; (d) the `ACH_IN_DEVTOOLS` / `container_target` mechanism explained; (e) the transactional `cluster-up`/`cluster-sync`/`cluster-reset` failure semantics and the `KEEP_ON_FAILURE` / `RESET_ON_FAILURE` knobs.

Include verbatim:

```markdown
## Host requirements

Docker only. Everything else (Go, helm, kind, kubectl, golangci-lint,
setup-envtest) lives in the devtools container and is reached through
`make` targets that auto-route via scripts/dev.sh.

Optional: host `kubectl` may be used for debugging against the kind
cluster. The kubeconfig is written to `./.gocache/kube/config`:

    KUBECONFIG=$PWD/.gocache/kube/config kubectl get pods -n ach-system

This is OPTIONAL and not required by any `make` target or by `make doctor`.
```

- [ ] **Step 2: Verify every table row maps to a real target**

Run:
```bash
for t in $(grep -oE '`(cluster|test|e2e|build|gen|qa|wait|logs|release|docs)-[a-z-]+`|`(doctor|doctor-cluster|shell|pre-commit|pre-push|verify|hooks)`' references/makefile.md | tr -d '`' | sort -u); do \
  make -n "$t" >/dev/null 2>&1 && echo "OK $t" || echo "CHECK $t"; done
```
Expected: targets resolve (args-required ones may show their guard).

- [ ] **Step 3: Commit**

```bash
git add references/makefile.md
git commit -m "docs(references): add makefile command reference + 3-context model"
```

### Task 11: Reconcile `CLAUDE.md` + e2e/examples READMEs

**Files:**
- Modify: `CLAUDE.md`
- Modify: `test/e2e/README.md`
- Modify: `examples/README.md`

- [ ] **Step 1: Add a `references/makefile.md` row to the MANDATORY Reading Table**

In `CLAUDE.md`, add to the MANDATORY Reading Table:

```markdown
| Any `make` command / command organization | `references/makefile.md` (authoritative command list + 3-context model) |
```

- [ ] **Step 2: Replace the stale "runs on host" claims**

In the "Toolchain" / "Waiting for state" / "Test phases" sections, replace every instruction that tells the reader to run `./scripts/dev.sh make X` or `bash scripts/cluster.sh X` directly with the new public `make` target (e.g. `make test-unit`, `make cluster-up`, `make e2e-run`). Replace the claim that cluster/waiters "run on host" with: "all `make` targets auto-route to the correct context; the host needs only docker." Add the optional-kubectl note (`KUBECONFIG=$PWD/.gocache/kube/config kubectl ...`).

- [ ] **Step 3: Update the Test-phases and Waiting-for-state tables**

Rename the command column entries to the new vocabulary (`make unit` → `make test-unit`, `make e2e-full` unchanged, `make e2e-focus`, `make cluster-up`, etc.). Point a footnote at `references/makefile.md` for the full list.

- [ ] **Step 4: Update `test/e2e/README.md` and `examples/README.md`**

Replace bare `make cluster-*` / `make e2e` / `./bin/ach hydrate` invocations with the new vocabulary (`make cluster-up`, `make e2e-run`, `make e2e-keep`; `./bin/ach-cli hydrate` for the CLI now that the binary split landed).

- [ ] **Step 5: Verify no stale command strings survive in docs**

Run:
```bash
grep -rnE 'make (unit|envtest-fast|envtest-run|lint|security|generate|manifests|bump|docs|leak-soak|smoke-idempotency-long|fuzz-long)\b|bash scripts/cluster\.sh' CLAUDE.md test/e2e/README.md examples/README.md || echo "OK no stale command strings"
```
Expected: `OK no stale command strings`.

- [ ] **Step 6: Commit**

```bash
git add CLAUDE.md test/e2e/README.md examples/README.md
git commit -m "docs: reconcile CLAUDE.md + READMEs to make-only command vocabulary"
```

---

## Final verification (run before opening the PR)

- [ ] `make doctor` → all OK/INFO, exit 0.
- [ ] `make doctor-cluster` → all OK, exit 0.
- [ ] `make build-all` → both binaries produced.
- [ ] `make test-unit` → green.
- [ ] `make cluster-reset && make cluster-status` → all `ach-system` pods Running incl. `ach-mcp-echo` (validates rebuild-id + mcp-echo build/load path).
- [ ] `make e2e-keep` then edit a service, `make cluster-image-load && make cluster-sync` → the rebuilt pod actually rolls (validates rebuild-id on all deployments).
- [ ] `make pre-push` → 17 gates pass, working tree unchanged afterwards (validates check-only gates).
- [ ] CI dry-run grep (Task 9 Step 6) → every workflow target resolves.

## Self-review notes

- **Spec coverage:** every overview section maps to a task — 3-context model + macro (Task 2/3), doctor two-level (Task 2/4), kubectl-optional (Task 10/11), cluster-sync rename + transactional up/sync/reset (Task 3/4), rebuild-id (Task 5), envtest pin (Task 6), gates check-only (Task 7), ach-old deletion (Task 8), workflow updates (Task 9), references/makefile.md + CLAUDE.md (Task 10/11).
- **Naming consistency:** private recipes are always `_<public-name>`; `container_target` always receives the `_`-prefixed name; `build-image*` and `logs-*` are intentionally host (unwrapped); `e2e-full`/`e2e-keep` are unwrapped orchestrators that call wrapped children.
- **Known follow-ups (out of scope, note in PR):** `helm-sync`/`build-installer`/`deploy-kustomize-sync*`/`ac-n3-audit`/`samples-audit` keep their current names (internal CI-drift gates, not part of the daily vocabulary); revisit if a future pass wants them under `gen-`/`qa-`.
