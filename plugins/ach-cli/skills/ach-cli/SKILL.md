---
name: ach-cli
description: "Work with this organization's AI platform (ACH) through the ach-cli command line. Use whenever the user mentions ACH, ach-cli, an Environment, an environment key (ek-), or wants to sign in, list, search, describe, hydrate, clone, update or uninstall Environments (globally or into a folder), lay out a workspace per Environment, share a setup with a teammate, sign in to or troubleshoot an MCP server that came from ACH, create, list, suspend, cap or revoke a key for an agent, CI job or script, set up Claude Code, Codex, OpenCode, Gemini CLI or pi against the platform, or install a plugin or skill straight from a git repo with ach-cli local. Also use for ach-cli errors and exit codes, even if the user does not name the tool."
---

# ACH from the command line

ACH is the organization's gateway for models, MCP servers, A2A agents, skills and
plugins. `ach-cli` is its user CLI: it signs the user in, shows the Environments they
may use, and installs ("hydrates") an Environment into an agent tool.

Never ask for, print, paste or commit a key or token. A key is shown once, on the
user's own terminal, and stays there.

## Where ACH is

`ach-cli` already knows the platform URL once the user has signed in: it is saved in
the active profile (`~/.config/ach/config.yaml`). `ach-cli whoami` prints it. If no
profile exists yet, take the URL from the `ACH_URL` environment variable (`ach-cli
login` reads it on its own), or ask the user for it. Never guess a URL.

## Start by looking

Before changing anything, read the state yourself. These commands only read:

```bash
command -v ach-cli && ach-cli --version
ach-cli whoami          # profile, URL, user, auth (session or ek- key), budget, keys
ach-cli env status      # what is installed in this folder (-g <env> for global)
```

Then ask once whether the user is setting up THIS machine (where you run) or another
one (their laptop, a server, CI). On this machine, run each step yourself and ask for a
go-ahead before every command that changes something (install, login, hydrate,
uninstall, keys). On another machine, show each command for the user to run there and
ask them to paste back the output. Skip what is already done. Never end a reply by
announcing a next step: run it in the same reply, or end with a question.

## Route the request

| The user wants to… | Read |
|---|---|
| Install or update ach-cli itself | [install](references/install.md) |
| Sign in, switch profiles, sign out, a machine/CI profile, `ach-cli token` | [login](references/login.md) |
| See, search or describe Environments | [environments](references/environments.md) |
| Install an Environment globally or into a folder, keep it updated, remove it | [hydrate](references/hydrate.md) |
| Sign in to or fix an MCP server | [mcp](references/mcp.md) |
| A key for an agent, CI job or script (`ek-`) | [keys](references/keys.md) |
| Install a plugin or skill straight from a git repo, without an Environment | [local](references/local.md) |
| An error, an exit code, admin commands | [troubleshooting](references/troubleshooting.md) |

## The model in one paragraph

An **Environment** is a named bundle the platform team publishes: models, MCP servers,
A2A agents (runtime) plus plugins, skills, prompts and artifacts (context). Access is
by team: `ach-cli env list` shows only the Environments the user's teams may use.
**Hydrating** writes an Environment into an agent tool's own config, either globally
(`-g`, every project) or into a folder (that folder and everything under it). A
**target** is the tool: `claude-code`, `codex`, `gemini-cli`, `opencode`, `pi`. Hydrate
records what it wrote under `.ach/<env>/` (or `~/.ach/<env>/` with `-g`), so it can update
or remove exactly that later without touching the user's own settings.

## Defaults worth knowing

- Global first: the organization's baseline Environment usually goes in once with
  `ach-cli env hydrate <env> -g --target <tool>`; project-specific ones go into folders.
- `--target` is optional on `env hydrate` inside a folder that already has one tool's
  config (it is autodetected; it errors when it finds none or several). `env clone` and
  `-g` always need it, since there is nothing to detect. `ACH_TARGET` is the same.
- Models: by default a tool keeps its own model login. `--models` also sends that tool's
  model traffic through ACH (Claude Code and Codex; needs `ach-cli` on the `PATH`).
  OpenCode signed in with ACH already gets its models from ACH and never needs it.
- Plugin commands and skills land namespaced by the plugin name (`/bro:ladder` in
  Claude Code, `bro-ladder` elsewhere), so they never shadow a built-in.
- Every network command takes `--profile <name>`, `--key <saved name|ek-…>` and
  `--verbose` (request headers to stderr, credentials redacted). List and read commands
  take `-o json` for machine-readable output: prefer it when you parse the result.
