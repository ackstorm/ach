# Hydrate Runtime-On-By-Default Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `ach-cli env hydrate` wire an Environment's direct MCP/A2A runtime by
default (not behind `--include-runtime`), be informative about models it deliberately
does not wire, keep `env uninstall` symmetric, and never write an empty `.mcp.json`
for a content-only Environment.

**Architecture:** The hydrate engine (`internal/cli/hydrate`) derives an `includeRuntime`
boolean from `Opts` in three spots. Today that boolean is `IncludeRuntime || OnlyRuntime`
(default OFF). We invert it to `!NoRuntime` (default ON) by adding an `Opts.NoRuntime`
opt-out, wiring a new `--no-runtime` cobra flag, demoting `--include-runtime` to a hidden
deprecated no-op, and flipping the sibling `env uninstall` default via `BuildScopedEmpty`.
A dispatcher-level guard suppresses the runtime adapter file when the Environment carries
no MCP/A2A. The success summary gains an honest, non-`✓` models line.

**Tech Stack:** Go (cobra CLI), no k8s deps (this is `ach-cli` surface). Tests are stdlib
`testing`. Build/test route through the `ach-devtools` container automatically via `make`.

---

## Decisions resolved by this plan (the brief left these open)

The source brief (`docs/plans/2026-07-17-hydrate-runtime-on-by-default.md`) is a pre-plan
brief with 4 open decisions plus items flagged "reviewer: verify". This plan resolves all
of them. **The user may override any of these at the plan-review checkpoint** — the two
most consequential (D2 flag name, D5 empty-file suppression) are pure judgment calls.

| # | Decision | Resolution | Why |
|---|----------|-----------|-----|
| D1 | Flip default vs remove `--include-runtime` | **Flip default; keep `--include-runtime` as a hidden, deprecated no-op** | Back-compat: existing scripts/muscle-memory that pass it keep working (it is now redundant, not an error). |
| D2 | Opt-out flag name `--no-runtime` vs `--only-context` | **`--no-runtime`** | Conventional `--no-X` opt-out prefix; reads as "disable the new default"; the brief itself frames it as an opt-OUT. (`--only-context` is the symmetric alternative — override here if preferred.) |
| D3 | Models line: stderr vs structured summary | **Structured summary** — a non-`✓` info line inside the existing Runtime block, only when `Models > 0` | The summary already owns the Runtime block; a `•` line there is discoverable and stays with the counts, unlike a detached stderr notice. |
| D4 | `--sync` scope symmetry | **Auto-preserved for `hydrate --sync`; the real asymmetry is `env uninstall`, which we ALSO flip** | `hydrate --sync` runs in the same engine pass as the flipped `includeRuntime`, so it reconciles runtime automatically. But `env uninstall` is a *separate* command whose default must flip too, else default-install writes runtime that default-uninstall never removes. See Task 5. |
| D5 | Empty runtime → empty `.mcp.json` | **Suppress**: a content-only Environment (0 MCP + 0 A2A) writes no runtime adapter file | "Wire everything projectable" ⇒ nothing to wire ⇒ write nothing. Avoids a confusing empty credential-file + gitignore entry in every content-only project. Gate lives at the dispatcher (`wiring.go`), so adapter-level `TestRenderRuntime_EmptyRuntime_Emits*` stay green. |
| D6 | `Opts.IncludeRuntime` field fate | **Remove it** (Task 4) once no reader/writer remains | A set-but-never-read field is exactly the dead code that misleads later ("why does `--include-runtime` do nothing yet the field exists?"). |

### The new scope model (both `hydrate` and `uninstall`)

| Intent | Flag | `hydrate` effect | `uninstall` effect |
|--------|------|------------------|--------------------|
| **default** | *(none)* | context **+** runtime | remove context **+** runtime |
| runtime only | `--only-runtime` | runtime only | remove runtime only |
| context only | `--no-runtime` | context only (no credential on disk) | remove context only (keep runtime) |
| deprecated | `--include-runtime` | **no-op** (runtime already on), hidden, one-line stderr notice | **no-op** (runtime already removed), hidden, one-line stderr notice |

Mutual exclusions (both commands): `--no-runtime` ⊗ `--only-runtime`, `--no-runtime` ⊗
`--include-runtime`; keep the existing `--include-runtime` ⊗ `--only-runtime`.

### Ground truth verified in code (brief §2 re-confirmed)

- `internal/cli/manifest/manifest.go:38` — `RuntimeBlock{Models, MCPServers, A2AAgents}`.
- Three engine derivations `includeRuntime := c.opts.IncludeRuntime || c.opts.OnlyRuntime`
  at `commit.go:476` (Render + mirror gate), `:601` (`runtimeSummary`), `:952`
  (`step6Diff`). `includeContext := !c.opts.OnlyRuntime` at `:613, :951` — **unchanged** by
  this plan (context stays on except under `--only-runtime`).
- `internal/cli/adapter/claudecode/claudecode.go:161 renderMcpJSON` ignores
  `m.Runtime.Models` entirely, and an empty runtime emits `{"mcpServers":{}}` (a real
  `FileWrite`) — confirming D5's problem. All 5 adapters have a
  `TestRenderRuntime_EmptyRuntime_Emits*` test (empty is graceful, never an error).
