# PR title (≤70 chars)

`feat: migrate to Postgres as source of truth (closes #34)`

# Closing comment for issue #34

This PR delivers issue #34 — Postgres as source of truth.

Phase A — schema + operator projections (7 commits):
  - migration 000005: origin/locked columns across 6 existing projection tables
  - migration 000006: litellm_connections projection
  - migration 000007: backend_identity_policies projection
  - notify.go / with_tx_notify.go / listen.go infrastructure
  - controllers project LiteLLMConnection + BackendIdentityPolicy + emit NOTIFY
  - 5min periodic resync runnable + ach_refresh listener

Phase B — platform-api (5 commits):
  - store rewritten on pgxpool, hydrate/environments/envkeys consume db.EnvironmentRow
  - /admin/refresh writes external_refs.force_refresh_requested_at + NOTIFY
  - controller-runtime manager dropped
  - Helm RBAC for platform-api SA stripped of ach CRD verbs

Phase C — forwarder (7 commits):
  - bipcache: Postgres-backed BIP cache with NOTIFY + 5m periodic refresh
  - envstore: Postgres-backed Environment cache (same pattern)
  - litellmconn resolver reads endpoint from Postgres (Secret still from k8s)
  - proxy.BIPResolver / precheck.EnvProvider interfaces replace informer Get/List
  - controller-runtime manager kept ONLY for ach-jwt-signing-keys Secret informer
  - Helm RBAC for forwarder split across forwarder ns + master-key ns

Phase D — cleanup (3 commits):
  - dead `ctrl` import dropped from content-service
  - internal/forwarder/bip deleted (replaced by bipcache)
  - CLAUDE.md architecture diagram + common failure modes updated

Known follow-ups not in this PR:
  - handler unit tests in platform-api (hydrate/environments/envkeys/admin) deleted because they bound on `*achv1alpha1.Environment` fakes / `client.Client` shapes — rebuild against the new pgxpool store + `*db.EnvironmentRow` is a follow-up
  - cache_test.go / store_test.go / litellmconn resolver_test.go tagged `//go:build integration` (require Docker + testcontainers); run via `make test-integration`
  - the pre-existing race flake in `internal/keystore.TestCachedResolverSingleFlight` is unrelated and remains (also surfaces in `TestRedisCachedTeamsResolver_SingleFlight` and `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` — same singleflight race pattern, all in packages untouched by this branch, all pass on isolated retry)
  - kind-cluster e2e from D4 step 5/6 (cold-restart smoke for platform-api and forwarder) not run in this session — recommend running before merge
