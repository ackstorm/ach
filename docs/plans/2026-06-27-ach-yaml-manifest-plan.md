# `ach.yaml` Project Hydrate Manifest — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a committed, secret-free `ach.yaml` project manifest listing the Environments a project hydrates, so a teammate runs bare `ach-cli env hydrate` and the workspace hydrates with their own credential; plus `ach-cli env save` to derive `ach.yaml` from realized hydrate state.

**Architecture:** A new leaf package `internal/cli/achfile` owns the file format (parse + serialize + validate). A new `env save` cobra subcommand derives the manifest from existing `.ach/<env>/` state (reusing `loadAllWorkspaceStates`). The hydrate command gains a thin dispatch: when no env is specified (no positional arg, no `ACH_ENVIRONMENT`), it loads `ach.yaml` and calls the **existing** per-env engine entrypoint (`runHydrateEngine`) once per listed env, best-effort. The hydrate engine, state model, authz, and conflict policy are untouched.

**Tech Stack:** Go 1.26, cobra, `gopkg.in/yaml.v3` (with `dec.KnownFields(true)` strict decode — matching `internal/cli/config/config.go`).

## Global Constraints

- Go 1.26. No host Go toolchain — every Go command routes via `make` / `./scripts/dev.sh` (devtools container). Never run bare `go`/`gofmt`.
- Every new `*.go` file MUST start with `// SPDX-License-Identifier: Apache-2.0` (pre-push gate enforces; `make fix-spdx` prepends if missing).
- YAML: use `gopkg.in/yaml.v3` with `dec.KnownFields(true)` for strict unknown-field rejection (mirror `internal/cli/config/config.go:192-193`). Do NOT use `sigs.k8s.io/yaml` (admin-surface only).
- Manifest filename is exactly `ach.yaml`, at the workspace root (cwd, or `--output` override). Project-scoped only — `--global` never reads/writes it.
- Manifest is hub-agnostic: env **names** only, no hub URL, no credentials, no per-env runtime flags (v1). Strict decode rejects unknown keys.
- New package name is `internal/cli/achfile` — NOT `internal/cli/manifest` (that is the unrelated server hydrate-response manifest).
- Target/platform ids are the SAME canonical strings from the adapter registry (`claude-code`, `codex`, `gemini-cli`, `opencode`, `pimono`); aliases (`claude`, `gemini`, …) are accepted on input and resolved by the existing `hydrate.ResolvePlatform`. `env save` EMITS canonical ids (read from `state.File.Adapter.ID`).
- Back-compat is absolute: `ach-cli env hydrate <name>` and `ACH_ENVIRONMENT=…` behavior must be byte-for-byte unchanged. The manifest path only activates when BOTH are absent.
- Multi-env hydrate is best-effort: hydrate every listed env that can be hydrated, record failures, continue, print a per-env summary, exit non-zero if any failed.
- E2E is a local-only gate (`make e2e-full`); run it before merging changes under `cmd/ach-cli/`, `internal/cli/`, or `test/e2e/`.

### Key existing symbols (verified, for reference)

- `cmd/ach-cli/cmd/hydrate.go:326` `func runHydrate(cmd *cobra.Command, in hydrateInputs) error`
- `cmd/ach-cli/cmd/hydrate.go:413` dispatch tail: `runErr = runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv)` (inside the `else` of the `--raw` branch)
- `cmd/ach-cli/cmd/hydrate.go:370-391` effective-env resolution + empty-env hard error
- `cmd/ach-cli/cmd/hydrate.go` `func runHydrateEngine(cmd *cobra.Command, in hydrateInputs, baseURL, bearer, effectiveEnv string) error` — resolves platforms, loops, renders summary, returns error. **This is the per-env entrypoint the manifest loop reuses.**
- `hydrateInputs` struct fields include `environment`, `envEnvironment`, `platform` (`--target`), `envPlatform` (`ACH_PLATFORM`), `output`, `global`, `raw`.
- `cmd/ach-cli/cmd/env.go:79` `func newEnvCmd() *cobra.Command` → `parent.AddCommand(newEnvListCmd(), newEnvDescribeCmd(), newHydrateCmd(), newEnvStatusCmd(), newUninstallCmd())`
- `cmd/ach-cli/cmd/list.go:164` `func loadAllWorkspaceStates(cwd string) ([]*state.File, error)` — enumerates `.ach/<env>/`, loads every `state-*.json`. Returns `[]*state.File`.
- `internal/cli/state/state.go:20` `type File struct { … Environment string; Adapter AdapterSection … }`; `AdapterSection{ ID string; … }` — `Adapter.ID` is the canonical platform id.
- `internal/cli/config/config.go:192-193` strict-decode pattern (`yaml.NewDecoder` + `dec.KnownFields(true)`).
- `internal/cli/gitignore/gitignore.go:40` `TopLevelEntry(rel string) string`; `commit.go:1331` `step12bGitignore` seeds `[]string{".ach/"}` then adds per-written-file top-level entries. `ach.yaml` is never a written file, so it is never added — Task 3 adds a guard test confirming this.

