---
phase: 06-cli-foundation
plan: 07
subsystem: cli-foundation
tags: [cli, synthetic-mode, cross-cutting, refactor, cli-07, cli-08, cli-09]
requirements: [CLI-07, CLI-08, CLI-09]
dependency_graph:
  requires:
    - "06-01-cli-shared-internals (internal/cli/exit.CodedError + General code)"
    - "06-03-ach-login-whoami-logout (inline synthetic checks in login/logout/whoami)"
    - "06-04-ach-config-env (inline synthetic checks in config + env)"
    - "06-05-ach-env-keys-d07-deviation (inline D-08 + isSynthetic helper in env_keys)"
    - "06-06-ach-hydrate (assertSyntheticConstraints inline check in hydrate)"
  provides:
    - "internal/cli/synthetic package (Gate enum + Params bag + IsActive + IsHalfSet + GuardCommand + SyntheticDeploymentLabel)"
    - "SyntheticDeploymentLabel = \"(env)\" const (consumed by Phase 7 state.json writer)"
    - "Single source of truth for CLI-07 / spec §3.3 enforcement across all 7 W1/W2 cobra subcommands"
  affects:
    - "Phase 7 hydrate engine: state.json writer imports SyntheticDeploymentLabel for the deployment-name field when synthetic mode is active"
    - "06-08 ach admin: new admin subcommands will gate via synthetic.GateAdmin (already wired in the Gate enum)"
    - "06-09 e2e demo-collapse: smoke matrix in plan §verification provides the canonical synthetic-mode commandcheck for the demo path"
tech_stack:
  added: []  # Pure stdlib (os + fmt) + foundation internal/cli/exit.
  patterns:
    - "Pattern P12 — RunE returns typed errors; cmd/ach/main.go's errors.As branch maps to exit code at process entry point"
    - "Closed-enum Gate (typed int) — adding a new subcommand requires editing the synthetic package, preventing silent drift across cobra files"
    - "Params bag — flag-resolved values flow IN, env vars are read by GuardCommand via package-level Getenv seam (default os.Getenv; tests use t.Setenv)"
    - "Composite check with fixed order: half-set → bare-allow → deny-set → --deployment → --env-key → allow. Each rejection branch carries a deterministic message that name the offending flag / env var (not its value — T-06-07-04)"
    - "Documentation hygiene per CLAUDE.md: every cmd/*.go file's package doc that mentioned 'inline synthetic check (W3-P1 will centralize)' updated in the SAME commit to point at synthetic.GuardCommand"
key_files:
  created:
    - "internal/cli/synthetic/doc.go"
    - "internal/cli/synthetic/synthetic.go"
    - "internal/cli/synthetic/synthetic_test.go"
    - "cmd/ach/cmd/synthetic_guard_test.go"
  modified:
    - "cmd/ach/cmd/login.go"
    - "cmd/ach/cmd/logout.go"
    - "cmd/ach/cmd/config.go"
    - "cmd/ach/cmd/env_keys.go"
    - "cmd/ach/cmd/hydrate.go"
    - "cmd/ach/cmd/env.go"
    - "cmd/ach/cmd/whoami.go"
