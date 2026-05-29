// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/lock"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// manifestFetcher is the function-typed test seam for step 5. Production
// defaults to a closure over manifest.Fetch wrapping the resolved
// httpclient.Client; unit tests inject a fake that returns either a
// canned *manifest.Manifest or a wrapped manifest.ErrSchemaMismatch.
type manifestFetcher func(ctx context.Context, environment string) (*manifest.Manifest, error)

// commit is the unexported orchestrator struct. Run constructs one via
// newCommit(opts) and dispatches stepN methods sequentially. Every
// W2/W3 dependency is held as an interface field on this struct so
// unit tests can inject fakes (07-PATTERNS.md test seam discipline).
type commit struct {
	opts Opts

	// Wired siblings — all overridable for test.
	stateStore StateStore
	locker     lock.Locker
	fetcher    manifestFetcher
	extractor  Extractor
	adapter    AdapterDispatcher
	differ     Differ

	// Resolved paths from step 0/1.
	achDir    string
	statePath string

	// TEST-ONLY SIGKILL injection seam consumed by 07-W4-01 sc2.
	//
	// injectSigkillAfterStep is populated once by readSigkillSeamFromEnv
	// at newCommit() entry. Under -tags=e2e the function reads
	// ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP from the environment;
	// under the default (release) build the function is a stub that
	// always returns 0, so the env var is never read and the seam is
	// disabled. Zero/unset/unparsable disables the seam (killFn never
	// invoked, no syscall, no overhead on the production path).
	//
	// killFn defaults to the build-tag-resolved default — under
	// -tags=e2e it invokes the OS-level SIGKILL syscall; under the
	// default build it is a no-op. Tests override to a recorder that
	// captures the would-be-killed step number without actually
	// crashing the process. Without the killFn indirection there
	// would be no way to assert the seam fires for a known step in
	// a unit test.
	//
	// WR-01 (07-W5-04): the seam was split into
	// sigkill_seam_{e2e,prod}.go behind //go:build {e2e,!e2e} so
	// release binaries cannot honor the env var even if it is set.
	// commit.go no longer references the env-var literal.
	//
	// TODO(post-Phase-7-close): remove this seam once SC#2 stabilizes
	// via a less invasive mechanism (e.g. an in-process synchronous
	// crash injection that does not require the env-var read at all).
	// The TODO marker is duplicated in doc.go so a grep for
	// "post-Phase-7-close" finds both touch points.
	injectSigkillAfterStep int
	killFn                 killFn
}

// Run is the single public entry point of the Phase 7 hydrate engine
// (CONTEXT.md Integration Points #2). It constructs a *commit via
// newCommit(opts) and invokes run(ctx). Errors flow back unwrapped so
// the caller layer (cmd/ach-cli/cmd/hydrate.go D-03 refactor) can
// dispatch via errors.As into the *exit.CodedError envelope.
func Run(ctx context.Context, opts Opts) (Result, error) {
	c, err := newCommit(opts)
	if err != nil {
		return Result{}, err
	}
	return c.run(ctx)
}

