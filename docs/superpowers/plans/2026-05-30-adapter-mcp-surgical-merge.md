# Adapter MCP-config redesign: surgical merge into native tool config

**Status:** in progress (this branch, phase7/w6-01-cluster-handoff)
**Origin:** W6-01 runtime gate surfaced that the hydrate engine writes adapter
runtime config to the wrong place AND clobbers the user's pre-existing MCP
servers. Decided with the user (2026-05-30) to redesign the adapter write layer.

## Decisions (locked with the user)

- **Implement now** on this branch (not a formal GSD wave).
- **Scope:** project (workspace) by default; user/global on `--global`.
- **Surgical merge:** read the tool's existing native config → upsert ONLY our
  server entries → atomic write. Never touch the user's other servers/keys.
- **Ownership:** tracked only in ACH's own `state.json` (the contributed server
  keys per target). Nothing leaks into the tool's config (no prefix, no marker).
- **Per-key drift/auto-claim:** drift & auto-claim become per-KEY — we only ever
  claim/refuse/remove OUR keys; the user's keys are invisible to the engine.
- **Content (plugins/prompts/artifacts):** stays under `.ach/` cache this pass.

## Target-file map (def key; project / global)

| Tool        | Key         | Project (workspace root)        | Global (`$HOME`)                       |
|-------------|-------------|---------------------------------|----------------------------------------|
| claude-code | `mcpServers`| `<ws>/.claude/settings.json`    | `~/.claude/settings.json`              |
| gemini-cli  | `mcpServers`| `<ws>/.gemini/settings.json`    | `~/.gemini/settings.json`              |
| opencode    | `mcp`       | `<ws>/.opencode/opencode.json`  | `~/.config/opencode/opencode.json`     |
| codex       | `mcp_servers` (TOML table) | `<ws>/.codex/config.toml` | `~/.codex/config.toml`        |

Only opencode's global path diverges from `$HOME/<same-rel-path>`.
NOTE (user-directed, against official docs): claude-code MCP *definitions* go in
`settings.json` per the user's instruction; concern that Claude Code reads defs
from `.mcp.json`/`~/.claude.json` (not settings.json) is recorded — paths are
centralized so this is a one-line change if it proves wrong.

## Build steps (ordered)

1. **Tool-config root (scope) plumbing.** `Render` currently joins
   `filepath.Join(achDir, fw.Path)`. Add a `toolRoot` resolved from
   `(Opts.Output|cwd)` for project and `$HOME` for `--global`; join adapter
   FileWrite.Path against `toolRoot`. `state.json` stays under `achDir`.
   Opencode global remap: orchestrator maps `.opencode/opencode.json` →
   `.config/opencode/opencode.json` when global (or adapter exposes scope paths).
2. **Adapter target paths.** claudecode `.claude/.mcp.json` → `.claude/settings.json`;
   confirm gemini `.gemini/settings.json`, opencode `.opencode/opencode.json`,
   codex `.codex/config.toml`. Update each adapter's path const, `Detect` notes,
   `MergeStrategies`, `ResolveOutputContent`, and unit tests.
3. **Forward surgical merge** (replaces overwrite in `wiring.go` Render). Add
   `mergeForwardJSON`/`mergeForwardTOML` mirroring `syncDeepJSON`/`syncDeepTOML`:
   read existing → deep-merge our keys (from fw.Content) into the parsed tree →
   atomic-write 0o600. New `setDottedKey`/deep-merge helper alongside the existing
   `removeDottedKey`. On-disk bytes != fw.Content now (user content coexists).
4. **Per-key ownership + drift.** state.FileEntry already carries `Keys[]`. Record
   the hash of OUR contributed subtree (not the whole merged file) so drift
   compares our keys only. `Classify`/`Cascade` for merge targets: a pre-existing
   file with only user keys is NOT a collision — merge in. Collision = one of OUR
   key paths already on disk with a different value AND not recorded as ours.
5. **Removal / --sync.** Existing inverse-merge (`syncDeepJSON/TOML` + Keys[])
   already removes only our keys — reconcile with the new per-key forward model
   (remove keys present in prior state but absent from the new manifest).