decisions:
  - "Params bag is the IMMUTABLE field set Phase 7 + 06-08 consumers can rely on. Adding a new field is a non-breaking change (zero-value default); renaming or removing requires bumping the synthetic package version. The four fields are: Gate (REQUIRED), APIKeyFlag, EnvKeyFlag, DeploymentFlag, NoSaveFlag. Only GateEnvKeysCreate reads NoSaveFlag — other gates ignore it. The closed list is the trust anchor (T-06-07-06)."
  - "Gate constants explicitly enumerate every cobra subcommand in the deny + allow set. The deny set is {GateLogin, GateLogout, GateConfig, GateEnvKeysCreate}. The allow set is {GateHydrate, GateWhoami, GateEnvList, GateEnvDescribe, GateEnvKeysList, GateEnvKeysRevoke, GateAdmin}. GateAdmin is added pre-emptively for 06-08; no code consumes it in this phase yet, but the enum value lets 06-08 wire its RunE with a single call."
  - "The readOnlyGatesRejectingEnvKey table is identical to the allow-set in this phase — every allowed-in-synthetic gate also rejects --env-key. The two tables are kept SEPARATE in the source (rather than collapsed to a single map) so a future gate that should accept --env-key under synthetic can be added to allowedInSyntheticGates without also adding to readOnlyGatesRejectingEnvKey."
  - "Half-set (IsHalfSet=true) is the FIRST branch of GuardCommand — it fires regardless of gate. This is the T-06-07-01 mitigation: a user who sets ACH_BASE_URL without ACH_API_KEY never falls back to bare-mode disk config silently. The half-set message names both fix paths (set ACH_API_KEY / pass --api-key OR unset ACH_BASE_URL) so a CI operator can self-recover in one shell command."
  - "The synthetic-mode bearer synthesis in whoami.go's resolveActiveBearer is retained (returns a one-off Deployment with URL=ACH_BASE_URL and label=SyntheticDeploymentLabel). The synthetic.GuardCommand call in doWhoami already rejected --deployment / --env-key / half-set before resolveActiveBearer fires, so the synthesis path is reached ONLY when synthetic mode is fully active + clean. The label is sourced from synthetic.SyntheticDeploymentLabel (not the literal '(env)') so Phase 7 + 06-08 agree on the constant by import, not by string-matching."
  - "env_keys.go's resolveEnvKeysBearer keeps its own synthetic-mode bearer-synthesis branch (it pre-dated 06-07 by one wave). The synthetic-mode --deployment rejection inside that branch was REMOVED (GuardCommand now owns it); the bearer synthesis itself stays. This avoids re-architecting the whole bearer-resolution chain for what is essentially a single-line redundancy fix."
  - "hydrate.go's assertSyntheticConstraints function was REMOVED outright (logic absorbed into GuardCommand). hydrate.go's assertMutexCreds (the D-11 closed-list cred mutex) was RETAINED — it's a separate concern from synthetic mode. The two checks run in this order in runHydrate: mutex first, then synthetic gate, then resolveBearer, then keys.ClassifyBearer + --environment gate."
  - "configSyntheticGuard helper kept its signature (`func(sub string) error`) but the `sub` arg is now unused. The local helper exists ONLY so the 5 config children's call sites stay readable as `configSyntheticGuard(\"list\")` etc. — collapsing to inline calls would dirty 5 RunE bodies for a one-line save. The helper now just calls synthetic.GuardCommand with GateConfig."
  - "Pre-existing per-subcommand synthetic-mode test assertions (login/logout/config/env-keys-create) continued to pass without modification — every test checks the err message via strings.Contains(\"synthetic\") OR strings.Contains(\"--no-save\"), which my centralized messages still satisfy. Zero existing test strings needed updating."
  - "Added cmd/ach/cmd/synthetic_guard_test.go with 3 cross-cutting tests (--deployment / --env-key / half-set). The --deployment test is scoped to the allow-set because the deny-set commands (login/logout) reject under the deny-set arm BEFORE the --deployment arm fires — testing the deny commands for --deployment-specific rejection would be a false test (they reject regardless of --deployment)."
metrics:
  duration_minutes: 15
  completed_date: 2026-05-28
  tasks: 2
  files_created: 4
  files_modified: 7
---

# Phase 6 Plan 07: Synthetic Mode Enforcement Summary

CLI-07 cross-cutting refactor — promotes the 7 inline synthetic-mode short-circuits scattered across W1/W2 subcommands into a single source of truth at `internal/cli/synthetic/`. Every W1/W2 cobra RunE now invokes `synthetic.GuardCommand(Params{Gate: ...})` BEFORE its credential resolution; the package owns the half-set / deny-set / --deployment / --env-key disposition matrix. New constant `SyntheticDeploymentLabel = "(env)"` is what Phase 7's state.json writer will record as the deployment name under synthetic mode (CLI-07 last clause).

## What landed

### internal/cli/synthetic — single source of truth (Task 1, commit `c39d313`)

3 files (`doc.go`, `synthetic.go`, `synthetic_test.go`); 14 tests; SPDX header on every file.

**Public API:**

