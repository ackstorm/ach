---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 01
subsystem: infra
tags: [kubebuilder, controller-runtime, go-modules, makefile, golangci-lint]

# Dependency graph
requires: []
provides:
  - kubebuilder v4 project scaffold (PROJECT, go.mod, Makefile, hack/boilerplate.go.txt, .golangci.yml)
  - Go module identity `github.com/ackstorm/ach` (single-module repo per D-02)
  - Multigroup CRD layout enabled (PROJECT `multigroup: true` per D-01)
  - Five-binary Makefile build target (operator/platform-api/forwarder/content-service/ach per D-03)
  - Empty `api/` and `internal/` package trees for Plan 02/04 to populate
  - Sister-conform tooling versions (controller-gen v0.17.0, kustomize v5.5.0, envtest derived from controller-runtime, golangci-lint v1.62.2)
affects:
  - 01-02 (CRD scaffolding via `kubebuilder create api`)
  - 01-04 (`internal/credhash` package will live under the already-created internal/ tree)
  - 01-05 (controller files in internal/controller/)
  - 01-06 (per-binary `cmd/<name>/main.go` entrypoints replace `cmd/main.go`)
  - 01-10 (per-binary Dockerfiles — repo-root Dockerfile already removed)
  - All later phases consume this scaffolding unchanged

# Tech tracking
tech-stack:
  added:
    - sigs.k8s.io/controller-runtime v0.19.4
    - k8s.io/apimachinery v0.31.0
    - k8s.io/client-go v0.31.0
    - github.com/onsi/ginkgo/v2 v2.19.0
    - github.com/onsi/gomega v1.33.1
    - controller-tools/controller-gen v0.17.0 (build-tool)
    - sigs.k8s.io/kustomize/kustomize/v5 v5.5.0 (build-tool)
    - sigs.k8s.io/controller-runtime/tools/setup-envtest (release-0.19, build-tool)
    - golangci-lint v1.62.2 (build-tool)
  patterns:
    - kubebuilder v4 layout (api/, cmd/, config/, internal/, hack/, test/)
    - Multigroup CRD organization (D-01)
    - Single-module repo (no `replace` directives, no vendored types — D-02)
    - controller-gen paths scoped to ./api/... and ./internal/... (sister convention; avoids module-cache descent)
    - Containerized toolchain via ./scripts/dev.sh (D-16)

