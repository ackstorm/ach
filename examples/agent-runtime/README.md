# Agent runtime example — `AgentProfile` + `ACHAgent`

Two CRDs run an ACH agent as a Kubernetes workload:

- **`AgentProfile`** — reusable infra (resources, persistence — the operator's own
  ephemeral control-pod volume, distinct from the Harness-level `workspace.persistence`
  backend below —, networkPolicy, podTemplate) plus agent-overridable defaults under
  `spec.achagent` (image, `ach` baseUrl/environment/capability defaults, model, engine
  knobs, limits, health, workspace, artifacts) and the required `spec.execution` block
  (contract §11 execution-role infra — image/resources/ephemeralStorage/scheduling for the
  per-Workspace execution pod the Harness creates). One profile is shared by many agents;
  an `ACHAgent` may override any `spec.achagent` field inline on its own spec (per-field
  deep merge — a set agent field wins, an omitted one inherits the profile's; nested blocks
  like `workspace`/`artifacts` merge recursively per leaf field, never as a wholesale
  replace). `spec.podTemplate` is an optional raw strategic-merge overlay over the rendered
  pod template (the control container is named `agent`; the operator re-pins its selector
  label and config-hash annotation; everything else is the author's responsibility).
- **`ACHAgent`** — an agent instance. References a profile, supplies its own
  `ach.identity` (`ek_`, never a profile default), the target Hub Environment
  (`ach.environment`, documentation-only), an optional persona prompt, and one or more
  inbound channels (webhook / webhook-script / cron / queue / a2a).

Both resources accept Pod-native `spec.env`. Entries merge by name and the
`ACHAgent` entry wins atomically. `engine.forwardEnv` sends selected names to the
agent engine; `channels[].handoff.forwardEnv` independently sends selected names
only to that channel's handoff script. A `forwardEnv` name absent from the merged
`spec.env` is an authoring error — the render fails (`engine.forwardEnv: "<name>" is not
set in spec.env`), it does not silently skip the name and leave it unset.

`channels[].handoff` is a generic configuration-owned shell hook. The operator
and harness do not clone, cache, lock, or delete repositories on its behalf. It
runs in the harness, in an empty `$ACH_HANDOFF_DIR`; its output wholesale-replaces
the session workspace's `handoff/` directory (content-agnostic — a repo clone, a
DB extract, arbitrary files). `scope: event` (the default) runs it every
invocation — the handoff always starts from an empty directory, so a script that
wants an incremental checkout must clone from scratch each time; `scope: session`
runs it only when a new session is created. `spec.hooks.sessionStart` /
`spec.hooks.sessionSuspend` are agent-level hooks that run inside the mini-harness
(only `engine.forwardEnv` variables, never channel credentials):
`sessionStart` runs once per new session, after the handoff and before the first
turn (fail-closed); `sessionSuspend` runs every time the session's engine stops
(idle, shutdown, sandbox suspend), before any HOME archive (best-effort, may run
many times).

The operator collapses the two into the workspace-v1 wire config (`schemaVersion:
"workspace-v1"`), writes it to a ConfigMap (`achagent-<name>`, key `config.json`), and
applies a single-replica control StatefulSet (`ach-control-<name>`, one `agent` container —
Channels+Harness) that mounts it at `/etc/ach-runtime/config.json`. The harness
**self-hydrates** against ACH at boot — there is no init container and no CLI step. It then
creates one standard zero/one-replica execution StatefulSet per non-migrable Workspace
directly, using `spec.execution` (image/resources/ephemeralStorage/scheduling) — a real,
consumed creator path in v0.1.0; only the declarative Workspace CR for hand-managing those
objects yourself is deferred to v0.1.1. There is no `standalone`/`distributed` placement
knob any more: one control container per agent.

The control pod runs as uid/gid/fsGroup 10001 (the image uid), so a fresh PVC — root-owned
0755 on cloud provisioners such as EBS — is writable without a `podTemplate` overlay.

## Prerequisites — Secrets you create yourself

The operator never mints credentials; it only references Secrets in the same
namespace.

