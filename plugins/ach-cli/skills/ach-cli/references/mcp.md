# MCP tools and A2A agents from ACH

## Call from the shell

Start with the action the user needs. For example, to look up a Zoho contact:

```bash
ach-cli mcp search zoho contacts
ach-cli mcp describe <server.tool> -o json
ach-cli mcp call <server.tool> - -o json < args.json
```

Copy the dotted tool name from search. Use the input schema to construct the JSON
object in `args.json`. You can also pass a quoted JSON object inline. Both commands accept
separate `<server> <tool>` arguments. Discovery prints this form when a dotted
name would be ambiguous. Omit the object for a tool with no arguments. JSON output includes
the MCP content and any structured content. Check the exit status before using it.

Use `ach-cli mcp list` to see the servers, `ach-cli mcp describe <server>` for one
server's tools, or `ach-cli mcp list --tools` for the full inventory. Search ranks
servers by their best match. Within each server, tools matching more words come
first. Search and `list --tools` check current server grants and reuse tool metadata for five minutes.
Use `--refresh` after a tool change. Servers needing sign-in appear in a single
stderr message and in the JSON `errors` array. Discovery succeeds if the other
servers respond. It exits nonzero if every queried server needs sign-in or a
query fails for another reason.

If a server requires provider consent, ask the person to run
`ach-cli mcp login <server>` with their personal login and approve the provider in the browser. Over SSH, the user can run
`ach-cli mcp auth <server> --device`, open the verification URL in any browser,
and confirm the short code shown in the terminal. For callback paste, use
`ach-cli mcp auth <server> --no-browser`. Paste the final loopback URL directly
into the waiting terminal. Keep it out of chat and shell history. Retry discovery
with the original credential. A grant is required for both discovery and calls.

For an A2A agent, search by name, description or skill, then send a message:

```bash
ach-cli a2a list
ach-cli a2a search <words...>
ach-cli a2a call <agent> 'Review the deployment'
ach-cli a2a call <agent> 'Explain the first finding' --context-id <id>
```

Use the context ID from the reply for a follow-up. Run these commands from the
user's current directory. Local MCP configuration is optional.

## Use a server inside a configured tool

Hydrating an Environment writes its MCP servers into the tool's config, enabled. Each
points at `<platform URL>/mcp/<server>` and signs in with the user's ACH single sign-on
(then, for some servers, the provider's own consent screen).

## Check first, then sign in

Always list what the tool already has before adding anything. A server that is listed is
already configured, even when it says it needs authentication: only sign in to it.
Adding it again creates a duplicate under another name.

The Codex and OpenCode sign-in commands wait for a browser and hang the session in the
foreground. Never run them directly: use the recipe below the table.

| Tool | List | Sign in |
|---|---|---|
| Claude Code | `claude mcp list` | `/mcp` inside a session, pick the server, Authenticate |
| Codex | `codex mcp list` | `codex mcp login <server>` |
| OpenCode | `opencode mcp list` | `opencode mcp auth <server>` |

One server at a time, with the tool's sign-in command:

```bash
LOG="${TMPDIR:-/tmp}/mcp-auth-<server>.log"
nohup opencode mcp auth <server> </dev/null >"$LOG" 2>&1 &    # or: codex mcp login <server>
for i in $(seq 1 20); do grep -q http "$LOG" && break; sleep 1; done; cat "$LOG"
```

Give the link to the user and wait for them to say they approved; do not poll. Then
check the tool's list.

## A server the tool does not show

Hydrate again (`ach-cli environment hydrate`), then list again. If it is still missing:

```bash
ach-cli environment describe <environment>    # the MCP servers each Environment provides
ach-cli environment search <server>           # which of your Environments has it
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
- **Remote sign-in**: use the client's callback prompt when available, or forward
  the displayed loopback port to the remote host over SSH.
