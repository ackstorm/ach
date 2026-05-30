# Phase 1: Foundation — CRDs, DB Schema, Operator Skeleton, Multi-tenancy - Context

**Gathered:** 2026-05-15
**Status:** Ready for planning

<domain>
## Phase Boundary

Phase 1 delivers the foundational substrate every later phase consumes:

- Six CRDs under `ach.ackstorm.ai/v1alpha1` (`Environment`, `Plugin`, `PluginMarketplace`, `Artifact`, `Prompt`, `BackendIdentityPolicy`) with CEL admission rules enforced via `x-kubernetes-validations` on the OpenAPIv3Schema (no `ValidatingAdmissionWebhook` in v1alpha1).
- Four Postgres tables (`personal_keys`, `environment_keys`, `external_refs`, `marketplace_plugins`) per Hub §16 — `key_id` PKs with `pkid_`/`ekid_` prefixes, UNIQUE on `credential_hash`, HMAC-SHA-256 with server-side pepper held outside Postgres. No plaintext key bytes anywhere.
- An ACH Operator binary (controller-runtime) that registers and finalizes all six CRDs. LiteLLM access-group + tag operations during the §6.5 Environment-deletion drain are gated behind a `litellm.Client` interface whose Phase 1 implementation is a no-op (Phase 2 swaps in the real REST client). The §6.5 `ek_` DB drain loop and finalizer removal are real Phase 1 code.
- A single-replica `strategy: Recreate` Operator + Content Service Pod with two containers sharing an RWO PVC mounted at `/var/cache/ach`. The Content Service container is a stub in Phase 1 (healthz-only long-running process) — its real streaming endpoint lands in Phase 5. Same for Platform API and Forwarder pods.
- Namespace-scoped RBAC (Role/RoleBinding, never ClusterRole/Binding) for all four Hub component ServiceAccounts per Hub §5.2, plus the narrow Platform API `patch`-on-annotation carve-out (MULTI-02). All four SAs ship in Phase 1 even though three of the binaries are stubs, so the Helm chart in Phase 7 has a single-source-of-truth manifest set.

Phase 1 boundary explicitly EXCLUDES anything that talks to LiteLLM live, any pk_/ek_ creation flow, any content-streaming response, any forwarder route, any metrics endpoint, any helm chart packaging. Those land in Phases 2–7.

</domain>

<decisions>
## Implementation Decisions

### Repo & Module Layout

- **D-01:** Mirror the sister project (`../ach_litellm`) kubebuilder v4 conventions verbatim. `domain: ackstorm.ai`, `group: ach`, `multigroup: true`, kinds under `api/ach/v1alpha1/`, reconcilers under `internal/controller/`, kustomize manifests under `config/{crd,rbac,manager,samples,prometheus,network-policy}/`. A kubebuilder-managed `PROJECT` file (no extension) lives at repo root — it does NOT conflict with `.planning/PROJECT.md`. Go 1.23+; `sigs.k8s.io/controller-runtime` v0.19+; Ginkgo + Gomega + envtest for tests (kubebuilder default).
- **D-02:** Single `go.mod` for the whole codebase (`github.com/ackstorm/ach`). Hub binaries and the CLI share the `api/ach/v1alpha1/` Go types via direct package import — no `replace` directive, no vendored copy. The CLI accepts the controller-runtime transitive deps as the cost of type-sharing; binary size is a Phase 7 distribution concern, not Phase 1.
- **D-03:** Extend kubebuilder's single-binary `cmd/main.go` to a per-binary tree: `cmd/operator/main.go` (the kubebuilder-generated controller-manager), plus stub mains in Phase 1 at `cmd/platform-api/main.go`, `cmd/forwarder/main.go`, `cmd/content-service/main.go`, `cmd/ach/main.go`. The three Hub-stub mains expose `/healthz` only and log "Phase X stub". The Content Service stub MUST be a real long-running healthy process — Phase 1 SC #2 requires `kubectl describe pod` to show two containers ready, both passing readiness probes. The CLI stub prints version + "not yet implemented" and exits.

### CRD Scaffolding & Code-Gen

