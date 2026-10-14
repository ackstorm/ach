---
name: {{skill}}
description: Onboard to and work with this organization's AI platform (ACH) at {{base}}. Use when the user mentions ACH, an Environment or an MCP server, or wants to get set up, install, update or sign in to ach-cli, list or hydrate Environments (globally or into the current folder), lay out a workspace per Environment, share that setup with a teammate, see, enable, sign in to or troubleshoot an MCP server, create, list or revoke an environment key (ek-) for an agent, CI job or script, or set up Claude Code or Codex against the platform.
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

Walk the user through these steps in this order.

1. Check this machine yourself, without asking: `command -v ach-cli`, `ach-cli --version`,
   `ach-cli whoami`, `ach-cli env status`. They only read.
2. Ask once whether the user is setting up THIS machine (the one you run on) or another
   one (their laptop, a server, CI).
   - This machine: run every step yourself. Ask for a go-ahead before each command that
     changes something (install, login, hydrate, uninstall), never before a read.
   - Another machine: you cannot reach it. Show each command for the user to run there,
     and ask them to paste back its output.
3. Skip a step that is already done. If `ach-cli` is missing, install it before anything
   else. Show each command's output, and stop to fix any error before moving on.
4. Never end a reply by announcing a next step ("now let me list…"). Either run it in that
   same reply, or end with a question for the user.

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

To update ach-cli, run the same commands again: they install the latest release over
the old one (`ach-cli --version` before and after).

### Sign in

Same single sign-on as OpenCode:

```bash
ach-cli login {{base}}
ach-cli whoami        # who you are, your budget and how many keys you may create
```

On a remote or headless machine (SSH, container, no browser), add `--no-browser`:
`ach-cli login {{base}} --no-browser` prints a code to enter in a browser on any other
device.

When YOU run the login (this machine), never run it in the foreground: it prints the link
and then waits up to 10 minutes for the user to sign in, so your shell tool shows nothing
until it times out. Start it in the background, read the link from its log, and give it to
the user:

```bash
LOG="${TMPDIR:-/tmp}/ach-login.log"
nohup ach-cli login {{base}} --no-browser --profile default </dev/null >"$LOG" 2>&1 &
for i in $(seq 1 20); do grep -q 'Waiting for you' "$LOG" && break; sleep 1; done; cat "$LOG"
```

Give the user the URL and the code from that output, then wait for them to say they
signed in (do not poll). Check with `ach-cli whoami`; if it is not signed in yet,
`cat "${TMPDIR:-/tmp}/ach-login.log"` shows why (an expired code: run it again). The URL,
`--profile default` and `--no-browser` keep `ach-cli` from asking anything on a stdin it
does not have; it exits on its own when the code expires. On Windows, run it from Git
Bash.

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

The organization's baseline Environment is `{{default_env}}`. Check whether it is
already installed (`ach-cli env status {{default_env}} -g`); if not, propose it to the
user. Hydrate it into the user's global OpenCode config, so every project gets it:

```bash
ach-cli env hydrate {{default_env}} -g --target opencode
```

This writes into `~/.config/opencode/` (its MCP servers, enabled, and its skills,
commands and agents).

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

To hydrate into the folder the user is working in now ("hydrate finops here"), drop
`--dir`: `ach-cli env hydrate finops --target opencode` writes into the current
directory. Confirm the folder with the user first.

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

Do not restart OpenCode after a hydrate or uninstall: it reloads its config by itself, and
restarting it from inside a session breaks that session. Check with `opencode mcp list`;
if a server is missing, wait 5 seconds (`sleep 5`) and check once more. Only if it is
still missing, offer to restart it yourself (`opencode service restart`), warning the
user first that the chat may then show an error message they can ignore; restart only
with their go-ahead.

### Sign in to MCP servers on first use

`opencode mcp auth <name>` opens the browser (ACH single sign-on, then, for some
servers, the provider's own consent screen). Or use a tool of that server in chat and
open the sign-in link OpenCode shows. Check with `opencode mcp list`.

On a remote machine (a Coder workspace: `CODER=true`; or an SSH session: `SSH_CONNECTION`
is set) this sign-in ends on `http://127.0.0.1:<port>/…callback?code=…` in the user's own
browser, which shows "127.0.0.1 refused to connect": OpenCode waits on the remote machine's
localhost, not the user's (OpenCode issue
<https://github.com/anomalyco/opencode/issues/54495>). Tell the user before they start, then
offer, in this order:
- Coder: check whether the workspace runs the callback helper (`pgrep -f '^python3? .*callback_relay[.]py'`).
  If it does, offer its "OAuth Callback" app at
  `https://callback-relay--$CODER_WORKSPACE_NAME--$CODER_WORKSPACE_OWNER_NAME.<coder domain>`
  (the domain as in `$VSCODE_PROXY_URI`): the user opens it in the browser where they are
  signed in to Coder (it asks for a Coder login otherwise; you cannot open it with curl),
  pastes the failed address, and it delivers it to OpenCode. Not running: skip to the next
  option.
- Forward the port before signing in, from the user's own machine, taking `<port>` from the
  failed address (OpenCode's MCP default is 19876): Coder,
  `coder port-forward $CODER_WORKSPACE_NAME --tcp <port>:<port>`; SSH,
  `ssh -L <port>:localhost:<port> <host>`. Then run the sign-in again.

The user runs these on their machine, not you. The code in that address is single-use and
expires in minutes: if the delivery fails, sign in again.

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
stop, do not invent a URL. An ACH administrator can add it to an Environment.

Do not add `--header`, a key, or `"oauth": false` to an MCP entry (that last one
disables the sign-in), and do not edit the entries hydrate wrote.

Troubleshooting:
- 401 after signing in: `opencode mcp logout <name>`, then `opencode mcp auth <name>`.
- 403: the user's account lacks access to that server. An ACH administrator grants it
  (through an Environment); signing in again will not help.
- A server missing after a change to the Environment: hydrate again, then `opencode mcp list`.

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
`GET {{base}}/v1/models` lists chat models only; add `?types=embedding` (any LiteLLM
model mode, comma-separated) or `?types=all` to see the others.

## Other tools

The same layout works for Claude Code and Codex: hydrate with `--target claude-code`
(or `codex`; several at once: `--target opencode,claude-code`). Add `--models` to also
send that tool's model traffic through ACH (needs `ach-cli` on the `PATH`); without it
the tool keeps its own model login. OpenCode never needs `--models`: its models already
come from ACH. Install one plugin or
skill only with `ach-cli env hydrate <environment> --only plugin/<name>` (or
`skill/<name>`); `ach-cli env uninstall <environment> --only plugin/<name>` removes it.
