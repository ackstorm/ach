# Prepare the prerequisites

The ACH chart installs ACH only. This page lists what must exist before you
[install the chart](install-the-chart.md), and the Secrets you create for it.

The examples use `https://ach.example.com` for ACH, `https://auth.example.com`
for Dex, and the namespace `ach`.

## Kubernetes and Helm

- Kubernetes 1.27 or later, and Helm 3 with OCI support.
- A StorageClass for the 10Gi cache volume of the operator. The default access
  mode is `ReadWriteOnce`; see [Choose a topology](choose-a-topology.md) for
  the case that needs `ReadWriteMany`.
- Rights to create cluster-scoped objects: the chart creates the ACH custom
  resource definitions (`*.ach.ackstorm.ai`), ClusterRoles and
  ClusterRoleBindings.

The chart is `oci://ghcr.io/ackstorm/charts/ach` and the image is
`ghcr.io/ackstorm/ach` (amd64 and arm64, public). Chart `X.Y.Z` runs image
`vX.Y.Z`.

## Postgres

ACH keeps its state in Postgres. The operator writes it and every other
service reads it.

- Create one database and one user that owns it. The install creates the
  tables, so the user needs the right to create them.
- ACH reads the connection from `ACH_DB_URL` in its full form:
  `postgres://ach:<password>@<host>:5432/ach?sslmode=<mode>`. A URL without
  the user, the port or the scheme stops the services at startup.
- Percent-encode special characters in the password (`/` as `%2F`, `?` as
  `%3F`, `#` as `%23`, `%` as `%25`), or use a password of letters and digits.
- Connect directly or through a pooler in session mode. ACH uses
  `LISTEN/NOTIFY`, which a pooler in transaction mode drops.
- Use `sslmode=require` or stricter when Postgres is outside the cluster
  network.

LiteLLM needs its own, separate database.

## Valkey or Redis

Valkey holds sign-in state and caches. One standalone instance is enough: if
it is lost, sign-ins in progress fail and the caches refill.

| Variable | Required | Value |
|---|---|---|
| `ACH_REDIS_ADDR` | Yes | `host:port` |
| `ACH_REDIS_PASSWORD` | No | The password, from a Secret |
| `ACH_REDIS_TLS` | No | `true` to connect with TLS |
| `ACH_REDIS_DB` | No | Logical database, `0` by default |

## LiteLLM

ACH controls access to a LiteLLM proxy and does not replace it.

- LiteLLM 1.108.0 or later, with its own Postgres database, reachable from the
  ACH namespace. The chart default is
  `http://litellm.litellm.svc.cluster.local:4000`.
- Its master key, which you store in a Secret (below).
- Its own public host, if anyone needs the LiteLLM UI or admin API. ACH does
  not serve LiteLLM's `/ui`, `/key/*` or `/health`.

[Connect ACH to LiteLLM](connect-litellm.md) covers what to configure inside
LiteLLM.

## Dex

Users sign in to ACH through [Dex](https://dexidp.io/), which connects to your
identity provider.

- One issuer URL that both the users' browsers and the ACH pods can reach. ACH
  reads `<issuer>/.well-known/openid-configuration` at startup and does not
  start without it.
- A connector that issues refresh tokens. ACH renews every session with the
  user's Dex refresh token, and a sign-in without one fails.
- A connector that sends the `groups` claim, if teams or admins should follow
  groups of your identity provider.
- Persistent storage. With in-memory storage every Dex restart signs everyone
  out.
- A client for ACH. [Configure sign-in](configure-sso.md#the-dex-client)
  shows it.

## Ingress and TLS

- One public hostname for ACH with TLS. `ACH_BASE_URL` must be exactly this
  origin, for example `https://ach.example.com`.
- The ingress must pass `Cookie` and `Set-Cookie` unchanged on
  `/platform/oauth/`, and allow long streaming responses on `/v1/` and
  `/mcp/`.
- Never publish `/metrics`.

The chart can create the Ingress, or you route to ACH yourself; see
[Choose a topology](choose-a-topology.md).

## Secrets to create

Create these in the ACH namespace before the install. The names of the first
three are the chart defaults.

| Secret | Key | Content |
|---|---|---|
| `ach-key-encryption-key` | `dek` | Base64 of exactly 32 random bytes |
| `ach-credential-hash-pepper` | `pepper` | Random, at least 32 bytes |
| `litellm-master-key` | `master_key` | The LiteLLM master key |
| Your choice, for example `ach-dex-client-secret` | Your choice | The secret of the Dex client for ACH |
| Your choice, for example `ach-postgresql` | `password` | The password of the ACH database user |
| Your choice, for example `ach-valkey` | `password` | The Valkey password, if it has one |

```bash
kubectl create namespace ach
kubectl -n ach create secret generic ach-key-encryption-key \
  --from-literal=dek="$(openssl rand -base64 32)"
kubectl -n ach create secret generic ach-credential-hash-pepper \
  --from-literal=pepper="$(openssl rand -base64 32)"
kubectl -n ach create secret generic litellm-master-key \
  --from-literal=master_key='<LiteLLM master key>'
kubectl -n ach create secret generic ach-dex-client-secret \
  --from-literal=ACH_DEX_CLIENT_SECRET='<the secret registered in Dex>'
```

!!! warning "Create the first two once and keep them"
    A new value in `ach-key-encryption-key` makes every stored LiteLLM key
    unreadable. A new value in `ach-credential-hash-pepper` invalidates every
    ACH key. Back both up, and keep them out of anything that regenerates
    Secrets.

Do not create `ach-jwt-signing-keys`: the operator creates it on its first
start.

## Verify

```bash
kubectl -n ach get secret ach-key-encryption-key ach-credential-hash-pepper \
  litellm-master-key ach-dex-client-secret
curl -fsS https://auth.example.com/.well-known/openid-configuration | jq .issuer
```

The four Secrets are listed, and the issuer printed is the URL you will give
ACH, character for character.

## Related

- [Install ACH with Helm](install-the-chart.md)
- [Helm values](../reference/helm-values.md)