// newCommit builds a *commit with default DI wiring. The achDir + lock
// path are resolved up-front so a lock-construction failure surfaces
// before any other work. The default extractor + adapter are nil — W1
// stubs the step-7+8 and step-10 dispatch; W3-05 cobra wiring supplies
// real instances by setting fields on the returned *commit before
// dispatching to run().
//
// The TEST-ONLY SIGKILL seam value is sourced here via
// readSigkillSeamFromEnv (build-tag-resolved). Under -tags=e2e the
// function reads ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP from the
// environment exactly once; under the default build the function is a
// no-op stub that always returns 0. An invalid (non-numeric) env-var
// value silently disables the seam in the e2e build — fail-soft is
// correct because the seam is for test infrastructure, not the
// production exit-code contract.
func newCommit(opts Opts) (*commit, error) {
	// Normalize Stdout/Stderr to os.* if zero.
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	// Resolve workspaceCwd. The hydrate orchestrator does not call
	// os.Getwd itself — the cobra layer owns that resolution. If the
	// caller supplied opts.Output, that wins; otherwise we fall back
	// to "" which state.ResolvePath rejects in workspace scope (the
	// caller layer is expected to pre-resolve cwd before Run).
	workspaceCwd := opts.Output
	if workspaceCwd == "" && !opts.Global {
		// Best-effort cwd. ResolvePath will surface a clean
		// ErrInvalidPath if both Output and cwd are empty.
		if wd, err := os.Getwd(); err == nil {
			workspaceCwd = wd
		}
	}

	statePath, err := state.ResolvePath(workspaceCwd, opts.Environment, opts.Global)
	if err != nil {
		return nil, &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("resolve <ach-dir>: %v", err),
			Wrapped: err,
		}
	}
	achDir := filepath.Dir(statePath)

	c := &commit{
		opts:       opts,
		stateStore: defaultStateStore{},
		locker:     lock.NewLocker(lock.Path(achDir)),
		differ:     NewDiffer(),
		extractor:  opts.Extractor,
		adapter:    opts.AdapterDispatcher,
		achDir:     achDir,
		statePath:  statePath,
		killFn:     newDefaultKillFn(),
	}

	// Default fetcher closes over a Phase 6 httpclient.Client built
	// from opts.BaseURL + opts.Bearer + opts.Verbose. Production
	// callers populate BaseURL/Bearer before calling Run; unit tests
	// override c.fetcher with a fake so the closure's nil-BaseURL
	// path is never hit.
	hc := &httpclient.Client{
		BaseURL: opts.BaseURL,
		APIKey:  opts.Bearer,
		Verbose: opts.Verbose,
		Stderr:  opts.Stderr,
	}
	c.fetcher = func(ctx context.Context, environment string) (*manifest.Manifest, error) {
		return manifest.Fetch(ctx, hc, environment)
	}

	// Read the TEST-ONLY SIGKILL injection seam value exactly once.
	// Under -tags=e2e this reads ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP
	// from the environment; under the default (release) build this is
	// a no-op stub that always returns 0 — the env-var literal is not
	// present in the release binary at all (WR-01).
	c.injectSigkillAfterStep = readSigkillSeamFromEnv()

	return c, nil
}

