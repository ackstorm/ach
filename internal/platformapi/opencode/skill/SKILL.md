---
name: {{skill}}
description: Get this OpenCode set up on the organization's AI platform (ACH) at {{base}}. Use when the user wants to get started or onboard, install or sign in to ach-cli, sees no ACH models in OpenCode, or asks how to get the organization's MCP servers, skills and plugins into OpenCode.
---

# {{provider}} platform (ACH): first setup

The organization's gateway for models, MCP servers, skills and plugins is ACH, at
`{{base}}`. The user is already signed in to it in this OpenCode: the models of the
`{{provider}}` provider come from ACH through single sign-on, with no API key. If they
stop working, the user signs in again with `opencode auth login {{provider}}` (OpenCode
v1: `opencode auth login -p {{provider}}`). No plugin at all? `opencode auth login {{base}}`
installs it first. After installing or signing in, restart OpenCode: on OpenCode v2,
`opencode service restart` (the background service loads the plugin only when it
starts); the models appear a few seconds later.

This skill only gets `ach-cli` working and the user's Environments visible. Everything
after that (installing Environments, folders, MCP sign-in, keys) is the `ach-cli` skill,
which an Environment brings when it is installed: load it then.

Never ask for, print, paste or commit a key or token.

## Steps

Check this machine yourself first, without asking (these only read):
`command -v ach-cli`, `ach-cli --version`, `ach-cli whoami`. Skip a step that is already
done, ask for a go-ahead before each command that changes something, show each
command's output, and stop to fix any error before moving on.

### 1. Install ach-cli

From the releases page <https://github.com/ackstorm/ach/releases/latest>. Linux and
macOS:

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

Windows (PowerShell):

```powershell
$v = (Invoke-RestMethod https://api.github.com/repos/ackstorm/ach/releases/latest).tag_name.TrimStart('v')
Invoke-WebRequest "https://github.com/ackstorm/ach/releases/download/v$v/ach-cli_${v}_windows_amd64.zip" -OutFile ach-cli.zip
Expand-Archive ach-cli.zip -DestinationPath "$env:LOCALAPPDATA\ach-cli" -Force
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ";$env:LOCALAPPDATA\ach-cli", 'User')
# open a new terminal, then: ach-cli --version
```

### 2. Sign in

Same single sign-on as OpenCode. `ach-cli login` waits up to 10 minutes for the browser,
so never run it in the foreground: start it in the background, read the link from its
log, and give it to the user.

```bash
LOG="${TMPDIR:-/tmp}/ach-login.log"
nohup ach-cli login {{base}} --no-browser --profile default </dev/null >"$LOG" 2>&1 &
for i in $(seq 1 20); do grep -q 'Waiting for you' "$LOG" && break; sleep 1; done; cat "$LOG"
```

Give the user the URL and the code from that output, then wait for them to say they
signed in (do not poll). Check with `ach-cli whoami`; if it is not signed in yet,
`cat "${TMPDIR:-/tmp}/ach-login.log"` shows why (an expired code: run it again). On
Windows, run it from Git Bash.

### 3. See your Environments

```bash
ach-cli env list
```

An Environment is what a tool may use: models, MCP servers, skills, plugins. If one the
user needs is missing, they do not have access to it: an ACH administrator grants it.

{{#default_env}}
### 4. Install the baseline for every project

The organization's baseline Environment is `{{default_env}}`. Check whether it is
already installed (`ach-cli env status {{default_env}} -g`); if not, propose it:

```bash
ach-cli env hydrate {{default_env}} -g --target opencode
```

This writes into `~/.config/opencode/` its MCP servers (enabled), skills, commands and
agents, including the `ach-cli` skill. Do not restart OpenCode afterwards: it picks up
the new MCP servers by itself within seconds, and restarting from inside a session
breaks that session. Then run `opencode mcp list`; for any server that needs
authentication, and for anything else (another Environment, a folder per project,
keys), load the `ach-cli` skill.

{{/default_env}}
