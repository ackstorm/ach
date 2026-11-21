// SPDX-License-Identifier: Apache-2.0

package merge_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/merge"
)

// --- DeepMergeInto ---------------------------------------------------------

// TestDeepMergeInto_PreservesSiblingKeys asserts that a src map whose keys
// are disjoint from dst's pre-existing keys leaves those keys untouched.
func TestDeepMergeInto_PreservesSiblingKeys(t *testing.T) {
	dst := map[string]any{
		"existing": "user-value",
		"nested": map[string]any{
			"user-key": "preserved",
		},
	}
	src := map[string]any{
		"new-key": "from-ach",
		"nested": map[string]any{
			"ach-key": "from-ach",
		},
	}
	merge.DeepMergeInto(dst, src)

	if dst["existing"] != "user-value" {
		t.Errorf("existing key clobbered; got %v", dst["existing"])
	}
	nested, ok := dst["nested"].(map[string]any)
	if !ok {
		t.Fatal("nested key not a map after merge")
	}
	if nested["user-key"] != "preserved" {
		t.Errorf("nested user key clobbered; got %v", nested["user-key"])
	}
	if nested["ach-key"] != "from-ach" {
		t.Errorf("nested ach key not merged; got %v", nested["ach-key"])
	}
	if dst["new-key"] != "from-ach" {
		t.Errorf("new key not inserted; got %v", dst["new-key"])
	}
}

// TestDeepMergeInto_OverlappingLeaves asserts that src's scalar values
// overwrite dst's scalars at the same key (not nested).
func TestDeepMergeInto_OverlappingLeaves(t *testing.T) {
	dst := map[string]any{"k": "old"}
	src := map[string]any{"k": "new"}
	merge.DeepMergeInto(dst, src)
	if dst["k"] != "new" {
		t.Errorf("leaf not overwritten; got %v", dst["k"])
	}
}

// --- ParseDoc / EncodeDoc --------------------------------------------------

func TestParseDoc_EmptyBodyYieldsEmptyMap(t *testing.T) {
	m, err := merge.ParseDoc([]byte("  \n\t  "), false)
	if err != nil {
		t.Fatalf("ParseDoc empty: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("empty body: want empty map, got %v", m)
	}
}

func TestParseEncodeDoc_JSONRoundTrip(t *testing.T) {
	input := []byte(`{"mcpServers":{"foo":{"url":"http://foo"}},"x":1}`)
	m, err := merge.ParseDoc(input, false)
	if err != nil {
		t.Fatalf("ParseDoc JSON: %v", err)
	}
	out, err := merge.EncodeDoc(m, false)
	if err != nil {
		t.Fatalf("EncodeDoc JSON: %v", err)
	}
	// Re-parse to compare semantically (key order may differ).
	var got, want map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode re-encoded: %v", err)
	}
	if err := json.Unmarshal(input, &want); err != nil {
		t.Fatalf("decode original: %v", err)
	}
	if got["x"] != want["x"] {
		t.Errorf("x: got %v want %v", got["x"], want["x"])
	}
}

func TestParseEncodeDoc_TOMLRoundTrip(t *testing.T) {
	tomlInput := []byte("[mcp_servers]\n[mcp_servers.foo]\nurl = \"http://foo\"\n")
	m, err := merge.ParseDoc(tomlInput, true)
	if err != nil {
		t.Fatalf("ParseDoc TOML: %v", err)
	}
	out, err := merge.EncodeDoc(m, true)
	if err != nil {
		t.Fatalf("EncodeDoc TOML: %v", err)
	}
	// Re-parse to verify round-trip.
	m2, err := merge.ParseDoc(out, true)
	if err != nil {
		t.Fatalf("ParseDoc re-encoded TOML: %v", err)
	}
	ms, ok := m2["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers missing after TOML round-trip; got %v", m2)
	}
	if _, hasFoo := ms["foo"]; !hasFoo {
		t.Errorf("mcp_servers.foo missing; mcp_servers=%v", ms)
	}
}

