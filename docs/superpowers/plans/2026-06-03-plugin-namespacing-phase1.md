# Plugin Collision Namespacing — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace hydrate's fail-fast on cross-plugin destination collisions with a `--conflict=namespace|skip|overwrite|refuse` policy (default `namespace`), namespacing colliding plugin resources by a `<plugin>-<name>` leaf prefix so two plugins shipping a same-named agent/skill/command coexist instead of aborting the hydrate.

**Architecture:** The projection leg (`internal/cli/hydrate/wiring.go` `projectPlugins`) currently projects each plugin's resources via `route.Project` and publishes them inline, fail-fasting when a second plugin lands on an already-`claimed[]` `Target`. Phase 1 restructures this to a **two-pass** within the function: (1) collect every plugin's projected `FileWrite`s without publishing; (2) detect `Target` collisions across plugins, apply the conflict policy (namespace → leaf-prefix the colliding writes; skip/overwrite/refuse per flag), then publish. The policy threads from a new `--conflict` cobra flag → `hydrateInputs` → `hydrate.NewWiring` → `adapterDispatcherImpl.conflict`. Composite (`CLAUDE.md`/`GEMINI.md`) and MergeDeep (MCP config) writes keep their existing co-ownership exemptions and are never namespaced.

**Tech Stack:** Go (stdlib `testing`), cobra. No new third-party deps. In-container toolchain via `./scripts/dev.sh` / `make test-unit-pkg`.

**Scope boundary (read before starting):** Phase 1 is intra-run collision resolution only. It does NOT add a cross-run ownership lockfile or content-hash dedup (Phase 2) and does NOT add the native claude-code `--claude-plugins` skills-dir mode (Phase 3). Determinism across re-hydrates is achieved by sorting plugin names so the same inputs always produce the same prefixed paths; no `state.json` migration is required because the final `Target` strings are stable run-to-run for identical manifests.

**Source of truth:** the research + locked decisions live in
`docs/superpowers/plans/2026-06-03-plugin-collision-namespacing-research.md`.

---

## File map

- Create `internal/cli/hydrate/conflict.go` — `ConflictPolicy` enum + `ParseConflictPolicy(string)`.
- Create `internal/cli/hydrate/conflict_test.go` — policy parse tests.
- Create `internal/cli/hydrate/namespace.go` — `namespaceLeaf(path, plugin)` + `shouldSkipPrefix(leaf, plugin)` + skill-marker handling.
- Create `internal/cli/hydrate/namespace_test.go` — namespacing unit tests.
- Modify `internal/cli/hydrate/flags.go` — add `Conflict ConflictPolicy` to `Opts` (carried for completeness/testability).
- Modify `internal/cli/hydrate/wiring.go` — add `conflict` field to `adapterDispatcherImpl`; add `conflict` param to `NewWiring`; rewrite `projectPlugins` into the two-pass collision resolver.
- Modify `internal/cli/hydrate/wiring_test.go` (or a new `projectplugins_test.go`) — two-plugin collision integration tests per policy.
- Modify `cmd/ach-cli/cmd/hydrate.go` — add `--conflict` flag, `hydrateInputs.conflict`, pass to `NewWiring`.
- Modify `cmd/ach-cli/cmd/hydrate_test.go` — flag-parse + default test.

