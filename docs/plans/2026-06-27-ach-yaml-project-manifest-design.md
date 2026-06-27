# `ach.yaml` Project Hydrate Manifest — Design

> Status: design (spec). Next step: `superpowers:writing-plans` → implementation plan.
> Date: 2026-06-27.

## Problem

A project that depends on one or more ACH Environments has no committed,
declarative record of *which* Environments (and which adapter targets) it
hydrates. Today `ach-cli env hydrate <name>` requires every developer to know
the Environment name(s) out-of-band and run hydrate per env by hand. A teammate
who clones the repo cannot just "hydrate and go".

## Goal

Add a committed, secret-free project manifest (`ach.yaml`) that declares the
list of Environments (with optional per-env targets) a project hydrates. A
teammate clones, runs bare `ach-cli env hydrate`, and the workspace is hydrated
— using **their own** credential and **their own** access (server-side authz is
unchanged). An explicit `ach-cli env save` command derives `ach.yaml` from the
realized hydrate state so the manifest is generated from truth, never
hand-maintained.

## Non-goals

- No secrets, credentials, or tokens in `ach.yaml`.
- No hub/platform-api URL binding — the manifest is **hub-agnostic** (see
  Decisions). Hub + credential resolve entirely from the running developer's
  active profile.
- No change to the hydrate engine's per-env state model, conflict policy, or
  governance/authz. This feature is a thin resolution/serialization layer
  *around* the existing engine.
- No global (`$HOME/.ach`) manifest. `ach.yaml` is strictly project-scoped
  (current working directory).
- Not `env diff` and not the planned CR-YAML GitOps `export` — those are
  separate, orthogonal features.

## Decisions (locked with user)

| # | Decision | Choice |
|---|----------|--------|
| A | Filename | `ach.yaml` (committed, at project root / cwd) |
| B | Cardinality | A **list** of environments, each with optional targets |
| C | Generator | Explicit command that **derives** the manifest from existing hydrate state (not auto-on-hydrate, not a hand-edited template) |
| D | Hub binding | **Hub-agnostic** — manifest carries env names only; hub resolves from the developer's active profile |
| E | Generator name | `ach-cli env save` |
| F | Multi-env failure | **Best-effort + report**: hydrate every listed env that can be hydrated, skip+report failures, exit non-zero if any failed |

## Naming collision note

`.ach/` is **already** the gitignored runtime cache/state directory the hydrate
engine owns (`.ach/<env>/state-<platform>.json`, `.ach/<env>/lock`, content
caches). The new manifest is a **flat file** `ach.yaml` at the project root — a
distinct name and a distinct role (committed *input*, not gitignored *output*).
We never reuse the bare `.ach` name for the manifest.

## `ach.yaml` schema

```yaml
# ach.yaml — committed. Declares which ACH Environments this project hydrates.
# Contains NO secrets. Each developer hydrates with their own credential and
# must have access to each Environment (server-side authz is unchanged).
version: 1
environments:
  - name: team-shared
    targets: [claude, codex]   # optional; omitted → autodetect from workspace
  - name: project-x            # targets omitted → autodetect
```

- `version` (int, required): schema version. v1 is the only version. An
  unknown/missing version is a hard parse error with a clear message.
- `environments` (list, required, non-empty): each entry:
  - `name` (string, required): Environment name. Must be non-empty.
  - `targets` (list of strings, optional): adapter target ids (e.g. `claude`,
    `codex`, `gemini`, `opencode`). Omitted → the consumer autodetects targets
    from the workspace (existing `hydrate.Autodetect`).
- Unknown top-level or per-entry keys → parse error (strict decode), so typos
  surface instead of silently no-op'ing.
- Duplicate `name` entries → parse error.

YAGNI: no hub URL, no per-entry `include-runtime`/`only-runtime`/conflict
policy, no profile pin. Global hydrate flags still apply to all manifest envs as
overrides (see Consumer precedence). If a per-env override need emerges later,
it is an additive v2 field, not a v1 requirement.

## Component 1 — `ach-cli env save` (generator)

**Responsibility:** derive `ach.yaml` from the realized hydrate state in the
current workspace. Pure read of `.ach/`, pure write of `ach.yaml`. No network.

