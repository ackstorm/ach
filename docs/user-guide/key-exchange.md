# Agent credentials by ServiceAccount token exchange

An agent running in ACH's cluster can obtain an Environment credential without
anyone minting an `ek_` by hand. Its pod proves who it is with its Kubernetes
ServiceAccount token; ACH answers with a short-lived ACH JWT that stands for one
stable `ek_` held inside ACH. The raw `ek_` never leaves ACH.

## Granting an identity to an agent

A platform admin writes one `KeyGrant` per agent in ACH's namespace:

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: KeyGrant
metadata:
  name: ai-run-k8s-doctor
  namespace: ach
spec:
  identity: ai.run@ackstorm.com        # exactly one email, lower-case
  environments: [agent-k8s-doctor]     # exact Environment names
  requesters:
    serviceAccounts:                   # exact "<namespace>/<name>"
      - ach-agent/ach-agent-control
  budget:                              # optional, per key this grant covers
    maxBudget: 50
    budgetDuration: 30d
```

- A grant naming a shared ServiceAccount is usable by every pod running it.
- No wildcards or patterns anywhere. A grant allows every combination of its
  `environments` and `serviceAccounts`; lists from different grants are never
  combined.
- The identity's teams must still intersect the Environment's
  `authorizedTeams`. Teams are enrolled at login, so the identity must log in
  to ACH once by hand before its first exchange; until then the grant reports
  `Ready=False` with reason `IdentityUnknown`. A missing Environment reports
  `EnvironmentMissing`.
- `spec.budget` caps each exchange key the grant covers, through the key's own
  `key:<id>` tag. When several grants cover one key the lowest `maxBudget`
  applies. Removing `spec.budget` leaves the last ceiling in place.
- Removing a grant entry cuts access within 60 seconds (the resolver cache) and
  the operator then revokes the key. Adding the entry back creates a new key:
  its spend history and per-key budget start over.
- There is no admin API route for grants; read them with `kubectl get keygrants`.

## Who may write grants

Writing a `KeyGrant` is the power to act as its identity. The chart ships the
ClusterRole `<release>-keygrant-editor` (`ach-keygrant-editor` for release
`ach`; create, update, patch, delete on `keygrants`) bound to nobody. Bind it to the platform-admin group only:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: platform-admins-keygrant-editor
  namespace: ach
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ach-keygrant-editor # <release>-keygrant-editor
subjects:
- apiGroup: rbac.authorization.k8s.io
  kind: Group
  name: platform-admins
```

The built-in `admin` and `edit` roles do not include `keygrants` writes, but any
wildcard role (`*` on `ach.ackstorm.ai` or on all resources) in ACH's namespace
grants them.

## The exchange

`POST <ACH_BASE_URL>/platform/keys/exchange` with
`Authorization: Bearer <pod-bound ServiceAccount token>` whose audience is
`ACH_BASE_URL` without a trailing slash, and the body
`{"identity": "ai.run@ackstorm.com", "environment": "agent-k8s-doctor"}`.

`200`: `{"token": "<jwt>", "expires_in": 900, "key_id": "ekid_…"}`. Schedule
the next exchange from `expires_in`. The JWT is accepted wherever an `ek_` is
(`x-ach-key` or `Authorization: Bearer`) on the model, MCP, A2A, hydrate and
content routes. Every exchange for the same identity, Environment and
ServiceAccount returns the same `key_id`; a new JWT does not invalidate the
previous one, which stays valid until it expires.

