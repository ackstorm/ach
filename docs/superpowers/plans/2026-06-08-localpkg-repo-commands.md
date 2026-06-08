# Phase 2.1 — Local registry + `repo` commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add the first half of the ccplugin-style local package manager to `ach-cli`: register external marketplaces/repos (`github:`/`git:`/local) and list them with auto-detected capabilities — `ach-cli repo add|list|remove|update`. No server, per-user, local state.

**Architecture:** New k8s-free packages under `internal/cli/localpkg/` consumed by a new `cmd/ach-cli/cmd/repo.go`. `repo add` parses the source URI, clones the repo via `internal/gitfetch` (Phase 1A), reads the tarball, and detects which of four *lenses* it provides via `internal/contentkit` (Phase 1B): `plugin-marketplace` (`.claude-plugin/marketplace.json`), `skill-marketplace` (convention `<root>/<d>/SKILL.md`), direct `plugin`, direct `skill`. State persists to `~/.config/ach/local/{repos.json, credentials.json}` via `internal/cli/state.WriteAtomic`. Tokens live in a separate `0600` file; `repos.json` only flags `hasToken`.

**Tech Stack:** Go (`github.com/ackstorm/ach`); `ach-cli` must NOT import `k8s.io/*` (only stdlib + `sigs.k8s.io/yaml` allowed). Toolchain: `make`/`./scripts/dev.sh` (docker works directly) or host Go at `/usr/local/go/bin/go`. SPDX header on every `*.go`. TDD: write the failing test, then the impl. Conventional commits, one per task.

**Boundary invariant (assert after every task):** `go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|controller-runtime'` stays empty.

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/cli/localpkg/source/source.go` (+`_test.go`) | Parse `github:`/`git:`/local source refs → `SourceURI` (clone URL, ref, auth scheme) |
| `internal/cli/localpkg/store/store.go` (+`_test.go`) | State types + dir resolution + atomic load/save of `repos.json` / `credentials.json` |
| `internal/cli/localpkg/discover/discover.go` (+`_test.go`) | Capability detection over a cloned tarball (the four lenses), wrapping `contentkit` |
| `cmd/ach-cli/cmd/repo.go` (+`repo_test.go`) | `repo add/list/remove/update` cobra commands wiring source→gitfetch→discover→store |

---

## Task 1: `internal/cli/localpkg/source` — source-URI parser

**Files:** Create `internal/cli/localpkg/source/source.go`, `internal/cli/localpkg/source/source_test.go`.

**Public API (define exactly this):**
```go
// Package source parses ach-cli local-package source references.
package source

type Kind int

const (
	KindGitHub Kind = iota + 1 // github:owner/repo[#ref]
	KindGit                    // git:https://host/path[.git][#ref]
	KindLocal                  // /abs, ./rel, ../rel, ~/home
)

// Auth scheme string values (mirror gitfetch.AuthScheme selection at fetch time).
const (
	AuthBearer     = "bearer"
	AuthBasicOAuth2 = "basic-oauth2"
)

type SourceURI struct {
	Kind       Kind
	CloneURL   string // https URL for github/git; empty for local
	GitRef     string // text after '#'; empty → default branch
	LocalPath  string // absolute path for local
	AuthScheme string // "bearer" (default) or "basic-oauth2" (gitlab heuristic / --auth override)
}

// Parse classifies ref. authOverride (""|"bearer"|"basic-oauth2") forces the
// scheme when non-empty; otherwise it is inferred (gitlab host → basic-oauth2).
func Parse(ref, authOverride string) (SourceURI, error)
```

Rules: `github:owner/repo` → `CloneURL=https://github.com/owner/repo.git`, strip optional `.git`, split `#ref`, scheme `bearer`. `git:<url>[#ref]` → strip `git:` prefix, split `#ref`, `CloneURL=<url>`; infer `basic-oauth2` when host matches `gitlab` or `git.` heuristic (mirror `internal/sources/gitprovider.schemeForProvider` intent), else `bearer`; `authOverride` wins. Local: starts with `/`, `./`, `../`, `~` → `KindLocal`, expand `~` + resolve absolute. Reject empty / unrecognized scheme with a plain `error`.

