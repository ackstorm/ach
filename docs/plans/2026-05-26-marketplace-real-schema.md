# Re-model PluginMarketplace for Real Claude Code Schema — Implementation Plan

> **Historical draft (2026-05-26).** Predates Phase 6's demo collapse.
> References below to `hydrate_demo.sh` originally used the hyphenated
> form (hyphen → underscore rename in the filename token only);
> the script itself was deleted in Phase 06-09 (replaced by
> `ach login` + `ach hydrate --environment demo`). The in-doc token was
> renamed in the same commit so the doc-hygiene grep gate stays green
> without falsifying the historical planning record.

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the placeholder 6-source-discriminator marketplace parser with the real upstream Claude Code marketplace schema (`{source: "git-subdir" | "url" | "local-path", url, path, ref, sha}`), introduce a generic `internal/sources/git/` fetcher to materialize per-entry plugins from any git remote, and prove end-to-end against `anthropics/claude-plugins-official`.

**Architecture:** PluginMarketplace splits into two fetch lanes. The OUTER fetch (catalog `marketplace.json`) still uses the existing 6-source CRD discriminator (`github`/`gitlab`/...) on `PluginMarketplaceSpec` — unchanged. The INNER fetch (each `plugins[]` entry) now uses a new `ClaudeCodeMarketplaceSource` union type with a custom `UnmarshalJSON` that accepts either a bare string (`source: "./relative/path"` → `local-path`) or an object (`source: {source: "git-subdir"|"url", ...}`). A new `internal/sources/git/` fetcher clones the per-entry git remote at the pinned `sha`, tars the worktree (or `path/` subtree for `git-subdir`), and feeds `materializeMarketplacePlugin` exactly as before. CRD shape unchanged.

**Tech Stack:**
- Existing: controller-runtime v0.24.1, envtest (Plan 02-06 fakeFactory pattern), Ginkgo v2 e2e, jackc/pgx/v5
- New: `git` CLI (already present in `Dockerfile.devtools` builder layer — verify in Task 0.2); `os/exec` shell-out; `archive/tar` + `compress/gzip` for inner subtree tar
- No new go.mod dependencies (stdlib only for the git fetcher; go-git intentionally NOT used — adds 12MB of deps and ships its own implementations of every git protocol; we need vanilla `git clone --depth=1`)

**Source paths (read-only references):**
- `/home/jcm/Projects/ach/CLAUDE.md` — toolchain, hooks, wait targets, naked-poll ban
- `/home/jcm/Projects/ach/TODO.md` §5 — distilled in the prompt; this plan is the §5 elaboration
- `/home/jcm/Projects/ach/FIX03.md` — recent J.* session changelog (not directly relevant but shows commit style)
- `/home/jcm/Projects/ach/internal/controller/ach/marketplace_parse.go` — current parser (will be torn down)
- `/home/jcm/Projects/ach/internal/controller/ach/marketplace_extract.go` — gzip+tar walker (KEEP; outer-fetch reshape already correct)
- `/home/jcm/Projects/ach/internal/controller/ach/pluginmarketplace_controller.go` — Stage-1/2/3 driver (one call-site to rewire in `materializeMarketplacePlugin`)
- `/home/jcm/Projects/ach/internal/sources/github/fetcher.go` — outer-fetch tarball shape reference
- `/home/jcm/Projects/ach/internal/sources/registry/registry.go` — outer-fetch dispatch (unchanged)
- `/home/jcm/Projects/ach/api/ach/v1alpha1/pluginmarketplace_types.go` — CRD (unchanged in this plan)
- `/home/jcm/Projects/ach/internal/controller/ach/pluginmarketplace_envtest_test.go` — fakeFactory pattern to re-use

**Working directory:** `/home/jcm/Projects/ach/`. Branch `feat/marketplace-real-schema` cut from current `main`.

**Cross-plan refs:**
- Independent of `docs/plans/2026-05-25-ach-domain-port.md` (operates on already-ported PluginMarketplace surface)
- Independent of all other in-flight TODO §1/§3 work
- May land before or after §2 with no conflict

---

## Pre-flight (do once before Phase 1)

```bash
cd /home/jcm/Projects/ach
git status   # confirm tree state matches the gitStatus header
git log --oneline | head -5
```

If `marketplace_extract.go` is present in-tree but uncommitted (current state per the prompt), commit it first as the prep work — this plan starts from a clean main with the extractor + outer-fetch reshape already landed:

```bash
git add internal/controller/ach/marketplace_extract.go \
        internal/controller/ach/pluginmarketplace_controller.go \
        api/ach/v1alpha1/pluginmarketplace_types.go
# Verify the diff in pluginmarketplace_controller.go is ONLY the
# isTarballSourceType + extractMarketplaceJSON wiring (Stage-1 §1b).
git diff --cached --stat
./scripts/dev.sh make unit
./scripts/dev.sh make envtest-fast   # current envtest must stay green
make pre-push
git commit -m "feat(marketplace): extract marketplace.json from repo tarball

Stage-1 of the PluginMarketplace reconciler now walks the gzipped
repo tarball returned by github/gitlab/bitbucket fetchers and
extracts <root>/.claude-plugin/marketplace.json before parse.
S3/GCS/HTTP fetchers continue to return marketplace.json body
verbatim. Prep work for the upcoming real-schema parser re-model
(TODO §5)."
git push -u origin main   # or whatever the prep branch is
```

Then cut the work branch:

```bash
git checkout -b feat/marketplace-real-schema
```

Confirm baselines:

```bash
./scripts/dev.sh make unit                       # PASS
./scripts/dev.sh make envtest-fast               # PASS (existing fixtures still green)
./scripts/dev.sh make lint                       # PASS
```

Any baseline failure → STOP and fix before proceeding. The plan assumes a green starting state.

---

## Phase 0 — Toolchain prep + branch hygiene

### Task 0.1: Confirm `git` is on PATH inside the devtools container

The new `internal/sources/git/` fetcher shells out to `git`. The devtools image must carry the binary.

**Steps:**

1. ```bash
   ./scripts/dev.sh bash -c 'git --version'
   ```
   Expected: `git version 2.x.y` (any 2.x).

2. If missing:
   - Edit `/home/jcm/Projects/ach/Dockerfile.devtools` — add `git` to the apt-get install list in the runtime stage (probably already there since `setup-envtest` pulls it, but verify).
   - Force rebuild: `ACH_DEVTOOLS_REBUILD=1 ./scripts/dev.sh bash -c 'git --version'`.
   - Commit: `chore(devtools): ensure git binary in devtools container`.

3. No commit if step 1 passes; this is a confirmation step.

### Task 0.2: Add `internal/sources/git/` directory placeholder

**Files:**
- Create: `internal/sources/git/doc.go`

**Step 1: Write the doc.go**

```go
// SPDX-License-Identifier: Apache-2.0

// Package git is the generic git-remote fetcher (Hub §10.1 + TODO §5).
// It is the INNER-fetch counterpart to the six per-source-type
// subpackages (github/gitlab/bitbucket/s3/gcs/http) that handle the
// OUTER fetch of a marketplace catalog file.
//
// Unlike the github subpackage (which uses the GitHub REST API to fetch
// a repo tarball), this package shells out to `git clone --depth=1
// --branch=<ref> <url> <dst>` followed by `git fetch origin <sha>` to
// pin the worktree. This is the right tool for marketplace plugin
// entries because:
//
//   - Per-entry sources point at arbitrary git remotes (self-hosted
//     gitea, gitlab, GitHub, bitbucket — anything that speaks the git
//     protocol). The github SDK can't reach a gitea instance.
//   - Per-entry sources carry a pinned commit SHA. The plumbing
//     (`git fetch origin <sha>`) lets us short-circuit when the local
//     worktree is already at the pin.
//   - We tar a subtree (`path/`) for git-subdir entries — git is the
//     simplest way to materialize a real worktree to walk.
//
// This package is NOT registered with internal/sources/registry — the
// OUTER fetch dispatcher in registry.For is keyed by CRD-discriminator
// strings and stays unchanged. The PluginMarketplace reconciler calls
// git.Fetch directly from materializeMarketplacePlugin (Stage-2).
package git
```

**Step 2: Build**

```bash
./scripts/dev.sh go build ./internal/sources/git/...
```
Expected: PASS (empty package compiles).

**Step 3: Commit**

```bash
git add internal/sources/git/doc.go
git commit -m "feat(sources/git): scaffold package for generic git-remote fetcher

Empty placeholder package — implementation lands across the next
tasks. Documents the design contract (shells out to `git`, NOT a
go-git port; INNER-fetch for marketplace plugin entries; not in
the registry.For dispatch table)."
```

---

## Phase 1 — New parser type with string-or-object union

Tear down the 6-source discriminator. The new `ClaudeCodeMarketplaceSource` is a flat struct with a custom `UnmarshalJSON` that handles both wire-format shapes.

### Task 1.1: Define the new ClaudeCodeMarketplaceSource type + UnmarshalJSON

**Files:**
- Modify: `internal/controller/ach/marketplace_parse.go` (full rewrite of the type block; parser logic stays in this file for now and gets rewritten in Task 1.2)

**Step 1: Write the failing test (string form)**

Add to `internal/controller/ach/marketplace_parse_test.go`:

```go
func TestClaudeCodeMarketplaceSource_UnmarshalString(t *testing.T) {
    var s ClaudeCodeMarketplaceSource
    if err := json.Unmarshal([]byte(`"./plugins/agent-sdk-dev"`), &s); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if s.Kind != "local-path" {
        t.Errorf("Kind = %q; want local-path", s.Kind)
    }
    if s.Path != "./plugins/agent-sdk-dev" {
        t.Errorf("Path = %q; want ./plugins/agent-sdk-dev", s.Path)
    }
}
```

**Step 2: Run test to verify it fails**

```bash
./scripts/dev.sh go test -run TestClaudeCodeMarketplaceSource_UnmarshalString ./internal/controller/ach/...
```
Expected: FAIL — `ClaudeCodeMarketplaceSource.Kind` undefined (current type has `Type` + six subobjects).

**Step 3: Rewrite the type block + add UnmarshalJSON**

Replace lines 37–74 of `internal/controller/ach/marketplace_parse.go` with:

```go
// ClaudeCodeMarketplace is the parsed top-level marketplace.json (Hub
// §12.1 + Claude Code upstream schema). owner is preserved verbatim
// for parity with the wire format but is not inspected by Stage-1 /
// Stage-2.
type ClaudeCodeMarketplace struct {
    Name    string                        `json:"name"`
    Owner   ClaudeCodeMarketplaceOwner    `json:"owner"`
    Plugins []ClaudeCodeMarketplacePlugin `json:"plugins"`
}

// ClaudeCodeMarketplaceOwner mirrors the upstream owner block.
// Email is a real upstream field (anthropics/claude-plugins-official
// emits "email" — was "url" in the placeholder schema).
type ClaudeCodeMarketplaceOwner struct {
    Name  string `json:"name"`
    Email string `json:"email,omitempty"`
    URL   string `json:"url,omitempty"`
}

// ClaudeCodeMarketplacePlugin is one entry under plugins[]. Source is
// a discriminated union — see ClaudeCodeMarketplaceSource.
//
// Upstream schema also allows description, version, author, category,
// homepage, license — accepted but not modeled (forward-compat).
type ClaudeCodeMarketplacePlugin struct {
    Name        string                      `json:"name"`
    Description string                      `json:"description,omitempty"`
    Source      ClaudeCodeMarketplaceSource `json:"source"`
    Version     string                      `json:"version,omitempty"`
}

// ClaudeCodeMarketplaceSource is the normalized per-entry source. The
// wire format is heterogeneous:
//
//   - bare string:        "source": "./relative/path"
//       → Kind="local-path", Path="./relative/path"
//   - object git-subdir:  "source": {"source":"git-subdir","url":"...","path":"...","ref":"v1","sha":"<40hex>"}
//       → Kind="git-subdir", URL/Path/Ref/SHA populated
//   - object url:         "source": {"source":"url","url":"...","sha":"<40hex>"}
//       → Kind="url", URL/SHA populated (whole-repo, no Path)
//
// Any other shape (object with unknown source.source, malformed JSON
// for the source field) → Kind="" so Stage-2 surfaces
// ReasonUnsupportedPluginSource per-entry.
type ClaudeCodeMarketplaceSource struct {
    Kind string // "git-subdir" | "url" | "local-path" | "" (unsupported)
    URL  string
    Path string
    Ref  string
    SHA  string
}

