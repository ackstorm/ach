# Phase 7: CLI Hydrate Engine + Adapters + Safe Extraction + State - Pattern Map

**Mapped:** 2026-05-29
**Files analyzed:** 41 new / 3 modified
**Analogs found:** 41 / 44 (3 files have no close analog — flagged in "No Analog Found")

## File Classification

Files grouped by W1 → W4 wave per CONTEXT.md D-01. Each file lists role, data flow, and the closest existing analog identified for pattern reuse.

### W1 — Foundation (state + lock + atomic write, ~7 plans)

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/cli/state/state.go` | state file marshaler | file-I/O / transform | `internal/cli/config/config.go` | exact (stdlib + atomic rename + mode discipline) |
| `internal/cli/state/state_test.go` | unit test | file-I/O | `internal/cli/config/config_test.go` | exact |
| `internal/cli/state/atomic.go` | atomic write helper | file-I/O | `internal/cli/config/config.go` `Save` (lines 142-185) | exact |
| `internal/cli/state/atomic_test.go` | unit test | file-I/O | `internal/cli/config/config_test.go` | exact |
| `internal/cli/state/path.go` | `<ach-dir>` resolution | utility / transform | `internal/cli/config/config.go` `Path` (lines 79-88) | exact |
| `internal/cli/state/path_test.go` | unit test | utility | `internal/cli/config/config_test.go` | exact |
| `internal/cli/state/sweep.go` | tmp-sweep + silent-prune | file-I/O / batch | `internal/cachefs/sweep.go` `SweepTmp` (lines 130-157) | exact |
| `internal/cli/state/sweep_test.go` | unit test | file-I/O | `internal/cachefs/sweep_test.go` | exact |
| `internal/cli/state/guard.go` | schema-v2 + same-`<ach-dir>` Environment guard | validation / transform | `internal/cli/config/config.go` `validateDeployments` (lines 241-252) | role-match |
| `internal/cli/state/guard_test.go` | unit test | validation | `internal/cli/config/config_test.go` | exact |
| `internal/cli/state/doc.go` | package doc | n/a | `internal/cachefs/doc.go` | exact |
| `internal/cli/lock/lock.go` | locker interface | utility | `internal/cli/config/config.go` (sentinel-error + interface idiom, lines 54-75) | role-match |
| `internal/cli/lock/lock_unix.go` | `flock(LOCK_EX)` impl (`//go:build !windows`) | file-I/O / blocking | NONE — flagged in "No Analog Found" | no analog |
| `internal/cli/lock/lock_test.go` | unit test | file-I/O | `internal/cachefs/bootstrap_test.go` (skip-on-Windows pattern) | role-match |
| `internal/cli/lock/path.go` | lock path resolver | utility | `internal/cli/config/config.go` `Path` | exact |
| `internal/cli/lock/doc.go` | package doc | n/a | `internal/cachefs/doc.go` | exact |
| `internal/cli/hash/xxh3.go` | xxh3 wrapper | transform / streaming | `internal/credhash/credhash.go` `Hash` (lines 31-38) | exact (HMAC vs xxh3 — same `Hash([]byte) (string, error)` shape) |
| `internal/cli/hash/xxh3_test.go` | unit test | transform | `internal/credhash/credhash_test.go` | exact |
| `internal/cli/hash/doc.go` | package doc | n/a | `internal/credhash/doc.go` | exact |
| `internal/cli/hydrate/commit.go` | 14-step orchestrator skeleton | event-driven / request-response | `cmd/ach-cli/cmd/hydrate.go` `runHydrate` (lines 192-267) | role-match |
| `internal/cli/hydrate/commit_test.go` | unit test | event-driven | `cmd/ach-cli/cmd/hydrate_test.go` | exact |
| `internal/cli/hydrate/flags.go` | `Opts` struct + flag bag | utility | `cmd/ach-cli/cmd/hydrate.go` `hydrateInputs` (lines 166-180) | exact |
| `internal/cli/hydrate/result.go` | summary for `--verbose` | utility / transform | `internal/cli/httpclient/redact.go` (HeaderDump output shape) | role-match |
| `internal/cli/hydrate/doc.go` | package doc | n/a | `internal/cli/doc.go` | exact |
| `internal/cli/exit/exit.go` (MODIFY) | add codes 2/4/5/7 | utility | self (lines 25-47 — extend the const block additively) | exact |

