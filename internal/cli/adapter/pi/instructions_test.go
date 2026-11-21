// SPDX-License-Identifier: Apache-2.0

package pi

import "testing"

func TestInstructionFiles(t *testing.T) {
	a := &Adapter{}
	fws := a.InstructionFiles("base", []byte("# Rules\n"), false)
	if len(fws) != 1 || fws[0].Path != ".pi/ach/base.md" || string(fws[0].Content) != "# Rules\n" {
		t.Fatalf("got %+v", fws)
	}
	l := a.InstructionLoader(false)
	if l == nil || l.Dir != ".pi/ach" || l.Path != ".pi/extensions/ach-instructions.ts" ||
		l.ArrayKey != "" || string(l.Content) != instructionExtension {
		t.Fatalf("loader %+v", l)
	}
}
