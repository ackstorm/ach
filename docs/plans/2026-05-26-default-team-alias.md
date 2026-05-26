# Make LiteLLM default-team alias configurable at operator startup

> **For Claude:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task.

**Goal:** Replace the three hardcoded LiteLLM team-alias `"default"` literals with a single deployer-controlled value sourced from `--default-team-alias` (cobra flag) / `ACH_DEFAULT_TEAM_ALIAS` (env mirror). Operator-bootstrapped team (commit `10a845c` J.5) and every SSO callback (commits `aaa175b`, `10a845c` J.4 + J.5) MUST resolve the same alias from the same canonical source.

**Architecture:** Single value, two wiring fan-outs:

```
        ┌────────────────────────────────┐
        │ cobra flag --default-team-alias│
        │  env mirror ACH_DEFAULT_TEAM_  │
        │            ALIAS               │
        │  default literal "default"     │
        └────────────────┬───────────────┘
                         │
              ┌──────────┴──────────┐
              ▼                     ▼
   `ach operator`              `ach platform-api`
        │                            │
        │                  ┌─────────┼──────────┐
        ▼                  ▼         ▼          ▼
LiteLLMConnection-     auth.Deps   envkeys.Deps  …
Reconciler.            .DefaultTeam .DefaultTeam
DefaultTeamAlias       Alias        Alias
        │                  │         │
        ▼                  ▼         ▼
client.EnsureDefault   provisionUser CreateHandler
Team(ctx, alias)       (reads from   (reads from
                       deps.DTA)     deps.DTA)
```

**Severity:** LOW (enhancement). The literal `"default"` continues to work for the bootstrap deployment topology; this plan opens the door to tenant-specific aliases without code patches.

**Tech Stack (no churn):**
- cobra/viper (already used in `cmd/ach/cmd/`)
- `internal/config` helpers: `EnvOr(key, fallback string) string` (the pattern every other env-mirror flag in `operator.go` and `platform_api.go` already follows)
- Go 1.26 + controller-runtime v0.24.1 (no version bump)
- Helm chart at `deploy/helm/ach/` (template edit only)

**Working directory:** `/home/jcm/Projects/ach/`

**Branch:** `feat/default-team-alias-configurable`

**Cross-plan refs:**
- DEPENDS ON the §2 domain port (`docs/plans/2026-05-25-ach-domain-port.md`) for the SSO `auth.Deps` + EnvKey `envkeys.Deps` surfaces. Both have landed (commit `10a845c` for J.4 + J.5, baseline already in `main`).
- INDEPENDENT of TODO §1, §3, §5, §6, §7, §8, §9, §10, §11, §16 — no overlapping files.
- Closes TODO §15.

**Out of scope (DO NOT touch in this plan):**
- Forwarder (`internal/forwarder/`) — does not touch the default team; nothing to configure there.
- Content-service (`internal/contentservice/`) — same.
- CRD-level configuration of the alias (e.g. a field on `LiteLLMConnection.Spec`). The decision is "startup config, not CR field" because the alias is a per-deployment identity-model knob, not a per-CR runtime parameter; threading it through the CRD would force every reconcile to re-read the spec and would let a single CR override the operator-wide identity model. Out of scope; revisit only if multi-tenant per-CR aliases become a documented requirement.
- The bootstrap deployment topology (chart `defaults` ship `defaultTeamAlias: "default"` so existing deployers see zero behavior change).
- Renaming `OutcomeDefaultTeamMissing` audit outcome string — even when the alias is `"engineering"`, the outcome name remains `default_team_missing` because the **semantic** "the operator's canonical bootstrap team is unreachable" is unchanged. Renaming would force a breaking change in downstream audit-log consumers. The user-visible message in `classifyProvisionError` already says "default team unreachable" — that phrasing stays; deployers reading it know their canonical team alias is whatever they configured.

**Definition of done for this plan:**
1. `git grep -nE '"default"' internal/litellm/team.go internal/platformapi/auth/sso.go internal/platformapi/envkeys/handler.go` returns ZERO matches in production code (test fixtures may still use `"default"` because that is the chart default).
2. `git grep -n 'defaultTeamAlias\b\|defaultTeam\b' internal/` shows the constants are GONE; only field/parameter names remain.
3. `./scripts/dev.sh make unit` PASS. New unit tests assert: (a) every `Deps` constructor wires `DefaultTeamAlias` through, (b) `provisionUser` and `envkeys.CreateHandler` use the dep value (not a string literal), (c) `RESTClient.EnsureDefaultTeam(ctx, "engineering")` calls `ListTeamsByAlias(ctx, "engineering")` and on empty calls `CreateTeam(...)` with `TeamAlias: "engineering"`.
4. `./scripts/dev.sh make envtest-run` PASS — the LiteLLMConnectionReconciler envtest exercises the new `DefaultTeamAlias` field via the kept wiring (re-uses existing `wiringFakeLiteLLM`).
5. `./scripts/dev.sh make e2e-focus FOCUS="default team alias configurable"` PASS — new e2e case deploys with `--set defaultTeamAlias=engineering`, asserts (a) team with alias=engineering exists in LiteLLM, (b) operator and platform-api Deployments carry `ACH_DEFAULT_TEAM_ALIAS=engineering` in env (`kubectl describe deploy`), (c) `kubectl logs deploy/ach-operator | grep -F 'default team alias'` shows the configured value at boot.
6. `make pre-push` GREEN (15/15) including SPDX + lint + govulncheck.
7. Single commit per phase using conventional commit format (`feat(litellm)`, `feat(platform-api)`, `feat(operator)`, `feat(helm)`, `docs(claude-md)`).

---

## Phase 0 — Branch setup

### Task 0.1: Confirm cwd and create branch

**Step 1:** Verify cwd.

```bash
cd /home/jcm/Projects/ach
pwd                       # must print: /home/jcm/Projects/ach
git branch --show-current
git status --short
```

**Expected:** `pwd` is `/home/jcm/Projects/ach`. The pre-existing modified files in `git status --short` (`.gitignore`, `go.sum`, `internal/platformapi/auth/sso.go`, `TODO`) are unrelated work; DO NOT include them in this plan's commits.

**Step 2:** Stash unrelated WIP and branch from `main`.

```bash
git stash push -m "pre-default-team-alias WIP" -- \
  .gitignore go.sum internal/platformapi/auth/sso.go TODO 2>/dev/null || true

git fetch origin main
git checkout -B feat/default-team-alias-configurable origin/main
git log --oneline -5
```

