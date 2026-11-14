# Find, install and update Environments

An Environment is a named set your platform team publishes: models, MCP
servers and A2A agents, plus plugins, skills, prompts and artifacts.
Installing one into an agent tool is called hydrating.

You need to be [signed in](sign-in.md). `<tool>` below is one of
`claude-code`, `codex`, `gemini-cli`, `opencode` or `pi`.

## Find an Environment

```bash
ach-cli environment list                    # the Environments your teams may use
ach-cli environment describe <environment>  # what one contains
ach-cli environment search <text>           # which Environment has a plugin, skill, MCP server…
```

`search` matches the name and description of each Environment and the name of
everything it serves, ignoring case. Several words must all match. It does not
search inside a skill or a plugin.

All three take `-o json`.

## Install one for every project

```bash
ach-cli environment hydrate <environment> -g --target <tool>
```

`-g` writes into the tool's own global configuration, so every project on the
machine gets the Environment. This suits an organisation-wide baseline.

## Install one into a folder

```bash
cd ~/Projects
ach-cli environment clone <environment> --target <tool>
```

`clone` creates `./<environment>`, hydrates into it, and removes the folder
again if that fails. It refuses a folder that exists and is not empty. A second
argument names another folder.

Everything under that folder gets the Environment, because Claude Code, Codex
and OpenCode read the configuration of each parent folder:

```
~/Projects/
  <environment>/      one folder per Environment
    <project>/        your work; a git repository or not
```

To hydrate into the folder you are in instead:

```bash
ach-cli environment hydrate <environment> --target <tool>
```

`--target` takes several tools (`--target claude-code,opencode`). Inside a
folder that already holds one tool's configuration you may leave it out.

Hydrate adds the folders it writes (`.ach/`, `.claude/`, …) to a `.gitignore`
in that folder.

## Update

```bash
cd ~/Projects/<environment>
ach-cli environment hydrate                       # every Environment recorded here
ach-cli environment hydrate <environment> --sync      # also remove what the Environment dropped
ach-cli environment hydrate <environment> --dry-run   # show the changes, write nothing
```

Hydrate lists what it changed, one line per item: `+` added, `~` changed,
`-` removed. After eight lines it prints a count; `--verbose` shows them all.

Hydrate does not overwrite a file you edited: it stops, names the file and
exits 2. Keep your edit, or pass `--force` to take the Environment's version.
`environment status` marks such files with `! edited`.

Do not edit the tool's configuration while hydrate runs: hydrate reads the
file, merges its entries and writes it back, so a save in between is lost.

## Install one plugin or skill

```bash
ach-cli environment hydrate <environment> --only plugin/<name>   # or skill/<name>
```

It adds that item to what is already installed and writes no MCP
configuration.

## See what is installed

```bash
ach-cli environment status                    # this folder
ach-cli environment status <environment> -g   # the global install
ach-cli environment status --files            # every file hydrate wrote
ach-cli environment status --offline          # without asking the server
```

`status` lists what is installed and the files you edited, then asks the
server whether each Environment changed since you hydrated it: `up to date`,
or `+` added, `-` removed, `~` new content. It downloads nothing.

## Remove

```bash
ach-cli environment uninstall <environment> --dry-run   # show what would go
ach-cli environment uninstall <environment>             # this folder; -g for the global install
```

Only what hydrate wrote is removed. In files it shares with you (`.mcp.json`,
`.codex/config.toml`, `.opencode/opencode.json`, `CLAUDE.md`) only its own
entries go. `--only plugin/<name>` removes one item; `--only-runtime` removes
just the MCP entries.

## An Environment is not listed

You do not have access to it. Signing in again does not help: a platform
admin grants it by adding one of your teams to that Environment. If you were
just added to a single sign-on group, run `ach-cli refresh` and wait a few
minutes.

## Related

- [Start a tool on an Environment](launch-a-tool.md) starts the tool, hydrating
  it first when asked, in one command.
- [Sign in to an MCP server](sign-in-to-an-mcp-server.md) for the servers an
  Environment brought.
- [`ach-cli environment`](../reference/cli.md#ach-cli-environment) in the
  command reference.