// run executes the §6.7 14-step commit sequence. Each stepN is an
// unexported method on *commit; the dispatch loop here checks the
// TEST-ONLY SIGKILL seam after each step returns AND before stepN+1
// begins. Steps 7/8/9/10/11 short-circuit (W1 stub) when extractor +
// adapter are nil; the caller layer (W3-05) supplies concrete impls.
//
// Step 14 is implicit return.
func (c *commit) run(ctx context.Context) (Result, error) {
	var result Result

	// Record PlatformID up-front so the field is populated even on
	// early-exit paths (T-07-W5-01-02 — caller-supplied value reflected
	// in --verbose stderr only; residual log-spoofing accepted).
	result.PlatformID = c.opts.Platform

	// Step 1: lock.
	lease, err := c.step1Lock(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = lease.Release() }()
	c.maybeKill(1)

	// Step 2: sweep tmp.
	c.step2SweepTmp()
	c.maybeKill(2)

	// Step 3: read state + GuardEnvironment.
	existingState, err := c.step3ReadState()
	if err != nil {
		return result, err
	}
	c.maybeKill(3)

	// Step 4: reconcile vs disk (silent prune of missing-but-recorded).
	existingState, pruned := c.step4ReconcileVsDisk(existingState)
	result.FilesPruned = pruned
	c.maybeKill(4)

	// Step 5: manifest.
	m, err := c.step5Manifest(ctx)
	if err != nil {
		return result, err
	}
	c.maybeKill(5)

	// Step 6: scope-aware diff.
	diffTargets := c.step6Diff(m)
	c.maybeKill(6)

	// Steps 7-9: fetch / extract / hash+classify. The W3-05 concrete
	// extractorImpl folds Steps 8 + 9 (StageAndPublish + per-file hash
	// classification) into ExtractContent, so the maybeKill(8) and
	// maybeKill(9) hooks bracket "after extract" boundaries with no
	// per-step intermediate work — that is the intended W3-05 design.
	//
	// T-07-W5-01-03 — gate each disk-touching call on !c.opts.DryRun
	// so --dry-run remains a true read-only path.
	if c.extractor != nil && !c.opts.DryRun {
		for _, dt := range diffTargets {
			extractResult, err := c.extractor.ExtractContent(ctx, dt.Ref, c.achDir)
			if err != nil {
				// adapter / extractor errors that already carry a
				// CodedError envelope (e.g. exit.CollisionRefuse) flow
				// through unwrapped so the cobra layer maps the exit
				// code correctly. Non-coded transport failures get
				// wrapped as exit.General.
				var ce *exit.CodedError
				if errors.As(err, &ce) {
					return result, err
				}
				return result, &exit.CodedError{
					Code:    exit.General,
					Msg:     fmt.Sprintf("extract content (%s): %v", dt.Kind, err),
					Wrapped: err,
				}
			}
			result.FilesWritten += len(extractResult.WrittenFiles)
		}
	}
	c.maybeKill(7)
	c.maybeKill(8)
	c.maybeKill(9)

	// Step 10: adapter dispatch. RenderRuntime + per-FileWrite SAFE-04
	// cascade + atomic publish all live inside adapterDispatcherImpl;
	// the orchestrator just calls Render once after the extraction
	// loop completes.
	if c.adapter != nil && !c.opts.DryRun {
		renderResult, err := c.adapter.Render(ctx, m, existingState, c.achDir)
		if err != nil {
			// Preserve any *exit.CodedError produced by the dispatcher
			// (e.g. CollisionRefuse from extract.WrapCollisionRefuseError)
			// so the exit code survives. Non-coded errors get wrapped.
			var ce *exit.CodedError
			if errors.As(err, &ce) {
				return result, err
			}
			return result, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("adapter render: %v", err),
				Wrapped: err,
			}
		}
		result.FilesWritten += len(renderResult.WrittenFiles)
		result.DroppedComponents = append(result.DroppedComponents, renderResult.DroppedComponents...)
	}
	c.maybeKill(10)

	// Step 11: STATE-05 / D-16 inverse-merge sync. maybeKill(11)
	// fires BEFORE the syncFn call so the SIGKILL injection point
	// remains at the step-11 boundary as advertised by
	// sc2_commit_sequence_sigkill. T-07-W5-01-03 — gated on !DryRun.
	c.maybeKill(11)
	if c.opts.Sync && !c.opts.DryRun {
		// TODO(STATE-05 composition): newFile arg is the composed
		// next-state once step12 builds it from
		// ExtractResult/RenderResult — for now, pass existingState as
		// a safe no-op until the composition follow-up plan lands.
		// Sync is wired so future composition automatically activates
		// STATE-05 inverse-merge.
		stats, err := syncFn(existingState, existingState, c.achDir, SyncOptions{
			Force:  c.opts.Force,
			Stderr: c.opts.Stderr,
		})
		if err != nil {
			var ce *exit.CodedError
			if errors.As(err, &ce) {
				return result, err
			}
			return result, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("sync inverse-merge: %v", err),
				Wrapped: err,
			}
		}
		result.FilesPruned += stats.Pruned
		result.FilesPreserved += stats.Preserved
	}

	// Step 12: atomic state write.
	if err := c.step12WriteState(existingState, m); err != nil {
		return result, err
	}
	c.maybeKill(12)

	// Step 13: cleanup tmp.
	c.step13Cleanup()
	c.maybeKill(13)

	// Step 14: return is implicit.
	return result, nil
}

// syncFn is the package-level test seam wrapping Sync. Production
// callers leave it at its default (= Sync); unit tests in this package
// swap it for a recorder to verify the step-11 wiring fires (or does
// not fire) under the expected conditions. Restoring the default is
// the test's responsibility (t.Cleanup).
var syncFn = Sync

// maybeKill is the TEST-ONLY SIGKILL injection dispatch. It is called
// after each stepN returns AND BEFORE stepN+1 begins. When
// injectSigkillAfterStep matches N, c.killFn(N) fires — under
// -tags=e2e c.killFn invokes the OS-level SIGKILL syscall so the
// process dies before the next step; test killFn records the step
// into a recorder so the test can assert the seam fired at the
// expected boundary. Under the default (release) build c.killFn is
// a no-op.
//
// Zero/unset injectSigkillAfterStep short-circuits before any call —
// no overhead on the production path beyond the int comparison.
func (c *commit) maybeKill(step int) {
	if c.injectSigkillAfterStep == step {
		c.killFn(step)
	}
}

