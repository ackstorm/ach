# Operator-side `mcpServers` (CONTRACT_v3 addendum) — implementation + coordination

**Date:** 2026-07-07 · **Upstream:** `../ach-agent/docs/plan/CONTRACT_v3-ADDENDUM-mcpservers.md`

The ach-agent harness is moving `engine.repoCheckout` → a new **top-level
`mcpServers`** map (discriminated union: `repoCheckout` / `local` / `remote`).
This is the ACH operator-side render for it. Built ahead of the frozen schema
landing upstream — see **Coordination** for the swap step.

## What shipped here

- `api/ach/v1alpha1/achagent_types.go` — `McpServerSpec` union (`Name`, `Type`
  enum, `RepoCheckout`/`Local`/`Remote` sub-specs) + per-type CEL (mirrors
  `channels`/`prompt.system`); `ACHAgentSpec.MCPServers []McpServerSpec`
  (`+listType=map` by `name`).
- `internal/agentrender/config.go` — `AgentConfig.McpServers map[string]McpServerBlock`
  + `McpServerBlock` (flattened union) + `RepoCheckoutParamsBlock`.
- `internal/agentrender/render.go` — `renderMcpServers` (list → map by name;
  `local.env` sanitized via `sanitizeForwardEnv`; `remote.headers` passthrough).
- `internal/agentrender/testdata/agent-config-v1.schema.json` — **hand-authored**
  pre-landing copy (top-level `mcpServers` + 5 `$defs`). Additions-only diff.
- `internal/agentrender/schema_test.go` — drift guard skips while upstream is
  pre-addendum; `mcp-servers` render-matrix case (all 3 arms → schema-conformant).
- generated: `zz_generated.deepcopy.go`, `config/crd/bases`, chart `crd-sources/`,
  `docs/api-reference/`. Example: `examples/agent-runtime/agent.yaml`.

## Decisions (D1–D4)

- **D1 — placement: `ACHAgent.spec.mcpServers`** (agent-side). `local`/`remote`
  are per-agent tools; `repoCheckout.sourceMcpServerId` refs the agent's
  `capability.environment`. Everything the union needs is agent-scoped. Matches
  the addendum's `AgentSpec.mcpServers`.
- **D2 — scope: all 3 arms.** `local`/`remote` are pure render passthrough; their
  `${env:NAME}` secrets ride the existing `profile.spec.extraEnv` (`secretKeyRef`)
  — no new secret machinery. YAGNI note: only `repoCheckout` has a live driver;
  `local`/`remote` are commented-out in the example.
- **D3 — `sourceMcpServerId` cross-validation: WON'T DO (not the operator's
  problem).** Intra-object CEL only (`repoCheckout` present ⇒ `sourceMcpServerId`
  non-empty, via `MinLength=1`). The addendum's cross-object check (id ∈ the
  Environment's MCP set) is **architecturally impossible** operator-side, not
  merely costly: `spec.capability.environment` is an **ACH-side** reference the
  harness resolves at runtime by hydrating against ACH **with the agent's ek_**.
  The Environment can live in **another cluster or region**, so there is no
  guaranteed-local `Environment` CR to `Get`; and the operator holds **no ACH
  keys** (the ek_ is the agent's, injected as a Secret — the operator never
  hydrates). So the operator cannot list/resolve the Environment's MCP set at all.
  The harness **fail-softs** on a wrong id (returns an error string, no crash) —
  which is the correct and only behaviour. ach-agent's §5 asks for the check, but
  a peer request does not compel work the operator physically cannot do. **Do NOT
  add an Environment `Get` here** — it would break the moment the Environment is
  remote (the common case). (Superseded the earlier "defer for RBAC/coupling"
  framing: the real reason is cross-cluster/keyless, not RBAC — operator-rbac
  already grants `environments`.)
- **D4 — schema: hand-authored now, byte-swap at coordination.** The vendored
  schema can't match upstream (not regenerated yet), so `TestSchema_NoDrift`
  skips while upstream lacks a top-level `mcpServers` property. Render-conformance
  (`TestRender_ConformsToSchema`) runs now and proves the output is structurally
  correct against the addendum contract.

## Coordination — DO THIS when ach-agent lands the addendum

1. ach-agent applies §4 to `config/schema.py`, deletes `RepoCheckoutBlock` +
   `engine.repoCheckout`, adds the `mcpServers` union, and regenerates
   `docs/schemas/agent-config-v1.schema.json` (`gen_schema.py`).
2. Re-vendor: copy their regenerated schema over
   `internal/agentrender/testdata/agent-config-v1.schema.json` (**replaces** the
   hand-authored copy). The drift guard's skip falls away automatically (upstream
   now has top-level `mcpServers`) and `TestSchema_NoDrift` resumes byte-checking.
3. `make test-unit-pkg PKG=./internal/agentrender/...` — expect drift + render
   conformance green. If render-conformance breaks, the upstream field
   names/shape drifted from this hand-authored contract → reconcile the render
   structs (`config.go`) to their regenerated `$defs`.
4. **D3 is settled — do not implement the cross-validation.** The Environment is
   ACH-side (possibly cross-cluster/region) and the operator has no ACH keys to
   resolve it; the harness fail-soft is correct. See D3 above.
