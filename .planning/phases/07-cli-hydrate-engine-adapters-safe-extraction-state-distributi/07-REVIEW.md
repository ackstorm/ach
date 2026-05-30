---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
reviewed: 2026-05-29T00:00:00Z
depth: standard
files_reviewed: 31
files_reviewed_list:
  - cmd/ach-cli/cmd/adapters_register.go
  - cmd/ach-cli/cmd/hydrate.go
  - internal/cli/adapter/adapter.go
  - internal/cli/adapter/claudecode/claudecode.go
  - internal/cli/adapter/codex/codex.go
  - internal/cli/adapter/gemini/gemini.go
  - internal/cli/adapter/opencode/opencode.go
  - internal/cli/adapter/registry.go
  - internal/cli/exit/exit.go
  - internal/cli/extract/autoclaim.go
  - internal/cli/extract/fetch.go
  - internal/cli/extract/limits.go
  - internal/cli/extract/stage.go
  - internal/cli/extract/tar.go
  - internal/cli/hash/xxh3.go
  - internal/cli/hydrate/autodetect.go
  - internal/cli/hydrate/commit.go
  - internal/cli/hydrate/drift.go
  - internal/cli/hydrate/flags.go
  - internal/cli/hydrate/result.go
  - internal/cli/hydrate/wiring.go
  - internal/cli/lock/lock.go
  - internal/cli/lock/lock_unix.go
  - internal/cli/lock/path.go
  - internal/cli/manifest/manifest.go
  - internal/cli/state/atomic.go
  - internal/cli/state/guard.go
  - internal/cli/state/path.go
  - internal/cli/state/state.go
  - internal/cli/state/sweep.go
  - test/fixtures/malicious-archives/fixtures.go
findings:
  critical: 3
  warning: 5
  info: 3
  total: 11
status: issues_found
---

# Phase 07: Code Review Report

**Reviewed:** 2026-05-29
**Depth:** standard
**Files Reviewed:** 31
**Status:** issues_found

## Summary

Reviewed all 31 production source files for Phase 7: the hydrate engine, tar extractor, state marshaler, workspace lock, manifest decoder, hash package, four platform adapters, and the cobra wiring. The safety-critical tar extraction logic (SAFE-01..06) is robustly implemented with correct path-traversal, symlink, hardlink, device, FIFO, and PAX-injection rejection. The lock, state, and atomic-write packages are well-structured.

Three blockers were found. The most severe is that `state.WriteAtomic` — used to write adapter config files that embed plaintext API keys — hardcodes mode 0o644, making credential-bearing files world-readable. The second blocker is that the `hydrate.NewWiring` result is discarded in the cobra layer and there is no code path to inject the constructed `Extractor`/`AdapterDispatcher` into the `commit` struct, meaning steps 7–10 (content fetch, extraction, adapter dispatch) are permanently dead in production. The third blocker is a path-comparison mismatch in `autoclaim.Classify`: workspace-relative `FileEntry.Target` values are compared against absolute `finalPath` arguments, so `CollisionOwnedByCurrent` is never returned for adapter-written files on re-hydrate.

Five warnings and three info items cover copyFile close-error swallowing, a v1 state-file migration dead end, the production-reachable SIGKILL env-var seam, the `_` discard of `diffTargets`, and minor style items.

---

## Critical Issues

### CR-01: Credential-bearing adapter config files written at 0o644 (world-readable)

**File:** `internal/cli/state/atomic.go:62`
**Issue:** `WriteAtomic` unconditionally creates the destination file at mode `0o644`. The comment on line 59 explains this as correct for `state.json` ("no secrets"), but `WriteAtomic` is also the write path for adapter runtime-config files — `.claude/.mcp.json`, `.codex/config.toml`, `.gemini/settings.json`, `.opencode/opencode.json` — via `adapterDispatcherImpl.Render` (`internal/cli/hydrate/wiring.go:230`). All four of these files embed the plaintext `x-ach-key` bearer credential (pk_ or ek_ token) in their `headers` maps. On any multi-user host or shared CI agent, other local users can read these files and extract the bearer credential, enabling them to make authenticated API calls as the victim.

