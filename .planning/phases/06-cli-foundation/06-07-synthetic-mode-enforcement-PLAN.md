---
phase: 06-cli-foundation
plan: 07
type: execute
wave: 3
depends_on:
  - 06-01-cli-shared-internals
  - 06-03-ach-login-whoami-logout
  - 06-04-ach-config-env
  - 06-05-ach-env-keys-d07-deviation
  - 06-06-ach-hydrate
files_modified:
  - internal/cli/synthetic/synthetic.go
  - internal/cli/synthetic/synthetic_test.go
  - internal/cli/synthetic/doc.go
  - cmd/ach/cmd/login.go
  - cmd/ach/cmd/logout.go
  - cmd/ach/cmd/config.go
  - cmd/ach/cmd/env_keys.go
  - cmd/ach/cmd/hydrate.go
  - cmd/ach/cmd/env.go
  - cmd/ach/cmd/whoami.go
autonomous: true
requirements:
  - CLI-07
  - CLI-08
  - CLI-09

must_haves:
  truths:
    - "internal/cli/synthetic.IsActive(env getenv) returns true iff ACH_BASE_URL is set AND a credential resolves from ACH_API_KEY OR --api-key (D-11)"
    - "synthetic.MustReject(cmd, flagsSet) returns exit.CodedError(General) for: ach login, ach logout, ach config *, ach env-keys create (without --no-save) per spec §3.3 + D-07"
    - "synthetic.IsHalfSet returns true iff ACH_BASE_URL set BUT no credential resolves — half-set is rejected with exit 1 (CLI-07)"
    - "Synthetic mode rejects --deployment AND ACH_DEPLOYMENT with exit 1 on ANY subcommand (CLI-07)"
    - "Synthetic mode rejects --env-key AND ACH_ENV_KEY on hydrate + whoami (CLI-09 — ek_ requires config registry)"
    - "State files (introduced in Phase 7) MUST record `\"deployment\": \"(env)\"` in synthetic mode — Phase 6 wires the constant into internal/cli/synthetic for Phase 7 consumption (CLI-07)"
    - "Every W1/W2 subcommand's RunE inline synthetic short-circuit is REPLACED by a call to synthetic.GuardCommand(...) — single source of truth"
  artifacts:
    - path: "internal/cli/synthetic/synthetic.go"
      provides: "IsActive / IsHalfSet / GuardCommand / SyntheticDeploymentLabel constant"
      contains: "func IsActive"
    - path: "cmd/ach/cmd/login.go MODIFIED"
      provides: "inline synthetic check replaced with synthetic.GuardCommand(synthetic.GateLogin)"
      contains: "synthetic.GuardCommand"
    - path: "cmd/ach/cmd/logout.go MODIFIED"
      provides: "same"
      contains: "synthetic.GuardCommand"
    - path: "cmd/ach/cmd/config.go MODIFIED"
      provides: "same"
      contains: "synthetic.GuardCommand"
  key_links:
    - from: "every Phase-6 subcommand RunE"
      to: "internal/cli/synthetic/synthetic.go"
      via: "synthetic.GuardCommand(gate)"
      pattern: "synthetic.GuardCommand"
---

<objective>
Promote the inline synthetic-mode short-circuits scattered across
W1/W2 commands into a single source of truth at
`internal/cli/synthetic/`. This is the **cross-cutting enforcement
plan** for CLI-07 + the synthetic clauses of CLI-08 + CLI-09.

The gate logic:
- **IsActive** = ACH_BASE_URL set AND a credential resolves (from
  ACH_API_KEY or --api-key).
- **IsHalfSet** = ACH_BASE_URL set BUT no credential.
- **GuardCommand(gate)** = return CodedError(General) when:
  - gate denies the command in synthetic mode (login/config/logout/
    env-keys-create-without-no-save), OR
  - --deployment / ACH_DEPLOYMENT is set under any command, OR
  - --env-key / ACH_ENV_KEY is set under hydrate/whoami (ek_ requires
    config registry).
  - IsHalfSet returns true (regardless of command).