**Expected:** HEAD matches `origin/main` HEAD (last commit `aaa175b fix(environment): J.6 ...`).

**Step 3:** Pop stash if created.

```bash
git stash list
git stash pop 2>/dev/null || true
```

**Commit:** none.

---

## Phase 1 — Pre-flight inventory (no code changes)

### Task 1.1: Enumerate every hardcoded `"default"` and every `EnsureDefaultTeam` call site

**Why first:** Before widening the interface we need an exhaustive list of touch points so later tasks land them all in one atomic widening (no partial migration leaves the `Client` interface broken). Output of this task is a single sanity-check command output saved as a scratch list — NOT a commit.

**Step 1:** Run the three discovery greps from the repo root.

```bash
cd /home/jcm/Projects/ach

# A. The two production sites that use the literal "default" as alias
git grep -n '"default"' \
  internal/litellm/team.go \
  internal/platformapi/auth/sso.go \
  internal/platformapi/envkeys/handler.go

# B. Every implementor / caller of EnsureDefaultTeam
git grep -nE 'EnsureDefaultTeam' \
  internal/litellm \
  internal/connection \
  internal/controller \
  internal/platformapi \
  internal/orphan

# C. Package-level constants we will remove
git grep -nE 'const\s+(defaultTeamAlias|defaultTeam)\b' internal/
```

**Step 2:** Confirm output matches expected snapshot (snapshot taken at plan-write time against commit `aaa175b`):

| File | Line | Token | Disposition (later phases) |
|---|---|---|---|
| `internal/litellm/team.go` | 18 | `const defaultTeamAlias = "default"` | DELETE; alias becomes a method parameter |
| `internal/litellm/team.go` | 30 | `ListTeamsByAlias(ctx, defaultTeamAlias)` | use parameter |
| `internal/litellm/team.go` | 32 | `%q, defaultTeamAlias)` | use parameter |
| `internal/litellm/team.go` | 37 | `TeamAlias: defaultTeamAlias` | use parameter |
| `internal/litellm/team.go` | 38 | `%q, defaultTeamAlias)` | use parameter |
| `internal/platformapi/auth/sso.go` | 503 | `ListTeamsByAlias(ctx, "default")` | read from `deps.DefaultTeamAlias` |
| `internal/platformapi/auth/sso.go` | 510 | `"LiteLLM has no team with alias 'default'"` | template the alias into the error |
| `internal/platformapi/auth/sso.go` | 522 | `Teams: []string{"default"}` | `[]string{deps.DefaultTeamAlias}` |
| `internal/platformapi/envkeys/handler.go` | 120 | `const defaultTeam = "default"` | DELETE; field on `Deps` |
| `internal/platformapi/envkeys/handler.go` | 314 | `Teams: []string{defaultTeam}` | `[]string{deps.DefaultTeamAlias}` |
| `internal/platformapi/envkeys/handler.go` | 329 | `TeamMemberAdd(ctx, defaultTeam, ...)` | `TeamMemberAdd(ctx, deps.DefaultTeamAlias, ...)` (see Pitfall §2 below) |
| `internal/platformapi/envkeys/handler.go` | 334 | `"team", defaultTeam` | `"team", deps.DefaultTeamAlias` |

| `EnsureDefaultTeam` site | Disposition |
|---|---|
| `internal/litellm/client.go:150` | Widen interface: `EnsureDefaultTeam(ctx context.Context, alias string) error` |
| `internal/litellm/team.go:29` | Method signature changes; body uses parameter |
| `internal/litellm/noop.go:142` | Signature changes; body logs the alias |
| `internal/connection/client.go:137` | Wrapper forwards `alias` |
| `internal/controller/ach/litellmconnection_controller.go:165` | Caller passes `r.DefaultTeamAlias` |
| `internal/controller/ach/main_wiring_envtest_test.go:117` | Test fake signature update |
| `internal/platformapi/teams/lookup_test.go:177` | Test fake signature update |
| `internal/platformapi/hydrate/handler_test.go:585` | Test fake signature update |
| `internal/platformapi/admin/handler_test.go:491` | Test fake signature update |
| `internal/platformapi/envkeys/handler_test.go:1362` | Test fake signature update |
| `internal/platformapi/environments/handler_test.go:574` | Test fake signature update |
| `internal/orphan/runnable_test.go:485` | Test fake signature update |

**Pitfalls captured by this inventory:**

1. The `envkeys/handler.go:329` call site is `TeamMemberAdd(ctx, defaultTeam, userInfo.UserID, "user")` — note that it passes the **alias** as the `team_id` argument. The SSO callback (`auth/sso.go`) takes the more-correct route of resolving the team_id from the alias via `ListTeamsByAlias` before calling `TeamMemberAdd`. The envkeys path is "incorrect but tolerated" because LiteLLM treats unknown team_ids gracefully under the duplicate-add-tolerant comment immediately below. This plan PRESERVES that behavior verbatim — passing `deps.DefaultTeamAlias` through is the literal substitution. Fixing the alias-vs-team_id confusion is a separate concern (call it TODO §15-followup) and would change observable error handling; out of scope here.

2. The `EnsureDefaultTeam` interface widening MUST land atomically with every implementor (RESTClient + NoopClient + connection.Client) AND every test fake — otherwise the build breaks. Phase 2 below does the widening as a single commit.