- [ ] **Step 1: Write the failing table test**

```go
// SPDX-License-Identifier: Apache-2.0
package source

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name, ref, auth string
		want            SourceURI
		wantErr         bool
	}{
		{"github bare", "github:obra/superpowers", "", SourceURI{Kind: KindGitHub, CloneURL: "https://github.com/obra/superpowers.git", AuthScheme: AuthBearer}, false},
		{"github ref", "github:obra/superpowers#main", "", SourceURI{Kind: KindGitHub, CloneURL: "https://github.com/obra/superpowers.git", GitRef: "main", AuthScheme: AuthBearer}, false},
		{"github dotgit", "github:obra/superpowers.git", "", SourceURI{Kind: KindGitHub, CloneURL: "https://github.com/obra/superpowers.git", AuthScheme: AuthBearer}, false},
		{"gitlab self-hosted infers oauth2", "git:https://git.ackstorm.com/grp/repo.git#v1", "", SourceURI{Kind: KindGit, CloneURL: "https://git.ackstorm.com/grp/repo.git", GitRef: "v1", AuthScheme: AuthBasicOAuth2}, false},
		{"git generic bearer", "git:https://example.com/x/y.git", "", SourceURI{Kind: KindGit, CloneURL: "https://example.com/x/y.git", AuthScheme: AuthBearer}, false},
		{"auth override wins", "git:https://example.com/x/y.git", "basic-oauth2", SourceURI{Kind: KindGit, CloneURL: "https://example.com/x/y.git", AuthScheme: AuthBasicOAuth2}, false},
		{"empty", "", "", SourceURI{}, true},
		{"unknown scheme", "ftp://x", "", SourceURI{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.ref, tc.auth)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestParseLocal(t *testing.T) {
	got, err := Parse("./fixtures/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindLocal || got.LocalPath == "" || got.LocalPath[0] != '/' {
		t.Fatalf("local parse wrong: %+v", got)
	}
}
```

- [ ] **Step 2: Run → FAIL** `/usr/local/go/bin/go test ./internal/cli/localpkg/source/` (undefined: Parse).
- [ ] **Step 3: Implement `source.go`** to satisfy the API + rules above (stdlib only: `strings`, `net/url`, `path/filepath`, `os`, `fmt`, `regexp`). SPDX header.
- [ ] **Step 4: Run → PASS** `/usr/local/go/bin/go test ./internal/cli/localpkg/source/`.
- [ ] **Step 5: Commit** `feat(cli): add localpkg source-URI parser (github/git/local)`.

---

## Task 2: `internal/cli/localpkg/store` — state types + atomic persistence

**Files:** Create `internal/cli/localpkg/store/store.go`, `store_test.go`.

**Public API:**
```go
// Package store persists ach-cli local package-manager state under ~/.config/ach/local/.
package store

type Capability struct {
	Lens  string `json:"lens"`  // "plugin-marketplace"|"skill-marketplace"|"plugin"|"skill"
	Count int    `json:"count"`
}

type RepoEntry struct {
	Name       string       `json:"name"`
	Source     string       `json:"source"`               // raw ref as typed
	Kind       string       `json:"kind"`                 // "github"|"git"|"local"
	CloneURL   string       `json:"cloneURL,omitempty"`
	GitRef     string       `json:"gitRef,omitempty"`
	LocalPath  string       `json:"localPath,omitempty"`
	AuthScheme string       `json:"authScheme,omitempty"`
	HasToken   bool         `json:"hasToken"`
	Provides   []Capability `json:"provides"`
	DetectedSHA string      `json:"detectedSHA,omitempty"`
	AddedAt    string       `json:"addedAt"`
}

type ReposFile struct {
	Version int         `json:"version"` // schema version, start at 1
	Repos   []RepoEntry `json:"repos"`
}

type FileRec struct {
	RelPath string `json:"relPath"`
	Hash    string `json:"hash"`
}

type InstalledEntry struct {
	Ref         string    `json:"ref"`  // "<name>@<repo>"
	Repo        string    `json:"repo"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`   // "plugin"|"skill"
	Target      string    `json:"target"` // adapter id
	ResolvedSHA string    `json:"resolvedSHA,omitempty"`
	Files       []FileRec `json:"files"`
	InstalledAt string    `json:"installedAt"`
}

type InstalledFile struct {
	Version   int              `json:"version"`
	Installed []InstalledEntry `json:"installed"`
}

// Dir returns ~/.config/ach/local (honoring XDG via config.Path's parent), creating it 0700.
func Dir() (string, error)

func LoadRepos() (*ReposFile, error)        // (&ReposFile{Version:1}, nil) when absent
func SaveRepos(f *ReposFile) error          // atomic, 0600
func LoadInstalled() (*InstalledFile, error)
func SaveInstalled(f *InstalledFile) error

// Credentials live in a separate 0600 file (never in repos.json).
func LoadToken(repo string) (string, error) // "" when absent
func SaveToken(repo, token string) error
func DeleteToken(repo string) error
```

