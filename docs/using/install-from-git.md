# Install a plugin or skill from a git repository

`ach-cli local` installs a plugin or a skill straight from a git repository
into a tool. It uses no Environment and no sign-in, so nothing it installs is
governed, audited or shared with your team. Use it to try something. For
anything a team relies on, ask a platform admin to publish it in an
Environment and [hydrate that](use-environments.md).

You need [`ach-cli`](install-ach-cli.md) and `git` 2.31 or later on your
`PATH`.

## Register the repository

```bash
ach-cli local repo add github:owner/repo --name <repo>
ach-cli local repo add git:https://git.example.com/group/repo.git#main --name <repo>
ach-cli local repo list
```

| Part | Meaning |
|---|---|
| `github:owner/repo[#ref]` | A GitHub repository |
| `git:https://host/path[.git][#ref]` | Any other git host |
| `#ref` | Branch, tag or commit; `main` when left out |
| `--name` | Required. The name you use for the repository afterwards |
| `--path <dir>` | For a skills repository: the folder that holds the `<skill>/SKILL.md` directories |

Local paths are not supported.

For a private repository pass `--token`, or set `GITHUB_TOKEN` (for
`github:`) or `GITLAB_TOKEN` (for `git:` on a GitLab host: one whose name
contains `gitlab` or starts with `git.`). A `git:` source on any other host
takes its token from `--token` only. Some GitLab setups need
`--auth basic-oauth2`.

## Install

```bash
ach-cli local plugin install <plugin>@<repo> --target claude-code
ach-cli local skill install <skill>@<repo> --target codex,opencode
```

`--target` is one or more of `antigravity`, `claude-code`, `codex`, `cursor`, `opencode`
and `pi`. The install goes into the current folder; `-g` puts it in the
tool's global configuration. `--dry-run` prints the plan and writes nothing.

An install does not replace a file of your own: when a destination already
exists with other content and no installed plugin or skill put it there, the
command names the file, does not install that plugin or skill and exits 7.
Move your file away, or pass `--force` to replace it.

## Verify

```bash
ach-cli local plugin list     # or: ach-cli local skill list
```

The item is listed, and the tool shows it the next time it starts. OpenCode
loads MCP servers when its service starts: when a plugin install, update or
uninstall for `opencode` adds or removes an MCP server, the command prints a
hint to run `opencode service restart`.

## Update and remove

```bash
ach-cli local plugin outdated                 # compare with the source; changes nothing
ach-cli local plugin update                   # all, or <plugin>@<repo>
ach-cli local plugin uninstall <plugin>@<repo>
ach-cli local repo remove <repo>
```

`ach-cli local skill` has the same commands.

`update` leaves a file you edited as it is and reports it as skipped; the
rest of the plugin or skill is updated. Like `install`, it stops with exit 7
on a file of your own that the new version would replace, before it touches
that plugin or skill, unless you pass `--force`.

## Related

- [`ach-cli local`](../reference/cli.md#ach-cli-local) in the command
  reference.
