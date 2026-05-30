# Image URL to use all building/pushing image targets
VERSION ?= dev
IMG ?= ghcr.io/ackstorm/ach:$(VERSION)

# MODES is the list of cobra subcommands that ship as long-running services.
# Used by waiters, Helm value generation, and per-mode RBAC tooling — NOT by build.
# Build produces a SINGLE image ($(IMG)); each Deployment runs it with args: ["<mode>"].
MODES := operator platform-api forwarder content-service migrate

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set).
# Guarded with `command -v go` so host-only targets (hooks, cluster-up, ...) do
# not surface a "make: go: No such file or directory" error: go lives inside the
# devtools container (see scripts/dev.sh), not on PATH.
ifeq (,$(shell command -v go >/dev/null 2>&1 && go env GOBIN))
GOBIN=$(shell command -v go >/dev/null 2>&1 && go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# --- execution-context routing (explicit opt-in; NO magic-by-prefix) -------
# container_target re-runs a PRIVATE target ($1, conventionally _name) inside
# the devtools container, unless we are already inside it. Each public target
# that needs the Go/helm toolchain calls this explicitly, so `make help` stays
# honest and a future host-only target is never auto-wrapped by accident.
#
# $(MAKEOVERRIDES) forwards the caller's command-line variable assignments
# (e.g. PKG=… FOCUS=… RUN=… TIMEOUT=… BASE_REF=…). It is REQUIRED on the
# dev.sh path: scripts/dev.sh only forwards an explicit -e allowlist into the
# container, so MAKEFLAGS (which normally carries command-line overrides to a
# sub-make) does NOT cross the docker boundary. Without this, arg-taking
# wrappers like test-envtest-pkg/e2e-focus would see empty $(PKG)/$(RUN).
ACH_IN_DEVTOOLS ?= 0
define container_target
	@if [ "$(ACH_IN_DEVTOOLS)" = "1" ]; then \
		$(MAKE) --no-print-directory $(1) $(MAKEOVERRIDES); \
	else \
		./scripts/dev.sh $(MAKE) --no-print-directory $(1) $(MAKEOVERRIDES); \
	fi
endef

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build-all

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

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

##@ Development

# NOTE: paths is scoped to ./api/... and ./internal/... (NOT the kubebuilder
# default ./...) because the repo also hosts a separate Go module under
# verification/ for the Phase 0 spike (plan 01-01). With paths="./...",
# controller-gen descends into verification/ and fails on its read-only
# Go module cache. ./internal/... was added in plan 01-04 so the
# NoOpReconciler's RBAC markers (+kubebuilder:rbac:...) are picked up.
.PHONY: gen-manifests
gen-manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	# crd:allowDangerousTypes=true is required because Team.spec.budget.limit
	# is *float64 (per spec §6.7 "Float64 precision is adopted for v1alpha1").
	# controller-gen rejects float types by default; the spec explicitly chose
	# this contract, so the flag is the documented kubebuilder escape hatch.
	$(CONTROLLER_GEN) rbac:roleName=ach-manager-role crd:allowDangerousTypes=true webhook paths="./api/..." paths="./internal/..." output:crd:artifacts:config=config/crd/bases

.PHONY: gen-code
gen-code: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths="./api/..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-clean (no mutation).
	$(call container_target,_fmt-check)
_fmt-check:
	@out=$$(gofmt -l $$(git ls-files '*.go' | grep -v -E 'zz_generated|/vendor/')); \
	if [ -n "$$out" ]; then echo "Not gofmt-clean:"; echo "$$out"; exit 1; fi; \
	echo "OK gofmt-clean"

.PHONY: test-full
test-full: ## All non-cluster tests (unit + envtest, race-enabled).
	$(call container_target,_test-full)
_test-full: _test-unit _test-envtest

.PHONY: test-unit
test-unit: ## Pure-logic unit tests (~10s warm).
	$(call container_target,_test-unit)
_test-unit: _fmt-check vet
	# `go test` defaults to -p=GOMAXPROCS across packages (speedup-ideas §5 confirmed).
	# Exclusions: internal/controller (envtest), test/e2e (cluster).
	# Anything else is pure-logic.
	go test -v -race -shuffle=on -count=1 \
		$$(go list ./... | grep -v -E "/internal/controller|/test/e2e") \
		-coverprofile cover-unit.out

.PHONY: test-envtest
test-envtest: ## Controller envtest with -race (CI gate, ~7m).
	$(call container_target,_test-envtest)
_test-envtest: gen-manifests gen-code fmt vet setup-envtest
	@# Runs envtest packages concurrently. Green runs show package status
	@# plus slow tests; failed packages dump their captured logs.
	@KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)"; \
	export KUBEBUILDER_ASSETS; \
	scripts/run-envtest-packages.sh --race --timeout 15m --coverprofile cover-envtest.out -- ./internal/controller/...

