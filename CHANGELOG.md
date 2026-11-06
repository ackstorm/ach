# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
