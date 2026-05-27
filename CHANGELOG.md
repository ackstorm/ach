# Changelog

All notable changes documented per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Real CRD types for Environment, Plugin, PluginMarketplace, Artifact, Prompt, BackendIdentityPolicy, LiteLLMConnection (ported from ach-old; placeholder `Foo string` Spec/Status replaced with real fields).
- Shared `ExternalRef` types (RefreshBlock, SourceAuthSecretRef, 6 source kinds: GitHub, GitLab, Bitbucket, S3, GCS, HTTP).
- Reconciler logic for all 7 CRDs with shared external_ref refresh loop, alphabetical conflict detection (PluginMarketplace + BackendIdentityPolicy), AuthorizedTeams enforcement (Environment), endpoint reachability + pepper Secret probe (LiteLLMConnection).
- Platform REST API (chi router, Dex SSO + OAuth2 PKCE, OIDC verifier, Redis-cached keystore resolver) across 9 subpackages (admin, auth, environments, envkeys, hydrate, middleware, render, store, teams).
- MCP/A2A forwarder (Phase 1 stub with /healthz; real proxy lands later).
- Content service (Phase 1 stub with /healthz; sendfile(2) streaming lands later).
- DB migrations (3 files: personal/environment keys, marketplace plugins, litellm users) wired to `ach migrate` cobra subcommand.
- Internal packages: audit (structured logging), cachefs (PVC layout + sweep), config (env-var helpers), credhash (HMAC-SHA-256 with pepper), keys (pk_/ek_ format validation), keystore (DB-backed + Redis-cached resolver), litellm (typed REST client), connection (LiteLLMConnection materialization), snapshot (in-memory LiteLLM state cache), orphan (ek_/pk_ drain Runnable), sources (6 upstream fetchers), platformapi (9 subpackages).
- Cobra subcommand bodies: `ach operator` (controller-runtime manager with all 7 reconcilers), `ach platform-api` (chi server + manager.Runnable), `ach forwarder` (healthz stub), `ach content-service` (healthz stub), `ach migrate` (golang-migrate via internal/db).
- E2E phase invariants ported (stdlib testing, kind+kubectl orchestration, 5 phase test files + fixtures).
- Integration tests via testcontainers-go (build tag: integration; covers internal/db + platformapi/admin handler).
- Controller envtest suite ported (CEL admission matrix across all 7 CRDs, per-Kind finalizer tests, external_ref refresh tests, marketplace algorithm tests, main-wiring envtest).
- Single-binary kustomize manager Pod (operator + content-service co-located, shared RWO PVC, `args:`-selected cobra subcommand).
- Per-binary RBAC (ServiceAccount + Role + RoleBinding for operator/platform-api/forwarder/content-service).
- Multi-component Helm chart templates (per-mode Deployments gated by values.yaml toggles + migrate Job via pre-install/upgrade hook).

