# Marketplace Plugin Manifest-Optional Gate Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Stop ACH from false-failing marketplace plugins that legitimately ship without a `.claude-plugin/plugin.json`, by relaxing the post-fetch gate to accept a plugin that contains the manifest **OR** at least one recognized convention component.

**Architecture:** The marketplace Stage-2 materialize step (`internal/controller/ach/pluginmarketplace_controller.go:520`) streams each fetched plugin tarball to `verifyPluginManifest` before the atomic `rename(2)`. Today that verifier requires `.claude-plugin/plugin.json` to exist, returning `sources.ErrUpstreamInvalid` when absent. Per the real Claude Code schema the manifest is **optional** (components auto-discovered from convention dirs; name derived from the marketplace entry). We rename the verifier to `verifyPluginContents` and broaden its accept condition to: manifest present, OR any of the convention dirs (`commands/`, `agents/`, `skills/`, `hooks/`, `output-styles/`, `themes/`, `monitors/`) / root files (`SKILL.md`, `.mcp.json`, `.lsp.json`) present. A tar with none of these (e.g. a stray `README.md` only) still fails `UpstreamInvalid` — that genuinely is not a plugin.

**Tech Stack:** Go, controller-runtime, `archive/tar` + `compress/gzip`, stdlib `testing`, envtest (controller-runtime), kind+Helm e2e.

**Why this is low-risk:** All five existing `verifyPluginManifest` tests stay green under the new gate — README-only and buried-manifest tars still reject; only convention-only tars *newly* pass. The change is purely additive acceptance.

**Scope boundary:** This touches ONLY the marketplace gate (`internal/controller/ach` package). The standalone Plugin-CR path (`internal/sources/pluginpack/filter.go`, `ErrManifestMissing`) is intentionally out of scope. No interaction with the recently-merged `environment_types.go` optional `runtime`/`context` change.

**Real-world trigger (verified):** `PluginMarketplace anthropics-claude-code` reports `stage-2: 1 plugin(s) failed: plugin-dev: UpstreamInvalid`. Upstream `plugins/plugin-dev/` at rev `b67fa4f` contains `README.md`, `agents/`, `commands/`, `skills/` and **no** `.claude-plugin/plugin.json`. `claude plugin install plugin-dev@claude-plugins-official` succeeds — confirming the manifest is optional. ACH's gate is the bug.

**Reference (authoritative):** `code.claude.com/docs/en/plugins-reference` — "The manifest is optional. If omitted, Claude Code auto-discovers components in default locations and derives the plugin name from the directory name." / file-locations table: `.claude-plugin/plugin.json` … "(optional)", convention dirs `commands/ agents/ skills/ output-styles/ themes/ monitors/ hooks/` at plugin root.

---

## Pre-flight

**Branch (ackstorm git guidelines — feature branch off `main`):**
```bash
cd /home/jcm/Projects/ach
git checkout main && git pull --ff-only origin main
git checkout -b fix/marketplace-plugin-manifest-optional
```

**MANDATORY reads before touching code (per CLAUDE.md):**
- `internal/controller/ach/marketplace_manifest.go` (the gate — full file, 73 lines)
- `internal/controller/ach/pluginmarketplace_controller.go:300-410` (Stage-2 loop) and `:488-531` (materialize / verify call site)
- `internal/controller/ach/marketplace_manifest_test.go` (existing 5 tests)
- `internal/controller/ach/pluginmarketplace_envtest_test.go:300-340,420-440,740-770` (fake-fetcher harness, `mustPluginTarGz`, `fakeGitFetcher`, `mkGitSubdirPlugin`, `drainReconcileUntil`)
- `test/e2e/README.md` (only if running the e2e gate)

---

## Task 1: Relax the gate (rename + broaden accept) with unit tests

**Files:**
- Modify: `internal/controller/ach/marketplace_manifest.go` (whole file)
- Test: `internal/controller/ach/marketplace_manifest_test.go`

### Step 1: Write the new failing test — convention-only tar is accepted

Add to `marketplace_manifest_test.go` (the `buildTarGz` helper already exists in this file):

```go
func TestVerifyPluginContents_ConventionOnlyAccepted(t *testing.T) {
	// plugin-dev shape: no manifest, just convention dirs. Claude Code
	// auto-discovers these; ACH must accept it (real anthropics/claude-code
	// plugin-dev at rev b67fa4f). Regression guard for the marketplace gate.
	tgz := buildTarGz(t, map[string]string{
		"README.md":     "docs",
		"agents/foo.md":  "# foo agent",
		"commands/bar.md": "# bar command",
		"skills/baz/SKILL.md": "# baz skill",
	})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil for convention-only plugin", err)
	}
}
```

