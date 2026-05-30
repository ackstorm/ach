---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 10
subsystem: container-images
tags: [dockerfile, distroless, multi-stage, golang-1.23, migrate-helper, docker-compose, local-dev]

# Dependency graph
requires:
  - phase: 01-01
    provides: "go.mod at github.com/ackstorm/ach, cmd/ binary tree (operator, platform-api, forwarder, content-service, ach), Makefile target conventions"
  - phase: 01-03
    provides: "internal/db.Migrate(url, path) — the entry point cmd/migrate/main.go wraps; db/migrations/000001_init.{up,down}.sql baked into Dockerfile.operator"
  - phase: 01-06
    provides: "internal/config.MustEnvNonEmpty/EnvOr — env-var helpers cmd/migrate/main.go uses"
  - phase: 01-08
    provides: "Pod manifests reference ach-operator:latest with command:[/migrate] for the migrations init container; expects two binaries + /db/migrations baked into the operator image"
provides:
  - "cmd/migrate/main.go — thin internal/db.Migrate wrapper invoked by Plan 08's init container; fails fast on empty ACH_DB_URL (D-08); structured slog JSON to stderr; defaults ACH_MIGRATIONS_PATH to /db/migrations"
  - "Dockerfile.operator — multi-stage golang:1.23 -> distroless/static:nonroot; builds BOTH /operator AND /migrate; COPY db/migrations /db/migrations; ENTRYPOINT [/operator] (init container overrides via Pod spec command)"
  - "Dockerfile.platform-api / Dockerfile.forwarder / Dockerfile.content-service / Dockerfile.ach — same multi-stage shape, single binary, ENTRYPOINT matching binary name"
  - ".dockerignore — excludes .git/, .planning/, .gocache/, bin/, testbin/, cover.out, spec markdown, devcontainer/IDE noise (T-10-02 mitigation)"
  - "docker-compose.yml — local-dev Postgres 16 + Redis 7 on :5432 / :6379 with healthchecks; persistent postgres_data volume"
  - "Makefile docker-build target builds all five images with ach-{operator,platform-api,forwarder,content-service}:latest + ach-cli:latest tags Plan 08 manifests reference"
  - "Makefile dev-up / dev-down targets invoke standalone docker-compose binary"
affects:
  - "01-11 (verifier): can now exercise the full image build pipeline (`make docker-build`) and verify Phase 1 SC #2 + SC #4 against a local kind cluster pulling these images"
  - "Phase 7 Helm chart: image references resolve to ach-{operator,platform-api,forwarder,content-service,cli}:latest by default; chart values.yaml overrides with --set image.repository/tag"
  - "Phase 4+ forwarder: docker-compose's redis service is reserved for the key-resolution cache (§16.x); Phase 1 binaries do not read from it"

# Tech tracking
tech-stack:
  added: []  # zero new go.mod entries — Plan 10 ships Dockerfiles + YAML + a thin Go main
  patterns:
    - "Multi-stage Docker build: golang:1.23 (full Go toolchain) -> gcr.io/distroless/static:nonroot (minimal runtime, no shell, no package manager, UID 65532). Pattern lifted from sister project ach_litellm/Dockerfile."
    - "Statically-compiled binaries via CGO_ENABLED=0 so the distroless/static base (no glibc) hosts them. -a flag forces full rebuild ignoring cached package files — defends against stale builder-stage layers."
    - "Dockerfile.operator carries two binaries + a static asset directory in one image. The /operator default ENTRYPOINT serves the controller-manager container; Plan 08's init container overrides via `command: [\"/migrate\"]` in the Pod spec. No emptyDir volume for migrations: they live in the image (T-10-04 immutability)."
    - "Per-binary Dockerfile (not a single multi-target Dockerfile with ARG-selected ENTRYPOINT). Five files = five build contexts = five OCI images with distinct manifests, which Phase 7's Helm chart distributes individually. BuildKit named contexts could deduplicate the builder stage; deferred to Phase 7 distribution optimization."
    - "docker-compose for local-dev only. Production deployment is the K8s manifest set in config/ (Plan 08); this compose file is just for `make test-integration` and ad-hoc psql/redis-cli on the developer's host. Healthchecks (pg_isready, redis-cli ping) make `docker-compose up -d --wait` viable in CI scripts."