### Changed
- Replaced kustomize-generated `install.yaml` monolith with explicit per-mode Helm templates so each service is independently togglable.
- Replaced kubebuilder-default scaffolded `*_controller_test.go` Ginkgo skeletons with ach-old's real controller envtest specs (CEL, finalizers, refresh, marketplace, main-wiring).
- Replaced Ginkgo-bootstrapped `test/e2e/suite_test.go` with stdlib-testing TestMain orchestration (matches ach-old's pattern; preserves `E2E_SKIP_SETUP=1` lifecycle handoff to `make e2e` / `cluster-up`).
- Default outer fetcher transport for `github`, `gitlab`, `bitbucket`
  source types swapped from REST/SDK to git protocol
  (`git ls-remote` + shallow clone + `git archive`). Eliminates
  per-IP REST rate-limit (GitHub 60 req/h, GitLab 60 req/min,
  Bitbucket 60 req/h) as a failure mode. All four consuming CRD
  kinds — `Plugin`, `Prompt`, `Artifact`, `PluginMarketplace` —
  benefit transparently; wire contract
  (`FetchResult{Body: tar.gz, UpstreamRev: SHA}`) is unchanged.
- Auth: token is now passed to git via
  `http.extraHeader=Authorization: Bearer <token>` instead of
  URL-embedded form. Closes T-02-02-02 leak path (token no longer
  persists to `git config remote.origin.url` on disk; still visible
  in `/proc/<pid>/cmdline` for the duration of the subprocess,
  which is unavoidable without `GIT_ASKPASS` plumbing).
- `transport: git|rest` field added to `GitHubSource`, `GitLabSource`,
  and `BitbucketSource` (default `git`). `rest` is a one-release
  escape hatch; will be removed in the following release.
- `authSecretRef` is documented as optional on `GitHubSource`,
  `GitLabSource`, `BitbucketSource` (the schema already permitted
  `nil`; the doc-comment shift reflects that anonymous fetch is now
  the supported shape for public repos — the git transport has no
  per-IP rate-limit so dummy-Secret workarounds are no longer
  necessary).
- `authSecretRef.key` is now optional with a provider-specific default
  matching the ecosystem env-var convention:
    github    → `GITHUB_TOKEN`
    gitlab    → `GITLAB_TOKEN`
    bitbucket → `BITBUCKET_TOKEN`
  Explicit `key: <name>` still overrides.
- `SourceReachable=True` (Plugin/Prompt/Artifact) and `Synced=True`
  (PluginMarketplace) condition messages now include
  `transport=<git|rest|n/a>` so operators can see which wire path was
  used.

### Spec follow-up
- Hub spec §10.1 currently marks `authSecretRef` as required on the
  three git source types. The spec rev that reconciles this with the
  v1alpha1 reality (now optional) will land in the next spec cadence
  (`spec/ach_hub_spec_*` revision after `v20260515_FINALv4`).

### Fixed
- Pre-port CI hygiene (separate PR #3 merged before this branch): removed alitellm-graft duplicate workflows (lint.yml, test.yml, test-e2e.yml), added missing `docs/requirements.txt` for mkdocs/mike, dropped GHAS-gated Dependency Review step (govulncheck ack-list is the canonical guard), gated `make fuzz-short`/`fuzz-long` on package presence.
- `internal/sources/github` and `internal/sources/gitlab` now validate
  `spec.Repo` / `spec.Project` / `spec.Host` / `spec.Ref` for URL-
  structural metacharacters at `New` time, matching the existing
  `bitbucket` constructor (CR-02 parity; PR #9 follow-up review HIGH).
  Validators extracted to a shared `internal/sources/cr02validate`
  subpackage.
- `internal/sources/git.LsRemote` now installs an inner 30s
  `context.WithTimeout` (plus `cmd.WaitDelay=2s` to close pipes after
  SIGKILL — `git-remote-http` helper inherits them otherwise) so a
  stalled upstream cannot hang the reconciler regardless of caller ctx.
- `internal/sources/git.Fetcher` uses `os.MkdirTemp` instead of a
  manual `crypto/rand` nonce; the prior code silently ignored
  `rand.Read` errors and on failure produced a predictable temp-dir
  name (symlink-race vector on shared cache PVCs).
- `internal/sources/git.buildGitInvocation` refactored from
  `(subcommand string, args ...string)` (token-as-last-variadic, a
  footgun) to `(subcommand, token string, args ...string)` — token
  positional + mandatory, compiler-enforced.
- `internal/sources/gitlab.Fetcher.constructCloneURL` strips scheme
  prefixes case-insensitively (`HTTPS://` no longer leaks through).
  Strip + trim-right-slash extracted to a `normalizeGitLabHost`
  helper, called both at `New` time (so `cr02validate.HostIdentifier`
  inspects the normalized form) AND at clone-URL construction.
- `internal/controller/ach.resolveTransportName` switches on
  `sourceSpec.Type` instead of pointer-non-nil ordering, matching
  the registry's dispatch discriminator. Type strings extracted to
  named constants to satisfy goconst.
- `extractToken` error message on missing-defaulted-key now includes
  a hint identifying the resolved key as a default (`(default for
  github; set authSecretRef.key to override)`); applied uniformly
  across github / gitlab / bitbucket fetchers.
- Every `internal/sources/git` git subprocess invocation now carries
  explicit `-c protocol.allow=never -c protocol.https.allow=always
  -c protocol.file.allow=user` config, pinning the wire-protocol
  allow-list. `ssh://`, `git://`, `ftp://` are blocked; `file://`
  is permitted only when supplied as a top-level command argument
  (defense against CVE-2022-39253-class submodule URL injection).

## [0.1.0] - 2026-05-25

### Added
- Initial scaffolding (kubebuilder v4 multigroup, single-binary cobra, Helm chart, CI/CD, mkdocs, goreleaser).
