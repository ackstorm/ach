# Phase 2.2 — `plugin`/`skill` install/uninstall/update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** The second half of the ccplugin-style local package manager: `ach-cli plugin install|uninstall|update|list` and `ach-cli skill install|uninstall|update|list` — actually project plugins/skills from registered repos into the per-tool config dirs (`.claude/`, `.opencode/`, …), tracked in `installed.json`.

**Architecture:** A new `internal/cli/localpkg/manager` orchestrates: resolve `name@repo` (+lens) against `repos.json` → `gitfetch` clone → resolve the named resource (marketplace entry sub-source / direct / skill slice via `contentkit`) → `extract` to a staging dir → `route.Project` through the requested adapter(s) → write `FileWrite`s to the target root (project cwd or `--global` home) honoring `MergeKind` → record `installed.json`. The deep-merge/composite write logic is extracted from `internal/cli/hydrate` into a shared k8s-free `internal/cli/merge` package (Task 1). New `cmd/ach-cli/cmd/{plugin,skill}.go` wire the commands. `ach-cli` stays k8s-free.

**Tech Stack:** Go (`github.com/ackstorm/ach`). Toolchain: `make`/`./scripts/dev.sh` (docker direct) or host Go `/usr/local/go/bin/go`. SPDX every file. TDD. Conventional commits.

**Boundary invariant (assert every task):** `go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|controller-runtime'` empty.

---

## Reused (verified) APIs
- `extract.StageAndPublish(ctx, body io.Reader, contentType, finalAbs, achDir string, kind extract.ResourceKind, limits extract.Limits, allowSymlinks bool) (extract.PublishResult, error)` — stages+extracts+publishes; `extract.DefaultLimits()`, `extract.KindPlugin`/`KindSkill`. For projection we instead extract to a plain staging dir then run route.Project (see Task 2).
- `route.Project(rules []route.Rule, src, source string) (route.ProjectResult, error)`; `ProjectResult.FileWrites []adapter.FileWrite`; type-assert `adapter.Lookup(id)` result to `route.RuleProvider` → `ProjectionRules()`.
- `adapter.Lookup(id string) (adapter.Adapter, bool)` (case-folds + alias-resolves). Canonical IDs: `claude-code`, `codex`, `gemini-cli`, `opencode`, `pimono`. `--target` user values map: claude→claude-code, codex→codex, gemini→gemini-cli, opencode→opencode.
- `adapter.FileWrite{Path, Content, SourceHash, Merge adapter.MergeKind, Keys}`; `adapter.MergeDeep/MergeComposite/MergeReplace`.
- `gitfetch.New(Spec).Fetch`, `gitfetch.LsRemote`; `contentkit.ParseClaudeCodeMarketplace/DiscoverSkillsInTree/SliceSkillSubtree/VerifyPluginContents/VerifySkillContents`.
- global/project root pattern (hydrate `commit.go`): `toolRoot = cwd` (project) or `$HOME` (`--global`); opencode remap `.opencode/` → `.config/opencode/` in global scope (`remapGlobalPath`).

---

## Task 1: Extract `internal/cli/merge` (shared doc-merge/composite write)

Move the pure merge helpers out of `internal/cli/hydrate/wiring.go` into a new k8s-free `internal/cli/merge` package; repoint hydrate. Behavior-preserving (touches hydrate → cli-hydrate e2e re-run required).

**Files:** Create `internal/cli/merge/merge.go` (+`_test.go`); modify `internal/cli/hydrate/wiring.go` (+ tests).

**Move + export** (currently unexported in wiring.go ~lines 1314-1441, 1476-1530): `deepMergeInto`, `parseDoc`, `encodeDoc`, `mergeForwardDoc`(→`MergeDoc`), `getDottedKey`, `setDottedKey`, and the composite writer `writeComposite`(→`WriteComposite`)/`compositeBlock` helpers. Proposed public API:
```go
package merge
// MergeDoc deep-merges `ours` into the doc at `abs` (created if absent), JSON or TOML by extension.
func MergeDoc(abs string, ours []byte, mode os.FileMode, isTOML bool) ([]byte, error)
// DeepMergeInto recursively merges src into dst (exported for callers needing in-memory merge).
func DeepMergeInto(dst, src map[string]any)
func ParseDoc(content []byte, isTOML bool) (map[string]any, error)
func EncodeDoc(m map[string]any, isTOML bool) ([]byte, error)
func GetDottedKey(root map[string]any, path string) (any, bool)
func SetDottedKey(root map[string]any, path string, val any)
// WriteComposite writes a marker-bounded (<!-- ach:begin/end <id> -->) block into the file at abs.
func WriteComposite(abs, id string, block []byte, mode os.FileMode) error
```
Keep `internal/cli/hydrate/wiring.go` calling `merge.MergeDoc`/`merge.WriteComposite` etc. (thin repoint). Confirm `internal/cli/merge` deps are stdlib only (`encoding/json`, `BurntSushi/toml` for TOML, `bytes`, `os`, `fmt`, `strings`) — NO k8s.

