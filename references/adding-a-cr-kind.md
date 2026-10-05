# Adding (or auditing) a CR kind — the kind-lifecycle contract

> Read this **before** adding a new `*.ach.ackstorm.ai` CRD kind, and when
> auditing an existing one for parity. It is the checklist that keeps every kind
> a first-class citizen instead of a half-wired one.

## The load-bearing principle

**Every `*.ach.ackstorm.ai` CR kind is a first-class citizen.** A new kind MUST
satisfy every *applicable* row of the surface matrix below — in the **same PR**
that introduces it. No second-class kinds: we do not merge a kind that reconciles
but has no projection, or that serves content but has no metrics, or that ships
without docs/tests. If a surface genuinely does not apply to the kind's
archetype, say so explicitly (it is a "—" in the matrix, not an omission to
discover later).

"Applicable" is decided by **archetype**, not by convenience. Pick the archetype
first, then walk the matrix.

## Archetypes

| Archetype | Kinds | One-line shape |
|-----------|-------|----------------|
| **object** | `Plugin`, `Skill`, `Artifact`, `Prompt` | Fetched, validated, cached, served, hydrated. Path **narrows at fetch** (F1). |
| **discovery** | `PluginMarketplace`, `SkillMarketplace` | Fetches the **whole repo**, tree-walks it, slices each discovered item into its own tarball + inventory entry. Opts OUT of fetch-narrowing (`withoutGitPath`). |
| **governance** | `Environment`, `BackendIdentityPolicy` | No content of its own; resolves/gates references and drives downstream state (LiteLLM access-groups, forwarder RBAC/JWT mint). |
| **config singleton** | `LiteLLMConnection` | Cluster-level configuration the operator reads; not served, not hydrated, not inventoried. |
| **runtime workload** | `Workspace` | Operational execution request consumed by the Harness and reconciled by the operator; it has no projection, admin inventory, content, hydrate, or finalizer lifecycle. |

## The 11 surfaces

1. **CRD + CEL** — `api/v1alpha1/<kind>_types.go` with kubebuilder validation
   markers and CEL `x-kubernetes-validations` for cross-field invariants. Run
   `make gen-code` (deepcopy + CRD bases) and `make gen-crd-ref-docs`.
2. **Projection table + `with_tx_notify`** — a Postgres projection table written
   **only by the operator**, emitting `NOTIFY ach_<kind>_changed` from the same
   transaction (`with_tx_notify`). Reader services (platform-api, forwarder,
   content-service) LISTEN + periodic-refresh; they never read the CRD.
3. **Status conditions** — kind-appropriate conditions via
   `meta.SetStatusCondition`. On **duplicate identity** emit
   `Synced=False reason=NameConflict` (G15). If the kind is UI-writable, carry
   `origin` / `locked` so a CRD write never silently clobbers a UI row.
4. **Reconciler + finalizer** — `internal/controller/ach/<kind>_controller.go`
   following `Reconcile(ctx, req) (Result, error)`; side-effecting I/O lives in a
   dedicated `internal/` package (the reconciler owns the k8s state machine, the
   service package owns the I/O). A **finalizer** cleans up the projection row and
   fires the delete-side `NOTIFY`.
5. **Content pipeline (F1)** — fetch → Stage-2 validation gate → cache tarball →
   content-service `/content/<kind>/{name}` serve. **objects** narrow at fetch
   (`spec.<git>.path`: dir → subtree, file → raw bytes); **discovery** kinds fetch
   the whole repo (`withoutGitPath`) and slice per discovered item. Symlink /
   traversal / missing path → `UpstreamInvalid`.
6. **Admin inventory** — the kind appears in the platform-api admin object
   inventory (read path). Discovery items fold in as `<item>@<marketplace>`.
7. **Admin refresh** — an admin-triggered refresh path that re-fetches / re-checks
   the source on demand (not only on the periodic timer).
8. **Hydrate** — `ach-cli env hydrate` projects the kind into the workspace via the
   adapter route rules. `Environment` is the hydrate *root*; objects ride the
   per-component projection.
9. **Metrics** — Prometheus counters/gauges for reconcile outcome and (if served)
   content serve, named consistently with the existing per-kind metrics.
10. **Docs / spec** — `CLAUDE.md` (architecture + tables), the relevant
    `references/` / `docs/` narrative, and the frozen spec — updated in the SAME
    commit (documentation-hygiene rule).
11. **Tests** — unit (pure logic) + envtest (reconciler state machine) + e2e
    fixtures under `test/e2e/cluster/` where the kind participates in the suite.

## Archetype applicability matrix

✓ = required · — = N/A for this archetype · ◑ = conditional (see footnotes)

