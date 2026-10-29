// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"testing"
)

// TestSplitFrontmatter covers the promoted fence-split helper (D-24): a
// well-formed document yields the raw frontmatter region + body + found=true;
// a document with no opening fence yields found=false and the whole input as
// body; CRLF line endings are handled.
func TestSplitFrontmatter(t *testing.T) {
	t.Run("well-formed LF", func(t *testing.T) {
		in := []byte("---\nname: foo\ntools: [a, b]\n---\nbody line 1\nbody line 2\n")
		fm, body, found := SplitFrontmatter(in)
		if !found {
			t.Fatalf("found=false on well-formed input")
		}
		if string(fm) != "name: foo\ntools: [a, b]\n" {
			t.Errorf("frontmatter = %q", fm)
		}
		if string(body) != "body line 1\nbody line 2\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("no opening fence", func(t *testing.T) {
		in := []byte("just a body, no frontmatter\n")
		fm, body, found := SplitFrontmatter(in)
		if found {
			t.Errorf("found=true on input without opening fence")
		}
		if fm != nil {
			t.Errorf("frontmatter = %q; want nil", fm)
		}
		if !bytes.Equal(body, in) {
			t.Errorf("body = %q; want whole input", body)
		}
	})

	t.Run("CRLF", func(t *testing.T) {
		in := []byte("---\r\nname: foo\r\n---\r\nbody\r\n")
		fm, body, found := SplitFrontmatter(in)
		if !found {
			t.Fatalf("found=false on CRLF input")
		}
		if string(fm) != "name: foo\r\n" {
			t.Errorf("frontmatter = %q", fm)
		}
		if string(body) != "body\r\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("opening fence but no closing fence", func(t *testing.T) {
		in := []byte("---\nname: foo\nno closing fence here\n")
		_, body, found := SplitFrontmatter(in)
		if found {
			t.Errorf("found=true without a closing fence")
		}
		if !bytes.Equal(body, in) {
			t.Errorf("body = %q; want whole input on malformed", body)
		}
	})
}