- [ ] Step 1: `git mv`-style move the functions (cut from wiring.go, paste into merge.go), export them, add SPDX + doc comments.
- [ ] Step 2: Repoint every call site in `internal/cli/hydrate/` to `merge.X`; add the import.
- [ ] Step 3: Move the unit tests for these helpers (if any in wiring_test.go) into `merge_test.go`; otherwise write fresh table tests for `MergeDoc` (json deep-merge preserves sibling keys; toml; composite round-trip; dotted get/set).
- [ ] Step 4: `./scripts/dev.sh go build ./...`; `make test-unit-pkg PKG=./internal/cli/merge/...` + `PKG=./internal/cli/hydrate/...` → PASS.
- [ ] Step 5: k8s-free: `go list -deps ./internal/cli/merge | grep -E 'k8s.io/api|controller-runtime' || echo CLEAN`.
- [ ] Step 6: Commit `refactor(cli): extract doc-merge/composite helpers to internal/cli/merge`.

---

## Task 2: `internal/cli/localpkg/manager` — resolve + fetch + project (compute half)

**Files:** Create `internal/cli/localpkg/manager/{manager.go, source_spec.go}` (+ `_test.go`).

**Client-side marketplace sub-source mapping** (`source_spec.go`, reimplements `buildGitSpecForEntry` WITHOUT k8s):
```go
// BuildEntrySpec maps a contentkit.ClaudeCodeMarketplaceSource to a gitfetch.Spec.
// marketplaceCloneURL/marketplaceRef identify the marketplace's OWN repo (for local-path entries).
func BuildEntrySpec(src contentkit.ClaudeCodeMarketplaceSource, marketplaceCloneURL, marketplaceRef, token string, scheme gitfetch.AuthScheme) (gitfetch.Spec, error)
```
Mapping (verbatim from the operator, k8s-stripped): `git-subdir`/`url` → `Spec{URL:src.URL, Ref:defaultRef(src.Ref), SHA:src.SHA, Subtree:src.Path}`; `github` → `Spec{URL:"https://github.com/"+src.Repo+".git", Ref:defaultRef(src.Ref), SHA:src.SHA, Subtree:""}`; `local-path` → `Spec{URL:marketplaceCloneURL, Ref:marketplaceRef, Subtree:src.Path}`; `""` → error. Token/scheme: pass the repo's token only for the repo's own host (the CLI registers one repo at a time, so reuse the repo's token+scheme for same-host entries, anonymous for foreign hosts — mirror `tokenForHost`).

**Resolve+project** (`manager.go`):
```go
type ResolveResult struct {
	Name, Kind, ResolvedSHA string // Kind: "plugin"|"skill"
	StageDir   string              // extracted tree on disk (caller cleans up)
}
// Resolve clones repo, locates the named resource via the lens, extracts to a temp stage dir.
func Resolve(ctx context.Context, repo store.RepoEntry, token, name, lens string) (ResolveResult, error)

type PlannedWrite struct{ Path string; Content []byte; Merge adapter.MergeKind; Keys []string }
// Project runs route.Project for one adapter over the staged tree.
func Project(stageDir, adapterID string) ([]PlannedWrite, error)
```
`Resolve` lens handling: `plugin-marketplace` → fetch repo, `ParseClaudeCodeMarketplace`, find entry `Name==name`, `BuildEntrySpec`→fetch entry tar→stage; `plugin`(direct) → stage the repo tar (Verify first); `skill-marketplace` → `DiscoverSkillsInTree`→`SliceSkillSubtree(name)`→stage; `skill`(direct) → stage repo tar. Gate via `VerifyPluginContents`/`VerifySkillContents` before staging. Stage = extract gz-tar into a `t`-style temp dir (use `os.MkdirTemp` + a gz-tar extractor; reuse `extract.Extract(ctx, gzReader, dst, kind, extract.DefaultLimits(), false)` with `dst` a fresh empty temp dir).

- [ ] Step 1: Failing tests: `BuildEntrySpec` table (4 kinds → expected Spec); `Resolve`+`Project` against LOCAL git fixtures (a marketplace repo with a `git-subdir` entry; a direct-skill repo) → assert StageDir contains the expected files and `Project("claude-code")` yields PlannedWrites with `.claude/...` paths (commands→MergeReplace, an mcp dir→MergeDeep). Read claudecode `ProjectionRules` to assert exact paths.
- [ ] Step 2-4: implement, run → PASS, k8s-free check.
- [ ] Step 5: Commit `feat(cli): add localpkg manager resolve+project`.

---

## Task 3: `manager` commit/write + uninstall + update

**Files:** add to `internal/cli/localpkg/manager/commit.go` (+ test).
```go
// Commit writes planned writes under root honoring MergeKind, returns the relative paths written.
func Commit(root string, global bool, adapterID string, writes []PlannedWrite) ([]store.FileRec, error)
// Uninstall removes recorded files (hash-verified; skip+warn on user-edit drift), prunes empty dirs.
func Uninstall(root string, files []store.FileRec) error
```
`Commit`: for each write, compute abs `filepath.Join(root, remapGlobalPath(adapterID, write.Path))` (apply the opencode global remap when `global`); `MkdirAll` parent; dispatch on Merge: `MergeReplace`→`state.WriteAtomic(abs, content, 0o644)`; `MergeDeep`→`merge.MergeDoc(abs, content, 0o644, isTOML(abs))`; `MergeComposite`→`merge.WriteComposite(abs, id, content, 0o644)`. Record `store.FileRec{RelPath, Hash:xxh3(content)}` (reuse `internal/cli/hash`). `Uninstall`: for each FileRec, if file hash matches → remove; else warn "user-modified, skipping"; prune now-empty dirs.

- [ ] Steps: failing test (commit writes files under a temp root incl. a MergeDeep settings.json that preserves a pre-existing sibling key; uninstall removes them; hash-drift file is preserved) → implement → PASS → commit `feat(cli): add localpkg commit/uninstall`.

---

## Task 4: `cmd/ach-cli/cmd/{plugin,skill}.go`

Parent `plugin` + children `install/uninstall/update/list`; same for `skill` (lens differs). Wire `manager`. `--target` (repeatable or comma list) → adapter IDs; `--global`; ref-suffix `name@repo` MANDATORY.
- `plugin install <name@repo>... --target claude[,opencode] [--global]`: split `name@repo`; load repo; require `plugin`/`plugin-marketplace` lens in `repo.Provides`; token from store; `Resolve`→`Project` per target→`Commit`; record `store.InstalledEntry` per (name@repo, target). Print per-resource `✓` line.
- `plugin list [--repo]`: list plugins across repos with installed status (cross-ref `installed.json`).
- `plugin uninstall <name@repo>... [--target]`: load installed entries; `Uninstall` recorded files; drop entries.
- `plugin update [<name@repo>...]`: re-resolve sha; no-op if unchanged; else uninstall-old-files + reinstall.
- `skill ...`: identical with `skill`/`skill-marketplace` lens + `extract.KindSkill`.

- [ ] Steps: failing cobra tests (local fixture repo registered via `repo add` flow or seeded `repos.json`; `plugin install x@fix --target claude` writes `.claude/...` under a temp cwd; `installed.json` records files; `plugin uninstall` removes them) → implement (mirror `repo.go` conventions, `init(){ rootCmd.AddCommand(newPluginCmd(), newSkillCmd()) }`) → PASS → commit `feat(cli): add 'ach-cli plugin' and 'skill' commands`.

---

## Task 5: Gate
- [ ] `make test-unit` → PASS. `make qa-lint` → PASS (mind `lll`/`errcheck`: `_, _ = fmt.Fprintf`, break >120-char lines).
- [ ] `go list -deps ./cmd/ach-cli` k8s-free.
- [ ] **e2e:** because Task 1 touched `internal/cli/hydrate`, run `make e2e-full` (clean cluster) — expect green except the known `Phase6CLI/hydrate_golden_diff` stale golden; **confirm no NEW failures** (esp. Phase6/7 which exercise hydrate's merge path).

---

## Self-review notes
- **Decision:** merge logic extracted to `internal/cli/merge` (DRY, shared with hydrate) rather than duplicated — costs one hydrate e2e re-run.
- **Marketplace sub-source** reimplemented client-side (k8s-free) since the operator's version is corev1-tainted.
- **Scope:** v1 `--target` covers claude-code/codex/gemini-cli/opencode. MergeDeep+Composite supported via `internal/cli/merge`. The `env` reorg is Phase 2.3 (separate).
