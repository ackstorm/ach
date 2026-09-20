# Signing in from Claude Code, Codex and opencode

ACH is an OAuth 2.1 authorization server for humans. You sign in once through
your organisation's SSO (Dex) and hold a short-lived access token (1 h,
refreshed for 30 days) that works for **model calls** and **MCP servers**
through ACH. Agents and CI keep the persistent `pk_` / `ek_` keys; this page
is for a person at a keyboard.

```bash
ach-cli login                 # browser → SSO → "Signed in to ACH"
ach-cli token                 # prints ONE line: a fresh access token
ach-cli env hydrate <env>     # writes the tools' configs (no credential inside — see below)
```

On a terminal `login` asks how to finish: **1)** the browser on this
machine, or **2)** another device — it prints a URL and a code like
`BCDF-GHJK`; open the URL in any browser, confirm the code matches, sign
in, and the CLI picks it up (RFC 8628 device grant). Option 2 is the way in
from an SSH host; `--no-browser` selects it without the menu. Both leave
the same token pair on the profile.

## MCP servers: the tool signs in itself

Hydrate writes MCP entries with **no credential**. The tool's first request
gets a `401` with an RFC 9728 pointer and the tool runs the OAuth ceremony
against ACH on its own — one browser round-trip per tool, then it refreshes
by itself. The entries hydrate writes are exactly these:

```json
// Claude Code — .mcp.json. `type` is REQUIRED with url.
{"mcpServers": {"ach": {"type": "http", "url": "https://ach.example.com/mcp/<svc>"}}}
```

```json
// opencode — opencode.json. OAuth is opt-OUT: a remote entry without "oauth": false gets a provider.
{"mcp": {"ach": {"type": "remote", "url": "https://ach.example.com/mcp/<svc>"}}}
```

```toml
# Codex — config.toml
[mcp_servers.ach]
url = "https://ach.example.com/mcp/<svc>"
```

Login is explicit in all three — none opens a browser mid-session:

| Tool | Command | Then |
|------|---------|------|
| Claude Code | `claude mcp login <name>` (`--no-browser` for SSH) | `/mcp` shows the server connected |
| Codex | `codex mcp login <name>` | tools list |
| opencode | `opencode mcp auth <name>` | tools list |

Do **not** add an `Authorization` header (or an empty `x-ach-key`) to these
entries: Claude Code disables its OAuth fallback entirely when a credential
header is configured, and opencode's configured headers silently override the
token it obtained. `x-ach-environment` is informational and fine.

Some MCP servers are **brokered**: ACH names no account of its own, so it
delegates that one service's consent to the service's own broker. For a
brokered service the browser makes one extra stop — at that service's broker,
and the provider behind it — before returning to the tool; that stop is where
the provider consent actually lives. You do not configure anything
differently for this; the extra hop happens inside the same login command
above.

## Model endpoint: a credential helper

No tool runs OAuth against a model base URL. Each takes a **command that
prints a credential to stdout**; `ach-cli token` is that command. `ach-cli
env hydrate` writes the wiring for you when you are signed in as a person
(OAuth or a `pk_` — never for an `ek_`); this is what it writes, for the
case you set it up by hand:

```json
// Claude Code — settings.json. Pair with ANTHROPIC_BASE_URL=https://ach.example.com
{"apiKeyHelper": "ach-cli token"}
```

```toml
# Codex — config.toml
[model_providers.ach]
name = "ACH"
base_url = "https://ach.example.com/v1"
wire_api = "responses"
[model_providers.ach.auth]
command = "ach-cli token"
refresh_interval_ms = 300000
```

```bash
# opencode — the provider comes from ACH's opencode document (optional adapter, not yet served);
# until then set the provider block by hand with apiKey: "{env:ACH_TOKEN}" and export ACH_TOKEN="$(ach-cli token)".
```

`ach-cli token` prints **only** the credential (Claude Code's helper fails on
any extra output); every diagnostic goes to stderr. On an OAuth profile it
refreshes automatically when the stored token is within 6 minutes of expiry,
under a file lock so Claude Code and Codex firing it together spend one
refresh, not two; on a `pk_` profile it prints the `pk_`.

## What a token can reach

Nothing changes in authorization: the token stands for you, exactly as a
`pk_` does. What you can reach is what your Environments' `authorizedTeams`
grant — `/mcp/<name>` is still gated by the forwarder's precheck, `/v1` by
LiteLLM's team/access-group model. Revoking your OAuth key from the admin
key list (`ach-cli admin keys`, purpose `oauth`) kills the session within a
minute; the next `ach-cli token` after that mints a fresh one.

## When it goes wrong

- **401 with `WWW-Authenticate` on every request** — the tool never ran
  login: `claude mcp login` / `codex mcp login` / `opencode mcp auth`.
- **`invalid_grant` on refresh** — the 30-day refresh expired or Redis was
  flushed: `ach-cli login` again.
- **OAuth works, `/mcp/<name>` is 403** — precheck, same as a `pk_`: the
  Environment's `authorizedTeams` do not include one of yours.
- **Connected, zero tools** — the backend has no provider grant for you; run
  the tool's MCP login again (for example, `claude mcp login <name>`).
- **The backend did not accept the consent** — the broker stored the grant
  under another account or rejected the hint; its owner should check
  `AUTH_BROKER_HINT_ISSUER` and the BIP's `consentAudience`.
- **"this browser did not start the authorization request"** — the sign-in
  must finish in the browser that opened it: ACH sets a cookie when the
  tool sends you to `/platform/oauth/authorize` and checks it when the
  identity provider sends you back. Copying the login URL into another
  browser or profile, or blocking cookies for the Hub's origin, fails here
  by design (it is what stops someone else's link from signing you into
  their client). Start the login again from the tool.
