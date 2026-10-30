# Quickstart

From a running ACH ([Installation](installation.md)) to an agent tool that has your
first Environment. You need `kubectl` access to the ACH namespace, a LiteLLM team your
user belongs to, and at least one model in LiteLLM.

## 1. Grant something

An Environment names what a team may use. Save this as `quickstart.yaml`, replacing the
model and team with ones your LiteLLM has (`ach-cli admin list models` and
`ach-cli admin list teams` show them, for an admin):

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
  name: quickstart
spec:
  runtime:
    models:
      - <a LiteLLM model name>
  context:
    plugins:
      - ach-cli@ach
  authorizedTeams:
    - <a LiteLLM team alias>
  description: |
    First Environment: one model and the ach-cli agent plugin.
```

```bash
kubectl -n ach apply -f quickstart.yaml
kubectl -n ach get pluginmarketplace ach        # Synced=True
kubectl -n ach get environment quickstart       # Available=True
```

`Available` needs both halves: `ExecutionResourcesResolved` (the plugin was found) and
`AccessGroupSynced` (LiteLLM accepted the grant). `kubectl -n ach describe environment
quickstart` shows the reason when one is False.

## 2. Use it

Install `ach-cli` from the [latest release](https://github.com/ackstorm/ach/releases/latest),
then:

```bash
ach-cli login https://<ACH host>
ach-cli env list                                       # quickstart is listed
ach-cli env clone quickstart --target claude-code      # creates ./quickstart and hydrates it
```

Start Claude Code inside `./quickstart`: it has the `ach-cli` skill, so from there you
can ask it to install more Environments, create keys or sign in to MCP servers.
`--target` also takes `codex`, `opencode`, `gemini-cli` or `pi`.

## 3. Clean up

```bash
ach-cli env uninstall quickstart --dir ./quickstart
kubectl -n ach delete -f quickstart.yaml
```

Deleting an Environment also revokes every `ek-` key created for it.