**Commit:** none (discovery only; output is for the implementer's eyes).

---

## Phase 2 — Widen the LiteLLM Client interface

**Why:** Every downstream change in Phases 3-5 depends on `EnsureDefaultTeam` accepting an alias parameter. If we widen the interface in a later phase, the intermediate phases leave the build broken. Phase 2 is one atomic commit that:
- changes `Client.EnsureDefaultTeam` signature
- changes `RESTClient`, `NoopClient`, `connection.Client` implementations
- changes every test-fake satisfier across the 7 files in the inventory
- temporarily leaves `LiteLLMConnectionReconciler.Reconcile` calling `client.EnsureDefaultTeam(ctx, "default")` — Phase 4 will replace the literal with `r.DefaultTeamAlias`

This phase changes ZERO behavior (still alias="default") so its tests can be a strict drop-in: add `, "default"` to every existing call.

### Task 2.1: Add the alias parameter to the interface and every implementor

**Files:**
- `internal/litellm/client.go` (interface signature + docstring)
- `internal/litellm/team.go` (RESTClient body — drop the const, use the parameter)
- `internal/litellm/noop.go` (NoopClient body — log the alias)
- `internal/connection/client.go` (wrapper forwards)

**Step 1:** Edit `internal/litellm/client.go` around line 140-150.

Replace the interface method signature:

```go
EnsureDefaultTeam(ctx context.Context) error
```

with:

```go
// EnsureDefaultTeam is the operator-side bootstrap call that
// guarantees LiteLLM has at least one Team with the configured
// alias (sourced from --default-team-alias / ACH_DEFAULT_TEAM_ALIAS
// at operator startup; default "default" preserves the historical
// bootstrap topology). Idempotent: list by alias first, only POST
// /team/new on empty. Called by the LiteLLMConnection reconciler
// after a successful probe so we never need the deployer to
// hand-seed the team via cluster.sh / curl.
//
// The alias is a startup-time configuration value and stays stable
// across the operator process lifetime; the parameter exists so
// the same RESTClient can be reused by tests that want to assert
// against multiple aliases without recompiling.
//
// Returns nil on success (team already present OR newly created).
// Returns wrapped error on LiteLLM unreachable / 5xx / unauthorized
// — caller logs and continues; the next reconcile retries.
EnsureDefaultTeam(ctx context.Context, alias string) error
```

**Step 2:** Edit `internal/litellm/team.go`:
- DELETE lines 12-18 (the `defaultTeamAlias` constant block — comment too).
- Change the method signature on line 29 to `func (c *RESTClient) EnsureDefaultTeam(ctx context.Context, alias string) error`.
- Replace every body reference to `defaultTeamAlias` with `alias` (lines 30, 32, 37, 38 per the inventory).
- Update the function-level docstring to mention "the configured alias" instead of "defaultTeamAlias".

The resulting body MUST be:

```go
// EnsureDefaultTeam guarantees LiteLLM has at least one Team with
// the configured alias. Idempotent — list-first via
// ListTeamsByAlias, only POST /team/new on empty. Called by the
// LiteLLMConnection reconciler after a successful probe so the
// operator-side bootstrap converges without deployer intervention.
//
// The alias is supplied by the caller (sourced from
// --default-team-alias / ACH_DEFAULT_TEAM_ALIAS at operator
// startup). Tests may invoke this directly with arbitrary
// aliases to assert behavior across multi-tenant configurations.
//
// Returns nil if the team already exists OR was just created.
// Returns wrapped error on LiteLLM unreachable / 5xx /
// unauthorized — caller logs and continues.
func (c *RESTClient) EnsureDefaultTeam(ctx context.Context, alias string) error {
	existing, err := c.ListTeamsByAlias(ctx, alias)
	if err != nil {
		return fmt.Errorf("ensure default team: list by alias %q: %w", alias, err)
	}
	if len(existing) > 0 {
		return nil
	}
	if _, err := c.CreateTeam(ctx, &NewTeamRequest{TeamAlias: alias}); err != nil {
		return fmt.Errorf("ensure default team: create %q: %w", alias, err)
	}
	return nil
}
```

**Step 3:** Edit `internal/litellm/noop.go` around line 142.

Replace:

```go
func (c *NoopClient) EnsureDefaultTeam(_ context.Context) error {
	c.Log.Info("stub: would ensure LiteLLM default team")
	return nil
}
```

with:

```go
func (c *NoopClient) EnsureDefaultTeam(_ context.Context, alias string) error {
	c.Log.Info("stub: would ensure LiteLLM default team", "alias", alias)
	return nil
}
```

**Step 4:** Edit `internal/connection/client.go` around line 137-143.

Replace the wrapper signature + body:

```go
func (c *Client) EnsureDefaultTeam(ctx context.Context, alias string) error {
	client, err := c.current()
	if err != nil {
		return err
	}
	return client.EnsureDefaultTeam(ctx, alias)
}
```

**Step 5:** Build (compile-only) to confirm the interface assertions on `RESTClient`, `NoopClient`, `connection.Client` still hold.

```bash
./scripts/dev.sh go build ./internal/litellm/... ./internal/connection/...
```

**Expected:** clean build (no error). The next step updates the controller + test fakes that still call the old signature; those packages will not build YET, which is fine — Task 2.2 closes the gap atomically in the same commit.

### Task 2.2: Update every test fake and the temporary controller call site

**Why same commit:** A widening of `EnsureDefaultTeam` that leaves any caller compiling against the old signature is a broken commit. Land all the touch-ups together.

**Files (in inventory order):**
- `internal/controller/ach/litellmconnection_controller.go:165` — TEMPORARILY pass the literal `"default"`. Phase 4 replaces with `r.DefaultTeamAlias`.
- `internal/controller/ach/main_wiring_envtest_test.go:117`
- `internal/platformapi/teams/lookup_test.go:177`
- `internal/platformapi/hydrate/handler_test.go:585`
- `internal/platformapi/admin/handler_test.go:491`
- `internal/platformapi/envkeys/handler_test.go:1362`
- `internal/platformapi/environments/handler_test.go:574`
- `internal/orphan/runnable_test.go:485`

**Step 1:** Edit `internal/controller/ach/litellmconnection_controller.go` around line 165.

Replace:

```go
if err := client.EnsureDefaultTeam(ctx); err != nil {
```

with:

```go
if err := client.EnsureDefaultTeam(ctx, "default"); err != nil {
```

Leave the surrounding comment block alone (Phase 4 rewrites it).

**Step 2:** For each test fake, change the method signature from `EnsureDefaultTeam(_ context.Context) error` to `EnsureDefaultTeam(_ context.Context, _ string) error`. The bodies are all `return nil` — leave them.

The minimal edit per file is the signature line; no test assertions reference the alias yet.

**Step 3:** Compile-and-test.

```bash
./scripts/dev.sh go build ./...
./scripts/dev.sh make unit
./scripts/dev.sh make lint-changed
```

**Expected:** all green. Unit suite previously green stays green because no behavior changed — just signatures.

### Task 2.3: New unit test — RESTClient.EnsureDefaultTeam uses the supplied alias

**File:** `internal/litellm/team_test.go` (the file already exists per the directory listing).

**Step 1:** Read the existing test file once to match its setup style (httptest fakeserver + RESTClient wiring).

```bash
head -80 /home/jcm/Projects/ach/internal/litellm/team_test.go
```

**Step 2:** Add a new sub-test `TestRESTClient_EnsureDefaultTeam_UsesSuppliedAlias` that:
1. Spins a fake httptest server with two routed handlers:
   - `GET /v2/team/list?team_alias=engineering&page_size=100` → returns empty `{"teams":[]}` JSON
   - `POST /team/new` → assert request body decodes to `NewTeamRequest{TeamAlias:"engineering"}`; respond with `{"team_id":"t-abc","team_alias":"engineering"}` JSON
2. Constructs a RESTClient pointed at the fake server.
3. Calls `c.EnsureDefaultTeam(ctx, "engineering")`.
4. Asserts the call returns nil AND that both fake handlers were hit exactly once.

Also add `TestRESTClient_EnsureDefaultTeam_NoCreateWhenPresent`:
1. Fake `GET /v2/team/list?team_alias=tenant-a&page_size=100` returns one team `{"teams":[{"team_id":"t-1","team_alias":"tenant-a"}]}`.
2. Fake `POST /team/new` handler asserts it is NEVER called (record a hit counter; the test fails if >0 after the call).
3. Calls `c.EnsureDefaultTeam(ctx, "tenant-a")`.
4. Asserts nil error AND the create handler hit counter is 0.

**Step 3:** Run.

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/litellm/...
```

**Expected:** both new tests PASS plus the pre-existing suite.

**Commit (single commit for Phase 2):**

```
feat(litellm): TODO §15 — widen EnsureDefaultTeam interface to accept alias

The Client interface's EnsureDefaultTeam method now accepts the team
alias as an explicit parameter instead of reading a package-level
constant. RESTClient, NoopClient, and the connection.Client wrapper
forward the alias verbatim. Every test fake satisfies the new
signature.

This is a refactor-only step: the LiteLLMConnectionReconciler caller
still passes the literal "default", so observable behavior is
unchanged. The downstream wiring (Phase 3-5) will source the alias
from the new --default-team-alias flag.

Refs: TODO §15
```

---

## Phase 3 — Add the cobra flag + env-mirror to both `operator` and `platform-api`

**Why two subcommands:** Both modes need the alias — the operator-side bootstrap (Phase 4) AND the SSO/EnvKey paths (Phase 5). They run in separate Pods with separate flag-sets; the flag MUST be declared on each so both Deployments can carry the env var.

### Task 3.1: Add the flag to `cmd/ach/cmd/operator.go`

**File:** `cmd/ach/cmd/operator.go`

**Step 1:** Locate the flag-declaration block near line 86-107 (between `metricsAddr` and the `zap.Options{}` line). Choose to add the new flag right after the `enableHTTP2` declaration so the visual grouping puts "ACH-domain configuration" together (vs the controller-runtime infra flags above).

**Step 2:** Add a local variable and flag declaration:

```go
var defaultTeamAlias string
flag.StringVar(&defaultTeamAlias, "default-team-alias",
	config.EnvOr("ACH_DEFAULT_TEAM_ALIAS", "default"),
	"LiteLLM team alias every SSO-provisioned user is enrolled into "+
		"and the operator bootstraps on LiteLLMConnection sync. "+
		"Defaults to \"default\"; deployers may pick \"engineering\", "+
		"\"tenant-a\", etc. Must be the SAME value across operator and "+
		"platform-api Pods (the chart wires both from the single "+
		"`defaultTeamAlias` value).")
```

**Step 3:** Log the resolved value at startup (right after the existing `operatorSetupLog.Info("plugin size limit configured", ...)` line near 137):

```go
operatorSetupLog.Info("default team alias configured", "alias", defaultTeamAlias)
```

**Step 4:** Pass the value into `LiteLLMConnectionReconciler` (Phase 4 adds the field; for now we just thread the variable into scope so Phase 4 is a one-line struct edit).

Locate the reconciler construction at lines 270-278. The full struct literal will be updated in Phase 4; for Phase 3 we ONLY add the local variable and the log line.

**Step 5:** Build + lint.

```bash
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint-changed
```

**Expected:** clean. The variable `defaultTeamAlias` will be flagged as "declared but not used" — that's intentional and resolved in Phase 4. To keep this phase green-compile, add `_ = defaultTeamAlias` immediately after the log line as a temporary marker (delete in Phase 4).

### Task 3.2: Add the flag to `cmd/ach/cmd/platform_api.go`

**File:** `cmd/ach/cmd/platform_api.go`

**Step 1:** Add `DefaultTeamAlias string` to the `platformAPIConfig` struct (alongside `Namespace string` near line 91).

**Step 2:** In `validatePlatformAPIConfig` (line 93+), source the value:

```go
cfg.DefaultTeamAlias = config.EnvOr("ACH_DEFAULT_TEAM_ALIAS", "default")
```

Add this line right before `cfg.AllowlistPath = ...` so the ACH-domain config sits together.

**Step 3:** Log the value in `runPlatformAPI` (line 301 block). Replace the existing `logger.Info` call's args with the additional kv:

```go
logger.Info("platform-api starting",
	"bind", cfg.BindAddr,
	"namespace", cfg.Namespace,
	"baseURL", cfg.BaseURL,
	"defaultTeamAlias", cfg.DefaultTeamAlias,
)
```

**Step 4:** Thread `cfg.DefaultTeamAlias` into the `platformapi.Deps` struct construction (lines 247-263). Add a new key — but the field doesn't exist yet on `platformapi.Deps`. Phase 5 owns that struct addition; for Phase 3 we add a temporary marker:

```go
_ = cfg.DefaultTeamAlias // wired into Deps in Phase 5
```

after the `out.server = platformapi.Deps{...}` construction.

**Step 5:** Build + lint.

```bash
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint-changed
```

**Expected:** clean.

### Task 3.3: Unit test the flag/env precedence

**File:** new file `cmd/ach/cmd/operator_flag_test.go` (and analogous `platform_api_flag_test.go`).

Use `t.Setenv` (stdlib `testing` since Go 1.17) to assert that `config.EnvOr("ACH_DEFAULT_TEAM_ALIAS", "default")` honors the env var. Two sub-tests:

1. `Unset` → returns `"default"`.
2. `Set=engineering` → returns `"engineering"`.

This is a thin assertion that doesn't require booting the whole cobra command (which would need a kubeconfig); it exercises the env-var-mirror contract that Phase 4 and 5 depend on.

```bash
./scripts/dev.sh make unit-pkg PKG=./cmd/ach/...
```

**Commit (single commit for Phase 3):**

```
feat(operator,platform-api): TODO §15 — add --default-team-alias flag

Operator and platform-api subcommands now accept --default-team-alias
(env mirror ACH_DEFAULT_TEAM_ALIAS, default "default"). The value is
logged at startup but not yet wired into the reconciler / Deps; that
wiring lands in Phase 4 (operator) and Phase 5 (platform-api).

Refs: TODO §15
```

---

## Phase 4 — Wire the alias into LiteLLMConnectionReconciler

### Task 4.1: Add `DefaultTeamAlias` field on the reconciler struct

**File:** `internal/controller/ach/litellmconnection_controller.go`

**Step 1:** Add the field to the struct near line 37-43:

```go
type LiteLLMConnectionReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Cache            connection.CacheReader
	Namespace        string
	// DefaultTeamAlias is the LiteLLM team alias the operator
	// bootstraps on every successful Reconcile (the "operator-side
	// bootstrap" branch at the end of Reconcile). Sourced from the
	// process-startup flag --default-team-alias / env var
	// ACH_DEFAULT_TEAM_ALIAS in cmd/ach/cmd/operator.go; defaulting
	// to "default" preserves the historical bootstrap topology.
	// All SSO-provisioned users are enrolled into the team with
	// this alias by the platform-api SSO callback (which reads the
	// same value from auth.Deps.DefaultTeamAlias) — operator and
	// platform-api MUST be configured with the same value, enforced
	// by the Helm chart wiring a single value into both env vars.
	DefaultTeamAlias string
	Log              logr.Logger
}
```

**Step 2:** Update the call site at line 165 in `Reconcile`:

```go
if err := client.EnsureDefaultTeam(ctx, r.DefaultTeamAlias); err != nil {
	r.Log.Info("EnsureDefaultTeam failed; will retry on next reconcile",
		"alias", r.DefaultTeamAlias, "err", err.Error())
}
```

Also update the surrounding comment block (lines 158-164) to reference "the configured team alias" instead of "canonical `default` team":

```go
// Operator-side bootstrap: guarantee LiteLLM has the configured
// team (alias from r.DefaultTeamAlias) before any SSO callback
// fires. Idempotent — list-first, create only on empty. Failure
// is logged + tolerated; the next reconcile (5 minutes) retries.
// We deliberately do not fail the reconcile on this: the
// LiteLLMConnection itself is Synced=True (probe succeeded);
// only the team-seed side effect failed, which a transient
// LiteLLM hiccup might cause.
```

### Task 4.2: Wire the value from operator.go into the reconciler

**File:** `cmd/ach/cmd/operator.go`

**Step 1:** Locate the reconciler construction at lines 270-278. Replace with:

```go
if err = (&achcontroller.LiteLLMConnectionReconciler{
	Client:           mgr.GetClient(),
	Scheme:           mgr.GetScheme(),
	Cache:            connCache,
	Namespace:        watchNS,
	DefaultTeamAlias: defaultTeamAlias,
	Log:              ctrl.Log.WithName("controller").WithName("LiteLLMConnection"),
}).SetupWithManager(mgr); err != nil {
	return fmt.Errorf("unable to create controller LiteLLMConnection: %w", err)
}
```

**Step 2:** Remove the temporary `_ = defaultTeamAlias` marker added in Phase 3.

### Task 4.3: Unit test — reconciler passes its configured alias

**File:** `internal/controller/ach/litellmconnection_controller_test.go` (already exists per the directory listing).

**Step 1:** Read existing test setup pattern (envtest+fake LiteLLM via `wiringFakeLiteLLM` at `main_wiring_envtest_test.go:117`).

**Step 2:** Promote `wiringFakeLiteLLM.EnsureDefaultTeam` to capture the received alias:

```go
type wiringFakeLiteLLM struct {
	// ... existing fields ...
	lastEnsureAlias string
}