- `step12bGitignore` (`commit.go:1331`) + mode `0600` already cover any written
  `.mcp.json`; default-on introduces no new credential exposure.
- `--raw` (`cmd/ach-cli/cmd/hydrate.go:402`) short-circuits BEFORE the engine, so the
  W3-P3 `examples/hydrate.json` golden-diff anchor is **unaffected** by the default flip.
- `env uninstall` (`cmd/ach-cli/cmd/uninstall.go:225`) calls
  `hydrate.BuildScopedEmpty(prev, includeRuntime, onlyRuntime)` (`scope.go:47`) — the D4
  symmetry surface.

---

## Task 1: Flip the engine scope derivation (runtime ON by default)

**Files:**
- Modify: `internal/cli/hydrate/flags.go` (add `NoRuntime`; retitle `IncludeRuntime` doc)
- Modify: `internal/cli/hydrate/commit.go:476, 601, 952` (derivations) + `:943` doc + `:376`/`:492` context comments if they mention the old default
- Modify: `internal/cli/hydrate/result.go:263-276` (interface doc comment)
- Test: `internal/cli/hydrate/commit_test.go` (step6 tests + the two `IncludeRuntime=true` sites)

**Step 1 — Write/adjust the failing tests first.**

In `internal/cli/hydrate/commit_test.go`:

(a) Rewrite `TestCommit_Step6Diff_DefaultScope_ContextOnly` to assert the NEW default
(context **+** runtime). Rename it `TestCommit_Step6Diff_DefaultScope_ContextAndRuntime`:

```go
// TestCommit_Step6Diff_DefaultScope_ContextAndRuntime asserts the new default
// scope (no flags) emits BOTH context and runtime targets (runtime-on-by-default).
func TestCommit_Step6Diff_DefaultScope_ContextAndRuntime(t *testing.T) {
	c, _, _ := newTestCommit(t)
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Runtime: &manifest.RuntimeBlock{
			Models:     []manifest.ContentRef{{ID: "m1"}},
			MCPServers: []manifest.ContentRef{{ID: "s1"}},
		},
		Context: &manifest.ContextBlock{
			Prompts: []manifest.ContentRef{{ID: "p1"}},
		},
	}
	targets := c.step6Diff(m)
	if len(targets) != 3 {
		t.Fatalf("default scope: got %d targets, want 3 (1 prompt + 1 model + 1 mcp)", len(targets))
	}
}
```

(b) Add a new test for the opt-out:

```go
// TestCommit_Step6Diff_NoRuntime_ContextOnly asserts --no-runtime narrows the
// default back to context-only (the pre-flip default behavior).
func TestCommit_Step6Diff_NoRuntime_ContextOnly(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.NoRuntime = true
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Runtime: &manifest.RuntimeBlock{Models: []manifest.ContentRef{{ID: "m1"}}},
		Context: &manifest.ContextBlock{Prompts: []manifest.ContentRef{{ID: "p1"}}},
	}
	targets := c.step6Diff(m)
	if len(targets) != 1 || targets[0].Kind != "prompt" {
		t.Fatalf("--no-runtime scope: got %+v, want 1 prompt only", targets)
	}
}
```

(c) Replace the body of `TestCommit_Step6Diff_IncludeRuntime_BothScopes`: since `--only-runtime`
is the only remaining runtime-narrowing flag, retarget it to `OnlyRuntime`. Rename to
`TestCommit_Step6Diff_OnlyRuntime_RuntimeOnly`:

```go
// TestCommit_Step6Diff_OnlyRuntime_RuntimeOnly asserts --only-runtime emits
// runtime targets only (context skipped).
func TestCommit_Step6Diff_OnlyRuntime_RuntimeOnly(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.OnlyRuntime = true
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Runtime: &manifest.RuntimeBlock{MCPServers: []manifest.ContentRef{{ID: "s1"}}},
		Context: &manifest.ContextBlock{Prompts: []manifest.ContentRef{{ID: "p1"}}},
	}
	targets := c.step6Diff(m)
	if len(targets) != 1 || targets[0].Kind != "mcpServer" {
		t.Fatalf("--only-runtime scope: got %+v, want 1 mcpServer only", targets)
	}
}
```

(d) In `TestRun_ExtractSkipsRuntimeKinds` (~line 855) and `TestRun_RuntimeMirror_WritesSnapshotsAndState`
(~line 905): **delete** the line `c.opts.IncludeRuntime = true` in each (runtime is now on by
default; `Opts.IncludeRuntime` is being removed in Task 4). Update the `TestRun_ExtractSkipsRuntimeKinds`
doc comment: replace "under --include-runtime" with "by default (runtime-on)".

**Step 2 — Run the tests; verify they FAIL to compile / fail.**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/`
Expected: compile error `c.opts.NoRuntime undefined` (field not added yet).

**Step 3 — Add the `Opts.NoRuntime` field.**

In `internal/cli/hydrate/flags.go`, replace the `IncludeRuntime` doc block + field (lines 41-44)
and insert `NoRuntime`. Keep `IncludeRuntime` the field for now (removed in Task 4), but mark it
deprecated so nobody adds a new reader:

```go
	// IncludeRuntime is DEPRECATED and unread by the engine as of the
	// runtime-on-by-default flip: runtime (mcp/a2a) is wired by default.
	// Retained only until the cobra layer stops constructing it (removed in
	// the same change that drops the hidden --include-runtime alias).
	// Deprecated: runtime is on by default; use NoRuntime to opt out.
	IncludeRuntime bool

	// NoRuntime opts OUT of the default runtime wiring: context is still
	// reconciled, but the Environment's direct mcp/a2a endpoints are NOT
	// written to the adapter runtime-config (no credential lands on disk).
	// Mutually exclusive with OnlyRuntime (caller layer enforces).
	NoRuntime bool
