---
name: {{skill}}
description: Onboard to and work with this organization's AI platform (ACH) at {{base}}. Use when the user wants to get set up, install or sign in to ach-cli, list or hydrate Environments, lay out a workspace per Environment, share that setup with a teammate, see, enable, sign in to or troubleshoot an MCP server, create, list or revoke an environment key (ek-) for an agent, CI job or script, or set up Claude Code or Codex against the platform.
---

# {{provider}} platform (ACH)

The organization's gateway for models, MCP servers, skills and plugins is ACH, at
`{{base}}`. The user is already signed in to it in this OpenCode: the models of the
`{{provider}}` provider come from ACH through single sign-on, with no API key. If they
stop working, the user signs in again with `opencode auth login {{provider}}` (OpenCode
v1: `opencode auth login -p {{provider}}`). No plugin at all? `opencode auth login {{base}}`
installs it first. After installing or signing in, restart OpenCode: on OpenCode v2,
`opencode service restart` (the background service loads the plugin only when it
starts); the models appear a few seconds later.

Never ask for, print, paste or commit a key or token. Keys created below are shown
once, on the user's terminal, and stay there.

## Onboarding, step by step

Walk the user through these steps in this order. Skip a step that is already done (check
first: `ach-cli --version`, `ach-cli whoami`, `ach-cli env status`). Run each command
only with the user's go-ahead, show its output, and stop to fix any error before moving
on.

### Install ach-cli

`ach-cli` ships as an archive per OS and CPU on the releases page
<https://github.com/ackstorm/ach/releases/latest>:
`ach-cli_<version>_<os>_<arch>.tar.gz` for `linux` and `darwin` (macOS) on `amd64` or
`arm64`, `ach-cli_<version>_windows_amd64.zip` for Windows, plus `checksums.txt`.
`<version>` is the release tag without its leading `v`.

Linux and macOS:

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/ackstorm/ach/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')            # linux or darwin
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac
FILE="ach-cli_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/${FILE}"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/checksums.txt"
grep " ${FILE}\$" checksums.txt | if command -v sha256sum >/dev/null; then sha256sum -c -; else shasum -a 256 -c -; fi  # must print "OK"
tar -xzf "$FILE" ach-cli
mkdir -p ~/.local/bin && mv ach-cli ~/.local/bin/    # ~/.local/bin must be on the PATH
ach-cli --version
```

Windows (PowerShell; the build is amd64, it also runs on ARM Windows):

```powershell
$v = (Invoke-RestMethod https://api.github.com/repos/ackstorm/ach/releases/latest).tag_name.TrimStart('v')
Invoke-WebRequest "https://github.com/ackstorm/ach/releases/download/v$v/ach-cli_${v}_windows_amd64.zip" -OutFile ach-cli.zip
Expand-Archive ach-cli.zip -DestinationPath "$env:LOCALAPPDATA\ach-cli" -Force
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ";$env:LOCALAPPDATA\ach-cli", 'User')
# open a new terminal, then:
ach-cli --version
```

### Sign in

Same single sign-on as OpenCode:

```bash
ach-cli login {{base}}
ach-cli whoami        # who you are, your budget and how many keys you may create
```

On a remote or headless machine (SSH, container, no browser), add `--no-browser`:
`ach-cli login {{base}} --no-browser` prints a code to enter in a browser on any other
device.

### See your Environments

An Environment is what a tool may use: models, MCP servers, skills, plugins, prompts.

```bash
ach-cli env list
ach-cli env describe <environment>    # what one Environment contains
```

If the one the user needs is not listed, they do not have access to it: an ACH
administrator grants it.

{{#default_env}}
### Install the baseline for every project

The organization's baseline Environment is `{{default_env}}`. Hydrate it into the
user's global OpenCode config, so every project gets it:

```bash
ach-cli env hydrate {{default_env}} -g --target opencode
```

This writes into `~/.config/opencode/` (its MCP servers, enabled, and its skills,
commands and agents). Restart OpenCode afterwards.

{{/default_env}}
### Lay out a workspace: one folder per Environment

Recommended layout:

```
<workspace>/            e.g. ~/Projects (the user picks it)
  <environment>/        one folder per Environment, e.g. gmail/
    <project>/          the actual work; may or may not be its own git repo