func (f *wiringFakeLiteLLM) EnsureDefaultTeam(_ context.Context, alias string) error {
	f.lastEnsureAlias = alias
	return nil
}
```

**Step 3:** Add a new sub-test `TestLiteLLMConnectionReconciler_EnsureDefaultTeam_UsesConfiguredAlias`:
1. Construct reconciler with `DefaultTeamAlias: "engineering"`.
2. Trigger a successful reconcile (the existing happy-path setup applies).
3. Assert `fake.lastEnsureAlias == "engineering"`.

**Step 4:** Add `TestLiteLLMConnectionReconciler_EnsureDefaultTeam_DefaultAlias`:
1. Construct reconciler with `DefaultTeamAlias: "default"` (the historical literal).
2. Run reconcile.
3. Assert `fake.lastEnsureAlias == "default"` — locks in the no-behavior-change for the default deployment.

```bash
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestLiteLLMConnectionReconciler_EnsureDefaultTeam
```

**Commit (single commit for Phase 4):**

```
feat(operator): TODO §15 — LiteLLMConnectionReconciler honors --default-team-alias

The operator-side bootstrap (EnsureDefaultTeam call after a
successful LiteLLMConnection probe) now uses the alias from the
new --default-team-alias / ACH_DEFAULT_TEAM_ALIAS startup config
instead of the hardcoded literal "default". Default value
preserves historical behavior.