```bash
# 1. The ACH ek_ the agent authenticates with (injected as ACH_SECRET_IDENTITY).
kubectl -n engineering create secret generic ops-ek \
  --from-literal="ek=<YOUR_EK>"

# 2. Per-channel secrets referenced by webhook/a2a auth (this example's webhook).
kubectl -n engineering create secret generic gitlab-webhook \
  --from-literal="secret=<YOUR_GITLAB_WEBHOOK_SECRET>"

# 3. Read-only token used by the GitLab channel's handoff clone.
kubectl -n engineering create secret generic gitlab-clone \
  --from-literal="token=<YOUR_GITLAB_READ_TOKEN>"

# 4. NOT needed for agent-memory.yaml as shipped: it uses memory auth type=ach,
#    where the harness sends its own ek_ and ACH resolves the principal. Only the
#    `type: bearer` arm (talking to ach-memory directly) needs a token, which the
#    operator injects as ACH_SECRET_MEMORY_AUTH — NOT the ek_. WHAT that token is
#    depends on `auth.header`: a JWT for the default `Authorization`, or whatever
#    the platform provider's resolver names for e.g. `x-litellm-api-key`.
# kubectl -n engineering create secret generic ach-memory-key \
#   --from-literal="token=<YOUR_ACH_MEMORY_TOKEN>"
```

## Apply

`profile.yaml`'s `achagent.image`/`execution.image` ship as conspicuous
`REPLACE_WITH_PUBLISHED_CONTROL_IMAGE` / `REPLACE_WITH_PUBLISHED_EXECUTION_IMAGE`
markers — no real control/execution image is published yet. Substitute real
version-tagged references **before** applying this profile: the execution marker
carries no tag, which the operator's wire schema rejects outright, so the ACHAgent
never reaches `WorkloadApplied=True` as shipped (`RenderFailed`) — this is a render/
schema failure, not merely a later image-pull failure once a pod exists.

```bash
kubectl apply -f profile.yaml
kubectl apply -f agent.yaml
# Optional: an agent with the ach-memory backend, reached through ACH with the
# agent's own ek_ (auth type: ach). Shares the `standard` profile.
kubectl apply -f agent-memory.yaml
```

## Status

```bash
kubectl -n engineering get achagent
kubectl -n engineering describe achagent gitlab-reviewer
```

`Ready=True` rolls up five conditions: `ProfileResolved`, `IdentityResolved`,
`ChannelSecretsResolved`, `WorkloadApplied`, `WorkloadReady`. Because the agent
self-hydrates, **`Ready=False` with `WorkloadReady=PodNotReady` usually means
hydration failed** — check the pod logs and the `/readyz` probe:

```bash
kubectl -n engineering logs -l ach.ackstorm.ai/agent=gitlab-reviewer -c agent --tail=100
```

## Notes

- **`prompt.system.type: file`** requires the file to be present under the
  agent's `.ach-state` (delivered by hydration) or baked into the image — the
  operator does not deliver prompt files. `type: text` (inline) and `type: ach`
  (a hydrated prompt by name) need no extra delivery.
- The webhook/a2a **Service is ClusterIP only**. Front it with the platform
  Ingress/gateway — the operator creates no Ingress.
- Reserved `ACH_*` env vars cannot be set via either resource's `spec.env`; the
  operator owns that namespace (the ek arrives via `ach.identity.secretRef` as
  `ACH_SECRET_IDENTITY`).

## Hardening the control pod

`spec.podTemplate`/`spec.networkPolicy` (both on the `AgentProfile`) only ever bound the
**control** StatefulSet (`ach-control-<name>`, one `agent` container — Channels+Harness).
That container holds the agent's private credentials (`ACH_SECRET_IDENTITY`, channel
secrets) but does **not** run opencode or any shell tool. opencode — and the shell tool —
run in the separate per-Workspace **execution** pod the Harness creates directly from
`spec.execution` (image/resources/ephemeralStorage/scheduling/tolerations/nodeSelector).
There is currently no `podTemplate`/`networkPolicy`-equivalent overlay for that execution
pod: the two knobs below harden the control pod's own process and its own egress (API
calls to ACH, Postgres/Redis if dialled directly), not opencode's shell.

