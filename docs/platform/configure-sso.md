# Configure sign-in

Every person signs in to ACH through Dex: from `ach-cli login`, the web
console, OpenCode, and the MCP sign-in of Claude Code and Codex. This page sets
up the Dex client, and decides who is an admin, which teams a user joins, and
what budget and key limit a new user gets.

You need Dex running with a connector to your identity provider; see the
[prerequisites](prerequisites.md#dex). The values on this page go in the
`values.yaml` of the [install](install-the-chart.md), and take effect with
`helm upgrade`.

## The Dex client

Register a client for ACH in the Dex configuration:

```yaml
staticClients:
  - id: ach
    name: ACH
    secretEnv: ACH_DEX_CLIENT_SECRET
    redirectURIs:
      - https://ach.example.com/platform/oauth/as-callback
```

Give ACH the same client through [`extraEnv`](../reference/helm-values.md#extraenv):

```yaml
extraEnv:
  - name: ACH_DEX_ISSUER_URL
    value: https://auth.example.com
  - name: ACH_DEX_CLIENT_ID
    value: ach
  - name: ACH_DEX_CLIENT_SECRET
    valueFrom:
      secretKeyRef: {name: ach-dex-client-secret, key: ACH_DEX_CLIENT_SECRET}
```

`ACH_DEX_ISSUER_URL` must equal the `issuer` of the Dex configuration
character for character.

ACH asks Dex for the scopes `openid email profile offline_access groups`. Dex
must provide:

| From Dex | Why |
|---|---|
| A refresh token (`offline_access`) | ACH renews each session with it. Without one, sign-in fails |
| `email` | ACH identifies a user by email in teams, budgets and keys |
| `groups` | Only if you map groups to admins or teams below. The connector to your identity provider must send them |

Dex keeps one refresh token per user and client, and ACH shares it across all
the tools of a user. `refreshTokens.validIfNotUsedFor` in the Dex
configuration therefore limits how long an idle user stays signed in.

## OAuth redirect hosts

Tools on the user's machine (`ach-cli`, OpenCode, Claude Code, Codex) receive
the sign-in on a local address, which ACH always accepts. A web application
that signs users in to ACH, such as the claude.ai MCP connector, needs its
host listed:

```yaml
platformApi:
  oauth:
    redirectHosts: [claude.ai]
```

Any application can register itself as a client, so list only hosts you trust
to receive your users' sign-in. Removing a host cuts its clients off at once.

## Admins

Admins can see every Environment and key, set any user's budget and key limit,
and revoke other people's keys. Nobody is an admin until you name one:

```yaml
platformApi:
  admins:
    users:                         # emails
      - alice@example.com
    sso:                           # groups from your identity provider
      - platform-admins@example.com
```

| List | Matching | A change takes effect |
|---|---|---|
| `users` | Exact, including upper and lower case: write the email as your identity provider sends it | At the `helm upgrade`, which restarts platform-api. Sessions survive |
| `sso` | Ignores case | At the user's next sign-in, or within an hour for a user who is signed in |

## Team enrolment at login

An Environment names the teams that may use it. ACH adds each user to teams
when they sign in:

```yaml
platformApi:
  teams:
    default: [default]               # every user
    users:                           # email: extra teams
      alice@example.com: [data-team]
    sso:                             # identity-provider group: teams
      platform-engineers@example.com: [platform]
```

Teams live in LiteLLM and are named by their alias. ACH creates the team
`default` itself and no other: create the rest in LiteLLM.

- **`default`** needs at least one team. If a team in it does not exist, the
  sign-in fails with `default_team_missing`.
- **`users` and `sso`** add teams and never block a sign-in: a team that does
  not exist yet is skipped and tried again at the next sign-in.
- A user who is already signed in picks up new `users` and `sso` teams with
  `ach-cli refresh`.
- Removing a line here removes nobody from a team. Remove the user from the
  team in LiteLLM.
- A user reaches an Environment a few minutes after joining its team.

## Default budget and key limit

```yaml
platformApi:
  userDefaults:
    maxBudget: 100         # USD; "" = no ceiling; 0 = refuse every request
    budgetDuration: "30d"  # the budget resets after this; "" = never
    maxKeys: 5             # environment keys one user may hold; 0 = none
```

**Budget.** One ceiling per person covers their own sessions and every
environment key they own. `maxBudget` is given to a user at their first
sign-in. Changing it later does not change the ceiling of users who already
have one. Change one person's ceiling with:

```bash
ach-cli admin users budget alice@example.com --max-budget 200 --budget-duration 30d
```

**Key limit.** `maxKeys` applies to every user without a limit of their own,
admins included, and a change applies to the next key anyone creates. Give one
person a different limit with:

```bash
ach-cli admin users limits alice@example.com --max-keys 10
```

## Token lifetimes

Both are set through `extraEnv`:

| Variable | Default | Minimum |
|---|---|---|
| `ACH_OAUTH_ACCESS_TTL` | `1h` | `1m` |
| `ACH_OAUTH_REFRESH_TTL` | `720h` | `1h` |

ACH checks a session against Dex each time its access token is renewed. A
shorter `ACH_OAUTH_ACCESS_TTL` ends the session of a user removed from your
identity provider sooner, and asks Dex more often.

A session that ends does not end the user's environment keys. When someone
leaves, also run:

```bash
ach-cli admin keys revoke --owner alice@example.com
```

## Verify

```bash
ach-cli login https://ach.example.com --profile default
ach-cli whoami                    # your email, your budget, your key limit
ach-cli admin list teams          # works only for an admin
```

A user who is not an admin gets `not_admin` from the last command.

## Related

- [`platformApi`](../reference/helm-values.md#platformapi) in the Helm values
  reference.
- [`forwarder.trustedIdP`](../reference/helm-values.md#forwarder), for a chat
  front end that signs users in with the same Dex.
- [Rotate the JWT signing keys](rotate-jwt-signing-keys.md)
- [Troubleshoot an ACH install](troubleshoot.md#sign-in-failures)
