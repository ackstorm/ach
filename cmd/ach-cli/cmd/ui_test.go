// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/hydrate"
)

func TestUI_ItemsCollapse(t *testing.T) {
	var b bytes.Buffer
	u := newUI(&b, false)
	var cs []hydrate.ItemChange
	for i := 0; i < 10; i++ {
		cs = append(cs, hydrate.ItemChange{Op: hydrate.OpAdd, Kind: "plugin", Name: fmt.Sprintf("p%d", i), Files: 3})
	}
	u.items("", cs)
	got := b.String()
	if !strings.HasPrefix(got, "             + plugin  p0               3 files\n") {
		t.Fatalf("first line wrong:\n%q", got)
	}
	if !strings.Contains(got, "… 2 more (--verbose)") || strings.Count(got, "\n") != 9 {
		t.Fatalf("collapse wrong:\n%s", got)
	}
}

func TestUI_RelayRewritesEngineNotes(t *testing.T) {
	var b bytes.Buffer
	u := newUI(&b, false)
	u.relay(bytes.NewBufferString("note: claude-code: x\nnotice: z\nwarning: y\nplain\n"))
	want := "        Note claude-code: x\n        Note z\n     Warning y\nplain\n"
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}

func TestUI_Verb(t *testing.T) {
	var b bytes.Buffer
	newUI(&b, false).verb("Finished", "3 changed, 50 unchanged in %s", elapsed(1400*time.Millisecond))
	if b.String() != "    Finished 3 changed, 50 unchanged in 1.4s\n" {
		t.Fatalf("%q", b.String())
	}
}