.PHONY: test-envtest-fast
test-envtest-fast: ## Controller envtest WITHOUT -race (dev loop, ~3m).
	$(call container_target,_test-envtest-fast)
_test-envtest-fast: setup-envtest
	@# Runs envtest packages concurrently. Green runs show package status
	@# plus slow tests; failed packages dump their captured logs.
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
	# `script -q /dev/null -c "..."` fakes a TTY so -v output streams.
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
qa-lint-changed: ## Lint packages touched vs BASE_REF (default origin/main), incl. untracked *.go.
	$(call container_target,_qa-lint-changed)
_qa-lint-changed: golangci-lint
	@BASE=$${BASE_REF:-origin/main}; \
	if ! git rev-parse --verify "$$BASE" >/dev/null 2>&1; then \
		BASE=main; \
		git rev-parse --verify "$$BASE" >/dev/null 2>&1 || { \
			echo "ERROR: neither origin/main nor main exists; pass BASE_REF=<ref>" >&2; exit 1; }; \
	fi; \
	CHANGED=$$( { git diff --name-only "$$BASE...HEAD" -- '*.go'; \
		git ls-files --others --exclude-standard -- '*.go'; } \
		| xargs -r -n1 dirname | sort -u | sed 's|^|./|; s|$$|/...|'); \
	if [ -z "$$CHANGED" ]; then \
		echo "No Go changes vs $$BASE"; \
	else \
		echo "Linting (vs $$BASE): $$CHANGED"; \
		$(GOLANGCI_LINT) run $$CHANGED; \
	fi

##@ Security

FUZZ_TIME_SHORT ?= 60s
FUZZ_TIME_LONG  ?= 10m

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

.PHONY: pre-commit
pre-commit: ## Host-only — fast local gate (lint-changed + unit). Runs automatically on `git commit` once `make hooks` is installed.
	./scripts/pre-commit-check.sh

.PHONY: pre-push
pre-push: ## Host-only — 17-gate pre-publication check (gitleaks + trufflehog + lint + unit + SPDX + govulncheck + ...). Uses docker on host; do NOT call via ./scripts/dev.sh.
	./scripts/pre-push-check.sh

.PHONY: verify
verify: ## Host-only — full pre-publication gate bundle: in-container security + host pre-push. Single command for all gates.
	$(MAKE) qa-security
	$(MAKE) pre-push

.PHONY: hooks
hooks: ## Install git hooks (pre-commit + pre-push).
	./scripts/install-hooks.sh

##@ Release

.PHONY: release-bump
release-bump: ## Internal: bump version across all manifests. Used by release.yml. Prefer `make release-cut VERSION=X.Y.Z` for local cuts.
	@test -n "$(VERSION)" || (echo "ERROR: VERSION=X.Y.Z required (no leading 'v')" >&2; exit 1)
	@echo "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$$' || \
		(echo "ERROR: VERSION must be semver without leading 'v' (e.g. 0.0.3 or 0.1.0-rc1)" >&2; exit 1)
	@echo "Bumping all manifests to v$(VERSION)..."
	@sed -i -E 's/^version: .*/version: $(VERSION)/' deploy/helm/ach/Chart.yaml
	@sed -i -E 's/^appVersion: .*/appVersion: v$(VERSION)/' deploy/helm/ach/Chart.yaml
	@sed -i -E 's|^([[:space:]]+)tag: v.*|\1tag: v$(VERSION)|' deploy/helm/ach/values.yaml
	@sed -i -E 's|^([[:space:]]+)newTag: v.*|\1newTag: v$(VERSION)|' config/manager/kustomization.yaml
	@sed -i -E 's|^([[:space:]]+)newTag: v.*|\1newTag: v$(VERSION)|' deploy/kustomize/kustomization.yaml
	@echo "Manifests bumped to v$(VERSION)."

