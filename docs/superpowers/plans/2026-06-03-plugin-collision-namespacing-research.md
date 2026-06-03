# Cross-plugin resource collision — namespacing research

> Status: **RESEARCH / decision pending.** Captured 2026-06-03 from a live
> hydrate of env `platform` (deployed 0.2.7) that fail-fasted on two
> plugins (`cicd-automation@wshobson-agents`, `cloud-infrastructure@wshobson-agents`)
> both projecting an agent `cloud-architect` → `.claude/agents/cloud-architect.md`.
> This is NOT an env-definition bug — it is structural and WILL recur
> (e.g. an `optimize` skill shipped by many plugins). No code written yet.

## The problem (verified in code)

ACH hydrate uses **D-01 flat kind-routing**. Plugins extract to
`<achDir>/plugin/<plugin-name>/`, then `route.Project(rules, pluginSrc, "")`
remaps each resource by its **first path segment (kind)** into the
adapter's per-kind dir — **dropping the plugin name from the destination**.

- Drop site: `internal/cli/hydrate/wiring.go:464-467` — `Project` is called
  with `pluginSrc` as the walk root, so `filepath.Rel` strips the
  plugin-name segment before classification (`route.go:285`); destination
  derives only from the rule's `ToGlob` + kind-relative suffix.
- Collision handling today: `wiring.go:484-489` — post-remap `claimed[]`
  map detects two plugins landing on the same `Target` and **fail-fasts**
  (CR-01). Composite targets (`CLAUDE.md`/`GEMINI.md`) are exempt
  (co-owned via per-id markers); MergeDeep MCP configs are single shared
  files merged by server-name.

Net: two plugins with a same-named agent/command/skill collide and abort
the whole hydrate.

## Per-adapter routing today (from `ProjectionRules()`)

| kind | claude-code | codex | gemini | opencode | pimono |
|---|---|---|---|---|---|
| commands | `.claude/commands/<name>` | `.codex/prompts/<name>.md` | `.gemini/commands/<name>` | `.opencode/commands/<name>` | `.pi/agent/prompts/<name>` |
| agents | `.claude/agents/<name>` | `.codex/agents/<name>.toml` | `.gemini/agents/<name>` | `.opencode/agents/<name>.md` | dropped |
| skills | `.claude/skills/<name>` | `.agents/skills/<name>` | `.gemini/skills/<name>` | `.opencode/skills/<name>` | `.pi/agent/skills/<name>` |
| mcp | `.claude/settings.json` (deep) | `.codex/config.toml` (deep) | `.gemini/settings.json` (deep) | `.opencode/opencode.json` (deep) | `.pi/mcp.json` (deep) |

All file kinds are flat `MergeReplace` (no plugin segment). `claude-code`
has a DEAD `.claude/plugins/<name>/` umbrella path (`TransformPlugin`,
claudecode.go:251-302) that the live projection leg does NOT use.

## How other tools / managers solve it

### Claude Code (native) — per-plugin umbrella + `plugin:name` namespacing
- Installed plugins are copied WHOLE into `~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/` — never flattened into shared `.claude/agents/`.
- Every component is invoked **namespaced**: `/plugin:command`, skills `/plugin:skill`, agents disambiguated by plugin. Two plugins shipping `cloud-architect` do NOT collide.
- Skills are discovered **one level deep** (`.claude/skills/<name>/SKILL.md`) — a per-plugin SUBDIR would hide them.
- Refs: code.claude.com/docs/en/plugins, …/plugins-reference, …/plugin-marketplaces.

### ccplugin (our sibling, `/home/coder/workspace/local/ccplugin`) — path-segment prefix
`paths.py:213-223`:
- commands & agents → nested dir: `<commands|agents>/<plugin>/<name>.md`
- skills → colon-prefixed flat dir: `skills/<plugin>:<name>/SKILL.md` (colon, BECAUSE Claude discovers skills one level deep + matches the `repo:skill` slash convention)
- MCP → NOT namespaced (last-wins, known residual collision).