### W2 — Safe extract + collision policy (~3 plans)

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/cli/extract/tar.go` | safe tar policy + gzip stream | streaming / transform | `internal/cachefs/bootstrap.go` (file/dir mode discipline) + stdlib `archive/tar` | partial (no in-repo tar consumer yet) |
| `internal/cli/extract/tar_test.go` | unit test (malicious-archive fixtures) | streaming | `internal/cachefs/bootstrap_test.go` | role-match |
| `internal/cli/extract/limits.go` | bomb-cap env-var parsing | config / transform | `internal/cli/config/config.go` `Path` env-var read pattern | role-match |
| `internal/cli/extract/limits_test.go` | unit test | config | `internal/cli/config/config_test.go` | exact |
| `internal/cli/extract/stage.go` | per-resource staging dir + atomic rename publication | file-I/O / batch | `internal/cachefs/sweep.go` + `internal/cli/config/config.go` `Save` rename pattern | role-match |
| `internal/cli/extract/stage_test.go` | unit test | file-I/O | `internal/cachefs/sweep_test.go` | exact |
| `internal/cli/extract/autoclaim.go` | three-tier cascade (eager → adapter → lazy source) | transform | NONE — flagged in "No Analog Found" | no analog |
| `internal/cli/extract/autoclaim_test.go` | unit test | transform | (uses fake `Adapter` impl) | n/a |
| `internal/cli/extract/doc.go` | package doc | n/a | `internal/cachefs/doc.go` | exact |
| `test/fixtures/malicious-archives/*.tar.gz` + generator | test fixture set | data | NONE — flagged in "No Analog Found" | no analog |

### W3 — 4 adapters in parallel (~5 plans incl. registry + manifest decoder)

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/cli/adapter/adapter.go` | `Adapter` interface | utility | `internal/cli/synthetic/synthetic.go` `Params` + `Gate` (lines 22-100) | role-match (interface + typed-enum shape) |
| `internal/cli/adapter/registry.go` | init-registered registry | utility | `cmd/ach-cli/cmd/hydrate.go` `init()` (line 447-449) + `rootCmd.AddCommand` idiom | role-match |
| `internal/cli/adapter/registry_test.go` | unit test | utility | `internal/cli/synthetic/synthetic_test.go` | exact |
| `internal/cli/adapter/doc.go` | package doc | n/a | `internal/cli/doc.go` | exact |
| `internal/cli/adapter/claudecode/claudecode.go` | pass-through adapter | transform | (reference impl — other 3 reference this) | n/a (first impl) |
| `internal/cli/adapter/claudecode/claudecode_test.go` | unit test | transform | `cmd/ach-cli/cmd/hydrate_test.go` (table-driven) | role-match |
| `internal/cli/adapter/codex/codex.go` | TOML/JSON merge adapter | transform | `internal/cli/adapter/claudecode/claudecode.go` (W3 pre-req) | role-match |
| `internal/cli/adapter/codex/codex_test.go` | unit test | transform | `cmd/ach-cli/cmd/hydrate_test.go` | role-match |
| `internal/cli/adapter/gemini/gemini.go` | merge adapter | transform | `internal/cli/adapter/claudecode/claudecode.go` | role-match |
| `internal/cli/adapter/gemini/gemini_test.go` | unit test | transform | `cmd/ach-cli/cmd/hydrate_test.go` | role-match |
| `internal/cli/adapter/opencode/opencode.go` | merge adapter | transform | `internal/cli/adapter/claudecode/claudecode.go` | role-match |
| `internal/cli/adapter/opencode/opencode_test.go` | unit test | transform | `cmd/ach-cli/cmd/hydrate_test.go` | role-match |
| `internal/cli/manifest/manifest.go` | POST hydrate decoder + schema-version assert | request-response | `internal/cli/httpclient/client.go` `Do` (lines 113-139) | exact |
| `internal/cli/manifest/manifest_test.go` | unit test | request-response | `internal/cli/httpclient/client_test.go` | exact |
| `internal/cli/manifest/doc.go` | package doc | n/a | `internal/cli/doc.go` | exact |
| `cmd/ach-cli/cmd/hydrate.go` (MODIFY) | engine flags + `--raw` short-circuit + dispatch | request-response | self (lines 86-153 cobra + lines 192-267 RunE) | exact |
| `cmd/ach-cli/cmd/hydrate_test.go` (MODIFY) | engine-flag test scenarios | request-response | self | exact |

### W4 — E2E demo + verifier + ROADMAP refresh (~2 plans)

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `test/e2e/cli_hydrate_engine_test.go` | e2e suite (4 platforms × {pk,ek}) | request-response | `test/e2e/cli_login_hydrate_test.go` (lines 37-260) | exact |
| `test/e2e/phase7_helpers_test.go` | helpers + suite guard + temp-XDG seeder | utility | `test/e2e/phase6_helpers_test.go` (lines 38-310) | exact |
| `test/e2e/cli_login_hydrate_test.go` (MODIFY) | add `--raw` to W3-P3 invocation | request-response | self (lines 222-226) | exact (one-line change) |
| `ROADMAP.md` (MODIFY) | slide DIST-*/SC#5 to new Phase 7.1 | docs | self (existing Phase 7 entry) | n/a |
| `.planning/REQUIREMENTS.md` (MODIFY) | DIST-* phase column → 7.1 | docs | self (Traceability table) | n/a |
| `CLAUDE.md` (MODIFY) | new failure-mode entries (schemaVersion mismatch, Environment guard) | docs | self ("Common failure modes" section) | exact |

