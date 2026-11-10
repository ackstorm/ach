# Errors, exit codes, admin commands

## Exit codes (`environment hydrate`, `environment clone`, most network commands)

| Code | Meaning | Do |
|---|---|---|
| 0 | success | |
| 1 | usage or credential error | Read the message; `ach-cli whoami` to check the sign-in |
| 2 | local edits would be lost | Show the user the file; keep it, or `--force` on their say-so |
| 3 | not authorized | No access to that Environment or command: an administrator grants it |
| 4 | wrong Environment for this key | An `ek-` works for its own Environment only |
| 5 | state mismatch | The `.ach/` state does not match the files; `environment status`, then hydrate again |
| 6 | network error | Check the URL in `ach-cli whoami` and the connection; retry |
| 7 | file-name collision | Two items write the same file; see `--conflict` |
| 8 | config read/write error | `~/.config/ach/config.yaml` or the tool's config is unreadable or not writable |

## Common situations

- **`ach-cli whoami` says not signed in, or a command returns 401**: sign in again
  ([login](login.md)). A session ends when the identity provider refuses the refresh with a
  dead token; a user who stops refreshing is not detected, and an `ek-` key of a disabled user
  stays alive until an admin runs `ach-cli admin keys revoke --owner <email>`.
- **An Environment is missing from `environment list`**: no access ([environments](environments.md)).
  If the user was just added to an SSO group, run `ach-cli refresh` (or `ach-cli login`)
  and allow a few minutes for the Environment to reconcile.
- **`warning: ach-cli X does not match server Y`**: this CLI and the ACH server are on
  different versions. Commands usually keep working; if one fails, update `ach-cli` first
  ([install](install.md)). An `unknown field` error from hydrate has this same cause, and
  older CLIs print no warning, so update on that error too.
- **Hydrate says another hydrate holds the lock**: one is still running in that folder.
  Wait for it, or pass `--lock-timeout 2m`.
- **Hydrate or uninstall fails `refusing to hydrate:` / `refusing to uninstall:`** (exit 1): a path
  in the workspace is a symlink that leaves it. Remove the link or point it inside the folder;
  links that stay inside are fine, and `-g` (global) is exempt from the symlink rule.
  `ach-cli local plugin|skill install|update|uninstall` refuse the same way (`commit:` /
  `uninstall:` ... `leaves the workspace`, exit 1), for example a `.claude` or `.mcp.json`
  linked outside the project.
  `state file tracks "<path>"` (hydrate exit 1, uninstall exit 8) means a recorded target is outside
  the tool's root: restore the config-dir variable used at hydrate time, or remove the state file.
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
ach-cli environment list --admin                   # every Environment, not only your teams'
```

`admin list` shows sync status per object (`Synced`, `Degraded(<reason>)`, `STALE`),
the first place to look when an Environment's plugin or skill does not show up after a
change. Writing or changing those objects is done in the platform's GitOps repo, not
with `ach-cli`.