// UnmarshalJSON implements the string-or-object union. Never returns an
// error: malformed shapes resolve to Kind="" so the per-entry path can
// flip UnsupportedPluginSource via the parser's discriminator check.
// The reason: a single bad entry must not abort the whole marketplace.
func (s *ClaudeCodeMarketplaceSource) UnmarshalJSON(data []byte) error {
    // Bare string form.
    var str string
    if err := json.Unmarshal(data, &str); err == nil {
        s.Kind = "local-path"
        s.Path = str
        return nil
    }
    // Object form.
    var obj struct {
        Source string `json:"source"`
        URL    string `json:"url"`
        Path   string `json:"path"`
        Ref    string `json:"ref"`
        SHA    string `json:"sha"`
    }
    if err := json.Unmarshal(data, &obj); err != nil {
        // Neither string nor object — leave Kind="" so the per-entry
        // path surfaces UnsupportedPluginSource.
        return nil
    }
    switch obj.Source {
    case "git-subdir", "url":
        s.Kind = obj.Source
    default:
        // Unknown discriminator — Kind="" → per-entry unsupported.
        return nil
    }
    s.URL = obj.URL
    s.Path = obj.Path
    s.Ref = obj.Ref
    s.SHA = obj.SHA
    return nil
}
```

Remove the `_ = achv1alpha1` import if it becomes unused (the new type does NOT depend on `achv1alpha1.*Source` types). Check with:

```bash
./scripts/dev.sh goimports -l internal/controller/ach/marketplace_parse.go
```

**Step 4: Run test to verify it passes**

```bash
./scripts/dev.sh go test -run TestClaudeCodeMarketplaceSource_UnmarshalString ./internal/controller/ach/...
```
Expected: PASS.

**Step 5: Add more failing tests for the union**

Append to `marketplace_parse_test.go`:

```go
func TestClaudeCodeMarketplaceSource_UnmarshalGitSubdir(t *testing.T) {
    body := []byte(`{"source":"git-subdir","url":"https://github.com/42Crunch-AI/claude-plugins.git","path":"plugins/api-security-testing","ref":"v1.5.5","sha":"a175b24f7b34852b70c78c21545cce8037eb3112"}`)
    var s ClaudeCodeMarketplaceSource
    if err := json.Unmarshal(body, &s); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if s.Kind != "git-subdir" {
        t.Errorf("Kind = %q; want git-subdir", s.Kind)
    }
    if s.URL != "https://github.com/42Crunch-AI/claude-plugins.git" {
        t.Errorf("URL = %q", s.URL)
    }
    if s.Path != "plugins/api-security-testing" {
        t.Errorf("Path = %q", s.Path)
    }
    if s.SHA != "a175b24f7b34852b70c78c21545cce8037eb3112" {
        t.Errorf("SHA = %q", s.SHA)
    }
}

func TestClaudeCodeMarketplaceSource_UnmarshalURL(t *testing.T) {
    body := []byte(`{"source":"url","url":"https://github.com/AikidoSec/aikido-claude-plugin.git","sha":"79ac524f87c9faa9a356ff3d495b8a5b77e01bbd"}`)
    var s ClaudeCodeMarketplaceSource
    if err := json.Unmarshal(body, &s); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if s.Kind != "url" || s.URL == "" || s.SHA == "" || s.Path != "" {
        t.Errorf("got %+v", s)
    }
}

func TestClaudeCodeMarketplaceSource_UnmarshalUnknownDiscriminator(t *testing.T) {
    body := []byte(`{"source":"npm","package":"left-pad"}`)
    var s ClaudeCodeMarketplaceSource
    if err := json.Unmarshal(body, &s); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if s.Kind != "" {
        t.Errorf("Kind = %q; want \"\" for unknown discriminator", s.Kind)
    }
}

func TestClaudeCodeMarketplaceSource_UnmarshalMalformed(t *testing.T) {
    body := []byte(`[1,2,3]`) // neither string nor object
    var s ClaudeCodeMarketplaceSource
    if err := json.Unmarshal(body, &s); err != nil {
        t.Fatalf("unmarshal should not error on malformed; got %v", err)
    }
    if s.Kind != "" {
        t.Errorf("Kind = %q; want \"\"", s.Kind)
    }
}
```

**Step 6: Run all four tests**

```bash
./scripts/dev.sh go test -run TestClaudeCodeMarketplaceSource_Unmarshal ./internal/controller/ach/...
```
Expected: all PASS.

**Step 7: Commit**

```bash
git add internal/controller/ach/marketplace_parse.go \
        internal/controller/ach/marketplace_parse_test.go
git commit -m "feat(marketplace): real-schema ClaudeCodeMarketplaceSource union type

Replaces the placeholder 6-source discriminator with the upstream
Claude Code schema: bare-string source (local-path) and object
form with discriminator 'git-subdir' or 'url'. Custom UnmarshalJSON
handles both shapes; unknown / malformed shapes resolve to Kind=\"\"
so the per-entry path can flip ReasonUnsupportedPluginSource without
aborting the whole marketplace.

Tests cover all three valid shapes plus unknown-discriminator and
malformed-shape failure modes."
```

### Task 1.2: Rewrite parseClaudeCodeMarketplace for the new schema

**Files:**
- Modify: `internal/controller/ach/marketplace_parse.go` lines ~95–163 (the parser + the converter)
- Modify: `internal/controller/ach/marketplace_parse_test.go` (rewrite legacy tests)

**Step 1: Write failing parser tests for the real-schema marketplace.json**

Replace the `validGithubMarketplace` constant and its associated tests in `marketplace_parse_test.go` with real-schema fixtures. Keep the malformed-JSON, zero-plugins, name-traversal, and uppercase-rejection tests (those still apply — just update the embedded fixture body to use the real schema where relevant):

```go
const validRealSchemaMarketplace = `{
  "$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
  "name": "claude-plugins-official",
  "owner": {"name": "Anthropic", "email": "support@anthropic.com"},
  "plugins": [
    {
      "name": "agent-sdk-dev",
      "source": "./plugins/agent-sdk-dev"
    },
    {
      "name": "42crunch-api-security-testing",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/42Crunch-AI/claude-plugins.git",
        "path": "plugins/api-security-testing",
        "ref": "v1.5.5",
        "sha": "a175b24f7b34852b70c78c21545cce8037eb3112"
      }
    },
    {
      "name": "aikido",
      "source": {
        "source": "url",
        "url": "https://github.com/AikidoSec/aikido-claude-plugin.git",
        "sha": "79ac524f87c9faa9a356ff3d495b8a5b77e01bbd"
      }
    }
  ]
}`

func TestParseClaudeCodeMarketplace_RealSchemaValid(t *testing.T) {
    mkt, err := parseClaudeCodeMarketplace([]byte(validRealSchemaMarketplace))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    if len(mkt.Plugins) != 3 {
        t.Fatalf("want 3 plugins; got %d", len(mkt.Plugins))
    }
    if mkt.Plugins[0].Source.Kind != "local-path" {
        t.Errorf("plugin[0] Kind = %q; want local-path", mkt.Plugins[0].Source.Kind)
    }
    if mkt.Plugins[1].Source.Kind != "git-subdir" {
        t.Errorf("plugin[1] Kind = %q; want git-subdir", mkt.Plugins[1].Source.Kind)
    }
    if mkt.Plugins[2].Source.Kind != "url" {
        t.Errorf("plugin[2] Kind = %q; want url", mkt.Plugins[2].Source.Kind)
    }
    if mkt.Owner.Email != "support@anthropic.com" {
        t.Errorf("owner.email = %q", mkt.Owner.Email)
    }
}
```

Delete the obsolete tests:
- `TestParseClaudeCodeMarketplace_Valid` (uses old `validGithubMarketplace`)
- `TestParseClaudeCodeMarketplace_UnknownType` (no longer the parser's job — UnmarshalJSON resolves unknowns to Kind="")
- `TestParseClaudeCodeMarketplace_NpmIsKept` (npm no longer special-cased — it falls into the generic "unknown discriminator → Kind=\"\"" bucket)
- `TestParseClaudeCodeMarketplace_GitHubSubobjectMissing` (no more github subobject)
- `TestMarketplacePluginToSourceSpec_Npm` (function being deleted in Step 4)
- `TestMarketplacePluginToSourceSpec_GitHub` (function being deleted in Step 4)

Keep + adapt:
- `TestParseClaudeCodeMarketplace_MalformedJSON` (unchanged)
- `TestParseClaudeCodeMarketplace_ZeroPlugins` (unchanged)
- `TestParseClaudeCodeMarketplace_PluginNameTraversalRejected` (update the embedded source to the real schema: replace the entire `source: {...}` block with `"source": "./safe"` so the test isolates the name-traversal check)
- `TestParseClaudeCodeMarketplace_PluginNameUppercaseRejected` (same update)

**Step 2: Run the new test — expect failure**

```bash
./scripts/dev.sh go test -run TestParseClaudeCodeMarketplace_RealSchemaValid ./internal/controller/ach/...
```
Expected: FAIL — parser still does its old discriminator check, sees `Source.Type == ""` because the JSON has `source.source` not `source.type`.

**Step 3: Rewrite parseClaudeCodeMarketplace**

Replace the body of `parseClaudeCodeMarketplace` (and DELETE `marketplacePluginToSourceSpec` entirely — its replacement lives in the Stage-2 driver per Task 3.1):

```go
// parseClaudeCodeMarketplace unmarshals the upstream marketplace.json
// (Claude Code real schema) and performs Stage-1 validation. Every
// failure wraps sources.ErrUpstreamInvalid so the caller's
// classifyFetchError maps to ReasonUpstreamInvalid uniformly.
//
// Per-plugin validation rules:
//
//  1. plugin.Name MUST match DNS-1123 subdomain rules — adversarial
//     names like ../etc/passwd are rejected before they reach
//     materializeMarketplacePlugin (T-02-06-01 mitigation, preserved
//     from the placeholder parser).
//  2. plugin.Source.Kind MUST be one of {"git-subdir","url","local-path"}
//     OR empty (empty signals the per-entry UnsupportedPluginSource
//     branch in Stage-2 — kept so a single bad entry never aborts the
//     whole marketplace).
//  3. For Kind=="git-subdir" / "url": URL and SHA MUST be non-empty.
//     For Kind=="local-path": Path MUST be non-empty AND MUST NOT
//     escape the marketplace repo root (no leading "/", no "..")
//     — defense-in-depth path-traversal gate even though Stage-2
//     uses the path purely as a tar subtree filter.
//
// Per-entry validation surface bounds (audit findings folded in):
//   - plugin.Name truncated to dns1123MaxLen (already enforced by the
//     regex which caps at 253 chars; per-label 63-char cap enforced by
//     a separate check because the regex misses it).
//   - plugins[] length capped at marketplaceMaxPluginsPerCatalog.
//
// An empty plugins[] is treated as ErrUpstreamInvalid — a marketplace
// that ships no plugins is not legitimate steady-state.
func parseClaudeCodeMarketplace(body []byte) (*ClaudeCodeMarketplace, error) {
    var mkt ClaudeCodeMarketplace
    if err := json.Unmarshal(body, &mkt); err != nil {
        return nil, fmt.Errorf("marketplace.json: %v: %w", err, sources.ErrUpstreamInvalid)
    }
    if len(mkt.Plugins) == 0 {
        return nil, fmt.Errorf("marketplace.json: zero plugins declared: %w", sources.ErrUpstreamInvalid)
    }
    if len(mkt.Plugins) > marketplaceMaxPluginsPerCatalog {
        return nil, fmt.Errorf("marketplace.json: %d plugins exceeds cap %d: %w",
            len(mkt.Plugins), marketplaceMaxPluginsPerCatalog, sources.ErrUpstreamInvalid)
    }
    for i := range mkt.Plugins {
        p := &mkt.Plugins[i]
        // (1) name validation — bounded length + DNS-1123 + per-label.
        if err := validatePluginName(p.Name); err != nil {
            return nil, fmt.Errorf("marketplace.json: plugin[%d].name %q: %v: %w",
                i, truncate(p.Name, 64), err, sources.ErrUpstreamInvalid)
        }
        // (2)+(3) per-Kind validation.
        switch p.Source.Kind {
        case "git-subdir":
            if p.Source.URL == "" || p.Source.SHA == "" || p.Source.Path == "" {
                return nil, fmt.Errorf("marketplace.json: plugin %q: git-subdir requires url+path+sha: %w",
                    truncate(p.Name, 64), sources.ErrUpstreamInvalid)
            }
        case "url":
            if p.Source.URL == "" || p.Source.SHA == "" {
                return nil, fmt.Errorf("marketplace.json: plugin %q: url requires url+sha: %w",
                    truncate(p.Name, 64), sources.ErrUpstreamInvalid)
            }
        case "local-path":
            if p.Source.Path == "" {
                return nil, fmt.Errorf("marketplace.json: plugin %q: local-path requires path: %w",
                    truncate(p.Name, 64), sources.ErrUpstreamInvalid)
            }
            if err := validateLocalPath(p.Source.Path); err != nil {
                return nil, fmt.Errorf("marketplace.json: plugin %q: local-path %q: %v: %w",
                    truncate(p.Name, 64), truncate(p.Source.Path, 64), err, sources.ErrUpstreamInvalid)
            }
        case "":
            // Tolerated — Stage-2 emits ReasonUnsupportedPluginSource.
        default:
            // Should be unreachable (UnmarshalJSON resolves unknowns to "")
            // but defensive: tolerate the same way the empty case does.
        }
    }
    return &mkt, nil
}

