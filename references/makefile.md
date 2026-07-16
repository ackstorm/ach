# `make` command reference — the single developer interface

`make` is the deterministic entry point for every developer workflow in
this repo. You do not call `go`, `helm`, `kind`, `kubectl`,
`golangci-lint`, or `setup-envtest` directly, and you do not call
`scripts/dev.sh` or `scripts/cluster.sh` by hand — those are internal
plumbing. Pick a `make` target; it routes itself to the correct
execution context.

## Host requirements

Docker only. Everything else (Go, helm, kind, kubectl, golangci-lint,
setup-envtest) lives in the devtools container and is reached through
`make` targets that auto-route via scripts/dev.sh.

Optional: host `kubectl` may be used for debugging against the kind
cluster. The kubeconfig is written to `./.gocache/kube/config`:

    KUBECONFIG=$PWD/.gocache/kube/config kubectl get pods -n ach-system

This is OPTIONAL and not required by any `make` target or by `make doctor`.

### Host kernel: inotify limits (kind)

kind runs each Kubernetes node as a docker container; kubelet,
containerd, and the API server each consume `fs.inotify` instances. The
common distro default (`fs.inotify.max_user_instances=128`) gets
exhausted partway through hydration and the API server crashes with
`connection refused` MID-bringup (e.g. while helm installs valkey or
litellm, right after postgres). `make cluster-up` / `make cluster-reset`
run an `ensure-inotify` prerequisite (context B, host) that raises the
limits to `INOTIFY_MIN_INSTANCES=512` / `INOTIFY_MIN_WATCHES=524288`
via `sudo -n sysctl -w` if they are below threshold. It is best-effort
and non-fatal: with no passwordless sudo it prints the manual command
and continues. Because inotify is a HOST kernel knob (not namespaced),
this MUST run on the host before cluster-up routes into the devtools
container — hence a plain prerequisite, not a `container_target`. To
persist across reboots, add a drop-in:

    echo -e 'fs.inotify.max_user_instances=512\nfs.inotify.max_user_watches=524288' \
      | sudo tee /etc/sysctl.d/99-kind-inotify.conf && sudo sysctl --system

## The 3-context model

Every target runs in exactly one of three contexts. The Makefile picks
the right one for you; this table is so you understand WHERE a target's
work happens when you debug it.

| Ctx | Where it runs | Tools available | Examples |
|-----|---------------|-----------------|----------|
| **A** Devtools container | inside `ach-devtools:latest` via `scripts/dev.sh` (auto-wrapped by the `container_target` macro) | go, helm, kind, kubectl, golangci-lint, controller-gen, setup-envtest | `test-*`, `qa-*`, `build-server`/`build-cli`/`build-cli-host`/`build-e2e`/`build-all`, `cluster-*`, `e2e-run`/`e2e-focus`, `doctor-cluster`, `shell` |
| **B** Host + docker | directly on the host (needs only the docker CLI/daemon) | docker | `build-image`, `build-image-mock`, `build-image-mcp-echo`, `doctor`, gate orchestrators `pre-push`/`verify`, `e2e-full` (orchestrates context-A children) |
| **C** Kubernetes infra | host `kubectl`/`helm` against the kind cluster (kubeconfig at `./.gocache/kube/config`) | kubectl | `wait-*`, `logs-*` |

> **Why the split is explicit.** Context-A targets opt in to container
> routing by calling the `container_target` macro (see below). There is
> NO magic-by-prefix — a target is only wrapped if it asks to be. That
> keeps `make help` honest and prevents a future host-only target from
> being auto-wrapped by accident.

> **⚠ The generator targets are NOT auto-wrapped.** `gen-code`,
> `gen-manifests`, `gen-crd-ref-docs`, `helm-sync`, and `fix-spdx` call
> `controller-gen`/`crd-ref-docs`/`scripts/*.sh` **directly** (no
> `container_target`), so those tools must already be on PATH. They run
> correctly in two situations: (1) as **prerequisites** of a wrapped
> target — `test-envtest`/`build-server`/etc. list `gen-manifests gen-code`
> as deps, and the wrapper puts the whole chain inside the container; or
> (2) invoked **standalone** as `./scripts/dev.sh make gen-code` (the
> `ACH_IN_DEVTOOLS` guard prevents nesting). A bare `make gen-code` on a
> Go-less host fails with `controller-gen: not found` — that is the missing
> wrap, not a broken toolchain. (Contrast: `gen-*` is deliberately kept out
> of the context-A auto-wrapped list above for exactly this reason.)