Implementation notes: derive the dir as `filepath.Join(filepath.Dir(configPath), "local")` where `configPath, _ = config.Path()` (reuse `internal/cli/config.Path` for XDG handling). Marshal with 2-space indent. Persist via `state.WriteAtomic(path, data, 0o600)` (import `internal/cli/state`). `repos.json`/`installed.json` at `0600` too (simplest; they may carry repo names only, but uniform 0600 is safe). Credentials file `credentials.json` = `map[string]string` (repo→token), `0600`.

- [ ] **Step 1: Failing test** — round-trip `SaveRepos`/`LoadRepos` and `SaveToken`/`LoadToken`/`DeleteToken` under a temp `XDG_CONFIG_HOME` (`t.Setenv("XDG_CONFIG_HOME", t.TempDir())`); assert: absent→empty (`Version:1`, no repos); saved entry round-trips; `credentials.json` mode is `0600` (`os.Stat`); `DeleteToken` removes the key; token never appears in `repos.json` bytes.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement `store.go`** (stdlib `encoding/json`, `os`, `path/filepath`, `fmt` + `internal/cli/config`, `internal/cli/state`). SPDX.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat(cli): add localpkg state store (repos/installed/credentials)`.

---

## Task 3: `internal/cli/localpkg/discover` — capability detection

**Files:** Create `internal/cli/localpkg/discover/discover.go`, `discover_test.go`.

**Public API:**
```go
// Package discover classifies a cloned repo tarball into the capability lenses
// it provides (mirrors ACH's PluginMarketplace vs SkillMarketplace vs direct kinds).
package discover

import "github.com/ackstorm/ach/internal/cli/localpkg/store"

