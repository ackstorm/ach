# Troubleshoot ach-cli

Start with these two; both only read:

```bash
ach-cli whoami               # profile, URL, user, budget
ach-cli environment status   # what is installed in this folder
```

Every failure prints `error:` and the reason, often followed by a `hint:`
with the command to run. Add `--verbose` to a network command to print the
request line and its headers, with credentials redacted.

## Exit codes

| Code | Meaning | Do |
|---|---|---|
| 0 | Success | |
| 1 | Any other error: wrong usage, a missing or malformed credential, a server that cannot be reached, a refused request | Read the message |
| 2 | Hydrate would overwrite a file you edited | See [A file you edited blocks hydrate](#a-file-you-edited-blocks-hydrate) |
| 3 | Not authorized: the sign-in or key was rejected, or you have no access to that Environment or admin command | Sign in again; if that does not help, a platform admin grants the access |
| 4 | The record under `.ach/` names another Environment than the one you asked for | Hydrate the Environment the folder holds, or pass `--force` |
| 5 | The record under `.ach/`, or the server's answer, has a format this `ach-cli` does not know | Run `ach-cli upgrade`. `--force` rewrites the local record |
| 6 | The server answered that it is unavailable; for `whoami`, `refresh` and `upgrade`, also a server that cannot be reached | Retry |
| 8 | `~/.config/ach/config.yaml`, or a record under `.ach/`, cannot be read or written | Check the file and its permissions |

`launch` exits with the tool's own code once the tool has started.

An `ek-` key used on another Environment than its own fails with
`wrong_environment` and exit 1.

## Not signed in, or a command returns 401

Sign in again, naming your profile:

```bash
ach-cli login https://ach.example.com --profile default
```

A session ends when your organisation's single sign-on stops accepting it.

## An Environment is missing from the list

You do not have access to it; a platform admin grants it by adding one of
your teams to the Environment. If you were just added to a single sign-on
group, run `ach-cli refresh` and wait a few minutes.

## `ach-cli X is older than the server (Y)`

Run `ach-cli upgrade`. Commands usually keep working until you do. An
`unknown field` error from hydrate has the same cause.

## `another ach-cli is running on this workspace`

A hydrate is still running in that folder. Wait for it, or pass
`--lock-timeout 2m` to wait up to that long.

## `refusing to hydrate`, or `path leaves the workspace`

A path in the folder is a symbolic link that leads outside it, for example a
`.claude` or `.mcp.json` linked to another directory. Remove the link or
point it inside the folder. Links that stay inside the folder are accepted,
and a global install (`-g`) is exempt. `environment uninstall` and
`ach-cli local` apply the same rule.

## `no agent target detected`

The folder has no tool configuration yet, or has several. Pass
`--target <tool>`.

## `nothing hydrated in this folder yet`

`environment hydrate` or `launch` ran without an Environment name in a folder
that holds none. Name one, or create the folder with
`ach-cli environment clone <environment>`.

## Two plugins write the same file

By default each file is installed under its plugin's name, so both stay.
`--conflict skip` and `--conflict overwrite` keep one of them;
`--conflict refuse` stops with an error.

## A file you edited blocks hydrate

Hydrate names the file and exits 2. When you update a whole folder with no
Environment name, it reports the failure per Environment and exits 1. Keep
your edit and leave it, or pass `--force` to replace the file with the
Environment's version. `ach-cli environment status` lists the files you
edited.

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