### Sandboxed control pod (`runtimeClassName`)

No dedicated field — `spec.podTemplate` is a raw strategic-merge overlay, so set it directly.
This puts the **control** pod (the Channels+Harness process holding `ACH_SECRET_IDENTITY` and
channel secrets) behind a gVisor/Kata boundary, not opencode:

```yaml
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
```

The RuntimeClass must already exist in the cluster. An unknown name means the pod will not run;
this surfaces as `WorkloadReady=False` on the ACHAgent.

### Control-pod egress allowlist (`networkPolicy`)

This bounds only what the **control** pod itself dials directly (the Harness process, not
opencode): the ACH API, any approved model/MCP facade it executes upstream requests for with
its own private credentials, and — if the agent uses them — redis and the memory backend
below. opencode's own direct model/MCP/A2A traffic still runs in the separate execution pod,
which has no `networkPolicy`-equivalent knob today; this allowlist does not reach that direct
traffic either way.

```yaml
spec:
  networkPolicy:
    egress:
      - to:
          - namespaceSelector:
              matchLabels:
                kubernetes.io/metadata.name: ach-system
            podSelector:
              matchLabels:
                app.kubernetes.io/name: ach
        ports:
          - protocol: TCP
            port: 8080
```

- **Omitted** → no policy, unrestricted egress (the default, unchanged from before this feature).
- **`networkPolicy: {}`** → deny-all egress except DNS. On an enforcing CNI this also cuts off
  `ACH_BASE_URL` and any configured facade peer — hydration and every control-pod-routed
  model/MCP call fail — while opencode's own direct model/MCP/A2A traffic in the execution
  pod, outside this policy's reach, is unaffected, and the pod stays `Ready` either way
  (kubelet probes don't check egress), so `{}` can look healthy while doing nothing. It's
  trivially recoverable though: dropping the block prunes the policy immediately, no pod
  restart needed.
- The operator always prepends a DNS rule (UDP+TCP port 53, any destination). Without it a
  default-deny policy breaks name resolution, and the failure looks like a DNS bug rather than a
  policy denial. Consequence: DNS-tunnel exfiltration is not covered by this policy.
- **Egress only.** `policyTypes` never includes `Ingress`, so `expose.service` and gateway→agent
  routing are unaffected.
- Rules are **declared, not derived**. NetworkPolicy has no FQDN peer type and `ach.baseUrl` is a
  URL, so the operator cannot compute the ACH peer for you. Use a `podSelector` +
  `namespaceSelector` for in-cluster ACH, or an `ipBlock` CIDR for an external endpoint.
- **Declare every peer the control pod dials directly, not just ACH.** Beyond the ACH API
  itself, the Harness also dials some endpoints straight from the control pod:
  - **redis**, if any `channels[].type: queue`, or if the stats sink (`ACH_STATS_REDIS_URL`) is
    configured
  - the **memory backend**, if `memory.achMemory.endpoint` is set
  - any **approved model/MCP facade** endpoint the Harness is configured to dial upstream
    itself, with its own private credentials — declare its actual configured peer, not an
    assumed one
  Miss the redis or memory peer and it won't error — both are fail-open by design, so the
  agent just degrades silently (no session recall, no metrics) instead of failing loudly.
  Execution's own direct model/MCP/A2A traffic — opencode's own calls, not routed through an
  approved facade — is dialled from the execution pod, outside this policy's reach either way.
- **`networkPolicy` lives on the shared `AgentProfile`, but `memory`/`channels` live on the
  `ACHAgent`.** A profile shared by several agents needs the *union* of all their peers, which
  over-grants egress to agents that don't need every peer.
- **Requires a CNI that enforces NetworkPolicy** (Calico, Cilium, …). On a CNI that ignores it, the
  object exists and enforces nothing — verify in your cluster before relying on it.
- Editing the block does **not** roll the pod (the policy is not a pod-template input); the new
  rules take effect as soon as the CNI picks the object up.
