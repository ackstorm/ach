# Hydrate Drop-Warning Semantics + Rich Summary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop `ach-cli hydrate` from warning about benign plugin metadata/docs (`.claude-plugin`, `.codex-plugin`, `README.md`), warn only when a genuine plugin component kind (e.g. `hooks`, `agents`) is dropped because the target platform has no rule for it, attribute each drop to its plugin + platform, and print a clear per-target success summary.

**Architecture:** The live projection path is `route.Project` (per-adapter `ProjectionRules()` consumed by `projectPlugins` in `internal/cli/hydrate/wiring.go`). Today its "no matching rule → drop + warn" branch fires for ANY unmatched top-level entry, so metadata/docs pollute the warning (cry-wolf). We restore the three-bucket model the now-dead `TransformPlugin` path once had: **kept** (rule matched), **dropped+warned** (a *known* component kind with no rule for this target), **silent** (metadata/docs/unknown). We add per-kind kept tallies + per-kind→plugin drop attribution, surface them in a new stdout summary and a reworded stderr warning, and delete the fully-superseded `TransformPlugin`/`PluginWrite` interface surface.

**Tech Stack:** Go (stdlib `testing`), cobra CLI, the existing `internal/cli/adapter/route` projection engine + `internal/cli/hydrate` orchestrator.

---

## Background facts (verified against the tree on 2026-06-03)

- `internal/cli/adapter/route/route.go` `Project()` returns `([]adapter.FileWrite, []string, error)`; the `[]string` is the deduped+sorted set of unmatched top-level kinds. The no-rule branch (`route.go:300-309`) calls `dropped.add(topLevel)` for **every** unmatched entry.
- `projectPlugins` (`internal/cli/hydrate/wiring.go:413-536`) calls `route.Project` once per plugin dir under `<achDir>/plugin/<name>`; `ent.Name()` is the plugin name. It already aggregates `route.Project`'s drops + the runtime-wins MCP shadow drops (`dropRuntimeOwnedMCP`, `fwDrops`) into `result.DroppedComponents`.
- `RenderResult.DroppedComponents` (`internal/cli/hydrate/result.go:107`) → copied into `Result.DroppedComponents` in `commit.go:419`.
- `commit.go:822-842` `warnDroppedComponents` emits the single line `warning: dropped unsupported components for platform <id>: <kinds>`.
- `cmd/ach-cli/cmd/hydrate.go:540` **discards** the `Result`: `if _, err := hydrateRunFn(...)`. There is NO success summary today — only the pk_/plaintext/dropped warnings (all on stderr).
- Adapter rule tables (source-kind → first segment matched by `matchRule`):
  | kind | claude | codex | gemini | opencode | pimono |
  |------|--------|-------|--------|----------|--------|
  | commands | ✓ | ✓ | ✓ | ✓ | ✓ |
  | agents | ✓ | ✓ | ✓ | ✓ | ✗ |
  | skills | ✓ | ✓ | ✓ | ✓ | ✓ |
  | mcp | ✓ | ✓ | ✓ | ✓ | ✓ |
  | rules | ✓ | ✗ | ✗ | ✗ | ✗ |
  | prompts | ✗ | ✗ | ✓ | ✗ | ✗ |
  | AGENTS.md | ✓ | ✗ | ✓ | ✗ | ✗ |
  | hooks | ✗ | ✗ | ✗ | ✗ | ✗ |
  So `KnownComponentKinds = {rules, commands, agents, skills, mcp, prompts, AGENTS.md, hooks}`.
- `Adapter.TransformPlugin` (`internal/cli/adapter/adapter.go:175`) + `PluginWrite` (`adapter.go:111-129`) are implemented by all 5 adapters but have **zero production call sites** (`grep '\.TransformPlugin('` → only `*_test.go`). The production path is `route.Project`. Helpers `componentKept`/`componentDropped`/`readPluginVersion` (gemini) and `droppableComponent` (opencode) exist solely to serve `TransformPlugin`.

## File Structure

**Create:**
- `internal/cli/adapter/route/kinds.go` — the canonical `KnownComponentKinds` set + doc. One responsibility: the source-format component vocabulary that gates drop-warnings.
- `internal/cli/adapter/route/kinds_test.go` — guards the set membership (regression anchor so a new adapter rule that adds a kind also adds it here).

**Modify:**
- `internal/cli/adapter/route/route.go` — `Project` returns a `ProjectResult` struct; gate drops by `KnownComponentKinds`; tally `KeptByKind`.
- `internal/cli/adapter/route/route_test.go` — update ~10 `Project(...)` call sites + add gate/tally assertions.
- `internal/cli/hydrate/result.go` — add `ProjectedByKind` + `DroppedByKind` to `RenderResult` and `Result`.
- `internal/cli/hydrate/wiring.go` — `projectPlugins` consumes `ProjectResult`, aggregates kept counts + per-kind→plugin drop attribution.
- `internal/cli/hydrate/wiring_projectplugins_test.go` — update for the new return shape + assert metadata is NOT dropped + attribution.
- `internal/cli/hydrate/commit.go` — flow new fields up; rewrite `warnDroppedComponents` (attributed kind warning + separate MCP-shadow warning).
- `internal/cli/hydrate/commit_test.go` — update the two drop-warning tests for the new wording.
- `cmd/ach-cli/cmd/hydrate.go` — capture `Result`, print the success summary to stdout.
- `cmd/ach-cli/cmd/hydrate_test.go` — assert summary output (add if absent).
- `test/e2e/projection_helpers_test.go` — new marker + descriptor semantics (metadata no longer dropped).
- `test/e2e/cli_hydrate_allplatforms_test.go` — update the marker assertion at line 364.

