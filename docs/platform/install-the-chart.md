# Install ACH with Helm

This page installs ACH on a cluster where the
[prerequisites](prerequisites.md) exist: Postgres, Valkey, LiteLLM, Dex, an
ingress controller and the Secrets.

For a first install with every step in order, follow the
[Install ACH](install-ach.md) tutorial.

## What gets deployed

One image, `ghcr.io/ackstorm/ach`, runs every component.

| Object | Purpose |
|---|---|
| Deployment `ach-operator` | Applies the ACH resources (Environments, plugins, skills) to Postgres and LiteLLM. Its Pod also runs content-service, which serves plugin and skill files |
| Deployment `ach-platform-api` | The API, the sign-in server and the web console |
| Deployment `ach-forwarder` | Carries model, MCP and A2A traffic to LiteLLM |
| Deployment `ach-gateway` | One Service, `ach-gateway`, in front of the three above |
| Job `ach-migrate-<revision>` | Creates and updates the database tables before each install and upgrade |
| The ACH custom resource definitions | Kept when the release is uninstalled, together with your resources |

The operator reads ACH resources in the release namespace only, so
Environments and the other resources go there.

## Write the values file

Start from this `values.yaml` and replace the hosts, names and emails. Every
value it leaves out keeps its default; the
[Helm values reference](../reference/helm-values.md) lists them all.

```yaml
litellmConnection:
  endpoint: http://litellm.litellm.svc.cluster.local:4000
  masterKeySecretRef:
    name: litellm-master-key
    key: master_key

platformApi:
  admins:
    users: [admin@example.com]
  userDefaults:
    maxBudget: 100           # USD per user for each budgetDuration
    budgetDuration: "30d"
    maxKeys: 5               # environment keys one user may hold

genai:
  providerName: my-org       # the name OpenCode users sign in with

ingress:
  enabled: true
  className: nginx
  host: ach.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  tls:
    - hosts: [ach.example.com]
      secretName: ach-tls

extraEnv:
  - name: ACH_BASE_URL
    value: https://ach.example.com
  - name: DB_PASSWORD
    valueFrom:
      secretKeyRef: {name: ach-postgresql, key: password}
  - name: ACH_DB_URL
    value: postgresql://ach:$(DB_PASSWORD)@ach-postgresql:5432/ach?sslmode=disable
  - name: ACH_REDIS_ADDR
    value: ach-valkey-primary:6379
  - name: ACH_DEX_ISSUER_URL
    value: https://auth.example.com
  - name: ACH_DEX_CLIENT_ID
    value: ach
  - name: ACH_DEX_CLIENT_SECRET
    valueFrom:
      secretKeyRef: {name: ach-dex-client-secret, key: ACH_DEX_CLIENT_SECRET}
```

What to know about these values:

- **The database, Valkey and Dex settings have no value of their own.** They
  go in the top-level [`extraEnv`](../reference/helm-values.md#extraenv), which
  the migration Job and every component except the classifier receive. A component's own `extraEnv`
  does not reach the migration Job.
- **`$(DB_PASSWORD)`** is replaced by Kubernetes with the variable defined
  above it, so the password stays in its Secret.
- **`ACH_BASE_URL`** is the public origin of this install, with `https://` and
  no path. Sign-in, download links and the keys ACH creates in LiteLLM carry
  it. Do not change it after users have signed in.
- **`genai.providerName`** is part of the credentials OpenCode stores. Changing
  it signs every OpenCode user out, so choose it once.
- **`platformApi.admins`** is empty by default, and so nobody is an admin.
  [Configure sign-in](configure-sso.md#admins) covers both ways to name admins.
- **`platformApi.userDefaults.maxKeys`** is `0` by default: nobody can create
  an environment key until an admin allows it.
- **Do not add** `ACH_KEY_ENCRYPTION_KEY`, `ACH_CREDENTIAL_HASH_PEPPER`,
  `ACH_LITELLM_BASE_URL` or `ACH_LITELLM_MASTER_KEY` to `extraEnv`. The chart
  sets them from the `security` and `litellmConnection` values.

## Render

Render the chart before you install. A missing required value fails here, with
a message that names it.

```bash
helm template ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
  -n ach -f values.yaml > /dev/null
```

## Install

```bash
helm upgrade --install ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
  -n ach --create-namespace -f values.yaml --wait --timeout 10m
```

On a first install the forwarder may restart once or twice, until the operator
has created the Secret `ach-jwt-signing-keys`.

## Verify

Run these in order; each depends on the one before.

```bash
# 1. Every Deployment is available
kubectl -n ach wait deploy -l app.kubernetes.io/name=ach \
  --for=condition=Available --timeout=5m

# 2. The operator created the signing keys and reached LiteLLM
kubectl -n ach get secret ach-jwt-signing-keys
kubectl -n ach get litellmconnection default        # READY is True

# 3. ACH answers on its public address
curl -fsS https://ach.example.com/.well-known/oauth-authorization-server | jq .issuer
curl -fsS https://ach.example.com/.well-known/jwks.json | jq '.keys | length'   # 1

# 4. A person can sign in
ach-cli login https://ach.example.com
ach-cli whoami
```

If a step fails, see [Troubleshoot an ACH install](troubleshoot.md).

## Agent credentials by token exchange

The platform API reviews ServiceAccount tokens for `POST /platform/keys/exchange`
through a cluster-wide `system:auth-delegator` binding the chart creates. Set
API server audit level `Metadata` for `tokenreviews`, and bind the
`<release>-keygrant-editor` ClusterRole (`ach-keygrant-editor` for release `ach`) to platform admins only. See
[Agent credentials by token exchange](../integrate/exchange-a-serviceaccount-token.md).

## Related

- [Configure sign-in](configure-sso.md): admins, teams, budgets.
- [Connect ACH to LiteLLM](connect-litellm.md)
- [Choose a topology](choose-a-topology.md): ingress, replicas, metrics.
- [Upgrade ACH](upgrade.md)
