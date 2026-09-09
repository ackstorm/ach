# JWT Forwarder — `/mcp` and `/a2a` Trust Path

The ACH Forwarder authenticates a caller's ACH key (`pk_…` or `ek_…`),
then — on routes covered by a `BackendIdentityPolicy` with
`forwardIdentityJWT: true` — mints a short-lived Ed25519 JWT and
attaches it as `Authorization: Bearer <jwt>` to the upstream request.
Downstream MCP / A2A backends verify the JWT against the Forwarder's
JWKS endpoint. This is the §9 / FWD-07..10 trust path.

This page documents the contract and the operational gotchas. For
backend-side verification code, see
[Writing an MCP backend](../runbooks/writing-an-mcp-backend.md). For
the rotation procedure, see
[JWT key rotation](../runbooks/jwt-key-rotation.md).

`/v1`, `/v2`, and `/gemini` are pass-through route families with **no** JWT
involvement. `/v2` behaves exactly as `/v1`: it receives the caller's bare
`x-litellm-api-key`, with no precheck; LiteLLM is the authorization boundary.
`/mcp` and `/a2a` remain the only JWT routes.

---

## 1. End-to-end flow

```
┌────────────┐  x-ach-key: pk_…           ┌──────────────┐
│   Client   │ ─────────────────────────▶ │  Forwarder   │
│            │  (or Authorization:        │              │
│            │   Bearer pk_…)             │              │
└────────────┘                            │              │
                                          │              │
                  ┌───────────────────────┤              │
                  │  1. Authenticate key  │              │
                  │  2. Pre-check         │              │
                  │     Env+team intersect│              │
                  │  3. BIP lookup        │              │
                  │     forwardIdentityJWT│              │
                  │  4. Sign EdDSA JWT    │              │
                  │     (claims §1.2)     │              │
                  │  5. Strip + rewrite   │              │
                  │     headers           │              │
                  │  6. Attach JWT LAST   │              │
                  └───────────────────────┤              │
                                          └───────┬──────┘
                                                  │ Authorization: Bearer <jwt>
                                                  ▼
                                          ┌──────────────┐
                                          │   LiteLLM    │   ◀── MCP/A2A gateway
                                          │ (MCP gateway)│       (drops Authorization
                                          │              │        unless extra_headers
                                          │              │        opts in — §3)
                                          └──────┬───────┘
                                                 │ Authorization: Bearer <jwt>
                                                 ▼
                                          ┌──────────────┐
                                          │   Backend    │
                                          │  (your MCP   │
                                          │   server)    │
                                          │              │
                                          │ JWKS verify  │
                                          │ iss/aud/exp  │
                                          │ check        │
                                          └──────────────┘
```

The Forwarder is the only ACH service that mints JWTs. It signs with
the `current` slot of the `ach-jwt-signing-keys` Secret (Ed25519 / 32-byte
raw seed). The JWKS endpoint exposes the public half so any backend can
verify offline.

---

## 1.2 JWS contract — what the Forwarder mints

| Field         | Value                                            | Reference  |
|---------------|--------------------------------------------------|------------|
| `alg`         | `EdDSA` (Ed25519)                                | FWD-07     |
| `typ`         | `JWT`                                            | RFC 7519   |
| `kid`         | Stable id of the signing slot (e.g. `ach-jwt-<ulid>` or `dev-<ts>`) | FWD-08 |
| `iss`         | Forwarder `ACH_BASE_URL` (HTTPS-only in prod per FWD-10) | Hub §9.1 |
| `sub`         | Bare `<owner-email>` (no namespace prefix)       | Hub §9.1   |
| `email`       | Bare `<owner-email>` (mirrors `sub`); **omitted when empty** — additive, for consumers that key by email | — |
| `groups`      | Caller's LiteLLM team aliases (ACH does no UUID→alias translation — see below), deduplicated + sorted, ACH's internal shell teams stripped; **omitted entirely when empty** — additive, see below | — |
| `aud`         | `mcp:<bare-name>` on `/mcp/<bare-name>`<br>`a2a:<bare-name>` on `/a2a/<bare-name>` | Hub §9.1 |
| `iat`         | Unix seconds at mint time                        | RFC 7519   |
| `exp`         | `iat + 120`                                      | FWD-07     |
| `nbf`         | **NOT emitted** (deliberate; see note below)     | Hub §9.1   |
| `jti`         | **NOT emitted** (deliberate; Hub §9.1 + §20)     | Hub §9.1   |