key-files:
  created:
    - cmd/migrate/main.go (67 lines — Apache-2.0 header, slog JSON to stderr, config.MustEnvNonEmpty fail-fast)
    - Dockerfile.operator (51 lines — two binaries + COPY db/migrations)
    - Dockerfile.platform-api (29 lines — single binary)
    - Dockerfile.forwarder (29 lines — single binary)
    - Dockerfile.content-service (32 lines — single binary; SC #2 long-running container)
    - Dockerfile.ach (32 lines — single binary CLI stub)
    - docker-compose.yml (53 lines — Postgres + Redis with healthchecks)
  modified:
    - .dockerignore (4 -> 47 lines — full exclusion list per T-10-02; excludes .git/, .planning/, .gocache/, bin/, testbin/, cover.out, spec md, devcontainer/IDE noise)
    - Makefile (docker-build target now builds all five images; new dev-up/dev-down targets)

key-decisions:
  - "Image entrypoint is `/operator` (not multi-purpose). Plan 08's init container overrides via Pod-spec `command: [\"/migrate\"]` — that is the K8s-idiomatic way to pick a sub-binary out of a multi-binary image. Plan-text was unambiguous; locked in."
  - "ACH_MIGRATIONS_PATH default is `/db/migrations` (compiled-in via const in cmd/migrate/main.go). The Dockerfile.operator `COPY db/migrations /db/migrations` matches. Plan 08's init container does not set ACH_MIGRATIONS_PATH explicitly — the default suffices, keeping the manifest minimal."
  - "Dockerfile.ach exists in Phase 1 even though Phase 6 owns the real CLI surface. The Makefile `docker-build` target consequently produces five images, not four. Rationale: the distribution pipeline (Phase 7 Helm + OCI) has a real artifact to reference end-to-end from Phase 1; the stub image's ENTRYPOINT exits 1 with `not yet implemented` so anyone running it gets a clear signal."
  - "Makefile dev-up/dev-down invoke `docker-compose` (hyphen, standalone binary) NOT `docker compose` (v2 plugin). The plan's `<verify>` block used `docker compose config`, which fails on hosts that have only the v2 standalone — including this host. Sister project's scripts/spike.sh uses `docker-compose` for the same reason. DOCKER_COMPOSE Makefile knob lets users override (e.g. DOCKER_COMPOSE='docker compose'). Rule 3 inline fix; documented in the decisions section here for traceability."
  - ".dockerignore EXCLUDES `Dockerfile.devtools` and `docker-compose.yml` from the build context. Neither belongs inside a production image (devtools is the developer toolchain image; compose is local-dev only). Net effect: faster image builds + zero leakage of dev-only manifests."

patterns-established:
  - "Per-binary Dockerfile naming: `Dockerfile.<binary>` at repo root. Five Phase 1 images: ach-operator, ach-platform-api, ach-forwarder, ach-content-service, ach-cli. Phase 7 may add ach-* image overrides via Helm values; pattern stays."
  - "Multi-binary image convention: build N binaries in a single builder stage with chained `go build` commands, then COPY each into the runtime stage. ENTRYPOINT picks the primary binary; Pod spec `command:` overrides for sibling-binary use cases (init containers, sidecars, one-shot jobs)."
  - "Migration-files-in-image (no separate ConfigMap/Volume). Migrations are versioned source code; they belong baked into the same image that runs them so a pulled image is hermetic. T-10-04 in the threat register notes this is by design: changing migrations requires a new image build."

requirements-completed: []  # Phase 1 Plan 10 does not retire any REQ-IDs by itself; it unblocks Phase 1 SC #2 + SC #4 verification (Plan 11)

# Metrics
duration: ~9min
completed: 2026-05-15
---

# Phase 1 Plan 10: Container Images + Local Dev Stack Summary

**Five multi-stage Dockerfiles landed at repo root (one per binary, all golang:1.23 -> gcr.io/distroless/static:nonroot running as USER 65532:65532). `Dockerfile.operator` is the richest: it builds both `/operator` and `/migrate` in one builder stage and bundles `/db/migrations/*.sql` so Plan 08's init container can run `command: ["/migrate"]` against the same image. `cmd/migrate/main.go` is the thin slog-bearing wrapper around `internal/db.Migrate` — fails fast on empty `ACH_DB_URL` (D-08). `docker-compose.yml` provisions local-dev Postgres 16 + Redis 7 with healthchecks; `make dev-up` is the developer's one-liner. `.dockerignore` excludes `.git/`, `.planning/`, `.gocache/`, `bin/`, `testbin/`, `cover.out`, and spec markdown so the build context stays minimal. Smoke tests: `docker build -f Dockerfile.operator` exits 0 and produces an image containing exactly `/operator`, `/migrate`, and `/db/migrations/000001_init.{up,down}.sql`; `docker build -f Dockerfile.ach` exits 0 and `docker run --rm ach-cli:test` exits 1 with `not yet implemented`. `./scripts/dev.sh go build ./...` and `./scripts/dev.sh make fmt vet` both exit 0.**

## Performance

- **Duration:** ~9 min
- **Started:** 2026-05-15 (sequential executor on main tree)
- **Completed:** 2026-05-15
- **Tasks:** 3 / 3
- **Files created:** 7 (1 Go + 5 Dockerfile + 1 compose)
- **Files modified:** 2 (.dockerignore, Makefile)

## Image Inventory

| Image                          | Binary entrypoint    | Extra contents                       | Referenced by |
|--------------------------------|----------------------|--------------------------------------|---------------|
| `ach-operator:latest`          | `/operator`          | `/migrate` + `/db/migrations/*.sql`  | Plan 08 `config/manager/manager.yaml` — manager container AND migrations init container |
| `ach-platform-api:latest`      | `/platform-api`      | (none)                               | Plan 08 `config/deployments/platform-api_deployment.yaml` |
| `ach-forwarder:latest`         | `/forwarder`         | (none)                               | Plan 08 `config/deployments/forwarder_deployment.yaml` |
| `ach-content-service:latest`   | `/content-service`   | (none)                               | Plan 08 `config/manager/manager.yaml` — content-service co-located container |
| `ach-cli:latest`               | `/ach`               | (none)                               | Phase 6 (CLI surface); Phase 1 ship for distribution pipeline only |

All five run as non-root (USER 65532:65532 — distroless `nonroot` UID), compatible with Plan 08's `runAsNonRoot: true` + `readOnlyRootFilesystem: true` security contexts.

## Task Commits

1. **Task 1: `cmd/migrate/main.go` init-container helper** — `29bfe47` (feat)
2. **Task 2: Five Dockerfiles + .dockerignore + Makefile docker-build update** — `6f24cb3` (feat)
3. **Task 3: `docker-compose.yml` + Makefile dev-up/dev-down** — `7e9bd16` (feat)

## Verification Outcomes

- `./scripts/dev.sh go build ./...` -> exit 0 (now compiles the new `./cmd/migrate` package alongside the existing four binaries).
- `./scripts/dev.sh make fmt vet` -> exit 0.
- `./scripts/dev.sh go build -o ./bin/migrate ./cmd/migrate` -> exit 0; binary is executable (13 MiB statically-linked).
- `ACH_DB_URL="" ./bin/migrate` -> exit 1 with structured stderr JSON: `{"level":"ERROR","msg":"ACH_DB_URL is required for migrate","err":"config: ACH_DB_URL is empty or unset"}`. D-08 fail-fast confirmed.
- `docker build -f Dockerfile.ach -t ach-cli:test .` -> exit 0.
- `docker run --rm ach-cli:test` -> exit 1, stderr `ach v0.1.0-phase1-stub — not yet implemented (CLI lands in Phase 6)`. Stub contract preserved.
- `docker build -f Dockerfile.operator -t ach-operator:test .` -> exit 0; image contents (via `docker export | tar -tf`) confirm `/operator`, `/migrate`, `/db/migrations/000001_init.up.sql`, `/db/migrations/000001_init.down.sql` at the expected paths.
- `docker-compose config` -> exit 0 (validates schema + interpolation).
- `.dockerignore` exclusions verified by grep: `bin/`, `testbin/`, `.git/`, `.planning/`, `.gocache/`, `cover.out` all present.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] `docker compose config` plan-verify command fails on hosts without the v2 plugin**