.PHONY: release-cut
release-cut: ## Cut a release: empty `chore(release): vX.Y.Z` commit, run pre-push, push to main. Usage: make release-cut VERSION=X.Y.Z
	@test -n "$(VERSION)" || (echo "ERROR: VERSION=X.Y.Z required (no leading 'v')" >&2; exit 1)
	@echo "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$$' || \
		(echo "ERROR: VERSION must be semver without leading 'v' (e.g. 0.0.3 or 0.1.0-rc1)" >&2; exit 1)
	@branch=$$(git rev-parse --abbrev-ref HEAD); \
	test "$$branch" = "main" || (echo "ERROR: must be on main (current: $$branch)" >&2; exit 1)
	@git diff --quiet || (echo "ERROR: working tree dirty; commit or stash first" >&2; exit 1)
	@git diff --cached --quiet || (echo "ERROR: index has staged changes; commit or reset first" >&2; exit 1)
	@git fetch origin main --quiet
	@local=$$(git rev-parse HEAD); remote=$$(git rev-parse origin/main); \
	test "$$local" = "$$remote" || (echo "ERROR: local main differs from origin/main; rebase or pull first" >&2; exit 1)
	git commit --allow-empty -m "chore(release): v$(VERSION)"
	$(MAKE) pre-push
	git push origin main
	@echo ""
	@echo "release.yml is now running. Watch with:"
	@echo "  gh run watch \$$(gh run list --branch main --limit 1 --json databaseId --jq '.[0].databaseId')"

##@ Build

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

.PHONY: run
run: gen-manifests gen-code fmt vet ## Run a controller from your host.
	go run ./cmd/ach

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: build-image
build-image: ## Build the ach services container image. Usage: make build-image IMG=ach:e2e
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name workspace-builder
	$(CONTAINER_TOOL) buildx use workspace-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm workspace-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: gen-manifests gen-code kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	# Standalone kustomize install bundle (kubectl apply -f dist/install.yaml).
	# The image override is a no-op when config/manager already pins
	# ghcr.io/ackstorm/ach; kept for kubebuilder-convention parity.
	cd config/manager && $(KUSTOMIZE) edit set image controller=controller:latest
	$(KUSTOMIZE) build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: gen-manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: gen-manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: gen-manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
CRD_REF_DOCS ?= $(LOCALBIN)/crd-ref-docs

## Tool Versions
KUSTOMIZE_VERSION ?= v5.5.0
CONTROLLER_TOOLS_VERSION ?= v0.17.0
# ENVTEST_VERSION + ENVTEST_K8S_VERSION are derived from go.mod by shelling
# out to `go list`. The host has no `go` on PATH (see scripts/dev.sh), so
# we guard each call with `command -v go` and fall back to an empty value
# on the host. Targets that actually need these vars run via dev.sh, where
# go IS available — make then re-evaluates the Makefile inside the
# container and the derivation succeeds. This avoids the cosmetic
# `make: go: No such file or directory` noise on host-only targets such
# as `make hooks`, `make cluster-up`, etc.
#
# (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell command -v go >/dev/null 2>&1 && go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
# (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell command -v go >/dev/null 2>&1 && go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v1.62.2
CRD_REF_DOCS_VERSION ?= v0.2.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: crd-ref-docs
crd-ref-docs: $(CRD_REF_DOCS) ## Download crd-ref-docs locally if necessary (renders docs/api-reference from CRD Go types).
$(CRD_REF_DOCS): $(LOCALBIN)
	$(call go-install-tool,$(CRD_REF_DOCS),github.com/elastic/crd-ref-docs,$(CRD_REF_DOCS_VERSION))

##@ Documentation
include docs/Makefile

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef

# --- helm / chart packaging ---