**Behavior:**
1. Enumerate `<cwd>/.ach/*/` subdirectories (each is one hydrated env, mirrors
   `cmd/ach-cli/cmd/list.go`'s `achRoot` enumeration).
2. For each env dir, collect the set of `state-<platform>.json` files present →
   that env's `targets` (platform ids parsed from the filenames).
3. Build the `environments` list, **sorted** by env name and with targets sorted
   — output is deterministic (stable across runs / machines, diff-friendly).
4. Write `<cwd>/ach.yaml` (`version: 1` + the list).
5. Print a summary of what was written (envs + targets) and a reminder to commit
   `ach.yaml`.

**Edge cases:**
- No `.ach/` dir, or `.ach/` has no env subdirs (nothing hydrated yet) → friendly
  error, exit non-zero: "nothing hydrated in this workspace yet — run
  `ach-cli env hydrate <name>` first, then `ach-cli env save`." Do **not** write
  an empty manifest.
- An env dir exists but has no `state-<platform>.json` (e.g. only legacy flat
  `state.json`, or a half-cleaned dir) → include the env with an **empty/omitted
  targets** list (consumer will autodetect), and note it in the summary.
- `ach.yaml` already exists → regenerate and overwrite (the manifest is derived
  from truth; `save` is idempotent against current state). Print the summary so
  the developer sees the new content before committing. No `--force` needed.

## Component 2 — bare `ach-cli env hydrate` (consumer)

**Responsibility:** when invoked without a specific env, resolve the env list
from `ach.yaml` and drive the **existing** hydrate engine once per listed env.

**Env-source precedence (highest wins):**
1. Positional `<name>` argument → classic single-env mode. `ach.yaml` is ignored.
   (Back-compat: existing behavior is byte-for-byte unchanged when an arg is
   passed.)
2. `ACH_ENVIRONMENT` env var → classic single-env mode. `ach.yaml` ignored.
   (Back-compat / CI escape hatch, unchanged.)
3. `ach.yaml` present in cwd → **manifest mode**: hydrate every listed env.
4. None of the above → the current "name positional argument is required" error
   (back-compat).

**Target precedence per env (highest wins):**
1. `--target <ids>` flag (applies uniformly to all manifest envs).
2. The env entry's `targets` in `ach.yaml`.
3. Autodetect (`hydrate.Autodetect(cwd)`).

**Multi-env execution (best-effort):**
- For each `{env, targets}` in order, run the existing hydrate engine into
  `.ach/<env>/` (the engine's per-env state, lock, and conflict policy are
  unchanged — each env hydrates exactly as it would standalone).
- Collect a per-env result (OK / FAIL + reason). A failure (no access, env not
  found, conflict, lock timeout) is recorded and execution continues to the next
  env.
- After all envs: print a per-env summary table (`env → OK` / `env → FAIL:
  reason`). Exit non-zero if **any** env failed; exit zero only if all succeeded.
- Other global hydrate flags (`--include-runtime`, `--only-runtime`, `--sync`,
  `--conflict`, `--dry-run`, `--force`, `--allow-symlinks`, `--lock-timeout`,
  `--output`) apply uniformly to every manifest env.

**Collision across envs:** multiple envs projecting into the same adapter dir
(`.claude/`, `.mcp.json`, …) is handled by the **existing** conflict policy /
`resolvePluginCollisions` — no new collision logic. Document that two envs
defining the same content surface follow the configured `--conflict` behavior.

## Security & gitignore

- `ach.yaml` is committed. The hydrate-managed `.gitignore` block lists `.ach/` +
  written adapter dirs; it must **not** list `ach.yaml`. `ach.yaml` lives at the
  project root and the adapter-dir globs do not match it — verify with a test
  (write the managed block, assert `ach.yaml` is not ignored), no production
  change expected.
- `ach.yaml` carries env **names** only — no credentials, tokens, or hub URLs.
  Committing it leaks no secret and grants no access; a teammate still needs
  their own login and Environment access. State this in the file's header
  comment (emitted by `env save`) and in docs.

## Testing

**Unit:**
- Schema parse: valid manifest; missing/unknown `version`; empty
  `environments`; missing `name`; unknown keys (strict-decode error); duplicate
  names.
- `env save` derivation: from a synthesized `.ach/` tree (envs A, B with varying
  `state-<platform>.json` sets) → assert deterministic sorted output; nothing
  hydrated → friendly error, no file written; env dir with no platform state →
  env emitted with empty targets + noted.
- Consumer env-source precedence: positional > `ACH_ENVIRONMENT` > `ach.yaml` >
  error — each rung asserted, including "positional present ⇒ manifest ignored".
- Consumer target precedence: `--target` > entry targets > autodetect.
- Best-effort aggregation: mixed pass/fail set → correct per-env summary and
  non-zero exit; all-pass → zero exit.

**E2E (kind cluster):** round-trip that simulates a teammate clone —
`env hydrate <a>` + `env hydrate <b>` → `env save` (writes `ach.yaml`) → delete
`.ach/` (simulate fresh clone keeping only the committed `ach.yaml`) → bare
`env hydrate` → assert both envs re-hydrate and the workspace matches.

## Docs to update (same commit as implementation)

- `CLAUDE.md`: the `env` verb summary (add `save`; note bare `hydrate` reads
  `ach.yaml`).
- `examples/README.md` / the login + hydrate demo: show the `ach.yaml` clone-and-go
  flow.
- `references/troubleshooting.md`: "wrong hub → env names don't resolve" entry
  (the accepted cost of the hub-agnostic decision), with the fix (point your
  active profile at the right hub).

## Open follow-ups (out of scope here)

- Per-env override fields (`include-runtime`, conflict policy) as additive v2.
- Interaction with the planned `env diff` (could diff `ach.yaml`-declared set vs
  realized state).
- A `--save` convenience on `hydrate` itself (explicitly rejected for v1 in
  favor of the standalone `env save` verb).