### OpenPackage (enulus `opkg`) — lockfile ownership + leaf-prefix
- Tracks a **workspace index (lockfile)**: path → owning package + version + content hash. Powers clean uninstall, idempotent re-install, and "owned-by-other vs hand-edited" detection.
- On collision, namespaces by **leaf prefix** `slug-<name>` (NOT a subdir) — deliberately, "so flat-name discovery (Claude Code's leaf-name identity) can distinguish resources." For `SKILL.md`-marker resources it prefixes the parent dir.
- Content-hash **dedup**: byte-identical resources from two packages collapse to one file, no prefix, no prompt. `shouldSkipPrefix` avoids `code-review-code-review`.
- `--conflict=namespace|skip|overwrite` + per-path overrides. Default for owned-by-other = `namespace` (no prompt).
- Refs: github.com/enulus/OpenPackage, openpackage.dev/docs; source `install/conflicts/{namespace-path.ts,file-conflict-resolver.ts}`, `workspace-index-yml.ts`.

### Flat-target tools' own behavior
- **Codex**: skills in flat `~/.agents/skills/`, identity by leaf name; scope-ordered **first-match-wins**, no namespace.
- **Gemini CLI**: subdir → `:`-namespaced command (`commands/git/commit.toml` → `/git:commit`); colliding extension command **auto-prefixed** with extension name; user/project beats extension.
- **OpenCode**: layered precedence (project > global > remote), first-file-wins per category; no per-plugin namespace.

### Prior-art strategy table
| Strategy | Tools |
|---|---|
| (a) per-source subdir | Homebrew taps, Krew custom indexes, ccplugin |
| (b) name prefix/suffix | OpenPackage (`slug-`), Gemini (ext prefix) |
| (c) first-wins + warn | kubectl/Krew PATH plugins, Codex scope order, oh-my-zsh |
| (d) last-wins / shadow | asdf shims (footgun), oh-my-zsh re-def |
| (e) hard refuse | Homebrew `conflicts_with`, Krew (one at a time), **ACH today** |
| (f) explicit precedence | Nix `mkForce`/priority, Gemini, OpenCode layers |

## The core tension (decision driver)

**Subdir vs leaf-prefix is not free** — it depends on how each TARGET
discovers resources:
- Subdir (`agents/<plugin>/<name>.md`) is clean BUT breaks tools that key
  resource identity on the **leaf name** or scan **one level deep**
  (Claude skills; Codex skill discovery). ccplugin had to special-case
  skills with a colon-prefix for exactly this reason.
- Leaf-prefix (`agents/<plugin>-<name>.md`) keeps a flat dir + unique leaf
  → works everywhere identity is the leaf, at the cost of uglier names and
  losing the tool's native `plugin:name` ergonomics.
- Native umbrella (full plugin install w/ `plugin.json`) is the BEST for
  claude-code (real `plugin:name`) but is a much bigger change and has no
  analog in codex/gemini/opencode.

→ **There is likely no single scheme for all 5 adapters.** Each adapter
should namespace the way ITS target natively disambiguates.

## Options

**O1 — Per-adapter native namespacing (recommended direction).** Each
adapter picks the scheme its target supports: claude-code → leaf-prefix
for agents/commands + colon-dir for skills (ccplugin-proven), or full
plugin-umbrella install; gemini → subdir (native `:` namespace); codex /
opencode → leaf-prefix. Inject at `wiring.go:471` (rewrite `fw.Path`
per-plugin) OR thread plugin name into `route.Project`. MCP stays
server-name-merged (namespace the keys, not the path).

**O2 — Uniform leaf-prefix everywhere (OpenPackage model).** `<plugin>-<name>`
for all file kinds, all adapters. Simplest, deterministic, order-independent,
works in every flat dir. Add content-hash dedup + `shouldSkipPrefix`. Loses
native `plugin:command` ergonomics on tools that have it.

**O3 — Uniform per-plugin subdir.** `<kind>/<plugin>/<name>`. Clean, but
KNOWN to break Claude skills (one-level discovery) and Codex leaf-identity
→ rejected as a uniform default.