## Pattern Assignments

### `internal/cli/state/state.go` (state file marshaler, file-I/O / transform)

**Analog:** `internal/cli/config/config.go`

**Why this analog:** State.json v2 is a stdlib-only JSON-marshaled file under `<ach-dir>/state.json`. `config.go` is the exact precedent: stdlib + serializer + atomic publication (`tmp → fsync → rename`) + mode discipline + sentinel errors + injectable seam for tests. Replace YAML with `encoding/json`; replace the `0600`/`0700` mode triad with `0644`/`0755` (state has no plaintext secrets).

**SPDX + package doc pattern** (lines 1-25):
```go
// SPDX-License-Identifier: Apache-2.0

// Package state owns <ach-dir>/state.json — the CLI's local
// state ledger per CLI spec §8.2. Schema v2:
//
//   { schemaVersion: "2", environment: <name>,
//     deployment: <name|"(env)">,
//     prompts: [{target, hash, sourceHash, ...}], ... }
//
// Discipline (mirrors internal/cli/config):
//   - stdlib + encoding/json only — NO yaml, NO log, NO log/slog.
//   - Atomic publication via tmp+fsync+rename in same dir (TOCTOU-safe).
//   - schemaVersion != "2" → exit 5 (CLI spec §8.2). NO v1 reader.
//   - Same-<ach-dir> different-Environment → exit 4 unless --force.
package state
```

**Sentinel error pattern** (`config.go` lines 54-75):
```go
var (
    ErrSchemaMismatch    = errors.New("state: schemaVersion != \"2\"")
    ErrEnvironmentGuard  = errors.New("state: same <ach-dir> bound to a different Environment")
    ErrStateParse        = errors.New("state: parse failed")
)
```

**Atomic write pattern** — copy verbatim from `config.go` `Save` (lines 142-185), substituting `json.Marshal` for `yaml.Marshal` and adding the `fsync(fd)` + `fsync(parent_dir)` calls that STATE-07 mandates (config.go currently omits `fsync` per its file-mode-only contract; state.go MUST add both).

---

### `internal/cli/state/sweep.go` (tmp-sweep + silent-prune, file-I/O / batch)

**Analog:** `internal/cachefs/sweep.go`

**Why this analog:** `SweepTmp` (cachefs/sweep.go lines 130-157) is the canonical "sweep `<root>/.tmp/` orphans on startup, idempotent, ErrNotExist-tolerant" pattern. STATE-01 + SAFE-05 require the same shape against `<ach-dir>/tmp/`.

**Sweep pattern** (cachefs/sweep.go lines 130-157):
```go
func SweepTmp(root string, maxAge time.Duration) error {
    tmp := filepath.Join(root, ".tmp")
    entries, err := os.ReadDir(tmp)
    if err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return nil
        }
        return err
    }
    cutoff := time.Now().Add(-maxAge)
    for _, entry := range entries {
        info, infoErr := entry.Info()
        if infoErr != nil { continue }
        if info.ModTime().Before(cutoff) {
            _ = os.Remove(filepath.Join(tmp, entry.Name()))
        }
    }
    return nil
}
```

State version: drop the `maxAge` cutoff (the spec §6.7 step 2 sweep is unconditional at hydrate-start) and recurse via `os.RemoveAll` because each entry is a `<rand>/` staging subtree, not a single file.

---

### `internal/cli/state/atomic.go` (atomic write helper, file-I/O)

**Analog:** `internal/cli/config/config.go` `Save` (lines 142-185)

