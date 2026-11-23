# Find, install and update Environments

An Environment is a named set your platform team publishes: models, MCP
servers and A2A agents, plus plugins, skills, prompts, instructions and artifacts.
Installing one into an agent tool is called hydrating.

You need to be [signed in](sign-in.md). `<tool>` below is one of
`antigravity`, `claude-code`, `codex`, `opencode` or `pi`.

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

Everything under that folder gets the Environment, because Claude Code and
OpenCode read the configuration of each parent folder. Codex and Antigravity read
a parent folder's `.codex/` or `.agents/` only up to the git root: a project that is
a git repository misses the Environment's Instructions (and, in Codex, its MCP
servers), so for those two hydrate inside the project or use `-g`.

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

Skills go to `.claude/skills` for Claude Code and to the shared `.agents/skills`
for Codex, OpenCode and pi (`~/.agents/skills` with `-g`), so those three
tools read one copy. Antigravity reads everything from `.agents/` (MCP servers in
`.agents/mcp_config.json`, skills in `.agents/skills`, subagents in
`.agents/agents`) and from `~/.gemini/config/` with `-g`, its user directory.
It has no command directory, so a plugin's commands are not installed for it.

An Instruction is a Markdown text the tool always loads. Hydrate writes each one
as a rules file:

| Tool | File (in a project) | With `-g` |
|---|---|---|
| Claude Code | `.claude/rules/ach-<name>.md` | `~/.claude/rules/` |
| OpenCode | `.opencode/ach/<name>.md`, plus the absolute path of `.opencode/ach/*.md` in `instructions` of `.opencode/opencode.json` | `~/.config/opencode/ach/` |
| Antigravity | `.agents/rules/ach-<name>.md` (`trigger: always_on`) | `~/.gemini/config/rules/` |
| Pi | `.pi/ach/<name>.md`, plus the extension `.pi/extensions/ach-instructions.ts` | `~/.pi/agent/ach/` and `~/.pi/agent/extensions/` |
| Codex | `.codex/ach/<name>.md`, plus a `SessionStart` hook in `.codex/hooks.json` that prints `.codex/ach/*.md` | `~/.codex/ach/` and `~/.codex/hooks.json` |

The OpenCode entry, the Pi extension and the Codex hook exist while their folder
holds an Instruction file and go when the last one goes; other hooks in
`hooks.json` stay. Pi loads a project extension only in a project it trusts.
Codex runs a hook only after you approve it: Codex asks when it starts (or use
`/hooks`), and `codex exec` skips a hook nobody approved. A project hook also
needs a trusted project. Approval holds while the folder stays put, so a changed
or added Instruction needs none. In a folder per Environment, Claude Code and
OpenCode load the Instructions in every project below it; Codex and Antigravity
in every folder below it up to the git root; Pi only when started in that folder. An Instruction the Environment drops is removed on
the next hydrate. Antigravity caps all always-on rules together at about
20,000 tokens.

A Prompt is a different thing: it is served to whoever names it, for example an
agent whose system prompt is that Prompt, and `ach-cli` does not write it into a
tool.

OpenCode loads MCP servers when its service starts. After a hydrate, an
`environment uninstall` (or a `local plugin` install, update or uninstall) that
adds or removes one, run `opencode service restart`.

Hydrate adds the folders it writes (`.ach/`, `.claude/`, `.agents/`, …) to a
`.gitignore` in that folder, whole: they can hold your key. Skills your team
commits under `.agents/skills` then need `git add -f`.

## Plugins your tool installs itself

An administrator can mark a plugin so that your tool's own plugin system loads
it. The plugin then keeps everything it ships, hooks and local MCP servers
included, instead of being split into files. Hydrate lists it like any plugin
(`+ plugin  ach-memory`) and may print a note:

| Tool | What hydrate does | What you do |
|---|---|---|
| Claude Code | registers the folder `.ach/<env>/native/claude-code/` in `.claude/settings.json` | trust the folder; the note reads `Claude Code loads <plugin> once you trust this folder` |
| pi | adds the plugin folder to `packages` in `.pi/settings.json` | start pi with `pi -a`; the note reads `pi loads <plugin> only in a trusted project (pi -a)` |
| OpenCode | adds the plugin folder to `plugins` in `.opencode/opencode.json` | run `opencode service restart`; the note reads `OpenCode loads <plugin> when its service starts (opencode service restart)` |
| Codex | runs `codex plugin marketplace add` and `codex plugin add` | approve the plugin's hooks when Codex asks; the note reads `Codex installs plugins for your user: <plugin> is active in every folder, and Codex may ask you to approve its hooks` |
| Antigravity | copies the plugin to `.agents/plugins/<plugin>/` | nothing |

Codex installs for your whole user, whatever the hydrate scope. With `--global`,
Antigravity's copy goes to `~/.gemini/config/plugins/<plugin>/`. Claude Code does
not list the plugin in `claude plugin list`: it reads it from the folder under
`.ach/`. Claude Code trusts each folder on its own (trusting a parent folder does not count) and skips the plugin without a word in a folder it does not trust; `ach-cli environment status` and `ach-cli launch claude` warn when that is the case.

A tool the plugin ships no manifest for gets a warning and nothing else:
`plugin <name> uses native delivery and carries no <tool> plugin manifest: not
installed`. When a tool's own command is not installed (Codex without `codex`
on your PATH), hydrate installs nothing for that tool and prints `plugin <name>
not installed for codex: codex: not found on PATH`; the next hydrate tries again.

`ach-cli environment uninstall <environment>` unregisters the plugin and removes
its folder. What the plugin wrote on its own outside that folder, for example a
server entry in pi's `mcp.json`, stays.

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

Hydrate does not replace a file of your own either. When a plugin or skill
would write a file that already exists with other content and that hydrate did
not install, it stops, names the file and exits 7. Move your file away, or
pass `--force` to replace it.

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
`.agents/mcp_config.json`, `.codex/config.toml`, `.opencode/opencode.json`) only its own
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
