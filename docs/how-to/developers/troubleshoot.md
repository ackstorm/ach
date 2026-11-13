# Troubleshoot ach-cli

Start with these two; both only read:

```bash
ach-cli whoami               # profile, URL, user, budget
ach-cli environment status   # what is installed in this folder
```

Add `--verbose` to any network command to print the request headers, with
credentials redacted: it shows which URL and profile the command really used.

## Exit codes

`environment hydrate`, `environment clone`, `launch` and most network
commands exit with:

| Code | Meaning | Do |
|---|---|---|
| 0 | Success | |
| 1 | Usage or credential error | Read the message; check the sign-in with `ach-cli whoami` |
| 2 | Local edits would be lost | Keep your edit, or pass `--force` to replace it |
| 3 | Not authorized | You have no access to that Environment or command. A platform admin grants it |
| 4 | Wrong Environment for this key | An `ek-` key works for its own Environment only |
| 5 | State mismatch | The record under `.ach/` does not match the files. Run `environment status`, then hydrate again |
| 6 | Network error | Check the URL in `ach-cli whoami` and your connection; retry |
| 7 | File-name collision | Two items write the same file. See [Two plugins write the same file](#two-plugins-write-the-same-file) |
| 8 | Configuration read or write error | `~/.config/ach/config.yaml` or the tool's configuration cannot be read or written |

`launch` exits with the tool's own code once the tool has started.

## Not signed in, or a command returns 401

Sign in again: `ach-cli login`. A session ends when your organisation's
single sign-on stops accepting it.

## An Environment is missing from the list

You do not have access to it; a platform admin grants it by adding one of
your teams to the Environment. If you were just added to a single sign-on
group, run `ach-cli refresh` and wait a few minutes.

## `ach-cli X is older than the server (Y)`

Run `ach-cli upgrade`. Commands usually keep working until you do. An
`unknown field` error from hydrate has the same cause.

## Another hydrate holds the lock

A hydrate is still running in that folder. Wait for it, or pass
`--lock-timeout 2m` to wait up to that long.

## `refusing to hydrate` or `refusing to uninstall`

A path in the folder is a symbolic link that leads outside it, for example a
`.claude` or `.mcp.json` linked to another directory. Remove the link or
point it inside the folder. Links that stay inside the folder are accepted,
and a global install (`-g`) is exempt. `ach-cli local` refuses the same way.

## Hydrate cannot detect the tool

The folder has no tool configuration yet, or has several. Pass
`--target <tool>`.

## Two plugins write the same file

By default the second one is installed under its plugin name, so both stay.
`--conflict skip`, `--conflict overwrite` or `--conflict refuse` change that.

## A file you edited blocks hydrate

Hydrate exits 2 and names the file. Keep your edit and leave it, or pass
`--force` to replace the file with the Environment's version.

## Budget exceeded

A model request fails with HTTP 422 and a message that names the budget that
was reached:

| In the message | Budget | Who raises it |
|---|---|---|
| `Tag=user:…` | Your own, across everything you use | A platform admin |
| `Tag=environment:…` | The Environment's, shared by all its keys | A platform admin |
| `Tag=key:…` | One key's | You: [`ach-cli keys budget`](manage-keys.md#change-a-keys-budget) |

`ach-cli whoami` shows your own spend and ceiling. Retrying does not help
until the budget is raised or its period resets.

## Related

- [Sign in to an MCP server](sign-in-to-an-mcp-server.md#errors) for MCP
  errors.
- [Command reference](../../reference/cli.md).
