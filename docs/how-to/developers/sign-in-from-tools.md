# Use ACH models from Claude Code, Codex and OpenCode

By default a tool keeps its own model login, and ACH only gives it MCP
servers, plugins and skills. This page sends the tool's model requests
through ACH as well, so they use your ACH sign-in and count against your ACH
budget.

You need to be [signed in](sign-in.md) with `ach-cli login`, not with an
environment key, and `ach-cli` must be on your `PATH`: the tools start it
without your shell, so an alias is not enough.

## Claude Code and Codex

Add `--models` when you hydrate:

```bash
ach-cli environment hydrate <environment> --target claude-code --models
ach-cli environment hydrate <environment> --target codex --models
```

`environment clone` and `launch` take `--models` too.

Pass `--models` every time you hydrate or `launch` in that folder. A run
without it removes what `--models` wrote, which is also how you go back to
the tool's own login.

### What `--models` writes

The tool gets your ACH as its model endpoint and `ach-cli token` as the
command that supplies the credential.

Claude Code, `.claude/settings.json`:

```json
{
  "apiKeyHelper": "ach-cli token",
  "env": { "ANTHROPIC_BASE_URL": "https://ach.example.com" }
}
```

Codex, `.codex/config.toml`:

```toml
model_provider = "ach"

[model_providers.ach]
name = "ACH"
base_url = "https://ach.example.com/v1"
wire_api = "responses"

[model_providers.ach.auth]
command = "ach-cli token"
refresh_interval_ms = 300000
```

### `ach-cli token`

[`ach-cli token`](../../reference/cli.md#ach-cli-token) prints the access
token of the active profile on one line and nothing else. It renews the token
when it is close to expiry. `ach-cli token -o json` adds the token's type and
expiry.

Any other tool that takes a command for its credential can use it the same
way, with `https://ach.example.com/v1` as an OpenAI-compatible base URL.

## OpenCode

OpenCode signs in to ACH with its own plugin and needs neither `--models` nor
`ach-cli`:

```bash
opencode auth login https://ach.example.com   # installs the ACH sign-in plugin
opencode auth login <provider>                # signs you in
opencode service restart                      # OpenCode v2: plugins load when the service starts
```

`<provider>` is the name your platform team chose for ACH; they have it. On
OpenCode v1 the second command is `opencode auth login -p <provider>`.

After the restart, the models of `<provider>` come from ACH.

## Verify

Start the tool and send a prompt. Then:

```bash
ach-cli whoami
```

The spend after `Budget` grows with your requests.

## What you can reach

The models you get are those of the Environments your teams may use, as
listed by `ach-cli environment describe <environment>`.

## Related

- [Sign in to an MCP server](sign-in-to-an-mcp-server.md) for the MCP servers
  of an Environment.
- [Create and manage environment keys](manage-keys.md) for an agent or a CI
  job, which cannot sign in with a browser.
