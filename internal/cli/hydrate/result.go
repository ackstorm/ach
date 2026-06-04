// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"

	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// Result is the engine's per-Run summary. Counts feed the --verbose
// stderr output (CONTEXT.md Integration Points); DroppedComponents
// is the ADAPT-07 silent-drop list the orchestrator surfaces as a
// single end-of-run stderr warning.
//
// All counts are zero-initialized; an early-exit Run (e.g. lock
// contended) returns a zero-value Result alongside its CodedError.
type Result struct {
	// FilesWritten is the total count of files committed to disk
	// across every category (prompts, plugins, artifacts, runtime,
	// adapter). Counted in step 8 (extract) and step 10 (adapter).
	FilesWritten int

	// FilesPreserved is the count of on-disk files the engine
	// REFUSED to overwrite because the drift four-outcome truth
	// table classified them as LocalEditPreserve or ConflictPreserve
	// (STATE-04). The user's edits remain intact; the engine raised
	// exit.Drift unless --force.
	FilesPreserved int

	// FilesPruned is the count of state entries silently dropped at
	// step 4 because the recorded target was missing on disk
	// (STATE-04 truth table — "tracked file missing on disk →
	// silently pruned"). Surfaced via --verbose only.
	FilesPruned int

	// DroppedComponents is the list of adapter components (per
	// ADAPT-07) the active platform could not meaningfully translate
	// (e.g. claude `hooks/` against the Codex adapter). The
	// orchestrator emits a single end-of-run stderr warning naming
	// these so the user knows which slices of upstream content
	// silently went un-rendered. Empty for the claude-code adapter
	// (pass-through).
	DroppedComponents []string

	// ProjectedByKind tallies projected regular files per source kind
	// (e.g. {"commands":12,"agents":8}) rolled up from RenderResult.
	// Feeds the hydrate success summary.
	ProjectedByKind map[string]int

	// DroppedByKind maps a dropped KNOWN component kind to the sorted unique
	// plugin names that shipped it but whose content the active platform has
	// no destination for. Drives the attributed end-of-run warning.
	DroppedByKind map[string][]string

	// PlatformID is the adapter id used for this Run — either the
	// caller's opts.Platform or the autodetected match (ADAPT-02 /
	// D-06). Surfaced in --verbose stderr; recorded into
	// state.File.Adapter.ID at step 12.
	PlatformID string
}

// ExtractResult is the typed return value of Extractor.ExtractContent.
// W2-01/02 supply the concrete impl; W1 unit tests use a stub returning
// a zero-value ExtractResult.
type ExtractResult struct {
	// WrittenFiles is the per-file outcome of the extract — each
	// entry feeds step 9 (hash + classify) and step 12 (state.Save).
	WrittenFiles []FileWrite

	// Preserved is the count of on-disk files left untouched on a re-hydrate
	// no-op (W6-01 Bug E: when the upstream source is unchanged the extract
	// is skipped and WrittenFiles is empty). Reported separately from
	// WrittenFiles — which feeds state composition — so commit.go can count
	// FilesPreserved without injecting phantom entries into the state. Zero
	// on a fresh extract or a real re-write.
	Preserved int

	// SourceHash is the xxh3 digest of the upstream input bytes
	// BEFORE any adapter transformation (D-14 dual-hash discipline).
	// For pass-through resources this equals the per-FileWrite Hash;
	// for adapter-merged files the two diverge. Recorded into
	// state.FileEntry.SourceHash at step 12.
	SourceHash string
}

// FileWrite is the per-file projection record emitted by Extractor and
// AdapterDispatcher and consumed by step 9 (hash + classify) and step
// 12 (state.Save). Target is the workspace-relative path; Hash is the
// xxh3 of the bytes actually written; SourceHash is the upstream input
// (D-14). Merge + Keys are populated only for adapter-merged shared
// files (D-16 inverse-merge driver).
type FileWrite struct {
	Target     string
	Hash       string
	SourceHash string
	Merge      string
	Keys       []string

	// Preserved is true when publishFile skipped the write because the
	// on-disk file was already byte-identical to the rendered output (D-05
	// no-op skip). The entry still flows to state composition (the file is
	// tracked either way); commit.go uses this to count it as FilesPreserved
	// rather than FilesWritten. Always false on the Extractor path (a no-op
	// extract reports preserved via ExtractResult.Preserved instead).
	Preserved bool
}

