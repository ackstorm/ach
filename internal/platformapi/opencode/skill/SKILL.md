---
name: genai-api
description: Work with this organization's GenAI API gateway (ACH). Use when the user wants to see, enable, sign in to or troubleshoot an MCP server; install or sign in to ach-cli; list Environments; create, list or revoke an environment key (ek-) for an agent, CI job or script; or set up Claude Code or Codex against the gateway.
---

# GenAI API (ACH)

The organization's gateway for models and MCP servers is ACH, at `{{base}}`. The user
is already signed in to it here: the models of the `{{provider}}` provider in this
OpenCode come from ACH through single sign-on, with no API key. If they stop working,
the user signs in again with `opencode auth login {{provider}}` (OpenCode v1:
`opencode auth login -p {{provider}}`). No plugin at all? `opencode auth login {{base}}`
installs it first. After installing or signing in, restart OpenCode: on OpenCode v2,
`opencode service restart` (the background service loads the plugin only when it
starts); the models appear a few seconds later.

Never ask for, print, paste or commit a key or token. Keys created below are shown
once, on the user's terminal, and stay there.

## MCP servers

Every MCP server this user can reach is already in OpenCode's config, **disabled**.
See them with:

```bash
opencode mcp list
```

If the one the user wants is not there, it is not available to them: say so and stop;
do not invent a URL. An ACH administrator can grant access.

To use one:

1. Enable it. Global (every project): `~/.config/opencode/opencode.json`. One project:
   `opencode.json` at the project root. Read the file first and merge; do not rewrite
   it or drop keys you did not add:

   ```json
   { "mcp": { "<name>": { "enabled": true } } }
   ```

   Only `enabled` is needed: the URL comes from ACH. Do not add `headers`, a key, or
   `"oauth": false` (that disables the sign-in).
2. Restart OpenCode (or OpenWork). Config is only read at startup.
3. Sign in: `opencode mcp auth <name>` opens the browser (ACH single sign-on, then,
   for some servers, the provider's own consent screen). Or use a tool of that server
   in chat and open the sign-in link OpenCode shows.
4. Check: `opencode mcp list`.

Troubleshooting:
- 401 after signing in: `opencode mcp logout <name>`, then `opencode mcp auth <name>`.
- 403: the user's account lacks access to that server. An ACH administrator grants it
  (through an Environment); signing in again will not help.

## ach-cli

The command-line client, for keys, Environments and setting up other tools.

Install: download the archive for the user's OS and CPU from
<https://github.com/ackstorm/ach/releases/latest>
(`ach-cli_<version>_<os>_<arch>.tar.gz`, `.zip` on Windows), extract `ach-cli`, put it
on the `PATH`. Then sign in, same single sign-on:

```bash
ach-cli login {{base}}      # add --no-browser on a remote/headless host
ach-cli whoami
```

Environments (what an agent or tool may use: models, MCP servers, skills, plugins):

```bash
ach-cli env list
ach-cli env describe <environment>
```

## Environment keys (ek-)

An `ek-` key lets a non-interactive consumer (an agent, a CI job, a script) use ACH
within ONE Environment, without the user's identity. Tell the user to run these in
their own terminal:

```bash
ach-cli keys create <environment> --name <name>   # shown once, saved in your profile as <name>
ach-cli keys create <environment> --no-save       # for CI / a secrets manager: stdout only
ach-cli keys list
ach-cli keys suspend <name>                        # pause it; keys resume <name> undoes it
ach-cli keys revoke <name>                         # for good (a saved name or an ekid_…)
```

Store the key in the consumer's secret store. It is sent as the `x-ach-key` header
(or `x-api-key`) to `{{base}}/v1` for models and `{{base}}/mcp/<name>` for MCP.

## Other tools

For Claude Code or Codex in a project, `ach-cli env hydrate <environment> --target
claude-code` (or `codex`) writes that tool's config from the Environment. Install one
plugin or skill only with `ach-cli env hydrate <environment> --only plugin/<name>` (or
`skill/<name>`); `ach-cli env uninstall <environment> --only plugin/<name>` removes it.
Do not run hydrate for OpenCode: this OpenCode is already configured by ACH, and
hydrate would write entries that override it.
