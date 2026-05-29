// SPDX-License-Identifier: Apache-2.0

// Package adapter owns the closed-set 4-adapter contract per ADAPT-01
// (CLI spec §7) and the init-side-effect registry that the cobra
// autodetection layer iterates.
//
// The four shipped adapters are:
//
//   - claude-code (subpackage internal/cli/adapter/claudecode): the
//     pass-through reference impl per CONTEXT.md D-05. Plugin canonical
//     format = Claude Code format per ADAPT-04, so claude-code's
//     TransformPlugin is a verbatim copy and RenderRuntime emits
//     .claude/.mcp.json directly from manifest.Runtime entries.
//
//   - codex (subpackage internal/cli/adapter/codex, plan 07-W3-02):
//     TOML merge into .codex/config.toml + plugin distribution into
//     .codex/{prompts,agents,skills}/<name>/ + agent frontmatter
//     rewrite. Silently drops hooks/ + commands/ components per
//     ADAPT-07.
//
//   - gemini-cli (subpackage internal/cli/adapter/gemini,
//     plan 07-W3-03): JSON merge into .gemini/settings.json + plugin
//     distribution into .gemini/extensions/<name>/. Silently drops
//     hooks/ per ADAPT-07.
//
//   - opencode (subpackage internal/cli/adapter/opencode,
//     plan 07-W3-04): platform-specific merge + plugin distribution
//     per spec §7.4 opencode.
//
// Boundary discipline (CONTEXT.md D-07 + D-09):
//
//   - This package owns the contract and the registry ONLY.
//   - Autodetection logic (zero/one/multi-match outcomes per ADAPT-02
//     and spec §7.5) lives in the cobra layer at plan 07-W3-05; this
//     package merely exposes Iter() so the cobra layer can ask each
//     registered adapter "do you see your signals at <root>?".
//   - The Adapter interface MUST NOT import internal/cli/hydrate,
//     internal/cli/extract, or internal/cli/lock. The contract is
//     testable standalone against manifest and state alone — that is
//     the point of the layered package layout (CONTEXT.md D-09).
//
// Credential propagation (ADAPT-03):
//
// Bearer credentials (pk_ or ek_) flow into RenderRuntime via a typed
// context key:
//
//	ctx = adapter.WithCredential(ctx, "pk_demo")
//	cred := adapter.CredentialFromContext(ctx) // "pk_demo"
//
// The context key type is unexported (credentialKey struct{}) — no
// other package can stuff a value into our key, and no caller can
// accidentally collide with a string-keyed value. Adapters MUST NOT
// read environment variables directly for the credential — the
// orchestrator (plan 07-W3-05 cobra wiring) is the single point that
// wraps the credential into ctx before calling RenderRuntime. The
// credential is then embedded into the runtime-config file's
// per-MCP-server headers (typically as "x-ach-key": "<cred>"); the
// on-disk plaintext discipline matches the existing CLI-04 model
// (Phase 6 D-04).
//
// Silent-drop accounting (ADAPT-07 / CONTEXT.md D-08):
//
// PluginWrite.Dropped is declared in adapter.go so the three sibling
// adapter plans (07-W3-02/03/04) can populate it without racing
// modifications to this file. Each adapter's TransformPlugin returns
// the names of the source-tree components it could not meaningfully
// translate (e.g. "hooks", "commands" for codex). The orchestrator
// emits a single end-of-hydration stderr warning; exit code is
// unchanged.
//
// SAFE-04 cascade contract (ResolveOutputContent):
//
// Every adapter implements ResolveOutputContent for the SAFE-04
// three-tier auto-claim cascade (Tier 2: adapter-driven content
// resolution). For pass-through adapters (claudecode), this returns
// the bytes RenderRuntime would emit for the named target — for any
// other target, source-byte read at Tier 3 suffices and the adapter
// may return (nil, nil) to signal "use Tier 3 fallback".
package adapter
