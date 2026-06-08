# Shared git-fetch extraction (sourceserr + gitfetch) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the git-clone engine importable by the k8s-free `ach-cli` binary by hoisting the fetch sentinels into a new `internal/sourceserr` package and the git engine into a new `internal/gitfetch` package — a behavior-preserving refactor used by *both* the operator and (later) the CLI.

**Architecture:** `internal/sources/git` is k8s-free in its own files but imports `internal/sources`, which pulls `k8s.io/api/core/v1` (via `*corev1.Secret` in `sources.go`). Its only real coupling to that package is four sentinel errors. We move the sentinels to a tiny k8s-free package (`internal/sourceserr`), leave behind same-identity aliases in `internal/sources` so every existing `sources.ErrX`/`sources.ReasonOf` call site keeps compiling untouched, then move the git engine to `internal/gitfetch` and repoint it (and its three callers) at `sourceserr`. Result: `go list -deps ./internal/gitfetch` is k8s-free.

**Tech Stack:** Go (module `github.com/ackstorm/ach`); host has NO Go toolchain — every `go`/`gofmt` command runs via `./scripts/dev.sh go ...`; every `make` target auto-routes into the devtools container. SPDX header required on every `*.go` (pre-push gate). Conventional-commit messages.

**This is a refactor, not new-feature work.** There are no new behaviors to test-drive; the *existing* test suites (`internal/sources/...`, `internal/sources/git`, `internal/controller/ach`) are the regression safety net. Each task's "test" steps run those existing suites and assert they stay green, plus a dependency-closure assertion that is the actual new invariant.

---

## File structure

| Path | Responsibility | Action |
|---|---|---|
| `internal/sourceserr/errors.go` | The four fetch sentinels + `ErrUnknownSource` + `ReasonOf` (k8s-free) | Create (moved from `internal/sources/errors.go`) |
| `internal/sourceserr/errors_test.go` | Unit tests for the sentinels + `ReasonOf` | Create (moved) |
| `internal/sources/errors.go` | Same-identity alias shim re-exporting `sourceserr.*` | Replace (becomes a thin shim) |
| `internal/gitfetch/{fetcher,lsremote,doc}.go` | Git clone/checkout/subtree/auth engine | Create (moved from `internal/sources/git/`) |
| `internal/gitfetch/fetcher_test.go` | Git engine unit tests | Create (moved) |
| `internal/sources/gitprovider/gitprovider.go` + `_test.go` | Operator git-transport flow; calls the engine | Modify (repoint import) |
| `internal/controller/ach/marketplace_dispatch.go` + `_test.go` | PluginMarketplace Stage-2 dispatch; calls the engine | Modify (repoint import) |
| `internal/controller/ach/pluginmarketplace_envtest_test.go` | envtest harness; references the engine | Modify (repoint import) |

---

## Task 1: Extract sentinels to `internal/sourceserr` + alias shim

**Files:**
- Create: `internal/sourceserr/errors.go` (moved from `internal/sources/errors.go`)
- Create: `internal/sourceserr/errors_test.go` (moved from `internal/sources/errors_test.go`)
- Replace: `internal/sources/errors.go` (new alias shim)

- [ ] **Step 1: Move the sentinel file and its test, rename the package**

The definitions move verbatim (error strings unchanged — they are part of the wire/log surface). Only the package clause changes.

```bash
cd /home/coder/workspace/local/ach
mkdir -p internal/sourceserr
git mv internal/sources/errors.go      internal/sourceserr/errors.go
git mv internal/sources/errors_test.go internal/sourceserr/errors_test.go
```

In `internal/sourceserr/errors.go` change the package clause only:

```go
// SPDX-License-Identifier: Apache-2.0

package sourceserr
```

(everything below the package line — the `var (...)` block and `func ReasonOf` — stays byte-for-byte identical, including the `"sources: ..."` error strings.)

- [ ] **Step 2: Re-home the test into the new package**

`internal/sources/errors_test.go` is an external test (`package sources_test`) that qualifies symbols with `sources.`. Re-home it onto `sourceserr`:

```bash
cd /home/coder/workspace/local/ach
sed -i \
  -e 's#package sources_test#package sourceserr_test#' \
  -e 's#"github.com/ackstorm/ach/internal/sources"#"github.com/ackstorm/ach/internal/sourceserr"#' \
  -e 's#\bsources\.#sourceserr.#g' \
  internal/sourceserr/errors_test.go
```

