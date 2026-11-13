# Start a tool on an Environment

`ach-cli launch` hydrates an Environment for one tool and then starts that
tool, so the tool always opens on the current version of the Environment.

You need to be [signed in](sign-in.md), and the tool must be installed and on
your `PATH`.

| `launch` name | Tool |
|---|---|
| `claude` | Claude Code |
| `codex` | Codex |
| `gemini` | Gemini CLI |
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
ach-cli launch opencode
```

Without `--environment`, `launch` updates every Environment already hydrated
in that folder. An Environment installed globally with `-g` does not count.

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

## Related

- [`ach-cli launch`](../../reference/cli.md#ach-cli-launch) in the command
  reference: `launch` takes the flags of `environment hydrate` except
  `--target`, `--only`, `--dry-run` and `--allow-symlinks`.
- [Find, install and update Environments](use-environments.md).
