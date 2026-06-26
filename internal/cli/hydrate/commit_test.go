// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hash"
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

func (f fakeExtractor) ExtractContent(_ context.Context, _ manifest.ContentRef, _ string, _ *state.File) (ExtractResult, error) {
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
// gotProjectPlugins, when non-nil, captures the projectPlugins scope-gate
// argument the orchestrator derived (WIRE-04 assertion).
type fakeAdapterDispatcher struct {
	calls             *int
	result            RenderResult
	err               error
	gotProjectPlugins *bool
}

func (f fakeAdapterDispatcher) Render(_ context.Context, _ *manifest.Manifest, _ *state.File, _, _ string, projectPlugins, _ bool) (RenderResult, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.gotProjectPlugins != nil {
		*f.gotProjectPlugins = projectPlugins
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
	if store.savedFile.SchemaVersion != "3" {
		t.Errorf("savedFile.SchemaVersion = %q, want %q", store.savedFile.SchemaVersion, "3")
	}
	if store.savedFile.Environment != "demo" {
		t.Errorf("savedFile.Environment = %q, want %q", store.savedFile.Environment, "demo")
	}
	if result.FilesWritten != 0 || result.FilesPreserved != 0 || result.FilesPruned != 0 {
		t.Errorf("Result counts non-zero on W1 stub path: %+v", result)
	}
}

// TestCommit_Step12_ComposesAdapterSection verifies W6-01 state composition:
// when the adapter runs, step 12 records RenderResult.WrittenFiles into
// state.Adapter (ID + Files). That recorded section IS the prior state the
// next hydrate's §8.4 per-key drift check (findAdapterEntry) reads — without
// it drift / auto-claim (sc3 / sc4) could never fire on re-hydrate.
func TestCommit_Step12_ComposesAdapterSection(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{
			WrittenFiles: []FileWrite{{
				Target:     ".claude/settings.json",
				Hash:       "xxh3:deadbeef",
				SourceHash: "xxh3:deadbeef",
				Merge:      mergeStrDeep,
				Keys:       []string{"mcpServers.demo-mcp-jwt"},
			}},
		},
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if store.savedFile == nil {
		t.Fatal("savedFile = nil; expected a state.File at step 12")
	}
	if store.savedFile.Adapter.ID != "claude-code" {
		t.Errorf("Adapter.ID = %q, want %q", store.savedFile.Adapter.ID, "claude-code")
	}
	if got := len(store.savedFile.Adapter.Files); got != 1 {
		t.Fatalf("Adapter.Files len = %d, want 1 (composed from render)", got)
	}
	fe := store.savedFile.Adapter.Files[0]
	if fe.Target != ".claude/settings.json" || fe.Hash != "xxh3:deadbeef" ||
		fe.SourceHash != "xxh3:deadbeef" || fe.Merge != "deep" ||
		len(fe.Keys) != 1 || fe.Keys[0] != "mcpServers.demo-mcp-jwt" {
		t.Errorf("composed adapter FileEntry = %+v; want the rendered file verbatim", fe)
	}
}

// containsTarget reports whether any FileEntry in the slice targets `target`.
func containsTarget(entries []state.FileEntry, target string) bool {
	for _, e := range entries {
		if e.Target == target {
			return true
		}
	}
	return false
}

// TestComposeNextState_IsPure_AndReflectsRender asserts G5.1: composeNextState
// is pure (no state.json write) and recomposes the Plugins bucket from the
// fresh render, so a resource dropped from the Environment is ABSENT from the
// composed next-state (the precondition for --sync to prune it).
func TestComposeNextState_IsPure_AndReflectsRender(t *testing.T) {
	store := &fakeStateStore{}
	c := &commit{
		opts:       Opts{Environment: "demo"},
		stateStore: store,
	}
	// Existing state has plugins A and B.
	existing := &state.File{
		SchemaVersion: "3",
		Plugins: []state.FileEntry{
			{Target: "pluginA"}, {Target: "pluginB"},
		},
	}
	// The fresh render projects ONLY pluginA (pluginB was dropped from the Env).
	render := RenderResult{
		ProjectedFiles: []FileWrite{{Target: "pluginA"}},
	}

	next := c.composeNextState(existing, nil, render, true /* adapterRan */, "claude-code", nil)

	// 1. purity: composing must not write state.json.
	if store.saveCount != 0 {
		t.Fatalf("composeNextState performed I/O — saveCount=%d, want 0", store.saveCount)
	}
	// 2. correctness: the dropped pluginB must be ABSENT; pluginA must survive.
	if containsTarget(next.Plugins, "pluginB") {
		t.Fatal("dropped resource still present in composed next-state")
	}
	if !containsTarget(next.Plugins, "pluginA") {
		t.Fatal("surviving resource missing from composed next-state")
	}
}

// TestRemapGlobalPath covers the W6-01 --global path remap, generalized for
// D-22 (plan 03-01): opencode's GLOBAL config root is XDG ~/.config/opencode/,
// not the project ~/.opencode/, so ALL .opencode/* projected paths remap (not
// just opencode.json); the other adapters' paths pass through.
func TestRemapGlobalPath(t *testing.T) {
	cases := []struct{ platform, in, want string }{
		{"opencode", ".opencode/opencode.json", ".config/opencode/opencode.json"},
		{"opencode", ".opencode/plugins/foo/x.md", ".config/opencode/plugins/foo/x.md"}, // D-22: all .opencode/* remap
		{"claude-code", ".claude/settings.json", ".claude/settings.json"},
		{"gemini-cli", ".gemini/settings.json", ".gemini/settings.json"},
		{"codex", ".codex/config.toml", ".codex/config.toml"},
	}
	for _, tc := range cases {
		if got := remapGlobalPath(tc.platform, tc.in); got != tc.want {
			t.Errorf("remapGlobalPath(%q, %q) = %q, want %q", tc.platform, tc.in, got, tc.want)
		}
	}
}

// TestStep4ReconcileVsDisk_AdapterResolvedViaToolRoot verifies that adapter
// state entries are reconciled against toolRoot, NOT achDir/.. — the two
// diverge under --global (achDir is $HOME/.ach/<env>, toolRoot is $HOME). An
// adapter file present under toolRoot must NOT be pruned. (Under the prior
// achDir/.. resolution this file would be looked up at the wrong path and
// silently pruned.)
func TestStep4ReconcileVsDisk_AdapterResolvedViaToolRoot(t *testing.T) {
	home := t.TempDir()
	achParent := t.TempDir() // distinct from home → mimics --global divergence

	c := &commit{
		achDir:   filepath.Join(achParent, ".ach", "demo"),
		toolRoot: home,
	}
	cfgDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "opencode.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded := &state.File{
		Adapter: state.AdapterSection{
			ID:    "opencode",
			Files: []state.FileEntry{{Target: ".config/opencode/opencode.json", Hash: "xxh3:x"}},
		},
	}
	got, pruned := c.step4ReconcileVsDisk(loaded)
	if pruned != 0 {
		t.Errorf("pruned = %d, want 0 (adapter file exists under toolRoot)", pruned)
	}
	if len(got.Adapter.Files) != 1 {
		t.Errorf("Adapter.Files len = %d, want 1 (resolved via toolRoot, not achDir/..)", len(got.Adapter.Files))
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
		return &state.File{SchemaVersion: "3", Environment: "prod"}, nil
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
		return &state.File{SchemaVersion: "3", Environment: "prod"}, nil
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
			SchemaVersion: "3",
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

// TestRun_PluginExtractNotDoubleCounted is the regression guard for the
// "N files written ≈ 2×" bug. Plugin content extracts to the EPHEMERAL
// pluginStageRoot (swept, never a final write) and is re-counted as
// renderResult.ProjectedFiles when published to the real dest. Counting
// the plugin extract too double-counts the on-disk file total (observed
// 110 reported vs 54 on disk).
//
// Setup: 1 prompt + 1 plugin diffTarget. The fake extractor returns 2
// WrittenFiles per call; the fake adapter returns 1 runtime WrittenFile +
// 2 ProjectedFiles (the published plugin). On-disk reality = prompt(2) +
// runtime(1) + projected-plugin(2) = 5; the plugin's ephemeral extract(2)
// must NOT be counted. Pre-fix this returned 7.
func TestRun_PluginExtractNotDoubleCounted(t *testing.T) {
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
				},
				Plugins: []manifest.ContentRef{
					{ID: "plug1", DownloadURL: "http://local/content/plugin/plug1"},
				},
			},
		}, nil
	}
	var extCalls, adCalls int
	// Every extract returns 2 files. The prompt's 2 are real (final dest);
	// the plugin's 2 are ephemeral staging and must be excluded.
	c.extractor = fakeExtractor{
		calls: &extCalls,
		result: ExtractResult{
			WrittenFiles: []FileWrite{{Target: "a", Hash: "xxh3:0a"}, {Target: "b", Hash: "xxh3:0b"}},
		},
	}
	// Render: 1 runtime write + 2 projected plugin files (the real publish).
	c.adapter = fakeAdapterDispatcher{
		calls: &adCalls,
		result: RenderResult{
			WrittenFiles:   []FileWrite{{Target: "rt", Hash: "xxh3:rt"}},
			ProjectedFiles: []FileWrite{{Target: "pp1", Hash: "xxh3:p1"}, {Target: "pp2", Hash: "xxh3:p2"}},
		},
	}

	result, err := c.run(context.Background())
	if err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if extCalls != 2 {
		t.Errorf("extractor calls = %d, want 2 (prompt + plugin)", extCalls)
	}
	// prompt extract(2) + render WrittenFiles(1) + ProjectedFiles(2) = 5.
	// The plugin extract(2) is ephemeral → excluded. Pre-fix this was 7.
	if result.FilesWritten != 5 {
		t.Errorf("result.FilesWritten = %d, want 5 (plugin ephemeral extract must NOT be double-counted; pre-fix=7)", result.FilesWritten)
	}
}

