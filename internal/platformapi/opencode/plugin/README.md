# GenAI platform OpenCode plugin

Signs OpenCode in to a GenAI platform backend's model gateway (OAuth: browser
or device code) and, at every start, fills in the user's models, MCP servers
and skills from that backend. The user's own config always wins.

```
opencode plugin https://<origin>/clients/opencode/plugin -g
opencode auth login -p <provider>
```

`<provider>` is the value in `platform.json` (the deployment's `PROVIDER_NAME`).

Restart OpenCode to pick up backend changes.

## platform.json

Written into the tarball by the backend that serves it; never edit by hand.

| Field | Meaning |
|---|---|
| `api` | The gateway's OpenAI-compatible base URL (e.g. `https://api.example.com/v1`). OAuth discovery starts here (RFC 9728 → RFC 8414). |
| `platform` | The origin serving `/clients/*`. Config comes from `<platform>/clients/opencode/config` (schema `ackstorm.opencode-config/1`). |
| `provider` | The OpenCode provider id (the deployment's PROVIDER_NAME): `opencode auth login -p <provider>`, and the name of the plugin's data folder. |

Override per install with plugin options in `opencode.json`:
`"plugin": [["https://<origin>/clients/opencode/plugin", {"api": "...", "platform": "...", "provider": "..."}]]`.

## Files it writes (`$XDG_DATA_HOME/opencode`, default `~/.local/share/opencode`)

| File | Content |
|---|---|
| `<provider>/client.json` | The dynamic client registration (client id). |
| `<provider>/config.json` | Last good backend config (0600), used for 30 days when the backend is down. |
| `<provider>/skills/<name>/SKILL.md` | Skills delivered by the backend. |

Tokens live in OpenCode's own `auth.json`; the plugin never writes it.

## Vendoring

The source of truth is `alitellm-auth/clients/opencode`. Other products vendor a
tagged copy unchanged and ship their own `platform.json`.