// noopLease is the safe zero-value Lease used when step1 fails before
// acquiring a real lease — the deferred Release call in run() still
// has a non-nil receiver and the defer is a no-op.
type noopLease struct{}

func (noopLease) Release() error { return nil }

// step1Lock — flock(LOCK_EX) on <ach-dir>/lock. Mode dispatch on
// opts.Wait + opts.LockTimeout per spec §6.7 step 1.
//
// Mode dispatch:
//   - opts.Wait                                → lock.AcquireWait
//   - opts.LockTimeout > 0 (and !opts.Wait)    → lock.AcquireWithTimeout
//   - otherwise (default)                      → lock.AcquireFailFast
//
// ErrLockContended + ErrLockTimeout both map to exit.General (1) per
// spec §6.7; the user-facing message is set here so the caller layer
// can rely on a stable surface.
func (c *commit) step1Lock(ctx context.Context) (lock.Lease, error) {
	// Ensure parent dir exists so the locker can create the lock file.
	if err := os.MkdirAll(c.achDir, 0o755); err != nil {
		return noopLease{}, &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("create <ach-dir>: %v", err),
			Wrapped: err,
		}
	}

	mode := lock.AcquireFailFast
	timeout := time.Duration(0)
	switch {
	case c.opts.Wait:
		mode = lock.AcquireWait
	case c.opts.LockTimeout > 0:
		mode = lock.AcquireWithTimeout
		timeout = c.opts.LockTimeout
	}

	lease, err := c.locker.Acquire(ctx, mode, timeout)
	if err != nil {
		switch {
		case errors.Is(err, lock.ErrLockContended):
			return noopLease{}, &exit.CodedError{
				Code:    exit.General,
				Msg:     "another ach-cli is running on this workspace; use --wait or --lock-timeout",
				Wrapped: err,
			}
		case errors.Is(err, lock.ErrLockTimeout):
			return noopLease{}, &exit.CodedError{
				Code:    exit.General,
				Msg:     "lock acquisition timed out",
				Wrapped: err,
			}
		default:
			return noopLease{}, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("acquire lock: %v", err),
				Wrapped: err,
			}
		}
	}
	return lease, nil
}

// step2SweepTmp — unconditional state.SweepTmp(achDir). Errors are
// swallowed per spec §6.7 step 2 ("benign cleanup, never aborts
// hydrate").
func (c *commit) step2SweepTmp() {
	_ = state.SweepTmp(c.achDir)
}

// step3ReadState — state.Load + GuardEnvironment. Schema mismatch
// → exit.SchemaMismatch (5) unless opts.Force. Environment mismatch
// → exit.EnvironmentMismatch (4) unless opts.Force. Both bypasses
// log a warning to opts.Stderr before proceeding.
//
// When state.Load fails with ErrSchemaMismatch and --force is set,
// the caller proceeds with a fresh File (nil-equivalent) so the
// next state.Save rewrites the file to the v2 schema.
func (c *commit) step3ReadState() (*state.File, error) {
	loaded, err := c.stateStore.Load(c.statePath)
	if err != nil {
		if errors.Is(err, state.ErrSchemaMismatch) {
			if !c.opts.Force {
				return nil, &exit.CodedError{
					Code:    exit.SchemaMismatch,
					Msg:     fmt.Sprintf("state.json schema mismatch: %v (use --force to overwrite)", err),
					Wrapped: err,
				}
			}
			// --force path: warn + proceed with fresh state.
			_, _ = fmt.Fprintf(c.opts.Stderr,
				"warning: --force bypassing state.json schemaVersion mismatch (%v); state will be rewritten on commit\n", err)
			loaded = nil
		} else {
			return nil, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("read state.json: %v", err),
				Wrapped: err,
			}
		}
	}

	if err := c.stateStore.GuardEnvironment(loaded, c.opts.Environment, c.opts.Force); err != nil {
		if errors.Is(err, state.ErrEnvironmentGuard) {
			return nil, &exit.CodedError{
				Code:    exit.EnvironmentMismatch,
				Msg:     fmt.Sprintf("environment guard tripped: %v (use --force to override)", err),
				Wrapped: err,
			}
		}
		return nil, &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("guard environment: %v", err),
			Wrapped: err,
		}
	}
	if c.opts.Force && loaded != nil && loaded.Environment != "" && loaded.Environment != c.opts.Environment {
		// --force override of environment mismatch — warn loudly.
		_, _ = fmt.Fprintf(c.opts.Stderr,
			"warning: --force bypassing environment guard (have=%q want=%q); state.json will be rebound\n",
			loaded.Environment, c.opts.Environment)
	}
	return loaded, nil
}