---

### Task 1: `internal/cli/achfile` package — parse, validate, serialize

**Files:**
- Create: `internal/cli/achfile/achfile.go`
- Create: `internal/cli/achfile/achfile_test.go`

**Interfaces:**
- Consumes: nothing (leaf package). Stdlib + `gopkg.in/yaml.v3`.
- Produces (later tasks rely on these exact signatures):
  - `const FileName = "ach.yaml"`
  - `type Manifest struct { Version int; Environments []Entry }`
  - `type Entry struct { Name string; Targets []string }`
  - `func Path(dir string) string` — `filepath.Join(dir, FileName)`
  - `func Load(dir string) (*Manifest, error)` — reads `<dir>/ach.yaml`; returns a wrapped `os.ErrNotExist` if the file is absent (callers test with `errors.Is(err, os.ErrNotExist)`); returns `ErrParse`-wrapped error on malformed/invalid content.
  - `func (m *Manifest) WriteTo(dir string) error` — writes `<dir>/ach.yaml` deterministically.
  - `var ErrParse = errors.New("ach.yaml parse error")`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/achfile/achfile_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package achfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/achfile"
)

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	body := "version: 1\n" +
		"environments:\n" +
		"  - name: team-shared\n" +
		"    targets: [claude-code, codex]\n" +
		"  - name: project-x\n"
	if err := os.WriteFile(filepath.Join(dir, "ach.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := achfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Version != 1 || len(m.Environments) != 2 {
		t.Fatalf("got %+v", m)
	}
	if m.Environments[0].Name != "team-shared" ||
		len(m.Environments[0].Targets) != 2 ||
		m.Environments[1].Name != "project-x" ||
		len(m.Environments[1].Targets) != 0 {
		t.Fatalf("entries wrong: %+v", m.Environments)
	}
}

func TestLoad_Absent_IsErrNotExist(t *testing.T) {
	_, err := achfile.Load(t.TempDir())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestLoad_RejectsBadContent(t *testing.T) {
	cases := map[string]string{
		"unknown version":  "version: 2\nenvironments:\n  - name: a\n",
		"empty envs":       "version: 1\nenvironments: []\n",
		"missing name":     "version: 1\nenvironments:\n  - targets: [codex]\n",
		"duplicate name":   "version: 1\nenvironments:\n  - name: a\n  - name: a\n",
		"unknown key":      "version: 1\nbogus: true\nenvironments:\n  - name: a\n",
		"unknown entrykey": "version: 1\nenvironments:\n  - name: a\n    bogus: 1\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "ach.yaml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := achfile.Load(dir); !errors.Is(err, achfile.ErrParse) {
				t.Fatalf("%s: want ErrParse, got %v", label, err)
			}
		})
	}
}

func TestWriteTo_DeterministicAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	m := &achfile.Manifest{
		Version: 1,
		Environments: []achfile.Entry{
			{Name: "zeta", Targets: []string{"codex", "claude-code"}},
			{Name: "alpha"},
		},
	}
	if err := m.WriteTo(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "ach.yaml"))
	got := string(raw)
	// Envs sorted by name (alpha before zeta); targets sorted within an entry.
	wantOrder := "  - name: alpha\n"
	idxAlpha, idxZeta := indexOf(got, "name: alpha"), indexOf(got, "name: zeta")
	if idxAlpha < 0 || idxZeta < 0 || idxAlpha > idxZeta {
		t.Fatalf("envs not sorted:\n%s", got)
	}
	if !containsSub(got, "[claude-code, codex]") {
		t.Fatalf("targets not sorted:\n%s", got)
	}
	_ = wantOrder
	// Round-trip: load it back and compare normalized.
	back, err := achfile.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if back.Environments[0].Name != "alpha" || back.Environments[1].Name != "zeta" {
		t.Fatalf("round-trip order wrong: %+v", back.Environments)
	}
}

