# OAuth client conformance — what ACH's authorization server must satisfy

Research verified 2026-09-17 against the three human-facing tools ACH hydrates
for. Each was checked from source (or the shipped binary) and from the version
installed on the dev box, not from docs alone. Gemini CLI is deprecated and was
not researched.

| Tool | Version checked | Source read |
|------|-----------------|-------------|
| Claude Code | 2.1.274 (local binary, bun-compiled bundle grepped) | code.claude.com/docs/en/mcp, binary |
| Codex CLI | 0.154.0 (local) | openai/codex `codex-rs/rmcp-client/`, learn.chatgpt.com config reference |
| opencode | 1.18.31 (local) | anomalyco/opencode `dev`, `@modelcontextprotocol/sdk` 1.29.0 |

## 1. MCP over HTTP: all three do full OAuth

Every one of them, on a `401` carrying `WWW-Authenticate: Bearer
resource_metadata="…"`, runs the complete chain:

1. fetch the RFC 9728 protected-resource document,
2. discover the authorization server from `authorization_servers[0]` via RFC 8414
   (`/.well-known/oauth-authorization-server`; opencode's SDK also probes
   `/.well-known/openid-configuration`),
3. register with RFC 7591 Dynamic Client Registration,
4. run authorization-code + PKCE S256 in a browser against a loopback redirect,
5. send `Authorization: Bearer <token>` on every subsequent request,
6. refresh with `grant_type=refresh_token`; on a rejected refresh, restart the
   browser flow.

### The access token is opaque to all three

None parses the token. None fetches a JWKS. None checks a signature. Expiry is
taken from `expires_in` in the token response, never from a decoded `exp`.

- Claude Code: zod schema `{access_token, token_type, expires_in?, scope?,
  refresh_token?, id_token?}.strip()`, stored verbatim.
- Codex: `StoredOAuthTokens` forwarded verbatim; the only JWT parser in the
  client is the separate enterprise ID-JAG path.
- opencode: SDK `OAuthTokensSchema`, `access_token: z.string()`; `expiresAt`
  computed from `expires_in`.

So an opaque `pk_` would work as an access token. ACH issues a JWT by decision
(see the credential-model decision in ach-memory), not by client constraint.

## 2. Requirements on the AS — union across the three clients

These are hard: the named client fails the flow without them.

| Requirement | Who needs it | Failure without it |
|-------------|--------------|--------------------|
| `registration_endpoint` in the RFC 8414 document | all three | Claude Code: `Incompatible auth server: does not support dynamic client registration`. opencode: status `needs_client_registration`. Codex: DCR mode fails; only a pre-registered `--oauth-client-id` works. |
| `code_challenge_methods_supported` includes `"S256"` | opencode (SDK refuses otherwise), Codex (tests assert it) | flow aborts before `/authorize` |
| Token response carries `access_token` **and** `token_type` | all three | schema validation fails; Claude Code's zod requires `token_type: string()` |
| `expires_in` in the token response | all three, soft | omitted ⇒ no local expiry; clients rely on the next `401` to refresh |
| Loopback redirect URIs with **any port** (RFC 8252 §7.3) | all three | Claude Code `http://localhost:<random>/callback`; opencode `http://127.0.0.1:19876/mcp/oauth/callback` (configurable); Codex random port or configured `callback_port` |
| Tolerate an RFC 8707 `resource` parameter on `/authorize` and `/token` | Claude Code, Codex | both send it, taken from the PRM document; rejecting the request kills the flow |
| PRM document `resource` **equals the MCP URL** (origin + path prefix) | opencode | SDK `checkResourceAllowed()` **throws** on mismatch — not a warning |
| Authorization response names the expected issuer | Claude Code | internal errors `issuer_echo_denied` / `issuer_echo_mismatch` / `issuer_response_mismatch` |
| `token_endpoint_auth_method: "none"` accepted | all three | they register as public clients; opencode uses `client_secret_post` only if a secret is configured |

Advisable, not required:

- Advertise `offline_access` in `scopes_supported`. Claude Code then appends it
  and sends `prompt=consent`, and refresh never needs a browser again.