## How auto-routing works (`ACH_IN_DEVTOOLS` + `container_target`)

A context-A public target is a thin wrapper over a private `_`-prefixed
target that holds the real recipe:

```makefile
test-unit: ## Pure-logic unit tests (~10s warm).
	$(call container_target,_test-unit)
_test-unit: _fmt-check vet
	go test ...
```

`container_target` expands to: "if `ACH_IN_DEVTOOLS=1`, run
`$(MAKE) _test-unit` directly; otherwise run
`./scripts/dev.sh $(MAKE) _test-unit`." `scripts/dev.sh` sets
`ACH_IN_DEVTOOLS=1` inside the container, so:

- On the **host**: `make test-unit` → `./scripts/dev.sh make _test-unit`
  → container starts → `_test-unit` runs with the Go toolchain present.
- **Inside** the container (`ACH_IN_DEVTOOLS=1`): the macro short-circuits
  to `$(MAKE) _test-unit` — no nested container.

`scripts/dev.sh` itself has a matching guard: if `ACH_IN_DEVTOOLS=1` it
`exec`s the command directly instead of launching another container. So
`./scripts/dev.sh make test-unit` from the host still works (one
container, not two), and CI can keep or drop the explicit `dev.sh`
prefix freely.

## Transactional cluster lifecycle

`scripts/cluster.sh` refuses to run outside the devtools container (it
needs helm/kind/kubectl), so always drive it through `make cluster-*`.

| Target | Semantics | Failure behavior |
|--------|-----------|------------------|
| `cluster-up` | create kind cluster + reconcile dependencies + wait Ready | Keeps the half-created cluster by DEFAULT (forensics-friendly). Opt into teardown with `DELETE_ON_FAILURE=1` — and only if THIS run created the cluster. |
| `cluster-sync` | reconcile infra/fixtures on an ALREADY-running cluster (never recreates) | NEVER deletes the cluster on failure — you are iterating on it. Use `cluster-down`/`cluster-reset` for a clean slate. |
| `cluster-reset` | `cluster-down` then `cluster-up` (clean recreate) | Same as `cluster-up`. |
| `cluster-down` | delete the kind cluster | — |
| `cluster-status` | print kind/helm/pod state | — |
| `cluster-image-load` | `build-image` + `kind load` the ach image (`IMG=...`) | — |

Env knobs: `DELETE_ON_FAILURE=1` tears down a half-created cluster after a
failed `cluster-up` (default is to KEEP it for forensics; only deletes a
cluster THIS run created). `cluster-sync` has no failure knob — it never
deletes. The knob is forwarded into the devtools container by
`scripts/dev.sh` (a bare `VAR=1 make …` env-prefix does NOT otherwise cross
the container boundary). `make doctor-cluster` runs a deep preflight
(tooling, values files, chart pins, port 8080) before you mutate anything.

> Note: the `cluster.sh` per-component reconcile functions are named
> `reconcile_*` (`reconcile_all`, `reconcile_ach`, `reconcile_fixtures`,
> `reconcile_objects` [stage 04], `reconcile_environments` [stage 05], …) and
> `reconcile_all` ends with `verify_all` [stage 06], which blocks until every
> synced object is healthy — shared by both `cluster-up` and `cluster-sync`.
> Unrelated to `ach-cli env hydrate` / `/platform/hydrate`, which materialize
> workspace artifacts.

## Command vocabulary