// --- GetDottedKey / SetDottedKey ------------------------------------------

func TestGetSetDottedKey_RoundTrip(t *testing.T) {
	root := map[string]any{}
	merge.SetDottedKey(root, "a.b.c", "leaf-value")

	got, ok := merge.GetDottedKey(root, "a.b.c")
	if !ok {
		t.Fatal("GetDottedKey: key not found after SetDottedKey")
	}
	if got != "leaf-value" {
		t.Errorf("GetDottedKey: got %v; want leaf-value", got)
	}
}

func TestGetDottedKey_MissingReturnsNotFound(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": "val"}}
	_, ok := merge.GetDottedKey(root, "a.b.c.d")
	if ok {
		t.Error("GetDottedKey: want not-found for missing deep key")
	}
}

func TestSetDottedKey_CreatesIntermediateMaps(t *testing.T) {
	root := map[string]any{}
	merge.SetDottedKey(root, "x.y.z", 42)
	xy, ok := root["x"].(map[string]any)
	if !ok {
		t.Fatal("x not a map")
	}
	xyz, ok := xy["y"].(map[string]any)
	if !ok {
		t.Fatal("x.y not a map")
	}
	if xyz["z"] != 42 {
		t.Errorf("x.y.z: got %v; want 42", xyz["z"])
	}
}

// --- RemoveDottedKey -------------------------------------------------------

func TestRemoveDottedKey_RemovesLeaf(t *testing.T) {
	root := map[string]any{
		"mcpServers": map[string]any{
			"foo": map[string]any{"url": "http://foo"},
			"bar": map[string]any{"url": "http://bar"},
		},
	}
	merge.RemoveDottedKey(root, "mcpServers.foo")
	ms, _ := root["mcpServers"].(map[string]any)
	if _, hasFoo := ms["foo"]; hasFoo {
		t.Errorf("RemoveDottedKey: foo not removed")
	}
	if _, hasBar := ms["bar"]; !hasBar {
		t.Errorf("RemoveDottedKey: bar unexpectedly removed")
	}
}

func TestRemoveDottedKey_Idempotent(t *testing.T) {
	root := map[string]any{"a": "v"}
	merge.RemoveDottedKey(root, "missing.path")
	if _, ok := root["a"]; !ok {
		t.Errorf("RemoveDottedKey: removed unrelated key a")
	}
}

// --- ExtractByKeys ---------------------------------------------------------

func TestExtractByKeys_OnlyContributedKeysLifted(t *testing.T) {
	src := map[string]any{
		"mcpServers": map[string]any{
			"ach-srv":  "ach-value",
			"user-srv": "user-value",
		},
		"other": "other-value",
	}
	out, found := merge.ExtractByKeys(src, []string{"mcpServers.ach-srv"})
	if !found {
		t.Fatal("ExtractByKeys: key not found")
	}
	ms, ok := out["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("ExtractByKeys: mcpServers not a map; got %v", out)
	}
	if _, hasAch := ms["ach-srv"]; !hasAch {
		t.Errorf("ExtractByKeys: ach-srv missing")
	}
	if _, hasUser := ms["user-srv"]; hasUser {
		t.Errorf("ExtractByKeys: user-srv should NOT be in extracted map")
	}
	if _, hasOther := out["other"]; hasOther {
		t.Errorf("ExtractByKeys: other should NOT be in extracted map")
	}
}

func TestExtractByKeys_NoneFound(t *testing.T) {
	src := map[string]any{"x": "v"}
	_, found := merge.ExtractByKeys(src, []string{"missing"})
	if found {
		t.Error("ExtractByKeys: want found=false for missing key")
	}
}

// --- MergeForward ----------------------------------------------------------