- **Found during:** Task 3 acceptance verification.
- **Issue:** The plan's `<verify>` block for Task 3 specified `docker compose config > /dev/null`. The execution host has the standalone `docker-compose` (v2.29.2) binary but no `docker compose` subcommand plugin — so the plan's verify command exits with `'compose' is not a docker command`. The compose file itself is structurally valid (the standalone `docker-compose config` exits 0).
- **Fix:** Used the standalone `docker-compose` binary for verification and wired the Makefile `dev-up`/`dev-down` targets to invoke `docker-compose` (overridable via `DOCKER_COMPOSE` Makefile knob). Matches sister project ach_litellm/scripts/spike.sh which also uses `docker-compose` (hyphen) throughout. Users with the modern subcommand plugin can override via `DOCKER_COMPOSE='docker compose'`.
- **Files modified:** Makefile.
- **Verification:** `docker-compose config` exits 0; `make dev-up` works against the local Docker daemon.
- **Committed in:** `7e9bd16` (Task 3 commit).

### Deferred Items

None. All Phase 1 Plan 10 scope landed in this run.

## Notes for Plan 11 (Verifier)

Plan 11's e2e gates that touch this plan's surface:

1. **`make docker-build` builds all five images.** Verifier runs `make docker-build` against a clean Docker cache; expects five `ach-*:latest` tags after success.
2. **Operator image contains `/migrate` + `/db/migrations/*.sql`.** After `make docker-build`, verifier runs `docker create ach-operator:latest` and inspects the resulting layer (e.g. `docker export <CID> | tar -tf - | grep -E '^(migrate|db/migrations/)'`) — expects 3 matching entries.
3. **All five images run as UID 65532.** Verifier runs `docker inspect ach-<binary>:latest --format '{{.Config.User}}'` for each — expects `65532:65532`.
4. **`make dev-up && psql $ACH_DB_URL -c '\dt'`** can be exercised against the local-dev Postgres (post Plan 08 init container apply against a kind cluster, but the dev-up path is the developer-machine flow).
5. **`docker run --rm ach-cli:latest` exits non-zero with `not yet implemented` in stderr** (CLI stub contract — must hold until Phase 6 promotes the binary).

## Threat Flags

No new security-relevant surface introduced beyond what the plan's `<threat_model>` already enumerates. The five Dockerfiles + the migrate helper all match the documented STRIDE register entries (T-10-01..T-10-SC). No new endpoints, no new auth paths, no new trust boundaries.

## Self-Check: PASSED

Files claimed in this summary:

- `cmd/migrate/main.go` — FOUND
- `Dockerfile.operator` — FOUND
- `Dockerfile.platform-api` — FOUND
- `Dockerfile.forwarder` — FOUND
- `Dockerfile.content-service` — FOUND
- `Dockerfile.ach` — FOUND
- `.dockerignore` — FOUND (modified)
- `docker-compose.yml` — FOUND
- `Makefile` — FOUND (modified)

Commits claimed:

- `29bfe47` — FOUND (Task 1)
- `6f24cb3` — FOUND (Task 2)
- `7e9bd16` — FOUND (Task 3)

---

*Phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Completed: 2026-05-15*
