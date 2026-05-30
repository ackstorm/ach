---
phase: 04-hub-forwarder-jwt-trust-path
reviewed: 2026-05-26
depth: standard
files_reviewed: 38
findings:
  critical: 2
  warning: 9
  info: 7
status: issues_found
---

# Phase 04 Code Review

## Summary

Phase 4 lands the Forwarder runtime trust path with a coherent package layout (`internal/forwarder/{server,runnable,headers,jwt,bip,precheck,proxy,metrics}`), good test discipline on the pure-function surfaces (headers strip, JWT signer, JWKS, BIP resolver, precheck), and the manual key-rotation runbook. Two BLOCKER-level defects are present: (1) the controller-runtime cache is wired to watch `corev1.Secret` cluster-/namespace-wide while the shipped RBAC restricts the Forwarder ServiceAccount to `resourceNames: ["ach-jwt-signing-keys"]` — the bare LIST issued by the informer will be 403'd by the API server, blocking startup; (2) `bip.ResolveWinner` swallows `c.List` errors silently, collapsing transient cache failures to "no policy" without a log entry, which fails-open at the JWT-mint layer. Beyond these, the cobra `RunE` is largely untested today (deferred to E2E, three of which are `Skipf`'d), the proxy test set was condensed below the planned PR/H/TG matrix, and the JWT Secret volume mount in the Helm Deployment is dead weight because the loader fetches the Secret via the API client, not from the mounted files.

## Findings

### Critical

#### C1. Forwarder informer LIST will be RBAC-rejected — pod cannot start

**File:** `cmd/ach/cmd/forwarder.go:213-221` (Secret informer registration) ↔ `config/rbac/forwarder_role.yaml:23-26` and `deploy/helm/ach/templates/forwarder-rbac.yaml:23-26` (`resourceNames` carve-out)

**Issue:** `buildForwarderDeps` calls `mgr.GetCache().GetInformer(ctx, &corev1.Secret{})` against a manager whose cache config has no `cache.ByObject` selector for `*corev1.Secret`. Controller-runtime's informer issues an unfiltered `LIST /api/v1/namespaces/<ns>/secrets`. The shipped Role grants `verbs: ["get","list","watch"]` on `secrets` ONLY with `resourceNames: ["ach-jwt-signing-keys"]`. K8s RBAC accepts `list`/`watch` with `resourceNames` only when the request carries a `fieldSelector=metadata.name=<name>` matching the carve-out; the bare LIST controller-runtime emits has no such selector and will be returned 403 Forbidden by the API server. The forwarder will fail to come ready (informer never syncs; `/readyz` stays 503; rollout times out). This regression bypasses Phase 4 SC#4's "only the Forwarder ServiceAccount can read" check by trading off a hard refuse-to-start failure mode.

**Risk:** Forwarder pod never reaches Ready. Helm install never completes. Hides as "cache failed to sync within 30s" or `forbidden: User "system:serviceaccount:...:ach-forwarder" cannot list resource "secrets"` in informer logs.

**Fix:** Add a field selector to the cache configuration so controller-runtime forwards `?fieldSelector=metadata.name=<jwtSecretName>` on LIST/WATCH:

```go
mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
    Scheme:                 forwarderScheme,
    LeaderElection:         false,
    HealthProbeBindAddress: "0",
    Metrics:                metricsserver.Options{BindAddress: "0"},
    Cache: cache.Options{
        DefaultNamespaces: map[string]cache.Config{cfg.Namespace: {}},
        ByObject: map[client.Object]cache.ByObject{
            &corev1.Secret{}: {
                Field: fields.OneTermEqualSelector("metadata.name", cfg.JWTSecretName),
            },
        },
    },
})
```

Verify under envtest (real apiserver + locked-down ServiceAccount) before declaring the SC#4 RBAC check passing.

#### C2. `bip.ResolveWinner` silently swallows List errors → JWT mint silently skipped

**File:** `internal/forwarder/bip/index.go:67-73`