**O4 — Tiebreak-and-warn (no namespace).** First-wins (alphabetical) +
hydration warning listing shadowed resources. Cheapest; but silently drops
a resource (the asdf footgun) — acceptable only as an opt-in `--conflict`
mode, not the default.

**O5 — Platform-side rename ("dumb CLI").** Operator renames colliding
resources in the manifest before the CLI sees them. Keeps CLI simple but
moves naming policy server-side and still needs a scheme + loses per-target
nuance (the right scheme differs per adapter, which the platform doesn't
know). User leaned AGAINST this (wants it CLI-side).

## Cross-cutting concerns (apply to whichever option)

1. **Ownership lockfile.** `state.Plugins[]` already records each projected
   `Target`; extend it to record the OWNER plugin so re-hydrate, uninstall,
   and "owned-by-other vs hand-edited" are decidable (OpenPackage's single
   most important piece). `findPluginEntry` keys on exact `Target`.
2. **Content-hash dedup.** Two plugins shipping byte-identical resources
   (very common for vendored skills) should collapse to one file, no prefix
   — avoids needless `<plugin>-optimize` noise.
3. **state.json migration.** ANY namespace change rewrites every projected
   `Target` string → on first re-hydrate, old rows orphan and old files are
   left on disk unless a migration sweeps them (`wiring.go` `findPluginEntry`
   / FMT-05 no-op both key on the prior Target). Needs a state v2→v3
   migration or a one-time reconcile.
4. **MCP & composite stay special.** MergeDeep MCP configs and
   MergeComposite `CLAUDE.md`/`GEMINI.md` are already co-owned; namespace
   their KEYS/markers, never their path.
5. **Skills will hit this hardest** — generic skills (`optimize`,
   `code-review`) ship in many plugins; the skill scheme matters most.

## Round-2 findings (decisive)

**openPackage (closest prior art) uniformly leaf-prefixes — even Claude Code.**
`namespace-path.ts`: prepend `<packageName>-` to the leaf (`rules/foo.md` →
`rules/acme-foo.md`); for `SKILL.md`-marker resources prefix the PARENT DIR
(`commands/review/SKILL.md` → `commands/pkg-a-review/SKILL.md`).
`shouldSkipPrefix` skips when leaf == slug (no `code-review-code-review`).
**Conflict-triggered** (only `owned-by-other` / `exists-unowned`) → the
first/only installer keeps the bare name. Per `platforms.jsonc` the `claude`
install target writes flat to `.claude/{agents,skills,rules,commands}` +
leaf-prefix on conflict; it does NOT use a native `.claude/plugins/<name>/`.
`claude-plugin` is a SEPARATE opt-in target that only *emits* a plugin
artifact. → The most-similar tool deliberately chose flat + package-leaf-prefix
uniformly, working around Claude's leaf-name identity rather than native
plugin packaging.

**Claude-code native namespacing IS achievable by pure file-write — caveated.**
"Skills-directory plugin": write `<project>/.claude/skills/<plugin>/.claude-plugin/plugin.json`
+ `agents/ skills/ commands/` at root → Claude loads it as `<plugin>@skills-dir`
with real `/<plugin>:<name>`, NO marketplace/install/global mutation. BUT:
(1) one-time workspace **trust** dialog; (2) **launch-dir-sensitive** — loads
only from `.claude/skills/` of the EXACT cwd Claude starts in, doesn't walk to
repo root (if ACH workspace root ≠ launch dir → missed; mitigate hydrate-into-
launch-dir or `/reload-plugins`); (3) project-scope background monitors don't
load, MCP/LSP need approval (skills/agents/commands/hooks fine). This launch-dir
+ trust fragility is why openPackage avoided native packaging.

## Recommendation (refined)

Adopt the **openPackage model**, conflict-triggered:

1. **Default = leaf-prefix `<plugin>-<name>` on collision**, uniform across all
   flat adapters (claude-code agents/commands, codex, gemini, opencode, pimono).
   Skills (`SKILL.md` marker) → prefix the parent DIR. Bare name when no
   collision; `shouldSkipPrefix` when leaf already == plugin.