**Atomic publication pattern** (config.go lines 156-184):
```go
buf, err := yaml.Marshal(f)
if err != nil { return err }
tmp, err := os.CreateTemp(dir, ".config-*.yaml.tmp")
if err != nil { return err }
tmpName := tmp.Name()
cleanup := func() { _ = os.Remove(tmpName) }
if _, err := tmp.Write(buf); err != nil {
    _ = tmp.Close(); cleanup(); return err
}
if err := tmp.Chmod(0o600); err != nil {
    _ = tmp.Close(); cleanup(); return err
}
if err := tmp.Close(); err != nil { cleanup(); return err }
if err := os.Rename(tmpName, path); err != nil {
    cleanup(); return err
}
return nil
```

**Adaptation for STATE-07** — INSERT two `fsync` calls config.go omits:
1. After `tmp.Write(buf)` and before `tmp.Chmod`: `if err := tmp.Sync(); err != nil { ... }`
2. After `os.Rename`: open parent dir, call `Sync()`, close (POSIX `fsync(parent_dir)`).

---

### `internal/cli/lock/lock_unix.go` (POSIX flock, file-I/O / blocking)

**Analog:** NONE in-repo. Use stdlib `golang.org/x/sys/unix.Flock` directly.

**Reference build-tag layout pattern** (no in-repo build-tagged Go files for the OS-divergent case yet — closest precedent is `internal/cli/config/config.go` `skipModeCheck` lines 262-270 which uses `runtime.GOOS` rather than build tags).

**Recommended file header**:
```go
//go:build !windows

// SPDX-License-Identifier: Apache-2.0

// Package lock — POSIX flock(LOCK_EX) implementation. Windows
// LockFileEx ships in Phase 7.1 alongside the windows-amd64
// goreleaser build (CLI spec §6.7 / D-23).
package lock

import (
    "golang.org/x/sys/unix"
)
```

The interface `lock.go` (no build tag) is the shape; `lock_unix.go` (build-tagged) is the impl. Phase 7.1 adds `lock_windows.go` without touching either.

---

### `internal/cli/hash/xxh3.go` (xxh3 wrapper, transform / streaming)

**Analog:** `internal/credhash/credhash.go` (lines 1-62)

**Why this analog:** Identical role — wrap a single hash primitive with a tight stdlib-shaped `Hash(...) (string, error)` API; the test discipline (`Equal` constant-time compare) carries over even if xxh3 doesn't need it.

**Hash function shape** (credhash.go lines 31-38):
```go
func Hash(pepper, plaintext []byte) (string, error) {
    if len(pepper) == 0 {
        return "", ErrEmptyPepper
    }
    h := hmac.New(sha256.New, pepper)
    h.Write(plaintext)
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

**Adaptation** — accept `io.Reader` instead of `[]byte` (state-engine streams large extracted files):
```go
func Hash(r io.Reader) (string, error) {
    h := xxh3.New()
    if _, err := io.Copy(h, r); err != nil { return "", err }
    return "xxh3:" + hex.EncodeToString(h.Sum(nil)), nil
}
```

`go.mod` already has `github.com/zeebo/xxh3 v1.1.0` (verified: go.sum:395-396) — NO new dep needed for D-10.

---

### `cmd/ach-cli/cmd/hydrate.go` (MODIFY — engine flags + `--raw` + dispatch)

**Analog:** self (existing Phase 6 surface). The refactor extends `newHydrateCmd` and inserts an early-branch on `--raw`.

**Existing cobra flag-wiring pattern** (hydrate.go lines 86-152):
```go
func newHydrateCmd() *cobra.Command {
    var (
        flagEnvironment string
        flagNoWarnings  bool
        flagVerbose     bool
        flagAPIKey      string
        flagEnvKey      string
        flagDeployment  string
    )

    cmd := &cobra.Command{
        Use:   "hydrate",
        Short: "POST /platform/hydrate and stream the response JSON to stdout",
        Long:  `...`,
        RunE: func(cmd *cobra.Command, _ []string) error {
            return runHydrate(cmd, flagEnvironment, flagNoWarnings, flagVerbose,
                flagAPIKey, flagEnvKey, flagDeployment)
        },
    }
    cmd.Flags().StringVar(&flagEnvironment, "environment", "",
        "Target Environment name (REQUIRED for pk_, OPTIONAL for ek_)")
    cmd.Flags().BoolVar(&flagNoWarnings, "no-warnings", false, "...")
    // ...
    return cmd
}
```

**Adaptation — add engine flag block then `--raw` short-circuit at top of `runHydrate`:**

Engine flags to add (per CONTEXT.md D-03): `--include-runtime`, `--only-runtime`, `--sync`, `--force`, `--dry-run`, `--wait`, `--lock-timeout <dur>`, `--output <dir>`, `--allow-symlinks`, `--platform <id>`, `--global`, plus a hidden `--raw`. Wire each via `cmd.Flags().BoolVar`/`StringVar`/`DurationVar`. The `--raw` flag uses `cmd.Flags().MarkHidden("raw")` to keep it out of `--help`.

**`--raw` short-circuit insertion point** — at the top of `runHydrate` (current line 192), BEFORE `assertMutexCreds`:
```go
if flagRaw {
    return runHydrateRaw(cmd, environment, noWarnings, verbose,
        flagAPIKey, flagEnvKey, flagDeployment)
}
// Engine path:
return hydrate.Run(cmd.Context(), hydrate.Opts{...})
```

`runHydrateRaw` IS the current `runHydrate` body unchanged (Phase 6 POST+stream); the new `runHydrate` is a thin dispatcher.

**Auth/Guard pattern preserved** (hydrate.go lines 218-226):
```go
if err := synthetic.GuardCommand(synthetic.Params{
    Gate:           synthetic.GateHydrate,
    APIKeyFlag:     in.flagAPIKey,
    EnvKeyFlag:     in.flagEnvKey,
    DeploymentFlag: in.flagDeployment,
}); err != nil {
    return err
}
```

Engine path also runs this gate. `--global` is the engine-path-only flag and does NOT cross the synthetic boundary (CONTEXT.md `<code_context>` "Established Patterns").

**`init()` subcommand registration** (hydrate.go lines 447-449):
```go
func init() {
    rootCmd.AddCommand(newHydrateCmd())
}
```

Unchanged.

---

### `internal/cli/hydrate/commit.go` (14-step orchestrator skeleton, event-driven)

**Analog:** `cmd/ach-cli/cmd/hydrate.go` `runHydrate` (lines 192-267) — the existing flat step-list shape (snapshot inputs → mutex assert → synthetic gate → resolveBearer → classify → warn → postAndStream). The engine version expands "postAndStream" into the 14 steps from spec §6.7.

**Flow-control idiom** (hydrate.go lines 207-251):
```go
if err := assertMutexCreds(...); err != nil { return err }
if err := synthetic.GuardCommand(synthetic.Params{...}); err != nil { return err }