6. **e2e rewrite** (`test/e2e/cli_hydrate_engine_test.go` + helpers):
   - sc1 paths → new target files; assert OUR entry present.
   - sc1 surgical proof: pre-seed a user MCP server in the target file, hydrate,
     assert the user server survives alongside ours.
   - reconcile `phase7StatePath` (workspace mode = `<out>/.ach/state.json`, no env
     segment per `state.ResolvePath`) and the runtime-config path consts.
   - sc2/sc3/sc4 adjusted to per-key drift + new paths.
7. **Build + gate.** `make build-e2e`; run `TestPhase7CLIEngine` to green;
   `make qa-security`; commit in logical increments; open PR.

## Already done (on disk, uncommitted)

- Bug A: content-fetch client sends CLI-03 `x-ach-environment` header for pk_
  (`cmd/ach-cli/cmd/hydrate.go`). Verified: failure shifted from missing_environment
  to the path assertion.
- Makefile: `build-e2e` prereqs (`gen-manifests gen-code`) + auto-route via
  `container_target`; global `KUBECONFIG` pin to `.gocache/kube/config` for
  host-only `wait-*`/`logs-*` targets.

## Status (2026-05-30 session)

**Committed + validated (gated green by pre-commit):**
- `643fc1f` fix(make): build-e2e prereqs + auto-route + wait-ach kubeconfig pin.
- `f0e1df7` feat(07-W6-01): surgical-merge adapter config (scope-root, forward
  merge, per-key drift), CLI-03 content header (Bug A), ADAPT-03 credential
  propagation. Unit tests rewritten (surgical-preserve + per-key-drift); lint
  clean; full test-unit green. Merge validated live: user's `my-personal-server`
  + `permissions` preserved, our servers added, `x-ach-key` populated.

**Remaining (NOT yet done):**
- **Bug E (content re-hydrate GAP, pre-existing, NOT the redesign):** the D-15
  short-circuit (`internal/cli/extract/stage.go` `fileSha256IfExists`) is
  single-file logic; a plugin extracts to a *directory* at `finalRelPath`, so
  on the 2nd hydrate `io.Copy` over the dir fails ("is a directory"); and
  `publishGzip`/`renameAtomic` can't rename over a pre-existing dir. The
  W5-01 "Phase-7 replace step in the orchestrator" (delete prior content
  before re-extract) and the orchestrator-level content drift/skip (compare
  fetched tarball xxh3 vs state SourceHash → no-op) were never wired. This is
  a substantive fix in the extract package + extractorImpl/commit.go, with
  w1_baseline no-op idempotency to satisfy. Blocks every multi-hydrate e2e
  subtest (w1/sc2/sc3/sc4).
- **Bug F:** `--only-runtime` → `extract content (model): artifact/ 404`.
  Pre-existing; the e2e does NOT use `--only-runtime`, so OUT OF SCOPE for
  the gate.
- **e2e rewrite** (`test/e2e/cli_hydrate_engine_test.go` + helpers): new paths
  (`.claude/settings.json` etc.), surgical-preserve assertions, per-key drift
  (sc3), per-key autoclaim (sc4), `phase7StatePath` (workspace mode = no env
  segment). ~20 subtests.
- Global-scope opencode path remap (`~/.config/opencode/...`) + `commit.go:~512`
  Sync target resolution (`achDir/..`) for `--global` consistency.
- Then: build-e2e → full gate green → qa-security → push (pre-push) → PR.

## Research references (live-fetched 2026-05-30)

- Claude Code: code.claude.com/docs/en/{mcp,settings,managed-mcp}
- Codex: developers.openai.com/codex/{mcp,config-reference}; openai/codex mcp_edit.rs
- OpenCode: opencode.ai/docs/{config,mcp-servers}; opencode.ai/config.json
- Gemini CLI: google-gemini/gemini-cli docs/{tools/mcp-server,cli/settings}
- ccplugin reference: /home/coder/workspace/local/ccplugin (merge_mcp_config: deepcopy
  existing → upsert by name → atomic write; ownership tracked in installed.json)
