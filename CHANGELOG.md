# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [1.3.10] - 2026-11-27

### Added

- Forwarder: the identity JWT sent to an MCP backend can carry a `key_share` claim, a per-user, per-server value the backend mixes with its own secret to encrypt what it stores for that user (an OAuth grant), so that its store and its secret alone decrypt nothing. Off by default: set the Helm value `security.mcpKeyShare.secretRef.name` to a Secret holding at least 32 random bytes. The claim is on `/mcp/<name>` tokens only. Never change that Secret's content afterwards: every grant stored by every MCP server that uses the claim becomes unreadable and each user consents again. Formula and test vector: `docs/reference/jwt.md`.

### Changed

- `ach-cli login`: the browser page shown after signing in is a styled card in the console's colours, light or dark as the system is set, instead of bare text. A failed sign-in shows the same card in red.

## [1.3.9] - 2026-11-27

### Breaking

- `KeyGrant.spec.budget` is removed. A KeyGrant decides who may exchange, not how much: an exchange key has no budget of its own, and its Environment's `spec.budget` caps it, together with the identity's own budget. The KeyGrant budget measured the same spend as the Environment's when one key used the Environment, so raising one of the two changed nothing. Before upgrading, move each grant's ceiling to `spec.budget` on its Environment and remove `spec.budget` from your KeyGrants; after the upgrade, `kubectl apply` refuses a KeyGrant that still sets it. The operator removes the `key:<id>` budget of every exchange key within 5 minutes. To give a workload its own ceiling, give it its own Environment. `PATCH /platform/keys/{id}/budget` on an exchange key still answers 409, now with code `budget_managed_by_environment` instead of `budget_managed_by_grant`.

### Added

- The operator's `/metrics` exports, for every Environment with `spec.budget`, `ach_environment_budget_spend_usd` (spend in the current budget cycle), `ach_environment_budget_max_usd` and `ach_environment_budget_reset_timestamp_seconds` (next reset; absent for a budget that never resets), labelled `environment`. LiteLLM exports no metric for tag budgets, so these are the series to alert on before calls are refused, for example `spend / (max > 0) >= 0.8`. They are read from LiteLLM every 5 minutes, two calls per budgeted Environment, never at scrape time; a failed read keeps the last value. Spend counts whole UTC days, so it can be up to 12 hours off at the start of a cycle.

## [1.3.8] - 2026-11-27

### Changed

- The console moved to `/console/`, for example `https://ach.example.com/console/`. `/` redirects there (302), so existing bookmarks keep working, including the ones to a page such as `/#/keys`. `/ui` and `/ui/` redirect there too; nothing below `/ui/` does, and LiteLLM's own UI stays on LiteLLM's host. A monitor that expects 200 from `/` now gets the redirect: point it at `/console/`. With the console's pages under `/console/` and its sign-in and API under `/platform/console/`, an Ingress rule can limit the console to a network; see "Restrict the console by source address" in the Choose a topology page.

### Fixed

- `ach-cli environment hydrate -g` without `--target` detects Antigravity from `~/.gemini/antigravity-cli/`, which a fresh `agy` install creates. It looked only for `~/.gemini/config/mcp_config.json`, which exists once an MCP server is configured, so Antigravity was left out of the detected targets.
- A path ACH does not serve, such as `https://ach.example.com/no-such-page`, answers 404. It answered 200 with the console, which then sent the browser to the Overview.

## [1.3.7] - 2026-11-26

### Added

- `BackendIdentityPolicy` has a new optional field, `spec.forwardTeams`. When it is `true`, the identity JWT ACH sends to that MCP server or A2A agent carries a `teams` claim listing every LiteLLM team of the caller. `groups` is unchanged: only the teams an Environment granting the backend authorizes. Off by default; turn it on for a backend that shares resources by team membership.

## [1.3.6] - 2026-11-25

### Added

- OpenCode TUI plugins for every signed-in user: list them in `genai.opencodeTuiPlugins` (Helm), each an npm package at one exact version (`name@1.2.3`). `/clients/opencode/config` serves them as `tuiPlugins`, and opencode-oidc-provider v0.5.0 keeps them in each user's `cli.json`, beside the user's own entries. platform-api refuses to start on a version range, dist-tag or git spec, because every user installs them unreviewed. The chart's default `genai.opencodePluginSpec` is opencode-oidc-provider v0.5.0.

### Changed

- Enterprise console Overview: the prompt cache-read share target is 95% (it was 80%), and Average generation input has a target of 150K tokens, shown red with an `Above target` pill beyond it. The uncached input / output target stays at 10:1. One line under the three figures says the targets are dashboard choices.

## [1.3.5] - 2026-11-24

### Fixed

- `ach-cli environment hydrate` reports `up to date` on a second run for an Environment that installs no MCP server, plugin, skill or other item, such as one with models only. It reported `0 items, 1 file` and repeated the first-run hints on every run.
- `ach-cli launch` starts the tool as the terminal's foreground job on Linux and macOS, as a shell does: the tool leads the foreground process group and `ach-cli` waits behind it. Terminal managers that find the agent from that process (Ovis showed a pane started with `ach-cli launch claude` as a plain terminal, with no Chat view) now see the tool. Ctrl-Z and `fg` work as before; Ctrl-C and Ctrl-\ reach the tool only, so a script that wraps `ach-cli launch` no longer receives them. Nothing changes when stdin is not a terminal, or on Windows.
- `ach-cli launch` exits with 128 plus the signal number when a signal kills the tool (143 for SIGTERM), as a shell reports it. It exited 1 and printed `error: signal: terminated`.

## [1.3.4] - 2026-11-24

### Security
- Built with OpenTelemetry 1.47.0, which fixes GO-2026-6974 (a span attribute
  could exceed its length limit).

### Fixed

- `ach-cli environment hydrate <environment>` without `--target` updates the tools the Environment is already hydrated for, in a folder and with `-g`. It stopped with "multiple agent targets detected" as soon as the Environment was hydrated for more than one tool; only the form without a name reused them. Adding a tool still takes `--target` once.
- `ach-cli environment hydrate` no longer stops with "your local changes to <file> would be overwritten" over the entries it writes into a tool's own configuration file (the MCP servers and the `--models` settings in `.mcp.json`, `opencode.json`, `.codex/config.toml`, `settings.json`). It stopped although nobody had edited anything when an Environment replaced one MCP server with another, and on an OpenCode configuration hydrated before 1.2.0; and once you added a field to one of those entries, such as `disabled` on a server, it stopped on every hydrate, and `--force` only got that one run through. Those entries are now written again on every hydrate and removed when the Environment drops them: a field you add inside one stays, a field hydrate writes goes back to the Environment's value, and the rest of the file is not touched. Hydrate still stops, with exit code 2, for a whole file you edited (a skill, an agent, a command).
- `ach-cli environment hydrate --models` ends with the Environment's models and how to start the tool on one (`claude --model <model>`, `codex -m <model>`). `--models` sets where the tool sends its requests, not which model it asks for, and said nothing about it: Codex with no model takes the first entry of the platform's model list, which can be an agent, and Claude Code asks for its own default model, which the platform may not serve.
- `ach-cli environment hydrate --models` routes the tool's model requests through ACH for an Environment that has models and no MCP server or agent. It wrote nothing for such an Environment and reported success.
- `ach-cli environment hydrate -g` without a name updates every Environment installed globally. It read the list from the folder it ran in, so inside a hydrated project it installed that project's Environments into your home folder, and anywhere else it said "nothing hydrated in this folder yet".