Refs: TODO §15
```

---

## Phase 5 — Wire the alias into platformapi `auth.Deps` and `envkeys.Deps`

### Task 5.1: Add `DefaultTeamAlias` field on `auth.Deps`

**File:** `internal/platformapi/auth/sso.go`

**Step 1:** Add the field to the `Deps` struct (around line 38-90). Place it right after `Namespace` and before `InsertPKFn` so it sits with the other namespace-scope ACH-domain config:

```go
// DefaultTeamAlias is the LiteLLM team alias every SSO-
// provisioned user is enrolled into (Hub §17 / API-02 fail-loud).
// Sourced at process start from --default-team-alias /
// ACH_DEFAULT_TEAM_ALIAS via cmd/ach/cmd/platform_api.go; the
// chart wires the same value into the operator Deployment so the
// operator-bootstrapped team and the SSO enrollment converge on
// the same name. Default "default" preserves historical behavior.
DefaultTeamAlias string
```

**Step 2:** Replace the literal `"default"` in `provisionUser` at lines 498-553.

Line 503 — replace:

```go
defaultTeams, ltErr := deps.LiteLLM.ListTeamsByAlias(ctx, "default")
```

with:

```go
defaultTeams, ltErr := deps.LiteLLM.ListTeamsByAlias(ctx, deps.DefaultTeamAlias)
```

Line 510 — replace the error message:

```go
err:  errors.New("LiteLLM has no team with alias 'default'"),
```

with:

```go
err:  fmt.Errorf("LiteLLM has no team with alias %q", deps.DefaultTeamAlias),
```

(this requires `fmt` already imported in the file — confirm; it is not currently imported in `sso.go`. Add `"fmt"` to the import block.)

Line 522 — replace:

```go
Teams:     []string{"default"},
```

with:

```go
Teams:     []string{deps.DefaultTeamAlias},
```

**Step 3:** The two `TeamMemberAdd` call sites at lines 532 and 550 use `defaultTeamID` (resolved from `ListTeamsByAlias`), NOT a string literal — those lines are already correct and need no change. Confirm with `grep -n 'TeamMemberAdd' internal/platformapi/auth/sso.go`.

### Task 5.2: Add `DefaultTeamAlias` field on `envkeys.Deps`; remove the package constant

**File:** `internal/platformapi/envkeys/handler.go`

**Step 1:** DELETE the package constant block at lines 116-120:

```go
// defaultTeam is the LiteLLM Team alias every first-SSO user gets
// enrolled into per Hub §17 (deployer concern). When LiteLLM rejects
// the TeamMemberAdd because the default Team does not exist, the
// handler emits OutcomeDefaultTeamMissing.
const defaultTeam = "default"
```

**Step 2:** Add the field to the `Deps` struct (around line 79-88). After `Namespace string`:

```go
// DefaultTeamAlias is the LiteLLM team alias every first-SSO user
// is enrolled into during the §8.2 CreateHandler flow. Sourced at
// process start from --default-team-alias / ACH_DEFAULT_TEAM_ALIAS
// via cmd/ach/cmd/platform_api.go; the chart wires the same value
// into the operator Deployment so the operator-bootstrapped team
// and the envkeys enrollment converge on the same name. Default
// "default" preserves historical behavior. NOTE: this handler
// passes the alias as the team_id argument to TeamMemberAdd —
// see the comment at the call site for the historical rationale
// (preserved verbatim by TODO §15 to avoid scope creep).
DefaultTeamAlias string
```

**Step 3:** Replace the three `defaultTeam` references at lines 314, 329, 334:

Line 314 — `Teams: []string{defaultTeam}` → `Teams: []string{deps.DefaultTeamAlias}`.

Line 329 — `deps.LiteLLM.TeamMemberAdd(ctx, defaultTeam, userInfo.UserID, "user")` → `deps.LiteLLM.TeamMemberAdd(ctx, deps.DefaultTeamAlias, userInfo.UserID, "user")`.

Line 334 — `"team", defaultTeam` → `"team", deps.DefaultTeamAlias`.

### Task 5.3: Thread `cfg.DefaultTeamAlias` through `platformapi.Deps` into both sub-Deps constructors

**File:** `internal/platformapi/server.go` (or wherever `platformapi.New(deps)` reads `Deps` to construct the auth and envkeys handlers) — locate via:

```bash
grep -rn "auth.Deps{\|envkeys.Deps{" /home/jcm/Projects/ach/internal/platformapi/
```

**Step 1:** Add `DefaultTeamAlias string` to `platformapi.Deps` (the top-level struct in `internal/platformapi/`, likely `server.go` or `deps.go`).

**Step 2:** Locate every construction of `auth.Deps{...}` and `envkeys.Deps{...}` inside `platformapi/`. For each, add `DefaultTeamAlias: deps.DefaultTeamAlias`.

**Step 3:** Edit `cmd/ach/cmd/platform_api.go` (line 247-263) — add `DefaultTeamAlias: cfg.DefaultTeamAlias,` to the `platformapi.Deps{...}` literal. Remove the temporary `_ = cfg.DefaultTeamAlias` marker added in Phase 3.

### Task 5.4: Unit tests — Deps wiring + handler behavior

**File 1:** `internal/platformapi/auth/sso_test.go` (assumed to exist; if not, create it next to the production file).

Add `TestProvisionUser_UsesDefaultTeamAliasFromDeps`:
1. Build a fake LiteLLM that records every method call (`ListTeamsByAlias` arg, `UserNew.Teams` slice).
2. Construct `auth.Deps{DefaultTeamAlias: "tenant-a", ...}`.
3. Trigger `provisionUser(ctx, deps, "user@example.com")` directly (the function is package-private; reach it via the same package).
4. Assert (a) `ListTeamsByAlias` was called with `"tenant-a"`, (b) on the not-found branch `UserNew.Teams` was `["tenant-a"]`.

Add `TestProvisionUser_DefaultTeamMissing_MessageUsesAlias`:
1. Fake `ListTeamsByAlias` returns empty.
2. `DefaultTeamAlias: "engineering"`.
3. Run provisionUser, capture the returned error.
4. Assert the wrapped error message contains the substring `"engineering"`.

**File 2:** `internal/platformapi/envkeys/handler_test.go` (already exists).

Add `TestCreateHandler_UsesDefaultTeamAliasFromDeps`:
1. Build `envkeys.Deps{DefaultTeamAlias: "qa", LiteLLM: fakeRecordingClient, ...}`.
2. Set up the fake LiteLLM so `UserInfoByEmail` returns 404 (first-time user branch).
3. POST to CreateHandler.
4. Assert recorded `UserNew.Teams == ["qa"]` and recorded `TeamMemberAdd(team_id, ...) == "qa"`.

**File 3:** `cmd/ach/cmd/platform_api_flag_test.go` (added in Phase 3) — extend to assert the value reaches `platformapi.Deps.DefaultTeamAlias`. Since `validatePlatformAPIConfig` returns the validated cfg, the assertion is simply:

```go
t.Setenv("ACH_DEFAULT_TEAM_ALIAS", "tenant-a")
// + all the other required env vars to make validatePlatformAPIConfig succeed
cfg, err := validatePlatformAPIConfig()
require.NoError(t, err)
require.Equal(t, "tenant-a", cfg.DefaultTeamAlias)
```

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/platformapi/auth/...
./scripts/dev.sh make unit-pkg PKG=./internal/platformapi/envkeys/...
./scripts/dev.sh make unit-pkg PKG=./cmd/ach/...
```

