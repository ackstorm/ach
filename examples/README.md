# examples/ — runnable ACH CR bundle + end-to-end hydrate demo

This directory ships a minimal-but-realistic set of ACH CRDs the
operator can reconcile end-to-end, plus the golden `/platform/hydrate`
output the CLI e2e suite diffs against.

## What's here

| File | Kind | Notes |
|------|------|-------|
| `01-litellmconnection.yaml`           | `LiteLLMConnection`  | Wires the operator to the in-cluster LiteLLM Service. Also seeded by `scripts/cluster.sh hydrate_fixtures`. |
| `04-environment-demo.yaml`            | `Environment`        | References the three external-reference CRs below. `authorizedTeams: [default]` ties it to the LiteLLM `default` Team the cluster seeds. |
| `05-pluginmarketplace-anthropic.yaml` | `PluginMarketplace`  | Pulls `anthropics/claude-plugins-official` via `http` source and filters to `^code-.*`. Kept as a real-upstream canary now that the 5-Kind parser (#16) has landed. |
| `05b-pluginmarketplace-caveman.yaml`  | `PluginMarketplace`  | Pulls `JuliusBrussee/caveman` — same repo as `06-plugin-caveman.yaml`, exercised as a single-plugin marketplace (`.claude-plugin/marketplace.json` with `source: "./"`). |
| `06-plugin-caveman.yaml`              | `Plugin`             | Third-party `JuliusBrussee/caveman` — directory-bundle plugin. |
| `07-prompt-claudecode-leak.yaml`      | `Prompt`             | Single-file fetch from `asgeirtj/system_prompts_leaks`. |
| `08-artifact-openclaw-templates.yaml` | `Artifact`           | Directory-scope tarball of `openclaw/openclaw` `docs/reference/templates`. Repo is ~261 MiB depth-1; the 512 MiB default git-clone cap accommodates it. |
| `09-…context7.yaml` / `10-…duplicate.yaml` | `BackendIdentityPolicy` | Illustrative-only (NOT applied by `cluster.sh`): a JWT-on policy and a duplicate `(kind,name)` resolved last-by-`metadata.name`. Document the dup-resolution rule; target `context7` need not exist. |
| `11-…demo-mcp-jwt.yaml` / `16-…demo-mcp-nojwt.yaml` | `BackendIdentityPolicy` | The demo Environment's BIP closed-loop. `11` attaches the ACH JWT for `/mcp/demo-mcp-jwt`; `16` forwards without one for `/mcp/demo-mcp-nojwt`. Both target the single `ach-mcp-echo` backend; applied by `scripts/cluster.sh`. |
| `hydrate.json`                        | json                 | Golden `/platform/hydrate` output — the CLI e2e suite (`test/e2e/cli_login_hydrate_test.go`) byte-for-byte diffs `ach hydrate --environment demo` stdout against this file (normalized for the live cluster's platform-api host + scheme). |

## End-to-end demo

This phase ships the headline replacement workflow — a single-line CLI
invocation collapses the previous 139-line shell driver into the
shipped `ach` binary:

```bash
# 1. Bring the cluster up (operator + platform-api + LiteLLM + Dex +
#    postgres + valkey, plus the litellm-master-key Secret +
#    LiteLLMConnection seed). Idempotent — re-applies cleanly.
make cluster-up

# 2. Apply the example CRs and wait the Environment Ready (the
#    Environment's Available composite rolls up
#    ExecutionResourcesResolved + AccessGroupSynced — see
#    examples/04-environment-demo.yaml and the operator's
#    Environment reconciler).
kubectl apply -f examples/01-litellmconnection.yaml \
              -f examples/06-plugin-caveman.yaml \
              -f examples/07-prompt-claudecode-leak.yaml \
              -f examples/08-artifact-openclaw-templates.yaml \
              -f examples/04-environment-demo.yaml
make wait-cr-ready KIND=environment NAME=demo NS=ach-system

# 3. Build the CLI + run the demo.
./scripts/dev.sh make build
ach login                                       # device-code SSO (browser opens)
ach hydrate --environment demo > hydrate.json   # POST /platform/hydrate
```

The `hydrate.json` output should match `examples/hydrate.json`
byte-for-byte against the standard kind+Helm fixture cluster (the host
`ach.local.test` is baked into the golden — when the live cluster
exposes the platform-api on a different externally-visible host,
the bytes-equal compare only holds after substituting the host on
every `downloadUrl`; the CLI e2e suite does this automatically via
`phase6NormalizeHydrate`).

The CLI e2e umbrella `TestPhase6CLI` in
`test/e2e/cli_login_hydrate_test.go` asserts this invariant
automatically. See `CLAUDE.md` "Common failure modes" entry
"Hydrate output != examples/hydrate.json" for the host-normalization
gotcha + remediation steps.

## What the example explicitly does NOT do

- **It does NOT register any LiteLLM Models / MCPServers / A2AAgents.**
  The Environment's `spec.runtime.*` are intentionally empty so the
  demo works without a real LLM provider key. The hydrate response
  still emits `runtime: {models: [], mcpServers: [], a2aAgents: []}`
  per Hub §15.1's "both blocks always present" invariant.

- **It does NOT use any GitHub PAT.** The Prompt + Plugin reference
  public upstream repos, so no `authSecretRef` is needed. Replace
  `spec.github.repo` with your own private repo + add a Secret per
  the API doc to test the authed path.

- **It does NOT pre-create the `default` LiteLLM Team with a literal
  `team_id="default"`.** The operator's LiteLLMConnection reconciler
  calls `EnsureDefaultTeam` (idempotent list-then-create) after a
  successful probe, so the demo reflects what a real deployment will
  see: LiteLLM auto-assigns a UUID `team_id` and the operator handles
  it via `ListTeamsByAlias` ordering.

## Cleanup

```bash
kubectl delete -f examples/04-environment-demo.yaml \
               -f examples/06-plugin-caveman.yaml \
               -f examples/07-prompt-claudecode-leak.yaml \
               -f examples/08-artifact-openclaw-templates.yaml
# The LiteLLMConnection seed and LiteLLM team stay (other workflows
# need them); see scripts/cluster.sh hydrate_fixtures.
```