### Step 2: Run it — expect a COMPILE failure (function not yet renamed)

Run: `make test-unit-pkg PKG=./internal/controller/ach`
Expected: build error `undefined: verifyPluginContents` (the function is still named `verifyPluginManifest`). This is the intended red.

### Step 3: Rewrite `marketplace_manifest.go` with the relaxed gate

Replace the **entire** file with:

```go
// SPDX-License-Identifier: Apache-2.0

// Post-fetch plugin-contents check. The marketplace dispatcher returns a
// gzipped tar of the plugin's contents (whole worktree or a subtree
// slice). Before persisting the tar via rename(2), the materialize step
// calls verifyPluginContents to ensure the fetched tar actually looks
// like a Claude Code plugin.
//
// Per the Claude Code plugin schema
// (code.claude.com/docs/en/plugins-reference), `.claude-plugin/plugin.json`
// is OPTIONAL. When omitted, Claude Code auto-discovers components from
// convention directories (commands/, agents/, skills/, hooks/,
// output-styles/, themes/, monitors/) and root files (SKILL.md, .mcp.json,
// .lsp.json), deriving the plugin name from the directory basename — or,
// for a marketplace install, from the marketplace.json entry (which ACH
// already holds as entry.Name). A manifest-less plugin is therefore valid
// and MUST be accepted. (Originally gated as mandatory under issue #15
// Pregunta 3; that conflated "is a real plugin" with "has a manifest" and
// false-failed legitimate convention-only plugins such as
// anthropics/claude-code's plugin-dev.)
//
// The check accepts the tar iff it contains EITHER the manifest OR at
// least one recognized component. A tar with none of these (e.g. only a
// stray README.md) indicates the upstream entry does not point at a real
// plugin (wrong path, repo renamed, contents moved) and surfaces as a
// wrapped sources.ErrUpstreamInvalid, which classifyFetchErrorMarketplace
// maps to ReasonUpstreamInvalid -> per-entry pluginFailure.
//
// Tar layout assumption: git.tarSubtree (the only producer of plugin
// tarballs as of v1alpha1) strips the subtree prefix — files appear
// relative to the requested subtree root. So the manifest, if present,
// lives at `.claude-plugin/plugin.json` and convention dirs appear at the
// tar root. The verifier therefore takes no subtree argument; an earlier
// version did and was broken in production for every subtree-based fetch.

package ach

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

// manifestRelPath is the OPTIONAL plugin manifest location inside the tar
// (relative to the tar root, which is also the subtree root thanks to
// git.tarSubtree's prefix-stripping).
const manifestRelPath = ".claude-plugin/plugin.json"

// recognizedComponentDirs are the top-level convention directories a
// manifest-less plugin may carry; Claude Code auto-discovers these. Names
// match the on-disk directory layout (plugins-reference "File locations"
// table), NOT the camelCase plugin.json field names (e.g. dir is
// "output-styles", manifest field is "outputStyles").
var recognizedComponentDirs = map[string]struct{}{
	"commands":      {},
	"agents":        {},
	"skills":        {},
	"hooks":         {},
	"output-styles": {},
	"themes":        {},
	"monitors":      {},
}

// recognizedRootFiles are tar-root files that, alone, mark the directory
// as a plugin: a single-skill plugin (SKILL.md) or inline component
// config (.mcp.json / .lsp.json).
var recognizedRootFiles = map[string]struct{}{
	"SKILL.md":  {},
	".mcp.json": {},
	".lsp.json": {},
}

// verifyPluginContents walks the gzipped tar stream r and returns nil iff
// the tar contains the optional `.claude-plugin/plugin.json` manifest OR
// at least one recognized plugin component (convention dir / root file).
// The walk is stream-only and returns early on the first match.
func verifyPluginContents(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("plugin contents check: gzip reader: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("plugin contents check: tar walk: %v: %w", err, sources.ErrUpstreamInvalid)
		}
		// Normalize the "./" some tar writers prefix, then Clean. We do
		// NOT filter by Typeflag: a bare convention directory entry is a
		// valid signal, and the manifest/root-file names only ever match
		// regular files anyway.
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == manifestRelPath {
			return nil
		}
		if _, ok := recognizedRootFiles[name]; ok {
			return nil
		}
		first := name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			first = name[:i]
		}
		if _, ok := recognizedComponentDirs[first]; ok {
			return nil
		}
	}
	return fmt.Errorf("plugin contents check: no plugin manifest or recognized component "+
		"(commands/agents/skills/hooks/...) found in fetched tar: %w", sources.ErrUpstreamInvalid)
}
```

