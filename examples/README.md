# examples/ — curated user-facing CR samples + golden hydrate output

This directory holds **curated, user-facing example CRs** plus the golden
`/platform/hydrate` output the CLI e2e suite diffs against. It is
**independent** of the e2e synced-fixture set.

## examples/ vs test/e2e/cluster/ — the split

There are two distinct CR collections in this repo; do not conflate them:

| Collection | Location | Purpose | Applied by |
|------------|----------|---------|------------|
| **Synced test fixtures** | `test/e2e/cluster/04-objects/` (non-Environment ACH CRs) + `test/e2e/cluster/05-environment/` (the demo Environments) | The complete, demo-ready object set the e2e suite asserts against. `cluster.sh` applies them as numbered bring-up stages and the `06-verify` gate blocks until every one is healthy. | `scripts/cluster.sh` (automatic on `make cluster-up` / `make cluster-sync`) |
| **Curated examples** | `examples/` (this dir) | Hand-picked, documentation-oriented samples + the golden `hydrate.json`. | Nobody automatically — copy/adapt by hand. |

Tests **assert against the synced cluster**; they do not apply fixtures. The
canonical demo CRs (LiteLLMConnection, plugins, prompts, artifacts, BIPs,
marketplaces, the `demo` + `demo-unresolved` Environments) now live under
`test/e2e/cluster/{04-objects,05-environment}/`, not here. If you are looking
for the object the operator reconciles in e2e, look there.

## What's here

| File | Kind | Notes |
|------|------|-------|
| `12-mcpremoteproxy-deepwiki.yaml`     | `MCPRemoteProxy` (`toolhive.stacklok.dev`) | Legacy ToolHive sample. NOT part of the synced set; candidate for removal as stale. |
| `13-mcpserver-aws.yaml`               | `MCPServer` (`toolhive.stacklok.dev`)      | Legacy ToolHive sample. NOT in the synced set. |
| `14-mcpserver-context7.yaml`          | `MCPServer` (`toolhive.stacklok.dev`)      | Legacy ToolHive sample. NOT in the synced set. |
| `15-mcpserver-echo.yaml`              | `MCPServer` (`toolhive.stacklok.dev`)      | Legacy ToolHive sample. NOT in the synced set. |
| `prometheus-servicemonitor.yaml`      | `ServiceMonitor`                           | Example Prometheus scrape config for the ach metrics endpoints. |
| `test-mcp-jwt.sh`                     | script                                     | Helper to exercise the `/mcp` JWT trust path by hand. |
| `hydrate.json`                        | json                                       | Golden `/platform/hydrate` output — the CLI e2e suite (`test/e2e/cli_login_hydrate_test.go`) byte-for-byte diffs `ach-cli hydrate --environment demo` stdout against this file (normalized for the live cluster's platform-api host + scheme). |

## End-to-end demo

`make cluster-up` now brings up a **fully synced, verified** cluster — the
operator + platform-api + LiteLLM + Dex + postgres + valkey, the dev secrets,
the stage-04 objects, and the demo Environments — and the `06-verify` gate
blocks until `environment/demo` is `Available=True` (its composite rolls up
`ExecutionResourcesResolved` + `AccessGroupSynced`). No further `kubectl apply`
is needed to reach the demo state:

```bash
# 1. Bring the cluster up — synchronous; everything healthy when it returns.
make cluster-up

# 2. Build the CLI + run the hydrate demo against the already-synced demo Env.
make build-all
./bin/ach-cli login                                        # device-code SSO (browser opens)
./bin/ach-cli hydrate --environment demo > hydrate.json    # POST /platform/hydrate
```

The `hydrate.json` output should match `examples/hydrate.json` byte-for-byte
against the standard kind+Helm fixture cluster (the base URL is baked into the
golden — when the live cluster exposes the platform-api on a different
externally-visible host, the bytes-equal compare only holds after substituting
the host on every `downloadUrl`; the CLI e2e suite does this automatically via
`phase6NormalizeHydrate`).

The CLI e2e umbrella `TestPhase6CLI` in `test/e2e/cli_login_hydrate_test.go`
asserts this invariant automatically. See `CLAUDE.md` "Common failure modes"
entry "Hydrate output != examples/hydrate.json" for the host-normalization
gotcha + remediation steps.

## What the demo Environment explicitly does NOT do

(The `demo` Environment now lives at `test/e2e/cluster/05-environment/demo.yaml`.)

- **It does NOT use any GitHub PAT.** The Prompt + Plugin reference public
  upstream repos, so no `authSecretRef` is needed. Replace `spec.github.repo`
  with your own private repo + add a Secret per the API doc to test the authed
  path.

- **It does NOT pre-create the `default` LiteLLM Team with a literal
  `team_id="default"`.** The operator's LiteLLMConnection reconciler calls
  `EnsureDefaultTeam` (idempotent list-then-create) after a successful probe,
  so the demo reflects what a real deployment will see: LiteLLM auto-assigns a
  UUID `team_id` and the operator handles it via `ListTeamsByAlias` ordering.
