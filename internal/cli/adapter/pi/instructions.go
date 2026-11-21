// SPDX-License-Identifier: Apache-2.0

package pi

import "github.com/ackstorm/ach/internal/cli/adapter"

// instructionExtension appends every ../ach/*.md to Pi's system prompt before
// each agent run. Pi's only file slot, APPEND_SYSTEM.md, is single and may be
// the user's own.
const instructionExtension = `import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = join(dirname(fileURLToPath(import.meta.url)), "..", "ach");

export default function (pi) {
  pi.on("before_agent_start", async (event) => {
    let names;
    try {
      names = readdirSync(dir).filter((n) => n.endsWith(".md")).sort();
    } catch {
      return;
    }
    const text = names.map((n) => readFileSync(join(dir, n), "utf8")).join("\n\n");
    if (text && !event.systemPrompt.includes(text)) {
      return { systemPrompt: event.systemPrompt + "\n\n" + text };
    }
  });
}
`

// InstructionFiles writes the text under .pi/ach/.
func (a *Adapter) InstructionFiles(name string, body []byte, _ bool) []adapter.FileWrite {
	return []adapter.FileWrite{{Path: ".pi/ach/" + name + ".md", Content: body, Merge: adapter.MergeReplace}}
}

// InstructionLoader is the extension that reads .pi/ach/.
func (a *Adapter) InstructionLoader(bool) *adapter.InstructionLoader {
	return &adapter.InstructionLoader{
		Dir:     ".pi/ach",
		Path:    ".pi/extensions/ach-instructions.ts",
		Content: []byte(instructionExtension),
	}
}
