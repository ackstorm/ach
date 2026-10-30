# MCP servers from ACH

Hydrating an Environment writes its MCP servers into the tool's config, enabled. Each
points at `<platform URL>/mcp/<server>` and signs in with the user's ACH single sign-on
(then, for some servers, the provider's own consent screen).

## Check first, then sign in

Always list what the tool already has before adding anything. A server that is listed is
already configured, even when it says it needs authentication: only sign in to it.
Adding it again creates a duplicate under another name.

| Tool | List | Sign in |
|---|---|---|
| Claude Code | `claude mcp list` | `/mcp` inside a session, pick the server, Authenticate |
| Codex | `codex mcp list` | `codex mcp login <server>` |
| OpenCode | `opencode mcp list` | `opencode mcp auth <server>` |

Sign-in opens a browser and waits for it. When you run it for the user, start it in the
background, read the link from its log, give the link to the user, and wait for them to
say they approved; do not poll.

## A server the tool does not show

Hydrate again (`ach-cli env hydrate`), then list again. If it is still missing:

```bash
ach-cli env describe <environment>    # the MCP servers each Environment provides
ach-cli env search <server>           # which of your Environments has it
```

If none of the user's Environments provides it, it is not available to them: say so and
stop. Never invent an MCP URL. A platform administrator can add it to an Environment.

Do not add a header, a key, or anything that disables OAuth to an MCP entry hydrate
wrote, and do not hand-edit those entries: the next hydrate owns them.

## Errors

- **401 after signing in**: sign out of that server in the tool, then sign in again.
- **403**: the account lacks access to that server. Signing in again will not help; a
  platform administrator grants it through an Environment.
- **A server missing after the Environment changed**: hydrate again.
- **Remote machine (SSH, Coder) and the browser shows "127.0.0.1 refused to connect"**:
  the tool waits for the sign-in on the remote machine's localhost. Ask the user to paste
  the whole failed address from the browser bar, then deliver it on this machine with
  `curl -s -o /dev/null "<that address>"` — but only when it starts with
  `http://127.0.0.1:` or `http://localhost:`; refuse anything else. The page always says
  it succeeded, so check the tool's own list afterwards. Port forwarding the callback port
  (`ssh -L <port>:localhost:<port>`) works too.