**Issue:** `if err := c.List(ctx, &list, ...); err != nil { return nil }` collapses every transient list failure (cache desync, API timeout that bypassed cache, namespace mid-rotation, malformed indexer key returned for some BIP object — unlikely but recoverable) into the same return value as "no BIP exists", with no log emission and no metric increment. The per-route handler interprets nil as "no policy, forward without JWT". Backends with `forwardIdentityJWT: true` policies will be silently bypassed by the JWT trust path on cache hiccups, while metrics record only `IncJWTSuppressed(kind, "no_policy")` — masking the failure as an intentional opt-out.

**Risk:** Silent loss of the JWT trust path on intermittent cache errors. Operators cannot tell a "no policy" outcome from "list failure" in logs or metrics; backends downgrade to whatever fallback they have (which may be "no auth" if they trust the Forwarder + LiteLLM shared key alone).

**Fix:** Distinguish the error path. Log at warn, increment a distinct suppressed-reason counter, and return nil (fail-open at JWT layer is correct — failing closed would reject every /mcp request during a transient cache blip):

```go
if err := c.List(ctx, &list, ...); err != nil {
    // Defense: log + metric so silent fail-open is observable.
    ctrl.Log.WithName("forwarder.bip").Error(err, "BIP list failed; treating as no-policy",
        "kind", kind, "name", name, "namespace", namespace)
    metrics.IncJWTSuppressed(kind, "list_failure") // new reason in enum
    return nil
}
```