- Accept any `client_name` at DCR. opencode registers as `"OpenCode"`; an AS that
  allowlists client names rejects it (reported against Figma's AS).

### The one that will bite: the PRM `resource` value

opencode's `checkResourceAllowed()` compares the document's `resource` against
the MCP URL the user configured. A `401` from `/mcp/<svc>/messages` that points
at a document declaring `resource: …/mcp/<svc>/messages` fails, because the
client holds `…/mcp/<svc>`. The challenge must always point at the **service
root's** document, and that document must name the service root. The
forwarder's existing challenge rewrite (`internal/forwarder/proxy/challenge.go`)
must be checked against this before the AS ships.

## 3. Model endpoint: none of them do OAuth

For the model API (not MCP), no client runs OAuth against a base URL you choose.
All three take a **command that prints a credential to stdout** instead:

| Tool | Mechanism | Re-run cadence | Notes |
|------|-----------|----------------|-------|
| Claude Code | `apiKeyHelper` in settings | 5 min default, `CLAUDE_CODE_API_KEY_HELPER_TTL_MS` | must print nothing but the credential; disables Remote Control / voice / Console analytics |
| Codex | `[model_providers.<x>].auth.command` | `auth.refresh_interval_ms`, default 300000 | mutually exclusive with `env_key` / `experimental_bearer_token`; `wire_api = "responses"` is the only value |
| opencode | `opencode auth login <url>` → fetches `<url>/.well-known/opencode` → `{"auth": {"command": [...], "env": "VAR"}}` | on `auth login` only; no auto-refresh | the same document may ship `config` / `remote_config` — the provider block, `baseURL`, `apiKey: "{env:VAR}"`, the model list |

Consequence: `ach-cli` printing a short-lived token is the model-path
credential for every tool. Hydrate writes the command into the config, not a
key.

opencode's `.well-known/opencode` is more than a hook: it lets the server ship
the provider configuration. ACH could **serve** opencode's config from the
platform instead of writing `.opencode/opencode.json`. Recorded as an option,
not a decision.

opencode also exposes an undocumented plugin `auth` hook
(`packages/plugin/src/index.ts`) with real OAuth — `authorize()` returning
`{access, refresh, expires}` and a `loader` that turns the live token into
provider options. That is the path to genuine OAuth on opencode's model
endpoint, if ever wanted.

## 4. Zero-credential MCP entries

Each tool has a config shape with no credential at all; a `401` then starts the
ceremony. This is what personal hydrate writes.

```json
// Claude Code — .mcp.json or the mcpServers block. `type` is REQUIRED with url.
{"mcpServers": {"ach": {"type": "http", "url": "https://ach.example.com/mcp/<svc>"}}}
```

```json
// opencode — OAuth is opt-OUT: any remote entry without "oauth": false gets a provider.
{"mcp": {"ach": {"type": "remote", "url": "https://ach.example.com/mcp/<svc>"}}}
```

```toml
# Codex — verified by running `codex mcp add` against a throwaway CODEX_HOME.
[mcp_servers.ach]
url = "https://ach.example.com/mcp/<svc>"
```

Two facts that govern hydrate:

- **A configured `Authorization` header defeats OAuth.** Claude Code disables
  the fallback entirely (a `401` becomes a connection error). opencode spreads
  configured headers *after* the OAuth header, silently overriding the token.
  The header must be **omitted**, not emitted empty.
- **Login is explicit in all three.** `claude mcp login <name>`,
  `codex mcp login <name>`, `opencode mcp auth <name>`. None opens a browser
  mid-session; Codex returns the tool result as an error carrying the challenge,
  Claude Code and opencode flag the server as needing auth. Credential-free
  hydrate costs one manual login per tool. Claude Code has `--no-browser` for
  SSH; `claude -p` cannot run the flow at all.

## 5. Where tokens land (for support)

- Claude Code: `~/.claude.json` under `mcpOAuth`, keyed per endpoint.
- Codex: OS keyring or `~/.codex/.credentials.json`
  (`mcp_oauth_credentials_store = keyring | file | auto`), keyed by server name + URL.
- opencode: `~/.local/share/opencode/mcp-auth.json`, mode 0600, flock-guarded,
  keyed by `serverUrl` — changing the URL forces re-auth.

## Not verified

- No live handshake was captured against a self-hosted `401`. Everything above
  is source and stored-state evidence. Before the AS ships, stand it up and
  capture the `/authorize` + `/token` requests from each tool.
- opencode carries a patch on `@modelcontextprotocol/sdk@1.29.0` whose contents
  could not be fetched; a deviation from stock SDK auth behaviour is possible.
- Whether the forwarder's existing `/.well-known/oauth-protected-resource/*`
  handling satisfies Claude Code's strict issuer echo.