2. **`--conflict=namespace|skip|overwrite|refuse`**, default **`namespace`**
   (✅ user-approved). `refuse` = today's fail-fast. Plus content-hash **dedup**
   (byte-identical resources collapse, no prefix).
3. **Ownership tracking** in `state.Plugins[]` (owner plugin per Target) →
   classify owned-by-other vs hand-edited, clean uninstall, idempotent
   re-hydrate. Triggers a **state v2→v3 migration**.
4. **MCP / composite unchanged** — namespace keys/markers, never the path.
5. **Claude-code native plugin (skills-dir) = OPTIONAL later mode** behind a
   flag, giving true `plugin:name` — deferred for the launch-dir + trust
   fragility. Leaf-prefix is the robust default; native is an opt-in upgrade.

Injection point: rewrite `fw.Path` per-plugin at `wiring.go:471` (alongside the
`claimed[]` ownership check), guarded by the `--conflict` policy.

## Phase 1 status — ✅ SHIPPED (2026-06-03, v0.2.8)

Implemented on branch `feat/hydrate-projection-namespacing`: the `--conflict`
flag (decision 1), uniform `<plugin>-<name>` leaf-prefix with skill-dir
prefixing (decisions 2 + 4), and the two-pass collision resolver in
`projectPlugins`. **Deliberate deviation from decision 5:** Phase 1 prefixes
*all* colliding writes (including the first), NOT "1st bare, 2nd prefixed" —
this buys run-to-run determinism by sorting plugin names, WITHOUT the Phase-2
ownership lockfile. Decisions 3 (lockfile + content-hash dedup, state v2→v3)
and the `--claude-plugins` native skills-dir mode remain **Phase 2 / Phase 3**.

## Decisions (LOCKED 2026-06-03)

1. ✅ **`--conflict=namespace|skip|overwrite|refuse`**, default **`namespace`**.
   `refuse` kept as the 4th value (= today's fail-fast).
2. ✅ **Claude-code = uniform leaf-prefix default**; native skills-dir
   `plugin:name` deferred to an opt-in `--claude-plugins` mode (launch-dir +
   trust fragility). All other adapters: leaf-prefix too.

## Recommended defaults for the remaining knobs (override if desired)

3. **Ownership lockfile + content-hash dedup — build NOW.** Recommend yes:
   conflict-triggered namespacing NEEDS owner info to tell "owned-by-other"
   from "hand-edited", and it's what makes uninstall + idempotent re-hydrate
   correct. Extend `state.Plugins[]` with owner; state v2→v3 migration.
4. **Prefix shape — `<plugin>-<name>` uniform** (openPackage), EXCEPT skills,
   where the marker forces prefixing the parent dir; use `<plugin>-<skill>/SKILL.md`.
   (Colon `<plugin>:<skill>` is the ccplugin/Claude-convention alternative —
   decide at plan time; `-` is filesystem-safest and uniform.)
5. **Trigger — conflict-triggered** (openPackage): bare name until a 2nd plugin
   collides. Note the order-dependence (1st stays bare, 2nd prefixed); the
   ownership lockfile makes it deterministic across re-runs.

## Next step

Decisions are sufficient to write a TDD implementation plan
(`superpowers:writing-plans`): per-adapter `fw.Path` rewrite at `wiring.go:471`,
the `--conflict` flag + policy enum, `state.Plugins[]` owner field + v2→v3
migration, content-hash dedup, and the deferred `--claude-plugins` native mode
as a follow-on phase. NOT to be executed without explicit confirmation.

## Related follow-up (separate, flagged 2026-06-03)

`#99` (pk-/ek- key format) left **user-facing `pk_`/`ek_` copy drift**
across CLI help/flags (logout, admin, whoami, env, config, hydrate). The
verbose §8.6 hydrate `pkWarning` was already trimmed + updated to `pk-`/`ek-`
(commit pending). A full copy sweep `pk_`→`pk-` / `ek_`→`ek-` across
user-facing strings is its own small task.
