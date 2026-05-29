// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/lock"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// ---- fakes ----

// fakeStateStore is the test-side StateStore. loadFn / saveFn /
// guardFn are nil-safe — nil returns the zero-value defaults
// (nil, nil for Load; nil for Save; nil for GuardEnvironment).
// saveCalled accumulates the most-recent Save target for assertion.
type fakeStateStore struct {
	loadFn  func(path string) (*state.File, error)
	saveFn  func(path string, f *state.File) error
	guardFn func(existing *state.File, requested string, force bool) error

	savedFile *state.File
	savedPath string
	saveCount int
}

func (f *fakeStateStore) Load(path string) (*state.File, error) {
	if f.loadFn != nil {
		return f.loadFn(path)
	}
	return nil, nil
}

func (f *fakeStateStore) Save(path string, file *state.File) error {
	f.saveCount++
	f.savedPath = path
	f.savedFile = file
	if f.saveFn != nil {
		return f.saveFn(path, file)
	}
	return nil
}

func (f *fakeStateStore) GuardEnvironment(existing *state.File, requested string, force bool) error {
	if f.guardFn != nil {
		return f.guardFn(existing, requested, force)
	}
	return nil
}

// fakeLocker — Acquire returns acquireErr (if non-nil) or a noop
// lease. acquireCount tracks invocations.
type fakeLocker struct {
	acquireErr   error
	acquireCount int
}

func (f *fakeLocker) Acquire(_ context.Context, _ lock.AcquireMode, _ time.Duration) (lock.Lease, error) {
	f.acquireCount++
	if f.acquireErr != nil {
		return nil, f.acquireErr
	}
	return fakeLease{}, nil
}

type fakeLease struct{}

func (fakeLease) Release() error { return nil }

// fakeExtractor implements hydrate.Extractor for unit tests. Each
// ExtractContent invocation increments calls and returns the canned
// result (or err if non-nil). Default values produce a zero-byte
// ExtractResult — useful for "did the orchestrator call me" assertions.
type fakeExtractor struct {
	calls  *int
	result ExtractResult
	err    error
}

func (f fakeExtractor) ExtractContent(_ context.Context, _ manifest.ContentRef, _ string) (ExtractResult, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.err != nil {
		return ExtractResult{}, f.err
	}
	return f.result, nil
}

// fakeAdapterDispatcher implements hydrate.AdapterDispatcher for unit
// tests. Render records call count + returns canned RenderResult.
type fakeAdapterDispatcher struct {
	calls  *int
	result RenderResult
	err    error
}

func (f fakeAdapterDispatcher) Render(_ context.Context, _ *manifest.Manifest, _ *state.File, _ string) (RenderResult, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.err != nil {
		return RenderResult{}, f.err
	}
	return f.result, nil
}

// ---- builder ----

// newTestCommit constructs a *commit with all fakes wired and an
// achDir under t.TempDir(). The defaults emit a happy-path
// (empty-manifest) commit; tests override individual fields before
// calling c.run.
func newTestCommit(t *testing.T) (*commit, *fakeStateStore, *fakeLocker) {
	t.Helper()
	dir := t.TempDir()

	store := &fakeStateStore{}
	locker := &fakeLocker{}

	c := &commit{
		opts: Opts{
			Environment: "demo",
			Stdout:      &bytes.Buffer{},
			Stderr:      &bytes.Buffer{},
		},
		stateStore: store,
		locker:     locker,
		fetcher: func(_ context.Context, _ string) (*manifest.Manifest, error) {
			return &manifest.Manifest{
				SchemaVersion: "v1alpha1",
				Environment:   "demo",
				Runtime:       &manifest.RuntimeBlock{},
				Context:       &manifest.ContextBlock{},
			}, nil
		},
		differ:    NewDiffer(),
		achDir:    dir,
		statePath: filepath.Join(dir, "state.json"),
		killFn:    defaultKillFn, // overridden in SIGKILL seam tests.
	}
	return c, store, locker
}

// ---- tests ----

