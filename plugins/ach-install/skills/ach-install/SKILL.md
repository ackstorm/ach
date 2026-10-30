---
name: ach-install
description: "Install, configure, upgrade and troubleshoot an ACH (Agent Capability Hub) deployment on Kubernetes with its Helm chart (oci://ghcr.io/ackstorm/charts/ach, image ghcr.io/ackstorm/ach). Use whenever the task is the ACH deployment itself: writing or reviewing ACH Helm values or a Flux/Argo HelmRelease for it, wiring ACH to Postgres, Valkey/Redis, Dex OIDC or LiteLLM, creating its Secrets (DEK, pepper, LiteLLM master key, Dex client, JWT signing keys), ingress/TLS and the gateway, OAuth redirect hosts, login team enrolment, user budgets and key limits, the OpenCode genai.* values, forwarder credential headers or trusted IdP, Enterprise observability and classifier, migrations, upgrading the chart, or an ACH pod that will not start or a fresh install that does not work. Not for day-to-day ach-cli use or for writing Environment/Plugin/Skill resources."
---

# Installing and operating ACH with Helm

ACH (Agent Capability Hub) is a Kubernetes control plane for agent
configuration: models, MCP servers, A2A agents, plugins, skills and prompts,
handed to users and agents through Environments. It sits in front of a LiteLLM
proxy and signs people in through Dex. This skill covers putting it on a
cluster and keeping it there.

Placeholders used throughout: `ach.example.com` (the public ACH origin),
`auth.example.com` (Dex), namespace `ach`, release name `ach`. Replace them
with the deployment's own; never copy a real organisation's hostnames.

## What gets deployed

One image, `ghcr.io/ackstorm/ach`, runs every mode; each Deployment picks its
mode with `args`. The chart (`oci://ghcr.io/ackstorm/charts/ach`) renders:

```
             Ingress / HTTPRoute (TLS)  https://ach.example.com
                          │
                   ach-gateway  (optional, logic-free reverse proxy)
       ┌──────────────────┼──────────────────────────┐
  /platform, /       /content                /v1 /gemini /mcp /a2a
  /.well-known/opencode  │                   /.well-known /v2/model/info
       ▼                 ▼                          ▼
  ach-platform-api   content-service           ach-forwarder ──▶ LiteLLM
  (REST, OAuth AS,   (sidecar in the           (credential resolution,
   console UI)        operator Pod)             budget tags, JWT mint)
       │                 │                          │
       └──────── Postgres (source of truth) + Valkey ┘
                          ▲
                   ach-operator  (reconciles CRDs → Postgres + LiteLLM)
                   ach-migrate   (pre-install/pre-upgrade Job)
```

External dependencies the chart does NOT install: Postgres, Valkey (or Redis),
Dex, LiteLLM, an ingress controller and TLS. The operator watches ACH custom
resources in the release namespace only, so Environments and the other CRs
live there.

## Install order

1. **Dependencies up** — Postgres database + user, Valkey, LiteLLM with its own
   database and a master key, Dex with an OIDC connector to the real IdP.
   Details: `references/prerequisites.md`.
2. **Dex client for ACH** — redirect URI
   `https://ach.example.com/platform/oauth/as-callback`; ACH asks for
   `openid email profile offline_access groups`. Details: `references/auth.md`.
3. **Secrets in the release namespace** — DEK, credential pepper, LiteLLM
   master key, Dex client secret, DB and Valkey passwords. The chart creates
   none of them. The JWT signing keys are minted by the operator; do not
   pre-create them unless pinning a seed.
4. **Values file** — start from the minimal production values in
   `references/values.md` and fill each block.
5. **Render first**, then install:
   ```bash
   helm template ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
     -n ach -f values.yaml >/dev/null          # catches every `fail`/`required`
   helm upgrade --install ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
     -n ach --create-namespace -f values.yaml --wait --timeout 10m
   ```
   On a first install the forwarder and content-service restart a few times
   until the operator has minted `ach-jwt-signing-keys`; that is expected.
6. **Verify** (checklist below), then create a first Environment.

## Where to look

| Configuring / doing | Read |
|---------------------|------|
| Postgres, Valkey, Dex, LiteLLM, ingress/TLS prerequisites; required Secrets | `references/prerequisites.md` |
| Every chart value with its default, minimal production values, `extraEnv` variables | `references/values.md` |
| Dex client, OAuth redirect hosts, admins, login team enrolment, budgets and key limits, trusted IdP, JWT signing keys and rotation | `references/auth.md` |
| `litellmConnection`, what ACH needs from LiteLLM, forwarder credential headers, Gemini prefix strip | `references/litellm.md` |
| OpenCode integration: `genai.*` and what users see | `references/opencode.md` |
| Gateway vs per-service ingress, content-service standalone + RWX cache, replicas, metrics | `references/topology.md` |
| `enterprise.enabled`, observability (ClickHouse, Thanos), classifier | `references/enterprise.md` |
| Upgrading, migrations, rollback limits, values that must never change | `references/upgrade.md` |
| A pod that will not start, a render that fails, login or verification failures | `references/troubleshooting.md` |

Read only the reference for the area at hand; each is self-contained.

