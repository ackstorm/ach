# CLAUDE.md — ach

Surgical reference card for AI agents working in this repository. Read the
sections relevant to your task. Reading MANDATORY Reading Table entries
before touching the corresponding code is non-negotiable.

## Documentation hygiene — keep this file and `docs/` in sync with the code

Whenever a change alters behavior, contracts, or workflows that this
`CLAUDE.md` or the user-facing docs describe, update the affected
documentation in the SAME commit as the code change. No "docs follow-up
PR later".

Triggers requiring a docs/CLAUDE.md update:
- New or changed CRD field, condition, or default value → update
  `docs/api-reference/` (`make gen-crd-ref-docs`) and any CRD examples
  under `examples/`.
- New `make` target, renamed target, or changed default behavior →
  update the relevant table in this file (Test phases, Waiting for
  state, ...) and `docs/` if user-facing.
- New `wait-*` target, blessed pattern, or polling rule → update the
  "Waiting for state" table and the bash anti-patterns section.
- New pre-push gate, govulncheck ack-list entry, or SPDX/license rule →
  update the "Publication" section and `references/security/...`.
- Release pipeline change (`release.yml`, goreleaser configs, bump
  flow) → update the "Release pipeline" section.
- New common failure mode encountered while debugging → add a
  `### ❌ ... ✅ ...` entry under "Common failure modes".
- New MANDATORY-read file for a workflow → add a row to the
  "MANDATORY Reading Table".

If a doc claim is found stale during work, fix it in the same change
that revealed the staleness. Drift is a bug, not tech debt.

## Quick context

