# Plan: `.ach/` runtime mirror + per-environment namespacing

Status: DRAFT — awaiting confirmation. Do NOT execute without explicit approval.

Two related changes to the `ach-cli hydrate` engine's on-disk model, requested
2026-06-02:

1. **Runtime mirror** — `.ach/` must hold mcp / a2a / models (today only
   plugin/prompt/artifact land there; runtime lives only in adapter config,
   models nowhere). Write `.ach/runtime/{mcp,a2a,model}.json` snapshots AND
   populate the (currently unused) `state.File.RuntimeFiles`.
2. **Per-env namespacing** — project scope becomes `<cwd>/.ach/<environment>/`
   (today flat `<cwd>/.ach/`, one env per project, guarded). Mirrors the
   existing `--global` layout (`$HOME/.ach/<env>/`).

## Background (verified in code)

- `state.File` already has `RuntimeFiles []FileEntry` (`json:"runtimeFiles,omitempty"`) — schema-ready, never populated.
- mcp/a2a ARE tracked today as `adapter.files[].keys` (e.g. `mcpServers.demo-mcp-jwt`) for surgical uninstall. **models are tracked nowhere.**
- Runtime entries carry `{id, endpoint}`, not `/content/` tarballs — not extractable (the just-landed `isExtractableContent` gate). The mirror is therefore a *serialized snapshot of `m.Runtime`*, not a fetch.
- `state.ResolvePath(workspaceCwd, environment, global)`: project branch ignores `environment`; global branch requires it (`$HOME/.ach/<env>/`).
- **Chicken-and-egg:** `statePath` is resolved in `newCommit` from `opts.Environment` BEFORE the manifest is fetched (state read = step 3, manifest = step 5). `pk_` requires `--environment`; `ek_` makes it optional (server resolves). So per-env project namespacing needs the env name up front.

## Hard constraint to call out (not solved by either change)

Agents read a FIXED config path (`.claude/settings.json`, `.claude/skills/`,
`.codex/config.toml`, …). Per-env `.ach/` isolates the *cache + state*, but the
**adapter-native projection is still single-path** — two environments hydrated
into one project still merge/collide in the same `.claude/`. True two-env
isolation in one project is impossible at the projection layer without the tools
supporting per-env config dirs. The env guard exists precisely to prevent that
silent merge. **Per-env `.ach/` should therefore be paired with a clear policy
on the native-config collision** (see Decision D2).

## Decisions needed (resolve before/at execution)

- **D1 — ek_ + env name for path.** To namespace the project dir we need the env
  before manifest fetch. Options:
  - (a) **Require `--environment` in project scope** when a credential is `ek_`
    too (today optional). Simplest, explicit. Breaks the "ek_ infers env"
    convenience.
  - (b) **Reorder**: fetch manifest first to learn `manifest.Environment`, then
    resolve the dir. Cleaner UX, but moves state read after manifest — changes
    the §6.7 14-step sequence + the SIGKILL-seam step indices (sc2 e2e).
  - Recommendation: (a) for this pass; (b) as a follow-up if desired.
- **D2 — native-config collision policy** when a 2nd env is hydrated into a
  project that already has `.claude/` from env A:
  - (a) keep the env guard (refuse 2nd env, exit 4) EVEN with per-env `.ach/`
    (namespacing is then only about clean re-use across `--force`/switch), or
  - (b) allow it and document that `.claude/` reflects the last-hydrated env.
  - Recommendation: (a) — namespacing fixes cache hygiene; guard still protects
    the single-path config.
- **D3 — migration of existing flat `.ach/`.** A repo with today's
  `<cwd>/.ach/state.json` must not break. On first namespaced run, detect a
  legacy flat state and either migrate it into `.ach/<env>/` or treat as fresh.
- **D4 — runtime mirror format.** `.ach/runtime/mcp.json` etc. = verbatim
  `m.Runtime.<bucket>` (id+endpoint, NO credential — the credential lives only
  in the adapter config, never in the cache, per OBS-02). Confirm no secret
  lands in `.ach`.

## Work breakdown

### Phase A — runtime mirror (additive, lower risk)
1. New commit step (after manifest, gated like context by scope): serialize
   `m.Runtime.{MCPServers,A2AAgents,Models}` → `<achDir>/runtime/{mcp,a2a,model}.json`
   (credential-free), atomic write via existing `state.WriteAtomic`.
2. Populate `state.RuntimeFiles` with one `FileEntry` per snapshot (Target +
   Hash) so drift + `--sync` uninstall cover runtime. Wire into
   `step12WriteState` + `step4ReconcileVsDisk` (prune-missing) + `--sync`.
3. Unit tests: snapshot written, hashed, recorded; no credential in bytes;
   re-hydrate is a no-op (stable hash); `--sync` removes a dropped runtime entry.
4. e2e: extend `TestPhase7AllPlatformsProjection` to assert `.ach/runtime/*.json`
   exist + are credential-free.

### Phase B — per-env namespacing (breaking layout; higher risk)
1. `state.ResolvePath` project branch → `<cwd>/.ach/<environment>/state.json`
   (+ require non-empty env per D1). Update `ErrInvalidPath` messaging.
2. Apply D1 in `cmd/ach-cli/cmd/hydrate.go` + `uninstall.go` (both resolve
   paths). Guard `--environment` presence in project scope.
3. D3 migration: detect legacy `<cwd>/.ach/state.json`; migrate or fresh.
4. Update the env guard rationale (still applies; D2).
5. Sweep call sites + tests: phase6/phase7 helpers (`phase7StatePath`,
   `phase7AchTmpDir`), `cli_hydrate_allplatforms_test.go`, examples,
   `references/*`, CLAUDE.md "Common failure modes" + spec §8.1/§8.3.
6. e2e: hydrate TWO envs into one project, assert `.ach/<envA>/` + `.ach/<envB>/`
   coexist; assert the D2 native-config policy.

### Docs (same commit per hygiene)
- CLI spec §8.1/§8.3, `references/` layout, examples README, CLAUDE.md.

## Risk / blast radius

- Phase A: contained, additive. Safe to ship alone.
- Phase B: changes a documented on-disk contract (spec §8.1) + the SIGKILL step
  indices if D1(b) chosen + many tests/docs. Recommend landing Phase A first,
  Phase B as its own reviewed change.

## Recommendation

Land **Phase A** now (clean win, low risk). Treat **Phase B** as a separate
change after D1–D3 are decided — it is a breaking layout change with a real
native-config caveat (the hard constraint above) that deserves its own review.
