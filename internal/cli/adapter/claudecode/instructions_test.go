// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

func TestInstructionFiles(t *testing.T) {
	a := &Adapter{}
	for _, global := range []bool{false, true} {
		fws := a.InstructionFiles("base", []byte("# Rules\n"), global)
		if len(fws) != 1 || fws[0].Path != ".claude/rules/ach-base.md" || string(fws[0].Content) != "# Rules\n" || fws[0].Merge != adapter.MergeReplace {
			t.Fatalf("global=%v got %+v", global, fws)
		}
		if a.InstructionLoader(global) != nil {
			t.Fatal("claude-code loads rules on its own")
		}
	}
}
