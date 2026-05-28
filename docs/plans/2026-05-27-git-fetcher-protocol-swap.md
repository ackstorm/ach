# Git Source Fetchers: REST/SDK → Git Protocol Transport Swap — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the REST/SDK outer fetchers for ALL THREE git source types (`github`, `gitlab`, `bitbucket`) with a single git-protocol (`git ls-remote` + `git clone --depth=1 + git fetch <sha> + git archive`) implementation. Eliminates per-IP REST rate-limits as a failure mode for every CR kind that resolves through these source types (Plugin, Prompt, Artifact, PluginMarketplace).

**Crystal-clear scope statement (this is the question that motivated the rewrite):**

The point of distinguishing `github` vs `gitlab` vs `bitbucket` in v1alpha1 is **not** that they speak different wire protocols — they all speak HTTPS git. The distinction exists only so the operator can:

1. **Construct the clone URL** from spec fields (e.g. github: `https://github.com/{owner}/{repo}.git`; gitlab: `https://{host}/{project}.git`; bitbucket: `https://bitbucket.org/{workspace}/{repo}.git`).
2. **Build the Authorization header** in whatever shape the upstream accepts (uniform `Bearer <token>` works on all three for git over HTTPS; per-provider variations stay isolated to the provider's subpackage in case any of them needs a different header later).
3. **Resolve the Secret** that holds the token (CRD field names differ: github+gitlab+bitbucket use `authSecretRef`; the key naming convention is the same `data.key`).

Once those three things are done, **everything downstream is identical across all three providers and across all four consuming CRD kinds**: `git ls-remote` → SHA; `git clone --depth=1 --branch=<ref>` + `git fetch origin <sha>` + `git archive --format=tar.gz` → tarball; return `FetchResult{Body, UpstreamRev, NotModified}` to the reconciler. The reconciler then processes the tarball exactly as it does today:

- **Plugin** — write the tarball verbatim to `plugin/<name>.tar.gz`.
- **Prompt** — extract `spec.<type>.path` (single file) from the tarball; write to `prompt/<name>`.
- **Artifact** — extract `spec.<type>.path` (file or directory per `spec.scope`); write to `artifact/<name>` or `artifact/<name>.tar.gz`.
- **PluginMarketplace** — extract `<root>/.claude-plugin/marketplace.json` from the tarball; parse it; apply RE2 `filters.include`/`filters.exclude`; UPSERT/DELETE `marketplace_plugins` rows; for each surviving entry run Stage-2 materialization (which itself reuses the SAME shared git fetcher).

So this plan touches the fetcher layer only. The reconciler-side tarball-processing code in `internal/controller/ach/*` is unchanged.

**Architecture:**

```
                              ┌──────────────────────────────┐
                              │ internal/sources/git/         │
  cloneURL + authHeader ───▶ │ LsRemote(url, ref, authHdr)   │
                              │ Fetcher.Fetch(spec, ...)      │   ─── shared
                              │   - clone --depth=1           │       engine,
                              │   - fetch origin <sha>        │       provider-
                              │   - archive --format=tar.gz   │       agnostic
                              │ ClassifyError(err)            │
                              └──────────────────┬────────────┘
                                                 ▲
                                                 │  thin per-provider shim:
                                                 │  1. build cloneURL from spec
                                                 │  2. build authHeader from Secret
                                                 │  3. call LsRemote, then Fetcher
                                                 │
   ┌──────────────────────┐    ┌──────────────────────┐    ┌──────────────────────┐
   │ internal/sources/    │    │ internal/sources/    │    │ internal/sources/    │
   │   github/Fetcher     │    │   gitlab/Fetcher     │    │   bitbucket/Fetcher  │
   │   (transport=git)    │    │   (transport=git)    │    │   (transport=git)    │
   │   (transport=rest    │    │   (transport=rest    │    │   (transport=rest    │
   │    legacy path)      │    │    legacy path)      │    │    legacy path)      │
   └──────────────────────┘    └──────────────────────┘    └──────────────────────┘
                                                 │
                                                 │  registry.For dispatches by spec.Type
                                                 ▼
   ┌──────────────────────────────────────────────────────────────────────────────┐
   │ Reconcilers (UNCHANGED):                                                      │
   │   - Plugin            → write plugin/<name>.tar.gz                            │
   │   - Prompt            → extract spec.path file → write prompt/<name>          │
   │   - Artifact          → extract spec.path → write artifact/<name>[.tar.gz]    │
   │   - PluginMarketplace → extract .claude-plugin/marketplace.json,              │
   │                          parse, filter, UPSERT/DELETE marketplace_plugins,    │
   │                          recurse Stage-2 per surviving plugin (same shared    │
   │                          git engine reused)                                   │
   └──────────────────────────────────────────────────────────────────────────────┘
```

**Tech Stack:** Go, `os/exec`, `git` (already in runtime image per `Dockerfile` L20-L35), controller-runtime CRDs.

**Out of scope (explicit):**
- Removing `go-github`, `gitlab.com/gitlab-org/api/client-go`, or any other REST SDK from `go.mod` — deferred until the legacy `rest` transport is observed unused for one full release and then ripped out wholesale in a separate cleanup PR.
- Per-target-platform plugin format conversion — Hub spec §11 pins `.tar.gz` Claude Code plugin format; CLI does adaptation.
- v1beta1 `spec.<git>.path` subset extraction at the fetcher layer — current behavior is "fetcher returns full repo tarball; reconciler extracts the path it needs". This plan does NOT change that boundary.
- Bitbucket Server / Stash (Bitbucket Cloud only — same as v1alpha1 today).
- ssh:// clone URLs — every CRD example today uses https://; ssh would require mounting host SSH keys into the operator Pod and is a separate concern.
- OperatorHub / OLM packaging (perma-no for ach).

---

## Pre-flight context for the implementer

Read in this order before touching code:

1. **`FIX_GIT.txt`** at repo root — the user's knowledge dump. Has the REST-vs-git mapping table and the rate-limit math. Originally written about GitHub only; this plan generalizes its reasoning to gitlab + bitbucket because the same anonymous-quota wall exists there (gitlab.com: 60 req/min/IP unauthenticated; bitbucket.org: 60 req/h unauthenticated).
2. **`spec/ach_hub_spec_v20260515_FINALv4.md`** §10 (External References & Cache) and §10.1 (Source Type Schemas). Reading the `github` / `gitlab` / `bitbucket` blocks confirms the wire-protocol-agnostic contract: spec fixes the CR fields and the refresh behavior; transport is operator-internal.
3. **`spec/ach_hub_spec_v20260515_FINALv4.md`** §11 (Plugin format), §12 (PluginMarketplace lifecycle), §13 (Artifact), §14 (Prompt). Each section pins the reconciler-side processing — what we MUST preserve.
4. **`internal/sources/sources.go`** — the `Fetcher` / `FetchRequest` / `FetchResult` interface. The new code keeps this interface bit-identical.
5. **`internal/sources/git/fetcher.go`** — current inner shallow-clone implementation (used by PluginMarketplace Stage-2 today). Read in particular:
   - `Spec` struct (`URL`, `Ref`, `SHA`, `Subtree`, `Token`, `CacheRoot`, `MaxCloneBytes`).
   - The auth path: `cloneURL = "https://" + spec.Token + ":x-oauth-basic@" + ...` — **this is the security defect we are also fixing.** The token lands in the process args and in `git config remote.origin.url`. The new auth path uses `git -c http.extraHeader="Authorization: Bearer <token>"` instead.
   - `classifyGitError` — exit-code + stderr regex → `sources.ErrXxx` mapping. We export it.
   - `redactArgs` — for safe logging.
6. **`internal/sources/github/fetcher.go`** — current REST implementation (go-github). Reference for what we are replacing.
7. **`internal/sources/gitlab/fetcher.go`** — current REST implementation (gitlab.com/gitlab-org/api/client-go + raw HTTP for archive). Reference.
8. **`internal/sources/bitbucket/fetcher.go`** — current REST implementation (raw HTTP against `api.bitbucket.org` + `bitbucket.org/{ws}/{repo}/get/<sha>.tar.gz`). Reference.
9. **`internal/sources/registry/registry.go`** — dispatch table. Unchanged by this plan; the registry sees the same `sources.Fetcher` interface and the same string keys (`"github"`, `"gitlab"`, `"bitbucket"`).
10. **`internal/controller/ach/pluginmarketplace_controller.go`** — Stage-1 fetch + tarball reshape (`isTarballSourceType`). Confirms reconciler-side processing already differentiates "tarball source types" (github/gitlab/bitbucket) from "object source types" (s3/gcs/http). Untouched by this plan.

Verify the toolchain works before starting:

```bash
./scripts/dev.sh go build ./...
./scripts/dev.sh make unit
```

If those don't run cleanly with no edits, STOP and fix the environment first. Every Go invocation goes through `./scripts/dev.sh` — the host has no Go.

---

## Auth conventions per provider — distilled

The whole point of having three subpackages is the auth-header construction. Distilled:

| Provider  | v1alpha1 token type accepted                          | Authorization header sent                         | URL-form (rejected — leak risk)                                              |
|-----------|--------------------------------------------------------|----------------------------------------------------|------------------------------------------------------------------------------|
| github    | PAT (`ghp_…`, `github_pat_…`), GitHub App token (`ghs_…`) | `Authorization: Bearer <token>`                    | `https://<token>@github.com/…`                                               |
| gitlab    | PAT (`glpat-…`), Project/Group Access Token            | `Authorization: Bearer <token>`                    | `https://oauth2:<token>@<host>/…`                                            |
| bitbucket | Repository Access Token (current v1alpha1 only — no app password) | `Authorization: Bearer <token>`                    | `https://x-token-auth:<token>@bitbucket.org/…`                               |

Three takeaways:

1. **The header shape is uniform** (`Bearer <token>`). The temptation to keep three header shapes (the legacy gitlab `PRIVATE-TOKEN`, the legacy github `token <pat>`, etc.) is unjustified for git-protocol traffic — all three providers accept Bearer on their HTTPS git endpoints today. The REST paths keep their legacy headers as long as they exist (escape-hatch parity).
2. **URL-embedded credentials are banned in this plan.** Every code path goes through `git -c http.extraHeader=…`. The current `internal/sources/git/Fetcher` URL-injection is replaced as part of Task 2.
3. **The CRD field that holds the Secret is the same shape** across the three providers (`authSecretRef: {name, key}` for github + gitlab + bitbucket — see Hub spec §10.1). The only per-provider divergence is the clone-URL construction, which is a 3-line `fmt.Sprintf`.

---

## Task 1: CRD shape changes — transport knob + auth-shape relaxation

**Why:** Three CRD-level changes ship together in one atomic commit so v1alpha1 admission lands consistent. Order:

1. **`transport: git|rest` field** on `GitHubSource`, `GitLabSource`, `BitbucketSource` — default `git`; `rest` is a one-release escape hatch.
2. **`authSecretRef` becomes optional** on the three git source types (CEL: required → optional). Anonymous fetch is now a supported shape for public repos. With the git transport landing in this PR there is no rate-limit pressure to push users into supplying a dummy Secret.
3. **`authSecretRef.key` becomes optional** with a **provider-specific default** when omitted:
   - `github` → `GITHUB_TOKEN`
   - `gitlab` → `GITLAB_TOKEN`
   - `bitbucket` → `BITBUCKET_TOKEN`
   Env-var UPPERCASE convention matches the ecosystem (`gh` CLI, `terraform-provider-github`, `gitlab-runner`, `glab`, etc.) so a Secret materialized from `kubectl create secret generic foo --from-literal=GITHUB_TOKEN=ghp_xxx` works zero-config. Explicit `key: GH_KEY` overrides as before.

These three changes are coordinated: the per-provider type now genuinely earns its keep — it both (a) selects the clone-URL construction and auth-header conventions (Task 3–5) AND (b) drives the default Secret-key naming.

**Files:**
- Modify: file(s) under `api/ach/v1alpha1/` declaring `GitHubSource`, `GitLabSource`, `BitbucketSource`, and the shared `SourceAuthSecretRef` (or per-type auth-ref structs — grep to confirm).
- Autogenerated (do not edit by hand): `config/crd/bases/*.yaml`, `docs/api-reference/`.
- Spec follow-up: `spec/ach_hub_spec_v20260515_FINALv4.md` §10.1 — note required→optional shift on `authSecretRef`. The spec rev itself is OUT OF SCOPE for this PR (specs roll on a separate cadence); just file a placeholder TODO in `CHANGELOG.md` so the spec maintainer picks it up.

### Step 1: Locate the structs

Run: `./scripts/dev.sh grep -rn "type GitHubSource struct\|type GitLabSource struct\|type BitbucketSource struct\|type SourceAuthSecretRef" api/ach/v1alpha1/`
Expected: three or four matches. Read the surrounding 30 lines for each.

### Step 2: Add the `Transport` field to each source struct

Append to each of `GitHubSource`, `GitLabSource`, `BitbucketSource`:

```go
	// Transport selects the wire protocol used to fetch from this upstream.
	//
	//   "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;
	//            recommended; default).
	//   "rest" — use the provider's REST API. Subject to per-IP anonymous
	//            quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).
	//            Retained as a one-release escape hatch; will be removed.
	//
	// +kubebuilder:default=git
	// +kubebuilder:validation:Enum=git;rest
	// +optional
	Transport string `json:"transport,omitempty"`
```

### Step 3: Relax `authSecretRef` to optional on all three git source types

For each of `GitHubSource`, `GitLabSource`, `BitbucketSource`:

- Change the `AuthSecretRef` field from `*SourceAuthSecretRef` with `// +kubebuilder:validation:Required` to `// +optional`. If a CEL `x-kubernetes-validations` rule enforces presence today, drop or relax it. Grep the field's existing comment block for the +kubebuilder marker; preserve the rest.
- Update the field's doc comment:

```go
	// AuthSecretRef is optional. When set, the Secret named here MUST
	// exist in the CR's namespace at reconcile time and the operator
	// reads the bearer token from the named key (see Key below). When
	// nil, the upstream fetch is anonymous — supported only for public
	// repositories on the git transport. Anonymous + transport=rest is
	// also supported but subject to the provider's anonymous REST
	// quota (and is the bug FIX_GIT.txt fixes).
	// +optional
	AuthSecretRef *SourceAuthSecretRef `json:"authSecretRef,omitempty"`
```

### Step 4: Relax `Key` to optional with provider-aware default semantics

The challenge: `SourceAuthSecretRef` is shared across `github` / `gitlab` / `bitbucket` (and `s3` / `gcs` / `http`, each of which already has per-type key naming via `accessKeyIdKey` / `headerValueKey` etc — those stay unchanged). On the three git source types, `Key` should now be optional and default at reconcile-resolution time to the provider-specific upper-case env-var name.

**Approach:** Keep the CRD field optional at the CRD layer (no per-type default in OpenAPI — defaulting differs by parent type, which CEL `default` rules cannot express cleanly across siblings). The operator's `extractToken` helper (Task 2 / per-provider shims in Task 3–5) applies the default at resolution time.

Modify the `Key` field doc comment to document the convention:

```go
	// Key is the name of the Secret data key holding the bearer token.
	// Optional; when omitted on a git source type the operator falls
	// back to a provider-specific default key name:
	//   - github     → GITHUB_TOKEN
	//   - gitlab     → GITLAB_TOKEN
	//   - bitbucket  → BITBUCKET_TOKEN
	// (Matches the ecosystem env-var convention used by gh, glab,
	// terraform-provider-*, gitlab-runner, etc.) Other source types
	// (s3 / gcs / http) carry their own per-type key fields and do
	// NOT use this fallback.
	// +optional
	Key string `json:"key,omitempty"`
```

If a CEL rule today asserts `key != ""`, relax it to permit empty on github/gitlab/bitbucket parents only. (CRD-level CEL can inspect siblings via `self`; pin the relaxation under those three parents only.)

### Step 5: Regenerate CRD manifests + API docs

Run:
```bash
./scripts/dev.sh make manifests
./scripts/dev.sh make gen-crd-ref-docs
```
Expected: deltas in `config/crd/bases/*.yaml` (new `transport` enum on all three source schemas; `authSecretRef` and its `key` now optional) and `docs/api-reference/`.

### Step 6: Add unit tests for the per-type default-key resolution

New file: `api/ach/v1alpha1/authsecretref_defaults_test.go` (or wherever helper code naturally lives). Pin the convention in code:

```go
func TestDefaultAuthSecretKey(t *testing.T) {
	cases := []struct {
		typ  string
		want string
	}{
		{"github", "GITHUB_TOKEN"},
		{"gitlab", "GITLAB_TOKEN"},
		{"bitbucket", "BITBUCKET_TOKEN"},
	}
	for _, tc := range cases {
		if got := DefaultAuthSecretKey(tc.typ); got != tc.want {
			t.Errorf("DefaultAuthSecretKey(%q)=%q want %q", tc.typ, got, tc.want)
		}
	}
}
```

Add the function (small helper next to the types):

```go
// DefaultAuthSecretKey returns the provider-specific Secret data-key
// the operator falls back to when authSecretRef.key is omitted on a
// git source type. Mirrors the ecosystem env-var convention:
//   github    → GITHUB_TOKEN
//   gitlab    → GITLAB_TOKEN
//   bitbucket → BITBUCKET_TOKEN
// Returns "" for source types that don't have a default (s3/gcs/http
// carry their own per-type key fields).
func DefaultAuthSecretKey(sourceType string) string {
	switch sourceType {
	case "github":
		return "GITHUB_TOKEN"
	case "gitlab":
		return "GITLAB_TOKEN"
	case "bitbucket":
		return "BITBUCKET_TOKEN"
	default:
		return ""
	}
}
```

Run: `./scripts/dev.sh go test ./api/ach/v1alpha1/`
Expected: pass.

### Step 7: Verify build

Run: `./scripts/dev.sh go build ./...`
Expected: no errors.

### Step 8: Commit

```bash
git add api/ach/v1alpha1/ config/crd/ docs/api-reference/
git commit -m "feat(api): transport knob + auth-shape relaxation on git sources

Three coordinated CRD changes on GitHubSource/GitLabSource/BitbucketSource:

1. transport: git|rest field (default git). Sets up the FIX_GIT.txt
   swap landing in subsequent commits.

2. authSecretRef becomes optional. Anonymous fetch is now a supported
   shape for public repos — the upcoming git transport has no
   per-IP rate-limit so dummy-Secret workarounds are unnecessary.

3. authSecretRef.key becomes optional with provider-aware fallback:
     github    → GITHUB_TOKEN
     gitlab    → GITLAB_TOKEN
     bitbucket → BITBUCKET_TOKEN
   Env-var UPPERCASE convention matches gh, glab, terraform-provider-*,
   gitlab-runner ecosystem so kubectl create secret generic foo
   --from-literal=GITHUB_TOKEN=ghp_xxx works zero-config. Explicit
   key: <name> still overrides.

Spec §10.1 currently marks authSecretRef as required; a spec-rev
follow-up will reconcile the wording (tracked in CHANGELOG).

Refs: FIX_GIT.txt"
```

---

## Task 2: Harden the shared `internal/sources/git` engine

**Why:** Three things need fixing in the inner git fetcher before it becomes the shared engine for all three outer fetchers:

1. **Auth leak.** Replace URL-embedded token (`https://<token>:x-oauth-basic@…`) with `git -c http.extraHeader="Authorization: Bearer <token>"`. The token must never appear in the process args slice (visible in `/proc/<pid>/cmdline` to anyone with namespace access), nor in `git config remote.origin.url` (which would persist on disk in the clone).
2. **Missing `LsRemote` helper.** The outer fetchers need a "resolve ref → SHA" step that does NOT clone. Add it next to the existing `Fetch`.
3. **Coarse error classifier needs to be exported** so the three outer fetchers reuse the same regex set.

**Files:**
- Modify: `internal/sources/git/fetcher.go`
- Create: `internal/sources/git/lsremote.go` (new file; keeps `fetcher.go` focused)
- Modify: `internal/sources/git/fetcher_test.go` (red-then-green for the auth-header change and the new LsRemote)

### Step 1: Write the failing tests

In `internal/sources/git/fetcher_test.go`, append:

```go
// TestFetcher_AuthHeader_NotURL asserts the token reaches git via
// http.extraHeader and is NEVER embedded in the URL position of the
// clone command. Token in URL would land in /proc/<pid>/cmdline AND
// in `git config remote.origin.url` on disk — both are leak paths.
func TestFetcher_AuthHeader_NotURL(t *testing.T) {
	args := buildGitInvocation(
		"clone",
		"https://github.com/octo/repo.git",
		"/tmp/x",
		"ghp_secrettoken",
	)
	for _, a := range args {
		if strings.Contains(a, "ghp_secrettoken@") {
			t.Fatalf("token in URL form: %v", args)
		}
	}
	hasHeader := false
	for _, a := range args {
		if a == "http.extraHeader=Authorization: Bearer ghp_secrettoken" {
			hasHeader = true
		}
	}
	if !hasHeader {
		t.Fatalf("expected http.extraHeader=Authorization: Bearer …; args=%v", args)
	}
}

// TestLsRemote_ParsesSHA exercises LsRemote against the local bare-repo
// fixture and asserts the returned 40-hex SHA matches the fixture HEAD.
func TestLsRemote_ParsesSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupBareFixture(t)
	want := fixtureHeadSHA(t, bare)

	got, err := LsRemote(context.Background(), bare, "main", "")
	if err != nil {
		t.Fatalf("LsRemote: %v", err)
	}
	if got != want {
		t.Errorf("LsRemote SHA mismatch: got %q want %q", got, want)
	}
}

// TestLsRemote_BogusRefIsNotFound asserts an unknown ref classifies via
// sources.ErrNotFound (matches the REST path's 404 semantics).
func TestLsRemote_BogusRefIsNotFound(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupBareFixture(t)

	_, err := LsRemote(context.Background(), bare, "no-such-ref", "")
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
	if !errors.Is(err, sources.ErrNotFound) &&
		!errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("expected ErrNotFound or ErrUpstreamInvalid; got %v", err)
	}
}

// TestClassifyError_Exported sanity-checks the exported classifier
// covers each documented category.
func TestClassifyError_Exported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stderr string
		want   error
	}{
		{"remote: Invalid username or password", sources.ErrUnauthorized},
		{"Repository not found", sources.ErrNotFound},
		{"could not resolve host", sources.ErrUnreachable},
		{"some unrecognized git failure", sources.ErrUpstreamInvalid},
	}
	for _, tc := range cases {
		got := ClassifyError(errors.New(tc.stderr))
		if !errors.Is(got, tc.want) {
			t.Errorf("ClassifyError(%q) wraps %v; want %v", tc.stderr, got, tc.want)
		}
	}
}
```

Run: `./scripts/dev.sh go test ./internal/sources/git/`
Expected: compile errors — `buildGitInvocation`, `LsRemote`, `ClassifyError` undefined.

### Step 2: Land the auth-header refactor + LsRemote + classifier export

**2a. Edit `internal/sources/git/fetcher.go`:**

- Remove the URL-injection branch (lines that build `https://<token>:x-oauth-basic@…`).
- Replace with a `-c http.extraHeader=…` arg prepended to every git invocation that needs auth. Helper:

```go
// buildGitInvocation returns the full args slice for a git subcommand,
// prepending `-c http.extraHeader=Authorization: Bearer <token>` when
// token is non-empty. Token is never inserted into any other arg
// position, so it cannot leak via /proc/<pid>/cmdline beyond the
// extraHeader value (which is unavoidable but at least colocated in
// one auditable place) and never persists to `git config`.
func buildGitInvocation(subcommand string, args ...string) []string {
	// The auth-aware variant is invoked as buildGitInvocation(sub, arg1, arg2, …, token)
	// at call sites that need auth. Non-auth call sites use the same
	// helper with token="" (empty last arg).
	if len(args) == 0 {
		return []string{subcommand}
	}
	token := args[len(args)-1]
	body := args[:len(args)-1]
	if token == "" {
		return append([]string{subcommand}, body...)
	}
	return append(
		[]string{"-c", "http.extraHeader=Authorization: Bearer " + token, subcommand},
		body...,
	)
}
```

- Rewrite `runGit` to accept the token explicitly and route through `buildGitInvocation`:

```go
func runGit(ctx context.Context, workdir, token string, subcommand string, args ...string) error {
	full := buildGitInvocation(subcommand, append(args, token)...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_HTTP_LOW_SPEED_LIMIT=1000",
		"GIT_HTTP_LOW_SPEED_TIME=60",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %v: %s",
			redactArgs(full), err, truncateBytes(out, 512))
	}
	return nil
}
```

- Update the three call sites inside `Fetch`:
  - `runGit(ctx, cloneDir, spec.Token, "clone", "--depth=1", "--branch="+spec.Ref, "--no-tags", "--single-branch", cloneURL, cloneDir)`
  - `runGit(ctx, cloneDir, spec.Token, "fetch", "--depth=1", "origin", spec.SHA)`
  - `runGit(ctx, cloneDir, "", "checkout", "--detach", spec.SHA)` (no remote interaction on checkout)
- Delete the URL-token-injection block (`if spec.Token != "" && strings.HasPrefix(cloneURL, "https://") { … }`).
- Update `redactArgs` to also redact any `http.extraHeader=Authorization: Bearer …` arg — the token in that position SHOULD be redacted from log lines (it's still in the process args for git itself, but logs are separate).

```go
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "http.extraHeader=Authorization:"):
			out[i] = "http.extraHeader=Authorization: Bearer ***"
		case strings.HasPrefix(a, "https://") && strings.Contains(a, "@"):
			at := strings.LastIndex(a, "@")
			out[i] = "https://***" + a[at:]
		default:
			out[i] = a
		}
	}
	return out
}
```

- Rename `classifyGitError` to `ClassifyError` and add an exported doc comment.

**2b. Create `internal/sources/git/lsremote.go`:**

```go
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// LsRemote resolves ref against url and returns the 40-hex commit SHA
// it points at. `git ls-remote --refs <url> <ref>` outputs one or more
// lines of the form `<sha>\trefs/heads/<branch>` (or refs/tags/…);
// LsRemote returns the first matching line's SHA.
//
// authToken, when non-empty, is sent via
// `-c http.extraHeader=Authorization: Bearer <token>` so it never
// appears in the URL (URL injection leaks via /proc/<pid>/cmdline AND
// via local git config — both threats are closed by construction).
//
// Errors are classified via [ClassifyError] so the caller observes the
// same internal/sources sentinel set as a Fetch failure does.
func LsRemote(ctx context.Context, url, ref, authToken string) (string, error) {
	full := buildGitInvocation("ls-remote", "--refs", url, ref, authToken)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(cmd.Env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_HTTP_LOW_SPEED_LIMIT=1000",
		"GIT_HTTP_LOW_SPEED_TIME=60",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", ClassifyError(fmt.Errorf("ls-remote %s %s: %v: %s",
			url, ref, err, truncateBytes(out, 512)))
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", fmt.Errorf("ls-remote %s %s: empty output: %w",
			url, ref, sources.ErrNotFound)
	}
	tabIdx := strings.IndexByte(line, '\t')
	if tabIdx != 40 {
		return "", fmt.Errorf("ls-remote %s %s: malformed line %q: %w",
			url, ref, line, sources.ErrUpstreamInvalid)
	}
	return line[:40], nil
}
```

### Step 3: Run the tests, verify green

Run: `./scripts/dev.sh go test ./internal/sources/git/ -v`
Expected: every test passes, including the new ones.

If `TestFetcher_AuthHeader_NotURL` still fails because `buildGitInvocation` is misshapen (e.g. inserting `-c` after the subcommand), fix the helper. Invariant: when `args[last] == ""` the helper returns `[subcommand, ...body]`; when `args[last] != ""` it returns `["-c", "http.extraHeader=Authorization: Bearer <token>", subcommand, ...body]`.

### Step 4: Commit

```bash
git add internal/sources/git/
git commit -m "fix(sources/git): replace URL-embedded token with http.extraHeader

Before: git clone https://<token>:x-oauth-basic@host/repo … — token
visible in /proc/<pid>/cmdline AND persisted to git config
remote.origin.url on disk (T-02-02-02 leak path).

After: git -c http.extraHeader='Authorization: Bearer <token>' clone … —
token still in cmdline for the duration of the subprocess (unavoidable
without GIT_ASKPASS plumbing) but never persists to disk and is the only
arg position holding it (single auditable surface).

Also lands:
  - LsRemote(ctx, url, ref, authToken) — provider-agnostic SHA resolver
    that callers compose before the clone, so the conditional-fetch
    NotModified shortcut can fire without paying for a clone.
  - ClassifyError exported (was classifyGitError) so per-provider
    outer fetchers reuse the regex set instead of duplicating it.
  - redactArgs widened to redact the new http.extraHeader form in logs.

Existing PluginMarketplace Stage-2 callers unaffected — Spec shape
unchanged.

Refs: FIX_GIT.txt"
```

---

## Task 3: Build the github git-transport (red → green)

**Files:**
- Create: `internal/sources/github/git_transport.go`
- Modify: `internal/sources/github/fetcher.go` (route by `Transport`, keep REST as legacy branch)
- Create: `internal/sources/github/git_transport_test.go`

### Step 1: Write failing tests

Create `internal/sources/github/git_transport_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// Happy path against a local bare-repo fixture (no network).
func TestGitTransport_GitHub_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupHubBareFixture(t)

	f, err := New(&achv1alpha1.GitHubSource{
		Repo:      "fixture/repo",
		Ref:       "main",
		Transport: "git",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.cloneURLForTesting = bare

	res, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer res.Body.Close()

	if res.NotModified {
		t.Errorf("NotModified=true on first fetch")
	}
	if len(res.UpstreamRev) != 40 {
		t.Errorf("UpstreamRev not 40-hex: %q", res.UpstreamRev)
	}
	n, _ := io.Copy(io.Discard, res.Body)
	if n == 0 {
		t.Errorf("empty body")
	}
}

func TestGitTransport_GitHub_NotModified(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupHubBareFixture(t)
	head := hubHeadSHA(t, bare)

	f, err := New(&achv1alpha1.GitHubSource{
		Repo:      "fixture/repo",
		Ref:       "main",
		Transport: "git",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.cloneURLForTesting = bare

	res, err := f.Fetch(context.Background(), sources.FetchRequest{PriorRev: head})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.NotModified {
		t.Errorf("expected NotModified=true when PriorRev matches HEAD")
	}
	if res.Body != nil {
		t.Errorf("expected nil Body on NotModified")
	}
	if res.UpstreamRev != head {
		t.Errorf("UpstreamRev should echo back the matched SHA")
	}
}

func TestGitTransport_GitHub_TransportRouting(t *testing.T) {
	cases := []struct {
		transport string
		want      string
	}{
		{"", "git"},
		{"git", "git"},
		{"rest", "rest"},
	}
	for _, tc := range cases {
		t.Run(tc.transport, func(t *testing.T) {
			f, _ := New(&achv1alpha1.GitHubSource{
				Repo:      "x/y",
				Ref:       "main",
				Transport: tc.transport,
			})
			if got := f.resolvedTransport(); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestGitTransport_GitHub_UnreachableClassifies(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	f, _ := New(&achv1alpha1.GitHubSource{
		Repo:      "no/such",
		Ref:       "main",
		Transport: "git",
	})
	f.cloneURLForTesting = "https://localhost:1/nonexistent.git"

	_, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sources.ErrUnreachable) &&
		!errors.Is(err, sources.ErrNotFound) &&
		!errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("expected one of {Unreachable, NotFound, UpstreamInvalid}; got %v", err)
	}
}

// Local fixture helpers (sibling to internal/sources/git's).
func setupHubBareFixture(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main", ".")
	_ = os.WriteFile(filepath.Join(work, "README.md"), []byte("# fix\n"), 0o644)
	run("add", "README.md")
	run("commit", "-m", "init")
	bare := filepath.Join(t.TempDir(), "fixture.git")
	_ = os.MkdirAll(bare, 0o755)
	run("clone", "--bare", ".", bare)
	return bare
}

func hubHeadSHA(t *testing.T, bare string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
	return string(out[:40])
}
```

Run: `./scripts/dev.sh go test ./internal/sources/github/`
Expected: compile errors — `cloneURLForTesting`, `resolvedTransport` undefined.

### Step 2: Implement the git transport

Create `internal/sources/github/git_transport.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// This file implements the git-protocol transport for the GitHub
// source fetcher (FIX_GIT.txt).
//
// Composition:
//   1. gitsrc.LsRemote(url, ref, token) → SHA
//   2. gitsrc.Fetcher{URL, Ref, SHA, Token}.Fetch() → tarball
//
// The token (when present) reaches git via http.extraHeader; never URL.
// See internal/sources/git for the engine.

package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
	gitsrc "github.com/ackstorm/ach/internal/sources/git"
)

// resolvedTransport returns "git" or "rest". Empty defaults to "git"
// (matches the kubebuilder default; defends against a CR submitted
// before kube-apiserver defaulting applies).
func (f *Fetcher) resolvedTransport() string {
	if f.spec.Transport == "rest" {
		return "rest"
	}
	return "git"
}

func (f *Fetcher) fetchViaGit(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	token, err := f.extractToken(req)
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(f.spec.Repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("github: spec.repo must be <owner>/<name>, got %q: %w",
			f.spec.Repo, sources.ErrUpstreamInvalid)
	}

	cloneURL := f.cloneURLForTesting
	if cloneURL == "" {
		cloneURL = fmt.Sprintf("https://github.com/%s/%s.git", parts[0], parts[1])
	}

	sha, err := gitsrc.LsRemote(ctx, cloneURL, f.spec.Ref, token)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	if req.PriorRev != "" && req.PriorRev == sha {
		return &sources.FetchResult{NotModified: true, UpstreamRev: sha}, nil
	}

	gitRes, err := gitsrc.New(gitsrc.Spec{
		URL:   cloneURL,
		Ref:   f.spec.Ref,
		SHA:   sha,
		Token: token,
	}).Fetch(ctx, gitsrc.Request{})
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	return &sources.FetchResult{
		Body:        gitRes.Body,
		UpstreamRev: gitRes.UpstreamRev,
	}, nil
}
```

In `internal/sources/github/fetcher.go`:

- Add a `cloneURLForTesting string` field to `Fetcher` (test-only override of the upstream URL).
- Replace the existing token-validation block at the top of `Fetch` with a helper method `extractToken(req sources.FetchRequest) (string, error)` shared across both branches. New shape per Task 1's auth-shape relaxation:

  ```go
  // extractToken returns the bearer token to send to GitHub, or empty
  // for anonymous fetch. Per Task 1 (CRD shape relaxation):
  //
  //   - spec.AuthSecretRef == nil  → anonymous (token="" returned).
  //   - spec.AuthSecretRef != nil, req.Secret == nil
  //                                 → ErrUnauthorized (operator
  //                                   declared intent for auth and
  //                                   we must not silently fall
  //                                   back to anonymous).
  //   - spec.AuthSecretRef != nil, key resolved from
  //         f.spec.AuthSecretRef.Key OR (when empty)
  //         achv1alpha1.DefaultAuthSecretKey("github") == GITHUB_TOKEN
  //   - resolved key missing from Secret.Data
  //                                 → ErrUnauthorized with key name
  //                                   in message (threat T-02-02-01:
  //                                   error string mentions key name,
  //                                   never the absent value).
  func (f *Fetcher) extractToken(req sources.FetchRequest) (string, error) {
      if f.spec.AuthSecretRef == nil {
          return "", nil
      }
      if req.Secret == nil {
          return "", fmt.Errorf("github: auth secret %q is nil: %w",
              f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
      }
      key := f.spec.AuthSecretRef.Key
      if key == "" {
          key = achv1alpha1.DefaultAuthSecretKey("github") // "GITHUB_TOKEN"
      }
      raw := req.Secret.Data[key]
      if len(raw) == 0 {
          return "", fmt.Errorf("github: missing auth secret key %q: %w",
              key, sources.ErrUnauthorized)
      }
      return string(raw), nil
  }
  ```

- At the top of `Fetch`, after `extractToken` is callable (reused by both branches), add the dispatch:

```go
	if f.resolvedTransport() == "git" {
		return f.fetchViaGit(ctx, req)
	}
	// REST legacy path falls through below (kept verbatim for one
	// release as an escape hatch — FIX_GIT.txt "Order of operations").
```

### Step 3: Run tests, expect green

Run: `./scripts/dev.sh go test ./internal/sources/github/ -v`
Expected: all `TestGitTransport_GitHub_*` pass + all pre-existing tests pass.

### Step 4: Commit

```bash
git add internal/sources/github/
git commit -m "feat(sources/github): land git-protocol transport

Routing: GitHubSource.transport
  - 'git' (default) → gitsrc.LsRemote → gitsrc.Fetcher (no per-IP
                      REST rate-limit; auth via http.extraHeader).
  - 'rest' (legacy) → unchanged go-github path; escape hatch.

Wire contract preserved: FetchResult{Body: tar.gz, UpstreamRev: SHA,
NotModified}. Reconcilers unchanged.

Refs: FIX_GIT.txt"
```

---

## Task 4: Build the gitlab git-transport (red → green)

Same shape as Task 3. Differences:

- Clone URL construction: `cloneURL := fmt.Sprintf("https://%s/%s.git", host, project)` where `host = spec.Host` (default `gitlab.com` when empty per Hub spec §10.1) and `project` is `spec.Project` (e.g. `acme/widgets`).
- Auth header: same `Bearer <token>` shape (GitLab accepts PAT and Project/Group Access Tokens via Bearer on the git endpoints since GitLab 15.x).
- `extractToken` helper uses `achv1alpha1.DefaultAuthSecretKey("gitlab") == "GITLAB_TOKEN"` as the per-provider fallback when `spec.AuthSecretRef.Key` is empty. Anonymous fetch (`spec.AuthSecretRef == nil`) supported.
- The legacy REST path stays in `internal/sources/gitlab/fetcher.go` and continues to use `PRIVATE-TOKEN` on REST calls — do NOT touch the legacy path's header conventions.

**Files:**
- Create: `internal/sources/gitlab/git_transport.go`
- Modify: `internal/sources/gitlab/fetcher.go`
- Create: `internal/sources/gitlab/git_transport_test.go`

Tests mirror Task 3's GitHub tests; replace `Repo`/`*GitHubSource` with `Project`/`Host`/`*GitLabSource`. Include one additional test pinning the default-host behavior:

```go
func TestGitTransport_GitLab_DefaultHost(t *testing.T) {
	f, _ := New(&achv1alpha1.GitLabSource{
		Project:   "acme/widgets",
		Ref:       "main",
		Transport: "git",
	})
	got := f.constructCloneURL()
	want := "https://gitlab.com/acme/widgets.git"
	if got != want {
		t.Errorf("default host: got %q want %q", got, want)
	}
}

func TestGitTransport_GitLab_CustomHost(t *testing.T) {
	f, _ := New(&achv1alpha1.GitLabSource{
		Host:      "gitlab.example.com",
		Project:   "acme/widgets",
		Ref:       "main",
		Transport: "git",
	})
	got := f.constructCloneURL()
	want := "https://gitlab.example.com/acme/widgets.git"
	if got != want {
		t.Errorf("custom host: got %q want %q", got, want)
	}
}
```

Commit message:

```text
feat(sources/gitlab): land git-protocol transport

Same shape as the github swap. Default host gitlab.com when
spec.host is empty (Hub spec §10.1). Auth via Bearer header (GitLab
15.x+; works on PATs and Project/Group Access Tokens). Legacy REST
path retained behind transport=rest with its PRIVATE-TOKEN header
contract unchanged.

Refs: FIX_GIT.txt
```

---

## Task 5: Build the bitbucket git-transport (red → green)

Same shape. Differences:

- Clone URL: `cloneURL := fmt.Sprintf("https://bitbucket.org/%s/%s.git", spec.Workspace, spec.Repo)`. Bitbucket Cloud only; no `host` field on `BitbucketSource`.
- Auth: same `Bearer <token>` (Repository Access Token already accepts Bearer in the existing REST path — same shape for git protocol).
- `extractToken` helper uses `achv1alpha1.DefaultAuthSecretKey("bitbucket") == "BITBUCKET_TOKEN"` as the per-provider fallback. Anonymous fetch supported.
- v1alpha1 supports Bearer Repository Access Tokens only (per existing fetcher.go doc) — no app-password handling.

**Files:**
- Create: `internal/sources/bitbucket/git_transport.go`
- Modify: `internal/sources/bitbucket/fetcher.go`
- Create: `internal/sources/bitbucket/git_transport_test.go`

Tests mirror Task 3's. Commit message:

```text
feat(sources/bitbucket): land git-protocol transport

Clone URL is https://bitbucket.org/<workspace>/<repo>.git; Bitbucket
Server (Stash) remains out of scope. Auth via Bearer header — same
Repository Access Token shape the legacy REST path uses. Legacy path
retained behind transport=rest.

Refs: FIX_GIT.txt
```

---

## Task 6: Surface the resolved transport on the CR status

**Why:** Operators need to see which wire path each CR actually used during the one-release window in which both transports coexist.

**Files:**
- Modify: `internal/controller/ach/external_ref_refresh.go`
- Modify: `internal/controller/ach/pluginmarketplace_controller.go`

### Step 1: Write the failing test

In whichever `_test.go` already exercises the `SourceReachable=True` happy path (likely `external_ref_refresh_test.go` or per-kind test files), append a test asserting the success-message message contains `transport=<git|rest>`. Example skeleton:

```go
func TestSourceReachable_MessageIncludesTransport(t *testing.T) {
	// reuse the same happy-path fixture used by the existing
	// SourceReachable=True test; only added assertion is on the
	// condition message.
	cond := apimeta.FindStatusCondition(plugin.Status.Conditions, "SourceReachable")
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected SourceReachable=True; got %+v", cond)
	}
	if !strings.Contains(cond.Message, "transport=") {
		t.Errorf("Message should mention transport=<git|rest>; got %q", cond.Message)
	}
}
```

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestSourceReachable_MessageIncludesTransport`
Expected: FAIL.

### Step 2: Update both reconcilers

At the `SourceReachable=True` setter, derive the transport string from `sourceSpec`:

```go
transport := "rest"
switch {
case sourceSpec.GitHub != nil && sourceSpec.GitHub.Transport != "rest":
	transport = "git"
case sourceSpec.GitLab != nil && sourceSpec.GitLab.Transport != "rest":
	transport = "git"
case sourceSpec.Bitbucket != nil && sourceSpec.Bitbucket.Transport != "rest":
	transport = "git"
case sourceSpec.GitHub == nil && sourceSpec.GitLab == nil && sourceSpec.Bitbucket == nil:
	transport = "n/a" // s3/gcs/http source
}
meta.SetStatusCondition(&conds, metav1.Condition{
	Type:    "SourceReachable",
	Status:  metav1.ConditionTrue,
	Reason:  "Fetched",
	Message: fmt.Sprintf("fetched upstream_rev=%s transport=%s", upstreamRev, transport),
})
```

Same delta in the marketplace controller's Stage-1 success branch.

### Step 3: Re-run the envtest

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/...`
Expected: PASS, no regressions.

### Step 4: Commit

```bash
git add internal/controller/ach/
git commit -m "feat(controller): surface transport= on SourceReachable message

Operators can see which wire path the outer fetch used by reading the
SourceReachable=True message ('fetched upstream_rev=… transport=git')
during the one-release window in which both transports coexist.
Returns 'n/a' for s3/gcs/http sources (no git transport applies).

Refs: FIX_GIT.txt"
```

---

## Task 7: E2E validation against real upstreams

**Files:** None modified. Validation only.

### Step 1: Bring up the kept-cluster dev loop

```bash
./scripts/dev.sh make e2e-keep
```

Wait for operator + platform-api + content-service to land Ready.

### Step 2: Apply one CR per (provider × kind) combination present in examples/

Per `examples/` directory contents:

```bash
./scripts/dev.sh kubectl apply -f examples/05-pluginmarketplace-anthropic.yaml      # github PluginMarketplace
./scripts/dev.sh kubectl apply -f examples/06-plugin-caveman.yaml                   # github Plugin
./scripts/dev.sh kubectl apply -f examples/07-prompt-claudecode-leak.yaml           # github Prompt
./scripts/dev.sh kubectl apply -f examples/08-artifact-openclaw-templates.yaml      # github Artifact
```

If `examples/` has no gitlab/bitbucket fixtures yet, create minimal ones for this validation (and commit them in this task):

```yaml
# examples/05c-pluginmarketplace-gitlab.yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: PluginMarketplace
metadata:
  name: example-gitlab-marketplace
spec:
  type: gitlab
  gitlab:
    host: gitlab.com
    project: gitlab-org/gitlab-foss
    path: README.md           # placeholder — exercise the fetch, not the parse
    ref: master
  refresh:
    interval: 1h
    maxStaleness: 24h
```

```yaml
# examples/06b-plugin-gitlab.yaml  /  examples/06c-plugin-bitbucket.yaml
# similar shape — pick any public repo per provider
```

(If a real gitlab/bitbucket public claude-plugin fixture exists, use it; otherwise the README-only smoke test confirms transport works.)

### Step 3: Confirm transport=git on every CR

```bash
for cr in plugin/caveman prompt/claudecode-leak artifact/openclaw-templates pluginmarketplace/anthropic-official; do
  ./scripts/dev.sh kubectl get $cr -n default -o jsonpath='{.status.conditions[?(@.type=="SourceReachable")].message}' ; echo
done
```

Expected: each message contains `transport=git`.

### Step 4: Smoke-test the escape hatch on one CR per provider

```bash
./scripts/dev.sh kubectl patch plugin/caveman -n default --type=merge \
  -p '{"spec":{"github":{"transport":"rest"}}}'
./scripts/dev.sh make wait-cr-ready KIND=Plugin NAME=caveman NS=default
./scripts/dev.sh kubectl get plugin/caveman -n default -o jsonpath='{.status.conditions[?(@.type=="SourceReachable")].message}'
```

Expected: now contains `transport=rest`. Patch back to `git` after.

Repeat for one gitlab CR + one bitbucket CR if fixtures exist.

### Step 5: Tear down

```bash
make cluster-down
```

If anything fails, return to the failing task with a concrete repro. No commit on a clean run; if fixtures were added in Step 2, commit them:

```bash
git add examples/
git commit -m "test(examples): add gitlab + bitbucket fixtures for transport-swap e2e

Public-repo CRs per provider so make e2e-keep can validate the git
transport hits every (provider × kind) cell."
```

---

## Task 8: Docs sync — CLAUDE.md, CHANGELOG, mkdocs

**Files:**
- Modify: `CLAUDE.md` — refresh the "Common failure modes" entry on the 403 rate-limit bug.
- Modify: `CHANGELOG.md` — Unreleased section.
- Modify: `docs/` — if a fetcher-overview page exists, mention the transport knob.

### Step 1: CLAUDE.md

Find the `### ❌ "SourceReachable=False reason=Unauthorized" on a public GitHub repo` entry. Append:

```markdown
**Resolution as of 2026-05-27**: The default outer transport for all three
git source types (`github`, `gitlab`, `bitbucket`) is now `git`
(FIX_GIT.txt), which has no per-IP REST rate-limit. If you still see this
error on the default transport, the upstream is genuinely unreachable or
the ref doesn't exist. To temporarily revert one CR to the legacy REST
path, set `spec.<github|gitlab|bitbucket>.transport: rest` on the CR;
that path still hits the per-provider anonymous quotas (GitHub 60/h,
GitLab 60/min, Bitbucket 60/h) and will be removed one release after the
git transport is observed clean.
```

### Step 2: CHANGELOG entry

Under `## [Unreleased]`:

```markdown
### Changed
- Default outer fetcher transport for `github`, `gitlab`, `bitbucket`
  source types swapped from REST/SDK to git protocol
  (`git ls-remote` + shallow clone + `git archive`). Eliminates
  per-IP REST rate-limit (GitHub 60 req/h, GitLab 60 req/min,
  Bitbucket 60 req/h) as a failure mode. All four consuming CRD
  kinds — `Plugin`, `Prompt`, `Artifact`, `PluginMarketplace` —
  benefit transparently; wire contract
  (`FetchResult{Body: tar.gz, UpstreamRev: SHA}`) is unchanged.
- Auth: token is now passed to git via `http.extraHeader=Authorization:
  Bearer <token>` instead of URL-embedded form. Closes T-02-02-02 leak
  path (token no longer persists to `git config remote.origin.url` on
  disk; still visible in `/proc/<pid>/cmdline` for the duration of the
  subprocess, which is unavoidable without GIT_ASKPASS plumbing).
- `transport: git|rest` field added to `GitHubSource`, `GitLabSource`,
  and `BitbucketSource` (default `git`). `rest` is a one-release escape
  hatch; will be removed in the following release.
- `authSecretRef` is now optional on `GitHubSource`, `GitLabSource`,
  `BitbucketSource`. Anonymous fetch is the supported shape for public
  repos (the git transport has no per-IP rate-limit, so dummy-Secret
  workarounds are no longer necessary).
- `authSecretRef.key` is now optional with a provider-specific default
  matching the ecosystem env-var convention:
    github    → `GITHUB_TOKEN`
    gitlab    → `GITLAB_TOKEN`
    bitbucket → `BITBUCKET_TOKEN`
  Explicit `key: <name>` still overrides.
- `SourceReachable=True` condition message now includes
  `transport=<git|rest|n/a>` so operators can see which wire path was
  used.

### Spec follow-up
- Hub spec §10.1 currently marks `authSecretRef` as required on the
  three git source types. The spec rev that reconciles this with the
  v1alpha1 reality (now optional) will land in the next spec cadence
  (`spec/ach_hub_spec_*` revision after `v20260515_FINALv4`).
```

### Step 3: Build docs

```bash
./scripts/dev.sh make gen-crd-ref-docs
make docs-build
```

Expected: docs site builds clean; the new `transport` field renders on all three source schemas.

### Step 4: Commit

```bash
git add CLAUDE.md CHANGELOG.md docs/
git commit -m "docs: record git-protocol transport swap across three providers

Refs: FIX_GIT.txt"
```

---

## Task 9: Pre-push gates + PR

### Step 1: Full toolchain

```bash
./scripts/dev.sh make lint
./scripts/dev.sh make test-all
./scripts/dev.sh make security
make pre-push
```

Expected: every gate green. If gitleaks/trufflehog complains, the most likely cause is a copy-pasted token in a test fixture — scrub it.

### Step 2: Open PR

Title: `feat(sources): swap github/gitlab/bitbucket outer fetchers from REST to git protocol`

PR body (skeleton):

```markdown
## Summary
- Replaces REST/SDK outer fetchers for `github`, `gitlab`, `bitbucket` with a single git-protocol path (`git ls-remote` + shallow clone + `git archive`).
- All four consuming CRD kinds (Plugin, Prompt, Artifact, PluginMarketplace) benefit transparently — wire contract preserved.
- New `transport: git|rest` field on each git source CRD (default `git`); `rest` retained one release as an escape hatch.
- Token now passed via `http.extraHeader` instead of URL form (closes T-02-02-02).

## Why
GitHub 60 req/h, GitLab 60 req/min, Bitbucket 60 req/h anonymous REST quotas hit in practice during dev churn (`cluster.sh up` cycles + force-refresh fires); FIX_GIT.txt has the math. Git protocol is bandwidth-bound, not request-bound; no per-IP ceiling.

## What did NOT change
- Reconcilers (Plugin / Prompt / Artifact / PluginMarketplace) — they receive the same `FetchResult{Body: tar.gz, UpstreamRev: SHA}` and process it identically (extract path, parse marketplace.json, etc.).
- Public CRD shape for s3/gcs/http source types.
- `internal/sources/registry` dispatch table.
- The Hub spec (transport is operator-internal; spec is wire-protocol-agnostic).

## Test plan
- [ ] `./scripts/dev.sh make test-all`
- [ ] `make pre-push`
- [ ] `make e2e-keep` against every (provider × kind) fixture in examples/
- [ ] Smoke-test escape hatch (`transport: rest`) on one CR per provider
- [ ] CLAUDE.md "Common failure modes" entry shows the resolution
```

---

## Summary

This plan swaps the outer fetcher transport for `github`, `gitlab`, and `bitbucket` source types in one PR. The wire contract returned to reconcilers (`FetchResult{Body: tar.gz, UpstreamRev: SHA, NotModified}`) is bit-identical, so:

- **Plugin** still writes `plugin/<name>.tar.gz`.
- **Prompt** still extracts the spec'd path and writes `prompt/<name>`.
- **Artifact** still writes `artifact/<name>` (object) or `artifact/<name>.tar.gz` (directory).
- **PluginMarketplace** still extracts `<root>/.claude-plugin/marketplace.json`, parses, filters, UPSERTs marketplace_plugins rows, and recurses Stage-2 (which itself reuses the same shared git engine for per-plugin materialization).

Per-provider differences are isolated to two places:

1. **Clone URL construction** — three short `fmt.Sprintf` lines, one per provider's CRD shape.
2. **Token extraction from Secret** — same `authSecretRef.{name,key}` shape across all three; uniform `Bearer <token>` header on the git wire.

`internal/sources/git` becomes the shared engine. Its existing behavior (PluginMarketplace Stage-2 callers) is preserved; the auth-header refactor in Task 2 closes T-02-02-02 for those callers too — a bonus fix.

Out of scope:
- Removing the REST SDK dependencies (`go-github`, `gitlab.com/gitlab-org/api/client-go`, `go-bitbucket`) — separate cleanup PR after one release of clean observation.
- ssh:// clone URLs.
- v1beta1 `spec.path` subset extraction at the fetcher layer (reconciler-side path extraction unchanged).
- Bitbucket Server.

---

## Execution

Plan saved to `docs/plans/2026-05-27-git-fetcher-protocol-swap.md`. Two execution options:

1. **Subagent-Driven (this session)** — dispatch fresh subagent per task, review between tasks, fast iteration.
2. **Parallel Session (separate)** — open new session with `superpowers:executing-plans`, batch execution with checkpoints.

Which?