Plus a `SyntheticDeploymentLabel = "(env)"` const that Phase 7's
state.json writer consumes (CLI-07 last clause).

Purpose: Without this consolidation, every subcommand carries its
own ad-hoc synthetic check — drift inevitable. Single helper makes
CLI-07's wire contract testable in one place.

Output: 1 new package (`internal/cli/synthetic/`) with 3 files +
modifications to 6 W1/W2 command files to swap inline checks for
`synthetic.GuardCommand(gate)`.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/phases/06-cli-foundation/06-CONTEXT.md
@.planning/phases/06-cli-foundation/06-PATTERNS.md
@spec/ach_cli_spec_v20260515_FINALv4.md
@CLAUDE.md
@cmd/ach/cmd/login.go
@cmd/ach/cmd/logout.go
@cmd/ach/cmd/config.go
@cmd/ach/cmd/env_keys.go
@cmd/ach/cmd/hydrate.go
@cmd/ach/cmd/env.go
@cmd/ach/cmd/whoami.go
@.planning/phases/06-cli-foundation/06-01-SUMMARY.md
@.planning/phases/06-cli-foundation/06-03-SUMMARY.md
@.planning/phases/06-cli-foundation/06-04-SUMMARY.md
@.planning/phases/06-cli-foundation/06-05-SUMMARY.md
@.planning/phases/06-cli-foundation/06-06-SUMMARY.md
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author internal/cli/synthetic package</name>
  <files>
    internal/cli/synthetic/doc.go
    internal/cli/synthetic/synthetic.go
    internal/cli/synthetic/synthetic_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"CLI-07" + §"D-08" (synthetic + env-keys create) + §"D-11" (mutex creds in synthetic)
    - 06-PATTERNS.md §"Pattern P4" lines 213-244 (config validation + env-var bag) + §"Pattern S5" (no plaintext)
    - spec/ach_cli_spec_v20260515_FINALv4.md §3.3 (synthetic mode definitive contract)
    - .planning/REQUIREMENTS.md CLI-07 (verbatim AC text)
    - 06-01-SUMMARY.md (internal/cli/config + internal/cli/exit APIs — synthetic returns *exit.CodedError)
  </read_first>
  <behavior>
    - Test 1: IsActive returns true when ACH_BASE_URL="https://x" + ACH_API_KEY="pk_..." (via injected getenv mock).
    - Test 2: IsActive returns false when only ACH_BASE_URL is set, OR only ACH_API_KEY is set.
    - Test 3: IsHalfSet returns true when ACH_BASE_URL set but ACH_API_KEY unset AND --api-key unset (passed via getenv + flag-resolved param).
    - Test 4: IsHalfSet returns false in not-synthetic, fully-synthetic, and bare modes.
    - Test 5: GuardCommand(GateLogin, getenv) when IsActive=true returns *exit.CodedError(General, "ach login is not available in synthetic mode").
    - Test 6: GuardCommand(GateConfig, getenv) when IsActive=true returns CodedError; same for GateLogout, GateEnvKeysCreate.
    - Test 7: GuardCommand(GateEnvKeysCreate, getenv) when IsActive=true AND noSaveFlag=true returns nil (D-08 allows --no-save in synthetic).
    - Test 8: GuardCommand(GateHydrate, getenv) when IsActive=true returns nil (hydrate works in synthetic).
    - Test 9: GuardCommand(GateAny, getenv) with ACH_DEPLOYMENT set returns CodedError "--deployment / ACH_DEPLOYMENT cannot be used in synthetic mode".
    - Test 10: GuardCommand(GateAny, getenv) with --env-key flag set under hydrate/whoami returns CodedError "--env-key / ACH_ENV_KEY cannot be used in synthetic mode".
    - Test 11: GuardCommand(GateAny) when IsHalfSet=true returns CodedError("synthetic mode is half-set: ACH_BASE_URL is set but no credential resolved").
    - Test 12: SyntheticDeploymentLabel == "(env)" — exact string.
  </behavior>
  <action>
    Author `internal/cli/synthetic/doc.go` — package doc citing CLI-07 + spec §3.3.

    Author `internal/cli/synthetic/synthetic.go`:
    - Package `synthetic` under `internal/cli/synthetic/`.
    - Imports: `os`, plus the leaf `internal/cli/exit` package (no other internal deps).
    - Const `SyntheticDeploymentLabel = "(env)"`.
    - Type `Gate int` with values:
      - `GateLogin`, `GateLogout`, `GateConfig`, `GateEnvKeysCreate` — denied in synthetic
      - `GateHydrate`, `GateWhoami`, `GateEnvList`, `GateEnvDescribe`, `GateEnvKeysList`, `GateEnvKeysRevoke`, `GateAdmin` — allowed in synthetic (but still guard against --deployment / --env-key)
    - Type `Params struct { Gate Gate; APIKeyFlag, EnvKeyFlag, DeploymentFlag string; NoSaveFlag bool }` — caller passes the resolved flag values. Env vars read via Getenv (default os.Getenv; injectable seam).
    - Var `Getenv func(string) string = os.Getenv` — test override seam.
    - Funcs:
      - `func IsActive(p Params) bool` — returns true iff Getenv("ACH_BASE_URL") != "" AND (Getenv("ACH_API_KEY") != "" OR p.APIKeyFlag != "").
      - `func IsHalfSet(p Params) bool` — returns true iff Getenv("ACH_BASE_URL") != "" AND IsActive(p) == false.
      - `func GuardCommand(p Params) error` — composite check:
        1. If IsHalfSet → return `&exit.CodedError{Code: exit.General, Msg: "synthetic mode is half-set: ACH_BASE_URL is set but no credential resolved"}`.
        2. If IsActive AND p.Gate in {GateLogin, GateLogout, GateConfig} → CodedError(General, "ach <verb> is not available in synthetic mode").
        3. If IsActive AND p.Gate == GateEnvKeysCreate AND !p.NoSaveFlag → CodedError(General, "ach env-keys create requires --no-save in synthetic mode").
        4. If IsActive AND (p.DeploymentFlag != "" OR Getenv("ACH_DEPLOYMENT") != "") → CodedError(General, "--deployment / ACH_DEPLOYMENT cannot be used in synthetic mode").
        5. If IsActive AND p.Gate in {GateHydrate, GateWhoami, GateEnvList, GateEnvDescribe, GateEnvKeysList, GateEnvKeysRevoke} AND (p.EnvKeyFlag != "" OR Getenv("ACH_ENV_KEY") != "") → CodedError(General, "--env-key / ACH_ENV_KEY cannot be used in synthetic mode").
        6. Else → nil.

    Test file uses `t.Setenv("ACH_BASE_URL", "...")` (Go 1.17+) for env-var fixtures — clean teardown without manual restore.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/cli/synthetic/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./internal/cli/synthetic/...` exits 0.
    - Source assertion: `grep -c 'SyntheticDeploymentLabel\s*=\s*"(env)"' internal/cli/synthetic/synthetic.go` returns 1.
    - Source assertion: `grep -cE 'GateLogin|GateLogout|GateConfig|GateEnvKeysCreate|GateHydrate' internal/cli/synthetic/synthetic.go` returns ≥ 5 (Gate constants present).
    - Source assertion: `grep -c 'IsActive\|IsHalfSet\|GuardCommand' internal/cli/synthetic/synthetic.go` returns ≥ 3.
    - Behavior: Test matrix covers every Gate value × {synthetic, bare, half-set} states.
    - SPDX header line 1: `head -1 internal/cli/synthetic/{doc,synthetic,synthetic_test}.go` all match `Apache-2.0`.
  </acceptance_criteria>
  <done>
    synthetic package green; closed-enum Gate type; single GuardCommand entry point.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Replace inline synthetic checks across all W1/W2 subcommands with synthetic.GuardCommand</name>
  <files>
    cmd/ach/cmd/login.go
    cmd/ach/cmd/logout.go
    cmd/ach/cmd/config.go
    cmd/ach/cmd/env_keys.go
    cmd/ach/cmd/hydrate.go
    cmd/ach/cmd/env.go
    cmd/ach/cmd/whoami.go
  </files>
  <read_first>
    - The current shape of each subcommand's RunE inline synthetic check (each has a 2-3 line `if os.Getenv("ACH_BASE_URL") != "" && ...` block at the top per 06-03/04/05/06 SUMMARYs)
    - 06-07 Task 1 SUMMARY (synthetic.GuardCommand signature)
    - Existing tests for each subcommand — they should continue to pass with the refactor; if a test asserts the inline string verbatim, update to match synthetic's error message.
  </read_first>
  <behavior>
    Pure refactor — no new behavior. Each subcommand swaps:
    ```go
    // before
    if os.Getenv("ACH_BASE_URL") != "" && os.Getenv("ACH_API_KEY") != "" {
        return &exit.CodedError{Code: exit.General, Msg: "ach login is not available in synthetic mode"}
    }
    ```
    For:
    ```go
    // after
    if err := synthetic.GuardCommand(synthetic.Params{
        Gate:           synthetic.GateLogin,
        APIKeyFlag:     apiKeyFlag,
        EnvKeyFlag:     envKeyFlag,
        DeploymentFlag: deploymentFlag,
        NoSaveFlag:     noSaveFlag,  // only env-keys-create populates this
    }); err != nil {
        return err
    }
    ```

    - All existing per-subcommand tests must continue to pass. If error-message string assertions need updates, propagate the new strings.
    - NEW: Each subcommand gains a "synthetic-mode rejection" subtest that exercises the new code path. (Some subtests already exist from W1/W2; this task ensures they still pass + adds coverage for the cases each command did NOT previously test — notably: `--deployment` rejection in synthetic on ANY command; `--env-key` rejection in synthetic on hydrate/whoami.)
  </behavior>
  <action>
    For each of the 7 modified files:
    1. Remove the existing inline `if os.Getenv("ACH_BASE_URL") != "" && ...` block at the top of the RunE.
    2. Insert the `synthetic.GuardCommand(synthetic.Params{...})` call with the appropriate Gate constant:
       - `login.go` → `GateLogin`
       - `logout.go` → `GateLogout`
       - `config.go` → `GateConfig` (apply to EACH child RunE: configListCmd, configShowCmd, configUseCmd, configRemoveCmd, configRenameCmd)
       - `env_keys.go` → `GateEnvKeysCreate` for create, `GateEnvKeysList` for list, `GateEnvKeysRevoke` for revoke
       - `hydrate.go` → `GateHydrate`
       - `env.go` → `GateEnvList` for list, `GateEnvDescribe` for describe
       - `whoami.go` → `GateWhoami`
    3. Resolve the flag values (apiKey/envKey/deployment/noSave) from the cobra flags and pass to Params. Some commands won't set all four — leave them as zero values.
    4. Add the import: `"github.com/ackstorm/ach/internal/cli/synthetic"`.
    5. Verify no behavior drift via the per-subcommand test suite (run `./scripts/dev.sh go test ./cmd/ach/cmd/...` after each file change).
    6. Per CLAUDE.md §"Documentation hygiene": if any error-message string was asserted in a previous test, update that test's expected string to match the new synthetic.GuardCommand wording — same commit.

    For env.go (list + describe) and whoami.go: these commands operate in synthetic mode (they're read-only). Their GuardCommand calls return nil under synthetic + valid creds; they only fail on half-set OR illegal --deployment/--env-key under synthetic.

    For hydrate.go: the inline mutex-cred check from W2-P3 stays (it's a separate concern from synthetic gate); GuardCommand runs IN ADDITION.

    Run `./scripts/dev.sh make lint` after the refactor — `goimports` may need to remove the now-unused `"os"` import if no other os call remains in some files.

    Preserve SPDX headers; no new files in this task.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./cmd/ach/cmd/... ./internal/cli/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./cmd/ach/cmd/... ./internal/cli/...` exits 0 (full Phase 6 unit suite green).
    - Source assertion across the 7 files: `grep -lE 'synthetic\.GuardCommand' cmd/ach/cmd/{login,logout,config,env_keys,hydrate,env,whoami}.go | wc -l` returns 7.
    - Source assertion: `grep -lE 'os\.Getenv\("ACH_BASE_URL"\).*os\.Getenv\("ACH_API_KEY"\)|os\.Getenv\("ACH_API_KEY"\).*os\.Getenv\("ACH_BASE_URL"\)' cmd/ach/cmd/{login,logout,config,env_keys,hydrate,env,whoami}.go | wc -l` returns 0 (every inline check removed).
    - Source assertion: `grep -l 'internal/cli/synthetic' cmd/ach/cmd/{login,logout,config,env_keys,hydrate,env,whoami}.go | wc -l` returns 7 (import landed everywhere).
    - Behavior: each subcommand under `ACH_BASE_URL=https://x ACH_API_KEY=pk_xyz` returns expected exit code per its Gate (login→1, logout→1, config→1, env-keys-create-without-no-save→1, env-keys-create-with-no-save→0, hydrate→0, whoami→0, env list→0, env describe→0).
    - Behavior: ACH_BASE_URL set + ACH_API_KEY unset (half-set) → every command exits 1 with synthetic half-set message.
    - Behavior: ACH_DEPLOYMENT=x + synthetic-active → any command exits 1 with deployment-conflict message.
    - `./scripts/dev.sh make lint` exits 0.
  </acceptance_criteria>
  <done>
    All inline synthetic checks removed; single source of truth in internal/cli/synthetic; full Phase 6 unit suite green; lint clean.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| Environment variables ↔ CLI process | `ACH_BASE_URL`, `ACH_API_KEY`, `ACH_ENV_KEY`, `ACH_DEPLOYMENT` arrive via `os.Getenv` (or a test-seam `Getenv` var). Synthetic mode activates iff ACH_BASE_URL set AND a credential resolves. |