// RenderResult is the typed return value of AdapterDispatcher.Render.
// W3-01..05 supply the concrete impl; W1 unit tests use a stub
// returning a zero-value RenderResult.
type RenderResult struct {
	// WrittenFiles flows into step 12 (state.File.Adapter.Files) — the
	// adapter RUNTIME bucket (RenderRuntime output: .claude/settings.json
	// etc.).
	WrittenFiles []FileWrite

	// ProjectedFiles flows into step 12 (state.File.Plugins) — the
	// PROJECTION bucket (route.Project output: plugin resources routed
	// into the adapter's native layout, e.g. .claude/rules/foo.md).
	// Kept distinct from WrittenFiles so commit.go step12WriteState
	// routes projected files to the Plugins[] bucket (D-07) rather than
	// Adapter.Files. Each entry already flowed through the SAME
	// publishRuntimeFile path (per-key drift + no-op skip + atomic
	// publish) as the runtime files (D-05).
	ProjectedFiles []FileWrite

	// DroppedComponents is the ADAPT-07 silent-drop list flowed into
	// Result.DroppedComponents — names of upstream component types
	// the active adapter cannot meaningfully translate (e.g.
	// claude-code "hooks/" against the Codex adapter).
	DroppedComponents []string

	// ProjectedByKind tallies projected regular files per source kind
	// (e.g. {"commands":12,"agents":8}) aggregated across every plugin tree.
	// Feeds the hydrate success summary.
	ProjectedByKind map[string]int

	// DroppedByKind maps a dropped KNOWN component kind to the sorted unique
	// plugin names that shipped it but whose content the active platform has
	// no destination for. Drives the attributed end-of-run warning.
	DroppedByKind map[string][]string
}

// Extractor is the safe-tar + auto-claim cascade interface (CLI spec
// §6.4 SAFE-01..06). The W1 orchestrator holds it via a *commit field
// so the W2 concrete impl (`internal/cli/extract`) can be wired in by
// the caller layer (07-W3-05) without modifying commit.go.
//
// TODO 07-W2-01 supplies the concrete Extractor implementation; the
// production-default in newCommit() leaves this field nil → step 7+8
// short-circuit as a W1 stub.
type Extractor interface {
	// ExtractContent fetches `ref.DownloadURL` (STATE-11 unconditional
	// GET), runs the SAFE-01 tar policy + SAFE-03 bomb defense, stages
	// into <achDir>/tmp/<rand>/, and computes the upstream-source
	// xxh3 sum. Returns the per-file writes + the upstream source hash.
	//
	// prev is the prior (reconciled) state.File or nil on a fresh
	// workspace. When it records this content's upstream SourceHash and
	// the freshly-fetched bytes match, ExtractContent skips the write and
	// returns zero WrittenFiles (W6-01 Bug E re-hydrate no-op). On a
	// genuine change a pre-existing extracted DIRECTORY is removed before
	// re-extract (delete-before-replace) since renameAtomic cannot replace
	// a non-empty dir.
	ExtractContent(ctx context.Context, ref manifest.ContentRef, achDir string, prev *state.File) (ExtractResult, error)
}