func indexOf(s, sub string) int   { return stringsIndex(s, sub) }
func containsSub(s, sub string) bool { return stringsIndex(s, sub) >= 0 }
```

> Note: `stringsIndex` is a placeholder for `strings.Index` / `strings.Contains` — replace the two helper funcs with direct `strings.Index`/`strings.Contains` calls and add `"strings"` to the test imports in Step 3 when you make it compile. (Kept inline here so the assertions read clearly.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test-unit-pkg PKG=./internal/cli/achfile`
Expected: FAIL — `package achfile is not in std` / `undefined: achfile`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/achfile/achfile.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Package achfile parses and serializes ach.yaml, the committed, secret-free
// project manifest that declares which ACH Environments (and optional adapter
// targets) a project hydrates. It is a pure file-format leaf: no network, no
// credential, no hub binding. Distinct from internal/cli/manifest, which is
// the server hydrate-response manifest.
package achfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the fixed manifest filename at the workspace root.
const FileName = "ach.yaml"

// ErrParse wraps any malformed-or-invalid ach.yaml. Callers test with
// errors.Is(err, ErrParse). An ABSENT file is reported as os.ErrNotExist
// instead (errors.Is(err, os.ErrNotExist)), so "no manifest" is
// distinguishable from "broken manifest".
var ErrParse = errors.New("ach.yaml parse error")

// Manifest is the decoded ach.yaml.
type Manifest struct {
	Version      int     `yaml:"version"`
	Environments []Entry `yaml:"environments"`
}

// Entry is one Environment the project hydrates. Targets are adapter ids
// (canonical or alias); empty means "autodetect at hydrate time".
type Entry struct {
	Name    string   `yaml:"name"`
	Targets []string `yaml:"targets,omitempty"`
}

