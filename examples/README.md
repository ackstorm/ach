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

`workspace.yaml` is a separate curated sample, not a synced fixture. It remains
at `replicas: 0`; omitted child UID preconditions are for fresh creation only
when no StatefulSet or Pod exists. Existing active adoption and guarded sleep
require observed child UIDs, and activation requires true idle (zero replicas
and no Pod of any phase) before the operator changes the execution template.

## What's here

| File | Kind | Notes |
|------|------|-------|
| `prometheus-servicemonitor.yaml`      | `ServiceMonitor`                           | Example Prometheus scrape config for the ach metrics endpoints. |
| `prometheus-alertrules.yaml`          | `PrometheusRule`                           | Recommended ACH alert rules (LiteLLM unreachable, stale content cache, pk_ on runtime route, external-ref refresh failures, Environment unavailable). |
| `ach-cli-initcontainer.yaml`          | `Pod`                                      | Headless-agent bootstrap: an `initContainer` runs `ghcr.io/ackstorm/ach-cli env hydrate` into a shared `emptyDir` so the main agent container starts on a fully-hydrated `/workspace` (creds = an `ek_` via `secretKeyRef`, no SSO). |
| `test-mcp-jwt.sh`                     | script                                     | Helper to exercise the `/mcp` JWT trust path by hand. |
| `ach-memory/`                         | `BackendIdentityPolicy`                    | Wiring the [ach-memory](https://github.com/ackstorm/ach-memory) MCP backend into the ACH JWT trust path — LiteLLM `extra_headers` registration, the BIP, Environment authorization, and the `MEMORY_AUTH_JWT_*` config. See `ach-memory/README.md`. |
| `hydrate.json`                        | json                                       | Golden `/platform/hydrate` output — the CLI e2e suite (`test/e2e/cli_login_hydrate_test.go`) byte-for-byte diffs `ach-cli env hydrate demo` stdout against this file (normalized for the live cluster's platform-api host + scheme). |
| `agent-runtime/`                      | `AgentProfile` + `ACHAgent`                | A running-agent example (workspace-v1 control/execution split, contract §11) — webhook/cron channels, handoff, memory. See `agent-runtime/README.md`. |
| `workspace.yaml`                      | `Workspace`                                 | Asleep per-execution request owned by an `ACHAgent`; name follows runtime 0.1.8 agent-name/ref naming (maximum 52 characters), while UID/reference ownership remains full and unchanged. |

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
./bin/ach-cli login http://localhost:8080                  # OAuth: browser here, or a code from any browser
./bin/ach-cli env hydrate demo --raw > hydrate.json        # POST /platform/hydrate, body verbatim
```

> **Tip — pre-fill the login URL:** `ach-cli login` without a URL prompts for
> it. Export `ACH_URL=https://ach.example` to pre-fill it (precedence: the
> positional `login <url>` → `ACH_URL` → prompt). `ACH_URL` alone never enables
> synthetic mode — that needs `ACH_KEY` too.

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

> **`runtime.guardrails`** — an additive arm, absent from `runtime.models`/
> `mcpServers`/`a2aAgents`. Unlike those, entries are plain strings, not
> `{id, endpoint}` objects: a guardrail is applied server-side by LiteLLM and
> is never called by the client, so there is no endpoint to publish. The key
> is present ONLY when the Environment declares at least one guardrail — the
> golden above omits it because `demo` declares none. The populated shape is
> covered by `internal/platformapi/hydrate`'s in-package tests, not this
> golden.
>
> Declaring guardrails at all **requires a LiteLLM Enterprise licence**:
> team-scoped guardrails are premium-gated, so on an unlicensed proxy a
> non-empty list 403s when the operator attaches it (the Environment never goes
> `Available`) and again on every request. Empty is exempt. Global `default_on`
> guardrails run ungated and need no ACH configuration — see
> `references/litellm-permission-model.md` §11.

## Headless agent / CI (no browser)

`ach-cli login` needs a browser for SSO (or `--no-browser` for a code you
confirm from any browser). On an agent or CI runner, use an environment key
you already minted instead:

```bash
# 1. (on a human machine) mint a key scoped to one environment:
ach-cli keys create prod --name ci-bot --no-save   # prints ek-... once

# 2. (on the agent) register a profile from that key — no SSO:
ach-cli profile add prod --url https://ach.example --key ek-...
ach-cli env hydrate prod

# or skip disk config entirely (secrets stay in env, ideal for CI):
export ACH_URL=https://ach.example
export ACH_KEY=ek-...
ach-cli env hydrate prod
```

On your own machine, `keys create` saves the key in your profile under its
name, and `--key <name>` (or `ACH_KEY=<name>`) picks it for one command:
`ach-cli env hydrate stg --key stg-bot`.

## Rotating / cleaning up your own keys

Your environment keys are yours to manage (no admin needed), by saved name or
by `ekid_…`:

```bash
ach-cli keys list                        # NAME ENVIRONMENT STATUS EXPIRES ID
ach-cli keys suspend ci-bot              # pause it; keys resume ci-bot undoes it
ach-cli keys budget ci-bot --max-budget 20 --budget-duration 30d
ach-cli keys revoke ci-bot               # for good; also drops the saved copy
```

Note: a key is scoped to ONE Environment; your sign-in session (`ach-cli
login`) spans every environment you can access. For long-lived agents use a key.

## Admin: read-only object inventory

`ach-cli admin list` gives an allowlisted admin a kubectl-free inventory of every
ACH-defined object, sourced from the Postgres projections (the SoT read path) —
version + sync status, no live cluster cross-check — plus the LiteLLM runtime
catalog. The caller's email must be in the Platform API allowlist; anyone else
gets `403 not_admin` (exit 3).

```bash
ach-cli admin list plugins             # one kind
ach-cli admin list models              # the LiteLLM runtime catalog (KIND NAME STATUS)
ach-cli admin list all                 # fan out across every kind (concurrent)
ach-cli admin list all -o json         # machine-readable (also: -o yaml)
```

Kinds: `environments`, `plugins`, `prompts`, `artifacts`, `skills`,
`marketplaces`, `skill-marketplaces`, `bips`, `models`, `mcp`, `a2a`, `teams`,
`guardrails`, or `all`.

Other admin verbs: `admin keys list [--owner e]`, `admin keys revoke <id>` /
`--owner <email>`, `admin users budget <email> --max-budget X`, `admin users
limits <email> --max-keys N`, `admin refresh <kind> <name>`.

Example (`ach-cli admin list all`, trimmed):

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
`ach-cli admin refresh <kind> <name>`.

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
ach-cli local repo add github:anthropics/skills --name skills          # skill-marketplace
ach-cli local repo add github:ackstorm/claude-plugins --name ackstorm  # plugin-marketplace
ach-cli local repo add git:https://git.example.com/x/y.git --name gl --token "$TOK" --auth oauth2
ach-cli local repo list                 # NAME · KIND · SOURCE · AUTH · PROVIDES

# Install by <name@repo> into one or more --target adapters (repo suffix is
# mandatory). -g writes to $HOME; default is the project (cwd, or --dir).
ach-cli local plugin install feature-dev@ackstorm --target claude-code,opencode
ach-cli local skill  install pdf@skills --target claude-code -g
ach-cli local plugin list               # installed items (from installed.json)
ach-cli local plugin update             # re-resolve all (or <name@repo>…)
ach-cli local skill  uninstall pdf@skills   # inverse-merges co-owned files (settings.json / CLAUDE.md)
```

Notes:
- `--target` takes adapter ids (`claude-code`, `codex`, `gemini-cli`,
  `opencode`, `pimono`) or their aliases (`claude`, `gemini`, `pi`), comma-separated
  or repeated — the same vocabulary as `env hydrate --target`. MCP/`AGENTS.md` contributions deep-/composite-merge into the
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