ACH — Agent Configuration Hub. Multi-service Kubernetes control plane for
declarative agent configuration management: operator + platform API +
forwarder + content service + CLI. All shipped as a **single Go binary**
(`ach`) with cobra subcommands selected at process start. Written in Go
(controller-runtime, k8s.io/* per `go.mod`).

Selected release plumbing + CI scaffolding grafted from
[ackstorm/alitellm-operator](https://github.com/ackstorm/alitellm-operator)
(Apache-2.0). See `NOTICE` for attribution. The graft covers non-code
surfaces: CI workflows, goreleaser configs, mkdocs site scaffold,
community files. All Go code, CRDs, and Helm chart values are original
ackstorm material (CRD set and reconciler shape derived from the ach-old
planning corpus at `/home/jcm/Projects/ach-old`).

## Architecture

```
┌──────────────┐ reconcile ┌─────────────────────────────┐ Postgres/Redis ┌────────────┐
│     CRDs     │──────────▶│       ach operator Pod      │───────────────▶│ Hub state  │
│ (AgentDef…)  │           │ ┌─────────────┐ ┌─────────┐ │                └────────────┘
└──────────────┘           │ │  operator   │ │ content │ │
                           │ │ (reconcile) │ │ service │ │  HTTP /content/{prompt,plugin,artifact}
                           │ └─────────────┘ └────┬────┘ │  (sendfile(2) streaming, RWO PVC)
                           └─────────┬────────────┼──────┘
                                     │            │
                                     ▼            ▼
                          ┌────────────────────┐  ┌───────────────────┐
                          │ ach platform-api   │  │ ach forwarder     │
                          │ (REST + Dex SSO +  │◀▶│ (JWT trust path,  │
                          │  /platform/hydrate)│  │  BIP read-cache)  │
                          └────────────────────┘  └───────────────────┘
```
Content-service runs as a **sidecar container in the operator Pod**
(co-located because the artifact PVC is RWO); there is no separate
`ach-content-service` Deployment. operator, platform-api, and
forwarder are independent Deployments. Each Deployment runs the same
`ach` image with `args: ["<mode>"]`.

Owned CRDs (API group `ach.ackstorm.ai/v1alpha1`): `AgentDefinition`,
`AgentSession`, `Team`, `EnvKey`, `BackendIdentityPolicy`, `ContentRef`
(subject to ROADMAP revision — `api/` is authoritative).

Each long-running service runs the same `ach` image with a cobra subcommand
as `args:`:

| Mode             | Subcommand          | Owns                                    |
|------------------|---------------------|-----------------------------------------|
| operator         | `ach operator`      | Reconciles ACH CRDs                     |
| platform-api     | `ach platform-api`  | REST + Dex SSO + `pk_`/`ek_` lifecycle  |
| forwarder        | `ach forwarder`     | JWT trust path, `/v1`/`/gemini`/`/mcp`/`/a2a` rewrite |
| content-service  | `ach content-service` | Artifact streaming via `sendfile(2)`  |
| migrate          | `ach migrate`       | Postgres schema migrations              |

Critical paths:
- CRD apply → reconciler → state mutation (k8s + Postgres) → status condition update
- `ach login` → platform-api → Dex SSO → `provisionUser` (LiteLLM user/team mint) → `pk_` issuance
- `ach hydrate` → platform-api `/platform/hydrate` → content-service sidecar (`/content/{prompt,plugin,artifact}` routes) → workspace materialization
- Environment reconcile → resolve `mcpServers` / `a2aAgents` / `authorizedTeams` against LiteLLM → `POST /v1/access_group` → `AccessGroupSynced=True`
- Environment status → composite `Available=True` rollup over `ExecutionResourcesResolved` + `AccessGroupSynced`
- BackendIdentityPolicy apply → operator writes RBAC for forwarder → forwarder watches BIPs (read-path cache) → JWT mint uses per-target identity
- LLM request → forwarder → BIP-driven JWT mint → upstream LLM/MCP backend
- Operator restart → informer resync → drift reconciliation

## Repository layout (post-graft)

```
ach/
├── .github/                 ← alitellm-graft; reconciled for ach
│   ├── workflows/           CI / release / docs / govulncheck / labeler / nightly
│   ├── CODEOWNERS, dependabot.yml, labeler.yml, ISSUE_TEMPLATE/, PR template
├── .goreleaser.yml          ← stable release config (single-binary)
├── .goreleaser.prerelease.yml   ← prerelease (alpha/beta/rc tags)
├── .goreleaser.snapshot.yml ← main-branch snapshot builds
├── Dockerfile               ← runtime image (golang builder → alpine + git)
├── Dockerfile.devtools      ← devtools container (scripts/dev.sh)
├── Dockerfile.goreleaser    ← release image, consumed by goreleaser
├── api/                     ← CRD Go types (ach.ackstorm.ai/v1alpha1)
├── cmd/ach/main.go          ← single-binary entrypoint
├── cmd/ach/cmd/              ← cobra root + per-mode subcommands
│   ├── root.go               (Version, root cmd)
│   ├── operator.go, platform_api.go, forwarder.go,
│   ├── content_service.go, migrate.go
├── internal/                ← controllers + service implementations
│   ├── controller/           controller-runtime reconcilers
│   ├── platformapi/, forwarder/, contentservice/   service-mode code
├── config/                  ← kubebuilder kustomize overlays
├── deploy/helm/ach/         ← Helm chart shipped on release (per-mode toggles)
├── deploy/kustomize/        ← raw kustomize bundle (install.yaml source)
├── docs/                    ← mkdocs site (api-reference auto-gen)
├── examples/                ← runnable CR fixtures + hydrate-demo driver
│   ├── 01-litellmconnection.yaml  LiteLLMConnection seed
│   ├── 04-environment-demo.yaml   Environment referencing the ext-ref CRs
│   ├── 05-pluginmarketplace-anthropic.yaml  real upstream canary (5-Kind parser landed via #16)
│   ├── 06-plugin-caveman.yaml     Plugin from JuliusBrussee/caveman
│   ├── 07-prompt-claudecode-leak.yaml  Prompt from asgeirtj/system_prompts_leaks
│   ├── 08-artifact-openclaw-templates.yaml  Artifact (directory scope)
│   ├── 09-backendidentitypolicy-context7.yaml  BIP jwt-on
│   ├── 10-backendidentitypolicy-duplicate.yaml  BIP duplicate-target demo
│   ├── hydrate-demo.sh            Stand-in for `ach login` + `ach hydrate` CLI
│   └── hydrate.json               Last-known-good /platform/hydrate output
├── hack/boilerplate.go.txt  ← SPDX one-liner, prepended to generated files
├── references/              ← agent-facing internal docs (NOT on public site)
│   ├── upstream-sync.md      ← what was grafted from alitellm and adapted
│   └── security/govulncheck-acknowledged.md
├── scripts/                 ← dev.sh, cluster.sh, pre-push-check.sh, ...
├── test/                    ← e2e + utils
├── ROADMAP.md, CHANGELOG.md, SECURITY.md, MAINTAINERS.md, CONTRIBUTING.md
└── PROJECT, README.md, LICENSE, NOTICE, PUBLISH.md
```

## MANDATORY Reading Table

| Working on...                          | MUST read first                          |
|----------------------------------------|------------------------------------------|
| New CR fixtures / hydrate-demo path    | `examples/README.md`                     |
| E2E tests (kind cluster + Helm)        | `test/e2e/README.md`                     |
| CI workflows (ci, docs, release, ...)  | `.github/workflows/*.yml` (authoritative); CI gating matrix in this file |
| Release tooling (goreleaser, signing)  | `.goreleaser.yml` + `release.yml` workflow |
| Pre-push gate logic                    | `scripts/pre-push-check.sh` (gate list)  |
| Publication / first-push procedure     | `PUBLISH.md`                             |
| Helm chart values + defaults           | `deploy/helm/ach/values.yaml` (per-mode toggles) |
| API reference rendering                | `docs/Makefile` (`gen-crd-ref-docs`) + `docs/.crd-ref-docs.yaml` |
| What was grafted from alitellm + how   | `references/upstream-sync.md`            |
| OLM packaging                          | NOT supported — explicit scope decision (no OperatorHub) |

## CI gating — one-line summary

| Event                                 | lint | unit | envtest | security | e2e |
|---------------------------------------|------|------|---------|----------|-----|
| push: feature branch                  |  ✓   |  ✓   |   -     |    -     |  -  |
| pull_request → main                   |  ✓   |  ✓   |   ✓     |    ✓     |  ✓  |
| push: main (non-release)              |  ✓   |  ✓   |   ✓     |    ✓     |  -  |
| push: main `chore(release): v*`       |  -   |  -   |   -     |    -     |  -  (release.yml owns it) |

E2E runs once per change: on the PR. Post-merge skips it (already
green on the PR ref). Docs-only commits (paths-ignore: `**/*.md`,
`docs/**`, `.planning/**`, `references/**`, `FIX*.txt`, `LICENSE`,
`NOTICE`, `CODEOWNERS`, `.gitignore`) skip `ci.yml` entirely.

**Why ach keeps push CI on main** (vs sister project alitellm-operator,
which is PR-only): GitHub branch protection on a private repo requires
a paid plan (Pro/Team/Enterprise) or making the repo public. Until
either condition is met, main can in principle accept direct pushes,
so post-merge CI on push:main is the defensive gate that catches a
direct push that bypassed PR review. Once protection is enabled, this
workflow can be trimmed to PR-only the same way alitellm-operator's
ci.yml is.

## Toolchain — host has NO Go (always Docker)

The host has no `go`, `kubebuilder`, `controller-gen`, `kustomize`,
`setup-envtest`, or `golangci-lint` binary on PATH. Every toolchain
invocation goes through the devtools container via `./scripts/dev.sh`.

```bash
./scripts/dev.sh go build ./...
./scripts/dev.sh go test ./internal/controller/...
./scripts/dev.sh make manifests
./scripts/dev.sh bash            # interactive shell
```

- Wrapper mounts repo at `/workspace`, mounts `/var/run/docker.sock`,
  preserves host UID:GID, persists Go module + build caches under
  `.gocache/`, resolves `KUBEBUILDER_ASSETS`.
- Image: `ach-devtools:latest` (built from `Dockerfile.devtools` on
  first use locally; force rebuild with `ACH_DEVTOOLS_REBUILD=1`).
- CI consumes a pre-baked image from GHCR
  (`ghcr.io/<owner>/ach-devtools:<hash>`, where hash =
  `sha256(Dockerfile.devtools)[:12]`). `.github/workflows/devtools-image.yml`
  builds + pushes when `Dockerfile.devtools` changes;
  `.github/actions/setup-devtools` pulls in each CI job (~30s warm /
  2-3min cold saved per job × 5 jobs = ~10min/PR). On miss (first push,
  PR that changes Dockerfile.devtools racing the image workflow, GHCR
  unavailable), the composite action falls back to a local build —
  slower but always correct.
- Versions are pinned in `Dockerfile.devtools` and `go.mod` — when in
  doubt, those files are authoritative.

`make` targets shelling out to `go` MUST be prefixed `./scripts/dev.sh`.
Targets that only call `kubectl`/`docker`/`helm`/`kind`/bash run on host
(e.g. `make cluster-up`, `make operator-redeploy`, `make logs-*`).

## Test phases

| Phase              | Command                                | When                                  |
|--------------------|----------------------------------------|---------------------------------------|
| `make unit`        | pure-logic, ~5s warm                   | every iteration                       |
| `make lint-changed`| golangci-lint scoped to touched pkgs   | every iteration                       |
| `make lint`        | golangci-lint full sweep               | before commit (pre-commit hook)       |
| `make envtest-run` | controller-runtime envtest (race), ~7m | before commit on controller changes   |
| `make envtest-fast`| envtest without -race, ~3m             | dev inner loop                        |
| `make e2e-full`    | kind + Helm + stdlib testing, ~6m      | final gate before commit              |
| `make e2e-focus`   | focused subtest. `RUN='TestPhase4Promotion/SC11a'` (stdlib) OR `FOCUS='ginkgo it'` (legacy) | dev loop on a single sub-test |
| `make security`    | gosec + govulncheck + fuzz-short, ≤6m  | in-container; before commit           |
| `make pre-commit`  | lint-changed + unit                    | host-only; runs on every `git commit` once `make hooks` installed |
| `make pre-push`    | gitleaks + trufflehog + 15 gates (incl. full lint + unit) | host-only; before push |

Umbrella targets:
- `make test-all` = `unit` + `envtest-run`
- `make verify` = `./scripts/dev.sh make security` + `make pre-push`
- `make hooks` installs `.git/hooks/pre-commit -> scripts/pre-commit-check.sh`
  AND `.git/hooks/pre-push -> scripts/pre-push-check.sh`

`make pre-push` is host-only — it spawns gitleaks/trufflehog containers
on host docker. Do NOT call it via `./scripts/dev.sh` (would nest docker
mounts that don't resolve). The same applies to `make pre-commit`.

Do NOT invoke `make pre-push` (or `make pre-commit`) manually before
pushing — the installed git hooks (`make hooks`) fire the same script
on every `git push` / `git commit`. Running them by hand is duplicate
work and burns the cache; just run `git push` and let the hook gate.
If the hook is intentionally bypassed with `--no-verify`, then a
manual `make pre-push` is the right safety net — but that's the
exception, not the rule.

Inner-loop iteration helpers:
- `make unit-pkg PKG=./internal/<service>/...`
- `make envtest-pkg PKG=./internal/controller/... [FOCUS=TestX] [TIMEOUT=10m]`
- `make lint-changed [BASE_REF=...]` (lints only packages touched vs `origin/main`)

## Documentation site (mkdocs)

The public docs site at `docs/` is mkdocs-material based.

```bash
./scripts/dev.sh make gen-crd-ref-docs   # regenerate docs/api-reference/ from CRDs
make docs-build                          # build site/ via docker (host)
make docs-serve                          # local preview at :8000
```

`docs/.crd-ref-docs.yaml` is the config for the `crd-ref-docs` tool
(installed via `make crd-ref-docs`); it targets the ACH API groups
(`ach.ackstorm.ai/v1alpha1`).

The site publishes to `https://ackstorm.github.io/ach/` via the
`mike` versioned-docs flow.

`.github/workflows/docs.yml` deploys the site to `gh-pages` on
pushes to `main` and on `v*` tags. PRs build the site (no deploy) to
catch broken links and missing pages.

## Release pipeline

Release artifacts are produced by **goreleaser** orchestrated by
`.github/workflows/release.yml`. The flow is **commit-message-driven
with tag-last**: a push to `main` whose head commit message starts with
`chore(release): v<MAJOR>.<MINOR>.<PATCH>` fires the pipeline. The
workflow then runs the tests, bumps manifests itself, builds + signs
artifacts, and creates the git tag as the final step — so a failure
anywhere upstream leaves origin with no orphan tag.

Cutting a release (stable example, `v0.1.0`):

```bash
# Most common — empty release commit (no manifest pre-bump).
# `make release` runs preconditions (on main, clean tree, in-sync
# with origin/main), creates `chore(release): v0.1.0` as an empty
# commit, runs the 15-gate pre-push, and pushes to main.
make release VERSION=0.1.0

# Bundle the release intent with a real change:
# (edit, then commit the change yourself, then:)
git commit -am 'chore(release): v0.1.0'
make pre-push
git push origin main
```

There is no need to `make bump` locally or to create the tag yourself.
`make bump VERSION=X.Y.Z` is still available as the internal target
release.yml invokes; it can also be run by hand if you want to pre-bump
manifests in the same commit (the workflow detects the clean tree and
skips its own bump step), but it is not the expected workflow.

Per-release flow (after the `chore(release): v0.1.0` push):

1. **parse** job (job-level `if` skips non-release pushes): pulls
   `X.Y.Z` from the head commit message via regex.
2. **run-tests** job: `make test` (`unit` + `envtest-run` = race-enabled).
   Failures stop the pipeline here — no manifest mutation, no tag.
3. **build-and-release** job:
   - Configures the github-actions[bot] identity.
   - Runs `make bump VERSION=X.Y.Z`, commits the four bumped manifests
     to `main` with a `[skip ci]` marker, and pushes the bot commit.
     If the tree is already clean (user pre-bumped), this is a no-op.
   - Picks the goreleaser config:
     - `vX.Y.Z`                  → `.goreleaser.yml`            (stable)
     - `vX.Y.Z-{alpha,beta,rc}*` → `.goreleaser.prerelease.yml`
   - `make generate manifests` regenerates CRDs (sanity).
   - cosign + cyclonedx-gomod installed on PATH (HRD-09).
   - goreleaser runs with `GORELEASER_CURRENT_TAG=v<X.Y.Z>` (no git
     tag at HEAD yet). The GitHub release-create API call auto-creates
     the tag at default-branch HEAD, which is the bot-bump commit.
     - cross-builds amd64 + arm64 (CGO_ENABLED=0, alpine runtime with
       git + ca-certificates baked).
     - builds multi-arch manifest list at
       `ghcr.io/ackstorm/ach:vX.Y.Z` (+ `:latest` on
       stable).
     - `sboms:` block generates the CycloneDX SBOM via cyclonedx-gomod.
     - `signs:` block signs the checksums file with cosign keyless OIDC.
     - `docker_signs:` block signs all image artifacts (per-arch +
       manifest list) with cosign keyless OIDC.
   - Pushes the chart to
     `oci://ghcr.io/ackstorm/charts/ach:<X.Y.Z>`.
   - **LAST**: idempotently creates and pushes the annotated git tag
     `v<X.Y.Z>`. If goreleaser's release API call already implicitly
     created the tag, this is a no-op.

Orphan-tag posture: tag-creation is the LAST step. A failure in tests
or bump or goreleaser leaves no tag on origin and no GH release
attached to one. The bot bump commit may be on `main` if the failure
happened in goreleaser — that is reversible by reverting the bot
commit or by simply running the next release attempt, since `make bump`
inside the workflow is idempotent.

Snapshot builds (`.goreleaser.snapshot.yml`) are intentionally NOT
signed and do NOT generate SBOMs — they are ephemeral dev artifacts
pushed as `ghcr.io/ackstorm/ach:main` +
`:main-<shortcommit>`.

`docker_signs:` and `signs:` blocks require:
- `id-token: write` in the workflow (already set).
- cosign on PATH (release.yml installs via `sigstore/cosign-installer`).

## Publication — pre-commit and pre-push gates are non-negotiable

Remote: `git@github.com:ackstorm/ach.git`. The local gate strategy
splits across two hook stages so the cost of "oops, CI failed lint"
is paid locally before the commit even lands:

- `pre-commit` (`make pre-commit`) — fast: `make lint-changed`
  (golangci-lint scoped to touched packages) + `make unit`. Runs on
  every `git commit` once `make hooks` is installed. Bypass with
  `--no-verify` only for justified WIP commits; the full lint sweep
  still fires on push.
- `pre-push` (`make pre-push`) — full: 17-gate publication check
  (lint + unit live INSIDE the 17 as defensive gates 16+17 so the
  push is still safe even if pre-commit was bypassed).

Hard gates (17) — failure blocks push:
- gitleaks + trufflehog (scope: `origin/main..HEAD`; full history on
  first push). Allowlist: `.gitleaks.toml`.
- Large files >2 MB
- Sensitive file patterns (`.env`, `*.pem`, `*.key`, kubeconfig, ...)
- LICENSE + README presence
- Origin remote matches expected
- govulncheck ack-list 1:1 match
  (see `scripts/govulncheck-gate.sh`; ack-list at
  `references/security/govulncheck-acknowledged.md`)
- `go mod tidy` drift
- Per-file SPDX license header
  (`// SPDX-License-Identifier: Apache-2.0`)
- golangci-lint full sweep (`make lint` inside devtools container)
- `make unit` (pure-logic regression — `./scripts/dev.sh make unit`)

If a gate fails, fix the root cause — never `--no-verify` or otherwise
bypass. Note: `--no-verify` skips ONLY the local hook; it does not
exempt CI, which reruns the same gates.

## Waiting for state — use blessed make targets

Naked polling loops (`until ...; do sleep N; done`) are banned: a
disappearing target makes the predicate unreachable, hanging the agent.
Use these Makefile targets instead:

| Need                                    | Target                                              |
|-----------------------------------------|-----------------------------------------------------|
| CR condition Ready                      | `make wait-cr-ready KIND=... NAME=... NS=...`       |
| Operator Deployment Ready               | `make wait-operator`                                |
| Platform API Deployment Ready           | `make wait-platform-api`                            |
| Forwarder Deployment Ready              | `make wait-forwarder`                               |
| Content Service container Ready (co-located in operator Pod) | `make wait-content-service`             |
| All ach Deployments (operator+platform-api+forwarder) Ready | `make wait-ach` (wraps `scripts/cluster.sh wait_ach`) |
| Postgres StatefulSet Ready              | `make wait-postgres`                                |
| Redis (Valkey) StatefulSet Ready        | `make wait-redis`                                   |
| Dex Deployment Ready                    | `make wait-dex`                                     |
| Container exit + PASS/FAIL marker       | `make wait-container NAME=<container>`              |
| Full cluster hydration                  | `make cluster-up` (synchronous; do not poll after)  |
| Operator hot-reload + Ready             | `make operator-redeploy` (bounded `rollout status`) |

> Some of these `wait-*` targets are not yet defined in the Makefile —
> add them on first use rather than introducing ad-hoc polling loops in
> scripts or tests. Targets are the contract; ad-hoc loops aren't.

Default `WAIT_TIMEOUT=300s` (override per call). `wait-container` takes
`TIMEOUT=<seconds>` (default 600).

Blessed wait patterns (every `wait-*` uses one):
1. `kubectl wait --timeout=...`
2. `kubectl rollout status --timeout=...`
3. `timeout N docker logs -f <cid> | grep -m1 ...`
4. `docker wait <cid>`

If a needed wait isn't covered, **add a new `wait-*` target** — targets
are the contract; ad-hoc loops aren't.

## Common failure modes

### ❌ Running `make X` directly on host
```bash
make unit
# command not found: go
```
✅ Prefix with `./scripts/dev.sh`:
```bash
./scripts/dev.sh make unit
```
WHY IT FAILS: Host has no Go binary. The devtools container does.

### ❌ Naked polling loop
```bash
until docker logs $(docker ps -q -f ancestor=mock) | grep -q PASS; do
  sleep 10
done
```
✅ Bounded wait via blessed pattern:
```bash
timeout 600 docker logs -f $cid 2>&1 | grep -m1 -E "PASS|FAIL" || {
  echo "FAIL: marker not seen within 600s" >&2; exit 1;
}
```
WHY IT FAILS: When the container exits and is removed, `docker ps -q`
returns empty; `docker logs` errors forever; the loop never exits.

### ❌ Invalid Postgres / Redis URLs in dev hydration
```bash
ach operator --postgres-url "postgres://localhost/ach"  # missing user, port
# panic: failed to connect to postgres: dial tcp: missing port in address
```
✅ Always pass full DSNs in dev hydration scripts and Helm values:
```bash
ach operator --postgres-url "postgres://ach:ach@postgres.ach.svc:5432/ach?sslmode=disable"
```
WHY IT FAILS: pgx parses `postgres://` URLs strictly; missing user, port,
or scheme yields panics on startup that look like unrelated network issues.

### ❌ Running a service mode without its subcommand
```bash
./bin/ach          # prints help, exits 0 — operator never starts
```
✅ Each long-running mode requires its subcommand explicitly:
```bash
./bin/ach operator         # k8s reconciler
./bin/ach platform-api     # REST API
./bin/ach forwarder        # JWT trust path
./bin/ach content-service  # artifact streaming
./bin/ach migrate          # one-shot DB migration
```
WHY IT FAILS: `cmd/ach/main.go` defers to cobra; with no subcommand, cobra
prints the help text for the root command and exits cleanly. A Deployment
that omits `args: ["<mode>"]` will sit in CrashLoopBackOff with exit code 0
or silently restart depending on the readiness probe.

### ❌ Re-running full E2E for every code change
```bash
make e2e-full       # ~10 min from clean each time
```
✅ Use the dev loop:
```bash
make cluster-keep                       # once
./scripts/dev.sh make e2e-focus FOCUS="rateLimits"   # ~30s-2min per iter
./scripts/dev.sh make operator-redeploy # hot-reload after code edit
```
WHY IT FAILS: `e2e-full` tears down and recreates the cluster every run.
The kept-cluster loop reuses state across iterations.

### ❌ Pushing without running pre-push
```bash
git push origin main --no-verify
```
✅ Run the gate:
```bash
make pre-push       # or rely on the installed git hook
git push origin main
```
WHY IT FAILS: Pushed secrets, license-header drift, govulncheck advisory
regressions cannot be untrue-d from public history. The 15-gate script
is the contract.

### ❌ Kubectl from host against the kind cluster
```bash
kubectl get pods -n default
# context not found
```
✅ Go through the devtools container:
```bash
./scripts/dev.sh kubectl get pods -n default
```
WHY IT FAILS: The kind kubeconfig lives at
`/workspace/.gocache/kube/config` — inside the container. Host kubectl
has no context for the kind cluster.

### ❌ Editing files via relative paths when cwd is the wrong repo
```bash
cat > internal/controller/scope_ac_n4_test.go <<EOF ...
# silently writes to a sibling repo if that is cwd
```
✅ Always use absolute paths for Edit/Write, and `cd /home/jcm/Projects/ach`
before bash operations. Verify with `pwd && git log --oneline | head -3`.
WHY IT FAILS: Sibling repos with similar layouts (e.g. `ach-old/`,
`alitellm-operator/`) live next to this one. Relative-path edits to the
wrong tree leave this repo unchanged while appearing to "succeed."

### ❌ `downloadUrl` from /platform/hydrate returns 404
```bash
curl https://ach.local.test/content/prompt/foo
# HTTP 404 (or no handler registered at all → chi 404)
```
✅ Confirm the content-service sidecar in the operator Pod is on a
build that includes `internal/contentservice` routes (not the Phase 1
stub). The content-service runs as the second container of the
`ach-operator` Deployment (RWO PVC forces co-location); there is NO
`ach-content-service` Deployment. Use the operator Deployment + the
`content-service` container name when exec'ing:
```bash
kubectl -n ach-system exec deploy/ach-operator \
  -c content-service -- ach content-service --help \
  | grep -q "/content/{prompt,plugin,artifact}"
```
WHY IT FAILS: Pre-`feat/content-service-routes` builds shipped a
`/healthz`-only stub. The Service is healthy, the Pod is Ready, the
hydrate URLs look right — and every GET 404s because the route doesn't
exist. Fix is a rolling image update; no data migration.

### ❌ Forwarder Pod CrashLoopBackOff: `ach-jwt-signing-keys` Secret missing
```bash
kubectl -n ach-system logs deploy/ach-forwarder -c forwarder
# fatal: load JWT signing keys: secret "ach-jwt-signing-keys" not found in namespace "ach-system"
```
✅ The forwarder refuses to start without `ach-jwt-signing-keys`
(FWD-09 — no in-cluster fallback, no implicit zero-key). The Secret
must carry two keys: `current.kid` (short ASCII id) and `current.seed`
(32 random bytes). `scripts/cluster.sh hydrate_fixtures` seeds a fresh
(kid=`dev-<timestamp>`, seed=`openssl rand 32`) pair on every
`cluster.sh up` if the Secret is absent; production deploys must
provision it explicitly (e.g. ExternalSecrets / SealedSecrets — never
the dev seed). Manual seed if you need one:
```bash
jwttmp=$(mktemp -d)
openssl rand 32 > "${jwttmp}/current.seed"
printf 'dev-%s' "$(date +%s)" > "${jwttmp}/current.kid"
kubectl -n ach-system create secret generic ach-jwt-signing-keys \
  --from-file=current.kid="${jwttmp}/current.kid" \
  --from-file=current.seed="${jwttmp}/current.seed"
rm -rf "${jwttmp}"
```
WHY IT FAILS: The forwarder mints the per-request JWT off this seed at
startup; a missing Secret turns the whole JWT trust path unreachable,
which would silently degrade upstream auth. Refusing to start is the
correct posture — fix the seed, not the check.

### ❌ "SourceReachable=False reason=Unauthorized" on a public GitHub repo
```bash
kubectl get plugin/caveman -o jsonpath='{.status.conditions[0]}'
# {"reason":"Unauthorized","message":"github: GetCommit 403: sources: unauthorized"}
```
✅ This is GitHub's 60 req/h/IP anonymous rate-limit — NOT a config bug. The
within-interval gate (`shouldSkipFetch` in `internal/controller/ach/external_ref_refresh.go`)
already prevents steady-state burn; a 403 here means a real burst (multiple
`cluster.sh up` cycles, operator restarts, or force-refresh fires within an
hour) has actually exhausted the quota. Either:
  - wait ~1h for the quota window to roll over, OR
  - set `authSecretRef` on a CR whose repo legitimately needs auth, OR
  - investigate why the operator is reconciling more often than expected
    (`kubectl -n ach-system logs deploy/ach-operator | grep -c GetCommit`)

WHY IT FAILS: GitHub rate-limits anonymous REST calls by source IP. The Hub's
default refresh interval is 1h per CR, so legitimate steady-state should
average <5 API calls/h across 3-5 CRs — well below the 60/h ceiling. Hitting
the limit means a tight loop or many cluster rebuilds in the same hour.

**Resolution as of 2026-05-27**: The default outer transport for all three
git source types (`github`, `gitlab`, `bitbucket`) is now `git`
(`FIX_GIT.txt`), which has no per-IP REST rate-limit. If you still see this
error on the default transport, the upstream is genuinely unreachable, the
ref doesn't exist, or git's HTTPS auth-prompt fired (anonymous + a
nonexistent or private repo cannot be told apart by git/HTTPS — both
surface as "please authenticate"). To temporarily revert one CR to the
legacy REST path, set `spec.<github|gitlab|bitbucket>.transport: rest` on
the CR; that path still hits the per-provider anonymous quotas (GitHub
60/h, GitLab 60/min, Bitbucket 60/h) and will be removed one release after
the git transport is observed clean. The transport that actually served
each fetch is now surfaced on the `SourceReachable=True` (Plugin/Prompt/
Artifact) and `Synced=True` (PluginMarketplace) condition messages as
`transport=<git|rest|n/a>`.

### ❌ Environment stuck in `AccessGroupSynced=False reason=UnresolvedReferences`
```bash
kubectl get environment demo -n ach-system -o jsonpath='{.status.conditions[?(@.type=="AccessGroupSynced")]}'
# {"type":"AccessGroupSynced","status":"False","reason":"UnresolvedReferences",
#  "message":"unresolved: mcpServers=[demo-mcp] a2aAgents=[] authorizedTeams=[]"}
```
✅ The named MCP server / A2A agent / authorized team does not exist in
LiteLLM. The reconciler resolves names on each reconcile via
`ListMCPServers` / `ListA2AAgents` / `ListTeamsByAlias`; any unresolved
entry flips the condition with the offending list in the message.

Register the missing resource(s):
```bash
# MCP server
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/v1/mcp/server \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"server_name":"<name>","transport":"http","url":"http://<addr>"}'

# A2A agent
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/v1/agents \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"agent_name":"<name>","agent_card_params":{"name":"<name>","url":"<addr>"}}'

# Team
kubectl -n litellm-system exec deploy/litellm -c litellm -- \
  curl -sf -X POST http://localhost:4000/team/new \
    -H 'Authorization: Bearer sk-test-master-key' \
    -d '{"team_alias":"<alias>"}'
```
The next reconcile (or any spec-change touch) re-runs the resolvers and
the condition flips to `True/Synced`.

WHY IT FAILS: the legacy `POST /access_group/new` rejected empty
`model_names` (issue #17). The `/v1/access_group` endpoint accepts
empty resource sets, but every ID in `access_mcp_server_ids` /
`access_agent_ids` / `assigned_team_ids` must exist upstream. The
reconciler converts names → IDs on-demand each reconcile (no
Snapshotter cache), so the condition reflects fresh upstream state.

## Repository-specific patterns

- **Single-binary cobra layout**: `cmd/ach/main.go` is a thin wrapper that
  calls `cmd.Execute()`. All long-running modes are subcommands under
  `cmd/ach/cmd/<mode>.go`. Each subcommand registers its flags + wires
  its service implementation from `internal/<service>/`. New modes go
  through `cmd/ach/cmd/`, NEVER as a second `cmd/<x>/main.go` tree.

- **Reconciler shape**: each controller in `internal/controller/<kind>_controller.go`
  follows `Reconcile(ctx, req) (Result, error)`, applies status conditions
  via `meta.SetStatusCondition`. Side-effecting work (Postgres writes,
  external HTTP calls) lives in dedicated packages under `internal/` for
  unit testability — the reconciler owns the k8s-state machine, the
  service packages own the I/O.

- **Per-mode Helm Deployments**: operator, platform-api, and forwarder
  are independent Deployments in the Helm chart, all consuming the same
  image. content-service is NOT a separate Deployment — it runs as the
  second container in the operator Pod (co-located because the
  artifacts PVC is RWO). Per-mode toggles in `deploy/helm/ach/values.yaml`
  (`operator.enabled`, `platformApi.enabled`, `forwarder.enabled`,
  `contentService.enabled`) let users install partial topologies (e.g.
  operator-only baseline, full hub). Each Deployment carries
  `args: ["<mode>"]` to select the cobra subcommand; the
  content-service sidecar carries `args: ["content-service"]` on the
  second container of the operator Pod.

- **Environment two-axis status**: the Environment CR exposes
  `ExecutionResourcesResolved` (Plugin/Prompt/Artifact closed-set
  marker) and `AccessGroupSynced` (LiteLLM access-group reconciler:
  resolves names → IDs on each reconcile via
  `ListMCPServers` / `ListA2AAgents` / `ListTeamsByAlias`, then
  `POST /v1/access_group`). The composite `Available=True` rolls both
  up — that is what `ach hydrate` / the demo script gate on, not
  individual sub-conditions.

- **BackendIdentityPolicy forwarder read-path**: BIPs are owned by the
  operator but consumed by the forwarder via an informer-backed cache
  (`internal/forwarder/bip`). The forwarder mints per-target JWTs off
  the BIP's identity material plus the `ach-jwt-signing-keys` seed —
  no Postgres lookup on the request path. Operator writes the policy
  and `Synced=True`; forwarder picks it up on the next informer event.

- **SPDX-only license headers**: every `*.go` outside `vendor/`,
  `zz_generated*.go`, `mock_*.go` starts with
  `// SPDX-License-Identifier: Apache-2.0`. Pre-push gate enforces.
  `hack/boilerplate.go.txt` provides the header for controller-gen
  output; `make generate` wires it in via `object:headerFile=`.

- **govulncheck ack-list**: stdlib HIGH advisories awaiting upstream Go
  fixes live in `references/security/govulncheck-acknowledged.md` (note:
  `references/`, not `docs/`, since the ack-list is agent-facing internal
  documentation that is not published on the mkdocs site). The gate
  script enforces the reachable set matches this list exactly — drift
  in either direction blocks push.

- **Upstream-sync ledger**: `references/upstream-sync.md` records every
  file or directory grafted from `/home/jcm/Projects/alitellm-operator`
  with the adaptations applied (sed renames, path rewrites, single-
  binary refactors). New grafts MUST add a row.

## E2E debug loop

`make e2e-full` is the clean-room final gate (~10 min). For iteration
use the kept-cluster loop:

```bash
# 1. Bring cluster up once (kept after run)
./scripts/dev.sh make e2e-keep
# = scripts/cluster.sh keep + make e2e (NO teardown after)

# 2. Diagnose live (cluster is up)
./scripts/dev.sh bash -c "kubectl -n default logs deploy/ach --tail=200"
./scripts/dev.sh bash -c "kubectl -n default describe team <name>"

# 3. Iterate with focused tests
./scripts/dev.sh make e2e-focus FOCUS="rateLimits composite"
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/... FOCUS=TestTeamReconciler_AC_T4

# 4. Code change → hot-reload → re-test (~30s)
./scripts/dev.sh make operator-redeploy
./scripts/dev.sh make e2e-focus FOCUS="..."

# 5. Final gate before commit (full suite from clean)
make cluster-down
./scripts/dev.sh make e2e-full
```

Never push a change touching `internal/controller/`, `internal/platformapi/`,
`internal/forwarder/`, `internal/contentservice/`, `api/v1alpha1/`,
`deploy/helm/ach/`, or `test/e2e/` without confirming E2E green.

## External references

For up-to-date API info beyond this project's docs:
- **controller-runtime / kubebuilder**: Context7 or DeepWiki for current
  APIs (reconciler patterns, manager setup, indexer builders).
- **client-go / k8s.io/***: Context7 for typed-client method signatures
  pinned to the version in `go.mod`.
- **cobra / viper**: Context7 for subcommand registration patterns,
  pflag binding, env-var fallback semantics.
- **Dex SSO**: WebFetch against `https://dexidp.io/docs/` for OIDC
  connector configuration and discovery endpoint contracts.
- **goreleaser v2**: https://goreleaser.com — pay attention to the
  `dockers` → `dockers_v2` migration warning (currently deferred; all
  three configs validate today).
- **Claude Code plugin / marketplace schemas**: official JSON Schemas at
  https://www.schemastore.org/claude-code-plugin-manifest.json and
  https://www.schemastore.org/claude-code-marketplace.json — authoritative
  for the shape of `marketplace.json` and `.claude-plugin/plugin.json`.
  Narrative docs at https://code.claude.com/docs/en/plugin-marketplaces.
  The marketplace parser (`internal/controller/ach/marketplace_parse.go`)
  follows the real schema with one ack of upstream drift: `url`-Kind
  entries carry an optional `path` field (schema says no path; real
  catalogs ship it — treated as `git-subdir` when set).
