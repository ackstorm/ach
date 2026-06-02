// SPDX-License-Identifier: Apache-2.0

package hydrate

import "github.com/ackstorm/ach/internal/cli/state"

// BuildScopedEmpty constructs the "new" state.File that the uninstall
// command (D-25/D-26) feeds to Sync as its set-difference target.
//
// Sync computes the to-delete set as the set-difference of prev's
// Targets minus newFile's Targets: a Target present in newFile SURVIVES,
// a Target absent from newFile is pruned. So to express "remove bucket
// X, keep bucket Y", the returned File must RETAIN the rows of bucket Y
// verbatim and leave bucket X empty. The returned File is therefore the
// scope-filtered SURVIVOR set, not the removal set.
//
// Scope semantics mirror the hydrate orchestrator's context/runtime
// split seam (commit.go:636-637):
//
//   - includeContext := !onlyRuntime
//   - removeRuntime  := onlyRuntime || includeRuntime
//
// where context buckets are Prompts/Plugins/Artifacts and runtime is
// RuntimeFiles + Adapter.Files. A bucket being REMOVED is left empty in
// the returned File; a bucket being RETAINED is copied through verbatim.
// The three flag combinations therefore map to:
//
//   - includeRuntime=true (full teardown): every bucket removed →
//     all-empty File → Sync prunes everything.
//   - default (both false, context-only): context removed, runtime
//     retained → returned File carries RuntimeFiles + Adapter.Files.
//   - onlyRuntime=true: runtime removed, context retained → returned
//     File carries Prompts/Plugins/Artifacts.
//
// The returned File always carries SchemaVersion "2" and prev's
// Environment/Deployment so a subsequent state.Save round-trips a valid
// v2 document. The Adapter.ID is preserved whenever runtime survives
// (so the retained adapter section stays addressable). prev is never
// mutated — every retained slice is shallow-copied into a fresh File.
func BuildScopedEmpty(prev *state.File, includeRuntime, onlyRuntime bool) *state.File {
	out := &state.File{
		SchemaVersion: "2",
	}
	if prev == nil {
		return out
	}
	out.Environment = prev.Environment
	out.Deployment = prev.Deployment

	// includeContext: context buckets are RETAINED (only runtime is
	// being removed). removeRuntime: runtime buckets are REMOVED.
	includeContext := onlyRuntime
	removeRuntime := onlyRuntime || includeRuntime

	if includeContext {
		out.Prompts = copyEntries(prev.Prompts)
		out.Plugins = copyEntries(prev.Plugins)
		out.Artifacts = copyEntries(prev.Artifacts)
	}
	if !removeRuntime {
		out.RuntimeFiles = copyEntries(prev.RuntimeFiles)
		out.Adapter = state.AdapterSection{
			ID:    prev.Adapter.ID,
			Files: copyEntries(prev.Adapter.Files),
		}
	}

	return out
}

// copyEntries returns a shallow copy of the FileEntry slice so the
// returned File never aliases prev's backing arrays. A nil input yields
// nil (omitempty round-trips to an absent JSON key).
func copyEntries(in []state.FileEntry) []state.FileEntry {
	if in == nil {
		return nil
	}
	out := make([]state.FileEntry, len(in))
	copy(out, in)
	return out
}