`internal/cli/hydrate/wiring.go:230` calls `state.WriteAtomic(finalAbs, fw.Content)` where `fw.Content` is the JSON/TOML bytes containing `"x-ach-key": "<bearer>"`. The mode is baked into `WriteAtomic`.

**Fix:** Accept an `os.FileMode` parameter (or a separate `WriteAtomicWithMode` variant) and have callers pass `0o600` for credential-bearing files and `0o644` for `state.json`. Alternatively, `wiring.go` can chmod the file to `0o600` immediately after `WriteAtomic` returns, before the final path is observable by other processes (though the rename makes the file visible at mode 0644 for a window):

```go
// In WriteAtomic, change the hardcoded mode or accept it as a parameter:
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
    // ...
    if err := tmp.Chmod(mode); err != nil { ... }
    // ...
}

// Caller in state.Save (no secrets):
return WriteAtomic(path, buf.Bytes(), 0o644)

// Caller in adapterDispatcherImpl.Render (credential-bearing):
if err := state.WriteAtomic(finalAbs, fw.Content, 0o600); err != nil { ... }
```

---

### CR-02: Extractor and AdapterDispatcher are permanently unwired — content fetch and adapter dispatch never execute

**File:** `cmd/ach-cli/cmd/hydrate.go:502`
**Issue:** `runHydrateEngine` constructs the `Extractor` and `AdapterDispatcher` via `hydrate.NewWiring(...)` but immediately discards both return values with `_, _ = hydrate.NewWiring(...)`. The `hydrate.Run` call on line 504 creates a fresh `*commit` internally via `newCommit(opts)`, which leaves `c.extractor` and `c.adapter` as `nil`. The `commit` struct and its `run()` method are unexported; there is no mechanism to inject the constructed impls from outside the `hydrate` package.

As a result, the `run()` body's steps 7–10 (`if c.extractor != nil { ... }` and `if c.adapter != nil { ... }`) always short-circuit. `ach-cli hydrate` (in engine mode) performs lock acquisition, state reading, manifest fetching, and state writing, but never fetches any content, never extracts any files, and never dispatches to any adapter. The workspace receives an updated `state.json` but no actual content files.

**Fix:** Either expose the `Extractor` and `AdapterDispatcher` as fields on `hydrate.Opts` so `Run()` can plumb them in, or add an exported functional-option or wire-method before dispatching to `run()`. The simplest approach is to add the interfaces to `Opts`:

```go
// In flags.go:
type Opts struct {
    // ...existing fields...
    // Optional wiring for the content pipeline. When nil, steps 7-10 are no-ops.
    Extractor       Extractor
    AdapterDisp     AdapterDispatcher
}

// In commit.go newCommit():
c.extractor = opts.Extractor
c.adapter = opts.AdapterDisp

// In cmd/ach-cli/cmd/hydrate.go runHydrateEngine():
ext, disp := hydrate.NewWiring(hc, platformID, limits, in.allowSymlinks, in.force)
opts := hydrate.Opts{
    // ...
    Extractor:   ext,
    AdapterDisp: disp,
}
```

---

### CR-03: Autoclaim `Classify` path-comparison mismatch — `CollisionOwnedByCurrent` never fires for adapter files on re-hydrate

**File:** `internal/cli/extract/autoclaim.go:131`
**Issue:** `Classify(finalPath, stateFile)` compares `entry.Target == finalPath`, where `finalPath` is an absolute path (e.g., `/workspace/.ach/.claude/.mcp.json`) but `state.FileEntry.Target` is stored as a workspace-relative path (e.g., `.claude/.mcp.json`). The values never compare equal, so `CollisionOwnedByCurrent` is never returned for adapter-written files. Every re-hydrate of any adapter file will enter the `CollisionExistsUnowned` branch and trigger the three-tier cascade, even for files the engine itself wrote on the previous run.