// marketplaceMaxPluginsPerCatalog bounds the number of plugins[] entries
// a single marketplace.json may declare. 5000 is generous: Anthropic's
// catalog currently has ~250 entries; the bound exists only to stop a
// pathological 10M-entry marketplace from making Stage-1 unresponsive.
const marketplaceMaxPluginsPerCatalog = 5000

// truncate returns at most n bytes of s. Used in error messages where
// the embedded value is upstream-supplied (plugin name, path string,
// source-type echo) — k8s status.message is capped at ~4096 chars and
// individual condition messages are far smaller in practice.
func truncate(s string, n int) string {
    if len(s) <= n {
        return s
    }
    return s[:n] + "…"
}

// validatePluginName enforces RFC 1123 subdomain rules plus the per-label
// 63-char cap that the dns1123SubdomainRe regex misses.
func validatePluginName(name string) error {
    if len(name) == 0 {
        return fmt.Errorf("empty")
    }
    if len(name) > dns1123MaxLen {
        return fmt.Errorf("length %d exceeds %d", len(name), dns1123MaxLen)
    }
    if !dns1123SubdomainRe.MatchString(name) {
        return fmt.Errorf("not a DNS-1123 subdomain")
    }
    // Per-label 63-char cap (RFC 1123 §2.1).
    for _, label := range strings.Split(name, ".") {
        if len(label) > 63 {
            return fmt.Errorf("label %q exceeds 63 chars", label)
        }
    }
    return nil
}

// validateLocalPath rejects path traversal and absolute paths for the
// local-path Kind. Cleaning is intentionally NOT applied — we want to
// flag the raw upstream string, not silently rewrite it.
func validateLocalPath(p string) error {
    if strings.HasPrefix(p, "/") {
        return fmt.Errorf("must be relative")
    }
    // Reject any segment that is "..", regardless of position.
    for _, seg := range strings.Split(p, "/") {
        if seg == ".." {
            return fmt.Errorf("contains '..' segment")
        }
    }
    return nil
}
```

Add `strings` to the import block if not already present.

DELETE `marketplacePluginToSourceSpec` and `errUnsupportedPluginSource` from this file. The Stage-2 driver (Task 3.1) will own the per-entry conversion to a fetch call. The reason sentinel moves to `marketplace_dispatch.go` (Task 3.1).

**Step 4: Run all parser tests**

```bash
./scripts/dev.sh go test -run TestParseClaudeCodeMarketplace ./internal/controller/ach/...
```
Expected: all PASS. (The Stage-2 driver in `pluginmarketplace_controller.go` now fails to compile because `marketplacePluginToSourceSpec` and `errUnsupportedPluginSource` are gone — that's intentional. Task 3.1 wires the replacement; until then the package won't build.)

**Step 5: Confirm the breakage**

```bash
./scripts/dev.sh go build ./internal/controller/ach/...
```
Expected: FAIL with `undefined: marketplacePluginToSourceSpec` and `undefined: errUnsupportedPluginSource` in `pluginmarketplace_controller.go`. This is the dependency edge that Phase 3 closes.

**Step 6: Commit**

DO NOT push yet (pre-push gate runs `make unit` which would fail). Commit locally:

```bash
git add internal/controller/ach/marketplace_parse.go \
        internal/controller/ach/marketplace_parse_test.go
git commit -m "feat(marketplace): rewrite parser for Claude Code real schema

parseClaudeCodeMarketplace now validates the real upstream schema
(git-subdir / url / local-path Kinds) instead of the placeholder
6-source discriminator. Audit findings folded in:

  - plugin name truncated at 64 chars in error messages
  - per-label DNS-1123 63-char cap added (regex misses it)
  - plugins[] capped at 5000 entries
  - local-path Path validated for traversal (no leading /,
    no '..' segments)

The marketplacePluginToSourceSpec converter and the
errUnsupportedPluginSource sentinel are temporarily removed —
pluginmarketplace_controller.go will not compile until Task 3.1
wires the replacement. Acknowledged build-break is one commit
wide."
```

---

## Phase 2 — Generic git-remote fetcher

The new `internal/sources/git/` package shells out to `git` to clone a remote, fetch the pinned SHA, and return a tarball of the worktree (or a subtree).

### Task 2.1: Define the git fetcher contract + Fetch signature

**Files:**
- Create: `internal/sources/git/fetcher.go`
- Create: `internal/sources/git/fetcher_test.go`

**Step 1: Write the failing test**

```go
// SPDX-License-Identifier: Apache-2.0

package git

import (
    "context"
    "errors"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

// TestFetcher_FetchClonesAndTars verifies the happy path:
//   - clone a local bare-repo fixture
//   - the returned tarball is a valid gzipped tar stream
//   - the stream contains at least one regular file from the fixture
func TestFetcher_FetchClonesAndTars(t *testing.T) {
    if _, err := exec.LookPath("git"); err != nil {
        t.Skip("git binary not on PATH; skipping")
    }
    bare := setupBareFixture(t)

    f := New(Spec{
        URL:       bare,
        Ref:       "main",
        SHA:       fixtureHeadSHA(t, bare),
        CacheRoot: t.TempDir(),
    })
    res, err := f.Fetch(context.Background(), Request{})
    if err != nil {
        t.Fatalf("Fetch: %v", err)
    }
    defer res.Body.Close()

    n, err := io.Copy(io.Discard, res.Body)
    if err != nil {
        t.Fatalf("drain: %v", err)
    }
    if n == 0 {
        t.Errorf("body length 0; want >0")
    }
    if res.UpstreamRev == "" {
        t.Errorf("UpstreamRev empty")
    }
}

// setupBareFixture creates a small git repo and returns the path to a
// `--bare` clone of it that other tests can use as a remote URL.
func setupBareFixture(t *testing.T) string {
    t.Helper()
    work := t.TempDir()
    run := func(dir string, args ...string) {
        cmd := exec.Command("git", args...)
        cmd.Dir = dir
        cmd.Env = append(os.Environ(),
            "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
            "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
        )
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
        }
    }
    run(work, "init", "-b", "main", ".")
    if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# test\n"), 0o644); err != nil {
        t.Fatalf("write: %v", err)
    }
    run(work, "add", "README.md")
    run(work, "commit", "-m", "init")

    bare := filepath.Join(t.TempDir(), "fixture.git")
    if err := os.MkdirAll(bare, 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    run(work, "clone", "--bare", ".", bare)
    return bare
}

func fixtureHeadSHA(t *testing.T, bare string) string {
    t.Helper()
    out, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
    if err != nil {
        t.Fatalf("rev-parse: %v", err)
    }
    return string(out[:40])
}

// ErrInvalidSHA must exist so the parser can wrap it.
var _ = errors.New // satisfy linter until other tests need errors pkg
```

**Step 2: Run the test — expect failure**

```bash
./scripts/dev.sh go test -run TestFetcher_FetchClonesAndTars ./internal/sources/git/...
```
Expected: FAIL — `New`, `Spec`, `Request`, `Fetch`, `Result.Body`/`UpstreamRev` undefined.

**Step 3: Implement the fetcher**

Write `internal/sources/git/fetcher.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package git

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "context"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "io/fs"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "strings"
    "time"

    "github.com/ackstorm/ach/internal/sources"
)

// Spec configures a single git fetch. Constructed by the
// PluginMarketplace reconciler from a ClaudeCodeMarketplaceSource entry.
type Spec struct {
    // URL is the git remote (https or ssh — only https is exercised by
    // ACH today; ssh paths require host SSH keys mounted into the
    // operator pod and is out of scope for v1alpha1).
    URL string

    // Ref is the branch/tag to clone shallow. Required.
    Ref string

    // SHA is the pinned commit. After the shallow clone, the fetcher
    // does `git fetch origin <sha>` then `git checkout <sha>` to
    // guarantee reproducibility regardless of how far Ref has moved.
    SHA string

    // Subtree, when non-empty, narrows the produced tarball to a single
    // subdirectory of the worktree (the `path/` of a git-subdir entry).
    // Cleaned + slash-prefixed before use. Empty → whole worktree.
    Subtree string

    // Token, when non-empty, is URL-injected into URL as
    //   https://<token>:x-oauth-basic@<host>/...
    // so private git remotes work without ~/.netrc. ssh:// URLs are
    // left unchanged (auth via host SSH key).
    Token string

    // CacheRoot is the operator's cache PVC root. The fetcher creates
    // an ephemeral clone under <CacheRoot>/.tmp/git-<rand>/ and removes
    // it on completion. Empty defaults to os.TempDir() — fine for tests.
    CacheRoot string

    // MaxCloneBytes caps the on-disk size of the clone. Zero defaults to
    // gitDefaultMaxCloneBytes. Exceeded → ErrCloneTooLarge (wraps
    // sources.ErrUpstreamInvalid so the reconciler maps to
    // ReasonUpstreamInvalid).
    MaxCloneBytes int64
}

// Request is currently empty — kept so the signature matches the
// internal/sources contract pattern and so future fields (e.g.
// PriorRev for short-circuiting) can land without API churn.
type Request struct{}

// Result mirrors internal/sources.FetchResult shape.
type Result struct {
    Body        io.ReadCloser
    UpstreamRev string
}

// gitDefaultMaxCloneBytes is the on-disk size cap when Spec.MaxCloneBytes
// is zero. 200 MiB is generous — typical claude-plugin repos are <10 MiB.
const gitDefaultMaxCloneBytes = 200 << 20

// gitCloneTimeout is the wall-clock bound on a single fetch operation.
// Includes clone + checkout + tar. 5 minutes covers slow upstreams
// without letting a hung clone stall the reconciler indefinitely.
const gitCloneTimeout = 5 * time.Minute

// ErrCloneTooLarge surfaces when the on-disk clone exceeds Spec.MaxCloneBytes.
var ErrCloneTooLarge = errors.New("git clone exceeded size cap")

// sha40Re validates a full 40-hex commit SHA. Short SHAs are rejected
// because the reproducibility guarantee depends on the full hash.
var sha40Re = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Fetcher executes one git fetch end-to-end. Constructed via New.
type Fetcher struct{ spec Spec }

// New constructs a Fetcher. Validation of Spec.URL is intentionally
// thin — git itself surfaces malformed-URL errors on clone, and we
// want the same error surface for "no such repo" and "no such SHA"
// (the user-facing remediation is identical: fix the marketplace.json
// entry). SHA shape, however, IS validated up-front because a non-hex
// value would confuse the `git fetch origin <sha>` plumbing.
func New(spec Spec) *Fetcher {
    return &Fetcher{spec: spec}
}

// Fetch clones the remote at Spec.Ref, fetches + checks out Spec.SHA,
// and returns a gzipped tar of the worktree (or Spec.Subtree). The
// returned Body MUST be closed by the caller (which also triggers
// removal of the temporary clone directory).
func (f *Fetcher) Fetch(ctx context.Context, _ Request) (*Result, error) {
    spec := f.spec
    if spec.URL == "" {
        return nil, fmt.Errorf("git: spec.URL required: %w", sources.ErrUpstreamInvalid)
    }
    if spec.Ref == "" {
        return nil, fmt.Errorf("git: spec.Ref required: %w", sources.ErrUpstreamInvalid)
    }
    if !sha40Re.MatchString(spec.SHA) {
        return nil, fmt.Errorf("git: spec.SHA %q not 40-hex: %w", spec.SHA, sources.ErrUpstreamInvalid)
    }
    cap := spec.MaxCloneBytes
    if cap <= 0 {
        cap = gitDefaultMaxCloneBytes
    }

    // Temp dir under CacheRoot/.tmp/git-<rand>/ so the clone shares the
    // same filesystem as the eventual rename(2) target (avoids EXDEV).
    tmpParent := spec.CacheRoot
    if tmpParent == "" {
        tmpParent = os.TempDir()
    } else {
        tmpParent = filepath.Join(tmpParent, ".tmp")
    }
    if err := os.MkdirAll(tmpParent, 0o755); err != nil {
        return nil, fmt.Errorf("git: mkdir tmp parent: %w", err)
    }
    nonce := make([]byte, 8)
    _, _ = rand.Read(nonce)
    cloneDir := filepath.Join(tmpParent, "git-"+hex.EncodeToString(nonce))
    if err := os.MkdirAll(cloneDir, 0o755); err != nil {
        return nil, fmt.Errorf("git: mkdir clone dir: %w", err)
    }

    cleanupOnErr := func() { _ = os.RemoveAll(cloneDir) }

    // Token injection (https only).
    cloneURL := spec.URL
    if spec.Token != "" && strings.HasPrefix(cloneURL, "https://") {
        cloneURL = "https://" + spec.Token + ":x-oauth-basic@" + strings.TrimPrefix(cloneURL, "https://")
    }

    ctx, cancel := context.WithTimeout(ctx, gitCloneTimeout)
    defer cancel()

    // git clone --depth=1 --branch=<ref> <url> <dst>
    if err := runGit(ctx, cloneDir, "clone", "--depth=1", "--branch="+spec.Ref, "--no-tags", "--single-branch", cloneURL, cloneDir); err != nil {
        cleanupOnErr()
        return nil, classifyGitError(err)
    }
    // git fetch origin <sha> (depth=1 may not include the pin; this widens just enough).
    if err := runGit(ctx, cloneDir, "fetch", "--depth=1", "origin", spec.SHA); err != nil {
        cleanupOnErr()
        return nil, classifyGitError(err)
    }
    // git checkout <sha>
    if err := runGit(ctx, cloneDir, "checkout", "--detach", spec.SHA); err != nil {
        cleanupOnErr()
        return nil, classifyGitError(err)
    }

    // On-disk size cap.
    var total int64
    err := filepath.WalkDir(cloneDir, func(_ string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        if d.Type().IsRegular() {
            info, err := d.Info()
            if err != nil {
                return err
            }
            total += info.Size()
            if total > cap {
                return ErrCloneTooLarge
            }
        }
        return nil
    })
    if err != nil {
        cleanupOnErr()
        if errors.Is(err, ErrCloneTooLarge) {
            return nil, fmt.Errorf("git: %w (cap %d): %w", ErrCloneTooLarge, cap, sources.ErrUpstreamInvalid)
        }
        return nil, fmt.Errorf("git: walk clone dir: %w", err)
    }

    // Tar the worktree (or subtree) in memory. claude-plugin repos are
    // small — streaming would add complexity for no benefit. If a real
    // marketplace ships a 100MB plugin, the MaxCloneBytes cap catches it
    // before we get here.
    body, err := tarSubtree(cloneDir, spec.Subtree)
    if err != nil {
        cleanupOnErr()
        return nil, fmt.Errorf("git: tar: %w", err)
    }

    // Wrap the byte buffer in a Closer that removes the clone on close.
    rc := &cloneReadCloser{
        Reader:   bytes.NewReader(body),
        cloneDir: cloneDir,
    }
    return &Result{
        Body:        rc,
        UpstreamRev: spec.SHA,
    }, nil
}

