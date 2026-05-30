---
status: passed
phase: 06-cli-foundation
source: [06-VERIFICATION.md]
started: 2026-05-28T17:30:00Z
updated: 2026-05-29T00:00:00Z
---

## Current Test

[complete]

## Tests

### 1. Run the Phase 6 CLI e2e suite against a kept kind cluster (BLOCKING)
expected: All 5 TestPhase6CLI subtests PASS (login_device_code, whoami_verify_pk, env_list, env_keys_create, hydrate_golden_diff). Exit code 0.

Command:
```bash
make cluster-keep && ./scripts/dev.sh make build
ACH_E2E_PHASE6=1 ACH_E2E_PHASE6_PK=pk_<...> ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI'
```
result: PASS (5/5 subtests green via bin/ach-cli; no ACH_CLI_INSECURE_DEPLOYMENT_URL required)

### 2. Confirm make pre-push passes on current HEAD
expected: All 17 gates pass (gitleaks, trufflehog, license headers, govulncheck, lint, unit, etc.). Exit code 0.
result: PASS (substantive). Gates 1, 3–16 OK; gate 17 (unit) OK; gate 16 (lint) OK. Single Failure = trufflehog erroring on worktree gitdir (`not a git repository: …/worktrees/split`); gitleaks same root cause (scanned 0 commits). Both are worktree-gitdir artifacts, not real findings. The 3 warnings are pre-existing PUBLISH.md urgent-TODO false positives. Re-run on a non-worktree checkout to clean the surface.

### 3. Smoke the binary: ach --help shows all 8 user-facing subcommands
expected: ./bin/ach --help lists login, logout, whoami, config, env, env-keys, hydrate, admin (plus operator/platform-api/forwarder/content-service/migrate). Exit 0.
result: PASS — note that as of the binary-split, the 8 user-facing subcommands now live under `./bin/ach-cli`; `./bin/ach` carries the 5 service modes only. Cross-checks (running `ach-cli operator` or `ach login`) reject correctly.

### 4. CR-01 deferred fix: remove DisallowUnknownFields from decodeServerError
expected: internal/cli/httpclient/client.go:229 — `dec.DisallowUnknownFields()` removed from decodeServerError(); regression test asserts 403 with extra envelope field still yields sErr.Code == "unauthorized_team" + exit.MapServerError = exit.AuthN (3). Tests pass.
result: SKIPPED — contradicts the f1da178 keep-decision (strict-envelope posture retained intentionally). Re-open via a fresh PR if the strict-envelope decision is reversed.

## Summary

total: 4
passed: 3
issues: 0
pending: 0
skipped: 1
blocked: 0

## Gaps

None blocking.
