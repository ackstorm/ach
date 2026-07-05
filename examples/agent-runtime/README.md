# Agent runtime example — `AgentProfile` + `ACHAgent`

Two CRDs run an ACH agent as a Kubernetes workload:

- **`AgentProfile`** — reusable infra + defaults (image, model, engine knobs,
  limits, health port, persistence). One profile is shared by many agents.
  `spec.podTemplate` is an optional raw strategic-merge overlay over the
  rendered pod template (containers merge by name `agent`; the operator
  re-pins its selector label and config-hash annotation; everything else is
  the author's responsibility).
- **`ACHAgent`** — an agent instance. References a profile, supplies its ACH
  identity (`ek_`), the target Hub Environment, an optional persona prompt, and
  one or more inbound channels (webhook / cron / queue / a2a).

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

# 3. (agent-memory.yaml only) the Hindsight admin bearer, injected as
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
- Reserved `ACH_*` env vars cannot be set via `AgentProfile.spec.extraEnv`; the
  operator owns that namespace (the ek arrives via `identity.secretRef` as
  `ACH_TOKEN`).
