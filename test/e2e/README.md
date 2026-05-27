# test/e2e — ACH end-to-end suite

Stdlib `testing` Go files behind build tag `e2e`. No Ginkgo
(per memory `feedback_023_tier_framework_rejected`).

Activation: `make e2e` (assumes `make cluster-up` already invoked).

## Suite map

| File                                  | Asserts                                                                                                                |
|---------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `e2e_suite_test.go`                   | `TestMain` bootstrap (cluster setup unless `E2E_SKIP_SETUP=1`), shared `runCmd`/`runCmdLonger`/`envOr` helpers          |
| `phase1_invariants_test.go`           | Phase 01 ROADMAP SCs                                                                                                   |
| `phase2_invariants_test.go`           | Phase 02 SCs #1–#4 + shared `applyFixtureServer`/`waitForCondition`/`getConditionField`/`dumpOperatorLogs` helpers      |
| `phase2_sc5_orphan_test.go`           | Phase 02 SC#5 — orphan-cleanup interval-floor + live revocation                                                         |
| `phase3_invariants_test.go`           | Phase 03 SCs #1–#6 (Platform API SSO + hydrate + revocation + audit)                                                   |
| `phase3_helpers_test.go`              | Port-forward + HTTP-client + audit-line parser helpers                                                                 |
| `phase4_invariants_test.go`           | Phase 04 SCs #1–#5 (Forwarder header rewrite + precheck + JWT mint + JWKS + audit), plan 04-09 helpers                 |
| `phase4_helpers_test.go`              | Phase 04 Forwarder helpers (SSO key acquisition, JWKS probe, BIP fixture seed)                                         |
| `phase4_environment_available_test.go`| TODO §9 acceptance — Environment Available composite condition (gated behind `ACH_E2E_PHASE9=1`)                       |
| `phase4_promotion_test.go`            | §11 UAT promotion: force-refresh, BIP, marketplace, restart, hydrate-golden, finalizer matrix                          |
| `phase4_promotion_helpers_test.go`    | `forceRefreshAndAssert`, `compareJSONShape`, BIP probes, fixture-server bring-up, DB-count helpers, hydrate driver     |

## Focused dev loop

```bash
make cluster-keep                                  # idempotent bring-up
make e2e-focus RUN='TestPhase4Promotion/SC11a'     # stdlib -run pattern
make e2e-focus RUN='TestPhase4Promotion'           # full §11 sub-suite
make e2e-focus FOCUS='registers via POST /model/new'   # legacy ginkgo
```

## Fixtures

| Path                                              | Used by                                                |
|---------------------------------------------------|--------------------------------------------------------|
| `fixtures/marketplace_*.yaml`                     | phase 2 SC#2                                           |
| `fixtures/plugin_*.yaml`                          | phase 2 SC#1 + SC#4                                    |
| `fixtures/marketplace_fixture_server.yaml`        | phase 2 fixture server (applied by `applyFixtureServer`) |
| `fixtures/phase4_marketplace_internal.json`       | §11c (served by `applyPhase4MarketplaceServer`)         |
| `fixtures/hydrate-golden.json`                    | §11e golden diff                                       |
| `phase3_fixtures/environment_*.yaml`              | phase 3 SCs #2/#3                                      |

## Re-capturing the §11e hydrate golden

When the hydrate response shape legitimately changes (e.g. a new field
lands), re-capture the golden:

```bash
make cluster-keep
bash examples/hydrate-demo.sh
cp examples/hydrate.json test/e2e/fixtures/hydrate-golden.json
git add test/e2e/fixtures/hydrate-golden.json
git commit -m "test(e2e): refresh §11e hydrate golden (<reason>)"
```

If the change is intentional drift on a single path (e.g.
`downloadUrl` host changes per cluster), prefer adding the path to the
`tolerated` map in `testSC11eHydrateGolden` over re-capturing.