// TestCommit_HappyPath drives commit.run end-to-end with stubs +
// fakes. Asserts no error, no Result counts populated (W1 stubs
// short-circuit), and state.Save called exactly once at step 12.
func TestCommit_HappyPath(t *testing.T) {
	c, store, locker := newTestCommit(t)

	result, err := c.run(context.Background())
	if err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if locker.acquireCount != 1 {
		t.Errorf("locker.Acquire calls = %d, want 1", locker.acquireCount)
	}
	if store.saveCount != 1 {
		t.Errorf("store.Save calls = %d, want 1 (step 12)", store.saveCount)
	}
	if store.savedFile == nil {
		t.Fatalf("store.savedFile = nil; expected a state.File at step 12")
	}
	if store.savedFile.SchemaVersion != "2" {
		t.Errorf("savedFile.SchemaVersion = %q, want %q", store.savedFile.SchemaVersion, "2")
	}
	if store.savedFile.Environment != "demo" {
		t.Errorf("savedFile.Environment = %q, want %q", store.savedFile.Environment, "demo")
	}
	if result.FilesWritten != 0 || result.FilesPreserved != 0 || result.FilesPruned != 0 {
		t.Errorf("Result counts non-zero on W1 stub path: %+v", result)
	}
}

// TestCommit_DryRun_NoStateWrite asserts step 12 is skipped under
// opts.DryRun — the read+diff path still runs but no state.json is
// published.
func TestCommit_DryRun_NoStateWrite(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.DryRun = true

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if store.saveCount != 0 {
		t.Errorf("store.Save calls under --dry-run = %d, want 0", store.saveCount)
	}
}

// TestCommit_GuardEnvironmentMismatch_ExitCode4 seeds a state.File
// with Environment="prod" and Opts{Environment: "demo"}; the
// fakeStateStore's guardFn returns state.ErrEnvironmentGuard; assert
// the returned error wraps exit.EnvironmentMismatch (code 4).
func TestCommit_GuardEnvironmentMismatch_ExitCode4(t *testing.T) {
	c, store, _ := newTestCommit(t)
	store.loadFn = func(_ string) (*state.File, error) {
		return &state.File{SchemaVersion: "2", Environment: "prod"}, nil
	}
	store.guardFn = func(_ *state.File, _ string, _ bool) error {
		return fmt.Errorf("test guard: %w", state.ErrEnvironmentGuard)
	}

	_, err := c.run(context.Background())
	if err == nil {
		t.Fatal("c.run = nil error; want CodedError with EnvironmentMismatch")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *exit.CodedError: %T %v", err, err)
	}
	if ce.Code != exit.EnvironmentMismatch {
		t.Errorf("ce.Code = %d, want exit.EnvironmentMismatch (%d)", ce.Code, exit.EnvironmentMismatch)
	}
}

// TestCommit_GuardEnvironmentMismatch_ForceBypasses asserts that
// --force flips Opts.Force=true and step3 calls GuardEnvironment with
// force=true — the fakeStateStore's guardFn observes the force arg
// and returns nil; c.run proceeds to the next stage.
func TestCommit_GuardEnvironmentMismatch_ForceBypasses(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Force = true
	c.opts.Environment = "demo"

	var sawForce bool
	store.loadFn = func(_ string) (*state.File, error) {
		return &state.File{SchemaVersion: "2", Environment: "prod"}, nil
	}
	store.guardFn = func(_ *state.File, _ string, force bool) error {
		sawForce = force
		if !force {
			return fmt.Errorf("test guard: %w", state.ErrEnvironmentGuard)
		}
		return nil
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run under --force = %v, want nil (force bypasses guard)", err)
	}
	if !sawForce {
		t.Error("guardFn was not called with force=true")
	}
}