// TestRun_FilesWrittenVsPreserved is the Bug B2 regression guard: the summary
// must distinguish real writes from preserved (byte-identical) files. A fresh
// hydrate counts everything written; an identical re-hydrate (publish engine
// no-op skips + extract no-op) counts everything preserved, zero written.
//
// Extract reports preserved via ExtractResult.Preserved (its WrittenFiles is
// empty on a no-op); render reports it per-file via FileWrite.Preserved.
func TestFilesWrittenVsPreserved(t *testing.T) {
	manifestFn := func(_ context.Context, _ string) (*manifest.Manifest, error) {
		return &manifest.Manifest{
			SchemaVersion: "v1alpha1",
			Environment:   "demo",
			Runtime:       &manifest.RuntimeBlock{},
			Context: &manifest.ContextBlock{
				Prompts: []manifest.ContentRef{{ID: "p1", DownloadURL: "http://local/content/prompt/p1"}},
			},
		}, nil
	}

	t.Run("fresh_all_written", func(t *testing.T) {
		c, _, _ := newTestCommit(t)
		c.fetcher = manifestFn
		c.extractor = fakeExtractor{result: ExtractResult{
			WrittenFiles: []FileWrite{{Target: "a"}, {Target: "b"}}, // 2 written, 0 preserved
		}}
		c.adapter = fakeAdapterDispatcher{result: RenderResult{
			WrittenFiles:   []FileWrite{{Target: "rt"}},                   // written
			ProjectedFiles: []FileWrite{{Target: "pp1"}, {Target: "pp2"}}, // written
		}}
		result, err := c.run(context.Background())
		if err != nil {
			t.Fatalf("c.run = %v", err)
		}
		if result.FilesWritten != 5 || result.FilesPreserved != 0 {
			t.Errorf("fresh: FilesWritten=%d FilesPreserved=%d; want 5 / 0", result.FilesWritten, result.FilesPreserved)
		}
	})

	t.Run("noop_all_preserved", func(t *testing.T) {
		c, _, _ := newTestCommit(t)
		c.fetcher = manifestFn
		// Extract no-op: empty WrittenFiles, 2 preserved on disk.
		c.extractor = fakeExtractor{result: ExtractResult{Preserved: 2}}
		// Render no-op: every entry flagged Preserved.
		c.adapter = fakeAdapterDispatcher{result: RenderResult{
			WrittenFiles:   []FileWrite{{Target: "rt", Preserved: true}},
			ProjectedFiles: []FileWrite{{Target: "pp1", Preserved: true}, {Target: "pp2", Preserved: true}},
		}}
		result, err := c.run(context.Background())
		if err != nil {
			t.Fatalf("c.run = %v", err)
		}
		if result.FilesWritten != 0 || result.FilesPreserved != 5 {
			t.Errorf("no-op: FilesWritten=%d FilesPreserved=%d; want 0 / 5", result.FilesWritten, result.FilesPreserved)
		}
	})
}