**Delete (Part 3 — dead code):**
- `Adapter.TransformPlugin` method + `PluginWrite` type from `internal/cli/adapter/adapter.go`.
- `TransformPlugin` impls + private helpers from `claudecode/`, `codex/`, `gemini/`, `opencode/`, `pimono/`.
- All `TransformPlugin`/`PluginWrite` tests across the 5 adapter `*_test.go` + `registry_test.go` + the dead refs in `wiring_projectplugins_test.go`.
- `TransformPlugin` narrative in `internal/cli/adapter/doc.go`.

---

# PART 1 — Semantic gate in `route.Project`

### Task 1: Refactor `route.Project` to return a `ProjectResult` struct (no behavior change)

**Files:**
- Modify: `internal/cli/adapter/route/route.go:272-406`
- Modify: `internal/cli/adapter/route/route_test.go` (call sites at lines 44, 78, 105, 109, 145, 170, 183, 199, 224, 241)

- [ ] **Step 1: Add the `ProjectResult` type above `Project`**

In `internal/cli/adapter/route/route.go`, immediately before `func Project(`:

```go
// ProjectResult is the structured return of Project. FileWrites is the
// sorted projected-file list; KeptByKind tallies how many regular files
// were projected per source component kind (e.g. {"commands":12,"agents":8})
// for the hydrate success summary; Dropped is the deduped+sorted set of
// KNOWN component kinds (KnownComponentKinds) present in the source tree
// that this adapter's rule table has no destination for. Metadata, docs,
// and unrecognized top-levels are silently skipped and never appear in
// Dropped (D-12 warning-surface focus).
type ProjectResult struct {
	FileWrites []adapter.FileWrite
	KeptByKind map[string]int
	Dropped    []string
}
```

- [ ] **Step 2: Change the `Project` signature + return statements**

Change the signature line:

```go
func Project(rules []Rule, src, source string) (ProjectResult, error) {
```

Change the early-error returns inside `Project` from `return nil, nil, fmt.Errorf(...)` style to the struct. The empty-src guard becomes:

```go
	if src == "" {
		return ProjectResult{}, fmt.Errorf("route: Project requires non-empty src")
	}
```

The `WalkDir` error return becomes:

```go
	if err != nil {
		return ProjectResult{}, err
	}
```

And the final success return (was `return fws, dropped.out, nil`) becomes:

```go
	return ProjectResult{
		FileWrites: fws,
		KeptByKind: kept,
		Dropped:    dropped.out,
	}, nil
```

- [ ] **Step 3: Declare the `kept` map next to `dropped`**

Inside `Project`, where `dropped := newDroppedSet()` is declared (`route.go:278`), add:

```go
	dropped := newDroppedSet()
	kept := map[string]int{}
```

> NOTE: `kept` is populated in Task 3. For this refactor task it is returned empty — behavior unchanged.

- [ ] **Step 4: Update all `route_test.go` call sites**

For each call site, replace the multi-return form with the struct. Pattern — change:

```go
	fws, dropped, err := Project(rules, src, "")
```
to:
```go
	pr, err := Project(rules, src, "")
```
then replace subsequent uses of `fws` → `pr.FileWrites` and `dropped` → `pr.Dropped` within that test. For sites that discarded a value (`fwsMatch, _, err := Project(...)`), use `pr, err := Project(...)` and reference `pr.FileWrites`. Apply to all 10 sites (lines 44, 78, 105, 109, 145, 170, 183, 199, 224, 241 — note 105/109 declare `fws1/dropped1` and `fws2/dropped2`; rename to `pr1`/`pr2`).

- [ ] **Step 5: Run route tests to verify the refactor compiles + passes**

Run: `make test-unit-pkg PKG=./internal/cli/adapter/route/...`
Expected: PASS (pure refactor; same behavior).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/adapter/route/route.go internal/cli/adapter/route/route_test.go
git commit -m "refactor(route): Project returns ProjectResult struct"
```

---

### Task 2: Create `KnownComponentKinds` and gate drops by it

**Files:**
- Create: `internal/cli/adapter/route/kinds.go`
- Create: `internal/cli/adapter/route/kinds_test.go`
- Modify: `internal/cli/adapter/route/route.go` (no-rule branch ~line 300-309)
- Modify: `internal/cli/adapter/route/route_test.go` (add a gate test)

- [ ] **Step 1: Write the failing gate test in `route_test.go`**

Add to `internal/cli/adapter/route/route_test.go`:

```go
func TestProject_DropsOnlyKnownKinds(t *testing.T) {
	src := t.TempDir()
	// Known kind with no rule for this rule set -> must be dropped.
	mustWriteFile(t, filepath.Join(src, "hooks", "pre.sh"), "#!/bin/sh\n")
	// Metadata + docs + unknown -> must be silently skipped (NOT dropped).
	mustWriteFile(t, filepath.Join(src, ".claude-plugin", "plugin.json"), "{}")
	mustWriteFile(t, filepath.Join(src, ".codex-plugin", "x.json"), "{}")
	mustWriteFile(t, filepath.Join(src, "README.md"), "# docs\n")
	mustWriteFile(t, filepath.Join(src, "random-dir", "x.txt"), "x")
	// A matched kind so the walk also keeps something.
	mustWriteFile(t, filepath.Join(src, "skills", "a", "SKILL.md"), "s")

	rules := []Rule{
		{FromGlob: "skills/**/*", ToGlob: ".x/skills/**/*", Merge: adapter.MergeReplace},
	}
	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project = %v", err)
	}
	if got := pr.Dropped; len(got) != 1 || got[0] != "hooks" {
		t.Fatalf("Dropped = %v; want exactly [hooks] (metadata/docs/unknown must be silent)", got)
	}
}
```

> If `mustWriteFile` does not already exist in `route_test.go`, add a helper that `os.MkdirAll`s the parent and `os.WriteFile`s the content with mode `0o644`, failing the test on error. (Check the file first — it likely has an equivalent; reuse it and adjust the call name.)

- [ ] **Step 2: Run to verify it fails**

Run: `make test-unit-pkg PKG=./internal/cli/adapter/route/... FOCUS=TestProject_DropsOnlyKnownKinds`
Expected: FAIL — `Dropped` currently contains `.claude-plugin`, `.codex-plugin`, `README.md`, `random-dir`, `hooks`.

- [ ] **Step 3: Create `internal/cli/adapter/route/kinds.go`**

```go
// SPDX-License-Identifier: Apache-2.0