If the test file was instead `package sources` (internal test), the `sed` for `package` line is a no-op and the `sources.` qualifier stripping still leaves valid code; either way Step 5 compiles+runs it.

- [ ] **Step 3: Write the alias shim at `internal/sources/errors.go`**

Create a fresh `internal/sources/errors.go` (the path is now empty after the `git mv`) containing only re-exports. Identity is preserved (the `var` copies the same `*errorString` pointer), so `errors.Is` across the alias is exact.

```go
// SPDX-License-Identifier: Apache-2.0

package sources

import "github.com/ackstorm/ach/internal/sourceserr"

// Sentinel errors live in the k8s-free internal/sourceserr package so that
// internal/gitfetch (and the ach-cli binary) can classify fetch failures
// without importing this package — which pulls k8s.io/api/core/v1 via the
// *corev1.Secret in sources.go. These aliases keep every existing
// `sources.ErrXxx` / `sources.ReasonOf` call site compiling unchanged;
// identity is preserved, so errors.Is across the alias is exact.
var (
	ErrUnauthorized    = sourceserr.ErrUnauthorized
	ErrNotFound        = sourceserr.ErrNotFound
	ErrUnreachable     = sourceserr.ErrUnreachable
	ErrUpstreamInvalid = sourceserr.ErrUpstreamInvalid
	ErrUnknownSource   = sourceserr.ErrUnknownSource
)

// ReasonOf re-exports sourceserr.ReasonOf — see that package for the
// Hub §6.6 SourceReachable.reason contract.
func ReasonOf(err error) string { return sourceserr.ReasonOf(err) }
```

- [ ] **Step 4: Format and build the whole tree (regression safety net)**

```bash
cd /home/coder/workspace/local/ach
./scripts/dev.sh gofmt -w internal/sourceserr/errors.go internal/sourceserr/errors_test.go internal/sources/errors.go
./scripts/dev.sh go build ./...
```
Expected: builds clean, no output. (The git engine still imports `internal/sources`; the aliases satisfy it. Nothing else changed.)

- [ ] **Step 5: Run the moved tests + the sources registry tests (they use the aliases)**

```bash
cd /home/coder/workspace/local/ach
make test-unit-pkg PKG=./internal/sourceserr/...
make test-unit-pkg PKG=./internal/sources/registry/...
```
Expected: both PASS. `sourceserr` exercises `ReasonOf` + sentinels directly; `sources/registry` exercises `sources.ReasonOf`/`sources.ErrUnknownSource` through the alias shim.

- [ ] **Step 6: Assert the new package is k8s-free**

```bash
cd /home/coder/workspace/local/ach
./scripts/dev.sh go list -deps ./internal/sourceserr | grep -E 'k8s.io|sigs.k8s.io/controller-runtime' || echo "CLEAN: no k8s deps"
```
Expected: `CLEAN: no k8s deps`.

- [ ] **Step 7: Commit**

```bash
cd /home/coder/workspace/local/ach
git add internal/sourceserr/ internal/sources/errors.go
git commit -m "refactor(sources): extract fetch sentinels to k8s-free internal/sourceserr

Move the four SourceReachable sentinels + ReasonOf into a new k8s-free
package; leave same-identity aliases in internal/sources so all existing
call sites compile unchanged. Prepares internal/gitfetch to drop its
transitive k8s.io/api dependency."
```

---

## Task 2: Move the git engine to `internal/gitfetch` and repoint all callers (atomic)

This task keeps the tree building green by moving the engine **and** updating its three callers in a single commit. (Leaving the move and the caller-repoint in separate commits would leave the tree non-building in between.)

**Files:**
- Create: `internal/gitfetch/{fetcher,lsremote,doc}.go` + `fetcher_test.go` (moved from `internal/sources/git/`)
- Modify: `internal/sources/gitprovider/gitprovider.go`, `internal/sources/gitprovider/gitprovider_test.go`
- Modify: `internal/controller/ach/marketplace_dispatch.go`, `internal/controller/ach/marketplace_dispatch_test.go`
- Modify: `internal/controller/ach/pluginmarketplace_envtest_test.go`

- [ ] **Step 1: Move the package directory and rename `package git` → `package gitfetch`**

