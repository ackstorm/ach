# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed
- Documentation site: pages are grouped by reader, in the tabs Overview, ACH
  Platform (install and operate), Using ACH (developers), Integrate and
  Reference. Page addresses changed; for example the install guide is at
  `https://ackstorm.github.io/ach/platform/install-ach/`. The ACH Platform tab
  has an install tutorial and one page each for the prerequisites, the Helm
  install, sign-in, LiteLLM, topology, upgrades, Enterprise, JWT key rotation
  and troubleshooting.

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
- `genai.opencodeExtraProviders` names `claude-agent` as its example: OpenCode
  2.2.3 and later ship the Claude Agent provider built in, and the
  `@openchamber/opencode-claude` plugin (provider id `claude-code`) is archived.
  A deployment that lists `claude-code` for that plugin lists `claude-agent`
  instead once its users are on the built-in provider.

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
