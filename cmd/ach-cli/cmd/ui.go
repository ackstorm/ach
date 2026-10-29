// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/ackstorm/ach/internal/cli/hydrate"
)

// ui renders the cargo-framed, git-itemised output every workspace verb shares:
// a right-aligned 12-col verb column, +/~/-/! item lines, and a Finished line.
// Color only on a terminal and only without NO_COLOR.
type ui struct {
	w       io.Writer
	color   bool
	verbose bool
}

// maxItems is how many item lines print before collapsing (--verbose shows all).
const maxItems = 8

func newUI(w io.Writer, verbose bool) *ui {
	f, ok := w.(*os.File)
	return &ui{w: w, verbose: verbose,
		color: ok && term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == ""}
}

const (
	green  = "32"
	yellow = "33"
	red    = "31"
	dim    = "2"
)

var verbColor = map[string]string{
	"Cloning": green, "Hydrating": green, "Removing": green, "Finished": green,
	"error": red, "Warning": yellow, "hint": dim, "Note": dim, "Notice": dim,
}

var opColor = map[hydrate.Op]string{hydrate.OpAdd: green, hydrate.OpUpdate: yellow, hydrate.OpRemove: red, '!': red}

func (u *ui) paint(code, s string) string {
	if !u.color || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *ui) verb(v, format string, args ...any) {
	pad := fmt.Sprintf("%12s", v)
	_, _ = fmt.Fprintf(u.w, "%s %s\n", u.paint(verbColor[v], pad), fmt.Sprintf(format, args...))
}

// items prints one line per change; label fills the verb column of the first
// line only (a target or environment name in multi-row views).
func (u *ui) items(label string, cs []hydrate.ItemChange) {
	for i, c := range cs {
		if i == maxItems && !u.verbose {
			u.verb("", "… %d more (--verbose)", len(cs)-maxItems)
			return
		}
		if i > 0 {
			label = ""
		}
		line := strings.TrimRight(fmt.Sprintf("%s %-7s %-16s %s",
			u.paint(opColor[c.Op], string(rune(c.Op))), c.Kind, c.Name, filesNote(c.Files)), " ")
		u.verb(label, "%s", line)
	}
}

// targetItems prints each target's changes under its name, or one unlabelled
// list when every target changed the same items.
func (u *ui) targetItems(results []hydrate.Result, none string) {
	if len(results) == 0 {
		return
	}
	if sameChanges(results) {
		u.items("", results[0].Changes)
		return
	}
	for _, r := range results {
		if len(r.Changes) == 0 {
			u.verb(r.PlatformID, "%s", none)
			continue
		}
		u.items(r.PlatformID, r.Changes)
	}
}

// relay re-emits the engine's buffered stderr after the Finished line (build
// u on stderr: these stay off stdout), mapping its "note:"/"notice:"/
// "warning:" lines onto the verb column. A repeated note prints once (one per
// target otherwise, e.g. the .gitignore note); other lines pass verbatim.
func (u *ui) relay(buf *bytes.Buffer) {
	seen := map[string]bool{}
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		line := sc.Text()
		prefixed := strings.HasPrefix(line, "note: ") || strings.HasPrefix(line, "notice: ") ||
			strings.HasPrefix(line, "warning: ")
		if prefixed && seen[line] {
			continue
		}
		seen[line] = true
		switch {
		case strings.HasPrefix(line, "note: "), strings.HasPrefix(line, "notice: "):
			u.verb("Note", "%s", line[strings.IndexByte(line, ' ')+1:])
		case strings.HasPrefix(line, "warning: "):
			u.verb("Warning", "%s", strings.TrimPrefix(line, "warning: "))
		default:
			_, _ = fmt.Fprintln(u.w, line)
		}
	}
}

func elapsed(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

func filesNote(n int) string {
	if n <= 1 {
		return ""
	}
	return fmt.Sprintf("%d files", n)
}
