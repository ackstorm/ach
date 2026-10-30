# Install, update and remove an Environment

## Globally: every project gets it

```bash
ach-cli env status <environment> -g                         # already there?
ach-cli env hydrate <environment> -g --target <tool>         # e.g. --target claude-code
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
cd ~/Projects && ach-cli env clone <environment> --target claude-code   # creates ~/Projects/<environment>
```

`env clone` works like `git clone`: it creates the folder (default: the Environment
name; a second argument picks another), refuses one that exists and is not empty,
hydrates into it, and removes the folder again if that fails. Every project under that
folder picks the Environment up, git repo or not: Claude Code, Codex and OpenCode read
the config of each parent folder of the directory they start in. Hydrate also adds its
own folders (`.ach/`, `.claude/`, …) to a `.gitignore` there.

To hydrate into the folder the user is in now ("install finops here"), run
`ach-cli env hydrate <environment> --target <tool>` without `--dir`. Confirm the folder
with the user first. Avoid hydrating two Environments into the same folder chain unless
the user wants both merged in one tool.

Several tools at once: `--target claude-code,opencode`.

## Keep it up to date

```bash
cd ~/Projects/<environment> && ach-cli env hydrate          # every Environment recorded here, with its targets
ach-cli env hydrate <environment> --sync                    # also remove what the Environment dropped
ach-cli env hydrate <environment> --dry-run                 # preview the diff, write nothing
```

Hydrate prints a per-item diff framed by `Hydrating …` / `Finished …`: `+` added,
`~` changed, `-` removed, `!` kept because the user edited it. A file the user edited is
never overwritten silently: hydrate stops with exit code 2. Show the user which file,
then either keep their edit (do nothing) or, only on their say-so, `--force`.

## One plugin or skill only

```bash
ach-cli env hydrate <environment> --only plugin/<name>      # or skill/<name>
ach-cli env uninstall <environment> --only plugin/<name>
```

`--only` installs that one item from the Environment and merges it into what is already
there; it writes no MCP config.

## See what is installed

```bash
ach-cli env status                          # this folder: every hydrated Environment
ach-cli env status <environment> -g         # the global install
ach-cli env status --files                  # every file hydrate wrote, with its path
```

`env status` reads local state only; it makes no network call.

## Remove it

```bash
ach-cli env uninstall <environment> --dry-run    # preview
ach-cli env uninstall <environment>              # this folder (-g for the global install)
```

Only what hydrate wrote is removed. In shared files (`.mcp.json`,
`.claude/settings.json`, `.codex/config.toml`, `.opencode/opencode.json`, `CLAUDE.md`)
only ACH's own keys or marked blocks go; the user's settings stay. `--only-runtime`
removes just the MCP and A2A entries.

## Don't edit the tool's config while hydrate runs

Hydrate reads the tool's config file, merges its entries in and writes it back. An editor
save in between is lost (the next hydrate re-adds ACH's part, not the user's edit). Let
hydrate finish, then edit.

## Model traffic through ACH

```bash
ach-cli env hydrate <environment> --target claude-code --models
```

Wires Claude Code (an `apiKeyHelper` + base URL) or Codex (a model provider) to send its
model requests through ACH, billed to the user's ACH budget. Needs `ach-cli` on the
`PATH`, because the tool calls it for a fresh token. Without `--models` the tool keeps
its own login.
