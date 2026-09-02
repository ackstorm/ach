# ACHAgent environment forwarding implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add inherited Pod-native environment variables to `ACHAgent`, selectively forward them into channel preparation, and ship patch releases of both the harness and operator.

**Architecture:** `AgentProfile.spec.env` and `ACHAgent.spec.env` merge by name with the agent entry winning atomically. The operator injects the merged environment into the harness Pod and resolves each `channels[].prepare.forwardEnv` name into the existing ach-agent `prepare.env` or `prepare.secretEnv` JSON contract. The harness contract stays unchanged; its patch fixes cancellation cleanup for prepare subprocess groups.

**Tech Stack:** Go/controller-runtime/Kubernetes CRDs, Python/asyncio/Pydantic, pytest, envtest, Make.

**Spec:** `docs/superpowers/specs/2026-09-02-achagent-env-forwarding-design.md`

## Global Constraints

- This is a breaking `v1alpha1` API change: remove `AgentProfile.spec.extraEnv` and `PrepareSpec.env`/`secretEnv`.
- Only `value` and `valueFrom.secretKeyRef` are accepted in profile/agent env.
- `ACHAgent.spec.env` wins over profile env by name.
- Unknown `prepare.forwardEnv` names are ignored and remain unset.
- Secret plaintext never enters the generated ConfigMap.
- Release ach-agent `v0.13.1` before ACH operator `v0.8.1`.

---

### Task 1: Harness cancellation safety

**Files:**
- Modify: `../ach-agent/tests/test_prepare.py`
- Modify: `../ach-agent/src/ach_agent/boot/prepare.py`
- Modify: `../ach-agent/CHANGELOG.md`

**Interfaces:**
- Consumes: existing `run_prepare(PrepareBlock, MessageEvent, Path)`.
- Produces: cancellation re-raises `asyncio.CancelledError` after killing and reaping the prepare process group.

- [ ] Add an async test that starts a long-running prepare child, cancels `run_prepare`, asserts cancellation propagates, and verifies the child process is gone.
- [ ] Run `./scripts/dev.sh uv run pytest tests/test_prepare.py -q` and confirm the test fails because the child survives.
- [ ] Add the minimal shared process-group cleanup used by timeout and cancellation; keep timeout metrics and errors unchanged.
- [ ] Re-run the focused test and add a changelog entry under `[unreleased]`.
- [ ] Commit as `fix(prepare): reap scripts when invocation is cancelled`.

### Task 2: Operator API and rendering

**Files:**
- Modify: `api/ach/v1alpha1/agentprofile_types.go`
- Modify: `api/ach/v1alpha1/achagent_types.go`
- Modify: `internal/agentrender/render.go`
- Modify: `internal/agentrender/render_test.go`
- Modify: `internal/agentrender/schema_test.go`
- Modify: `internal/controller/ach/achagent_workload.go`
- Modify: `internal/controller/ach/achagent_workload_test.go`

**Interfaces:**
- Produces: `ResolveEnv(agent, profile []corev1.EnvVar) []corev1.EnvVar`.
- Produces: rendered literals in `PrepareBlock.Env`, secret aliases in `PrepareBlock.SecretEnv`, and generated Pod secret refs.

- [ ] Replace existing prepare tests with failing tests for profile inheritance, atomic agent override, literal forwarding, secret aliasing, and ignored unknown names.
- [ ] Run `make test-unit-pkg PKG=./internal/agentrender/...` and confirm failures are caused by the absent API.
- [ ] Replace `extraEnv` with `env`, add agent `env`, replace prepare maps with `forwardEnv`, and add CRD markers for merge/validation semantics.
- [ ] Implement the name merge and resolve prepare allowlists into the unchanged harness config.
- [ ] Update Pod env assembly to inject merged entries plus generated prepare secret aliases.
- [ ] Run focused agentrender and controller unit packages until green.

### Task 3: Secret lifecycle and generated surfaces

**Files:**
- Modify: `internal/controller/ach/achagent_controller.go`
- Modify: `internal/controller/ach/achagent_envtest_test.go`
- Generate: `api/ach/v1alpha1/zz_generated.deepcopy.go`
- Generate: `config/crd/bases/ach.ackstorm.ai_{achagents,agentprofiles}.yaml`
- Generate: `deploy/helm/ach/crd-sources/ach.ackstorm.ai_{achagents,agentprofiles}.yaml`
- Generate: `docs/api-reference/ach.ackstorm.ai.md`
- Modify: `examples/agent-runtime/{profile,agent}.yaml`
- Modify: `examples/agent-runtime/README.md`

**Interfaces:**
- Consumes: merged env from Task 2.
- Produces: secret validation/hash/watch coverage for both profile and agent env.

- [ ] Add envtest coverage proving inherited/overridden secrets reconcile, hash, and enqueue on Secret changes.
- [ ] Run the focused envtest and confirm the missing lifecycle behavior fails.
- [ ] Pass the profile into referenced-secret resolution and include merged env secret refs in reconciliation and Secret watch mapping.
- [ ] Regenerate code, CRDs, Helm mirror, API reference, and update the runnable examples.
- [ ] Run unit, focused envtest, generation drift, lint, and full non-cluster verification.
- [ ] Commit operator implementation and generated documentation.

### Task 4: Patch releases

**Files:**
- Modify: `../ach-agent/pyproject.toml`, `../ach-agent/uv.lock`, `../ach-agent/CHANGELOG.md`
- Release-generated in ACH: `deploy/helm/ach/Chart.yaml`, `deploy/helm/ach/values.yaml`, `config/manager/kustomization.yaml`

**Interfaces:**
- Produces: ach-agent `v0.13.1`, then ACH operator `v0.8.1`.

- [ ] Run fresh full verification in ach-agent, bump to `0.13.1`, sync the lock, commit, push implementation and bump, cut the release, and verify the GitHub release/tag/image.
- [ ] Rebase ACH `main` on the current remote if release automation advanced it, preserving the design and implementation commits.
- [ ] Run fresh `make pre-push`, push operator implementation, cut `0.8.1`, and verify the GitHub release/tag/manifests.
- [ ] Report commit hashes, tags, release URLs, and exact test evidence.
