// SPDX-License-Identifier: Apache-2.0

package opencode

import "github.com/ackstorm/ach/internal/cli/adapter"

// InstructionFiles writes the text under .opencode/ach/.
func (a *Adapter) InstructionFiles(name string, body []byte, _ bool) []adapter.FileWrite {
	return []adapter.FileWrite{{Path: ".opencode/ach/" + name + ".md", Content: body, Merge: adapter.MergeReplace}}
}

// InstructionLoader keeps the glob of .opencode/ach/ in the instructions
// array of opencode.json.
func (a *Adapter) InstructionLoader(bool) *adapter.InstructionLoader {
	return &adapter.InstructionLoader{Dir: ".opencode/ach", Path: configJSONPath, ArrayKey: "instructions"}
}
