# OAuth broker chain — design

**Date:** 2026-09-18 · **Target release:** v0.10.0 · **Depends on:** v0.9.1 (OAuth front door + browser-binding cookie)

## Problem

A user signing in to an MCP service behind a broker (mcp-oauth) reaches the
provider's consent screen, and the broker must then store the grant under
the user's identity. Providers that name no account (Zoho) — or an account
whose email is not the Dex one (some GitLab/Slack) — leave the broker with
nothing to key on: `This session belongs to '__broker_pending__', but you
authorized as 'an account the provider did not name'`.

The fix agreed with the mcp-oauth / alitellm-auth team: the authorization
server that already knows the user (ACH's, right after the Dex leg) chains
the browser through each broker and names the grant owner with a signed
`login_hint`. mcp-oauth (≥ a227d3f) verifies the hint against ACH's issuer
+ JWKS (EdDSA accepted) and keys the grant by `sub`. ACH v0.9.1's AS does
Dex → token only; it ignores `scope` and never visits a broker.

The immediate production symptom is unrelated to ACH's AS: 23 HTTPRoutes
on `ach.*` send the per-service PRM to the pod's own broker, so clients
skip ACH entirely. Those routes are deleted the day ACH serves the
per-service PRM (this design).

## Goals

- `/authorize` chains through the broker of every requested MCP service the
  user has not yet granted, sending `login_hint`.
- The ACH access token carries `scope` = `ach` + the granted services; the
  forwarder gates `/mcp/<svc>` on it for OAuth bearers.
- ACH serves the per-service PRM with `scopes_supported`, so clients ask
  for the right scope on first use and re-authorize on `insufficient_scope`.
- Behaviour byte-compatible with alitellm-auth v0.8.3 (`_chain_next`,
  `broker_callback`, `Grants`) so the 23 brokers need no ACH-specific code.

## Non-goals

- Redeeming the broker's code (the grant projection is the truth).
- Any change to `pk_`/`ek_` bearers, the device-code flow, hydrate, or the
  `ach-cli` (its login keeps `scope=offline_access`; MCP scopes are for the
  tools' own OAuth clients).
- Discovering brokers from LiteLLM. The service map is static config.
- Per-user revocation of broker grants (owned by the broker).

## Configuration

**Service map** — one Helm value, rendered as JSON into BOTH platform-api
and forwarder as `ACH_OAUTH_SERVICES`. Same shape as alitellm-auth
`authServer.services`; gitops renders the same 23 entries for both charts.

```yaml
oauth:
  audience: ach
  services: {}
  #  mcp-zoho-desk-ro: { store: zoho-desk-ro, broker: https://api.ackstorm.ai/zoho-desk-ro-callback }
  grantsRedis:
    secretRef: { name: "", key: url }   # redis://:<pw>@host:6379/0 — the brokers' projection
```

`ACH_OAUTH_SERVICES` unset/empty ⇒ the whole feature is dormant: no chain,
no `scope` gate, PRMs unchanged. `ACH_OAUTH_GRANTS_REDIS_URL` (platform-api
only) unset with a non-empty map ⇒ refuse to start (a map without a
projection would re-prompt every login). The Redis user should be the ACL
user limited to `+get ~oauth:*:state:*` the mcp team offered; ACH never
writes there.

Keys of the map are the `/mcp/<name>` path segment (the client-facing
scope); `store` is what the broker calls the service (its `scope`/`aud`);
`broker` is the broker's OAuth AS base (has `/register`, `/authorize`).

## Components

### `internal/platformapi/auth` — the chain (platform-api)

New file `oauth_chain.go`; `oauth_authorize.go` gains the scope check and
the hand-off. Store kinds added to `OAuthStore`: `brokerclient`
(`<broker> → client_id`, TTL 30 d), `chain` (TTL 10 min).

`OAuthDeps` gains `Services map[string]Service` (`{Store, Broker string}`)
and `Grants GrantReader` (`Granted(ctx, email, store) (bool, error)`; the
production reader is a Redis `GET`). No broker seam: unit tests stand up a
real `httptest.Server` broker and point `Services[*].Broker` at it.

**`/authorize`**: after the existing checks, parse `scope` (space-separated).
Every value must be the audience (`ach`), `offline_access`, or a key of
`Services`; anything else ⇒ `invalid_scope` redirect to the client (same
shape as the existing `invalid_request` one). The requested services are
stored on `oauthPending.Scopes`.

**`/as-callback`** (existing): after `provision`, compute
`todo = [s for s in pending.Scopes if !Grants.Granted(email, Services[s].Store)]`.
Empty ⇒ `finish` (today's code-mint path). Otherwise `chainNext`.

**`chainNext(pending, todo)`**:
1. `clientID = brokerClientID(broker)`: `POST <broker>/register` with
   `{client_name: "ACH", redirect_uris: [<issuer>/platform/oauth/broker-callback], token_endpoint_auth_method: "none"}`
   once per broker, cached in `brokerclient`. Any error ⇒ log, drop this
   service from `todo`, continue (alitellm-auth `_skip_and_continue`).
2. `chainID = NewSessionID()`; store kind `chain` = pending + `Todo` + the
   PKCE verifier (unused; kept for a future revision that redeems).
3. 302 to `<broker>/authorize?response_type=code&client_id=…&redirect_uri=<issuer>/platform/oauth/broker-callback&scope=<store>&state=<chainID>&code_challenge=S256(v)&code_challenge_method=S256&login_hint=<hint>`.
4. `hint` = `Signer.Sign(Claims{Iss: issuer, Sub: email, Aud: store, TTL: 600s})`
   — the existing Ed25519 signer; no `email`/`groups`/`scope` claims.

**`/broker-callback`** (new route, anonymous like the rest): pop `chain` by
`state`; missing ⇒ 400 HTML. Verify the v0.9.1 **browser-binding cookie**
for the chain's `pendingID` (the chain record carries it; the cookie set at
`/authorize` is still in the browser, `Max-Age` 10 min). Mismatch ⇒ 400,
chain burned. `error` param ⇒ log at info, treat as skipped. The `code` is
ignored. Then `skipAndContinue`: next `todo` ⇒ `chainNext`, else `finish`.

**`finish`**: today's code-mint, plus `oauthCode.Scopes` = the requested
services (not the granted ones — granted is recomputed at `/token`). The
binding cookie is cleared here, not at `/as-callback` any more (the cookie
must survive the chain). On the `todo`-empty path `/as-callback` still
clears it via `finish`.

### `/token` — `scope` on the token

`issue()` computes `scope = [audience] + [s for s in requested if Granted(email, Services[s].Store)]`
for BOTH grants: `authorization_code` uses `oauthCode.Scopes`; the refresh
record gains `Scopes` (the originally requested services) so a refresh
re-reads the projection — a grant revoked at the broker drops off the next
token. `Claims.Scope` (space-separated string) is added to the access JWT;
the token response gains `"scope"`. Grants Redis unreachable at `/token` ⇒
`503 temporarily_unavailable` (never mint a wider token than the
projection allows, never silently narrower).

### `internal/forwarder/jwt`

- `Claims.Scope string` (omitted when empty).
- `Ed25519Signer.Verify` returns the verified claims (`sub`, `scope`) — the
  `keystore.JWTVerifier` interface changes from `(sub string, err)` to
  `(*VerifiedClaims, error)`; `NoJWT` follows.
- `ASMetadata(issuer, services)`: `scopes_supported` = `["offline_access", audience, ...service keys]`.

### `internal/keystore`

`KeyInfo` gains `OAuth bool` and `Scopes []string` (both set only by the
OAuth resolver; `pk_`/`ek_` rows leave them zero). Cached by the existing
peppered-JWT-hash key, so a token's scopes are stable for its lifetime.
`middleware.KeyContext` mirrors both fields.

### `internal/forwarder/proxy` — PRM + gate

- `WellKnownHandler(base, services)`: for `/mcp/<svc>` with `<svc>` in the
  map, add `"scopes_supported": ["<audience>", "<svc>"]`. `/a2a/*` and
  unknown services unchanged.
- `/mcp/<svc>` handler, before precheck: if `kc.OAuth && svc ∈ services && !contains(kc.Scopes, svc)`
  ⇒ `403` JSON `{"error":"insufficient_scope"}` with
  `WWW-Authenticate: Bearer error="insufficient_scope", resource_metadata="<base>/.well-known/oauth-protected-resource/mcp/<svc>"`.
  Metric outcome `insufficient_scope`. Everything else (pk_/ek_, unmapped
  services, `/a2a`) falls through untouched.

### Helm

`oauth.services` → `ACH_OAUTH_SERVICES` (JSON via `toJson`) on the
platform-api and forwarder Deployments; `oauth.grantsRedis.secretRef` →
`ACH_OAUTH_GRANTS_REDIS_URL` on platform-api. `helm template` must render
identically to today when both are empty.

## Data flow (one Zoho login, nothing granted yet)

```
opencode → GET /.well-known/oauth-protected-resource/mcp/mcp-zoho-desk-ro   (forwarder: AS=ACH, scopes ach mcp-zoho-desk-ro)
opencode → /platform/oauth/authorize?scope=ach mcp-zoho-desk-ro…            (pending{Scopes}, binding cookie)
        → Dex → /as-callback   email=u@x  Granted(u@x, zoho-desk-ro)=false
        → chainNext: register@broker (cached) → 302 broker/authorize?…&login_hint=<EdDSA JWT sub=u@x aud=zoho-desk-ro>
        → broker verifies hint via ACH jwks_uri → Zoho consent → broker writes oauth:zoho-desk-ro:state:u@x {granted:true}
        → 302 /platform/oauth/broker-callback?code=…&state=<chainID>        (cookie ok, code ignored)
        → finish: 302 opencode?code=<achcode>
opencode → /token  → Granted(u@x, zoho-desk-ro)=true → JWT scope="ach mcp-zoho-desk-ro"
opencode → /mcp/mcp-zoho-desk-ro  (forwarder: scope ok → precheck → per-target JWT sub=u@x → pod finds the grant)
```

## Error handling

| Condition | Behaviour |
|---|---|
| Unknown scope on `/authorize` | `invalid_scope` redirect to the client |
| Broker `/register` or unreachable | log warn, skip that service, continue chain; token simply lacks it |
| User declines at provider (`error` on broker-callback) | log info, skip, continue |
| Browser-binding cookie missing on `/broker-callback` | 400 HTML, chain burned |
| Grants Redis down at `/as-callback` | treat every requested service as ungranted (chain runs; harmless re-consent) |
| Grants Redis down at `/token` | 503 `temporarily_unavailable` |
| `ACH_OAUTH_SERVICES` set, Redis URL unset | platform-api refuses to start |
| OAuth JWT without the service scope on `/mcp/<svc>` | 403 `insufficient_scope` + PRM pointer |

## Security notes

- `login_hint` is signed by the same Ed25519 slot as access tokens, but
  `aud` = store name, so it verifies at exactly one broker and nowhere in
  ACH (ACH verifiers require `aud = ach`). 600 s lifetime.
- The chain inherits the v0.9.1 browser binding end to end; an attacker
  cannot splice a victim's broker consent into their own chain.
- The broker code is never redeemed, so ACH holds no provider credentials.
- Scope is enforced only for OAuth bearers. `pk_`/`ek_` semantics are
  unchanged (the key's team/access-group is their ceiling).

## Testing

**Unit (`internal/platformapi/auth`)**: `httptest.Server` broker with
`/register` + `/authorize` (records `login_hint`, verifies it with the test
signer: `iss`, `sub`, `aud`, `exp ≤ 600 s`), fake `Grants` map. Cases:
two services, one granted ⇒ exactly one hop; broker down ⇒ skipped, token
still minted; decline ⇒ skipped; binding cookie missing on
`/broker-callback` ⇒ 400; `invalid_scope`; `/token` and refresh recompute
scope from the fake projection; hint has no ACH audience.

**Unit (`forwarder`)**: PRM with/without map; `/mcp/<svc>` 403 for an OAuth
`KeyContext` lacking the scope, pass with it, pass for `pk_` without it,
pass for an unmapped service.

**e2e**: new fixture `test/e2e/mock/broker` (Go, same image family as
mcp-echo): `/register` (any client), `/authorize` verifies `login_hint`
against `http://ach.e2e.local:8080/.well-known/jwks.json`, writes
`oauth:<store>:state:<sub>` `{"granted":true}` to the e2e Redis, 302s to
`redirect_uri?code=x&state=…`. Chart values in `02-ach/ach.values.yaml`:
`oauth.services: { demo-mcp-echo: {store: echo, broker: http://mock-broker.ach-system.svc:8080} }`
and the grants Redis = the e2e ach-redis. `TestOAuthFrontDoor` gains:
PRM for `/mcp/demo-mcp-echo` lists `scopes_supported`; authorize with
`scope=ach demo-mcp-echo` walks one broker hop (`followToLoopback` already
carries cookies); the token's `scope` contains `demo-mcp-echo`; `/mcp/demo-mcp-echo`
answers 200 with it; a second ceremony without the scope gets 403
`insufficient_scope` on `/mcp/demo-mcp-echo` and 200 on `/v1/models`.

## Rollout

1. Ship v0.10.0. Chart default: `oauth.services: {}` (dormant).
2. Gitops: set `oauth.services` (23 entries) + `grantsRedis.secretRef` on
   ach; add `https://ach.<domain>` to `AUTH_BROKER_HINT_ISSUER` on the 23
   MCPServers.
3. Delete the 23 `<svc>-ach-resource-metadata` HTTPRoutes and
   `AUTH_RESOURCE_URLS`. From then on clients discover ACH as the AS.

Docs in the same change: CLAUDE.md service-table rows (platform-api,
forwarder), `docs/developer-guide/jwt-forwarder.md` (scope gate + PRM),
`docs/user-guide/signing-in-from-tools.md` (what the extra browser hop is),
`references/troubleshooting.md` (`insufficient_scope`, chain skipped).
