# Sign-in, identity and per-user limits

## Contents

- How login works
- The Dex client for ACH
- OAuth redirect hosts
- Admins
- Login team enrolment (default / users / sso)
- User defaults: budget and key limit
- Trusted external IdP (forwarder)
- JWT signing keys and rotation
- Token lifetimes

## How login works

ACH runs its own OAuth 2.1 authorization server inside platform-api
(`/platform/oauth/*`). Every human entry point uses it: `ach-cli login`
(browser loopback or the RFC 8628 device code), OpenCode, Claude Code / Codex
MCP sign-in, and the web console. Platform-api sends the browser to Dex, Dex
to the real IdP, and back to `https://<ACH_BASE_URL>/platform/oauth/as-callback`.

On each interactive login ACH provisions the user in LiteLLM, adds them to the
configured teams, and seeds their budget. Access tokens are 1-hour Ed25519
JWTs; every refresh goes back to Dex with the user's Dex refresh token, so a
user disabled at the IdP loses access at the next refresh. No API key is
minted at login.

Agents and CI do not log in: they use environment keys (`ek-`) that a person
creates with `ach-cli keys create`.

## The Dex client for ACH

platform-api reads three variables, set through `extraEnv`:

```yaml
extraEnv:
  - name: ACH_DEX_ISSUER_URL      # must equal Dex's `issuer`, byte for byte
    value: https://auth.example.com
  - name: ACH_DEX_CLIENT_ID
    value: ach
  - name: ACH_DEX_CLIENT_SECRET
    valueFrom:
      secretKeyRef: {name: ach-dex-client-secret, key: ACH_DEX_CLIENT_SECRET}
```

The matching static client in the Dex configuration:

```yaml
staticClients:
  - id: ach
    name: ACH
    secretEnv: ACH_DEX_CLIENT_SECRET      # or `secret:`; keep it out of git
    redirectURIs:
      - https://ach.example.com/platform/oauth/as-callback
```

ACH requests the scopes `openid email profile offline_access groups`. What Dex
must provide:

- **Refresh tokens** (`offline_access`). A connector that cannot issue them
  makes every login fail at the callback with a clear error.
- **`email`**: users are keyed by email everywhere (teams, budgets, keys).
- **`groups`** only if `platformApi.teams.sso` is used; the upstream connector
  has to emit groups (Google needs its groups configuration, Entra ID the
  groups claim).
- Dex's `issuer` reachable from the platform-api pods: OIDC discovery runs at
  startup and a failure stops the pod.

Dex keeps ONE refresh token per user and client, and ACH shares it across all
of a user's tools. Leave Dex's refresh-token reuse interval at its default or
longer, and size `refreshTokens.validIfNotUsedFor` knowing it caps how long an
idle user stays signed in.

## OAuth redirect hosts

```yaml
platformApi:
  oauth:
    redirectHosts: []          # e.g. [claude.ai] for claude.ai's web MCP connector
```

Client registration is anonymous (RFC 7591). By default only loopback
redirects (`http://127.0.0.1`, `localhost` — ach-cli, OpenCode, Claude Code,
Codex) are accepted. An `https` redirect URI is accepted only when its host is
listed here, at registration AND at every `/authorize`, so removing a host cuts
existing clients off immediately. List only hosts you trust to receive your
users' authorization codes.

## Admins

```yaml
platformApi:
  admins:
    - alice@example.com
    - bob@example.com
```

Rendered into the ConfigMap `ach-platform-api-admins`, read at startup. Empty
means every admin route answers 403 `not_admin` (safe default). Admins can list
every Environment and key, set any user's budget and key limit, and revoke
other people's keys. The file is read once at startup and the pod template does
not change with it, so after editing the list run
`kubectl -n ach rollout restart deploy/ach-platform-api`. Matching is
case-sensitive: write each email exactly as the IdP sends it.

## Login team enrolment

```yaml
platformApi:
  teams:
    default: [default]               # mandatory for everyone
    users:                           # email -> extra teams
      alice@example.com: [data-team]
    sso:                             # Dex `groups` value -> teams
      platform-engineers@example.com: [platform]
```

At every interactive login (not on token refresh) ACH adds the USER to these
LiteLLM teams, by team alias. LiteLLM is the source of truth: ACH never creates
these teams, except that the operator bootstraps the alias `default` itself.

- `default` teams are mandatory: at least one (an empty list refuses to start),
  and a missing one fails the login (`default_team_missing`). Right after a
  fresh install the first login can hit this before the operator has created
  `default`; it heals on the next attempt.
