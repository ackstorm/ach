# Agent plugins

ACH ships guidance for AI coding agents as a plugin marketplace named `ach`, at the root
of this repository. It works with Claude Code and Codex, and through `ach-cli env
hydrate` with every tool ACH supports.

| Plugin | For | What it covers |
|--------|-----|----------------|
| `ach-cli` | every user | sign in, find and hydrate Environments, MCP sign-in, `ek-` keys, `ach-cli local` |
| `ach-gitops` | platform administrators | writing Environments, marketplaces, Plugins, Skills and the other ACH custom resources |
| `ach-install` | operators | installing, configuring and upgrading the Helm chart |

None of them names a platform URL: `ach-cli` reads it from the signed-in profile, or from
`ACH_URL` before the first login.

## Install directly

```bash
claude plugin marketplace add ackstorm/ach
claude plugin install ach-cli@ach
```

Codex reads the same marketplace from `.agents/plugins/marketplace.json`.

## Serve them through ACH

Register the repository as a `PluginMarketplace` and list the plugins in an Environment;
users then get them with `ach-cli env hydrate`:

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: PluginMarketplace
metadata:
  name: ach
spec:
  type: github
  github:
    repo: ackstorm/ach
    ref: main
  refresh:
    interval: 1h
    maxStaleness: 24h
---
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: baseline
spec:
  context:
    plugins:
      - ach-cli@ach
  authorizedTeams:
    - default
```

Put `ach-cli@ach` in the Environment named by the chart value
`genai.defaultEnvironment`: OpenCode users get a short bootstrap skill (`<provider>-ach`)
from `/clients/opencode/config` that installs `ach-cli`, signs in and hydrates that
Environment, and from there the `ach-cli` plugin takes over. Keep `ach-gitops` and
`ach-install` in the Environments of the people who run the platform.