// step4ReconcileVsDisk — silent prune of state entries whose target
// is missing on disk (STATE-04 "tracked file missing on disk →
// silently pruned"). Returns the rebuilt File plus the prune count
// for Result.FilesPruned. A nil input means no prior state (fresh
// hydrate) — returns (nil, 0).
func (c *commit) step4ReconcileVsDisk(loaded *state.File) (*state.File, int) {
	if loaded == nil {
		return nil, 0
	}
	pruned := 0
	loaded.Prompts, pruned = c.pruneMissing(loaded.Prompts, pruned)
	loaded.Plugins, pruned = c.pruneMissing(loaded.Plugins, pruned)
	loaded.Artifacts, pruned = c.pruneMissing(loaded.Artifacts, pruned)
	loaded.RuntimeFiles, pruned = c.pruneMissing(loaded.RuntimeFiles, pruned)
	loaded.Adapter.Files, pruned = c.pruneMissing(loaded.Adapter.Files, pruned)
	return loaded, pruned
}

// pruneMissing walks a FileEntry slice and rebuilds it with only the
// entries whose Target stat'd successfully. fs.ErrNotExist is the
// silent-drop case; any other error keeps the entry (let the next
// stage error appropriately rather than masking a real I/O fault here).
func (c *commit) pruneMissing(entries []state.FileEntry, pruned int) ([]state.FileEntry, int) {
	if len(entries) == 0 {
		return entries, pruned
	}
	kept := entries[:0]
	for _, e := range entries {
		target := e.Target
		if !filepath.IsAbs(target) {
			target = filepath.Join(c.achDir, "..", target)
		}
		if _, err := os.Stat(target); err != nil && errors.Is(err, fs.ErrNotExist) {
			pruned++
			continue
		}
		kept = append(kept, e)
	}
	return kept, pruned
}

// step5Manifest — POST /platform/hydrate via c.fetcher. Schema
// mismatch (manifest.ErrSchemaMismatch) → exit.SchemaMismatch (5)
// unless opts.Force. Other errors flow through as transport/general
// failures; *httpclient.ServerError unwrap is the caller layer's
// responsibility (exit.MapServerError owns 401/403/503/504).
func (c *commit) step5Manifest(ctx context.Context) (*manifest.Manifest, error) {
	m, err := c.fetcher(ctx, c.opts.Environment)
	if err != nil {
		if errors.Is(err, manifest.ErrSchemaMismatch) {
			if !c.opts.Force {
				return nil, &exit.CodedError{
					Code:    exit.SchemaMismatch,
					Msg:     fmt.Sprintf("manifest schema mismatch: %v (use --force to override)", err),
					Wrapped: err,
				}
			}
			// --force on manifest schema is dangerous — the engine has no
			// way to decode an unknown shape, so we still cannot proceed.
			// Warn and surface the original error.
			_, _ = fmt.Fprintf(c.opts.Stderr,
				"warning: --force cannot override manifest schemaVersion mismatch (no v2 reader code shipped)\n")
			return nil, &exit.CodedError{
				Code:    exit.SchemaMismatch,
				Msg:     fmt.Sprintf("manifest schema mismatch: %v", err),
				Wrapped: err,
			}
		}
		return nil, err
	}
	return m, nil
}

// diffTarget is the intermediate W1 typed-tuple step 6 emits. Each
// entry names the upstream content the orchestrator MAY fetch in step
// 7. W2/W3 consume this slice; W1 unit tests assert the right entries
// are produced for a given scope filter (STATE-10).
type diffTarget struct {
	// Kind names the resource category (prompt / plugin / artifact /
	// model / mcpServer / a2aAgent). Used for routing in step 7+.
	Kind string

	// Ref is the manifest entry whose downloadUrl/endpoint feeds the
	// next stage. The fields used depend on Kind: context Kinds use
	// Ref.DownloadURL, runtime Kinds use Ref.Endpoint.
	Ref manifest.ContentRef
}

