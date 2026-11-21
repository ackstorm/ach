// SPDX-License-Identifier: Apache-2.0

package cursor

import "testing"

func TestInstructionFiles(t *testing.T) {
	a := &Adapter{}
	fws := a.InstructionFiles("base", []byte("# Rules\n"), false)
	if len(fws) != 1 || fws[0].Path != ".cursor/rules/ach-base.mdc" {
		t.Fatalf("got %+v", fws)
	}
	if string(fws[0].Content) != "---\nalwaysApply: true\n---\n\n# Rules\n" {
		t.Fatalf("content %q", fws[0].Content)
	}
	if got := a.InstructionFiles("base", []byte("x"), true); got != nil {
		t.Fatalf("cursor has no global rules file; got %+v", got)
	}
	if a.InstructionLoader(false) != nil || a.InstructionLoader(true) != nil {
		t.Fatal("cursor loads rules on its own")
	}
}
