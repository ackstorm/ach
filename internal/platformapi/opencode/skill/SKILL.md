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
ach-cli env search <text>             # find a plugin, skill, MCP server… by name, in all of them
```

`env search finops` matches each Environment's name and description and the name of
everything it serves (plugins, skills, prompts, artifacts, MCP servers, models, agents);
several words must all match. It searches names, not what a skill or plugin says inside.

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

Clone each Environment into its own folder, NOT globally:

```bash
cd ~/Projects && ach-cli env clone gmail --target opencode   # creates ~/Projects/gmail
```

Like `git clone`: it creates the folder (default: the Environment name; pass a second
argument to pick another), refuses a folder that exists and is not empty, and hydrates
into it.

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

To share the folder with a teammate, write `ach.yaml` (Environment names + targets, no
secrets) and commit it: `ach-cli env save --dir ~/Projects/gmail`.

A teammate who has the same folder (with its `ach.yaml`) runs a bare
`ach-cli env hydrate` in it: it hydrates every Environment listed there.

- Pick up changes to the Environment: `cd ~/Projects/gmail && ach-cli env hydrate`; add
  `--sync` to also remove what the Environment no longer contains.
- See what is installed: `ach-cli env status --dir ~/Projects/gmail` (`--files` lists
  every file); for the global install, `ach-cli env status <environment> -g`.
- Remove it: `ach-cli env uninstall gmail --dir ~/Projects/gmail` (global:
  `ach-cli env uninstall <environment> -g`). `--dry-run` previews; only what hydrate
  wrote is removed, the user's own settings in shared files stay.

Do not restart OpenCode after a hydrate or uninstall: it picks up new and removed MCP
servers by itself within seconds, and restarting it from inside a session breaks that
session. Never run `opencode service restart`.

### Sign in to MCP servers on first use

After a hydrate, run `opencode mcp list` yourself. If any server says `needs
authentication`, offer to sign the user in, one server at a time.

`opencode mcp auth <name>` signs in (ACH single sign-on, then, for some servers, the
provider's own consent screen). Like `ach-cli login`, it prints a link and then waits for
the browser, so never run it in the foreground. Run this block as is (replace only
`<name>`): it starts the sign-in in the background and prints, already filled in, what the
user must do.

```bash
NAME=<name>
LOG="${TMPDIR:-/tmp}/mcp-auth-$NAME.log"
nohup opencode mcp auth "$NAME" </dev/null >"$LOG" 2>&1 &
for i in $(seq 1 20); do grep -q 'https://' "$LOG" && break; sleep 1; done
echo "1. Open this link and approve: $(grep -o 'https://[^ ]*' "$LOG" | head -1)"
if [ -n "$CODER" ] || [ -n "$SSH_CONNECTION" ]; then
  echo "2. The browser then shows \"127.0.0.1 refused to connect\". Copy that whole address from the address bar and paste it here in the chat right away: it expires in minutes."
fi
```

Give the user every line it prints, word for word: do not rewrite it, shorten it or
replace it with your own advice. Then wait.

On a remote machine (Coder, SSH) the browser cannot reach OpenCode, which waits on this
machine's localhost (OpenCode issue <https://github.com/anomalyco/opencode/issues/54495>),
so the user pastes the failed address to you and you deliver it from here. Do it as soon
as it arrives (replace `<name>` and the address; nothing else):

```bash
NAME=<name>
CB='<the address the user pasted>'
LOG="${TMPDIR:-/tmp}/mcp-auth-$NAME.log"
case "$CB" in
  http://127.0.0.1:*/callback\?*|http://localhost:*/callback\?*)
    curl -s -o /dev/null --max-time 15 "$CB"
    for i in $(seq 1 15); do grep -q -E 'Connected to|Failed' "$LOG" && break; sleep 1; done
    if grep -q 'Connected to' "$LOG"; then echo "$NAME: signed in"
    else echo "$NAME: sign-in failed (the code expired or was already used): run the sign-in block again"; fi ;;
  *) echo "Not an OpenCode callback address: nothing was sent" ;;
esac
```

It only ever calls a `127.0.0.1`/`localhost` callback. The page that address shows always
says "Authorization successful", even when the sign-in fails, so trust only this block's
last line.

Once signed in, the OpenCode server this chat runs on still shows the server as
`needs authentication` until it reloads. Reload it (no restart, the chat survives) and read
the verdict:

```bash
NAME=<name>
P=$(pgrep -f 'opencode serve --hostname' | head -1)
PORT=$(tr '\0' ' ' </proc/$P/cmdline | grep -o -E 'port [0-9]+' | cut -d' ' -f2)
PW=$(tr '\0' '\n' </proc/$P/environ | sed -n 's/^OPENCODE_SERVER_PASSWORD=//p')
code=$(curl -s -o /dev/null -w '%{http_code}' -u "opencode:$PW" -X POST "http://127.0.0.1:$PORT/api/location/reload")
if [ "$code" != 204 ]; then echo "reload failed (HTTP $code): ask the user before restarting anything"
else
  for i in $(seq 1 10); do
    sleep 1
    st=$(curl -s -u "opencode:$PW" "http://127.0.0.1:$PORT/api/mcp" | python3 -c 'import json,sys; print({m["name"]: m["status"]["status"] for m in json.load(sys.stdin)["data"]}.get(sys.argv[1], "missing"))' "$NAME")
    [ "$st" != missing ] && break
  done
  if [ "$st" = connected ]; then echo "$NAME: connected, done"
  else echo "$NAME: $st. The sign-in did not complete: run the sign-in block again. Do not restart anything."; fi
fi
```

Never print `$PW`. `connected` means done: move on to the next server. A server that is
still `needs_auth` after a reload is a sign-in that did not complete, never a reason to
restart. Only if the reload itself failed, ask the user for a go-ahead, warn them the chat
will reconnect, and restart that server with `kill -HUP $P` (OpenChamber starts it again).

If the user signed in with the browser on their own machine without you (no address to
paste), they can instead forward the port while the sign-in waits, taking `<port>` from
`redirect_uri` in the link (`127.0.0.1%3A<port>`): `coder port-forward <workspace> --tcp
<port>:<port>` on Coder, `ssh -L <port>:localhost:<port> <host>` over SSH. Coder workspaces
may also have an "OAuth Callback" app that takes the failed address.

## MCP servers

Hydrating an Environment (globally or into a folder, as above) writes its MCP servers
into OpenCode's config, enabled. Always run `opencode mcp list` first: a server it lists
is already configured, even when it says `needs authentication`. Only sign in to it
(see "Sign in to MCP servers on first use"); never `opencode mcp add` it again, that
creates a duplicate under another name.

Only for a server `opencode mcp list` does not show, add it by hand by its ACH URL
`{{base}}/mcp/<server>`:

```bash
ach-cli env describe <environment>    # the MCP servers an Environment provides (<server>)
opencode mcp add <server> --url {{base}}/mcp/<server> --global   # drop --global: this project only
opencode mcp auth <server>
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
