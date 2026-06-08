# Phase 2.3 — `env` command reorg (clean break) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make `env` the unambiguous noun for the *remote/CR-defined* object by moving the three top-level workspace commands under it, as a **clean break with no aliases**:

| Before (top-level)            | After (under `env`)              |
|-------------------------------|----------------------------------|
| `ach-cli hydrate --environment <n>` | `ach-cli env hydrate <n>`  |
| `ach-cli list`                | `ach-cli env status`             |
| `ach-cli uninstall --environment <n>` | `ach-cli env uninstall <n>` |

Two ratified surface changes accompany the move (user-confirmed 2026-06-08):
1. **`--environment` flag → positional `<name>`** on `env hydrate` and `env uninstall` (`env status` keeps `--environment` as an optional flag — it is a lister, not a per-env action).
2. **`--platform` flag → `--target`** on all three commands, matching `plugin`/`skill` (Phase 2.2). No alias.

This is the final phase of the local-package-manager plan. It is **CLI-surface only** (no operator/api/server change) but it **fires the local e2e gate** because it rewrites the `ach-cli hydrate` invocations the e2e suite shells out to.

**Tech Stack:** Go (`github.com/ackstorm/ach`). Toolchain: `make`/`./scripts/dev.sh`. SPDX every file. Conventional commits.

**Boundary invariant (assert every task):** `go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|controller-runtime'` empty.

**Behavior-preservation invariant:** the engine/runtime behavior of hydrate/status/uninstall is **unchanged** — only the command path (`env` parent), the env identifier (positional vs flag), and the platform flag name (`--target`) change. The `runHydrate`/list/uninstall RunE bodies and `internal/cli/hydrate` engine are untouched except for reading `args[0]` instead of `flagEnvironment` and the renamed flag var binding.

---

## Reused / affected surface (verified)

- `cmd/ach-cli/cmd/env.go` — `newEnvCmd()` parent; currently
  `parent.AddCommand(newEnvListCmd(), newEnvDescribeCmd())`. The three reorged
  children get added here. `init(){ rootCmd.AddCommand(newEnvCmd()) }` stays.
- `cmd/ach-cli/cmd/hydrate.go` — `newHydrateCmd()`; `Use: "hydrate"`;
  flags `--environment` (line ~243) + `--platform` (line ~275); `init()` at
  line ~940 does `rootCmd.AddCommand(newHydrateCmd())`. RunE reads
  `flagEnvironment`/`flagPlatform` into `hydrateInputs{environment, platform}`.
- `cmd/ach-cli/cmd/list.go` — `newListCmd()`; `Use: "list"`; flags
  `--environment` + `--platform`; `init()` line ~212 `rootCmd.AddCommand`.
- `cmd/ach-cli/cmd/uninstall.go` — `newUninstallCmd()`; `Use: "uninstall"`;
  flags `--environment` + `--platform`; `init()` line ~298 `rootCmd.AddCommand`.
- Unit tests: `hydrate_test.go`, `list_test.go`, `uninstall_test.go`,
  `env_test.go` (+ `helpers_test.go`, `synthetic_guard_test.go` may reference
  the commands).
- e2e call sites (binary invocations, **35** across 6 files): subcommand arg
  `"hydrate"` → `"env", "hydrate"`; **37** `"--environment", X` pairs →
  positional `X`; **34** `"--platform"` → `"--target"`. Files:
  `cli_login_hydrate_test.go`, `cli_hydrate_engine_test.go`,
  `cli_hydrate_allplatforms_test.go`, `phase7_helpers_test.go`,
  `projection_lifecycle_test.go`, `projection_idempotence_test.go`.
- Live docs (doc-hygiene — update in this phase): `CLAUDE.md`, `README.md`,
  `examples/README.md`, `references/makefile.md`, `references/troubleshooting.md`.
  **Historical** dated plan docs under `docs/plans/**` and
  `docs/superpowers/plans/**` are point-in-time records → **NOT** updated.

---

## Task 1: CLI reorg in package `cmd` (atomic — single coherent change)

The three commands + their parent wiring + their unit tests must change together
or the `cmd` package won't compile (removing a root `init()` registration while
the `env.go` parent doesn't yet add the child leaves the command unregistered;
both halves land in one task).

- [ ] **hydrate.go** → `env hydrate <name>`:
  - `Use: "hydrate <name>"`, `Args: cobra.MaximumNArgs(1)` (positional optional —
    pk- requires it, ek- optional; the existing D-12 "missing --environment"
    gate in `runHydrate` already errors when empty + pk-, so keep that gate but
    feed it from `args`).
  - RunE: `env := ""; if len(args) > 0 { env = args[0] }`; pass `environment: env`.
  - Delete the `--environment` flag registration. Rename the `--platform` flag
    to `--target` (var stays `flagPlatform` or rename to `flagTarget` for
    clarity; bind to `"target"`). Update the `Long:` help: replace the
    `--environment` paragraph with positional `<name>` wording and
    `--platform` → `--target`.
  - Remove `init(){ rootCmd.AddCommand(newHydrateCmd()) }` (registration moves to
    env.go Task 1 wiring).
- [ ] **list.go** → `env status`:
  - `Use: "status"`, `Short` updated. Keep `--environment` (optional lister
    filter) but rename `--platform` → `--target`. Rename `newListCmd` →
    `newEnvStatusCmd` for clarity (optional but preferred — update its test).
  - Remove its root `init()` registration.