// AdapterDispatcher renders the per-platform runtime config + plugin
// transformations (D-05 / ADAPT-01..07). The W1 orchestrator holds it
// via a *commit field; the W3 concrete impl wires up the 4 registered
// adapters via init() side-effect registration.
//
// TODO 07-W3-01..05 supplies the concrete AdapterDispatcher; the
// production-default in newCommit() leaves this field nil → step 10
// short-circuits as a W1 stub.
type AdapterDispatcher interface {
	// Render is invoked at step 10 of the §6.7 sequence. The
	// orchestrator passes the fetched manifest + the (possibly nil)
	// prior state.File + the resolved <ach-dir>. The dispatcher
	// chooses the platform (caller layer pre-sets opts.Platform or
	// the dispatcher autodetects), invokes that adapter's
	// RenderRuntime and (when projectPlugins) route.Project, and
	// returns FileWrites + DroppedComponents.
	// toolRoot is the base the adapter's workspace-relative FileWrite
	// paths join against: the workspace root in project scope, $HOME in
	// --global scope. It is DISTINCT from achDir (ACH's private state +
	// content cache) so adapter runtime-config (e.g. .claude/settings.json)
	// lands where the tool actually reads it, not buried under .ach/.
	//
	// projectPlugins is the WIRE-04 / D-11 scope gate: when true the
	// projection leg (route.Project → publishRuntimeFile → Plugins[])
	// runs after the RenderRuntime loop; when false it is skipped
	// entirely. The orchestrator derives it as !opts.OnlyRuntime in
	// commit.go step 10 (where opts is in scope, per D-11) — default
	// context scope and --include-runtime project plugins; --only-runtime
	// skips them (OnlyRuntime has precedence per spec §6.3).
	//
	// includeRuntime gates the DIRECT runtime block (m.Runtime mcp/a2a/
	// models via RenderRuntime). Derived as opts.IncludeRuntime ||
	// opts.OnlyRuntime: a default hydrate projects ONLY plugin-contributed
	// mcps (the context slice via projectPlugins); the Environment's direct
	// runtime mcp/a2a/models reach the adapter config only under
	// --include-runtime / --only-runtime. Plugin mcps and direct runtime
	// mcps are independent axes — projectPlugins governs the former,
	// includeRuntime the latter.
	Render(ctx context.Context, m *manifest.Manifest, s *state.File, achDir, toolRoot string, projectPlugins, includeRuntime bool) (RenderResult, error)
}

// DriftOutcome is the typed-int enum returned by Differ.Compare for the
// STATE-04 §8.4 four-outcome truth table. The concrete constants and
// the helper functions (ShouldExit2 / WrapDriftError) live in drift.go;
// the type itself is declared here so the Differ interface compiles
// without depending on drift.go's load order.
type DriftOutcome int

// Differ computes the STATE-04 four-outcome truth table (§8.4) for a
// single state entry vs the fresh on-disk and freshly-staged source
// hashes. The W1 concrete impl lives in drift.go (drift.NewDiffer).
//
// TODO 07-W1-06 Task 3 supplies the concrete Differ here (drift.go).
type Differ interface {
	// Compare classifies a single FileEntry against the fresh hash
	// values per §8.4. Returns one of the four DriftOutcome values
	// (NoOp / UpstreamOnlyOverwrite / LocalEditPreserve /
	// ConflictPreserve). When stateEntry == nil (fresh extract — no
	// prior state to compare against), returns NoOp.
	Compare(stateEntry *state.FileEntry, onDiskHash, freshSourceHash string) DriftOutcome
}

// StateStore is the data-layer seam wrapping the internal/cli/state
// package's File-level operations. Unit tests inject a fake to drive
// GuardEnvironment / Schema-mismatch error paths without writing a
// real state.json. The production-default in newCommit() wraps
// state.Load / state.Save / state.GuardEnvironment verbatim.
type StateStore interface {
	// Load delegates to state.Load. Returns (nil, nil) on absent file
	// (fresh workspace); ErrSchemaMismatch on version drift;
	// ErrStateParse on decode failure.
	Load(path string) (*state.File, error)

	// Save delegates to state.Save (= state.WriteAtomic, STATE-07
	// four-step contract). Rejects nil File defensively.
	Save(path string, f *state.File) error

	// GuardEnvironment delegates to state.GuardEnvironment. Returns
	// nil for nil/empty/matching cases or when force is true;
	// otherwise wraps state.ErrEnvironmentGuard.
	GuardEnvironment(existing *state.File, requested string, force bool) error
}
