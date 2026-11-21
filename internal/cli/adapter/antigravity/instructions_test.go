// SPDX-License-Identifier: Apache-2.0

package antigravity

import "testing"

func TestInstructionFiles(t *testing.T) {
	a := &Adapter{}
	fws := a.InstructionFiles("base", []byte("# Rules\n"), false)
	if len(fws) != 1 || fws[0].Path != ".agents/rules/ach-base.md" {
		t.Fatalf("got %+v", fws)
	}
	if string(fws[0].Content) != "---\ntrigger: always_on\n---\n\n# Rules\n" {
		t.Fatalf("content %q", fws[0].Content)
	}
	if a.InstructionLoader(false) != nil {
		t.Fatal("antigravity loads rules on its own")
	}
}
