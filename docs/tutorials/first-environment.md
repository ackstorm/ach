# Your first Environment

In this tutorial you install `ach-cli`, sign in to your organisation's ACH,
put an Environment into a folder and start Claude Code on it. It takes about
ten minutes.

You need:

- The URL of your ACH, for example `https://ach.example.com`. Your platform
  team has it.
- An Environment your team may use. The examples call it `quickstart`; use a
  name from your own list in step 3.
- Linux or macOS, with [Claude Code](https://claude.com/claude-code) installed.

On Windows, or with another tool, follow the same steps with
[Install or update ach-cli](../how-to/developers/install-ach-cli.md) and
[Find, install and update Environments](../how-to/developers/use-environments.md).

## 1. Install ach-cli

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/ackstorm/ach/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac
FILE="ach-cli_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/${FILE}"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/checksums.txt"
grep " ${FILE}\$" checksums.txt | if command -v sha256sum >/dev/null; then sha256sum -c -; else shasum -a 256 -c -; fi
tar -xzf "$FILE" ach-cli
mkdir -p ~/.local/bin && mv ach-cli ~/.local/bin/
```

The checksum line prints `OK`. Check the install:

```bash
ach-cli --version
```

If the shell does not find `ach-cli`, add `~/.local/bin` to your `PATH`.

## 2. Sign in

```bash
ach-cli login https://ach.example.com
```

Press Enter to accept the profile name `default`, then choose `1` to open the
browser on this machine. Sign in with your organisation account. The terminal
continues when the browser says you are signed in.

```bash
ach-cli whoami
```

`whoami` prints your email after `User` and `session` after `Auth`.

## 3. Look at your Environments

```bash
ach-cli environment list
```

The list holds the Environments your teams may use. Pick one, then look at
what it contains:

```bash
ach-cli environment describe quickstart
```

`describe` prints the Environment's runtime (models, MCP servers, A2A agents)
and its context (plugins, skills, prompts, artifacts).

## 4. Put it in a folder

```bash
mkdir -p ~/Projects && cd ~/Projects
ach-cli environment clone quickstart --target claude-code
```

`clone` creates `~/Projects/quickstart` and hydrates the Environment into it:
it writes the Environment's plugins and skills under `.claude/`, its MCP
servers into `.mcp.json`, and a record of what it wrote under `.ach/`. It
lists what it added, each line starting with `+`, and ends with `Finished`.

## 5. Start Claude Code on it

```bash
cd ~/Projects/quickstart
ach-cli launch claude
```

`launch` brings the folder up to date with the Environment and then starts
Claude Code in it. Inside Claude Code, type `/` to see the commands the
Environment's plugins added, and `/mcp` to see its MCP servers. Leave Claude
Code to return to the shell.

Any project you create under `~/Projects/quickstart` gets the same
Environment.

## 6. Check and update

```bash
ach-cli environment status
```

`status` lists what is installed in this folder and ends with `up to date`.
When your platform team changes the Environment, `status` shows what changed,
and this brings the folder up to date:

```bash
ach-cli environment hydrate
```

## 7. Remove it

```bash
ach-cli environment uninstall quickstart
```

`uninstall` removes what hydrate wrote and leaves your own files. The folder
stays; delete it if you no longer want it.

## Where next

- [Sign in to an MCP server](../how-to/developers/sign-in-to-an-mcp-server.md)
  the Environment brought.
- [Find, install and update Environments](../how-to/developers/use-environments.md):
  a global install, several tools, one plugin only.
- [Create and manage environment keys](../how-to/developers/manage-keys.md)
  for an agent or a CI job.
- [Concepts](../explanation/concepts.md) explains Environments, teams and
  keys.