// TestCommit_SchemaMismatch_ExitCode5 — fakeStateStore.Load returns
// state.ErrSchemaMismatch; assert c.run returns CodedError with
// exit.SchemaMismatch (code 5).
func TestCommit_SchemaMismatch_ExitCode5(t *testing.T) {
	c, store, _ := newTestCommit(t)
	store.loadFn = func(_ string) (*state.File, error) {
		return nil, fmt.Errorf("test: %w", state.ErrSchemaMismatch)
	}

	_, err := c.run(context.Background())
	if err == nil {
		t.Fatal("c.run = nil error; want CodedError with SchemaMismatch")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *exit.CodedError: %T %v", err, err)
	}
	if ce.Code != exit.SchemaMismatch {
		t.Errorf("ce.Code = %d, want exit.SchemaMismatch (%d)", ce.Code, exit.SchemaMismatch)
	}
}

// TestCommit_SchemaMismatch_ForceProceeds — Load returns
// ErrSchemaMismatch + opts.Force=true → c.run warns to stderr and
// proceeds with a fresh state, eventually Saving the new v2 file.
func TestCommit_SchemaMismatch_ForceProceeds(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Force = true
	stderr := &bytes.Buffer{}
	c.opts.Stderr = stderr
	store.loadFn = func(_ string) (*state.File, error) {
		return nil, fmt.Errorf("test: %w", state.ErrSchemaMismatch)
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run under --force = %v, want nil", err)
	}
	if store.saveCount != 1 {
		t.Errorf("store.Save calls = %d, want 1", store.saveCount)
	}
	if !strings.Contains(stderr.String(), "schemaVersion mismatch") {
		t.Errorf("stderr did not surface schemaVersion warning: %q", stderr.String())
	}
}

// TestCommit_ManifestSchemaMismatch_ExitCode5 — fetcher returns
// manifest.ErrSchemaMismatch; assert CodedError with
// exit.SchemaMismatch.
func TestCommit_ManifestSchemaMismatch_ExitCode5(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.fetcher = func(_ context.Context, _ string) (*manifest.Manifest, error) {
		return nil, fmt.Errorf("test: %w", manifest.ErrSchemaMismatch)
	}

	_, err := c.run(context.Background())
	if err == nil {
		t.Fatal("c.run = nil error; want CodedError with SchemaMismatch")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *exit.CodedError: %T %v", err, err)
	}
	if ce.Code != exit.SchemaMismatch {
		t.Errorf("ce.Code = %d, want exit.SchemaMismatch (%d)", ce.Code, exit.SchemaMismatch)
	}
}

// TestCommit_LockContended_ExitCode1 — fakeLocker returns
// ErrLockContended; assert CodedError with exit.General (1).
func TestCommit_LockContended_ExitCode1(t *testing.T) {
	c, _, locker := newTestCommit(t)
	locker.acquireErr = fmt.Errorf("test contention: %w", lock.ErrLockContended)

	_, err := c.run(context.Background())
	if err == nil {
		t.Fatal("c.run = nil error; want CodedError with General")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *exit.CodedError: %T %v", err, err)
	}
	if ce.Code != exit.General {
		t.Errorf("ce.Code = %d, want exit.General (%d)", ce.Code, exit.General)
	}
	if !strings.Contains(ce.Msg, "another ach-cli") {
		t.Errorf("ce.Msg = %q, want substring 'another ach-cli'", ce.Msg)
	}
}

// TestCommit_LockTimeout_ExitCode1 — fakeLocker returns ErrLockTimeout
// when opts.LockTimeout > 0; assert CodedError with exit.General (1)
// and the user-facing "lock acquisition timed out" message.
func TestCommit_LockTimeout_ExitCode1(t *testing.T) {
	c, _, locker := newTestCommit(t)
	c.opts.LockTimeout = 100 * time.Millisecond
	locker.acquireErr = fmt.Errorf("test timeout: %w", lock.ErrLockTimeout)

	_, err := c.run(context.Background())
	if err == nil {
		t.Fatal("c.run = nil error; want CodedError with General")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *exit.CodedError: %T %v", err, err)
	}
	if ce.Code != exit.General {
		t.Errorf("ce.Code = %d, want exit.General (%d)", ce.Code, exit.General)
	}
	if !strings.Contains(ce.Msg, "timed out") {
		t.Errorf("ce.Msg = %q, want substring 'timed out'", ce.Msg)
	}
}