```

Hydrate each Environment into its own folder, NOT globally:

```bash
mkdir -p ~/Projects/gmail
ach-cli env hydrate gmail --dir ~/Projects/gmail --target opencode
```

Every project under that folder gets the Environment: OpenCode merges the
`.opencode/opencode.json` of every parent folder of the directory it starts in, git
repo or not, so no `git init` is needed. Start OpenCode inside the project
(`cd ~/Projects/gmail/<project> && opencode`). Hydrate also adds `.ach/` and `.opencode/`
to a `.gitignore` in that folder.

Do not hydrate two Environments into the same folder chain unless the user wants both
merged into one OpenCode.

### Keep the folder reproducible

```bash
ach-cli env save --dir ~/Projects/gmail   # writes ach.yaml (Environment names + targets, no secrets): commit it
```

A teammate who has the same folder (with its `ach.yaml`) runs a bare
`ach-cli env hydrate` in it: it hydrates every Environment listed there.

- Pick up changes to the Environment: run the same `ach-cli env hydrate …` again; add
  `--sync` to also remove what the Environment no longer contains.
- See what is installed: `ach-cli env status --dir ~/Projects/gmail` (`--files` lists
  every file); for the global install, `ach-cli env status <environment> -g`.
- Remove it: `ach-cli env uninstall gmail --dir ~/Projects/gmail` (global:
  `ach-cli env uninstall <environment> -g`). `--dry-run` previews; only what hydrate
  wrote is removed, the user's own settings in shared files stay.

Restart OpenCode after any hydrate or uninstall: config is only read at startup.

### Sign in to MCP servers on first use

`opencode mcp auth <name>` opens the browser (ACH single sign-on, then, for some
servers, the provider's own consent screen). Or use a tool of that server in chat and
open the sign-in link OpenCode shows. Check with `opencode mcp list`.

## MCP servers

Hydrating an Environment (globally or into a folder, as above) writes its MCP servers
into OpenCode's config, enabled. One server can also be added by hand, by its ACH URL
`{{base}}/mcp/<server>`:

```bash
ach-cli env describe <environment>    # the MCP servers an Environment provides (<server>)
opencode mcp add <name> --url {{base}}/mcp/<server> --global   # drop --global: this project only
opencode mcp auth <name>              # sign in; with no name it lists every server to pick from
opencode mcp list
```

Take `<server>` from `ach-cli env describe` (or `opencode mcp list`); if the server the
user wants is in none of their Environments, it is not available to them: say so and
stop, do not invent a URL. An ACH administrator can add it to an Environment. Restart
OpenCode after adding or hydrating.

Do not add `--header`, a key, or `"oauth": false` to an MCP entry (that last one
disables the sign-in), and do not edit the entries hydrate wrote.

Troubleshooting:
- 401 after signing in: `opencode mcp logout <name>`, then `opencode mcp auth <name>`.
- 403: the user's account lacks access to that server. An ACH administrator grants it
  (through an Environment); signing in again will not help.
- A server missing after a change to the Environment: hydrate again, then restart.

## Environment keys (ek-)

An `ek-` key lets a non-interactive consumer (an agent, a CI job, a script) use ACH
within ONE Environment, without the user's identity. Tell the user to run these in
their own terminal:

```bash
ach-cli keys create <environment> --name <name>   # <name> (required) = what it is for; shown once, saved as <name>
ach-cli keys create <environment> --name ci-deploy --no-save   # CI / a secrets manager: stdout only
ach-cli keys list
ach-cli keys suspend <name>                        # pause it; keys resume <name> undoes it
ach-cli keys revoke <name>                         # for good (a saved name or an ekid_…)
```

`keys create` also takes `--expires 90d` and `--max-budget <USD>` (with
`--budget-duration 30d`); `ach-cli keys budget <name> --max-budget <USD>` changes the
cap later.

Store the key in the consumer's secret store. It is sent as the `x-ach-key` header
(or `x-api-key`) to `{{base}}/v1` for models and `{{base}}/mcp/<name>` for MCP.

## Other tools

The same layout works for Claude Code and Codex: hydrate with `--target claude-code`
(or `codex`; several at once: `--target opencode,claude-code`). Add `--models` to also
send that tool's model traffic through ACH (needs `ach-cli` on the `PATH`); without it
the tool keeps its own model login. OpenCode never needs `--models`: its models already
come from ACH. Install one plugin or
skill only with `ach-cli env hydrate <environment> --only plugin/<name>` (or
`skill/<name>`); `ach-cli env uninstall <environment> --only plugin/<name>` removes it.