**Anchors verified in the live code (do NOT trust from memory — re-open and confirm before editing):**
- `internal/cli/adapter/adapter.go:82` `FileWrite{ Path, Content, SourceHash, Merge, Keys }`; `MergeDeep`/`MergeComposite`/`MergeReplace` at `adapter.go:25/32/37`.
- `internal/cli/hydrate/wiring.go:305` `type adapterDispatcherImpl struct { platformID string; force bool; global bool }`.
- `internal/cli/hydrate/wiring.go:1713` `func NewWiring(client *httpclient.Client, platformID string, limits extract.Limits, allowSymlinks bool, force bool, global bool) (Extractor, AdapterDispatcher)`.
- `internal/cli/hydrate/wiring.go:413` `func (d *adapterDispatcherImpl) projectPlugins(ad adapter.Adapter, s *state.File, achDir, toolRoot string, result *RenderResult) error` — the loop reads `os.ReadDir(<achDir>/plugin)`, per dir calls `route.Project(rules, pluginSrc, "")`, then for each `fw`: remaps global path, runs the `claimed[]` collision fail-fast (skipped for `MergeComposite`), sets `fw.Keys=[plugin]` for composite, and publishes via `publishFile`.
- `cmd/ach-cli/cmd/hydrate.go:513` `ext, ad := hydrate.NewWiring(hc, platformID, limits, in.allowSymlinks, in.force, in.global)`; `Opts` built at `:515`.
- `internal/cli/hydrate/flags.go:22` `type Opts struct`.

---

## Task 1: ConflictPolicy type + parser

**Files:**
- Create: `internal/cli/hydrate/conflict.go`
- Test: `internal/cli/hydrate/conflict_test.go`

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: Apache-2.0

package hydrate

import "testing"