// TestCommit_SigkillSeam_ReachableForKnownStep injects a recorder
// killFn, sets injectSigkillAfterStep = 11, runs c.run, asserts the
// recorder captured exactly 11 (and only 11 — no other steps fired
// the seam). This proves the seam is reachable for the W4-01 sc2
// step-11 boundary case.
func TestCommit_SigkillSeam_ReachableForKnownStep(t *testing.T) {
	c, _, _ := newTestCommit(t)
	var recorded []int
	c.killFn = func(step int) { recorded = append(recorded, step) }
	c.injectSigkillAfterStep = 11

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if len(recorded) != 1 || recorded[0] != 11 {
		t.Errorf("recorded killFn calls = %v, want exactly [11]", recorded)
	}
}

// TestCommit_SigkillSeam_DisabledByDefault asserts the seam is silent
// when injectSigkillAfterStep == 0 (production default). killFn is
// installed but should never be invoked across the full 13-step run.
func TestCommit_SigkillSeam_DisabledByDefault(t *testing.T) {
	c, _, _ := newTestCommit(t)
	var recorded []int
	c.killFn = func(step int) { recorded = append(recorded, step) }
	// injectSigkillAfterStep left at zero.

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if len(recorded) != 0 {
		t.Errorf("seam fired despite injectSigkillAfterStep=0: recorded=%v", recorded)
	}
}

// TestCommit_SigkillSeam_FiresForEachKnownStep iterates 1..13 and
// asserts the seam fires at the expected boundary for each. This is
// the regression gate against a future refactor that accidentally
// drops one of the maybeKill calls.
func TestCommit_SigkillSeam_FiresForEachKnownStep(t *testing.T) {
	for step := 1; step <= 13; step++ {
		t.Run(fmt.Sprintf("step%d", step), func(t *testing.T) {
			c, _, _ := newTestCommit(t)
			var recorded []int
			c.killFn = func(s int) { recorded = append(recorded, s) }
			c.injectSigkillAfterStep = step

			if _, err := c.run(context.Background()); err != nil {
				t.Fatalf("c.run = %v, want nil", err)
			}
			if len(recorded) != 1 || recorded[0] != step {
				t.Errorf("step %d: recorded = %v, want [%d]", step, recorded, step)
			}
		})
	}
}

// TestCommit_NewCommit_ReadsSigkillEnvVar (relocated to
// commit_sigkill_seam_test.go behind //go:build e2e by 07-W5-04 Task
// 2 — under the default release build the env var is not read at
// all, so the test only makes sense under -tags=e2e). The
// complementary release-build assertion that the env var is IGNORED
// in release builds lives in commit_release_build_test.go behind
// //go:build !e2e.

