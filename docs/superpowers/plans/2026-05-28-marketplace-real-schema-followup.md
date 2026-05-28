# Marketplace Real-Schema Follow-Up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `PluginMarketplace` parser + dispatcher in line with the official Claude Code marketplace schema so `examples/05-pluginmarketplace-anthropic.yaml` reaches `Synced=True` against the live upstream catalog, and `.claude-plugin/plugin.json` existence is verified per entry.

**Architecture:** Five atomic, independently green phases on one branch (`feat/marketplace-real-schema-followup`). Layered responsibility kept intact: parser validates wire shape (per-entry demote on field gaps, hard fail on catalog-wide invariants), dispatcher maps Kind→`git.Spec` and pre-resolves `ref→sha` when needed, materialize-step walks the post-fetch tar to verify `<subtree>/.claude-plugin/plugin.json` exists. No `internal/sources/git/fetcher.go` changes — the Fetcher's 40-hex SHA contract is preserved and the marketplace layer feeds it a resolved SHA.

**Tech Stack:** Go 1.x · controller-runtime · `internal/sources/git` (LsRemote + Fetcher) · `archive/tar` stdlib · testing stdlib (no Ginkgo for new tests).

**Issue reference:** [ackstorm/ach#15](https://github.com/ackstorm/ach/issues/15) — PluginMarketplace parser aborts whole catalog instead of per-entry skip.

**Official schemas (added to CLAUDE.md in Phase 5):**
- https://www.schemastore.org/claude-code-plugin-manifest.json
- https://www.schemastore.org/claude-code-marketplace.json
- https://code.claude.com/docs/en/plugin-marketplaces

---

## File Structure

```
internal/controller/ach/
├── marketplace_parse.go            MODIFY: relax per-Kind validations to per-entry demote;
│                                            add `github` Kind + `Repo` field; accept `url+path`
├── marketplace_parse_test.go       MODIFY: extend with demote + github + url+path cases
├── marketplace_dispatch.go         MODIFY: pre-resolve ref→sha for all Kinds (not just local-path);
│                                            add `github` Kind dispatch; treat `url+path` as git-subdir
├── marketplace_dispatch_test.go    MODIFY: add ref→sha + github + url+path tests
├── marketplace_manifest.go         CREATE: verifyPluginManifest(tarball reader, subtree) error
├── marketplace_manifest_test.go    CREATE: unit tests for the tar walk
├── pluginmarketplace_controller.go MODIFY: call verifyPluginManifest in materializeMarketplacePlugin
│                                            between staging-copy/fsync and rename(2)
└── conditions.go                   MODIFY (Phase 5 only): broaden ReasonUnsupportedPluginSource doc

CLAUDE.md                           MODIFY (Phase 5): add schemastore + claude.com docs links
```

**Branch:** `feat/marketplace-real-schema-followup` — one branch, five commits, one PR at the end.

**Test discipline per phase:** Each phase MUST be green via `./scripts/dev.sh make unit` before committing. Phases that touch the controller materialize path (F4) MUST additionally pass `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=PluginMarketplace`.

---

## Task 0: Branch Setup

**Files:** none

- [ ] **Step 1: Verify working tree is clean and on `main`**

```bash
cd /home/coder/workspace/local/ach
git status
git rev-parse --abbrev-ref HEAD
```

Expected: clean working tree, current branch `main`. If `.dockerignore` / `Dockerfile` are dirty (per user's note they were just committed) confirm `git status` shows no `M` lines.

- [ ] **Step 2: Create the feature branch off `main`**

```bash
git checkout -b feat/marketplace-real-schema-followup
```

Expected: `Switched to a new branch 'feat/marketplace-real-schema-followup'`

- [ ] **Step 3: Confirm baseline tests pass before any edits**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...
```

Expected: all tests PASS. If anything fails on baseline, STOP and investigate — do not start the plan against a red baseline.

---

## Task 1 (Phase 1): Demote Per-Entry Validation Failures in Parser

**Goal:** A marketplace.json with one `url`-Kind entry missing `sha` plus a valid `git-subdir` entry must parse cleanly. The missing-`sha` entry's `Source.Kind` is flipped to `""` so Stage-2 surfaces it as `ReasonUnsupportedPluginSource`. The whole-catalog abort path is preserved ONLY for: malformed JSON, empty `plugins[]`, plugin-count cap, non-DNS-1123 plugin name.

**Files:**
- Modify: `internal/controller/ach/marketplace_parse.go` (the per-Kind switch in `parseClaudeCodeMarketplace`, ~lines 181-206)
- Modify: `internal/controller/ach/marketplace_parse_test.go` (extend with demote cases)

- [ ] **Step 1: Write the failing parser test — per-entry demote**

Append to `internal/controller/ach/marketplace_parse_test.go`:

```go
// ─── Per-entry demote tests (issue #15 / Phase 1) ─────────────────────

func TestParseClaudeCodeMarketplace_UrlMissingShaDemotedPerEntry(t *testing.T) {
	// A url-Kind entry missing `sha` MUST NOT abort the catalog. The
	// invalid entry resolves to Kind="" so Stage-2 demotes it via
	// ReasonUnsupportedPluginSource. The sibling valid git-subdir entry
	// must round-trip intact.
	body := `{
	  "name": "mkt",
	  "owner": {"name": "o"},
	  "plugins": [
	    {
	      "name": "missing-sha",
	      "source": {"source": "url", "url": "https://example.com/p.git"}
	    },
	    {
	      "name": "valid-git-subdir",
	      "source": {
	        "source": "git-subdir",
	        "url": "https://github.com/o/r.git",
	        "path": "plugins/x",
	        "ref": "v1",
	        "sha": "0123456789abcdef0123456789abcdef01234567"
	      }
	    }
	  ]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v (whole-catalog abort is the bug)", err)
	}
	if len(mkt.Plugins) != 2 {
		t.Fatalf("want 2 plugins; got %d", len(mkt.Plugins))
	}
	if mkt.Plugins[0].Source.Kind != "" {
		t.Errorf("plugin[0].Kind = %q; want \"\" (demoted)", mkt.Plugins[0].Source.Kind)
	}
	if mkt.Plugins[1].Source.Kind != "git-subdir" {
		t.Errorf("plugin[1].Kind = %q; want git-subdir", mkt.Plugins[1].Source.Kind)
	}
}

func TestParseClaudeCodeMarketplace_GitSubdirMissingUrlDemoted(t *testing.T) {
	body := `{
	  "name": "mkt", "owner": {"name": "o"},
	  "plugins": [{
	    "name": "bad",
	    "source": {"source": "git-subdir", "path": "p", "sha": "0123456789abcdef0123456789abcdef01234567"}
	  }]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mkt.Plugins[0].Source.Kind != "" {
		t.Errorf("Kind = %q; want demoted to \"\"", mkt.Plugins[0].Source.Kind)
	}
}

func TestParseClaudeCodeMarketplace_LocalPathTraversalDemotedPerEntry(t *testing.T) {
	// local-path with `..` segment must NOT abort the catalog (#4 (b)
	// decision: demote per-entry, T-02-06-01 mitigation still applies
	// because the traversal path never reaches the filesystem — the
	// dispatcher short-circuits on Kind="").
	body := `{
	  "name": "mkt", "owner": {"name": "o"},
	  "plugins": [{
	    "name": "evil",
	    "source": "../etc/passwd"
	  }]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mkt.Plugins[0].Source.Kind != "" {
		t.Errorf("Kind = %q; want \"\" (demoted)", mkt.Plugins[0].Source.Kind)
	}
}
```

- [ ] **Step 2: Run the new tests and verify they FAIL**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestParseClaudeCodeMarketplace_(UrlMissingShaDemoted|GitSubdirMissingUrlDemoted|LocalPathTraversalDemoted)' -v
```

Expected: all three FAIL — the current parser returns `ErrUpstreamInvalid` for these shapes.

- [ ] **Step 3: Rewrite the per-Kind switch in `parseClaudeCodeMarketplace`**

In `internal/controller/ach/marketplace_parse.go`, replace lines 173-207 (`for i := range mkt.Plugins { ... }`) with:

```go
	for i := range mkt.Plugins {
		p := &mkt.Plugins[i]
		// (1) name validation — bounded length + DNS-1123 + per-label.
		// Hard fail (catalog-wide): adversarial names land in
		// status.message via formatStage2Message (T-02-06-08).
		if err := validatePluginName(p.Name); err != nil {
			return nil, fmt.Errorf("marketplace.json: plugin[%d].name %q: %v: %w",
				i, truncateErrField(p.Name), err, sources.ErrUpstreamInvalid)
		}
		// (2)+(3) per-Kind field validation. Failure demotes the entry to
		// Kind="" so Stage-2 emits ReasonUnsupportedPluginSource per-entry
		// (issue #15 contract). The catalog continues.
		switch p.Source.Kind {
		case "git-subdir":
			if p.Source.URL == "" || p.Source.Path == "" {
				p.Source = ClaudeCodeMarketplaceSource{} // demote
			}
		case "url":
			if p.Source.URL == "" {
				p.Source = ClaudeCodeMarketplaceSource{} // demote
			}
		case "local-path":
			if p.Source.Path == "" || validateLocalPath(p.Source.Path) != nil {
				p.Source = ClaudeCodeMarketplaceSource{} // demote
			}
		case "":
			// Already demoted upstream by UnmarshalJSON (unknown discriminator).
		default:
			// Should be unreachable.
			p.Source = ClaudeCodeMarketplaceSource{}
		}
	}
```

Note: `sha` is no longer required at parse time — Phase 2 adds the pre-resolution path. We deliberately don't validate `sha` shape here; the dispatcher / `git.Fetcher` is the authoritative validator.

- [ ] **Step 4: Update the doc comment of `parseClaudeCodeMarketplace`**

In the same file, replace the doc-comment block (lines 132-160) to reflect the demote contract:

```go
// parseClaudeCodeMarketplace unmarshals the upstream marketplace.json
// (Claude Code real schema — see CLAUDE.md "External references" for
// the schemastore URLs) and performs Stage-1 validation.
//
// Validation surface:
//
// Catalog-level HARD FAIL (returns wrapped sources.ErrUpstreamInvalid):
//   - JSON-level unmarshal failure.
//   - len(plugins) == 0 — a marketplace with zero entries is not legit.
//   - len(plugins) > marketplaceMaxPluginsPerCatalog (DoS guard).
//   - plugin.Name fails DNS-1123 / per-label / length check
//     (T-02-06-08: adversarial names propagate to k8s status.message
//     via formatStage2Message).
//
// Per-entry DEMOTE (sets Source.Kind="", catalog continues):
//   - git-subdir entry missing url OR path.
//   - url entry missing url.
//   - local-path entry with empty / path-traversal path.
//   - Unknown source discriminator (already demoted by UnmarshalJSON).
//
// Per-entry validation is intentionally minimal — sha / ref are both
// optional and Phase-2 pre-resolution handles the rest at dispatch time.
```

- [ ] **Step 5: Run the Phase 1 tests + the existing parse-suite, expect PASS**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestParseClaudeCodeMarketplace' -v
```

Expected: all parse tests PASS (new + existing).

- [ ] **Step 6: Run the full controller test suite to catch regressions**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/ach/marketplace_parse.go internal/controller/ach/marketplace_parse_test.go
git commit -m "fix(marketplace): demote per-entry parser failures (#15)

A url-Kind entry missing sha (or any per-Kind field) used to bubble
ErrUpstreamInvalid up to markSyncedFalse, aborting the whole catalog.
Per the TODO §5 contract, per-entry field gaps must demote to Kind=\"\"
so Stage-2 emits ReasonUnsupportedPluginSource and the rest of the
catalog continues.

Hard-fail surface preserved for: malformed JSON, empty plugins[],
plugin-count cap, non-DNS-1123 plugin name (T-02-06-08).

Phase 1 of 5 (real-schema follow-up). examples/05 still red until
Phase 2 lands sha-optional support."
```

---

## Task 2 (Phase 2): Pre-Resolve `ref→sha` When `sha` Absent

**Goal:** `git-subdir` / `url` entries without `sha` are resolved via `git.LsRemote` (existing helper in `internal/sources/git/lsremote.go`) before invoking `git.Fetcher`. `Fetcher`'s 40-hex contract is unchanged. After this phase, `examples/05-pluginmarketplace-anthropic.yaml` reaches `Synced=True` against the live Anthropic catalog.

**Files:**
- Modify: `internal/controller/ach/marketplace_dispatch.go` (`buildGitSpecForEntry` + `dispatchMarketplacePlugin`)
- Modify: `internal/controller/ach/marketplace_dispatch_test.go` (add ref-only test, sha-takes-precedence test)

- [ ] **Step 1: Write the failing dispatch test — ref-only entry triggers LsRemote**

Append to `internal/controller/ach/marketplace_dispatch_test.go`:

```go
func TestDispatchMarketplacePlugin_GitSubdir_RefOnly_PreResolvesSHA(t *testing.T) {
	// Entry without sha: dispatcher MUST call newResolveHeadSHAFn to
	// resolve ref→sha before constructing the git.Spec, then pass the
	// resolved SHA to Fetch (40-hex contract preserved).
	const resolvedSHA = "fedcba9876543210fedcba9876543210fedcba98"
	var captured sourcesgit.Spec
	var resolveCalled bool

	origFetch := newGitFetcherFn
	origResolve := newResolveHeadSHAFn
	defer func() {
		newGitFetcherFn = origFetch
		newResolveHeadSHAFn = origResolve
	}()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	newResolveHeadSHAFn = func(_ context.Context, url, ref, _ string) (string, error) {
		resolveCalled = true
		if url != "https://github.com/o/r.git" {
			t.Errorf("LsRemote url = %q", url)
		}
		if ref != "main" {
			t.Errorf("LsRemote ref = %q; want main (default)", ref)
		}
		return resolvedSHA, nil
	}

	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "git-subdir",
			URL:  "https://github.com/o/r.git",
			Path: "plugins/x",
			// No Ref, no SHA — both should default + resolve.
		},
	}
	mp := &achv1alpha1.PluginMarketplace{}
	_, rev, err := dispatchMarketplacePlugin(context.Background(), mp, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !resolveCalled {
		t.Fatal("newResolveHeadSHAFn was not called for sha-less entry")
	}
	if captured.SHA != resolvedSHA {
		t.Errorf("captured.SHA = %q; want %q", captured.SHA, resolvedSHA)
	}
	if rev != resolvedSHA {
		t.Errorf("rev = %q; want %q", rev, resolvedSHA)
	}
}

func TestDispatchMarketplacePlugin_SHA_TakesPrecedenceOverRef(t *testing.T) {
	// When both sha and ref are present, the explicit sha wins —
	// LsRemote MUST NOT be invoked.
	const pinnedSHA = "0123456789abcdef0123456789abcdef01234567"
	origFetch := newGitFetcherFn
	origResolve := newResolveHeadSHAFn
	defer func() {
		newGitFetcherFn = origFetch
		newResolveHeadSHAFn = origResolve
	}()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	newResolveHeadSHAFn = func(_ context.Context, _, _, _ string) (string, error) {
		t.Fatal("LsRemote called even though entry has explicit sha")
		return "", nil
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "git-subdir",
			URL:  "https://github.com/o/r.git",
			Path: "plugins/x",
			Ref:  "v1",
			SHA:  pinnedSHA,
		},
	}
	_, rev, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if rev != pinnedSHA {
		t.Errorf("rev = %q; want %q", rev, pinnedSHA)
	}
}
```

- [ ] **Step 2: Run the new tests and verify they FAIL**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestDispatchMarketplacePlugin_(GitSubdir_RefOnly|SHA_TakesPrecedence)' -v
```

Expected: `GitSubdir_RefOnly` FAILs (current dispatcher only resolves ref for local-path; for git-subdir without sha it would pass `SHA=""` to `git.Fetcher`, which rejects it).

- [ ] **Step 3: Rewrite `dispatchMarketplacePlugin` to pre-resolve for all sha-less Kinds**

In `internal/controller/ach/marketplace_dispatch.go`, replace the function body of `dispatchMarketplacePlugin` (lines 96-121) with:

```go
func dispatchMarketplacePlugin(
	ctx context.Context,
	mp *achv1alpha1.PluginMarketplace,
	entry ClaudeCodeMarketplacePlugin,
	auth *corev1.Secret,
	cacheRoot string,
) (io.ReadCloser, string, error) {
	spec, err := buildGitSpecForEntry(mp, entry, auth, cacheRoot)
	if err != nil {
		return nil, "", err
	}
	// Pre-resolve ref→sha when the entry didn't pin one. Applies to every
	// git-backed Kind (git-subdir, url, github, local-path). local-path
	// already had this branch — Phase 2 generalizes it to all Kinds the
	// dispatcher returned a Spec for.
	if spec.SHA == "" {
		sha, rerr := newResolveHeadSHAFn(ctx, spec.URL, spec.Ref, spec.Token)
		if rerr != nil {
			return nil, "", rerr
		}
		spec.SHA = sha
	}
	f := newGitFetcherFn(spec)
	res, err := f.Fetch(ctx, sourcesgit.Request{})
	if err != nil {
		return nil, "", err
	}
	return res.Body, res.UpstreamRev, nil
}
```

- [ ] **Step 4: Update the doc comment for `dispatchMarketplacePlugin`**

In the same file, replace the comment block above the function (lines 82-95) with:

```go
// dispatchMarketplacePlugin runs the per-entry fetch and returns a
// streaming io.ReadCloser + the UpstreamRev (the resolved SHA).
//
//   - git-subdir / url / github / local-path: build a git.Spec via
//     buildGitSpecForEntry. If Spec.SHA is empty, pre-resolve via
//     newResolveHeadSHAFn (git ls-remote semantics) so the Fetcher's
//     40-hex contract is satisfied. Then Fetch + return the streaming
//     tar.gz body.
//   - "": errUnsupportedPluginSource (Stage-2 maps to
//     ReasonUnsupportedPluginSource).
//
// Sha-optional rationale: the official schema lists ref and sha as both
// optional. Upstream catalogs (Anthropic) ship entries without sha that
// MUST still materialize. The Fetcher itself keeps the 40-hex pin
// contract — we satisfy it by resolving on the marketplace layer.
//
// auth is the marketplace's auth Secret (re-used per the v1alpha1
// design: per-entry auth is NOT yet a wire-format field). May be nil.
```

- [ ] **Step 5: Run Phase 2 + Phase 1 tests, expect PASS**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestDispatchMarketplacePlugin|TestParseClaudeCodeMarketplace' -v
```

Expected: all PASS.

- [ ] **Step 6: Full package regression**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/ach/marketplace_dispatch.go internal/controller/ach/marketplace_dispatch_test.go
git commit -m "feat(marketplace): pre-resolve ref→sha for sha-less entries (#15)

Generalize the local-path-only ls-remote resolution to every git-backed
Kind. Entries without an explicit sha trigger newResolveHeadSHAFn before
Fetcher invocation, satisfying Fetcher's 40-hex contract without
weakening it. With sha present, the pinned value still wins.

Closes the acceptance criterion on issue #15: examples/05 now reaches
Synced=True against the live Anthropic catalog (where several entries
omit sha).

Phase 2 of 5."
```

---

## Task 3 (Phase 3): Add `github` Kind + Normalize `url+path` ≡ `git-subdir`

**Goal:** Support `"source": "github"` entries (`{repo, ref?, sha?}` → `https://github.com/{repo}.git`) and accept `url`-Kind entries that carry a non-empty `path` (treated identically to `git-subdir`, per ack of upstream drift). Schema reference: schemastore plugin-manifest spec (added to CLAUDE.md in Phase 5).

**Files:**
- Modify: `internal/controller/ach/marketplace_parse.go` (UnmarshalJSON: new `Repo` field + `github` discriminator; per-Kind switch: validate `repo`)
- Modify: `internal/controller/ach/marketplace_parse_test.go` (extend with github cases + url+path case)
- Modify: `internal/controller/ach/marketplace_dispatch.go` (`buildGitSpecForEntry`: new `case kindGitHub`; `url+path` collapse)
- Modify: `internal/controller/ach/marketplace_dispatch_test.go` (extend with github dispatch + url+path dispatch)

- [ ] **Step 1: Write the failing parser tests for `github` Kind + `url+path`**

Append to `internal/controller/ach/marketplace_parse_test.go`:

```go
// ─── Phase 3: github Kind + url+path normalization ────────────────────

func TestClaudeCodeMarketplaceSource_UnmarshalGitHub(t *testing.T) {
	body := []byte(`{"source":"github","repo":"owner/name","ref":"v1","sha":"0123456789abcdef0123456789abcdef01234567"}`)
	var s ClaudeCodeMarketplaceSource
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Kind != "github" {
		t.Errorf("Kind = %q; want github", s.Kind)
	}
	if s.Repo != "owner/name" {
		t.Errorf("Repo = %q", s.Repo)
	}
	if s.Ref != "v1" || s.SHA == "" {
		t.Errorf("got %+v", s)
	}
}

func TestParseClaudeCodeMarketplace_GitHubMissingRepoDemoted(t *testing.T) {
	body := `{
	  "name": "mkt", "owner": {"name": "o"},
	  "plugins": [{
	    "name": "bad",
	    "source": {"source": "github", "ref": "main"}
	  }]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mkt.Plugins[0].Source.Kind != "" {
		t.Errorf("Kind = %q; want demoted to \"\"", mkt.Plugins[0].Source.Kind)
	}
}

func TestParseClaudeCodeMarketplace_UrlWithPathAccepted(t *testing.T) {
	// Upstream schema says `url` has no path. Real-world catalogs (e.g.
	// the zilliz entry in claude-plugins-official) ship url+path. We
	// accept the drift: url with non-empty path is parsed as-is and the
	// dispatcher treats it like git-subdir.
	body := `{
	  "name": "mkt", "owner": {"name": "o"},
	  "plugins": [{
	    "name": "zilliz",
	    "source": {
	      "source": "url",
	      "url": "https://github.com/zilliztech/zilliz-plugin.git",
	      "path": "plugins/zilliz",
	      "sha": "e960396da0bd0b1cb219fa97e3bcbb425ee1abbd"
	    }
	  }]
	}`
	mkt, err := parseClaudeCodeMarketplace([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mkt.Plugins[0].Source.Kind != "url" {
		t.Errorf("Kind = %q; want url", mkt.Plugins[0].Source.Kind)
	}
	if mkt.Plugins[0].Source.Path != "plugins/zilliz" {
		t.Errorf("Path = %q; want path preserved", mkt.Plugins[0].Source.Path)
	}
}
```

- [ ] **Step 2: Run the new parser tests and verify they FAIL**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestClaudeCodeMarketplaceSource_UnmarshalGitHub|TestParseClaudeCodeMarketplace_(GitHubMissingRepoDemoted|UrlWithPathAccepted)' -v
```

Expected: all three FAIL.

- [ ] **Step 3: Add `Repo` field + `github` discriminator in `marketplace_parse.go`**

In `internal/controller/ach/marketplace_parse.go`, modify the `ClaudeCodeMarketplaceSource` struct (lines 70-76) to add the `Repo` field:

```go
type ClaudeCodeMarketplaceSource struct {
	Kind string // "git-subdir" | "url" | "github" | "local-path" | "" (unsupported)
	URL  string
	Repo string // github-Kind only: "owner/name" → cloned as https://github.com/<repo>.git
	Path string
	Ref  string
	SHA  string
}
```

Then update `UnmarshalJSON` (lines 82-115). Replace the object-form block:

```go
	// Object form.
	var obj struct {
		Source string `json:"source"`
		URL    string `json:"url"`
		Repo   string `json:"repo"`
		Path   string `json:"path"`
		Ref    string `json:"ref"`
		SHA    string `json:"sha"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		// Neither string nor object — leave Kind="" so the per-entry
		// path surfaces UnsupportedPluginSource.
		return nil
	}
	switch obj.Source {
	case "git-subdir", "url", "github":
		s.Kind = obj.Source
	default:
		// Unknown discriminator (npm, ftp, ...) — Kind="" → per-entry
		// unsupported. npm is a known wire-format value we intentionally
		// route here per the v1alpha1 git-only operator scope.
		return nil
	}
	s.URL = obj.URL
	s.Repo = obj.Repo
	s.Path = obj.Path
	s.Ref = obj.Ref
	s.SHA = obj.SHA
	return nil
```

- [ ] **Step 4: Extend the per-Kind validation switch in `parseClaudeCodeMarketplace`**

In `internal/controller/ach/marketplace_parse.go`, locate the per-Kind switch (lines you edited in Phase 1, Task 1 Step 3). Replace it with:

```go
		switch p.Source.Kind {
		case "git-subdir":
			if p.Source.URL == "" || p.Source.Path == "" {
				p.Source = ClaudeCodeMarketplaceSource{}
			}
		case "url":
			if p.Source.URL == "" {
				p.Source = ClaudeCodeMarketplaceSource{}
			}
		case "github":
			if p.Source.Repo == "" {
				p.Source = ClaudeCodeMarketplaceSource{}
			}
		case "local-path":
			if p.Source.Path == "" || validateLocalPath(p.Source.Path) != nil {
				p.Source = ClaudeCodeMarketplaceSource{}
			}
		case "":
			// Already demoted upstream by UnmarshalJSON.
		default:
			p.Source = ClaudeCodeMarketplaceSource{}
		}
```

- [ ] **Step 5: Update the file-level header comment of `marketplace_parse.go`**

Replace the doc-comment block (lines 3-12) with:

```go
// Claude Code marketplace.json wire-format types + parser. The schema
// is the upstream Claude Code real schema:
//
//   plugins[].source can be either a bare string (local-path) or an
//   object with a "source" discriminator. Recognised discriminators:
//
//     - "git-subdir": {url, path, ref?, sha?}
//     - "url":        {url, path?, ref?, sha?}   (path accepts upstream
//                                                  drift — schema says
//                                                  no path, real catalogs
//                                                  ship it; treated as
//                                                  git-subdir when set)
//     - "github":     {repo, ref?, sha?} → cloned as
//                     https://github.com/<repo>.git
//
//   Any other discriminator (npm, ftp, ...) resolves to Kind="" so the
//   per-entry Stage-2 path surfaces ReasonUnsupportedPluginSource
//   without aborting the whole catalog.
//
// Per-entry dispatch + fetch lives in marketplace_dispatch.go.
// Schema URLs: see CLAUDE.md "External references".
```

- [ ] **Step 6: Run the parser tests, expect PASS**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestClaudeCodeMarketplaceSource_|TestParseClaudeCodeMarketplace_' -v
```

Expected: all PASS.

- [ ] **Step 7: Write the failing dispatch tests for `github` Kind + `url+path`**

Append to `internal/controller/ach/marketplace_dispatch_test.go`:

```go
func TestDispatchMarketplacePlugin_GitHub(t *testing.T) {
	const pinnedSHA = "0123456789abcdef0123456789abcdef01234567"
	var captured sourcesgit.Spec
	origFetch := newGitFetcherFn
	defer func() { newGitFetcherFn = origFetch }()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "github",
			Repo: "owner/name",
			Ref:  "v2",
			SHA:  pinnedSHA,
		},
	}
	_, _, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if captured.URL != "https://github.com/owner/name.git" {
		t.Errorf("URL = %q; want https://github.com/owner/name.git", captured.URL)
	}
	if captured.Subtree != "" {
		t.Errorf("Subtree = %q; want \"\" (github clones whole repo)", captured.Subtree)
	}
	if captured.Ref != "v2" || captured.SHA != pinnedSHA {
		t.Errorf("captured = %+v", captured)
	}
}

func TestDispatchMarketplacePlugin_UrlWithPath_TreatedAsGitSubdir(t *testing.T) {
	const pinnedSHA = "abcdefabcdefabcdefabcdefabcdefabcdef0123"
	var captured sourcesgit.Spec
	origFetch := newGitFetcherFn
	defer func() { newGitFetcherFn = origFetch }()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "zilliz",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "url",
			URL:  "https://github.com/zilliztech/zilliz-plugin.git",
			Path: "plugins/zilliz",
			SHA:  pinnedSHA,
		},
	}
	_, _, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if captured.Subtree != "plugins/zilliz" {
		t.Errorf("Subtree = %q; want plugins/zilliz (url+path collapsed to git-subdir)", captured.Subtree)
	}
}
```

- [ ] **Step 8: Run the new dispatch tests and verify they FAIL**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestDispatchMarketplacePlugin_(GitHub|UrlWithPath_TreatedAsGitSubdir)' -v
```

Expected: both FAIL.

- [ ] **Step 9: Add the `kindGitHub` constant and extend `buildGitSpecForEntry`**

In `internal/controller/ach/marketplace_dispatch.go`, locate the Kind constants block (lines 34-39) and update:

```go
const (
	kindGitSubdir = "git-subdir"
	kindURL       = "url"
	kindGitHub    = "github"
	kindLocalPath = "local-path"
)
```

Then replace the `buildGitSpecForEntry` body (lines 130-171). Replace the `case kindURL` and add `case kindGitHub`:

```go
	switch entry.Source.Kind {
	case kindGitSubdir:
		return sourcesgit.Spec{
			URL:       entry.Source.URL,
			Ref:       defaultRef(entry.Source.Ref),
			SHA:       entry.Source.SHA,
			Subtree:   entry.Source.Path,
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case kindURL:
		// url+path collapse: when path is non-empty the entry behaves
		// like git-subdir (upstream-drift ack — see marketplace_parse.go
		// header). Empty path → whole-worktree tar.
		return sourcesgit.Spec{
			URL:       entry.Source.URL,
			Ref:       defaultRef(entry.Source.Ref),
			SHA:       entry.Source.SHA,
			Subtree:   entry.Source.Path,
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case kindGitHub:
		return sourcesgit.Spec{
			URL:       "https://github.com/" + entry.Source.Repo + ".git",
			Ref:       defaultRef(entry.Source.Ref),
			SHA:       entry.Source.SHA,
			Subtree:   "", // github Kind has no path → whole-worktree
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case kindLocalPath:
		url, ref, err := marketplaceOwnRepo(mp)
		if err != nil {
			return sourcesgit.Spec{}, err
		}
		return sourcesgit.Spec{
			URL:       url,
			Ref:       ref,
			SHA:       "", // resolved by dispatcher via newResolveHeadSHAFn
			Subtree:   entry.Source.Path,
			Token:     token,
			CacheRoot: cacheRoot,
		}, nil
	case "":
		return sourcesgit.Spec{}, errUnsupportedPluginSource
	default:
		return sourcesgit.Spec{}, fmt.Errorf("plugin %q: unknown source Kind %q: %w",
			truncateErrField(entry.Name), entry.Source.Kind, sources.ErrUpstreamInvalid)
	}
```

- [ ] **Step 10: Run all dispatch tests, expect PASS**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestDispatchMarketplacePlugin' -v
```

Expected: all PASS.

- [ ] **Step 11: Full package regression**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...
```

Expected: all PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/controller/ach/marketplace_parse.go internal/controller/ach/marketplace_parse_test.go internal/controller/ach/marketplace_dispatch.go internal/controller/ach/marketplace_dispatch_test.go
git commit -m "feat(marketplace): support github Kind + url+path normalization

Adds the \`github\` plugin-source Kind from the official Claude Code
marketplace schema, mapping {repo, ref?, sha?} to
https://github.com/<repo>.git via the existing git fetcher.

Also acks the upstream drift in real catalogs: \`url\`-Kind entries with
a non-empty \`path\` (e.g. zilliz in claude-plugins-official) are now
parsed and dispatched as git-subdir semantics. Schema says url has no
path; reality says otherwise.

npm Kind remains per-entry unsupported (operator is git-only).

Phase 3 of 5."
```

---

## Task 4 (Phase 4): Verify `<subtree>/.claude-plugin/plugin.json` Exists Post-Fetch

**Goal:** A marketplace entry whose fetched tarball lacks `.claude-plugin/plugin.json` at the resolved subtree fails per-entry with `ReasonUpstreamInvalid` (per #3 decision). Existence check only — no JSON parsing. Implemented as a `tar.Reader` walk over the already-staged tarball, between the staging fsync and the atomic `rename(2)`.

**Files:**
- Create: `internal/controller/ach/marketplace_manifest.go` (`verifyPluginManifest`)
- Create: `internal/controller/ach/marketplace_manifest_test.go` (unit tests)
- Modify: `internal/controller/ach/pluginmarketplace_controller.go` (call site inside `materializeMarketplacePlugin`)

- [ ] **Step 1: Write the failing unit tests for `verifyPluginManifest`**

Create `internal/controller/ach/marketplace_manifest_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

// buildTarGz produces an in-memory tar.gz with the given path→content map.
// Used to synthesize the staged tarball verifyPluginManifest walks.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("Write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

func TestVerifyPluginManifest_PresentAtRoot(t *testing.T) {
	// Whole-repo tar (subtree=""): manifest at .claude-plugin/plugin.json.
	tgz := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"x"}`,
		"README.md":                  "hi",
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz), ""); err != nil {
		t.Errorf("verify: %v; want nil", err)
	}
}

func TestVerifyPluginManifest_PresentInSubtree(t *testing.T) {
	// Subtree tar: manifest at plugins/x/.claude-plugin/plugin.json.
	tgz := buildTarGz(t, map[string]string{
		"plugins/x/.claude-plugin/plugin.json": `{"name":"x"}`,
		"plugins/x/README.md":                  "hi",
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz), "plugins/x"); err != nil {
		t.Errorf("verify: %v; want nil", err)
	}
}

func TestVerifyPluginManifest_Missing(t *testing.T) {
	tgz := buildTarGz(t, map[string]string{
		"README.md": "no manifest here",
	})
	err := verifyPluginManifest(bytes.NewReader(tgz), "")
	if err == nil {
		t.Fatal("expected error on missing manifest")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err = %v; want wrap of sources.ErrUpstreamInvalid", err)
	}
	if !strings.Contains(err.Error(), "plugin.json") {
		t.Errorf("err message should mention plugin.json; got %q", err.Error())
	}
}

func TestVerifyPluginManifest_SubtreeButOnlyRootManifest(t *testing.T) {
	// Subtree=plugins/x; manifest only at top-level — should fail.
	tgz := buildTarGz(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"top"}`,
		"plugins/x/README.md":        "no manifest in this subtree",
	})
	err := verifyPluginManifest(bytes.NewReader(tgz), "plugins/x")
	if err == nil {
		t.Fatal("expected error: manifest in wrong location")
	}
}

func TestVerifyPluginManifest_LeadingDotSlashSubtreeTolerated(t *testing.T) {
	// local-path entries arrive with "./plugins/x" style paths — the
	// verifier must normalize ./ prefixes so it doesn't double up.
	tgz := buildTarGz(t, map[string]string{
		"plugins/x/.claude-plugin/plugin.json": `{}`,
	})
	if err := verifyPluginManifest(bytes.NewReader(tgz), "./plugins/x"); err != nil {
		t.Errorf("verify: %v; want nil with ./ prefix", err)
	}
}
```

- [ ] **Step 2: Run the new tests and verify they FAIL with "undefined: verifyPluginManifest"**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestVerifyPluginManifest' -v
```

Expected: build failure / undefined symbol.

- [ ] **Step 3: Implement `verifyPluginManifest`**

Create `internal/controller/ach/marketplace_manifest.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Post-fetch plugin.json existence check. The marketplace dispatcher
// returns a gzipped tar of the plugin's contents (whole worktree or a
// subtree slice). Before persisting the tar via rename(2), the
// materialize step calls verifyPluginManifest to ensure
// `<subtree>/.claude-plugin/plugin.json` is actually present in the
// stream — a fetched tar that lacks the manifest indicates the
// upstream entry doesn't point at a real plugin (e.g. wrong path,
// repo renamed, manifest moved). The contents of the manifest are NOT
// parsed; only presence is checked (issue #15 Pregunta 3).
//
// A missing manifest surfaces as a wrapped sources.ErrUpstreamInvalid,
// which classifyFetchErrorMarketplace already maps to
// ReasonUpstreamInvalid → per-entry pluginFailure.

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

// manifestRelPath is the path-suffix the verifier searches for inside
// the tarball (relative to the resolved subtree root).
const manifestRelPath = ".claude-plugin/plugin.json"

// verifyPluginManifest walks the gzipped tar stream r and returns nil
// iff a regular-file entry exists at <subtree>/.claude-plugin/plugin.json
// (subtree is normalized: leading "./" stripped, trailing "/" stripped).
// Empty subtree means whole-repo tar (manifest at top level).
//
// The walk is stream-only: no full-buffer materialization. Returns
// early once the manifest entry is found.
func verifyPluginManifest(r io.Reader, subtree string) error {
	want := path.Join(normalizeSubtree(subtree), manifestRelPath)
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("plugin.json check: gzip reader: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("plugin.json check: tar walk: %v: %w", err, sources.ErrUpstreamInvalid)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == want {
			return nil
		}
	}
	return fmt.Errorf("plugin.json check: %s not found in fetched tar: %w",
		want, sources.ErrUpstreamInvalid)
}

// normalizeSubtree strips a leading "./" and any trailing "/" so the
// resulting path can be path.Join'd against manifestRelPath without
// producing ".//x/y/.claude-plugin/plugin.json".
func normalizeSubtree(s string) string {
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimSuffix(s, "/")
	return s
}
```

- [ ] **Step 4: Run the unit tests, expect PASS**

```bash
./scripts/dev.sh go test ./internal/controller/ach/ -run 'TestVerifyPluginManifest' -v
```

Expected: all PASS.

- [ ] **Step 5: Wire `verifyPluginManifest` into `materializeMarketplacePlugin`**

In `internal/controller/ach/pluginmarketplace_controller.go`, locate the comment `// ─── 6: fsync + close ───` (around line 477). Insert a new step BETWEEN `closed = true` (end of step 6) and `// ─── 7: atomic rename(2) ───` (start of step 7).

The new block is a `verifyPluginManifest` call that re-opens the staged file (read-only) and walks it. Failure leaves the staging file in place for cleanup-by-os.Remove on the error path.

Replace the existing step 7 block (currently at approximately lines 488-492) so it reads:

```go
	// ─── 6.5: post-fetch plugin.json existence check ───
	// Stream the staged tar to verifyPluginManifest before rename(2).
	// Subtree comes from entry.Source.Path (or "" for whole-repo Kinds).
	stagedForVerify, openErr := os.Open(stagingPath)
	if openErr != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("plugin %q: open staged tar: %w", entry.Name, openErr)
	}
	verifyErr := verifyPluginManifest(stagedForVerify, entry.Source.Path)
	_ = stagedForVerify.Close()
	if verifyErr != nil {
		_ = os.Remove(stagingPath)
		return verifyErr
	}

	// ─── 7: atomic rename(2) ───
	if err := os.Rename(stagingPath, finalPath); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("plugin %q: §10.3 rename(2): %w", entry.Name, err)
	}
```

- [ ] **Step 6: Sanity-build the controller package**

```bash
./scripts/dev.sh go build ./internal/controller/ach/...
```

Expected: builds cleanly.

- [ ] **Step 7: Run the full controller-package unit suite**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...
```

Expected: all PASS. If any pre-existing envtest fixture uses tarballs without `.claude-plugin/plugin.json`, those will now fail — investigate and either fix the fixture or skip that test from Phase-4 scope (note the skip with a TODO so it's not lost). DO NOT relax `verifyPluginManifest` to make stale fixtures pass.

- [ ] **Step 8: Run envtest for the marketplace controller**

```bash
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/ FOCUS=PluginMarketplace TIMEOUT=10m
```

Expected: PASS. Same caveat as Step 7 — if existing fixtures regress, fix the fixture (add `.claude-plugin/plugin.json` to the synthesized tar), not the verifier.

- [ ] **Step 9: Commit**

```bash
git add internal/controller/ach/marketplace_manifest.go internal/controller/ach/marketplace_manifest_test.go internal/controller/ach/pluginmarketplace_controller.go
git commit -m "feat(marketplace): verify .claude-plugin/plugin.json exists post-fetch

Stream-walk the staged tar after fsync, before rename(2), to assert
that <subtree>/.claude-plugin/plugin.json is present. Existence only —
the manifest contents are NOT parsed here (downstream Plugin reconciler
owns that).

Missing manifest wraps sources.ErrUpstreamInvalid →
ReasonUpstreamInvalid per-entry, the rest of the catalog continues.

Phase 4 of 5."
```

If any pre-existing envtest fixture had to be updated to include a `.claude-plugin/plugin.json` entry, include those fixture files in the same commit (they are part of the same logical change).

---

## Task 5 (Phase 5): Doc Hygiene — CLAUDE.md References + Stale Comment Sweep

**Goal:** Add the official schema URLs and Claude Code marketplace docs link to `CLAUDE.md` "External references". Sweep `conditions.go` and any other stale doc-comments that say "only npm is unsupported" — the per-entry path now covers a wider surface (npm, malformed shapes, missing manifest).

**Files:**
- Modify: `CLAUDE.md` (External references section)
- Modify: `internal/controller/ach/conditions.go` (ReasonUnsupportedPluginSource doc-comment)

- [ ] **Step 1: Append schema URLs to CLAUDE.md "External references"**

In `CLAUDE.md`, the "External references" section starts at line 647. After the `goreleaser v2` bullet (line 658-660), append a new bullet:

```markdown
- **Claude Code plugin / marketplace schemas**: official JSON Schemas at
  https://www.schemastore.org/claude-code-plugin-manifest.json and
  https://www.schemastore.org/claude-code-marketplace.json — authoritative
  for the shape of `marketplace.json` and `.claude-plugin/plugin.json`.
  Narrative docs at https://code.claude.com/docs/en/plugin-marketplaces.
  The marketplace parser (`internal/controller/ach/marketplace_parse.go`)
  follows the real schema with one ack of upstream drift: `url`-Kind
  entries carry an optional `path` field (schema says no path; real
  catalogs ship it — treated as `git-subdir` when set).
```

- [ ] **Step 2: Update the `ReasonUnsupportedPluginSource` doc-comment**

In `internal/controller/ach/conditions.go` (around line 105-110), replace the doc-comment:

```go
	// ReasonUnsupportedPluginSource (Plan 02-06 / issue #15) — a
	// marketplace entry resolved to Source.Kind="" because its wire
	// shape is not materializable. Covers:
	//
	//   - Known-but-unsupported discriminators (today: "npm" — the
	//     v1alpha1 operator is git-only).
	//   - Unknown discriminators (any other source.source value).
	//   - Required-field gaps the parser couldn't recover from
	//     (e.g. git-subdir without url+path, github without repo,
	//     local-path with path-traversal).
	//
	// Per-entry only — the marketplace's Synced condition stays True if
	// Stage-1 succeeded; the rejected entry is recorded in
	// status.message via the partial-failure path.
	ReasonUnsupportedPluginSource = "UnsupportedPluginSource"
```

- [ ] **Step 3: Build + lint the controller package**

```bash
./scripts/dev.sh make lint-changed
./scripts/dev.sh go build ./internal/controller/ach/...
```

Expected: clean lint, clean build.

- [ ] **Step 4: Full package unit regression**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md internal/controller/ach/conditions.go
git commit -m "docs(marketplace): record official schema URLs + sweep stale comments

Adds schemastore + claude.com docs links to CLAUDE.md \"External
references\" so future agents know the authoritative shape of
marketplace.json and .claude-plugin/plugin.json.

Broadens the ReasonUnsupportedPluginSource doc-comment in
conditions.go: the per-entry demote path now covers npm, unknown
discriminators, AND required-field gaps (post-Phase 1+3 refactor).

Phase 5 of 5 — closes #15."
```

---

## Task 6: Final Gate + PR

**Goal:** Run the full pre-push gate locally, then push and open the PR.

**Files:** none (verification + git ops only).

- [ ] **Step 1: Run the full controller test suite one more time**

```bash
./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...
./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/ FOCUS=PluginMarketplace
```

Expected: both green.

- [ ] **Step 2: Run the lint sweep**

```bash
./scripts/dev.sh make lint
```

Expected: zero issues. If issues exist, fix and re-commit (small fixup commit per phase, not amend).

- [ ] **Step 3: Run the pre-push gate**

```bash
make pre-push
```

Expected: all 15+ gates PASS. If gitleaks/trufflehog flags a false positive on the new test fixtures (unlikely — they use synthetic SHAs), update `.gitleaks.toml` allowlist in a separate fixup commit.

- [ ] **Step 4: Push the branch**

```bash
git push -u origin feat/marketplace-real-schema-followup
```

- [ ] **Step 5: Open the PR**

```bash
gh pr create --title "fix(marketplace): per-entry demote + real-schema support (#15)" --body "$(cat <<'EOF'
## Summary

Closes #15. Five atomic commits on one branch:

- **F1** — Parser demotes per-entry validation failures (`url`/`git-subdir`/`local-path` with missing required fields) to `Kind=""` instead of aborting the catalog. Hard-fail surface preserved for malformed JSON / empty plugins[] / plugin-count cap / non-DNS-1123 plugin name (T-02-06-08 mitigation).
- **F2** — Pre-resolve `ref→sha` via `git.LsRemote` for every git-backed Kind. `git.Fetcher`'s 40-hex contract preserved.
- **F3** — Add `github` Kind (`{repo, ref?, sha?}` → `https://github.com/<repo>.git`). Normalize `url+path` ≡ `git-subdir` (upstream drift ack).
- **F4** — Verify `<subtree>/.claude-plugin/plugin.json` exists post-fetch via streaming `tar.Reader` walk. Missing manifest → per-entry `ReasonUpstreamInvalid`.
- **F5** — `CLAUDE.md` records official schemastore + Claude Code docs URLs. Stale comments swept in `conditions.go`.

`npm` Kind remains per-entry unsupported (operator is git-only — out of scope, separate issue).

## Test plan

- [ ] `./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...` — green
- [ ] `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/ FOCUS=PluginMarketplace` — green
- [ ] `kubectl apply -f examples/05-pluginmarketplace-anthropic.yaml` reaches `Synced=True` within 60s on a fresh cluster (live Anthropic catalog)
- [ ] `marketplace_plugins` table contains one row per anthropic entry matching `^code-.*`
- [ ] Pre-push 15-gate clean

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL printed.

- [ ] **Step 6: Report the PR URL back to the user.**

---

## Self-Review

**Spec coverage:**
- Issue #15 acceptance criteria:
  - "kubectl apply ... reaches Synced=True within 60s" → covered by F1 + F2 (sha-less Anthropic entries no longer abort).
  - "marketplace_plugins table contains one row per anthropic entry that matches `^code-.*`" → covered by F1 + F2 (filtered survivors are materialized; demoted entries go to `failures` not the DB row).
  - "whole-marketplace abort path remains for genuinely malformed JSON" → preserved by F1 (JSON unmarshal + empty plugins[] still hard-fail).
- User decisions:
  - 1.a npm unsupported → F3 keeps `npm` as per-entry demote (UnmarshalJSON's `default` branch).
  - 2.b tar.Reader walk → F4.
  - 3. ReasonUpstreamInvalid for missing manifest → F4.
  - 4.a validatePluginName hard fail → preserved in F1 step 3.
  - 4.b local-path traversal per-entry → demoted in F1 step 3.
  - Pregunta 2 corregida (`url+path` accepted) → F3.
  - Pregunta 3 (`plugin.json` exists, not parsed) → F4.

**Placeholder scan:** No `TBD`/`TODO later`/"implement later"/"similar to" in plan steps. Each step has either exact code or exact commands.

**Type consistency:**
- `ClaudeCodeMarketplaceSource.Repo` introduced in F3 Step 3 is used in F3 Step 4 (validation), F3 Step 9 (dispatcher), and references the same lowercase JSON tag `"repo"` throughout.
- `kindGitHub` constant added in F3 Step 9 is consistent with the literal `"github"` used in parser switch (F3 Step 4) — the parser writes `s.Kind = "github"` via the discriminator switch, the dispatcher reads it via `case kindGitHub`. Verified literal === constant value.
- `verifyPluginManifest(io.Reader, string) error` signature in F4 Step 3 matches the test calls in F4 Step 1 and the call site in F4 Step 5.
- `normalizeSubtree` helper (F4 Step 3) is exercised by `TestVerifyPluginManifest_LeadingDotSlashSubtreeTolerated` (F4 Step 1).
