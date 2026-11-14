# Installation

ACH installs on Kubernetes with one Helm chart, `oci://ghcr.io/ackstorm/charts/ach`,
running one image, `ghcr.io/ackstorm/ach`. The full guide (every value, sign-in,
topology, Enterprise, upgrades, troubleshooting) is the `ach-install` agent skill in
this repository: [`plugins/ach-install/skills/ach-install/`](https://github.com/ackstorm/ach/tree/main/plugins/ach-install/skills/ach-install).
Install it into your agent (see [Install the ACH agent plugins](../using/install-agent-plugins.md)) and
ask it, or read it directly.

## What you need first

The chart installs none of these:

- **Postgres**: a database and user for ACH. It is the source of truth for ACH state.
- **Valkey or Redis**: caches and OAuth session state.
- **LiteLLM** with its own database and a master key: ACH sits in front of it.
- **Dex** with a connector to your identity provider: the only way users sign in. Register
  a client for ACH with redirect URI `https://<ACH host>/platform/oauth/as-callback`.
- An ingress controller and TLS for the ACH host.

## Steps

1. Create the Secrets in the release namespace: the at-rest DEK and the credential
   pepper (`security.*` values; generated once, kept stable), the LiteLLM master key
   (`litellmConnection.masterKeySecretRef`), the Dex client secret and the database and
   Valkey passwords. The operator mints the JWT signing keys itself.
2. Write a values file. Start from the minimal production values in the skill's
   `references/values.md`; `deploy/helm/ach/values.yaml` documents every key.
3. Render, then install:

   ```bash
   helm template ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
     -n ach -f values.yaml >/dev/null
   helm upgrade --install ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
     -n ach --create-namespace -f values.yaml --wait --timeout 10m
   ```

   On a first install the forwarder and content-service restart a few times until the
   operator has minted `ach-jwt-signing-keys`.

4. Verify: every Deployment is Ready, `kubectl -n ach get litellmconnection default`
   is Ready, and `ach-cli login https://<ACH host>` followed by `ach-cli whoami` shows
   your user. Then [grant a first Environment](#grant-a-first-environment).

The chart manages the CRDs (`installCRDs: true`), so `helm upgrade` upgrades them too
and `helm uninstall` leaves them and your resources in place.

## Agent credentials by token exchange

The platform API reviews ServiceAccount tokens for `POST /platform/keys/exchange`
through a cluster-wide `system:auth-delegator` binding the chart creates. Set
API server audit level `Metadata` for `tokenreviews`, and bind the
`<release>-keygrant-editor` ClusterRole (`ach-keygrant-editor` for release `ach`) to platform admins only. See
[Agent credentials by token exchange](../integrate/exchange-a-serviceaccount-token.md).

## Grant a first Environment

An Environment names what a team may use. Save this as `quickstart.yaml`, replacing the
model and team with ones your LiteLLM has (`ach-cli admin list models` and
`ach-cli admin list teams` show them, for an admin):

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: PluginMarketplace
metadata:
  name: ach
spec:
  type: github
  github:
    repo: ackstorm/ach
    ref: main
  refresh:
    interval: 1h
    maxStaleness: 24h
---
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: quickstart
spec:
  runtime:
    models:
      - <a LiteLLM model name>
  context:
    plugins:
      - ach-cli@ach
  authorizedTeams:
    - <a LiteLLM team alias>
  description: |
    First Environment: one model and the ach-cli agent plugin.
```

```bash
kubectl -n ach apply -f quickstart.yaml
kubectl -n ach get pluginmarketplace ach        # Synced=True
kubectl -n ach get environment quickstart       # Available=True
```

`Available` needs both halves: `ExecutionResourcesResolved` (the plugin was found) and
`AccessGroupSynced` (LiteLLM accepted the grant). `kubectl -n ach describe environment
quickstart` shows the reason when one is False.

Users of that team can now follow [Your first Environment](../using/first-environment.md).
To remove it again:

```bash
kubectl -n ach delete -f quickstart.yaml
```

Deleting an Environment also revokes every `ek-` key created for it.