| Flag/env ↔ GuardCommand | The gate consults BOTH flag-resolved values AND env vars to compute the disposition (active / half-set / bare). |
| Inline check → GuardCommand refactor | Task 2 replaces ad-hoc per-subcommand inline checks with the single helper; behavior continuity is the trust anchor. |
| Child process inheritance ↔ ACH_* env vars | Synthetic creds set in the parent shell flow into any child the CLI spawns (e.g. browser opener). |
| Synthetic mode label "(env)" ↔ Phase 7 state.json | `SyntheticDeploymentLabel = "(env)"` const is consumed by Phase 7 state-file writer (CLI-07 last clause). |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-07-01 | Spoofing | Half-set bypass via shell quoting / partial env | mitigate | `IsHalfSet` is the explicit branch in GuardCommand: ACH_BASE_URL set + no credential resolves → exit 1 with "synthetic mode is half-set: …". A user attempting `ACH_BASE_URL=x ./bin/ach hydrate` without ACH_API_KEY hits this gate FIRST — no fallback to bare/config mode. Closes a quoting-edge bypass where the user thought they were synthetic but the bare path silently took over. |
| T-06-07-02 | Tampering | `--env-key` smuggled into synthetic via child-process env injection | mitigate | The GuardCommand check for `--env-key` / `ACH_ENV_KEY` under hydrate/whoami/etc. fires REGARDLESS of how the value arrived. Even if a wrapper script sets ACH_ENV_KEY in the child's environment, the gate rejects on synthetic+ek_-credential. CLI-09 wire contract: ek_ requires the config registry, which is unavailable in synthetic mode by definition. |
| T-06-07-03 | Repudiation | GuardCommand refactor regression silently re-allows a denied command | mitigate | Task 2 mandates per-subcommand "synthetic-mode rejection" subtest CONTINUITY — every previously-passing inline-check test continues to pass with the refactored GuardCommand call. The full Phase 6 unit suite is the verification gate; lint clean + test-green is the merge gate. |
| T-06-07-04 | Information Disclosure | GuardCommand error message leaks the synthetic credential | mitigate | The error messages reference SOURCE (`--api-key`, `ACH_API_KEY`, etc.) NOT VALUE. Source-assertion gate verifies no `pk_` / `ek_` substring flows through the error message constants. |
| T-06-07-05 | Elevation of Privilege | Synthetic mode lets an attacker bypass `ach login` and impersonate via `ACH_API_KEY` | accept | This is the DESIGNED semantics — synthetic mode is the documented escape hatch for CI / container environments where the operator has explicitly provisioned a pk_ via secrets manager. spec §3.3 acknowledges; D-01..D-19 acknowledge. The synthetic-mode posture is a separate trust-domain decision; v1alpha1 accepts it. |
| T-06-07-06 | Tampering | `Getenv` test-seam left as `os.Getenv` in production | mitigate | The seam is a package-level `var Getenv func(string) string = os.Getenv`; tests OVERRIDE the var (`Getenv = func(k string) string { return testMap[k] }`). In production the var points at `os.Getenv` — no functional change. The var is not exported, so external packages cannot override. (Note: `Getenv` is exported per the action; if so, document in the SUMMARY that the override is test-only convention.) |
| T-06-07-07 | Denial of Service | GuardCommand mass-rejects every subcommand on a misconfigured env | mitigate | Every rejection includes a clear stderr message naming the conflicting env var or flag. The user can `unset ACH_BASE_URL` to return to bare mode in one shell command. No state corruption; no recovery procedure needed. |
| T-06-07-SC | Tampering | npm/pip/cargo installs | mitigate | No new third-party deps; stdlib `os` + the foundation `internal/cli/exit` package only. Existing govulncheck ack-list applies. |
</threat_model>

