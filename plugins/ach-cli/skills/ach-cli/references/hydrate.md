# Install, update and remove an Environment

## Globally: every project gets it

```bash
ach-cli environment status <environment> -g                         # already there?
ach-cli environment hydrate <environment> -g --target <tool>         # e.g. --target claude-code
```

Use this for the organization's baseline Environment. It writes into the tool's global
config (`~/.claude/`, `~/.codex/`, `~/.config/opencode/`, …).

## One folder per Environment

Recommended layout:

```
<workspace>/            e.g. ~/Projects (the user picks it)
  <environment>/        one folder per Environment
    <project>/          the actual work; may or may not be its own git repo
```

```bash
cd ~/Projects && ach-cli environment clone <environment> --target claude-code   # creates ~/Projects/<environment>
```

`environment clone` works like `git clone`: it creates the folder (default: the Environment
name; a second argument picks another), refuses one that exists and is not empty,
hydrates into it, and removes the folder again if that fails. Every project under that
folder picks the Environment up, git repo or not: Claude Code, Codex and OpenCode read
the config of each parent folder of the directory they start in. Hydrate also adds its
own folders (`.ach/`, `.claude/`, …) to a `.gitignore` there.

To hydrate into the folder the user is in now ("install finops here"), run
`ach-cli environment hydrate <environment> --target <tool>` without `--dir`. Confirm the folder
with the user first. Avoid hydrating two Environments into the same folder chain unless
the user wants both merged in one tool.

Several tools at once: `--target claude-code,opencode`.

## Keep it up to date

```bash
cd ~/Projects/<environment> && ach-cli environment hydrate          # every Environment recorded here, with its targets
ach-cli environment hydrate <environment> --sync                    # also remove what the Environment dropped
ach-cli environment hydrate <environment> --dry-run                 # preview the diff, write nothing
```

Hydrate prints a per-item diff framed by `Hydrating …` / `Finished …`: `+` added,
`~` changed, `-` removed, `!` kept because the user edited it. A file the user edited is
never overwritten silently: hydrate stops with exit code 2. Show the user which file,
then either keep their edit (do nothing) or, only on their say-so, `--force`.

## Hydrate and start the tool in one step

```bash
ach-cli launch claude --environment <environment>                  # or codex, gemini, opencode, pi
ach-cli launch opencode                                            # the Environments already in this folder
ach-cli launch codex --environment <environment> --dir ~/work/infra -- --model <model>
```

`launch` runs the same hydrate for that one tool (same flags as `environment hydrate`, minus
`--target`, `--only` and `--dry-run`), then starts the tool in `--dir` (default: the
current folder) and exits with the tool's exit code. With no `--environment` and no
`ACH_ENVIRONMENT`, every Environment already hydrated in that folder (`.ach/`) is updated
for the tool; a global hydrate does not count. Nothing starts when the tool is not on the
`PATH`, there is no Environment to hydrate, or the hydrate fails. Arguments after the tool
name go to it; its own flags go after `--`.
It takes over the terminal: tell the user to run it themselves, never run it yourself.

## One plugin or skill only

```bash
ach-cli environment hydrate <environment> --only plugin/<name>      # or skill/<name>
ach-cli environment uninstall <environment> --only plugin/<name>
```

`--only` installs that one item from the Environment and merges it into what is already
there; it writes no MCP config.

## See what is installed

```bash
ach-cli environment status                          # this folder: every hydrated Environment
ach-cli environment status <environment> -g         # the global install
ach-cli environment status --files                  # every file hydrate wrote, with its path
```

`environment status` reads local state only; it makes no network call.

## Remove it

```bash
ach-cli environment uninstall <environment> --dry-run    # preview
ach-cli environment uninstall <environment>              # this folder (-g for the global install)
```

Only what hydrate wrote is removed. In shared files (`.mcp.json`,
`.claude/settings.json`, `.codex/config.toml`, `.opencode/opencode.json`, `CLAUDE.md`)
only ACH's own keys or marked blocks go; the user's settings stay. `--only-runtime`
removes just the MCP entries.

An Environment's A2A agents are not written into any tool's config: no supported tool
reads an A2A entry there. `environment describe` still lists them.

## Don't edit the tool's config while hydrate runs

Hydrate reads the tool's config file, merges its entries in and writes it back. An editor
save in between is lost (the next hydrate re-adds ACH's part, not the user's edit). Let
hydrate finish, then edit.

## Model traffic through ACH

```bash
ach-cli environment hydrate <environment> --target claude-code --models
```

Wires Claude Code (an `apiKeyHelper` + base URL) or Codex (a model provider) to send its
model requests through ACH, billed to the user's ACH budget. Needs `ach-cli` on the
`PATH`, because the tool calls it for a fresh token. Without `--models` the tool keeps
its own login.