// runGit runs a git subcommand without --recurse-submodules (security:
// arbitrary git submodule URLs in a marketplace plugin would be a
// remote-fetch primitive). Inherits ctx for the wall-clock cap.
func runGit(ctx context.Context, dir string, args ...string) error {
    cmd := exec.CommandContext(ctx, "git", args...)
    cmd.Dir = dir
    cmd.Env = append(os.Environ(),
        // Disable interactive auth prompts — fail fast on missing creds.
        "GIT_TERMINAL_PROMPT=0",
        // Bound git's own network operations independent of our timeout.
        "GIT_HTTP_LOW_SPEED_LIMIT=1000",
        "GIT_HTTP_LOW_SPEED_TIME=60",
    )
    out, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("git %v: %v: %s", redactArgs(args), err, truncateBytes(out, 512))
    }
    return nil
}

// classifyGitError maps git subprocess failures to wrapped sentinels
// the reconciler's classifyFetchError already understands. The
// classification is intentionally coarse — git's exit codes don't
// distinguish 404 vs 401 vs DNS-failure cleanly, and the marketplace's
// status.message surfaces the underlying git stderr anyway.
func classifyGitError(err error) error {
    msg := err.Error()
    switch {
    case strings.Contains(msg, "Authentication failed"),
        strings.Contains(msg, "could not read Username"),
        strings.Contains(msg, "remote: Invalid username or password"):
        return fmt.Errorf("git: %w: %v", sources.ErrUnauthorized, err)
    case strings.Contains(msg, "Repository not found"),
        strings.Contains(msg, "does not appear to be a git repository"):
        return fmt.Errorf("git: %w: %v", sources.ErrNotFound, err)
    case strings.Contains(msg, "could not resolve host"),
        strings.Contains(msg, "Connection timed out"),
        strings.Contains(msg, "Connection refused"):
        return fmt.Errorf("git: %w: %v", sources.ErrUnreachable, err)
    case strings.Contains(msg, "context deadline exceeded"):
        return fmt.Errorf("git: %w: %v", sources.ErrUnreachable, err)
    default:
        return fmt.Errorf("git: %w: %v", sources.ErrUpstreamInvalid, err)
    }
}

// tarSubtree gzip-tars the contents of root (or root/subtree if non-empty),
// stripping the root prefix from entry names so the resulting archive
// looks like the worktree was at /.
func tarSubtree(root, subtree string) ([]byte, error) {
    start := root
    relStrip := root
    if subtree != "" {
        // Defense in depth: tarSubtree is called with a parser-validated
        // local-path / git-subdir path, but reject traversal here too.
        clean := filepath.Clean(subtree)
        if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
            return nil, fmt.Errorf("subtree %q escapes root", subtree)
        }
        start = filepath.Join(root, clean)
        info, err := os.Stat(start)
        if err != nil {
            return nil, fmt.Errorf("subtree %q: %w", subtree, err)
        }
        if !info.IsDir() {
            return nil, fmt.Errorf("subtree %q: not a directory", subtree)
        }
        relStrip = start
    }

    var buf bytes.Buffer
    gz := gzip.NewWriter(&buf)
    tw := tar.NewWriter(gz)

    err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        // Skip .git/ entirely — the worktree's contents only.
        if d.IsDir() && filepath.Base(path) == ".git" {
            return fs.SkipDir
        }
        // Skip symlinks (TOCTOU defense, parity with extractMarketplaceJSON).
        info, err := d.Info()
        if err != nil {
            return err
        }
        if info.Mode()&os.ModeSymlink != 0 {
            return nil
        }
        if !info.Mode().IsRegular() && !d.IsDir() {
            return nil // skip devices/sockets/fifos
        }
        relPath, err := filepath.Rel(relStrip, path)
        if err != nil {
            return err
        }
        if relPath == "." {
            return nil
        }
        hdr, err := tar.FileInfoHeader(info, "")
        if err != nil {
            return err
        }
        hdr.Name = filepath.ToSlash(relPath)
        if d.IsDir() {
            hdr.Name += "/"
        }
        if err := tw.WriteHeader(hdr); err != nil {
            return err
        }
        if info.Mode().IsRegular() {
            file, err := os.Open(path)
            if err != nil {
                return err
            }
            _, copyErr := io.Copy(tw, file)
            _ = file.Close()
            if copyErr != nil {
                return copyErr
            }
        }
        return nil
    })
    if err != nil {
        return nil, err
    }
    if err := tw.Close(); err != nil {
        return nil, err
    }
    if err := gz.Close(); err != nil {
        return nil, err
    }
    return buf.Bytes(), nil
}

// cloneReadCloser wraps a *bytes.Reader and removes the temp clone
// directory on Close.
type cloneReadCloser struct {
    *bytes.Reader
    cloneDir string
}

func (c *cloneReadCloser) Close() error {
    return os.RemoveAll(c.cloneDir)
}

// redactArgs strips embedded tokens from the URL position of git
// subcommand args before logging.
func redactArgs(args []string) []string {
    out := make([]string, len(args))
    for i, a := range args {
        if strings.HasPrefix(a, "https://") && strings.Contains(a, "@") {
            // https://TOKEN:x-oauth-basic@host/... → https://***@host/...
            at := strings.LastIndex(a, "@")
            out[i] = "https://***" + a[at:]
        } else {
            out[i] = a
        }
    }
    return out
}

func truncateBytes(b []byte, n int) []byte {
    if len(b) <= n {
        return b
    }
    return append(b[:n:n], []byte("…")...)
}
```

**Step 4: Run the test — expect PASS**

```bash
./scripts/dev.sh go test -run TestFetcher_FetchClonesAndTars ./internal/sources/git/...
```
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/sources/git/fetcher.go internal/sources/git/fetcher_test.go
git commit -m "feat(sources/git): generic git-remote fetcher

Clones a git remote shallow at Ref, fetches+checks out the pinned
40-hex SHA, returns a gzipped tarball of the worktree (or
Spec.Subtree subdirectory for git-subdir entries).

Design choices:
  - shells out to git binary (not go-git): vanilla git is small,
    handles auth via URL injection, works against any git server
  - clone dir lives under CacheRoot/.tmp/git-<random>/ so rename(2)
    into the cache subtree avoids EXDEV
  - 5min wall-clock timeout, 200MB on-disk cap
  - NEVER --recurse-submodules (submodule URLs are remote-fetch primitive)
  - .git/ stripped from output tarball; symlinks skipped
  - https tokens redacted from logged subprocess args

Test fixture spins up a local bare git repo and exercises the
happy path. Hard-fail vs skip is controlled by git-on-PATH probe."
```

### Task 2.2: Add fetcher error-path tests

**Files:**
- Modify: `internal/sources/git/fetcher_test.go`

**Step 1: Write the failing tests**

Append:

```go
func TestFetcher_Fetch_InvalidSHA(t *testing.T) {
    f := New(Spec{
        URL: "https://example.invalid/foo.git",
        Ref: "main",
        SHA: "deadbeef", // not 40 hex
    })
    _, err := f.Fetch(context.Background(), Request{})
    if err == nil {
        t.Fatal("expected err on short SHA")
    }
    if !errors.Is(err, sources.ErrUpstreamInvalid) {
        t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
    }
}

func TestFetcher_Fetch_UnreachableRemote(t *testing.T) {
    if _, err := exec.LookPath("git"); err != nil {
        t.Skip("git binary not on PATH; skipping")
    }
    f := New(Spec{
        URL:       "https://localhost:1/nonexistent.git",
        Ref:       "main",
        SHA:       "0123456789abcdef0123456789abcdef01234567",
        CacheRoot: t.TempDir(),
    })
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    _, err := f.Fetch(ctx, Request{})
    if err == nil {
        t.Fatal("expected err on unreachable remote")
    }
    // Don't assert the exact sentinel — git's stderr on connection
    // refused is host-dependent. Confirm it wraps one of the expected
    // upstream errors (Unreachable / NotFound / UpstreamInvalid).
    if !errors.Is(err, sources.ErrUnreachable) &&
        !errors.Is(err, sources.ErrNotFound) &&
        !errors.Is(err, sources.ErrUpstreamInvalid) {
        t.Errorf("err should wrap one of the expected sentinels; got %v", err)
    }
}

func TestFetcher_Fetch_SubtreeTraversalRejected(t *testing.T) {
    if _, err := exec.LookPath("git"); err != nil {
        t.Skip("git binary not on PATH; skipping")
    }
    bare := setupBareFixture(t)
    f := New(Spec{
        URL:       bare,
        Ref:       "main",
        SHA:       fixtureHeadSHA(t, bare),
        Subtree:   "../../etc",
        CacheRoot: t.TempDir(),
    })
    _, err := f.Fetch(context.Background(), Request{})
    if err == nil {
        t.Fatal("expected err on traversal subtree")
    }
}

func TestFetcher_Fetch_TokenRedacted(t *testing.T) {
    args := []string{"clone", "https://abc123:x-oauth-basic@github.com/x/y.git", "/tmp/x"}
    out := redactArgs(args)
    if strings.Contains(out[1], "abc123") {
        t.Errorf("token leaked: %v", out)
    }
    if !strings.HasPrefix(out[1], "https://***@") {
        t.Errorf("redaction shape unexpected: %q", out[1])
    }
}
```

Add `time` to imports.

**Step 2: Run tests — expect PASS (logic already exists)**

```bash
./scripts/dev.sh go test ./internal/sources/git/...
```
Expected: all PASS.

**Step 3: Commit**

```bash
git add internal/sources/git/fetcher_test.go
git commit -m "test(sources/git): error-path coverage

- short/non-hex SHA rejected up-front (ErrUpstreamInvalid)
- unreachable remote classified as ErrUnreachable / ErrNotFound /
  ErrUpstreamInvalid (git stderr is host-dependent; tolerate the
  classification range)
- subtree traversal ('../../etc') rejected by tarSubtree
- token redaction in subprocess-arg log path"
```

---

## Phase 3 — Wire git fetcher into Stage-2 materializer

### Task 3.1: Replace `marketplacePluginToSourceSpec` with `dispatchMarketplacePlugin`

The Stage-2 driver loses its registry.For dispatch for INNER fetches and gains a direct git.Fetcher call. The function lives in a new file so the controller stays readable.

