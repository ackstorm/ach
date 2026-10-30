# Prerequisites

The chart installs ACH only. Everything below must exist first, reachable from
the release namespace.

## Contents

- Kubernetes and Helm
- Postgres
- Valkey / Redis
- LiteLLM
- Dex
- Ingress and TLS
- Secrets to create
- A worked `extraEnv` block

## Kubernetes and Helm

- Kubernetes `>= 1.27` (the chart's `kubeVersion`), Helm 3 with OCI support.
- Chart: `oci://ghcr.io/ackstorm/charts/ach`, versioned like the image
  (`<X.Y.Z>` chart = `v<X.Y.Z>` image, the chart's `image.tag` default).
- Image: `ghcr.io/ackstorm/ach` (multi-arch amd64/arm64, public).
- A StorageClass for the 10Gi artifact cache PVC (`operator.cache`), RWO by
  default; RWX only when content-service runs standalone
  (`references/topology.md`).
- Cluster-scoped install rights: the chart creates CRDs
  (`*.ach.ackstorm.ai`) and a ClusterRole/Binding for the operator.

## Postgres

ACH's source of truth. Every service reads it; the operator writes it.

- One database and one owner user for ACH (`ach`/`ach` is the convention).
  The migrate Job creates the schema; give the user rights to create tables in
  its database.
- DSN in `ACH_DB_URL`, full form only:
  `postgres://ach:<password>@<host>:5432/ach?sslmode=<mode>`. A DSN missing the
  user, port or scheme panics at startup and looks like a network fault.
- The forwarder, platform-api and content-service keep in-memory caches fed by
  Postgres `LISTEN/NOTIFY`. Point them at Postgres directly or through a
  session-mode pooler; a transaction-mode pooler drops `LISTEN`.
- Any Postgres operator or chart works (Bitnami `postgresql`, CloudNativePG,
  RDS). LiteLLM needs its own, separate database.

## Valkey / Redis

Holds OAuth state, the credential resolver cache and the per-user OpenCode
config cache.

- `ACH_REDIS_ADDR` = `host:port` (required by platform-api and forwarder).
- Optional: `ACH_REDIS_PASSWORD` (from a Secret), `ACH_REDIS_TLS=true`,
  `ACH_REDIS_DB` (logical DB, default 0).
- A standalone instance is enough. Losing it signs users out of in-flight
  logins and empties caches; it is not the source of truth.

## LiteLLM

ACH governs access to a LiteLLM proxy; it does not replace it. See
`references/litellm.md` for the configuration ACH relies on. Before install:

- LiteLLM running with its own Postgres, reachable in-cluster (default value
  `http://litellm.litellm.svc.cluster.local:4000`).
- Its master key in a Secret in the ACH release namespace (or set
  `litellmConnection.masterKeySecretRef.namespace`).
- LiteLLM published on its own host if anyone needs its UI or admin API: ACH
  never proxies `/ui`, `/key/*`, `/health`.

## Dex

ACH's only login is its own OAuth 2.1 authorization server, which delegates
the human sign-in to Dex. Dex in turn federates the real IdP (Google, Entra ID,
Okta, LDAP...). See `references/auth.md` for the client definition. Before
install:

- Dex reachable at ONE issuer URL from both the users' browsers and the
  platform-api pods (platform-api fetches `<issuer>/.well-known/openid-configuration`
  at startup and refuses to start when it cannot).
- A connector that supports refresh tokens: every ACH token refresh replays
  the user's Dex refresh token, so a user disabled at the IdP is out within the
  hour. A login without a Dex refresh token fails loudly.
- If login teams should follow IdP groups, a connector configured to emit the
  `groups` claim.
- Persistent Dex storage (Kubernetes CRDs or a database). In-memory storage
  signs everyone out at each Dex restart.

## Ingress and TLS

- One public hostname for ACH (`ach.example.com`), TLS-terminated at the
  ingress or gateway. The value of `ACH_BASE_URL` must be exactly this origin.
- An `https://` base makes ACH issue `__Host-`/`Secure` cookies. Plain `http://`
  works only for local tests, and `ach-cli` then needs `--insecure`
  (`ACH_INSECURE=1`).
- The ingress must pass `Set-Cookie`/`Cookie` untouched on `/platform/oauth/*`
  and allow long-lived streaming responses (SSE on `/v1` and `/mcp`).
- Never expose `/metrics`; the gateway already answers 404 there.

Options: the chart's own `ingress` block (one rule to `ach-gateway`), a Gateway
API `HTTPRoute` you write, or per-service routing without the gateway. See
`references/topology.md`.

## Secrets to create

All in the release namespace, before the first install. Names below are the
chart defaults; any name works if the values point at it.

| Secret | Key | Content | Read by |
|--------|-----|---------|---------|
| `ach-key-encryption-key` | `dek` | base64 of exactly 32 random bytes | platform-api, forwarder |
| `ach-credential-hash-pepper` | `pepper` | random, at least 32 bytes | operator, platform-api, forwarder, content-service |
| `litellm-master-key` | `master_key` | LiteLLM master key | all services (via `litellmConnection`) |
| your choice, e.g. `ach-dex-client-secret` | your choice | the Dex client secret for ACH | platform-api (via `extraEnv`) |
| your choice, e.g. `ach-postgresql` | `password` | the ACH DB password | everything that builds `ACH_DB_URL` |
| your choice, e.g. `ach-valkey` | `password` | Valkey password, if auth is on | platform-api, forwarder, content-service |

```bash
kubectl -n ach create secret generic ach-key-encryption-key \
  --from-literal=dek="$(openssl rand -base64 32)"
kubectl -n ach create secret generic ach-credential-hash-pepper \
  --from-literal=pepper="$(openssl rand -base64 32)"
kubectl -n ach create secret generic litellm-master-key \
  --from-literal=master_key='<LiteLLM master key>'
kubectl -n ach create secret generic ach-dex-client-secret \
  --from-literal=ACH_DEX_CLIENT_SECRET='<the secret registered in Dex>'
```

In GitOps, generate the DEK and pepper once (a secret generator or an
ExternalSecret from a vault) and make sure nothing ever regenerates them:
rotating the DEK orphans every sealed LiteLLM key, rotating the pepper
invalidates every stored credential hash.

`ach-jwt-signing-keys` is NOT in this list: the operator mints it on first boot
and keeps it across `helm uninstall`. Create it by hand only to pin a known
seed (recipe in `references/auth.md`).

## A worked `extraEnv` block

The infrastructure settings have no dedicated chart values; they go in the
top-level `extraEnv`, which every component and the migrate Job receive.
Kubernetes expands `$(VAR)` from variables defined earlier in the same list,
which keeps the password out of the DSN literal:

```yaml
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
  - name: ACH_REDIS_PASSWORD
    valueFrom:
      secretKeyRef: {name: ach-valkey, key: password}
  - name: ACH_DEX_ISSUER_URL
    value: https://auth.example.com
  - name: ACH_DEX_CLIENT_ID
    value: ach
  - name: ACH_DEX_CLIENT_SECRET
    valueFrom:
      secretKeyRef: {name: ach-dex-client-secret, key: ACH_DEX_CLIENT_SECRET}
```

Use `sslmode=require` (or stricter) when Postgres is outside the cluster
network.
