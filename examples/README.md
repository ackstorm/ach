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
| `prometheus-servicemonitor.yaml`      | `ServiceMonitor`                           | Example Prometheus scrape config for the ach metrics endpoints. |
| `prometheus-alertrules.yaml`          | `PrometheusRule`                           | Recommended ACH alert rules (LiteLLM unreachable, stale content cache, pk_ on runtime route, external-ref refresh failures, Environment unavailable). |
| `ach-cli-initcontainer.yaml`          | `Pod`                                      | Headless-agent bootstrap: an `initContainer` runs `ghcr.io/ackstorm/ach-cli env hydrate` into a shared `emptyDir` so the main agent container starts on a fully-hydrated `/workspace` (creds = an `ek_` via `secretKeyRef`, no SSO). |
| `test-mcp-jwt.sh`                     | script                                     | Helper to exercise the `/mcp` JWT trust path by hand. |
| `hydrate.json`                        | json                                       | Golden `/platform/hydrate` output — the CLI e2e suite (`test/e2e/cli_login_hydrate_test.go`) byte-for-byte diffs `ach-cli env hydrate demo` stdout against this file (normalized for the live cluster's platform-api host + scheme). |

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
#    The kind+Helm gateway is plaintext http://localhost:8080, so the CLI needs
#    the insecure opt-in (it refuses http:// by default — localhost included).
make build-all
export ACH_INSECURE=1                                      # or pass --insecure per command
./bin/ach-cli login                                        # device-code SSO (browser opens)
./bin/ach-cli env hydrate demo > hydrate.json              # POST /platform/hydrate
```

> **Tip — pre-fill the login URL:** `ach login` prompts for the Hub URL
> interactively. Export `ACH_PLATFORM_URL=https://ach.example` to pre-fill it
> (precedence: `--base-url` flag → `ACH_PLATFORM_URL` env → prompt).
> `ACH_PLATFORM_URL` is a login-only convenience — distinct from `ACH_BASE_URL`
> (the synthetic-mode trigger); it does NOT enable synthetic mode.

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

## Headless agent / CI (no browser)

`ach login` needs a browser for SSO. On an agent or CI runner, seed the
profile from a credential you already minted instead:

```bash
# 1. (on a human machine) mint a service key scoped to an environment:
ach keys create prod --name ci-bot      # prints ek_... (env is positional; --name optional, defaults to env)

# 2. (on the agent) register a profile from that ek_ — no SSO:
ach config add --profile prod --url https://ach.example --api-key ek_...

# multi-environment: seed several ek_ under labels in one profile:
ach config add --profile svc --url https://ach.example --api-key pk_... \
  --env-key prod=ek_AAA --env-key stg=ek_BBB
ach env hydrate prod --env-key prod
ach env hydrate stg  --env-key stg

# or skip disk config entirely (secrets stay in env, ideal for CI):
export ACH_BASE_URL=https://ach.example
export ACH_API_KEY=ek_...
ach env hydrate prod   # repeat per env, no --api-key
```

## Rotating / cleaning up your own keys

`login` mints a fresh `pk_` each time, so personal keys accumulate. You can
revoke your OWN keys (no admin needed) and bulk-prune the stale ones:

```bash
ach keys list --type pk                 # see your personal keys (newest first)
ach keys revoke pkid_01kt6fe0...        # revoke one (your own pk_ or ek_)
ach keys prune --dry-run                # preview: keeps the newest, lists the rest
ach keys prune --yes                    # revoke all but the newest pk_ (active key is auto-skipped)
```

Revoking the key your current session authenticates with is refused unless you
pass `--force` (then re-login). `prune` never force-revokes, so your active key
is always preserved.

Note: an `ek_` is scoped to ONE Environment; a `pk_` (from `ach login`)
spans every environment you can access but expires on a 7-day sliding
window. For long-lived agents prefer per-environment `ek_`.

## Admin: read-only object inventory

`ach admin list` gives an allowlisted admin a kubectl-free inventory of every
ACH-defined object, sourced from the Postgres projections (the SoT read path) —
version + sync status, no live cluster cross-check. Requires a `pk-` whose owner
email is in the Platform API allowlist; a non-allowlisted caller gets
`403 not_admin` (exit 3).

```bash
ach admin list plugins                 # one kind
ach admin list all                     # fan out across every kind (concurrent)
ach admin list all -o json             # machine-readable (also: -o yaml)
```

Kinds: `environments`, `plugins`, `prompts`, `artifacts`, `marketplaces`,
`bips`, `litellm-connections`, `external-refs`, or `all`.

Example (`ach admin list all`, trimmed):

```text
ENVIRONMENTS (2)
NAME             NAMESPACE  VERSION  SYNC                                AGE  ORIGIN
demo             ach        1182     Available                           3m   cr
demo-unresolved  ach        1184     Degraded(UnresolvedContextPlugins)  3m   cr

PLUGINS (1)
NAME     NAMESPACE  VERSION  SYNC   AGE  ORIGIN
caveman  ach        842      fresh  2m   -

PROMPTS (1)
NAME      NAMESPACE  VERSION  SYNC    AGE  ORIGIN
greeting  ach        844      fresh*  2m   -

* prompts/artifacts: name-resolved only; content presence is not gated
```

### SYNC column semantics

| Value | Kinds | Meaning |
|-------|-------|---------|
| `Available` / `Degraded(<reason>)` / `Pending` | environments | the `Available` composite condition (rolls up `ExecutionResourcesResolved` + `AccessGroupSynced`) |
| `fresh` / `STALE(<age> over)` / `never` | plugins, marketplaces, external-refs | refresh staleness (`last_successful_refresh` + `maxStaleness`) |
| `fresh*` | prompts, artifacts | **false-green** — their refresh tracks *name resolution*, not content presence. Only `plugins` is truly content-gated, so the asterisk warns the inventory cannot promise the content is present + current. |
| `projected` | bips, litellm-connections | row is projected from its CR (presence only) |

The inventory reads the stored projection only — to force a re-sync use
`ach admin refresh <kind> <name>`.

## Local package manager (serverless — no Environment/CRD)

For direct, personal use you don't need an Environment, the operator, or
Postgres. `ach-cli repo`/`plugin`/`skill` register an external marketplace (or a
direct plugin/skill source) and install straight into per-tool adapter dirs
(`.claude/`, `.codex/`, `.gemini/`, `.opencode/`, `.pi/`). State lives in a local
registry under `~/.config/ach/local/` (tokens in a separate `0600`
`credentials.json`).

```bash
# Register a source — capabilities (plugin-marketplace / skill-marketplace /
# direct plugin / direct skill) are auto-detected at add time.
ach-cli repo add github:anthropics/skills --name skills          # skill-marketplace
ach-cli repo add github:ackstorm/claude-plugins --name ackstorm  # plugin-marketplace
ach-cli repo add git:https://git.example.com/x/y.git --name gl --token "$TOK" --auth oauth2
ach-cli repo list                       # NAME · KIND · SOURCE · AUTH · PROVIDES

# Install by <name@repo> into one or more --target adapters (repo suffix is
# mandatory). --global writes to $HOME; default is the project (cwd).
ach-cli plugin install feature-dev@ackstorm --target claude,opencode
ach-cli skill  install pdf@skills --target claude --global
ach-cli plugin list                     # installed items (from installed.json)
ach-cli plugin update                   # re-resolve all (or <name@repo>…)
ach-cli skill  uninstall pdf@skills     # inverse-merges co-owned files (settings.json / CLAUDE.md)
```

Notes:
- `--target` values map to adapter ids (`claude`→claude-code, `codex`, `gemini`,
  `opencode`). MCP/`AGENTS.md` contributions deep-/composite-merge into the
  tool's native config and are inverse-merged on uninstall (other plugins' and
  your own keys survive).
- `--path` is the **skills-marketplace root hint** only (e.g. `skills` for an
  `anthropics/skills`-style monorepo); v1 does not narrow a direct plugin/skill
  that lives in a subdirectory.
- This is the **local-first** path. `ach-cli env hydrate <name>` remains the
  **governed** flow (CR-defined Environment, server-mediated, full conflict
  policy).

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