### Diagnostics
| Target | Ctx | Description |
|--------|-----|-------------|
| `doctor` | B | Fast local preflight: docker, devtools image, cache paths, in-container tools, kubeconfig. No network. |
| `doctor-cluster` | A | Deep cluster preflight: tooling, values files, chart pins, free ports. |
| `ensure-inotify` | B | Raise host `fs.inotify` limits if below kind's needs (best-effort, non-fatal). Auto-run as a prerequisite of `cluster-up`/`cluster-reset`. |
| `shell` | A | Interactive shell inside the devtools container. |
| `clean-cache` | B | Remove `./.gocache`, first `chmod -R u+w` to unlock Go's read-only modcache. Host-only — runs on the host filesystem as your UID (the dir is yours, not root's). Re-created on next `scripts/dev.sh` use. |
| `clean` | B | Full clean: `bin/` + `dist/` + `testbin/` + `cover*.out`, then `clean-cache`. Host-only. Tool + service binaries are re-fetched/rebuilt on next `make`. NOT a docker prune — see `clean-docker`. |
| `clean-docker` | B | Reclaim docker disk: `docker builder prune -af` (all build cache — the big reclaim, tens of GB) + `docker image prune -f` (dangling/untagged images only). Host-only. **SAFE with a kind cluster up** — never touches running containers, tagged images (kind node / `ach-devtools` / `ach`), or volumes. NOT in the `clean` umbrella (kept docker-free) and deliberately NOT `docker system prune` / `image prune -a` (those evict the kind+devtools images → expensive re-pull/re-build). |
| `clear` | B | Reclaim BOTH `./.gocache` and docker disk in one go: runs `clean-cache` + `clean-docker`. Host-only. Same safety as its parts (no cluster/volume/tagged-image eviction). |

> **Per-worktree `.gocache` & "Permission denied" on delete.** `scripts/dev.sh`
> roots every cache under `./.gocache` relative to the workspace, so each git
> worktree gets its own. Go marks the module cache read-only (`0444` files /
> `0555` dirs) by design; since you can't unlink entries from a non-writable
> dir, `rm -rf <worktree>` (or `rm -rf .gocache`) fails with `Permission denied`
> even though every file is owned by **you, not root**. Fix: `make clean-cache`
> (or `chmod -R u+w .gocache && rm -rf .gocache`, or `./scripts/dev.sh go clean
> -modcache -cache`) before removing the worktree.

