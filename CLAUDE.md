# CLAUDE.md — ach

Surgical **navigation hub** for AI agents — a smart index, not a textbook. Read
the section for your task, then follow the MANDATORY Reading Table into the
deeper docs. Reading the MANDATORY entry before touching the corresponding code
is non-negotiable.

> **Lean on purpose** (loaded every conversation). Deep narrative in
> `references/`: `repo-layout.md`, `release-pipeline.md`, `makefile.md`,
> `troubleshooting.md` (service-specific debugging).

## Documentation hygiene — update docs IN THE SAME COMMIT

When a change alters behavior/contracts/workflows that `CLAUDE.md`,
`references/`, or `docs/` describe, update the affected doc in the SAME commit —
no "docs follow-up PR later". Drift is a bug; fix a stale claim in the change
that revealed it.

| Change | Update |
|--------|--------|
| CRD field / condition / default | `docs/api-reference/` (`make gen-crd-ref-docs`) + `examples/` |
| New/renamed `make` target or default behavior | table here + `references/makefile.md` |
| New `wait-*` / blessed pattern / polling rule | "Waiting for state" table |
| Pre-push gate / govulncheck ack / SPDX rule | "Publication" + `references/security/...` |
| Release pipeline (`release.yml`, goreleaser, bump) | `references/release-pipeline.md` |
| Repo layout / synced-fixture set | `references/repo-layout.md` |
| **Service/domain** failure mode | `references/troubleshooting.md` |
| **Generic/workflow** failure mode | "Common failure modes" here |
| New MANDATORY-read file for a workflow | MANDATORY Reading Table |

## Quick context