func TestMergeForward_JSONMergesExistingFile(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "settings.json")

	// Seed with a user key.
	seed := `{"user":"mine","mcpServers":{"user-srv":{"url":"http://user"}}}`
	if err := os.WriteFile(abs, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ours := []byte(`{"mcpServers":{"ach-srv":{"url":"http://ach"}}}`)
	got, err := merge.MergeForward(abs, ours, 0o600)
	if err != nil {
		t.Fatalf("MergeForward: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if m["user"] != "mine" {
		t.Errorf("user key clobbered")
	}
	ms, _ := m["mcpServers"].(map[string]any)
	if _, ok := ms["user-srv"]; !ok {
		t.Errorf("user-srv clobbered")
	}
	if _, ok := ms["ach-srv"]; !ok {
		t.Errorf("ach-srv not merged in")
	}

	// Mode should be 0o600.
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o; want 0o600", info.Mode().Perm())
	}
}

func TestMergeForward_AbsentFile_WritesOurs(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "settings.json")
	ours := []byte(`{"k":"v"}`)
	got, err := merge.MergeForward(abs, ours, 0o644)
	if err != nil {
		t.Fatalf("MergeForward absent: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["k"] != "v" {
		t.Errorf("k: got %v; want v", m["k"])
	}
}

// --- MergeDoc --------------------------------------------------------------

func TestMergeDoc_TOML_MergesExistingFile(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "config.toml")

	seed := "[mcp_servers]\n[mcp_servers.user]\nurl = \"http://user\"\n"
	if err := os.WriteFile(abs, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ours := []byte("[mcp_servers]\n[mcp_servers.ach]\nurl = \"http://ach\"\n")
	got, err := merge.MergeDoc(abs, ours, 0o600, true)
	if err != nil {
		t.Fatalf("MergeDoc TOML: %v", err)
	}

	m, err := merge.ParseDoc(got, true)
	if err != nil {
		t.Fatalf("ParseDoc result: %v", err)
	}
	ms, _ := m["mcp_servers"].(map[string]any)
	if _, ok := ms["user"]; !ok {
		t.Errorf("user server clobbered")
	}
	if _, ok := ms["ach"]; !ok {
		t.Errorf("ach server not merged in")
	}
}

func TestPruneDottedKeys(t *testing.T) {
	doc := map[string]any{"a": map[string]any{"b": 1}, "keep": true}
	out, empty, err := merge.PruneDottedKeys(doc, []string{"a.b"}, false)
	if err != nil || empty {
		t.Fatalf("prune: out=%q empty=%v err=%v", out, empty, err)
	}
	if !strings.Contains(string(out), "keep") {
		t.Errorf("kept key missing from re-encode: %q", out)
	}

	// Parents emptied by the removal go too; a parent with other content stays.
	doc1 := map[string]any{"mcp_servers": map[string]any{"x": 1}, "keep": true}
	if out, _, _ := merge.PruneDottedKeys(doc1, []string{"mcp_servers.x"}, true); strings.Contains(string(out), "mcp_servers") {
		t.Errorf("emptied parent table left behind: %q", out)
	}
	doc3 := map[string]any{"mcp_servers": map[string]any{"x": 1, "user": 2}}
	if out, _, _ := merge.PruneDottedKeys(doc3, []string{"mcp_servers.x"}, true); !strings.Contains(string(out), "user") {
		t.Errorf("non-empty parent dropped: %q", out)
	}

	doc2 := map[string]any{"only": 1}
	_, empty, err = merge.PruneDottedKeys(doc2, []string{"only"}, false)
	if err != nil || !empty {
		t.Errorf("full prune: empty=%v err=%v, want empty=true", empty, err)
	}
}

func TestParseDoc_JSONNull(t *testing.T) {
	doc, err := merge.ParseDoc([]byte("null"), false)
	if err != nil || doc == nil {
		t.Fatalf("doc=%v err=%v", doc, err)
	}
	if _, err := merge.EnsureArrayEntry(doc, "instructions", "x"); err != nil {
		t.Fatal(err)
	}
}
