# examples/ — runnable ACH CR bundle + end-to-end hydrate demo

This directory ships a minimal-but-realistic set of ACH CRDs the
operator can reconcile end-to-end, plus a shell driver that walks
the same path the future `ach hydrate` CLI command (ROADMAP Phase 7)
will take.

## What's here

| File | Kind | Notes |
|------|------|-------|
| `01-litellmconnection.yaml`           | `LiteLLMConnection`  | Wires the operator to the in-cluster LiteLLM Service. Also seeded by `scripts/cluster.sh hydrate_fixtures`. |
| `04-environment-demo.yaml`            | `Environment`        | References the three external-reference CRs below. `authorizedTeams: [default]` ties it to the LiteLLM `default` Team the demo script seeds. |
| `05-pluginmarketplace-anthropic.yaml` | `PluginMarketplace`  | Pulls `anthropics/claude-plugins-official` via `http` source and filters to `^code-.*`. Kept as a real-upstream canary now that the 5-Kind parser (#16) has landed. |
| `06-plugin-caveman.yaml`              | `Plugin`             | Third-party `JuliusBrussee/caveman` — directory-bundle plugin. |
| `07-prompt-claudecode-leak.yaml`      | `Prompt`             | Single-file fetch from `asgeirtj/system_prompts_leaks`. |
| `08-artifact-openclaw-templates.yaml` | `Artifact`           | Directory-scope tarball of `openclaw/openclaw` `docs/reference/templates`. Repo is ~261 MiB depth-1; the 512 MiB default git-clone cap accommodates it. |
| `hydrate-demo.sh`                     | shell                | End-to-end driver — apply + wait + SSO + hydrate. |
| `hydrate.json`                        | json                 | Last-known-good output of the hydrate path. |

## Run it

```bash
# 1. Bring the cluster up (operator + platform-api + LiteLLM + Dex +
#    postgres + valkey, plus the litellm-master-key Secret +
#    LiteLLMConnection seed).
make cluster-up

# 2. Apply the CRs + drive a hydrate.
bash examples/hydrate-demo.sh
```

The script runs end-to-end against a green cluster: it applies the
CRs, waits for `Synced=True` on each external-reference CR plus
`Available=True` / `AccessGroupSynced=True` on the Environment,
port-forwards the platform API, drives the Dex SSO callback, and
writes the resulting hydrate response to `examples/hydrate.json`.
The previously documented LiteLLM-client / SSO blockers (issues #17
and #19) have landed; if the script fails today, treat it as a real
regression rather than a known caveat.

## What the example explicitly does NOT do

- **It does NOT register any LiteLLM Models / MCPServers / A2AAgents.**
  The Environment's `spec.runtime.*` are intentionally empty so the
  demo works without a real LLM provider key. The hydrate response
  still emits `runtime: {models: [], mcpServers: [], a2aAgents: []}`
  per Hub §15.1's "both blocks always present" invariant.

- **It does NOT use any GitHub PAT.** The Prompt + Plugin reference
  this very repo (`ackstorm/ach`), which is public, so no
  `authSecretRef` is needed. Replace `spec.github.repo` with your own
  private repo + add a Secret per the API doc to test the authed path.

- **It does NOT pre-create the `default` LiteLLM Team with a literal
  `team_id="default"`.** The operator's LiteLLMConnection reconciler
  calls `EnsureDefaultTeam` (idempotent list-then-create) after a
  successful probe, so the demo reflects what a real deployment will
  see: LiteLLM auto-assigns a UUID `team_id` and the operator handles
  it via `ListTeamsByAlias` ordering.

## Cleanup

```bash
kubectl delete -f examples/04-environment-demo.yaml \
               -f examples/03-plugin-docs.yaml \
               -f examples/02-prompt-readme.yaml
# The LiteLLMConnection seed and LiteLLM team stay (other workflows
# need them); see scripts/cluster.sh hydrate_fixtures.
```