| Status | Code | Meaning |
|---|---|---|
| 400 | `invalid_argument` | body is not `{"identity","environment"}` |
| 401 | `invalid_token` | not a pod-bound ServiceAccount token for this ACH (wrong audience, a token minted without a bound pod, a `pk_`/`ek_`/ACH JWT, or a token whose signature, issuer or expiry the cluster's ServiceAccount keys do not confirm) |
| 429 | `rate_limited` | too many exchange requests from this client or overall |
| 403 | `not_granted` | no single `KeyGrant` names this identity, Environment and ServiceAccount together |
| 404 | `environment_not_found` | no such Environment |
| 503 | `not_ready` | the Environment is not synced yet, TokenReview is unavailable (it times out after 5 s), or the cluster's ServiceAccount keys cannot be read |
| 403 | `unauthorized_team` | the identity's teams do not intersect `authorizedTeams` |
| 409 | `key_suspended` | the exchange key was suspended (by its owner or an admin) |

The JWT's lifetime (and the response's `expires_in`) is
`platformApi.keyExchange.tokenTTL` (default `15m`, between `1m` and `1h`).

The route is mounted only when platform-api has in-cluster Kubernetes
configuration (for TokenReview) and the OAuth authorization server; otherwise
it answers 404.

## Installation requirements

- The chart binds the platform-api ServiceAccount to `system:auth-delegator`
  cluster-wide, which allows TokenReview and SubjectAccessReview only.
- Before TokenReview, platform-api verifies the token's signature against the
  cluster's ServiceAccount JWKS (`GET /openid/v1/jwks` and
  `GET /.well-known/openid-configuration`). Kubernetes lets any authenticated
  caller read both through the default `system:service-account-issuer-discovery`
  ClusterRoleBinding, so the chart adds no RBAC for them. A cluster that
  removed or narrowed that binding must restore it for the platform-api
  ServiceAccount, or every exchange answers 503 `not_ready`.
- Only RS256 and ES256 ServiceAccount signing keys are supported; a cluster
  signing with ES384, ES512 or another algorithm gets 401 `invalid_token` for
  every exchange. A token carrying an old issuer after a
  `--service-account-issuer` migration passes TokenReview but fails the local
  issuer check, so agents must restart to receive tokens of the new issuer.
- The API server stores TokenReview bodies, which contain the token, only under
  audit level `Request` or `RequestResponse`. Set level `Metadata` for
  `tokenreviews` in the API server audit policy (on EKS, check the control-plane
  audit policy).
- Run agents in a namespace dedicated to them, where only Flux and the
  ach-agent operator can create pods: anyone who can create a pod there can run
  it as an agent's ServiceAccount.

## Audit

Every exchange from the body check onwards, granted or denied, emits one
`platform.ek.exchange` audit event: actor (the ServiceAccount), requester, pod name and uid, identity,
Environment, key id, outcome and, for a denial, the reason. No token is ever
recorded.

Requests rejected earlier (wrong audience, rate limit, including the global
limit after a valid signature, a token the cluster's keys do not confirm) are
not audited, because the caller is unauthenticated;
they are counted in `ach_key_exchange_rejected_total{reason}` with reason
`guard`, `rate_limited`, `invalid_signature` or `keys_unavailable`.

## Limits and residual exposure

- The per-client rate limit keys on the first `X-Forwarded-For` hop, which a
  caller can forge. The global limit (10 requests per second) is the real bound
  on TokenReview calls; it is charged only after the token's cluster signature
  checks out, so an anonymous caller cannot consume it with forged tokens.
- A JWT is a bearer token valid until it expires; deleting the pod does not
  revoke a JWT already issued. After a pod is deleted, exposure is bounded by
  the JWT lifetime (`tokenTTL`, 15 minutes by default) plus TokenReview's own
  cache, which accepts a deleted pod's token for up to 60 seconds.
- Grant removal, key revoke and key suspend take effect within the 60 second
  resolver cache; suspend does not purge the cached JWT resolutions. When the
  operator revokes a key, forwarded LiteLLM traffic stops at once because the
  LiteLLM key is revoked first, while hydrate and content reads can keep
  working for up to 60 seconds.
- `PATCH /platform/keys/{id}/budget` on an exchange key answers 409
  `budget_managed_by_grant`: the grant's ceiling is the only one, applied at
  every exchange and by the operator sweep within 5 minutes. The identity's own
  user can still suspend, resume and revoke the key; while it is suspended,
  `POST /platform/keys/exchange` answers 409 `key_suspended` until it is
  resumed.
