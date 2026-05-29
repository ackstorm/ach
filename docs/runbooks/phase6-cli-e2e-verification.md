# Phase 6 CLI E2E — Verification Runbook

**Audience:** engineer running the kind+Helm cluster on a separate machine.
**Outcome:** confirm Phase 6 CLI ships green so phase verification flips from
`human_needed` → `passed`.

This file is a self-contained checklist. Each section is a discrete check;
run them in order, capture pass/fail + relevant output, and reply with the
results so the GSD orchestrator can close out the verification cycle.

---

## 0. Prerequisites

- Repo: `github.com:ackstorm/ach.git`, HEAD at `e86e9a5` or later
  (`docs(plans): add git-fetcher follow-up + protocol-swap drafts`).
- Docker + git on host. NO host-level Go required — devtools container is
  used for all Go toolchain invocations via `./scripts/dev.sh`.
- One free TCP port for the kind cluster's NodePort / Ingress
  (`make cluster-keep` handles the host wiring).
- ~10 min free CPU budget for the cluster + binary + suite.

```bash
git pull --ff-only origin main
git rev-parse HEAD          # expect e86e9a5 or later
docker info >/dev/null      # expect no error
./scripts/dev.sh --version  # expect devtools container builds/pulls
```

---

## CHECK 1 — surface separation: `ach-cli` (user CLI) vs `ach` (services)

**Why human:** both binaries compile inside CI, but a runtime smoke confirms
the cobra trees are fully wired AND that the user CLI / service-mode split
landed (the 8 user subcommands live on `ach-cli`; the 5 service modes on
`ach`). `make build` writes both `bin/ach` and `bin/ach-cli`.

```bash
./scripts/dev.sh make build
./bin/ach-cli --help    # user CLI
./bin/ach --help        # services
echo "EXIT=$?"
```

**Expected:** `./bin/ach-cli --help` exits `0` and lists every one of:

```
  login         Authenticate against the platform-api
  logout        Revoke local session
  whoami        Show current identity
  config        Inspect / mutate local config
  env           List + switch environments
  env-keys      Create / list / revoke environment keys
  hydrate       Materialize workspace artifacts
  admin         Admin subcommands (keys revoke, users revoke-keys, refresh)
```

AND `./bin/ach --help` lists only the 5 service modes:

```
  operator, platform-api, forwarder, content-service, migrate
```

Cross-check the split: `./bin/ach login` and `./bin/ach-cli operator` must
each fail with "unknown command".

**If any user-facing subcommand is missing from `ach-cli`, or a service mode
is missing from `ach`, or the cross-check passes when it should fail:**
capture the full `--help` output and tag this check as FAIL.

---

## CHECK 2 — `make pre-push` 17-gate sweep passes on current HEAD

**Why human:** the pre-push gate is host-only (gitleaks + trufflehog spawn
container peers on host docker). Cannot run inside the GSD orchestrator
context. Already gated successfully on push of `e86e9a5` but a clean
re-run on the target machine confirms reproducibility.

```bash
make pre-push 2>&1 | tee /tmp/phase6-pre-push.log
echo "EXIT=$?"
```

**Expected:** exit `0` and tail of log shows `Failures: 0`. Warnings are OK
(PUBLISH.md urgent-TODO false positives are pre-existing).

**If FAIL:** capture `/tmp/phase6-pre-push.log` (or just the last 60 lines)
and tag this check as FAIL.

---

## CHECK 3 — Live Phase 6 CLI e2e suite (BLOCKING)

**Why human:** the e2e suite requires a real kind cluster + a live `pk_`
minted via Phase 3 SSO. Cannot run inside the GSD orchestrator context.
This is the gating check — phase 6 cannot be marked `passed` without it.

### 3a. Bring up the kind cluster (kept)

```bash
make cluster-keep
```

Synchronous. Wait until the command returns exit `0`. This stands up
postgres + redis + dex + ach-operator + ach-platform-api + ach-forwarder
+ content-service sidecar, plus seeds the demo CRs.

```bash
./scripts/dev.sh kubectl -n ach-system get pods   # expect all Running/Ready
```

### 3b. Mint a real `pk_` via Phase 3 SSO

The Phase 6 e2e uses `ACH_E2E_PHASE6_PK` as the bootstrap bearer (D-18
Option A: synthetic config injection under temp `XDG_CONFIG_HOME`).
Easiest path is to copy a known-good `pk_` from a prior Phase 3 UAT run
(or run `examples/hydrate-demo.sh` to mint one and grep its output):