// step6Diff — STATE-10 scope-aware iteration. Builds the slice of
// upstream content refs that subsequent stages will fetch/extract/
// classify. Out-of-scope state slices are NOT touched per spec §6.3.
//
// Scope filter:
//   - opts.OnlyRuntime           → runtime only (skip context entirely)
//   - opts.IncludeRuntime        → runtime + context
//   - default                    → context only (skip runtime entirely)
func (c *commit) step6Diff(m *manifest.Manifest) []diffTarget {
	var targets []diffTarget
	if m == nil {
		return targets
	}

	includeContext := !c.opts.OnlyRuntime
	includeRuntime := c.opts.OnlyRuntime || c.opts.IncludeRuntime

	if includeContext && m.Context != nil {
		for _, p := range m.Context.Prompts {
			targets = append(targets, diffTarget{Kind: "prompt", Ref: p})
		}
		for _, p := range m.Context.Plugins {
			targets = append(targets, diffTarget{Kind: "plugin", Ref: p})
		}
		for _, a := range m.Context.Artifacts {
			targets = append(targets, diffTarget{Kind: "artifact", Ref: a})
		}
	}
	if includeRuntime && m.Runtime != nil {
		for _, r := range m.Runtime.Models {
			targets = append(targets, diffTarget{Kind: "model", Ref: r})
		}
		for _, r := range m.Runtime.MCPServers {
			targets = append(targets, diffTarget{Kind: "mcpServer", Ref: r})
		}
		for _, r := range m.Runtime.A2AAgents {
			targets = append(targets, diffTarget{Kind: "a2aAgent", Ref: r})
		}
	}
	return targets
}

// step12WriteState — atomic state.json publication via state.Save
// (= state.WriteAtomic, STATE-07 four-step contract). Skipped when
// opts.DryRun is set so a `hydrate --dry-run` is genuinely read-only.
//
// W1 writes a minimal state.File derived from the prior state +
// the fresh manifest's environment. Once W2/W3 land their concrete
// Extractor + AdapterDispatcher, the FileEntries flow back from
// ExtractResult.WrittenFiles / RenderResult.WrittenFiles and step 12
// composes them into the final state.File before the atomic write.
func (c *commit) step12WriteState(existing *state.File, m *manifest.Manifest) error {
	if c.opts.DryRun {
		return nil
	}

	// Build the next-state from the prior + the manifest environment.
	next := &state.File{
		SchemaVersion: "2",
		Environment:   c.opts.Environment,
	}
	if existing != nil {
		// Preserve everything W2/W3 haven't yet been called to update.
		next.Deployment = existing.Deployment
		next.Prompts = existing.Prompts
		next.Plugins = existing.Plugins
		next.Artifacts = existing.Artifacts
		next.RuntimeFiles = existing.RuntimeFiles
		next.Adapter = existing.Adapter
	}
	if m != nil && m.Environment != "" && next.Environment == "" {
		next.Environment = m.Environment
	}

	if err := c.stateStore.Save(c.statePath, next); err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("write state.json: %v", err),
			Wrapped: err,
		}
	}
	return nil
}

// step13Cleanup — final state.SweepTmp(achDir). Errors swallowed per
// spec §6.7.
func (c *commit) step13Cleanup() {
	_ = state.SweepTmp(c.achDir)
}

// defaultStateStore is the package-private StateStore wrapping
// state.Load + state.Save + state.GuardEnvironment verbatim. Unit
// tests inject their own StateStore implementation.
type defaultStateStore struct{}

func (defaultStateStore) Load(path string) (*state.File, error) {
	return state.Load(path)
}

func (defaultStateStore) Save(path string, f *state.File) error {
	return state.Save(path, f)
}

func (defaultStateStore) GuardEnvironment(existing *state.File, requested string, force bool) error {
	return state.GuardEnvironment(existing, requested, force)
}

// Compile-time assertion that http.MethodPost (manifest.Fetch's verb)
// is used somewhere — the linter would otherwise prune the
// net/http import the codepath docs reference. Cheap and harmless.
var _ = http.MethodPost

// Compile-time assertion that io.Writer (Stdout/Stderr) is the right
// type — defends against a future refactor of Opts.
var _ io.Writer = (*os.File)(nil)