```

**Step 4 — Flip the three derivations in `commit.go`.**

At `commit.go:476` (inside `run`, the Render + runtime-mirror gate):

```go
	includeRuntime := !c.opts.NoRuntime
```

At `commit.go:601` (`runtimeSummary`):

```go
	includeRuntime := !c.opts.NoRuntime
```

At `commit.go:952` (`step6Diff`):

```go
	includeRuntime := !c.opts.NoRuntime
```

(All three previously read `c.opts.IncludeRuntime || c.opts.OnlyRuntime`. `--only-runtime`
keeps runtime on because it never sets `NoRuntime`; `includeContext := !c.opts.OnlyRuntime`
is untouched.)

Update the `step6Diff` doc comment (`commit.go:941-944`):

```go
// Scope filter:
//   - opts.OnlyRuntime  → runtime only (skip context entirely)
//   - opts.NoRuntime    → context only (skip runtime entirely)
//   - default           → runtime + context (runtime-on-by-default)
```

Update the inline comment at `commit.go:470-476` (the `includeRuntime` hoist) to describe the
new default: "a default hydrate projects the Environment's direct mcp/a2a runtime AND
plugin-contributed mcps; `--no-runtime` opts out of the direct-runtime leg."

**Step 5 — Update the `AdapterDispatcher.Render` doc in `result.go:268-275`.**

Replace the `includeRuntime` derivation sentence ("Derived as opts.IncludeRuntime || opts.OnlyRuntime…")
with: "Derived as `!opts.NoRuntime`: a default hydrate projects the Environment's direct
runtime mcp/a2a AND plugin-contributed mcps; `--no-runtime` opts out of the direct-runtime leg."

**Step 6 — Run the package tests; verify PASS.**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/`
Expected: PASS (the `hydrate` package compiles and its tests are green). The cobra package
(`cmd/ach-cli`) still references `Opts.IncludeRuntime` and compiles fine because the field
still exists.

**Step 7 — Commit.**

```bash
git add internal/cli/hydrate/flags.go internal/cli/hydrate/commit.go \
        internal/cli/hydrate/result.go internal/cli/hydrate/commit_test.go
git commit -m "feat(hydrate): wire runtime by default in the engine scope derivation"
```

---

## Task 2: Suppress the empty runtime adapter file (D5)

**Files:**
- Modify: `internal/cli/hydrate/wiring.go` (`Render`, ~line 387; add `hasDirectRuntime` helper)
- Test: `internal/cli/hydrate/wiring_test.go` (or a new focused test file)

**Step 1 — Write the failing test.**

Add to `internal/cli/hydrate/wiring_test.go`:

```go
// TestRender_EmptyRuntime_WritesNoMcpJson asserts a content-only manifest
// (no mcpServers, no a2aAgents) with includeRuntime=true writes NO runtime
// adapter file — "wire everything projectable" means nothing to wire ⇒ nothing
// written, not an empty {"mcpServers":{}}.
func TestRender_EmptyRuntime_WritesNoMcpJson(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime:       &manifest.RuntimeBlock{Models: []manifest.ContentRef{{ID: "gpt"}}}, // models only
		Context:       &manifest.ContextBlock{},
	}
	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, hydrate.ConflictNamespace)
	// includeRuntime=true, projectPlugins=true — the DEFAULT scope.
	res, err := disp.Render(context.Background(), m, nil, achDir, toolRoot, true, true)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(res.WrittenFiles) != 0 {
		t.Fatalf("content-only env must write no runtime file, got %d: %+v", len(res.WrittenFiles), res.WrittenFiles)
	}
	if _, err := os.Stat(filepath.Join(toolRoot, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatalf(".mcp.json must not be created for a content-only env (stat err=%v)", err)
	}
}

// TestRender_NonEmptyRuntime_WritesMcpJson is the positive control: a manifest
// WITH an mcp server still writes .mcp.json under the default scope.
func TestRender_NonEmptyRuntime_WritesMcpJson(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{{ID: "s1", Endpoint: "http://x/mcp/s1"}},
		},
		Context: &manifest.ContextBlock{},
	}
	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, hydrate.ConflictNamespace)
	res, err := disp.Render(context.Background(), m, nil, achDir, toolRoot, true, true)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(res.WrittenFiles) != 1 {
		t.Fatalf("env with 1 mcp must write 1 runtime file, got %d", len(res.WrittenFiles))
	}
}
```

Confirm imports (`os`, `path/filepath`, `context`, `extract`, `manifest`, `hydrate`) are
present in the test file; add any missing.

**Step 2 — Run; verify the empty-env test FAILS.**