// TestNewCommit_PopulatesExtractorAndAdapter asserts that the
// Extractor + AdapterDispatcher fields on Opts are read by newCommit
// and assigned to the *commit struct's extractor / adapter slots
// (07-W5-01 gap closure — the W1 stub fall-through is preserved when
// the fields are nil, but production callers MUST be able to inject
// real impls).
func TestNewCommit_PopulatesExtractorAndAdapter(t *testing.T) {
	fakeExt := fakeExtractor{}
	fakeAd := fakeAdapterDispatcher{}

	c, err := newCommit(Opts{
		Output:            t.TempDir(),
		Environment:       "demo",
		Extractor:         fakeExt,
		AdapterDispatcher: fakeAd,
	})
	if err != nil {
		t.Fatalf("newCommit = %v, want nil", err)
	}
	if c.extractor == nil {
		t.Errorf("c.extractor = nil; want non-nil (opts.Extractor was set)")
	}
	if c.adapter == nil {
		t.Errorf("c.adapter = nil; want non-nil (opts.AdapterDispatcher was set)")
	}

	// Sanity: when both fields are zero on Opts, the W1 stub
	// fall-through is preserved (c.extractor == nil + c.adapter == nil).
	c2, err := newCommit(Opts{
		Output:      t.TempDir(),
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("newCommit (nil seams) = %v, want nil", err)
	}
	if c2.extractor != nil {
		t.Errorf("c2.extractor = %v; want nil (Opts.Extractor was zero)", c2.extractor)
	}
	if c2.adapter != nil {
		t.Errorf("c2.adapter = %v; want nil (Opts.AdapterDispatcher was zero)", c2.adapter)
	}
}

// TestCommit_NewCommit_UnparsableSigkillEnvVar_LeavesZero (relocated
// to commit_sigkill_seam_test.go behind //go:build e2e by 07-W5-04
// Task 2 — under the default release build the env var is not read
// at all, so the test only makes sense under -tags=e2e).

// TestCommit_Step4_PrunesMissingFiles seeds a state.File with an
// entry whose Target does not exist on disk; assert the entry is
// silently dropped from the in-memory state AND the prune count is
// reflected in Result.FilesPruned (STATE-04 "tracked file missing on
// disk → silently pruned").
func TestCommit_Step4_PrunesMissingFiles(t *testing.T) {
	c, store, _ := newTestCommit(t)
	// State references a file that doesn't exist on disk.
	store.loadFn = func(_ string) (*state.File, error) {
		return &state.File{
			SchemaVersion: "2",
			Environment:   "demo",
			Prompts: []state.FileEntry{
				{Target: "nonexistent.md", Hash: "xxh3:aaaa", SourceHash: "xxh3:aaaa"},
			},
		}, nil
	}

	result, err := c.run(context.Background())
	if err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if result.FilesPruned != 1 {
		t.Errorf("result.FilesPruned = %d, want 1", result.FilesPruned)
	}
	// The saved state.File should have the missing entry pruned.
	if store.savedFile == nil {
		t.Fatal("savedFile = nil; expected step 12 to save")
	}
	if got := len(store.savedFile.Prompts); got != 0 {
		t.Errorf("savedFile.Prompts len = %d, want 0 (pruned)", got)
	}
}

// TestRun_InvokesExtractorPerDiffTarget seeds a manifest with two
// context Prompts so step6Diff produces 2 diffTargets; injects a
// fakeExtractor (calls counter) and a fakeAdapterDispatcher (calls
// counter, returns one FileWrite). Asserts: extractor invoked exactly
// 2 times, adapter invoked exactly 1 time, Result.FilesWritten == 3
// (2 from extract + 1 from render), Result.PlatformID matches the
// caller-supplied opts.Platform value.
func TestRun_InvokesExtractorPerDiffTarget(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.fetcher = func(_ context.Context, _ string) (*manifest.Manifest, error) {
		return &manifest.Manifest{
			SchemaVersion: "v1alpha1",
			Environment:   "demo",
			Runtime:       &manifest.RuntimeBlock{},
			Context: &manifest.ContextBlock{
				Prompts: []manifest.ContentRef{
					{ID: "p1", DownloadURL: "http://local/content/prompt/p1"},
					{ID: "p2", DownloadURL: "http://local/content/prompt/p2"},
				},
			},
		}, nil
	}
	var extCalls, adCalls int
	c.extractor = fakeExtractor{
		calls: &extCalls,
		result: ExtractResult{
			WrittenFiles: []FileWrite{{Target: "x", Hash: "xxh3:00"}},
		},
	}
	c.adapter = fakeAdapterDispatcher{
		calls: &adCalls,
		result: RenderResult{
			WrittenFiles: []FileWrite{{Target: "y", Hash: "xxh3:01"}},
		},
	}

	result, err := c.run(context.Background())
	if err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if extCalls != 2 {
		t.Errorf("extractor calls = %d, want 2 (one per diffTarget)", extCalls)
	}
	if adCalls != 1 {
		t.Errorf("adapter calls = %d, want 1 (once after extraction)", adCalls)
	}
	// 2 extract + 1 render = 3 file writes recorded.
	if result.FilesWritten != 3 {
		t.Errorf("result.FilesWritten = %d, want 3", result.FilesWritten)
	}
	if result.PlatformID != "claude-code" {
		t.Errorf("result.PlatformID = %q, want %q", result.PlatformID, "claude-code")
	}
}

// TestRun_DryRun_SkipsExtractorAndAdapter asserts that --dry-run gates
// every disk-touching call site introduced in 07-W5-01: extractor +
// adapter MUST NOT be invoked. Result.FilesWritten stays at zero.
func TestRun_DryRun_SkipsExtractorAndAdapter(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.DryRun = true
	c.fetcher = func(_ context.Context, _ string) (*manifest.Manifest, error) {
		return &manifest.Manifest{
			SchemaVersion: "v1alpha1",
			Environment:   "demo",
			Runtime:       &manifest.RuntimeBlock{},
			Context: &manifest.ContextBlock{
				Prompts: []manifest.ContentRef{
					{ID: "p1", DownloadURL: "http://local/content/prompt/p1"},
				},
			},
		}, nil
	}
	var extCalls, adCalls int
	c.extractor = fakeExtractor{calls: &extCalls}
	c.adapter = fakeAdapterDispatcher{calls: &adCalls}

	result, err := c.run(context.Background())
	if err != nil {
		t.Fatalf("c.run --dry-run = %v, want nil", err)
	}
	if extCalls != 0 {
		t.Errorf("extractor calls under --dry-run = %d, want 0", extCalls)
	}
	if adCalls != 0 {
		t.Errorf("adapter calls under --dry-run = %d, want 0", adCalls)
	}
	if result.FilesWritten != 0 {
		t.Errorf("result.FilesWritten under --dry-run = %d, want 0", result.FilesWritten)
	}
}

// TestRun_Step11Sync_InvokedWhenSyncOptSet asserts that the step-11
// Sync wiring fires when opts.Sync is true. Uses the syncFn package
// seam to swap in a recorder and verify the call args match the
// orchestrator contract: prev == existingState, opts.Force flows
// through, opts.Stderr is the orchestrator's stderr.
func TestRun_Step11Sync_InvokedWhenSyncOptSet(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Sync = true
	store.loadFn = func(_ string) (*state.File, error) {
		return &state.File{
			SchemaVersion: "2",
			Environment:   "demo",
		}, nil
	}

	prevSync := syncFn
	t.Cleanup(func() { syncFn = prevSync })
	var calls int
	var sawForce bool
	syncFn = func(_, _ *state.File, _ string, opts SyncOptions) (SyncStats, error) {
		calls++
		sawForce = opts.Force
		return SyncStats{Pruned: 0, Preserved: 0}, nil
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("syncFn calls = %d, want 1", calls)
	}
	if sawForce {
		t.Error("syncFn observed Force=true; want Force=false (opts.Force was unset)")
	}
}

// TestRun_Step11Sync_NotInvokedWhenSyncOptUnset asserts the step-11
// wiring stays inert when opts.Sync == false (the default — non-sync
// invocations are no-ops at the step-11 boundary).
func TestRun_Step11Sync_NotInvokedWhenSyncOptUnset(t *testing.T) {
	c, _, _ := newTestCommit(t)
	// c.opts.Sync left at zero-value (false).

	prevSync := syncFn
	t.Cleanup(func() { syncFn = prevSync })
	var calls int
	syncFn = func(_, _ *state.File, _ string, _ SyncOptions) (SyncStats, error) {
		calls++
		return SyncStats{}, nil
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if calls != 0 {
		t.Errorf("syncFn calls = %d, want 0 (opts.Sync was unset)", calls)
	}
}

// TestRun_Step11Sync_NotInvokedUnderDryRun asserts that --dry-run gates
// the step-11 Sync call even when --sync is explicitly set
// (T-07-W5-01-03 — --dry-run must skip every disk-touching call).
func TestRun_Step11Sync_NotInvokedUnderDryRun(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.Sync = true
	c.opts.DryRun = true

	prevSync := syncFn
	t.Cleanup(func() { syncFn = prevSync })
	var calls int
	syncFn = func(_, _ *state.File, _ string, _ SyncOptions) (SyncStats, error) {
		calls++
		return SyncStats{}, nil
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run --dry-run = %v, want nil", err)
	}
	if calls != 0 {
		t.Errorf("syncFn calls under --dry-run = %d, want 0", calls)
	}
}

// TestCommit_Step6Diff_OnlyRuntime_SkipsContext seeds a manifest with
// both runtime and context entries; asserts step6Diff under
// OnlyRuntime emits ONLY runtime targets and the context iteration
// is bypassed entirely.
func TestCommit_Step6Diff_OnlyRuntime_SkipsContext(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.OnlyRuntime = true

	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			Models:     []manifest.ContentRef{{ID: "m1"}},
			MCPServers: []manifest.ContentRef{{ID: "s1"}},
			A2AAgents:  []manifest.ContentRef{{ID: "a1"}},
		},
		Context: &manifest.ContextBlock{
			Prompts:   []manifest.ContentRef{{ID: "p1"}},
			Plugins:   []manifest.ContentRef{{ID: "pl1"}},
			Artifacts: []manifest.ContentRef{{ID: "ar1"}},
		},
	}
	targets := c.step6Diff(m)
	if len(targets) != 3 {
		t.Fatalf("OnlyRuntime: got %d targets, want 3 (runtime only): %+v", len(targets), targets)
	}
	for _, tgt := range targets {
		if tgt.Kind == "prompt" || tgt.Kind == "plugin" || tgt.Kind == "artifact" {
			t.Errorf("OnlyRuntime leaked context kind %q", tgt.Kind)
		}
	}
}

