// SPDX-License-Identifier: Apache-2.0

package adapter

// InstructionTarget is implemented by adapters whose tool loads an
// Environment Instruction from a file ach-cli owns.
type InstructionTarget interface {
	// InstructionFiles returns the writes for one Instruction.
	InstructionFiles(name string, body []byte, global bool) []FileWrite
	// InstructionLoader returns what the tool needs to read the files, or nil
	// when it reads them on its own.
	InstructionLoader(global bool) *InstructionLoader
}

// InstructionLoader is shared by every Environment in a folder and is not
// recorded in state: it exists exactly while Dir holds at least one *.md.
// With ArrayKey set, the absolute glob <Dir>/*.md is kept in the top-level
// JSON array ArrayKey of Path. With HookEvent set, Path is a hooks.json whose
// hooks.<HookEvent> list holds one command printing <Dir>/*.md, with an
// additionalContextLimit of HookContextLimit tokens. Otherwise Path is a
// whole file holding Content.
type InstructionLoader struct {
	Dir              string
	Path             string
	ArrayKey         string
	HookEvent        string
	HookContextLimit int
	Content          []byte
}

// WithFrontmatter prefixes body with a YAML frontmatter block holding header.
func WithFrontmatter(header string, body []byte) []byte {
	return append([]byte("---\n"+header+"\n---\n\n"), body...)
}
