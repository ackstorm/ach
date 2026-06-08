# contentkit extraction (Phase 1B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the pure marketplace-parse / skill-discovery / content-verify / tar-safety logic out of the k8s-tainted `internal/controller/ach` package into a new k8s-free package `internal/contentkit`, so both the operator and the `ach-cli` binary can reuse it.

**Architecture:** Five files (`marketplace_parse.go`, `skillmarketplace_discover.go`, `marketplace_manifest.go`, `skill_verify.go`, `tar_safety.go`) are pure (no controller-runtime / CRD coupling) but trapped in `package ach`. Entanglement analysis confirms they form a **self-contained closure** — their only cross-file dependencies are *among themselves* (`skill_verify`→`tar_safety`, `marketplace_manifest`→`tar_safety`, `skillmarketplace_discover`→`skill_verify`), with **no back-reference to any symbol staying in `package ach`**. So they move together cleanly: rename `package ach`→`package contentkit`, repoint the `internal/sources` sentinels to the k8s-free `internal/sourceserr` (extracted in Phase 1A, already on main), export the symbols the operator still calls, and repoint those operator call-sites. Behavior-preserving.

**Tech Stack:** Go (`github.com/ackstorm/ach`); host has a usable Go at `/usr/local/go/bin/go`; the canonical toolchain is the devtools container via `./scripts/dev.sh` / `make` (docker works directly now). SPDX header required on every `*.go`. Conventional commits.

**This is a refactor, not new-feature work.** The existing test suites are the regression net. Verification = build + existing unit/envtest stay green + the k8s-free closure assertion (the real new invariant).

---

## File structure

| Path | Action |
|---|---|
| `internal/contentkit/marketplace_parse.go` (+`_test.go`) | Create (moved) — `ParseClaudeCodeMarketplace` + `ClaudeCodeMarketplace*` types |
| `internal/contentkit/skillmarketplace_discover.go` (+`_test.go`) | Create (moved) — `DiscoverSkillsInTree`, `SliceSkillSubtree`, `DiscoveredSkill` |
| `internal/contentkit/marketplace_manifest.go` (+`_test.go`) | Create (moved) — `VerifyPluginContents` |
| `internal/contentkit/skill_verify.go` (+`_test.go`) | Create (moved) — `VerifySkillContents`, `StageSkillBody`, `ValidateSkillName`, `SkillRawIngressCap` |
| `internal/contentkit/tar_safety.go` (+`_test.go`) | Create (moved) — internal helpers (`tarEntrySafe`, `cappedTarReader`, caps) stay package-private |
| `internal/controller/ach/{pluginmarketplace_controller,marketplace_dispatch,marketplace_filters,skillmarketplace_controller,external_ref_refresh}.go` (+ their tests) | Modify — `import internal/contentkit` + repoint symbols to `contentkit.*` |

---

## Exported-symbol map (the only symbols crossing the new package boundary)

Capitalize these (definition **and** every reference, intra- and inter-package). They are exactly the symbols with callers OUTSIDE the 5 files:

| Old (package-private) | New (exported `contentkit.*`) | External caller(s) |
|---|---|---|
| `parseClaudeCodeMarketplace` | `ParseClaudeCodeMarketplace` | pluginmarketplace_controller.go |
| `ClaudeCodeMarketplace` | `ClaudeCodeMarketplace` | pluginmarketplace_controller, envtest_test |
| `ClaudeCodeMarketplacePlugin` | `ClaudeCodeMarketplacePlugin` | controller, dispatch, filters, envtest_test |
| `ClaudeCodeMarketplaceSource` | `ClaudeCodeMarketplaceSource` | dispatch, envtest_test |
| `ClaudeCodeMarketplaceOwner` (field type of the above) | `ClaudeCodeMarketplaceOwner` | (transitively, via exported struct fields) |
| `truncateErrField` | `TruncateErrField` | marketplace_dispatch.go |
| `discoverSkillsInTree` | `DiscoverSkillsInTree` | skillmarketplace_controller.go |
| `sliceSkillSubtree` | `SliceSkillSubtree` | skillmarketplace_controller.go |
| `discoveredSkill` | `DiscoveredSkill` | skillmarketplace_controller.go |
| `verifyPluginContents` | `VerifyPluginContents` | pluginmarketplace_controller.go |
| `stageSkillBody` | `StageSkillBody` | external_ref_refresh.go |
| `verifySkillContents` | `VerifySkillContents` | skillmarketplace_controller.go |
| `skillRawIngressCap` | `SkillRawIngressCap` | skillmarketplace_controller.go |