// TestCommit_Step6Diff_DefaultScope_ContextOnly asserts default scope
// (neither OnlyRuntime nor IncludeRuntime) emits ONLY context targets.
func TestCommit_Step6Diff_DefaultScope_ContextOnly(t *testing.T) {
	c, _, _ := newTestCommit(t)
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Runtime: &manifest.RuntimeBlock{
			Models: []manifest.ContentRef{{ID: "m1"}},
		},
		Context: &manifest.ContextBlock{
			Prompts: []manifest.ContentRef{{ID: "p1"}},
		},
	}
	targets := c.step6Diff(m)
	if len(targets) != 1 {
		t.Fatalf("default scope: got %d targets, want 1 (context.prompt only)", len(targets))
	}
	if targets[0].Kind != "prompt" {
		t.Errorf("default scope kind = %q, want %q", targets[0].Kind, "prompt")
	}
}

// TestCommit_Step6Diff_IncludeRuntime_BothScopes asserts
// --include-runtime emits both runtime and context targets.
func TestCommit_Step6Diff_IncludeRuntime_BothScopes(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.IncludeRuntime = true
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Runtime: &manifest.RuntimeBlock{
			Models: []manifest.ContentRef{{ID: "m1"}},
		},
		Context: &manifest.ContextBlock{
			Prompts: []manifest.ContentRef{{ID: "p1"}},
		},
	}
	targets := c.step6Diff(m)
	if len(targets) != 2 {
		t.Fatalf("--include-runtime: got %d targets, want 2", len(targets))
	}
}