### Build (`build-`)
| Target | Ctx | Description |
|--------|-----|-------------|
| `build-all` | A | Build both binaries (ach services + ach-cli). |
| `build-server` | A | Build `bin/ach` (operator/platform-api/forwarder/content-service/migrate). |
| `build-cli` | A | Build `bin/ach-cli` (user CLI; **container glibc — NOT host-runnable**). |
| `build-cli-host` | A | Build `bin/ach-cli-host` (static, `CGO_ENABLED=0`; runs on the host outside the devtools container — use for host hydrate/login testing). |
| `build-e2e` | A | Build `bin/ach` + `bin/ach-cli` with `-tags=e2e` (required by Phase 7 SIGKILL-seam tests). |
| `build-image` | B | Build the ach services container image (`IMG=...`). |
| `build-image-mock` | B | Build `ach-mock:e2e` (LiteLLM-shaped mock). |
| `build-image-mcp-echo` | B | Build `ach-mcp-echo:e2e` (JWT-verifying MCP backend, issue #35). |

`deploy/kustomize/manager-rbac.yaml` is a static snapshot — its regen
tooling (`deploy-kustomize-sync{,-check}`) was removed 2026-07-13 as
unwired (no CI/pre-push caller); edit it by hand if `config/rbac/` changes.
Helm (`cluster.sh`, `helm-sync`) is the actual deploy path.

### Code generation (`gen-`)
| Target | Ctx | Description |
|--------|-----|-------------|
| `gen-code` | A | controller-gen DeepCopy methods; then runs `fix-spdx` to self-heal SPDX headers. |
| `gen-manifests` | A | controller-gen CRDs + RBAC + webhook manifests. |
| `fix-spdx` | B | Prepend the SPDX header to any in-scope `*.go` missing it (host script; same scope as pre-push gate 15). Also auto-run by `gen-code`. |
| `gen-crd-ref-docs` | A | Render `docs/api-reference/` from CRD Go types. |
| `helm-render-check` | A | Render-smoke of non-default chart topologies (gateway off / ingress on / G16 standalone CS). Runs in CI lint job. |

### Tests (`test-`)
| Target | Ctx | Description |
|--------|-----|-------------|
| `test-full` | A | All non-cluster tests (unit + envtest, race-enabled). |
| `test-unit` | A | Pure-logic unit tests (~10s warm). |
| `test-envtest` | A | Controller envtest with -race (CI gate, ~7m). |
| `test-envtest-fast` | A | Controller envtest WITHOUT -race (dev loop, ~3m). |
| `test-integration` | A | Integration tests (build tag: integration; testcontainers). |
| `test-unit-pkg PKG=…` | A | Unit tests for one package. |
| `test-envtest-pkg PKG=… [FOCUS=…] [TIMEOUT=…]` | A | envtest for one package. |
| `test-smoke-idempotency` | A | Accelerated AC-R1 idempotency smoke (10s). |
| `test-smoke-idempotency-long` | A | Real 35-min AC-R1 idempotency (nightly). |
| `test-leak-soak` | A | REL-03 1000-reconcile leak harness (nightly). |

### E2E (`e2e-`)
| Target | Ctx | Description |
|--------|-----|-------------|
| `e2e-full` | B | `cluster-up` → `e2e-run`; cluster **kept up** after the run (pass or fail). `make cluster-down` to reclaim. CI does NOT use this target (it tears down via its own `if: always()` step). |
| `e2e-run` | A | Build e2e-tagged binaries, then run the e2e suite against an already-up cluster; executes `./test/e2e` first, then helper packages, so verbose output from the main suite streams sooner. |
| `e2e-focus RUN=…` | A | Focused subtest (stdlib `-run`). |

### QA (`qa-`)
| Target | Ctx | Description |
|--------|-----|-------------|
| `qa-lint` | A | golangci-lint full sweep. |
| `qa-lint-fix` | A | golangci-lint with `--fix`. |
| `qa-lint-config` | A | Verify golangci-lint config. |
| `qa-lint-changed [BASE_REF=…]` | A | Lint only packages touched vs BASE_REF. |
| `qa-security` | A | govulncheck + fuzz-short (gosec runs inside qa-lint — CI lint job + pre-push gate 16). |
| `qa-fuzz-short` | A | Go fuzz targets, 60s budget each. |
| `qa-fuzz-long` | A | Go fuzz targets, 10-min budget each (nightly). |
| `fmt-check` | A | Fail if any Go file is not gofmt-clean (no mutation). |

### Cluster (`cluster-`)
See "Transactional cluster lifecycle" above.

### Waiters (`wait-`) — context C
`wait-operator`, `wait-platform-api`, `wait-forwarder`,
`wait-content-service`, `wait-mcp-echo`, `wait-postgres`, `wait-redis`,
`wait-dex`, `wait-litellm`, `wait-ach`, `wait-cr-ready KIND=… NAME=… NS=…`,
`wait-container NAME=… [TIMEOUT=…]`. Default `WAIT_TIMEOUT=300s`. Never
write ad-hoc `until …; do sleep N; done` loops — add a `wait-*` target.

### Logs (`logs-`) — context C
`logs-operator`, `logs-platform-api`, `logs-forwarder`, `logs-litellm`
(all `ach-system` except litellm in `litellm-system`).

### Release (`release-`)
| Target | Ctx | Description |
|--------|-----|-------------|
| `release-bump VERSION=X.Y.Z` | B | Bump version across manifests (used by release.yml). |
| `release-cut VERSION=X.Y.Z` | B | Empty `chore(release)` commit + pre-push + push to main. |

### Docs (`docs-`)
| Target | Ctx | Description |
|--------|-----|-------------|
| `docs-build` | B | Build the mkdocs site (regenerates api-reference first). |
| `docs-serve` | B | Local mkdocs preview at :8000. |

### Gates (no prefix) — context B
| Target | Description |
|--------|-------------|
| `pre-push` | 18-gate publication check (scanners + lint + unit + SPDX + govulncheck + …). Installed git hook. |
| `verify` | `qa-security` + `pre-push` — full gate bundle. |
| `hooks` | Install the pre-push git hook (and remove any stale pre-commit hook). |

## Debugging against the kind cluster

Host `kubectl` has no context for the kind cluster by default — the
kubeconfig lives at `./.gocache/kube/config` (written by `cluster.sh`
inside the container, bind-mounted to the host). Two ways in:

```bash
# Option 1 — point host kubectl at the kind kubeconfig (optional).
KUBECONFIG=$PWD/.gocache/kube/config kubectl get pods -n ach-system

# Option 2 — run kubectl inside the devtools container.
make shell
kubectl get pods -n ach-system
```

`make logs-*` and `make wait-*` already read this kubeconfig.