```bash
cd /home/coder/workspace/local/ach
git mv internal/sources/git internal/gitfetch
sed -i 's#^package git$#package gitfetch#' internal/gitfetch/*.go
```

- [ ] **Step 2: Repoint the engine's sentinel import at `sourceserr`**

Only the four `sources.Err*` references and the import line change. The comment `// Result mirrors internal/sources.FetchResult shape.` stays (it correctly names the real `sources.FetchResult`).

```bash
cd /home/coder/workspace/local/ach
sed -i \
  -e 's#"github.com/ackstorm/ach/internal/sources"#"github.com/ackstorm/ach/internal/sourceserr"#' \
  -e 's#\bsources\.Err#sourceserr.Err#g' \
  internal/gitfetch/fetcher.go internal/gitfetch/lsremote.go internal/gitfetch/fetcher_test.go
```

- [ ] **Step 3: Refresh the engine's package doc to the new name/path**

In `internal/gitfetch/doc.go`, update the opening line and the self-reference so the prose matches reality:

```go
// Package gitfetch is the generic git-remote fetcher (Hub §10.1 + TODO §5).
```

and change the closing paragraph's `git.Fetch` reference to `gitfetch.Fetch`. (The rest of the prose is accurate as-is.)

- [ ] **Step 4: Repoint the operator git-transport flow (`gitprovider`)**

```bash
cd /home/coder/workspace/local/ach
sed -i 's#gitsrc "github.com/ackstorm/ach/internal/sources/git"#gitsrc "github.com/ackstorm/ach/internal/gitfetch"#' \
  internal/sources/gitprovider/gitprovider.go \
  internal/sources/gitprovider/gitprovider_test.go
```

Then fix the now-stale cycle note in the `gitprovider.go` package doc (lines ~3-8) — replace the sentence explaining the old taint with:

```go
// Package gitprovider holds the shared git-transport flow used by the
// github / gitlab / bitbucket source fetchers. It lives in its own leaf
// package (not the parent internal/sources) because it composes both
// internal/sources (for FetchResult) and internal/gitfetch — keeping
// FetchViaProvider out of the parent avoids an import cycle.
```

- [ ] **Step 5: Repoint the PluginMarketplace dispatch + envtest harness**

```bash
cd /home/coder/workspace/local/ach
sed -i 's#sourcesgit "github.com/ackstorm/ach/internal/sources/git"#sourcesgit "github.com/ackstorm/ach/internal/gitfetch"#' \
  internal/controller/ach/marketplace_dispatch.go \
  internal/controller/ach/marketplace_dispatch_test.go \
  internal/controller/ach/pluginmarketplace_envtest_test.go
```

(The local alias `sourcesgit` is kept, so every `sourcesgit.Spec`/`.New`/`.LsRemote`/`.AuthScheme`/`.Request`/`.Result` reference in those files resolves unchanged.)

- [ ] **Step 6: Confirm no dangling references to the old path remain**

```bash
cd /home/coder/workspace/local/ach
grep -rn 'internal/sources/git"' --include='*.go' . || echo "CLEAN: no references to internal/sources/git"
```
Expected: `CLEAN: no references to internal/sources/git`.

- [ ] **Step 7: Format and build the whole tree**

```bash
cd /home/coder/workspace/local/ach
./scripts/dev.sh gofmt -w internal/gitfetch/ internal/sources/gitprovider/ internal/controller/ach/marketplace_dispatch.go internal/controller/ach/marketplace_dispatch_test.go internal/controller/ach/pluginmarketplace_envtest_test.go
./scripts/dev.sh go build ./...
```
Expected: builds clean, no output.

- [ ] **Step 8: Run the engine + gitprovider unit tests (regression safety net)**

```bash
cd /home/coder/workspace/local/ach
make test-unit-pkg PKG=./internal/gitfetch/...
make test-unit-pkg PKG=./internal/sources/gitprovider/...
```
Expected: both PASS (the moved `fetcher_test.go` clone/checkout/subtree/auth cases and the gitprovider scheme/flow cases — identical behavior, new import path).

- [ ] **Step 9: Assert `internal/gitfetch` is now k8s-free (the core invariant of this plan)**

```bash
cd /home/coder/workspace/local/ach
./scripts/dev.sh go list -deps ./internal/gitfetch | grep -E 'k8s.io|sigs.k8s.io/controller-runtime' || echo "CLEAN: gitfetch has no k8s deps"
```
Expected: `CLEAN: gitfetch has no k8s deps`.