// Detect inspects a gzipped-tar repo snapshot and returns the lenses it provides
// (possibly several). skillsRootHint is the optional --path narrow ("" → autodetect).
func Detect(tarball []byte, skillsRootHint string) ([]store.Capability, error)
```

Detection (each lens independent; a repo may match several):
- **plugin-marketplace:** locate `.claude-plugin/marketplace.json` (or root `marketplace.json`) inside the tar; if present, `contentkit.ParseClaudeCodeMarketplace(bytes)` → `Capability{Lens:"plugin-marketplace", Count:len(Plugins)}`.
- **skill-marketplace:** `contentkit.DiscoverSkillsInTree(tarball, root)` for `root` ∈ {`skillsRootHint` if set, else try `""` then `"skills"`}; first non-empty result → `Capability{Lens:"skill-marketplace", Count:len(skills)}`.
- **plugin (direct):** if NO marketplace.json AND `contentkit.VerifyPluginContents(tarReader)` returns nil → `Capability{Lens:"plugin", Count:1}`.
- **skill (direct):** if `contentkit.VerifySkillContents(tarReader)` returns nil (root `SKILL.md`) → `Capability{Lens:"skill", Count:1}`.

Read the tarball into memory once; for the `VerifyPluginContents`/`VerifySkillContents` calls (which take an `io.Reader`), wrap fresh `bytes.NewReader(tarball)` each call (they expect the gzip stream). For the marketplace.json lookup, stream the gzip-tar and capture the file bytes. Return `[]store.Capability` (empty slice, not nil, when nothing detected — caller renders "unknown").

- [ ] **Step 1: Failing test with in-memory tar fixtures.** Build small gzip-tar fixtures in the test (`archive/tar`+`compress/gzip`): (a) a `.claude-plugin/marketplace.json` with 2 plugins → expect `plugin-marketplace:2`; (b) `skills/foo/SKILL.md` + `skills/bar/SKILL.md` with valid frontmatter (`name: foo`/`name: bar`, non-empty description) → expect `skill-marketplace:2`; (c) a root `commands/x.md` (no marketplace.json) → expect `plugin:1`; (d) a root `SKILL.md` (`name: solo`) → expect `skill:1`; (e) empty repo → expect `[]`. Assert the returned `[]store.Capability` matches (order-insensitive). Reuse the frontmatter shape `---\nname: foo\ndescription: d\n---` that `contentkit` expects.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement `discover.go`** (`archive/tar`, `compress/gzip`, `bytes`, `path` + `internal/contentkit` + `internal/cli/localpkg/store`). SPDX.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: k8s-free check** `./scripts/dev.sh go list -deps ./internal/cli/localpkg/... | grep -E 'k8s.io/api|controller-runtime' || echo CLEAN`.
- [ ] **Step 6: Commit** `feat(cli): add localpkg capability detection (4 lenses)`.

---

## Task 4: `cmd/ach-cli/cmd/repo.go` — the `repo` commands

**Files:** Create `cmd/ach-cli/cmd/repo.go`, `cmd/ach-cli/cmd/repo_test.go`.

Wire a `repo` parent with `add/list/remove/update` children (mirror `env.go`'s `newEnvCmd()` + `init(){ rootCmd.AddCommand(newRepoCmd()) }` pattern). Use the `list.go` cwd-seam idiom only if needed; these commands operate on `~/.config/ach/local` (use a `t.Setenv("XDG_CONFIG_HOME", ...)` test seam — no cwd needed). RunE returns `*exit.CodedError` (`exit.ConfigFile` on store errors, `exit.Network` on clone/fetch failures classified via `sourceserr`, else `exit.General`).

**Commands:**
```
ach-cli repo add <source> --name <n> [--token <t>] [--auth bearer|basic-oauth2] [--path <dir>]
ach-cli repo list
ach-cli repo remove <name>
ach-cli repo update <name>
```

**`repo add` flow (the core):**
1. `su, err := source.Parse(args[0], flagAuth)`.
2. Determine clone inputs: for github/git use `su.CloneURL` + `su.GitRef` (default `"HEAD"`→ resolve below) ; for local, set `RepoEntry.Kind="local"`, `LocalPath=su.LocalPath`, skip network and detect from the local dir tarred in-memory (helper `tarLocalDir`) — OR (simpler v1) require github/git for `repo add` and reject local with a clear "local sources not yet supported in repo add" error. **v1: support github/git; defer local** (note in `--help`).
3. Resolve ref→sha: `ref := su.GitRef; if ref=="" { ref="HEAD" }`; `sha, err := gitfetch.LsRemote(ctx, su.CloneURL, ref, token, scheme)` where `token` from `--token` (else host env-var fallback `GITHUB_TOKEN`/`GITLAB_TOKEN`) and `scheme` = `gitfetch.AuthBearer`/`AuthBasicOAuth2` from `su.AuthScheme`.
4. Clone whole repo: `res, err := gitfetch.New(gitfetch.Spec{URL:su.CloneURL, Ref:ref, SHA:sha, Token:token, AuthScheme:scheme}).Fetch(ctx, gitfetch.Request{})`; `defer res.Body.Close()`; `tarball, _ := io.ReadAll(res.Body)`.
5. `caps, err := discover.Detect(tarball, flagPath)`; if empty → error "no installable plugins or skills found in <name>".
6. Persist: `store.SaveToken(name, token)` if token non-empty; append `store.RepoEntry{Name, Source:args[0], Kind, CloneURL, GitRef:ref, AuthScheme:su.AuthScheme, HasToken:token!="", Provides:caps, DetectedSHA:sha, AddedAt:<RFC3339>}` to `LoadRepos()` (reject duplicate name); `SaveRepos`.
7. Render: `✓ repo "<name>"  <kind>  <source>  (provides: plugins:N skills:M)`.

(For the timestamp: `ach-cli` may use `time.Now()` — confirm no test-determinism rule forbids it; the `repo_test.go` should inject a clock seam OR assert structure not the exact time. Use a package-level `var nowFn = time.Now` seam.)

**`repo list`:** `LoadRepos` → table `NAME  KIND  SOURCE  AUTH  PROVIDES` (AUTH renders `oauth2 •••`/`bearer •••` when `HasToken`, else `-`; PROVIDES joins `lens:count`). Tokens NEVER printed.

**`repo remove <name>`:** drop entry from `repos.json` + `store.DeleteToken(name)`; idempotent (exit 0 if absent, with a note).

**`repo update <name>`:** re-run steps 3–6 for the stored repo (re-resolve sha, re-clone, re-detect, update `DetectedSHA`/`Provides`); report changed vs unchanged.

- [ ] **Step 1: Failing cobra test** using a LOCAL bare-git fixture so no network is touched. In `repo_test.go`: `t.Setenv("XDG_CONFIG_HOME", t.TempDir())`; create a temp git repo on disk with a `.claude-plugin/marketplace.json` (2 plugins) + commit (helper shells out to `git init`/`add`/`commit` in a tempdir, or build a bare repo); register it via a `git:file://<path>` source. Drive `newRepoCmd()` with args `["add","git:file://"+repoPath,"--name","fix"]`, assert exit 0 and that `store.LoadRepos()` shows `fix` with `provides` containing `plugin-marketplace:2` and a non-empty `DetectedSHA`. Then `["list"]` → output contains `fix` and `plugin-marketplace:2` and NOT the token. Then `["remove","fix"]` → repos empty. (If `git:file://` host-heuristic interferes, use a `--auth bearer` override; `file://` clone needs `gitfetch`'s `protocol.file.allow=user` pin, which it already sets.)
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement `repo.go`.** Imports: `context`, `fmt`, `io`, `os`, `time`, `github.com/spf13/cobra`, `internal/gitfetch`, `internal/sourceserr`, `internal/cli/exit`, `internal/cli/localpkg/{source,store,discover}`. Add `func init() { rootCmd.AddCommand(newRepoCmd()) }`. SPDX.
- [ ] **Step 4: Run → PASS** `make test-unit-pkg PKG=./cmd/ach-cli/...` (or host-go).
- [ ] **Step 5: Build the binary + smoke** `./scripts/dev.sh go build ./cmd/ach-cli` ; `go vet ./...`.
- [ ] **Step 6: Boundary check** `./scripts/dev.sh go list -deps ./cmd/ach-cli | grep -E 'k8s.io/api|controller-runtime' || echo CLEAN`.
- [ ] **Step 7: Commit** `feat(cli): add 'ach-cli repo' commands (add/list/remove/update)`.

