# AgentProfile + ACHAgent — rendering, placement, e2e

> Relocated from `CLAUDE.md`. **Read before touching `internal/agentrender/`,
> `achagent_*.go`, or the e2e stage-06 agent fixtures.** The wire contract itself
> (`workspace-v1`) is owned jointly with ach-agent — see the MANDATORY row in
> `CLAUDE.md`.

## Rendering (workspace-v1)

**`AgentProfile`** (reusable infra + `spec.achagent` defaults) **+ `ACHAgent`** (an agent
instance) collapse via `agentrender.Render2` into the single workspace-v1 wire document
(`schemaVersion: "workspace-v1"`, contract §11 — `ach-agent` repo,
`docs/schemas/ach-workspace-contract-v1.md` + `ach-workspace-config-v1.schema.json`).
The `ACHAgentReconciler` writes `config.json` to a ConfigMap (`achagent-<name>`) and
applies a single-replica, single-container **control StatefulSet**
(`ach-control-<name>`, container `agent` — Channels+Harness only) that mounts it at
`/etc/ach-runtime/config.json`; inbound channel-auth secrets ride in env
(`secretKeyRef`), never file-mounted (NOT an isolation boundary: same-uid reads
`/proc/<pid>/environ` either way); a salted config-hash annotation rolls the pod on
change; optional profile `spec.podTemplate` raw overlay strategic-merges over THIS
control pod's template only — pass-through, selector label + config-hash re-pinned
after merge. The harness **self-hydrates** against ACH at boot (no init container, no
CLI step); status derives from `/readyz`/`/healthz` probes on the fixed control port
8080 (`controlProbePort`) — `health`/`spec.achagent.health` is accepted and stored but
not read by anything that builds real k8s objects (compatibility-only CRD field, no
standalone/distributed placement left to drive it).