### Step 4: Run the new test — expect PASS

Run: `make test-unit-pkg PKG=./internal/controller/ach`
Expected: still RED — the file now compiles, but the **existing** five tests still call `verifyPluginManifest` (undefined). That's the next step. (If you prefer a clean green here, Step 5 must land in the same edit pass.)

### Step 5: Migrate the existing five tests to the new name + semantics

In `marketplace_manifest_test.go`, rename every `verifyPluginManifest(` call to `verifyPluginContents(`, rename the test funcs `TestVerifyPluginManifest_*` → `TestVerifyPluginContents_*`, and fix the one message assertion that pins the old wording:

- `TestVerifyPluginManifest_PresentAtRoot` → `TestVerifyPluginContents_PresentAtRoot` (body unchanged; still nil)
- `TestVerifyPluginManifest_LeadingDotSlashTolerated` → `TestVerifyPluginContents_LeadingDotSlashTolerated` (unchanged; still nil)
- `TestVerifyPluginManifest_Missing` → `TestVerifyPluginContents_NoComponentsRejected` — README-only still errors `UpstreamInvalid`. Change the message assertion:
  ```go
  if !strings.Contains(err.Error(), "recognized component") {
      t.Errorf("err message should mention recognized component; got %q", err.Error())
  }
  ```
- `TestVerifyPluginManifest_BuriedInSubdirRejected` → `TestVerifyPluginContents_BuriedInSubdirRejected` (unchanged; `plugins/x/.claude-plugin/plugin.json` has first-segment `plugins`, not a convention dir, so still rejects)
- `TestVerifyPluginManifest_CorruptGzip` → `TestVerifyPluginContents_CorruptGzip` (unchanged)

### Step 6: Add two more positive tests (root SKILL.md + .mcp.json)

```go
func TestVerifyPluginContents_RootSkillAccepted(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{"SKILL.md": "# single-skill plugin"})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil for root SKILL.md plugin", err)
	}
}

func TestVerifyPluginContents_McpConfigOnlyAccepted(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{".mcp.json": `{"mcpServers":{}}`})
	if err := verifyPluginContents(bytes.NewReader(tgz)); err != nil {
		t.Errorf("verify: %v; want nil for .mcp.json-only plugin", err)
	}
}
```

### Step 7: Run the full package unit tests — expect PASS

Run: `make test-unit-pkg PKG=./internal/controller/ach`
Expected: PASS (all `TestVerifyPluginContents_*` green).

### Step 8: Lint the touched package

Run: `make qa-lint-changed`
Expected: clean (no unused symbols; `manifestRelPath` is still referenced).

### Step 9: Commit

```bash
git add internal/controller/ach/marketplace_manifest.go internal/controller/ach/marketplace_manifest_test.go
git commit -m "fix(marketplace): accept manifest-less plugins (plugin.json is optional)

Claude Code treats .claude-plugin/plugin.json as optional and auto-discovers
components from convention dirs. The Stage-2 gate required the manifest,
false-failing legit plugins (e.g. anthropics/claude-code plugin-dev) as
UpstreamInvalid. Rename verifyPluginManifest -> verifyPluginContents and
accept a tar that has the manifest OR >=1 recognized component."
```

---

## Task 2: Update the production caller + envtest comment references

**Files:**
- Modify: `internal/controller/ach/pluginmarketplace_controller.go:511-520`
- Modify: `internal/controller/ach/pluginmarketplace_envtest_test.go` (comments only: ~316, ~321, ~748)

### Step 1: Rename the call site

In `pluginmarketplace_controller.go`, change the `§6.5` block:
- Comment `// 6.5: post-fetch plugin.json existence check` → `// 6.5: post-fetch plugin-contents check`
- `verifyErr := verifyPluginManifest(stagedForVerify)` → `verifyErr := verifyPluginContents(stagedForVerify)`
- Update the inline comment "Stream the staged tar to verifyPluginManifest before rename(2)." → "...verifyPluginContents before rename(2)."

### Step 2: Update envtest comments that name the old function