// TestRun_ExtractSkipsRuntimeKinds asserts the extraction loop runs ONLY for
// context kinds (prompt/plugin/artifact) and skips runtime kinds
// (model/mcpServer/a2aAgent) under --include-runtime. Runtime entries carry an
// {id, endpoint}, not a /content/{kind}/{name} tarball; feeding one to
// ExtractContent derives an empty content name → "content name: empty" (the
// historical --include-runtime crash). step6Diff still emits 4 diffTargets
// here for scope symmetry, but only the 1 context prompt is extracted.
func TestRun_ExtractSkipsRuntimeKinds(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.opts.IncludeRuntime = true

	manifestFn := func() *manifest.Manifest {
		return &manifest.Manifest{
			SchemaVersion: "v1alpha1",
			Environment:   "demo",
			Runtime: &manifest.RuntimeBlock{
				Models:     []manifest.ContentRef{{ID: "demo-model", Endpoint: "http://local/v1"}},
				MCPServers: []manifest.ContentRef{{ID: "s1", Endpoint: "http://local/mcp/s1"}},
				A2AAgents:  []manifest.ContentRef{{ID: "a1", Endpoint: "http://local/a2a/a1"}},
			},
			Context: &manifest.ContextBlock{
				Prompts: []manifest.ContentRef{
					{ID: "p1", DownloadURL: "http://local/content/prompt/p1"},
				},
			},
		}
	}
	c.fetcher = func(_ context.Context, _ string) (*manifest.Manifest, error) {
		return manifestFn(), nil
	}

	// Precondition: under --include-runtime step6Diff emits 4 targets
	// (1 context prompt + 3 runtime) — the scope contract is unchanged.
	if got := len(c.step6Diff(manifestFn())); got != 4 {
		t.Fatalf("precondition: step6Diff = %d targets, want 4 (1 context + 3 runtime)", got)
	}

	var extCalls, adCalls int
	c.extractor = fakeExtractor{calls: &extCalls}
	c.adapter = fakeAdapterDispatcher{calls: &adCalls, result: RenderResult{}}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil (runtime kinds must not be extracted)", err)
	}
	if extCalls != 1 {
		t.Errorf("extractor calls = %d, want 1 (context prompt only; runtime kinds skipped)", extCalls)
	}
	if adCalls != 1 {
		t.Errorf("adapter calls = %d, want 1 (RenderRuntime still projects mcp/a2a)", adCalls)
	}
}

