# Sign in to an MCP server

## Use the shell from any directory

Your active ACH credential can discover and call a remote tool without local MCP configuration. To look up a contact, start with `ach-cli mcp search zoho contacts`. Copy the dotted tool name into `ach-cli mcp describe <server.tool>` to inspect its input schema. You can pass a JSON object inline or read it from a file. For example, if search returns `mcp-zoho-crm-ro.crm_search_contacts`, run:

```bash
ach-cli mcp describe mcp-zoho-crm-ro.crm_search_contacts -o json
ach-cli mcp call mcp-zoho-crm-ro.crm_search_contacts - -o json < args.json
```

Search groups results by server, with the strongest matching group first. Within each group, tools matching more words come first. A tool can match words from its own name and description and from the server's. Search and `mcp list --tools` check your server grants on every run and reuse tool metadata for five minutes. Add `--refresh` to read the inventory again. If some servers need sign-in, the command lists them on stderr and succeeds when the other servers respond. In JSON, check `errors` for the skipped names. If every queried server needs sign-in or any query fails for another reason, the command exits nonzero.

If the server asks for provider consent, run `ach-cli mcp login <server>` with your personal login, approve access in the browser, and retry.

Over SSH, run `ach-cli mcp auth <server> --device`. Open the verification URL in any browser, confirm the short code, and approve provider access. The terminal waits for approval. To use callback paste, run `ach-cli mcp auth <server> --no-browser` and paste the final loopback URL into the waiting terminal.

The browser must be signed in to ACH with the same account as your ACH
profile. If it is signed in as someone else, ACH refuses the sign-in and says
which account you used and which one was expected, both in the browser and in
the terminal. This happens when you sign in as an agent identity (a shared
account such as `ai.run@example.com`) on a machine where your browser holds
your own session: open the authorization URL in a private window and sign in
there as the agent identity. The same applies to `ach-cli login` on an
existing profile; to switch a profile to another account, run `ach-cli logout`
first.

If the account has no access to the server, ACH says so instead of finishing
the sign-in. Ask an administrator to grant the server in one of your
Environments.

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
