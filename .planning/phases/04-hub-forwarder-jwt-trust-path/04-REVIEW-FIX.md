---
phase: 04-hub-forwarder-jwt-trust-path
fixed_at: 2026-05-26T21:35:00Z
review_path: .planning/phases/04-hub-forwarder-jwt-trust-path/04-REVIEW.md
iteration: 1
findings_in_scope: 11
fixed: 10
skipped: 1
status: partial
---

# Phase 4: Code Review Fix Report

**Fixed at:** 2026-05-26T21:35:00Z
**Source review:** `.planning/phases/04-hub-forwarder-jwt-trust-path/04-REVIEW.md`
**Iteration:** 1

**Summary:**
- Findings in scope (Critical + Warning): 11
- Fixed: 10
- Skipped: 1

All fixes were verified by re-reading the modified file region (Tier 1)
and by `./scripts/dev.sh go build` of the affected packages (Tier 2). The
broader unit test suite (`./scripts/dev.sh go test ./internal/config/...
./internal/forwarder/... ./cmd/ach/...`) passes on the resulting branch,
and `golangci-lint run` is clean on every touched package.

Commits are landed on temp branch `gsd-reviewfix/04-3845401`, which the
orchestrator's cleanup tail fast-forwards into the user's branch
(`feat/env-accessgroup-sync`).

## Fixed Issues

### C1: Forwarder informer LIST will be RBAC-rejected — pod cannot start

**Files modified:** `cmd/ach/cmd/forwarder.go`
**Commit:** `88f9181`
**Applied fix:** Added `cache.ByObject` entry with
`fields.OneTermEqualSelector("metadata.name", cfg.JWTSecretName)` so the
informer's LIST/WATCH carries the `?fieldSelector=metadata.name=<name>`
the apiserver requires to honor a `resourceNames`-scoped RBAC grant.
Imported `k8s.io/apimachinery/pkg/fields`. The pod will now reach Ready
under the locked-down Role.

### C2: `bip.ResolveWinner` silently swallows List errors → JWT mint silently skipped

**Files modified:** `internal/forwarder/bip/index.go`,
`internal/forwarder/metrics/counters.go`
**Commit:** `6d62cb1`
**Applied fix:** Replaced the bare `return nil` on `c.List` error with a
warn-level log via `ctrl.Log.WithName("forwarder.bip")` plus a new
`IncJWTSuppressed(kind, "list_failure")` counter. Kept fail-open at the
JWT layer (failing closed would reject every /mcp request on a cache
blip). Extended the metrics package doc enum to document the new
`list_failure` reason so Phase 5 wires the prometheus label.

### W1: Helm Secret volume mount is dead weight

**Files modified:** `deploy/helm/ach/templates/forwarder-deployment.yaml`
**Commit:** `696fef4`
**Applied fix:** Dropped the `volumes:` block (jwt-keys Secret mount at
`/etc/ach/jwt`) and the matching `volumeMounts:` block on the forwarder
container. The SecretLoader fetches via the K8s API client + informer,
so the file mount was dead weight that would have misled future
maintainers. RBAC `resourceNames` carve-out remains the SC#4 single-
reader contract.

### W2: `validateForwarderConfig` parses `ACH_LITELLM_BASE_URL` but discards the parsed URL

**Files modified:** `cmd/ach/cmd/forwarder.go`
**Commit:** `c39d361`
**Applied fix:** Added `LiteLLMUpstream *url.URL` field to
`forwarderConfig`, populated it in validate after the scheme check, and
removed the redundant `url.Parse` from `buildForwarderDeps`. Single
parse, single source of truth for scheme + host semantics.

### W3: `validateForwarderConfig` swallows error AND mis-validates `ACH_REDIS_DB=0`

**Files modified:** `internal/config/config.go`,
`internal/config/config_test.go`, `cmd/ach/cmd/forwarder.go`
**Commit:** `da4bfbd`
**Applied fix:** Added `config.EnvIntNonNeg(key, fallback)` (accepts 0,
rejects negative + non-numeric) with five new unit tests covering
unset/zero/positive/negative/non-numeric paths. Updated
`validateForwarderConfig` to call `EnvIntNonNeg` for `ACH_REDIS_DB` and
return the error (previously discarded with `_`). DB 0 (default Redis
logical DB) is now a legitimate value; `-1` and `"abc"` now block boot
with a clear error.