func TestParseConflictPolicy(t *testing.T) {
	cases := []struct {
		in      string
		want    ConflictPolicy
		wantErr bool
	}{
		{"namespace", ConflictNamespace, false},
		{"skip", ConflictSkip, false},
		{"overwrite", ConflictOverwrite, false},
		{"refuse", ConflictRefuse, false},
		{"", ConflictNamespace, false}, // empty → default
		{"NAMESPACE", ConflictNamespace, false}, // case-insensitive
		{"bogus", ConflictNamespace, true},
	}
	for _, c := range cases {
		got, err := ParseConflictPolicy(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseConflictPolicy(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Errorf("ParseConflictPolicy(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestConflictPolicy_String(t *testing.T) {
	if ConflictNamespace.String() != "namespace" {
		t.Errorf("String() = %q want namespace", ConflictNamespace.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/ FOCUS=TestParseConflictPolicy`
Expected: FAIL — `undefined: ConflictPolicy` / `ParseConflictPolicy`.

- [ ] **Step 3: Write minimal implementation**

```go
// SPDX-License-Identifier: Apache-2.0

// ConflictPolicy selects how the plugin projection leg resolves a
// cross-plugin destination collision (two plugins projecting to the same
// workspace path). Default `namespace` keeps both by leaf-prefixing;
// `refuse` reproduces the pre-Phase-1 CR-01 fail-fast.
package hydrate

import "fmt"

type ConflictPolicy int

const (
	// ConflictNamespace leaf-prefixes the colliding writes (<plugin>-<name>)
	// so both plugins' resources survive. Default.
	ConflictNamespace ConflictPolicy = iota
	// ConflictSkip drops the later-sorted plugin's colliding write, keeping
	// the first.
	ConflictSkip
	// ConflictOverwrite lets the later-sorted plugin's write win (last-wins).
	ConflictOverwrite
	// ConflictRefuse fails the hydrate on any cross-plugin collision
	// (the pre-Phase-1 CR-01 behavior).
	ConflictRefuse
)

// String renders the flag spelling.
func (p ConflictPolicy) String() string {
	switch p {
	case ConflictNamespace:
		return "namespace"
	case ConflictSkip:
		return "skip"
	case ConflictOverwrite:
		return "overwrite"
	case ConflictRefuse:
		return "refuse"
	default:
		return "namespace"
	}
}

// ParseConflictPolicy maps the `--conflict` flag value to a policy.
// Empty → ConflictNamespace (the default). Unknown → error.
func ParseConflictPolicy(s string) (ConflictPolicy, error) {
	switch s {
	case "", "namespace":
		return ConflictNamespace, nil
	case "skip":
		return ConflictSkip, nil
	case "overwrite":
		return ConflictOverwrite, nil
	case "refuse":
		return ConflictRefuse, nil
	default:
		// Accept case-insensitively before erroring.
		switch toLowerASCII(s) {
		case "namespace":
			return ConflictNamespace, nil
		case "skip":
			return ConflictSkip, nil
		case "overwrite":
			return ConflictOverwrite, nil
		case "refuse":
			return ConflictRefuse, nil
		}
		return ConflictNamespace, fmt.Errorf("invalid --conflict %q; want namespace|skip|overwrite|refuse", s)
	}
}

// toLowerASCII lowercases ASCII letters without importing strings (keep
// the package dependency surface minimal in this leaf file).
func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/ FOCUS=TestParseConflictPolicy`
Expected: PASS (both `TestParseConflictPolicy` and `TestConflictPolicy_String`).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/hydrate/conflict.go internal/cli/hydrate/conflict_test.go
git commit -m "feat(hydrate): add ConflictPolicy enum + parser"
```

---

## Task 2: leaf-prefix namespacing function

**Files:**
- Create: `internal/cli/hydrate/namespace.go`
- Test: `internal/cli/hydrate/namespace_test.go`

The scheme (openPackage-proven, locked in the research doc):
- Plain file resource → prefix the **leaf filename**: `.claude/agents/cloud-architect.md` + plugin `cloud-infra` → `.claude/agents/cloud-infra-cloud-architect.md`.
- `SKILL.md`-marker resource → prefix the **parent dir**, not the file: `.claude/skills/optimize/SKILL.md` + plugin `acme` → `.claude/skills/acme-optimize/SKILL.md`.
- `shouldSkipPrefix`: if the leaf (sans extension) already equals the plugin name, skip (avoid `code-review-code-review`).

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: Apache-2.0

package hydrate

import "testing"

func TestNamespaceLeaf(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		plugin string
		want   string
	}{
		{"agent file", ".claude/agents/cloud-architect.md", "cloud-infra", ".claude/agents/cloud-infra-cloud-architect.md"},
		{"command file", ".codex/prompts/deploy.md", "ci-tools", ".codex/prompts/ci-tools-deploy.md"},
		{"skill marker dir", ".claude/skills/optimize/SKILL.md", "acme", ".claude/skills/acme-optimize/SKILL.md"},
		{"nested skill marker", ".claude/skills/optimize/extra/x.md", "acme", ".claude/skills/acme-optimize/extra/x.md"},
		{"skip when leaf==plugin", ".claude/agents/code-review.md", "code-review", ".claude/agents/code-review.md"},
		{"skip when skilldir==plugin", ".claude/skills/acme/SKILL.md", "acme", ".claude/skills/acme/SKILL.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := namespaceLeaf(c.path, c.plugin)
			if got != c.want {
				t.Errorf("namespaceLeaf(%q,%q) = %q want %q", c.path, c.plugin, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/ FOCUS=TestNamespaceLeaf`
Expected: FAIL — `undefined: namespaceLeaf`.

- [ ] **Step 3: Write minimal implementation**

```go
// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"path"
	"strings"
)

// markerFilenames are the resource markers whose PARENT DIRECTORY (not the
// file itself) carries the resource identity — for these, the namespace
// prefix is applied to the parent dir so leaf-name discovery still finds
// SKILL.md. Mirrors openPackage's marker handling.
var markerFilenames = map[string]bool{
	"SKILL.md": true,
}

// namespaceLeaf returns path with a `<plugin>-` prefix applied so two
// plugins' same-named resources no longer collide. For a marker resource
// (e.g. <skill>/SKILL.md) the prefix goes on the skill DIRECTORY segment
// immediately under the kind dir; for a plain file it goes on the leaf
// filename. When the to-be-prefixed segment already equals the plugin name
// (shouldSkipPrefix), path is returned unchanged.
//
// path is "/"-separated workspace-relative (FileWrite.Path is always
// produced by route.Project with forward slashes); use the `path` package,
// never `filepath`, so behavior is OS-independent.
func namespaceLeaf(p, plugin string) string {
	dir, leaf := path.Split(p) // dir keeps trailing slash; leaf is the last segment

	if markerFilenames[leaf] {
		// dir = ".claude/skills/optimize/" → split off the skill dir segment.
		parent := path.Clean(dir)                 // ".claude/skills/optimize"
		base := path.Base(parent)                 // "optimize"
		if shouldSkipPrefix(base, plugin) {
			return p
		}
		grand := path.Dir(parent) // ".claude/skills"
		return path.Join(grand, plugin+"-"+base, leaf)
	}

	if shouldSkipPrefix(leaf, plugin) {
		return p
	}
	return path.Join(path.Clean(dir), plugin+"-"+leaf)
}

// shouldSkipPrefix reports whether seg (minus any file extension) already
// equals plugin, so prefixing would produce a redundant `<plugin>-<plugin>`.
func shouldSkipPrefix(seg, plugin string) bool {
	base := seg
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return base == plugin
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/ FOCUS=TestNamespaceLeaf`
Expected: PASS (all 6 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/hydrate/namespace.go internal/cli/hydrate/namespace_test.go
git commit -m "feat(hydrate): add leaf-prefix namespacing helper"
```

---

## Task 3: thread `--conflict` flag → dispatcher

**Files:**
- Modify: `internal/cli/hydrate/flags.go:22` (Opts) — add `Conflict ConflictPolicy`.
- Modify: `internal/cli/hydrate/wiring.go:305` (struct) and `:1713` (NewWiring).
- Modify: `cmd/ach-cli/cmd/hydrate.go` (flag + `hydrateInputs` + NewWiring call).
- Test: `cmd/ach-cli/cmd/hydrate_test.go`

- [ ] **Step 1: Write the failing test (flag default + parse)**

Add to `cmd/ach-cli/cmd/hydrate_test.go`:

```go
func TestHydrate_ConflictFlag_Default(t *testing.T) {
	cmd := newHydrateCmd()
	f := cmd.Flags().Lookup("conflict")
	if f == nil {
		t.Fatal("--conflict flag not registered")
	}
	if f.DefValue != "namespace" {
		t.Errorf("--conflict default = %q; want namespace", f.DefValue)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd/ FOCUS=TestHydrate_ConflictFlag_Default`
Expected: FAIL — `--conflict flag not registered`.

- [ ] **Step 3a: Add `Conflict` to Opts**

In `internal/cli/hydrate/flags.go`, inside the `behavior toggles` group of `type Opts struct`, add:

```go
	// Conflict selects how cross-plugin destination collisions are
	// resolved during the projection leg. Default ConflictNamespace.
	Conflict ConflictPolicy
```

- [ ] **Step 3b: Add `conflict` field + NewWiring param**

In `internal/cli/hydrate/wiring.go`, extend the struct at `:305`:

```go
type adapterDispatcherImpl struct {
	platformID string
	force      bool
	// global marks --global scope so Render can remap adapters whose GLOBAL
	// config path differs from the simple $HOME-join (currently opencode).
	global bool
	// conflict selects the cross-plugin collision policy (Phase 1).
	conflict ConflictPolicy
}
```

And `NewWiring` at `:1713` — add the param (LAST, after `global`) and set it:

```go
func NewWiring(
	client *httpclient.Client,
	platformID string,
	limits extract.Limits,
	allowSymlinks bool,
	force bool,
	global bool,
	conflict ConflictPolicy,
) (Extractor, AdapterDispatcher) {
	ext := &extractorImpl{
		client:        client,
		limits:        limits,
		allowSymlinks: allowSymlinks,
	}
	disp := &adapterDispatcherImpl{
		platformID: platformID,
		force:      force,
		global:     global,
		conflict:   conflict,
	}
	return ext, disp
}
```

- [ ] **Step 3c: Register the cobra flag + thread into hydrateInputs + NewWiring**

In `cmd/ach-cli/cmd/hydrate.go`:
1. Add a `flagConflict string` var in `newHydrateCmd` alongside the other flags, and register it:

```go
	cmd.Flags().StringVar(&flagConflict, "conflict", "namespace",
		"Cross-plugin collision policy: namespace|skip|overwrite|refuse")
```

2. Add `conflict hydrate.ConflictPolicy` to the `hydrateInputs` struct (find it in the same file) and populate it where the struct is built from flags — parse via `hydrate.ParseConflictPolicy(flagConflict)`, returning a `&exit.CodedError{Code: exit.General, Msg: err.Error()}` on parse error.
3. At `:513`, pass it to NewWiring:

```go
	ext, ad := hydrate.NewWiring(hc, platformID, limits, in.allowSymlinks, in.force, in.global, in.conflict)
```

(Confirm the exact `hydrateInputs` field-population site by reading the function that builds `in` before editing — it is the same function that holds the `:513` NewWiring call.)

- [ ] **Step 4: Run test to verify it passes + nothing else breaks**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd/` then `make test-unit-pkg PKG=./internal/cli/hydrate/`
Expected: PASS. If any other caller of `NewWiring` exists (grep `NewWiring(` across the repo incl. tests), update each call site to pass a policy — tests can pass `hydrate.ConflictNamespace`.

Run: `grep -rn "NewWiring(" --include='*.go' | grep -v "func NewWiring"` — update every hit.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/hydrate/flags.go internal/cli/hydrate/wiring.go cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/hydrate_test.go
git commit -m "feat(hydrate): wire --conflict flag through to the dispatcher"
```

---

## Task 4: two-pass collision resolution in projectPlugins

**Files:**
- Modify: `internal/cli/hydrate/wiring.go:413` `projectPlugins`.
- Test: `internal/cli/hydrate/projectplugins_test.go` (new).

**Current behavior to preserve** (re-read `wiring.go:413-536` before editing): per plugin dir (sorted by `os.ReadDir` order — Phase 1 MUST sort explicitly for determinism), `route.Project(rules, pluginSrc, "")` returns `projected []adapter.FileWrite`; each `fw` is global-remapped, the `claimed[]` map fail-fasts on a cross-plugin same-`Target` collision (EXEMPT for `fw.Merge == adapter.MergeComposite`), composite writes get `fw.Keys = []string{plugin}`, then `publishFile` writes it and records the projected file. The `dropped` kinds aggregation + stable-sort for stderr (D-12) must be preserved.

**New behavior (two-pass):**
1. **Pass A — collect.** Iterate plugin dirs in **sorted** order; for each, run `route.Project`, global-remap each `fw`, and append `{plugin, fw}` to a slice `all []projectedWrite`. Do NOT publish yet. Still aggregate `dropped`.
2. **Resolve.** Build `byTarget map[string][]int` (indices into `all`) keyed on `fw.Path`, considering ONLY non-composite, non-MergeDeep writes (composite + MergeDeep keep today's co-ownership). For each target with >1 distinct owning plugin, apply `d.conflict`:
   - `ConflictRefuse` → return the existing CR-01 error (verbatim message).
   - `ConflictNamespace` → for EACH colliding write, set `all[i].fw.Path = namespaceLeaf(all[i].fw.Path, all[i].plugin)`. (All colliding writes get prefixed, including the first — deterministic because plugins were sorted.)
   - `ConflictSkip` → keep the first (lowest index = earliest-sorted plugin), mark the rest skipped.
   - `ConflictOverwrite` → keep the last (highest index), mark the rest skipped.
3. **Pass B — publish.** Iterate `all` in order; skip marked-skipped entries; for composite set `fw.Keys=[plugin]`; publish via the SAME `publishFile` call the current code uses, recording the projected file. After namespacing, re-check that no two surviving writes share a `Target` (a post-namespace collision = a real bug; return an error).

- [ ] **Step 1: Write the failing integration test**

Create `internal/cli/hydrate/projectplugins_test.go`. Build a temp `<achDir>/plugin/` with two plugin dirs each containing `agents/cloud-architect.md`, run the dispatcher's `projectPlugins` (or `Render` with `projectPlugins=true`) for `claude-code`, and assert per policy. Use the existing test helpers in `wiring_test.go` for constructing an `adapterDispatcherImpl` + temp dirs (READ `wiring_test.go` first to reuse its fixture builders rather than inventing new ones).

```go
// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"path/filepath"
	"testing"
)

// helper: write a plugin tree <achDir>/plugin/<name>/agents/<agent>.md
func writePluginAgent(t *testing.T, achDir, plugin, agent string) {
	t.Helper()
	dir := filepath.Join(achDir, "plugin", plugin, "agents")
	mkdirAll(t, dir)
	writeFile(t, filepath.Join(dir, agent+".md"), "# "+agent+"\n")
}

func TestProjectPlugins_Namespace_KeepsBoth(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	writePluginAgent(t, achDir, "cloud-infra", "cloud-architect")
	writePluginAgent(t, achDir, "ci-tools", "cloud-architect")

	d := newTestDispatcher(t, "claude-code", ConflictNamespace) // helper in wiring_test.go style
	var result RenderResult
	ad := lookupAdapter(t, "claude-code")
	if err := d.projectPlugins(ad, emptyState(t), achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectPlugins: %v", err)
	}
	// Both namespaced files exist; neither bare.
	assertFileExists(t, filepath.Join(toolRoot, ".claude/agents/cloud-infra-cloud-architect.md"))
	assertFileExists(t, filepath.Join(toolRoot, ".claude/agents/ci-tools-cloud-architect.md"))
	assertNoFile(t, filepath.Join(toolRoot, ".claude/agents/cloud-architect.md"))
}

func TestProjectPlugins_Refuse_FailsFast(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	writePluginAgent(t, achDir, "a", "dup")
	writePluginAgent(t, achDir, "b", "dup")
	d := newTestDispatcher(t, "claude-code", ConflictRefuse)
	ad := lookupAdapter(t, "claude-code")
	err := d.projectPlugins(ad, emptyState(t), achDir, t.TempDir(), &RenderResult{})
	if err == nil {
		t.Fatal("ConflictRefuse must fail-fast on collision")
	}
}

func TestProjectPlugins_NoCollision_BareNames(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	writePluginAgent(t, achDir, "a", "alpha")
	writePluginAgent(t, achDir, "b", "beta")
	d := newTestDispatcher(t, "claude-code", ConflictNamespace)
	ad := lookupAdapter(t, "claude-code")
	if err := d.projectPlugins(ad, emptyState(t), achDir, toolRoot, &RenderResult{}); err != nil {
		t.Fatalf("projectPlugins: %v", err)
	}
	// No collision → bare names, no prefix.
	assertFileExists(t, filepath.Join(toolRoot, ".claude/agents/alpha.md"))
	assertFileExists(t, filepath.Join(toolRoot, ".claude/agents/beta.md"))
}
```

> NOTE TO EXECUTOR: `newTestDispatcher`, `lookupAdapter`, `emptyState`, `mkdirAll`, `writeFile`, `assertFileExists`, `assertNoFile` — REUSE or thinly wrap whatever the existing `wiring_test.go` already provides (read it first). If equivalents exist under different names, use those; do not duplicate fixture infra. If none exist, add minimal `t.Helper()` wrappers in this test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/ FOCUS=TestProjectPlugins`
Expected: FAIL — `TestProjectPlugins_Namespace_KeepsBoth` fails because today's code fail-fasts on the collision (or only one bare file lands).

- [ ] **Step 3: Implement the two-pass rewrite**

Rewrite `projectPlugins` per the "New behavior (two-pass)" spec above. Keep the `validatePluginName` guard, the `dropped` aggregation + stable-sort, the global remap, the composite `fw.Keys=[plugin]` assignment, and the exact `publishFile` call/record from the current body. Introduce:

```go
type projectedWrite struct {
	plugin  string
	fw      adapter.FileWrite
	skipped bool
}
```

Sort plugin dir names with `sort.Strings` before Pass A. In Resolve, only consider `fw.Merge == adapter.MergeReplace` writes for the `byTarget` collision map (composite + MergeDeep are co-owned and exempt — same exemption the current `claimed[]` check applies via `fw.Merge != adapter.MergeComposite`; extend it to also skip `MergeDeep`). The `ConflictRefuse` arm returns the SAME error string currently at `wiring.go:485-487` (copy it verbatim, including the `owner`/`ent.Name()`/`fw.Path` args).

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/`
Expected: PASS — all `TestProjectPlugins_*` plus the pre-existing wiring tests (the old CR-01 fail-fast test, if any, should now be asserted under `ConflictRefuse`; update it to construct the dispatcher with `ConflictRefuse`).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/hydrate/wiring.go internal/cli/hydrate/projectplugins_test.go
git commit -m "feat(hydrate): policy-driven cross-plugin collision resolution"
```

---

## Task 5: end-to-end + help-text verification

**Files:**
- Modify: `cmd/ach-cli/cmd/hydrate_test.go` (help text mentions --conflict).

- [ ] **Step 1: Add help-text assertion test**

```go
func TestHydrate_Help_MentionsConflict(t *testing.T) {
	cmd := newHydrateCmd()
	if !strings.Contains(cmd.Flag("conflict").Usage, "namespace") {
		t.Errorf("--conflict usage missing policy list: %q", cmd.Flag("conflict").Usage)
	}
}
```

- [ ] **Step 2: Run it**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd/ FOCUS=TestHydrate_Help_MentionsConflict`
Expected: PASS.

- [ ] **Step 3: Full hydrate-package + cmd regression**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/` and `make test-unit-pkg PKG=./cmd/ach-cli/cmd/`
Expected: PASS, no regressions.

- [ ] **Step 4: Lint the changed packages**

Run: `make qa-lint-changed`
Expected: clean.

- [ ] **Step 5: Commit (if any test-only additions remain unstaged)**

```bash
git add cmd/ach-cli/cmd/hydrate_test.go
git commit -m "test(hydrate): assert --conflict help + e2e namespacing"
```

---

## Final verification (before declaring done)

- [ ] `make test-unit` — full unit suite green.
- [ ] `make e2e-focus RUN='...hydrate...'` if an e2e hydrate test exists that exercises plugin projection — confirm a two-plugin env no longer fails. (If the deployed env from the 2026-06-03 session is reachable, a live `ach-cli hydrate --environment platform --platform claude-code` should now namespace `cloud-architect` instead of aborting.)
- [ ] Update `docs/superpowers/plans/2026-06-03-plugin-collision-namespacing-research.md` Decisions section: mark Phase 1 DONE; note Phase 2 (ownership lockfile + content-hash dedup) and Phase 3 (native `--claude-plugins`) remain.
- [ ] CLAUDE.md "Common failure modes" / `references/troubleshooting.md`: the cross-plugin collision is now policy-resolved, not a hard error — update any doc that described it as fatal.

## Deferred to later plans (NOT in scope here)

- **Phase 2:** ownership lockfile in `state.Plugins[]` (owner-plugin per Target) for cross-run hand-edit detection + clean uninstall; content-hash dedup (byte-identical resources collapse, no prefix). Requires state v2→v3 migration.
- **Phase 3:** native claude-code `--claude-plugins` skills-dir mode (`<workspace>/.claude/skills/<plugin>/.claude-plugin/plugin.json`) for true `plugin:name` namespacing, with the launch-dir + trust caveats documented in the research doc.
