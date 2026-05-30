# Phase 1: Foundation — CRDs, DB Schema, Operator Skeleton, Multi-tenancy - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in `01-CONTEXT.md` — this log preserves the alternatives considered.

**Date:** 2026-05-15
**Phase:** 1-foundation-crds-db-schema-operator-skeleton-multi-tenancy
**Areas discussed:** Repo & module layout, CRD scaffolding & code-gen, DB schema & migration ownership, Credential pepper sourcing

---

## Gray Area Selection

Initial multi-select offered four areas. User responded with a free-form request: *"check this brother project ../ach_litellm"* — a redirect to study the sister project at `/home/jcm/Projects/ach_litellm` before answering.

**Action taken:** Read `../ach_litellm/PROJECT`, `go.mod`, `Makefile`, `api/litellm/v1alpha1/`, `internal/controller/`, `config/{crd,rbac,manager}/`, `hack/boilerplate.go.txt`. Confirmed sister is canonical kubebuilder v4 (`go.kubebuilder.io/v4` layout, `multigroup: true`, `domain: ackstorm.ai`, controller-runtime v0.19.4, Ginkgo/Gomega/envtest, kustomize-managed manifests). This effectively answers gray areas 1 (repo layout) and 2 (CRD scaffolding) — "mirror what they did." Remaining gray areas (DB migrations, pepper sourcing) the sister project doesn't speak to.

Re-asked three focused follow-ups.

---

## Repo & Module Layout

| Option | Description | Selected |
|--------|-------------|----------|
| Mirror + 4 cmd/<hub-binary>/ + cmd/ach/ | kubebuilder v4 + multigroup; Phase 1 scaffolds cmd/operator/main.go (real), plus empty cmd/platform-api/, cmd/forwarder/, cmd/content-service/, cmd/ach/ stub mains so Phase 3+ has somewhere to land. api/ach/v1alpha1/ holds all 6 CRD types. internal/{controller,db,litellm,metrics} as it grows. | ✓ |
| Mirror + only cmd/operator/ in Phase 1 | Same kubebuilder layout but Phase 1 ships ONLY the operator binary. Phase 3/4/5 add their own cmd/<binary>/main.go when they need them. Smaller Phase 1 surface; some risk of layout drift when later phases land. | |
| Two kubebuilder projects (Hub + CLI separate) | Hub stays kubebuilder canonical; CLI lives in a separate repo or a separate go.mod under a sibling dir (since CLI doesn't need controller-runtime + apiserver deps). Cleaner CLI binary size, but loses shared api/ach/v1alpha1/ types between Hub and CLI. | |

**User's choice:** Mirror + 4 cmd/<hub-binary>/ + cmd/ach/
**Notes:** Lock contract for all five binaries in Phase 1. Stubs need `/healthz` and must pass readiness so Pod-topology Success Criterion #2 (two-containers ready) is satisfied for the Operator+CS pod. The other two binaries (Platform API, Forwarder) run in their own Deployments — their stubs exist for manifest completeness, not for Phase 1 SC.

---

## DB Schema & Migration Ownership

| Option | Description | Selected |
|--------|-------------|----------|
| golang-migrate, run by Operator init container | Industry-standard migrations as numbered .sql files under db/migrations/. A dedicated init container in the Operator+CS pod runs migrate up before either container starts. Single-replica Recreate pod = no race. | ✓ |
| golang-migrate, embedded in Operator at startup | Same tooling, but the Operator binary calls migrate.Up() in main() before starting controller-manager. No extra container. Operator becomes the schema owner. | |
| Separate K8s migration Job (Helm pre-install/pre-upgrade) | Helm chart ships a Job with migrate up gated by helm.sh/hook: pre-install,pre-upgrade. Cleanest separation but adds Helm hook ordering complexity for Phase 7. | |
| sqlc with embedded migrations + Operator startup | sqlc for type-safe queries + golang-migrate embedded via go:embed running on Operator startup. More Go-idiomatic but doubles tooling surface in Phase 1. | |

**User's choice:** golang-migrate, run by Operator init container
**Notes:** Init container = same Operator image with `migrate up` entrypoint override. Failure exits non-zero and Pod stays `Init:Error` for visibility via `kubectl describe pod`. Later phases (Platform API/Forwarder/CS) connect to the schema but never run migrations.

---

## Credential Pepper Sourcing

| Option | Description | Selected |
|--------|-------------|----------|
| K8s Secret → env var (ACH_CREDENTIAL_HASH_PEPPER) | Simplest operational story. Single Secret ach-credential-hash-pepper mounted as env var into Operator + Platform API. Rotation requires pod restart but pepper rotation is a planned maintenance event anyway. Matches spec's "Kubernetes Secret or equivalent" language verbatim. | ✓ |
| K8s Secret → file mount (/etc/ach/pepper) | Secret projected as a file the binary reads at startup. Slightly better for static-analysis 'no secrets in env' policies. Still requires pod restart on rotation. | |
| KMS reference (AWS KMS / GCP KMS / HashiCorp Vault) | Pepper held by external KMS, fetched on startup with workload identity. Strongest posture but adds cloud-coupling and a new failure mode at startup. Out-of-scope for v1alpha1. | |

**User's choice:** K8s Secret → env var (ACH_CREDENTIAL_HASH_PEPPER)
**Notes:** Phase 1 ships the Secret manifest + env-var wiring + an `internal/credhash/` package with constant-time HMAC-SHA-256 compute/compare. No live hashing in Phase 1 (no pk_/ek_ until Phase 3) — Phase 1 only locks the contract.

---

## Claude's Discretion

Items Claude is taking on as defaults aligned with the sister project; user did not need to decide.

- **Logging:** `log/slog` (stdlib, Go 1.21+) for application logs; `sigs.k8s.io/controller-runtime/pkg/log/zap` for the manager.
- **Test infra:** Ginkgo + Gomega + envtest + testcontainers-go for Postgres integration.
- **Config plumbing:** `os.Getenv` + small validation helper; no viper. Phase 1 knobs: `ACH_CACHE_ROOT`, `ACH_NAMESPACE`, `ACH_DB_URL`, `ACH_CREDENTIAL_HASH_PEPPER`, `ACH_PLUGIN_MAX_SIZE_MIB`.
- **Linter:** `golangci-lint` mirroring sister's `.golangci.yml`.
- **Docker:** Multi-stage builds, five Dockerfiles (one per binary), `docker-compose.yml` for local dev.
- **Spike directory:** `verification/` reserved (sister convention).
- **Phase 1 stub strategy for non-Operator Hub binaries:** real long-running processes exposing `/healthz` only, logging "Phase X stub".

## Deferred Ideas

Nothing surfaced during discussion. Phase boundaries on the roadmap already capture every later-phase concern (LiteLLM client → Phase 2; pk_/ek_ flow → Phase 3; Forwarder/JWT → Phase 4; CS streaming + metrics → Phase 5; CLI → Phase 6; adapters + Helm + distribution → Phase 7). Pepper rotation tooling stays in the v1beta1 backlog per Hub spec §20.
