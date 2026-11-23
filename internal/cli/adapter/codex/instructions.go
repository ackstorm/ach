// SPDX-License-Identifier: Apache-2.0

package codex

import "github.com/ackstorm/ach/internal/cli/adapter"

// hookContextLimit is the token cap on the hook's output. Codex truncates
// above 2,500 by default; this fits four Instructions of the 23,900-byte
// maximum. The value is part of the hook's approval hash, so changing it
// makes every user approve the hook again.
const hookContextLimit = 25000

// InstructionFiles writes the text under .codex/ach/.
func (a *Adapter) InstructionFiles(name string, body []byte, _ bool) []adapter.FileWrite {
	return []adapter.FileWrite{{Path: ".codex/ach/" + name + ".md", Content: body, Merge: adapter.MergeReplace}}
}

// InstructionLoader is a SessionStart hook in .codex/hooks.json printing
// .codex/ach/*.md: Codex has no setting that lists extra instruction files.
func (a *Adapter) InstructionLoader(bool) *adapter.InstructionLoader {
	return &adapter.InstructionLoader{
		Dir:              ".codex/ach",
		Path:             ".codex/hooks.json",
		HookEvent:        "SessionStart",
		HookContextLimit: hookContextLimit,
	}
}
