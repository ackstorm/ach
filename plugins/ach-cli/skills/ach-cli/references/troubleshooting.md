# Errors, exit codes, admin commands

## Exit codes (`env hydrate`, `env clone`, most network commands)

| Code | Meaning | Do |
|---|---|---|
| 0 | success | |
| 1 | usage or credential error | Read the message; `ach-cli whoami` to check the sign-in |
| 2 | local edits would be lost | Show the user the file; keep it, or `--force` on their say-so |
| 3 | not authorized | No access to that Environment or command: an administrator grants it |
| 4 | wrong Environment for this key | An `ek-` works for its own Environment only |
| 5 | state mismatch | The `.ach/` state does not match the files; `env status`, then hydrate again |
| 6 | network error | Check the URL in `ach-cli whoami` and the connection; retry |
| 7 | file-name collision | Two items write the same file; see `--conflict` |
| 8 | config read/write error | `~/.config/ach/config.yaml` or the tool's config is unreadable or not writable |

## Common situations

- **`ach-cli whoami` says not signed in, or a command returns 401**: sign in again
  ([login](login.md)). A session also ends when the user is disabled at the identity
  provider.
- **An Environment is missing from `env list`**: no access ([environments](environments.md)).
- **Hydrate says another hydrate holds the lock**: one is still running in that folder.
  Wait for it, or pass `--lock-timeout 2m`.
- **Hydrate cannot detect the tool**: pass `--target`.
- **Two plugins write the same file**: by default the second is namespaced with its
  plugin name. `--conflict skip|overwrite|refuse` changes that.
- **Budget exceeded**: `ach-cli whoami` shows spend against the ceiling. Only an
  administrator raises a user's ceiling; a key's own cap is `ach-cli keys budget`.
- **`--verbose`** prints the request headers (credentials redacted): useful to see which
  URL and profile a command really used.

## Admin commands

Only for users on the platform's admin allowlist; everyone else gets `403 not_admin`
(exit 3). They never work with an `ek-` key.

```bash
ach-cli admin list <kind|all>              # environments, plugins, skills, marketplaces, bips, models, mcp, a2a, teams, guardrails…
ach-cli admin refresh <kind> <name>        # re-fetch a plugin, skill, prompt, artifact or marketplace now
ach-cli admin keys list --owner <email>    # every key, any owner
ach-cli admin keys revoke <id> | --owner <email>
ach-cli admin users budget <email> --max-budget <USD> --budget-duration 30d
ach-cli admin users limits <email> --max-keys <N>
ach-cli env list --admin                   # every Environment, not only your teams'
```

`admin list` shows sync status per object (`Synced`, `Degraded(<reason>)`, `STALE`),
the first place to look when an Environment's plugin or skill does not show up after a
change. Writing or changing those objects is done in the platform's GitOps repo, not
with `ach-cli`.
