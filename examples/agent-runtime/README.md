# Agent runtime example — `AgentProfile` + `ACHAgent`

Two CRDs run an ACH agent as a Kubernetes workload:

- **`AgentProfile`** — reusable infra (resources, persistence, networkPolicy,
  podTemplate) plus agent-overridable defaults under `spec.achagent` (image,
  ach, model, engine knobs, limits, health port, cost source). One profile is shared by many
  agents; an `ACHAgent` may override any `spec.achagent` field flat on its own
  spec (per-field deep merge — a set agent field wins, an omitted one inherits
  the profile's). `spec.podTemplate` is an optional raw strategic-merge overlay
  over the rendered pod template (containers merge by name `agent`; the
  operator re-pins its selector label and config-hash annotation; everything
  else is the author's responsibility).
- **`ACHAgent`** — an agent instance. References a profile, supplies its ACH
  identity (`ek_`), the target Hub Environment, an optional persona prompt, and
  one or more inbound channels (webhook / webhook-script / cron / queue / a2a).

Both resources accept Pod-native `spec.env`. Entries merge by name and the
`ACHAgent` entry wins atomically. `engine.forwardEnv` sends selected names to the
agent engine; `channels[].prepare.forwardEnv` independently sends selected names
only to that channel's prepare script, while `channels[].cleanup.forwardEnv` does
the same independently for cleanup. Unknown names are ignored and remain unset.

`prepare` and `cleanup` are generic configuration-owned shell hooks. The
operator and harness do not clone, cache, lock, or delete repositories. Prepare
runs on the lane before the session engine is acquired or reused for each invocation
and is fail-closed. Cleanup runs best-effort when the reserved session is torn down:
after an acquired engine stops, or after prepare/engine-acquire failure before
acquisition completes. Graceful shutdown attempts cleanup; abrupt Pod/node termination
cannot guarantee it. Prepare and engine cwd are `ACH_WORKSPACE`; cleanup cwd is its
parent. Workspace growth is bounded only when configured cleanup succeeds; absent or
failed cleanup can leave workspaces behind. If you use a shared bare mirror and
per-session worktrees, implement both the Git operations and cross-session locking
inside these scripts.

The operator collapses the two into a single `agent-config-v1` config, writes it
to a ConfigMap (`config.json`), and applies a single-replica Deployment that
mounts it at `/etc/ach-agent/config.json`. The `ach-agent` harness
**self-hydrates** against ACH at boot — there is no init container and no CLI
step.

## Prerequisites — Secrets you create yourself

The operator never mints credentials; it only references Secrets in the same
namespace.

```bash
# 1. The ACH ek_ the agent authenticates with (injected as ACH_TOKEN).
kubectl -n engineering create secret generic ops-ek \
  --from-literal=ek=<ek_...>

# 2. Per-channel secrets referenced by webhook/a2a auth (this example's webhook).
kubectl -n engineering create secret generic gitlab-webhook \
  --from-literal=secret=<gitlab-webhook-token>

# 3. Read-only token used by the GitLab channel's prepare clone.
kubectl -n engineering create secret generic gitlab-clone \
  --from-literal=token=<gitlab-read-token>

# 4. (agent-memory.yaml only) the Hindsight admin bearer, injected as
#    ACH_SECRET_MEMORY_HINDSIGHT — NOT the ek_.
kubectl -n engineering create secret generic hindsight-admin \
  --from-literal=token=<hindsight-admin-bearer>
```

## Apply

```bash
kubectl apply -f profile.yaml
kubectl apply -f agent.yaml
# Optional: an agent with the Hindsight memory backend (auth + mission +
# mentalModels the harness provisions at boot). Shares the `standard` profile.
kubectl apply -f agent-memory.yaml
# Optional: a GitLab MR reviewer wired to Hindsight over an internal (no-auth)
# URL — webhook channel + four mentalModels. No hindsight-admin secret needed.
kubectl apply -f agent-gitlab-hindsight.yaml
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
kubectl -n engineering logs deploy/achagent-gitlab-reviewer
```

## Notes

- **`prompt.system.type: file`** requires the file to be present under the
  agent's `.ach-state` (delivered by hydration) or baked into the image — the
  operator does not deliver prompt files. `type: text` (inline) and `type: ach`
  (a hydrated prompt by name) need no extra delivery.
- The webhook/a2a **Service is ClusterIP only**. Front it with the platform
  Ingress/gateway — the operator creates no Ingress.
- Reserved `ACH_*` env vars cannot be set via either resource's `spec.env`; the
  operator owns that namespace (the ek arrives via `identity.secretRef` as
  `ACH_TOKEN`).

## Hardening the agent pod

The agent container runs opencode, which has a shell tool. Two knobs bound what that shell can
reach. Both live on the `AgentProfile` (reusable infra), not on the `ACHAgent`.

### Sandboxed runtime (`runtimeClassName`)

No dedicated field — `spec.podTemplate` is a raw strategic-merge overlay, so set it directly:

```yaml
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
```

The RuntimeClass must already exist in the cluster. An unknown name means the pod will not run;
this surfaces as `WorkloadReady=False` on the ACHAgent.

### Egress allowlist (`networkPolicy`)

The harness fronts every model and MCP call through a localhost proxy that injects the `ek_`, but
that proxy is *cooperative* — opencode's shell tool can reach anything the pod's network reaches.
`spec.networkPolicy` makes the boundary enforced instead of assumed.

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
  `ACH_BASE_URL` — every model and MCP call fails — while the pod stays `Ready` (kubelet probes
  don't check egress), so `{}` can look healthy while doing nothing. It's trivially recoverable
  though: dropping the block prunes the policy immediately, no pod restart needed.
- The operator always prepends a DNS rule (UDP+TCP port 53, any destination). Without it a
  default-deny policy breaks name resolution, and the failure looks like a DNS bug rather than a
  policy denial. Consequence: DNS-tunnel exfiltration is not covered by this policy.
- **Egress only.** `policyTypes` never includes `Ingress`, so `expose.service` and gateway→agent
  routing are unaffected.
- Rules are **declared, not derived**. NetworkPolicy has no FQDN peer type and `ach.baseUrl` is a
  URL, so the operator cannot compute the ACH peer for you. Use a `podSelector` +
  `namespaceSelector` for in-cluster ACH, or an `ipBlock` CIDR for an external endpoint.
- **Declare every peer the harness dials directly, not just ACH.** The localhost proxy covers
  model/MCP/A2A egress via `ACH_BASE_URL`, but the harness also dials some endpoints straight
  from the pod:
  - **redis**, if any `channels[].type: queue`, or if the stats sink (`ACH_STATS_REDIS_URL`) is
    configured
  - the **memory backend**, if `memory.hindsight.endpoint` is set
  Miss one and it won't error — memory and stats are fail-open by design, so the agent just
  degrades silently (no session recall, no metrics) instead of failing loudly.
  A2A peers need no rule of their own: they arrive from hydration and are dialled through
  `ACH_BASE_URL` like model and MCP traffic.
- **`networkPolicy` lives on the shared `AgentProfile`, but `memory`/`channels` live on the
  `ACHAgent`.** A profile shared by several agents needs the *union* of all their peers, which
  over-grants egress to agents that don't need every peer.
- **Requires a CNI that enforces NetworkPolicy** (Calico, Cilium, …). On a CNI that ignores it, the
  object exists and enforces nothing — verify in your cluster before relying on it.
- Editing the block does **not** roll the pod (the policy is not a pod-template input); the new
  rules take effect as soon as the CNI picks the object up.