Authoritative implementation: [`internal/forwarder/jwt/signer.go`](https://github.com/ackstorm/ach/blob/main/internal/forwarder/jwt/signer.go).

`sub` is the bare `<owner-email>` (no namespace prefix). The additive `email`
claim mirrors it for backends that key their per-user token storage by email and
read `email` first, falling back to `sub`. It is omitted entirely when the owner
email is empty. The legacy `<namespace>/<email>` `sub` form is **gone**
(pre-release hard-cut) — do not split `sub` on `/`; key on `iss` + `sub`.

**`nbf` is intentionally omitted.** With a 120 s `exp` it would add only
clock-skew risk for no security gain — `exp` is the sole time bound. The minted
claim set is `iss` / `sub` / `aud` / `iat` / `exp` (plus the additive `email`
and `groups`): **no `nbf`, no `jti`.** Backends MUST verify `iss` / `aud` /
`exp` only and MUST NOT require `nbf`.

The 120-second TTL is intentionally tight. There is no refresh mechanism;
every forwarded request mints a fresh JWT. Clock skew between the
Forwarder and the backend MUST stay well under 120 s for the trust path
to be steady-state.

### The `groups` claim

The Forwarder emits `groups` — the caller's LiteLLM **team aliases** — on
minted tokens whose resolved set is non-empty. Backends use it to authorize
group-owned resources.

| Key type | `groups` |
|----------|----------|
| `pk_` | the caller's LiteLLM teams ∩ `authorizedTeams` of active Environments whose `runtime.mcpServers` (or `runtime.a2aAgents`) contains `<name>` |
| `ek_` | its Environment's `authorizedTeams` |

Both are sorted, deduplicated, and stripped of ACH's internal shell teams
(`ach-env-*`, `ach-user-*`). The claim is **omitted entirely** when the set
is empty — consumers must read an absent claim as "no groups", never as an
error.

Values are whatever LiteLLM's `team_alias` resolves to (falling back to
`team_id` if the alias is empty) — ACH performs no UUID→alias translation
of its own, so an `authorizedTeams` entry authored as a raw team UUID
ships through unchanged. Renaming a LiteLLM team therefore changes the
claim; a backend keying on the old alias stops matching.

**Staleness.** There is no revocation channel: a backend trusts the claim
for the token's life. Two distinct vectors, two distinct bounds. Removing a
user from a LiteLLM team affects the `pk_` path only (the 60s
team-membership cache) and takes effect at the backend after at most
`exp` (120s) + 60s ≈ 180s. Dropping a team from
`Environment.spec.authorizedTeams` affects **both** key types — `ek_`
groups read the Environment row directly, and `pk_` groups intersect
against it — and bounds on `exp` (120s) + the `envstore` package's 5-minute
NOTIFY-loss safety-net refresh (`internal/forwarder/envstore/store.go`)
≈ ~7 minutes worst case.

---

## 1.3 JWKS endpoint

```
GET <iss>/.well-known/jwks.json
```

Response shape (RFC 7517 §5):

```json
{
  "keys": [
    {
      "kty": "OKP",
      "crv": "Ed25519",
      "use": "sig",
      "alg": "EdDSA",
      "kid": "ach-jwt-01J…",
      "x":   "<base64url-no-pad of the 32-byte public key>"
    },
    {
      "kty": "OKP",
      "crv": "Ed25519",
      "use": "sig",
      "alg": "EdDSA",
      "kid": "ach-jwt-01K…",   // OPTIONAL — only during a rotation window
      "x":   "..."
    }
  ]
}
```

Response headers:

| Header           | Value                          |
|------------------|--------------------------------|
| `Content-Type`   | `application/jwk-set+json`     |
| `Cache-Control`  | `public, max-age=3600`         |

The cache TTL is the contract for backends; a 1-hour stale view is
correct by design. During a rotation, both slots are published for
≥24 hours of overlap before the old `kid` is removed
([JWT key rotation](../runbooks/jwt-key-rotation.md)).

---

## 1.4 Public hostname forwarding + RFC 9728 discovery