<verification>
After both tasks complete:

```bash
./scripts/dev.sh go test ./internal/cli/... ./cmd/ach/cmd/...
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint
```

Manual smoke matrix (all combinations should match the table in CLI-07):
```bash
# bare mode (config-driven)
unset ACH_BASE_URL ACH_API_KEY
./bin/ach login --base-url https://hub.test --deployment prod  # interactive

# synthetic mode — denied commands
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach login  # exit 1
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach config list  # exit 1
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach logout  # exit 1
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach env-keys create --environment demo --name x  # exit 1
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach env-keys create --environment demo --name x --no-save  # exit 0

# synthetic mode — allowed commands
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach hydrate --environment demo  # exit 0 (or whatever server returns)
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach whoami --verify  # exit 0
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach env list  # exit 0

# half-set
ACH_BASE_URL=https://hub.test ./bin/ach whoami  # exit 1 half-set message

# --deployment conflict
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach env list --deployment prod  # exit 1

# --env-key conflict
ACH_BASE_URL=https://hub.test ACH_API_KEY=pk_xyz ./bin/ach hydrate --env-key local-laptop --environment demo  # exit 1
```
</verification>

<success_criteria>
- `internal/cli/synthetic` is the single source of truth for CLI-07 enforcement.
- All 7 W1/W2 subcommands consume `synthetic.GuardCommand`.
- No inline `ACH_BASE_URL && ACH_API_KEY` checks remain in subcommand files.
- Half-set, --deployment conflict, --env-key conflict all return exit 1 with deterministic messages.
- Full unit test suite + lint green.
- `SyntheticDeploymentLabel = "(env)"` const exposed for Phase 7 state.json consumption.
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-07-SUMMARY.md` when done. Record:
- Final synthetic.Params field set (immutable for Phase 7 consumers).
- Each subcommand's chosen Gate constant (for the verifier's mapping table).
- Any error-message string changes that broke existing tests and were updated.
</output>
