// SPDX-License-Identifier: Apache-2.0

package antigravity

import "github.com/ackstorm/ach/internal/cli/adapter"

// InstructionFiles writes an always-on rule. Antigravity silently discards a
// .agents/rules file without a valid trigger. --global lands in
// ~/.gemini/config/rules through the .agents/ remap.
func (a *Adapter) InstructionFiles(name string, body []byte, _ bool) []adapter.FileWrite {
	return []adapter.FileWrite{{
		Path:    ".agents/rules/ach-" + name + ".md",
		Content: adapter.WithFrontmatter("trigger: always_on", body),
		Merge:   adapter.MergeReplace,
	}}
}

// InstructionLoader is nil: Antigravity reads .agents/rules on its own.
func (a *Adapter) InstructionLoader(bool) *adapter.InstructionLoader { return nil }