**Stay package-private** (no external caller; only used within the 5 moved files): `tarEntrySafe`, `cappedTarReader`, `maxVerifyEntries`, `maxVerifyDecompressedBytes`, `countingReader`, `safeRelLexical`, `hasWindowsVolume`, `symlinkTargetSafe`, `parseSkillFrontmatter`, `validateSkillName`, `validatePluginName`, `validateLocalPath`, `skillMaxManifestBytes`, `isRecognizedRootFile`, `isRecognizedComponentDir`, `detectArchiveRoot`, `normRel`, and any other intra-file helpers. (Phase 2 will additionally export `validatePluginName`/`validateLocalPath` when `ach-cli` needs them — out of scope here.)

> When capitalizing, do the definition and ALL references together (intra-package refs in the moved files + the external caller sites). The build is the check: a missed reference fails to compile. Do NOT capitalize a struct *field* unless it is already exported.

---

## Task 1: Move the five files to `internal/contentkit` and repoint callers (atomic)

One commit, so the tree never stops building. **Files** as listed in the File-structure + exported-symbol-map tables above.

- [ ] **Step 1: Move the five files + their tests; rename the package**

```bash
cd /home/coder/workspace/local/ach
mkdir -p internal/contentkit
for f in marketplace_parse skillmarketplace_discover marketplace_manifest skill_verify tar_safety; do
  git mv internal/controller/ach/$f.go internal/contentkit/$f.go
  [ -f internal/controller/ach/${f}_test.go ] && git mv internal/controller/ach/${f}_test.go internal/contentkit/${f}_test.go
done
sed -i 's/^package ach$/package contentkit/' internal/contentkit/*.go
```

- [ ] **Step 2: Repoint the sentinel import at the k8s-free `sourceserr`**

Four of the moved files import `internal/sources` only for the fetch sentinels. Repoint them so `contentkit`'s closure is k8s-free (preserve the `// Result mirrors …` style comments — only `sources.ErrX` tokens + the import line change):

```bash
cd /home/coder/workspace/local/ach
sed -i \
  -e 's#"github.com/ackstorm/ach/internal/sources"#"github.com/ackstorm/ach/internal/sourceserr"#' \
  -e 's#\bsources\.Err#sourceserr.Err#g' \
  internal/contentkit/*.go
```
(If any moved file references a `sources.` symbol other than the `Err*` sentinels, the build in Step 5 will flag it — handle case-by-case; the entanglement analysis found only sentinels.)

- [ ] **Step 3: Export the boundary symbols inside `contentkit`**

For each row in the exported-symbol map, rename the definition AND every reference *within `internal/contentkit/`* to the capitalized name. Do them one symbol at a time with `git grep -l` scoped to the package, e.g.:

```bash
cd /home/coder/workspace/local/ach
# Example for one symbol — repeat for every row in the map:
grep -rl '\bparseClaudeCodeMarketplace\b' internal/contentkit/ | xargs sed -i 's/\bparseClaudeCodeMarketplace\b/ParseClaudeCodeMarketplace/g'
grep -rl '\bdiscoverSkillsInTree\b'        internal/contentkit/ | xargs sed -i 's/\bdiscoverSkillsInTree\b/DiscoverSkillsInTree/g'
grep -rl '\bsliceSkillSubtree\b'           internal/contentkit/ | xargs sed -i 's/\bsliceSkillSubtree\b/SliceSkillSubtree/g'
grep -rl '\bdiscoveredSkill\b'             internal/contentkit/ | xargs sed -i 's/\bdiscoveredSkill\b/DiscoveredSkill/g'
grep -rl '\bverifyPluginContents\b'        internal/contentkit/ | xargs sed -i 's/\bverifyPluginContents\b/VerifyPluginContents/g'
grep -rl '\bverifySkillContents\b'         internal/contentkit/ | xargs sed -i 's/\bverifySkillContents\b/VerifySkillContents/g'
grep -rl '\bstageSkillBody\b'              internal/contentkit/ | xargs sed -i 's/\bstageSkillBody\b/StageSkillBody/g'
grep -rl '\btruncateErrField\b'            internal/contentkit/ | xargs sed -i 's/\btruncateErrField\b/TruncateErrField/g'
grep -rl '\bskillRawIngressCap\b'          internal/contentkit/ | xargs sed -i 's/\bskillRawIngressCap\b/SkillRawIngressCap/g'
grep -rl '\bClaudeCodeMarketplace\b'       internal/contentkit/ | xargs sed -i 's/\bClaudeCodeMarketplace\b/ClaudeCodeMarketplace/g'   # already exported — no-op, verify
```
`ClaudeCodeMarketplace*` and `DiscoveredSkill`/`ValidateSkillName`/`SkillRawIngressCap` may already be exported in source — verify with `grep -n 'type ClaudeCodeMarketplace' internal/contentkit/marketplace_parse.go`; only rename what is actually lowercase. **Word-boundary `\b` is mandatory** so `ClaudeCodeMarketplacePlugin` isn't mangled by a `ClaudeCodeMarketplace` rule (do the longest names first, or rely on `\b` end-anchor).

- [ ] **Step 4: Repoint the operator call-sites to `contentkit.*`**

Add `import "github.com/ackstorm/ach/internal/contentkit"` to each caller file and rewrite the moved symbols to `contentkit.X`. Caller files (+ their `_test.go`): `pluginmarketplace_controller.go`, `marketplace_dispatch.go`, `marketplace_filters.go`, `skillmarketplace_controller.go`, `external_ref_refresh.go`, `pluginmarketplace_envtest_test.go`, `marketplace_dispatch_test.go`. Per-symbol, scoped to the controller package:

```bash
cd /home/coder/workspace/local/ach
CTRL=internal/controller/ach
for pair in \
  'parseClaudeCodeMarketplace:contentkit.ParseClaudeCodeMarketplace' \
  'ClaudeCodeMarketplacePlugin:contentkit.ClaudeCodeMarketplacePlugin' \
  'ClaudeCodeMarketplaceSource:contentkit.ClaudeCodeMarketplaceSource' \
  'ClaudeCodeMarketplaceOwner:contentkit.ClaudeCodeMarketplaceOwner' \
  'ClaudeCodeMarketplace:contentkit.ClaudeCodeMarketplace' \
  'truncateErrField:contentkit.TruncateErrField' \
  'discoverSkillsInTree:contentkit.DiscoverSkillsInTree' \
  'sliceSkillSubtree:contentkit.SliceSkillSubtree' \
  'discoveredSkill:contentkit.DiscoveredSkill' \
  'verifyPluginContents:contentkit.VerifyPluginContents' \
  'stageSkillBody:contentkit.StageSkillBody' \
  'verifySkillContents:contentkit.VerifySkillContents' \
  'skillRawIngressCap:contentkit.SkillRawIngressCap' ; do
    old=${pair%%:*}; new=${pair##*:}
    grep -rl "\\b$old\\b" $CTRL --include='*.go' | xargs -r sed -i "s/\\b$old\\b/$new/g"
done
```
**Order matters** — `ClaudeCodeMarketplacePlugin`/`Source`/`Owner` are listed BEFORE the bare `ClaudeCodeMarketplace` so the longer names are rewritten first; the `\b` end-anchor also protects them. After running, add the `contentkit` import to every caller file that now references `contentkit.` (goimports/gofmt or manual). Verify none double-prefixed (`contentkit.contentkit.`).

- [ ] **Step 5: Format, then build the whole tree**