This cascades into `extract.Cascade`, which computes `sha256` of the existing file and compares with the expected bytes. If the cascade finds `Identical=false` (for example, because the credentials changed between hydrations), `WrapCollisionRefuseError` is returned and the hydrate exits with code 7, refusing to update a file the engine should freely own. The user is stuck at exit 7 with no way forward except `--force`, violating the SAFE-04 contract which only applies to genuinely foreign files.

**Fix:** Store absolute paths in `FileEntry.Target` (the simplest fix), or normalize both sides of the comparison to the same form before comparing:

```go
// In autoclaim.go Classify, normalize to absolute before comparison:
func Classify(finalPath string, stateFile *state.File) (CollisionClass, error) {
    if _, err := os.Stat(finalPath); err != nil {
        if os.IsNotExist(err) {
            return CollisionNone, nil
        }
        return 0, fmt.Errorf("autoclaim: stat %s: %w", finalPath, err)
    }
    if stateFile != nil {
        for _, entry := range walkAllEntries(stateFile) {
            // Normalize entry.Target to absolute for comparison.
            entryAbs := entry.Target
            if !filepath.IsAbs(entryAbs) {
                // The achDir is not available here; callers must pass it,
                // OR store absolute paths in FileEntry.Target at write time.
            }
            if entryAbs == finalPath {
                return CollisionOwnedByCurrent, nil
            }
        }
    }
    return CollisionExistsUnowned, nil
}
```

The cleanest fix is to ensure `FileEntry.Target` is always stored as an absolute path, which also aligns `Classify` with `Sync`'s `pruneMissing` and `walkEntries` behavior.

---

## Warnings

### WR-01: `ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP` env var is reachable in production — any user can crash mid-hydrate

**File:** `internal/cli/hydrate/commit.go:178-182`
**Issue:** The SIGKILL injection seam is documented as "TEST-ONLY" but is unconditionally read from the environment at every `newCommit()` call in production. Any user who sets `ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP=12` before running `ach-cli hydrate` will terminate the process immediately after step 12 (atomic state write), leaving state.json written but the lock released prematurely. In shared CI environments where env vars are inherited across steps, this can cause silent data-loss behavior (state.json updated, no content fetched because steps 7-10 are stubbed, yet the environment guard is now bound).

The seam is also dangerous in scripted environments where a misconfigured parent process sets arbitrary numeric env vars.

**Fix:** Gate the seam behind a build tag or a test-binary-only check so it is never compiled into the release binary:

```go
// In a separate file hydrate_test_seam.go with build tag:
//go:build e2e || test

package hydrate
// ... seam code
```

Alternatively, accept the seam as intentional but add an explicit check that the value is in a known-valid range (1-14) and log a warning to stderr so unintentional triggers are visible.

---

### WR-02: `copyFile` in all four adapters silently swallows close error on the destination file

**File:** `internal/cli/adapter/claudecode/claudecode.go:327`, `internal/cli/adapter/codex/codex.go:457`, `internal/cli/adapter/gemini/gemini.go:547`, `internal/cli/adapter/opencode/opencode.go:408`
**Issue:** All four adapters' `copyFile` helper close the destination file via `defer func() { _ = out.Close() }()`. The `io.Copy` error is checked (line 329 in claudecode), but the `out.Close()` error is silently discarded. On Linux with buffered I/O, `close(2)` can return `EIO` when the final kernel buffer flush to disk fails (e.g., disk full, ECC error). A failed close means the file's final bytes may not be on disk, but the function returns `nil` to the caller, which records the file as successfully written and proceeds to hash the (possibly corrupt) output.

**Fix:** Close the destination file explicitly and check the error before returning:

```go
func copyFile(srcPath, dstPath string) error {
    in, err := os.Open(srcPath)
    if err != nil {
        return err
    }
    defer func() { _ = in.Close() }()

    out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
    if err != nil {
        return err
    }
    if _, err := io.Copy(out, in); err != nil {
        _ = out.Close()
        return err
    }
    return out.Close() // surface flush/close errors
}
```

---

