// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"io"
	"time"
)

// Opts carries every engine flag the §6.7 14-step orchestrator
// consumes. The shape mirrors cmd/ach-cli/cmd/hydrate.go's
// hydrateInputs (Phase 6 D-09 surface-form) but extends with the
// Phase 7 engine flags from D-03.
//
// Fields are grouped: target selectors → scope filters → behavior
// toggles → I/O / locking → transport bag → test seams.
//
// Run(ctx, opts) is the single public engine entry point per
// CONTEXT.md `<code_context>` Integration Points #2. Callers
// construct an Opts directly; cmd/ach-cli/cmd/hydrate.go's D-03
// refactor wires cobra flags into this struct.
type Opts struct {
	// --- target selectors ---

	// Environment is the target Environment name. REQUIRED for pk_
	// credentials (D-12 / spec §5.7); OPTIONAL for ek_.
	Environment string

	// Platform is the adapter id (ADAPT-01: claude-code / codex /
	// gemini-cli / opencode). Empty → autodetect via the adapter
	// registry's Detect() (ADAPT-02 / D-06). Multi-match autodetect
	// exits 1 with the candidate list.
	Platform string

	// Global selects $HOME/.ach/<env> scope instead of
	// <cwd>/.ach (spec §8.1). state.ResolvePath dispatches on this.
	Global bool

	// --- scope filters (STATE-10) ---

	// IncludeRuntime expands the diff to cover runtime entries
	// (models, mcpServers, a2aAgents). Default false → only context
	// entries (prompts, plugins, artifacts) are reconciled.
	IncludeRuntime bool

	// OnlyRuntime restricts the diff to runtime entries only.
	// Mutually exclusive with the default context-only mode and
	// with IncludeRuntime (caller layer enforces; engine treats
	// OnlyRuntime as having precedence per spec §6.3).
	OnlyRuntime bool

	// --- behavior toggles ---

	// Sync enables STATE-05 inverse-merge deletion of state entries
	// missing from the fresh manifest. Deepest-first; preserves
	// drift-bearing files with stderr warning (D-16).
	Sync bool

	// Force overrides STATE-03 environment guard (exit 4), STATE-04
	// drift refusal (exit 2 → write anyway), and STATE-02/09 schema
	// mismatch (exit 5 → treat state as empty and rewrite).
	// Single flag, multiple bypasses by design (CLI spec §6.7).
	Force bool

	// Conflict selects how cross-plugin destination collisions are
	// resolved during the projection leg. Default ConflictNamespace.
	Conflict ConflictPolicy

	// DryRun runs every read+diff step but skips step 12 (state
	// write) and step 8 (real extract). Result still reflects what
	// WOULD be written, so --verbose can preview.
	DryRun bool

	// AllowSymlinks relaxes the SAFE-01 tar policy's symlink reject
	// (CLI spec §6.4). Default false — symlinks are dropped from
	// archives. Documented as an unsafe escape hatch.
	AllowSymlinks bool

	// --- I/O / locking ---

	// Output is the workspace root override. Empty → use cwd in
	// workspace scope, or $HOME/.ach in global scope. The engine
	// passes (Output, Environment, Global) to state.ResolvePath
	// to derive <ach-dir>.
	Output string

	// Wait selects lock.AcquireWait — block indefinitely (subject to
	// ctx cancellation) until the workspace lock is granted. Mutually
	// exclusive with LockTimeout (caller layer enforces).
	Wait bool

	// LockTimeout caps the wait at the supplied duration; selects
	// lock.AcquireWithTimeout. Zero with Wait==false selects
	// lock.AcquireFailFast.
	LockTimeout time.Duration

	// --- transport bag ---

	// BaseURL is the platform-api root the manifest fetcher targets.
	// Caller layer resolves from ACH_BASE_URL or config (D-11).
	BaseURL string

	// Bearer is the resolved credential (pk-<…> or ek-<…>) for the
	// POST /platform/hydrate Authorization header. Caller layer
	// resolves per D-11 mutex / D-12 pk- + --environment gate.
	Bearer string

	// --- behavior + test seams ---

	// Verbose mirrors `--verbose` — drives the Result-summary stderr
	// emit at end of Run (CONTEXT.md Integration Points).
	Verbose bool

	// Stdout / Stderr are I/O seams for testability — the cobra
	// layer wires cmd.OutOrStdout / cmd.ErrOrStderr; tests pass
	// bytes.Buffer. Both default-via-zero to os.Stdout / os.Stderr
	// when nil (commit.go normalizes).
	Stdout io.Writer
	Stderr io.Writer

	// --- DI seams (07-W5-01 gap closure) ---

	// Extractor is the concrete extractor impl that drives steps 7-9
	// of the §6.7 14-step commit sequence (FetchContent +
	// StageAndPublish). Production callers (cmd/ach-cli/cmd/hydrate.go
	// runHydrateEngine) supply hydrate.NewWiring's first return value;
	// unit tests leave nil to exercise the W1 stub fall-through in
	// commit.run().
	Extractor Extractor

	// AdapterDispatcher is the concrete dispatcher impl that drives
	// step 10 of the §6.7 14-step commit sequence (Lookup + Render +
	// SAFE-04 cascade). Production callers supply hydrate.NewWiring's
	// second return value; unit tests leave nil to exercise the W1
	// stub fall-through in commit.run().
	AdapterDispatcher AdapterDispatcher
}