## Post-install verification

Run these in order; each step depends on the one before.

```bash
NS=ach; BASE=https://ach.example.com
# 1. Everything rolled out
kubectl -n $NS wait deploy -l app.kubernetes.io/name=ach --for=condition=Available --timeout=5m
kubectl -n $NS get jobs,pods
# 2. Operator bootstrap: JWT keys minted, LiteLLM connection Ready
kubectl -n $NS get secret ach-jwt-signing-keys
kubectl -n $NS get llmconn default        # READY=True
# 3. Public surface answers through the ingress
curl -fsS $BASE/.well-known/opencode | jq .config.enabled_providers
curl -fsS $BASE/.well-known/jwks.json | jq '.keys | length'     # 1 (2 during a rotation)
curl -fsS $BASE/.well-known/oauth-authorization-server | jq .issuer
# 4. A human signs in end to end (browser or device code)
ach-cli login $BASE            # --no-browser on a headless host
ach-cli whoami
ach-cli env list
```

Then apply a first Environment in the release namespace and wait for it:

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: starter
  namespace: ach
spec:
  authorizedTeams: [default]      # a LiteLLM team alias; "default" is bootstrapped by the operator
  description: "first Environment"
  runtime:
    models: [<a model name that exists in LiteLLM>]
```

```bash
kubectl -n $NS wait env/starter --for=condition=Available --timeout=2m
ach-cli env list                 # the signed-in user, if in team "default", sees it
```

`Available=True` proves the whole loop: operator ↔ Postgres ↔ LiteLLM. Writing
richer Environments, marketplaces and backends is the job of the `ach-gitops`
skill, not this one.

## Upgrade in one paragraph

Pin the chart version, read the release notes, render with the new version,
`helm upgrade` with the same values file. The `ach-migrate-<revision>` Job
runs the schema migrations as a pre-upgrade hook before any pod rolls, and CRDs
upgrade with the release (`installCRDs: true`). A chart rollback does not roll
the schema back. Full procedure: `references/upgrade.md`.

## Gotchas that cost the most time

- **`extraEnv` is where the infrastructure lives.** `ACH_BASE_URL`,
  `ACH_DB_URL`, `ACH_REDIS_ADDR`, the three `ACH_DEX_*` variables have no
  dedicated chart value. Put them in the top-level `extraEnv` (every component
  and the migrate Job read it), not `forwarder.extraEnv`: the migrate Job sees
  only the top-level list.
- **Never duplicate what the chart already injects.** Do not set
  `ACH_KEY_ENCRYPTION_KEY`, `ACH_CREDENTIAL_HASH_PEPPER`, `ACH_LITELLM_BASE_URL`
  or `ACH_LITELLM_MASTER_KEY` in `extraEnv`; the `security` and
  `litellmConnection` blocks own them and a second entry is a duplicate env var.
- **The DEK and pepper are forever.** Rotating the DEK orphans every sealed
  LiteLLM key; rotating the pepper invalidates every stored credential hash.
  Create them once, back them up, keep them out of any auto-rotation.
- **`ACH_BASE_URL` is the identity of the deployment.** It is the OAuth issuer,
  the base of the Dex redirect URI, the hydrate download URLs and the stamp on
  the LiteLLM keys this release owns (the orphan reaper only touches keys
  stamped with it). Set it to the exact public origin, `https://`, no trailing
  path, and do not change it casually.
- **`genai.providerName` is part of every OpenCode user's credentials.**
  Changing it signs every OpenCode user out.
- **`platformApi.userDefaults.maxKeys` defaults to 0**: nobody can create an
  `ek-` key until an admin grants an allowance (`ach-cli admin users limits`)
  or the default is raised. Intentional, but surprising on day one.
- **`platformApi.admins` starts empty**: every admin route answers 403 until
  emails are listed. platform-api reads the list once at start and matches it
  case-sensitively, so restart platform-api after changing it.
- **What fails the render** (`helm template` shows it): a leftover `profile:`
  value other than `full`; `litellmConnection.enabled` with an empty endpoint or
  Secret ref; empty `security.*.secretRef` names; with Enterprise on,
  observability without `projectIds`/`secretName` or the classifier without
  `secretName`/`clickhouseUrl`/`modelUrl`; `metrics.serviceMonitor` or
  `metrics.prometheusRule` without the Prometheus Operator CRDs.
- **What refuses to start** (pod CrashLoopBackOff, reason in the first log
  lines): a missing or non-`http(s)` `ACH_BASE_URL`; missing DB, Valkey, Dex
  or LiteLLM settings; a DEK that is not base64 of exactly 32 bytes; Dex not
  reachable for OIDC discovery; an empty `teams.default`; a bad
  `providerName`/`opencodeExtraProviders` id; a `passthrough` or `authorization`
  entry in `platformApi.headers`; `trustedIdP.issuer` without `audiences`;
  Enterprise-only settings while `enterprise.enabled` is false. Map each one to
  its fix in `references/troubleshooting.md`.
- **LiteLLM's own UI and admin API are not reachable through ACH.** Publish
  LiteLLM on its own host for `/ui`, `/key/*`, `/health`.