**Expected:** all PASS.

**Commit (single commit for Phase 5):**

```
feat(platform-api): TODO §15 — SSO + EnvKey handlers read DefaultTeamAlias from Deps

auth.Deps and envkeys.Deps gain a DefaultTeamAlias field; the SSO
provisionUser path and the EnvKey CreateHandler path now source the
team alias from Deps instead of from package-level string literals.
The cmd/ach/cmd/platform_api.go wiring feeds cfg.DefaultTeamAlias
(from --default-team-alias / ACH_DEFAULT_TEAM_ALIAS) into both.

The hardcoded literal "default" is GONE from internal/litellm/team.go,
internal/platformapi/auth/sso.go, and internal/platformapi/envkeys/
handler.go.

Refs: TODO §15
```

---

## Phase 6 — Helm chart wiring

### Task 6.1: Add `defaultTeamAlias` to values.yaml

**File:** `deploy/helm/ach/values.yaml`

**Step 1:** Insert after the `watchNamespace: ""` line (around line 28-29):

```yaml
# defaultTeamAlias: the LiteLLM team alias the operator bootstraps on
# every successful LiteLLMConnection sync, and every SSO-provisioned
# user is enrolled into. Maps to ACH_DEFAULT_TEAM_ALIAS on BOTH the
# operator and platformApi Deployments — keeping them in lockstep is
# the chart's responsibility. Default "default" matches the historical
# bootstrap topology. Multi-tenant deployers may pick "engineering",
# "tenant-a", etc.; the alias is purely a string identifier.
defaultTeamAlias: "default"
```