### WR-03: v1 `state.json` with unknown fields returns `ErrStateParse` (exit 1), not `ErrSchemaMismatch` (exit 5) — `--force` bypass does not work

**File:** `internal/cli/state/state.go:103-113`
**Issue:** `Load` decodes with `DisallowUnknownFields` before checking `schemaVersion`. A v1 `state.json` that carries the now-removed `contentHashes` field fails the unknown-fields check and returns `fmt.Errorf("%w: %v", ErrStateParse, err)` — exit 1. The caller (`commit.go:step3ReadState`) only bypasses the error with `--force` when `errors.Is(err, state.ErrSchemaMismatch)`. The `ErrStateParse` arm maps to exit 1 (General) with no `--force` escape hatch.

A user upgrading from a workspace that had Phase 6 or earlier state will see:
```
error: read state.json: state: parse failed: json: unknown field "contentHashes"
```
with no way to recover except manually deleting `state.json`, which is not documented anywhere in the error message.

**Fix:** Check `schemaVersion` first using a partial decode (without `DisallowUnknownFields`), return `ErrSchemaMismatch` for non-`"2"` values before attempting the strict decode:

```go
func Load(path string) (*File, error) {
    raw, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, err
    }
    // Quick schema check before strict decode.
    var sv struct {
        SchemaVersion string `json:"schemaVersion"`
    }
    _ = json.Unmarshal(raw, &sv) // best-effort; ignore error
    if sv.SchemaVersion != "" && sv.SchemaVersion != "2" {
        return nil, fmt.Errorf("%w: got %q, want \"2\"", ErrSchemaMismatch, sv.SchemaVersion)
    }
    // Strict decode.
    var f File
    dec := json.NewDecoder(bytes.NewReader(raw))
    dec.DisallowUnknownFields()
    if err := dec.Decode(&f); err != nil {
        return nil, fmt.Errorf("%w: %v", ErrStateParse, err)
    }
    if f.SchemaVersion != "2" {
        return nil, fmt.Errorf("%w: got %q, want \"2\"", ErrSchemaMismatch, f.SchemaVersion)
    }
    return &f, nil
}
```

---

### WR-04: `step6Diff` result is discarded — scope filter logic is dead in production

**File:** `internal/cli/hydrate/commit.go:230`
**Issue:** The result of `step6Diff(m)` is assigned to `diffTargets` and immediately discarded with `_ = diffTargets // W1: not consumed further; W2 wires.`. The scope filter logic in `step6Diff` (which honors `opts.OnlyRuntime`, `opts.IncludeRuntime`, and builds a typed `[]diffTarget` slice) executes but produces no observable effect because the extractor (CR-02) is also never wired.

The user-facing impact: `--only-runtime` and `--include-runtime` flags are accepted without error but silently have no effect. This may confuse users who expect these flags to control what is hydrated.

**Fix:** This is a documented W1 stub; the primary fix is CR-02 (wire the extractor). Once wired, `diffTargets` should be passed to `ExtractContent`. In the interim, add a `--dry-run`-only stdout notice that lists the diffTargets scope so users can at least verify the scope filter is being applied correctly, or emit a warning if `--only-runtime` / `--include-runtime` are passed without the extractor being wired.

---

### WR-05: `Lookup` in `adapter/registry.go` is O(n) due to linear scan instead of direct map lookup

**File:** `internal/cli/adapter/registry.go:97-100`
**Issue:** `Lookup` iterates all entries in `registry` comparing `strings.ToLower(canonical) == folded` rather than looking up `folded` directly. With 4 adapters this is negligible, but the code is architecturally incorrect: `Register` stores adapters keyed by their original (non-lowercased) canonical ID, so `registry["claude-code"]` works fine — a direct `registry[folded]` lookup would find it since all canonical IDs are already lowercase. The linear scan is unnecessary and fragile if a future adapter registers a mixed-case canonical ID (the registration-time collision check would miss mixed-case collisions in `registry`).

**Fix:** Key the registry by lowercase canonical ID at registration time and use a direct O(1) lookup:

