// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"strings"
	"testing"
)

// TestCanonicalJSON_SortedKeys: map keys are emitted in sorted order and
// the output is byte-identical across repeated calls (FMT-05 idempotence).
func TestCanonicalJSON_SortedKeys(t *testing.T) {
	in := map[string]any{"b": 1, "a": 2, "c": 3}

	out1, err := CanonicalJSON(in)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	out2, err := CanonicalJSON(in)
	if err != nil {
		t.Fatalf("CanonicalJSON (2nd): %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("not byte-identical across calls:\n %q\n %q", out1, out2)
	}

	s := string(out1)
	ai := strings.Index(s, `"a"`)
	bi := strings.Index(s, `"b"`)
	ci := strings.Index(s, `"c"`)
	if !(ai >= 0 && ai < bi && bi < ci) {
		t.Errorf("keys not in sorted order a<b<c: a=%d b=%d c=%d in %q", ai, bi, ci, s)
	}
}

// TestCanonicalJSON_NoHTMLEscape: <, &, > appear literally (SetEscapeHTML(false)).
func TestCanonicalJSON_NoHTMLEscape(t *testing.T) {
	in := map[string]any{"k": "a<b>c&d"}
	out, err := CanonicalJSON(in)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "a<b>c&d") {
		t.Errorf("expected literal HTML chars, got %q", s)
	}
	// With SetEscapeHTML(false) the \uXXXX-escaped forms must be absent
	// (the literal <, >, & runes are present instead). These are the
	// 6-character escape sequences encoding/json emits when escaping IS
	// on — backslash-u-zero-zero-... — not the runes themselves.
	for _, esc := range []string{"\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(s, esc) {
			t.Errorf("HTML char was escaped (%s present), got %q", esc, s)
		}
	}
}

// TestCanonicalJSON_TwoSpaceIndent: output uses 2-space indent.
func TestCanonicalJSON_TwoSpaceIndent(t *testing.T) {
	in := map[string]any{"a": 1}
	out, err := CanonicalJSON(in)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !strings.Contains(string(out), "\n  \"a\"") {
		t.Errorf("expected 2-space indented key, got %q", string(out))
	}
}

// TestCanonicalTOML_Deterministic: TOML round-trips and is byte-identical
// across two calls on the same input.
func TestCanonicalTOML_Deterministic(t *testing.T) {
	type server struct {
		Command string `toml:"command"`
		Timeout int    `toml:"startup_timeout_sec"`
	}
	in := struct {
		McpServers map[string]server `toml:"mcp_servers"`
	}{
		McpServers: map[string]server{
			"alpha": {Command: "run-alpha", Timeout: 30},
			"beta":  {Command: "run-beta", Timeout: 60},
		},
	}

	out1, err := CanonicalTOML(in)
	if err != nil {
		t.Fatalf("CanonicalTOML: %v", err)
	}
	out2, err := CanonicalTOML(in)
	if err != nil {
		t.Fatalf("CanonicalTOML (2nd): %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("TOML not byte-identical across calls:\n %q\n %q", out1, out2)
	}
	if len(out1) == 0 {
		t.Errorf("expected non-empty TOML output")
	}
}
