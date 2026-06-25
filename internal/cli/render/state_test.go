// SPDX-License-Identifier: Apache-2.0

package render_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/render"
)

// TestFormatStateList_Empty asserts the stable empty-state stub for
// nil/empty input (LIFE-03 empty/missing-state path).
func TestFormatStateList_Empty(t *testing.T) {
	if got := render.FormatStateList(nil); got != "No resources installed\n" {
		t.Fatalf("nil input: want %q, got %q", "No resources installed\n", got)
	}
	if got := render.FormatStateList([]render.StateEntryView{}); got != "No resources installed\n" {
		t.Fatalf("empty slice: want %q, got %q", "No resources installed\n", got)
	}
}

// TestFormatStateList_Table asserts the multi-row table carries the D-31
// header KIND / TARGET / ENVIRONMENT and one line per entry.
func TestFormatStateList_Table(t *testing.T) {
	entries := []render.StateEntryView{
		{Kind: "prompt", Target: ".claude/prompts/a.md", Environment: "prod"},
		{Kind: "plugin", Target: ".claude/plugins/p", Environment: "prod"},
		{Kind: "artifact", Target: ".ach/artifacts/x", Environment: "prod"},
	}
	out := render.FormatStateList(entries)

	// Header columns present (D-31).
	for _, col := range []string{"KIND", "TARGET", "ENVIRONMENT"} {
		if !strings.Contains(out, col) {
			t.Errorf("table missing header column %q; got:\n%s", col, out)
		}
	}

	// One row per entry: header + 3 data rows + trailing newline ⇒ 4 newlines.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(entries)+1 {
		t.Fatalf("want %d lines (header + %d rows), got %d:\n%s",
			len(entries)+1, len(entries), len(lines), out)
	}

	// Each entry's Kind + Target appear in the body.
	for _, e := range entries {
		if !strings.Contains(out, e.Kind) {
			t.Errorf("table missing kind %q", e.Kind)
		}
		if !strings.Contains(out, e.Target) {
			t.Errorf("table missing target %q", e.Target)
		}
	}
}

// TestFormatStateListJSON_Deterministic asserts the --json renderer is
// byte-identical across two calls with the same logical set, regardless
// of input order (sorted before marshalling).
func TestFormatStateListJSON_Deterministic(t *testing.T) {
	a := []render.StateEntryView{
		{Kind: "plugin", Target: "b", Environment: "prod"},
		{Kind: "prompt", Target: "a", Environment: "prod"},
	}
	// Same set, reversed order.
	b := []render.StateEntryView{
		{Kind: "prompt", Target: "a", Environment: "prod"},
		{Kind: "plugin", Target: "b", Environment: "prod"},
	}

	out1, err := render.FormatStateListJSON(a)
	if err != nil {
		t.Fatalf("FormatStateListJSON(a): %v", err)
	}
	out2, err := render.FormatStateListJSON(b)
	if err != nil {
		t.Fatalf("FormatStateListJSON(b): %v", err)
	}
	if out1 != out2 {
		t.Fatalf("JSON not deterministic across input orders:\n a=%q\n b=%q", out1, out2)
	}

	// Output decodes back to the full entry set.
	var decoded []render.StateEntryView
	if err := json.Unmarshal([]byte(out1), &decoded); err != nil {
		t.Fatalf("unmarshal JSON output: %v\n%s", err, out1)
	}
	if len(decoded) != len(a) {
		t.Fatalf("round-trip count: want %d, got %d", len(a), len(decoded))
	}
}

// TestFormatStateList_DedupesIdenticalRows asserts that rows identical across
// (Kind, Target, Environment) are collapsed to a single display row while
// preserving first-seen order and leaving unique rows untouched.
func TestFormatStateList_DedupesIdenticalRows(t *testing.T) {
	entries := []render.StateEntryView{
		{Kind: "plugin", Target: ".mcp.json", Environment: "demo"},
		{Kind: "plugin", Target: ".mcp.json", Environment: "demo"},
		{Kind: "plugin", Target: "CLAUDE.md", Environment: "demo"},
		{Kind: "plugin", Target: ".mcp.json", Environment: "demo"},
	}
	out := render.FormatStateList(entries)
	if got := strings.Count(out, ".mcp.json"); got != 1 {
		t.Errorf(".mcp.json appears %d times, want 1:\n%s", got, out)
	}
	if got := strings.Count(out, "CLAUDE.md"); got != 1 {
		t.Errorf("CLAUDE.md appears %d times, want 1:\n%s", got, out)
	}
}

// TestFormatStateListJSON_EmptyIsArray asserts nil input yields a stable
// empty JSON array (never "null").
func TestFormatStateListJSON_EmptyIsArray(t *testing.T) {
	out, err := render.FormatStateListJSON(nil)
	if err != nil {
		t.Fatalf("FormatStateListJSON(nil): %v", err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("nil input: want %q, got %q", "[]", strings.TrimSpace(out))
	}
}
