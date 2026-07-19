# Brief: hydrate runtime-on-by-default + informative models

**Status:** pre-plan brief for review. NOT the final execution plan. A reviewing
agent validates the findings/decisions and produces the final plan.

**Date:** 2026-07-17
**Repo:** `ackstorm/ach` — `ach-cli env hydrate` (`internal/cli/hydrate`, `internal/cli/adapter`, `cmd/ach-cli/cmd/hydrate.go`)

---

## 1. Goal / motivation

`ach-cli env hydrate` today defaults to **context-only** (prompts / skills /
artifacts). The Environment's directly-attached **MCP / A2A** endpoints are
projected only under the opt-in `--include-runtime`. Result: a bare `hydrate`
produces a **half-wired workspace** — content lands, connections don't — so the
agent errors reaching MCP servers that were never written. Users hit **more
errors when they forget the flag than when they pass it**.

An Environment is a bundle ("here are your models + MCPs + A2A + content").
Splitting the runtime wiring out of the default inverts the common case. Goal:
**hydrate wires everything projectable by default**, and is **informative** about
what it deliberately does not wire (models).

## 2. Ground truth (verified in code)

`manifest.RuntimeBlock` (`internal/cli/manifest/manifest.go:38`) =
`{ Models, MCPServers, A2AAgents []ContentRef }`.

- **Default hydrate:** context + plugin-contributed MCPs only. Skips the env's
  direct `m.Runtime` (mcp/a2a). Gated by
  `includeRuntime := opts.IncludeRuntime || opts.OnlyRuntime` at
  `commit.go:476, 601, 952` and `wiring.go:387`.
- **`--include-runtime`:** `RenderRuntime` writes the adapter runtime-config
  (claudecode: `.mcp.json`) with `mcpServers.{id}` **and** `a2aAgents.{id}`, each
  `{type:"http", url:endpoint, headers:{x-ach-key:<cred>, x-ach-environment:<env>}}`
  (`internal/cli/adapter/claudecode/claudecode.go` `renderMcpJSON`).
- **Models are NEVER projected** — `renderMcpJSON` ignores `m.Runtime.Models`.
  Silent drop, flag or not. Correct (model access = server-side LiteLLM
  access-group; the tool only points at the gateway, nothing client-side to
  write) — but **silent**, not informative.
- **A2A is already "hydrated via MCP"** — a2a agents are written as `http`
  entries in the same `.mcp.json` (`a2aAgents` block). No new work needed.
- **Credential guardrail already exists** — `step12bGitignore`
  (`commit.go:1341`) adds a marker-bounded `.gitignore` block covering every
  written file incl. the runtime `.mcp.json`, and files are mode `0600`. So
  turning runtime on by default does **not** newly expose the `x-ach-key`
  credential; the defense already fires whenever runtime writes the file.

Grading the user's mental model: models-can't-be-hydrated ✅ (already dropped),
MCP-can ✅ (opt-in today), A2A-via-MCP ✅ (already implemented), more-errors-
without-flag ✅ (default skips exactly the direct mcp/a2a).

## 3. Proposed change

### Change 1 — flip the default: runtime ON unless opted out *(primary)*
Hydrate projects **content + mcp + a2a** by default. Introduce an opt-OUT
(`--no-runtime`, or `--only-context`) for the rare "just prompts, no live
credential on disk" case. Keep `--only-runtime` (runtime-only narrowing).

Touch points:
- `cmd/ach-cli/cmd/hydrate.go:229-232` — flag registration + help. Add the
  opt-out flag; `--include-runtime` becomes a **no-op/deprecated alias** (keep it
  accepted for back-compat, hidden, so existing scripts don't break).
- The three derivations `includeRuntime := opts.IncludeRuntime || opts.OnlyRuntime`
  (`commit.go:476, 601, 952`) + `wiring.go:387` → derive from the new opt-out
  (runtime on unless `--no-runtime`/`--only-context`; `--only-runtime` still
  forces it).
- `assertScopeFlags` mutual-exclusion (`hydrate.go:~420-429`) — update: new
  opt-out is mutually exclusive with `--only-runtime`; reconcile with
  `--include-runtime` alias.
- `internal/cli/hydrate/scope.go` `BuildScopedEmpty(prev, includeRuntime, onlyRuntime)`
  — the teardown/`--sync` scope must match the new default so a default `--sync`
  also reconciles runtime (else default hydrate writes runtime but default sync
  never removes it → drift). **Reviewer: verify sync symmetry.**

### Change 2 — models: informative, not silent
When `m.Runtime.Models` is non-empty, emit a summary line (stderr or the hydrate
summary), e.g.:

> environment provides N models: a, b, c — access is server-side via the gateway;
> nothing wired locally.

Not a projected file. Surfacing point: the hydrate result summary
(`internal/cli/hydrate/result.go` `ProjectedByKind` / the summary printer) or the
command-layer summary in `cmd/ach-cli/cmd/hydrate.go`. Reviewer picks the layer;
the count source must be `m.Runtime.Models`, consistent with how mcp/a2a are
tallied from the keys that actually landed.

## 4. Decisions for the reviewer
- [ ] **Flip-default + `--no-runtime` alias** (recommended, back-compat) **vs
      remove `--include-runtime` entirely** (simplest mental model, breaks
      scripts/muscle-memory that pass it). Recommend the flip.
- [ ] Opt-out flag name: `--no-runtime` vs `--only-context`.
- [ ] Models line: stderr notice vs part of the structured summary; wording.
- [ ] `--sync` scope symmetry (`scope.go`) — confirm default sync now reconciles
      runtime so there's no write-but-never-remove drift.
- [ ] Any adapter whose `RenderRuntime` is a no-op / errors on empty runtime
      (codex/gemini/opencode/pimono) — confirm default-on doesn't regress a
      target that legitimately has nothing to wire.

## 5. Verification
- Unit: scope derivation + `assertScopeFlags` mutual-exclusion table incl. the
  new flag and the deprecated alias.
- Golden-diff: a default hydrate now produces `.mcp.json` — update the W3-P3
  golden-diff anchors (`--raw` / scope fixtures) that assumed context-only
  default.
- The models-info line: assert it fires only when `m.Runtime.Models` non-empty.
- `.gitignore` + 0600 still covers the now-default `.mcp.json`
  (`step12bGitignore`).
- `make test-unit` + hydrate golden tests. (CLI-only change — no envtest/e2e
  controller surface, but hydrate golden fixtures are the gate.)

## 6. Out of scope / non-goals
- No change to how models are accessed (server-side access-group / gateway
  stays). Models remain informational-only — we are NOT writing model config
  into any tool.
- No new credential handling — the existing gitignore + 0600 defense already
  covers default-on runtime.
- A2A already rides `.mcp.json`; no new a2a transport work.
