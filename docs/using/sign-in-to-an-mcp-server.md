# Sign in to an MCP server

## Use the shell from any directory

Your active ACH credential can discover and call a remote tool without local MCP configuration. To look up a contact, start with `ach-cli mcp search zoho contacts`. Copy the names into `ach-cli mcp describe <server> <tool>`, then pass the fields the input schema requires to `ach-cli mcp call <server> <tool> '<json-object>'`. Add `-o json` to keep the complete MCP result for a script.

If the server asks for provider consent, run `ach-cli mcp login <server>` with your personal login, approve access in the browser, and retry.

Over SSH, run `ach-cli mcp auth <server> --device`. Open the verification URL in any browser, confirm the short code, and approve provider access. The terminal waits for approval. To use callback paste, run `ach-cli mcp auth <server> --no-browser` and paste the final loopback URL into the waiting terminal.

If you maintain a device-code client, use the `device_authorization_endpoint`
from ACH's OAuth metadata. Send the MCP server URL (for example,
`https://ach.example.com/mcp/zoho`) in the `resource` form field alongside
`client_id`. Poll the token endpoint at the advertised interval while the
user signs in and approves the provider. The client receives
`authorization_pending` while consent is in progress.

For an A2A agent, use `ach-cli a2a search <words...>` and `ach-cli a2a call <agent> '<message>'`.

## Configure a server in your coding tool

Hydrating an Environment writes its MCP servers into your tool's
configuration. Each entry points at `https://ach.example.com/mcp/<server>`.
An A2A agent of the Environment appears as an MCP server named
`a2a-<agent>`.

When you hydrate with your sign-in, the entries hold no credential: the tool
signs in to ACH itself, once per server, with your single sign-on. A hydrate
with an environment key writes that key into the entries, and there is
nothing to sign in to.

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

The browser redirects to `localhost` on the machine where you opened the
authorization link. If the remote tool offers a callback prompt, as OpenCode
does, paste the full final URL into that prompt. Keep it out of chat and shell
history. For a tool that requires a loopback connection, forward its callback
port with `ssh -L <port>:localhost:<port> <host>` before finishing sign-in.
Then list the servers in the tool to check the connection.

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
| Connected, but no tools | The service behind the server has no consent from you | Run the tool's sign-in command again and approve the consent screen. If it stays empty, ask a platform admin |
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