.PHONY: helm-sync
helm-sync: gen-manifests ## Sync generated CRDs into the Helm chart's crd-sources/ (the chart's ONLY generated surface — per-mode Deployments are hand-authored templates).
	# CRDs land in crd-sources/ (NOT the reserved crds/ dir name) so the
	# templates/crds.yaml loop can range over them and emit each one as a
	# Helm-managed template. helm-inject-crd-annotation.py adds
	# `helm.sh/resource-policy: keep` so `helm uninstall` preserves
	# CRDs and the user's CR data.
	cp -f config/crd/bases/ach.ackstorm.ai_*.yaml deploy/helm/ach/crd-sources/
	python3 scripts/helm-inject-crd-annotation.py deploy/helm/ach/crd-sources/*.yaml

.PHONY: helm-sync-check
helm-sync-check: helm-sync ## CI gate: fail if `make helm-sync` left uncommitted CRD drift in the chart.
	@if ! git diff --quiet deploy/helm/ach/crd-sources/; then \
	  echo "CHART CRD DRIFT: deploy/helm/ach/crd-sources/ is out of sync with config/crd/bases. Run \`make helm-sync\` and commit."; \
	  git --no-pager diff --stat deploy/helm/ach/crd-sources/; \
	  exit 1; \
	fi

# --- deploy/kustomize snapshot ---

.PHONY: deploy-kustomize-sync
deploy-kustomize-sync: ## Regenerate deploy/kustomize/manager-rbac.yaml from config/rbac/ (operator-runtime + metrics-auth subset).
	bash scripts/render-deploy-kustomize-rbac.sh

.PHONY: deploy-kustomize-sync-check
deploy-kustomize-sync-check: deploy-kustomize-sync ## CI gate: fail if `make deploy-kustomize-sync` produced uncommitted diff (drift between config/rbac/ and the bundled snapshot).
	@if ! git diff --quiet deploy/kustomize/manager-rbac.yaml; then \
	  echo "DEPLOY KUSTOMIZE DRIFT: deploy/kustomize/manager-rbac.yaml is out of sync with config/rbac/. Run \`make deploy-kustomize-sync\` and commit."; \
	  git diff deploy/kustomize/manager-rbac.yaml; \
	  exit 1; \
	fi

.PHONY: ac-n3-audit
ac-n3-audit: ## SCOPE-03 / AC-N3 static gate: fail if any non-test .go file references /user/ or /key/ as string literals.
	@hits=$$(grep -RnE '"/user/|"/key/' --include='*.go' --exclude='*_test.go' internal/ cmd/ 2>/dev/null \
	  | grep -v '^\s*//' || true); \
	if [ -n "$$hits" ]; then \
	  echo "AC-N3 VIOLATION: forbidden path-prefix string literals found:"; \
	  echo "$$hits"; \
	  exit 1; \
	fi; \
	echo "ac-n3-audit: PASS (zero /user/* or /key/* literals in non-test source)"

# --- samples-audit ---

.PHONY: samples-audit
samples-audit: ## DEPLOY-02: fail the build if any sample manifest contains a TODO(user) placeholder (per plan 07-03 audit gate).
	@hits=$$(grep -RIE 'TODO\(user\)' config/samples/ 2>/dev/null || true); \
	if [ -n "$$hits" ]; then \
	  echo "DEPLOY-02 VIOLATION: TODO(user) placeholders found in samples:"; \
	  echo "$$hits"; \
	  exit 1; \
	fi; \
	echo "samples-audit: PASS (zero TODO(user) placeholders in config/samples/)"

##@ E2E (cluster + mocks)

.PHONY: build-image-mock
build-image-mock: ## Build the ach-mock:e2e image (LiteLLM-shaped chat-completion mock).
	$(CONTAINER_TOOL) build -t ach-mock:e2e -f test/e2e/mock/Dockerfile .

.PHONY: build-image-mcp-echo
build-image-mcp-echo: ## Build the ach-mcp-echo:e2e image (JWT-verifying MCP backend, issue #35).
	$(CONTAINER_TOOL) build -t ach-mcp-echo:e2e -f test/e2e/mcp-echo/Dockerfile .