### Task 6.2: Wire the env var into both Deployment templates

**Files:**
- `deploy/helm/ach/templates/operator-deployment.yaml`
- `deploy/helm/ach/templates/platform-api-deployment.yaml`

**Step 1:** In each Deployment's container `env:` block (line 71 for operator, line 68 for platform-api), add after the `ACH_NAMESPACE` entry:

```yaml
            - name: ACH_DEFAULT_TEAM_ALIAS
              value: {{ .Values.defaultTeamAlias | quote }}
```

The `| quote` filter ensures even single-token values render as YAML strings (defense against an unquoted boolean-looking alias like `"yes"` that YAML 1.1 would coerce).

### Task 6.3: Helm-template smoke + lint

```bash
./scripts/dev.sh helm lint deploy/helm/ach/
./scripts/dev.sh helm template ach deploy/helm/ach/ \
  --set defaultTeamAlias=engineering \
  | grep -A1 ACH_DEFAULT_TEAM_ALIAS
```

**Expected:**
- `helm lint` exits 0.
- The grep prints the env var TWICE (once on the operator Deployment, once on platform-api), each followed by `value: "engineering"`.

### Task 6.4: Unit test the chart with helm-unittest (if already used) OR with a Go-based template assertion

**Step 1:** Check if the chart already has unit tests.

```bash
ls /home/jcm/Projects/ach/deploy/helm/ach/tests/ 2>&1 || echo "no chart tests yet"
```

**Step 2:** If no chart tests exist, skip — the e2e (Phase 7) covers it. If they do, add an assertion file that:
1. Renders the chart with `defaultTeamAlias: engineering`.
2. Asserts the operator Deployment env contains `ACH_DEFAULT_TEAM_ALIAS=engineering`.
3. Asserts the platform-api Deployment env contains `ACH_DEFAULT_TEAM_ALIAS=engineering`.

**Commit (single commit for Phase 6):**

```
feat(helm): TODO §15 — defaultTeamAlias chart value wires both Deployments

New top-level value `defaultTeamAlias` (default "default") renders
ACH_DEFAULT_TEAM_ALIAS on the operator AND platformApi Deployments,
keeping the two modes in lockstep on a single deployer-controlled
team alias.

Refs: TODO §15
```

---

## Phase 7 — End-to-end validation

### Task 7.1: New e2e Ginkgo case — non-default alias propagates through both Deployments