---

## Task 5: Gate
- [ ] `make test-unit` → PASS (new localpkg + cmd packages green).
- [ ] `make qa-lint-changed` (or `qa-lint`) → PASS (SPDX on every new file; no unused imports). If `goconst`/`gosec` fire on new short literals, prefer named consts (no pre-existing exclusion to inherit here).
- [ ] `go list -deps ./cmd/ach-cli` → no `k8s.io/api`/controller-runtime.
- [ ] NO envtest/e2e needed — this task adds only `internal/cli/*` + `cmd/ach-cli/*` (the e2e-gated controller/sources/api/helm are untouched). Confirm `git diff --name-only main..HEAD` lists only those paths.

---

## Self-review notes
- **Scope:** 2.1 delivers register + list with capability detection. `repo add` supports github/git (local deferred). Install/uninstall (`plugin`/`skill` commands via `manager`) is **Phase 2.2**; the `env` reorg is **Phase 2.3**.
- **No placeholders:** each package's API is fully specified; tests use local fixtures (no network).
- **Type consistency:** `store.Capability` is the shared shape returned by `discover.Detect` and stored in `RepoEntry.Provides`.
- **Boundary:** every new import is stdlib / `sigs.k8s.io/yaml` (via contentkit) / existing k8s-free `internal/{gitfetch,contentkit,sourceserr,cli/*}` — `ach-cli` stays k8s-free.