(Adding `list_failure` to the Hub §18.5 normative reason enum is a doc-side follow-up; flag it in the next phase's planning.)

### Warning

#### W1. Helm Secret volume mount is dead weight — loader fetches via API client instead of reading mounted files

**File:** `cmd/ach/cmd/forwarder.go:246-257` vs `deploy/helm/ach/templates/forwarder-deployment.yaml:38-46,90-93`

**Issue:** PATTERNS.md / CONTEXT D-Discretion specifies the Secret mounted at `/etc/ach/jwt/{current.kid,current.seed,...}` with the loader reading files. Implementation creates a SECOND `client.New(ctrl.GetConfigOrDie(), ...)` direct API client and `apiClient.Get(ctx, key, secret)` instead. The volume mount becomes a defense-in-depth existence check (pod fails to start if Secret missing — but only because `optional: false`), while the actual data path is the API client + informer. The two surfaces can drift: a Secret edited by a non-cluster-aware tool (e.g. `mv` on the underlying file in the projected volume cache) would not be observed since the loader doesn't read disk.

**Risk:** Plan drift; surface confusion; future maintainer changing the mount path expects Reload to pick it up. No runtime impact today.

**Fix:** Either (a) drop the volume mount + volumeMounts blocks from `forwarder-deployment.yaml` and document the API-client load path explicitly, OR (b) switch `jwt.SecretLoader` to read from `/etc/ach/jwt/` and have the informer's event handler trigger a re-read of the files (kubelet refreshes the projected files within a few seconds of the Secret changing). (a) is the cheaper fix.

#### W2. `validateForwarderConfig` parses `ACH_LITELLM_BASE_URL` but discards the parsed URL; `buildForwarderDeps` re-parses

**File:** `cmd/ach/cmd/forwarder.go:118-124` and `cmd/ach/cmd/forwarder.go:286-289`

**Issue:** Lines 118-124 parse the URL to validate scheme, then throw the `*url.URL` away. Lines 286-289 parse the SAME string again to produce `out.server.LiteLLMUpstream`. If the parse can fail at validate time, it succeeds at build time (and vice versa) deterministically — but the redundant parse is a maintenance hazard. A future hand making validation stricter (e.g. requiring a non-empty `u.Host`) would need to touch both.

**Risk:** Drift between validate-time scheme check and build-time parse semantics.

**Fix:** Store the parsed URL on `forwarderConfig.LiteLLMUpstream *url.URL` in validate, drop the re-parse in build.

#### W3. `validateForwarderConfig` swallows `MustEnvIntPositive` error AND mis-validates `ACH_REDIS_DB=0`

**File:** `cmd/ach/cmd/forwarder.go:135`

**Issue:** `cfg.RedisDB, _ = config.MustEnvIntPositive("ACH_REDIS_DB", 0)` — the `_` discards a real error (`ACH_REDIS_DB` set to non-numeric or negative). Additionally, `MustEnvIntPositive` treats `0` as an error (rejects zero by contract per `internal/config/config.go:107`), but DB 0 is the default Redis logical database number and a legitimate value. Setting `ACH_REDIS_DB=0` explicitly fails validation; the error is silently ignored and `cfg.RedisDB` ends up `0` anyway. The validation surface is inconsistent with the rest of the codebase that uses `MustEnvIntPositive` for size limits (where `0` truly is invalid).

**Risk:** A user who tries to set `ACH_REDIS_DB=-1` to "disable" thinking it's optional gets a config bug silently. A user setting it to `7` gets validated. A user explicitly setting `0` hits a contradictory rejected-but-ignored path.

**Fix:** Use a different helper (e.g. an `EnvIntNonNeg` that allows zero) OR check the error and return it:

```go
cfg.RedisDB, err = config.EnvIntNonNeg("ACH_REDIS_DB", 0)
if err != nil { return nil, err }
```

#### W4. Proxy `Director` no-ops on `KeyContext` absent — strip pass still writes empty `x-litellm-key-id`

**File:** `internal/forwarder/proxy/proxy.go:71-75`

**Issue:** When `middleware.KeyContextFromCtx(req.Context())` returns `ok=false` (e.g. handler called without going through Authn — defensive only since chi groups gate routes through Authn), `litellmToken` stays `""` and `headers.StripAndRewrite` writes `x-litellm-key-id: ""` to the upstream request. LiteLLM will see an empty key ID. For ACH-internal callers this is correct (LiteLLM rejects on missing API key, not key_id), but if some future test bypasses Authn and reaches the Director, the upstream gets a misleading empty header rather than no header at all.

**Risk:** Subtle test-only foot-gun. Real traffic path is correctly gated.

**Fix:** Only write `x-litellm-key-id` when a token is present:

```go
if kc, ok := middleware.KeyContextFromCtx(req.Context()); ok && kc.LiteLLMToken != nil && *kc.LiteLLMToken != "" {
    headers.StripAndRewrite(req.Header, deps.LiteLLMSharedKey, *kc.LiteLLMToken)
} else {
    headers.StripAndRewrite(req.Header, deps.LiteLLMSharedKey, "")
    req.Header.Del("X-Litellm-Key-Id") // re-strip what StripAndRewrite wrote
}
```

(Or update `StripAndRewrite` to skip writes when the value is empty.)

#### W5. JWT key-rotation runbook step 5 uses `op:replace` on possibly-absent `next.kid`/`next.seed`

**File:** `docs/runbooks/jwt-key-rotation.md:79-82` and `:109-113`

**Issue:** Step 2 uses `{"op":"replace","path":"/data/next.kid","value":...}`. If the Secret was created with only `current.kid` + `current.seed` (the e2e fixture at `test/e2e/fixtures/phase4_jwt_signing_keys_seed.yaml` is exactly this case — no `next.kid` data field), JSON Patch `replace` fails with "missing path" because RFC 6902 §4.3 requires the target path to exist. The operator follows the runbook verbatim and the rotation command errors out.

**Risk:** First rotation on any deployment fails until the operator switches to `op:add`. Pure docs bug.

**Fix:** Use `op:add` (RFC 6902 §4.1 — succeeds whether the path exists or not, replacing if present). Step 5 also uses `replace`; consider `add` there for the `next.*` clears too (empty-string `add` on an existing path is well-defined).

#### W6. E2E fixture's deterministic 32-byte seed (all 0x42) materializes a Secret label `test.ach.ackstorm.ai/phase: "4"`, but no fixture safety net prevents apply-to-prod

**File:** `test/e2e/fixtures/phase4_jwt_signing_keys_seed.yaml:14-30`

**Issue:** The seed bytes `QkJC...` decode to 32 bytes of `0x42`. The comment explicitly says "DANGER: this file MUST never be applied to a non-test cluster. The seed is widely known." There's no machine-readable guard (e.g. a namespace requirement, kustomize patch that requires `test.ach.ackstorm.ai/test-cluster=true` on the Namespace, or an admission webhook). A copy-paste into a prod kubectl session compromises the JWT signing material for that deployment.

**Risk:** Operational footgun. The deterministic seed is the only reason SC#4 can assert a known JWKS `x` field across runs.

**Fix:** Pre-pend a `kind: Namespace` + a `kustomization.yaml` that requires the namespace to carry `ach.ackstorm.ai/test-cluster=true` via a `commonLabels` or a `patches` block. Alternatively, rename the file `*.UNSAFE.yaml` and add a CI gate that rejects it appearing in a `helm install` context. Cheapest fix: name the namespace something like `ach-e2e-test-DO-NOT-USE-IN-PROD` in the fixture and add a `kubectl create namespace` requirement to the runbook.

#### W7. Mock Dockerfile uses `golang:1.26` — does not exist as of 2026-05

**File:** `test/e2e/mock/Dockerfile:1`

**Issue:** `FROM golang:1.26 AS builder`. The latest Go release as of the review date is Go 1.23 (with 1.24 in beta). `1.26` will fail `docker pull` on first use, blocking the e2e mock build. The Dockerfile.devtools at the repo root will pin a real version.

**Risk:** First-time mock build fails. Engineer must patch the Dockerfile.

**Fix:** Match the version pinned in the project's main Dockerfile / `Dockerfile.devtools` (likely `golang:1.23` or whatever go.mod targets).

#### W8. Handler test `H6` confirms 403 envelope on precheck fail, but no test asserts upstream is NOT reached on signing failure — handler exists but no test verifies `metrics.IncJWTSuppressed("...", "signing_failure")` is emitted

**File:** `internal/forwarder/proxy/handlers_test.go:396-437` (TestHandlerMCP_SigningFailure)

**Issue:** `TestHandlerMCP_SigningFailure` checks the 500 envelope and upstream call count == 0, but does NOT inspect the metric counter (because `metrics.IncJWTSuppressed` is a no-op stub today — by design, per Plan 04-01). The deviation is the test surface that ALSO doesn't observe the call (e.g. via a test-only `var lastSuppressedReason string` in metrics). Phase 5 will replace the stub body with prometheus; the call site won't change, but the absence of any seam to assert the call happened means Phase 5 will need to add coverage for FWD-08 metric emission without inheriting any Phase 4 baseline.

**Risk:** Phase 5 inherits an under-tested counter call-site graph. No present runtime impact.

**Fix:** Either expose a test seam (e.g. swap `metrics` for an injected `MetricEmitter` interface in `HandlerDeps`) OR explicitly note in `04-09-VERIFICATION` that Phase 5 owns the prometheus call-site assertion. Pre-decision: the lower-disruption fix is to ship a test-only `package metrics` build tag that records calls in a slice; visit during Phase 5 OBS work.

#### W9. Forwarder `apiClient` creation duplicates `ctrl.GetConfigOrDie()` rather than passing the manager config

**File:** `cmd/ach/cmd/forwarder.go:246-249`

**Issue:** `apiClient, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: forwarderScheme})`. `ctrl.GetConfigOrDie()` was already called at line 191. Each call re-evaluates the in-cluster config (KUBERNETES_SERVICE_HOST + token file + CA cert) — cheap but redundant. More importantly, `client.New` here is used because the cached client requires `mgr.Start()` to populate, and the LoadOnce path runs before manager start. The right primitive is `mgr.GetAPIReader()`, which is the explicit uncached reader controller-runtime offers exactly for this pre-cache scenario.

**Risk:** Idiomatic drift; future maintainer wonders why two configs are constructed.

**Fix:** Replace lines 246-254 with:

```go
secret := &corev1.Secret{}
key := types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.JWTSecretName}
if err := mgr.GetAPIReader().Get(ctx, key, secret); err != nil {
    return out, fmt.Errorf("get Secret %s/%s: %w", cfg.Namespace, cfg.JWTSecretName, err)
}
```

### Info

#### I1. `tags.go`: tag deduplication absent — repeated injection appends duplicates

**File:** `internal/forwarder/proxy/tags.go:100-101`

**Issue:** `tags = append(tags, tagPrefix+environmentName)` blindly appends. A request body already containing `metadata.tags: ["environment:prod"]` injected upstream and then re-routed through the Forwarder gains a second identical tag. LiteLLM's tag aggregation likely dedupes, but the spec doesn't promise that.

**Risk:** Cosmetic; bytes-on-the-wire bloat.

**Fix:** Skip the append when `slices.Contains(tags, tagPrefix+environmentName)`. One-line fix using stdlib `slices`.

#### I2. `precheck.checkPk` lists all Environments in the namespace per request — O(N) on a hot path

**File:** `internal/forwarder/precheck/check.go:128-153`

**Issue:** Each `/mcp/{name}` or `/a2a/{name}` request from a `pk_` caller does a namespace-scoped `EnvironmentList` via the cache (no apiserver round-trip thanks to the informer, but still O(N_envs × N_teams)). The PATTERNS analog (`internal/platformapi/teams/lookup.go`) acknowledged this would be replaced; Phase 4 inherits the linear scan. Spec out-of-scope for v1, but flag it for the SLO conversation.

**Risk:** Acceptable until N_envs grows past ~hundreds in a single namespace.

**Fix:** Phase 5 or later: add an indexer over `Environment.spec.runtime.mcpServers[]` / `a2aAgents[]` so the lookup is O(log N). The same indexer Phase 4 introduced for BIP serves as the template.

#### I3. `e2e/phase4_invariants_test.go` SC#5 is `Skipf`'d as manual — automation surface exists for it

**File:** `test/e2e/phase4_invariants_test.go:187-189`

**Issue:** SC#5 (`refuse-to-start on non-HTTPS ACH_BASE_URL`) skips with "engineer-manual: helm upgrade with ACH_BASE_URL=http://... and observe CrashLoopBackOff". The actual SC can be exercised programmatically: `kubectl set env deployment/ach-forwarder ACH_BASE_URL=http://invalid` → poll `kubectl get pod -l app.kubernetes.io/component=forwarder -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}'` until `CrashLoopBackOff` or timeout → revert. ~15 lines of Go using the bounded `exec.Command` pattern the helper file already establishes.

**Risk:** SC#5 acceptance criterion unverified end-to-end.

**Fix:** Promote SC#5 from Skip to an automated subtest that mutates the Deployment + waits + reverts.

#### I4. Proxy `handlers_test.go` is in the same `package proxy` (not `proxy_test`) — couples tests to unexported symbols

**File:** `internal/forwarder/proxy/handlers_test.go:3`

**Issue:** `package proxy` (internal test package) lets tests poke at unexported `keyTypeFor`, `routeFor`, `classifyPrecheckErr`, the `outcome*` constants. The `headers_test.go` file uses the external test package convention (`package headers_test`). Consistency drift across files.

**Risk:** Style only.

**Fix:** Decide per-package: either internal tests (proxy chose this) or external. The mixed convention across one phase is a smell — propose external (`proxy_test`) and re-export `keyTypeFor`/`routeFor` as `KeyTypeFor`/`RouteFor` or unit-test them indirectly through `HandlerV1`/`HandlerMCP`.

#### I5. JWT `freshSeed` (signer_test.go) and `mustSeed` (jwks_test.go) duplicate the same helper

**File:** `internal/forwarder/jwt/signer_test.go:21-28` vs `internal/forwarder/jwt/jwks_test.go:17-24`

**Issue:** Both helpers do the identical thing (`make([]byte, 32); rand.Read(seed); ...`). Both files are in the same `package jwt`. One helper would suffice.

**Risk:** None.

**Fix:** Delete `mustSeed` from `jwks_test.go`; have it call `freshSeed`.

#### I6. `internal/forwarder/proxy/proxy.go:69` clears `req.Host = ""` after Director runs — the comment justifies it ("never leak client-supplied Host") but the same is achievable with `httputil.ReverseProxy.Director` default behavior

**File:** `internal/forwarder/proxy/proxy.go:67-69`

**Issue:** Setting `req.Host = ""` forces Go's HTTP client to derive Host from `req.URL.Host`. This is correct, but the inline comment "never leak client-supplied Host to LiteLLM" obscures that `httputil.ReverseProxy` does NOT preserve the client Host by default — only when you explicitly set `Director` to leave `req.Host` alone (which the default Director does, copying `r.URL.Host`). The defensive clear is fine; the comment makes it sound like a security boundary it isn't.

**Risk:** Documentation accuracy only.

**Fix:** Tighten the comment to "Force Go to derive Host from req.URL.Host (defensive — not a security boundary; httputil.ReverseProxy already does this by default)."

#### I7. `IncRequests` outcome enum in metrics counters.go documents 9 values; handlers.go's `classifyPrecheckErr` emits 6, plus "forwarded", "upstream_unreachable", "internal_error" = 9. But the doc enum also lists "expired_or_revoked" and "invalid_key_format" which the Forwarder code never emits

**File:** `internal/forwarder/metrics/counters.go:10-13`

**Issue:** The doc comment says outcome ∈ `{forwarded, unauthorized_resource, unauthorized_team, expired_or_revoked, litellm_unreachable, internal_error, invalid_key_format, invalid_key_type, https_required}`. The Forwarder code never increments with `"expired_or_revoked"`, `"invalid_key_format"`, or `"https_required"`. Phase 5's wiring of prometheus will inherit a label set wider than the actual emit set — labels with zero counts forever.

**Risk:** Cardinality bloat; metric consumers see labels that never increment.

**Fix:** Either narrow the doc enum to what Phase 4 actually emits (cleaner) OR add the missing call sites (e.g. `Authn` middleware would normally emit `expired_or_revoked` — but the Forwarder reuses platformapi/middleware.Authn which already returns 401 envelopes WITHOUT calling metrics). Pre-decision: trim the doc enum to `{forwarded, unauthorized_resource, unauthorized_team, litellm_unreachable, internal_error, invalid_key_type, upstream_unreachable}`.

## Coverage Gaps (test-specific)

Plan acceptance criteria NOT exercised by any test landed in this phase:

1. **Plan 04-07 PR/H/TG matrix condensed:** Original plan ACs called out tests PR1-PR8 (8 proxy tests), H1-H16 (16 handler tests), TG1-TG13 (13 tag-injection tests). Landed: 6 proxy, 7 handler, 9 tag = 22 of the planned 37. Specifically missing:
   - `PR4`: Director attaches `X-Forwarded-For` correctly with chained proxies (RFC 7239 compliance). No test asserts the X-Forwarded-For default behavior is preserved through the strip pass.
   - `H2/H5/H7/H8/H10/H12/H13/H15/H16`: handler variants for `gemini` route, `unauthorized_team` outcome, `litellm_unreachable` outcome (returns 503), pk_ on /mcp/ with successful team intersection, BIP opt-out (winner exists but ForwardIdentityJWT=false), JWT Sub composition with empty namespace, JWT Aud composition for /a2a, BIP-resolve-failure metric path (which compounds with finding C2), and explicit "no signer.Loaded" handler-side response.
   - `TG4/TG6/TG12`: `metadata` is a non-object JSON value (string/number/array), max body exactly at the cap (boundary), and the ContentLength header de-sync after rewrite.

2. **Plan 04-08 RunE wiring deferred to e2e:** The cobra `runForwarder` flow (`validateForwarderConfig → buildForwarderDeps → runForwarderServer`) has zero unit coverage. Three of the five SC#1–SC#5 e2e subtests are `Skipf`'d:
   - SC#2-tag (FWD-06 tag injection in cluster) — pending LiteLLM mock with body capture endpoint.
   - SC#3 (BIP alpha-LAST + JWT mint with backend echo) — pending MCP echo backend wiring.
   - SC#5 (FWD-10 non-HTTPS refuse-to-start) — explicitly engineer-manual.
   The mock binary at `test/e2e/mock/main.go` includes the body capture + auth echo endpoints needed for SC#2-tag and SC#3 — the e2e suite just doesn't drive it. Wiring effort: low; finding I3 covers SC#5.

3. **Plan 04-09 RBAC negative test runs only when Forwarder is Ready:** `phase4AssertSecretRbacNegative` uses `kubectl auth can-i --as=platform-api-sa`. If C1 lands as expected (informer LIST 403), the Forwarder never becomes Ready and the RBAC negative test never even runs — so the OPERATOR side bug masks the test entirely. The RBAC negative test should run as a standalone subtest gated only on the cluster + Helm being installed, not on the Forwarder being Ready.

4. **No envtest for the Secret reload event handler path in `cmd/ach/cmd/forwarder.go`:** The FilteringResourceEventHandler at lines 264-281 wires `Reload` to AddFunc/UpdateFunc. The `secret_test.go` covers `LoadOnce`/`Reload` semantics in isolation. The wiring (FilterFunc correctness, reload-on-add-during-initial-sync semantics) has zero coverage. Would catch C1 directly with a real envtest + locked-down ServiceAccount.

## Recurring Patterns

Observations across multiple files:

- **`--no-verify` justification confessed but unverifiable:** Every phase-4 commit landed with `--no-verify` per the prompt's framing (worktree gitdir bug). The pre-push 17 gates would have caught: gosec (any G104 ignored errors — finding W3 is exactly that), govulncheck regressions, license header drift, and `make lint` violations. Now that `scripts/dev.sh` has the worktree gitdir mount fix (commit `cdd4439`), the next phase-4 follow-up commit MUST re-run the full pre-push gate against `dbddaa8..HEAD` to confirm no smuggled regressions. Findings W3 (swallowed error) and likely some lint hits would surface there.
- **`mockTeamsResolver` duplicated between `internal/forwarder/precheck/check_test.go` and `internal/forwarder/proxy/handlers_test.go`:** Same interface, near-identical impl. Could live in a shared test helper package (`internal/forwarder/internal/testfakes/`?) or be promoted to `internal/keystore/testfakes`. Phase 5 will likely add a third copy in `internal/contentservice/`.
- **Doc comments are extensive but occasionally overshoot the code:** `proxy.go:67-69` claim "security boundary"-shaped intent for `req.Host = ""` (finding I6). `metrics/counters.go:10-13` documents outcome labels broader than the actual emit set (finding I7). `index.go` doc comment is sound. Pattern: doc-first development is correctly catching design intent, but tests don't tighten the contract to match the docs — every doc-stated label/enum/behavior should have a corresponding assertion.
- **Fail-closed posture is consistent except where it surfaces silently:** Refuse-to-start on missing JWT Secret + non-HTTPS `ACH_BASE_URL` is enforced. Refuse-to-update with prior-slot retention on Reload errors is enforced. But finding C2 (BIP list silently fails open) violates the posture, and finding W4 (Director writing empty x-litellm-key-id) is the same shape: when the resolver layer doesn't produce expected state, the downstream gets an ambiguous value rather than a typed error. Tighten the silent-fallback paths to log + emit metrics.

---

_Reviewed: 2026-05-26_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