```go
const SyntheticDeploymentLabel = "(env)"

type Gate int  // closed enum
const (
    GateLogin Gate = iota + 1
    GateLogout
    GateConfig
    GateEnvKeysCreate
    GateHydrate
    GateWhoami
    GateEnvList
    GateEnvDescribe
    GateEnvKeysList
    GateEnvKeysRevoke
    GateAdmin  // 06-08 pre-wire; no consumer yet in Phase 6
)

type Params struct {
    Gate           Gate    // REQUIRED
    APIKeyFlag     string
    EnvKeyFlag     string
    DeploymentFlag string
    NoSaveFlag     bool
}

var Getenv = os.Getenv  // test seam (rarely used; t.Setenv preferred)

func IsActive(Params) bool       // ACH_BASE_URL set AND credential resolves
func IsHalfSet(Params) bool      // ACH_BASE_URL set BUT no credential
func GuardCommand(Params) error  // composite check
```

**GuardCommand check order:**

1. Half-set → reject with the half-set message regardless of gate (T-06-07-01).
2. Not synthetic-active → allow (bare-mode invocation).
3. Synthetic + deny-set (login/logout/config) → reject.
4. Synthetic + GateEnvKeysCreate without --no-save → reject (D-08).
5. Synthetic + --deployment / ACH_DEPLOYMENT → reject regardless of gate.
6. Synthetic + read-only gate + --env-key / ACH_ENV_KEY → reject (CLI-09).
7. Otherwise → allow.

### cmd/ach/cmd refactor — 7 files swap inline checks for synthetic.GuardCommand (Task 2, commit `3bf7c5d`)

| File          | Removed (inline)                                                          | Added (centralized)                                         |
| ------------- | ------------------------------------------------------------------------- | ----------------------------------------------------------- |
| login.go      | `if os.Getenv("ACH_BASE_URL") && os.Getenv("ACH_API_KEY")` block          | `synthetic.GuardCommand(Params{Gate: GateLogin, ...})`      |
| logout.go     | same                                                                      | `synthetic.GuardCommand(Params{Gate: GateLogout, ...})`     |
| config.go     | `configSyntheticGuard` body + `syntheticActive()` helper                  | `configSyntheticGuard` now delegates to `synthetic.GuardCommand(Params{Gate: GateConfig})` |
| env_keys.go   | `isSynthetic()` helper + redundant synthetic-mode --deployment reject in `resolveEnvKeysBearer` | `synthetic.GuardCommand` for each of create/list/revoke RunEs |
| hydrate.go    | `assertSyntheticConstraints(in)` function (logic absorbed)                | `synthetic.GuardCommand(Params{Gate: GateHydrate, ...})` — mutex check `assertMutexCreds` retained |
| env.go        | (no inline check pre-existed)                                             | `synthetic.GuardCommand` for env list + env describe        |
| whoami.go     | synthetic-mode --deployment reject inside `resolveActiveBearer` removed   | `synthetic.GuardCommand(Params{Gate: GateWhoami, ...})` in `doWhoami`; bearer-synthesis path now uses `synthetic.SyntheticDeploymentLabel` const |

### Cross-cutting test gap-fillers (cmd/ach/cmd/synthetic_guard_test.go — Task 2 commit)

| Test                                              | Coverage                                                  |
| ------------------------------------------------- | --------------------------------------------------------- |
| `TestSyntheticGuard_DeploymentFlagRejected`       | Synthetic + --deployment on every allow-set subcommand exits 1 with `--deployment` in msg |
| `TestSyntheticGuard_EnvKeyFlagRejected`           | Synthetic + --env-key on whoami / hydrate / env list / env describe / env-keys list / env-keys revoke exits 1 with `--env-key` in msg |
| `TestSyntheticGuard_HalfSetRejected`              | Half-set (ACH_BASE_URL set, no credential) on every subcommand exits 1 with `half-set` in msg |

Pre-existing per-subcommand synthetic tests (`TestLogin_SyntheticModeRejected`, `TestLogout_SyntheticMode_Exit1`, `TestConfig_SyntheticMode_Exit1`, `TestEnvKeys_Create_SyntheticWithoutNoSave_Exit1`, `TestHydrate_SyntheticMode_*`, `TestEnv_SyntheticMode_Allowed`) all PASS WITHOUT MODIFICATION — they assert via `strings.Contains(err.Error(), "synthetic")` (or "--no-save") which my centralized messages still carry.

## Gate × subcommand × cred-source disposition matrix