- **D-04:** kubebuilder markers + controller-gen drive CRD/RBAC/DeepCopy generation. CEL admission rules expressed as `// +kubebuilder:validation:XValidation:rule="...",message="..."` markers on the Go types. `make manifests` regenerates `config/crd/bases/*.yaml`; `make generate` rewrites `zz_generated.deepcopy.go`. No hand-edited CRD YAML — generation is the source of truth.
- **D-05:** All six CRDs live in `api/ach/v1alpha1/` as `<kind>_types.go` files (`environment_types.go`, `plugin_types.go`, `pluginmarketplace_types.go`, `artifact_types.go`, `prompt_types.go`, `backendidentitypolicy_types.go`) alongside a single `groupversion_info.go`. CEL rules required per REQ-IDs CRD-01..CRD-08 — required-field, cross-field (`refresh.interval ≤ refresh.maxStaleness`), source-type-subobject-matches-`spec.type`, `Artifact.type=http ⇒ scope=object`, `BackendIdentityPolicy.target.kind ∈ {MCPServer, A2AAgent}`, `forwardIdentityJWT` REQUIRED.

### DB Schema & Migration Ownership

- **D-06:** `github.com/golang-migrate/migrate/v4` for migrations. Migration files at `db/migrations/000001_init.{up,down}.sql`. Phase 1 ships migration `000001` covering all four tables per Hub §16 column lists. Driver: `pgx/v5` via `database/sql` interface for the migration tool; native `pgx` for application query paths.
- **D-07:** Migrations run by a **dedicated `migrations` init container** in the Operator + Content Service Pod. Single-replica `Recreate` rollout eliminates startup races. Init container image = same Operator image with `migrate up` entrypoint (overrideable via container `command`). Platform API, Forwarder, and Content Service (later phases) connect to the same database read/write as needed but never run migrations.
- **D-08:** Migration tooling refuses to start with empty/invalid `ACH_DB_URL`. On migration failure, the init container exits non-zero and the Pod stays in `Init:Error` — operator visibility via `kubectl describe pod`. No "best-effort" migration semantics.

### Credential Pepper Sourcing

- **D-09:** HMAC-SHA-256 pepper sourced from a Kubernetes `Secret` named `ach-credential-hash-pepper` with a single key `pepper`, mounted as the env var `ACH_CREDENTIAL_HASH_PEPPER` on the Operator, Platform API, Forwarder, and Content Service Pods (every component that may need to compute or compare a credential hash). Components refuse to start when the env var is empty or missing — matches the spec's "Kubernetes Secret, KMS reference, or equivalent" language and meets DB-03/DB-04.
- **D-10:** Phase 1 ships the Secret manifest + the wiring + an `internal/credhash/` package with a constant-time HMAC-SHA-256 hash + compare API. No live hashing happens in Phase 1 (no pk_/ek_ creation until Phase 3) — Phase 1 just makes the contract concrete. Pepper rotation is a planned-maintenance event in v1alpha1 (rotation tooling deferred to v1beta1 backlog).

### Finalizer Stub Strategy (Phase 1 → Phase 2 handoff)

- **D-11:** Phase 1 wires real finalizer registration on all six CRDs (`environments.ach.ackstorm.ai/finalizer` and `<kindPlural>.ach.ackstorm.ai/finalizer`). The LiteLLM-touching steps of the §6.5 Environment-deletion drain (step 2 access-group delete, step 3 tag delete) and the §10.3 external-reference cleanup go through an `internal/litellm.Client` interface. Phase 1 ships a `NoopClient` that logs `"stub: would delete LiteLLM access group <name>"` and returns nil. Phase 2 swaps in the real REST client via dependency-injection at controller construction time.
- **D-12:** The §6.5 step-4 `ek_` DB drain loop and step 5 finalizer-removal are REAL Phase 1 code. With no `ek_` rows extant in Phase 1 the loop trivially exits on first iteration — but the code path is exercised. External-reference CRD finalizer cleanup of cached files (OP-12) is also real Phase 1 code; the cache layout exists (D-13) and removing files is filesystem-local, not LiteLLM-dependent.

### PVC Cache Layout Initialization