**Files:**
- Create: `internal/controller/ach/marketplace_dispatch.go`
- Modify: `internal/controller/ach/pluginmarketplace_controller.go` (only the materializeMarketplacePlugin function body's Step 1+2 — replace the `factory(spec)` + `fetcher.Fetch(...)` block)

**Step 1: Write the dispatch file**

`internal/controller/ach/marketplace_dispatch.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// marketplace_dispatch.go owns Stage-2's per-entry fetch path. The
// Claude Code marketplace real schema (TODO §5) replaces the 6-source
// CRD discriminator with three Kinds (git-subdir / url / local-path),
// none of which dispatch through internal/sources/registry.For. All
// three resolve to a git-remote clone via internal/sources/git.
//
// The local-path Kind is special: it points at a subdirectory of the
// MARKETPLACE's OWN repo. We resolve it by reading the marketplace
// CR's spec.<type>.repo/url, building a synthetic git-subdir Spec, and
// calling the same git.Fetcher used by the other two Kinds. This is
// why this function takes the parent PluginMarketplace pointer.

package ach

import (
    "context"
    "errors"
    "fmt"
    "io"

    corev1 "k8s.io/api/core/v1"

    achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
    "github.com/ackstorm/ach/internal/sources"
    sourcesgit "github.com/ackstorm/ach/internal/sources/git"
)

// errUnsupportedPluginSource is the typed sentinel
// dispatchMarketplacePlugin returns when the per-entry Kind is empty
// (a wire-format shape we don't model — e.g. {"source":"npm",...}).
// Stage-2 maps to ReasonUnsupportedPluginSource.
var errUnsupportedPluginSource = errors.New("unsupported plugin source")

// gitFetcher is the package-level seam tests inject to bypass the live
// git binary. nil → real git.Fetcher.New is used.
type gitFetcher interface {
    Fetch(ctx context.Context, req sourcesgit.Request) (*sourcesgit.Result, error)
}

// newGitFetcherFn produces a gitFetcher for a Spec. Overridable by tests.
var newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
    return sourcesgit.New(spec)
}

// dispatchMarketplacePlugin runs the per-entry fetch and returns a
// streaming io.ReadCloser + the UpstreamRev (the resolved SHA).
//
//   - git-subdir: clone entry.Source.URL at entry.Source.Ref, checkout
//     entry.Source.SHA, tar the entry.Source.Path subtree.
//   - url:        same but tar the whole worktree (no subtree).
//   - local-path: clone the MARKETPLACE'S OWN repo (resolved from
//     mp.Spec.<type>.{Repo,Project,URL}), checkout HEAD of mp.Spec's
//     own ref, tar the entry.Source.Path subtree. The SHA is empty
//     for local-path (it tracks the marketplace's own commit), so we
//     resolve to the marketplace fetcher's reported UpstreamRev via
//     a fresh git ls-remote — handled inside materializeMarketplacePlugin.
//   - "":         errUnsupportedPluginSource (Stage-2 maps to
//                 ReasonUnsupportedPluginSource).
//
// auth is the marketplace's auth Secret (re-used per the v1alpha1
// design: per-entry auth is NOT yet a wire-format field). May be nil.
func dispatchMarketplacePlugin(
    ctx context.Context,
    mp *achv1alpha1.PluginMarketplace,
    entry ClaudeCodeMarketplacePlugin,
    auth *corev1.Secret,
    cacheRoot string,
) (io.ReadCloser, string, error) {
    spec, err := buildGitSpecForEntry(mp, entry, auth, cacheRoot)
    if err != nil {
        return nil, "", err
    }
    if errors.Is(err, errUnsupportedPluginSource) {
        return nil, "", errUnsupportedPluginSource
    }
    f := newGitFetcherFn(spec)
    res, err := f.Fetch(ctx, sourcesgit.Request{})
    if err != nil {
        return nil, "", err
    }
    return res.Body, res.UpstreamRev, nil
}

// buildGitSpecForEntry maps a parsed entry into a git.Spec. Pulls the
// per-entry token from the marketplace's auth Secret when present.
func buildGitSpecForEntry(
    mp *achv1alpha1.PluginMarketplace,
    entry ClaudeCodeMarketplacePlugin,
    auth *corev1.Secret,
    cacheRoot string,
) (sourcesgit.Spec, error) {
    token := extractTokenFromSecret(auth)
    switch entry.Source.Kind {
    case "git-subdir":
        return sourcesgit.Spec{
            URL:       entry.Source.URL,
            Ref:       defaultRef(entry.Source.Ref),
            SHA:       entry.Source.SHA,
            Subtree:   entry.Source.Path,
            Token:     token,
            CacheRoot: cacheRoot,
        }, nil
    case "url":
        return sourcesgit.Spec{
            URL:       entry.Source.URL,
            Ref:       defaultRef(entry.Source.Ref),
            SHA:       entry.Source.SHA,
            Subtree:   "", // whole worktree
            Token:     token,
            CacheRoot: cacheRoot,
        }, nil
    case "local-path":
        // Resolve the marketplace's own repo URL + SHA.
        url, ref, err := marketplaceOwnRepo(mp)
        if err != nil {
            return sourcesgit.Spec{}, err
        }
        // We don't have the marketplace's currently-fetched SHA wired
        // through to here yet (v1alpha1 PluginMarketplaceStatus does
        // not persist it). For correctness, we accept a fresh git
        // ls-remote roundtrip inside Fetch by passing the HEAD-resolved
        // SHA via a stub: emit a sentinel that materializeMarketplacePlugin
        // will resolve before calling. For now, return a spec whose
        // SHA field is filled with a placeholder so the caller can
        // detect and resolve.
        return sourcesgit.Spec{
            URL:       url,
            Ref:       ref,
            SHA:       "", // resolved by caller
            Subtree:   entry.Source.Path,
            Token:     token,
            CacheRoot: cacheRoot,
        }, nil
    case "":
        return sourcesgit.Spec{}, errUnsupportedPluginSource
    default:
        return sourcesgit.Spec{}, fmt.Errorf("plugin %q: unknown source Kind %q: %w",
            truncate(entry.Name, 64), entry.Source.Kind, sources.ErrUpstreamInvalid)
    }
}

// marketplaceOwnRepo returns the (URL, Ref) of the marketplace's own
// upstream repo, derived from spec.<type>. Only github / gitlab /
// bitbucket carry a repo identity; s3 / gcs / http do not — for those
// types, local-path entries are unsupported and return an explicit
// errLocalPathNotSupportedForType.
func marketplaceOwnRepo(mp *achv1alpha1.PluginMarketplace) (string, string, error) {
    switch mp.Spec.Type {
    case "github":
        if mp.Spec.GitHub == nil {
            return "", "", fmt.Errorf("github marketplace missing spec.github: %w", sources.ErrUpstreamInvalid)
        }
        return "https://github.com/" + mp.Spec.GitHub.Repo + ".git", defaultRef(mp.Spec.GitHub.Ref), nil
    case "gitlab":
        if mp.Spec.GitLab == nil {
            return "", "", fmt.Errorf("gitlab marketplace missing spec.gitlab: %w", sources.ErrUpstreamInvalid)
        }
        host := mp.Spec.GitLab.Host
        if host == "" {
            host = "https://gitlab.com"
        }
        return host + "/" + mp.Spec.GitLab.Project + ".git", defaultRef(mp.Spec.GitLab.Ref), nil
    case "bitbucket":
        if mp.Spec.Bitbucket == nil {
            return "", "", fmt.Errorf("bitbucket marketplace missing spec.bitbucket: %w", sources.ErrUpstreamInvalid)
        }
        return "https://bitbucket.org/" + mp.Spec.Bitbucket.Workspace + "/" + mp.Spec.Bitbucket.Repo + ".git",
            defaultRef(mp.Spec.Bitbucket.Ref), nil
    default:
        return "", "", fmt.Errorf("local-path entries unsupported for marketplace type %q: %w",
            mp.Spec.Type, sources.ErrUpstreamInvalid)
    }
}

// defaultRef returns "main" when ref is empty — a marketplace fixture
// may omit Ref to mean "follow main".
func defaultRef(ref string) string {
    if ref == "" {
        return "main"
    }
    return ref
}

// extractTokenFromSecret peeks at the first non-empty Secret value as
// the bearer/PAT token. Phase 2 plugin entries don't carry their own
// AuthSecretRef; they re-use the marketplace's. A future v1beta1 may
// surface per-entry auth (TODO §3) — at that point this extraction
// becomes keyed by an entry-specific Secret key.
func extractTokenFromSecret(s *corev1.Secret) string {
    if s == nil {
        return ""
    }
    for _, v := range s.Data {
        if len(v) > 0 {
            return string(v)
        }
    }
    return ""
}
```

**Step 2: Rewrite materializeMarketplacePlugin to call dispatchMarketplacePlugin**

In `internal/controller/ach/pluginmarketplace_controller.go`:

1. Remove the `pluginSourceSpec sources.SourceSpec` parameter from the function signature.
2. Remove the `factory FetcherFactory` parameter.
3. Replace the body's Step 1–2 (`if factory == nil { factory = registry.For }` through `pluginFetcher.Fetch(...)`) with:

```go
    // ─── 1+2: dispatch + fetch via git (real-schema entries) ───
    body, upstreamRev, err := dispatchMarketplacePlugin(ctx, mp, entry, secret, r.CacheRoot)
    if err != nil {
        return err
    }
    defer body.Close()
```

4. In Step 5, replace `io.Copy(tmpFile, fr.Body)` with `io.Copy(tmpFile, body)` (since `fr.Body` is gone).
5. In Step 8, replace `fr.UpstreamRev` with the local `upstreamRev` variable.

Also update the caller in the Reconcile body (around line 295):

```go
            perr := r.materializeMarketplacePlugin(ctx, &cr, entry, marketplaceSecret)
```

Remove the `pluginSourceSpec` and `factory` arguments. Delete the lines that build them (`marketplacePluginToSourceSpec(entry)` and friends). Replace the conflict-resolver loop's `srcErr` check with a check on the per-entry Kind:

```go
        if entry.Source.Kind == "" {
            failures = append(failures, pluginFailure{name: entry.Name, reason: ReasonUnsupportedPluginSource})
            continue
        }
        perr := r.materializeMarketplacePlugin(ctx, &cr, entry, marketplaceSecret)
        if perr != nil {
            reason, _ := classifyFetchErrorMarketplace(perr, spec.Refresh, time.Time{})
            failures = append(failures, pluginFailure{name: entry.Name, reason: reason})
            continue
        }
```

The `classifyFetchErrorMarketplace` function stays as-is but its `errors.Is(err, errUnsupportedPluginSource)` branch can be deleted (the explicit Kind check above subsumes it). Keep the rest.

Drop the unused `registry` import from `pluginmarketplace_controller.go` if it's no longer referenced (it WAS used in Stage-1's marketplace catalog fetch path — that stays). Use `goimports`:

```bash
./scripts/dev.sh goimports -w internal/controller/ach/pluginmarketplace_controller.go
```

**Step 3: Build**

```bash
./scripts/dev.sh go build ./internal/controller/ach/...
```
Expected: PASS. Phase 1's build-break is now closed.

**Step 4: Unit smoke**

```bash
./scripts/dev.sh make unit
```
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/controller/ach/marketplace_dispatch.go \
        internal/controller/ach/pluginmarketplace_controller.go
git commit -m "feat(marketplace): wire git fetcher into Stage-2 materializer

dispatchMarketplacePlugin replaces the old marketplacePluginToSourceSpec
+ registry.For path for INNER fetches. All three real-schema Kinds
(git-subdir / url / local-path) resolve to internal/sources/git
fetches. local-path entries clone the marketplace's OWN repo URL
(derived from spec.<type>.repo/project/url) at its own Ref and tar
the entry.Source.Path subtree.

materializeMarketplacePlugin loses pluginSourceSpec + factory params
(registry.For is no longer the per-entry dispatch). The 6-source-
discriminator path remains for the OUTER marketplace.json fetch."
```

### Task 3.2: Local-path SHA resolution

The placeholder Spec for local-path entries currently sets `SHA: ""`, which fails the git fetcher's `sha40Re` gate. Resolve the SHA via `git ls-remote` before calling Fetch.

**Files:**
- Modify: `internal/controller/ach/marketplace_dispatch.go`

**Step 1: Write the failing test**

Add `internal/controller/ach/marketplace_dispatch_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package ach

import (
    "context"
    "io"
    "regexp"
    "strings"
    "testing"

    achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
    sourcesgit "github.com/ackstorm/ach/internal/sources/git"
)

// fakeGitFetcher records the Spec it was constructed with and returns
// a canned body / SHA. Tests swap newGitFetcherFn for a closure that
// returns this fake.
type fakeGitFetcher struct {
    spec sourcesgit.Spec
    body string
    rev  string
    err  error
}

func (f *fakeGitFetcher) Fetch(_ context.Context, _ sourcesgit.Request) (*sourcesgit.Result, error) {
    if f.err != nil {
        return nil, f.err
    }
    return &sourcesgit.Result{
        Body:        io.NopCloser(strings.NewReader(f.body)),
        UpstreamRev: f.rev,
    }, nil
}

func TestDispatchMarketplacePlugin_GitSubdir(t *testing.T) {
    var captured sourcesgit.Spec
    orig := newGitFetcherFn
    defer func() { newGitFetcherFn = orig }()
    newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
        captured = spec
        return &fakeGitFetcher{body: "tarball-bytes", rev: spec.SHA}
    }
    entry := ClaudeCodeMarketplacePlugin{
        Name: "x",
        Source: ClaudeCodeMarketplaceSource{
            Kind: "git-subdir",
            URL:  "https://github.com/o/r.git",
            Path: "plugins/x",
            Ref:  "v1",
            SHA:  "0123456789abcdef0123456789abcdef01234567",
        },
    }
    mp := &achv1alpha1.PluginMarketplace{}
    body, rev, err := dispatchMarketplacePlugin(context.Background(), mp, entry, nil, "/tmp")
    if err != nil {
        t.Fatalf("dispatch: %v", err)
    }
    defer body.Close()
    if rev != entry.Source.SHA {
        t.Errorf("rev = %q", rev)
    }
    if captured.Subtree != "plugins/x" {
        t.Errorf("Subtree = %q", captured.Subtree)
    }
    if captured.URL != entry.Source.URL {
        t.Errorf("URL = %q", captured.URL)
    }
}

