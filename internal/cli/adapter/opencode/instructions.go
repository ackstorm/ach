// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"path/filepath"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

// projectInstructionsEntry is the instructions entry OpenCode resolves to the
// project's instruction files.
const projectInstructionsEntry = ".opencode/ach/*.md"

// InstructionFiles writes the text under .opencode/ach/.
func (a *Adapter) InstructionFiles(name string, body []byte, _ bool) []adapter.FileWrite {
	return []adapter.FileWrite{{Path: ".opencode/ach/" + name + ".md", Content: body, Merge: adapter.MergeReplace}}
}

// InstructionLoader keeps one entry in the instructions array of
// opencode.json. A global entry must be absolute or start with ~/, and it
// follows XDG_CONFIG_HOME like the files do.
func (a *Adapter) InstructionLoader(global bool) *adapter.InstructionLoader {
	entry := projectInstructionsEntry
	if global {
		entry = "~/.config/opencode/ach/*.md"
		if root := adapter.GlobalRoot("opencode", ""); root != "" {
			entry = filepath.Join(root, "ach") + "/*.md"
		}
	}
	return &adapter.InstructionLoader{Dir: ".opencode/ach", Path: configJSONPath, ArrayKey: "instructions", Entry: entry}
}