// TestEncodeFrontmatterDoc_Deterministic proves the net-new deterministic
// re-encoder (D-24): keys are sorted lexicographically, scalars/string-slices/
// string→bool maps are emitted stably, and repeated calls are byte-identical.
func TestEncodeFrontmatterDoc_Deterministic(t *testing.T) {
	fm := map[string]any{
		"name":        "foo",
		"description": "a thing",
		"enabled":     true,
		"count":       int(3),
		"aliases":     []string{"b", "a", "c"},
		"tools":       map[string]bool{"write": true, "read": true, "edit": true},
	}
	body := []byte("the body\n")

	out1, err := EncodeFrontmatterDoc(fm, body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out2, err := EncodeFrontmatterDoc(fm, body)
	if err != nil {
		t.Fatalf("encode (2nd): %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("non-deterministic output:\n1: %q\n2: %q", out1, out2)
	}

	s := string(out1)
	// Opens and closes with a fence; body trails.
	if !bytes.HasPrefix(out1, []byte("---\n")) {
		t.Errorf("output does not open with --- fence:\n%s", s)
	}
	if !bytes.HasSuffix(out1, []byte("the body\n")) {
		t.Errorf("output does not end with body:\n%s", s)
	}
	// Keys are sorted: aliases < count < description < enabled < name < tools.
	wantOrder := []string{"aliases:", "count:", "description:", "enabled:", "name:", "tools:"}
	prev := -1
	for _, k := range wantOrder {
		idx := bytes.Index(out1, []byte(k))
		if idx < 0 {
			t.Fatalf("key %q missing from output:\n%s", k, s)
		}
		if idx < prev {
			t.Errorf("key %q out of sorted order:\n%s", k, s)
		}
		prev = idx
	}
	// String-slice ordering preserved as given (a YAML flow/block list).
	// tools map keys are sorted: edit < read < write.
	toolsIdx := bytes.Index(out1, []byte("tools:"))
	editIdx := bytes.Index(out1[toolsIdx:], []byte("edit"))
	readIdx := bytes.Index(out1[toolsIdx:], []byte("read"))
	writeIdx := bytes.Index(out1[toolsIdx:], []byte("write"))
	if !(editIdx < readIdx && readIdx < writeIdx) {
		t.Errorf("tools keys not sorted edit<read<write:\n%s", s)
	}
}

// TestEncodeFrontmatterDoc_UnknownShapePassThrough proves CR-01: a value
// outside the optimized lifted grammar is re-marshalled verbatim through
// yaml.v3 rather than aborting the whole hydrate. Real Claude agent
// frontmatter routinely carries these shapes (a non-integral float, a list of
// non-strings, a nested string-valued map like `permissions: {bash: ask}`, and
// a nil-valued key), and opencode tolerates unknown keys (opencode.go:407), so
// each must pass through, not error.
func TestEncodeFrontmatterDoc_UnknownShapePassThrough(t *testing.T) {
	t.Run("non-integral float", func(t *testing.T) {
		out, err := EncodeFrontmatterDoc(map[string]any{"temperature": 0.7}, []byte("body"))
		if err != nil {
			t.Fatalf("expected pass-through, got error: %v", err)
		}
		if !bytes.Contains(out, []byte("temperature")) || !bytes.Contains(out, []byte("0.7")) {
			t.Errorf("float not passed through:\n%s", out)
		}
	})

	t.Run("string-valued nested map (permissions: {bash: ask})", func(t *testing.T) {
		fm := map[string]any{"permissions": map[string]any{"bash": "ask"}}
		out, err := EncodeFrontmatterDoc(fm, []byte("body"))
		if err != nil {
			t.Fatalf("expected pass-through, got error: %v", err)
		}
		if !bytes.Contains(out, []byte("permissions")) || !bytes.Contains(out, []byte("bash")) || !bytes.Contains(out, []byte("ask")) {
			t.Errorf("string-valued map not passed through:\n%s", out)
		}
	})

	t.Run("list of non-strings", func(t *testing.T) {
		out, err := EncodeFrontmatterDoc(map[string]any{"weird": []any{1, 2, 3}}, []byte("body"))
		if err != nil {
			t.Fatalf("expected pass-through, got error: %v", err)
		}
		if !bytes.Contains(out, []byte("weird")) {
			t.Errorf("non-string list not passed through:\n%s", out)
		}
	})

	t.Run("nil-valued key", func(t *testing.T) {
		out, err := EncodeFrontmatterDoc(map[string]any{"description": nil}, []byte("body"))
		if err != nil {
			t.Fatalf("expected pass-through, got error: %v", err)
		}
		if !bytes.Contains(out, []byte("description")) {
			t.Errorf("nil-valued key not passed through:\n%s", out)
		}
	})

	t.Run("output still re-splits with body intact", func(t *testing.T) {
		body := []byte("the body\n")
		out, err := EncodeFrontmatterDoc(map[string]any{"temperature": 0.7}, body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		_, gotBody, found := SplitFrontmatter(out)
		if !found {
			t.Fatalf("encoded output did not re-split")
		}
		if !bytes.Equal(gotBody, body) {
			t.Errorf("round-trip body = %q; want %q", gotBody, body)
		}
	})
}

// TestEncodeFrontmatterDoc_RoundTripWithSplit proves split→encode composes:
// the encoder's output re-splits to the same body.
func TestEncodeFrontmatterDoc_RoundTripWithSplit(t *testing.T) {
	fm := map[string]any{"name": "x", "tools": map[string]bool{"read": true}}
	body := []byte("hello world\n")
	out, err := EncodeFrontmatterDoc(fm, body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, gotBody, found := SplitFrontmatter(out)
	if !found {
		t.Fatalf("encoded output did not re-split")
	}
	if !bytes.Equal(gotBody, body) {
		t.Errorf("round-trip body = %q; want %q", gotBody, body)
	}
}