Run: `make test-envtest-pkg PKG=./internal/cli/hydrate/ FOCUS=TestRender_EmptyRuntime_WritesNoMcpJson`
(or `make test-unit-pkg PKG=./internal/cli/hydrate/` — this is a pure-logic test).
Expected: FAIL — a `.mcp.json` with `{"mcpServers":{}}` is written, `WrittenFiles` len 1.

**Step 3 — Add the guard in `wiring.go`.**

At the top of `Render` gate (currently `if includeRuntime {` at ~line 387), tighten to:

```go
	if includeRuntime && hasDirectRuntime(m) {
		fws, err := ad.RenderRuntime(ctx, m, s)
		// ... unchanged body ...
	}
```

Add the helper near the other file-scope helpers in `wiring.go`:

```go
// hasDirectRuntime reports whether the manifest carries a direct runtime
// endpoint the adapter can actually wire (an MCP server or an A2A agent).
// Models are excluded: no adapter's RenderRuntime projects a model (access is
// server-side via the gateway), so a models-only Environment has nothing to
// write. Gating RenderRuntime on this predicate keeps a content-only hydrate
// from emitting an empty {"mcpServers":{}} adapter file (D5).
func hasDirectRuntime(m *manifest.Manifest) bool {
	if m == nil || m.Runtime == nil {
		return false
	}
	return len(m.Runtime.MCPServers) > 0 || len(m.Runtime.A2AAgents) > 0
}
```

