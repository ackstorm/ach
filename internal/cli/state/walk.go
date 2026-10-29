// SPDX-License-Identifier: Apache-2.0

package state

// WalkEntries flattens every FileEntry across all projection buckets on
// a File (Prompts → Plugins → Artifacts → Skills → RuntimeFiles →
// Adapter.Files) into a single slice of value copies. The deterministic order keeps
// behavior consistent across the Phase 7 surface; callers must not rely
// on the order for semantic decisions.
//
// A nil File yields nil. A non-nil File with no entries yields a non-nil
// empty slice (the make-then-append discipline) — preserving the prior
// hydrate-package flattener's exact return shape.
func WalkEntries(f *File) []FileEntry {
	if f == nil {
		return nil
	}
	total := len(f.Prompts) + len(f.Plugins) + len(f.Artifacts) + len(f.Skills) +
		len(f.RuntimeFiles) + len(f.Adapter.Files)
	out := make([]FileEntry, 0, total)
	out = append(out, f.Prompts...)
	out = append(out, f.Plugins...)
	out = append(out, f.Artifacts...)
	out = append(out, f.Skills...)
	out = append(out, f.RuntimeFiles...)
	out = append(out, f.Adapter.Files...)
	return out
}