## [1.3.3] - 2026-11-23

### Removed
- Cursor support: `ach-cli environment hydrate|clone --target cursor`, `ach-cli local plugin|skill install --target cursor`, `ach-cli launch cursor`, the `cursor-agent mcp enable` hint, and the Cursor tab of the console's MCP client guide. Files an earlier `ach-cli` wrote for Cursor (`.cursor/mcp.json`, which holds your key, `.cursor/rules/ach-*.mdc`, `.cursor/commands/`, `.cursor/agents/`) stay until you delete them.

### Changed
- `ach-cli environment hydrate --target codex --models` needs `--global`: Codex reads its model provider only from your user config and ignores `model_provider` in a project's `.codex/config.toml`, so the project-scope run sent no model traffic through ACH. It now stops with a message saying so.
- `ach-cli environment status` and `ach-cli launch claude` warn when the folder holds native Claude Code plugins and Claude Code does not trust it: Claude Code keeps trust per folder (a trusted parent does not count) and skips those plugins without a word.
- `ach-cli environment uninstall` also removes `.ach/<env>/lock`, the empty `.ach/<env>/tmp/` and the `.ach/` folders it leaves empty.

### Fixed
- `ach-cli environment hydrate --models` for Codex writes the credential command as `command = "ach-cli"` and `args = ["token"]`. Codex runs `command` as a program name, so `command = "ach-cli token"` failed with "provider auth command `ach-cli token` failed to start: No such file or directory".

## [1.3.2] - 2026-11-23

### Added
- `Plugin` has `spec.delivery`: `ach` (the default) splits the plugin's files into each tool's folders, `native` keeps it whole so the tool's own plugin system loads its hooks, local MCP server and extensions. A native plugin is stored unfiltered and needs `ach-cli` 1.3.1 or newer on every machine that hydrates an Environment holding it; an older `ach-cli` stops with an unknown-field error. `ach-cli environment status` reports a plugin whose delivery changed as changed, so hydrating again installs it the new way.

## [1.3.1] - 2026-11-23

### Added
- `ach-cli environment hydrate` delivers an Environment's Instructions to Codex: it writes `.codex/ach/<name>.md` and a `SessionStart` hook in `.codex/hooks.json` that prints them (`$CODEX_HOME/ach/` and `$CODEX_HOME/hooks.json` with `-g`). Codex runs the hook only once you approve it (it asks when it starts, or use `/hooks`), and a project hook also needs a trusted project; hydrate prints a reminder. Your own hooks in that file are kept, and uninstall removes only this one.
- `ach-cli environment hydrate` installs a plugin the platform marks for native delivery as an intact folder that the tool's own plugin system loads, so its hooks, local MCP server and tool extensions work. Claude Code, pi and OpenCode get an entry in their settings, Antigravity a copy under `.agents/plugins/`, and Codex is installed with `codex plugin add`, for the whole user. A tool the plugin does not support gets a warning and nothing from that plugin. Cursor has no native delivery.

### Changed
- Correction to the docs: Codex and Antigravity read a parent folder's `.codex/` or `.agents/` only up to the git root, so in a folder per Environment a project that is a git repository gets neither the Environment's Instructions nor, in Codex, its MCP servers. Hydrate inside the project or use `-g`.

### Fixed
- `ach-cli environment hydrate` for a second tool that also uses `.agents/skills` (Codex, Cursor, OpenCode, pi, Antigravity) no longer stops with "already exists with other content and this Environment did not install it" when a skill changed on the server; it updates the file the first tool installed. Until now `--force` was the only way past it. Also, a skill with the same name as one a sibling Environment in the folder installed is again installed as `<environment>-<skill>` instead of over the sibling's copy.

## [1.3.0] - 2026-11-22

