# Plan — hydrate summary: count runtime components (mcp/a2a/model)

**Date:** 2026-06-04
**Author:** planner (separate agent executes; self-contained).
**Goal:** Surface runtime components in the `ach hydrate` success summary, e.g.

```
Hydrated for claude-code
  ✓ 13 agents, 2 commands, 35 skills, 1 mcp
  ✓ 110 files written, 0 preserved
```

Today the first line only counts **plugin-projected** kinds (agents/commands/
skills). Runtime mcp/a2a/model project to `.claude/settings.json` +
`.ach/runtime/*.json` but are NOT counted, so the summary stayed silent even
when a runtime MCP server WAS projected — and stayed identically silent in the
v0.2.7 bug where it was NOT projected. Counting them makes the summary a real
signal (and would have made that bug obvious).

## Why count from the written file, not the manifest

Count source = the runtime `FileWrite.Keys` that actually landed in
`.claude/settings.json` (`mcpServers.<id>`, `a2aAgents.<id>`). This guarantees
the summary number can NEVER disagree with the file the user inspects — which
is the entire point of the request. Counting `len(m.Runtime.MCPServers)` from
the manifest would re-introduce the exact disagreement we are fixing (manifest
said 1, file had 0).

Models are NOT written to settings.json (all models share the `/v1` endpoint);
they only appear in the `.ach/runtime/model.json` mirror. Count `model` from the
mirror tally (see step 2), gated to when the mirror was written.

## Current code (verbatim anchors)