```bash
# Option A — copy from a previous UAT run
PK=$(grep -E '^pk_[a-z2-7]{26}$' .planning/phases/03-*/03-VERIFICATION.md | head -1)
# Option B — mint fresh (uses the kept cluster)
# (Plan 06-09 explicitly retired examples/hydrate-demo.sh, so this Option B is
#  no longer available on main HEAD — prefer Option A. If Option A returns
#  empty, run `./scripts/dev.sh kubectl -n ach-system port-forward svc/ach-platform-api 8443:443`
#  and complete the device-code flow against http://127.0.0.1:8443/auth/cli/init.)
echo "PK=$PK"
[ -n "$PK" ] || { echo "FAIL: no pk_ available — see Option B above"; exit 1; }
```

### 3c. Run the suite

```bash
ACH_E2E_PHASE6=1 ACH_E2E_PHASE6_PK="$PK" \
  ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI' 2>&1 \
  | tee /tmp/phase6-e2e.log
echo "EXIT=$?"
```

**Expected:** exit `0` and tail of log shows all 5 subtests PASS:

```
--- PASS: TestPhase6CLI (Ns)
    --- PASS: TestPhase6CLI/login_device_code
    --- PASS: TestPhase6CLI/whoami_verify_pk
    --- PASS: TestPhase6CLI/env_list
    --- PASS: TestPhase6CLI/env_keys_create
    --- PASS: TestPhase6CLI/hydrate_golden_diff
```

**If any subtest FAILs:** capture the full subtest failure block from
`/tmp/phase6-e2e.log` (the `--- FAIL: TestPhase6CLI/<name>` line + the next
~30 lines of `t.Logf` output) and tag this check as FAIL.

**Common failure modes (see CLAUDE.md "Common failure modes" section):**

- `hydrate_golden_diff` FAIL with `downloadUrl` host mismatch → cluster
  exposes platform-api on a host other than `ach.local.test`. The
  `phase6NormalizeHydrate` helper should auto-rewrite, but if the
  cluster host is exotic (port, non-DNS literal, etc.) the helper may
  miss it. Surface the actual host in your FAIL report.
- `whoami_verify_pk` FAIL with `401 invalid_token` → the supplied `PK`
  was revoked or never minted. Re-mint and retry.
- All subtests FAIL with `connection refused` → the kept cluster
  Service was torn down. Re-run `make cluster-keep` and retry.

### 3d. Tear down (optional)

```bash
make cluster-down   # only if you don't need the cluster for follow-up work
```

---

## CHECK 4 — CR-01 deferred fix (advisory, NOT blocking phase 6)

**Why human:** correctness risk for future server-side envelope extensions.
Documented in `06-REVIEW.md` as critical. Not blocking — current tests pass
because the test server emits exactly the expected fields. Recommend
removing before Phase 7 ships any new `*ServerError` envelope field.

**Action (defer to Phase 7 prep):**

1. Edit `internal/cli/httpclient/client.go:229` — remove the
   `dec.DisallowUnknownFields()` line inside `decodeServerError`.
2. Add a regression test in `internal/cli/httpclient/client_test.go`
   that constructs a 403 response with an extra envelope field
   (e.g., `{"error":{"code":"unauthorized_team","message":"...","retry_after":30}}`)
   and asserts `sErr.Code == "unauthorized_team"` and
   `exit.MapServerError(sErr) == exit.AuthN` (3).
3. Run `./scripts/dev.sh make unit` — expect exit 0.
4. Commit as `fix(httpclient): tolerate additive ServerError envelope fields (CR-01)`.

**This check does NOT need to run before reporting back** — it's advisory
context for the next phase planning, not a phase 6 gate.

---

## Reporting back

Reply with one of:

- **`approved`** if Checks 1-3 all PASS (Check 4 is optional/deferred).
  The orchestrator will then mark phase 6 `passed` and advance.

- **List of failures** if any of Checks 1-3 FAIL, in this format:

  ```
  CHECK 1: PASS|FAIL <one-line note>
  CHECK 2: PASS|FAIL <one-line note + log path if FAIL>
  CHECK 3a: PASS|FAIL <pods status if FAIL>
  CHECK 3b: PASS|FAIL <how PK was obtained>
  CHECK 3c: PASS|FAIL <subtest failures + first failing assertion>
  ```

  Then the orchestrator will route to `/gsd-plan-phase 06 --gaps` to
  generate fix plans.