**File:** `test/e2e/e2e_test.go` (or wherever the suite's "default team" coverage lives — search with `grep -rn 'default team\|EnsureDefaultTeam' test/e2e/`).

**Step 1:** Add a Describe block `Configurable default team alias (TODO §15)` with one It:

```
It("propagates a non-default alias through operator + platform-api Deployments", func() {
	// Helm upgrade with defaultTeamAlias=engineering.
	// Wait for operator + platform-api rollouts.
	// (a) kubectl describe deploy ach-operator | grep ACH_DEFAULT_TEAM_ALIAS → "engineering"
	// (b) kubectl describe deploy ach-platform-api | grep ACH_DEFAULT_TEAM_ALIAS → "engineering"
	// (c) Hit LiteLLM /v2/team/list?team_alias=engineering, assert one team returned (operator bootstrap worked).
	// (d) Tail operator logs, grep -F "default team alias configured" "alias":"engineering"
})
```

Use the existing `make wait-operator` and `make wait-platform-api` blessed targets for readiness — NO naked polling loops.

**Step 2:** Run focused.

```bash
make cluster-keep
./scripts/dev.sh make e2e-focus FOCUS="default team alias configurable"
```

**Expected:** PASS.

**Step 3:** Final clean-room run before commit.

```bash
make cluster-down
./scripts/dev.sh make e2e-full
```

**Expected:** entire suite PASS.

**Commit (single commit for Phase 7):**

```
test(e2e): TODO §15 — assert configurable default team alias end-to-end

Adds an e2e case that deploys with defaultTeamAlias=engineering
and asserts (a) both Deployments carry the env var, (b) the
operator bootstrapped a LiteLLM team with the configured alias,
(c) the operator startup log records the alias.

Refs: TODO §15
```

---

## Phase 8 — Documentation

### Task 8.1: Update `CLAUDE.md` "Common failure modes" with the alias-mismatch trap

**File:** `CLAUDE.md`

**Step 1:** Locate the "Common failure modes" section (after the existing `### ❌ Invalid Postgres / Redis URLs in dev hydration` block).

**Step 2:** Add a new entry:

````markdown
### ❌ Operator and platform-api configured with different default team aliases
```bash
helm upgrade ach … \
  --set operator.extraEnv[0].name=ACH_DEFAULT_TEAM_ALIAS \
  --set operator.extraEnv[0].value=engineering   # operator only — platform-api still "default"
# operator bootstraps team alias="engineering"
# SSO callback looks up team alias="default" → 500 default_team_missing
```
✅ Always set the chart's top-level `defaultTeamAlias` value — it wires
BOTH Deployments to the same alias:
```bash
helm upgrade ach … --set defaultTeamAlias=engineering
```
WHY IT FAILS: The operator-side bootstrap and the SSO/EnvKey enrollment
read the alias from independent process-startup config values. The
chart's top-level `defaultTeamAlias` exists specifically to keep the
two Pods in lockstep; setting `extraEnv` on only one Deployment breaks
that invariant and leaves the SSO path failing with `default_team_missing`
until the other Pod is also reconfigured.
````

### Task 8.2: Update the inline doc comments

No additional Hub §8.1 amendment is needed beyond what already lives in the inline docstrings (this plan added them in Phases 2, 4, 5). The `docs/` mkdocs site auto-renders the CRD reference, not the cobra flag set — there is no public-facing markdown file claiming "the alias is always `default`" that needs editing. Confirm with:

```bash
grep -rn '"default"' docs/ | grep -v api-reference/ | grep -v -E '\.(yaml|json):'
```

**Expected:** zero matches in narrative `.md` content (the api-reference/ tree is auto-generated; ignore). If the grep returns hits, edit each to mention "the configured team alias (default `default`)".

### Task 8.3: Append to CHANGELOG.md

**File:** `CHANGELOG.md`

Under the `Unreleased` heading add an entry:

```
### Added
- Operator + platform-api now accept `--default-team-alias` (env mirror
  `ACH_DEFAULT_TEAM_ALIAS`, default `"default"`). The Helm chart's new
  top-level `defaultTeamAlias` value wires BOTH the operator and
  platform-api Deployments to the same alias so the operator-bootstrapped
  team and the SSO-callback enrollment converge on a single
  deployer-controlled identity. (TODO §15)
```

**Commit (single commit for Phase 8):**

```
docs(claude-md,changelog): TODO §15 — default-team-alias config notes

Documents the new --default-team-alias flag, the Helm chart value, and
the lockstep invariant between the operator and platform-api
Deployments (added as a "Common failure modes" entry).

Refs: TODO §15
```

---

## Phase 9 — Final gate

### Task 9.1: Run the full pre-commit / pre-push battery

```bash
cd /home/jcm/Projects/ach

# Fast iteration loop:
./scripts/dev.sh make unit
./scripts/dev.sh make lint
./scripts/dev.sh make envtest-run

# Security + supply chain:
./scripts/dev.sh make security

# Final publication gate:
make pre-push
```

**Expected:** 15/15 PASS on `make pre-push`. Any new govulncheck advisories MUST be triaged against `references/security/govulncheck-acknowledged.md` per the CLAUDE.md gate.

### Task 9.2: Self-audit against TODO §15 acceptance criteria

Re-read the source TODO §15 acceptance block:

> Acceptance: operator started with `--default-team-alias=engineering` (a) creates team with that alias on first startup (idempotent), (b) enrolls every SSO-provisioned user into it, (c) makes value visible in `kubectl describe deploy ach-operator` env.

Confirm:
- (a) Covered by the unit test in Task 2.3 + the e2e in Task 7.1 step c.
- (b) Covered by the unit test in Task 5.4 (auth Deps) + the e2e e2e covers operator-side; the SSO callback assertion is inherent in the unit test on `provisionUser`.
- (c) Covered by the e2e in Task 7.1 step a + b.

### Task 9.3: Push and open PR

```bash
git push origin feat/default-team-alias-configurable
gh pr create --title "feat: TODO §15 — configurable LiteLLM default-team alias" \
  --body "$(cat <<'EOF'
## Summary
- Adds `--default-team-alias` (env mirror `ACH_DEFAULT_TEAM_ALIAS`) to the
  `ach operator` and `ach platform-api` subcommands.
- Widens the LiteLLM Client interface: `EnsureDefaultTeam(ctx, alias string)`.
- Threads the alias through `LiteLLMConnectionReconciler`,
  `internal/platformapi/auth/Deps`, and `internal/platformapi/envkeys/Deps`.
- Helm chart gains a top-level `defaultTeamAlias` value (default `"default"`)
  that wires the env var into BOTH the operator and platform-api Deployments.

## Test plan
- [x] `./scripts/dev.sh make unit` PASS — new unit tests for RESTClient, provisionUser, envkeys CreateHandler.
- [x] `./scripts/dev.sh make envtest-run` PASS — new envtest sub-test asserts the reconciler passes its configured alias.
- [x] `./scripts/dev.sh make e2e-focus FOCUS="default team alias configurable"` PASS — deploys with `defaultTeamAlias=engineering`, asserts env on both Deployments + operator-bootstrapped team in LiteLLM.
- [x] `make pre-push` PASS (15/15).

Closes TODO §15.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

**Commit (Phase 9):** none — the work is in Phases 2-8; Phase 9 is verification + push only.

---

## Summary

| Phase | Tasks | Commits | What lands |
|---|---|---|---|
| 0 | 1 | 0 | branch setup |
| 1 | 1 | 0 | discovery (no code change) |
| 2 | 3 | 1 | `feat(litellm)`: widen Client.EnsureDefaultTeam signature + all implementor/test-fake updates + RESTClient alias unit test |
| 3 | 3 | 1 | `feat(operator,platform-api)`: cobra flag + env mirror + flag-precedence unit test |
| 4 | 3 | 1 | `feat(operator)`: LiteLLMConnectionReconciler.DefaultTeamAlias wiring + envtest assertion |
| 5 | 4 | 1 | `feat(platform-api)`: auth.Deps + envkeys.Deps DefaultTeamAlias + handler unit tests |
| 6 | 4 | 1 | `feat(helm)`: chart value + both Deployment templates |
| 7 | 1 | 1 | `test(e2e)`: deploy-with-non-default-alias case |
| 8 | 3 | 1 | `docs(claude-md,changelog)`: CLAUDE.md failure mode + CHANGELOG entry |
| 9 | 3 | 0 | final gate + PR open |

**Total: 26 tasks across 10 phases (0-9); 7 commits.**

Each phase's commit is independently reviewable and revertable. Phases 2, 3, 6, 8 carry zero behavior change (refactor, scaffolding, chart wiring, docs). Phases 4, 5, 7 are the load-bearing behavior changes.