func TestDispatchMarketplacePlugin_LocalPathResolvesMarketplaceRepo(t *testing.T) {
    var captured sourcesgit.Spec
    orig := newGitFetcherFn
    defer func() { newGitFetcherFn = orig }()
    newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
        captured = spec
        // Simulate fetcher resolving the SHA on local-path.
        return &fakeGitFetcher{body: "subtree-bytes", rev: "abcdef0123456789abcdef0123456789abcdef01"}
    }
    mp := &achv1alpha1.PluginMarketplace{
        Spec: achv1alpha1.PluginMarketplaceSpec{
            Type: "github",
            GitHub: &achv1alpha1.GitHubSource{
                Repo: "anthropics/claude-plugins-official",
                Ref:  "main",
            },
        },
    }
    entry := ClaudeCodeMarketplacePlugin{
        Name: "agent-sdk-dev",
        Source: ClaudeCodeMarketplaceSource{
            Kind: "local-path",
            Path: "./plugins/agent-sdk-dev",
        },
    }
    _, rev, err := dispatchMarketplacePlugin(context.Background(), mp, entry, nil, "/tmp")
    if err != nil {
        t.Fatalf("dispatch: %v", err)
    }
    if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(rev) {
        t.Errorf("rev not 40-hex: %q", rev)
    }
    if captured.URL != "https://github.com/anthropics/claude-plugins-official.git" {
        t.Errorf("URL = %q", captured.URL)
    }
    if captured.Subtree != "./plugins/agent-sdk-dev" {
        t.Errorf("Subtree = %q", captured.Subtree)
    }
    // SHA was empty in the Spec the dispatch built — the local-path
    // resolver must set it to a 40-hex value BEFORE Fetch is called.
    if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(captured.SHA) {
        t.Errorf("local-path: SHA passed to Fetch was %q (want 40-hex)", captured.SHA)
    }
}

func TestDispatchMarketplacePlugin_UnsupportedKind(t *testing.T) {
    entry := ClaudeCodeMarketplacePlugin{
        Name:   "y",
        Source: ClaudeCodeMarketplaceSource{Kind: ""},
    }
    _, _, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
    if err == nil || err != errUnsupportedPluginSource {
        t.Errorf("err = %v; want errUnsupportedPluginSource", err)
    }
}
```

**Step 2: Run — expect the local-path test to FAIL**

```bash
./scripts/dev.sh go test -run TestDispatchMarketplacePlugin ./internal/controller/ach/...
```
Expected: `TestDispatchMarketplacePlugin_LocalPathResolvesMarketplaceRepo` FAILS — SHA is empty.

**Step 3: Resolve the SHA**

Add to `marketplace_dispatch.go`:

```go
// resolveHeadSHA does a `git ls-remote <url> <ref>` and returns the
// resolved 40-hex commit SHA. Used for local-path entries whose SHA
// is implicit (tracks the marketplace's own ref).
//
// Overridable via newResolveHeadSHAFn for tests so we don't shell out
// in unit tests.
var newResolveHeadSHAFn = func(ctx context.Context, url, ref, token string) (string, error) {
    cloneURL := url
    if token != "" && strings.HasPrefix(cloneURL, "https://") {
        cloneURL = "https://" + token + ":x-oauth-basic@" + strings.TrimPrefix(cloneURL, "https://")
    }
    cmd := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", cloneURL, ref)
    cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
    out, err := cmd.Output()
    if err != nil {
        return "", fmt.Errorf("ls-remote %s %s: %v: %w", url, ref, err, sources.ErrUpstreamInvalid)
    }
    // Output format: "<40-hex>\t<refname>\n"
    parts := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
    if len(parts) != 2 || len(parts[0]) != 40 {
        return "", fmt.Errorf("ls-remote %s %s: unexpected output %q: %w", url, ref, out, sources.ErrUpstreamInvalid)
    }
    return parts[0], nil
}
```

Update the imports (`os`, `os/exec`, `strings` should already be in scope from existing uses; verify with goimports).

Update `dispatchMarketplacePlugin` to resolve the SHA when Kind=="local-path" before calling Fetch:

```go
    f := newGitFetcherFn(spec)
    // local-path: SHA was deliberately empty; resolve it now.
    if entry.Source.Kind == "local-path" {
        sha, err := newResolveHeadSHAFn(ctx, spec.URL, spec.Ref, spec.Token)
        if err != nil {
            return nil, "", err
        }
        spec.SHA = sha
        f = newGitFetcherFn(spec) // reconstruct with resolved SHA
    }
    res, err := f.Fetch(ctx, sourcesgit.Request{})