// TestRun_RuntimeMirror_WritesSnapshotsAndState asserts step 10b mirrors the
// manifest runtime block into credential-free .ach/runtime/{mcp,a2a,model}.json
// snapshots and records them in state.RuntimeFiles. Empty buckets are not
// written. Re-running is byte-stable (same hash).
func TestRun_RuntimeMirror_WritesSnapshotsAndState(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.opts.IncludeRuntime = true
	c.fetcher = func(_ context.Context, _ string) (*manifest.Manifest, error) {
		return &manifest.Manifest{
			SchemaVersion: "v1alpha1",
			Environment:   "demo",
			Runtime: &manifest.RuntimeBlock{
				Models:     []manifest.ContentRef{{ID: "demo-model", Endpoint: "http://local/v1"}},
				MCPServers: []manifest.ContentRef{{ID: "demo-mcp-jwt", Endpoint: "http://local/mcp/demo-mcp-jwt"}},
				A2AAgents:  nil, // empty bucket → no a2a.json
			},
			Context: &manifest.ContextBlock{},
		}, nil
	}
	c.extractor = fakeExtractor{}
	c.adapter = fakeAdapterDispatcher{result: RenderResult{}}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}

	// Per-platform runtime mirror dir (runtime-<platform>).
	runtimeRel := "runtime-" + c.opts.Platform
	runtimeDir := filepath.Join(c.achDir, runtimeRel)
	// mcp + model written; a2a NOT (empty bucket).
	for _, name := range []string{"mcp", "model"} {
		p := filepath.Join(runtimeDir, name+".json")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("runtime mirror %s.json missing: %v", name, err)
		}
		// Credential-free (OBS-02): the snapshot is id+endpoint only.
		if bytes.Contains(b, []byte("x-ach-key")) || bytes.Contains(b, []byte("pk_")) || bytes.Contains(b, []byte("ek_")) {
			t.Errorf("runtime mirror %s.json leaked a credential:\n%s", name, b)
		}
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "a2a.json")); !os.IsNotExist(err) {
		t.Errorf("a2a.json should NOT exist (empty bucket); stat err=%v", err)
	}

	// state.RuntimeFiles records the 2 written snapshots (mcp, model).
	if store.savedFile == nil {
		t.Fatal("savedFile = nil")
	}
	rf := store.savedFile.RuntimeFiles
	if len(rf) != 2 {
		t.Fatalf("state.RuntimeFiles = %d entries, want 2 (mcp, model): %+v", len(rf), rf)
	}
	gotTargets := map[string]string{}
	for _, e := range rf {
		gotTargets[e.Target] = e.Hash
		if e.Hash == "" || e.Hash != e.SourceHash {
			t.Errorf("RuntimeFiles[%s]: hash=%q sourceHash=%q (want equal, non-empty)", e.Target, e.Hash, e.SourceHash)
		}
	}
	if _, ok := gotTargets[filepath.Join(runtimeRel, "mcp.json")]; !ok {
		t.Errorf("RuntimeFiles missing runtime/mcp.json: %+v", rf)
	}
	if _, ok := gotTargets[filepath.Join(runtimeRel, "model.json")]; !ok {
		t.Errorf("RuntimeFiles missing runtime/model.json: %+v", rf)
	}

	// Byte-stable on re-run: same manifest → same hashes.
	mcpHash1 := gotTargets[filepath.Join(runtimeRel, "mcp.json")]
	rf2, err := c.writeRuntimeMirror(mustRuntimeManifest())
	if err != nil {
		t.Fatalf("re-run writeRuntimeMirror: %v", err)
	}
	for _, e := range rf2 {
		if e.Target == filepath.Join(runtimeRel, "mcp.json") && e.Hash != mcpHash1 {
			t.Errorf("mcp.json hash not stable across runs: %q vs %q", e.Hash, mcpHash1)
		}
	}
}

func mustRuntimeManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			Models:     []manifest.ContentRef{{ID: "demo-model", Endpoint: "http://local/v1"}},
			MCPServers: []manifest.ContentRef{{ID: "demo-mcp-jwt", Endpoint: "http://local/mcp/demo-mcp-jwt"}},
		},
	}
}