package route

// KnownComponentKinds is the canonical Claude-source plugin component
// vocabulary — the set of top-level source-tree kinds that SOME adapter
// in this build knows how to route, plus "hooks" (a real source kind no
// adapter currently supports). It gates the Project drop-warning surface
// (D-12): when a source tree carries one of these kinds but the active
// adapter's rule table has no destination for it, the kind is reported as
// dropped so the user learns "platform X does not support <kind>".
//
// Entries NOT in this set (plugin manifests like .claude-plugin /
// .codex-plugin, docs like README.md / LICENSE, and any unrecognized
// directory) are non-content by design and are skipped SILENTLY — they
// must never pollute the warning, or the signal trains users to ignore it.
//
// INVARIANT: every FromGlob anchor first-segment across all adapter
// ProjectionRules() tables MUST appear here (kinds_test.go enforces the
// direction that matters). When a new adapter rule introduces a new
// source kind, add it here in the SAME commit.
var KnownComponentKinds = map[string]bool{
	"rules":     true,
	"commands":  true,
	"agents":    true,
	"skills":    true,
	"mcp":       true,
	"prompts":   true,
	"AGENTS.md": true,
	"hooks":     true,
}
```

- [ ] **Step 4: Gate the no-rule branch in `route.go`**

In `internal/cli/adapter/route/route.go`, the no-rule branch (currently):

```go
		rule, ok := matchRule(rules, topLevel, source)
		if !ok {
			// No matching rule → record the top-level kind once and skip
			// recursion into the unrouted dir.
			dropped.add(topLevel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
```

becomes:

```go
		rule, ok := matchRule(rules, topLevel, source)
		if !ok {
			// No matching rule. Only KNOWN component kinds are reported as
			// dropped (so the user learns the target lacks support for them);
			// metadata, docs, and unrecognized top-levels are skipped
			// silently to keep the D-12 warning surface focused.
			if KnownComponentKinds[topLevel] {
				dropped.add(topLevel)
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
```

- [ ] **Step 5: Run the gate test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/cli/adapter/route/... FOCUS=TestProject_DropsOnlyKnownKinds`
Expected: PASS.

- [ ] **Step 6: Write `kinds_test.go` to lock the adapter-rules ⊆ KnownComponentKinds invariant**

```go
// SPDX-License-Identifier: Apache-2.0

package route_test

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	"github.com/ackstorm/ach/internal/cli/adapter/codex"
	"github.com/ackstorm/ach/internal/cli/adapter/gemini"
	"github.com/ackstorm/ach/internal/cli/adapter/opencode"
	"github.com/ackstorm/ach/internal/cli/adapter/pimono"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
)

// TestKnownComponentKinds_CoversAllAdapterRules asserts every source kind
// any adapter routes is declared Known — otherwise a real dropped kind
// would be silently swallowed (the bug this set prevents).
func TestKnownComponentKinds_CoversAllAdapterRules(t *testing.T) {
	providers := []route.RuleProvider{
		&claudecode.Adapter{}, &codex.Adapter{}, &gemini.Adapter{},
		&opencode.Adapter{}, &pimono.Adapter{},
	}
	for _, p := range providers {
		for _, r := range p.ProjectionRules() {
			anchor := strings.SplitN(strings.TrimPrefix(r.FromGlob, "./"), "/", 2)[0]
			if !route.KnownComponentKinds[anchor] {
				t.Errorf("rule FromGlob %q -> kind %q is not in KnownComponentKinds", r.FromGlob, anchor)
			}
		}
	}
}
```

> Verify the constructor for each adapter. If adapters are obtained via a registry rather than `&X.Adapter{}`, use the registry accessor instead (check `internal/cli/adapter/registry.go`); the test's intent is "iterate every registered adapter's ProjectionRules". Adjust imports/construction to match the actual exported surface.

- [ ] **Step 7: Run the invariant test**

Run: `make test-unit-pkg PKG=./internal/cli/adapter/route/... FOCUS=TestKnownComponentKinds_CoversAllAdapterRules`
Expected: PASS (the set already covers commands/agents/skills/mcp/rules/prompts/AGENTS.md).

- [ ] **Step 8: Commit**

```bash
git add internal/cli/adapter/route/kinds.go internal/cli/adapter/route/kinds_test.go internal/cli/adapter/route/route.go internal/cli/adapter/route/route_test.go
git commit -m "feat(route): gate drop-warnings to known component kinds"
```

---

### Task 3: Tally `KeptByKind` in `route.Project`

**Files:**
- Modify: `internal/cli/adapter/route/route.go` (the FileWrite append site ~line 388)
- Modify: `internal/cli/adapter/route/route_test.go`

- [ ] **Step 1: Write the failing tally test**

Add to `route_test.go`:

```go
func TestProject_KeptByKind(t *testing.T) {
	src := t.TempDir()
	mustWriteFile(t, filepath.Join(src, "commands", "a.md"), "a")
	mustWriteFile(t, filepath.Join(src, "commands", "b.md"), "b")
	mustWriteFile(t, filepath.Join(src, "skills", "s", "SKILL.md"), "s")
	rules := []Rule{
		{FromGlob: "commands/**/*", ToGlob: ".x/commands/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".x/skills/**/*", Merge: adapter.MergeReplace},
	}
	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatalf("Project = %v", err)
	}
	if pr.KeptByKind["commands"] != 2 || pr.KeptByKind["skills"] != 1 {
		t.Fatalf("KeptByKind = %v; want commands=2 skills=1", pr.KeptByKind)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `make test-unit-pkg PKG=./internal/cli/adapter/route/... FOCUS=TestProject_KeptByKind`
Expected: FAIL — `KeptByKind` is empty (returned empty since Task 1).

- [ ] **Step 3: Increment `kept` at the FileWrite append**

In `route.go`, immediately before the `fws = append(fws, adapter.FileWrite{...})` statement (~line 388), add:

```go
		kept[topLevel]++
		fws = append(fws, adapter.FileWrite{
			Path:       dest,
			Content:    content,
			SourceHash: srcHash,
			Merge:      rule.Merge,
			Keys:       keys,
		})
```

> `topLevel` is in scope inside the walk closure. It is computed once per entry (`parts[0]`), so a multi-file kind increments once per file — exactly the file count we want.

- [ ] **Step 4: Run to verify it passes**

Run: `make test-unit-pkg PKG=./internal/cli/adapter/route/... FOCUS=TestProject_KeptByKind`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/adapter/route/route.go internal/cli/adapter/route/route_test.go
git commit -m "feat(route): tally KeptByKind for hydrate summary"
```

---

# PART 2 — Plumb counts + attribution + UX

### Task 4: Add `ProjectedByKind` + `DroppedByKind` to results; aggregate in `projectPlugins`

**Files:**
- Modify: `internal/cli/hydrate/result.go` (`RenderResult` ~line 86-110, `Result` ~line 17-50)
- Modify: `internal/cli/hydrate/wiring.go` (`projectPlugins` 413-536)
- Modify: `internal/cli/hydrate/wiring_projectplugins_test.go`

- [ ] **Step 1: Write the failing aggregation test**

In `internal/cli/hydrate/wiring_projectplugins_test.go`, add a test that stages a plugin tree under `<achDir>/plugin/<name>/` containing a `hooks/` dir (Known, unsupported by the test adapter) plus a `.claude-plugin/` and `README.md` (must NOT be dropped) and a routed kind, runs `projectPlugins` against the existing test dispatcher/adapter, and asserts:

```go
	// Metadata/docs are silent; only the known-but-unsupported kind is attributed.
	if _, ok := result.DroppedByKind[".claude-plugin"]; ok {
		t.Errorf("metadata .claude-plugin must not be dropped; got %v", result.DroppedByKind)
	}
	if got := result.DroppedByKind["hooks"]; len(got) != 1 || got[0] != "myplugin" {
		t.Errorf("DroppedByKind[hooks] = %v; want [myplugin]", got)
	}
	if result.ProjectedByKind["skills"] == 0 {
		t.Errorf("ProjectedByKind[skills] = 0; want > 0")
	}
```

> Reuse the existing harness in this test file (it already builds a `RenderResult`, an adapter with `ProjectionRules()`, and calls the dispatcher's `projectPlugins`). Match the existing helper names — read the top of the file first and mirror its setup (`adapterDispatcherImpl`, `state.File`, `achDir`, `toolRoot`). Pick a test adapter whose rules include `skills` but NOT `hooks` (any of them — none support hooks).

- [ ] **Step 2: Run to verify it fails**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/... FOCUS=<new test name>`
Expected: FAIL — `DroppedByKind`/`ProjectedByKind` fields do not exist yet (compile error).

- [ ] **Step 3: Add the fields to `RenderResult` and `Result`**

In `internal/cli/hydrate/result.go`, add to `RenderResult` (after `DroppedComponents`):

```go
	// ProjectedByKind tallies projected regular files per source kind
	// (e.g. {"commands":12,"agents":8}) aggregated across every plugin
	// tree. Feeds the hydrate success summary.
	ProjectedByKind map[string]int

	// DroppedByKind maps a dropped KNOWN component kind to the sorted
	// unique plugin names that shipped it but whose content the active
	// platform has no destination for. Drives the attributed end-of-run
	// "platform X does not support <kind>" warning.
	DroppedByKind map[string][]string
```

Add the SAME two fields (same doc, s/aggregated across every plugin tree/rolled up from RenderResult/) to `Result`.

- [ ] **Step 4: Aggregate in `projectPlugins`**

In `internal/cli/hydrate/wiring.go` `projectPlugins`:

Initialize the attribution map once, near the top of the function (after `result` is in scope — it is a `*RenderResult` parameter). Before the `for _, ent := range entries {` loop:

```go
	if result.ProjectedByKind == nil {
		result.ProjectedByKind = map[string]int{}
	}
	if result.DroppedByKind == nil {
		result.DroppedByKind = map[string][]string{}
	}
```

Replace the `route.Project` call and its result handling. The current code is:

```go
		projected, drops, perr := route.Project(rules, pluginSrc, "")
		if perr != nil {
			return fmt.Errorf("adapter %s project plugin %s: %w", d.platformID, ent.Name(), perr)
		}
		for _, fw := range projected {
```

becomes:

```go
		pr, perr := route.Project(rules, pluginSrc, "")
		if perr != nil {
			return fmt.Errorf("adapter %s project plugin %s: %w", d.platformID, ent.Name(), perr)
		}
		for k, n := range pr.KeptByKind {
			result.ProjectedByKind[k] += n
		}
		for _, fw := range pr.FileWrites {
```

Then the trailing per-tree drop aggregation (currently):

```go
		for _, dr := range drops {
			if !seen[dr] {
				seen[dr] = true
				dropped = append(dropped, dr)
			}
		}
	}
```

becomes (attribute each dropped kind to this plugin, plus keep the flat list for back-compat with the existing DroppedComponents flow):

```go
		for _, dr := range pr.Dropped {
			if !seen[dr] {
				seen[dr] = true
				dropped = append(dropped, dr)
			}
			result.DroppedByKind[dr] = appendUniqueSorted(result.DroppedByKind[dr], ent.Name())
		}
	}
```

Add the small helper at the bottom of `wiring.go`:

```go
// appendUniqueSorted inserts name into xs if absent, keeping xs sorted.
func appendUniqueSorted(xs []string, name string) []string {
	for _, x := range xs {
		if x == name {
			return xs
		}
	}
	xs = append(xs, name)
	sort.Strings(xs)
	return xs
}
```

> `sort` is already imported in `wiring.go` (used at line 533). Confirm and skip the import edit if so.

> The `seen`/`dropped`/`result.DroppedComponents` machinery (route.go drops + runtime-wins MCP `fwDrops`) is LEFT INTACT — `DroppedComponents` continues to carry the flat kind list AND the MCP-shadow ids. Task 5 splits the warning so the MCP-shadow ids stop being mislabeled "unsupported".

- [ ] **Step 5: Run the aggregation test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/... FOCUS=<new test name>`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/hydrate/result.go internal/cli/hydrate/wiring.go internal/cli/hydrate/wiring_projectplugins_test.go
git commit -m "feat(hydrate): aggregate projected/dropped components per kind+plugin"
```

---

### Task 5: Flow new fields up + rewrite `warnDroppedComponents`

**Files:**
- Modify: `internal/cli/hydrate/commit.go` (~line 418-426 flow-up, 822-842 warning)
- Modify: `internal/cli/hydrate/commit_test.go` (~line 1295-1340)

- [ ] **Step 1: Write the failing warning tests in `commit_test.go`**

Replace `TestCommit_DropWarning_SingleDedupedSorted` body to drive the attributed format, and add a separate MCP-shadow test. New/updated tests:

```go
func TestCommit_DropWarning_AttributedByPlugin(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.Platform = "pimono"
	var stderr bytes.Buffer
	c.opts.Stderr = &stderr
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{
			DroppedByKind: map[string][]string{
				"agents": {"foo", "bar"},
				"hooks":  {"foo"},
			},
		},
	}
	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v; want nil (warning must not change exit code)", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "platform pimono does not support") {
		t.Errorf("missing platform-attributed header; got:\n%s", out)
	}
	if !strings.Contains(out, "agents") || !strings.Contains(out, "bar, foo") {
		t.Errorf("agents drop not attributed to sorted plugins; got:\n%s", out)
	}
	if !strings.Contains(out, "hooks") {
		t.Errorf("hooks drop missing; got:\n%s", out)
	}
}

func TestCommit_DropWarning_EmptyNoWarning(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	var stderr bytes.Buffer
	c.opts.Stderr = &stderr
	c.adapter = fakeAdapterDispatcher{result: RenderResult{}}
	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if strings.Contains(stderr.String(), "does not support") {
		t.Errorf("unexpected drop warning for empty drop list:\n%s", stderr.String())
	}
}
```

> Delete the old `TestCommit_DropWarning_SingleDedupedSorted` (its `DroppedComponents: []string{"hooks","rules"}` + `hooks, rules` assertion no longer matches the attributed format). If `fakeAdapterDispatcher`'s `result` field does not yet carry `DroppedByKind`, no change needed — it's a plain `RenderResult`, which now has the field.

- [ ] **Step 2: Run to verify they fail**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/... FOCUS=TestCommit_DropWarning`
Expected: FAIL — current warning says "dropped unsupported components for platform", not "platform X does not support".

- [ ] **Step 3: Flow the new fields up in `commit.go`**

After `result.DroppedComponents = append(result.DroppedComponents, renderResult.DroppedComponents...)` (commit.go:419), add:

```go
		if result.ProjectedByKind == nil {
			result.ProjectedByKind = map[string]int{}
		}
		for k, n := range renderResult.ProjectedByKind {
			result.ProjectedByKind[k] += n
		}
		if result.DroppedByKind == nil {
			result.DroppedByKind = map[string][]string{}
		}
		for k, plugins := range renderResult.DroppedByKind {
			for _, p := range plugins {
				result.DroppedByKind[k] = appendUniqueSorted(result.DroppedByKind[k], p)
			}
		}
```

- [ ] **Step 4: Rewrite `warnDroppedComponents`**

Replace `warnDroppedComponents` (commit.go:822-842) and its single call site `c.warnDroppedComponents(result.DroppedComponents)` (commit.go:426).

New call site (commit.go:426):

```go
		c.warnDropped(result.DroppedByKind, result.DroppedComponents)
```

New function (keep the doc updated):

```go
// warnDropped emits up to two end-of-hydration stderr warnings; exit code
// is never affected.
//
//  1. byKind (the attributed projection drops): for each KNOWN component
//     kind the active platform has no rule for, a line naming the kind and
//     the plugins that shipped it — "platform X does not support <kind>
//     (plugins: a, b)". Skipped entirely when empty (e.g. claude-code).
//  2. mcpShadow (the runtime-wins D-10 drops still carried in the flat
//     DroppedComponents list): MCP server ids a runtime-owned definition
//     shadowed. These are NOT "unsupported" — they were intentionally
//     superseded — so they get a distinct, correctly-worded line.
func (c *commit) warnDropped(byKind map[string][]string, flat []string) {
	if len(byKind) > 0 {
		kinds := make([]string, 0, len(byKind))
		for k := range byKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		_, _ = fmt.Fprintf(c.opts.Stderr,
			"warning: platform %s does not support some plugin components — they were skipped:\n",
			c.opts.Platform)
		for _, k := range kinds {
			_, _ = fmt.Fprintf(c.opts.Stderr,
				"    %s (plugins: %s)\n", k, strings.Join(byKind[k], ", "))
		}
	}

	// The flat list may still carry runtime-wins MCP shadow ids (D-10).
	// Filter out anything already reported as a kind drop, then warn
	// separately if any remain.
	if len(flat) > 0 {
		isKind := func(s string) bool { _, ok := byKind[s]; return ok }
		seen := map[string]bool{}
		var shadow []string
		for _, s := range flat {
			if s == "" || isKind(s) || seen[s] {
				continue
			}
			seen[s] = true
			shadow = append(shadow, s)
		}
		if len(shadow) > 0 {
			sort.Strings(shadow)
			_, _ = fmt.Fprintf(c.opts.Stderr,
				"warning: platform %s: projected MCP server(s) shadowed by runtime-owned definitions: %s\n",
				c.opts.Platform, strings.Join(shadow, ", "))
		}
	}
}
```

> `fmt`, `sort`, `strings` are already imported in `commit.go`. Confirm before relying on it.

- [ ] **Step 5: Run the warning tests to verify they pass**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/... FOCUS=TestCommit_DropWarning`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/hydrate/commit.go internal/cli/hydrate/commit_test.go
git commit -m "feat(hydrate): attributed plugin-component drop warnings"
```

---

### Task 6: Print the success summary in `runHydrateEngine`

**Files:**
- Modify: `cmd/ach-cli/cmd/hydrate.go` (~line 540 — capture the Result)
- Modify/Create: `cmd/ach-cli/cmd/hydrate_test.go`

- [ ] **Step 1: Write the failing summary test**

In `cmd/ach-cli/cmd/hydrate_test.go` add a test that swaps `hydrateRunFn` for a stub returning a populated `hydrate.Result` and asserts the summary lands on stdout. Mirror the existing test that overrides `hydrateRunFn` (search the file for `hydrateRunFn =`); reuse its scaffolding (cobra command construction + buffer capture). Core assertions:

```go
	// summaryFromResult is the unit under test; given a populated Result it
	// returns the human summary string.
	got := summaryFromResult(hydrate.Result{
		Environment:     "platform", // see Step 3 note
		PlatformID:      "claude-code",
		FilesWritten:    20,
		FilesPreserved:  1,
		ProjectedByKind: map[string]int{"commands": 12, "agents": 8},
	})
	if !strings.Contains(got, "claude-code") {
		t.Errorf("summary missing platform; got %q", got)
	}
	if !strings.Contains(got, "12 commands") || !strings.Contains(got, "8 agents") {
		t.Errorf("summary missing per-kind counts; got %q", got)
	}
	if !strings.Contains(got, "20 files written") {
		t.Errorf("summary missing file count; got %q", got)
	}
```

> `hydrate.Result` has no `Environment` field today. EITHER drop the `Environment` line from `summaryFromResult` (use `PlatformID` only — simplest, recommended) OR add an `Environment string` field to `Result` and set it in `commit.go`. Choose the simpler PLATFORM-only summary and remove the `Environment:` line from the test. Keep the env name out of the summary; the pk_ warning already names scope.

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/dev.sh go test ./cmd/ach-cli/cmd/ -run TestHydrateSummary -v`
Expected: FAIL — `summaryFromResult` undefined.

- [ ] **Step 3: Implement `summaryFromResult` + wire it into `runHydrateEngine`**

In `cmd/ach-cli/cmd/hydrate.go`, add:

```go
// summaryFromResult renders the post-hydrate success summary printed to
// stdout. Per-kind counts come from Result.ProjectedByKind (sorted for a
// byte-stable line); the totals come from the engine counters.
func summaryFromResult(res hydrate.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hydrated for %s\n", res.PlatformID)
	if len(res.ProjectedByKind) > 0 {
		kinds := make([]string, 0, len(res.ProjectedByKind))
		for k := range res.ProjectedByKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		parts := make([]string, 0, len(kinds))
		for _, k := range kinds {
			parts = append(parts, fmt.Sprintf("%d %s", res.ProjectedByKind[k], k))
		}
		fmt.Fprintf(&b, "  ✓ %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "  ✓ %d files written, %d preserved\n",
		res.FilesWritten, res.FilesPreserved)
	return b.String()
}
```

Change the call site (hydrate.go:540) from:

```go
	if _, err := hydrateRunFn(cmd.Context(), opts); err != nil {
		return err
	}
	return nil
```

to:

```go
	res, err := hydrateRunFn(cmd.Context(), opts)
	if err != nil {
		return err
	}
	if !in.dryRun {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), summaryFromResult(res))
	}
	return nil
```

> Add `"sort"` to `cmd/ach-cli/cmd/hydrate.go` imports if absent (`fmt`/`strings` are present). The `--output`/raw path returns earlier (hydrate.go:421 `runHydrateRaw`) so the summary only affects the engine path. Gate on `!in.dryRun` so `--dry-run` stays quiet (matches its read-only contract).

- [ ] **Step 4: Run to verify it passes**

Run: `./scripts/dev.sh go test ./cmd/ach-cli/cmd/ -run TestHydrateSummary -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/hydrate_test.go
git commit -m "feat(cli): hydrate success summary with per-kind counts"
```

---

### Task 7: Update e2e projection assertions

**Files:**
- Modify: `test/e2e/projection_helpers_test.go` (comment block ~40-64, marker const at line 235, `assertDropsWarned` ~233-260)
- Modify: `test/e2e/cli_hydrate_allplatforms_test.go:364`

- [ ] **Step 1: Update the marker constant + helper logic**

In `test/e2e/projection_helpers_test.go`, change the marker:

```go
	const marker = "warning: platform "
```

and update `assertDropsWarned` so the per-kind presence check matches the new line shape `    <kind> (plugins: ...)`. The existing `dropLineMentions(line, kind)` approach must now look at the multi-line warning body, not a single line. Simplest correct form — assert the whole stderr (after the marker) contains each `d.mustDrop` kind and contains NONE of `d.mustNotDrop`:

```go
func assertDropsWarned(t *testing.T, platformID string, stderr []byte, d projectionDescriptor) {
	t.Helper()
	s := string(stderr)
	const marker = "warning: platform "

	if len(d.mustDrop) > 0 {
		if !strings.Contains(s, marker+platformID+" does not support") {
			t.Errorf("%s: expected an attributed drop warning (kinds %v) but stderr had none\nstderr=%s",
				platformID, d.mustDrop, stderr)
			return
		}
		for _, kind := range d.mustDrop {
			if !strings.Contains(s, kind) {
				t.Errorf("%s: drop warning missing expected kind %q\nstderr=%s", platformID, kind, s)
			}
		}
	}
	for _, kind := range d.mustNotDrop {
		// A kind we expect kept must not appear in a "does not support" body.
		if strings.Contains(s, "does not support") && strings.Contains(dropBody(s), kind) {
			t.Errorf("%s: kind %q unexpectedly dropped\nstderr=%s", platformID, kind, s)
		}
	}
}
```

> If `dropWarningLine`/`dropLineMentions` become unused after this rewrite, delete them. Add a tiny `dropBody(s string) string` helper returning the substring from the first `marker` to the end, or inline it. Match the existing helper style in the file.

- [ ] **Step 2: Update the comment block (lines ~40-64)**

The comment currently claims "Every adapter drops caveman's non-resource top-levels (.claude-plugin, src, LICENSE, README.md)". Replace that paragraph to state the NEW contract: metadata/docs/unknown top-levels (`.claude-plugin`, `.codex-plugin`, `src`, `LICENSE`, `README.md`) are now **silently skipped** and never appear in the warning; only KNOWN component kinds with no adapter rule are reported (e.g. pimono's `agents`). Update the format example to:

```
//	"warning: platform <id> does not support some plugin components — they were skipped:"
//	"    <kind> (plugins: <name, …>)"
```

Update the per-adapter notes accordingly (pimono still drops `agents`; the "Every adapter drops ... non-resource top-levels" note is now false — remove it).

- [ ] **Step 3: Add metadata kinds to `mustNotDrop` where descriptors exist**

In the descriptor table (search `projectionDescriptor{` literals in this file), for each adapter add `.claude-plugin`, `README.md` (and `src`, `LICENSE` if the caveman fixture ships them) to `mustNotDrop` so the new silent-skip behavior is positively asserted. (If `mustNotDrop` is a `[]string` field, append; match the existing struct.)

- [ ] **Step 4: Update `cli_hydrate_allplatforms_test.go:364`**

Change:

```go
	if !strings.Contains(s, "dropped unsupported components") {
```

to match the new marker (or route the assertion through `assertDropsWarned`). Read the surrounding ~20 lines first; if it asserts a specific platform's drop, switch to `"does not support"`; if it asserts claude-code drops NOTHING, that assertion still holds (claude-code now drops only Known-unsupported kinds, of which caveman ships none → no warning).

- [ ] **Step 5: Run the focused e2e projection suite**

Run: `make e2e-full` then `make e2e-focus RUN='TestHydrateAllPlatforms'` (or the actual test name — confirm via `grep -rn "func Test.*Projection\|func TestHydrateAllPlatforms" test/e2e/`). Use the kept-cluster loop per CLAUDE.md.
Expected: PASS for all platforms; pimono shows the attributed `agents` warning; claude-code shows none.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/projection_helpers_test.go test/e2e/cli_hydrate_allplatforms_test.go
git commit -m "test(e2e): assert attributed drop warnings + silent metadata"
```

---

# PART 3 — Delete the dead `TransformPlugin` / `PluginWrite` surface

### Task 8: Remove `TransformPlugin` from the interface, all impls, and helpers

> This is one ATOMIC change: removing an interface method requires deleting every implementation AND every test that calls it in the same commit, or the build breaks. Sub-steps are per-file; the commit lands only after the whole tree compiles.

**Files:**
- Modify: `internal/cli/adapter/adapter.go` (remove `PluginWrite` type 111-129; remove `TransformPlugin` from `Adapter` 172-176)
- Modify: `internal/cli/adapter/{claudecode,codex,gemini,opencode,pimono}/*.go` (remove the method + dead helpers)
- Modify: `internal/cli/adapter/doc.go` (remove `TransformPlugin`/`PluginWrite` narrative)
- Delete tests: `TransformPlugin`/`PluginWrite` cases in the 5 adapter `*_test.go` + `registry_test.go` + dead refs in `wiring_projectplugins_test.go`

- [ ] **Step 1: Remove the interface method + type from `adapter.go`**

Delete the `PluginWrite` struct (lines 111-129) and the `TransformPlugin(ctx context.Context, src, dst string) (PluginWrite, error)` method + its doc comment from the `Adapter` interface (lines 172-176). Update the interface header comment that says "Every adapter MUST implement all seven methods" → "six methods" (count the remaining methods and correct the number).

- [ ] **Step 2: Remove each adapter's `TransformPlugin` impl + private helpers**

For each adapter package, delete the `func (a *Adapter) TransformPlugin(...)` method and any now-unreferenced private helpers it owned:
- `gemini/gemini.go`: `TransformPlugin`, `componentKept`, `componentDropped`, `readPluginVersion` (and any helper only it called, e.g. version-parsing).
- `opencode/opencode.go`: `TransformPlugin`, `droppableComponent`.
- `claudecode/claudecode.go`, `codex/codex.go`, `pimono/pimono.go`: `TransformPlugin` (+ any private helper exclusive to it — verify with grep before deleting).

After each deletion, `grep` the package for the helper name to confirm zero remaining references before removing it.

- [ ] **Step 3: Delete the `TransformPlugin`/`PluginWrite` tests**

Remove the test functions that call `a.TransformPlugin(...)` (call sites enumerated in the plan background) from:
`claudecode_test.go`, `codex_test.go`, `gemini_test.go`, `opencode_test.go`, `registry_test.go`, and the dead `PluginWrite`/`TransformPlugin` references in `wiring_projectplugins_test.go`. Delete the whole test func when its sole purpose was `TransformPlugin`; for shared tests that merely touch `PluginWrite`, excise only the dead lines. `opencode_test.go`/`gemini_test.go` also reference the deleted helpers indirectly — remove those assertions.

- [ ] **Step 4: Update `doc.go`**

Remove paragraphs describing `TransformPlugin`/`PluginWrite`/ADAPT-07-via-TransformPlugin. Where ADAPT-07 drop accounting is described, point it at `route.Project` + `KnownComponentKinds` instead.

- [ ] **Step 5: Verify the whole module compiles + adapter/hydrate units pass**

Run: `./scripts/dev.sh go build ./...`
Expected: clean build (no "undefined: PluginWrite", no "does not implement Adapter").

Run: `make test-unit-pkg PKG=./internal/cli/adapter/...` then `make test-unit-pkg PKG=./internal/cli/hydrate/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/adapter cmd internal/cli/hydrate
git commit -m "refactor(adapter): drop dead TransformPlugin/PluginWrite surface"
```

---

# PART 4 — Full verification

### Task 9: Run the gates and the hydrate e2e

**Files:** none (verification only)

- [ ] **Step 1: Lint the touched packages**

Run: `make qa-lint`
Expected: 0 issues. (Watch for unused imports after deletions — `sort`/`context` in adapters, `dropWarningLine` helpers in e2e.)

- [ ] **Step 2: Full unit sweep**

Run: `make test-unit`
Expected: PASS.

- [ ] **Step 3: Hydrate e2e (all platforms)**

Run: `make e2e-full` (kept cluster), then inspect with `make logs-...` if red; reclaim with `make cluster-down`.
Expected: PASS. Manually confirm against a real plugin fixture:
- claude-code hydrate of a plugin with only `commands/agents/skills` → summary printed, NO drop warning.
- pimono hydrate of a plugin with `agents/` → attributed `agents` warning; `.claude-plugin`/`README.md` absent from any warning.
- any target with a `hooks/` plugin → `hooks` warning attributed to the plugin.

- [ ] **Step 4: Pre-push gate (host-only)**

Run: `make pre-push`
Expected: all 18 gates green (SPDX header on new `kinds.go`/`kinds_test.go`; `go mod tidy` clean; full lint + unit inside the gate).

- [ ] **Step 5: Clean per-feature cache**

Run: `make clean-cache`

---

## Self-Review

**Spec coverage:**
- "Silence metadata/docs (`.claude-plugin`, `.codex-plugin`, `README.md`)" → Task 2 (gate by `KnownComponentKinds`); positively asserted in Task 2 Step 1 + Task 7 Step 3.
- "Warn when a known component kind is unsupported by the target (hooks everywhere, agents@pimono)" → Task 2 + Task 5; e2e Task 7.
- "Attribute the drop to plugin + platform" → Task 4 (`DroppedByKind`) + Task 5 (`platform X does not support <kind> (plugins: …)`).
- "Much clearer message, gsd-style per-target summary" → Task 6 (`summaryFromResult`, per-kind ✓ counts + totals).
- "Delete dead TransformPlugin code" → Part 3.

**Placeholder scan:** No "TBD"/"handle edge cases"/"similar to Task N"; every code step shows full code. Remaining judgement calls are explicitly flagged as `>` NOTEs with a recommended default (helper-name reuse, registry-vs-struct adapter construction, `Environment` field omission, unused-helper deletion) — these depend on current file contents the implementer must read, not on undecided design.

**Type consistency:** `ProjectResult{FileWrites, KeptByKind, Dropped}` defined Task 1, consumed Task 4. `RenderResult`/`Result` gain identical `ProjectedByKind map[string]int` + `DroppedByKind map[string][]string` (Task 4), flowed in Task 5, read in Task 6. `KnownComponentKinds` defined Task 2, used Task 2 (gate) + Task 2 Step 6 (invariant test). `warnDropped(byKind, flat)` defined + called Task 5. `summaryFromResult(hydrate.Result) string` defined + tested Task 6. `appendUniqueSorted` defined Task 4, reused Task 5.

---

## Execution Handoff

Plan complete and saved to `docs/plans/2026-06-03-hydrate-drop-warning-and-summary.md`. Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session with checkpoints.

Per your standing rule I will NOT execute without explicit confirmation. Which approach?
