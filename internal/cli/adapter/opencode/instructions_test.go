// SPDX-License-Identifier: Apache-2.0

package opencode

import "testing"

func TestInstructionFiles(t *testing.T) {
	a := &Adapter{}
	fws := a.InstructionFiles("base", []byte("# Rules\n"), false)
	if len(fws) != 1 || fws[0].Path != ".opencode/ach/base.md" || string(fws[0].Content) != "# Rules\n" {
		t.Fatalf("got %+v", fws)
	}
	l := a.InstructionLoader(false)
	if l == nil || l.Dir != ".opencode/ach" || l.Path != ".opencode/opencode.json" ||
		l.ArrayKey != "instructions" || l.Entry != ".opencode/ach/*.md" {
		t.Fatalf("loader %+v", l)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := a.InstructionLoader(true).Entry; got != "~/.config/opencode/ach/*.md" {
		t.Fatalf("global entry %q", got)
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got := a.InstructionLoader(true).Entry; got != dir+"/opencode/ach/*.md" {
		t.Fatalf("global entry %q", got)
	}
}