// TestCommit_Run_PublicEntryPoint asserts the public Run function
// (Opts → newCommit → c.run) works end-to-end. Uses a dedicated
// temp dir as Output to avoid os.Getwd dependence.
//
// NOTE: this exercises the real defaultStateStore and the real
// lock.NewLocker — both targeting a fresh tmpdir — so no real
// concurrency or file I/O surprises beyond the lock-file create.
func TestCommit_Run_PublicEntryPoint(t *testing.T) {
	dir := t.TempDir()
	stderr := &bytes.Buffer{}

	// The real fetcher would hit the network; we cannot easily
	// override via Opts (no public seam). This test deliberately uses
	// a non-existent BaseURL so Fetch errors — we ASSERT the error
	// flows back (a useful smoke-check that Run is correctly wired).
	_, err := Run(context.Background(), Opts{
		Environment: "demo",
		Output:      dir,
		BaseURL:     "http://127.0.0.1:1", // unroutable port
		Bearer:      "pk_dummy",
		Stderr:      stderr,
	})
	if err == nil {
		t.Fatal("Run with unroutable BaseURL = nil error; want network failure")
	}
	// We tolerate any error type here — the point is the orchestrator
	// got far enough to call the fetcher (proving Run wired the
	// commit struct and the step5 dispatch).
}
