# Repository layout (post-graft)

> Relocated from `CLAUDE.md` to keep the hub lean. Authoritative for the
> on-disk tree and the **synced-fixtures vs examples** distinction. Update
> this file in the SAME commit when the layout or fixture set changes
> (Documentation-hygiene rule).

```
ach/
├── .github/                 ← alitellm-graft; reconciled for ach
│   ├── workflows/           CI / release / docs / govulncheck / labeler
│   ├── CODEOWNERS, dependabot.yml, labeler.yml, ISSUE_TEMPLATE/, PR template
├── .goreleaser.yml          ← stable release config (single-binary)
├── .goreleaser.prerelease.yml   ← prerelease (alpha/beta/rc tags)
├── .goreleaser.snapshot.yml ← main-branch snapshot builds
├── Dockerfile               ← runtime image (golang builder → alpine + git)
├── Dockerfile.devtools      ← devtools container (scripts/dev.sh)
├── Dockerfile.goreleaser    ← release image, consumed by goreleaser
├── api/                     ← CRD Go types (ach.ackstorm.ai/v1alpha1)
├── cmd/ach/main.go          ← service-mode entrypoint (exit.DispatchAndRender)
├── cmd/ach/cmd/              ← cobra root + service-mode subcommands
│   ├── root.go               (Version, services root cmd)
│   ├── operator.go, platform_api.go, forwarder.go,
│   ├── content_service.go, gateway.go, migrate.go
├── cmd/ach-cli/main.go      ← user-CLI entrypoint (shares exit.DispatchAndRender)
├── cmd/ach-cli/cmd/          ← cobra root + user-facing subcommands
│   ├── root.go               (Version, cli root cmd)
│   ├── login.go, logout.go, whoami.go, config.go,
│   ├── env.go, env_keys.go, hydrate.go, admin.go
├── internal/                ← controllers + service implementations
│   ├── controller/           controller-runtime reconcilers
│   ├── platformapi/, forwarder/, contentservice/   service-mode code
│   ├── gateway/             route table + reverse proxy for `ach gateway` mode
├── config/                  ← kubebuilder kustomize overlays
├── deploy/helm/ach/         ← Helm chart shipped on release (per-mode toggles)
│   │                          (templates/gateway-deployment.yaml = ach-gateway
│   │                           Deployment+Service; the prod edge router)
│                            (deploy/kustomize/ deleted 2026-07-17 — Helm is the
│                             only supported install; see ach-project-spec.md)
├── docs/                    ← mkdocs site (api-reference auto-gen)
├── examples/                ← CURATED user-facing samples + golden hydrate.json
│   │                          (independent of the synced fixtures below; NOT
│   │                          auto-applied — copy/adapt by hand)
│   │                          (the legacy ToolHive 12-15 samples were deleted
│   │                           2026-07-17 — orphaned by the 2026-07-15
│   │                           architecture audit that removed all ToolHive
│   │                           scaffolding; see issue #59)
│   ├── prometheus-servicemonitor.yaml  example metrics scrape config
│   ├── test-mcp-jwt.sh        manual /mcp JWT trust-path helper
│   └── hydrate.json           Golden /platform/hydrate output (CLI e2e diffs vs this)
├── hack/boilerplate.go.txt  ← SPDX one-liner, prepended to generated files
├── references/              ← agent-facing internal docs (NOT on public site)
│   ├── upstream-sync.md      ← what was grafted from alitellm and adapted
│   └── security/govulncheck-acknowledged.md
├── scripts/                 ← dev.sh, cluster.sh, pre-push-check.sh, ...
├── test/                    ← e2e + utils
│   └── e2e/cluster/         numbered cluster bring-up stages (cluster.sh):
│       ├── 00-namespaces 01-base 02-ach(+secrets/) 03-test-backends
│       │     (03's ach-local-gateway.yaml nginx is now a DEV SHIM —
│       │      adds /dex + /metrics/<svc>, falls through to the prod
│       │      ach-gateway pod; it is NOT the primary router anymore)
│       ├── 04-objects/      SYNCED FIXTURES — all non-Environment ACH CRs
│       │                    (incl. the phase5 CS-exercise valid/invalid
│       │                    matrix: {prompt,artifact}-{valid,invalid};
│       │                    the plugin/marketplace fixtures were REMOVED —
│       │                    plugins are gated off via featuregate.PluginsEnabled)
│       └── 05-environment/  SYNCED FIXTURES — demo + demo-unresolved + env-valid
│                            + env-team-denied (SC2 unauthorized_team negative)
├── ROADMAP.md, CHANGELOG.md, SECURITY.md, MAINTAINERS.md, CONTRIBUTING.md
└── PROJECT, README.md, LICENSE, NOTICE
```

## Synced fixtures vs examples (independent collections)

`test/e2e/cluster/{04-objects,05-environment}/` holds the **synced test
fixtures** — the complete demo-ready ACH object set `scripts/cluster.sh`
applies as bring-up stages, gated healthy by `06-verify` (the `verify_all`
step). The e2e suite **asserts** against this synced state; tests do NOT apply
their own copies of these objects. The set includes a **valid/invalid matrix**
for the content-service exercise (`prompt-valid`/`prompt-invalid`,
`artifact-valid`/`artifact-invalid`, `env-valid`; the former
`plugin-valid`/`plugin-invalid` fixtures were removed — `Plugin` is gated off
via `featuregate.PluginsEnabled`, and the plugin CRDs are no longer shipped in
the chart's `crd-sources/`): `verify_all` gates the valid half to its healthy condition
(`SourceReachable`/`Available`) and the invalid half to its **expected failure
state** (`SourceReachable=False`, nonexistent upstream), so "everything is in
its known state" before tests run. `env-team-denied` is a third Environment
fixture for the SC2 `unauthorized_team` case: same context as `env-valid` but
`authorizedTeams` names a sentinel team absent from LiteLLM + the e2e user, so
it is `Available=False` BY DESIGN and `verify_all` gates it on
`ExecutionResourcesResolved=True` (the condition set in the same reconcile that
writes the projection row the content-service reads). Tests only create/delete throwaways for
mutation-specific checks (e.g. the SC4 staleness patch, the §11f drains).
`examples/` holds **curated, user-facing
samples** (the ServiceMonitor, the alert rules, the ach-cli initContainer, the
manual JWT helper) plus the golden `hydrate.json`. The two are independent —
moving or editing one does not touch the other.
