# See, search and describe Environments

An Environment is what a tool may use: models, MCP servers, A2A agents (runtime) and
plugins, skills, prompts, artifacts (context).

```bash
ach-cli environment list                       # the Environments your teams may use
ach-cli environment describe <environment>     # what one contains
ach-cli environment search <text>              # find a plugin, skill, MCP server… across all of them
```

- `environment search finops` matches each Environment's name and description and the NAME of
  everything it serves (plugins, skills, prompts, artifacts, MCP servers, models, A2A
  agents), case-insensitively; several words must all match. It searches names only, not
  what a skill or plugin says inside.
- `environment describe --metadata-only` skips the content lookup and is faster when you only
  need the description and runtime.
- `-o json` on `environment list`, `environment search` and `environment describe` gives output you can parse.
  `environment describe -o json` prints `{"environment","runtime","context"}`: the environment's
  `name`, `namespace`, `status`, `description` and `conditions` (`[]` when none), then the
  runtime and context lists the table shows. `runtime` and `context` are `null` when the
  content lookup is unavailable or skipped with `--metadata-only`.

If the Environment the user needs is not listed, they do not have access to it. Signing
in again will not help: a platform administrator grants it by adding one of the user's
teams to that Environment. Administrators can see every Environment with
`environment list --admin` (others get `403 not_admin`).

A user can belong to several Environments: grants add up. A common shape is one
organization-wide baseline installed globally plus one team or project Environment per
folder.
