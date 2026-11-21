# Start a tool on an Environment

`ach-cli launch` starts one tool, hydrating an Environment for it first when
you ask, so the tool opens on the current version of the Environment.

You need to be [signed in](sign-in.md), and the tool must be installed and on
your `PATH`.

| `launch` name | Tool |
|---|---|
| `agy` | Antigravity CLI |
| `claude` | Claude Code |
| `codex` | Codex |
| `cursor` | Cursor (starts `cursor-agent`) |
| `opencode` | OpenCode |
| `pi` | pi |

## Start the tool

```bash
cd ~/Projects/my-project
ach-cli launch claude --environment <environment>
```

`launch` hydrates `<environment>` into the current folder for Claude Code,
starts `claude` there, and exits with the tool's exit code. `ACH_ENVIRONMENT`
is the same as `--environment`.

## Start it on what the folder already has

```bash
cd ~/Projects/<environment>
ach-cli launch opencode                          # as the folder is
ach-cli launch opencode --update-environments    # update its Environments first
```

Without `--environment`, `launch` starts the tool without hydrating.
`--update-environments` (or `--update-envs`) first updates every Environment
already hydrated in that folder. An Environment installed globally with `-g`
does not count.

`launch opencode` starts the tool but does not restart an OpenCode service that
is already running. After a hydrate that changes MCP servers, run
`opencode service restart`.

## Start it in another folder

```bash
ach-cli launch codex --environment <environment> --dir ~/work/infra
```

## Pass arguments to the tool

Put the tool's own flags after `--`:

```bash
ach-cli launch codex --environment <environment> -- --model <model>
```

## Nothing starts

`launch` does not start the tool when:

- the tool is not on your `PATH`;
- there is no Environment to hydrate: none named and none in the folder;
- the hydrate fails. The message and exit code are those of
  [`environment hydrate`](troubleshoot.md#exit-codes).

## Verify

Inside the tool, the Environment's skills, plugins and MCP servers are
listed. From a second terminal in the same folder:

```bash
ach-cli environment status
```

It shows the Environment, the tool as its target, and `up to date`.

## Cursor asks to approve MCP servers

Cursor's CLI asks you to approve a project MCP server before it uses it. Approve
the Environment's servers with `cursor-agent mcp enable <name>`.

## Related

- [`ach-cli launch`](../reference/cli.md#ach-cli-launch) in the command
  reference: `launch` takes the flags of `environment hydrate` except
  `--target`, `--only`, `--dry-run` and `--allow-symlinks`.
- [Find, install and update Environments](use-environments.md).
