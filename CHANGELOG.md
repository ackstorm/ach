# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
