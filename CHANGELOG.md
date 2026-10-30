# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