Both proxy hops clear `req.Host` so the upstream `Host` header is the
internal Service name and a client-supplied `Host` never leaks
(`internal/gateway/proxy.go`, `internal/forwarder/proxy/proxy.go`). That
stays true — but on its own it left a backend behind ACH with **no way to
learn which public hostname it was reached at**, which breaks OAuth
discovery for any MCP backend fronted by more than one door (issue #177).

The gateway hop therefore publishes the public hostname in a header the
upstream can opt into:

| Header              | Set by            | Rule |
|---------------------|-------------------|------|
| `X-Forwarded-Host`  | gateway hop       | **Always overwritten** from the incoming `Host`. A client-supplied value is discarded, so it cannot be spoofed past the front door. |
| `X-Forwarded-Proto` | gateway hop       | Filled in **only when absent** (`https` iff the gateway itself terminated TLS). |

The asymmetry is deliberate: an Ingress preserves the original `Host`
end-to-end, so the gateway's `req.Host` is authoritative — but TLS
terminates at the Ingress, so the gateway only ever sees plaintext and
must trust the Ingress's `X-Forwarded-Proto` when it is present.

Neither header is stripped by `headers.StripAndRewrite`, so both survive
the forwarder hop unchanged and reach LiteLLM and the backend.

### Why a backend needs this — and when it cannot have it

RFC 9728 §3.2: a client rejects protected-resource metadata whose
`resource` is not the resource it is addressing. A backend that serves
several front doors must publish the `resource` matching the one the
client actually used — it needs `X-Forwarded-Host` to pick.

**Match against it; never echo it.** The value names a document the
client goes on to trust. Select from a configured allowlist of public
URLs (normalizing default ports) and fall back to the canonical URL on a
miss.

> **This applies only to a backend ACH proxies to DIRECTLY.** On the full
> `/mcp/{name}` path — `client → gateway → forwarder → LiteLLM → toolhive
> → MCP pod` — the header has exactly one reachable consumer, LiteLLM, and
> it never reaches the pod:
>
> - toolhive's transparent proxy calls `SetXForwarded()` unconditionally
>   and `SetURL()` rewrites `Host`, so a client-supplied `X-Forwarded-*`
>   never survives;
> - vMCP rejects `X-Forwarded-*` unconditionally
>   ([stacklok/toolhive#6523](https://github.com/stacklok/toolhive/issues/6523)),
>   and keeps that exclusion by design — those headers are smuggling and
>   spoofing vectors.
>
> A backend that cannot learn its front door should **omit**
> `resource_metadata` rather than advertise a document the client will
> reject. §1.5 is what makes that safe: ACH supplies the pointer itself.

### The metadata route

RFC 9728 §3 places the well-known segment *between the authority and the
resource path* (following RFC 8414, not RFC 8615) — so for resource
`https://ach.example.com/mcp/my-server` the document lives at
`https://ach.example.com/.well-known/oauth-protected-resource/mcp/my-server`,
a **sibling** of `/mcp/my-server`, not a child.

The forwarder mounts that subtree **outside** the Authn group, alongside
JWKS:

```go
r.Handle("/.well-known/oauth-protected-resource/*", proxy.New(hdeps.Deps))
```

Anonymous by design (RFC 9728 §5): a client fetches this document
precisely because it does not yet hold a credential — gating it on
`x-ach-key` makes the ceremony unstartable.

> `/.well-known/jwks.json` is registered explicitly and therefore
> **shadows** the one LiteLLM also serves. That is required, not
> incidental: backends point `AUTH_JWT_JWKS_URI` at ACH and must receive
> ACH's Ed25519 signing keys, never LiteLLM's.

### Required LiteLLM configuration

`/mcp/*` traffic traverses LiteLLM (§3), and LiteLLM builds the
document's `resource` / `authorization_servers` from the request base URL
(`get_request_base_url`). It honors `X-Forwarded-Host` / `-Proto` /
`-Port` **only** when `general_settings` sets **both** of these:

```yaml
general_settings:
  use_x_forwarded_for: true
  mcp_trusted_proxy_ranges: ["<ach-forwarder Pod CIDR>"]
```

With `use_x_forwarded_for` alone LiteLLM deliberately fails closed
(`litellm/proxy/auth/ip_address_utils.py`, `is_request_from_trusted_proxy`)
— it cannot distinguish a trusted reverse proxy from a direct attacker,
and these URLs become `redirect_uri`s. **Never set the range to
`0.0.0.0/0`**; scope it to the forwarder's Pod CIDR.

Measured on LiteLLM `v1.99.1`, through ACH, **with `PROXY_BASE_URL` unset**:

| LiteLLM config | `resource` returned |
|---|---|
| neither key | `http://litellm.<ns>.svc.cluster.local:4000/mcp` ❌ |
| both keys | `https://ach.example.com/mcp` ✅ |

> **⚠ `PROXY_BASE_URL` short-circuits all of the above.**
> `get_request_base_url` returns it verbatim **before** any `X-Forwarded-*`
> handling:
>
> ```python
> configured: Final = _resolve_proxy_base_url_env()
> if configured:
>     return configured          # returns BEFORE the trusted-proxy branch
> ```
>
> Most real deployments set it — the admin UI, OAuth callbacks and spend
> links need it — and on those `use_x_forwarded_for`,
> `mcp_trusted_proxy_ranges` and `X-Forwarded-Host` are **unreachable code**
> on this path. Unsetting it is not a workaround either: an Ingress behind a
> TLS-terminating L4 load balancer truthfully reports `http`, so the pointer
> becomes `http://ach.example.com/…` — a wrong scheme traded for a wrong
> host. §1.5 is what actually closes this.

This failure is **silent** — no error is logged at any hop. The client
simply refuses with `does not match expected …`, which looks identical to
the route not existing at all.

### Scope

The forwarder serves the protected-resource document only. The rest of
LiteLLM's OAuth surface —
`/.well-known/oauth-authorization-server*`,
`/.well-known/openid-configuration`, and the ceremony endpoints under
`/v1/mcp/oauth/{authorize,token,register}` — is **not** exposed
anonymously: the first two are unmounted (404) and the last sits inside
the Authn group (`401 missing_key`). Completing a full OAuth grant
through ACH would require opening that surface, which is an open design
decision, not an oversight.

Deployments that control their ingress can bypass ACH for the metadata
path entirely — an `Exact` match outranks the catch-all prefix route, and
Istio preserves the original `Host`:

```yaml
hostnames: [ach.example.com]
rules:
  - matches:
      - path: {type: Exact, value: /.well-known/oauth-protected-resource/mcp/my-server}
    backendRefs: [{name: my-server-headless, port: 8000}]
```

---

## 1.5 Auth-challenge rewrite — pointing `resource_metadata` at ACH

Header forwarding (§1.4) gets the public hostname as far as LiteLLM. On the
`/mcp` path that is not enough, for two upstream reasons measured on
`v1.99.1`:

1. **LiteLLM composes the challenge itself.** Its pre-session probe loop
   (`proxy/_experimental/mcp_server/server.py`) discards the upstream's
   `WWW-Authenticate` and synthesizes a replacement:

   ```python
   for (srv, _, challenge_server_name), (probe_status, _) in zip(probe_targets, probe_results):
       if probe_status == 401:
           www_authenticate = get_passthrough_www_authenticate(
               scope=scope, server_name=challenge_server_name, invalid_token=True,
           )
   ```

   (The `is_true_passthrough` block ~200 lines earlier does the opposite and
   relays it — an upstream inconsistency, not something ACH can configure
   around.) So even a backend that correctly selects its own `resource` from
   `X-Forwarded-Host` cannot reach the client: its header is replaced.

2. **That synthesized pointer comes from `PROXY_BASE_URL`** when set — see
   the callout in §1.4.

Together: the challenge is composed by LiteLLM, from a static env var, and
the backend's answer is discarded. **No header-propagation rule can
influence it.**

The forwarder is the last hop that knows which front door the client
dialled, and it knows its own public name without trusting any header:
`ACH_BASE_URL`, validated `http(s)://` at process start and already used as
the JWT `iss`. Consuming no header is the point — it is the one mechanism
immune to both the `PROXY_BASE_URL` precedence above and the toolhive
`X-Forwarded-*` rejection in §1.4.

Two branches, one invariant: **a challenge leaving ACH names an ACH
document, always** — whether upstream sent the wrong pointer, or none at
all. ACH guarantees this alone, with no cooperation from LiteLLM, toolhive,
or the backend, which is what makes the forwarder the right layer for it.

**Branch 1 — rewrite** (`internal/forwarder/proxy/challenge.go`), when a
pointer is present:

```
upstream:  WWW-Authenticate: Bearer error="invalid_token",
             resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/my-server"
client:    WWW-Authenticate: Bearer error="invalid_token",
             resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/my-server"
```

Only the **scheme and authority** change. The path tail names the backend
the document describes and is upstream's to choose.

**Branch 2 — insert**, when no pointer is present and the route is
`/mcp/{name}` or `/a2a/{name}`:

```
upstream:  WWW-Authenticate: Bearer error="invalid_token", scope="read"
client:    WWW-Authenticate: Bearer error="invalid_token", scope="read",
             resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/my-server"
```

Branch 2 is not hypothetical housekeeping: branch 1 alone **silently
no-ops** the day LiteLLM stops discarding the backend's challenge. ACH
would then receive a bare `Bearer error="invalid_token", …`, find nothing
to rewrite, and pass it through — leaving the client to derive the metadata
URL from the resource it dialled (§3.1). That derivation is legal and some
clients do it, but it is not something to depend on, and the failure mode
is the same silent one this issue opened with.

The inserted pointer names the document the forwarder already serves at
that path (§1.4), so both branches produce the same URL. Only `Bearer`
challenges are touched — appending an OAuth auth-param to a `Basic`
challenge would be meaningless. A 401 carrying no challenge at all gets a
minimal `Bearer` one (RFC 7235 requires a challenge on 401); a 403 does
not, so an upstream that sent none keeps none.

Deliberately narrow — the client FOLLOWS this URL, so a broader rewrite
would be a redirect-injection primitive rather than a fix:

| Condition | Behaviour |
|---|---|
| status is not 401/403 | untouched |
| no `resource_metadata`, route is not `/mcp/{name}` or `/a2a/{name}` | untouched |
| no `resource_metadata`, `Basic`-only challenge | untouched |
| no `resource_metadata`, 403 with no challenge | untouched |
| value does not parse as a URL | untouched |
| path lacks `/.well-known/oauth-protected-resource` | untouched |
| quoted-string unterminated | untouched |
| param name merely *ends* in `resource_metadata` | untouched |
| `ACH_BASE_URL` empty/unparseable | hook not installed at all |

Every other auth-param (`error`, `error_description`, `scope`, `realm`) and
all surrounding whitespace survive byte for byte — only the quoted value is
substituted.

> **`ModifyResponse` is header-only.** It was previously `nil`, documented
> as deliberate "for streaming pass-through (D-05)". The constraint that
> comment encoded is that the **body** is never touched, and it still is
> not: `ModifyResponse` runs before the body is copied and buffers nothing,
> so SSE and streamable-http pass-through are unaffected. The proxy suite's
> streaming test runs with the hook installed precisely to keep that honest.

---

## 2. BackendIdentityPolicy (BIP) — turning JWT mint on

JWT mint is **opt-in per target**. A `BackendIdentityPolicy` CR selects
a route and sets `forwardIdentityJWT: true`:

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: BackendIdentityPolicy
metadata:
  name: bip-demo-mcp-echo
  namespace: ach-system
spec:
  target:
    kind: MCPServer       # or "A2AAgent"
    name: demo-mcp-echo   # bare name; matches /mcp/<name> route
  forwardIdentityJWT: true
```

When the Forwarder handles `/mcp/<name>/*`, it looks up the BIP by
`(MCPServer, <name>)` via the in-process `bipcache` (Postgres-backed
projection refreshed by `NOTIFY ach_backend_identity_policies_changed`).
If the BIP exists and `forwardIdentityJWT = true`, the JWT is minted
and attached. Otherwise the request is forwarded **without** an
Authorization header (the `Authorization` slot is intentionally cleared
by the strip pass — clients cannot smuggle a JWT through a no-policy
route).

Symmetric behaviour applies to `/a2a/<name>/*` with `kind: A2AAgent`.

The Forwarder consumes the BIP cache directly — there is no CRD watch
on the request path. Operator → Postgres → forwarder cache is the only
flow (issue #34 C1). Status conditions on the BIP CR are informational;
the Forwarder reads from Postgres unconditionally.

### Observability — confirming a JWT was attached

On the mint path the Forwarder logs at **Info**:

```
forwarder: backend identity forwarded (JWT minted)  kind=MCPServer target=<name> aud=mcp:<name> owner=<email> request_id=...
```

When no BIP matches it logs the same fact at **Debug**
(`no backend identity policy; forwarding without JWT`) — raise the log
level to see *why* identity is not being forwarded. The corresponding
counters are `ach_forwarder_jwt_signed_total{kind}` (mint) and
`ach_forwarder_jwt_suppressed_total{kind,reason}` with
`reason ∈ {no_policy, policy_opt_out, signing_failure, list_failure}`.
A common gotcha: the BIP must live in the **same namespace as the
forwarder Pod** (`POD_NAMESPACE`) — the cache query is namespace-scoped,
so a BIP in `default` is invisible and shows up as `reason=no_policy`.

---

## 2.6 LiteLLM gateway auth — `x-litellm-api-key` (per-user, TESTING-PHASE)

Distinct from the backend-facing `Authorization` JWT (§2-§3), the Forwarder
must authenticate the proxied request **to LiteLLM itself**. As of migration
`000011` it does this with the **caller's own LiteLLM virtual key**, not the
shared master key.

- At pk_/ek_ mint, platform-api persists the `/key/generate` `sk-…` plaintext
  into the new `litellm_key_material` column (reversing FIX01 §A.6 — plaintext,
  deliberate for this testing phase).
- The resolver carries it through `KeyInfo` → `KeyContext`; the Director writes
  it as `x-litellm-api-key` — **bare** on `/v1`/`/v2`/`/a2a`, `Bearer `-prefixed on
  `/mcp` (LiteLLM's MCP key parser requires the prefix).
- **`/gemini` is the exception:** LiteLLM's native Google AI Studio passthrough
  authenticates the virtual key ONLY via `x-goog-api-key` (or `?key=`) — it does
  NOT read `x-litellm-api-key` (that is the `/v1` OpenAI-compat proxy); sending
  it yields a `Virtual Key expected … 'sk-'` 401. So on `/gemini` the Director
  sets the caller's material as **bare `x-goog-api-key`** and DROPS
  `x-litellm-api-key` so exactly one auth header is sent. Any client-supplied
  `x-goog-api-key` is stripped first (§2 strip pass — no credential smuggling).
- LiteLLM therefore attributes the request **1:1 to the user** (their own key,
  their own budget/tags), and the master key never enters the data path — so it
  can no longer leak to an MCP backend that echoes inbound headers.
- The `x-litellm-key-id` delegation header is **no longer sent** (it was only
  meaningful when authenticating as the master and delegating).
- **No fallback:** a key minted before `000011` has NULL material → the header
  is forwarded empty (`Bearer ` on `/mcp`) → LiteLLM **401s**. Re-mint the key.

The master key survives **only** for the Forwarder's `TeamsResolver` precheck
(`/user/info`, `unauthorized_team`) and for operator/platform-api admin tasks
(`/key/generate`, `/user/new`, access-group sync) — never in the proxied user
request. This is a TESTING-PHASE simplification; grep `TESTING-PHASE (reverts
FIX01` for the full revert surface.

### 2.6.1 The MCP gateway accepts the user key only on the bare path + `allow_all_keys`

Forwarding the caller's **non-admin** virtual key (§2.6) instead of the master
collides with two LiteLLM MCP-gateway guards. Both must be satisfied or the
`/mcp` request fails even though the same key works on `/v1`:

1. **Route-level (admin-only).** LiteLLM's `mcp_inference_routes` lists
   `/mcp/{subpath}` — a **single** path segment. `/mcp/<server>` matches and is
   treated as an LLM-API route (any key); `/mcp/<server>/` (trailing slash) and
   `/mcp/<server>/mcp` do **not** match, fall through to
   `_raise_admin_only_route_exception`, and 500 for anything but `proxy_admin`.
   The Forwarder Director therefore **collapses the upstream `/mcp` path to the
   bare `/mcp/<server>`** (`mcpServerPath`, `proxy.go`) — hydrate already writes
   the bare URL (`platformapi/hydrate/handler.go`), but a client (or test) that
   appends a slash/subpath is normalized back.
2. **Object-permission.** Even on the bare path, a non-admin key is rejected
   (`200` body `"User not allowed to call this tool"`) unless it is granted the
   server. The simplest grant is registering the MCP server with
   `allow_all_keys: true` (the e2e seed in `scripts/cluster.sh` does this; the
   live setup mirrors it). With it the user key reaches the upstream exactly as
   `proxy_admin` would; the per-user identity to the backend still flows via the
   `Authorization` JWT (§2-§3), unchanged.

`proxy_admin` (master) sidestepped both guards, which is why the pre-§2.6
master-key path worked on any `/mcp` URL. `/a2a` may carry the same gateway
guards; it has no full round-trip e2e yet — track as a follow-up.

---

## 3. LiteLLM intermediary — the `extra_headers` opt-in

In the production ACH topology, `/mcp/<name>/*` proxies to LiteLLM's
MCP gateway (not directly to the backend). LiteLLM then dispatches to
the actual MCP server registered under `<name>`. **By default LiteLLM
strips the caller's `Authorization` header before calling the backend**
— a sensible posture for the generic case where the gateway holds its
own credential, but it breaks the ACH JWT trust path.

To opt in to forwarding, register (or re-register) the MCP server in
LiteLLM with `extra_headers: ["authorization"]` (and `allow_all_keys: true`
so the per-user virtual key is accepted — see §2.6.1):

```bash
curl -s -X POST http://litellm.litellm-system.svc:4000/v1/mcp/server \
  -H 'Authorization: Bearer <master-key>' \
  -H 'Content-Type: application/json' \
  -d '{
    "server_name": "demo-mcp-echo",
    "transport":   "http",
    "url":         "http://ach-mcp-echo.ach-system.svc",
    "extra_headers": ["authorization"],
    "allow_all_keys": true
  }'
```

What the opt-in does:

- LiteLLM keeps the incoming `Authorization` header AS-IS and forwards
  it verbatim to the upstream MCP server.
- The backend sees `Authorization: Bearer <ach-jwt>` — i.e. the JWT the
  Forwarder minted — and can run its JWKS-based verification.

What it does **not** do:

- It is NOT a per-call header injection. Static per-server headers go
  in `static_headers` instead.
- It does NOT bypass LiteLLM's MCP gateway logic — `tools/list`,
  `tools/call`, etc. still flow through LiteLLM. The header is just
  preserved.

`scripts/cluster.sh hydrate_fixtures` already registers the e2e
`demo-mcp-echo` server with this opt-in. Operators registering their
own MCP/A2A backends MUST do the same — without it the Forwarder will
mint a perfectly valid JWT that the backend never sees, and the call
will surface as `tools = []` (LiteLLM returns an empty tool set when
the upstream `tools/list` 401s) or as `401 invalid_token` from the
backend if it does receive a half-stripped header.

> **Direct-from-Forwarder mode**: If your backend is reachable
> directly (the Forwarder skips LiteLLM and proxies straight to the
> backend), `extra_headers` is irrelevant — the Forwarder's
> Director writes Authorization on the outbound request itself. This
> mode is reserved for future use; today every `/mcp/*` request
> traverses LiteLLM.

---

## 4. Backend-side verification

Recommended posture (full detail and a runnable Go reference at
[Writing an MCP backend](../runbooks/writing-an-mcp-backend.md)):

1. **Pin the algorithm.** Reject anything other than `alg=EdDSA` on the
   JWS header. Never accept `alg=none`. Never derive the verifier from
   the token's own `alg`.
2. **Resolve `kid` against a fresh-enough JWKS.** Cache the JWK Set
   but refresh on `kid` miss; the Forwarder rotates with ≥24h overlap
   so a backend that holds a 1-hour-stale view is correct by design.
3. **Validate `iss`, `aud`, `exp`.** Pin `iss` to the Forwarder's
   `ACH_BASE_URL`; pin `aud` to the route prefix
   (`mcp:<bare-name>` or `a2a:<bare-name>`) for the route(s) this
   backend serves.
4. **Do not trust `sub` as identity** unless your security model
   matches ACH's. `sub` carries the bare `<owner-email>` for audit
   purposes — not for authorization decisions.
5. **Refuse on the slightest mismatch.** The trust path is only
   meaningful if the backend rejects on any failure. There is no
   "soft" mode.

A runnable reference implementation lives at
[`test/e2e/mcp-echo/`](https://github.com/ackstorm/ach/tree/main/test/e2e/mcp-echo).
The verifier (`jwt/verify.go`) and JWKS cache (`jwt/jwks.go`) are each
~150 lines of pure stdlib — copy-and-adapt, do not import.

---

## 5. End-to-end verification recipe

The full round-trip can be exercised on a `kind` cluster:

```bash
# 1. Bring up the cluster with the e2e mocks.
./scripts/dev.sh make cluster-up \
  HELM_EXTRA_ARGS="--set testMocks.enabled=true --set testMocks.mcpEcho.enabled=true"

# 2. Mint a pk_ via SSO (drives Dex + platform-api via the unified
#    local-gateway). See references/local-testing-gateway.md for the
#    Python helper that handles __Host- cookie HTTP-localhost workaround.
PK="$(python3 scripts/sso-mint.py)"

# 3. Apply the BIP fixture so /mcp/demo-mcp-echo gets a JWT.
./scripts/dev.sh kubectl -n ach-system apply \
  -f test/e2e/fixtures/phase4_bip_mcp_echo.yaml

# 4. Fire a tools/call through the forwarder.
curl -s -X POST http://localhost:8080/mcp/demo-mcp-echo/ \
  -H "x-ach-key: $PK" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,
       "method":"tools/call",
       "params":{"name":"demo-mcp-echo.echo",
                 "arguments":{"text":"hello"}}}'

# 5. Inspect what mcp-echo saw.
./scripts/dev.sh kubectl run cap --rm -i --restart=Never \
  --image=curlimages/curl:latest --quiet --command -- \
  curl -sS http://ach-mcp-echo.ach-system.svc/__capture/last
```

A passing run prints the echoed text plus the verified claim set
(`iss`, `sub`, `aud`, `kid`, `iat`, `exp`) inside the tool result.
`/__capture/last` shows the rewritten `Authorization` header
(`Bearer ey…` — the ACH JWT, not the original `pk_`) and the parsed
claims.

The automated suite is
[`test/e2e/phase4_jwt_validate_test.go`](https://github.com/ackstorm/ach/blob/main/test/e2e/phase4_jwt_validate_test.go);
gate it on `ACH_E2E_PHASE4=1` + `ACH_E2E_PK=<pk_…>` and run via
`make e2e-focus RUN=TestPhase4JWTValidate`.

---

## 6. Troubleshooting

See the Common failure modes section in
[CLAUDE.md](https://github.com/ackstorm/ach/blob/main/CLAUDE.md#common-failure-modes)
for the canonical "401 from `/mcp/<name>`" decision tree. The top three
causes, in order of frequency:

1. **LiteLLM gateway dropped Authorization** — missing
   `extra_headers: ["authorization"]` on the MCP server registration
   (§3 above).
2. **Backend `expectedIss` doesn't match Forwarder `ACH_BASE_URL`** —
   the `iss` claim must match exactly, character for character.
3. **Backend `expectedAud` doesn't match the route** — `aud` is
   derived from the route name (`mcp:<name>`), not from anything the
   client controls.

Forwarder logs to look for:

- `"forwarder starting" … "baseURL": "<URL>"` confirms which `iss`
  the Forwarder will mint.
- `"jwt signing keys loaded" … "current.kid": "<KID>"` confirms the
  `kid` that ends up in the JWS header.
- A `403 unauthorized_team` from the Forwarder means precheck rejected
  the request **before** JWT mint — fix the Environment's
  `authorizedTeams` or the caller's LiteLLM team membership before
  debugging the JWT path.

Backend logs to look for:

- A 401 with `WWW-Authenticate: Bearer error="invalid_token"` means
  the backend saw a JWT but rejected it. Decode the JWS payload
  (`echo "<payload>" | base64 -d`) to see which claim mismatched.
- No backend hit at all (capture empty, no log line) means the JWT
  never reached the backend — almost always the LiteLLM strip case.

---

## 7. Related references

- [JWT key rotation](../runbooks/jwt-key-rotation.md) — operational
  procedure for rotating the `ach-jwt-signing-keys` Secret with
  zero-downtime ≥24h overlap.
- [Writing an MCP backend](../runbooks/writing-an-mcp-backend.md) —
  backend-side verification posture, common pitfalls, and a runnable
  Go reference.
- [LiteLLM Custom Auth](litellm-custom-auth.md) — the parallel trust
  path for `/v1` chat completions (impersonation via
  `X-Litellm-Api-Key` + `X-Litellm-Key-Id`, NOT the JWT path).
- [`internal/forwarder/jwt/signer.go`](https://github.com/ackstorm/ach/blob/main/internal/forwarder/jwt/signer.go) —
  authoritative implementation of the signer.
- [`test/e2e/mcp-echo/`](https://github.com/ackstorm/ach/tree/main/test/e2e/mcp-echo) —
  reference backend verifier (stdlib-only).
