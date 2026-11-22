// SPDX-License-Identifier: Apache-2.0

package cursor

import "github.com/ackstorm/ach/internal/cli/adapter"

// InstructionFiles writes an always-applied project rule. Cursor reads only
// .mdc files there, and user rules live in its Settings, not in a file.
func (a *Adapter) InstructionFiles(name string, body []byte, global bool) []adapter.FileWrite {
	if global {
		return nil
	}
	return []adapter.FileWrite{{
		Path:    ".cursor/rules/ach-" + name + ".mdc",
		Content: adapter.WithFrontmatter("alwaysApply: true", body),
		Merge:   adapter.MergeReplace,
	}}
}

// InstructionLoader is nil: Cursor reads .cursor/rules on its own.
func (a *Adapter) InstructionLoader(bool) *adapter.InstructionLoader { return nil }