```go
// In Register:
registry[strings.ToLower(id)] = a

// In Lookup:
mu.RLock()
defer mu.RUnlock()
if a, ok := registry[folded]; ok {
    return a, true
}
if canonical, ok := aliasIndex[folded]; ok {
    if a, ok := registry[canonical]; ok {
        return a, true
    }
}
return nil, false
```

---

## Info

### IN-01: `paxInjectedPath` check is redundant — `tar.Reader` already applies PAX records to `hdr.Name`

**File:** `internal/cli/extract/tar.go:131-136`
**Issue:** The comment at line 126 correctly notes that `tar.Reader` transparently applies PAX records to the next entry's `Name` before `Next()` returns. Therefore `hdr.Name` at line 139 (which is checked by `checkSafeRel`) already reflects any PAX `path` injection. The earlier call to `paxInjectedPath(hdr)` at line 131 checks `hdr.PAXRecords["path"]` and calls `checkSafeRel` on it — but since `hdr.Name` is already the injected value, both calls check the same effective path. This is defense-in-depth and harmless, but the comment "We still defensively check" is slightly misleading since there is no scenario where the PAX check fires without the `hdr.Name` check also firing on the same value.

**Fix:** No code change required. Add a comment clarifying that the PAX check fires on the raw PAX record value before the Go reader normalizes it, and is retained for the edge case of global PAX headers (`TypeXGlobalHeader`) which the reader may not apply to `hdr.Name`:

```go
// paxInjectedPath checks PAXRecords["path"] explicitly because TypeXGlobalHeader
// entries carry a path override that the reader may apply to multiple subsequent
// entries — hdr.Name alone captures only the single-entry local override.
```

---

### IN-02: `gemini.TransformPlugin` silently drops unknown top-level components without recording them in `Dropped`

**File:** `internal/cli/adapter/gemini/gemini.go:401-403`
**Issue:** The walk function returns `nil` (skip silently) for any top-level component that is neither in `componentKept` nor in `componentDropped`. The comment says "Unknown top-level components ... are silently dropped per ADAPT-07. We do NOT record them in Dropped to keep the warning surface focused on documented-but-unsupported components". This differs from the opencode and codex adapters, which drop only explicitly named components. A plugin that has a new component type (e.g., `tools/` from a future Hub spec version) would be silently lost without any user-visible warning, making it harder to diagnose incomplete plugin hydration.

**Fix:** Record unknown components in `Dropped` rather than silently ignoring them, consistent with the codex/opencode approach. The ADAPT-07 contract says accumulate dropped names; there is no exception for "unknown" vs "known-unsupported":

```go
// Replace the current silent skip:
if !componentKept[topLevel] {
    droppedSet[topLevel] = true // record unknown → user sees warning
    return nil
}
```

---

### IN-03: `findFrontmatterFences` has an unreachable `openEnd=0` default case

**File:** `internal/cli/adapter/codex/codex.go:591-596`
**Issue:** The `switch raw[3]` in `findFrontmatterFences` handles `'\n'` (openEnd=4) and `'\r'` (openEnd=5). No `default` case is provided, so `openEnd` remains `0` if `raw[3]` is neither. This code path is unreachable in practice because `startsWithFrontmatterFence` only returns `true` when `raw[3]` is `'\n'` or `raw[3]=='\r' && raw[4]=='\n'`. However, the combination of "caller guarantees raw starts with a frontmatter fence" (godoc) plus no explicit default means a future caller that does not call `startsWithFrontmatterFence` first would silently scan from offset 0, doubling-counting the opening fence as frontmatter content.

**Fix:** Add a defensive `default` panic or return `(0,0,0,false)` in `findFrontmatterFences` for the impossible case:

```go
switch raw[3] {
case '\n':
    openEnd = 4
case '\r':
    openEnd = 5
default:
    // Caller contract violated: startsWithFrontmatterFence guarantees raw[3] is \n or \r.
    return 0, 0, 0, false
}
```

---

_Reviewed: 2026-05-29_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