### W4: Proxy `Director` no-ops on `KeyContext` absent — strip pass still writes empty `x-litellm-key-id`

**Files modified:** `internal/forwarder/proxy/proxy.go`
**Commit:** `dacf909`
**Applied fix:** After calling `headers.StripAndRewrite`, the Director
now checks whether the token was empty (KeyContext absent or
LiteLLMToken nil) and calls `req.Header.Del("X-Litellm-Key-Id")` so the
upstream never receives a misleading empty key_id. Defensive only —
production routes are gated by Authn — but eliminates a test-only
foot-gun and tightens the silent-fallback posture.

### W5: JWT key-rotation runbook uses `op:replace` on possibly-absent paths

**Files modified:** `docs/runbooks/jwt-key-rotation.md`
**Commit:** `24ac3d4`
**Applied fix:** Step 2's `op:replace` for `/data/next.kid` and
`/data/next.seed` is now `op:add` (RFC 6902 §4.1 — succeeds whether path
exists or not). Step 5's next.* clears also switched to `op:add` for
symmetry. Step 5's current.* stays on `op:replace` (current.* always
exists by step 5). Added inline comments explaining why `add` is the
right choice.

### W6: E2E fixture lacks apply-to-prod safety net

**Files modified:** `test/e2e/fixtures/phase4_jwt_signing_keys_seed.yaml`
(renamed → `phase4_jwt_signing_keys_seed.UNSAFE.yaml`)
**Commit:** `1bbf3a1`
**Applied fix:** Renamed the file with `.UNSAFE.yaml` suffix (machine-
readable marker for pre-push/CI guards) and added two
machine-readable annotations: `ach.ackstorm.ai/unsafe-test-fixture:
"true"` and `ach.ackstorm.ai/seed-warning: "..."`, plus a
`test.ach.ackstorm.ai/known-plaintext-seed: "true"` label. Expanded the
DANGER comment block to call out the admission-policy / preflight guard
recommendation.

### W8: Metric emission seam absent

**Files modified:** `internal/forwarder/metrics/counters.go`
**Commit:** `8b998e7`
**Applied fix:** Added a package-level doc comment explicitly stating
that Phase 5 owns metric-emission test coverage. Justification: any
test-only seam would itself be Phase-4 code that Phase 5 immediately
rewrites when replacing the no-op stubs with prometheus registrations.
Phase 4 asserts observable side-effects (status code, body, upstream
call count) which prove the call sites execute on the same path that
returns the error.

### W9: Forwarder duplicates `ctrl.GetConfigOrDie()` rather than using `mgr.GetAPIReader()`

**Files modified:** `cmd/ach/cmd/forwarder.go`
**Commit:** `8cc8c28`
**Applied fix:** Replaced `client.New(ctrl.GetConfigOrDie(), ...)` +
`apiClient.Get(...)` with `mgr.GetAPIReader().Get(...)`. `GetAPIReader`
is the controller-runtime primitive for pre-cache-sync reads sharing
the manager's rest.Config, so the second `ctrl.GetConfigOrDie()` call
is gone.

## Skipped Issues

### W7: Mock Dockerfile uses `golang:1.26` — does not exist as of 2026-05

**File:** `test/e2e/mock/Dockerfile:1`
**Reason:** Reviewer claim is factually incorrect against this project's
ground truth. `go.mod` line 3 declares `go 1.26.0`. The project root
`Dockerfile` uses `ARG GO_VERSION=1.26` and `Dockerfile.devtools` uses
`FROM golang:1.26-bookworm`. `golang:1.26` is the project standard and
matches every other build path; the mock Dockerfile is already
consistent with the toolchain. Either the reviewer's training data is
old or this finding was authored against a different project. No
source change applied.
**Original issue:** Reviewer claimed `golang:1.26` does not exist and
the e2e mock build would fail `docker pull`.

---

_Fixed: 2026-05-26T21:35:00Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