- **D-13:** The Operator container's `main` runs an idempotent PVC bootstrap before `manager.Start()`: ensures `prompt/`, `plugin/`, `marketplace/`, `artifact/`, `.tmp/` exist as directories under the PVC mount root (default `/var/cache/ach`, configurable via env `ACH_CACHE_ROOT`). Bootstrap failure (mount missing, permission error, ENOSPC) aborts startup with a structured error. Per OP-10 the staging directory is `.tmp/` so `rename(2)` is atomic on the same filesystem.

### Multi-tenancy & RBAC Manifests

- **D-14:** `config/rbac/` declares exactly the per-component access pattern from Hub §5.2 — all bindings namespace-scoped (`Role` + `RoleBinding`, never `ClusterRole`/`ClusterRoleBinding`) per MULTI-01:
  - **Operator SA:** `get/list/watch/create/update/patch/delete` on all `ach.ackstorm.ai` kinds + status subresource (sole writer).
  - **Platform API SA:** `get/list/watch` on all six kinds, plus `patch` on `plugins`/`prompts`/`artifacts`/`pluginmarketplaces` — used only for the `ach.ackstorm.ai/force-refresh` annotation (MULTI-02 carve-out).
  - **Forwarder SA:** `get/list/watch` on `Environment` + `BackendIdentityPolicy` only.
  - **Content Service SA:** `get/list/watch` on `Environment` + `Plugin` + `Prompt` + `Artifact` + `PluginMarketplace`.
- **D-15:** Phase 1 ships all four ServiceAccounts and their Role/RoleBindings even though three of the binaries are stubs. The Helm chart in Phase 7 will pick up these manifests unchanged.

### Containerized Toolchain (added during Phase 1 execution)