ACH — Agent Configuration Hub. Multi-service Kubernetes control plane for
declarative agent configuration management: operator + platform API + forwarder
+ content service + CLI. The long-running services ship as a **single Go binary**
(`ach`) with cobra subcommands selected at process start; the user-facing CLI
ships as a **separate `ach-cli` binary** (login/logout/whoami/config/env/
env-keys/hydrate/admin) that drops the k8s.io/* + controller-runtime deps. Both
share `internal/cli/*`. Go (controller-runtime, k8s.io/* per `go.mod`).

Release plumbing + CI scaffolding grafted from
[ackstorm/alitellm-operator](https://github.com/ackstorm/alitellm-operator)
(Apache-2.0; see `NOTICE` + `references/upstream-sync.md`) — non-code surfaces
only. All Go code, CRDs, and Helm values are original ackstorm material.

## Architecture

```
┌──────────────┐ reconcile ┌─────────────────────────────┐  project    ┌────────────┐
│     CRDs     │──────────▶│       ach operator Pod      │────rows────▶│  Postgres  │
│ (AgentDef…)  │           │ ┌─────────────┐ ┌─────────┐ │  + NOTIFY   │  (SoT for  │
└──────────────┘           │ │  operator   │ │ content │ │             │ ACH state) │
                           │ │ (reconcile) │ │ service │◀┼──READ ROWS──│            │
                           │ └─────────────┘ └────┬────┘ │             └─────┬──────┘
                           └─────────┬────────────┼──────┘                   │
                                     │            │                          │
                                     ▼            ▼ /content/{prompt,…}      │
                          ┌────────────────────┐  ┌───────────────────┐      │
                          │ ach platform-api   │  │ ach forwarder     │      │
                          │ (REST + Dex SSO +  │◀▶│ (JWT trust path,  │      │
                          │  /platform/hydrate)│  │  BIP+Env caches)  │      │
                          └─────────┬──────────┘  └─────────┬─────────┘      │
                                    │                       │                │
                                    └──── READ ROWS + LISTEN ach_*_changed ──┘
```
**Source of truth (Phase D, #34)**: the operator is the only writer to Postgres
(8 projection tables incl. `environments`, `plugins`, `backend_identity_policies`,
`external_refs`, `marketplace_plugins`); platform-api, forwarder, and
content-service READ from Postgres and LISTEN on the `ach_*_changed` channels
emitted by `with_tx_notify`. CRDs are no longer the read path for any
non-operator service. The forwarder's only remaining k8s read is the
`ach-jwt-signing-keys` Secret informer; the platform-api's only remaining k8s
touchpoint is the Dex SSO flow.

Content-service runs as a **sidecar in the operator Pod** (co-located because
the artifact PVC is RWO) — there is no `ach-content-service` Deployment.
operator, platform-api, and forwarder are independent Deployments, each running
the same `ach` image with `args: ["<mode>"]`. Owned CRDs
(`ach.ackstorm.ai/v1alpha1`): `AgentDefinition`, `AgentSession`, `Team`,
`EnvKey`, `BackendIdentityPolicy`, `ContentRef` (`api/` is authoritative).

| Service mode | Subcommand | Owns |
|--------------|------------|------|
| operator        | `ach operator`        | Reconciles ACH CRDs |
| platform-api    | `ach platform-api`    | REST + Dex SSO + `pk_`/`ek_` lifecycle |
| forwarder       | `ach forwarder`       | JWT trust path, `/v1`/`/gemini`/`/mcp`/`/a2a` rewrite |
| content-service | `ach content-service` | Artifact streaming via `sendfile(2)` |
| migrate         | `ach migrate`         | Postgres schema migrations |

User CLI = separate `ach-cli` binary (NOT in the service image): `login`/
`logout`/`whoami`/`config`/`env`/`env-keys`/`hydrate`/`admin`.

Critical paths:
- CRD apply → reconciler → state mutation (k8s + Postgres) → status condition
- `ach-cli login` → platform-api → Dex SSO → `provisionUser` (LiteLLM mint) → `pk_` issuance
- `ach-cli hydrate` → platform-api `/platform/hydrate` → content-service sidecar → workspace
- Environment reconcile → resolve refs against LiteLLM → `POST /v1/access_group`; `Available=True` = `ExecutionResourcesResolved` + `AccessGroupSynced`
- BackendIdentityPolicy → operator RBAC → forwarder cache → per-target JWT mint → upstream

On-disk tree + the **synced-fixtures vs examples** distinction →
`references/repo-layout.md`. (Agents confuse the `test/e2e/cluster/` synced
fixtures the e2e suite asserts against with the curated `examples/` — they are
independent collections.)

## MANDATORY Reading Table

**DO NOT guess. DO NOT skip. Read the doc FIRST.**

| Working on...                          | MUST read first                          |
|----------------------------------------|------------------------------------------|
| Any `make` command / command organization | `references/makefile.md` (command list + 3-context model) |
| Repo layout / synced fixtures / examples | `references/repo-layout.md` + `verify_all` in `scripts/cluster.sh` |
| Release tooling / goreleaser / docs site | `references/release-pipeline.md` + `.goreleaser.yml` + `release.yml` |
| Debugging a service/domain failure     | `references/troubleshooting.md`          |
| New/changed SYNCED CR fixtures         | `test/e2e/cluster/{04-objects,05-environment}/` + `references/repo-layout.md` |
| Curated examples / `ach-cli login` + `hydrate` demo | `examples/README.md` |
| E2E tests (kind cluster + Helm)        | `test/e2e/README.md`                     |
| CI workflows (ci, docs, release, ...)  | `.github/workflows/*.yml` (authoritative); CI matrix below |
| Pre-push gate logic                    | `scripts/pre-push-check.sh`              |
| Publication / first-push procedure     | `PUBLISH.md`                             |
| Helm chart values + defaults           | `deploy/helm/ach/values.yaml` (per-mode toggles) |
| API reference rendering                | `docs/Makefile` + `docs/.crd-ref-docs.yaml` |
| What was grafted from alitellm + how   | `references/upstream-sync.md`            |
| Local testing, SSO login & Gateway     | `references/local-testing-gateway.md`    |
| OLM packaging                          | NOT supported — explicit scope decision (no OperatorHub) |
| Writing/forking the JWT-validating MCP fixture | `test/e2e/mcp-echo/README.md` + `docs/runbooks/writing-an-mcp-backend.md` |
| Changing forwarder JWT mint, JWKS, or `/mcp` / `/a2a` routing | `docs/developer-guide/jwt-forwarder.md` (trust-path contract incl. LiteLLM `extra_headers` opt-in) |

## CI gating

| Event | lint | unit | envtest | security | e2e |
|-------|------|------|---------|----------|-----|
| pull_request → main | ✓ | ✓ | ✓ | ✓ | ✓ |

`ci.yml` is **PR-only** — `pull_request → main` is the single trigger, no
`push:` trigger. Release commits (`chore(release): v*`) → `release.yml`; nightly
→ `nightly.yml`. E2E runs unless the PR is a draft (`if: !draft`). Docs-only PRs
(paths-ignore `**/*.md`, `docs/**`, `references/**`, `FIX*.txt`, `LICENSE`,
`NOTICE`, `CODEOWNERS`, `.gitignore`) skip `ci.yml`. **⚠ PR-only is a real gate
only if branch protection on `main` is enabled** (needs a paid plan / public
repo); until then direct pushes to `main` are unguarded.

## Toolchain — host has NO Go (always Docker)

The host has no Go toolchain on PATH. **Every `make` target auto-routes — the
host needs only docker.** Toolchain targets (`test-*`, `qa-*`, `gen-*`,
`build-server`/`-cli`/`-all`, `cluster-*`, `e2e-run`) wrap into the
`ach-devtools` container via the `container_target` macro; host+docker targets
(`build-image*`, the gates) and `kubectl`-only targets (`wait-*`, `logs-*`) run
on the host. Never prefix a `make` target with `./scripts/dev.sh` — see
`references/makefile.md` for the 3-context model.

```bash
make build-all                   # build both binaries (auto-routes to devtools)
make shell                       # interactive shell in the devtools container
./scripts/dev.sh go build ./...  # raw go, when no make target fits
```

`scripts/dev.sh` mounts the repo + docker socket, preserves host UID:GID, and
persists Go caches under `.gocache/` (per-workspace, so **each git worktree gets
its own**). CI uses a pre-baked GHCR image keyed by
`sha256(Dockerfile.devtools)[:12]` (local-build fallback on miss). Tool versions
pinned in `Dockerfile.devtools` + `go.mod`.

**Keep the environment clean — `make clean-cache` after each feature.** Go marks
its module cache read-only (`0444`/`0555`), so a plain `rm -rf .gocache` (or
`git worktree remove`) fails with `Permission denied` — the files are owned by
**you, not root**; you just can't unlink from a non-writable dir. `make
clean-cache` (host-only: `chmod -R u+w` then `rm -rf ./.gocache`) clears it
safely. Recommended once a feature/worktree is done so stale per-worktree caches
don't pile up; re-created on next `scripts/dev.sh` use. (`make clean` is the
broader umbrella — also drops `bin/`/`dist/`/`testbin/`/coverage on top of the
cache.)

## Test phases

`references/makefile.md` is the authoritative command list. Common phases:

| Phase | Command | When |
|-------|---------|------|
| `make test-unit`        | pure-logic, ~10s warm | every iteration |
| `make qa-lint-changed`  | golangci-lint scoped to touched pkgs | every iteration |
| `make qa-lint`          | golangci-lint full sweep | before commit (pre-commit hook) |
| `make test-envtest`     | controller-runtime envtest (race), ~7m | before commit on controller changes |
| `make test-envtest-fast`| envtest without -race, ~3m | dev inner loop |
| `make e2e-full`         | kind + Helm + stdlib testing, ~6m | final gate before commit |
| `make e2e-focus`        | `RUN='TestPhase4Promotion/SC11a'` (stdlib) OR `FOCUS='ginkgo it'` (legacy) | dev loop on one sub-test |
| `make qa-security`      | gosec + govulncheck + fuzz-short, ≤6m | in-container; before commit |
| `make pre-commit`       | qa-lint-changed + test-unit | host-only; every `git commit` once `make hooks` installed |
| `make pre-push`         | gitleaks + trufflehog + 18 gates | host-only; before push |

- Umbrellas: `test-full` = `test-unit` + `test-envtest`; `verify` =
  `qa-security` + `pre-push`; `make hooks` installs both git hooks. Inner loop:
  `make test-unit-pkg PKG=...`, `make test-envtest-pkg PKG=... [FOCUS=TestX]`.
- `pre-push`/`pre-commit` are **host-only** — never via `./scripts/dev.sh`.
  Don't run them by hand; the installed hook fires the same script (exception:
  after a `--no-verify` push).

## Waiting for state — use blessed make targets

Naked polling loops (`until ...; do sleep N; done`) are **banned**: when the
target disappears the predicate is unreachable and the agent hangs. Use a
`wait-*` target (each uses: `kubectl wait`, `kubectl rollout status`,
`timeout N docker logs -f <cid> | grep -m1`, or `docker wait <cid>`).

| Need | Target |
|------|--------|
| CR condition Ready | `make wait-cr-ready KIND=... NAME=... NS=...` |
| Operator / Platform API / Forwarder Ready | `make wait-operator` / `wait-platform-api` / `wait-forwarder` |
| Content Service container Ready (sidecar in operator Pod) | `make wait-content-service` |
| All ach Deployments Ready | `make wait-ach` (also `ach-local-gateway` when present — a dev/test add-on) |
| Postgres / Redis(Valkey) / Dex | `make wait-postgres` / `wait-redis` / `wait-dex` |
| Container exit + PASS/FAIL marker | `make wait-container NAME=<c>` (`TIMEOUT=<s>`, default 600) |
| Full cluster hydration | `make cluster-up` (synchronous; do not poll after) |
| Reconcile infra/fixtures on a running cluster | `make cluster-sync` (rebuilds + rolls ach pods) |

All listed `wait-*` exist (plus `wait-litellm`/`wait-mcp-echo`/`wait-mocks` for
test backends). Default `WAIT_TIMEOUT=300s`. If none cover a new wait need,
**add a new `wait-*` target** — targets are the contract, not ad-hoc loops.

## Publication — pre-commit and pre-push gates are non-negotiable

Remote: `git@github.com:ackstorm/ach.git`. Two hook stages so "oops, CI failed
lint" is paid locally before the commit lands:

- `pre-commit` (fast): `qa-lint-changed` + `test-unit`, every `git commit` once
  `make hooks` installed. `--no-verify` only for justified WIP; the full sweep
  still fires on push.
- `pre-push` (full): **18-gate** publication check. lint + unit live INSIDE the
  18 (gates 16+17), so push is safe even if pre-commit was bypassed.

The 18 hard gates (failure blocks push): gitleaks + trufflehog
(`origin/main..HEAD`; allowlist `.gitleaks.toml`) · large files >2 MB ·
sensitive patterns (`.env`, `*.pem`, `*.key`, kubeconfig) · LICENSE + README ·
origin-remote match · govulncheck ack-list 1:1 (`scripts/govulncheck-gate.sh`,
list at `references/security/govulncheck-acknowledged.md`) · `go mod tidy` drift
· per-file SPDX header · full golangci-lint · `make test-unit` · chart CRD drift
(`make helm-sync-check` — `crd-sources/` vs `config/crd/bases`, #44). Fix the
root cause — never `--no-verify` (it skips ONLY the local hook; CI reruns the
gates).

## Common failure modes (generic / workflow)

Service-specific debugging (content-service 404, forwarder JWT 401,
SourceReachable rate-limit, AccessGroupSynced, hydrate-golden diff, mcp-echo,
ConflictWithUIRow, stale image roll) → **`references/troubleshooting.md`**. The
seven below are the cross-cutting workflow traps:

### ❌ Prefixing a `make` target with `./scripts/dev.sh`
`./scripts/dev.sh make test-unit` works but the prefix is redundant —
`make test-unit` ✅ auto-routes into devtools via `container_target`. If docker
is down you get a clear preflight error, not `command not found: go`. The prefix
still works (`ACH_IN_DEVTOOLS` guard prevents nesting).

### ❌ Naked polling loop
```bash
until docker logs $(docker ps -q -f ancestor=mock) | grep -q PASS; do sleep 10; done
```
✅ Bounded wait (or a `wait-*` target):
`timeout 600 docker logs -f $cid 2>&1 | grep -m1 -E "PASS|FAIL" || { echo FAIL >&2; exit 1; }`
WHY: when the container exits and is removed, `docker ps -q` is empty,
`docker logs` errors forever, and the loop never exits.

### ❌ Invalid Postgres / Redis URLs in dev hydration
`ach operator --postgres-url postgres://localhost/ach` → panic. ✅ Always pass
full DSNs: `postgres://ach:ach@postgres.ach.svc:5432/ach?sslmode=disable`. WHY:
pgx parses `postgres://` strictly; a missing user/port/scheme panics on startup,
looking like a network issue.

### ❌ Running a service mode without its subcommand
`./bin/ach` prints help and exits 0 — the operator never starts. ✅ Each
long-running mode needs its subcommand (`operator`, `platform-api`, `forwarder`,
`content-service`, `migrate`). WHY: a Deployment omitting `args: ["<mode>"]`
CrashLoopBackOffs / silently restarts.

### ❌ Pushing without the gate
`git push --no-verify` bypasses the local hook ONLY (CI still runs it). ✅ Let
the installed hook gate (`make hooks`), or `make pre-push` then push. WHY:
pushed secrets / license-header drift / govulncheck regressions cannot be
un-true'd from public history. The 18-gate script is the contract.

### ❌ Kubectl from host against the kind cluster
`kubectl get pods` → context not found. ✅ Go through devtools:
`./scripts/dev.sh kubectl get pods`. WHY: the kind kubeconfig lives at
`/workspace/.gocache/kube/config` — inside the container.

### ❌ Editing files via relative paths when cwd is the wrong repo
Relative-path writes silently hit a sibling repo (`ach-old/`,
`alitellm-operator/`) if cwd is wrong — "succeeding" while leaving this repo
unchanged. ✅ Use absolute paths; verify with `pwd && git remote -v` (expect
`ackstorm/ach`).

### ❌ Editor save vs `ach-cli hydrate` runtime-config — user edit silently lost
`ach-cli hydrate` reads the adapter runtime-config file (`.claude/settings.json`,
`.gemini/settings.json`, `.codex/config.toml`, `.opencode/opencode.json`),
deep-merges ACH's keys, and atomic-renames the result back. The `<achDir>/lock`
flock excludes other ach-cli processes — NOT other tools. A concurrent editor
save (auto-format on file change, manual write) between hydrate's read and
hydrate's rename overwrites the merge with the user's pre-merge edit; on the
NEXT hydrate ACH re-merges its keys back in, so the engine self-heals — but the
user's edit made during the hydrate window is silently lost.

✅ Avoid saving the runtime-config files while `ach-cli hydrate` is running. If
you need to edit the config concurrently, run hydrate to completion first
(`echo $?` == 0), THEN edit. There's no telemetry for the race; the user-visible
symptom is "my edit reverted." Documented as a known v1 trade-off (security
2.4 — accept-disposition); a future mtime-recheck would close it.

## Repository-specific patterns

- **Single-binary cobra layout**: each long-running mode is a subcommand under
  `cmd/ach/cmd/<mode>.go` wiring its `internal/<service>/` impl. New modes go
  here, NEVER as a second `cmd/<x>/main.go` tree.
- **Reconciler shape**: `internal/controller/<kind>_controller.go` follows
  `Reconcile(ctx, req) (Result, error)` + `meta.SetStatusCondition`.
  Side-effecting I/O lives in dedicated `internal/` packages for
  unit-testability — the reconciler owns the k8s state machine; service packages
  own the I/O.
- **Per-mode Helm Deployments**: operator, platform-api, forwarder are
  independent Deployments sharing one image; content-service is the **second
  container in the operator Pod** (RWO artifacts PVC forces co-location). Toggle
  topology via `deploy/helm/ach/values.yaml` `*.enabled` flags. Each Deployment
  carries `args: ["<mode>"]`.
- **Environment two-axis status**: `ExecutionResourcesResolved`
  (Plugin/Prompt/Artifact closed-set) + `AccessGroupSynced` (LiteLLM: names →
  IDs each reconcile, then `POST /v1/access_group`). Composite `Available=True`
  rolls both up — that's what `ach-cli hydrate` / the demo gate on.
- **BIP + Environment forwarder read-path (Postgres-as-SoT, #34)**: operator
  projects BIPs/Environments → tables, emitting `NOTIFY ach_*_changed` from the
  same tx via `with_tx_notify`. The forwarder's `internal/forwarder/bipcache` +
  `internal/forwarder/envstore` each run a `db.Listener` + 5-min periodic
  refresh (LISTEN/NOTIFY is at-most-once on session loss). JWT mint reads the
  in-memory cache — no per-request Postgres/k8s hit.
- **SPDX-only license headers**: every `*.go` (outside `vendor/`,
  `zz_generated*.go`, `mock_*.go`) starts with
  `// SPDX-License-Identifier: Apache-2.0` (pre-push gate enforces;
  `hack/boilerplate.go.txt` feeds controller-gen via `make gen-code`).
- **govulncheck ack-list**: stdlib HIGH advisories awaiting upstream Go fixes
  live in `references/security/govulncheck-acknowledged.md`; the gate enforces a
  1:1 match (drift either way blocks push).
- **Upstream-sync ledger**: `references/upstream-sync.md` records every file
  grafted from `alitellm-operator` + adaptations. New grafts MUST add a row.

## E2E debug loop

`make e2e-full` is the full-suite final gate (~10 min). It **keeps the cluster
up** after the run — pass OR fail — so a red run can be diagnosed live; reclaim
with `make cluster-down`. (`e2e-keep` is now just an alias of `e2e-full`.) CI
does NOT use this target — it runs `cluster-up`/`e2e-run`/`cluster-down` as
separate steps with an `if: always()` teardown, so CI never leaks a cluster.
Iterate with the kept-cluster loop (full diagnosis recipe in
`test/e2e/README.md`):

```bash
make e2e-full                                 # cluster-up + e2e-run, cluster KEPT (pass or fail)
make logs-operator                            # diagnose live
make e2e-focus FOCUS="rateLimits composite"   # focused subtest
make cluster-sync                             # after a code edit: rebuild image + roll ach pods
make cluster-down && make e2e-full            # clean-room start; cluster kept after
```

Never push a change touching `internal/controller|platformapi|forwarder|
contentservice/`, `api/v1alpha1/`, `deploy/helm/ach/`, or `test/e2e/` without
confirming E2E green.

## External references

Project docs may lag — verify current APIs with Context7 / DeepWiki / WebSearch:
- **controller-runtime / kubebuilder / client-go / cobra**: signatures pinned
  to `go.mod`.
- **Dex SSO**: WebFetch `https://dexidp.io/docs/` (OIDC connector/discovery).
- **goreleaser v2**: https://goreleaser.com — watch the `dockers` → `dockers_v2`
  migration (deferred; configs validate today).
- **Claude Code plugin / marketplace schemas**: JSON Schemas at schemastore.org
  (narrative: code.claude.com/docs/en/plugin-marketplaces). The parser
  (`internal/controller/ach/marketplace_parse.go`) follows the real schema with
  one drift ack: `url`-Kind entries carry an optional `path` (→ `git-subdir`).

