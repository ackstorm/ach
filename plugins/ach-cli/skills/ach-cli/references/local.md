# Install from a git repo directly (`ach-cli local`)

`ach-cli local` installs plugins and skills straight from a git repository into a tool,
with no Environment, no platform and no sign-in. It is the quick path for trying
something; nothing is governed, audited or shared. For anything a team relies on, ask a
platform administrator to put it in an Environment and hydrate that instead.

```bash
ach-cli local repo add github:owner/repo --name <repo>                 # a marketplace, a plugin or a skills repo
ach-cli local repo add git:https://git.example.com/group/repo.git#main --name <repo>
ach-cli local repo list

ach-cli local plugin install <plugin>@<repo> --target claude-code      # -g for the tool's global config
ach-cli local skill install <skill>@<repo> --target codex,opencode
ach-cli local plugin list                 # what is installed (--repo to filter)
ach-cli local plugin outdated             # compare with the source, read-only
ach-cli local plugin update               # all, or name@repo
ach-cli local plugin uninstall <plugin>@<repo>
```

- A source is `github:owner/repo[#ref]` or `git:https://host/path[.git][#ref]`; the
  default ref is `main`. Local paths are not supported.
- Private repos: `--token`, else `GITHUB_TOKEN` (github:) or `GITLAB_TOKEN` (git:).
  Some GitLab setups need `--auth basic-oauth2`.
- `--path <dir>` on `repo add` points at the folder that holds a skills repo's
  `<skill>/SKILL.md` directories.
- `--dry-run` on install and uninstall prints the plan and writes nothing.
