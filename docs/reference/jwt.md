# Forwarder JWT

The forwarder mints this token on `/mcp/<name>` and `/a2a/<name>` when a
`BackendIdentityPolicy` with `spec.forwardIdentityJWT: true` targets `<name>`.
Every forwarded request gets a new token; there is no refresh.

## Header

| Field | Value |
|---|---|
| `alg` | `EdDSA` (Ed25519) |
| `typ` | `JWT` |
| `kid` | Id of the signing key; resolve it against the JWKS |

## Claims

| Claim | Value | Present |
|---|---|---|
| `iss` | The forwarder's `ACH_BASE_URL`; HTTPS in production | always |
| `sub` | Owner email of the calling key | always |
| `email` | Same value as `sub` | when the owner email is not empty |
| `groups` | Array of LiteLLM team aliases; see [The `groups` claim](#the-groups-claim) | when the set is not empty |
| `teams` | Array of every LiteLLM team alias of the caller; see [The `teams` claim](#the-teams-claim) | when the target's policy sets `forwardTeams` and the set is not empty |
| `azp` | `ek:<environment>`; for audit, grants nothing | on a token minted for an environment key |
| `key_share` | Per-user, per-server key material; see [The `key_share` claim](#the-key_share-claim) | on `/mcp/<name>` when the forwarder has a key share secret |
| `aud` | `mcp:<name>` on `/mcp/<name>`, `a2a:<name>` on `/a2a/<name>` | always |
| `iat` | Mint time, Unix seconds | always |
| `exp` | `iat + 120` | always |
| `nbf` | — | never |
| `jti` | — | never |

A verifier checks `iss`, `aud` and `exp`. It must not require `nbf` or `jti`.

## The `groups` claim

| Calling key | `groups` |
|---|---|
| `pk_` | The caller's LiteLLM teams ∩ `authorizedTeams` of the active Environments whose `runtime.mcpServers` (or `runtime.a2aAgents`) contains `<name>` |
| `ek_` | The same set, computed for the key's owner email; never the Environment's full `authorizedTeams` |

| Property | Value |
|---|---|
| Element | LiteLLM `team_alias`, or `team_id` when the alias is empty. An `authorizedTeams` entry written as a team id appears as that id |
| Order | Sorted, deduplicated |
| Renaming a LiteLLM team | Changes the value in the claim |
| Excluded | ACH's internal teams `ach-env-*` and `ach-user-*` |
| Empty set | The claim is omitted |
| Revocation | None: a backend trusts the claim until `exp` |
| A user removed from a LiteLLM team keeps the group, on `pk_` and `ek_`, for at most | 180 s (`exp` 120 s + 60 s membership cache) |
| A team removed from `Environment.spec.authorizedTeams` stays in the claim for at most | about 7 min (`exp` 120 s + 5 min refresh) |

## The `teams` claim

Emitted only for a target whose `BackendIdentityPolicy` sets
`spec.forwardTeams: true`.

| Calling key | `teams` |
|---|---|
| `pk_` | Every LiteLLM team of the caller, whether or not an Environment that grants `<name>` authorizes it |
| `ek_` | The same set, computed for the key's owner email |

| Property | Value |
|---|---|
| Element, order, excluded teams, empty set, revocation | As for `groups` |
| Relation to `groups` | A superset: every entry of `groups` is in `teams` |
| A user removed from a LiteLLM team keeps it, on `pk_` and `ek_`, for at most | 180 s (`exp` 120 s + 60 s membership cache) |
| Removing a team from `Environment.spec.authorizedTeams` | Does not remove it from `teams` |

## The `key_share` claim

An MCP backend that stores something per user (an OAuth grant for the upstream
provider) can derive that user's encryption key from its own secret plus
`key_share`. A copy of its store and its secret then decrypts nothing without a
request from ACH that carries the user's identity.

| Property | Value |
|---|---|
| Present | On `/mcp/<name>` tokens when the forwarder has `ACH_MCP_KEY_SHARE_SECRET` (Helm `security.mcpKeyShare`). Never on `/a2a/<name>` tokens |
| Value | base64url without padding of HMAC-SHA256(secret, `aud` + one `0x00` byte + `sub`) |
| Stable | Identical on every request with the same `aud` and `sub` while the secret is unchanged |
| Changes when | The secret changes (every user on every server), the MCP server is renamed (its `aud` changes) or the user's email changes |
| A changed value | What the backend stored under the old value is unreadable; the user consents again |
| In transit | The token passes through LiteLLM to the backend. The token expires after 120 seconds, the share inside it does not: never log the token |

Test vector:

| Input | Value |
|---|---|
| Secret (ASCII) | `test-secret-0123456789abcdef0123` |
| `aud` | `mcp:mcp-gcp-cli-ro` |
| `sub` | `user@example.com` |
| `key_share` | `L7cb17nvOPFQFJIyXEF4atVSJTPmoKtjmMtt5cjrNKw` |

## JWKS endpoint

```
GET <iss>/.well-known/jwks.json
```

| Response header | Value |
|---|---|
| `Content-Type` | `application/jwk-set+json` |
| `Cache-Control` | `public, max-age=3600` |

| JWK field | Value |
|---|---|
| `kty` | `OKP` |
| `crv` | `Ed25519` |
| `use` | `sig` |
| `alg` | `EdDSA` |
| `kid` | Id of the key; matches the token header |
| `x` | The 32-byte public key, base64url without padding |

`keys` holds one key, or two during a rotation: both stay published for at
least 24 hours before the old one is removed
([JWT key rotation](../platform/rotate-jwt-signing-keys.md)).

Implementation:
[`internal/forwarder/jwt/signer.go`](https://github.com/ackstorm/ach/blob/main/internal/forwarder/jwt/signer.go).