- **D-16:** The host has **no Go toolchain installed** — `go`, `kubebuilder`, `controller-gen`, `kustomize`, `setup-envtest`, `kind`, `kubectl`, and `psql` are NOT on `PATH`. Every such invocation MUST go through `./scripts/dev.sh`, which builds (on first run) and execs commands inside `ach-devtools:latest` (built from `Dockerfile.devtools`, lifted verbatim from `../ach_litellm/` with the `ach-litellm-devtools` → `ach-devtools` tag rename and the addition of `postgresql-client` for Phase 1 SC #4). The wrapper mounts the workspace at `/workspace`, mounts the host docker socket for sibling-container access (`docker-compose`, `kind`), preserves Go module + build caches under `.gocache/` (gitignored), and forwards `ACH_DB_URL` + `ACH_CREDENTIAL_HASH_PEPPER` as env vars. Plans' `<action>` blocks and `<verify>` commands that reference `go`/`kubebuilder`/`make` are read as "run via `./scripts/dev.sh`"; executor agents prefix them automatically.



The following are not user decisions — Claude will pick defaults aligned with the sister project unless a planner concern arises later:

- **Logging:** `log/slog` (Go 1.21+ stdlib) for application logs; `sigs.k8s.io/controller-runtime/pkg/log/zap` for the manager (sister project pattern). Output is JSON for production builds, text for dev.
- **Test infra:** Ginkgo + Gomega + envtest (kubebuilder default; sister convention). CEL admission rules exercised via envtest `kubectl apply` of valid + invalid CR fixtures under `test/`. Postgres integration via testcontainers-go (no live cluster required for unit tests).
- **Config plumbing:** `os.Getenv` + a small validation helper. No viper. Knobs Phase 1 introduces: `ACH_CACHE_ROOT` (default `/var/cache/ach`), `ACH_NAMESPACE` (downward-API), `ACH_DB_URL`, `ACH_CREDENTIAL_HASH_PEPPER`. The `ACH_PLUGIN_MAX_SIZE_MIB` knob (default 50, refuses to start at 0/negative/non-numeric per OP-09) is wired and validated in Phase 1 even though it's unused until Phase 2 — the contract belongs with the Operator from day one.
- **Linter:** `golangci-lint` mirroring sister's `.golangci.yml`.
- **Docker:** Multi-stage builds per `~/.claude/CLAUDE.md`. Five Dockerfiles (`Dockerfile.{operator,platform-api,forwarder,content-service,ach}`) sharing a builder stage. `docker-compose.yml` for local dev brings up Postgres + Redis.
- **Spike directory:** `verification/` (sister convention) reserved for Hub-spec assumption spikes if needed mid-phase.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### ACH Specs (source of truth)

- `ach_hub_spec_v20260515_FINALv4.md` §2 — API group, six first-class kinds, CEL-only admission policy
- `ach_hub_spec_v20260515_FINALv4.md` §5.1 — component decomposition, header rewrite contract, Pod boundaries
- `ach_hub_spec_v20260515_FINALv4.md` §5.2 — Environment read model (informer cache), per-component RBAC table, no-projection-layer rule
- `ach_hub_spec_v20260515_FINALv4.md` §6 — Environment field semantics; §6.5 — deletion drain (the Phase 1 finalizer flow)
- `ach_hub_spec_v20260515_FINALv4.md` §6.6 — condition reasons closed set (needed for status writes from Phase 1)
- `ach_hub_spec_v20260515_FINALv4.md` §10.3 — cache layout, `.tmp/` staging, atomic `rename(2)` publication, external-ref finalizer cleanup
- `ach_hub_spec_v20260515_FINALv4.md` §11 — `ACH_PLUGIN_MAX_SIZE_MIB` start-time validation
- `ach_hub_spec_v20260515_FINALv4.md` §16 — DB schema (four tables, columns, PKs, UNIQUE constraints)
- `ach_hub_spec_v20260515_FINALv4.md` §16.1 — credential storage rules, plaintext non-persistence, HMAC-SHA-256 with server-side pepper
- `ach_hub_spec_v20260515_FINALv4.md` §18.3 — multi-tenancy / namespace scoping / JWT `sub` composition

### Planning Artifacts

- `.planning/PROJECT.md` — project context, constraints, Key Decisions (Go-on-both-sides, `pk_` permanent first-class, prefix convention)
- `.planning/REQUIREMENTS.md` — Phase 1 maps to REQ-IDs: CRD-01..CRD-08, DB-01..DB-06, OP-01/02/04/05/10/11/12, MULTI-01..MULTI-04 (25 REQ-IDs)
- `.planning/ROADMAP.md` — Phase 1 entry: goal, depends-on, Success Criteria 1..5
- `.planning/STATE.md` — current position, last activity

### Sister Project (layout + conventions reference; explicitly invoked by user)

- `../ach_litellm/PROJECT` — kubebuilder v4 PROJECT-file format (`domain`, `multigroup: true`, `resources[]` shape)
- `../ach_litellm/go.mod` — Go 1.23, controller-runtime v0.19.4, Ginkgo v2.19, Gomega v1.33 baseline
- `../ach_litellm/api/litellm/v1alpha1/` — per-kind `<kind>_types.go` + single `groupversion_info.go` + `zz_generated.deepcopy.go` pattern
- `../ach_litellm/internal/controller/` — controller split-file pattern (per-kind controller file + per-aspect test files)
- `../ach_litellm/config/` — kustomize layout (`crd/`, `rbac/`, `manager/`, `samples/`, `prometheus/`, `network-policy/`, `default/`, `e2e/`)
- `../ach_litellm/Makefile` — `manifests`, `generate`, `fmt`, `vet`, `test`, `lint`, `build`, `install`, `deploy` target conventions
- `../ach_litellm/.golangci.yml`, `../ach_litellm/.devcontainer/`, `../ach_litellm/Dockerfile`, `../ach_litellm/hack/boilerplate.go.txt`

### External Standards

- kubebuilder book — https://book.kubebuilder.io/ (CEL marker syntax, RBAC markers, status subresource pattern)
- `github.com/golang-migrate/migrate/v4` — migration runner library
- Kubernetes CEL admission validation — https://kubernetes.io/docs/reference/using-api/cel/

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets

Repository is greenfield. The only files in `/home/jcm/Projects/ach/` are the two specs (`ach_hub_spec_v20260515_FINALv4.md`, `ach_cli_spec_v20260515_FINALv4.md`), `CLAUDE.md`, and the `.planning/` tree. No code, no `go.mod`, no manifests.

### Established Patterns

All patterns come from the sister project `../ach_litellm/` (see canonical refs). Phase 1's planner should:
1. Run `kubebuilder init --domain ackstorm.ai --repo github.com/ackstorm/ach --owner ACKstorm` to seed the project file + Makefile + skeleton.
2. Run `kubebuilder create api --group ach --version v1alpha1 --kind <Kind>` six times (Environment, Plugin, PluginMarketplace, Artifact, Prompt, BackendIdentityPolicy), each `--resource --controller`.
3. Layer the four extra `cmd/<binary>/main.go` stubs and the migration init container on top of the kubebuilder-generated scaffolding.

### Integration Points

All forward-looking — Phase 1 is the bottom of the dependency graph:

- **Phase 2** swaps `internal/litellm.NoopClient` for the real REST client; turns on external-ref refresh against the cache layout Phase 1 created; consumes `ACH_PLUGIN_MAX_SIZE_MIB`.
- **Phase 3** writes the first real rows into `personal_keys` and `environment_keys`; uses `internal/credhash` for the first time; promotes Platform API stub to a real binary.
- **Phase 4** promotes Forwarder stub; reads `BackendIdentityPolicy` from the informer Phase 1 wired.
- **Phase 5** promotes Content Service stub to real `sendfile(2)` streaming; reads the PVC cache layout Phase 1 created.
- **Phase 7** packages all five binaries + the `config/` kustomize manifests into a Helm chart.

</code_context>

<specifics>
## Specific Ideas

- The user explicitly pointed to `../ach_litellm` mid-discussion as the layout/conventions reference. **Phase 1 should pattern-match the sister project as closely as practical**: kubebuilder v4 layout, `multigroup: true`, `domain: ackstorm.ai`, `internal/<subsystem>/` split (`internal/connection/`, `internal/controller/`, `internal/litellm/`, `internal/metrics/`, `internal/substitution/` pattern → ACH's analogs `internal/controller/`, `internal/litellm/`, `internal/db/`, `internal/metrics/`, `internal/credhash/`), same Makefile target names, same `.golangci.yml` style, same boilerplate header.
- Phase 1 SC #2 is load-bearing for the Pod-topology check: `kubectl describe pod` MUST show **two ready containers** and **one PVC**. The Content Service stub container in Phase 1 must be a real long-running process that passes readiness — a sidecar that immediately exits would fail SC #2.
- Phase 1 SC #4 (`psql` shows tables + UNIQUE constraints + pepper outside DB) demands a real Postgres instance in the dev/test path. Use testcontainers-go for unit tests; docker-compose Postgres for local dev.
- The `pkid_`/`ekid_` prefix convention (Hub §16) is enforced at the DB layer (CHECK constraints on `key_id`). Phase 1's migration sets up those CHECK constraints — Phase 3's INSERT statements will rely on them.

</specifics>

<deferred>
## Deferred Ideas

Discussion stayed within Phase 1 scope. Items intentionally out of Phase 1 (already mapped to later phases by REQUIREMENTS.md):

- LiteLLM REST client (Phase 2 — OP-03, OP-13)
- External-reference refresh + marketplace materialization (Phase 2 — OP-03/06/07/08/09)
- Orphan LiteLLM key cleanup loop (Phase 2 — OP-15)
- pk_/ek_ lifecycle, Dex SSO, hydrate endpoint, admin endpoints (Phase 3 — KEY/API)
- Forwarder route handlers, JWKS publication, JWT signing (Phase 4 — FWD)
- BackendIdentityPolicy duplicate-target status reconciliation (Phase 4 — OP-14, OP-16)
- Content Service streaming + scope-aware authorization (Phase 5 — CS)
- Prometheus metric endpoints (Phase 5 — OBS-03..OBS-06)
- CLI command surface (Phase 6 — CLI)
- Hydrate engine + adapters + safe extraction + Helm packaging (Phase 7 — STATE/ADAPT/SAFE/DIST)
- Pepper rotation tooling — v1beta1 backlog (per Hub §20)

</deferred>

---

*Phase: 1-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Context gathered: 2026-05-15*