Summary formatter — `cmd/ach-cli/cmd/hydrate.go:561`:
```go
func summaryFromResult(res hydrate.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hydrated for %s\n", res.PlatformID)
	if len(res.ProjectedByKind) > 0 {
		kinds := make([]string, 0, len(res.ProjectedByKind))
		for k := range res.ProjectedByKind { kinds = append(kinds, k) }
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
No change needed here IF runtime counts are merged into `ProjectedByKind` under
keys `mcp`/`a2a`/`model` — they print automatically (alpha-sorted, so order
becomes `agents, commands, mcp, skills` — acceptable; no pluralization, so
`1 mcp` renders exactly as asked).

`Result.ProjectedByKind` — `internal/cli/hydrate/result.go:47`. Populated in
`internal/cli/hydrate/commit.go:447`:
```go
if result.ProjectedByKind == nil { result.ProjectedByKind = map[string]int{} }
for k, n := range renderResult.ProjectedByKind { result.ProjectedByKind[k] += n }
```
`RenderResult.ProjectedByKind` — `internal/cli/hydrate/result.go:~120`
(plugin-only today). Runtime `WrittenFiles` live in the same `RenderResult`.

Runtime FileWrite is produced by `renderMcpJSON` /
`claudecode.RenderRuntime` (`internal/cli/adapter/claudecode/claudecode.go:161,
:219`) with `Keys = ["mcpServers.<id>", "a2aAgents.<id>"...]`
(`claudecode.go:179, :189`). The dispatcher publishes them in the runtime loop
at `internal/cli/hydrate/wiring.go:356-365` (`result.WrittenFiles`).

Kind label set — `internal/cli/adapter/route/kinds.go:21` already includes
`"mcp"`. Add `"a2a"`, `"model"` if a known-kind membership check matters
(executor: verify whether the summary path requires membership; it does not
today — `ProjectedByKind` is free-form).

## Change — single commit

### `internal/cli/hydrate/wiring.go` — dispatcher Render runtime loop
After publishing each runtime `FileWrite` (the `result.WrittenFiles` loop ~356),
tally its `Keys` by prefix into `result.ProjectedByKind`:
```go
for _, fw := range fws {
	// ... existing publish ...
	result.WrittenFiles = append(result.WrittenFiles, entry)
	if result.ProjectedByKind == nil { result.ProjectedByKind = map[string]int{} }
	for _, k := range fw.Keys {
		switch {
		case strings.HasPrefix(k, "mcpServers."): result.ProjectedByKind["mcp"]++
		case strings.HasPrefix(k, "a2aAgents."):  result.ProjectedByKind["a2a"]++
		}
	}
}
```
Rationale: counts exactly what landed in settings.json. Adapter-agnostic enough
for claude-code; other adapters (gemini/codex/opencode) that emit the same
`mcpServers.`/`a2aAgents.` key shape get counted for free. If an adapter uses a
different key prefix, executor adds its prefix (verify the other adapters'
RenderRuntime Keys shape — `internal/cli/adapter/*/`).

### `model` count (optional, recommended)
Models don't hit settings.json. In `writeRuntimeMirror`
(`internal/cli/hydrate/commit.go:1008`) the `model` bucket =
`m.Runtime.Models`. Where that mirror's entries are tallied back to `result`
(commit.go:~488 `result.FilesWritten += len(rf)`), also add:
```go
if len(m.Runtime.Models) > 0 {
	if result.ProjectedByKind == nil { result.ProjectedByKind = map[string]int{} }
	result.ProjectedByKind["model"] += len(m.Runtime.Models)
}
```
Gate to the same condition under which the runtime mirror is written (executor:
confirm — likely `IncludeRuntime || OnlyRuntime`, NOT dry-run). If models aren't
mirrored in this run, don't count them. Keep mcp/a2a (settings.json) and model
(mirror) consistent with what was actually written.

## Tests
- `cmd/ach-cli/cmd/hydrate_test.go` (or wherever `summaryFromResult` is tested):
  add a `Result{ProjectedByKind:{"skills":35,"mcp":1}}` case → assert the line
  `35 skills, 1 mcp` (note alpha order if both present: `1 mcp, 35 skills`).
- `internal/cli/hydrate/wiring_test.go` / `wiring_phase3_test.go`: a runtime
  FileWrite with `Keys=["mcpServers.my-mcp"]` → assert
  `RenderResult.ProjectedByKind["mcp"] == 1`. Reuse the existing
  `wiring_phase3_test.go:183` mcpServers fixture.
- Round-trip: 0 runtime servers → no `mcp` key in the map → summary unchanged
  (no `0 mcp`).

**Verify:** `make test-unit-pkg PKG=./internal/cli/...`
+ `make test-unit-pkg PKG=./cmd/ach-cli/...` + `make qa-lint-changed`.
Manual: rebuild `make build-cli-host`, hydrate the `platform` env, confirm the
summary shows `1 mcp` AND `cat .claude/settings.json` agrees.

## Decisions / scope
- Merge into `ProjectedByKind` (no new struct field, no formatter rewrite).
- Labels `mcp` / `a2a` / `model` — match the `.ach/runtime/*.json` bucket names
  and the existing `KnownComponentKinds["mcp"]`. No pluralization (consistent
  with `commands`/`skills` today; `1 mcp` is the asked-for output).
- Count from written keys (settings.json) for mcp/a2a; from the mirror for
  model. Never from the raw manifest.
- Out of scope: pluralization rules, per-id listing, dropped-runtime warnings
  (separate WIRE-03 path already handles plugin drops).

## Commit
`feat(cli): count runtime mcp/a2a/model in hydrate summary`
(CLI-only, unit-isolated; pre-push 18-gate still applies before push.)

## Executor verifications (don't assume)
1. Does `RenderRuntime`/settings.json project runtime UNCONDITIONALLY, or only
   under `--include-runtime`? The mcp/a2a count (from WrittenFiles keys) is
   automatically correct either way (it counts what was written). Just confirm
   the WrittenFiles loop is where the settings.json FileWrite lands.
2. Other adapters' runtime Keys prefixes (gemini/codex/opencode) — extend the
   switch if they differ, or scope the tally to claude-code if they don't emit
   `mcpServers.`/`a2aAgents.` keys.
3. The exact commit.go line where the runtime mirror file count bubbles to
   `result` (for the `model` tally insertion point).