In `pluginmarketplace_envtest_test.go`, replace the three textual references `verifyPluginManifest` (the `mustPluginTarGz` doc comment ~line 316/321 and the inline comment ~line 748) with `verifyPluginContents`. No logic changes — `mustPluginTarGz` still produces a manifest tar, which the new gate still accepts.

### Step 3: Build the package to confirm no dangling references

Run: `./scripts/dev.sh go build ./internal/controller/ach/...`
Expected: builds clean. Then:
Run: `cd /home/jcm/Projects/ach && grep -rn "verifyPluginManifest" internal/ docs/ references/ CLAUDE.md`
Expected: **no matches** (every reference migrated).

### Step 4: Commit

```bash
git add internal/controller/ach/pluginmarketplace_controller.go internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "refactor(marketplace): rename verifyPluginManifest call site to verifyPluginContents"
```

---

## Task 3: Envtest — a manifest-less plugin materializes end-to-end

Proves the relaxed gate through the real reconcile loop: fetch → verify → `rename(2)` → DB upsert → `Synced=True` with no per-plugin failure.

**Files:**
- Modify: `internal/controller/ach/pluginmarketplace_envtest_test.go` (add one helper + one test)

### Step 1: Add a convention-only tar helper

Next to `mustPluginTarGz` (~line 326), add:

```go
// mustConventionOnlyPluginTarGz returns a tar.gz body with NO
// .claude-plugin/plugin.json — only a convention component dir. Mirrors a
// real manifest-less plugin (e.g. anthropics/claude-code plugin-dev) that
// verifyPluginContents must accept.
func mustConventionOnlyPluginTarGz(t *testing.T, _ string) string {
	t.Helper()
	tgz := buildTarGz(t, map[string]string{
		"README.md":     "docs",
		"agents/foo.md":  "# foo",
		"skills/bar/SKILL.md": "# bar",
	})
	return string(tgz)
}
```

### Step 2: Write the failing integration test

Model it on the existing partial-failure test (~line 740). Adapt the marketplace to a single `git-subdir` plugin whose fake fetcher returns the manifest-less body, and assert it lands in the materialized set with a clean `Synced`:

```go
func TestPluginMarketplace_ManifestLessPluginMaterializes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cr := /* build a PluginMarketplace CR with one plugin "skillonly" via
	         the same builder used by the neighbouring tests; see
	         mkGitSubdirPlugin + the CR construction around line 700-740 */ nil
	_ = cr

	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Plugins: []ClaudeCodeMarketplacePlugin{mkGitSubdirPlugin("skillonly")},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})
	gitReg := withFakeGitFetcher(t)
	gitReg.register(shaForName("skillonly"), &fakeGitFetcher{
		body: mustConventionOnlyPluginTarGz(t, "plugins/skillonly"),
		rev:  shaForName("skillonly"),
	})

	r := &PluginMarketplaceReconciler{
		Client: k8sClient, Namespace: WatchNamespace, Log: logr.Discard(),
		CacheRoot: root, Fetchers: factory.factory(),
		// DB: <wire the suite test DB if the neighbouring tests do>
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced &&
			strings.Contains(c.Message, "plugins=1") && !strings.Contains(c.Message, "failed")
	})
	if !ok {
		t.Fatalf("manifest-less plugin did not materialize cleanly")
	}
	// Optional, if DB wired: assert a marketplace_plugins row exists for "skillonly".
}
```

> **Executor note:** copy the exact CR-construction and DB-wiring idiom from the nearest existing envtest in this file (the partial-failure test around line 700-770). Do not invent new harness plumbing — reuse `mkGitSubdirPlugin`, `newMarketplaceFakeFactory`, `withFakeGitFetcher`, `shaForName`, `drainReconcileUntil`, `syncedCondition`, `ReasonSynced`.

### Step 3: Run it on the (already-fixed) code — expect PASS