- [ ] **Step 10: Commit**

```bash
cd /home/coder/workspace/local/ach
git add internal/gitfetch/ internal/sources/gitprovider/ internal/controller/ach/marketplace_dispatch.go internal/controller/ach/marketplace_dispatch_test.go internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "refactor(git): move git engine to k8s-free internal/gitfetch

Move internal/sources/git -> internal/gitfetch (package git -> gitfetch),
repoint its sentinel import at internal/sourceserr, and update the three
callers (gitprovider, marketplace_dispatch, pluginmarketplace envtest).
go list -deps ./internal/gitfetch is now k8s-free, unblocking reuse from
the ach-cli binary."
```

---

## Task 3: Full gate — prove behavior preserved across operator + lint

The two moves above touch `internal/controller/ach` and `internal/sources`, so the controller envtest suite is the real behavior gate. Run it (and the unit sweep + lint) before the PR; run `make e2e-full` before merge per repo policy.

- [ ] **Step 1: Full unit sweep**

```bash
cd /home/coder/workspace/local/ach
make test-unit
```
Expected: PASS (includes the new `internal/sourceserr` + `internal/gitfetch` packages; excludes `internal/controller` + `test/e2e`).

- [ ] **Step 2: Controller envtest for the affected reconcilers**

```bash
cd /home/coder/workspace/local/ach
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestPluginMarketplace TIMEOUT=15m
```
Expected: PASS. (Focused run covers the marketplace dispatch path that calls the moved engine. Substitute/extend `FOCUS` if the harness uses a different top-level test name; `make test-envtest` runs the full controller suite if you prefer the unfocused gate.)

- [ ] **Step 3: Lint the changed packages**

```bash
cd /home/coder/workspace/local/ach
make qa-lint-changed
```
Expected: PASS (SPDX headers present on every moved/created file; no unused imports left behind).

- [ ] **Step 4: Belt-and-braces — confirm the ach-cli boundary is unaffected**

The CLI doesn't import these packages yet, but assert the binary is still k8s-free so the invariant is protected as the baseline for the Phase 2 plan.

```bash
cd /home/coder/workspace/local/ach
./scripts/dev.sh go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|sigs.k8s.io/controller-runtime' || echo "CLEAN: ach-cli has no k8s deps"
```
Expected: `CLEAN: ach-cli has no k8s deps`.

- [ ] **Step 5: Pre-merge e2e gate (per CLAUDE.md — touched internal/controller + internal/sources)**

```bash
cd /home/coder/workspace/local/ach
make e2e-full          # ~10 min; cluster kept up after. Reclaim with: make cluster-down
```
Expected: green e2e run. This is the mandatory local gate for any change to `internal/controller`/`internal/sources`; there is no behavior change, so it must pass unchanged.

- [ ] **Step 6: Open the PR**

```bash
cd /home/coder/workspace/local/ach
git push -u origin HEAD        # the installed pre-push hook runs the 18-gate check
gh pr create --fill --base main
```
PR description must note: *behavior-preserving refactor; touched `internal/controller` + `internal/sources`; envtest + `make e2e-full` green.*

---

## Self-review notes

- **Spec coverage:** This plan implements design §1a (`sourceserr`) + §1b (`gitfetch`) from `~/.claude/plans/resilient-weaving-stream.md`. Design §1c (`internal/contentkit`) and all of Phase 2 (the `ach-cli` feature) are **out of scope here** and get their own plans — `contentkit` needs its own move-cleanliness verification, and the CLI feature depends on these finalized exported signatures (`gitfetch.New`/`Fetch`/`LsRemote`/`AuthScheme`).
- **Type consistency:** the engine's exported surface (`Spec`, `Request`, `Result`, `Fetcher`, `New`, `LsRemote`, `AuthScheme`, `AuthBearer`, `AuthBasicOAuth2`) is unchanged — only its package name (`git` → `gitfetch`) and one import (`sources` → `sourceserr`) move. Caller aliases (`gitsrc`, `sourcesgit`) are preserved, so no call-site symbol references change.
- **No placeholders:** every edit is an exact `sed`/file body; every verification is an exact command with expected output.
- **Known follow-ups (not this plan):** `internal/contentkit` extraction; `ach-cli` `repo`/`plugin`/`skill` commands + `env` reorg.
```