There is **no `standalone`/`distributed` placement knob any more** (contract §11 scope
reset, 0.1.0 — `ResolvePlacement`/`distributedContainers`/`distributedVolumes`/role ports
are gone, not merely deferred). The Harness, running IN the control pod, creates one
real, zero/one-replica **execution StatefulSet per non-migrable Workspace directly** at
runtime, from `spec.execution` (image/resources/ephemeralStorage/scheduling/
imagePullSecrets/nodeSelector/tolerations/terminationGracePeriodSeconds — contract §11
execution-role infra) — a consumed creator path in v0.1.0, not a stub; only the
declarative `Workspace` CR for hand-managing those objects yourself is deferred to
v0.1.1. OpenCode and its shell tool run in THIS execution pod, never the control pod —
there is currently no `podTemplate`/`networkPolicy`-equivalent overlay for it, so
`AgentProfile.spec.podTemplate`/`spec.networkPolicy` harden only the control pod's own
process and its own direct egress (the ACH API, redis, the memory backend — never
opencode's model/MCP/A2A traffic). Execution and control pods talk over the control
pod's own headless Service (`controlEndpoint`/`facadeEndpoint` in the wire config); D2
reuses the existing signed mini-harness bearer and HMAC facade authentication for that,
not a client-TLS/broker boundary (excluded from scope, not deferred).

BOTH control and execution pods pin uid/gid/fsGroup 10001 (the image uid): without
fsGroup a fresh root-owned cloud PVC (EBS) is unwritable (ach-agent finding
2026-09-15; kind's local-path dirs are 0777 and never show it). The profile's
`spec.achagent` block (image/ach/model/engine/limits/health/workspace/artifacts) holds
the agent-overridable defaults; an `ACHAgent` sets the same fields flat on its spec
(inline `AgentDefaults`) and resolution is a uniform per-field deep merge
(`agentrender.Resolve{Image,Model,Engine,Limits,Workspace,Artifacts}` + `ResolveAchBaseURL`
+ `ResolveAch`/`ResolveEnv`): a set agent field wins, an omitted one inherits the
profile's. `engine.forwardEnv`/`model.params`/`model.thinking` are atomic (present on
the agent ⇒ replace as a whole); `workspace.persistence`/`workspace.session`/`artifacts`
merge recursively per leaf field — an agent overriding just one leaf (e.g.
`workspace.session.idleTimeoutSeconds`) still inherits every untouched sibling from the
profile, never a wholesale sub-block replace. Everything else on the profile is
profile-only infrastructure an agent cannot override: `imagePullSecrets`, `resources`,
`extraEnv`, `nodeSelector`, `tolerations`, `persistence` (the operator's OWN ephemeral
control-pod volume — distinct from the Harness-level `workspace.persistence` policy
above), `networkPolicy`, `terminationGracePeriodSeconds`, `podTemplate`, `spec.execution`.
`ach.baseUrl` resolves `ACHAgent.spec.ach.baseUrl ?? AgentProfile.spec.achagent.ach.baseUrl
?? operator ACH_BASE_URL` (empty everywhere ⇒ Render2 blocks the agent).
`configVersion` is the RFC 8785 (JCS) canonical SHA-256 of the rendered object with its
own `configVersion` member removed (`agentrender.ComputeConfigVersion`).

Global runtime **Storage** (operator-level `runtime.storage.s3` Helm values →
`ACH_STORAGE_S3_*` operator env, `RuntimeStorageOptions`) is real backend
configuration, never a per-agent setting or a health/reachability probe: an empty
bucket only fails closed (`WorkloadApplied=False/StorageUnavailable`) for an agent
whose resolved `workspace`/`artifacts` policy actually requires storage
(`RuntimeRequiresStorage`); an all-disabled agent is unaffected. The optional
namespace-local `credentialsSecretName` Secret (`AWS_ACCESS_KEY_ID`/
`AWS_SECRET_ACCESS_KEY`, optional `AWS_SESSION_TOKEN`) is read-checked alongside the
agent's own channel secrets, never mounted into the execution bootstrap.

## Retired: ACHAgent-only Sandbox and Egress

The ACHAgent-only `sandboxed` placement (`SandboxTemplate`/`SandboxWarmPool`,
`AgentProfile.spec.sandbox`) and `ACHAgent.spec.egress` (harness credential-injection
proxy) are retired with standalone/distributed placement — no second adapter, no
silent compatibility preservation. A pre-existing ACHAgent that owned agent-sandbox
objects gets them pruned, once, right after its new control StatefulSet successfully
applies (`internal/controller/ach/achagent_legacy_cleanup.go`,
`pruneLegacySandbox`/`pruneLegacyDeployment` — pool before template, UID-precondition
delete, foreign/prior-UID/unowned objects left untouched, no Secret ever deleted). The
**optional external chart infrastructure** kubernetes-sigs/agent-sandbox installs
(`scripts/cluster.sh install_agent_sandbox`, `deploy/helm/ach/templates/
agent-sandbox-rbac.yaml`, `agentSandbox.enabled` values/SA/Role) is preserved
standalone — nothing in the workspace-v1 render/control path creates, watches, or
depends on it any more; only the legacy cleanup above still knows its GVKs
(`extensions.agents.x-k8s.io` `sandboxtemplates`/`sandboxwarmpools`, RBAC narrowed to
`get`+`delete`).

## Migrating a pre-workspace-v1 ACHAgent

Update the existing `ACHAgent`/`AgentProfile` object's manifest in place — preserve its
name/namespace/UID, never delete and recreate it (recreation defeats the UID-scoped legacy
cleanup below and loses the object's history). There is no compatibility adapter; a manifest
still on the old shape is rejected or blocked, not silently translated:

- **Identity**: top-level `identity` is gone. Move the ek_ `SecretRef` under nested
  `spec.ach.identity` (object-level CEL requires it). A stored object that still carries only
  the old top-level field renders no children at all — it blocks at
  `IdentityResolved=False/IdentityMissing` ("`spec.ach.identity` is required — this object
  predates admission validation and must be updated") before any key, StatefulSet or cleanup
  runs, with no panic and no child mutation.
- **Channel session → routing**: the old `channels[].session.type: none|auto|custom` enum is
  gone. The Harness now picks its own default Workspace/Session identity per channel; set
  `channels[].routing.{workspaceKey,sessionKey}` only to override one of those two `{{ }}`
  templates (`event.*`/`payload.*`) — there is no mandatory template to author, and an omitted
  key just keeps the adapter's default.
- **Handoff destination**: `channels[].handoff.destination` (the relative path inside the
  Workspace the handoff output replaces) is required, non-empty — it has no implicit default.
- **Engine/placement/egress**: `engine.type`/`pi`/`home`/`workDir`/`idleTtlSeconds`/
  `maxToolCalls`, every `placement` value, and `spec.egress` are gone with no replacement
  field; drop them. Tool-call bounding moves to `limits.maxSteps`.
- **Nothing is converted or deleted for you**: the old per-agent `achagent-<name>-sandbox-key`
  Secret and any harness session snapshots are left exactly as they are — migration touches
  manifests, never that data.
- **Legacy workload cleanup stays narrow and ordered**: once (and only once) the new control
  StatefulSet applies successfully, the reconciler prunes the agent's own legacy Deployment,
  then its owned `SandboxWarmPool`, then its owned `SandboxTemplate` (UID-precondition delete;
  see "Retired" above) — a failing control apply leaves all legacy children in place.
- **New fixed infrastructure to expect**: the control pod answers its probes on public port
  8080 and exposes a private control Service on port 8081; the execution pod's bootstrap
  ConfigMap `ach-execution-<uid>` carries only `bootstrapVersion`, the agent's
  name/namespace/UID and `controlEndpoint`/`facadeEndpoint` — never private model/env/
  credential values.
- **Prerequisites are operator-wide, not per-agent**: runtime storage (`runtime.storage.s3`)
  is configured once on the operator, and both the control and execution images must be real,
  version-tagged references — the committed `REPLACE_WITH_PUBLISHED_*` markers (see "Code + e2e
  evidence" below) cannot satisfy this and fail workload readiness closed.

## Code + e2e evidence

- **Rendering**: `internal/agentrender/render2.go` (`Render2`, `RenderInfrastructureV1`)
  and `workspacev1.go` (`RenderAchV1`/`RenderEngineV1`/`RenderLimitsV1`/
  `RenderWorkspaceV1`/`RenderArtifactsV1`/channel mappers), schema-locked against the
  vendored `testdata/ach-workspace-config-v1.schema.json`
  (`TestWorkspaceV1Schema_NoDrift`) and exercised end-to-end through real committed YAML
  (typed decode → merge → Render2 → schema/configVersion) by
  `internal/agentrender/examples_test.go`.
- **E2E stage 06** ships TWO shapes — `e2e-profile`/`e2e-agent` (ephemeral) and
  `e2e-profile-pvc`/`e2e-agent-pvc` (the operator's own control-pod PVC) — NOT three:
  the old `e2e-agent-dist` distributed-placement fixture and the sandboxed
  `e2e-agent-sbx`/`profile-sbx.yaml` fixture were both retired with their respective
  placements, not renamed or merged. Both control/execution images now reference the
  `ghcr.io/ackstorm/ach-runtime-control:0.1.0`/`ghcr.io/ackstorm/ach-runtime-execution:0.1.0`
  0.1.0 candidate references (not yet published releases); Root kind-loads them into the
  cluster before stage 06 applies, so stage 06 as shipped reaches
  `WorkloadApplied=True`. Two evidence tracks: `scripts/cluster.sh verify_all` gates the RENDERING
  shape (`WorkloadApplied=True`, a schema-valid
  `config.json`, the `ACH_SECRET_IDENTITY` secretKeyRef, the control StatefulSet's
  single `agent` container + fsGroup 10001, the Harness's own creator RoleBinding
  `ach-harness-<uid>`); `test/e2e/agent_runtime_ready_test.go` mints a real `ek_`,
  swaps it into `e2e-agent-ek`, and (same 0.1.0-candidate image prerequisite, checked per profile by
  `classifyRuntimeImages`/`phase6RequireRealRuntimeImages` — a kubectl/read failure
  fails the test, never a silent skip) requires `WorkloadReady=True` on both agents
  plus a PVC write as uid 10001 on the persistent one. There is no Workspace
  lifecycle/distributed-isolation test — that evidence does not exist yet.
- `install_agent_sandbox` (`scripts/cluster.sh`) still installs the optional
  kubernetes-sigs/agent-sandbox cluster prerequisite before `reconcile_ach`, but no
  stage-06 fixture exercises it any more — the chart surface is preserved
  independently of workspace-v1 runtime readiness (`helm-render-check.sh` topology 6).

## Object names (contract §11)

One source of truth each, in `internal/agentrender/render2.go`:

| Object | Name | Helper |
|--------|------|--------|
| control StatefulSet + headless Service (pod `<name>-0`) | `ach-control-<agent name>` | `ControlName` |
| control SA (default), per-agent Role + RoleBinding | `ach-harness-<uid>` | `HarnessName` |
| execution SA + bootstrap ConfigMap | `ach-execution-<uid>` | `ExecutionServiceAccountName` |
| workspace StatefulSet/Service/pod | `ach-ws-…` | runtime-owned — the operator never computes or validates it |

`ControlName`: the name as is when ≤ 40 chars; otherwise (or when it contains `.`, legal
in `metadata.name` but not in a Service name) its first 31 chars (dots → `-`, trailing `-`
dropped) + `-` + first 8 hex of `sha256(full name)`. 12 + 40 = 52 keeps the pod name
(`-0`) and the controller-revision-hash label value (`-` + 10 chars) within 63. The UID
stays in labels/ownership, not in the name. `controlEndpoint`/`facadeEndpoint` are
`http://<ControlName>.<ns>.svc:8081[/facades]`.

The rendered `agent` block always carries `name` (= `metadata.name`), `namespace`, `uid`:
the runtime names workspace pods `ach-ws-<name part>-<ref>` and requires `agent.name`.

## Harness RBAC (`ach-harness-<uid>` Role)

Exactly what the runtime's Kubernetes client (`ach-runtime` `harness/kubernetes.py`)
issues — GET, LIST (by `runtime.ach.ackstorm.ai/agent-uid`), POST, merge-PATCH, DELETE
with a UID precondition: `apps/statefulsets` get/list/create/patch/delete (replicas 0↔1,
recreate on template drift), `services` get/create, `pods` get/list/delete. No `watch`,
no `update`, no `statefulsets/scale` (the Harness patches `spec.replicas` on the
StatefulSet itself). RBAC cannot express an `ach-ws-*` name prefix, so the Role covers the
namespace; the Harness scopes every call to its own UID-labelled/owned objects. When the
runtime client grows a verb, add it here AND to the operator ClusterRole (escalation
prevention: the operator must hold whatever it grants).

## Stable control ServiceAccount (`AgentProfile.spec.controlServiceAccountName`)

Optional DNS-1123 label (max 63). Set: control StatefulSet (and the podTemplate re-pin), the
per-agent RoleBinding subject, and rendered `infrastructure.control.serviceAccount` all use that
pre-existing SA in the agent's namespace; the operator does not create/own/delete it and skips
`ach-harness-<uid>`. Role/RoleBinding names stay `ach-harness-<uid>`. Unset: unchanged output
(same `configVersion`). Switching a profile back and forth leaves orphaned `ach-harness-<uid>`
SAs (owner-ref GC only on ACHAgent delete); the operator never deletes them. A named SA that does not
exist sets `ControlServiceAccountResolved=False` (reason `ServiceAccountNotFound`) and
applies nothing for that agent; a ServiceAccount watch re-enqueues it once the SA appears.
Unset: `ControlServiceAccountResolved=True` (`PerAgentServiceAccount`). Execution SA/RBAC
unchanged. Helper: `agentrender.ControlServiceAccountName`.
