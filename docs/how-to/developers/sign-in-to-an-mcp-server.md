# Sign in to an MCP server

Hydrating an Environment writes its MCP servers into your tool's
configuration. Each entry points at `https://ach.example.com/mcp/<server>`
and holds no credential: the tool signs in to ACH itself, once per server,
with your single sign-on.

You need an Environment [hydrated](use-environments.md) into the tool.

## Sign in

List the servers first. A server in the list is configured, even when the
tool says it needs authentication: sign in to it, do not add it again.

| Tool | List | Sign in |
|---|---|---|
| Claude Code | `claude mcp list` | `claude mcp login <server>`, or `/mcp` inside a session |
| Codex | `codex mcp list` | `codex mcp login <server>` |
| OpenCode | `opencode mcp list` | `opencode mcp auth <server>` |

The command opens a browser on the ACH sign-in page. For some servers the
browser makes one more stop, at the consent screen of the service behind the
server (for example a Google or GitHub account); approve it there.

Finish the sign-in in the browser that opened it. A link copied into another
browser or browser profile is refused.

## Verify

List again. The server shows as connected, and its tools appear in the tool.

## Sign in over SSH

The tool waits for the browser on the remote machine's `localhost`, so the
last redirect fails in your local browser with "127.0.0.1 refused to
connect". Either:

- forward the callback port before you sign in:
  `ssh -L <port>:localhost:<port> <host>`; or
- copy the whole failed address from the browser bar and open it on the
  remote machine: `curl -s -o /dev/null "<address>"`. Only do this for an
  address that starts with `http://127.0.0.1:` or `http://localhost:`.

Then list the servers in the tool to check.

## The tool does not show a server

1. Hydrate again and list again:

    ```bash
    ach-cli environment hydrate
    ```

2. Check that one of your Environments provides it:

    ```bash
    ach-cli environment describe <environment>
    ach-cli environment search <server>
    ```

If none does, the server is not available to you. A platform admin adds it to
an Environment.

## Errors

| Symptom | Cause | Do |
|---|---|---|
| `401` on every request | The tool never signed in to that server | Run the tool's sign-in command |
| `401` after signing in | The stored sign-in is no longer valid | Sign out of that server in the tool, then sign in again |
| `403` | Your teams have no access to that server | Signing in again does not help. A platform admin grants it through an Environment |
| Connected, but no tools | The service behind the server has no consent from you | Run the tool's sign-in command again and approve the consent screen |
| "this browser did not start the authorization request" | The sign-in finished in another browser than the one that started it, or cookies are blocked for your ACH | Start the sign-in again from the tool |
| A server disappeared | The Environment changed | Hydrate again |

## Leave the entries as hydrate wrote them

Do not add a header, a key or an OAuth setting to an MCP entry that hydrate
wrote. A credential header turns off the tool's own sign-in, and the next
hydrate rewrites the entry.

## Related

- [Use ACH models from Claude Code, Codex and OpenCode](sign-in-from-tools.md)
  for model traffic.
- [Troubleshoot ach-cli](troubleshoot.md).