key-files:
  created:
    - PROJECT (kubebuilder project metadata)
    - go.mod, go.sum
    - Makefile (with 5-binary build target)
    - hack/boilerplate.go.txt (Apache-2.0, Copyright 2026 ACKstorm.)
    - .golangci.yml (verbatim sister config)
    - .dockerignore
    - .devcontainer/devcontainer.json, .devcontainer/post-install.sh
    - .github/workflows/{lint,test,test-e2e}.yml
    - cmd/main.go (kubebuilder placeholder; Plan 06 replaces with cmd/<binary>/main.go)
    - api/.gitkeep, internal/.gitkeep
    - config/{default,manager,network-policy,prometheus,rbac}/* (kubebuilder defaults)
    - test/e2e/, test/utils/
    - README.md (one-paragraph stub)
  modified: []

key-decisions:
  - "PROJECT.multigroup set to true (kubebuilder init defaults to false; D-01 mandates multigroup)"
  - "PROJECT.projectName overridden to 'ach' (kubebuilder defaulted to 'workspace' from cwd basename)"
  - "Makefile manifests/generate targets scoped to ./api/... and ./internal/... (not the kubebuilder default ./...) because controller-gen's package loader descends into the read-only Go module cache when given ./..., failing with `permission denied`. This is the same fix the sister project applies."
  - "Empty api/ and internal/ directories created with .gitkeep so controller-gen's `lstat ./api` succeeds on an otherwise empty scaffold; Plan 02 and Plan 04 will populate them with real Go files."

patterns-established:
  - "Containerized toolchain: every go/kubebuilder/make/controller-gen invocation goes through ./scripts/dev.sh (D-16)"
  - "Scoped controller-gen paths: ./api/... and ./internal/... only (sister convention)"
  - "Single Go module rooted at github.com/ackstorm/ach for both Hub binaries and CLI (D-02)"
  - "Apache-2.0 boilerplate (Copyright 2026 ACKstorm.) prepended to all generated and hand-written Go files via hack/boilerplate.go.txt"

requirements-completed: []  # Plan 01-01 frontmatter declares requirements: []; this plan is scaffolding only (OP-01 substantively satisfied by 01-05 + 01-09 per must_haves.truths)

# Metrics
duration: ~7min
completed: 2026-05-15
---

# Phase 1 Plan 1: Foundation — Kubebuilder Bootstrap Summary

**Kubebuilder v4 scaffolding at `github.com/ackstorm/ach` with multigroup CRD layout, 5-binary Makefile build target, controller-runtime v0.19.4, and sister-conform tooling versions (controller-gen v0.17.0, kustomize v5.5.0, golangci-lint v1.62.2).**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-05-15T13:04:00Z (approx; first kubebuilder init invocation)
- **Completed:** 2026-05-15T13:11:05Z
- **Tasks:** 1 / 1
- **Files modified:** 41 (entire kubebuilder scaffold + post-init edits, single atomic commit)

## Accomplishments

- `kubebuilder init --domain ackstorm.ai --repo github.com/ackstorm/ach --owner ACKstorm` succeeded.
- PROJECT file declares `domain: ackstorm.ai`, `repo: github.com/ackstorm/ach`, `multigroup: true`, `projectName: ach`, `version: "3"` — matches sister-project shape (D-01).
- `go.mod` declares `module github.com/ackstorm/ach`, Go 1.23.0, controller-runtime v0.19.4, ginkgo v2.19, gomega v1.33 — matches sister baseline (D-02).
- Makefile `build` target lists all five ACH binaries per D-03 (`./cmd/operator`, `./cmd/platform-api`, `./cmd/forwarder`, `./cmd/content-service`, `./cmd/ach`).
- `.golangci.yml` and `hack/boilerplate.go.txt` are byte-for-byte identical to the sister project (kubebuilder generated them in the same shape — verified via `diff -q`).
- Repo-root kubebuilder-generated `Dockerfile` removed (Plan 10 ships per-binary Dockerfiles); `Dockerfile.devtools` (D-16) preserved.
- `README.md` reduced to a one-paragraph stub pointing at `.planning/PROJECT.md` and `.planning/ROADMAP.md`.
- Verification: `./scripts/dev.sh go build ./...` exits 0; `./scripts/dev.sh make manifests generate fmt vet` exits 0.

## Pinned Tool Versions (recorded per <output> instruction)

| Tool                              | Version            | Source                                      |
|-----------------------------------|--------------------|---------------------------------------------|
| Go                                | 1.23.0             | go.mod (`go 1.23.0`)                        |
| sigs.k8s.io/controller-runtime    | v0.19.4            | go.mod                                       |
| k8s.io/apimachinery, api, client-go | v0.31.0          | go.mod                                       |
| github.com/onsi/ginkgo/v2         | v2.19.0            | go.mod                                       |
| github.com/onsi/gomega            | v1.33.1            | go.mod                                       |
| controller-tools/controller-gen   | v0.17.0            | Makefile `CONTROLLER_TOOLS_VERSION`         |
| sigs.k8s.io/kustomize/kustomize/v5 | v5.5.0            | Makefile `KUSTOMIZE_VERSION`                |
| setup-envtest                     | release-0.19 (derived) | Makefile `ENVTEST_VERSION` (computed from controller-runtime version) |
| ENVTEST_K8S_VERSION               | 1.31 (derived)     | Makefile (computed from k8s.io/api version)  |
| golangci-lint                     | v1.62.2            | Makefile `GOLANGCI_LINT_VERSION`            |
| kubebuilder (host tool)           | 4.4.0              | `kubebuilder version` (Kubernetes vendor 1.31.0) |

These exact versions match `ach_litellm/go.mod` and `ach_litellm/Makefile` byte-for-byte. Downstream plans (01-02..01-11, Phase 2+) inherit this version pin set unchanged.

## Task Commits

1. **Task 1: Run `kubebuilder init` and verify the seed scaffolding** — `b79c625` (feat)

**Plan metadata commit:** appended below this SUMMARY commit.

## Files Created/Modified

Kubebuilder-generated (kept verbatim except as noted):
- `.devcontainer/devcontainer.json`, `.devcontainer/post-install.sh`
- `.dockerignore`
- `.github/workflows/{lint,test,test-e2e}.yml`
- `.golangci.yml` (byte-identical to sister)
- `cmd/main.go` (kubebuilder placeholder; Plan 06 replaces)
- `config/default/`, `config/manager/`, `config/network-policy/`, `config/prometheus/`, `config/rbac/` (kustomize manifests)
- `go.mod`, `go.sum`
- `hack/boilerplate.go.txt` (byte-identical to sister)
- `test/e2e/e2e_suite_test.go`, `test/e2e/e2e_test.go`, `test/utils/utils.go`

Post-init edits:
- `PROJECT` — added `multigroup: true`; set `projectName: ach` (kubebuilder defaulted to `workspace` from cwd basename, which is wrong here).
- `Makefile` — replaced single-binary build target with five-binary list; scoped `manifests`/`generate` `paths=` to `./api/...` and `./internal/...`.
- `README.md` — replaced kubebuilder TODO scaffold with one-paragraph project description.
- `api/.gitkeep`, `internal/.gitkeep` — created so controller-gen's `lstat ./api` succeeds on the empty scaffold.
- Repo-root `Dockerfile` — deleted (Plan 10 ships per-binary Dockerfiles).

## Decisions Made

- **PROJECT.projectName:** Kubebuilder defaulted to `workspace` (cwd basename). Overrode to `ach` to match the canonical project identity. The plan acceptance criteria require `projectName: ach`.
- **Scoped controller-gen paths:** The kubebuilder-default `paths="./..."` causes controller-gen's package loader to descend into the Go module cache (read-only at chmod 555 under `.gocache/`), which then tries to "update" `go.mod` files in the cache and errors out with `permission denied`. The sister project (`ach_litellm/Makefile` line 52) applies the exact same fix — scope to `./api/...` and `./internal/...`. Per the plan's pattern map (Repo bootstrap section, line 1004-1014), the sister Makefile is the verbatim lift target; using its scoped-path approach here is faithful to the planner's intent.
- **api/ and internal/ stub trees:** controller-gen errors out with `lstat /workspace/api: no such file or directory` if the path doesn't exist. Created empty directories with `.gitkeep` so the manifests/generate targets exit 0 on a CRD-less scaffold. Plan 02 will populate `api/ach/v1alpha1/` with CRD types; Plan 04 will populate `internal/credhash/`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Scoped controller-gen paths to ./api/... and ./internal/...**
- **Found during:** Task 1 verify (`make manifests generate fmt vet`)
- **Issue:** Kubebuilder's default `paths="./..."` in the `manifests` and `generate` targets caused controller-gen to descend into the read-only Go module cache (`/workspace/.gocache/gopath/pkg/mod/`), failing with `go: updating go.mod: open .../go.mod: permission denied`. The chmod-555 modcache is a Go-tooling invariant — the modcache MUST be read-only.
- **Fix:** Changed `paths="./..."` to `paths="./api/..." paths="./internal/..."` in the `manifests` target, and `paths="./..."` to `paths="./api/..."` in the `generate` target. Added an explanatory comment block above the targets describing why. This mirrors the sister project verbatim (`ach_litellm/Makefile` lines 50-56) — the same workaround the planner referenced in PATTERNS.md as the canonical lift.
- **Files modified:** `Makefile`
- **Verification:** `./scripts/dev.sh make manifests generate fmt vet` exits 0 after the fix.
- **Committed in:** `b79c625` (part of Task 1 commit)

**2. [Rule 3 — Blocking] Created empty api/ and internal/ stub directories with .gitkeep**
- **Found during:** Task 1 verify (immediately after fix #1)
- **Issue:** After scoping paths to `./api/...` and `./internal/...`, controller-gen failed with `lstat /workspace/api: no such file or directory` — the directories don't exist yet (Plans 02 and 04 create them).
- **Fix:** Created empty `api/` and `internal/` directories with `.gitkeep` files. controller-gen accepts an empty directory and emits no output (correct behavior for a scaffold with no Go types).
- **Files modified:** `api/.gitkeep`, `internal/.gitkeep` (new)
- **Verification:** `make manifests generate fmt vet` exits 0; no spurious output in `config/crd/bases/`.
- **Committed in:** `b79c625` (part of Task 1 commit)

**3. [Rule 2 — Missing Critical] Plan step 5 (.gitignore append) was a no-op; pre-existing .gitignore already covers all four entries**
- **Found during:** Task 1 step 5
- **Issue:** Plan step 5 says: 'Append to `.gitignore`: `/bin/`, `/testbin/`, `/cover.out`, `/.gocache/`'. The pre-existing D-16-era `.gitignore` already contains broader globs (`bin/`, `testbin/`, `cover.out`, `.gocache/`) which functionally cover the same patterns (without the leading slash, they match in any subdir; with it, only at repo root — broader is fine for these patterns).
- **Fix:** Step 5 was skipped. No duplicate entries added. The plan-specifics note in the executor prompt explicitly flagged this: "/.gocache/ is already in `.gitignore` — don't duplicate it"; the same logic extends to the other three.
- **Files modified:** None.
- **Verification:** `cat .gitignore` shows all four patterns present.
- **Committed in:** N/A (no change).

---

**Total deviations:** 3 (2 blocking auto-fixes, 1 no-op-step note)
**Impact on plan:** All adjustments preserve plan intent. The scoped-paths fix is verbatim sister-convention referenced by PATTERNS.md. The stub directories let Plans 02 and 04 land their content without further Makefile edits. The .gitignore no-op preserves the D-16 setup intact.

## Issues Encountered

- **Kubebuilder init rejected non-dot/non-CAPS/non-.md files in the workspace.** The pre-existing D-16 setup files (`Dockerfile.devtools`, `scripts/`) tripped kubebuilder's "target directory is not empty" guard. Resolved by temporarily moving them to `/tmp/ach-bootstrap-stash/` during the init, then restoring them. The `scripts/dev.sh` wrapper resolves `WORKSPACE` from `BASH_SOURCE`, so it had to be invoked directly via `docker run` for this single bootstrap step — restored to normal `./scripts/dev.sh <cmd>` usage immediately after. Documented in the commit message.
- **First make manifests/generate invocations failed before `go build` had primed the module cache.** Running `./scripts/dev.sh go build ./...` first to materialize and lock-down the modcache, then re-running `make manifests generate fmt vet`, worked. This is a one-time bootstrap concern — subsequent invocations are fast.

## User Setup Required

None — no external service configuration required. The containerized toolchain (D-16) is already set up; Postgres + Redis + LiteLLM dependencies are not introduced until later plans.

## Next Phase Readiness

- **Plan 01-02 (CRD scaffolding):** Ready. `kubebuilder create api --group ach --version v1alpha1 --kind <Kind>` can be invoked six times without further setup. The `multigroup: true` and `domain: ackstorm.ai` settings ensure the generated paths land at `api/ach/v1alpha1/<kind>_types.go` as the plan map expects.
- **Plan 01-04 (credhash):** Ready. `internal/` directory tree exists; `internal/credhash/` can be created directly.
- **Plan 01-06 (per-binary mains):** Ready. Makefile build target already lists the five binaries — Plan 06 just needs to drop `cmd/<binary>/main.go` files into the cmd/ tree.
- **Plan 01-10 (Dockerfiles):** Ready. Repo-root Dockerfile is already removed.
- No blockers, no concerns.

## Self-Check: PASSED

- [x] `PROJECT` exists with `domain: ackstorm.ai`, `repo: github.com/ackstorm/ach`, `multigroup: true`, `projectName: ach`, `version: "3"` — confirmed via `grep`.
- [x] `go.mod` first line is `module github.com/ackstorm/ach`, Go directive is `go 1.23.0` — confirmed via `head -3 go.mod`.
- [x] `Makefile` `build:` target lists all five `go build -o bin/<name> ./cmd/<name>` lines — confirmed.
- [x] `.golangci.yml` byte-equal to sister: `diff -q .golangci.yml /home/jcm/Projects/ach_litellm/.golangci.yml` returned silently (match).
- [x] `hack/boilerplate.go.txt` contains `Copyright 2026 ACKstorm.` and `http://www.apache.org/licenses/LICENSE-2.0` — confirmed via `grep`.
- [x] `./scripts/dev.sh go build ./...` exits 0 — confirmed (exit code captured).
- [x] `./scripts/dev.sh make manifests generate fmt vet` exits 0 — confirmed (clean output).
- [x] `cmd/main.go` exists (kubebuilder placeholder).
- [x] No `Dockerfile` at repo root — confirmed via `test ! -f Dockerfile`.
- [x] `.planning/` directory untouched — confirmed via `git status` (no entries under `.planning/`).
- [x] Task 1 committed at `b79c625` — confirmed via `git log --oneline -3`.

---
*Phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Completed: 2026-05-15*