**Step 4 — Run; verify PASS.**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/`
Expected: PASS (both new tests + all prior). Adapter-level
`internal/cli/adapter/*/TestRenderRuntime_EmptyRuntime_Emits*` are untouched (they call
`RenderRuntime` directly, below this dispatcher gate).

**Step 5 — Commit.**

```bash
git add internal/cli/hydrate/wiring.go internal/cli/hydrate/wiring_test.go
git commit -m "feat(hydrate): skip empty runtime adapter file for content-only envs"
```

---

## Task 3: Informative (non-`✓`) models line + honest Runtime block (D3)

**Files:**
- Modify: `cmd/ach-cli/cmd/hydrate.go` `summaryFromResult` (~lines 782-787)
- Test: `cmd/ach-cli/cmd/hydrate_test.go` (summary rendering)

**Step 1 — Write the failing test.**

Add to `cmd/ach-cli/cmd/hydrate_test.go` (adjust to the existing summary-test helper style in
that file — locate the existing `summaryFromResult` tests and mirror their construction):

```go
// TestSummary_ModelsLine_InformativeNotWired asserts the Runtime block renders
// models as an informational line (server-side via the gateway), NOT a ✓
// "wired locally" line, and only when Models > 0.
func TestSummary_ModelsLine_InformativeNotWired(t *testing.T) {
	meta := summaryMeta{keyPrefix: keys.PrefixEk}

	withModels := summaryFromResult(hydrate.Result{
		Environment:    "demo",
		PlatformID:     "claude-code",
		RuntimeSummary: hydrate.RuntimeSummary{Models: 3, MCPServers: 1},
	}, meta)
	if !strings.Contains(withModels, "Models: 3") ||
		!strings.Contains(withModels, "gateway") {
		t.Errorf("models line must state count + server-side gateway note:\n%s", withModels)
	}
	if strings.Contains(withModels, "✓ Models") {
		t.Errorf("models must NOT render with a ✓ (they are not wired locally):\n%s", withModels)
	}

	noModels := summaryFromResult(hydrate.Result{
		Environment:    "demo",
		PlatformID:     "claude-code",
		RuntimeSummary: hydrate.RuntimeSummary{MCPServers: 1},
	}, meta)
	if strings.Contains(noModels, "Models") {
		t.Errorf("no Models line when Models==0:\n%s", noModels)
	}
	if strings.Contains(noModels, "A2A agents: 0") {
		t.Errorf("Runtime block must not print zero-count lines:\n%s", noModels)
	}
}
```

**Step 2 — Run; verify FAIL.**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd/`
Expected: FAIL — current code prints `✓ Models: 3` and unconditional `✓ A2A agents: 0`.

**Step 3 — Rewrite the Runtime block in `summaryFromResult`.**

Replace `cmd/ach-cli/cmd/hydrate.go:782-787`:

```go
	if hasRuntimeSummary(res.RuntimeSummary) {
		fmt.Fprintln(&b, "  Runtime")
		if res.RuntimeSummary.MCPServers > 0 {
			fmt.Fprintf(&b, "    ✓ MCP servers: %d\n", res.RuntimeSummary.MCPServers)
		}
		if res.RuntimeSummary.A2AAgents > 0 {
			fmt.Fprintf(&b, "    ✓ A2A agents: %d\n", res.RuntimeSummary.A2AAgents)
		}
		if res.RuntimeSummary.Models > 0 {
			// Models are NOT wired locally: access is a server-side LiteLLM
			// access-group behind the gateway, so this is informational (•),
			// never a ✓ "installed" line.
			fmt.Fprintf(&b, "    • Models: %d (served server-side via the gateway — nothing to install locally)\n",
				res.RuntimeSummary.Models)
		}
		fmt.Fprintln(&b)
	}
```

(`hasRuntimeSummary` already returns true iff any of the three counts > 0, so the block only
appears when there is something to report; each line is now individually gated on `> 0`,
matching the already-gated `compactSegments` multi-target path.)

**Step 4 — Run; verify PASS.**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd/`
Expected: PASS. If a pre-existing summary golden/string test asserted `✓ Models:` or a
zero-count `✓ A2A agents: 0`, update it to the new honest rendering (grep the test file for
`Models:` / `A2A agents:` and reconcile).

**Step 5 — Commit.**

```bash
git add cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/hydrate_test.go
git commit -m "feat(hydrate): render models as informative server-side line in summary"
```

---

## Task 4: Cobra scope flags — add `--no-runtime`, deprecate `--include-runtime`, remove `Opts.IncludeRuntime`

**Files:**
- Modify: `cmd/ach-cli/cmd/hydrate.go` (flag registration, `hydrateInputs`, `assertScopeFlags`, `runHydrate`, `runHydrateEngine`, `--help` Long)
- Modify: `internal/cli/hydrate/flags.go` (remove the deprecated `IncludeRuntime` field)
- Test: `cmd/ach-cli/cmd/hydrate_test.go` (mutual-exclusion table + deprecation notice + flag list)

**Step 1 — Write the failing tests.**

In `cmd/ach-cli/cmd/hydrate_test.go`:

(a) Extend the mutual-exclusion coverage (near the existing `--include-runtime --only-runtime`
tests at ~line 767). Add cases for the new flag:

```go
// TestHydrate_ScopeFlags_MutualExclusion covers the new --no-runtime pairings.
func TestHydrate_ScopeFlags_MutualExclusion(t *testing.T) {
	cases := []struct{ name string; args []string }{
		{"no-runtime + only-runtime", []string{"--no-runtime", "--only-runtime"}},
		{"no-runtime + include-runtime", []string{"--no-runtime", "--include-runtime"}},
		{"include-runtime + only-runtime", []string{"--include-runtime", "--only-runtime"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := hydrateInputs{}
			// map tc.args onto the input booleans:
			for _, a := range tc.args {
				switch a {
				case "--no-runtime":
					in.noRuntime = true
				case "--only-runtime":
					in.onlyRuntime = true
				case "--include-runtime":
					in.includeRuntime = true
				}
			}
			if err := assertScopeFlags(in); err == nil ||
				!strings.Contains(err.Error(), "mutually exclusive") {
				t.Fatalf("want 'mutually exclusive' error, got %v", err)
			}
		})
	}
}
```

(b) Add a deprecation-notice test (uses the command factory + a buffer; mirror the existing
`newHydrateCmd()` / `swapHydrateHTTPClientForTest` test harness in this file — locate an
existing end-to-end cobra test and copy its scaffolding for stderr capture). The assertion:
when `--include-runtime` is explicitly passed, stderr contains a "deprecated" notice; when it
is NOT passed, stderr does not. Keep this test focused on the notice string via a small helper
if the full run harness is heavy; otherwise assert against the `runHydrate` stderr.

(c) Update the flag-registration list test at `hydrate_test.go:547` — add `"no-runtime"` to
the expected set. `"include-runtime"` stays in the list (still registered, just hidden).

**Step 2 — Run; verify FAIL.**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd/`
Expected: FAIL — `in.noRuntime` undefined; `--no-runtime` not registered.

**Step 3 — Register the `--no-runtime` flag; hide + deprecate `--include-runtime`.**

In `newHydrateCmd()`:

- Add a `flagNoRuntime bool` to the `var (...)` block.
- Register it after `--only-runtime` (`hydrate.go:~232`):

```go
	cmd.Flags().BoolVar(&flagNoRuntime, "no-runtime", false,
		"Opt OUT of runtime wiring: hydrate context only (no mcp/a2a credential on disk)")
```

- Change the `--include-runtime` registration (`hydrate.go:229`) help text + mark it hidden:

```go
	cmd.Flags().BoolVar(&flagIncludeRuntime, "include-runtime", false,
		"(deprecated, no-op) runtime is wired by default; use --no-runtime to opt out")
	if err := cmd.Flags().MarkHidden("include-runtime"); err != nil {
		panic(fmt.Sprintf("MarkHidden(include-runtime) failed: %v", err))
	}
```

- Thread `flagNoRuntime` into the `runHydrate(cmd, hydrateInputs{...})` call
  (`hydrate.go:189-210`): add `noRuntime: flagNoRuntime,`.

**Step 4 — Update `hydrateInputs` + `Opts` construction.**

In `hydrateInputs` (`hydrate.go:290-302`): add `noRuntime bool`. **Keep** `includeRuntime bool`
(still read for the deprecation notice + mutual-exclusion) but stop passing it to `Opts`.

In `runHydrateEngine` `Opts{...}` (`hydrate.go:523-544`): **remove** the line
`IncludeRuntime: in.includeRuntime,` and add `NoRuntime: in.noRuntime,`.

**Step 5 — Update `assertScopeFlags` (`hydrate.go:425-439`).**

```go
func assertScopeFlags(in hydrateInputs) error {
	switch {
	case in.noRuntime && in.onlyRuntime:
		return &exit.CodedError{Code: exit.General,
			Msg: "--no-runtime and --only-runtime are mutually exclusive"}
	case in.noRuntime && in.includeRuntime:
		return &exit.CodedError{Code: exit.General,
			Msg: "--no-runtime and --include-runtime are mutually exclusive (--include-runtime is the deprecated default)"}
	case in.includeRuntime && in.onlyRuntime:
		return &exit.CodedError{Code: exit.General,
			Msg: "--include-runtime and --only-runtime are mutually exclusive"}
	}
	if in.wait && in.lockTimeout > 0 {
		return &exit.CodedError{Code: exit.General,
			Msg: "--wait and --lock-timeout are mutually exclusive"}
	}
	return nil
}
```

**Step 6 — Emit the deprecation notice.**

In `runHydrate`, right after the `assertScopeFlags` call (`hydrate.go:354-356`):

```go
	if cmd.Flags().Changed("include-runtime") && !in.noWarnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
			"notice: --include-runtime is deprecated and now a no-op (runtime is wired by default; use --no-runtime to opt out)")
	}
```

**Step 7 — Update the `--help` Long text (`hydrate.go:141-179`).**

Rewrite the `Scope:` section:

```
Scope:
  (default)           Wire everything: context (prompts / artifacts / skills)
                      AND the environment's runtime (mcp / a2a). Models are
                      server-side (via the gateway) and never written locally.
  --no-runtime        Context only — do not write the mcp/a2a runtime config
                      (no forwarder credential lands on disk).
  --only-runtime      Runtime only (excludes context).
```

Also fix the opening `Long` sentence if it implies context-only default; it already reads
"Download an environment's content … and wire up its runtime" — keep that (now literally the
default).

**Step 8 — Remove the dead `Opts.IncludeRuntime` field.**

In `internal/cli/hydrate/flags.go`: delete the `IncludeRuntime bool` field + its (now
deprecated) doc block added in Task 1. Confirm no readers remain:

Run: `grep -rn "\.IncludeRuntime" internal/ cmd/ --include="*.go"`
Expected: **no matches** (all engine derivations flipped in Task 1; the cobra `Opts{}` line
removed in Step 4; `commit_test.go` sites removed in Task 1). If any remain, fix them before
proceeding.

**Step 9 — Run; verify PASS + boundary intact.**

```bash
make test-unit-pkg PKG=./cmd/ach-cli/cmd/
make test-unit-pkg PKG=./internal/cli/hydrate/
./scripts/dev.sh go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|controller-runtime' && echo "BOUNDARY VIOLATION" || echo "boundary clean"
```
Expected: both packages PASS; "boundary clean".

**Step 10 — Commit.**

```bash
git add cmd/ach-cli/cmd/hydrate.go internal/cli/hydrate/flags.go cmd/ach-cli/cmd/hydrate_test.go
git commit -m "feat(hydrate): add --no-runtime opt-out; deprecate --include-runtime to a no-op"
```

---

## Task 5: Flip `env uninstall` default for symmetry (D4)

**Files:**
- Modify: `internal/cli/hydrate/scope.go` (`BuildScopedEmpty` signature + derivation + doc)
- Modify: `cmd/ach-cli/cmd/uninstall.go` (flags, help, mutual-exclusion, notice, `BuildScopedEmpty` call)
- Test: `internal/cli/hydrate/scope_test.go` + `cmd/ach-cli/cmd/uninstall_test.go`

**Step 1 — Rewrite `scope_test.go` for the flipped semantics.**

Change `BuildScopedEmpty`'s parameter meaning from `(includeRuntime, onlyRuntime)` to
`(noRuntime, onlyRuntime)`. New truth table for the SURVIVOR set:

| call | meaning | context bucket | runtime bucket |
|------|---------|----------------|----------------|
| `(false, false)` **default** | remove context + runtime (full teardown) | empty | empty |
| `(true, false)` `--no-runtime` | remove context only | empty | **retained** |
| `(false, true)` `--only-runtime` | remove runtime only | **retained** | empty |

Update the sub-tests in `TestBuildScopedEmpty`:

```go
	t.Run("default_full_teardown_empties_all_buckets", func(t *testing.T) {
		got := BuildScopedEmpty(fullPrev(), false, false)
		if ctx, rt := bucketCounts(got); ctx != 0 || rt != 0 {
			t.Fatalf("default teardown must empty all buckets, got context=%d runtime=%d", ctx, rt)
		}
	})

	t.Run("noRuntime_context_only_retains_runtime", func(t *testing.T) {
		got := BuildScopedEmpty(fullPrev(), true, false)
		if len(got.Prompts) != 0 || len(got.Plugins) != 0 || len(got.Artifacts) != 0 {
			t.Fatalf("context buckets must be empty (context removed), got %+v", got)
		}
		if len(got.RuntimeFiles) != 1 || len(got.Adapter.Files) != 1 {
			t.Fatalf("runtime must be retained: runtimeFiles=%d adapterFiles=%d",
				len(got.RuntimeFiles), len(got.Adapter.Files))
		}
		if got.Adapter.ID != "claude-code" {
			t.Fatalf("Adapter.ID must survive when runtime retained, got %q", got.Adapter.ID)
		}
	})

	t.Run("onlyRuntime_retains_context", func(t *testing.T) {
		got := BuildScopedEmpty(fullPrev(), false, true)
		if len(got.Prompts) != 1 || len(got.Plugins) != 1 || len(got.Artifacts) != 1 {
			t.Fatalf("context must be retained, got prompts=%d plugins=%d artifacts=%d",
				len(got.Prompts), len(got.Plugins), len(got.Artifacts))
		}
		if len(got.RuntimeFiles) != 0 || len(got.Adapter.Files) != 0 {
			t.Fatalf("runtime must be empty, got runtimeFiles=%d adapterFiles=%d",
				len(got.RuntimeFiles), len(got.Adapter.Files))
		}
	})
```

Update the `does_not_mutate_prev` sub-test's three calls to the new arg meanings
(`(false,false)`, `(true,false)`, `(false,true)`), and `nil_prev` / `retained_slices` (change
`BuildScopedEmpty(prev, false, false)` — which previously "retained runtime" — to
`BuildScopedEmpty(prev, true, false)` for the "retains runtime" precondition, since default now
empties everything).

**Step 2 — Run; verify FAIL.**

Run: `make test-unit-pkg PKG=./internal/cli/hydrate/`
Expected: FAIL — old derivation retains runtime by default; new tests expect full teardown.

**Step 3 — Flip the `BuildScopedEmpty` derivation (`scope.go:47-60`).**

```go
func BuildScopedEmpty(prev *state.File, noRuntime, onlyRuntime bool) *state.File {
	out := &state.File{SchemaVersion: "3"}
	if prev == nil {
		return out
	}
	out.Environment = prev.Environment
	out.Profile = prev.Profile

	// includeContext: context SURVIVES only when removing runtime only
	// (--only-runtime). removeRuntime: runtime is removed by default and under
	// --only-runtime; only --no-runtime keeps it.
	includeContext := onlyRuntime
	removeRuntime := !noRuntime

	if includeContext {
		out.Prompts = slices.Clone(prev.Prompts)
		out.Plugins = slices.Clone(prev.Plugins)
		out.Artifacts = slices.Clone(prev.Artifacts)
		out.Skills = slices.Clone(prev.Skills)
	}
	if !removeRuntime {
		out.RuntimeFiles = slices.Clone(prev.RuntimeFiles)
		out.Adapter = state.AdapterSection{
			ID:    prev.Adapter.ID,
			Files: slices.Clone(prev.Adapter.Files),
		}
	}
	return out
}
```

Update the function doc block (`scope.go:11-46`) to describe the flipped default and the new
`(noRuntime, onlyRuntime)` parameters.

**Step 4 — Update `uninstall.go` flags/help/call.**

- Add `flagNoRuntime bool` to the `var (...)` block; add `noRuntime bool` to `uninstallInputs`.
- Register `--no-runtime` after `--only-runtime` (`uninstall.go:130`):

```go
	cmd.Flags().BoolVar(&flagNoRuntime, "no-runtime", false,
		"Remove context resources only, leaving runtime config in place")
```

- Deprecate + hide `--include-runtime` (`uninstall.go:128`):

```go
	cmd.Flags().BoolVar(&flagIncludeRuntime, "include-runtime", false,
		"(deprecated, no-op) runtime is removed by default; use --no-runtime to keep it")
	if err := cmd.Flags().MarkHidden("include-runtime"); err != nil {
		panic(fmt.Sprintf("MarkHidden(include-runtime) failed: %v", err))
	}
```

- Thread `noRuntime: flagNoRuntime` into the `runUninstall(cmd, uninstallInputs{...})` call.
- Rewrite the `Long` `Scope (mirrors hydrate, D-26):` block:

```
Scope (mirrors hydrate):
  (default)           Remove the WHOLE projection: context (prompts / plugins /
                      artifacts / skills) AND runtime config (mcp / a2a).
  --no-runtime        Remove context resources only, leaving runtime in place.
  --only-runtime      Strip ONLY runtime config (mutually exclusive with
                      --no-runtime).
```

- Replace the mutual-exclusion gate (`uninstall.go:143-148`) with the three-way check
  (mirror Task 4 Step 5, using `in.noRuntime` / `in.onlyRuntime` / `in.includeRuntime`), and
  add the deprecation notice after it:

```go
	if in.includeRuntime && !in.dryRun { // notice regardless of dry-run is fine; keep simple:
	}
```

  — simpler: emit the notice unconditionally when passed:

```go
	if cmd.Flags().Changed("include-runtime") {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
			"notice: --include-runtime is deprecated and now a no-op (runtime is removed by default; use --no-runtime to keep it)")
	}
```

- Update the `BuildScopedEmpty` call (`uninstall.go:225`):

```go
		scopedEmpty := hydrate.BuildScopedEmpty(prev, in.noRuntime, in.onlyRuntime)
```

**Step 5 — Update `uninstall_test.go`.**

- The "default (context-only) scope: scopedEmpty retains runtime" test (~line 98-103) now
  expects a **full teardown**: `scopedEmpty` empties BOTH context and runtime. Rename/retarget:
  default asserts `len(RuntimeFiles)==0` AND context empty. Add a `--no-runtime` case asserting
  runtime is retained (the old default behavior).
- The `--include-runtime --only-runtime` mutual-exclusion test (~line 149) stays valid.
- The `--include-runtime` full-teardown test (~line 177) now equals the default: retarget it
  to assert `--include-runtime` still yields a full teardown (no-op alias) OR replace with a
  default-teardown assertion.
- The "scoped uninstall retains runtime survivor rows in state" test (~line 207-224): this
  used the old default (remove context, keep runtime). Change its invocation to `--no-runtime`
  so it still exercises the "state.json retained with survivor runtime rows" path.

**Step 6 — Run; verify PASS.**

```bash
make test-unit-pkg PKG=./internal/cli/hydrate/
make test-unit-pkg PKG=./cmd/ach-cli/cmd/
```
Expected: both PASS.

**Step 7 — Commit.**

```bash
git add internal/cli/hydrate/scope.go internal/cli/hydrate/scope_test.go \
        cmd/ach-cli/cmd/uninstall.go cmd/ach-cli/cmd/uninstall_test.go
git commit -m "feat(uninstall): remove runtime by default; add --no-runtime to keep it"
```

---

## Task 6: Documentation (same-commit hygiene)

**Files:**
- Modify: `internal/cli/CLAUDE.md` (CLI subsystem hub — the hydrate command surface)
- Modify: `/home/jcm/Projects/ach/CLAUDE.md` (root hub — where it describes hydrate default scope)
- Modify: `examples/README.md` if it documents the `--include-runtime` opt-in for the demo
- Check: `references/troubleshooting.md`, `references/local-testing-gateway.md` for any
  "context-only default" / "pass --include-runtime" claims

**Step 1 — Grep for stale claims.**

```bash
grep -rn "include-runtime\|context-only\|context only\|only-runtime" \
  CLAUDE.md internal/cli/CLAUDE.md examples/ references/ docs/ --include="*.md" | grep -vi "docs/plans/"
```

**Step 2 — Update each hit** to the new model: hydrate wires context **+** runtime by
default; `--no-runtime` opts out; `--only-runtime` narrows to runtime; `--include-runtime` is
a deprecated no-op. In the root `CLAUDE.md`, the "Environment two-axis status" and the
"`ach-cli env hydrate` → …" bullets that mention runtime should reflect default-on. In
`internal/cli/CLAUDE.md`, update the `env hydrate` command-surface line.

**Step 3 — Commit.**

```bash
git add -A -- '*.md'
git commit -m "docs(hydrate): document runtime-on-by-default + --no-runtime opt-out"
```

---

## Task 7: Full-suite gate + CLI smoke test

**Step 1 — Full unit sweep (catches any test not enumerated above).**

Run: `make test-unit`
Expected: PASS. Likely additional fallout to reconcile (fix in place, then re-run):
- Any test asserting a default hydrate leaves `RuntimeSummary` empty.
- Any `wiring_projectplugins_test.go` / `wiring_phase3_test.go` case that passed
  `includeRuntime=false` as the DEFAULT via a `Render(...false)` call — those pass the raw
  bool directly and are unaffected, but grep to confirm none derive it from `Opts`.
- `projection_pimono_smoke_test.go:TestProjection_Pimono_RuntimeGate` calls
  `Render(..., true, false)` with an explicit `includeRuntime=false` — **unaffected** (it
  tests the param, not the default). Do not change it.

**Step 2 — Lint the touched packages.**

Run: `make qa-lint-changed`
Expected: clean (SPDX headers already present on all modified files; no new files created
except possibly a test file — ensure it starts with `// SPDX-License-Identifier: Apache-2.0`).

**Step 3 — Build the CLI + manual smoke (per internal/cli/CLAUDE.md release rule).**

Run: `make build-cli`
Then verify help + flag surface on the HOST binary:

```bash
./bin/ach-cli env hydrate --help    # expect --no-runtime listed; --include-runtime NOT listed (hidden)
./bin/ach-cli env uninstall --help  # expect --no-runtime listed; --include-runtime NOT listed
./bin/ach-cli env hydrate x --no-runtime --only-runtime 2>&1 | grep "mutually exclusive"
```
Expected: `--no-runtime` visible in both; `--include-runtime` absent from help; the mutex
error prints.

**Step 4 — Final commit (if smoke revealed doc/help tweaks) + full local gate.**

```bash
make test-unit
make qa-lint
```

Then hand off to `superpowers:finishing-a-development-branch`. **Do NOT** run `make e2e-full`
as a required gate: this change is CLI-only (`internal/cli/**`, `cmd/ach-cli/**`) and touches
none of `internal/controller|platformapi|forwarder|contentservice/`, `api/v1alpha1/`,
`deploy/helm/ach/`, or `test/e2e/`. The hydrate unit + golden tests are the gate. `make pre-push`
runs via the installed hook at push time (18 gates incl. lint + unit).

---

## Out of scope / non-goals

- No change to how models are accessed (server-side LiteLLM access-group via the gateway).
  Models remain informational-only; nothing is written to any tool config.
- No new credential handling — `step12bGitignore` + `0600` already cover the now-default
  `.mcp.json`.
- A2A already rides `.mcp.json` (`a2aAgents` block); no new transport work.
- The `--raw` surface (`examples/hydrate.json` golden-diff anchor) is untouched — it
  short-circuits before the engine.
- No change to `--only-runtime`, `--sync`, `--force`, `--dry-run`, `--global`, `--target`
  semantics beyond the derivation flip they inherit.