baseURL, bearer, err := resolveBearer(in)
if err != nil { return err }

prefix, classifyErr := keys.ClassifyBearer(bearer)
if classifyErr != nil {
    return &exit.CodedError{Code: exit.General, Msg: ..., Wrapped: classifyErr}
}
```

**Engine adaptation** — each of §6.7's 14 steps is a method on a `commit` struct (sequential execution, fail-fast). Test seam: inject `stateStore`, `lockProvider`, `client`, `extractor`, `adapter` as interface fields so unit tests swap fakes.

---

### `internal/cli/manifest/manifest.go` (POST hydrate decoder, request-response)

**Analog:** `internal/cli/httpclient/client.go` `Do` (lines 113-139)

**HTTP call pattern** (hydrate.go lines 361-381 — actual call site):
```go
hc := &httpclient.Client{
    BaseURL:    baseURL,
    APIKey:     bearer,
    Verbose:    verbose,
    Stderr:     cmd.ErrOrStderr(),
    HTTPClient: hydrateHTTPClient,
}
resp, err := hc.DoRaw(cmd.Context(), http.MethodPost, "/platform/hydrate", body)
if err != nil {
    return mapHydrateError(err)
}
defer func() { _ = resp.Body.Close() }()
if _, err := io.Copy(cmd.OutOrStdout(), resp.Body); err != nil { ... }
```

**Manifest adaptation** — use `Do` (typed decode) instead of `DoRaw` (stream). Phase 7 NEEDS the parsed `runtime`/`context` structure for diff computation; raw bytes belong to `--raw` path. After `Do`, immediately assert:
```go
if m.SchemaVersion != "v1alpha1" {
    return &exit.CodedError{Code: exit.SchemaMismatch, ...}  // exit 5
}
if m.Runtime == nil || m.Context == nil {
    return &exit.CodedError{Code: exit.SchemaMismatch, ...}  // exit 5
}
```

---

### `internal/cli/adapter/adapter.go` (Adapter interface)

**Analog:** `internal/cli/synthetic/synthetic.go` (lines 22-100 — typed enum + Params struct pattern)

**Typed-enum pattern** (synthetic.go lines 22-72):
```go
type Gate int

const (
    GateLogin Gate = iota + 1
    GateLogout
    GateConfig
    // ...
)

type Params struct {
    Gate Gate
    APIKeyFlag string
    // ...
}
```

**Adaptation — MergeKind enum + Adapter interface**:
```go
type MergeKind int