# --- inotify preflight (host-only) -----------------------------------------
# kind runs each Kubernetes node as a docker container; kubelet, containerd,
# and the API server each consume fs.inotify instances. The common distro
# default (max_user_instances=128) gets exhausted partway through hydration
# and the API server crashes with "connection refused" mid-bringup (e.g. while
# helm installs valkey, right after postgres). These are HOST kernel knobs
# (not namespaced), so they must be raised on the host BEFORE cluster-up
# routes into the devtools container — hence a plain prerequisite, not a
# container_target. Override the thresholds with INOTIFY_MIN_* if needed.
INOTIFY_MIN_INSTANCES ?= 512
INOTIFY_MIN_WATCHES   ?= 524288

.PHONY: ensure-inotify
ensure-inotify: ## Host-only: raise fs.inotify limits if below kind's needs (best-effort, non-fatal).
	@if [ "$(ACH_IN_DEVTOOLS)" = "1" ]; then exit 0; fi; \
	cur_i=$$(sysctl -n fs.inotify.max_user_instances 2>/dev/null || echo 0); \
	cur_w=$$(sysctl -n fs.inotify.max_user_watches 2>/dev/null || echo 0); \
	if [ "$$cur_i" -ge "$(INOTIFY_MIN_INSTANCES)" ] && [ "$$cur_w" -ge "$(INOTIFY_MIN_WATCHES)" ]; then \
	  echo "OK   inotify limits sufficient (instances=$$cur_i watches=$$cur_w)"; \
	else \
	  echo "INFO raising inotify limits for kind (instances=$$cur_i->$(INOTIFY_MIN_INSTANCES), watches=$$cur_w->$(INOTIFY_MIN_WATCHES))"; \
	  if sudo -n sysctl -w fs.inotify.max_user_instances=$(INOTIFY_MIN_INSTANCES) fs.inotify.max_user_watches=$(INOTIFY_MIN_WATCHES) >/dev/null 2>&1; then \
	    echo "OK   inotify limits raised (live only; add an /etc/sysctl.d drop-in to persist across reboots)"; \
	  else \
	    echo "WARN could not raise inotify limits (no passwordless sudo). kind may die mid-bringup with 'connection refused'."; \
	    echo "WARN raise them manually, then re-run:"; \
	    echo "       sudo sysctl -w fs.inotify.max_user_instances=$(INOTIFY_MIN_INSTANCES) fs.inotify.max_user_watches=$(INOTIFY_MIN_WATCHES)"; \
	  fi; \
	fi

.PHONY: cluster-up cluster-down cluster-reset cluster-sync cluster-status cluster-image-load
cluster-up: ensure-inotify ## Bring up canonical kind cluster + hydration (transactional).
	$(call container_target,_cluster-up)
_cluster-up:
	bash scripts/cluster.sh up
cluster-down: ## Tear down canonical kind cluster.
	$(call container_target,_cluster-down)
_cluster-down:
	bash scripts/cluster.sh down
cluster-reset: ensure-inotify ## Tear down then bring up a clean cluster.
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
_cluster-image-load:
	$(MAKE) build-image
	kind load docker-image $(IMG) --name $${KIND_CLUSTER:-ach-e2e}

##@ Waiters (use these; never write ad-hoc until/while loops)

WAIT_TIMEOUT ?= 300s

.PHONY: wait-cr-ready
wait-cr-ready: ## Wait for a CR Ready condition. Usage: make wait-cr-ready KIND=litellmconnection NAME=default NS=default
	@test -n "$(KIND)" -a -n "$(NAME)" -a -n "$(NS)" || { echo "ERROR: KIND= NAME= NS= all required" >&2; exit 1; }
	kubectl -n $(NS) wait --for=condition=Ready --timeout=$(WAIT_TIMEOUT) $(KIND)/$(NAME)

.PHONY: wait-operator
wait-operator: ## Wait operator Deployment Ready (bounded).
	kubectl -n ach-system rollout status deploy/ach-operator --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-litellm
wait-litellm: ## Wait LiteLLM Deployment Ready (bounded).
	kubectl -n litellm-system rollout status deploy/litellm --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-mocks
wait-mocks: ## Wait all mock Pods Ready (bounded).
	kubectl -n mocks wait --for=condition=Ready --timeout=$(WAIT_TIMEOUT) pod --all

.PHONY: wait-postgres
wait-postgres: ## Wait for postgres StatefulSet Ready
	kubectl -n ach-system rollout status statefulset/ach-postgres --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-redis
