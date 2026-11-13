# Install the ACH agent plugins

ACH publishes three plugins that teach an AI coding agent to work with it.
They live in a plugin marketplace named `ach`, at the root of the
[ACH repository](https://github.com/ackstorm/ach).

| Plugin | For | The agent learns to |
|---|---|---|
| `ach-cli` | Every user | Sign in, find and hydrate Environments, sign in to MCP servers, manage `ek-` keys, use `ach-cli local` |
| `ach-gitops` | Platform admins | Write Environments, marketplaces, Plugins, Skills and the other ACH custom resources |
| `ach-install` | Operators | Install, configure and upgrade the Helm chart |

None of them contains the URL of your ACH: `ach-cli` reads it from your
profile, or from `ACH_URL` before you first sign in.

## Through an Environment

If your platform team serves the plugins through an Environment, hydrating it
installs them, for every tool ACH supports:

```bash
ach-cli environment search ach-cli           # which Environment has it
ach-cli environment hydrate <environment> --target <tool>
```

## Directly in Claude Code

```bash
claude plugin marketplace add ackstorm/ach
claude plugin install ach-cli@ach
```

## Directly in Codex

```bash
codex plugin marketplace add ackstorm/ach
codex plugin add ach-cli@ach
```

## Verify

Start the tool and ask it to list your ACH Environments. It runs
`ach-cli environment list`.

## For platform admins: serve them through ACH

Register the repository as a `PluginMarketplace` and list the plugins in an Environment;
users then get them with `ach-cli environment hydrate`:

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