| # | Surface | object | discovery | governance | config singleton | runtime workload |
|---|---------|:------:|:---------:|:----------:|:----------------:|:---------------:|
| 1 | CRD + CEL | ✓ | ✓ | ✓ | ✓ | ✓ |
| 2 | Projection table + `with_tx_notify` | ✓ | ✓ | ✓ | ◑¹ | — |
| 3 | Status conditions (+`NameConflict` G15; `origin`/`locked` if UI-writable) | ✓ | ✓ | ✓ | ✓ | ✓ |
| 4 | Reconciler + finalizer | ✓ | ✓ | ✓ | ✓ | ◑⁶ |
| 5 | Content pipeline (F1) | ✓ (narrow-at-fetch) | ◑² (whole-repo → slice) | — | — | — |
| 6 | Admin inventory | ✓ | ✓ (`<item>@<mkt>`) | ✓ | — | — |
| 7 | Admin refresh | ✓ | ✓ | ◑³ | — | — |
| 8 | Hydrate | ✓ | ◑⁴ | ◑⁵ | — | — |
| 9 | Metrics | ✓ | ✓ | ✓ | ✓ | ✓ |
| 10 | Docs / spec | ✓ | ✓ | ✓ | ✓ | ✓ |
| 11 | Tests | ✓ | ✓ | ✓ | ✓ | ✓ |

**Footnotes**

1. The config singleton is read by the operator; it does not require a served
   projection unless a reader service needs it. If it stays operator-internal, a
   projection + `NOTIFY` is not mandated — but document the decision.
2. Discovery kinds do not serve themselves; they produce per-item content
   (`skill-marketplace/<mkt>/<name>.tar.gz`) by tree-walking the whole repo. They
   opt OUT of fetch-narrowing via `withoutGitPath` and use `spec.<git>.path` only
   as the post-fetch tree-walk root.
3. Governance kinds re-resolve their references each reconcile; an explicit admin
   refresh applies only if the kind caches an external lookup that an operator
   wants to force-refresh.
4. The marketplace itself is not hydrated; its **discovered items** are, via their
   object archetype.
5. `Environment` is the hydrate root (`ach-cli env hydrate`);
   `BackendIdentityPolicy` is not hydrated.
6. `Workspace` has status and an operator reconciler, but no finalizer: deleting
   a Workspace request retains its execution workload and storage. The operator
   consumes it directly and the Harness observes resulting children; it is not a
   projected product object or user hydrate input.

## `Workspace` runtime-workload archetype

`Workspace` is an operational request created by the ach-agent Harness. Its
CRD/CEL, status, controller-runtime metrics, reconciler, unit tests, envtests, and
documentation are required. Postgres projection/NOTIFY, admin inventory/refresh,
content delivery, CLI hydrate, and external-cleanup finalizers do not apply: the
operator and Harness consume the object directly. The operator keeps ACHAgent
ownership of the execution StatefulSet and Service; the Workspace CR is not their
garbage-collection owner.

The Workspace generation/status reports the evaluated request generation. A
request carries observed StatefulSet UID and, when closing a live Pod, Pod UID
preconditions so delayed operations cannot mutate replacement children. True idle
means replicas=0 and no Pod of any phase. Only true idle permits execution-template
updates; while a Pod exists or the StatefulSet has one replica, profile drift is
reported as pending and does not rewrite the live template. Scale-to-zero changes
only replicas after identity and precondition checks. Matching-shape adoption
preserves the full live template and both child UIDs while reporting `UpdatePending`;
it is not actual runtime-origin migration evidence.

The operator stamps a private Workspace UID marker on a StatefulSet only when
creating it. Matching marker/full identity may complete no-mutation status
observation after an interrupted write or generation advance; it does not
authorize scale/template writes or legacy adoption. Omitting UID preconditions
does not authorize mutation of an existing workload: creation requires no existing
StatefulSet or Pod. Workspace deletion retains workload and storage; ACHAgent
deletion keeps its existing owner-reference garbage-collection behavior.

Harness RBAC grants Workspace create/delete/get/list/patch/update/watch,
StatefulSet and Service get/list/watch, and Pod delete/get/list/watch. The operator
retains corresponding permissions to reconcile and delegate these requests. No
`statefulsets/scale` grant is delegated. Legacy UID-derived workload names are
conflicts, not automatically translated or adopted. Runtime-backend migration,
native-S3 behavior, and canonical real-cluster acceptance remain separate gates;
unit/envtest and synthetic adoption are not substitutes.

## Retroactive audit note

`Skill` / `SkillMarketplace` were brought to full parity with `Plugin` /
`PluginMarketplace` (G8). When touching content/discovery kinds, re-walk the
matrix for **both** the kind you are changing and its parity twin, and for
`LiteLLMConnection` (config singleton) — they are the kinds most likely to drift
out of first-class status.
