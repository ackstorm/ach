# Standalone Plugin, Skill, Prompt and Artifact

Each of these kinds is one item fetched from one upstream. An Environment references it
by its bare `metadata.name`. All take the shared source block (`content-sources.md`):
`type`, the matching sub-object, and `refresh` with a required `maxStaleness`.

`kubectl get <kind>` shows `Reachable` (`SourceReachable`); `status.upstreamRev` and
`status.lastSuccessfulRefresh` show what was fetched and when.

## Plugin

A Claude Code plugin: a directory with `.claude-plugin/plugin.json`. Hydrate installs it
into each agent tool, namespacing its commands and skills by the plugin name.

```yaml
# Public third-party plugin, pinned to a tag so updates are a deliberate Git change.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Plugin
metadata:
  name: caveman
spec:
  type: github
  github:
    repo: JuliusBrussee/caveman
    ref: v1.2.0
  refresh:
    interval: 12h
    maxStaleness: 72h
```

- Point `path` at the plugin root when the plugin sits in a subdirectory of a repo.
- A standalone Plugin **requires** `.claude-plugin/plugin.json`; without it the fetch
  fails with `UpstreamInvalid`. (Plugins inside a marketplace are more lenient; see
  `marketplaces.md`.)
- The operator keeps only what agents use: `plugin.json`, root `README.md` and
  `LICENSE`, the convention directories (`commands/`, `agents/`, `skills/`, `hooks/`,
  `mcpServers/`) and any directory the manifest references. Tests, build files and
  other tools' mirrors are dropped.
- The filtered plugin must fit the operator's size cap (50 MiB by default), otherwise
  `PluginTooLarge`.
- A bare name in `context.plugins` resolves only to a Plugin CR. If a marketplace also
  has a plugin with that name, the two never collide: the marketplace one is
  `name@marketplace`.

## Skill

An agent skill (agentskills.io): a directory with `SKILL.md` and optional `scripts/`,
`references/`, `assets/`. Served as-is, no filtering.

```yaml
# One skill out of a monorepo of skills (private GitLab).
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Skill
metadata:
  name: finops-audit
spec:
  type: gitlab
  gitlab:
    host: git.example.com
    project: my-org/aws-tools
    ref: main
    path: skills/finops-audit
    authSecretRef:
      name: git-read-token
      key: token
  refresh:
    interval: 1h
    maxStaleness: 24h
```

- `SKILL.md` must sit at the root of the fetched tree (or one directory below), with YAML
  frontmatter holding `name` (lowercase kebab-case, 1-64 chars) and `description`
  (1-1024 chars). Otherwise `UpstreamInvalid`.
- Name the CR after the skill's frontmatter `name`; the CR name is what Environments
  reference.
- For a repo with many skills, a `SkillMarketplace` is less YAML than one Skill each;
  use a standalone Skill when you want just one or two, or a skill from a repo whose
  layout a marketplace cannot walk.
- Same 50 MiB default size cap as plugins.

## Prompt

A single file (usually Markdown) served verbatim and extracted by hydrate into the
workspace's ACH state directory.

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Prompt
metadata:
  name: backend-system-prompt
spec:
  type: github
  contentType: text/markdown
  github:
    repo: my-org/agent-prompts
    ref: main
    path: backend/system.md      # a file, not a directory
  refresh:
    interval: 6h
    maxStaleness: 24h
```

- On git types, `path` must name a file.
- `contentType` is optional; empty falls back to the upstream's Content-Type.

## Artifact

Any other file or directory bundle an Environment should ship: templates, examples,
sample CRs.

```yaml
# Directory bundle: the whole templates/ tree, packed as one archive.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Artifact
metadata:
  name: service-templates
spec:
  type: github
  scope: directory
  github:
    repo: my-org/platform-templates
    ref: v2.3.0
    path: templates
  refresh:
    interval: 6h
    maxStaleness: 24h
---
# Single file from a plain HTTPS server.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Artifact
metadata:
  name: coding-standards
spec:
  type: http
  scope: object
  http:
    url: https://docs.example.com/standards/coding.md
  refresh:
    maxStaleness: 24h
```

- `scope` is required: `object` serves one file, `directory` packs a tree.
- `type: http` only allows `scope: object` (admission rejects `directory`).
- Make `path` match the scope: a file for `object`, a directory for `directory`.

## Prompt and Artifact vs Plugin and Skill in an Environment

Plugins and skills gate the Environment: until their content is fetched, the Environment
reports `ExecutionResourcesResolved=False` and is not Available. Prompts and artifacts
are only checked by name, so a Prompt that fails to fetch does not show up on the
Environment; check the Prompt or Artifact itself (`kubectl get prompts,artifacts`, or
`ach-cli admin list prompts`, whose `fresh*` marks that only the name is tracked).