const (
    MergeDeep MergeKind = iota + 1
    MergeComposite
    MergeReplace
)

type Adapter interface {
    ID() string
    Aliases() []string
    Detect(root string) (Match, error)
    RenderRuntime(ctx context.Context, m *manifest.Manifest, s *state.File) ([]FileWrite, error)
    TransformPlugin(ctx context.Context, src, dst string) (PluginWrite, error)
    MergeStrategies() map[string]MergeKind
    ResolveOutputContent(ctx context.Context, m *manifest.Manifest, target string) ([]byte, error)
}
```

---

### `internal/cli/adapter/registry.go` (init-registered registry)

**Analog:** cobra root + `rootCmd.AddCommand` (`cmd/ach-cli/cmd/hydrate.go` line 447-449 / every other subcommand `init()`)

**Init-side-effect registration pattern**:
```go
// In each adapter subpackage:
func init() {
    adapter.Register(&Adapter{...})
}
```

```go
// In registry.go:
var registry = map[string]Adapter{}

func Register(a Adapter) {
    if _, dup := registry[a.ID()]; dup {
        panic(fmt.Sprintf("adapter: duplicate id %q", a.ID()))
    }
    registry[a.ID()] = a
}

func Lookup(id string) (Adapter, bool) { a, ok := registry[id]; return a, ok }

func Iter() []Adapter {
    out := make([]Adapter, 0, len(registry))
    for _, a := range registry {
        out = append(out, a)
    }
    return out
}
```

---

### `test/e2e/cli_hydrate_engine_test.go` (e2e suite)

**Analog:** `test/e2e/cli_login_hydrate_test.go` (lines 37-260)

**Build tag + file header pattern** (cli_login_hydrate_test.go lines 1-37):
```go
//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 7 CLI engine e2e suite. Drives `ach-cli hydrate --environment
// demo --platform <id>` across all 4 adapters × {pk_, ek_} against
// the kept kind cluster (per CLAUDE.md "E2E debug loop" —
// `make cluster-keep`).
//
// Activation: ACH_E2E_PHASE7=1 ACH_E2E_PHASE7_PK=pk_<...> ...
package e2e
```

**Umbrella + t.Run subtests pattern** (cli_login_hydrate_test.go lines 51-57):
```go
func TestPhase7CLIEngine(t *testing.T) {
    t.Run("w1_baseline_no_op", testPhase7BaselineNoOp)
    t.Run("sc1_claudecode_pk", testPhase7Sc1ClaudeCodePk)
    t.Run("sc1_claudecode_ek", testPhase7Sc1ClaudeCodeEk)
    t.Run("sc1_codex_pk", testPhase7Sc1CodexPk)
    // ... 8 platform×key subtests
    t.Run("sc2_commit_sequence_sigkill", testPhase7Sc2SigkillRecovery)
    t.Run("sc3_drift_four_outcomes", testPhase7Sc3Drift)
    t.Run("sc4_safe_extract_malicious", testPhase7Sc4Malicious)
    t.Run("sc4_safe_extract_bomb", testPhase7Sc4Bomb)
    t.Run("sc4_autoclaim_three_tier", testPhase7Sc4AutoClaim)
}
```

**Binary path pattern** (`phase6_helpers_test.go` line 62):
```go
const phase7BinaryPath = "../../bin/ach-cli"  // Same single-binary target
```

---

### `test/e2e/phase7_helpers_test.go` (helpers)

**Analog:** `test/e2e/phase6_helpers_test.go` (lines 38-310)

**Suite guard pattern** (phase6_helpers_test.go lines 90-144):
```go
func phase7SuiteGuard(t *testing.T) {
    t.Helper()
    if os.Getenv("ACH_E2E_PHASE7") != "1" {
        t.Skipf("Phase 7 CLI engine e2e gated behind ACH_E2E_PHASE7=1 ...")
        return
    }
    if _, err := os.Stat(phase7BinaryPath); err != nil { t.Skipf(...); return }
    // kubectl probe against ach-platform-api Deployment
    out, err := runCmd("kubectl", "get", "deploy", "ach-platform-api",
        "-n", "ach-system", "--no-headers")
    if err != nil { t.Skipf(...); return }
}
```

**Binary exec helper pattern** (phase6_helpers_test.go lines 243-254):
```go
func phase7RunAchCli(t *testing.T, xdgHome string, args ...string) ([]byte, []byte, error) {
    t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, phase7BinaryPath, args...)
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdgHome)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    err := cmd.Run()
    return stdout.Bytes(), stderr.Bytes(), err
}
```

---

## Shared Patterns

### SPDX header
**Source:** every existing `*.go` file (e.g. `internal/cli/exit/exit.go` line 1)
**Apply to:** every new `*.go` outside `vendor/`, `zz_generated*`, `mock_*`
```go
// SPDX-License-Identifier: Apache-2.0
```
Pre-push gate enforces (CLAUDE.md §Publication). Generated files inherit via `hack/boilerplate.go.txt`.

### CodedError construction (exit-code dispatch)
**Source:** `internal/cli/exit/exit.go` (lines 52-63) + every CLI subcommand call site
**Apply to:** every engine package that raises a coded exit (hydrate, state, extract, adapter)
```go
return &exit.CodedError{
    Code:    exit.Drift,             // NEW Phase 7 const: code 2
    Msg:     "drift detected on …",
    Wrapped: err,
}
```

**Phase 7 additions to `internal/cli/exit/exit.go`** (additive, no renumber per the file's own comment lines 6-8):
```go
// Drift (2) — STATE-04 four-outcome truth table: local-edit-preserve
// and conflict-preserve both surface as Drift.
Drift Code = 2

