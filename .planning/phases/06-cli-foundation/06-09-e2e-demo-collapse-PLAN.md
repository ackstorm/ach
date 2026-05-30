---
phase: 06-cli-foundation
plan: 09
type: execute
wave: 3
depends_on:
  - 06-01-cli-shared-internals
  - 06-02-server-device-code-endpoints
  - 06-03-ach-login-whoami-logout
  - 06-04-ach-config-env
  - 06-05-ach-env-keys-d07-deviation
  - 06-06-ach-hydrate
  - 06-07-synthetic-mode-enforcement
  - 06-08-ach-admin
files_modified:
  - test/e2e/cli_login_hydrate_test.go
  - test/e2e/phase6_helpers_test.go
  - examples/hydrate-demo.sh
  - examples/README.md
  - CLAUDE.md
  - README.md
autonomous: false
requirements:
  - CLI-01
  - CLI-03
  - CLI-05
  - CLI-06
  - CLI-11

must_haves:
  truths:
    - "test/e2e/cli_login_hydrate_test.go exists with //go:build e2e + SPDX header, drives `ach login` + `ach hydrate --environment demo` + `ach env list` + `ach env-keys create` + `ach whoami --verify` subtests against the kept kind cluster (D-18)"
    - "phase6SuiteGuard mirrors phase3/4/5 SuiteGuard discipline — skips cleanly when ACH_E2E_PHASE6 unset OR Platform-API not reachable OR ./bin/ach not built"
    - "Login subtest uses a test-only bypass (env-var-injected pk_ OR a build-tag-gated --token flag) per D-18 — planner picks; see Task 1 decision"
    - "Hydrate subtest exec's `./bin/ach hydrate --environment demo` and byte-for-byte diffs the stdout against examples/hydrate.json (the golden) — assertion is `bytes.Equal(out, phase6NormalizeHydrate(golden, clusterHost))`; helper `phase6NormalizeHydrate(golden []byte, clusterHost string) []byte` substitutes the golden's `ach.local.test` host with the live cluster's externally-visible host before compare. Decision locked NOW per W4 — NOT deferred to SUMMARY (D-17 + D-18)"
    - "Whoami --verify subtest asserts exit 0 against a live pk_; ek_ subtest (after env-keys create) asserts exit 0 with POST /platform/hydrate {} (CLI-11 asymmetric verify)"
    - "examples/hydrate-demo.sh DELETED (`git rm`) in the same commit as the e2e test addition (D-17)"
    - "examples/README.md updated to remove the hydrate-demo.sh row + add a new entry pointing at `ach login` + `ach hydrate --environment demo > hydrate.json` workflow (CLAUDE.md §Documentation hygiene — same commit)"
    - "CLAUDE.md lines 126, 135, 151 (hydrate-demo references) updated to reflect the new CLI-driven workflow (same commit — CLAUDE.md §Documentation hygiene)"
    - "README.md gains a Quick Start section showing `ach login` + `ach hydrate --environment demo` (if no equivalent section exists yet) — keeps the headline demo discoverable to first-time readers"
    - "Human-verify checkpoint MUST run the full demo against the kept kind cluster before merge — visual confirmation that the byte-for-byte golden diff holds AND the new CLAUDE.md/README.md instructions are accurate"
  artifacts:
    - path: "test/e2e/cli_login_hydrate_test.go"
      provides: "TestPhase6CLI umbrella with login/hydrate/env-list/env-keys-create/whoami-verify subtests"
      min_lines: 200
    - path: "test/e2e/phase6_helpers_test.go"
      provides: "phase6SuiteGuard + exec helpers + golden-diff helper"
      min_lines: 100
  key_links:
    - from: "test/e2e/cli_login_hydrate_test.go"
      to: "examples/hydrate.json"
      via: "bytes.Equal(exec.Command('./bin/ach','hydrate','--environment','demo').Output(), os.ReadFile('examples/hydrate.json'))"
      pattern: "examples/hydrate.json"
    - from: "examples/README.md"
      to: "cmd/ach/cmd/login.go + cmd/ach/cmd/hydrate.go"
      via: "Documentation pointer — replace hydrate-demo.sh row with `ach login` + `ach hydrate --environment demo` workflow"
      pattern: "ach login.*ach hydrate"
---

