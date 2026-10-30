# PluginMarketplace and SkillMarketplace

A marketplace is one upstream that yields many plugins or skills. Environments then
reference each item as `<item-name>@<marketplace metadata.name>`. Use a marketplace when
a repo holds several plugins or skills; use a standalone `Plugin` or `Skill`
(`objects.md`) for a single one.

Both kinds take the shared source block (`content-sources.md`).

## PluginMarketplace

```yaml
# The organization's own plugins (private GitLab). Granted through
# context.plugins in environments/global.yaml.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: PluginMarketplace
metadata:
  name: my-org-plugins
spec:
  type: gitlab
  gitlab:
    host: git.example.com
    project: my-org/agent-plugins
    ref: main
    authSecretRef:
      name: git-read-token
      key: token
  refresh:
    interval: 1h
    maxStaleness: 24h
---
# Anthropic's public catalogue, narrowed to the code-* plugins.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: PluginMarketplace
metadata:
  name: anthropic-code
spec:
  type: github
  github:
    repo: anthropics/claude-plugins-official
    ref: main
  refresh:
    interval: 6h
    maxStaleness: 72h
  filters:
    include:
      - "code-.*"
    exclude:
      - "code-experimental$"
```

Referenced from an Environment as `code-review@anthropic-code`.

### How it is read

- **Git types** fetch the whole repo and use the first `.claude-plugin/marketplace.json`
  found. `path` is ignored. This is the Claude Code marketplace format, so the same repo
  works with `claude plugin marketplace add`.
- **`s3` / `gcs` / `http`** must point straight at the `marketplace.json` file.

Each `plugins[]` entry's `source` is then fetched on its own:

| `source` form | Fetched from |
|---------------|--------------|
| a string such as `"./plugins/code-review"` | that directory of the marketplace's own repo |
| `{"source": "github", "repo": "owner/name", "ref": ...}` | that GitHub repo |
| `{"source": "url", "url": "https://...git", "path": ...}` | that git URL (subdirectory when `path` is set) |
| `{"source": "git-subdir", "url": ..., "path": ..., "ref": ...}` | that subdirectory of that git URL |
| anything else (e.g. `npm`) | skipped with `UnsupportedPluginSource` |

The marketplace's `authSecretRef` is reused for every entry's git fetch, so entries that
live in other private repos need a token that can read them too.

### What a valid plugin directory needs

The entry's source must resolve to the **plugin root**. It is accepted when it has
`.claude-plugin/plugin.json` **or** at least one Claude Code component at its root:
`commands/`, `agents/`, `skills/`, `hooks/`, `output-styles/`, `themes/`, `monitors/`,
`SKILL.md`, `.mcp.json` or `.lsp.json`. The archive must also be safe to unpack: no
`..` paths, absolute paths, hardlinks, device files or symlinks pointing outside the
tree.

A bad entry does not fail the marketplace: `Synced` stays True, the good plugins keep
serving, and the condition message says `stage-2: <N> plugin(s) failed: ...` with the
reason (`UpstreamInvalid`, `UnsupportedPluginSource`). A name listed twice in one
`marketplace.json` keeps the first entry (`DuplicateName` in the message).

### filters

`filters.include` and `filters.exclude` are RE2 patterns matched against plugin names
and anchored at the start (`^` is prepended for you; add `$` yourself to match the whole
name). Include runs first, exclude wins on conflict. An include list that matches zero
plugins fails the marketplace (`Synced=False`, `UpstreamInvalid`); a pattern that does not
compile fails it with `InvalidConfig`.

Filtering is optional. Without it every plugin in the catalogue is cached, but users only
get the ones an Environment lists, so filters mostly save fetch time and cache space.

### Status

`kubectl get pluginmarketplaces` shows `Reachable` (`SourceReachable`), `Synced` and the
`Plugins` count. `status.plugins[]` lists each materialized `name` and `upstreamRev`.

## SkillMarketplace

```yaml
# Anthropic's public skills repo: every skills/<name>/SKILL.md becomes <name>@anthropic-skills.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: SkillMarketplace
metadata:
  name: anthropic-skills
spec:
  type: github
  github:
    repo: anthropics/skills
    ref: main
    path: skills
  refresh:
    interval: 6h
    maxStaleness: 72h
```

Referenced from an Environment as `pdf@anthropic-skills`.

### How skills are discovered

There is no index file. The operator fetches the whole repo and walks the directory named
by `path`:

- `path` unset means `skills`. That fits the common `skills/<name>/SKILL.md` layout; set it
  explicitly when the skills live elsewhere (`skills/core-skills`), or to `"."` when the
  skill folders sit at the repo root.
- Only **direct children** of that directory count: `<path>/<dir>/SKILL.md`.
- A child is a skill when its `SKILL.md` has YAML frontmatter with `name` equal to the
  folder name, `name` in lowercase kebab-case (`^[a-z0-9]+(-[a-z0-9]+)*$`, up to 64
  chars), and a `description` of 1-1024 characters. Anything else is skipped silently.
- For `s3` / `gcs` / `http`, the source must be a ready-made `.tar.gz` of that layout.

A wrong `path` gives `Synced=True` with `Skills` 0. Check the count after every new
marketplace: `kubectl get skillmarketplaces -n <ns>` or `ach-cli admin list skills`.

To serve two subtrees of one repo (e.g. `skills/core` and `skills/specialized`), create
two SkillMarketplaces with different `path` and names.

`filters` exists in the schema but the operator does not apply it to skill
marketplaces; every discovered skill is cached. Users still only get the skills an
Environment lists.

### Status

`kubectl get skillmarketplaces` shows `Reachable`, `Synced` and `Skills`;
`status.skills[]` lists the discovered names.

## Naming and references

- The marketplace's `metadata.name` is the suffix users and Environments see
  (`<item>@<name>`), so pick a stable, descriptive name. Renaming it breaks every
  Environment that references it.
- Two marketplaces may expose the same item name; `x@a` and `x@b` are independent.
- A bare name in `context.plugins` never searches marketplaces; it only matches a
  standalone `Plugin` CR. Same for skills.
- An Environment that lists `x@m` stays not Available until `m` has fetched `x` at least
  once.