### Changed
- `ach-cli environment uninstall` reminds you to run `opencode service restart` when it removes an MCP server for OpenCode, as hydrate does: until then the running service keeps the server and the key in its headers.
- `ach-cli environment hydrate` and `ach-cli local plugin install|update` remind you to run `cursor-agent mcp enable <name>` when they add an MCP server for Cursor, which does not load a server until it is enabled.
- Correction to 1.2.0: Codex already wrote skills to `.agents/skills`; the ones that moved there are OpenCode's (from `.opencode/skills`) and pi's (from `.pi/skills`, `~/.pi/agent/skills` with `-g`).
- Correction to 1.2.0: a folder hydrated for Gemini CLI, or with a `state-pimono.json`, keeps those files, and `.gemini/settings.json` holds your key in plain text. Remove them with `ach-cli environment uninstall <env>` (it also removes the Environment's other targets: hydrate them again afterwards), and a local install for `gemini-cli` with `ach-cli local plugin|skill uninstall <ref>` (without `--target`, it removes the item for every tool: install it again for the others).

### Removed
- The undocumented target aliases `cursor-cli` and `antigravity-cli`: use `cursor` (or `cursor-agent`) and `antigravity` (or `agy`).

### Fixed
- The `Prompt` kind is back, unchanged from 1.1.x: `spec.context.prompts`, `context.prompts` in `POST /platform/hydrate`, `/content/prompt/<name>`, `/platform/admin/prompts` and the `prompt` kind in `ach-cli admin` and `environment fetch`. 1.2.0 removed it although agents use it for their system prompt; 1.2.0 is withdrawn, do not install it. Upgrading from 1.1.x: your Prompts keep working, and Instruction is added beside Prompt. `ach-cli environment hydrate` does not write Prompts into the folder (an older ach-cli put them in `.ach/<env>/prompt/` or `.ach/prompt/`); those files stay until you delete them, and a Prompt is loaded by whoever names it.
- `ach-cli local plugin update` prints the `opencode service restart` hint only when the plugin's MCP servers change, and `local plugin uninstall` of a plugin installed for OpenCode before 1.2.0 prints it too.
- `ach-cli environment hydrate` without `--target` picks the folder's own tool again when `~/.cursor/mcp.json`, `~/.gemini/config/mcp_config.json` or `~/.pi/mcp.json` exists. Those files counted as a Cursor, Antigravity or pi signal in every folder, so the hydrate stopped with "multiple agent targets detected" or chose that tool. They now count only with `-g`.
- `ach-cli local plugin|skill`: the same item installed for the same tool in
  two folders, or in a folder and with `-g`, is two installs. Before, the second
  install replaced the first one's record, so `uninstall` and `update` missed
  the first folder's files. `update` and `uninstall` act on the current folder
  (`--dir`, or `-g`); `list` and `outdated` show a LOCATION column.
- `ach-cli environment hydrate` for a target added after another one (`--target cursor` in a folder hydrated for codex, or `ach-cli launch <tool> --environment`) no longer refuses a skill in `.agents/skills` that the other target wrote: it updates it, or keeps it when you edited it.
- Two Environments hydrated into one folder for different targets that share `.agents/skills` (codex, cursor, opencode, pi, antigravity) no longer overwrite each other's same-named skill: the later one is prefixed with its Environment name, as with one target.
- `ach-cli local plugin|skill` with targets that share `.agents/skills`: `update` no longer reports the shared skill as edited by you and leaves the second target's record behind; `install` for a second target no longer refuses a skill the first one installed; `uninstall --target <one>` keeps the skill the other target still uses.
- OpenCode now loads an Environment's Instructions in projects inside an Environment folder (the layout `environment clone` creates). `ach-cli environment hydrate` writes the absolute path of `.opencode/ach/*.md` in `instructions` of `.opencode/opencode.json`, because OpenCode searches a relative entry only up to the project's git root.

### Added
- An Environment is not ready until each of its Prompts has been fetched once (`status.unresolvedContextPrompts`). After the upgrade, an Environment that lists a Prompt that does not exist or was never fetched is not ready.

## [1.2.0] - 2026-11-21

### Breaking
- The `Prompt` kind is replaced by the `Instruction` kind (same spec without `contentType`), and `spec.context.prompts` by `spec.context.instructions`. Renamed with it: `ach-cli admin list prompts` → `admin list instructions`, `ach-cli admin refresh prompt` → `admin refresh instruction`, `ach-cli environment fetch <env> prompt <name>` → `… instruction <name>`, the JSON key `prompts` in `environment describe`/`search` output → `instructions`, the routes `/platform/admin/prompts` → `/platform/admin/instructions` and `/content/prompt/{name}` → `/content/instruction/{name}`, and the Environment version item kind `prompt` → `instruction`. Prompt data is not migrated. Steps: (1) before upgrading, delete every `Prompt` and remove `spec.context.prompts` from your Environments, so the running operator clears finalizers and cache; (2) upgrade; (3) `kubectl delete crd prompts.ach.ackstorm.ai` — Helm keeps CRDs (`helm.sh/resource-policy: keep`); (4) if a `Prompt` is stuck deleting, remove its finalizer with `kubectl patch prompt <name> -n <ns> --type=merge -p '{"metadata":{"finalizers":null}}'`; (5) recreate your texts as `Instruction` objects. The operator cache keeps an unused `prompt/` folder. An Instruction must be plain UTF-8 Markdown without frontmatter, at most 23,900 bytes. `POST /platform/hydrate` no longer returns `context.prompts`: it returns `context.instructions` (`[{name, id, downloadUrl}]`, omitted when empty), and the download URL is `/content/instruction/<name>`. API consumers, including agents built on the hydrate API, read `context.instructions`. ach-cli older than this release fails to hydrate an Environment that lists instructions: run `ach-cli upgrade`.

### Security
- `ach-cli environment hydrate` no longer writes a plugin's top-level
  `AGENTS.md` into the folder's `CLAUDE.md` (`~/CLAUDE.md` with `--global`).
  A plugin author could put text there that the
  tool loads in every session, in a file you own and usually commit. Hydrate
  reports `AGENTS.md` as dropped, as it already did for Codex, OpenCode and Pi.

### Added
- `ach-cli environment hydrate` writes each Instruction of the Environment as an always-loaded rules file for Claude Code, OpenCode, Cursor, Antigravity and Pi. Codex does not get them, and Cursor gets none with `--global`.
- An Environment stays not ready while one of its Instructions has never been fetched (`status.unresolvedContextInstructions`).
- Audit: `examples/litellm/ach_audit.py`, a LiteLLM callback that writes one
  audit event per model, MCP tool and A2A agent call (`litellm.model.call`,
  `litellm.tool.call`, `litellm.agent.call`) with the caller, key, Environment,
  target, guardrail verdicts, tokens and cost, and no request or response
  content. "Connect ACH to LiteLLM" in the docs has the install steps.
- Docs: an "Audit events" reference page lists every audit event ACH and the
  callback write, and "Collect audit events" shows how to query them in Grafana
  Loki and give them their own stream and retention.
- `ach-cli environment hydrate` and `ach-cli local plugin install|update|uninstall` remind you to run `opencode service restart` when they add or remove an MCP server for OpenCode, which loads them when its service starts.
- ach-cli supports Antigravity CLI as a target (`--target antigravity`, `launch agy`): MCP servers in `.agents/mcp_config.json`, skills in `.agents/skills`, subagents in `.agents/agents`. Plugin commands are not installed: Antigravity has none.
- ach-cli supports Cursor as a target (`--target cursor`, `launch cursor`): MCP servers in `.cursor/mcp.json`, skills in `.agents/skills`, commands and subagents in `.cursor/`.

### Changed
- Audit: a caller refused on an admin route is recorded as
  `platform.admin.authz`. It was `platform.admin.keys.revoke`; update any alert
  or query that matches the old name.
- Audit: the `actor` of a `platform.ek.exchange` event is
  `<namespace>/<ServiceAccount username>`. It was the username alone.
- Audit: `operator.orphan-cleanup` events carry `action` and
  `actor` (`<namespace>/system:operator`), like every other event.
- `ach-cli` writes skills for Codex, OpenCode and pi to the shared `.agents/skills` directory (`~/.agents/skills` with `-g`). The next hydrate moves existing ones; a skill you edited in place is kept at its old path with a warning.
- ach-cli writes OpenCode MCP servers in the OpenCode 2 format (`mcp.servers`). OpenCode 1.x is not supported.

### Removed
- ach-cli no longer supports Gemini CLI as a target (`--target gemini-cli`, `launch gemini`).
- `--target pimono` and `--target pi-mono`: use `--target pi`.
- `environment hydrate --target pi` no longer renames a `state-pimono.json` left by an ach-cli older than 1.0.0.

## [1.1.27] - 2026-11-20

### Added
- Helm chart: a `platformApi.oauth.redirectHosts` entry that starts with `*.`
  allows every host under that domain (`*.example.com` covers
  `app.example.com` and `a.b.example.com`, not `example.com`). Web tools that
  run on one host per user, such as OpenChamber in a cloud workspace, can then
  sign users in to MCP servers. A `*.` entry trusts everyone who can serve a
  page under that domain.

## [1.1.26] - 2026-11-20

### Breaking
- Helm chart: the gateway no longer receives the top-level `extraEnv`, so the
  database password and any other credential set there stay out of the public
  entry point. If you set a gateway variable (`ACH_GATEWAY_*`) in the top-level
  `extraEnv`, move it to `gateway.extraEnv`.

### Added
- Helm chart: `platformApi.extraEnv` sets environment variables for
  platform-api only. The install guides put the Dex client there
  (`ACH_DEX_ISSUER_URL`, `ACH_DEX_CLIENT_ID`, `ACH_DEX_CLIENT_SECRET`), so no
  other component holds its secret. An install that keeps them in the top-level
  `extraEnv` still works; move them to tighten it.
- Releases publish `checksums.txt.pem`, the certificate `cosign verify-blob`
  needs to check the signature of `checksums.txt`.
- Conversation classifier (Enterprise): `classifier.modelApi: decisions` labels
  sessions with a decision model through LiteLLM's `/v1/decisions`. The label's
  confidence is then the model's probability for the category. The default,
  `chat`, calls `/chat/completions` as before.

### Changed
- The `ach` image is built on Alpine 3.23 (was 3.20).
- `ach` and `ach-cli` run with the Go 1.26 runtime defaults instead of the
  Go 1.23 ones: the thread count follows the container's CPU limit, and TLS
  SHA-1 signatures and RSA keys under 1024 bits are refused.

### Security
- platform-api answers 401 to a request whose `Authorization` is not an ACH
  credential (Basic, a raw LiteLLM key). `/platform/console/bootstrap` answered
  200 to it with the chat URL, the provider name and the Enterprise flag, and
  `/platform/hydrate` answered 500. The forwarder still forwards such a
  request for LiteLLM to authenticate.
- platform-api cuts a request body off at 1 MiB.
- platform-api sends `X-Content-Type-Options`, `X-Frame-Options`,
  `Content-Security-Policy: frame-ancestors 'none'` and `Referrer-Policy` on
  every response, so the console and the sign-in pages cannot be framed.
- Helm chart: the operator's permissions are namespaced. It reads Secrets and
  ACH resources in the release namespace only, through a Role in place of the
  ClusterRole. It never read anything outside that namespace.

## [1.1.25] - 2026-11-20

### Changed
- Helm chart: every Deployment prefers to place its pods on different nodes,
  and `platformApi`, `forwarder`, `gateway` and a standalone `contentService`
  get a PodDisruptionBudget (`maxUnavailable: 1`) when their `replicas` is
  above 1. No new values; installs at 1 replica render no budget.

### Fixed
- With `platformApi.replicas` above 1, replicas no longer refresh the same
  user's sign-in at the identity provider at the same moment, which answered
  503 and asked MCP clients to re-authenticate.
- The hourly identity-provider check of `ek_` owners retries an owner at the
  next sweep after a Valkey error, instead of skipping them for 24 hours.

## [1.1.24] - 2026-11-19

### Fixed
- MCP servers signed in through ACH no longer ask to re-authenticate when a
  client refreshes several of them at once (Claude Code reconnecting every
  server on start). The refreshes answered 503 or 500 because they replayed the
  user's sign-in at the identity provider in parallel and raced to replace the
  user's key; they now share one replay and the key one of them minted.
- Console Traces, Sessions view: a session's status is the outcome of its last
  model call. A session whose calls all completed shows Success instead of
  Unknown, and one that recovered from a failed call (for example an upstream
  rate limit its client retried) shows Success instead of Failed, with the
  number of failed calls beside it. The sessions API carries that count as
  `errors`.

## [1.1.23] - 2026-11-19

### Fixed
- A message sent to an agent through `/a2a/<name>/mcp` (`ach-cli a2a call`, or
  the `a2a-<name>` MCP server in a coding tool) returns the agent's answer when
  the agent restarts mid-run, instead of "the agent ended the stream without a
  final reply". ACH waits for the task up to 30 minutes; past that, the error
  names the task id.
- `ach-cli mcp call` and `ach-cli a2a call` ask the server for progress, so a
  long call is not closed as idle while the server reports it.

## [1.1.22] - 2026-11-19

### Changed
- Operator: a `KeyGrant` reconcile writes exchange key budgets only for the
  keys that grant covers. The LiteLLM `/tag/info` reads per 5-minute resync
  drop from grants × keys to one per key per covering grant. A ceiling that
  rises because another grant was edited off the key lands within 5 minutes.

### Fixed
- Console Traces counts one turn per request in a session and lists one row
  per request under Calls. A run from a client that sends its own trace context,
  such as OpenCode, showed as a single turn and a single call with no type or
  outcome. Opening a call shows that request alone.
- `ach-cli environment hydrate` no longer warns `preserving <file> — local
  edits detected; pass --force to remove` when the MCP server it meant to
  remove from a tool's config is already gone from that file.

## [1.1.21] - 2026-11-18

### Breaking
- `ach-cli mcp search` ranks partial word matches. Scripts that require every
  term to match should filter the JSON results. Search and `list --tools` exit
  successfully when some servers need provider sign-in and the rest respond.
  Check the JSON `errors` field when your script requires a complete inventory.

### Added
- MCP discovery caches tool metadata for five minutes. Use `--refresh` with
  `search` or `list --tools` to fetch it again. Both commands check server grants
  on every run.
- Copy a dotted tool name from MCP discovery into `describe` or `call`. Pass
  `-` to `call` to read a JSON object from a pipe or redirected file.

### Fixed
- MCP calls and descriptions give the same diagnostic for a server outside
  the active credential's catalog. The provider login example uses a catalog name.

## [1.1.20] - 2026-11-18

### Added
- Discover, inspect and call MCP tools from the shell with `ach-cli mcp`, including
  browser consent for providers that require it. Find and message A2A agents with
  `ach-cli a2a`. Both use the active credential from any directory.
- OAuth device clients can request provider consent for a specific MCP server
  by sending its URL as `resource` when starting device authorization.

## [1.1.19] - 2026-11-18

### Added
- `ach-cli local plugin install` and `update` (and the `skill` forms) take
  `--force`, to replace a file that already exists and that `ach-cli` did not
  install.
- Helm value `forwarder.testTagsHeader`, off by default, for test clusters:
  the forwarder also sends each request's budget tags as the
  `X-Achtest-Tags` header.

### Changed
- Upgrade guide: going back to an older chart version through Flux or Argo CD
  needs `migrate.enabled: false` when the release you are leaving added a
  migration. The migration Job otherwise fails with `no migration found for
  version N` and stops the upgrade. `helm rollback` is unaffected.
- The chart's default `genai.opencodePluginSpec` is `opencode-oidc-provider`
  v0.4.11: a token refresh waits up to 40 s for the authorization server,
  instead of failing at 15 s while the identity provider is slow.

### Fixed
- The forwarder sends the `X-Achtest-Tags` header upstream only with
  `forwarder.testTagsHeader` set. Before, every request carried it, with the
  caller's e-mail, Environment and key id, and LiteLLM passed it on to the
  model provider when `forward_client_headers_to_llm_api` was on.
- `ach-cli local repo add git:<url>` falls back to `GITLAB_TOKEN` only for a
  GitLab host (its name contains `gitlab` or starts with `git.`). Before, the
  token was sent to, and saved for, any `git:` host. For a GitLab under
  another host name, pass `--token`.
- `ach-cli environment hydrate` and `ach-cli local plugin install` (and
  `skill install`, and both `update` forms) do not replace a file that already
  exists with other content and that they did not install, such as an agent or
  command of your own with the same name: they name the file, leave it as it
  is and exit 7. Move the file away, or pass `--force`. The first hydrate of an
  Environment into a folder where another Environment installed the same
  plugin at an older version stops the same way: hydrate that one first.
- `ach-cli local plugin update` (and `skill update`) leaves a file you edited
  as it is. Before, it reported the file as skipped and then overwrote it.

## [1.1.18] - 2026-11-18

### Fixed
- After the operator's cache volume is lost or replaced, standalone Plugin,
  Skill, Prompt and Artifact content is fetched again on the next reconcile.
  Before, a resource whose upstream had not changed stayed `Synced` while its
  content answered 404 until its spec was edited.

### Changed
- The chart's default `genai.opencodePluginSpec` is
  `opencode-oidc-provider` v0.4.10: `opencode auth login` registers again
  when the saved client registration is older than 60 days, instead of
  failing once the authorization server has expired it.

## [1.1.17] - 2026-11-14

### Breaking
- The `ach` binary's default listen ports changed so every service can run in
  one container: `ach platform-api` listens on `:8083` (was `:8080`) and
  `ach forwarder` on `:8084`, health `:8085` (were `:8080` and `:8081`). The
  Helm chart sets its own ports and is unaffected. If you run the binary
  outside the chart, set `ACH_PLATFORM_API_BIND_ADDRESS`,
  `ACH_FORWARDER_BIND_ADDRESS` and `FORWARDER_HEALTH_BIND_ADDRESS` to keep the
  old ports.

### Added
- `ach all` runs the schema migration and then every service (operator,
  content-service, platform-api, forwarder, gateway) from one container, for
  installs outside Kubernetes. If one service stops, the others are stopped
  and the command exits with an error.
- `ach gateway` takes `ACH_GATEWAY_PLATFORM_API_URL`,
  `ACH_GATEWAY_FORWARDER_URL` and `ACH_GATEWAY_CONTENT_SERVICE_URL` to reach
  the services by an address other than their Kubernetes Service.

### Changed
- Documentation site: pages are grouped by reader, in the tabs Overview, ACH
  Platform (install and operate), Using ACH (developers), Integrate and
  Reference. Page addresses changed; for example the install guide is at
  `https://ackstorm.github.io/ach/platform/install-ach/`. The ACH Platform tab
  has an install tutorial and one page each for the prerequisites, the Helm
  install, sign-in, LiteLLM, topology, upgrades, Enterprise, JWT key rotation
  and troubleshooting.
- `genai.opencodeExtraProviders` names `claude-agent` as its example: OpenCode
  2.2.3 and later ship the Claude Agent provider built in, and the
  `@openchamber/opencode-claude` plugin (provider id `claude-code`) is archived.
  A deployment that lists `claude-code` for that plugin lists `claude-agent`
  instead once its users are on the built-in provider.

### Fixed
- The `ach-install` plugin (1.1.14) corrects six facts it gave agents: LiteLLM's
  `/ui`, `/key/*` and `/health` through ACH show the console, not a 404;
  losing Valkey's data signs every user out; deleting `ach-jwt-signing-keys`
  invalidates access tokens and does not sign users out; a user refused at the
  identity provider loses their `ek_` keys with a Dex build and connector that
  report it; Dex's message for a missing redirect address is
  `Unregistered redirect_uri`; and the LiteLLM master-key Secret must be in
  the release namespace.

## [1.1.16] - 2026-11-14

### Fixed
- `ach-cli launch` hydrates only when asked: `--environment` (or
  `ACH_ENVIRONMENT`) hydrates that Environment, and the new
  `--update-environments` (`--update-envs`) updates the Environments already
  in the folder. Without either, the tool starts as the folder is, instead of every launch
  updating the folder's Environments and rewriting the tool's configuration.

## [1.1.15] - 2026-11-13

### Added
- `ach-cli launch claude` keeps your Claude login and sends claude's model
  traffic through ACH with your ACH sign-in, refreshed on its own: no
  `ANTHROPIC_BASE_URL`, `ANTHROPIC_CUSTOM_HEADERS` or `apiKeyHelper` to set.
  `--profile` and `--key` pick the credential. Shell `ANTHROPIC_BASE_URL` and
  `ANTHROPIC_CUSTOM_HEADERS` are ignored with a warning; a Claude settings file
  that sets `ANTHROPIC_BASE_URL` stops the launch; `--models` is refused for
  claude.

### Changed
- `ach-cli launch` in a folder with no Environment starts the tool without
  hydrating, instead of refusing.
- Documentation site: one version, served at `https://ackstorm.github.io/ach/`
  (the `/dev/` and `/latest/` prefixes are gone), with pages grouped as
  Tutorials, How-to, Reference and Explanation. The Reference tab lists every
  `ach-cli` command, the Helm values, the forwarder JWT claims and this
  changelog. The How-to tab has a developer section: installing `ach-cli`,
  signing in, Environments, `launch`, MCP sign-in, model traffic, keys,
  `ach-cli local` and troubleshooting.
- Console Apps page: tiles fill the row at any width, each icon has its own
  color under every skin, and each tile shows the host it opens.

### Fixed
- `ach-cli admin --help` names the Helm values that make someone an admin
  (`platformApi.admins.users`, `platformApi.admins.sso`) instead of a variable
  that does not exist.

## [1.1.14] - 2026-11-12

### Added
- Default skin from the chart: `platformApi.console.theme` (`dark`, `light`,
  `pastel` or `red`) sets the skin of the console and the sign-in pages for a
  browser whose user has not picked one. The console's toggle still wins for
  that browser. Empty keeps following the browser's light/dark preference.
- Console Apps page: `platformApi.console.apps` lists tiles linking to the
  platform's other tools (name, url, optional group, subtitle and icon). A tile
  with `teams` shows only to members of any one of those teams, one with `admin: true`
  only to platform admins; the server filters the list, so the browser never
  receives a tile the user cannot see. The sidebar shows Apps between
  Observability and Help when at least one tile is visible. Hiding a tile is
  not access control: each app keeps its own login.
- `ach-cli environment status` tells whether a hydrated folder is up to date:
  per Environment, `up to date` or what changed since the last hydrate (`+`
  added to the Environment, `-` removed, `~` new content). It asks the server
  (`POST /platform/hydrate/version`) and downloads nothing; `--offline` keeps
  the local-only view. A folder hydrated by an older ach-cli says
  `version unknown` until its next hydrate. A state file written by this
  version cannot be read by an older ach-cli.

### Fixed
- An A2A agent's MCP face (`/a2a/<name>/mcp`) fetches the agent's card
  once per request instead of several times, so each tool call costs the agent
  one card request.

## [1.1.13] - 2026-11-11

### Security
- Built with Go 1.26.11, which fixes GO-2026-6859 (an HTTP/1 server crash in
  `net/http`).

### Added
- Admin from an IdP group: `platformApi.admins.sso` lists Dex `groups` whose
  members are admins, alongside the `admins.users` emails. Membership is read
  at login and at every token refresh, so someone removed from the group loses
  admin within an hour, with no restart (#47).
- Pin a git-sourced Plugin, Skill, Prompt, Artifact or marketplace to a commit
  with `spec.<github|gitlab|bitbucket>.commit`. ACH serves that commit, keeps
  watching `ref`, and reports a newer one in `status.latestUpstreamRev`, the
  `UpdateAvailable` condition and the AVAILABLE column of `ach-cli admin list`.
  Promote it by committing the new SHA (#48).
- An Environment's A2A agents now reach Claude Code, Codex, Gemini CLI, OpenCode
  and pi: `ach-cli environment hydrate` writes each one as an MCP server
  `a2a-<agent>` (forwarder `/a2a/<agent>/mcp`) with one tool, `send_message`,
  which shows the agent's progress and returns its answer. It replaces the
  `a2aAgents` / `[a2a_agents]` entries earlier versions wrote, which no tool
  read; the next hydrate removes them (#51).

### Fixed
- `ach-cli environment hydrate`, `environment uninstall` and `ach-cli local`
  no longer overwrite an edit saved to a tool's config file, `CLAUDE.md`,
  `GEMINI.md` or `.gitignore` while they run: they merge again on the saved
  file, and stop with an error, writing nothing, if it keeps changing.

### Changed
- Helm `platformApi.admins` is now a map: move your email list from
  `platformApi.admins` to `platformApi.admins.users`. A list-shaped value
  fails the render with that instruction.
- Changing `platformApi.admins.users` restarts platform-api on the next
  `helm upgrade`; before, the new list applied only after a manual restart.
- Enterprise licence (`ee/LICENSE`): enabling the Enterprise features in any
  environment, evaluation included, needs a signed agreement with ACKstorm.
  Development and test use without one is no longer granted. Running the
  image with `enterprise.enabled: false` needs no agreement.
- Leaver detection (added in 1.1.10) needs the Dex connector fixes
  dexidp/dex#5142 (`google`) and dexidp/dex#5144 (`gitlab`, `microsoft`,
  `github`, `gitea`, `bitbucketcloud`), merged 2026-11-10. No Dex release
  includes them yet (v2.46.1 does not). The 1.1.10 entry cited the tracking
  issue dexidp/dex#5143 instead.

### Documentation
- Supported LiteLLM range stated in the install guide: 1.108.0 or later,
  tested with 1.108.1.
- ACH follows Semantic Versioning: a breaking change bumps the major version
  and is listed under `### Breaking`.

## [1.1.12] - 2026-11-10

### Added
- `ach-cli` suggests the command you meant at every level, not only the top:
  `ach-cli keys revkoe` → `revoke`. Common synonyms also point to the right
  command: `update` → `upgrade`, `ach-cli hydrate` → `environment`,
  `environment pull|sync` → `hydrate`, `environment show` → `describe`,
  `keys delete|new` → `revoke`|`create`, `local plugin|skill upgrade|add` →
  `update`|`install`, `rm` → `remove`/`uninstall`/`revoke`.

## [1.1.11] - 2026-11-10

### Added
- Console: an ach-cli guide under Help (`#/howto/ach-cli`): install and
  update, sign in and profiles, Environments, environment keys and local
  packages.
- `ach-cli upgrade` replaces the running `ach-cli` with the latest GitHub
  release (or `--version X.Y.Z`), checked against the release's
  `checksums.txt`.

### Changed
- `ach-cli` warns only when it is older than the server, as `warning: ach-cli
  X is older than the server (Y)` plus `hint: upgrade with \`ach-cli
  upgrade\``; a newer CLI is no longer warned.
- `ach-cli keys create` prints the key once and stores it nowhere: put it in the
  secret store of whatever uses it. `--no-save` is gone, `--key` / `ACH_KEY`
  take only an `ek-…` key (no saved name), and `profile list|show` no longer
  show saved keys (`saved_keys` left `-o json`). A `keys:` block in an existing
  `~/.config/ach/config.yaml` still loads, is never read, and is dropped on the
  next write; those keys keep working on the server until revoked, and
  `keys revoke <name>` still finds them.
- `ach-cli environment` is the command's name and `--environment` the flag's
  everywhere (`keys list` and `admin keys list` took `--env`); `env` and
  `--env` keep working as aliases on every command.

## [1.1.10] - 2026-11-09

### Added
- When the identity provider behind Dex refuses a user (suspended, disabled or
  deleted there), every `ek_` that user owns, KeyGrant exchange keys included,
  stops working until they sign in again. ACH acts only on Dex's upstream
  rejection (400 `invalid_grant`, "The upstream identity provider rejected the
  refresh token."), so it needs a Dex build with dexidp/dex#5132 and a connector
  that reports the refusal (dexidp/dex#5143); `saml`, `oauth`, `keystone` and
  `authproxy` never do, and `ach-cli admin keys revoke --owner` stays the
  offboarding step there.
- platform-api re-validates, once a day, the owners of `ek_` keys used in the
  last 7 days, so a user who never signs in again is still caught.
- `POST /platform/keys/exchange` answers 403 `idp_refused` for a refused
  identity and mints nothing. The admin key inventory shows the reason
  `idp_refused` (`ach-cli admin keys list -o json`), as does the owner's own list.
- `ach-cli token`, `whoami`, `keys create`, `env describe`, `profile list` and
  `profile show` take `-o json`.

### Fixed
- The chart's Grafana dashboards load with auto-refresh off, so an open
  dashboard does not re-query its datasources on a timer; refresh by hand or
  pick an interval.
- Enterprise observability counts a generation's whole input under both
  Langfuse usage conventions (`input` with or without cache reads and writes):
  the Overview's uncached input ratio is no longer negative, its cache-read
  share stays at or below 100%, and sessions, traces, workloads, classification
  and the cost cache comparison stop undercounting input.
- platform-api presents a user's Dex refresh token once per refresh. A refused
  refresh was sent to Dex twice, which Dex can read as token reuse and which then
  ended every session of that user.

## [1.1.9] - 2026-11-07

### Fixed
- A rollout no longer cuts open requests after 10 s: gateway, forwarder and
  platform-api leave their Service before stopping (`shutdown.drainSeconds`,
  default 10) and let open requests finish for up to `shutdown.timeoutSeconds`
  (default 120, `ACH_SHUTDOWN_TIMEOUT`).
- platform-api has a readiness probe on `/readyz`, so it gets no traffic before
  Postgres and Valkey answer.

## [1.1.8] - 2026-11-07

### Fixed
- `ach migrate` never prints the database URL or password: a DSN that does not
  parse fails with a fixed message and driver errors are scrubbed. Percent-encode
  special characters in the password (see the `ach-install` prerequisites).
- `ach-cli` no longer sends `x-ach-key` to a different origin on a redirect.
- The http source no longer forwards its Secret-valued header across a
  cross-host redirect.
- `ach-cli local plugin install` and `uninstall` never widen the mode of an
  existing file: a `0600` runtime config stays `0600`.
- The forwarder drops a client `tags` header: only ACH sets request tags, on every
  route.
- The forwarder refuses a multipart body whose Content-Type repeats `boundary`
  or carries a starred `boundary*` with 400 `invalid_request`.
- An archive that fills its extraction cap exactly no longer lifts the cap for
  the entries after it; prompt archives share the skill cap.
- The console login `next=` refuses control characters, so a browser cannot
  normalise it to another origin.
- Release images are signed by digest, not by tag.
- `ach-cli local plugin|skill` install, update and uninstall refuse a project
  destination that leaves the folder through a symlink, as `env hydrate` does;
  `--global` is unchanged.
- A BackendIdentityPolicy `consentAudience` of `ach`, `ach-ek`, or starting with
  `mcp:` or `a2a:` is refused at admission, and the consent login skips the
  broker hop for such a value already stored.
- A private-repo token reaches `git` through its environment, not its command
  line (needs git 2.31 or later for `ach-cli local`).

### Removed
- The operator no longer looks up or deletes an Environment's access group under
  the names `ach-<env>` or `<env>`; only `ach-env-<env>` is used.
- OpenWork and its Den API: the `openwork.*` chart values and the `/openwork`,
  `/api/den` routes are gone, and the console bootstrap no longer carries
  `openwork_enabled`.

### Upgrade notes
- An install on v0.6.19 or earlier that jumps straight to this release leaves
  each Environment's old access group (named `ach-<env>` or `<env>`) in LiteLLM,
  orphaned and still bound to its teams; delete it by hand. Installs that ran
  any later release are unaffected.

## [1.1.7] - 2026-11-06

### Changed
- Docs, the console and the e2e suite follow LiteLLM v1.108's budget refusal:
  HTTP 422 with `type: budget_exceeded`, not 429. ACH keeps that default
  because clients stop at once and show why, instead of retrying a request
  that cannot succeed. The e2e LiteLLM pin moves to v1.108.1.
- `scripts/dev.sh` shares one Go build and module cache per machine, Go's own
  defaults, so a new worktree starts warm. Existing `.gocache/build` and
  `.gocache/gopath/pkg` directories can be deleted with `make clean-cache`.
- `make cluster-up` and `make e2e-run` refuse to start with under 5 GB free on
  the workspace disk.
- ACH requires LiteLLM 1.108 or later.
- The forwarder refuses request bodies on `/v1`, `/gemini` and `/a2a` that set
  a LiteLLM per-request control (`api_key`, `deployment_id`, guardrail fields,
  `mcp` tools) with 400
  `client_param_not_allowed`. Form bodies refuse the same top-level names.
- `/gemini` serves only the model routes (`GET /v1beta/models[/{model}]` and
  `POST /v1beta/models/{model}:{generateContent|streamGenerateContent|countTokens|embedContent|batchEmbedContents}`);
  other paths answer 404 `route_not_allowed`. A body or batch entry model that differs from
  the path model, or a POST body whose Content-Type is neither
  `application/json` nor absent, is 400 `model_mismatch`.
- Requests that outlive their key end within about a minute of the key being
  suspended, revoked or expired, including streams and `/v1/realtime`.
- `POST /platform/oauth/register` answers 413 over 4 KiB and 429 `rate_limited`
  past a per-address and a global limit, per replica.
- `ach-cli env hydrate` and `env uninstall` refuse a path in the workspace that
  resolves outside it (`--global` is exempt). In `--global` mode a recorded
  absolute target must sit under the tool's config root.
- While an Environment has unresolved references, the operator applies
  removals only: access can shrink, nothing is added.
- `POST /platform/admin/users/{email}/revoke-keys` normalises the email, walks
  every page of keys and answers 500 if a page fails; rerun it to finish. It
  ends the user's OAuth sessions once, with or without an active `pk_`, and
  answers 500 if that fails, after sweeping the keys; rerun it to end them.
- Ending a user's OAuth sessions also invalidates refresh tokens issued before
  a later login.

### Documentation
- Writing a content CR or a `LiteLLMConnection` is equivalent to reading every
  Secret in the ACH namespace; give such writers admin-tier trust.
- A user disabled at the IdP keeps working `ek-` keys; offboard with
  `ach-cli admin keys revoke --owner <email>`.

### Fixed
- A killed `make` no longer leaves its `ach-devtools` container running.

## [1.1.6] - 2026-11-06

### Changed
- Agent keys get the backend identity JWT: on an `/mcp` or `/a2a` target with a
  `forwardIdentityJWT` policy, an `ek_` arrives as its owner, with the same
  `sub`, `email` and `groups` as the owner's `pk_`, plus an `azp` claim
  (`ek:<environment>`) for audit. MCP backends no longer need the LiteLLM key,
  which LiteLLM v1.106+ stops forwarding.

## [1.1.5] - 2026-11-06

### Changed
- Releases go through a PR: `make release-cut` opens it with the rotated
  CHANGELOG and manifest bumps, and the merged commit is what gets tagged.
  GitHub release notes are that version's CHANGELOG section.
- The OpenCode bootstrap skill (`<provider>-ach`) loads on any mention of an
  Environment and installs a named one, instead of leaving agents to search
  the disk for it.
- The OpenCode bootstrap skill and the `ach-cli` plugin (1.1.4) give agents a
  background recipe for MCP sign-in, so they hand over the link instead of
  hanging the session on the browser wait.

### Fixed
- `GET /v2/model/info?model=<alias>` returns a row for a LiteLLM model-group
  alias, with the target model's vision, PDF, token-limit, tool and cost fields.
  Agents on an alias such as `ackstorm.smart` no longer lose images from tool
  results or run unpriced. A name the caller is not granted stays a 403.

### Removed
- The `pre-push` git hook, with `make pre-push`, `make hooks` and `make verify`.
  CI is the gate. Delete `.git/hooks/pre-push` from existing clones.

## [1.1.4] - 2026-11-05

### Fixed
- Deleting a `Plugin`, `Prompt`, `Skill`, `Artifact`, `BackendIdentityPolicy`
  or `LiteLLMConnection` now removes its Postgres row too. `Plugin`, `Prompt`
  and `Artifact` rows left soft-deleted still answered by name; their cached
  content was already gone.

### Removed
- The `deletion_timestamp` column on every table and the `deletionTimestamp`
  field of the Environment API view. Migration `000028` purges the remaining
  soft-deleted rows, then drops the column. Pods from an earlier version
  fail reads on those tables from the migration until the rollout replaces
  them. `helm rollback` below this release leaves them failing until
  `000028_drop_deletion_timestamp.down.sql` is applied by hand.

## [1.1.3] - 2026-11-04

### Changed
- **Breaking for older `ach-cli`:** `POST /platform/hydrate` now returns
  `runtime.models[].limits` (`{context, output}`) when LiteLLM knows them,
  aliases resolved. `ach-cli` decodes this response strictly, so v1.1.2 and
  older fail with `unknown field "limits"` until updated. Release this
  `ach-cli` first, then upgrade the server. ach-agent ignores the field and
  keeps working.
- Console pages use the full width, and Reliability is hidden from the sidebar.
  The page stays served at `/analytics/reliability`.

### Added
- `ach-cli` warns on stderr, once per run, when its version differs from the
  server's. platform-api reports its version in `X-ACH-Server-Version` on
  authenticated responses.
- `ach-cli launch` without `--environment` updates every Environment recorded
  under the folder's `.ach/`, then starts the tool.

### Fixed
- Deleting an `Environment` now removes its Postgres row. It used to stay
  soft-deleted forever: gone from `env list`, but still answering hydrate,
  `env describe`, the console and content downloads by name, reported as
  `Available`. Migration `000027` purges the rows already left behind.

## [1.1.2] - 2026-11-04

### Fixed
- Console catalogs: MCP cards show the gateway URL, Models lists agents
  published as models, and an empty A2A or MCP list reads as no grant rather
  than a disabled gateway.

## [1.1.1] - 2026-11-03

### Added
- `ach-cli launch <claude|codex|gemini|opencode|pi> --environment <env>`
  hydrates that one target, then starts the tool in `--dir` and exits with its
  code. A missing tool or a failed hydrate starts nothing.
- `ach-cli refresh` (`POST /platform/refresh`): after being added to an SSO
  group, join the teams it maps to (`platformApi.teams.sso`/`users`) without
  logging in again. It re-reads the groups at the identity provider; the
  Environments follow at their next reconcile.

### Fixed
- `KeyGrant` ServiceAccount names are validated as DNS-1123 subdomains.
- The console's Spend KPI becomes Budget when a cycle exists, plotting that
  cycle's days. The console range opens on the last 7 days.

## [1.1.0] - 2026-10-30

### Added
- `KeyGrant` (`ach.ackstorm.ai/v1alpha1`): a platform admin names one identity,
  its Environments and the ServiceAccounts (`<namespace>/<name>`) that may act
  as it, with an optional per-key budget. The operator projects it, reports
  `Ready` (`IdentityUnknown`, `EnvironmentMissing`), revokes exchange keys no
  grant allows any more and keeps each covered key's `key:<id>` budget at the
  lowest covering `spec.budget`.
- `POST /platform/keys/exchange`: a pod-bound ServiceAccount token plus a
  matching grant buys a short-lived ACH JWT (`aud: ach-ek`) standing for one
  stable `ek_` per identity, Environment and ServiceAccount; the `ek_` never
  leaves ACH. The token's signature is verified against the cluster JWKS before
  any rate limiting or TokenReview. The JWT lifetime is
  `platformApi.keyExchange.tokenTTL` (default `15m`, `1m` to `1h`). The
  forwarder, content service and platform API accept the JWT wherever an `ek_`
  goes. Every exchange emits a `platform.ek.exchange` audit event.
- Helm: platform-api `system:auth-delegator` binding (TokenReview); ClusterRole
  `<release>-keygrant-editor` (create, update, patch, delete on `keygrants`),
  bound to nobody.
- Plugin marketplace `ach` at the repo root (Claude Code and Codex) with three
  plugins: `ach-cli` (using the platform from the CLI), `ach-gitops` (writing
  ACH custom resources) and `ach-install` (deploying the Helm chart). Install
  with `claude plugin marketplace add ackstorm/ach`, or serve them to users
  through a `PluginMarketplace` and an Environment.

### Changed
- platform-api now holds a cluster-wide `system:auth-delegator`
  ClusterRoleBinding (it held no Kubernetes privilege before) and relies on the
  default `system:service-account-issuer-discovery` binding to read the cluster
  JWKS.
- The API-server audit level for `tokenreviews` must be `Metadata`; `Request`
  or `RequestResponse` would log the submitted ServiceAccount tokens.
- Operator RBAC for `keygrants`: get, list, watch, update, plus
  `keygrants/finalizers` and `keygrants/status`; never create, delete or patch.
- Exchange keys do not count against `max_keys`.
- Owner budget changes on exchange keys are refused (409
  `budget_managed_by_grant`); the grant's `spec.budget` is the only source.
- Migration `000026` adds `environment_keys.issued_via` and `requester` and the
  `key_grants` table; its down step refuses while live exchange keys exist.
- The `ach-gitops` and `ach-install` plugins (1.1.0) gain KeyGrant and
  key-exchange guidance.
- The OpenCode onboarding skill (`<provider>-ach`) is a bootstrap only: install
  `ach-cli`, sign in, list Environments, hydrate the default Environment. The
  rest moved to the `ach-cli` plugin, which the default Environment should carry.
- Chart `NOTES.txt` describes this chart (10 CRDs managed by Helm, the operator
  creating `LiteLLMConnection/default`); `platformApi.admins` is declared in
  `values.yaml`; the unused `watchNamespace` value is gone.

### Fixed
- `BackendIdentityPolicy` docs: when several policies target the same backend,
  the alphabetically FIRST one wins (the docs said last).

## [1.0.1] - 2026-10-30

### Fixed
- Console Enterprise Overview: the input statistics (median, maximum, reasoning
  share, period) sit in the fourth column of the input/output panel again instead
  of wrapping to a row of their own.

## [1.0.0] - 2026-10-29

### Added
- ACH 1.0.0: the operator, platform API (OAuth 2.1 AS, `pk_`/`ek_` keys, console),
  forwarder, content service, optional gateway, and the `ach-cli` user CLI.
- One container image, `ghcr.io/ackstorm/ach`. The Enterprise features
  (conversation classification, observability analytics, traces) are enabled with
  the Helm value `enterprise.enabled` and need an ACKstorm Enterprise licence
  agreement for production use; the rest is Apache-2.0.