<objective>
Close Phase 6 with the e2e umbrella that proves the headline demo
(`ach login` → `ach hydrate --environment demo` reproduces
`examples/hydrate.json` byte-for-byte) AND the demo collapse that
deletes the 139-line `examples/hydrate-demo.sh` stand-in (D-17) plus
the documentation hygiene updates to CLAUDE.md, examples/README.md,
and README.md (all in the SAME commit per CLAUDE.md §"Documentation
hygiene").

This is W3-P3 per the wave structure in `06-CONTEXT.md` `<domain>`.
Depends on every prior Phase 6 plan because the e2e suite exercises
the full subcommand surface: login (06-03), hydrate (06-06), env list
(06-04), env-keys create (06-05), whoami --verify (06-03). The
synthetic-mode plan (06-07) is also a dependency because the e2e
test relies on every subcommand having consistent synthetic-mode
behavior (the test itself does NOT use synthetic mode — it uses a
real config file under XDG_CONFIG_HOME — but bug-free CLI requires
the synthetic gate to be in place).

This plan has ONE non-autonomous checkpoint at the end: a
`checkpoint:human-verify` that asks the user to run the full demo
manually against the kept kind cluster before merge. This is required
because (a) the byte-for-byte golden diff is the headline demo and
must hold; (b) CLAUDE.md is updated in the same commit and the user
must confirm the new instructions actually work.

Purpose: collapses the demo-driver shell-script complexity into a
single CLI invocation; gives Phase 6 its live verification gate
(matching the Phase 3/4/5 invariants-suite pattern); converts the
demo from "go read 139 lines of bash + jq" to "ach login && ach
hydrate".

Output: 1 new e2e test file + 1 helpers file, 1 deleted file
(hydrate-demo.sh), 3 modified docs (examples/README.md, CLAUDE.md,
README.md). ~300 LOC test + ~100 LOC helpers + small doc diffs.
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
@README.md
@examples/README.md
@examples/hydrate.json
@examples/hydrate-demo.sh
@test/e2e/phase3_invariants_test.go
@test/e2e/phase3_helpers_test.go
@test/e2e/phase5_invariants_test.go
@test/e2e/phase5_helpers_test.go
@.planning/phases/06-cli-foundation/06-03-ach-login-whoami-logout-PLAN.md
@.planning/phases/06-cli-foundation/06-06-ach-hydrate-PLAN.md
</context>

<tasks>

<task type="auto" tdd="false">
  <name>Task 1: Author test/e2e/cli_login_hydrate_test.go + phase6_helpers_test.go</name>
  <files>
    test/e2e/cli_login_hydrate_test.go
    test/e2e/phase6_helpers_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-18" (test against `make cluster-keep`, byte-for-byte diff vs examples/hydrate.json) + Claude's Discretion note (env-var-injected pk_ OR build-tag-gated --token flag — planner picks)
    - 06-PATTERNS.md §"Pattern P11" lines 587-643 (entire e2e test shape — //go:build e2e + SPDX header + phase6SuiteGuard + t.Run subtests + exec.CommandContext)
    - test/e2e/phase3_invariants_test.go lines 1-65 (file-header + TestMain pattern — copy verbatim, swap phase3 → phase6)
    - test/e2e/phase3_helpers_test.go lines 1-100 (phase3SuiteGuard implementation — mirror for phase6)
    - test/e2e/phase5_helpers_test.go lines 1-100 (phase5SuiteGuard variant — cleaner kubectl-based skip logic for newer phases)
    - examples/hydrate.json (the golden — the test diffs against this exact byte sequence)
    - examples/hydrate-demo.sh (whole file — understand what the test must replicate WITHOUT bash)
    - cmd/ach/cmd/login.go (built earlier in this phase) — confirm the test-only bypass mechanism the login plan landed (env-var injected pk_ OR --token build-tag flag); the planner of THIS plan picks ONE of those two paths and documents the choice in the SUMMARY
    - CLAUDE.md §"E2E debug loop" — the `make cluster-keep` / `make e2e-focus FOCUS=...` iteration pattern
    - go.mod (confirm no new third-party deps needed; stdlib `testing`, `os/exec`, `bytes`, `encoding/json` is enough)
  </read_first>
  <action>
    Author `test/e2e/cli_login_hydrate_test.go` mirroring Pattern P11 exactly:

    1. File header:
       ```
       //go:build e2e

       // SPDX-License-Identifier: Apache-2.0

       // Phase 6 CLI e2e suite. Drives `ach login` + `ach hydrate
       // --environment demo` against the kept kind cluster (per
       // CLAUDE.md "E2E debug loop" — `make cluster-keep`), then
       // byte-for-byte diffs vs examples/hydrate.json (D-17, D-18).
       //
       // Activation: ./scripts/dev.sh make e2e-focus FOCUS=TestPhase6CLI
       //
       // Engineer-pending until kind cluster is up + Phase 3-5 services
       // are deployed; phase6SuiteGuard skips cleanly otherwise.

       package e2e
       ```

    2. `TestPhase6CLI(t *testing.T)` umbrella with five `t.Run` subtests in this order:
       - `login_device_code` → testPhase6Login
       - `whoami_verify_pk` → testPhase6WhoamiVerifyPk (must run after login)
       - `env_list` → testPhase6EnvList
       - `env_keys_create` → testPhase6EnvKeysCreate
       - `hydrate_golden_diff` → testPhase6HydrateGoldenDiff (the headline assertion)

       Order matters: login writes pk_ to config; whoami verifies it; env list confirms the deployment is reachable; env-keys create produces an ek_ that whoami --verify can additionally exercise; hydrate emits the JSON the golden-diff asserts.

    3. Each subtest function starts with `t.Helper(); phase6SuiteGuard(t)` (skip when prerequisites missing).

    4. **Login subtest** — picks ONE of the two D-18 bypass mechanisms (record the choice in the SUMMARY):
       - **Option A (recommended): env-var-injected pk_.** The test does NOT shell out to `ach login`; instead it writes a synthetic config file under a temp `XDG_CONFIG_HOME` directory: `default: demo` + `deployments.demo.url: <kind-platform-api-url>` + `deployments.demo.pk: <pk_ minted by direct-injection into Postgres>`. The "direct-injection" path mints a pk_ via the same code path as Phase 3 e2e (call out to a phase3 helper if one exists; else POST to `/platform/auth/cli/init` + simulate the Dex callback). This avoids the real interactive prompt entirely.
       - **Option B: build-tag-gated --token flag.** Adds `//go:build e2e_login_bypass` to a new file `cmd/ach/cmd/login_bypass.go` exposing a hidden `--token pk_...` flag that skips the device-code dance. Test exec's `ach login --base-url <url> --token <pk_>`.
       - Planner of THIS plan PICKS Option A as the default (simpler — no production-code surface change). Document the decision in the SUMMARY; if Option B is chosen, also implement the new login_bypass.go file and call it out in this plan's files_modified list.

    5. **Whoami subtest** uses `exec.CommandContext(t.Context(), "./bin/ach", "whoami", "--verify")` with `XDG_CONFIG_HOME=<tempdir>` env. Asserts exit code 0. Capture stdout — must include the deployment name AND a masked pk_ tail (e.g. `pk_****` literal substring, no trailing dot allowed since it would mask the last 4 chars).

    6. **Env list subtest** exec's `ach env list`. Asserts exit 0 + stdout contains the row for environment `demo` (the kind cluster's fixture environment from `examples/04-environment-demo.yaml`).

    7. **Env-keys create subtest** exec's `ach env-keys create --environment demo --name e2e-test-key`. Asserts exit 0 + stdout includes the freshly-minted `ek_` plaintext (the one-time return per CLI-04). Captures the ek_ plaintext for the next subtest.

    8. **Whoami --verify with ek_ subtest** (optional add-on to the env-keys subtest OR its own subtest): exec's `ach whoami --verify --env-key e2e-test-key`. Asserts exit 0 (the ek_ resolves to POST /platform/hydrate {} per CLI-11).

    9. **Hydrate golden diff subtest** (the headline assertion):
       ```
       func testPhase6HydrateGoldenDiff(t *testing.T) {
           t.Helper()
           phase6SuiteGuard(t)
           out, err := exec.CommandContext(t.Context(), "./bin/ach",
               "hydrate", "--environment", "demo").Output()
           if err != nil { t.Fatalf("ach hydrate: %v", err) }
           golden, err := os.ReadFile("../../examples/hydrate.json")
           if err != nil { t.Fatalf("read golden: %v", err) }
           clusterHost := phase6PlatformAPIHost(t)  // helper: discover externally-visible host (e.g. "ach.local.test" in standard fixture; whatever the kept cluster exposes)
           expected := phase6NormalizeHydrate(golden, clusterHost)
           if !bytes.Equal(out, expected) {
               t.Errorf("hydrate output != golden (normalized for clusterHost=%s):\nwant=%s\ngot=%s", clusterHost, expected, out)
           }
       }
       ```
       Per W4 — the host-substitution decision is LOCKED here, not deferred to SUMMARY. The byte-for-byte assertion compares against the NORMALIZED golden (with `ach.local.test` rewritten to the live cluster's host). The 06-06 hydrate command emits the response body verbatim via `httpclient.Client.DoRaw + io.Copy` (no re-encoding); the only intentional transform happens in the test helper. The CLAUDE.md "Common failure modes" entry added by Task 2 documents this gotcha unconditionally.

       Helper contract (lands in `test/e2e/phase6_helpers_test.go` — see Task 1 helpers section):
       - `func phase6NormalizeHydrate(golden []byte, clusterHost string) []byte` — replaces every `ach.local.test` occurrence in the golden with `clusterHost`. Idempotent when clusterHost == "ach.local.test". Documented in the file's package doc.
       - `func phase6PlatformAPIHost(t *testing.T) string` — discovers the externally-visible platform-api host (mirrors `phase6PlatformAPIURL` but returns host only, no scheme).

    Author `test/e2e/phase6_helpers_test.go` mirroring Pattern P11 + the phase3 helpers style:

    1. `//go:build e2e` + SPDX header + package `e2e`.

    2. `func phase6SuiteGuard(t *testing.T)`:
       - Skip when `ACH_E2E_PHASE6` env var is unset (engineer-pending opt-in).
       - Skip when `./bin/ach` binary doesn't exist OR isn't executable (`os.Stat`).
       - Skip when `kubectl get deploy ach-platform-api -n ach-system` fails (cluster not up OR platform-api not deployed).
       - Skip when `examples/hydrate.json` doesn't exist (working dir issue).
       - Use `t.Skipf("phase6 prereqs: %s", reason)` with a clear reason string mirroring phase3SuiteGuard.

    3. Helper `phase6KubectlCmd(args ...string) *exec.Cmd` — returns an `exec.Cmd` wrapping `kubectl` with the kept-cluster kubeconfig path baked in (mirror phase3 + phase5 patterns).

    4. Helper `phase6PlatformAPIURL(t *testing.T) string` — discovers the externally-visible platform-api URL (port-forward, ingress, or NodePort) and returns it. Mirror the existing phase3 helper if one exists; else implement against the cluster's documented URL.

    5. Helper `phase6MintPkDirectInjection(t *testing.T, ctx context.Context) string` — mints a pk_ via the same code path as phase3 e2e tests (POST /platform/auth/cli/init + simulate Dex callback OR direct Postgres write — pick the same approach as phase3). Used by the login subtest under Option A.

    6. Helper `phase6BuildBinary(t *testing.T)` — runs `./scripts/dev.sh make build` if `./bin/ach` is stale (`os.Stat` mtime older than cmd/ach/**/*.go). Else no-op. Saves dev-loop iteration time.

    7. Helper `phase6PlatformAPIHost(t *testing.T) string` — returns the externally-visible host (without scheme) for the kept cluster's platform-api. Standard fixture: `"ach.local.test"`. Mirrors `phase6PlatformAPIURL` (`https://<host>`) but returns the host only.

    8. Helper `phase6NormalizeHydrate(golden []byte, clusterHost string) []byte` — per W4: substitutes every `ach.local.test` occurrence in the golden with the supplied `clusterHost` and returns the rewritten bytes. Implementation: `bytes.ReplaceAll(golden, []byte("ach.local.test"), []byte(clusterHost))`. Idempotent when clusterHost is `"ach.local.test"`. The exported public contract: the test never compares against the raw golden; always against the normalized form.

    SPDX header on every new file. Stdlib `testing` only — no Ginkgo.

    Run `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...` to confirm the //go:build e2e tag is respected and no compile errors leak in.
  </action>
  <verify>
    <automated>./scripts/dev.sh go vet -tags=e2e ./test/e2e/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go vet -tags=e2e ./test/e2e/...` exits 0 (the new test file compiles under the e2e build tag).
    - File `test/e2e/cli_login_hydrate_test.go` exists and `head -1` returns `//go:build e2e` (line 1 of every e2e file per Pattern P11).
    - File `test/e2e/phase6_helpers_test.go` exists with `//go:build e2e` + SPDX as lines 1-3.
    - Source assertion: `grep -c "func TestPhase6CLI" test/e2e/cli_login_hydrate_test.go` returns 1.
    - Source assertion: `grep -c "t.Run(" test/e2e/cli_login_hydrate_test.go` returns ≥ 5 (one per subtest).
    - Source assertion: `grep -c "examples/hydrate.json" test/e2e/cli_login_hydrate_test.go` returns ≥ 1 (golden file path referenced).
    - Source assertion: `grep -c "bytes.Equal" test/e2e/cli_login_hydrate_test.go` returns ≥ 1 (byte-for-byte assertion).
    - Source assertion (per W4): `grep -c "phase6NormalizeHydrate" test/e2e/{cli_login_hydrate_test.go,phase6_helpers_test.go} | awk -F: '{s+=$2} END {print s}'` returns ≥ 2 (helper defined + called by hydrate subtest).
    - Source assertion (per W4): `grep -c "phase6PlatformAPIHost" test/e2e/phase6_helpers_test.go` returns ≥ 1 (host-discovery helper defined).
    - Source assertion: `grep -c "phase6SuiteGuard" test/e2e/{cli_login_hydrate_test.go,phase6_helpers_test.go} | awk -F: '{s+=$2} END {print s}'` returns ≥ 6 (one Guard call per subtest + the function def).
    - When `ACH_E2E_PHASE6` is unset OR `./bin/ach` is absent, running `./scripts/dev.sh make e2e-focus FOCUS=TestPhase6CLI` reports SKIP for every subtest (engineer-pending posture mirrors phase3 — no failures from missing infra).
    - SPDX header line 1: `head -1` on both new files matches `//go:build e2e` (NOT the SPDX comment — SPDX is line 3 in e2e files per Pattern P11).
    - SPDX header line 3: `sed -n '3p' test/e2e/{cli_login_hydrate_test.go,phase6_helpers_test.go}` matches `Apache-2.0`.
  </acceptance_criteria>
  <done>
    e2e umbrella + helpers compile under //go:build e2e; phase6SuiteGuard skips cleanly when prereqs absent; the byte-for-byte golden-diff invariant is encoded.
  </done>
</task>

<task type="auto" tdd="false">
  <name>Task 2: Delete examples/hydrate-demo.sh + sync docs (CLAUDE.md, examples/README.md, README.md)</name>
  <files>
    examples/hydrate-demo.sh
    examples/README.md
    CLAUDE.md
    README.md
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-17" (delete examples/hydrate-demo.sh in W3; `ach login` + `ach hydrate --environment demo > hydrate.json` is the replacement workflow)
    - 06-PATTERNS.md §"Modification Hotspots" rows for examples/hydrate-demo.sh (DELETE), examples/README.md, README.md, CLAUDE.md (Common failure modes — but the current CLAUDE.md does NOT have hydrate-demo.sh in a Common failure modes section; verify before writing the edit)
    - CLAUDE.md §"Documentation hygiene — keep this file and `docs/` in sync with the code" (lines 5-25 — the SAME-commit rule for code+doc changes; this plan is the canonical example)
    - examples/hydrate-demo.sh (read whole file once — confirm what is being replaced)
    - examples/README.md (read whole file — confirm the row to replace and any other references)
    - CLAUDE.md lines 126, 135, 151 (the three current hydrate-demo references — `examples/` tree comment, `hydrate-demo.sh` line in the layout, and the MANDATORY Reading Table row pointing at examples/README.md)
    - README.md (confirm whether a Quick Start section exists; if absent, add one)
    - Output of `grep -rn "hydrate-demo" --include="*.md" --include="*.sh" /home/jcm/Projects/ach/ 2>/dev/null` BEFORE the edit to enumerate every reference that must be updated
  </read_first>
  <action>
    Documentation hygiene mandates ALL of these changes ship in ONE commit. Sequence the edits in this task before invoking git:

    1. **Enumerate references** — run `grep -rn "hydrate-demo" --include="*.md" --include="*.sh" .` from repo root; the output is the canonical edit list. Confirm it matches the three known hits (`examples/README.md` lines 19 + 31, `CLAUDE.md` lines 126 + 135 + 151) plus the file itself. If new references have appeared since CONTEXT.md was written, add them to the edit list.

    2. **Delete `examples/hydrate-demo.sh`** via `git rm examples/hydrate-demo.sh`. The replacement workflow is the new CLI flow.

    3. **Update `examples/README.md`**:
       - Remove the `hydrate-demo.sh` row from the file-summary table (line 19 currently).
       - Remove the `bash examples/hydrate-demo.sh` invocation example (line 31 currently).
       - Add a new section titled `## End-to-end demo` (or merge into the existing intro) explaining the replacement:
         ```
         End-to-end demo (replaces the deleted `hydrate-demo.sh`):

             ach login                                      # device-code SSO
             ach hydrate --environment demo > hydrate.json  # POST /platform/hydrate

         The output should match examples/hydrate.json byte-for-byte
         when run against the standard kind+Helm fixture cluster.
         The e2e test test/e2e/cli_login_hydrate_test.go asserts this
         invariant automatically.
         ```

    4. **Update `CLAUDE.md`**:
       - Line 126 (`├── examples/                ← runnable CR fixtures + hydrate-demo driver`):
         change to `├── examples/                ← runnable CR fixtures + golden hydrate.json` (drop the "driver" word and the hydrate-demo reference).
       - Line 135 (`│   ├── hydrate-demo.sh            Stand-in for ach login + ach hydrate CLI`):
         DELETE this line entirely (file removed). Renumber the surrounding tree comment if needed (the tree is illustrative; no strict numbering).
       - Line 151 (MANDATORY Reading Table row): change `| New CR fixtures / hydrate-demo path    | examples/README.md |` to `| New CR fixtures / `ach login` + `ach hydrate` demo path | examples/README.md |`.
       - UNCONDITIONALLY add a new Common-failure-mode entry titled `### ❌ Hydrate output ≠ examples/hydrate.json ✅ Normalize golden against cluster host`. Body: explain that the `examples/hydrate.json` golden uses `ach.local.test` host (the standard fixture); when the kept kind cluster exposes a different externally-visible host, raw `diff -u` will fail. Show the resolution: either (a) regenerate the golden against the current cluster's host, OR (b) use `phase6NormalizeHydrate` (the test does the latter automatically). Cite `test/e2e/phase6_helpers_test.go` as the canonical normalization helper. Per W5, this entry lands REGARDLESS of whether the in-cluster host happens to match `ach.local.test` — the gotcha is documented for future cluster topologies.

    5. **Update `README.md`**:
       - If no Quick Start section exists, add one at the top with the headline demo:
         ```
         ## Quick start

         Once the Hub is deployed and reachable at `https://ach.local.test`:

             ach login                                      # one-time device-code SSO
             ach hydrate --environment demo > hydrate.json

         The output is the same as examples/hydrate.json. See
         examples/README.md for the full demo walkthrough.
         ```
       - If a Quick Start section already exists, ensure it shows the `ach login` + `ach hydrate` workflow (NOT the deleted `bash examples/hydrate-demo.sh` invocation).

    6. Run `grep -rn "hydrate-demo" --include="*.md" --include="*.sh" .` from repo root AGAIN — output MUST be empty (no surviving references). If references survive (e.g., in CHANGELOG.md, MAINTAINERS.md, docs/), update those too in the SAME commit.

    7. Run `./scripts/dev.sh make lint` — confirm no markdown-lint issues (if markdown-lint is part of lint; otherwise the markdown change is text-only).

    8. **Documentation hygiene gate** — confirm:
       - All four file changes (delete hydrate-demo.sh + 3 .md updates) are staged together.
       - The new e2e test file from Task 1 is staged in the SAME commit as the doc updates (the e2e test is the live verification that the new documented workflow holds).
       - The commit message starts with `feat(cli): collapse hydrate-demo into ach login + ach hydrate` or `docs(cli): collapse hydrate-demo into ach login + ach hydrate` (conventional commit per `ackstorm-git`).
  </action>
  <verify>
    <automated>! grep -rn "hydrate-demo" --include="*.md" --include="*.sh" .</automated>
  </verify>
  <acceptance_criteria>
    - `git ls-files examples/hydrate-demo.sh | wc -l` returns 0 (file removed from git index).
    - `test -f examples/hydrate-demo.sh && echo "STILL PRESENT" || echo "OK"` returns `OK`.
    - `grep -rn "hydrate-demo" --include="*.md" --include="*.sh" .` returns NO output (no surviving references).
    - `grep -c "ach login" examples/README.md` returns ≥ 1 (replacement workflow documented).
    - `grep -c "ach hydrate --environment demo" examples/README.md` returns ≥ 1.
    - `grep -c "ach login" README.md` returns ≥ 1 (Quick Start section reflects the new flow).
    - CLAUDE.md line 135 region no longer contains `hydrate-demo.sh` (`! grep -n "hydrate-demo" CLAUDE.md`).
    - CLAUDE.md gains the new Common-failure-mode entry (per W5, UNCONDITIONAL): `grep -c "Hydrate output ≠ examples/hydrate.json" CLAUDE.md` returns 1.
    - CLAUDE.md Common-failure-mode entry references the normalization helper: `grep -c "phase6NormalizeHydrate\|test/e2e/phase6_helpers_test.go" CLAUDE.md` returns ≥ 1.
    - All edited files staged in the same commit alongside the Task 1 test files (`git diff --cached --name-only` after `git add` shows both the test files and the modified docs and the deleted hydrate-demo.sh in a single staged set).
    - `./scripts/dev.sh make lint` exits 0.
    - Conventional commit subject line is present.
  </acceptance_criteria>
  <done>
    `examples/hydrate-demo.sh` removed; every doc reference updated in the same commit; the new CLI-driven workflow is the documented headline demo.
  </done>
</task>

<task type="checkpoint:human-verify" gate="blocking">
  <name>Task 3: Human-verify the e2e suite + the new documented workflow against the kept kind cluster</name>
  <what-built>
    - `test/e2e/cli_login_hydrate_test.go` + `test/e2e/phase6_helpers_test.go` (Task 1).
    - `examples/hydrate-demo.sh` deleted; `examples/README.md`, `CLAUDE.md`, `README.md` updated (Task 2).
  </what-built>
  <how-to-verify>
    1. Bring up the kind cluster (one-time):
       ```
       ./scripts/dev.sh make cluster-keep
       ```
       Wait for `make wait-ach` to return Ready (all three Hub Deployments + operator+content-service Pod). The `examples/` fixtures should already be applied as part of `cluster-keep`.

    2. Build the binary:
       ```
       ./scripts/dev.sh make build
       ```
       Confirm `./bin/ach` exists and is executable.

    3. Run the new e2e suite under the engineer-pending opt-in:
       ```
       ACH_E2E_PHASE6=1 ./scripts/dev.sh make e2e-focus FOCUS=TestPhase6CLI
       ```
       Expected: All 5 subtests PASS (or all SKIP cleanly if a prereq is missing — read the skip reason carefully and fix root cause before declaring failure).

    4. Run the new documented workflow manually (this is the doc-accuracy check):
       ```
       ./bin/ach login                                 # interactive: enter deployment name + URL
       ./bin/ach whoami --verify                       # exit 0
       ./bin/ach env list                              # demo row present
       ./bin/ach hydrate --environment demo > /tmp/hydrate-test.json
       diff -u /tmp/hydrate-test.json examples/hydrate.json
       ```
       Expected: The diff returns exit 0 (no differences) OR the documented host-substitution caveat applies (Task 1 SUMMARY).

    5. Read the updated `examples/README.md` and `CLAUDE.md` — confirm the new workflow instructions are accurate and reproduce step 4 (no missing flags, no wrong URLs, no stale "bash examples/hydrate-demo.sh" copy-paste).

    6. Run the full pre-push gate:
       ```
       make pre-push
       ```
       Expected: exit 0 (all 17 gates green, including SPDX, govulncheck, gitleaks, lint, unit).

    Approve with "approved" if every step passes. If any step fails, describe the failure (paste the exact error / diff output) so it can be addressed in a follow-up commit before merge.
  </how-to-verify>
  <resume-signal>Type "approved" if all 6 verification steps passed against the kept kind cluster, or describe failures.</resume-signal>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| e2e test process → kept kind cluster | Test-only network path; cluster runs in localhost; no remote attack surface |
| Documentation → reader | README/CLAUDE.md/examples/README.md drive operator behavior; stale doc → bad runbook |
| Deletion of examples/hydrate-demo.sh → workflow break | Anyone with a personal automation referencing the script will break; explicit deletion + doc update is the mitigation |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-09-01 | Tampering | hydrate golden diff drift | mitigate | The byte-for-byte assertion catches any unintentional schema/format change in `ach hydrate`'s stdout. Phase 7 schemaVersion bump → golden regeneration → checked into the same commit. |
| T-06-09-02 | Repudiation | e2e suite never run before merge | mitigate | Task 3 human-verify checkpoint is BLOCKING — the suite MUST be exercised against a real cluster before the PR merges. CLAUDE.md §"Test phases" `make e2e-focus` is the documented surface. |
| T-06-09-03 | Information Disclosure | leaked pk_ in test logs | mitigate | Pattern S5 applies: phase6SuiteGuard + helpers MUST NOT log the pk_ plaintext or the device-code session_id; the test asserts `pk_***` (redacted) appears in --verbose output, never the raw value. |
| T-06-09-04 | Denial of Service | runaway test against shared cluster | accept | The cluster is local kind; cleanup happens via `make cluster-down`. No remote impact. |
| T-06-09-05 | Tampering | doc updates without matching code | mitigate | Documentation hygiene (CLAUDE.md §"Documentation hygiene") — all four file changes (delete + 3 docs) ship in the SAME commit as the e2e test. The pre-push hook does not enforce this rule mechanically (it's a CONTRIBUTING/CLAUDE.md guideline), so the human-verify checkpoint is the gate. |
| T-06-09-06 | Information Disclosure | examples/hydrate.json containing prod data | accept | The golden file contains fixture data only (the `claude-code-system-prompt` / `caveman` / `openclaw-templates` IDs are public example fixtures). No PII, no prod URLs. |
| T-06-09-SC | Tampering | npm/pip/cargo installs | mitigate | No new package installs in this plan — only stdlib + already-pinned testing helpers. No govulncheck ack-list change. |
</threat_model>

<verification>
After all 3 tasks complete:

```bash
# Compile + lint
./scripts/dev.sh go vet -tags=e2e ./test/e2e/...
./scripts/dev.sh make lint

# Confirm no surviving hydrate-demo references
! grep -rn "hydrate-demo" --include="*.md" --include="*.sh" .

# Run the new e2e suite (requires kept cluster + ACH_E2E_PHASE6 opt-in)
./scripts/dev.sh make cluster-keep   # one-time, if not already up
./scripts/dev.sh make build
ACH_E2E_PHASE6=1 ./scripts/dev.sh make e2e-focus FOCUS=TestPhase6CLI

# Pre-push gate (full 17 gates)
make pre-push
```

The human-verify checkpoint (Task 3) is the merge gate — type "approved" only after the full demo workflow round-trip succeeds against the live cluster.
</verification>

<success_criteria>
- `test/e2e/cli_login_hydrate_test.go` exists with `TestPhase6CLI` umbrella + 5 subtests + byte-for-byte golden diff.
- `test/e2e/phase6_helpers_test.go` exists with `phase6SuiteGuard` skip discipline mirroring phase3/4/5 helpers.
- `examples/hydrate-demo.sh` removed from git (no surviving file, no surviving references in any `.md`).
- `examples/README.md` documents the `ach login` + `ach hydrate --environment demo` workflow.
- `CLAUDE.md` lines 126/135/151 updated (no hydrate-demo references remain).
- `README.md` Quick Start section shows the new CLI workflow.
- All edits land in ONE commit per CLAUDE.md §"Documentation hygiene".
- Task 3 human-verify checkpoint approved by the user after running the full demo against the kept kind cluster.
- `make pre-push` exits 0 (all 17 gates green).
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-09-SUMMARY.md` when done. Record:
- Which D-18 bypass mechanism was chosen (Option A env-var-injected pk_ OR Option B build-tag-gated --token flag).
- Confirm `phase6NormalizeHydrate` shipped (per W4 — host-substitution helper is the locked contract; bytes.Equal compares against the normalized golden).
- Confirm the new CLAUDE.md Common-failure-mode entry `### ❌ Hydrate output ≠ examples/hydrate.json ✅` is present (per W5 — UNCONDITIONAL).
- Total count of doc references updated (must match the pre-edit grep output).
- The exact `make pre-push` runtime (for STATE.md velocity tracking).
</output>
