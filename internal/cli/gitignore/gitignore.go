// SPDX-License-Identifier: Apache-2.0

// Package gitignore maintains a marker-bounded block in a project's .gitignore
// listing the agent-config paths ach-cli writes (the adapter dirs it hydrates
// plus a project-root .mcp.json when it generates one, and ACH's own .ach/
// cache). Those files embed the forwarder bearer / LiteLLM key in plaintext —
// mode 0600 guards other local users, but only a .gitignore entry guards
// against an accidental `git add`/commit.
//
// The block coexists with any pre-existing .gitignore: every line outside the
// markers is preserved verbatim, and the block carries the sorted, deduped
// union of its prior entries and the new ones, so successive hydrate/install
// runs accumulate (a later `--target codex` adds .codex/ without dropping an
// earlier .claude/). Ensure is idempotent — it never rewrites a byte-identical
// file.
package gitignore

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/cli/state"
)

// The marker lines that bound the ach-cli managed block. They are matched by
// exact (trimmed) line equality, so they MUST stay byte-stable forever —
// changing them would orphan blocks written by older binaries.
const (
	beginMarker = "# BEGIN ach-cli — agent config with embedded credentials, do not commit"
	endMarker   = "# END ach-cli"
)

// TopLevelEntry maps a project-root-relative path ach-cli wrote to the single
// .gitignore pattern that covers it: the first path segment with a trailing
// slash for anything nested inside a directory (".claude/agents/x.md" ->
// ".claude/"), or the bare name for a root-level file (".mcp.json"). Returns ""
// for an empty or absolute path (nothing project-relative to ignore).
func TopLevelEntry(rel string) string {
	if rel == "" || filepath.IsAbs(rel) {
		return ""
	}
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" {
		return ""
	}
	parts := strings.SplitN(rel, "/", 2)
	if strings.HasSuffix(rel, "/") || (len(parts) == 2 && strings.TrimSpace(parts[1]) != "") {
		return parts[0] + "/"
	}
	return parts[0]
}

// Ensure merges entries into the ach-cli managed block of <dir>/.gitignore,
// creating the file when absent and preserving all content outside the markers.
// It reports whether it wrote (false on a no-op idempotent call). Entries are
// trimmed; empties are ignored. When the union of entries is empty, it is a
// no-op.
func Ensure(dir string, entries []string) (bool, error) {
	want := map[string]struct{}{}
	for _, e := range entries {
		if e = strings.TrimSpace(e); e != "" {
			want[e] = struct{}{}
		}
	}

	path := filepath.Join(dir, ".gitignore")
	orig, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	lines := splitLines(string(orig))
	before, inside, after, hadBlock := extractBlock(lines)

	// Fold any pre-existing block entries into the union so we accumulate.
	for _, e := range inside {
		if e = strings.TrimSpace(e); e != "" {
			want[e] = struct{}{}
		}
	}
	if len(want) == 0 {
		return false, nil
	}
	merged := make([]string, 0, len(want))
	for e := range want {
		merged = append(merged, e)
	}
	sort.Strings(merged)

	var out []string
	if hadBlock {
		out = append(out, before...)
		out = append(out, beginMarker)
		out = append(out, merged...)
		out = append(out, endMarker)
		out = append(out, after...)
	} else {
		out = append(out, lines...)
		// Trim trailing blank lines so we control the single-blank separator.
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, beginMarker)
		out = append(out, merged...)
		out = append(out, endMarker)
	}

	next := strings.Join(out, "\n") + "\n"
	if next == string(orig) {
		return false, nil
	}
	if err := state.WriteAtomic(path, []byte(next), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// splitLines splits s into lines, dropping exactly one trailing newline so a
// normal "a\nb\n" yields ["a","b"] (not a trailing ""). An empty string yields
// a nil slice.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// extractBlock splits lines around the FIRST complete ach-managed marker block,
// returning the lines before the begin marker, the interior lines, the lines
// after the end marker, and whether a complete block was found. A begin marker
// with no matching end marker is treated as no block (defensive — the rebuild
// then appends a fresh block rather than eating the rest of the file).
func extractBlock(lines []string) (before, inside, after []string, ok bool) {
	begin := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == beginMarker {
			begin = i
			break
		}
	}
	if begin == -1 {
		return lines, nil, nil, false
	}
	for i := begin + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == endMarker {
			return lines[:begin], lines[begin+1 : i], lines[i+1:], true
		}
	}
	return lines, nil, nil, false
}