// EnvironmentMismatch (4) — STATE-03 same-<ach-dir> different-Env guard.
EnvironmentMismatch Code = 4

// SchemaMismatch (5) — STATE-09 manifest schemaVersion != "v1alpha1"
// AND STATE-02 state.schemaVersion != "2".
SchemaMismatch Code = 5

// CollisionRefuse (7) — SAFE-04 exists-unowned + bytes differ + !--force.
CollisionRefuse Code = 7
```

### `keys.ClassifyBearer` for pk_/ek_ discrimination
**Source:** `internal/keys/keys.go` (lines 160-182) + `cmd/ach-cli/cmd/hydrate.go` (lines 234-251)
**Apply to:** every engine entry point that branches on credential kind (manifest decoder, env header attachment, --global gate)
```go
prefix, classifyErr := keys.ClassifyBearer(bearer)
if classifyErr != nil {
    return &exit.CodedError{Code: exit.General, Msg: ..., Wrapped: classifyErr}
}
if prefix == keys.PrefixPk && effectiveEnv == "" {
    return &exit.CodedError{Code: exit.General, Msg: "--environment is required for pk_"}
}
```

### synthetic.GuardCommand
**Source:** `internal/cli/synthetic/synthetic.go` `GuardCommand` (lines 211-274) + every CLI subcommand call site
**Apply to:** `cmd/ach-cli/cmd/hydrate.go` engine-path branch (preserve existing call); add a comment that `--global` is allowed in synthetic (workspace scope only)
```go
if err := synthetic.GuardCommand(synthetic.Params{
    Gate:           synthetic.GateHydrate,
    APIKeyFlag:     in.flagAPIKey,
    EnvKeyFlag:     in.flagEnvKey,
    DeploymentFlag: in.flagDeployment,
}); err != nil {
    return err
}
```

### httpclient.Client.DoRaw for content download (binary stream)
**Source:** `internal/cli/httpclient/client.go` `DoRaw` (lines 147-158) + `cmd/ach-cli/cmd/hydrate.go` `postAndStream` (lines 354-381)
**Apply to:** content fetch loop in `internal/cli/extract/stage.go` (per-resource GET) — the response body streams via `io.Copy` into the staging tmp file; no buffering
```go
hc := &httpclient.Client{BaseURL: baseURL, APIKey: bearer, ...}
resp, err := hc.DoRaw(ctx, http.MethodGet,
    fmt.Sprintf("/content/%s/%s", kind, name), nil)
if err != nil { return mapHydrateError(err) }
defer resp.Body.Close()
// io.Copy(stagingFile, resp.Body) — bomb-cap counter wraps the io.Reader
```

For `pk_` Content Service requests, set `ExtraHeaders: http.Header{"x-ach-environment": []string{env}}` on the Client per CLI-03.

### httpclient.Client.Do for manifest decode (typed)
**Source:** `internal/cli/httpclient/client.go` `Do` (lines 113-139)
**Apply to:** `internal/cli/manifest/manifest.go` only — every other engine HTTP call streams via DoRaw

### Atomic-rename + tmp-staging
**Source:** `internal/cli/config/config.go` `Save` (lines 156-184) + `internal/cachefs/sweep.go` `SweepTmp` (lines 130-157)
**Apply to:** every file write in `internal/cli/state/`, `internal/cli/extract/`, every adapter `RenderRuntime` result staging

### Package doc.go discipline
**Source:** `internal/cli/doc.go` + `internal/cachefs/doc.go`
**Apply to:** every new `internal/cli/<subpkg>/doc.go`
```go
// SPDX-License-Identifier: Apache-2.0