// Path returns <dir>/ach.yaml.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load reads and validates <dir>/ach.yaml.
func Load(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		return nil, err // includes os.ErrNotExist for an absent file
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.Version != 1 {
		return fmt.Errorf("unsupported version %d (only version 1 is supported)", m.Version)
	}
	if len(m.Environments) == 0 {
		return errors.New("environments must list at least one entry")
	}
	seen := map[string]bool{}
	for i, e := range m.Environments {
		if strings.TrimSpace(e.Name) == "" {
			return fmt.Errorf("environments[%d]: name is required", i)
		}
		if seen[e.Name] {
			return fmt.Errorf("duplicate environment name %q", e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

// WriteTo serializes the manifest to <dir>/ach.yaml deterministically:
// environments sorted by name, targets sorted within each entry. A header
// comment documents the no-secrets contract.
func (m *Manifest) WriteTo(dir string) error {
	out := &Manifest{Version: 1, Environments: make([]Entry, len(m.Environments))}
	copy(out.Environments, m.Environments)
	sort.Slice(out.Environments, func(i, j int) bool {
		return out.Environments[i].Name < out.Environments[j].Name
	})
	for i := range out.Environments {
		t := append([]string(nil), out.Environments[i].Targets...)
		sort.Strings(t)
		out.Environments[i].Targets = t
	}
	body, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	header := "# ach.yaml — committed. Declares which ACH Environments this project hydrates.\n" +
		"# Contains NO secrets. Each developer hydrates with their own credential and\n" +
		"# must have access to each Environment (server-side authz is unchanged).\n"
	return os.WriteFile(Path(dir), append([]byte(header), body...), 0o644)
}
```

- [ ] **Step 4: Make the test compile and pass**

Replace the `stringsIndex`/helper stubs in the test with direct `strings.Index`/`strings.Contains` calls and add `"strings"` to the test imports. Then:

Run: `make test-unit-pkg PKG=./internal/cli/achfile`
Expected: PASS (all subtests).

- [ ] **Step 5: Lint + commit**

Run: `make qa-lint-changed`
Expected: clean.

```bash
git add internal/cli/achfile/achfile.go internal/cli/achfile/achfile_test.go
git commit -m "feat(achfile): ach.yaml project manifest parse + serialize"
```

---

### Task 2: `ach-cli env save` — derive `ach.yaml` from hydrate state

**Files:**
- Create: `cmd/ach-cli/cmd/save.go`
- Create: `cmd/ach-cli/cmd/save_test.go`
- Modify: `cmd/ach-cli/cmd/env.go:79-99` (register `newEnvSaveCmd()`)
- Modify: `CLAUDE.md` (env verb list: add `save`)

**Interfaces:**
- Consumes: `achfile.Manifest`, `achfile.Entry`, `(*Manifest).WriteTo` (Task 1); `loadAllWorkspaceStates(cwd string) ([]*state.File, error)` (existing, `list.go:164`); `state.File.Environment`, `state.File.Adapter.ID`.
- Produces: `func newEnvSaveCmd() *cobra.Command`; `func deriveManifest(cwd string) (*achfile.Manifest, error)`; `var errNothingHydrated = …`.

- [ ] **Step 1: Write the failing test**

Create `cmd/ach-cli/cmd/save_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// seedState writes a per-platform state file under .ach/<env>/state-<platform>.json
// with the given environment + adapter id, the two fields deriveManifest reads.
func seedState(t *testing.T, cwd, env, platform string) {
	t.Helper()
	dir := filepath.Join(cwd, ".ach", env)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schemaVersion":"3","environment":"` + env +
		`","adapter":{"id":"` + platform + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "state-"+platform+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveManifest_GroupsAndSorts(t *testing.T) {
	cwd := t.TempDir()
	seedState(t, cwd, "team-shared", "codex")
	seedState(t, cwd, "team-shared", "claude-code")
	seedState(t, cwd, "project-x", "claude-code")

	m, err := deriveManifest(cwd)
	if err != nil {
		t.Fatalf("deriveManifest: %v", err)
	}
	if len(m.Environments) != 2 {
		t.Fatalf("want 2 envs, got %+v", m.Environments)
	}
	if m.Environments[0].Name != "project-x" || m.Environments[1].Name != "team-shared" {
		t.Fatalf("envs not sorted: %+v", m.Environments)
	}
	ts := m.Environments[1].Targets // team-shared
	if len(ts) != 2 || ts[0] != "claude-code" || ts[1] != "codex" {
		t.Fatalf("team-shared targets wrong/unsorted: %v", ts)
	}
}

func TestDeriveManifest_NothingHydrated(t *testing.T) {
	if _, err := deriveManifest(t.TempDir()); !errors.Is(err, errNothingHydrated) {
		t.Fatalf("want errNothingHydrated, got %v", err)
	}
}

func TestEnvSave_WritesFile(t *testing.T) {
	cwd := t.TempDir()
	seedState(t, cwd, "demo", "claude-code")

	// Run the command with cwd switched to the temp workspace.
	restore := chdir(t, cwd)
	defer restore()

	if _, _, err := executeCommand(t, newEnvSaveCmd()); err != nil {
		t.Fatalf("env save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "ach.yaml")); err != nil {
		t.Fatalf("ach.yaml not written: %v", err)
	}
}

// chdir switches the process cwd for the test and returns a restore func.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(prev) }
}
```

> `executeCommand(t, cmd, args...)` is the shared driver at `cmd/ach-cli/cmd/helpers_test.go:27`. If its signature differs (e.g. returns `(stdout, stderr string, err error)`), adapt the call — check `helpers_test.go` before writing. The `chdir` helper is local to keep the test self-contained; if the package already has one, reuse it instead of redefining.

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd`
Expected: FAIL — `undefined: deriveManifest`, `undefined: errNothingHydrated`, `undefined: newEnvSaveCmd`.

- [ ] **Step 3: Write the implementation**

Create `cmd/ach-cli/cmd/save.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/achfile"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// errNothingHydrated is returned by deriveManifest when the workspace has no
// hydrated environments to derive ach.yaml from.
var errNothingHydrated = errors.New("nothing hydrated in this workspace yet")

// newEnvSaveCmd builds `ach-cli env save`: derive ach.yaml from the realized
// hydrate state under .ach/<env>/ and write it to the workspace root.
func newEnvSaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "save",
		Short: "Write ach.yaml from the environments already hydrated in this workspace",
		Long: "Derives a committed ach.yaml from the environments hydrated under " +
			".ach/, so a teammate can clone and run `ach-cli env hydrate` with no " +
			"arguments. ach.yaml contains environment names only — no secrets.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			m, err := deriveManifest(cwd)
			if errors.Is(err, errNothingHydrated) {
				return &exit.CodedError{
					Code: exit.General,
					Msg: "nothing hydrated in this workspace yet — run " +
						"`ach-cli env hydrate <name>` first, then `ach-cli env save`",
				}
			}
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			if err := m.WriteTo(cwd); err != nil {
				return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Wrote %s (%d environment(s)):\n", achfile.FileName, len(m.Environments))
			for _, e := range m.Environments {
				if len(e.Targets) == 0 {
					fmt.Fprintf(out, "  - %s (targets: autodetect)\n", e.Name)
				} else {
					fmt.Fprintf(out, "  - %s (targets: %v)\n", e.Name, e.Targets)
				}
			}
			fmt.Fprintf(out, "Commit %s so teammates can `ach-cli env hydrate`.\n", achfile.FileName)
			return nil
		},
	}
}

// deriveManifest builds an achfile.Manifest from the hydrate state under
// <cwd>/.ach/. Environments are grouped by state.File.Environment; targets are
// the sorted-unique canonical adapter ids (state.File.Adapter.ID). Returns
// errNothingHydrated when no hydrated environment is found.
func deriveManifest(cwd string) (*achfile.Manifest, error) {
	files, err := loadAllWorkspaceStates(cwd)
	if err != nil {
		return nil, err
	}
	byEnv := map[string]map[string]bool{}
	for _, f := range files {
		if f == nil || f.Environment == "" {
			continue
		}
		if byEnv[f.Environment] == nil {
			byEnv[f.Environment] = map[string]bool{}
		}
		if f.Adapter.ID != "" {
			byEnv[f.Environment][f.Adapter.ID] = true
		}
	}
	if len(byEnv) == 0 {
		return nil, errNothingHydrated
	}
	names := make([]string, 0, len(byEnv))
	for n := range byEnv {
		names = append(names, n)
	}
	sort.Strings(names)
	entries := make([]achfile.Entry, 0, len(names))
	for _, n := range names {
		targets := make([]string, 0, len(byEnv[n]))
		for id := range byEnv[n] {
			targets = append(targets, id)
		}
		sort.Strings(targets)
		entries = append(entries, achfile.Entry{Name: n, Targets: targets})
	}
	return &achfile.Manifest{Version: 1, Environments: entries}, nil
}
```

- [ ] **Step 4: Register the subcommand**

In `cmd/ach-cli/cmd/env.go`, add `newEnvSaveCmd()` to the `parent.AddCommand(...)` call in `newEnvCmd()` (place it after `newEnvStatusCmd()`):

```go
	parent.AddCommand(
		newEnvListCmd(),
		newEnvDescribeCmd(),
		newHydrateCmd(),
		newEnvStatusCmd(),
		newEnvSaveCmd(),
		newUninstallCmd(),
	)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd`
Expected: PASS (including the three new `save_test.go` tests).

- [ ] **Step 6: Update CLAUDE.md (docs in same commit)**

In `CLAUDE.md`, find the `ach-cli` verb summary line (the "User CLI = separate `ach-cli` binary" paragraph listing `login`/`logout`/`whoami`/`config`/`env`/`keys`/`admin`/`runtime` and the `env` workspace verbs `hydrate`/`status`/`uninstall`). Add `save` to the `env` workspace verbs, e.g. change `hydrate`/`status`/`uninstall` to `hydrate`/`status`/`save`/`uninstall`, and append one clause: "`env save` writes a committed `ach.yaml` (env names + targets) so a teammate's bare `ach-cli env hydrate` reproduces the workspace."

- [ ] **Step 7: Lint + commit**

Run: `make qa-lint-changed`
Expected: clean.

```bash
git add cmd/ach-cli/cmd/save.go cmd/ach-cli/cmd/save_test.go cmd/ach-cli/cmd/env.go CLAUDE.md
git commit -m "feat(cli): ach-cli env save derives ach.yaml from hydrate state"
```

---

### Task 3: bare `ach-cli env hydrate` reads `ach.yaml` (multi-env best-effort)

**Files:**
- Modify: `cmd/ach-cli/cmd/hydrate.go` (relax empty-env gate; add manifest dispatch + `runHydrateManifest`)
- Create: `cmd/ach-cli/cmd/hydrate_manifest_test.go`
- Modify: `references/troubleshooting.md` (wrong-hub entry)
- Modify: `CLAUDE.md` (bare-hydrate-reads-ach.yaml note)

**Interfaces:**
- Consumes: `achfile.Load(dir) (*Manifest, error)`, `achfile.Manifest.Environments`, `achfile.Entry.{Name,Targets}` (Task 1); existing `runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv string) error`; `hydrateInputs` fields `output`, `platform`, `envPlatform`, `environment`, `envEnvironment`.
- Produces: `func runHydrateManifest(cmd *cobra.Command, in hydrateInputs, baseURL, bearer string) error`.

**Resolution rules implemented here:**
- Env source: positional `<name>` → `ACH_ENVIRONMENT` → `ach.yaml` → original required-arg error. A non-empty positional/env keeps the **single-env** path unchanged.
- Per-env target: `--target` flag → `ACH_PLATFORM` → manifest entry `targets` → autodetect. (The entry only fills in when both the flag and `ACH_PLATFORM` are empty.)
- Multi-env: best-effort; collect `(env, err)`; print a summary table; exit non-zero if any failed.

- [ ] **Step 1: Write the failing test**

Create `cmd/ach-cli/cmd/hydrate_manifest_test.go`. This drives `runHydrateManifest` with a stubbed engine seam so no network is needed. The engine seam is the package-level `hydrateRunFn` (`hydrate.go:101`, `= hydrate.Run`); but `runHydrateManifest` calls `runHydrateEngine`, which builds wiring and calls `hydrateRunFn`. To unit-test best-effort aggregation without a real server, stub at the lowest seam the test can reach — `hydrateRunFn` — and assert per-env invocation + exit behavior. Verify the seam name in `hydrate.go` before writing; if a narrower seam exists for env-level stubbing, prefer it.

```go
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/hydrate"
)

func writeManifest(t *testing.T, cwd, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cwd, "ach.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubEngine replaces hydrateRunFn for the test, recording each env it is
// asked to hydrate and failing the envs named in failFor.
func stubEngine(t *testing.T, failFor map[string]bool, seen *[]string) {
	t.Helper()
	prev := hydrateRunFn
	hydrateRunFn = func(_ context.Context, opts hydrate.Opts) (hydrate.Result, error) {
		*seen = append(*seen, opts.Environment)
		if failFor[opts.Environment] {
			return hydrate.Result{}, &os.PathError{Op: "hydrate", Path: opts.Environment, Err: os.ErrPermission}
		}
		return hydrate.Result{Environment: opts.Environment, PlatformID: opts.Platform}, nil
	}
	t.Cleanup(func() { hydrateRunFn = prev })
}

func TestRunHydrateManifest_BestEffort(t *testing.T) {
	cwd := t.TempDir()
	writeManifest(t, cwd, "version: 1\nenvironments:\n  - name: a\n    targets: [claude-code]\n  - name: b\n    targets: [claude-code]\n")
	restore := chdir(t, cwd)
	defer restore()

	var seen []string
	stubEngine(t, map[string]bool{"b": true}, &seen)

	in := hydrateInputs{output: cwd}
	err := runHydrateManifest(testCmd(t), in, "https://hub.example", "pk_x")
	if err == nil {
		t.Fatal("want non-zero exit because env b failed, got nil")
	}
	if len(seen) != 2 || seen[0] != "a" || seen[1] != "b" {
		t.Fatalf("both envs should be attempted in order: %v", seen)
	}
}

func TestRunHydrateManifest_AllOK(t *testing.T) {
	cwd := t.TempDir()
	writeManifest(t, cwd, "version: 1\nenvironments:\n  - name: a\n    targets: [claude-code]\n")
	restore := chdir(t, cwd)
	defer restore()

	var seen []string
	stubEngine(t, nil, &seen)

	if err := runHydrateManifest(testCmd(t), hydrateInputs{output: cwd}, "https://hub.example", "pk_x"); err != nil {
		t.Fatalf("all-ok should exit zero: %v", err)
	}
}

func TestRunHydrateManifest_NoManifest_RequiredArgError(t *testing.T) {
	cwd := t.TempDir()
	restore := chdir(t, cwd)
	defer restore()
	err := runHydrateManifest(testCmd(t), hydrateInputs{output: cwd}, "https://hub.example", "pk_x")
	if err == nil || !strings.Contains(err.Error(), "ach.yaml") {
		t.Fatalf("absent manifest should error mentioning ach.yaml, got %v", err)
	}
}
```

> `testCmd(t)` is a placeholder for whatever minimal `*cobra.Command` the package's tests construct (with `OutOrStdout`/`ErrOrStderr` wired to buffers). Check `helpers_test.go` / `hydrate_test.go` for the existing constructor (e.g. `executeHydrateEngine`'s setup) and reuse it; do not invent a new one if one exists.

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd`
Expected: FAIL — `undefined: runHydrateManifest`.

- [ ] **Step 3: Add `runHydrateManifest` and the dispatch**

In `cmd/ach-cli/cmd/hydrate.go`, add the function (near `runHydrateEngine`):

```go
// runHydrateManifest drives the manifest path: with no positional <name> and
// no ACH_ENVIRONMENT, it loads ach.yaml from the workspace root and hydrates
// each listed environment best-effort (a failing env is recorded and the run
// continues). It reuses runHydrateEngine per env, so each env hydrates exactly
// as a standalone `env hydrate <name>` would. Exits non-zero if any env failed.
func runHydrateManifest(cmd *cobra.Command, in hydrateInputs, baseURL, bearer string) error {
	root := in.output
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
		}
		root = wd
	}
	m, err := achfile.Load(root)
	if errors.Is(err, os.ErrNotExist) {
		return &exit.CodedError{
			Code: exit.General,
			Msg: "<name> positional argument is required: the hydrate engine " +
				"namespaces state by environment (.ach/<name>/); pass <name>, set " +
				"ACH_ENVIRONMENT, or create ach.yaml with `ach-cli env save`",
		}
	}
	if err != nil {
		return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
	}

	type envResult struct {
		name string
		err  error
	}
	results := make([]envResult, 0, len(m.Environments))
	for _, e := range m.Environments {
		perEnv := in
		// Target precedence: --target flag and ACH_PLATFORM both override the
		// manifest entry; the entry only fills in when neither is set.
		if perEnv.platform == "" && perEnv.envPlatform == "" && len(e.Targets) > 0 {
			perEnv.platform = strings.Join(e.Targets, ",")
		}
		runErr := runHydrateEngine(cmd, perEnv, baseURL, bearer, e.Name)
		results = append(results, envResult{name: e.Name, err: runErr})
	}

	failed := 0
	stderr := cmd.ErrOrStderr()
	fmt.Fprintf(stderr, "\nHydrated %d environment(s) from ach.yaml:\n", len(results))
	for _, r := range results {
		if r.err != nil {
			failed++
			fmt.Fprintf(stderr, "  - %s → FAIL: %v\n", r.name, r.err)
		} else {
			fmt.Fprintf(stderr, "  - %s → OK\n", r.name)
		}
	}
	if failed > 0 {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  fmt.Sprintf("%d of %d environment(s) failed to hydrate", failed, len(results)),
		}
	}
	return nil
}
```

Ensure `hydrate.go` imports `"errors"`, `"os"`, `"strings"`, and `"github.com/ackstorm/ach/internal/cli/achfile"` (add any missing; several are likely already present — check the import block).

- [ ] **Step 4: Relax the empty-env gate and wire the dispatch**

In `runHydrate` (`hydrate.go`), the current dispatch tail (~line 413) is:

```go
	} else {
		runErr = runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv)
	}
```

Change the `else` branch to route empty-env to the manifest path:

```go
	} else if effectiveEnv != "" {
		runErr = runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv)
	} else {
		runErr = runHydrateManifest(cmd, in, baseURL, bearer)
	}
```

Then **relax the empty-env hard error** at `hydrate.go:382-391` so an empty env no longer returns early before reaching the dispatch (the manifest path now owns the "no env + no manifest" error). Replace the two empty-env guards:

```go
	if prefix == keys.PrefixPk && effectiveEnv == "" {
		return &exit.CodedError{ Code: exit.General, Msg: "<name> positional argument is required when using a pk- key (CLI-06 / spec §5.7)" }
	}
	if !in.raw && effectiveEnv == "" {
		return &exit.CodedError{ Code: exit.General, Msg: "<name> positional argument is required: ..." }
	}
```

with a single guard that only fires for `--raw` (which has no manifest support) and otherwise lets the dispatch handle it:

```go
	// --raw has no manifest path; it still requires an explicit env.
	if in.raw && effectiveEnv == "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg:  "--raw requires an explicit <name> (or ACH_ENVIRONMENT); it has no ach.yaml manifest path",
		}
	}
```

The pk-/empty combination is now handled per-env inside the engine (each manifest entry supplies a non-empty env; a bare run with no manifest hits the required-arg error in `runHydrateManifest`). Verify by reading the surrounding lines that no other code between the gate and the dispatch assumes `effectiveEnv != ""`.

- [ ] **Step 5: Add the gitignore guard test**

Append to `cmd/ach-cli/cmd/hydrate_manifest_test.go` a test asserting the managed `.gitignore` block never lists `ach.yaml` (it is committed, not a written artifact):

```go
func TestGitignore_DoesNotIgnoreAchYaml(t *testing.T) {
	// ach.yaml is a committed file at the workspace root; the hydrate-managed
	// gitignore block only lists .ach/ and written adapter dirs. TopLevelEntry
	// is fed only render-written targets, never ach.yaml, so it can never be
	// added. This guards against a future change that sweeps cwd files in.
	if got := gitignore.TopLevelEntry("ach.yaml"); got == "ach.yaml" || got == "ach.yaml/" {
		t.Fatalf("ach.yaml must not become a managed gitignore entry, got %q", got)
	}
}
```

Add `"github.com/ackstorm/ach/internal/cli/gitignore"` to the test imports. (Note: `TopLevelEntry("ach.yaml")` returning `"ach.yaml"` would only matter if `ach.yaml` were ever passed as a written target; this test documents the invariant and fails loudly if someone later routes it through the block.)

- [ ] **Step 6: Run the tests to verify they pass**

Run: `make test-unit-pkg PKG=./cmd/ach-cli/cmd`
Expected: PASS — new manifest tests green AND every pre-existing hydrate test still green (back-compat: a positional `<name>` never touches the manifest path).

- [ ] **Step 7: Update docs (same commit)**

In `references/troubleshooting.md`, add an entry under the generic-failures section:

```markdown
### ❌ `ach-cli env hydrate` (bare, via ach.yaml) → an env "not found" though it exists
`ach.yaml` is **hub-agnostic** — it lists Environment names only, never which
hub they live on. Bare `env hydrate` resolves the hub from your **active
profile**. If your active profile points at a different hub than the one where
those Environments live, the names won't resolve and you get a per-env
`FAIL: … not found` in the summary (best-effort: other envs still hydrate).
Fix: point your active profile at the right hub (`ach-cli config use <profile>`
or `ach-cli login` against the correct hub), then re-run `ach-cli env hydrate`.
```

In `CLAUDE.md`, in the same `ach-cli` paragraph touched in Task 2, add one clause: "bare `ach-cli env hydrate` (no `<name>`, no `ACH_ENVIRONMENT`) reads a committed `ach.yaml` and hydrates each listed Environment best-effort (exit ≠0 if any fails)."

- [ ] **Step 8: Lint + commit**

Run: `make qa-lint-changed`
Expected: clean.

```bash
git add cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/hydrate_manifest_test.go references/troubleshooting.md CLAUDE.md
git commit -m "feat(cli): bare env hydrate reads ach.yaml (multi-env best-effort)"
```

---

### Task 4: E2E round-trip — clone-and-go

**Files:**
- Modify or create: a test under `test/e2e/` that exercises the init→clone→hydrate round-trip (follow the existing stdlib `testing` e2e pattern; consult `test/e2e/README.md` and an existing `test/e2e/*_test.go` for the harness — cluster bring-up, `ach-cli` invocation, env fixtures).

**Interfaces:**
- Consumes: a running kind cluster with the e2e Environment fixtures (`test/e2e/cluster/05-environment/`), the built `ach-cli`, `ach-cli env hydrate`, `ach-cli env save`.

- [ ] **Step 1: Write the round-trip test**

Add an e2e test that, against the kept e2e cluster (see `test/e2e/README.md` for SSO mint + host-rewrite credential setup used by other CLI e2e tests):
1. In a temp workspace, hydrate two fixture Environments: `ach-cli env hydrate <envA>` then `ach-cli env hydrate <envB>` (use the synced fixture env names from `test/e2e/cluster/05-environment/`).
2. Run `ach-cli env save`; assert `ach.yaml` exists and lists both envs.
3. Delete `.ach/` (simulate a fresh clone keeping only the committed `ach.yaml`).
4. Run bare `ach-cli env hydrate`; assert exit 0 and that both envs' content/adapter dirs are re-materialized (assert the same files the standalone hydrate produced).

Mirror the assertion helpers an existing hydrate e2e test uses (e.g. file-count / projected-dir assertions). Keep the test behind the same build tag / suite entry as the other CLI e2e tests.

- [ ] **Step 2: Run the focused e2e**

Run: `make e2e-focus RUN='<TestNameYouAdded>'`
Expected: PASS (cluster kept up after the run).

- [ ] **Step 3: Full e2e gate**

Run: `make e2e-full`
Expected: PASS (final gate; cluster kept up — reclaim with `make cluster-down`).

- [ ] **Step 4: Commit**

```bash
git add test/e2e/<file>
git commit -m "test(e2e): ach.yaml init→clone→hydrate round-trip"
```

---

## Self-Review

**Spec coverage:**
- Schema + parse/validate (version, non-empty, name required, dup names, unknown keys) → Task 1. ✅
- `env save` derive-from-state, sorted/deterministic, nothing-hydrated error, empty-targets edge → Task 2. ✅
- Bare hydrate manifest mode, env-source precedence (positional > `ACH_ENVIRONMENT` > `ach.yaml` > error), target precedence, best-effort + summary + non-zero exit, back-compat → Task 3. ✅
- Hub-agnostic (names only) + wrong-hub troubleshooting → Task 3 docs. ✅
- Gitignore: `ach.yaml` not ignored (guard test, no prod change) → Task 3 Step 5. ✅
- Docs in same commit (CLAUDE.md, troubleshooting, examples) → Tasks 2/3. (`examples/README.md` clone-and-go demo is optional polish; add it in Task 3 Step 7 if the demo section warrants it.) ✅
- E2E round-trip → Task 4. ✅

**Placeholder scan:** Test helpers flagged as "verify against `helpers_test.go`" (`executeCommand`, `testCmd`, `chdir`, `stringsIndex`) are explicit adaptation points with named existing sources, not silent TODOs — the implementer reconciles them in the failing-test step. The engine stub seam (`hydrateRunFn`) is named with its file:line. No `add error handling`-style placeholders remain.

**Type consistency:** `achfile.Manifest{Version int; Environments []Entry}`, `Entry{Name string; Targets []string}`, `Load(dir) (*Manifest, error)`, `(*Manifest).WriteTo(dir) error`, `FileName`, `ErrParse` are used identically in Tasks 2 and 3. `runHydrateManifest(cmd, in, baseURL, bearer)` and `runHydrateEngine(cmd, in, baseURL, bearer, effectiveEnv)` signatures match their definitions. `state.File.Environment` / `state.File.Adapter.ID` match the verified struct. ✅