```bash
cd /home/coder/workspace/local/ach
./scripts/dev.sh gofmt -w internal/contentkit/ internal/controller/ach/
./scripts/dev.sh go build ./...
```
Expected: clean build. Fix any missed rename / missing `contentkit` import / `contentkit.contentkit.` double-prefix the compiler reports. (Host-Go fallback: `/usr/local/go/bin/go build ./...`.)

- [ ] **Step 6: No stale references to the moved symbols remain in `package ach`**

```bash
cd /home/coder/workspace/local/ach
grep -rnE '\b(parseClaudeCodeMarketplace|discoverSkillsInTree|sliceSkillSubtree|verifyPluginContents|verifySkillContents|stageSkillBody|truncateErrField)\b' internal/controller/ach/ || echo "CLEAN: no un-repointed references"
```
Expected: `CLEAN`.

- [ ] **Step 7: Run the moved unit tests + the affected controller envtest**

```bash
cd /home/coder/workspace/local/ach
make test-unit-pkg PKG=./internal/contentkit/...
make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS='TestPluginMarketplace|TestSkillMarketplace' TIMEOUT=15m
```
Expected: contentkit unit tests PASS; the marketplace/skillmarketplace envtest suites PASS (they exercise the repointed `contentkit.*` calls). (Host-Go fallback for the unit pkg: `/usr/local/go/bin/go test ./internal/contentkit/...`.)

- [ ] **Step 8: Assert `internal/contentkit` is k8s-free (the core invariant)**

```bash
cd /home/coder/workspace/local/ach
./scripts/dev.sh go list -deps ./internal/contentkit | grep -E 'k8s.io/api|sigs.k8s.io/controller-runtime' || echo "CLEAN: contentkit has no k8s deps"
```
Expected: `CLEAN` (only `sigs.k8s.io/yaml`, which carries no `k8s.io/api`/controller-runtime, is allowed).

- [ ] **Step 9: Commit**

```bash
cd /home/coder/workspace/local/ach
git add internal/contentkit/ internal/controller/ach/
git commit -m "refactor(content): extract marketplace/skill parsers to k8s-free internal/contentkit

Move the pure marketplace-JSON parse, skill discovery, plugin/skill
content verification, and tar-safety logic out of internal/controller/ach
into a new k8s-free package, repoint its sentinel import at
internal/sourceserr, export the operator-facing symbols, and repoint the
controller call-sites. go list -deps ./internal/contentkit is k8s-free,
unblocking reuse from the ach-cli binary (Phase 2)."
```

---

## Task 2: Full gate

- [ ] **Step 1: Unit sweep** — `make test-unit` → PASS (now includes `internal/contentkit`).
- [ ] **Step 2: Controller envtest** — `make test-envtest` → PASS (full controller suite against the repointed code).
- [ ] **Step 3: Lint** — `make qa-lint-changed` (or `make qa-lint`) → PASS (SPDX headers intact on every moved file; no unused imports left in callers).
- [ ] **Step 4: Boundary** — `./scripts/dev.sh go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|controller-runtime'` → empty (baseline protected for Phase 2).
- [ ] **Step 5: e2e** — `make e2e-full` → green **except** the known pre-existing `Phase6CLI/hydrate_golden_diff` stale-golden failure (tracked separately; unrelated to this change). Confirm no NEW failures vs. the Phase 1A clean-run baseline.

---

## Self-review notes
- **Verdict from entanglement analysis:** clean move-with-companions — all 5 files form a self-contained closure; no symbol staying in `package ach` is referenced by the movers, so no cycle. k8s footprint clean apart from `sigs.k8s.io/yaml`.
- **Type consistency:** the exported-symbol map is the contract; the build (Step 5) and `CLEAN` grep (Step 6) catch any missed rename.
- **No placeholders:** every step is an exact command; the per-symbol renames are enumerated.
- **Next:** Phase 2 (the `ach-cli` `env`/`repo`/`plugin`/`skill` feature) consumes `contentkit.*` + `gitfetch.*`.