wait-redis: ## Wait for redis (valkey) StatefulSet Ready
	kubectl -n ach-system rollout status statefulset/valkey-primary --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-dex
wait-dex: ## Wait for dex pod Ready
	kubectl wait --for=condition=Ready pod -l app=dex -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-platform-api
wait-platform-api: ## Wait for platform-api Deployment Available
	kubectl rollout status deploy/ach-platform-api -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-forwarder
wait-forwarder: ## Wait for forwarder Deployment Available
	kubectl rollout status deploy/ach-forwarder -n ach-system --timeout=$(WAIT_TIMEOUT)

# Context C (host kubectl), consistent with the sibling wait-* targets.
# Mirrors scripts/cluster.sh wait_ach but does NOT shell into cluster.sh,
# which refuses to run outside the devtools container — calling it here made
# `make wait-ach` fail with "run via 'make cluster-up'... not directly".
.PHONY: wait-ach
wait-ach: ## Wait for all ach Deployments (operator+platform-api+forwarder+local-gateway) Ready.
	@rc=0; \
	for d in ach-operator ach-platform-api ach-forwarder ach-local-gateway; do \
	  kubectl -n ach-system rollout status deploy/"$$d" --timeout=$(WAIT_TIMEOUT) || rc=$$?; \
	done; \
	if [ "$$rc" -ne 0 ]; then \
	  echo "one or more ach Deployments failed to become Ready — dumping pods:" >&2; \
	  kubectl -n ach-system get pods >&2 || true; \
	  exit "$$rc"; \
	fi

.PHONY: wait-content-service
wait-content-service: ## Wait for content-service container (co-located in operator Pod) Ready (bounded).
	# Co-located topology: content-service is the second container in
	# the ach-operator Pod (RWO PVC forces co-location; Plan 01-08 + 05-07).
	# There is NO ach-content-service Deployment — the operator Deployment
	# rollout encompasses both containers and the Pod readinessProbe already
	# verifies CS :8082/healthz, so rollout=Ready ⇒ both containers serving.
	kubectl rollout status deploy/ach-operator -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-mcp-echo
wait-mcp-echo: ## Wait for ach-mcp-echo Deployment Available (bounded) (issue #35)
	kubectl rollout status deploy/ach-mcp-echo -n ach-system --timeout=$(WAIT_TIMEOUT)

.PHONY: wait-container
wait-container: ## Wait for named container exit + PASS/FAIL marker. Usage: make wait-container NAME=<container> [TIMEOUT=600]
	@test -n "$(NAME)" || { echo "ERROR: NAME= required" >&2; exit 1; }
	@cid=$$(docker ps -q -f name=$(NAME)); \
	test -n "$$cid" || { echo "ERROR: no running container named '$(NAME)'" >&2; exit 1; }; \
	timeout $${TIMEOUT:-600} docker logs -f $$cid 2>&1 \
		| grep -m1 -E "PASS|FAIL|ok\s+github|--- FAIL|Ginkgo ran" \
		|| { echo "FAIL: marker not seen within $${TIMEOUT:-600}s (container may have exited early)" >&2; exit 1; }

.PHONY: logs-operator logs-platform-api logs-forwarder logs-litellm
logs-operator:     ## Tail operator logs.
	kubectl -n ach-system logs -f --timestamps deploy/ach-operator
logs-platform-api: ## Tail platform-api logs.
	kubectl -n ach-system logs -f --timestamps deploy/ach-platform-api
logs-forwarder:    ## Tail forwarder logs.
	kubectl -n ach-system logs -f --timestamps deploy/ach-forwarder
logs-litellm:      ## Tail LiteLLM logs.
	kubectl -n litellm-system logs -f --timestamps deploy/litellm

# --- E2E harness wiring ----------------------------------------------------
# Everything reaches the synced cluster through the gateway (localhost:8080;
# kind extraPortMapping + devtools --network=host). Data-plane URLs ARE the
# gateway base (it routes /v1 /content /platform /mcp /a2a /.well-known /dex);
# metrics get distinct /metrics/<svc> routes (a bare /metrics can't
# disambiguate four services behind one base). Phase gates default ON — the
# synced cluster closes the LiteLLM seed gap (TODO §16) and the alpine+git
# operator image closes the git gap, so the formerly-pending tests run. All
# overridable on the command line (e.g. `make e2e-focus ACH_E2E_PHASE9=0`).
ACH_BASE_URL   ?= http://localhost:8080
ACH_E2E_PHASE4 ?= 1
ACH_E2E_PHASE5 ?= 1
ACH_E2E_PHASE6 ?= 1
ACH_E2E_PHASE9 ?= 1
ACH_E2E_SC11C  ?= 1