- `users` and `sso` are best-effort and additive: a team that does not exist
  yet is skipped (log `login: team does not exist`) and retried at the next
  login; LiteLLM errors never block a login.
- Removing a mapping never detaches anyone; remove the user from the LiteLLM
  team by hand.
- Keys match case-insensitively.
- Access to an Environment follows at that Environment's next reconcile (its
  `authorizedTeams` lists team aliases).

## User defaults: budget and key limit

```yaml
platformApi:
  userDefaults:
    maxBudget: 100         # USD (LiteLLM's unit); "" = uncapped; 0 = refuse everything
    budgetDuration: "30d"  # reset window; "" = never resets
    maxKeys: 5             # non-revoked ek- keys a person may hold; 0 = none
```

**Budget.** One ceiling per person covers their personal sessions and every
`ek-` key they own, across all Environments; LiteLLM enforces it on the
`user:<email>` tag ACH stamps on every forwarded request. The chart value is a
starting value: it is seeded only while the user's tag has no budget. So
changing it does not retune anyone already seeded, and a login never overwrites
a ceiling an admin set. Retune one person:

```bash
ach-cli admin users budget alice@example.com --max-budget 200 --budget-duration 30d
```

Environment-wide and per-key ceilings exist too (`Environment.spec.budget`,
`ach-cli keys budget`); they are not chart values.

**Key limit.** `maxKeys` is read fresh on each `ek-` key creation and applies
to anyone without a personal limit; admins are not exempt. Grant one person a
different allowance (takes effect on the next create):

```bash
ach-cli admin users limits alice@example.com --max-keys 10
```

## Trusted external IdP (forwarder)

For a chat front end (e.g. LibreChat) that signs users in with the same Dex and
sends the user's Dex access token straight to ACH's `/v1`:

```yaml
forwarder:
  trustedIdP:
    issuer: https://auth.example.com   # must equal the token's `iss` byte for byte
    audiences: [chat]                  # the Dex client id(s) of that front end
    claim: email
```

The forwarder verifies the token (RS256 against the issuer's JWKS, `exp`,
`aud`, `email_verified` not false) and maps it to the user's EXISTING ACH
login. A user who never signed in to ACH gets 403 `ach_login_required` until
they open the console once; an unreachable issuer is 503 `idp_unreachable`.
Empty `issuer` = off; an issuer without `audiences` refuses to start.

## JWT signing keys and rotation

`ach-jwt-signing-keys` (name from `forwarder.jwtSecretName`) holds the Ed25519
seed that signs ACH's OAuth access tokens and the per-request identity JWTs the
forwarder mints for MCP/A2A backends. The operator creates it on first boot
(random seed, created once, kept across `helm uninstall`). Consumers:

- forwarder: watches it through the API; refuses to start until it exists.
- platform-api and content-service: mount it as files, re-read every 30 s.

A fresh install therefore shows a few restarts of forwarder and content-service
until the operator is up. To pin a seed yourself, create it before installing:

```bash
d=$(mktemp -d)
openssl rand 32 > "$d/current.seed"
printf 'ach-jwt-%s' "$(date +%s)" > "$d/current.kid"
kubectl -n ach create secret generic ach-jwt-signing-keys \
  --from-file=current.kid="$d/current.kid" --from-file=current.seed="$d/current.seed"
rm -rf "$d"
```

**Planned rotation** (backends cache the JWKS for up to an hour, so overlap
24 hours):

1. Generate a new 32-byte seed and kid; add them as `next.kid` / `next.seed`
   (`kubectl patch secret ... --type=json` with `op: add`).
2. Check `curl -s https://ach.example.com/.well-known/jwks.json | jq '.keys|length'`
   answers `2`.
3. Wait at least 24 hours.
4. Replace `current.*` with the new values and set `next.kid` / `next.seed`
   to empty strings.
5. Check the JWKS shows one key, the new kid.

**Compromised key**: replace `current.*` directly and accept up to an hour of
backends still trusting the old key. Deleting the Secret also works (the
operator re-mints on its next start) but signs every user out and breaks every
backend until it refetches the JWKS.

## Token lifetimes

`ACH_OAUTH_ACCESS_TTL` (default `1h`, minimum `1m`) and `ACH_OAUTH_REFRESH_TTL`
(default `720h`, minimum `1h`) through `extraEnv`. The access TTL is also how
often the console re-validates a session at Dex. Shorter TTLs revoke disabled
users sooner and cost more Dex round-trips.