Run: `make test-envtest-pkg PKG=./internal/controller/ach FOCUS=TestPluginMarketplace_ManifestLessPluginMaterializes`
Expected: PASS. (Sanity: temporarily `git stash` Task 1's `marketplace_manifest.go` change and re-run to confirm it FAILS `UpstreamInvalid` without the fix, then `git stash pop`. This proves the test actually guards the behavior.)

### Step 4: Run the controller envtest package (fast, no race) to catch regressions

Run: `make test-envtest-fast PKG=./internal/controller/ach`
Expected: PASS (the existing marketplace envtests still green).

### Step 5: Commit

```bash
git add internal/controller/ach/pluginmarketplace_envtest_test.go
git commit -m "test(marketplace): envtest a manifest-less plugin materializes cleanly"
```

---

## Task 4: Docs — same-commit hygiene

Per CLAUDE.md "update docs IN THE SAME COMMIT". The behavior-contract doc lives in code comments (done in Task 1) plus the index/troubleshooting surfaces.

**Files:**
- Modify: `CLAUDE.md` (External references → marketplace schema note)
- Modify: `references/troubleshooting.md` (marketplace stage-2 section, if present)

### Step 1: Update the CLAUDE.md marketplace-schema note

In the "External references" section, the bullet about the marketplace parser currently reads (approx):
> "Claude Code plugin / marketplace schemas: ... The parser (`internal/controller/ach/marketplace_parse.go`) follows the real schema with one drift ack: `url`-Kind entries carry an optional `path` (→ `git-subdir`)."

Append a sentence:
> "Plugin manifests (`.claude-plugin/plugin.json`) are **optional** per the schema; the Stage-2 gate (`verifyPluginContents`, `marketplace_manifest.go`) accepts a plugin that has the manifest OR ≥1 convention component (`commands/`/`agents/`/`skills/`/`hooks/`/`output-styles/`/`themes/`/`monitors/`, or root `SKILL.md`/`.mcp.json`/`.lsp.json`). Only a tar with none of these fails `UpstreamInvalid`."

### Step 2: Add a troubleshooting entry

Check `references/troubleshooting.md` for the marketplace/SourceReachable section. Add (or extend) an entry:

> **`plugin <name>: UpstreamInvalid` in a marketplace stage-2 summary.** The fetched plugin subtree has neither `.claude-plugin/plugin.json` nor any recognized convention component — i.e. the marketplace entry's `source` points at the wrong path/dir (manifest-less plugins ARE accepted as of `verifyPluginContents`). Verify the entry `source` resolves to the plugin root (where `commands/`/`agents/`/`skills/`/… live), not a parent or a docs-only dir. The marketplace stays `Synced=True`; the good plugins keep serving while the bad entry is reported in `status.message`.

If `troubleshooting.md` has no marketplace section, skip this step and note it in the commit body.

### Step 3: Commit

```bash
git add CLAUDE.md references/troubleshooting.md
git commit -m "docs(marketplace): note plugin.json is optional; gate accepts convention-only plugins"
```

---

## Task 5: Full gates + push

Touches `internal/controller/...` → E2E is mandatory before push (CLAUDE.md: "Never push a change touching `internal/controller|...` without confirming E2E green").

### Step 1: Full unit + lint sweep

Run: `make test-unit`
Expected: PASS.
Run: `make qa-lint`
Expected: clean.

### Step 2: Controller envtest with race

Run: `make test-envtest-pkg PKG=./internal/controller/ach`
Expected: PASS (race-enabled).

### Step 3: E2E full gate

Run: `make e2e-full`
Expected: green. Cluster is kept up after the run (pass or fail) for diagnosis. If a marketplace e2e fixture exists, confirm it still reconciles; reclaim with `make cluster-down` when done.

> **Note:** the live `anthropics-claude-code` fixture's `plugin-dev` failure is the prod symptom this fixes — it is NOT part of the kind e2e suite. After deploying the new image to a cluster, the `plugin-dev` entry should move from the failure summary into `status.plugins[]` and the message become `plugins=13` with no failure note. That deployment is user-owned, not part of this plan.

### Step 4: Pre-push gate (host-only) + push (REQUIRES USER CONFIRMATION)

```bash
make pre-push
git push -u origin fix/marketplace-plugin-manifest-optional
```

Then open a PR (`gh pr create`) targeting `main`. Do NOT `--no-verify`. Do NOT push until the user confirms E2E is green and approves.

---

## Done criteria

- `verifyPluginContents` accepts: manifest tar, convention-only tar (plugin-dev shape), root-`SKILL.md` tar, `.mcp.json`-only tar.
- `verifyPluginContents` still rejects (`UpstreamInvalid`): README-only tar, buried-manifest tar, corrupt gzip.
- Envtest: a manifest-less plugin reconciles to `Synced=True`, `plugins=1`, no per-plugin failure, DB row present.
- No remaining `verifyPluginManifest` references anywhere in the repo.
- `make test-unit`, `make qa-lint`, `make test-envtest-pkg PKG=./internal/controller/ach`, `make e2e-full`, `make pre-push` all green.
- CLAUDE.md + troubleshooting reflect manifest-optional behavior.