// TestMigrateLegacyFlatState_RelocatesIntoEnvSubdir asserts the D3 migration:
// a pre-namespacing flat <cwd>/.ach/state.json (+ content dirs) is relocated
// into <cwd>/.ach/<env>/, is idempotent on a second call, and no-ops when no
// flat state exists.
func TestMigrateLegacyFlatState_RelocatesIntoEnvSubdir(t *testing.T) {
	ws := t.TempDir()
	achRoot := filepath.Join(ws, ".ach")
	if err := os.MkdirAll(filepath.Join(achRoot, "plugin", "caveman"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(achRoot, "plugin", "caveman", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Legacy flat state bound to env "demo".
	if err := state.Save(filepath.Join(achRoot, "state.json"), &state.File{
		SchemaVersion: "3",
		Environment:   "demo",
		Plugins:       []state.FileEntry{{Target: ".claude/skills/caveman/SKILL.md", Hash: "h", SourceHash: "h"}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyFlatState(ws, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Relocated into .ach/demo/.
	if _, err := os.Stat(filepath.Join(achRoot, "demo", "state.json")); err != nil {
		t.Errorf("state.json not relocated to .ach/demo/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(achRoot, "demo", "plugin", "caveman", "SKILL.md")); err != nil {
		t.Errorf("plugin dir not relocated to .ach/demo/plugin/: %v", err)
	}
	// Flat copies gone.
	if _, err := os.Stat(filepath.Join(achRoot, "state.json")); !os.IsNotExist(err) {
		t.Errorf("flat state.json should be gone after migration; stat err=%v", err)
	}

	// Idempotent: a second call is a clean no-op (target already present).
	if err := migrateLegacyFlatState(ws, nil); err != nil {
		t.Errorf("second migrate (idempotent) returned err: %v", err)
	}

	// No flat state → no-op.
	fresh := t.TempDir()
	if err := migrateLegacyFlatState(fresh, nil); err != nil {
		t.Errorf("migrate on fresh workspace returned err: %v", err)
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
			SchemaVersion: "3",
			Environment:   "demo",
		}, nil
	}

	prevSync := syncFn
	t.Cleanup(func() { syncFn = prevSync })
	var calls int
	var sawForce bool
	syncFn = func(_, _ *state.File, _, _ string, opts SyncOptions) (SyncStats, error) {
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
	syncFn = func(_, _ *state.File, _, _ string, _ SyncOptions) (SyncStats, error) {
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
	syncFn = func(_, _ *state.File, _, _ string, _ SyncOptions) (SyncStats, error) {
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

// TestRun_Step11Sync_PassesComposedNextState asserts the G5 fix: step 11 feeds
// the COMPOSED next-state as `newFile` (not existingState twice). Before the
// fix prev and newFile were the same pointer, so the set-difference was always
// empty and nothing pruned. Here existing has plugins A+B, the render projects
// only A, and the composed newFile must omit the dropped pluginB.
func TestRun_Step11Sync_PassesComposedNextState(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Sync = true
	c.opts.Platform = "claude-code" // make the adapter run so Plugins recompose.
	store.loadFn = func(_ string) (*state.File, error) {
		return &state.File{
			SchemaVersion: "3",
			Environment:   "demo",
			Plugins:       []state.FileEntry{{Target: "pluginA"}, {Target: "pluginB"}},
		}, nil
	}
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{ProjectedFiles: []FileWrite{{Target: "pluginA"}}},
	}

	prevSync := syncFn
	t.Cleanup(func() { syncFn = prevSync })
	var gotPrev, gotNew *state.File
	syncFn = func(prev, newFile *state.File, _, _ string, _ SyncOptions) (SyncStats, error) {
		gotPrev, gotNew = prev, newFile
		return SyncStats{}, nil
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if gotNew == nil {
		t.Fatal("syncFn newFile is nil; step 11 did not pass a composed state")
	}
	if gotPrev == gotNew {
		t.Fatal("prev and newFile are the same pointer — the STATE-05 no-op bug")
	}
	if containsTarget(gotNew.Plugins, "pluginB") {
		t.Fatal("composed newFile still contains the dropped resource pluginB")
	}
	if !containsTarget(gotNew.Plugins, "pluginA") {
		t.Fatal("composed newFile missing the surviving resource pluginA")
	}
}

// TestSync_PrunesDroppedPlugin_EndToEnd is the G5 acceptance criterion against
// the REAL composeNextState + REAL Sync on real disk: an Environment that had
// plugins A and B, re-hydrated against a render that projects only A, prunes
// B's projected file while preserving A's. (Combined with
// TestRun_Step11Sync_PassesComposedNextState, this closes the silent-no-op
// bug end-to-end — orchestrator passes the composed state, Sync acts on it.)
func TestSync_PrunesDroppedPlugin_EndToEnd(t *testing.T) {
	achDir := t.TempDir()
	fileA := filepath.Join(achDir, ".claude", "agents", "pluginA.md")
	fileB := filepath.Join(achDir, ".claude", "agents", "pluginB.md")
	for _, p := range []string{fileA, fileB} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	c := &commit{opts: Opts{Environment: "demo"}, achDir: achDir, toolRoot: achDir}
	// Prior hydrate recorded BOTH plugins (hashes match the on-disk content so
	// neither is treated as a local edit).
	existing := &state.File{
		SchemaVersion: "3",
		Environment:   "demo",
		Plugins: []state.FileEntry{
			{Target: fileA, Hash: hash.HashBytes([]byte("x"))},
			{Target: fileB, Hash: hash.HashBytes([]byte("x"))},
		},
	}
	// The fresh render projects ONLY pluginA — pluginB was dropped from the Env.
	render := RenderResult{ProjectedFiles: []FileWrite{{Target: fileA, Hash: hash.HashBytes([]byte("x"))}}}
	composed := c.composeNextState(existing, nil, render, true /* adapterRan */, "claude-code", nil)

	var stderr bytes.Buffer
	stats, err := Sync(existing, composed, achDir, achDir, SyncOptions{Stderr: &stderr})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Pruned != 1 {
		t.Errorf("Pruned = %d; want 1 (pluginB)", stats.Pruned)
	}
	if _, err := os.Stat(fileB); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("pluginB file should be pruned; stat err = %v", err)
	}
	if _, err := os.Stat(fileA); err != nil {
		t.Errorf("pluginA file should be preserved; stat err = %v", err)
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

// TestCommit_Step12_ComposesPluginsSection verifies WIRE-02 / D-07: when
// the adapter runs AND projection is in scope (default), step 12 records
// RenderResult.ProjectedFiles into state.File.Plugins[] (the projection
// bucket), distinct from Adapter.Files.
func TestCommit_Step12_ComposesPluginsSection(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{
			ProjectedFiles: []FileWrite{{
				Target:     ".claude/rules/foo.md",
				Hash:       "xxh3:abc123",
				SourceHash: "xxh3:abc123",
				Merge:      mergeStrReplace,
			}},
		},
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if store.savedFile == nil {
		t.Fatal("savedFile = nil; expected a state.File at step 12")
	}
	if got := len(store.savedFile.Plugins); got != 1 {
		t.Fatalf("Plugins len = %d, want 1 (composed from ProjectedFiles)", got)
	}
	fe := store.savedFile.Plugins[0]
	if fe.Target != ".claude/rules/foo.md" || fe.Hash != "xxh3:abc123" ||
		fe.SourceHash != "xxh3:abc123" || fe.Merge != "replace" {
		t.Errorf("composed Plugins FileEntry = %+v; want the projected file verbatim", fe)
	}
	if !strings.HasPrefix(fe.Hash, "xxh3:") {
		t.Errorf("Plugins[0].Hash = %q; want xxh3: prefix", fe.Hash)
	}
}

// TestCommit_Step12_KeysVerbatim_MergeDeep is the T-01-04 invariant: a
// MergeDeep projected FileWrite's contributed Keys are recorded VERBATIM
// into state.Plugins[] (never dropped or rewritten), so Phase 4 uninstall
// subtracts exactly the plugin's keys and never the user's other keys.
func TestCommit_Step12_KeysVerbatim_MergeDeep(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{
			ProjectedFiles: []FileWrite{{
				Target:     ".claude/.mcp.json",
				Hash:       "xxh3:deadbeef",
				SourceHash: "xxh3:cafe",
				Merge:      mergeStrDeep,
				Keys:       []string{"mcp.server1"},
			}},
		},
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if got := len(store.savedFile.Plugins); got != 1 {
		t.Fatalf("Plugins len = %d, want 1", got)
	}
	fe := store.savedFile.Plugins[0]
	if fe.Merge != "deep" {
		t.Errorf("Plugins[0].Merge = %q; want deep", fe.Merge)
	}
	if len(fe.Keys) != 1 || fe.Keys[0] != "mcp.server1" {
		t.Errorf("Plugins[0].Keys = %v; want [mcp.server1] VERBATIM (T-01-04)", fe.Keys)
	}
}

// TestCommit_Step10_ScopeGate_OnlyRuntimeSkipsProjection asserts WIRE-04 /
// D-11: with OnlyRuntime=true the orchestrator passes projectPlugins=false
// to Render (the projection leg is suppressed). Default scope passes true.
func TestCommit_Step10_ScopeGate_OnlyRuntimeSkipsProjection(t *testing.T) {
	for _, tc := range []struct {
		name        string
		onlyRuntime bool
		want        bool
	}{
		{"default scope projects", false, true},
		{"only-runtime skips", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestCommit(t)
			c.opts.Platform = "claude-code"
			c.opts.OnlyRuntime = tc.onlyRuntime
			var got bool
			c.adapter = fakeAdapterDispatcher{gotProjectPlugins: &got}

			if _, err := c.run(context.Background()); err != nil {
				t.Fatalf("c.run = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("projectPlugins arg = %v; want %v (OnlyRuntime=%v)", got, tc.want, tc.onlyRuntime)
			}
		})
	}
}

// TestCommit_Step12_OnlyRuntime_CarriesForwardPlugins asserts that under
// --only-runtime the existing Plugins[] is carried forward unchanged (the
// projection bucket is NOT recomposed when projection was out of scope).
func TestCommit_Step12_OnlyRuntime_CarriesForwardPlugins(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.opts.OnlyRuntime = true
	// step4ReconcileVsDisk prunes entries whose target is missing on disk.
	// Content buckets resolve workspace-relative (achDir/..); Plugins now
	// resolve against toolRoot (projected resources live in native dirs). In
	// project scope toolRoot == wsRoot, so pin them equal here and stage the
	// prior file there so it survives pruning and we genuinely test carry-forward.
	wsRoot := filepath.Join(c.achDir, "..")
	c.toolRoot = wsRoot
	priorAbs := filepath.Join(wsRoot, ".claude", "rules", "prior.md")
	if err := os.MkdirAll(filepath.Dir(priorAbs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(priorAbs, []byte("# prior\n"), 0o644); err != nil {
		t.Fatalf("write prior: %v", err)
	}
	existing := &state.File{
		SchemaVersion: "3",
		Environment:   "demo",
		Plugins: []state.FileEntry{{
			Target: ".claude/rules/prior.md", Hash: "xxh3:prior", SourceHash: "xxh3:prior",
		}},
	}
	store.loadFn = func(string) (*state.File, error) { return existing, nil }
	// Even if the (stale) render returned projected files, OnlyRuntime must
	// NOT recompose Plugins[].
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{ProjectedFiles: []FileWrite{{Target: ".claude/rules/new.md", Hash: "xxh3:new"}}},
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if got := len(store.savedFile.Plugins); got != 1 {
		t.Fatalf("Plugins len = %d, want 1 (prior carried forward)", got)
	}
	if store.savedFile.Plugins[0].Target != ".claude/rules/prior.md" {
		t.Errorf("Plugins[0].Target = %q; want the prior entry carried forward unchanged",
			store.savedFile.Plugins[0].Target)
	}
}

// TestCommit_Step4Reconcile_GlobalScope_PrunesPluginsAgainstToolRoot is the
// CR-01 regression gate: under --global, wsRoot ($HOME/.ach) and toolRoot
// ($HOME) diverge. Projected plugins are published under toolRoot, so
// step4ReconcileVsDisk must stat them against toolRoot — not wsRoot. The whole
// e2e matrix runs project scope (where the two coincide) and cannot catch this,
// so it is pinned here. A regression (resolving Plugins against wsRoot) would
// ErrNotExist on a live projected plugin and silently prune it from state on
// every re-hydrate, re-opening the survive-uninstall defect.
func TestCommit_Step4Reconcile_GlobalScope_PrunesPluginsAgainstToolRoot(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.opts.OnlyRuntime = true
	// Diverge wsRoot and toolRoot as --global does: achDir/.. is the ach-state
	// root; toolRoot is a wholly separate tree (the user's $HOME).
	wsRoot := filepath.Join(c.achDir, "..")
	c.toolRoot = t.TempDir()
	if c.toolRoot == wsRoot {
		t.Fatalf("test setup: toolRoot must differ from wsRoot to exercise --global")
	}
	// Stage the projected plugin under toolRoot ONLY (never under wsRoot), so a
	// wsRoot-based stat would miss it and prune.
	pluginAbs := filepath.Join(c.toolRoot, ".claude", "agents", "caveman.md")
	if err := os.MkdirAll(filepath.Dir(pluginAbs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pluginAbs, []byte("# caveman\n"), 0o644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	existing := &state.File{
		SchemaVersion: "3",
		Environment:   "demo",
		Plugins: []state.FileEntry{{
			Target: ".claude/agents/caveman.md", Hash: "xxh3:cm", SourceHash: "xxh3:cm",
		}},
	}
	store.loadFn = func(string) (*state.File, error) { return existing, nil }
	c.adapter = fakeAdapterDispatcher{result: RenderResult{}}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if got := len(store.savedFile.Plugins); got != 1 {
		t.Fatalf("Plugins len = %d, want 1 (projected plugin under toolRoot must survive reconcile under --global)", got)
	}
	if store.savedFile.Plugins[0].Target != ".claude/agents/caveman.md" {
		t.Errorf("Plugins[0].Target = %q; want the toolRoot-resolved plugin retained",
			store.savedFile.Plugins[0].Target)
	}
}

// TestCommit_DropWarning_AttributedByPlugin verifies the WIRE-03 / D-12
// attributed drop warning: when a RenderResult carries DroppedByKind entries
// the warnDropped path emits a "does not support" header line followed by one
// indented line per kind listing the plugins sorted, and the exit code is
// unaffected (c.run returns nil).
func TestCommit_DropWarning_AttributedByPlugin(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.Platform = "pimono"
	var stderr bytes.Buffer
	c.opts.Stderr = &stderr
	// DroppedByKind is set directly on RenderResult; Part B flow-up copies it
	// into result.DroppedByKind via appendUniqueSorted (already sorted).
	// Pre-sort plugin lists to match what appendUniqueSorted produces.
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{
			DroppedByKind: map[string][]string{
				"agents": {"bar", "foo"},
				"hooks":  {"foo"},
			},
		},
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v; want nil (drop warning must not change exit code)", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "platform pimono does not support") {
		t.Errorf("missing header line; stderr:\n%s", out)
	}
	if !strings.Contains(out, "agents") {
		t.Errorf("missing 'agents' kind in warning; stderr:\n%s", out)
	}
	if !strings.Contains(out, "bar, foo") {
		t.Errorf("missing sorted plugin list 'bar, foo'; stderr:\n%s", out)
	}
	if !strings.Contains(out, "hooks") {
		t.Errorf("missing 'hooks' kind in warning; stderr:\n%s", out)
	}
}

// TestCommit_DropWarning_EmptyNoWarning asserts an adapter with no drops
// (e.g. claude-code) emits NO drop-warning line.
func TestCommit_DropWarning_EmptyNoWarning(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	var stderr bytes.Buffer
	c.opts.Stderr = &stderr
	c.adapter = fakeAdapterDispatcher{result: RenderResult{}}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if strings.Contains(stderr.String(), "does not support") {
		t.Errorf("unexpected drop warning for empty drop list:\n%s", stderr.String())
	}
}

// TestStep12bGitignore asserts the hydrate engine writes the ach-managed
// .gitignore block under toolRoot (project scope) listing .ach/ plus the
// top-level adapter dirs/files it wrote, and is a no-op under --global / empty
// toolRoot (the unit-test default) so it never pollutes the working directory.
func TestStep12bGitignore(t *testing.T) {
	t.Run("project scope writes block", func(t *testing.T) {
		root := t.TempDir()
		c := &commit{opts: Opts{Stderr: &bytes.Buffer{}}, toolRoot: root}
		c.step12bGitignore(RenderResult{
			WrittenFiles:        []FileWrite{{Target: ".mcp.json"}},
			ProjectedFiles:      []FileWrite{{Target: ".claude/agents/a.md"}},
			ProjectedSkillFiles: []FileWrite{{Target: ".claude/skills/s/SKILL.md"}},
		})
		body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}
		for _, want := range []string{".ach/", ".claude/", ".mcp.json"} {
			if !strings.Contains(string(body), "\n"+want+"\n") {
				t.Errorf(".gitignore missing %q; got:\n%s", want, body)
			}
		}
	})

	t.Run("global scope is a no-op", func(t *testing.T) {
		root := t.TempDir()
		c := &commit{opts: Opts{Global: true, Stderr: &bytes.Buffer{}}, toolRoot: root}
		c.step12bGitignore(RenderResult{ProjectedFiles: []FileWrite{{Target: ".claude/a.md"}}})
		if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
			t.Errorf("--global must not write .gitignore; stat err=%v", err)
		}
	})

	t.Run("empty toolRoot is a no-op", func(t *testing.T) {
		c := &commit{opts: Opts{Stderr: &bytes.Buffer{}}, toolRoot: ""}
		// Must not panic or write to cwd.
		c.step12bGitignore(RenderResult{ProjectedFiles: []FileWrite{{Target: ".claude/a.md"}}})
		if _, err := os.Stat(".gitignore"); !os.IsNotExist(err) {
			_ = os.Remove(".gitignore")
			t.Errorf("empty toolRoot must not write ./.gitignore")
		}
	})
}

// TestCommit_Step12_SkillSourcePropagates asserts that Source set on a
// ProjectedSkillFile FileWrite flows through skillsSectionFromRender into the
// saved state.File.Skills[] entry (U5 per-resource grouping contract).
func TestCommit_Step12_SkillSourcePropagates(t *testing.T) {
	c, store, _ := newTestCommit(t)
	c.opts.Platform = "claude-code"
	c.adapter = fakeAdapterDispatcher{
		result: RenderResult{
			ProjectedSkillFiles: []FileWrite{{
				Target:     ".claude/skills/myskill/SKILL.md",
				Hash:       "xxh3:abc",
				SourceHash: "xxh3:abc",
				Source:     "myskill",
			}},
		},
	}

	if _, err := c.run(context.Background()); err != nil {
		t.Fatalf("c.run = %v, want nil", err)
	}
	if store.savedFile == nil {
		t.Fatal("savedFile = nil; expected step 12 to save")
	}
	if got := len(store.savedFile.Skills); got != 1 {
		t.Fatalf("Skills len = %d, want 1 (composed from ProjectedSkillFiles)", got)
	}
	if got := store.savedFile.Skills[0].Source; got != "myskill" {
		t.Errorf("Skills[0].Source = %q; want %q (U5 per-resource grouping)", got, "myskill")
	}
}