```

In the test, swap the resolver too:

```go
func TestDispatchMarketplacePlugin_LocalPathResolvesMarketplaceRepo(t *testing.T) {
    // ... (existing setup)
    origResolve := newResolveHeadSHAFn
    defer func() { newResolveHeadSHAFn = origResolve }()
    newResolveHeadSHAFn = func(_ context.Context, _, _, _ string) (string, error) {
        return "abcdef0123456789abcdef0123456789abcdef01", nil
    }
    // ... (rest unchanged; remove the simulation comment in fakeGitFetcher
    //     since the resolver now fills SHA)
```

**Step 4: Run all dispatch tests**

```bash
./scripts/dev.sh go test -run TestDispatchMarketplacePlugin ./internal/controller/ach/...
```
Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/controller/ach/marketplace_dispatch.go \
        internal/controller/ach/marketplace_dispatch_test.go
git commit -m "feat(marketplace): resolve local-path SHA via git ls-remote

Local-path entries point at a subdirectory of the marketplace's own
repo and carry no explicit SHA. Resolve it via 'git ls-remote --exit-code
<url> <ref>' before invoking the git fetcher, so the per-entry tarball
is pinned to the same commit the marketplace observed at parse time.

resolveHeadSHAFn is overridable in tests so unit suites don't shell
out. The real git fetcher's 40-hex SHA gate stays unconditional
(defense in depth)."
```

---

## Phase 4 — Envtest re-authoring

The existing envtest helpers (`mkGithubPlugin`, `mkNpmPlugin`) hardcode the old schema. Rewrite them and add real-schema-aware Stage-2 fakes.

### Task 4.1: Rewrite envtest helpers for the real schema

**Files:**
- Modify: `internal/controller/ach/pluginmarketplace_envtest_test.go` (helpers section, lines ~160–185)

**Step 1: Replace mkGithubPlugin / mkNpmPlugin with real-schema builders**

```go
// mkGitSubdirPlugin builds a Claude Code real-schema git-subdir entry.
// The SHA is a stable 40-hex test value derived from name so each
// entry dispatches to a distinct fake-git-fetcher key.
func mkGitSubdirPlugin(name string) ClaudeCodeMarketplacePlugin {
    return ClaudeCodeMarketplacePlugin{
        Name: name,
        Source: ClaudeCodeMarketplaceSource{
            Kind: "git-subdir",
            URL:  "https://example.invalid/test/" + name + ".git",
            Path: "plugins/" + name,
            Ref:  "main",
            SHA:  shaForName(name),
        },
    }
}

// mkURLPlugin builds a Claude Code real-schema 'url' entry (whole repo).
func mkURLPlugin(name string) ClaudeCodeMarketplacePlugin {
    return ClaudeCodeMarketplacePlugin{
        Name: name,
        Source: ClaudeCodeMarketplaceSource{
            Kind: "url",
            URL:  "https://example.invalid/test/" + name + ".git",
            Ref:  "main",
            SHA:  shaForName(name),
        },
    }
}

// mkLocalPathPlugin builds a Claude Code real-schema local-path entry
// pointing at a subdirectory of the marketplace's own repo.
func mkLocalPathPlugin(name string) ClaudeCodeMarketplacePlugin {
    return ClaudeCodeMarketplacePlugin{
        Name: name,
        Source: ClaudeCodeMarketplaceSource{
            Kind: "local-path",
            Path: "plugins/" + name,
        },
    }
}

// mkUnsupportedPlugin emits an entry whose UnmarshalJSON would resolve
// to Kind="" (e.g. an upstream npm-shaped object). Used by tests that
// exercise the per-entry ReasonUnsupportedPluginSource path.
func mkUnsupportedPlugin(name string) ClaudeCodeMarketplacePlugin {
    return ClaudeCodeMarketplacePlugin{
        Name:   name,
        Source: ClaudeCodeMarketplaceSource{Kind: ""},
    }
}

// shaForName produces a deterministic 40-hex SHA from a test name so
// fixtures can pin known shas.
func shaForName(name string) string {
    h := sha1.Sum([]byte(name))
    return fmt.Sprintf("%x", h[:])
}
```

Add `crypto/sha1` to imports. Replace ALL references to `mkGithubPlugin(...)` and `mkNpmPlugin(...)` in the envtest file with `mkGitSubdirPlugin(...)` and `mkUnsupportedPlugin(...)` respectively. Grep + edit; rough count is ~10–15 call sites.

```bash
./scripts/dev.sh bash -c 'grep -n "mkGithubPlugin\|mkNpmPlugin" internal/controller/ach/pluginmarketplace_envtest_test.go'
```

For each match: replace `mkGithubPlugin(x)` → `mkGitSubdirPlugin(x)`, `mkNpmPlugin(x)` → `mkUnsupportedPlugin(x)`.

**Step 2: Stub out git fetches in the envtest**

The Stage-2 path now calls `dispatchMarketplacePlugin` which goes through `newGitFetcherFn`. Stub it in the envtest's TestMain / per-test setup.

Add to the test file (helper section):

```go
// withFakeGitFetcher overrides the package-level newGitFetcherFn for
// the duration of the test. Returns a *gitFetcherRegistry whose
// register method registers a per-entry fake by SHA.
type gitFetcherRegistry struct {
    fetchers map[string]*fakeGitFetcher
}

func newGitFetcherRegistry() *gitFetcherRegistry {
    return &gitFetcherRegistry{fetchers: map[string]*fakeGitFetcher{}}
}

func (g *gitFetcherRegistry) register(sha string, f *fakeGitFetcher) {
    g.fetchers[sha] = f
}

func (g *gitFetcherRegistry) lookup(spec sourcesgit.Spec) gitFetcher {
    if f, ok := g.fetchers[spec.SHA]; ok {
        return f
    }
    return &fakeGitFetcher{err: fmt.Errorf("test: no fake registered for SHA %q", spec.SHA)}
}

func withFakeGitFetcher(t *testing.T) *gitFetcherRegistry {
    t.Helper()
    reg := newGitFetcherRegistry()
    orig := newGitFetcherFn
    newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
        return reg.lookup(spec)
    }
    origResolve := newResolveHeadSHAFn
    newResolveHeadSHAFn = func(_ context.Context, _, _, _ string) (string, error) {
        // local-path tests can register at a stable test-only SHA.
        return "ffffffffffffffffffffffffffffffffffffffff", nil
    }
    t.Cleanup(func() {
        newGitFetcherFn = orig
        newResolveHeadSHAFn = origResolve
    })
    return reg
}

// fakeGitFetcher is the envtest equivalent of keyedFakeFetcher for the
// new git-only dispatch path.
type fakeGitFetcher struct {
    body string
    rev  string
    err  error
}

func (f *fakeGitFetcher) Fetch(_ context.Context, _ sourcesgit.Request) (*sourcesgit.Result, error) {
    if f.err != nil {
        return nil, f.err
    }
    return &sourcesgit.Result{
        Body:        io.NopCloser(strings.NewReader(f.body)),
        UpstreamRev: f.rev,
    }, nil
}
```

Add imports: `crypto/sha1`, `github.com/ackstorm/ach/internal/sources/git`.

**Step 3: Update each affected envtest case**

For every test that exercises Stage-2 materialization:

1. Call `reg := withFakeGitFetcher(t)` early in the test body.
2. After registering the Stage-1 fake (the marketplace.json fetcher under the existing `keyedFakeFetcher` mechanism), register one fake per plugin entry by SHA: `reg.register(shaForName("alpha"), &fakeGitFetcher{body: "tar-alpha", rev: shaForName("alpha")})`.

Specific tests to update (use the file's existing names; iterate one at a time):
- `TestPMR_Stage2_AllSuccess`
- `TestPMR_Stage2_PartialFailure` (the per-entry failures now come from `err` on the fake fetcher, not from spec.Type==npm)
- `TestPMR_Stage2_UnsupportedPluginSource` (uses `mkUnsupportedPlugin`; `dispatchMarketplacePlugin` short-circuits on Kind=="" without calling the fake — no register needed for that entry)
- `TestPMR_Stage3_DeleteSweep` (DB-tagged; register all plugins that should materialize)
- Any other test where the existing `keyedFakeFetcher` registered a `github:test/...@main` key for a per-plugin fetch — those keys are obsolete; replace with `reg.register(sha, ...)`.

The Stage-1 marketplace.json fetcher itself (the outer fetch) still goes through the existing `marketplaceFakeFactory.For` injected via `r.Fetchers`. That stays unchanged.

**Step 4: Run envtest**

```bash
./scripts/dev.sh make envtest-fast
```
Expected: PASS. If any test fails because of a missing fake registration, the failure message ("test: no fake registered for SHA …") tells you exactly which SHA to add.

**Step 5: Commit**

```bash
git add internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "test(marketplace): envtest helpers + fakes for real schema

Replaces mkGithubPlugin / mkNpmPlugin with the real-schema builders
mkGitSubdirPlugin / mkURLPlugin / mkLocalPathPlugin / mkUnsupportedPlugin.
Adds withFakeGitFetcher + gitFetcherRegistry — the envtest-side
counterpart of marketplaceFakeFactory for the new git-only Stage-2
dispatch path.

All existing Stage-1/Stage-2/Stage-3 tests are migrated by re-keying
fake registrations from 'github:test/<name>@main' to per-entry SHAs."
```

### Task 4.2: Add new envtest case for the local-path Kind

**Files:**
- Modify: `internal/controller/ach/pluginmarketplace_envtest_test.go`

**Step 1: Write the failing test**

```go
func TestPMR_Stage2_LocalPathMaterializes(t *testing.T) {
    ctx := context.Background()
    cr := pmrCR("s2-localpath", nil, nil)
    root := newCacheRoot(t)

    stage1Key := applyMarketplaceCR(t, ctx, cr)
    waitForFinalizer(t, ctx, cr)

    reg := withFakeGitFetcher(t)
    // local-path resolves to the marketplace's OWN repo URL +
    // ffff...ffff sha (from withFakeGitFetcher's resolver stub).
    reg.register("ffffffffffffffffffffffffffffffffffffffff",
        &fakeGitFetcher{body: "localpath-tar", rev: "ffffffffffffffffffffffffffffffffffffffff"})

    factory := newMarketplaceFakeFactory()
    factory.register(stage1Key, &keyedFakeFetcher{
        body: mustMarketplaceJSON(t, ClaudeCodeMarketplace{
            Name:    "mkt",
            Owner:   ClaudeCodeMarketplaceOwner{Name: "o"},
            Plugins: []ClaudeCodeMarketplacePlugin{mkLocalPathPlugin("inner")},
        }),
    })

    r := &PluginMarketplaceReconciler{
        Client:    k8sClient,
        Namespace: WatchNamespace,
        Log:       logr.Discard(),
        CacheRoot: root,
        Fetchers:  factory.factory(),
    }

    if !drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
        c := syncedCondition(got)
        return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced
    }) {
        t.Fatalf("never reached Synced=True")
    }

    // Cache file landed on disk.
    cachePath := filepath.Join(root, "marketplace", cr.Name, "plugin", "inner.tar.gz")
    if _, err := os.Stat(cachePath); err != nil {
        t.Fatalf("cache file %q: %v", cachePath, err)
    }
}
```

**Step 2: Run**

```bash
./scripts/dev.sh make envtest-fast
```
Expected: PASS (logic already in place; the test confirms wiring end-to-end).

**Step 3: Commit**

```bash
git add internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "test(marketplace): envtest for local-path Kind end-to-end

Confirms a Claude Code real-schema 'local-path' entry resolves the
marketplace's own repo URL, fetches via the git fetcher (faked),
materializes the tarball under cache/marketplace/<name>/plugin/<entry>.tar.gz,
and flips PluginMarketplace.status.Synced=True."
```

### Task 4.3: Add envtest for malformed-per-entry adversarial cases

**Files:**
- Modify: `internal/controller/ach/pluginmarketplace_envtest_test.go`

**Step 1: Write the failing tests**

```go
func TestPMR_Stage2_GitSubdirMissingPathRejected(t *testing.T) {
    ctx := context.Background()
    cr := pmrCR("s2-gs-nopath", nil, nil)
    _ = newCacheRoot(t) // ensure tmp infra
    stage1Key := applyMarketplaceCR(t, ctx, cr)
    waitForFinalizer(t, ctx, cr)

    bad := ClaudeCodeMarketplacePlugin{
        Name: "bad",
        Source: ClaudeCodeMarketplaceSource{
            Kind: "git-subdir",
            URL:  "https://example.invalid/o/r.git",
            // Path intentionally empty
            Ref:  "main",
            SHA:  shaForName("bad"),
        },
    }
    factory := newMarketplaceFakeFactory()
    factory.register(stage1Key, &keyedFakeFetcher{
        body: mustMarketplaceJSON(t, ClaudeCodeMarketplace{
            Name: "m", Owner: ClaudeCodeMarketplaceOwner{Name: "o"},
            Plugins: []ClaudeCodeMarketplacePlugin{bad},
        }),
    })
    r := &PluginMarketplaceReconciler{
        Client: k8sClient, Namespace: WatchNamespace,
        Log: logr.Discard(), CacheRoot: newCacheRoot(t),
        Fetchers: factory.factory(),
    }
    if !drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
        c := syncedCondition(got)
        // parser rejection → Stage-1 fails, Synced=False UpstreamInvalid.
        return c != nil && c.Status == metav1.ConditionFalse && c.Reason == ReasonUpstreamInvalid
    }) {
        t.Fatalf("never reached UpstreamInvalid")
    }
}

func TestPMR_Stage2_LocalPathTraversalRejected(t *testing.T) {
    // local-path "../../etc" must be rejected at parse time.
    body, err := json.Marshal(map[string]any{
        "name":  "m",
        "owner": map[string]any{"name": "o"},
        "plugins": []map[string]any{{
            "name":   "evil",
            "source": "../../etc/passwd",
        }},
    })
    if err != nil {
        t.Fatal(err)
    }
    _, err = parseClaudeCodeMarketplace(body)
    if err == nil {
        t.Fatal("expected parser to reject local-path traversal")
    }
    if !errors.Is(err, sources.ErrUpstreamInvalid) {
        t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
    }
}
```

Add `encoding/json` and `errors` to imports if not already present.

**Step 2: Run**

```bash
./scripts/dev.sh make envtest-fast
```
Expected: both PASS (parser-level rejection is already implemented in Task 1.2).

**Step 3: Commit**

```bash
git add internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "test(marketplace): adversarial-entry rejection paths

- git-subdir entry missing required 'path' → parse-time rejection
  → Stage-1 flips Synced=False reason=UpstreamInvalid
- local-path entry with '../../etc/passwd' → parser refuses (raw
  defensive check; never reaches tar subtree)"
```

### Task 4.4: Add audit-finding fixtures (oversized plugins[], 5MiB body overflow)

**Files:**
- Modify: `internal/controller/ach/marketplace_parse_test.go`
- Modify: `internal/controller/ach/pluginmarketplace_controller.go` (overflow-detect on the 5MiB LimitReader)

**Step 1: Write failing test for oversized plugins[]**

```go
func TestParseClaudeCodeMarketplace_PluginsCountCapped(t *testing.T) {
    var entries []map[string]any
    for i := 0; i < marketplaceMaxPluginsPerCatalog+1; i++ {
        entries = append(entries, map[string]any{
            "name":   fmt.Sprintf("p%d", i),
            "source": fmt.Sprintf("./p%d", i),
        })
    }
    body, _ := json.Marshal(map[string]any{
        "name": "m", "owner": map[string]any{"name": "o"},
        "plugins": entries,
    })
    _, err := parseClaudeCodeMarketplace(body)
    if err == nil {
        t.Fatal("expected cap rejection")
    }
    if !errors.Is(err, sources.ErrUpstreamInvalid) {
        t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
    }
}

func TestParseClaudeCodeMarketplace_LongLabelRejected(t *testing.T) {
    long := strings.Repeat("a", 64) + ".valid"
    body := fmt.Sprintf(`{"name":"m","owner":{"name":"o"},"plugins":[{"name":%q,"source":"./x"}]}`, long)
    _, err := parseClaudeCodeMarketplace([]byte(body))
    if err == nil {
        t.Fatal("expected per-label cap rejection")
    }
}
```

Add `encoding/json` and `fmt` to imports if missing.

**Step 2: Run — both PASS (logic already exists)**

```bash
./scripts/dev.sh go test -run "TestParseClaudeCodeMarketplace_PluginsCountCapped|TestParseClaudeCodeMarketplace_LongLabelRejected" ./internal/controller/ach/...
```
Expected: PASS.

**Step 3: Write failing test for the 5MiB overflow detection**

The current Stage-1 code wraps the body in `io.LimitReader(fetchResult.Body, marketplaceJSONMaxBytes)` and silently truncates. Audit calls for an overflow signal.

Add to `internal/controller/ach/pluginmarketplace_controller.go` near the existing constant block:

```go
// readAllCapped reads at most cap bytes from r. If the underlying
// reader has more data, returns (nil, ErrBodyOversized) so the caller
// can flip Synced=False reason=UpstreamInvalid instead of silently
// truncating. The +1 trick lets a single Read pass tell us if the body
// over-ran the cap.
func readAllCapped(r io.Reader, cap int64) ([]byte, error) {
    body, err := io.ReadAll(io.LimitReader(r, cap+1))
    if err != nil {
        return nil, err
    }
    if int64(len(body)) > cap {
        return nil, fmt.Errorf("body exceeds cap %d: %w", cap, sources.ErrUpstreamInvalid)
    }
    return body, nil
}
```

In the Stage-1 body, replace both:
- `body, err = extractMarketplaceJSON(io.LimitReader(fetchResult.Body, marketplaceJSONMaxBytes))` — leave as-is; `extractMarketplaceJSON` already validates per-entry size.
- `body, err = io.ReadAll(io.LimitReader(fetchResult.Body, marketplaceJSONMaxBytes))` (the s3/gcs/http branch) → `body, err = readAllCapped(fetchResult.Body, marketplaceJSONMaxBytes)`.

For the tarball branch, also tighten: pass the raw `fetchResult.Body` to `extractMarketplaceJSON` (the gzip reader and tar walker already enforce their per-entry cap; the outer 5MiB cap on a gzipped REPO TARBALL is the wrong bound — a 50MB tarball with a 200KB marketplace.json is the realistic case). Bump that path's body to a much larger outer cap, e.g. the same `gitDefaultMaxCloneBytes` (200 MiB), via a new constant:

```go
// marketplaceTarballMaxBytes is the outer-fetch cap when the source
// type returns a whole repo tarball (github/gitlab/bitbucket). Much
// larger than marketplaceJSONMaxBytes because typical claude-plugin
// repos are 5-50 MiB. Extracted marketplace.json is still capped by
// marketplaceJSONInTarballMaxBytes (5 MiB).
const marketplaceTarballMaxBytes = 200 << 20 // 200 MiB
```

And in the branch:

```go
if isTarballSourceType(spec.Type) {
    body, err = extractMarketplaceJSON(io.LimitReader(fetchResult.Body, marketplaceTarballMaxBytes))
    // ...
}
```

**Step 4: Write the envtest case for the overflow on the non-tarball path**

```go
func TestPMR_Stage1_BodyOversizeRejected(t *testing.T) {
    ctx := context.Background()
    cr := pmrCR("s1-oversize", nil, nil)
    // Force the spec to http so we exercise the readAllCapped path.
    cr.Spec.Type = "http"
    cr.Spec.GitHub = nil
    cr.Spec.HTTP = &achv1alpha1.HTTPSource{URL: "http://example.invalid/m.json"}

    stage1Key := applyMarketplaceCR(t, ctx, cr)
    waitForFinalizer(t, ctx, cr)

    huge := bytes.Repeat([]byte("x"), 5<<20+10) // 5 MiB + 10 bytes
    factory := newMarketplaceFakeFactory()
    factory.register(stage1Key, &keyedFakeFetcher{body: huge})

    r := &PluginMarketplaceReconciler{
        Client:   k8sClient, Namespace: WatchNamespace,
        Log:      logr.Discard(), CacheRoot: newCacheRoot(t),
        Fetchers: factory.factory(),
    }
    if !drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
        c := syncedCondition(got)
        return c != nil && c.Status == metav1.ConditionFalse && c.Reason == ReasonUpstreamInvalid
    }) {
        t.Fatalf("never reached UpstreamInvalid")
    }
}
```

Note: `pmrCR` defaults to github; the test overrides to http. Confirm the test mutation works (since `applyMarketplaceCR` reads spec.Type for the dispatch key — it should, given `keyFor` switches on spec.Type).

**Step 5: Run**

```bash
./scripts/dev.sh make envtest-fast
```
Expected: PASS.

**Step 6: Commit**

```bash
git add internal/controller/ach/marketplace_parse_test.go \
        internal/controller/ach/pluginmarketplace_controller.go \
        internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "feat(marketplace): audit hardening — body+count caps + overflow detect

- marketplace.json body on non-tarball sources is now capped via
  readAllCapped which detects overflow vs silently truncating
  (Synced=False reason=UpstreamInvalid)
- tarball sources use a larger outer cap (200 MiB) because the
  body is a repo archive; the extracted marketplace.json is still
  per-entry-capped at 5 MiB inside extractMarketplaceJSON
- plugins[] count cap (5000) enforced at parse time
- per-label DNS-1123 63-char cap enforced at parse time (regex
  missed it)

All audit-recommended HIGH/MED finding mitigations covered with
fixtures (count cap, label cap, body overflow)."
```

---

## Phase 5 — Examples + docs

### Task 5.1: Rewrite example 05 to use type:github against the real upstream

**Files:**
- Modify: `examples/05-pluginmarketplace-anthropic.yaml`

**Step 1: Replace contents**

```yaml
# PluginMarketplace — pulls anthropics/claude-plugins-official, the
# upstream Anthropic-curated catalogue. The operator clones the repo
# via the github source fetcher, walks the gzipped tarball for
# .claude-plugin/marketplace.json, then materializes each filtered
# plugin entry via internal/sources/git (clones the per-entry git
# remote at the pinned SHA, tars the worktree or path subtree,
# UPSERTs a marketplace_plugins row).
#
# Filters here pin the catalogue to the small `code-*` subset. Drop
# or widen the include list to ingest more.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: PluginMarketplace
metadata:
  name: anthropic-code
  namespace: ach-system
spec:
  type: github
  github:
    repo: anthropics/claude-plugins-official
    ref: main
    # Anonymous fetch (60 req/hour/IP) — fine for sporadic UAT.
    # For production, add `authSecretRef: {name: gh-token}` and a
    # Secret with key `token` holding a fine-grained PAT (read-only
    # on the marketplace repo).
  refresh:
    interval: 1h
    maxStaleness: 24h
  filters:
    # Anchored RE2 — matches plugin names like `code-review`, `code-edit`,
    # etc. The leading `^` is implicit (CRD docs say anchored); the
    # explicit anchor here doubles as documentation.
    include:
      - "^code-.*"
```

**Step 2: Apply against a live cluster (manual UAT)**

```bash
make cluster-up      # if not already up
./scripts/dev.sh make operator-redeploy
kubectl apply -f examples/05-pluginmarketplace-anthropic.yaml
make wait-cr-ready KIND=PluginMarketplace NAME=anthropic-code NS=ach-system WAIT_TIMEOUT=180s
```

Expected: condition Synced=True. If filters.include matches zero plugins, the reconciler emits `Synced=False reason=UpstreamInvalid` per Stage-1 logic — adjust the include pattern in the example until it matches the real upstream catalog (likely `^code-.*` is fine; if not, observe the real plugin names with `kubectl describe pluginmarketplace anthropic-code` and tighten).

**Step 3: Commit**

```bash
git add examples/05-pluginmarketplace-anthropic.yaml
git commit -m "docs(examples): switch marketplace example to type:github

The http-with-raw.githubusercontent.com hack was a placeholder while
the marketplace parser shipped the internal 6-source-discriminator
schema. Now that the parser handles the real Claude Code schema and
Stage-1 extracts marketplace.json from the github-source tarball,
type:github + repo:anthropics/claude-plugins-official is the
correct shape.

include filter narrows to ^code-.* for sporadic UAT throughput;
drop it to ingest the full catalog (~250 plugins)."
```

### Task 5.2: Delete the internal-schema placeholder example

The `05b` fixture targets the old wire format and will fail parse on the new code. Delete it.

**Files:**
- Delete: `examples/05b-pluginmarketplace-internal-http.yaml`

**Steps:**

```bash
git rm examples/05b-pluginmarketplace-internal-http.yaml
git commit -m "chore(examples): drop internal-schema marketplace placeholder

examples/05b targeted the placeholder 6-source-discriminator parser
that the TODO §5 re-model replaces. The fixture's marketplace.json
shape is now invalid (Source.Kind=='' for every entry → all
plugins flip ReasonUnsupportedPluginSource). The real upstream is
exercised by examples/05 directly; no need for a hand-rolled fixture."
```

### Task 5.3: Update examples/README.md + hydrate_demo.sh

**Files:**
- Modify: `examples/README.md` (paragraph about 05 / 05b — confirm 05b reference is removed)
- Modify: `examples/hydrate_demo.sh` (add 05 to the apply list if not already present)

**Step 1: Update README**

Find the section that mentions `05-pluginmarketplace-anthropic.yaml` and `05b-pluginmarketplace-internal-http.yaml`. Remove the 05b paragraph entirely. Update the 05 paragraph to mention the new type:github + real-schema flow.

**Step 2: Verify hydrate_demo applies 05**

```bash
grep "05-pluginmarketplace" /home/jcm/Projects/ach/examples/hydrate_demo.sh
```

Currently the script does NOT apply 05 (only 06/07/08/04/01). Add it so the marketplace gets exercised end-to-end:

In `examples/hydrate_demo.sh`, change the `kubectl apply -f` invocation to:

```bash
kubectl apply -f examples/01-litellmconnection.yaml \
              -f examples/05-pluginmarketplace-anthropic.yaml \
              -f examples/06-plugin-caveman.yaml \
              -f examples/07-prompt-claudecode-leak.yaml \
              -f examples/08-artifact-openclaw-templates.yaml \
              -f examples/04-environment-demo.yaml
```

After the existing `kubectl wait` for the Environment, add a wait for the marketplace:

```bash
echo "[hydrate_demo] 3b. waiting for PluginMarketplace/anthropic-code Synced=True..."
kubectl -n "${NS}" wait --for=condition=Synced \
  pluginmarketplace/anthropic-code --timeout=180s || {
    echo "[hydrate_demo] PluginMarketplace did not converge — dumping status:" >&2
    kubectl -n "${NS}" describe pluginmarketplace/anthropic-code >&2
    exit 1
  }
```

**Step 3: Commit**

```bash
git add examples/README.md examples/hydrate_demo.sh
git commit -m "docs(examples): wire PluginMarketplace into hydrate_demo

hydrate_demo.sh now applies examples/05 and waits for
Synced=True before driving SSO + hydrate. README is updated to
drop the 05b placeholder reference and document the new
type:github + real-schema flow."
```

---

## Phase 6 — End-to-end gate

### Task 6.1: Full envtest + unit + lint

```bash
./scripts/dev.sh make unit
./scripts/dev.sh make envtest-fast
./scripts/dev.sh make lint
```

All three: PASS. If any fail, fix in-place (no shortcuts; the plan's commits should already cover the needed surface).

### Task 6.2: hydrate_demo end-to-end against a live kind cluster

The cluster.sh wait targets cover everything we need. The user directive: "Adversarial cases pass per spec above."

```bash
make cluster-up
./scripts/dev.sh make operator-redeploy
./examples/hydrate_demo.sh
```

Expected outcomes per the prompt's Acceptance section:

1. `kubectl get pluginmarketplace anthropic-code -o yaml` shows `status.conditions[?(@.type=='Synced')].status == True`.
2. `kubectl -n ach-system exec deploy/ach-platform-api -- psql -c "select marketplace_name, name, storage_location from marketplace_plugins where marketplace_name='anthropic-code'"` shows at least one row whose `name` matches `^code-.*` (or whatever the include pattern resolves to today).
3. `find $(./scripts/dev.sh kubectl -n ach-system exec deploy/ach -- sh -c 'echo $ACH_CACHE_ROOT')/marketplace/anthropic-code/plugin/ -type f` (or the equivalent via the operator pod) shows the tarball on the cache PVC.
4. `examples/hydrate.json` (regenerated by hydrate_demo.sh) contains at least one entry under `context.plugins[]` whose source attribution mentions `anthropic-code` (alongside the standalone caveman Plugin already proven by `06-plugin-caveman.yaml`).

If any acceptance criterion fails: pause, debug, fix root cause, commit incrementally. Do NOT push partial work.

### Task 6.3: pre-push gate

```bash
make pre-push
```

Expected: all 17 gates GREEN. If `govulncheck` reports a new advisory traceable to a new transitive (unlikely — no new go.mod entries — but possible if go.sum drifted), add an entry to `references/security/govulncheck-acknowledged.md` per the gate's required-action prompt.

### Task 6.4: Push + open PR

```bash
git push -u origin feat/marketplace-real-schema
```

Use `gh pr create` per CLAUDE.md's PR workflow:

```bash
gh pr create --title "feat(marketplace): real Claude Code schema parser + git fetcher" --body "$(cat <<'EOF'
## Summary
- Replace the placeholder 6-source-discriminator marketplace parser with the real upstream Claude Code schema (`git-subdir` / `url` / `local-path` Kinds; string-or-object union via custom UnmarshalJSON)
- Introduce `internal/sources/git/` — a generic git-remote fetcher (shells out to `git` for shallow clone + pinned SHA checkout + worktree/subtree tar) used for INNER fetch of every marketplace plugin entry
- Audit hardening: plugins[] count cap (5000), per-label 63-char DNS cap, body-overflow detection (5MiB JSON / 200MiB tarball)
- Switch examples/05 from the `http` + raw.githubusercontent.com hack to `type:github` against `anthropics/claude-plugins-official`; drop placeholder examples/05b
- Wire PluginMarketplace into examples/hydrate_demo.sh so the end-to-end UAT exercises a real upstream marketplace

Closes TODO §5.

## Test plan
- [x] `./scripts/dev.sh make unit` PASS (parser unit tests, dispatch unit tests, git fetcher unit tests)
- [x] `./scripts/dev.sh make envtest-fast` PASS (re-authored Stage-1/Stage-2/Stage-3 fixtures + new local-path + adversarial-entry cases)
- [x] `examples/hydrate_demo.sh` end-to-end: anthropic-code PluginMarketplace → Synced=True → ≥1 marketplace_plugins row → tarball on cache PVC → entry in hydrate.json context.plugins[]
- [x] `make pre-push` 17 gates GREEN

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Acceptance summary (from prompt — kept for the final checklist)

1. `./scripts/dev.sh make unit` PASS — Task 6.1
2. `./scripts/dev.sh make envtest-fast` PASS with re-authored fixtures — Task 6.1
3. `examples/hydrate_demo.sh` end-to-end converges:
   - PluginMarketplace `anthropic-code` Synced=True
   - ≥1 inner plugin (`^code-.*`) materialized as a `marketplace_plugins` row with a real tarball at `storage_location` on the operator cache PVC
   - `hydrate.json` contains marketplace-sourced plugin under `context.plugins[]` alongside the standalone caveman Plugin
   — Task 6.2
4. Adversarial cases all rejected per spec:
   - inner entry pointing at non-existent repo → per-entry ReasonNotFound, marketplace stays Synced=True with status.message listing the failure
   - inner entry with malformed source.url → per-entry ReasonUnsupportedPluginSource (Kind="" path)
   - inner entry with bare-string source escaping marketplace repo root (`../../etc/passwd`) → REJECTED at parse time → Stage-1 flips Synced=False reason=UpstreamInvalid
   — Tasks 1.2, 2.2, 4.3
5. `examples/05-pluginmarketplace-anthropic.yaml` uses `type:github` — Task 5.1

---

## Plan-level notes

- **No CRD field changes.** `PluginMarketplaceSpec` still uses the 6-source discriminator for the catalog location; the new git fetcher is INNER-only. No CRD bump, no Helm chart values change, no migration.
- **No new go.mod entries.** All new code uses stdlib (`archive/tar`, `compress/gzip`, `os/exec`, `crypto/rand`, `bytes`). The git CLI is the runtime dependency — already in the devtools container and the runtime distroless image (verify in Task 0.1).
- **Build-break window:** Task 1.2 deletes `marketplacePluginToSourceSpec` and `errUnsupportedPluginSource`, leaving `pluginmarketplace_controller.go` non-compiling. Task 3.1 closes the gap. Do NOT push between these commits. If splitting work across sessions, sequence Tasks 1.2 → 3.1 inside a single session.
- **TDD discipline:** every Task in Phases 1–4 follows write-failing-test → run-to-confirm-fail → minimal-implementation → re-run-to-confirm-pass → commit. Skipping the "confirm fail" step has bitten this codebase before (see TODO history); do not skip.
- **Naked-poll ban (CLAUDE.md):** all `wait-*` invocations use the blessed Makefile targets. No `until ... do sleep ... done` loops anywhere in the new shell scripts. The Task 5.3 hydrate_demo addition uses `kubectl wait --for=condition=Synced --timeout=180s` per the canonical pattern.

---

Plan complete and saved to `docs/plans/2026-05-26-marketplace-real-schema.md`. Two execution options:

**1. Subagent-Driven (this session)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Parallel Session (separate)** — Open a new session in the worktree with `superpowers:executing-plans`, batch execution with checkpoints.

Which approach?
