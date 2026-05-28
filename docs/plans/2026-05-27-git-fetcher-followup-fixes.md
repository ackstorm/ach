# Git Fetcher Follow-up Fixes — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix the 8 actionable code-review findings against PR #9 (the git-protocol transport swap merged at `81c4391`). Closes the validation-parity gap that lets crafted CRs reach `git` subprocess args unescaped (HIGH), adds the missing `LsRemote` deadline (MED), hardens the temp-clone nonce (MED), refactors the footgun-shaped `buildGitInvocation` signature (MED), plus four LOW-severity cleanups (host case-insensitive normalization, transport label switch on `Type` not pointer, error message when key was defaulted, git protocol allow-list pin).

**Architecture:** Single follow-up PR on a worktree off `main`, atomic commit per finding, TDD throughout (red → green → commit). Most fixes are 1-3 file edits; no controller-shape change; no CRD shape change. The fixes preserve every existing public/exported API.

**Tech Stack:** Go (`os/exec`, `crypto/rand`, `context`), controller-runtime, the same toolchain conventions PR #9 used (`./scripts/dev.sh make ...`, `make pre-push` from primary worktree).

**Out of scope (explicit):**
- The 8 pre-existing `TestPMR_Stage1_*` / `TestPMR_Stage2_*` envtest failures (verified independent of this stack — separate PR).
- Removing the `transport: rest` legacy escape hatch (deferred per PR #9 plan — one release after `git` transport is observed clean).
- Any new behavior. This PR is hardening + bug fixes only.
- Refactoring `buildSourceSpec` to deduplicate by `Type` — Task 5's switch-on-Type is a one-line behavioral fix; structural dedup is a separate concern.

---

## Pre-flight context for the implementer

Read in this order before touching code:

1. **`docs/plans/2026-05-27-git-fetcher-protocol-swap.md`** — the parent plan that produced PR #9. Has the architecture diagram + per-provider auth-shape distillation.
2. **`FIX_GIT.txt`** at repo root — original motivation for the transport swap. Same rate-limit math applies; nothing in this PR changes that.
3. **`internal/sources/bitbucket/fetcher.go`** L88-112 — `validateFlatIdentifier` + `validateRefIdentifier`. These are the reference shapes the github + gitlab CR-02 validation MUST mirror (Task 1).
4. **`internal/sources/git/fetcher.go`** — current state post-PR-#9. The shared engine. Tasks 2, 3, 4, 8 modify this file.
5. **`internal/sources/git/lsremote.go`** — added in PR #9. Task 2 adds the missing timeout.
6. **`internal/controller/ach/conditions.go`** — `resolveTransportName` helper. Task 6 switches its dispatch from pointer-presence to `sourceSpec.Type`.
7. **`internal/sources/registry/registry.go`** — confirms `Type` is the registry's dispatch discriminator; the fetcher actually invoked is selected by `Type`, not by pointer presence.

Verify the toolchain works before starting:

```bash
./scripts/dev.sh go build ./...
./scripts/dev.sh make unit
```

If those don't run cleanly with no edits, STOP and fix the environment first. Every Go invocation goes through `./scripts/dev.sh`.

---

## Worktree setup (required first step)

Per CLAUDE.md global rule + parent plan convention: never work on `main` directly. Set up worktree first.

```bash
cd /home/jcm/Projects/ach
# .worktrees/ is already in .gitignore from PR #9.
git worktree add .worktrees/git-fetcher-followup -b feat/git-fetcher-followup-fixes
cd .worktrees/git-fetcher-followup
cp /home/jcm/Projects/ach/docs/plans/2026-05-27-git-fetcher-followup-fixes.md docs/plans/
```

All subsequent file paths below are relative to `/home/jcm/Projects/ach/.worktrees/git-fetcher-followup/` unless otherwise specified.

---

## Task 1: Refactor `buildGitInvocation` to explicit-token signature

**Why:** Current signature `buildGitInvocation(subcommand string, args ...string)` treats the LAST variadic arg as the token. Callers forgetting to pass the trailing token put a real arg in the token slot — token leaks into a non-auth slot OR a real arg becomes the auth header value. Compiler can't catch. The explicit signature `buildGitInvocation(subcommand, token string, args ...string)` makes the contract positional and compile-checkable.

This refactor closes BOTH finding #4 (signature footgun) and is the prerequisite for Task 4 (closing `lsremote.go` caller against the same footgun).

**Files:**
- Modify: `internal/sources/git/fetcher.go` — `buildGitInvocation` signature + every caller (`runGit`).
- Modify: `internal/sources/git/lsremote.go` — single `buildGitInvocation` call site.
- Modify: `internal/sources/git/fetcher_test.go` — `TestFetcher_AuthHeader_NotURL` + `TestFetcher_AuthHeader_EmptyTokenNoArg` (signature-shape tests).

### Step 1: Update the two existing signature-shape tests to the new shape

Both tests already call `buildGitInvocation` directly. Update them to the new signature (this is the failing-test step — they'll fail to compile against current production code).

```go
// In internal/sources/git/fetcher_test.go:

func TestFetcher_AuthHeader_NotURL(t *testing.T) {
	args := buildGitInvocation(
		"clone",
		"ghp_secrettoken",  // token NOW second positional
		"https://github.com/octo/repo.git",
		"/tmp/x",
	)
	// existing assertions unchanged
}

func TestFetcher_AuthHeader_EmptyTokenNoArg(t *testing.T) {
	args := buildGitInvocation("clone", "", "https://example.com/x.git", "/tmp/x")
	// existing assertions unchanged
}
```

Run: `./scripts/dev.sh go test ./internal/sources/git/ -run TestFetcher_AuthHeader`
Expected: BUILD FAIL — wrong number of args to `buildGitInvocation`.

### Step 2: Refactor `buildGitInvocation` to explicit-token signature

In `internal/sources/git/fetcher.go`, replace the helper:

```go
// buildGitInvocation returns the full args slice for a git subcommand.
// token, when non-empty, is prepended as
//
//	-c http.extraHeader=Authorization: Bearer <token>
//
// so it never appears in the URL position of any arg (the URL form
// would persist on disk via `git config remote.origin.url` AND remain
// visible in /proc/<pid>/cmdline). The extraHeader value itself is
// also in cmdline for the duration of the subprocess, which is
// unavoidable without GIT_ASKPASS plumbing — but it is colocated in
// one auditable arg slot and is redacted by redactArgs in any logs.
//
// token is positional + mandatory so callers cannot accidentally
// forget it (which under the previous variadic-last convention would
// silently put a real arg in the token slot — see PR #9 follow-up
// review finding #4).
func buildGitInvocation(subcommand, token string, args ...string) []string {
	if token == "" {
		return append([]string{subcommand}, args...)
	}
	prefix := []string{"-c", "http.extraHeader=Authorization: Bearer " + token, subcommand}
	return append(prefix, args...)
}
```

### Step 3: Update `runGit` to call new signature

In the same file, replace `runGit`:

```go
// runGit runs a git subcommand without --recurse-submodules (security:
// arbitrary git submodule URLs in a marketplace plugin would be a
// remote-fetch primitive). Inherits ctx for the wall-clock cap.
//
// token, when non-empty, lands as -c http.extraHeader=Authorization:
// Bearer <token> via buildGitInvocation. Pass "" for purely local
// subcommands that don't touch the remote (e.g. checkout).
func runGit(ctx context.Context, workdir, token, subcommand string, args ...string) error {
	full := buildGitInvocation(subcommand, token, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_HTTP_LOW_SPEED_LIMIT=1000",
		"GIT_HTTP_LOW_SPEED_TIME=60",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %v: %s", redactArgs(full), err, truncateBytes(out, 512))
	}
	return nil
}
```

### Step 4: Update `LsRemote` to call new signature

In `internal/sources/git/lsremote.go`:

```go
// Inside LsRemote, replace:
//   full := buildGitInvocation("ls-remote", "--refs", url, ref, authToken)
// with:
full := buildGitInvocation("ls-remote", authToken, "--refs", url, ref)
```

### Step 5: Run tests + commit

```bash
./scripts/dev.sh go test ./internal/sources/git/ -v
```
Expected: PASS (all 11 existing tests + the updated two).

```bash
./scripts/dev.sh go build ./...
```
Expected: exit 0.

```bash
git add internal/sources/git/
git commit -m "refactor(sources/git): buildGitInvocation explicit-token signature

Before: buildGitInvocation(subcommand string, args ...string) — last
variadic element interpreted as token. Caller forgetting trailing \"\"
silently puts a real arg in the token slot (token leaks into wrong
position OR ref becomes the auth header value). Compiler can't catch.

After: buildGitInvocation(subcommand, token string, args ...string) —
token is positional + mandatory. Compiler-enforced.

runGit + LsRemote call sites updated. Behavior identical; this is
purely a signature shape change to close PR #9 review finding #4.

Refs: PR #9 follow-up review"
```

---

## Task 2: Add timeout to `LsRemote`

**Why:** `LsRemote` has no inner `context.WithTimeout` — the caller's ctx may be unbounded. `Fetcher.Fetch` installs `gitCloneTimeout = 5 * time.Minute` but `LsRemote` is invoked BEFORE that wrapper, with the caller's outer ctx. A stalled upstream that accepts TCP then never replies hangs until `GIT_HTTP_LOW_SPEED_TIME=60` (seconds at <1000B/s) — much longer than the implicit user expectation that LsRemote is "cheap".

**Files:**
- Modify: `internal/sources/git/lsremote.go`.
- Modify: `internal/sources/git/fetcher_test.go` — new test pinning the timeout behavior via a fake unresponsive server.

### Step 1: Write the failing timeout test

In `internal/sources/git/fetcher_test.go`, append:

```go
// TestLsRemote_RespectsInnerTimeout asserts that even with a long
// (or absent) caller ctx, LsRemote bounds the subprocess via its
// own internal deadline so a stalled upstream cannot hang the
// reconciler indefinitely.
func TestLsRemote_RespectsInnerTimeout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	// Hangs forever — accepts TCP, never sends a byte. git ls-remote
	// would block on GIT_HTTP_LOW_SPEED_TIME (60s) without an inner
	// deadline.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection; never reply.
			defer c.Close()
		}
	}()
	url := "http://" + ln.Addr().String() + "/x.git"

	start := time.Now()
	_, err = LsRemote(context.Background(), url, "main", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from stalled upstream")
	}
	// Inner deadline should be ~30s. Give a generous wall-clock cap
	// to avoid flake on slow CI.
	if elapsed > 45*time.Second {
		t.Errorf("LsRemote took %v; inner deadline should fire well before 45s", elapsed)
	}
}
```

Add the `net` import to the file's import block.

Run: `./scripts/dev.sh go test -timeout 90s -run TestLsRemote_RespectsInnerTimeout ./internal/sources/git/`
Expected: FAIL with elapsed > 45s OR test timeout at 90s.

### Step 2: Add the inner timeout

In `internal/sources/git/lsremote.go`, add a constant at file top:

```go
// lsRemoteTimeout bounds an individual ls-remote subprocess. The
// caller's ctx may not carry a deadline (some controllers pass
// context.Background()); this internal wrapper guarantees an upper
// bound so a stalled upstream cannot block the reconciler. 30s is
// generous for a one-round-trip protocol exchange.
const lsRemoteTimeout = 30 * time.Second
```

Add `"time"` to the imports if not already present.

Then in `LsRemote`, wrap immediately:

```go
func LsRemote(ctx context.Context, url, ref, authToken string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()

	full := buildGitInvocation("ls-remote", authToken, "--refs", url, ref)
	// ... rest unchanged
}
```

### Step 3: Run test + commit

```bash
./scripts/dev.sh go test -timeout 90s -run TestLsRemote_RespectsInnerTimeout ./internal/sources/git/
```
Expected: PASS in <45s.

```bash
./scripts/dev.sh go test ./internal/sources/git/
```
Expected: full pkg green.

```bash
git add internal/sources/git/
git commit -m "fix(sources/git): bound LsRemote with inner 30s deadline

Caller may pass an unbounded ctx (some reconcilers use
context.Background() at the top of the fetch path). Without an inner
deadline, git ls-remote against a stalled upstream blocks until
GIT_HTTP_LOW_SPEED_TIME=60s threshold, much longer than the implicit
user expectation that ls-remote is cheap.

Test asserts the call returns in well under 45s against an
unresponsive TCP listener.

Closes PR #9 follow-up review finding #3.

Refs: PR #9 follow-up review"
```

---

## Task 3: Harden temp-clone nonce against `crypto/rand` failure

**Why:** `internal/sources/git/fetcher.go` line ~138 does `_, _ = rand.Read(nonce)` — error ignored. If `crypto/rand` fails (minimal containers with seccomp blocking `getrandom(2)` and no `/dev/urandom` fallback), nonce stays zeroed, `cloneDir` becomes `.tmp/git-0000000000000000`. Concurrent reconciles collide. On a shared cache PVC with multi-tenant namespaces, an attacker controlling another tenant could pre-create that path as a symlink → arbitrary write target during `git clone`. Defense in depth.

**Files:**
- Modify: `internal/sources/git/fetcher.go`.
- Modify: `internal/sources/git/fetcher_test.go` — new test (via `os.MkdirTemp` if we swap, OR via a stubbed rand source).

**Approach choice:** The cleanest fix is to swap the manual `rand.Read` + nonce + `MkdirAll` to `os.MkdirTemp(tmpParent, "git-*")` which already handles randomness-or-error correctly and uses the same secure RNG. Smaller change, no error-path divergence.

### Step 1: Write the failing test

In `internal/sources/git/fetcher_test.go`, append:

```go
// TestFetcher_TempDirCollisionResistance asserts that two parallel
// Fetch calls against the same CacheRoot allocate distinct cloneDirs
// (PR #9 follow-up review finding #6: defense against a zeroed-nonce
// collision when crypto/rand silently fails). Uses a real Fetch
// against a local bare-repo fixture in parallel; if both calls
// shared a cloneDir name the second clone would race on EEXIST or
// overwrite.
func TestFetcher_TempDirCollisionResistance(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupBareFixture(t)
	cacheRoot := t.TempDir()

	const parallel = 8
	errs := make(chan error, parallel)
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := New(Spec{
				URL:       bare,
				Ref:       "main",
				SHA:       fixtureHeadSHA(t, bare),
				CacheRoot: cacheRoot,
			})
			res, err := f.Fetch(context.Background(), Request{})
			if err != nil {
				errs <- err
				return
			}
			_ = res.Body.Close()
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("parallel Fetch failed: %v", err)
		}
	}
}
```

Add `"sync"` to imports if not already present.

Run: `./scripts/dev.sh go test -run TestFetcher_TempDirCollisionResistance ./internal/sources/git/`
Expected: PASS (current code happens to work because crypto/rand reads succeed; the test is regression-protection for the upcoming refactor).

This test is regression-protection, not a red-now case — it would only fail if we BROKE collision resistance. Acceptable: it pins the invariant.

### Step 2: Swap manual nonce to `os.MkdirTemp`

In `internal/sources/git/fetcher.go`, replace the temp-dir block:

```go
// BEFORE:
nonce := make([]byte, 8)
_, _ = rand.Read(nonce)
cloneDir := filepath.Join(tmpParent, "git-"+hex.EncodeToString(nonce))
if err := os.MkdirAll(cloneDir, 0o755); err != nil {
	return nil, fmt.Errorf("git: mkdir clone dir: %w", err)
}

// AFTER:
cloneDir, err := os.MkdirTemp(tmpParent, "git-*")
if err != nil {
	return nil, fmt.Errorf("git: mkdir clone dir: %w", err)
}
```

Remove now-unused imports: `crypto/rand`, `encoding/hex`. (Confirm via `go build` after the edit — Go's compiler catches unused imports.)

### Step 3: Run tests + commit

```bash
./scripts/dev.sh go test ./internal/sources/git/
```
Expected: PASS.

```bash
./scripts/dev.sh go build ./...
```
Expected: exit 0.

```bash
git add internal/sources/git/
git commit -m "fix(sources/git): use os.MkdirTemp for collision-safe clone dir

Before: manual 8-byte crypto/rand nonce → hex encode → MkdirAll. The
rand.Read error was ignored — if crypto/rand failed (rare but possible
on minimal containers with seccomp blocking getrandom and no
/dev/urandom fallback), nonce stayed zero-bytes and cloneDir collapsed
to .tmp/git-0000000000000000. Concurrent reconciles collide; on a
shared cache PVC a co-tenant could pre-create that path as a symlink.

After: os.MkdirTemp handles randomness-or-error correctly using the
same secure RNG, AND returns an error when allocation fails so the
fetch returns a wrapped error instead of silently using a predictable
path. crypto/rand + encoding/hex imports dropped.

Parallel-fetch regression test added.

Closes PR #9 follow-up review finding #6.

Refs: PR #9 follow-up review"
```

---

## Task 4: Add CR-02 metachar validation to github + gitlab `New` constructors

**Why:** PR #9 review HIGH finding. bitbucket validates `spec.Workspace`, `spec.Repo`, `spec.Ref` for URL-structural metacharacters (`/`, `?`, `#`, `\`, whitespace) in `bitbucket.New` (fetcher.go:79-86 via `validateFlatIdentifier` / `validateRefIdentifier`). github + gitlab don't validate. Crafted CRs reach `git ls-remote` args + the Sprintf clone URL unescaped. No host hijack (host is hardcoded to `github.com` / `gitlab.com`/`spec.Host`), but path-shape, newline injection, and arg-smuggling vectors exist.

**Files:**
- Create: `internal/sources/cr02validate/validate.go` — shared validators extracted to a small subpackage so all three providers can use one definition (no copy-paste).
- Create: `internal/sources/cr02validate/validate_test.go` — unit tests.
- Modify: `internal/sources/github/fetcher.go` — call validators in `New`.
- Modify: `internal/sources/gitlab/fetcher.go` — call validators in `New`.
- Modify: `internal/sources/bitbucket/fetcher.go` — replace inline `validateFlatIdentifier`/`validateRefIdentifier` with the shared package (single source of truth).
- Test: per-provider test files — add cases asserting `New` rejects each metacharacter category.

### Step 1: Extract the validators to a shared subpackage

Create `internal/sources/cr02validate/validate.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Package cr02validate provides URL-metacharacter rejection helpers
// for git source CR fields (CR-02 mitigation). Used by the three
// per-provider source fetchers (github/gitlab/bitbucket) to ensure
// user-supplied spec.{Workspace,Repo,Project,Host,Ref} cannot smuggle
// query strings, fragments, path traversals, or whitespace into the
// constructed clone URL OR into the git subprocess argv.
//
// The bitbucket fetcher introduced these helpers inline in v1alpha1;
// extracting them here ensures github + gitlab apply identical rules
// (PR #9 follow-up review finding #1).
package cr02validate

import (
	"fmt"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// FlatIdentifier rejects URL-structural metacharacters in a flat
// identifier (workspace / repo / project namespace segment). Forbidden:
//
//	/  ?  #  \  space  tab  CR  LF
//
// Returns wrapped sources.ErrUpstreamInvalid on failure. The `field`
// argument names the offending CR field for the error message
// (operator-readable).
func FlatIdentifier(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty: %w", field, sources.ErrUpstreamInvalid)
	}
	if strings.ContainsAny(value, "/?#\\ \t\r\n") {
		return fmt.Errorf("%s %q contains forbidden URL metacharacter: %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	return nil
}

// RefIdentifier permits '/' (feature/branch shapes are legal git refs)
// but otherwise rejects the same metacharacter set as FlatIdentifier.
// Forbidden:
//
//	?  #  \  space  tab  CR  LF
func RefIdentifier(value string) error {
	if value == "" {
		return fmt.Errorf("ref must not be empty: %w", sources.ErrUpstreamInvalid)
	}
	if strings.ContainsAny(value, "?#\\ \t\r\n") {
		return fmt.Errorf("ref %q contains forbidden URL metacharacter: %w",
			value, sources.ErrUpstreamInvalid)
	}
	return nil
}

// RepoSlashIdentifier validates a `<owner>/<name>`-style two-segment
// identifier (e.g. github spec.Repo, gitlab spec.Project). Splits on
// the first '/', then runs FlatIdentifier on each half. The split
// itself permits exactly ONE '/' separator; multiple slashes
// (`a/b/c`) on github are rejected, while gitlab projects can have
// deeper namespaces — call sites pass `allowMultiSegment=true` for
// gitlab.
func RepoSlashIdentifier(field, value string, allowMultiSegment bool) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty: %w", field, sources.ErrUpstreamInvalid)
	}
	// Disallow the metachars FlatIdentifier would on the whole string
	// EXCEPT '/'.
	if strings.ContainsAny(value, "?#\\ \t\r\n") {
		return fmt.Errorf("%s %q contains forbidden URL metacharacter: %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	parts := strings.Split(value, "/")
	if !allowMultiSegment && len(parts) != 2 {
		return fmt.Errorf("%s %q must be exactly <segment>/<segment>: %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	if allowMultiSegment && len(parts) < 2 {
		return fmt.Errorf("%s %q must contain at least one '/': %w",
			field, value, sources.ErrUpstreamInvalid)
	}
	for _, seg := range parts {
		if seg == "" {
			return fmt.Errorf("%s %q has empty segment: %w",
				field, value, sources.ErrUpstreamInvalid)
		}
	}
	return nil
}

// HostIdentifier validates a hostname-like spec.Host (gitlab only).
// Accepts `gitlab.com`, `gitlab.example.com`, optionally with port
// `gitlab.example.com:8080`. Rejects scheme prefixes (those are
// stripped at construction time), paths, queries, fragments,
// whitespace. Optional empty (the caller will substitute the
// per-provider default).
func HostIdentifier(value string) error {
	if value == "" {
		// Empty host means "use the provider default" — allowed.
		return nil
	}
	if strings.ContainsAny(value, "/?#\\ \t\r\n") {
		return fmt.Errorf("host %q contains forbidden URL metacharacter: %w",
			value, sources.ErrUpstreamInvalid)
	}
	return nil
}
```

### Step 2: Write the failing validator unit tests

Create `internal/sources/cr02validate/validate_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package cr02validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

func TestFlatIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		wantOK      bool
	}{
		{"happy", "acme", true},
		{"empty", "", false},
		{"slash", "ac/me", false},
		{"question", "ac?me", false},
		{"fragment", "ac#me", false},
		{"backslash", "ac\\me", false},
		{"space", "ac me", false},
		{"tab", "ac\tme", false},
		{"cr", "ac\rme", false},
		{"lf", "ac\nme", false},
		{"unicode-fine", "ácmé", true}, // non-ASCII fine
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := FlatIdentifier("field", tc.value)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
			if !tc.wantOK && !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("err should wrap ErrUpstreamInvalid: %v", err)
			}
		})
	}
}

func TestRefIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		wantOK      bool
	}{
		{"plain", "main", true},
		{"feature-slash", "feature/branch", true}, // '/' allowed in ref
		{"tag-with-dots", "v1.2.3", true},
		{"empty", "", false},
		{"newline", "main\n", false},
		{"question", "main?evil", false},
		{"fragment", "main#anchor", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RefIdentifier(tc.value)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
		})
	}
}

func TestRepoSlashIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		value             string
		allowMultiSegment bool
		wantOK            bool
	}{
		{"github-happy", "octocat/repo", false, true},
		{"gitlab-deep", "group/sub/project", true, true},
		{"github-deep-rejected", "a/b/c", false, false},
		{"empty-segment", "owner/", false, false},
		{"leading-slash", "/repo", false, false},
		{"newline", "owner/repo\n", false, false},
		{"question", "owner/repo?evil=1", false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RepoSlashIdentifier("repo", tc.value, tc.allowMultiSegment)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
			if !tc.wantOK && err != nil && !strings.Contains(err.Error(), tc.value) {
				t.Errorf("err should mention the offending value; got %q", err.Error())
			}
		})
	}
}

func TestHostIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		wantOK      bool
	}{
		{"empty-ok", "", true},
		{"saas", "gitlab.com", true},
		{"self-hosted", "gitlab.example.com", true},
		{"with-port", "gitlab.example.com:8080", true},
		{"with-path", "gitlab.example.com/foo", false},
		{"with-scheme-stripped-by-caller", "https://gitlab.example.com", false}, // / forbids
		{"newline", "gitlab.example.com\n", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := HostIdentifier(tc.value)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
		})
	}
}
```

Run: `./scripts/dev.sh go test ./internal/sources/cr02validate/`
Expected: PASS (all helpers + tests are new; no prior code to fail against).

### Step 3: Adopt the shared package in `bitbucket.New`

In `internal/sources/bitbucket/fetcher.go`:

- Remove inline `validateFlatIdentifier` + `validateRefIdentifier` definitions (L88-112).
- Import `"github.com/ackstorm/ach/internal/sources/cr02validate"`.
- Replace the constructor's three validation calls:

```go
if err := cr02validate.FlatIdentifier("bitbucket.workspace", spec.Workspace); err != nil {
	return nil, fmt.Errorf("bitbucket: %w", err)
}
if err := cr02validate.FlatIdentifier("bitbucket.repo", spec.Repo); err != nil {
	return nil, fmt.Errorf("bitbucket: %w", err)
}
if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
	return nil, fmt.Errorf("bitbucket: %w", err)
}
```

Run: `./scripts/dev.sh go test ./internal/sources/bitbucket/`
Expected: PASS (the pre-existing bitbucket validation tests still pin the behavior; only the implementation moved).

### Step 4: Add validators to `github.New`

In `internal/sources/github/fetcher.go`:

Add import `"github.com/ackstorm/ach/internal/sources/cr02validate"`.

In `New`, after the nil-spec check:

```go
if err := cr02validate.RepoSlashIdentifier("github.repo", spec.Repo, false); err != nil {
	return nil, fmt.Errorf("github: %w", err)
}
if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
	return nil, fmt.Errorf("github: %w", err)
}
```

(github repos are exactly `<owner>/<name>` — `allowMultiSegment=false`.)

### Step 5: Add validators to `gitlab.New`

In `internal/sources/gitlab/fetcher.go`:

Add import `"github.com/ackstorm/ach/internal/sources/cr02validate"`.

In `New`, after nil-spec check:

```go
if err := cr02validate.HostIdentifier(spec.Host); err != nil {
	return nil, fmt.Errorf("gitlab: %w", err)
}
if err := cr02validate.RepoSlashIdentifier("gitlab.project", spec.Project, true); err != nil {
	return nil, fmt.Errorf("gitlab: %w", err)
}
if err := cr02validate.RefIdentifier(spec.Ref); err != nil {
	return nil, fmt.Errorf("gitlab: %w", err)
}
```

(gitlab projects can be deeply nested — `allowMultiSegment=true`.)

### Step 6: Add per-provider New-rejection tests

In `internal/sources/github/fetcher_test.go`, append:

```go
// TestNew_RejectsMetacharRepo asserts CR-02 mitigation: crafted Repo
// values with URL-structural metacharacters are rejected at New time,
// never reaching the git subprocess.
func TestNew_RejectsMetacharRepo(t *testing.T) {
	t.Parallel()
	cases := []string{
		"owner/repo\n",
		"owner/repo?evil=1",
		"owner/repo#frag",
		"owner/repo with space",
		"a/b/c", // github rejects 3+ segments
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			_, err := New(&achv1alpha1.GitHubSource{Repo: c, Ref: "main"})
			if err == nil {
				t.Errorf("expected New to reject %q", c)
			}
			if err != nil && !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
			}
		})
	}
}

func TestNew_RejectsMetacharRef(t *testing.T) {
	t.Parallel()
	cases := []string{"main\n", "main?evil", "main#frag"}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			_, err := New(&achv1alpha1.GitHubSource{Repo: "owner/repo", Ref: c})
			if err == nil {
				t.Errorf("expected New to reject ref %q", c)
			}
		})
	}
}
```

Add the analogous tests in `internal/sources/gitlab/fetcher_test.go` (substituting `GitLabSource`, `Project`, and including a Host-metachar case).

### Step 7: Run all tests + commit

```bash
./scripts/dev.sh go test ./internal/sources/...
```
Expected: PASS (all packages, including the new cr02validate).

```bash
./scripts/dev.sh make lint-changed
```
Expected: clean.

```bash
git add internal/sources/
git commit -m "feat(sources): CR-02 metachar validation parity on github/gitlab

bitbucket already rejected URL-structural metacharacters in
spec.{Workspace,Repo,Ref} at New time (CR-02 mitigation). github and
gitlab did not — crafted CRs with metacharacters in spec.Repo /
spec.Project / spec.Ref / spec.Host reached the constructed clone URL
+ git subprocess argv unescaped. No host-hijack (host fixed at
github.com / gitlab.com or spec.Host respectively) but path-shape
smuggling, newline injection, and arg confusion were live vectors.

Validators extracted to a shared internal/sources/cr02validate
subpackage so all three providers share one definition. Bitbucket's
inline copies are dropped; github + gitlab adopt the same helpers.
The bitbucket-side migration preserves all pre-existing behavior
(validated by the existing CR-02 unit tests).

Per-provider New-rejection tests added for github + gitlab covering
each metacharacter category.

Closes PR #9 follow-up review finding #1 (HIGH).

Refs: PR #9 follow-up review"
```

---

## Task 5: Case-insensitive host normalization on `gitlab.constructCloneURL`

**Why:** `strings.TrimPrefix(host, "https://")` is case-sensitive. `HTTPS://example.com` passes through unscrubbed, yielding `https://HTTPS://example.com/<project>.git` — git rejects, but the error surface is confusing. Trivial fix; bundled here so the gitlab module's hardening lands in one PR.

**Files:**
- Modify: `internal/sources/gitlab/git_transport.go`.
- Modify: `internal/sources/gitlab/git_transport_test.go` — new test case.

### Step 1: Write the failing test

In `internal/sources/gitlab/git_transport_test.go`, append a sub-case to `TestGitTransport_GitLab_CustomHost` OR add a new test:

```go
func TestGitTransport_GitLab_HostCaseInsensitive(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"https://gitlab.example.com", "https://gitlab.example.com/acme/widgets.git"},
		{"HTTPS://gitlab.example.com", "https://gitlab.example.com/acme/widgets.git"},
		{"Https://gitlab.example.com", "https://gitlab.example.com/acme/widgets.git"},
		{"http://gitlab.example.com", "https://gitlab.example.com/acme/widgets.git"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.host, func(t *testing.T) {
			f, _ := New(&achv1alpha1.GitLabSource{
				Host:      tc.host,
				Project:   "acme/widgets",
				Ref:       "main",
				Transport: "git",
			})
			if got := f.constructCloneURL(); got != tc.want {
				t.Errorf("host %q → %q; want %q", tc.host, got, tc.want)
			}
		})
	}
}
```

Run: `./scripts/dev.sh go test -run TestGitTransport_GitLab_HostCaseInsensitive ./internal/sources/gitlab/`
Expected: FAIL on the `HTTPS://` and `Https://` cases.

### Step 2: Normalize case before trim

In `internal/sources/gitlab/git_transport.go`, update `constructCloneURL`:

```go
func (f *Fetcher) constructCloneURL() string {
	host := f.spec.Host
	if host == "" {
		host = "gitlab.com"
	}
	// Strip scheme regardless of case so HTTPS:// / Https:// also
	// normalize. We don't accept ws://, ftp://, etc — only http(s).
	low := strings.ToLower(host)
	switch {
	case strings.HasPrefix(low, "https://"):
		host = host[len("https://"):]
	case strings.HasPrefix(low, "http://"):
		host = host[len("http://"):]
	}
	host = strings.TrimRight(host, "/")
	return fmt.Sprintf("https://%s/%s.git", host, f.spec.Project)
}
```

### Step 3: Run + commit

```bash
./scripts/dev.sh go test ./internal/sources/gitlab/
```
Expected: PASS.

```bash
git add internal/sources/gitlab/
git commit -m "fix(sources/gitlab): case-insensitive scheme stripping in clone URL

strings.TrimPrefix is case-sensitive; HTTPS:// passed through
producing https://HTTPS://example.com/<project>.git. Git rejects,
but the error surface was confusing. Now strips any case variant of
http:// or https:// before re-prefixing https://.

Closes PR #9 follow-up review finding #2.

Refs: PR #9 follow-up review"
```

---

## Task 6: `resolveTransportName` switches on `sourceSpec.Type` not pointer presence

**Why:** Today the helper dispatches by pointer-non-nil ordering (github → gitlab → bitbucket → default). CEL admission enforces exactly-one-non-nil per `Type`, but if admission is ever bypassed (alpha feature, conversion bug, finalizer-only update) and multiple per-type pointers are non-nil, the label mismatches the fetcher that actually ran. The registry dispatches on `Type` (see `internal/sources/registry/registry.go`); the label helper should follow the same source of truth.

**Files:**
- Modify: `internal/controller/ach/conditions.go`.
- Modify: `internal/controller/ach/conditions_test.go` — add a "multi-pointer + Type=gitlab" case asserting we report gitlab, not github.

### Step 1: Write the failing test

In `internal/controller/ach/conditions_test.go`, add to `TestResolveTransportName`:

```go
{
	name: "multi-pointer-respects-type",
	spec: sources.SourceSpec{
		Type:   "gitlab",
		GitHub: &achv1alpha1.GitHubSource{Transport: "git"},
		GitLab: &achv1alpha1.GitLabSource{Transport: "rest"},
	},
	want: "rest",
},
```

Run: `./scripts/dev.sh go test -run TestResolveTransportName ./internal/controller/ach/...`
Expected: FAIL — current pointer-order switch hits GitHub first → returns "git" not "rest".

### Step 2: Switch the helper to `Type`-dispatch

In `internal/controller/ach/conditions.go`, replace the helper body:

```go
func resolveTransportName(sourceSpec sources.SourceSpec) string {
	switch sourceSpec.Type {
	case "github":
		if sourceSpec.GitHub != nil && sourceSpec.GitHub.Transport == transportLabelRest {
			return transportLabelRest
		}
		return transportLabelGit
	case "gitlab":
		if sourceSpec.GitLab != nil && sourceSpec.GitLab.Transport == transportLabelRest {
			return transportLabelRest
		}
		return transportLabelGit
	case "bitbucket":
		if sourceSpec.Bitbucket != nil && sourceSpec.Bitbucket.Transport == transportLabelRest {
			return transportLabelRest
		}
		return transportLabelGit
	default:
		return transportLabelNA
	}
}
```

### Step 3: Run + commit

```bash
./scripts/dev.sh go test ./internal/controller/ach/... -run TestResolveTransportName
```
Expected: PASS.

```bash
./scripts/dev.sh go test ./internal/controller/ach/... -run TestSourceReachableMessage
```
Expected: PASS (no regression).

```bash
git add internal/controller/ach/
git commit -m "fix(controller): resolveTransportName switches on Type not pointer

Previously dispatched by per-type-pointer non-nil ordering. The
registry (internal/sources/registry) dispatches by sourceSpec.Type;
the label helper that reports which fetcher ran should follow the
same source of truth. If CEL admission is ever bypassed and multiple
per-type pointers are non-nil, the label now matches the fetcher
actually invoked instead of whichever pointer happens to be checked
first.

Test pins behavior: spec with Type=gitlab + GitHub-pointer-set +
GitLab-pointer-set (Transport=rest) → 'rest', not 'git'.

Closes PR #9 follow-up review finding #8.

Refs: PR #9 follow-up review"
```

---

## Task 7: Distinct error message when `AuthSecretRef.Key` was defaulted

**Why:** When `spec.AuthSecretRef.Key == ""` and the Secret's data map lacks `GITHUB_TOKEN` (the resolved default), the error reads `missing auth secret key "GITHUB_TOKEN"`. An operator who never spelled the key gets confused — they wonder why ACH is asking for "GITHUB_TOKEN" when their CR has no `key:` field. Append a hint when the key was defaulted.

**Files:**
- Modify: `internal/sources/github/fetcher.go` — `extractToken`.
- Modify: `internal/sources/gitlab/fetcher.go` — `extractToken`.
- Modify: `internal/sources/bitbucket/fetcher.go` — `extractToken`.
- Modify: per-provider `fetcher_test.go` — new test cases for the defaulted-key error message shape.

### Step 1: Write failing tests in github first

In `internal/sources/github/fetcher_test.go`, append:

```go
// TestFetch_DefaultedKeyMissing_ErrorMessageHasHint asserts the error
// when AuthSecretRef.Key is empty AND the Secret lacks GITHUB_TOKEN
// includes a hint pointing at the default-key convention so the
// operator knows where the GITHUB_TOKEN name came from.
func TestFetch_DefaultedKeyMissing_ErrorMessageHasHint(t *testing.T) {
	t.Parallel()
	f, err := New(&achv1alpha1.GitHubSource{
		Repo:      "owner/repo",
		Ref:       "main",
		Transport: "rest",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name: "s",
			// Key intentionally empty → resolved to GITHUB_TOKEN.
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	emptySecret := &corev1.Secret{Data: map[string][]byte{}}
	_, err = f.Fetch(context.Background(), sources.FetchRequest{Secret: emptySecret})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized; got %v", err)
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error should still mention the resolved key name; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("error should hint that GITHUB_TOKEN was the default-key fallback; got %q", err.Error())
	}
}
```

Run: `./scripts/dev.sh go test -run TestFetch_DefaultedKeyMissing_ErrorMessageHasHint ./internal/sources/github/`
Expected: FAIL — current message has no "default" hint.

### Step 2: Update `extractToken` in all three providers

The pattern (github example):

```go
func (f *Fetcher) extractToken(req sources.FetchRequest) (string, error) {
	if f.spec.AuthSecretRef == nil {
		return "", nil
	}
	if req.Secret == nil {
		return "", fmt.Errorf("github: auth secret %q is nil: %w",
			f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
	}
	key := f.spec.AuthSecretRef.Key
	defaulted := false
	if key == "" {
		key = achv1alpha1.DefaultAuthSecretKey("github")
		defaulted = true
	}
	raw := req.Secret.Data[key]
	if len(raw) == 0 {
		if defaulted {
			return "", fmt.Errorf(
				"github: missing auth secret key %q (default for github; set authSecretRef.key to override): %w",
				key, sources.ErrUnauthorized)
		}
		return "", fmt.Errorf("github: missing auth secret key %q: %w",
			key, sources.ErrUnauthorized)
	}
	return string(raw), nil
}
```

Apply the same change to gitlab (`"gitlab"` / `GITLAB_TOKEN`) and bitbucket (`"bitbucket"` / `BITBUCKET_TOKEN`).

### Step 3: Add analogous tests in gitlab + bitbucket fetcher_test files

Copy the test shape from Step 1 into `internal/sources/gitlab/fetcher_test.go` and `internal/sources/bitbucket/fetcher_test.go`, substituting the relevant struct/constant names.

### Step 4: Run + commit

```bash
./scripts/dev.sh go test ./internal/sources/...
```
Expected: PASS.

```bash
git add internal/sources/
git commit -m "fix(sources): extractToken error names default-key as default

When AuthSecretRef.Key is empty and the Secret lacks the
provider-default key (GITHUB_TOKEN / GITLAB_TOKEN / BITBUCKET_TOKEN),
the error now reads e.g.

  github: missing auth secret key \"GITHUB_TOKEN\" (default for github;
  set authSecretRef.key to override)

instead of just naming the resolved key. Operators who never spelled
key: in their CR get a hint that GITHUB_TOKEN came from the default-key
convention, not from a CR they forgot they wrote.

Behavior when Key IS spelled explicitly is unchanged (no hint added).

Closes PR #9 follow-up review finding #9.

Refs: PR #9 follow-up review"
```

---

## Task 8: Pin git wire protocols on every git invocation

**Why:** Defense in depth. Current invocations don't pin `protocol.allow` / `protocol.file.allow`. With Git ≥2.38 `file://` is no longer auto-enabled for submodules, but the principle of least authority says the operator's git subprocess should accept ONLY `https://` (and rarely `git://`, never `file://` or `ssh://` in v1alpha1). The fetcher's URL is operator-built so today the risk is theoretical; pinning closes a future-foot-gun if a code path ever passes user-supplied URL strings.

**Files:**
- Modify: `internal/sources/git/fetcher.go` — `buildGitInvocation` prepends protocol-allow config.
- Modify: `internal/sources/git/fetcher_test.go` — assert the config args appear.

### Step 1: Write failing test

Append to `internal/sources/git/fetcher_test.go`:

```go
// TestFetcher_PinsProtocolAllow asserts every git invocation carries
// the explicit protocol allow-list so an accidental file:// or ssh://
// URL cannot be honored. Defense in depth per PR #9 review finding #7.
func TestFetcher_PinsProtocolAllow(t *testing.T) {
	args := buildGitInvocation("clone", "tok", "https://example.com/x.git", "/tmp/x")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "protocol.allow=never") {
		t.Errorf("expected protocol.allow=never; got %q", joined)
	}
	if !strings.Contains(joined, "protocol.https.allow=always") {
		t.Errorf("expected protocol.https.allow=always; got %q", joined)
	}
}
```

Run: `./scripts/dev.sh go test -run TestFetcher_PinsProtocolAllow ./internal/sources/git/`
Expected: FAIL.

### Step 2: Update `buildGitInvocation` to prepend the protocol pins

In `internal/sources/git/fetcher.go`:

```go
func buildGitInvocation(subcommand, token string, args ...string) []string {
	// Pin allowed wire protocols. v1alpha1 only ever issues https://
	// clone URLs; file:// and ssh:// must be rejected even if some
	// future caller accidentally constructs such a URL. See PR #9
	// follow-up review finding #7.
	configPins := []string{
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
	}

	if token == "" {
		return append(append(configPins, subcommand), args...)
	}
	prefix := append(configPins,
		"-c", "http.extraHeader=Authorization: Bearer "+token,
		subcommand,
	)
	return append(prefix, args...)
}
```

### Step 3: Update redactArgs to redact the new config slot — wait, no token leaks here

The protocol-allow config does not carry secrets, so `redactArgs` doesn't need updating. Confirm with a quick read.

### Step 4: Run tests + commit

```bash
./scripts/dev.sh go test ./internal/sources/git/
```
Expected: PASS (incl. the new test + all 11 pre-existing).

```bash
./scripts/dev.sh go test ./internal/sources/...
```
Expected: PASS (per-provider transport tests still pass — they don't assert against the protocol pins).

```bash
git add internal/sources/git/
git commit -m "fix(sources/git): pin protocol.allow=never + https=always per invocation

Defense in depth. Every git invocation now carries explicit
protocol.allow=never + protocol.https.allow=always so a code path
that ever passes a file:// or ssh:// URL (today: none — URLs are
always operator-built https://) cannot have the URL honored. Closes
a future-foot-gun if user-supplied URL strings ever reach this
helper.

Closes PR #9 follow-up review finding #7.

Refs: PR #9 follow-up review"
```

---

## Task 9: Docs sync + CHANGELOG

**Files:**
- Modify: `CHANGELOG.md` — Unreleased block.
- Modify: `CLAUDE.md` — no change required (the failure-mode entry from PR #9 stays accurate).
- Optionally modify: `docs/api-reference/` — only if any CRD shape changed (this PR does not change CRDs; skip).

### Step 1: CHANGELOG entry

Under `## [Unreleased]`, in the `### Fixed` block (create if absent):

```markdown
### Fixed
- `internal/sources/github` and `internal/sources/gitlab` now validate
  `spec.Repo` / `spec.Project` / `spec.Host` / `spec.Ref` for URL-
  structural metacharacters at `New` time, matching the existing
  `bitbucket` constructor (CR-02 parity; PR #9 follow-up review HIGH).
  Validators extracted to a shared `internal/sources/cr02validate`
  subpackage.
- `internal/sources/git.LsRemote` now installs an inner 30s
  `context.WithTimeout` so a stalled upstream cannot hang the
  reconciler regardless of caller ctx.
- `internal/sources/git.Fetcher` uses `os.MkdirTemp` instead of a
  manual `crypto/rand` nonce; the prior code silently ignored
  `rand.Read` errors and on failure produced a predictable temp-dir
  name (symlink-race vector on shared cache PVCs).
- `internal/sources/git.buildGitInvocation` refactored from
  `(subcommand string, args ...string)` (token-as-last-variadic, a
  footgun) to `(subcommand, token string, args ...string)` — token
  positional + mandatory, compiler-enforced.
- `internal/sources/gitlab.Fetcher.constructCloneURL` strips scheme
  prefixes case-insensitively (`HTTPS://` no longer leaks through).
- `internal/controller/ach.resolveTransportName` switches on
  `sourceSpec.Type` instead of pointer-non-nil ordering, matching
  the registry's dispatch discriminator.
- `extractToken` error message on missing-defaulted-key now includes a
  hint identifying the resolved key as a default (`(default for
  github; set authSecretRef.key to override)`).
- Every `internal/sources/git` git subprocess invocation now carries
  explicit `protocol.allow=never -c protocol.https.allow=always`
  config, pinning the wire-protocol allow-list (defense in depth).
```

### Step 2: Commit

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): record PR #9 follow-up fixes

CR-02 parity, LsRemote deadline, MkdirTemp hardening, explicit-token
signature, host case-normalization, Type-based transport label,
defaulted-key error hint, protocol allow-list pin.

Refs: PR #9 follow-up review"
```

---

## Task 10: Pre-push + PR

### Step 1: Run gates from a non-worktree path

The pre-push secret gates (gitleaks + trufflehog) cannot scan a git worktree (their `git clone file:///pwd` fails — see PR #9 finding documented in plan #1). Run from the primary worktree:

```bash
# In the worktree: confirm everything committed.
cd /home/jcm/Projects/ach/.worktrees/git-fetcher-followup
git status  # should be clean

# Remove worktree to free the branch for checkout in primary.
cd /home/jcm/Projects/ach
chmod -R u+w .worktrees/git-fetcher-followup  # .gocache may have read-only dirs
git worktree remove --force .worktrees/git-fetcher-followup
git checkout feat/git-fetcher-followup-fixes

# Run honest pre-push.
make pre-push
```

Expected: 0 failures, gitleaks scans N commits, trufflehog reports 0 verified secrets, all 17 gates green.

### Step 2: Push + open PR

```bash
git push -u origin feat/git-fetcher-followup-fixes
gh pr create --title "fix(sources): close PR #9 review findings (CR-02 parity, LsRemote deadline, MkdirTemp, etc.)" --body "..."
```

PR body template:

```markdown
## Summary
Closes the 8 actionable findings from the post-merge review of PR #9 (git-protocol transport swap).

| # | Severity | Fix |
|---|---|---|
| 1 | HIGH | CR-02 metachar validation parity on github + gitlab (shared `internal/sources/cr02validate` package) |
| 2 | MED | `LsRemote` inner 30s deadline |
| 3 | MED | `os.MkdirTemp` instead of manual `rand.Read`-without-error-check |
| 4 | MED | `buildGitInvocation` explicit-token signature |
| 5 | LOW | gitlab host case-insensitive scheme stripping |
| 6 | LOW | `resolveTransportName` switches on `sourceSpec.Type` |
| 7 | LOW (sec defense) | git invocations pin `protocol.allow=never -c protocol.https.allow=always` |
| 8 | LOW (UX) | `extractToken` error message hints when key was defaulted |

## What did NOT change
- No CRD shape change.
- No reconciler shape change.
- Public/exported APIs unchanged (one internal signature change: `buildGitInvocation` — no external callers).

## Test plan
- [x] Unit tests: `./scripts/dev.sh make unit`
- [x] Lint + security: `./scripts/dev.sh make lint && ./scripts/dev.sh make security`
- [x] Pre-push 17 gates: `make pre-push`
- [x] New tests for every finding (TDD: red → green → commit)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

---

## Summary

8 atomic commits, one per finding, each landing with its own TDD test. Stack order matters for one pair: Task 1 (`buildGitInvocation` signature change) is a prerequisite for Task 4 (validators add new constructor paths that the new helpers may exercise indirectly via the existing fetch tests) and Task 8 (protocol pins go through the same helper — easier on the new signature). The other tasks are independent and can be reordered freely.

Net diff: ~1 new package (`internal/sources/cr02validate`), ~6 modified files in `internal/sources`, ~2 modified files in `internal/controller/ach`, ~1 modified file `CHANGELOG.md`. Maybe 600 lines added (mostly tests), ~50 lines deleted (the duplicated bitbucket validators).

Out of scope, not addressed by this plan:
- The 8 pre-existing `TestPMR_Stage{1,2}_*` envtest failures.
- Removing the `transport: rest` legacy escape hatch.
- Removing the REST SDK dependencies (`go-github`, gitlab SDK, bitbucket REST module).
- ssh:// clone URLs.
- v1beta1 path-subset extraction at the fetcher layer.

---

## Execution

Plan saved to `docs/plans/2026-05-27-git-fetcher-followup-fixes.md`. Two execution options:

1. **Subagent-Driven (this session)** — dispatch fresh subagent per task, review between tasks, fast iteration.
2. **Parallel Session (separate)** — open new session with `superpowers:executing-plans`, batch execution with checkpoints.

Which?