Definitive mapping (also the SUMMARY's "verifier table" per plan §output spec):

| Subcommand          | Gate                  | Synthetic + ok creds | Half-set | Synth + --deployment | Synth + --env-key |
| ------------------- | --------------------- | -------------------- | -------- | -------------------- | ----------------- |
| ach login           | `GateLogin`           | exit 1 (deny)        | exit 1   | exit 1 (deny-first)  | exit 1 (deny-first) |
| ach logout          | `GateLogout`          | exit 1 (deny)        | exit 1   | exit 1 (deny-first)  | exit 1 (deny-first) |
| ach config *        | `GateConfig`          | exit 1 (deny)        | exit 1   | exit 1 (deny-first)  | exit 1 (deny-first) |
| ach env-keys create | `GateEnvKeysCreate`   | exit 1 unless --no-save (D-08); 0 with --no-save | exit 1 | exit 1 | exit 0 / exit 1 if no --no-save (deny-first via D-08) |
| ach env-keys list   | `GateEnvKeysList`     | exit 0               | exit 1   | exit 1               | exit 1            |
| ach env-keys revoke | `GateEnvKeysRevoke`   | exit 0               | exit 1   | exit 1               | exit 1            |
| ach hydrate         | `GateHydrate`         | exit 0               | exit 1   | exit 1               | exit 1            |
| ach whoami          | `GateWhoami`          | exit 0               | exit 1   | exit 1               | exit 1            |
| ach env list        | `GateEnvList`         | exit 0               | exit 1   | exit 1               | exit 1            |
| ach env describe    | `GateEnvDescribe`     | exit 0               | exit 1   | exit 1               | exit 1            |
| ach admin (06-08)   | `GateAdmin`           | exit 0 (pre-wired)   | exit 1   | exit 1               | exit 1            |

"deny-first" means the deny-set arm fires before the --deployment / --env-key check — same exit code, different error message ("login not available in synthetic" vs "--deployment cannot be used in synthetic"). Either way the user exits with code 1 and a clear reason.

## Per-subcommand error-message strings (for the verifier's grep matrix)

```
half-set                    → "synthetic mode is half-set: ACH_BASE_URL is set but no credential resolved (set ACH_API_KEY or pass --api-key; or `unset ACH_BASE_URL` to use the disk config)"
deny-set (login/logout/cfg) → "ach <verb> is not available in synthetic mode (ACH_BASE_URL + credential set; see CLI spec §3.3)"
env-keys create + no --no-save → "ach env-keys create requires --no-save in synthetic mode (ACH_BASE_URL + credential set; no writable config file — D-08)"
--deployment / ACH_DEPLOYMENT  → "--deployment / ACH_DEPLOYMENT cannot be used in synthetic mode (deployment is fixed to \"(env)\"; see CLI spec §3.3)"
--env-key / ACH_ENV_KEY        → "--env-key / ACH_ENV_KEY cannot be used in synthetic mode (ek_ labels require the config registry; CLI-09 / spec §3.3)"
```

All five strings are deterministic and stable — Phase 7 + 06-08 + e2e demo-collapse can grep for the substrings 'half-set', 'is not available in synthetic', '--no-save in synthetic', '--deployment / ACH_DEPLOYMENT', '--env-key / ACH_ENV_KEY' for behavior assertions.

## Deviations from Plan

None. Plan executed exactly as written with the following clarifications recorded in `decisions[]`:

- Plan §Task-2 `<acceptance_criteria>` example "synthetic mode rejects --deployment under any command exits 1 with deployment-conflict message" — for the deny-set commands (login/logout/config) this is satisfied transitively (they reject under the deny arm, which fires FIRST; the same exit code 1 is returned). The new `TestSyntheticGuard_DeploymentFlagRejected` test scopes the message-substring assertion to the allow-set commands only, which is the only honest assertion shape. Documented in `decisions[]`.

## Auth Gates

None encountered. Plan is pure refactor + new package; no HTTP / OAuth flows touched.

## Threat Surface Scan

No new security-relevant surface introduced. The refactor reduces drift surface (one helper instead of seven inline copies) — every threat in the plan's `<threat_model>` (T-06-07-01..07) is honored as documented:

| Threat | Mitigation status | Notes |
|--------|------------------|-------|
| T-06-07-01 (half-set bypass) | mitigated | IsHalfSet is the FIRST check in GuardCommand; fires regardless of gate. |
| T-06-07-02 (--env-key smuggled via env injection) | mitigated | readOnlyGatesRejectingEnvKey table; ek_ requires config registry, which synthetic has no access to. |
| T-06-07-03 (refactor regression silently re-allows) | mitigated | All pre-existing synthetic-mode tests pass without modification; lint clean; 14 new tests cover the matrix. |
| T-06-07-04 (error msg leaks plaintext) | mitigated | Messages reference SOURCE (`--api-key`, `ACH_API_KEY`, etc.) NOT VALUE. `grep 'pk_\|ek_' internal/cli/synthetic/synthetic.go` = 0 (verified). |
| T-06-07-05 (synthetic bypasses login) | accepted | Documented in package doc.go — synthetic mode is the spec-§3.3-acknowledged CI/container escape hatch. |
| T-06-07-06 (Getenv seam left as os.Getenv in production) | mitigated | Var defaults to os.Getenv; tests prefer `t.Setenv` (which mutates the real env transparently). The seam is exported but documented as test-only. |
| T-06-07-07 (mass-rejection on misconfig DoS) | mitigated | Every rejection message names the conflicting var; user recovers via `unset ACH_BASE_URL`. |
| T-06-07-SC (third-party deps) | mitigated | Zero new deps; pure stdlib + internal/cli/exit. |

No threat-flagged surface beyond the plan's register.

## Verification (full plan §verification gates)

```
$ ./scripts/dev.sh go test ./internal/cli/... ./cmd/ach/cmd/...
ok    github.com/ackstorm/ach/cmd/ach/cmd            0.174s
ok    github.com/ackstorm/ach/internal/cli/config    0.061s
ok    github.com/ackstorm/ach/internal/cli/devicecode 0.233s
ok    github.com/ackstorm/ach/internal/cli/exit      0.078s
ok    github.com/ackstorm/ach/internal/cli/httpclient 0.087s
ok    github.com/ackstorm/ach/internal/cli/render    0.055s
ok    github.com/ackstorm/ach/internal/cli/synthetic 0.004s

$ ./scripts/dev.sh go build ./cmd/ach/...
(clean)

$ ./scripts/dev.sh make lint-changed
(clean; exit 0)
```

Source-assertion gates from plan §acceptance_criteria all PASS:

- Task 1: `grep -c 'SyntheticDeploymentLabel\s*=\s*"(env)"' internal/cli/synthetic/synthetic.go` = 1 ✓
- Task 1: `grep -cE 'GateLogin|GateLogout|GateConfig|GateEnvKeysCreate|GateHydrate' internal/cli/synthetic/synthetic.go` = 22 (≥ 5) ✓
- Task 1: `grep -c 'IsActive\|IsHalfSet\|GuardCommand' internal/cli/synthetic/synthetic.go` = 13 (≥ 3) ✓
- Task 1: SPDX header on all 3 new files ✓
- Task 2: 7 files contain `synthetic.GuardCommand` ✓
- Task 2: 0 files contain inline `ACH_BASE_URL` + `ACH_API_KEY` pair check ✓
- Task 2: 7 files import `internal/cli/synthetic` ✓
- Task 2: pre-commit hook (lint-changed + unit) PASSED on the Task-2 commit ✓

## Self-Check: PASSED

Verified:
- `internal/cli/synthetic/doc.go` exists.
- `internal/cli/synthetic/synthetic.go` exists.
- `internal/cli/synthetic/synthetic_test.go` exists.
- `cmd/ach/cmd/synthetic_guard_test.go` exists.
- `cmd/ach/cmd/login.go`, `logout.go`, `config.go`, `env_keys.go`, `hydrate.go`, `env.go`, `whoami.go` all carry `synthetic.GuardCommand` call (verified via grep).
- Commits `c39d313` and `3bf7c5d` present in `git log`.
- `./scripts/dev.sh go test ./internal/cli/... ./cmd/ach/cmd/...` exits 0 (all packages PASS).
- `./scripts/dev.sh go build ./cmd/ach/...` exits 0.
- `./scripts/dev.sh make lint-changed` exits 0.
- Pre-commit hook gate (lint-changed + unit) passed on the Task 2 commit (visible in commit output: "All pre-commit gates passed.").

---
*Phase: 06-cli-foundation*
*Plan: 07*
*Completed: 2026-05-28*
