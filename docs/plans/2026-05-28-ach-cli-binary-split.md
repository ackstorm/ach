# ach-cli Binary Split (Option B — Clean Separation) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ship `ach-cli` as a separate Go binary containing only user-facing subcommands (login/logout/whoami/config/env/env-keys/hydrate/admin). The `ach` binary keeps only the long-running service modes (operator/platform-api/forwarder/content-service/migrate). Both binaries share the existing `internal/cli/*` libraries unchanged.

**Architecture:** New `cmd/ach-cli/` tree with its own `main.go` + `cmd/` cobra root. Move (git mv, preserve history) the 8 user-facing `*.go` + their `*_test.go` from `cmd/ach/cmd/` to `cmd/ach-cli/cmd/`. Extract the typed-error dispatch loop from `cmd/ach/main.go` to a shared `internal/cli/exit/dispatch.go` consumed by both entrypoints. Goreleaser builds + signs + SBOMs both binaries; container image keeps only `ach` (CLI ships as goreleaser archives). Helm chart unchanged.

**Tech Stack:** Go 1.x cobra, controller-runtime (service side only), goreleaser v2, cosign keyless OIDC, cyclonedx-gomod, kind+Helm e2e suite. Toolchain entirely via `./scripts/dev.sh` (no host Go).

**Scope discipline:** Pure refactor + release-pipeline change. No new behavior, no new flags, no new tests beyond what file-move requires + 1 binary-size smoke test. CLI binary size target: < 50% of `ach` size (drops k8s.io/* + controller-runtime).

---

## Pre-flight

**Branch:** Create `feat/ach-cli-binary-split` off `main`. Worktree-isolated execution recommended (`@superpowers:using-git-worktrees`).

**Baseline capture:** Before any change, record:

```bash
./scripts/dev.sh make build && ls -la bin/ach
./scripts/dev.sh go test ./cmd/ach/... ./internal/cli/... 2>&1 | tail -5
# Capture test count + binary size for diff at end.
```

Expected: `bin/ach` size ~50-80 MB, all CLI tests PASS (existing Phase 6 suite).

---

## Task 1: Extract typed-error dispatch helper

**Why first:** Both binaries' `main.go` need the same `*ServerError`/`*CodedError`/fallthrough dispatch. Pull it out once before either binary needs a copy.

**Files:**
- Create: `internal/cli/exit/dispatch.go`
- Create: `internal/cli/exit/dispatch_test.go`
- Modify: `cmd/ach/main.go` (replace dispatch block with call into new helper)

**Step 1: Write the failing test**

```go
// internal/cli/exit/dispatch_test.go
// SPDX-License-Identifier: Apache-2.0
package exit_test

import (
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

func TestDispatch_OK(t *testing.T) {
	code := exit.Dispatch(nil)
	if code != exit.OK {
		t.Errorf("nil err → exit.OK; got %d", code)
	}
}

func TestDispatch_ServerError(t *testing.T) {
	sErr := &httpclient.ServerError{StatusCode: 403, Code: "not_admin"}
	code := exit.Dispatch(sErr)
	if code != exit.AuthN {
		t.Errorf("not_admin → AuthN; got %d", code)
	}
}

func TestDispatch_CodedError(t *testing.T) {
	cErr := &exit.CodedError{Code: exit.Usage, Err: errors.New("bad flag")}
	code := exit.Dispatch(cErr)
	if code != exit.Usage {
		t.Errorf("CodedError preserves Code; got %d", code)
	}
}

func TestDispatch_Fallthrough(t *testing.T) {
	code := exit.Dispatch(errors.New("unrecognized"))
	if code != exit.General {
		t.Errorf("unknown err → General; got %d", code)
	}
}
```

**Step 2: Run test — verify it fails**

```bash
./scripts/dev.sh go test ./internal/cli/exit/... -run TestDispatch -v
```

Expected: FAIL with `undefined: exit.Dispatch`.

**Step 3: Write minimal implementation**

```go
// internal/cli/exit/dispatch.go
// SPDX-License-Identifier: Apache-2.0
package exit

import (
	"errors"
	"fmt"
	"io"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// Dispatch resolves any error returned from cobra.Execute() to the
// process exit Code per CLI spec §9.3. nil → OK; *httpclient.ServerError
// → MapServerError; *CodedError → its Code; anything else → General.
//
// Use DispatchAndRender from main() when you also want to print the
// error to stderr before exiting.
func Dispatch(err error) Code {
	if err == nil {
		return OK
	}
	var sErr *httpclient.ServerError
	if errors.As(err, &sErr) {
		return MapServerError(sErr)
	}
	var cErr *CodedError
	if errors.As(err, &cErr) {
		return cErr.Code
	}
	return General
}

// DispatchAndRender resolves the exit Code and writes the error string
// to stderr (when err is non-nil). main() should call os.Exit(int(code))
// with the returned Code.
func DispatchAndRender(err error, stderr io.Writer) Code {
	code := Dispatch(err)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
	}
	return code
}
```

**Step 4: Run test — verify it passes**

```bash
./scripts/dev.sh go test ./internal/cli/exit/... -run TestDispatch -v
```

Expected: PASS all four.

**Step 5: Refactor `cmd/ach/main.go` to consume `DispatchAndRender`**

```go
// cmd/ach/main.go
// SPDX-License-Identifier: Apache-2.0
// (header comment unchanged — keeps the §9.3 contract docs)
package main

import (
	"os"

	"github.com/ackstorm/ach/cmd/ach/cmd"
	"github.com/ackstorm/ach/internal/cli/exit"
)

func main() {
	os.Exit(int(exit.DispatchAndRender(cmd.Execute(), os.Stderr)))
}
```

**Step 6: Verify nothing regressed**

```bash
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make unit
```

Expected: build clean, unit suite PASS.

**Step 7: Commit**

```bash
git add internal/cli/exit/dispatch.go internal/cli/exit/dispatch_test.go cmd/ach/main.go
git commit -m "refactor(exit): hoist typed-error dispatch to internal/cli/exit for binary split"
```

---

## Task 2: Create `cmd/ach-cli/` skeleton with empty cobra root

**Why:** Get the new entrypoint compiling before any subcommand moves. Smoke-tests the `cmd/ach-cli` package structure under goreleaser-eligible path.

**Files:**
- Create: `cmd/ach-cli/main.go`
- Create: `cmd/ach-cli/cmd/root.go`

**Step 1: Write the root**

```go
// cmd/ach-cli/cmd/root.go
// SPDX-License-Identifier: Apache-2.0
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is overridden via -ldflags at build time (see Makefile build target).
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "ach-cli",
	Short: "ACH CLI — operator/developer client for the ACH control plane",
	Long: `ach-cli is the user-facing client for the ACH (Agent Configuration Hub)
control plane. Subcommands:

  login        Authenticate against the platform-api
  logout       Revoke local session
  whoami       Show current identity
  config       Inspect / mutate local config
  env          List + switch environments
  env-keys     Create / list / revoke environment keys
  hydrate      Materialize workspace artifacts
  admin        Admin subcommands (keys revoke, users revoke-keys, refresh)

For service-mode commands (operator, platform-api, forwarder,
content-service, migrate), use the 'ach' binary instead.`,
	Version: Version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func Execute() error { return rootCmd.Execute() }

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("ach-cli %s\n", Version))
}
```

**Step 2: Write the entrypoint**

```go
// cmd/ach-cli/main.go
// SPDX-License-Identifier: Apache-2.0

// `ach-cli` is the user-facing client binary for the ACH control plane.
// Sibling to `ach` (services-only). Exit-code dispatch is shared via
// internal/cli/exit.DispatchAndRender per CLI spec §9.3.
package main

import (
	"os"

	"github.com/ackstorm/ach/cmd/ach-cli/cmd"
	"github.com/ackstorm/ach/internal/cli/exit"
)

func main() {
	os.Exit(int(exit.DispatchAndRender(cmd.Execute(), os.Stderr)))
}
```

**Step 3: Build smoke**

```bash
./scripts/dev.sh go build -o /tmp/ach-cli-smoke ./cmd/ach-cli/...
/tmp/ach-cli-smoke --help
echo "EXIT=$?"
```

Expected: build clean; `--help` prints the Long block; exit 0. No subcommands listed yet (next tasks add them).

**Step 4: Commit**

```bash
git add cmd/ach-cli/main.go cmd/ach-cli/cmd/root.go
git commit -m "feat(ach-cli): scaffold ach-cli binary skeleton (empty cobra root)"
```

---

## Task 3: Move `login` subcommand — proof of refactor pattern

**Why:** Establish the pattern once. All 7 remaining subcommands repeat steps 1-4 verbatim with their own filenames.

**Files:**
- Move: `cmd/ach/cmd/login.go` → `cmd/ach-cli/cmd/login.go`
- Move: `cmd/ach/cmd/login_test.go` → `cmd/ach-cli/cmd/login_test.go`

**Step 1: Move files (preserves git history)**

```bash
git mv cmd/ach/cmd/login.go cmd/ach-cli/cmd/login.go
git mv cmd/ach/cmd/login_test.go cmd/ach-cli/cmd/login_test.go
```

**Step 2: Build to surface any cross-file dependencies**

```bash
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
```

Expected output: BOTH builds succeed. `login.go` references `rootCmd` from its package — package name didn't change (`package cmd`), so the new `cmd/ach-cli/cmd/root.go::rootCmd` resolves identically. If anything fails (e.g., login.go references a helper that lives in another `cmd/ach/cmd/` file), capture the error and resolve before continuing.

Likely culprits if step fails:
- shared helpers in another file not yet moved (defer move until that helper lands too)
- import of a symbol from `cmd/ach/cmd` (rare — login.go imports `internal/cli/*` only)

**Step 3: Smoke the wired command**

```bash
./scripts/dev.sh go build -o /tmp/ach-cli-smoke ./cmd/ach-cli/...
/tmp/ach-cli-smoke login --help | head -5
echo "EXIT=$?"
```

Expected: help block prints "Authenticate against the platform-api" or similar; exit 0.

**Step 4: Test moved file in new location**

```bash
./scripts/dev.sh go test ./cmd/ach-cli/cmd/... -run TestLogin -v 2>&1 | tail -10
```

Expected: every previously-passing TestLogin* subtest PASSES from the new path. If a test references `executeCommand` (defined in `helpers_test.go`, not yet moved), the test won't compile until Task 11 — that's OK; defer this verification step to Task 11.

**Step 5: Commit**

```bash
git add cmd/ach-cli/cmd/login.go cmd/ach-cli/cmd/login_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move login subcommand from cmd/ach/cmd to cmd/ach-cli/cmd"
```

---

## Task 4: Move `logout` subcommand

Repeat Task 3 pattern verbatim:

```bash
git mv cmd/ach/cmd/logout.go cmd/ach-cli/cmd/logout.go
git mv cmd/ach/cmd/logout_test.go cmd/ach-cli/cmd/logout_test.go
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
git add cmd/ach-cli/cmd/logout.go cmd/ach-cli/cmd/logout_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move logout subcommand to cmd/ach-cli/cmd"
```

---

## Task 5: Move `whoami` subcommand

```bash
git mv cmd/ach/cmd/whoami.go cmd/ach-cli/cmd/whoami.go
git mv cmd/ach/cmd/whoami_test.go cmd/ach-cli/cmd/whoami_test.go
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
git add cmd/ach-cli/cmd/whoami.go cmd/ach-cli/cmd/whoami_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move whoami subcommand to cmd/ach-cli/cmd"
```

---

## Task 6: Move `config` subcommand

```bash
git mv cmd/ach/cmd/config.go cmd/ach-cli/cmd/config.go
git mv cmd/ach/cmd/config_test.go cmd/ach-cli/cmd/config_test.go
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
git add cmd/ach-cli/cmd/config.go cmd/ach-cli/cmd/config_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move config subcommand to cmd/ach-cli/cmd"
```

---

## Task 7: Move `env` subcommand

```bash
git mv cmd/ach/cmd/env.go cmd/ach-cli/cmd/env.go
git mv cmd/ach/cmd/env_test.go cmd/ach-cli/cmd/env_test.go
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
git add cmd/ach-cli/cmd/env.go cmd/ach-cli/cmd/env_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move env subcommand to cmd/ach-cli/cmd"
```

---

## Task 8: Move `env-keys` subcommand

```bash
git mv cmd/ach/cmd/env_keys.go cmd/ach-cli/cmd/env_keys.go
git mv cmd/ach/cmd/env_keys_test.go cmd/ach-cli/cmd/env_keys_test.go
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
git add cmd/ach-cli/cmd/env_keys.go cmd/ach-cli/cmd/env_keys_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move env-keys subcommand to cmd/ach-cli/cmd"
```

---

## Task 9: Move `hydrate` subcommand

```bash
git mv cmd/ach/cmd/hydrate.go cmd/ach-cli/cmd/hydrate.go
git mv cmd/ach/cmd/hydrate_test.go cmd/ach-cli/cmd/hydrate_test.go
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
git add cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/hydrate_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move hydrate subcommand to cmd/ach-cli/cmd"
```

---

## Task 10: Move `admin` subcommand

```bash
git mv cmd/ach/cmd/admin.go cmd/ach-cli/cmd/admin.go
git mv cmd/ach/cmd/admin_test.go cmd/ach-cli/cmd/admin_test.go
./scripts/dev.sh go build ./cmd/ach-cli/... ./cmd/ach/...
git add cmd/ach-cli/cmd/admin.go cmd/ach-cli/cmd/admin_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move admin subcommand to cmd/ach-cli/cmd"
```

---

## Task 11: Move shared test helpers + cross-cmd guard test

**Why:** `helpers_test.go::executeCommand` is consumed by every `*_test.go` in the CLI tree. `synthetic_guard_test.go` enumerates every user-facing subcommand and asserts each one wires `synthetic.GuardCommand`. Both must follow the subcommands.

**Files:**
- Move: `cmd/ach/cmd/helpers_test.go` → `cmd/ach-cli/cmd/helpers_test.go`
- Move: `cmd/ach/cmd/synthetic_guard_test.go` → `cmd/ach-cli/cmd/synthetic_guard_test.go`

**Step 1: Move**

```bash
git mv cmd/ach/cmd/helpers_test.go cmd/ach-cli/cmd/helpers_test.go
git mv cmd/ach/cmd/synthetic_guard_test.go cmd/ach-cli/cmd/synthetic_guard_test.go
```

**Step 2: Run the full CLI test suite from new location**

```bash
./scripts/dev.sh go test ./cmd/ach-cli/cmd/... -v 2>&1 | tail -30
```

Expected: ALL Phase 6 tests PASS from the new location. Test counts should match the baseline captured in Pre-flight. If `synthetic_guard_test.go` enumerates commands by hard-coded list, no edit needed — the commands all live in the same package now.

**Step 3: Run the service-side test suite (regression)**

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -v 2>&1 | tail -10
```

Expected: service-mode tests (operator/platform-api/forwarder/content-service/migrate) PASS. Service-mode `*.go` files have no `*_test.go` partners pre-move, so this should be a quick no-op pass or just compile.

**Step 4: Commit**

```bash
git add cmd/ach-cli/cmd/helpers_test.go cmd/ach-cli/cmd/synthetic_guard_test.go cmd/ach/cmd/
git commit -m "refactor(ach-cli): move helpers_test + synthetic_guard_test to cmd/ach-cli/cmd"
```

---

## Task 12: Update `cmd/ach/cmd/root.go` long-help — drop CLI surface mention

**Why:** Service-mode rootCmd's `Long` field currently mentions "Run as CLI: invoke without a subcommand (CLI surface lands in Phase 6)." After split, `ach` no longer ships CLI; the Long text is stale.

**Files:**
- Modify: `cmd/ach/cmd/root.go` (Long field only)

**Step 1: Edit**

Replace the Long block with:

```go
	Long: `ach is the service-mode binary for the ACH control plane.

Run a long-running service:
  ach operator
  ach platform-api
  ach forwarder
  ach content-service

Run a one-shot job:
  ach migrate

For the user-facing CLI (login/whoami/logout/config/env/env-keys/
hydrate/admin), use the sibling 'ach-cli' binary.`,
```

**Step 2: Build smoke**

```bash
./scripts/dev.sh go build ./cmd/ach/...
./bin/ach --help 2>&1 | head -15
```

Expected: help text references `ach-cli` for user CLI; no mention of login/hydrate/etc. as `ach` subcommands.

**Step 3: Commit**

```bash
git add cmd/ach/cmd/root.go
git commit -m "docs(ach): update root --help to point users at ach-cli for user CLI"
```

---

## Task 13: Add `ach-cli` build block to `.goreleaser.yml`

**Why:** Stable release config must produce both binaries + per-binary archives + per-binary SBOMs + cosign signatures.

**Files:**
- Modify: `.goreleaser.yml`

**Step 1: Read existing `builds:` block**

```bash
sed -n '/^builds:/,/^[a-z]/p' .goreleaser.yml | head -50
```

Capture: `id`, `main`, `binary`, `env`, `goos`, `goarch`, `ldflags`. The new entry mirrors the existing one with `id: ach-cli`, `main: ./cmd/ach-cli`, `binary: ach-cli`.

**Step 2: Add second `builds:` entry**

Append (after the existing `ach` block, before the next top-level key):

```yaml
  - id: ach-cli
    main: ./cmd/ach-cli
    binary: ach-cli
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X github.com/ackstorm/ach/cmd/ach-cli/cmd.Version={{.Version}}
```

(Match the existing block's `env` / `goos` / `goarch` / `ldflags` verbatim — only `id`, `main`, `binary`, and the `-X` package path differ.)

**Step 3: Add separate `archives:` entry if existing archives is filtered by build id**

Read the `archives:` block. If it lists `builds: [ach]`, add a sibling entry for `ach-cli`. If it uses the default (all builds), no edit needed.

**Step 4: Validate config**

```bash
./scripts/dev.sh sh -c 'goreleaser check --config .goreleaser.yml'
```

Expected: exit 0, no warnings about missing fields.

**Step 5: Repeat for `.goreleaser.prerelease.yml` and `.goreleaser.snapshot.yml`**

```bash
# Add the same ach-cli build block + archive entry to both configs.
./scripts/dev.sh sh -c 'goreleaser check --config .goreleaser.prerelease.yml'
./scripts/dev.sh sh -c 'goreleaser check --config .goreleaser.snapshot.yml'
```

Expected: both check exit 0.

**Step 6: Snapshot release rehearsal**

```bash
./scripts/dev.sh sh -c 'goreleaser release --snapshot --clean --config .goreleaser.snapshot.yml' 2>&1 | tail -20
ls -la dist/ach_linux_amd64*/ach dist/ach-cli_linux_amd64*/ach-cli 2>&1
```

Expected: both binaries exist in `dist/`. Both have non-zero size. `ach-cli` should be visibly smaller than `ach` (drops k8s deps).

**Step 7: Commit**

```bash
git add .goreleaser.yml .goreleaser.prerelease.yml .goreleaser.snapshot.yml
git commit -m "build(release): add ach-cli binary build to all three goreleaser configs"
```

---

## Task 14: Container image — keep `ach` only

**Why:** Service Deployments run `ach <mode>`; they do not need `ach-cli` inside the image. Keeping the image lean preserves the size benefit of the split.

**Files:**
- Modify: `Dockerfile` (no change expected — already only builds `ach`)
- Modify: `Dockerfile.goreleaser` (no change expected)

**Step 1: Verify current Dockerfile only builds `ach`**

```bash
grep -E 'cmd/ach[-/]|COPY.*ach[-/ ]|ENTRYPOINT|CMD' Dockerfile Dockerfile.goreleaser
```

Expected: no references to `ach-cli`. If anything builds `cmd/ach/...` as a wildcard, narrow to `cmd/ach` (no trailing dash variant matches by accident).

**Step 2: Verify goreleaser `dockers:` block targets `ach` only**

```bash
sed -n '/^dockers:/,/^[a-z]/p' .goreleaser.yml | head -20
```

Expected: image_templates reference `ach` only; if it copies `ach-cli`, remove that line so the image stays lean.

**Step 3: Snapshot Docker build sanity (already exercised by Task 13's snapshot rehearsal)**

The goreleaser snapshot already attempts the container build. If it succeeded in Task 13 step 6, this task is verification-only.

**Step 4: If any change was made, commit**

```bash
git add Dockerfile Dockerfile.goreleaser .goreleaser.yml
git commit -m "build(docker): keep ach-cli out of the service container image"
```

(Skip the commit if no change was needed.)

---

## Task 15: Helm chart — no change required, verify

**Why:** Deployments in `deploy/helm/ach/templates/*.yaml` set `args: ["<mode>"]` on the operator/platform-api/forwarder/content-service Pods. They never reference login/hydrate/etc., so no edits needed. Verify there's no stale reference.

**Files:**
- Read-only check: `deploy/helm/ach/templates/*.yaml`

**Step 1: Grep for CLI-only subcommands in Helm templates**

```bash
grep -rE 'login|whoami|logout|hydrate|env-keys|admin' deploy/helm/ach/templates/ 2>&1
```

Expected: NO matches. (Some `env:` blocks may match the literal word "env" — narrow to whole-word if false positives: `grep -rwE '...'`.)

**Step 2: If grep finds a stale reference, capture + fix**

Report the location; edit + commit. If clean, no commit.

---

## Task 16: E2E suite — point `phase6RunAch` at `ach-cli` binary

**Why:** `test/e2e/phase6_helpers_test.go::phase6RunAch` invokes `./bin/ach <args>`. After split, user-CLI args land on `ach-cli`. Helper must invoke the new binary.

**Files:**
- Modify: `test/e2e/phase6_helpers_test.go` (helper binary path)
- Modify: `Makefile` `build` target (build both binaries)

**Step 1: Update the Makefile `build` target**

Read current target:

```bash
grep -A 3 '^build:' Makefile
```

Add a second `go build` line (or extend the existing) so both binaries land in `bin/`:

```makefile
build:
	./scripts/dev.sh go build -ldflags "-s -w -X github.com/ackstorm/ach/cmd/ach/cmd.Version=$(VERSION)" -o bin/ach ./cmd/ach
	./scripts/dev.sh go build -ldflags "-s -w -X github.com/ackstorm/ach/cmd/ach-cli/cmd.Version=$(VERSION)" -o bin/ach-cli ./cmd/ach-cli
```

(Preserve existing flags / -X paths from the current target.)

**Step 2: Update `phase6RunAch` to invoke `bin/ach-cli`**

Open `test/e2e/phase6_helpers_test.go`; find the line that exec's `./bin/ach`. Change to `./bin/ach-cli`.

**Step 3: Rebuild + run a focused subset**

```bash
./scripts/dev.sh make build
ls -la bin/ach bin/ach-cli
```

Expected: both binaries exist.

```bash
# Smoke each subcommand exists in ach-cli:
for sub in login logout whoami config env env-keys hydrate admin; do
  ./bin/ach-cli $sub --help >/dev/null && echo "OK: $sub" || echo "FAIL: $sub"
done
```

Expected: 8 OK lines.

```bash
# Confirm service subcommands stayed on ach:
for sub in operator platform-api forwarder content-service migrate; do
  ./bin/ach $sub --help >/dev/null && echo "OK: $sub" || echo "FAIL: $sub"
done
```

Expected: 5 OK lines.

```bash
# Confirm cross-binary surface separation:
./bin/ach login --help 2>&1 | head -3   # expect "unknown command 'login'"
./bin/ach-cli operator --help 2>&1 | head -3   # expect "unknown command 'operator'"
```

**Step 4: Run a focused e2e to validate the binary path swap**

If a kept cluster is available:

```bash
ACH_E2E_PHASE6=1 ACH_E2E_PHASE6_PK=pk_<...> \
  ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI/login_device_code' 2>&1 | tail -10
```

Expected: PASS. Other subtests can wait until full e2e in Task 18.

**Step 5: Commit**

```bash
git add Makefile test/e2e/phase6_helpers_test.go
git commit -m "test(e2e): point phase6RunAch at ach-cli binary; Makefile builds both binaries"
```

---

## Task 17: Docs sweep — CLAUDE.md, README, runbook, examples

**Why:** Every `./bin/ach login` / `./bin/ach hydrate` reference in docs is stale.

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `docs/runbooks/phase6-cli-e2e-verification.md`
- Modify: `examples/README.md`

**Step 1: Find all references**

```bash
grep -rnE 'ach (login|logout|whoami|config|env|env-keys|hydrate|admin)' \
  CLAUDE.md README.md examples/README.md docs/ 2>&1 | head -30
```

Expected: a list of locations. For each:
- `./bin/ach login` → `./bin/ach-cli login`
- `ach login` (bare, in prose) → `ach-cli login`
- Mode tables / repo-layout diagrams referencing the user CLI on `ach` → move to a sibling `ach-cli` table or annotate

**Step 2: Update CLAUDE.md Mode table**

The "Each long-running service runs the same `ach` image with a cobra subcommand as `args:`" table stays (service modes only). Add a sibling section directly below it:

```markdown
The user-facing CLI ships as a separate binary, `ach-cli`:

| Subcommand   | Owns                                           |
|--------------|------------------------------------------------|
| `ach-cli login`       | Device-code SSO login                  |
| `ach-cli whoami`      | Display current identity               |
| `ach-cli logout`      | Revoke local session                   |
| `ach-cli config`      | Inspect / mutate local config          |
| `ach-cli env`         | List + switch environments             |
| `ach-cli env-keys`    | Create / list / revoke env keys        |
| `ach-cli hydrate`     | Materialize workspace artifacts        |
| `ach-cli admin`       | Admin keys revoke / users revoke-keys / refresh |
```

**Step 3: Update CLAUDE.md repo layout**

Find the `cmd/ach/cmd/` section. Add a sibling `cmd/ach-cli/` entry:

```
├── cmd/ach/main.go          ← service-mode entrypoint
├── cmd/ach/cmd/              ← cobra root + service-mode subcommands
│   ├── root.go               (Version, services root cmd)
│   ├── operator.go, platform_api.go, forwarder.go,
│   ├── content_service.go, migrate.go
├── cmd/ach-cli/main.go      ← user CLI entrypoint
├── cmd/ach-cli/cmd/          ← cobra root + user-facing subcommands
│   ├── root.go               (Version, cli root cmd)
│   ├── login.go, logout.go, whoami.go, config.go,
│   ├── env.go, env_keys.go, hydrate.go, admin.go
```

**Step 4: Update README.md Quick Start**

Find the "Quick Start" section (added by Plan 06-09). Replace every `./bin/ach <user-cmd>` with `./bin/ach-cli <user-cmd>`. Service-mode invocations (`./bin/ach operator`) stay on `ach`.

**Step 5: Update docs/runbooks/phase6-cli-e2e-verification.md**

CHECK 1: replace `./bin/ach --help` smoke with two checks — `./bin/ach --help` (services only) and `./bin/ach-cli --help` (user CLI). Expected subcommand lists split accordingly.

**Step 6: Update examples/README.md**

Find the "End-to-end demo" section. Replace every `ach login` / `ach hydrate` with `ach-cli login` / `ach-cli hydrate`.

**Step 7: Confirm grep is clean**

```bash
grep -rnE '\./bin/ach (login|logout|whoami|config|env|env-keys|hydrate|admin)' \
  CLAUDE.md README.md examples/README.md docs/ 2>&1
```

Expected: no matches.

**Step 8: Commit**

```bash
git add CLAUDE.md README.md docs/ examples/README.md
git commit -m "docs: split user-CLI references to ach-cli binary (Option B refactor)"
```

---

## Task 18: Binary-size validation + final full-suite verification

**Why:** Confirm the size benefit landed; confirm nothing regressed.

**Step 1: Build both binaries clean**

```bash
rm -rf bin/
./scripts/dev.sh make build
ls -la bin/ach bin/ach-cli
du -sh bin/ach bin/ach-cli
```

**Step 2: Record sizes in the commit message**

Capture:
- `bin/ach` size (service-only — should be roughly unchanged from baseline)
- `bin/ach-cli` size (user-CLI only — target < 50% of `ach` size)

If `ach-cli` is NOT visibly smaller, investigate: `go build -ldflags '-s -w' ...` should have stripped symbols; check that no test code or `_ = "import"` style import-for-side-effect leaks k8s.io into the CLI binary.

```bash
./scripts/dev.sh sh -c 'go list -deps ./cmd/ach-cli/... | grep k8s.io'
```

Expected: NO matches. If any `k8s.io/*` package shows up in ach-cli's transitive deps, find the import and remove it (likely a stray import in one of the moved subcommand files that was already dead code in the monolithic binary).

**Step 3: Run the full unit suite**

```bash
./scripts/dev.sh make unit
```

Expected: PASS. Test count should match (or exceed by 4) the baseline captured in Pre-flight (4 = the new `TestDispatch*` tests from Task 1).

**Step 4: Run the full lint sweep**

```bash
./scripts/dev.sh make lint
```

Expected: clean.

**Step 5: Run a full Phase 6 e2e (cluster required)**

```bash
ACH_E2E_PHASE6=1 ACH_E2E_PHASE6_PK=pk_<...> \
  ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI' 2>&1 | tail -20
```

Expected: all 5 subtests PASS against the new `ach-cli` binary.

**Step 6: Run `make pre-push`**

```bash
make pre-push 2>&1 | tail -10
```

Expected: 17 gates pass, exit 0.

**Step 7: Commit size + verification receipt**

If anything was tweaked in steps 1-6, commit each fix as it was found. If steps 1-6 were all clean, no commit needed — the verification is documented in the PR description.

---

## Task 19: Open PR + spec follow-up Phase 7 prep

**Why:** Capture the refactor as a discrete PR for review independent of Phase 6 + flag the Phase 7 implications (CR-01 fix, brew/scoop taps).

**Step 1: Open PR against `main`**

```bash
git push -u origin feat/ach-cli-binary-split
gh pr create --title "refactor: split ach-cli into separate binary (Option B)" \
  --body "$(cat <<'EOF'
## Summary

Splits the `ach` monolithic binary into two siblings:

- `ach` — service-mode only (operator, platform-api, forwarder, content-service, migrate)
- `ach-cli` — user-facing CLI only (login, logout, whoami, config, env, env-keys, hydrate, admin)

Pure refactor. No behavior change. Shared library code (`internal/cli/*`) unchanged.

## Binary sizes

| Binary | Before | After | Δ |
|--------|--------|-------|----|
| ach    | <N MB> | <N MB> | <0 to +1 MB> |
| ach-cli | (n/a) | <M MB> | new, < 50% of ach |

## Tasks

- [x] Task 1: extract `internal/cli/exit.Dispatch`
- [x] Task 2: scaffold `cmd/ach-cli/`
- [x] Tasks 3-10: move 8 user subcommands
- [x] Task 11: move shared test helpers
- [x] Task 12: update service rootCmd --help
- [x] Task 13: goreleaser builds + archives both
- [x] Task 14: container image keeps ach only
- [x] Task 15: Helm chart unchanged (verified)
- [x] Task 16: e2e suite targets ach-cli
- [x] Task 17: docs sweep
- [x] Task 18: size + full suite + pre-push

## Follow-ups (deferred)

- Brew tap formula for ach-cli (separate PR)
- Scoop bucket manifest for ach-cli (separate PR)
- CR-01 fix (remove `DisallowUnknownFields` in `internal/cli/httpclient/client.go:229`) — flagged in 06-REVIEW.md, unrelated to this refactor but recommended before Phase 7

## Test plan

- [x] `./scripts/dev.sh make unit` — PASS (baseline + 4 new TestDispatch* tests)
- [x] `./scripts/dev.sh make lint` — clean
- [x] `make pre-push` — 17 gates pass
- [x] `./bin/ach --help` + `./bin/ach-cli --help` — surface separation correct
- [x] `goreleaser release --snapshot --clean` — both binaries build + sign
- [ ] Phase 6 e2e suite against kept kind cluster — gated on engineer access
EOF
)"
```

**Step 2: Note in `.planning/STATE.md`**

This isn't a numbered phase; document in STATE.md "Current focus" line so `/gsd-progress` surfaces the open refactor branch.

---

## Done

Final state:

- `cmd/ach/main.go` — 1-line entrypoint, calls `exit.DispatchAndRender`
- `cmd/ach/cmd/` — root.go + 5 service-mode files (~1500 LOC)
- `cmd/ach-cli/main.go` — 1-line entrypoint, same dispatcher
- `cmd/ach-cli/cmd/` — new root.go + 8 user-CLI files + 2 test helpers (~6500 LOC)
- `internal/cli/exit/dispatch.go` — shared dispatch + tests
- Both binaries built by Makefile + goreleaser, signed + SBOM'd
- Container image stays lean (services only)
- Phase 6 e2e suite targets `ach-cli`
- Docs reflect the split

## Notes / risks

- This refactor compounds with the unmerged `feat/ach-cli-binary-split` branch — coordinate with any in-flight Phase 7 work that may also touch `cmd/ach/cmd/`. If Phase 7 lands first, redo Tasks 3-11 as a single squashed commit on top.
- `CR-01` (httpclient `DisallowUnknownFields`) is independent and not touched here — fix as a separate PR before Phase 7 ships extended envelopes.
- Brew/scoop taps are out of scope — add as discrete follow-up PRs once ach-cli is on GitHub Releases.