- [ ] **uninstall.go** → `env uninstall <name>`:
  - `Use: "uninstall <name>"`, `Args: cobra.MaximumNArgs(1)`; RunE reads
    `args[0]` into the environment input (same pattern as hydrate). Delete
    `--environment` flag; rename `--platform` → `--target`.
  - Remove its root `init()` registration.
- [ ] **env.go** wiring: `parent.AddCommand(newEnvListCmd(), newEnvDescribeCmd(),
  newHydrateCmd(), newEnvStatusCmd(), newUninstallCmd())`.
- [ ] **Unit tests** (`hydrate_test.go`, `list_test.go`, `uninstall_test.go`,
  `env_test.go`): update every `SetArgs` to the new surface
  (`--environment X` → positional `X`; `--platform` → `--target`; if a test
  builds the command via `newHydrateCmd()` directly and calls Execute, it still
  works — just fix the args). Add an `env_test.go` assertion that
  `env hydrate`/`env status`/`env uninstall` resolve under the `env` parent and
  that `rootCmd` no longer has top-level `hydrate`/`list`/`uninstall`.
- [ ] **Gate:** `./scripts/dev.sh go build ./cmd/ach-cli` compiles;
  `make test-unit-pkg PKG=./cmd/ach-cli/...` green; `make qa-lint-changed`
  green; boundary grep empty.

**Commit:** `refactor(cli)!: move hydrate/list/uninstall under 'env' (env hydrate/status/uninstall), positional <name>, --platform→--target`

---

## Task 2: e2e call-site sweep (mechanical)

Rewrite the binary invocations in the 6 e2e files. Pattern per invocation:
- arg slice element `"hydrate"` → two elements `"env", "hydrate"`.
- the pair `"--environment", <X>` that accompanies a hydrate/uninstall call →
  drop `"--environment"`, keep `<X>` as the trailing positional (place it after
  the `"env", "hydrate"` tokens, respecting cobra positional rules — positionals
  may follow flags, so simplest is to append `<X>` at the end of the arg slice
  OR right after `"hydrate"`; verify the chosen position parses).
- every `"--platform"` → `"--target"`.

- [ ] Sweep `cli_hydrate_engine_test.go` (27 hydrate calls — the bulk).
- [ ] Sweep `cli_hydrate_allplatforms_test.go`, `cli_login_hydrate_test.go`,
  `phase7_helpers_test.go`, `projection_lifecycle_test.go`,
  `projection_idempotence_test.go`.
- [ ] If any e2e **helper** wraps the arg construction (e.g. a
  `runHydrate(t, env, platform, …)` builder), fix it once at the source rather
  than each call site.
- [ ] **Gate:** `./scripts/dev.sh go vet -tags e2e ./test/e2e/...` (or
  `go build -tags e2e ./test/e2e/...`) compiles. No runtime gate here — the live
  run is Task 4.

**Commit:** `test(e2e): repoint CLI invocations to 'env hydrate <name>' + --target`

---

## Task 3: Live-doc update (doc-hygiene, same change)

- [ ] Update `CLAUDE.md` (User CLI surface lines: the `ach-cli` subcommand list
  mentions `hydrate`; the `ach-cli hydrate` critical-path bullets; any
  `--platform`), `README.md`, `examples/README.md` (the login+hydrate demo
  invocations), `references/makefile.md` (if it shows `ach-cli hydrate`),
  `references/troubleshooting.md` (the "Hydrate output != examples/hydrate.json"
  entry + any `ach-cli hydrate` command). Replace `ach-cli hydrate --environment X`
  → `ach-cli env hydrate X`; `ach-cli list` → `ach-cli env status`;
  `ach-cli uninstall …` → `ach-cli env uninstall …`; `--platform` → `--target`.
- [ ] **Do NOT** touch dated historical plan docs under `docs/plans/**` or
  `docs/superpowers/plans/**` (point-in-time records).
- [ ] **Gate:** none (docs). `grep -rn 'ach-cli hydrate\|ach-cli list\b\|ach-cli uninstall\|--platform'`
  over the live-doc set returns nothing.

**Commit:** `docs: repoint CLI references to 'env hydrate/status/uninstall' + --target`

---

## Task 4: Full gate + plan-doc record

- [ ] `make test-unit` green.
- [ ] `make qa-lint` (full sweep) green.
- [ ] Boundary: `go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|controller-runtime'` empty.
- [ ] `make cluster-down && make e2e-full` — green **except** the pre-existing,
  separately-tracked `TestPhase6CLI/hydrate_golden_diff` (stale
  `examples/hydrate.json`); any OTHER e2e red is a real regression from the
  sweep (most likely a mis-placed positional arg) → fix and re-run.
- [ ] Commit this plan doc: `docs(plans): record Phase 2.3 (env command reorg) plan`.

---

## Verification

- Unit: the four `cmd` tests pass with the new surface; `env_test.go` proves the
  reparent + the absence of the old top-level names.
- e2e: `make e2e-full` green modulo the known golden-diff. The
  `cli_hydrate_engine_test.go` Phase 7 engine scenarios are the strongest proof
  the positional/`--target` rewrite is correct (27 real hydrate runs).
- Boundary + lint + SPDX gates pass.

## Out of scope

Any engine/runtime behavior change; re-capturing the stale
`examples/hydrate.json` golden (separate follow-up); deprecation aliases (clean
break, none); changes to `plugin`/`skill`/`repo` (Phase 2.1/2.2, already merged).
