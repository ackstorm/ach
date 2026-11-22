// SPDX-License-Identifier: Apache-2.0

package claudecode

import "github.com/ackstorm/ach/internal/cli/adapter"

// InstructionFiles writes the text as a rules file; Claude Code loads every
// .claude/rules/*.md at launch.
func (a *Adapter) InstructionFiles(name string, body []byte, _ bool) []adapter.FileWrite {
	return []adapter.FileWrite{{Path: ".claude/rules/ach-" + name + ".md", Content: body, Merge: adapter.MergeReplace}}
}

// InstructionLoader is nil: Claude Code reads .claude/rules on its own.
func (a *Adapter) InstructionLoader(bool) *adapter.InstructionLoader { return nil }
