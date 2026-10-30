# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Plugin marketplace `ach` at the repo root (Claude Code and Codex) with three
  plugins: `ach-cli` (using the platform from the CLI), `ach-gitops` (writing
  ACH custom resources) and `ach-install` (deploying the Helm chart). Install
  with `claude plugin marketplace add ackstorm/ach`, or serve them to users
  through a `PluginMarketplace` and an Environment.

### Changed
- The OpenCode onboarding skill (`<provider>-ach`) is a bootstrap only: install
  `ach-cli`, sign in, list Environments, hydrate the default Environment. The
  rest moved to the `ach-cli` plugin, which the default Environment should carry.

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