// Package <name> implements <single-sentence contract>.
//
// Discipline: stdlib + <named-dep> only. Atomic publication via
// tmp+rename. Error handling via sentinel errors + errors.Is.
package <name>
```

### Cobra subcommand factory + init() registration
**Source:** every existing `cmd/ach-cli/cmd/<verb>.go` (e.g. hydrate.go lines 86-153, 447-449)
**Apply to:** the modified `cmd/ach-cli/cmd/hydrate.go` ONLY — Phase 7 adds no new top-level cobra subcommand
```go
func newHydrateCmd() *cobra.Command {
    var flagX string
    cmd := &cobra.Command{Use: "hydrate", RunE: func(cmd *cobra.Command, _ []string) error {
        return runHydrate(cmd, flagX, ...)
    }}
    cmd.Flags().StringVar(&flagX, "x", "", "...")
    return cmd
}

func init() { rootCmd.AddCommand(newHydrateCmd()) }
```

### Test helper: `executeCommand` + table-driven cobra tests
**Source:** `cmd/ach-cli/cmd/hydrate_test.go` (lines 38-41) + `cmd/ach-cli/cmd/helpers_test.go`
**Apply to:** the modified `cmd/ach-cli/cmd/hydrate_test.go` (new engine-flag scenarios) — reuse the existing harness
```go
func executeHydrate(t *testing.T, args ...string) (string, string, exit.Code, error) {
    t.Helper()
    return executeCommand(t, newHydrateCmd(), args...)
}
```

### E2E test build tag + package + skip-on-cluster-missing
**Source:** `test/e2e/cli_login_hydrate_test.go` (lines 1-37) + `test/e2e/phase6_helpers_test.go` (lines 90-144)
**Apply to:** `test/e2e/cli_hydrate_engine_test.go` + `test/e2e/phase7_helpers_test.go`
```go
//go:build e2e

// SPDX-License-Identifier: Apache-2.0
package e2e
```
+ ACH_E2E_PHASE7 gate + kubectl Deployment probe + `t.Skipf` on prerequisite miss.

## No Analog Found

Files with no close in-repo match. The planner should reach for the canonical stdlib doc + the spec for these.

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/cli/lock/lock_unix.go` | flock(LOCK_EX) impl | file-I/O / blocking | No in-repo `golang.org/x/sys/unix.Flock` consumer. Closest is `internal/cli/config/config.go` `skipModeCheck` which uses runtime.GOOS (not build tags). Use `//go:build !windows` per CONTEXT.md D-18; pull the impl directly from `pkg.go.dev/golang.org/x/sys/unix#Flock` docs. |
| `internal/cli/extract/tar.go` | safe tar + gzip stream | streaming / transform | No in-repo `archive/tar` consumer. Closest reference is the §6.4 spec table (CLI spec) plus the stdlib `archive/tar` + `compress/gzip` examples. Hand-rolled safety checks per CONTEXT.md D-11. |
| `internal/cli/extract/autoclaim.go` | 3-tier collision cascade | transform | The cascade pattern (eager → adapter `ResolveOutputContent` → lazy source read) is new in this codebase. Closest precedent is the `internal/cli/hydrate/commit.go` step-list flow shape, but the cascade itself is novel. |
| `test/fixtures/malicious-archives/*.tar.gz` + generator | malicious archive set | data | No prior malicious-fixture set in repo. Recommendation: a Go generator under `test/fixtures/malicious-archives/generator/main.go` emits each fixture deterministically; this matches Phase 5's `test/e2e/phase5_fixtures/` style but for tar archives. |

## Metadata

**Analog search scope:**
- `cmd/ach-cli/cmd/` (cobra subcommands — hydrate, logout, env_keys references)
- `internal/cli/{config,httpclient,exit,synthetic,devicecode,render}/`
- `internal/{cachefs,credhash,keys,keystore}/`
- `test/e2e/` (cli_login_hydrate, phase6_helpers, phase5_helpers headers)
- `go.mod` / `go.sum` (confirm `github.com/zeebo/xxh3 v1.1.0` present)

**Files scanned:** 14 source files Read end-to-end, 4 additional Read for headers/structure only
**Key dependency confirmation:** `github.com/zeebo/xxh3 v1.1.0` already in `go.sum` (lines 395-396) — D-10 ships with NO new go.mod entry
**Pattern extraction date:** 2026-05-29
