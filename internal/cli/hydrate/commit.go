// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/gitignore"
	"github.com/ackstorm/ach/internal/cli/hash"
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

	// Resolved paths from step 0/1.
	achDir    string
	statePath string
	// toolRoot is the base for adapter runtime-config writes (the tools'
	// native config files): the workspace root in project scope, $HOME in
	// --global scope. Distinct from achDir (ACH's private .ach/ cache).
	toolRoot string

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

	// D3 migration: relocate a legacy FLAT <cwd>/.ach/state.json (+ its content
	// dirs) into the per-environment <cwd>/.ach/<env>/ layout introduced with
	// namespacing. Project scope only — --global was always namespaced. Best-
	// effort + idempotent: a no-op once migrated or when no legacy state exists.
	if !opts.Global {
		if err := migrateLegacyFlatState(workspaceCwd, opts.Stderr); err != nil {
			return nil, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("migrate legacy flat .ach: %v", err),
				Wrapped: err,
			}
		}
	}

	// Per-platform state file: the engine tracks each agent target in its own
	// <ach-dir>/state-<platform>.json so a multi-target hydrate cannot let one
	// platform's render overwrite another's projection buckets (step12WriteState
	// replaces buckets wholesale). achDir stays the per-environment dir, so the
	// platform-independent content cache (prompt/, artifact/, plugin/) is shared.
	if opts.Platform != "" {
		platformStatePath, perr := state.ResolvePlatformPath(workspaceCwd, opts.Environment, opts.Platform, opts.Global)
		if perr != nil {
			return nil, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("resolve per-platform state path: %v", perr),
				Wrapped: perr,
			}
		}
		// Adopt a pre-per-platform <ach-dir>/state.json that belongs to THIS
		// platform (Adapter.ID match, or an untagged legacy state), carrying its
		// file tracking forward so uninstall/--sync still manage those files.
		if err := adoptLegacyEnvState(statePath, platformStatePath, opts.Platform); err != nil {
			return nil, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("adopt legacy env state: %v", err),
				Wrapped: err,
			}
		}
		statePath = platformStatePath
	}

	// Resolve toolRoot — the base for adapter runtime-config writes. In
	// project scope it is the workspace root (the dir that CONTAINS
	// .ach/). In --global scope adapter configs go under $HOME (the tools'
	// user-level config dirs, e.g. ~/.claude/settings.json), NOT under
	// ~/.ach/<env>/. This decouples the tool config location from achDir
	// so the tools actually read what we write.
	toolRoot := workspaceCwd
	if opts.Global {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return nil, &exit.CodedError{
				Code:    exit.General,
				Msg:     fmt.Sprintf("resolve $HOME for --global adapter config: %v", herr),
				Wrapped: herr,
			}
		}
		toolRoot = home
	}

	c := &commit{
		opts:       opts,
		stateStore: defaultStateStore{},
		locker:     lock.NewLocker(lock.Path(achDir)),
		extractor:  opts.Extractor,
		adapter:    opts.AdapterDispatcher,
		achDir:     achDir,
		statePath:  statePath,
		toolRoot:   toolRoot,
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

// legacyFlatMovableEntries is the closed set of flat <cwd>/.ach entries the D3
// migration relocates into the per-environment subdir. `lock` is intentionally
// excluded (recreated; may be held); existing env-namespace subdirs are never
// in this set so a second env's dir is never swallowed.
var legacyFlatMovableEntries = []string{"state.json", "plugin", "prompt", "artifact", "runtime", "tmp"}

// migrateLegacyFlatState relocates a pre-namespacing flat
// <workspaceCwd>/.ach/state.json (and its sibling content dirs) into
// <workspaceCwd>/.ach/<legacyEnv>/, where legacyEnv is the Environment that
// flat state was bound to. Idempotent + best-effort:
//
//   - no flat state.json present  → no-op (already namespaced, or fresh).
//   - flat state has empty/unreadable Environment → left untouched (cannot
//     choose a namespace safely).
//   - target <legacyEnv>/state.json already exists → no-op (don't clobber).
//
// Only legacyFlatMovableEntries are moved (same-parent os.Rename, cheap), so a
// sibling env-namespace dir from a prior migration is never relocated. Emits a
// single stderr notice when it actually moves something.
func migrateLegacyFlatState(workspaceCwd string, stderr io.Writer) error {
	if workspaceCwd == "" {
		return nil
	}
	achRoot := filepath.Join(workspaceCwd, ".ach")
	flatState := filepath.Join(achRoot, "state.json")

	loaded, err := state.Load(flatState)
	if err != nil {
		// A corrupt/legacy-schema flat state.json is not a migration blocker —
		// leave it in place; the fresh namespaced hydrate proceeds independently.
		return nil
	}
	if loaded == nil || loaded.Environment == "" {
		return nil
	}

	targetDir := filepath.Join(achRoot, loaded.Environment)
	if _, err := os.Stat(filepath.Join(targetDir, "state.json")); err == nil {
		return nil // already migrated
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", targetDir, err)
	}

	moved := 0
	for _, name := range legacyFlatMovableEntries {
		src := filepath.Join(achRoot, name)
		if _, err := os.Lstat(src); err != nil {
			continue // entry absent — skip
		}
		dst := filepath.Join(targetDir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue // already present at target — don't clobber
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("move %s -> %s: %w", src, dst, err)
		}
		moved++
	}
	if moved > 0 && stderr != nil {
		_, _ = fmt.Fprintf(stderr,
			"notice: migrated legacy flat .ach/ into .ach/%s/ (per-environment layout)\n",
			loaded.Environment)
	}
	return nil
}

// adoptLegacyEnvState migrates a pre-per-platform <ach-dir>/state.json to the
// per-platform <ach-dir>/state-<platform>.json so its file tracking carries
// forward (uninstall/--sync keep working). It renames legacyPath → platformPath
// ONLY when:
//   - platformPath does not already exist (never clobber a real per-platform state), AND
//   - the legacy state is readable AND belongs to THIS platform — its
//     Adapter.ID == platform, or is empty (untagged legacy state predating
//     platform tagging).
//
// A legacy state tagged for a DIFFERENT platform is left untouched (that
// platform's own hydrate adopts it). All other conditions (missing/corrupt
// legacy state) are silent no-ops — the fresh per-platform hydrate proceeds.
func adoptLegacyEnvState(legacyPath, platformPath, platform string) error {
	if _, err := os.Stat(platformPath); err == nil {
		return nil // per-platform state already exists — nothing to adopt
	}
	loaded, err := state.Load(legacyPath)
	if err != nil || loaded == nil {
		return nil // no/unreadable legacy state
	}
	if loaded.Adapter.ID != "" && loaded.Adapter.ID != platform {
		return nil // belongs to a different platform
	}
	return os.Rename(legacyPath, platformPath)
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
	result.Environment = c.opts.Environment

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

	// Drop the legacy persistent plugin projection-cache. Pre-ephemeral builds
	// extracted plugins to <achDir>/plugin and kept the tree across runs, which
	// let a plugin removed from the Environment linger on disk and be
	// re-projected (the cross-plugin destination-collision bug). The projection
	// source is now the per-run <achDir>/tmp stage (swept at steps 2 + 13), so
	// the old dir is both dead weight and a stale source — remove it. Scoped to
	// context hydrations (plugins are out of scope under --only-runtime) and
	// skipped under --dry-run (read-only). Prompts/artifacts are hydrator-core
	// deliverables (CLI §6.4), never touched here.
	if !c.opts.DryRun && !c.opts.OnlyRuntime {
		c.dropLegacyPluginCache()
	}

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
	result.RuntimeSummary = c.runtimeSummary(m)
	result.ContextSummary = c.contextSummary(m)
	result.Notice = m.Notice
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
			// Runtime kinds (model / mcpServer / a2aAgent) are NOT extractable
			// content: their ContentRef carries an {id, endpoint} (e.g. /v1,
			// /mcp/<id>), not a /content/{kind}/{name} tarball. They are
			// projected by the adapter's RenderRuntime leg (which reads
			// m.Runtime directly, independent of diffTargets), never fetched +
			// extracted. Feeding one to ExtractContent yields an empty content
			// name → "content name: empty" (the --include-runtime crash). Skip
			// non-context kinds here; step6Diff still emits them for scope
			// symmetry, but extraction is a context-only leg.
			if !dt.isExtractableContent() {
				continue
			}
			// Plugins AND skills are projection CACHE (projectPlugins /
			// projectSkills disk-walk the extracted tree); extract them to the
			// per-run ephemeral stage root so the projection source holds ONLY
			// this run's diffTargets and a removed plugin/skill can never linger
			// and be re-projected. Prompts/artifacts are hydrator-core
			// deliverables (CLI §6.4) and keep their <achDir>/<kind> destination.
			extractBase := c.achDir
			switch dt.Kind {
			case kindPlugin:
				extractBase = c.pluginStageRoot()
			case kindSkill:
				extractBase = c.skillStageRoot()
			}
			extractResult, err := c.extractor.ExtractContent(ctx, dt.Ref, extractBase, existingState)
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
			addExtractCounts(&result, dt.Kind, extractResult)
		}
	}
	c.maybeKill(7)
	c.maybeKill(8)
	c.maybeKill(9)

	// Step 10: adapter dispatch. RenderRuntime + per-FileWrite SAFE-04
	// cascade + atomic publish all live inside adapterDispatcherImpl;
	// the orchestrator just calls Render once after the extraction
	// loop completes.
	//
	// includeRuntime gates the DIRECT runtime block (m.Runtime mcp/a2a/
	// models): a default hydrate projects only plugin-contributed mcps; the
	// Environment's directly-attached runtime endpoints reach the adapter
	// config AND the runtime mirror ONLY under --include-runtime /
	// --only-runtime. Hoisted here so both the Render call and the runtime
	// mirror (step 10b) share one definition.
	includeRuntime := c.opts.IncludeRuntime || c.opts.OnlyRuntime
	var renderResult RenderResult
	adapterRan := false
	if c.adapter != nil && !c.opts.DryRun {
		// ADAPT-03: propagate the bearer credential to the adapter via
		// ctx so RenderRuntime can embed it as the x-ach-key header.
		// Without this the rendered MCP config carries an empty credential
		// and the agent cannot authenticate to the forwarder. Credentials
		// travel by context key only (never env/param) per adapter.go.
		renderCtx := adapter.WithCredential(ctx, c.opts.Bearer)
		// WIRE-04 / D-11 scope gate: plugin/resource projection is the
		// CONTEXT slice. Run it when NOT --only-runtime (default context
		// scope and --include-runtime both project; OnlyRuntime has
		// precedence per spec §6.3 and skips it). The gate lives here in
		// the orchestrator where c.opts is in scope (per D-11/PATTERNS),
		// NOT inside Render.
		projectPlugins := !c.opts.OnlyRuntime
		// projectPlugins reads <base>/plugin; plugins were extracted to the
		// ephemeral pluginStageRoot above, so the projection source is
		// <achDir>/tmp/plugin (this run's diffTargets only). RenderRuntime
		// uses toolRoot, not this base, so the tmp base affects projection only.
		rr, err := c.adapter.Render(renderCtx, m, existingState, c.pluginStageRoot(), c.toolRoot, projectPlugins, includeRuntime)
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
		renderResult = rr
		adapterRan = true
		// Split runtime + projected files by their per-file no-op verdict so
		// the summary distinguishes real writes from preserved (byte-identical)
		// files (D-05). publishFile sets Preserved on the no-op skip path; the
		// entry still flows to state composition either way.
		addPublishedCounts(&result, renderResult)
		result.DroppedComponents = append(result.DroppedComponents, renderResult.DroppedComponents...)
		if result.ProjectedByKind == nil {
			result.ProjectedByKind = map[string]int{}
		}
		for k, n := range renderResult.ProjectedByKind {
			result.ProjectedByKind[k] += n
		}
		result.SourceSummaries = append(result.SourceSummaries, renderResult.SourceSummaries...)
		if result.DroppedByKind == nil {
			result.DroppedByKind = map[string][]string{}
		}
		for k, plugins := range renderResult.DroppedByKind {
			for _, p := range plugins {
				result.DroppedByKind[k] = appendUniqueSorted(result.DroppedByKind[k], p)
			}
		}

		// WIRE-03 / D-12: end-of-hydration stderr warnings (attributed
		// per-kind + MCP-shadow). Exit code is UNCHANGED — these are
		// warnings, never errors. Skipped entirely when nothing was dropped.
		c.warnDropped(result.DroppedByKind, result.DroppedComponents)
	}
	c.maybeKill(10)

	// Step 10b: runtime mirror (gated on !DryRun && includeRuntime).
	runtimeFiles, err := c.step10bRuntimeMirror(m, includeRuntime, &result)
	if err != nil {
		return result, err
	}

	// Step 11: STATE-05 / D-16 inverse-merge sync. maybeKill(11)
	// fires BEFORE the syncFn call so the SIGKILL injection point
	// remains at the step-11 boundary as advertised by
	// sc2_commit_sequence_sigkill. T-07-W5-01-03 — gated on !DryRun.
	c.maybeKill(11)
	if c.opts.Sync && !c.opts.DryRun {
		// STATE-05: prune state entries (and their projected files) for
		// resources dropped from the Environment. composeNextState is PURE
		// (no I/O), so state.json is still untouched at the maybeKill(11)
		// boundary above (the sc2 invariant). Passing the composed next-state
		// as newFile is the fix — previously existingState was passed as BOTH
		// prev and newFile, so the inverse-merge set-difference was always
		// empty and nothing was ever pruned.
		composed := c.composeNextState(existingState, m, renderResult, adapterRan, result.PlatformID, runtimeFiles)
		stats, err := syncFn(existingState, composed, c.achDir, c.toolRoot, SyncOptions{
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
	if err := c.step12WriteState(existingState, m, renderResult, adapterRan, result.PlatformID, runtimeFiles); err != nil {
		return result, err
	}
	c.maybeKill(12)

	// Step 12b: ensure the project .gitignore ignores the credential-bearing
	// agent config this hydrate wrote (project scope only).
	c.step12bGitignore(renderResult)

	// Step 13: cleanup tmp.
	c.step13Cleanup()
	c.maybeKill(13)

	// Step 14: return is implicit.
	return result, nil
}

func (c *commit) runtimeSummary(m *manifest.Manifest) RuntimeSummary {
	includeRuntime := c.opts.OnlyRuntime || c.opts.IncludeRuntime
	if !includeRuntime || m == nil || m.Runtime == nil {
		return RuntimeSummary{}
	}
	return RuntimeSummary{
		Models:     len(m.Runtime.Models),
		MCPServers: len(m.Runtime.MCPServers),
		A2AAgents:  len(m.Runtime.A2AAgents),
	}
}

func (c *commit) contextSummary(m *manifest.Manifest) ContextSummary {
	includeContext := !c.opts.OnlyRuntime
	if !includeContext || m == nil || m.Context == nil {
		return ContextSummary{}
	}
	return ContextSummary{
		Plugins:   len(m.Context.Plugins),
		Prompts:   len(m.Context.Prompts),
		Artifacts: len(m.Context.Artifacts),
		Skills:    len(m.Context.Skills),
	}
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

// pluginStageRoot is the per-run ephemeral base under which plugin content is
// extracted (<root>/plugin/<name>) AND from which projectPlugins reads. It
// lives under <achDir>/tmp so SweepTmp (steps 2 + 13) reclaims it every run —
// the projection source therefore only ever contains the current run's plugins
// (no persistent cache, no orphan re-projection of a removed plugin). The
// extract call and the Render call MUST use this same root or projection reads
// an empty tree, so it is centralized here rather than duplicated at both sites.
func (c *commit) pluginStageRoot() string {
	return filepath.Join(c.achDir, "tmp")
}

// skillStageRoot is the per-run ephemeral base under which skill content is
// extracted (<root>/skill/<name>) AND from which projectSkills reads. Skills
// share the plugin tmp base — ExtractContent appends the /skill/ subdir from
// the resource kind, so this is the SAME directory as pluginStageRoot, named
// separately for call-site clarity. Living under <achDir>/tmp means SweepTmp
// reclaims the extracted skills (and the synthetic skills/ projection tree)
// every run.
func (c *commit) skillStageRoot() string {
	return c.pluginStageRoot()
}

// dropLegacyPluginCache removes the pre-ephemeral persistent plugin
// projection-cache at <achDir>/plugin. Best-effort + idempotent: a missing dir
// is a no-op and any error is swallowed (a residual cache dir never blocks a
// hydrate, matching SweepTmp's benign-cleanup contract). ONLY the plugin cache
// is removed — <achDir>/{prompt,artifact} hold hydrator-core deliverables
// (CLI §6.4), not projection cache, and are left intact.
func (c *commit) dropLegacyPluginCache() {
	_ = os.RemoveAll(filepath.Join(c.achDir, "plugin"))
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
	// Content buckets are workspace-relative (resolved against the workspace
	// root = achDir's parent). Adapter files live under toolRoot — the same
	// workspace root in project scope, but $HOME under --global (where achDir
	// is $HOME/.ach/<env>, so achDir/.. would wrongly point at $HOME/.ach).
	wsRoot := filepath.Join(c.achDir, "..")
	loaded.Prompts, pruned = c.pruneMissing(loaded.Prompts, wsRoot, pruned)
	// Projected plugin resources are published under toolRoot (native resource
	// dirs), so their reconcile-vs-disk stat must resolve there too — symmetric
	// with the Adapter.Files line below and with walkEntriesTagged tagging every
	// Plugins entry ResolveAgainstToolRoot. Using wsRoot coincides with toolRoot
	// in project scope but points at $HOME/.ach under --global, silently pruning
	// live projected plugins from state on every re-hydrate (breaks FMT-05
	// idempotence and re-opens the survive-uninstall defect CR-01).
	loaded.Plugins, pruned = c.pruneMissing(loaded.Plugins, c.toolRoot, pruned)
	loaded.Artifacts, pruned = c.pruneMissing(loaded.Artifacts, wsRoot, pruned)
	// Projected skill resources land under toolRoot (.claude/skills/…), like
	// plugins — stat against toolRoot for the same --global correctness reason.
	loaded.Skills, pruned = c.pruneMissing(loaded.Skills, c.toolRoot, pruned)
	// RuntimeFiles live UNDER achDir (.ach/runtime/*.json) in BOTH scopes —
	// their Target is achDir-relative, so they stat against achDir (not wsRoot,
	// which points at $HOME/.ach under --global and would mis-prune).
	loaded.RuntimeFiles, pruned = c.pruneMissing(loaded.RuntimeFiles, c.achDir, pruned)
	loaded.Adapter.Files, pruned = c.pruneMissing(loaded.Adapter.Files, c.toolRoot, pruned)
	return loaded, pruned
}

// pruneMissing walks a FileEntry slice and rebuilds it with only the
// entries whose Target stat'd successfully. fs.ErrNotExist is the
// silent-drop case; any other error keeps the entry (let the next
// stage error appropriately rather than masking a real I/O fault here).
func (c *commit) pruneMissing(entries []state.FileEntry, base string, pruned int) ([]state.FileEntry, int) {
	if len(entries) == 0 {
		return entries, pruned
	}
	kept := entries[:0]
	for _, e := range entries {
		target := e.Target
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
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

// Context diffTarget kinds — extractable /content/{kind}/{name} resources.
// Runtime kinds (model / mcpServer / a2aAgent) are NOT extractable (see
// diffTarget.isExtractableContent).
const (
	kindPrompt   = "prompt"
	kindPlugin   = "plugin"
	kindArtifact = "artifact"
	kindSkill    = "skill"
)

// isExtractableContent reports whether dt is a context kind whose Ref points at
// a /content/{kind}/{name} tarball the extractor can fetch+stage. Runtime kinds
// (model / mcpServer / a2aAgent) carry an {id, endpoint} instead and are
// projected by the adapter RenderRuntime leg, never extracted — sending one to
// ExtractContent yields "content name: empty".
func (dt diffTarget) isExtractableContent() bool {
	switch dt.Kind {
	case kindPrompt, kindPlugin, kindArtifact, kindSkill:
		return true
	default:
		return false
	}
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
			targets = append(targets, diffTarget{Kind: kindPrompt, Ref: p})
		}
		for _, p := range m.Context.Plugins {
			targets = append(targets, diffTarget{Kind: kindPlugin, Ref: p})
		}
		for _, a := range m.Context.Artifacts {
			targets = append(targets, diffTarget{Kind: kindArtifact, Ref: a})
		}
		for _, s := range m.Context.Skills {
			targets = append(targets, diffTarget{Kind: kindSkill, Ref: s})
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

// warnDropped emits up to two end-of-hydration stderr warnings; exit code is
// never affected.
//
//  1. byKind (attributed projection drops): for each KNOWN component kind the
//     active platform has no rule for, a line naming the kind and the plugins
//     that shipped it. Skipped entirely when empty.
//  2. flat (the runtime-wins MCP-shadow drops still carried in
//     DroppedComponents): MCP server ids a runtime-owned definition shadowed.
//     These are NOT "unsupported" — they were intentionally superseded — so
//     they get a distinct, correctly-worded line.
func (c *commit) warnDropped(byKind map[string][]string, flat []string) {
	if len(byKind) > 0 {
		kinds := make([]string, 0, len(byKind))
		for k := range byKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		_, _ = fmt.Fprintf(c.opts.Stderr,
			"warning: platform %s does not support some plugin components — they were skipped:\n",
			c.opts.Platform)
		for _, k := range kinds {
			_, _ = fmt.Fprintf(c.opts.Stderr,
				"    %s (plugins: %s)\n", k, strings.Join(byKind[k], ", "))
		}
	}

	if len(flat) > 0 {
		isKind := func(s string) bool { _, ok := byKind[s]; return ok }
		seen := map[string]bool{}
		var shadow []string
		for _, s := range flat {
			if s == "" || isKind(s) || seen[s] {
				continue
			}
			seen[s] = true
			shadow = append(shadow, s)
		}
		if len(shadow) > 0 {
			sort.Strings(shadow)
			_, _ = fmt.Fprintf(c.opts.Stderr,
				"warning: platform %s: projected MCP server(s) shadowed by runtime-owned definitions: %s\n",
				c.opts.Platform, strings.Join(shadow, ", "))
		}
	}
}

// step12WriteState — atomic state.json publication via state.Save
// (= state.WriteAtomic, STATE-07 four-step contract). Skipped when
// opts.DryRun is set so a `hydrate --dry-run` is genuinely read-only.
//
// Content buckets (Prompts/Plugins/Artifacts/RuntimeFiles) are carried
// forward from the prior state (their composition from ExtractResult is a
// separate follow-up; see issue tracker / W6-01 notes). The ADAPTER section,
// however, IS composed from the fresh render (W6-01): recording the adapter
// FileEntries is what gives the next hydrate a prior state for the §8.4
// per-key drift truth table — without it findAdapterEntry always misses and
// drift / auto-claim (sc3 / sc4) cannot fire. The adapter section is replaced
// only when the adapter actually ran this hydrate; a context-only run leaves
// the prior adapter section untouched (spec §8.2 field rules).
// composeNextState builds the post-hydrate state.File in memory. PURE — no
// I/O. Used by BOTH step 11 (the --sync prune target / newFile, STATE-05) and
// step 12 (the persisted state), so the set the sync diffs against and the set
// actually written are guaranteed identical. Keeping it I/O-free is what lets
// step 11 run AFTER the maybeKill(11) SIGKILL boundary without ever touching
// state.json (the sc2 invariant — see step12WriteState's caller).
func (c *commit) composeNextState(existing *state.File, m *manifest.Manifest, render RenderResult, adapterRan bool, platformID string, runtimeFiles []state.FileEntry) *state.File {
	// Build the next-state from the prior + the manifest environment.
	next := &state.File{
		SchemaVersion: "3",
		Environment:   c.opts.Environment,
	}
	if existing != nil {
		// Preserve everything not (re)composed this hydrate.
		next.Profile = existing.Profile
		next.Prompts = existing.Prompts
		next.Plugins = existing.Plugins
		next.Artifacts = existing.Artifacts
		next.Skills = existing.Skills
		next.RuntimeFiles = existing.RuntimeFiles
		next.Adapter = existing.Adapter
	}
	if m != nil && m.Environment != "" && next.Environment == "" {
		next.Environment = m.Environment
	}

	// Compose the adapter section from the fresh render. publishRuntimeFile
	// returns its FileWrite even on a no-op skip, so render.WrittenFiles is the
	// complete set of adapter files this hydrate is responsible for.
	if adapterRan {
		next.Adapter = adapterSectionFromRender(platformID, render)
	}

	// Compose the Plugins[] bucket (D-07 / WIRE-02) from the fresh
	// projected files — one FileEntry per projected file, carrying
	// Target/Hash/SourceHash/Merge/Keys VERBATIM (the Keys faithfulness
	// is the T-01-04 invariant: Phase 2-3 deep-merge resources inherit
	// correct per-plugin key scoping so Phase 4 uninstall subtracts
	// exactly the plugin's keys). Replace the bucket only when the
	// adapter ran AND projection was in scope (NOT --only-runtime);
	// otherwise carry forward existing.Plugins unchanged (mirroring the
	// adapter-section field rule — spec §8.2).
	if adapterRan && !c.opts.OnlyRuntime {
		next.Plugins = pluginsSectionFromRender(render)
		next.Skills = skillsSectionFromRender(render)
	}

	// RuntimeFiles is composed from the fresh runtime mirror written at
	// step 10b (replaces the carried-forward set). The mirror always runs
	// on a non-dry-run hydrate, so an empty slice here means "the manifest
	// exposed no runtime entries" — recorded faithfully so --sync prunes a
	// now-empty bucket.
	next.RuntimeFiles = runtimeFiles

	return next
}

func (c *commit) step12WriteState(existing *state.File, m *manifest.Manifest, render RenderResult, adapterRan bool, platformID string, runtimeFiles []state.FileEntry) error {
	if c.opts.DryRun {
		return nil
	}
	next := c.composeNextState(existing, m, render, adapterRan, platformID, runtimeFiles)
	if err := c.stateStore.Save(c.statePath, next); err != nil {
		return &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("write state.json: %v", err),
			Wrapped: err,
		}
	}
	return nil
}

// runtimeMirrorBuckets is the closed set of runtime mirror files, in a
// deterministic order so the written set + state rows are byte-stable.
var runtimeMirrorBuckets = []string{"mcp", "a2a", "model"}

// addExtractCounts folds one extract outcome into the written/preserved
// tallies. Plugins extract to the ephemeral pluginStageRoot (swept, never a
// final write) and are re-counted as renderResult.ProjectedFiles when
// published — counting them here would double-count (observed 110 reported
// vs 54 on disk), so only prompts/artifacts (final dest extractBase==achDir)
// count. A re-hydrate no-op reports zero WrittenFiles + a Preserved count
// (the on-disk tree was left untouched) → "N preserved", not "N written".
func addExtractCounts(result *Result, kind string, er ExtractResult) {
	if kind == kindPlugin || kind == kindSkill {
		// Skills, like plugins, extract to the ephemeral stage root and are
		// re-counted as render ProjectedSkillFiles when published — counting
		// the extract WrittenFiles here would double-count.
		return
	}
	result.FilesWritten += len(er.WrittenFiles)
	result.FilesPreserved += er.Preserved
	fileCount := len(er.WrittenFiles) + er.Preserved
	switch kind {
	case kindPrompt:
		result.ContextSummary.PromptFiles += fileCount
	case kindArtifact:
		result.ContextSummary.ArtifactFiles += fileCount
	}
}

// addPublishedCounts splits a RenderResult's published files (runtime
// WrittenFiles + plugin ProjectedFiles) into the written vs preserved
// tallies by each entry's D-05 no-op verdict (FileWrite.Preserved).
func addPublishedCounts(result *Result, rr RenderResult) {
	for _, fw := range rr.WrittenFiles {
		if fw.Preserved {
			result.FilesPreserved++
		} else {
			result.FilesWritten++
		}
	}
	for _, fw := range rr.ProjectedFiles {
		if fw.Preserved {
			result.FilesPreserved++
		} else {
			result.FilesWritten++
		}
	}
	for _, fw := range rr.ProjectedSkillFiles {
		if fw.Preserved {
			result.FilesPreserved++
		} else {
			result.FilesWritten++
		}
	}
	// Standalone-skill projected files feed the summary's Skills line (distinct
	// from the plugin ProjectedByKind aggregate).
	result.ContextSummary.SkillFiles += len(rr.ProjectedSkillFiles)
}

// step10bRuntimeMirror writes the credential-free runtime mirror and folds
// its file count into result. Gated on !DryRun && includeRuntime:
// the runtime block (mcp / a2a / models) is the --include-runtime scope slice,
// so a default hydrate reads no runtime objects and writes no mirror —
// consistent with the gated RenderRuntime projection. The mirror is the
// canonical secret-free cache + the state rows that let --sync/uninstall and
// drift see runtime entries (incl. models, which have no adapter destination).
func (c *commit) step10bRuntimeMirror(m *manifest.Manifest, includeRuntime bool, result *Result) ([]state.FileEntry, error) {
	if c.opts.DryRun || !includeRuntime {
		return nil, nil
	}
	rf, err := c.writeRuntimeMirror(m)
	if err != nil {
		return nil, &exit.CodedError{
			Code:    exit.General,
			Msg:     fmt.Sprintf("runtime mirror: %v", err),
			Wrapped: err,
		}
	}
	result.FilesWritten += len(rf)
	return rf, nil
}

// writeRuntimeMirror serializes the manifest runtime block into
// credential-free <achDir>/runtime/{mcp,a2a,model}.json snapshots and returns
// the state.FileEntry rows (achDir-relative Target) for state.RuntimeFiles.
//
// The ContentRefs carry only {id, name, downloadUrl, endpoint} — the bearer
// credential is injected exclusively at adapter render (RenderRuntime), never
// here, so the cache holds NO secret (OBS-02). A bucket with no entries is not
// written and any stale snapshot from a prior hydrate is removed so .ach/runtime
// always reflects the current Environment. Returns the rows in
// runtimeMirrorBuckets order.
func (c *commit) writeRuntimeMirror(m *manifest.Manifest) ([]state.FileEntry, error) {
	// Per-platform runtime mirror dir so a multi-target hydrate does not let one
	// platform's MCP/A2A snapshot overwrite another's (the snapshots differ per
	// tool) and trigger false drift on the other's next hydrate.
	runtimeRel := "runtime-" + c.opts.Platform
	runtimeDir := filepath.Join(c.achDir, runtimeRel)
	bucketRefs := map[string][]manifest.ContentRef{}
	if m != nil && m.Runtime != nil {
		bucketRefs["mcp"] = m.Runtime.MCPServers
		bucketRefs["a2a"] = m.Runtime.A2AAgents
		bucketRefs["model"] = m.Runtime.Models
	}

	entries := make([]state.FileEntry, 0, len(runtimeMirrorBuckets))
	var madeDir bool
	for _, name := range runtimeMirrorBuckets {
		rel := filepath.Join(runtimeRel, name+".json")
		abs := filepath.Join(runtimeDir, name+".json")
		refs := bucketRefs[name]
		if len(refs) == 0 {
			// Empty bucket: drop any stale snapshot so the cache is accurate.
			if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("remove stale %s: %w", rel, err)
			}
			continue
		}
		data, err := json.MarshalIndent(refs, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", rel, err)
		}
		data = append(data, '\n')
		if !madeDir {
			if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir runtime dir: %w", err)
			}
			madeDir = true
		}
		if err := state.WriteAtomic(abs, data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", rel, err)
		}
		h := hash.HashBytes(data)
		entries = append(entries, state.FileEntry{Target: rel, Hash: h, SourceHash: h})
	}
	return entries, nil
}

// adapterSectionFromRender projects a RenderResult into the
// state.AdapterSection recorded at step 12 — the prior state the next
// hydrate's §8.4 per-key drift check reads via findAdapterEntry. Each
// FileWrite maps 1:1 to a state.FileEntry (Target/Hash/SourceHash carry the
// canonical hash of OUR contributed subtree; Merge/Keys drive the inverse-
// merge + per-key drift comparison).
func adapterSectionFromRender(platformID string, render RenderResult) state.AdapterSection {
	sec := state.AdapterSection{ID: platformID}
	for _, fw := range render.WrittenFiles {
		sec.Files = append(sec.Files, state.FileEntry{
			Target:     fw.Target,
			Hash:       fw.Hash,
			SourceHash: fw.SourceHash,
			Merge:      fw.Merge,
			Keys:       fw.Keys,
		})
	}
	return sec
}

// pluginsSectionFromRender projects RenderResult.ProjectedFiles into the
// state.File.Plugins bucket recorded at step 12 (D-07 / WIRE-02). Each
// projected FileWrite maps 1:1 to a state.FileEntry. Keys is copied
// VERBATIM for every MergeKind (T-01-04): the recording path never filters
// or rewrites Keys, so a MergeDeep co-owned resource's contributed dotted
// paths survive into state.Plugins[] and Phase 4 uninstall/--sync subtracts
// exactly the plugin's keys (and never the user's other keys). Phase-1
// passthrough resources use Merge=replace/Keys=nil; the faithful-Keys
// guarantee is what lets Phase 2-3 deep-merge resources inherit correct
// per-plugin scoping unchanged.
func pluginsSectionFromRender(render RenderResult) []state.FileEntry {
	if len(render.ProjectedFiles) == 0 {
		return nil
	}
	out := make([]state.FileEntry, 0, len(render.ProjectedFiles))
	for _, fw := range render.ProjectedFiles {
		out = append(out, state.FileEntry{
			Target:     fw.Target,
			Hash:       fw.Hash,
			SourceHash: fw.SourceHash,
			Merge:      fw.Merge,
			Keys:       fw.Keys,
		})
	}
	return out
}

// skillsSectionFromRender projects RenderResult.ProjectedSkillFiles into the
// state.File.Skills bucket recorded at step 12 — the standalone-Skill analogue
// of pluginsSectionFromRender. One projected FileWrite maps 1:1 to a
// state.FileEntry (Keys copied verbatim, always nil for the passthrough
// skills/** MergeReplace rule).
func skillsSectionFromRender(render RenderResult) []state.FileEntry {
	if len(render.ProjectedSkillFiles) == 0 {
		return nil
	}
	out := make([]state.FileEntry, 0, len(render.ProjectedSkillFiles))
	for _, fw := range render.ProjectedSkillFiles {
		out = append(out, state.FileEntry{
			Target:     fw.Target,
			Hash:       fw.Hash,
			SourceHash: fw.SourceHash,
			Merge:      fw.Merge,
			Keys:       fw.Keys,
		})
	}
	return out
}

// step12bGitignore appends an ach-managed block to <toolRoot>/.gitignore listing
// the top-level dirs/files this hydrate wrote under the project root (the adapter
// dirs, a project-root .mcp.json when claude generated one, and ACH's own .ach/
// cache) so the credential-bearing agent config is not accidentally committed.
//
// Project scope only — under --global toolRoot is $HOME (no repo to guard) — and
// never on --dry-run. Best-effort: a .gitignore write failure warns but never
// fails the hydrate (the config files are already published; the ignore block is
// defense-in-depth on top of their 0600 mode).
func (c *commit) step12bGitignore(render RenderResult) {
	// toolRoot is always set in production (newCommit resolves workspaceCwd or
	// $HOME). An empty toolRoot only occurs in direct-struct unit tests; guard
	// it so we never write a .gitignore into the current working directory.
	if c.opts.Global || c.opts.DryRun || c.toolRoot == "" {
		return
	}
	entries := []string{".ach/"}
	for _, set := range [][]FileWrite{render.WrittenFiles, render.ProjectedFiles, render.ProjectedSkillFiles} {
		for _, fw := range set {
			if e := gitignore.TopLevelEntry(fw.Target); e != "" {
				entries = append(entries, e)
			}
		}
	}
	wrote, err := gitignore.Ensure(c.toolRoot, entries)
	if err != nil {
		_, _ = fmt.Fprintf(c.opts.Stderr,
			"warning: could not update .gitignore (agent config may be committable): %v\n", err)
		return
	}
	if wrote {
		_, _ = fmt.Fprintln(c.opts.Stderr,
			"notice: updated .gitignore (ach-cli block: agent config carries credentials)")
	}
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
