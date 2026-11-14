# Install ACH

In this tutorial you install ACH on a Kubernetes cluster with its Helm chart,
sign in, and give your users a first Environment. It takes about thirty
minutes.

ACH runs next to services the chart does not install. You need them in place,
with these names; [Prepare the prerequisites](prerequisites.md) describes each
one.

| You need | The tutorial uses |
|---|---|
| A Kubernetes cluster, with `kubectl` and `helm` pointing at it | Namespace `ach` |
| A Postgres database and its user | Database `ach`, user `ach`, host `ach-postgresql` |
| Valkey or Redis | `ach-valkey-primary:6379`, without a password |
| LiteLLM with at least one model, and its master key | `http://litellm.litellm.svc.cluster.local:4000` |
| Dex, with a client for ACH and its secret | `https://auth.example.com`, client `ach` with the redirect address `https://ach.example.com/platform/oauth/as-callback` |
| An ingress controller and a certificate issuer | Class `nginx`, cert-manager issuer `letsencrypt` |
| A DNS name for ACH that points at the ingress | `ach.example.com` |
| [`ach-cli`](../using/install-ach-cli.md) on your machine | |

Replace the names with your own as you go.

## 1. Create the Secrets

```bash
kubectl create namespace ach
kubectl -n ach create secret generic ach-key-encryption-key \
  --from-literal=dek="$(openssl rand -base64 32)"
kubectl -n ach create secret generic ach-credential-hash-pepper \
  --from-literal=pepper="$(openssl rand -base64 32)"
kubectl -n ach create secret generic litellm-master-key \
  --from-literal=master_key='<LiteLLM master key>'
kubectl -n ach create secret generic ach-dex-client-secret \
  --from-literal=ACH_DEX_CLIENT_SECRET='<the secret of the Dex client>'
kubectl -n ach create secret generic ach-postgresql \
  --from-literal=password='<the password of the database user>'
```

The first two are generated here and must never change afterwards. Back them
up.

## 2. Write the values

Save this as `values.yaml`, with your own email under `admins`:

```yaml
litellmConnection:
  endpoint: http://litellm.litellm.svc.cluster.local:4000
  masterKeySecretRef:
    name: litellm-master-key
    key: master_key

platformApi:
  admins:
    users: [you@example.com]
  userDefaults:
    maxKeys: 5

genai:
  providerName: my-org

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

`providerName` is the name OpenCode users will sign in with. Choose it now:
changing it later signs them out.

## 3. Install

Find the latest version of the chart:

```bash
helm show chart oci://ghcr.io/ackstorm/charts/ach | grep '^version:'
```

Render the chart with your values, then install it, with that version in
place of `<X.Y.Z>`:

```bash
helm template ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
  -n ach -f values.yaml > /dev/null
helm upgrade --install ach oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z> \
  -n ach -f values.yaml --wait --timeout 10m
```

The first command prints nothing when the values are complete. The second
ends with `STATUS: deployed` and a summary of the install.

## 4. Check the install

```bash
kubectl -n ach get deployments
```

```text
NAME               READY   UP-TO-DATE   AVAILABLE
ach-forwarder      1/1     1            1
ach-gateway        1/1     1            1
ach-operator       1/1     1            1
ach-platform-api   1/1     1            1
```

```bash
kubectl -n ach get litellmconnection default
```

```text
NAME      READY   REASON
default   True    Ready
```

`READY` is `True` once the operator has reached LiteLLM with the master key.

```bash
curl -fsS https://ach.example.com/.well-known/oauth-authorization-server | jq -r .issuer
```

```text
https://ach.example.com
```

## 5. Sign in

```bash
ach-cli login https://ach.example.com
```

Your browser opens the sign-in page of your identity provider. When it says
you can close the page, check who you are:

```bash
ach-cli whoami
```

`User` shows the email you listed under `admins`.

## 6. Grant a first Environment

An Environment names what a team may use: models, tools, plugins and skills.
List the models your LiteLLM serves:

```bash
ach-cli admin list models
```

Save this as `quickstart.yaml`, with one of those models:

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
      - <a model from the list>
  context:
    plugins:
      - ach-cli@ach
  authorizedTeams:
    - default
  description: |
    First Environment: one model and the ach-cli agent plugin.
```

The `PluginMarketplace` makes the plugins of the ACH repository available.
The Environment gives one model and one of those plugins to the team
`default`, which every user joins when they sign in.

```bash
kubectl -n ach apply -f quickstart.yaml
kubectl -n ach wait environment/quickstart --for=condition=Available --timeout=5m
ach-cli environment list
```

`quickstart` is in the list.

## 7. Hand over to a developer

Send your users the address `https://ach.example.com`. They follow
[Your first Environment](../using/first-environment.md) from their own
machine, and `quickstart` is the Environment they find there.

When you no longer need it, remove it:

```bash
kubectl -n ach delete -f quickstart.yaml
```

Deleting an Environment also revokes every environment key created for it.

## Where next

- [Configure sign-in](configure-sso.md): more admins, teams from your identity
  provider's groups, budgets.
- [Connect ACH to LiteLLM](connect-litellm.md): what to register in LiteLLM.
- [Choose a topology](choose-a-topology.md): replicas, metrics, dashboards.
- [Upgrade ACH](upgrade.md)
- [Troubleshoot an ACH install](troubleshoot.md) when a step above fails.
