# test/e2e — ACH end-to-end suite

Stdlib `testing` Go files behind build tag `e2e`. No Ginkgo
(per memory `feedback_023_tier_framework_rejected`).

Activation: `make e2e-run` (builds `bin/ach` + `bin/ach-cli` with
`-tags=e2e`; assumes `make cluster-up` already invoked).

## Suite map

| File                                  | Asserts                                                                                                                |
|---------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `e2e_suite_test.go`                   | `TestMain` bootstrap (cluster setup unless `E2E_SKIP_SETUP=1`), shared `runCmd`/`runCmdLonger`/`envOr` helpers          |
| `phase1_invariants_test.go`           | Phase 01 ROADMAP SCs                                                                                                   |
| `phase2_invariants_test.go`           | Phase 02 SCs #1–#3 asserted against the real synced `04-objects` (caveman plugin/marketplace + `conflict-mkt-a/-b`); SC#4 size-cap lives in unit+envtest. Shared `waitForCondition`/`getConditionField`/`dumpOperatorLogs` helpers |
| `phase2_sc5_orphan_test.go`           | Phase 02 SC#5 — orphan-cleanup interval-floor + live revocation                                                         |
| `phase3_invariants_test.go`           | Phase 03 SCs #1–#6 (Platform API SSO + hydrate + revocation + audit)                                                   |
| `phase3_helpers_test.go`              | Port-forward + HTTP-client + audit-line parser helpers                                                                 |
| `phase4_invariants_test.go`           | Phase 04 SCs #1–#5 (Forwarder header rewrite + precheck + JWT mint + JWKS + audit), plan 04-09 helpers                 |
| `phase4_helpers_test.go`              | Phase 04 Forwarder helpers (SSO key acquisition, JWKS probe, BIP fixture seed)                                         |
| `phase4_bip_loop_test.go`             | Phase 04 BIP closed loop — single-shot JWT-present and JWT-absent MCP calls through both seeded routes                 |
| `phase4_environment_available_test.go`| TODO §9 acceptance — Environment Available composite condition (runs by default; opt out via `ACH_SKIP_PHASE4=1`)      |
| `phase4_promotion_test.go`            | §11 UAT promotion: force-refresh, BIP, marketplace, restart, hydrate-golden, finalizer matrix                          |
| `phase4_promotion_helpers_test.go`    | `forceRefreshAndAssert`, BIP finalizer probes, fixture-server bring-up, DB-count helpers     |
| `phase5_invariants_test.go`           | Phase 05 SCs (content-service sendfile path, env-cache observability, hydrate URL surface)                            |
| `phase5_helpers_test.go`              | Phase 05 helpers (port-forward, kubectl exec into pods, strace seam)                                                  |
| `cli_login_hydrate_test.go`           | Phase 06 CLI umbrella `TestPhase6CLI` — login (env-var-injected pk_) + whoami --verify + env list + keys create + hydrate byte-for-byte vs `examples/hydrate.json` (normalized) |
| `phase6_helpers_test.go`              | Phase 06 helpers (`phase6SuiteGuard`, `phase6WriteTempConfig`, `phase6NormalizeHydrate`, `phase6RunAch`)                |

## Focused dev loop

```bash
make cluster-up                                    # idempotent bring-up (kept)
make e2e-focus RUN='TestPhase4BIPClosedLoop'        # single-shot BIP/MCP route checks
make e2e-focus RUN='TestPhase4Promotion/SC11a'     # stdlib -run pattern
make e2e-focus RUN='TestPhase4Promotion'           # full §11 sub-suite
```

`make cluster-up` and `make cluster-sync` wait for the Ready `ach-mcp-echo`
backend and LiteLLM's live MCP discovery of the three seeded echo tools before
reporting readiness. The BIP loop calls are intentionally single-shot: cluster
readiness owns discovery convergence, so a focused test on a retained cluster
only proves the already-warmed registry and must not hide a failed readiness
gate with its own retry.

The seeded routes are `demo-mcp-jwt` (JWT), `demo-mcp-nojwt` (no JWT), and
`demo-mcp-consent` (JWT plus `consentBroker`; mcp-echo gates its audience on
`oauth:echo:state:<sub>`).

Two ACH releases share the kind hostPort 8080 (both names map to 127.0.0.1 in
devtools; CoreDNS rewrites both to the nginx shim for pods): `ach.e2e.local`
is the full release (namespace `ach-system`); `api.e2e.local` is the
**identity** release (`cluster/02-ach/identity.values.yaml`, Helm release
`ach-identity`, namespace `ach-identity`, database `ach_identity` on the shared
Postgres, valkey db 1, Dex client `ach-identity`, `installCRDs=false`) — ACH in
front of the same LiteLLM with no operator/CRDs/BIP. The shim picks the backend
by `server_name`. `TestIdentityProfile` covers it.

## Fixtures

| Path                                              | Used by                                                |
|---------------------------------------------------|--------------------------------------------------------|
| `fixtures/marketplace_*.yaml`                     | phase 2 SC#2                                           |
| `fixtures/plugin_*.yaml`                          | phase 2 SC#1 + SC#4                                    |
| `fixtures/marketplace_fixture_server.yaml`        | phase 2 fixture server (applied by `applyFixtureServer`) |
| `fixtures/phase4_marketplace_internal.json`       | §11c (served by `applyPhase4MarketplaceServer`)         |
| `phase3_fixtures/environment_*.yaml`              | phase 3 SCs #2/#3                                      |

## Re-capturing the hydrate golden

The hydrate wire-path golden is `examples/hydrate.json`, asserted by
`TestPhase6CLI` (CLI-driven). When the hydrate response shape
legitimately changes (e.g. a new field lands), re-capture it:

```bash
make cluster-up
make build-all
./bin/ach-cli login                                          # one-time SSO
./bin/ach-cli hydrate --environment demo > examples/hydrate.json
git add examples/hydrate.json
git commit -m "test(e2e): refresh hydrate golden (<reason>)"
```

If the change is just a per-cluster host difference (e.g. the
`downloadUrl` host), no re-capture is needed: the Phase 6 CLI suite's
`phase6NormalizeHydrate` helper handles the platform-api host
substitution automatically; see CLAUDE.md "Common failure modes" entry
"Hydrate output != examples/hydrate.json".