# Shared env block prefixed onto EVERY e2e go-test invocation (run + focus)
# so URL-gated and phase-gated tests actually exercise the synced cluster.
# Make variables are not exported to recipe shells unless referenced inline,
# so both _e2e-run and _e2e-focus expand this explicitly.
E2E_RUN_ENV = \
	ACH_BASE_URL=$(ACH_BASE_URL) \
	ACH_FORWARDER_URL=$(ACH_BASE_URL) \
	ACH_CONTENT_SERVICE_URL=$(ACH_BASE_URL) \
	ACH_PLATFORM_API_URL=$(ACH_BASE_URL) \
	ACH_FORWARDER_METRICS_URL=$(ACH_BASE_URL)/metrics/forwarder \
	ACH_CONTENT_METRICS_URL=$(ACH_BASE_URL)/metrics/content \
	ACH_PLATFORM_METRICS_URL=$(ACH_BASE_URL)/metrics/platform \
	ACH_OPERATOR_METRICS_URL=$(ACH_BASE_URL)/metrics/operator \
	ACH_E2E_PHASE4=$(ACH_E2E_PHASE4) ACH_E2E_PHASE5=$(ACH_E2E_PHASE5) \
	ACH_E2E_PHASE6=$(ACH_E2E_PHASE6) ACH_E2E_PHASE9=$(ACH_E2E_PHASE9) \
	ACH_E2E_SC11C=$(ACH_E2E_SC11C)

.PHONY: e2e-run e2e-focus e2e-full e2e-keep
e2e-run: ## Run e2e suite against an already-up cluster.
	$(call container_target,_e2e-run)
_e2e-run:
	# E2E_SKIP_SETUP=1 hands cluster lifecycle to scripts/cluster.sh; without
	# it, test/e2e/e2e_suite_test.go's TestMain calls setupCluster() which
	# tries `kind load docker-image ach-operator:latest` — a per-binary
	# image name from ach-old that does not exist under the single-binary
	# `ach` layout. cluster-up handles the actual image load. The synced
	# cluster is reached entirely through the gateway — zero port-forwards.
	E2E_SKIP_SETUP=1 $(E2E_RUN_ENV) \
		go test -tags=e2e -v -count=1 -timeout 20m ./test/e2e/...

e2e-focus: ## Focused subtest. RUN='TestPhase4Promotion/SC11a' (stdlib) OR FOCUS='ginkgo it' (legacy).
	$(call container_target,_e2e-focus)
_e2e-focus:
	@test -n "$(RUN)$(FOCUS)" || { echo "ERROR: pass RUN=<go-test -run pattern> OR FOCUS=<ginkgo it>" >&2; exit 1; }
	# `-args` is required for ginkgo: without it, `go test` parses the value after
	# `-ginkgo.focus=` as a package path and reports "no Go files in /workspace".
	# Same gateway-URL + phase-gate env as _e2e-run so focused runs of
	# formerly-gated tests actually execute (not skip).
	E2E_SKIP_SETUP=1 $(E2E_RUN_ENV) \
	    go test -tags=e2e -v -count=1 -timeout 5m \
	    $(if $(RUN),-run "$(RUN)") ./test/e2e/... \
	    $(if $(FOCUS),-args -ginkgo.focus="$(FOCUS)")

e2e-full: ## cluster-up → e2e-run → cluster-down (trap-guaranteed teardown).
	@bash -c '\
	  set -e; \
	  trap "$(MAKE) cluster-down || true" EXIT; \
	  $(MAKE) cluster-up; \
	  $(MAKE) e2e-run'

e2e-keep: ## cluster-up (kept) → e2e-run (NO teardown — local iteration).
	$(MAKE) cluster-up
	$(MAKE) e2e-run
