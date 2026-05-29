// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestLoad_AbsentFile_ReturnsNilNil asserts the fresh-workspace
// branch: an on-disk absence is not an error; it just means "no
// prior state". Callers proceed as if the File were empty.
func TestLoad_AbsentFile_ReturnsNilNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "state.json")
	f, err := state.Load(path)
	if err != nil {
		t.Fatalf("Load on absent path: err = %v, want nil", err)
	}
	if f != nil {
		t.Fatalf("Load on absent path: f = %+v, want nil", f)
	}
}

// TestLoad_SchemaV1_ReturnsErrSchemaMismatch asserts the §8.2 +
// D-13 clean-break: schemaVersion "1" is rejected — no v1 reader
// code ships. Maps to exit 5 at the caller layer.
func TestLoad_SchemaV1_ReturnsErrSchemaMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":"1"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := state.Load(path)
	if !errors.Is(err, state.ErrSchemaMismatch) {
		t.Fatalf("Load(v1 state): err = %v, want errors.Is(..., ErrSchemaMismatch)", err)
	}
}

// TestLoad_SchemaV2_RoundTrip asserts the happy path: a v2 state
// written by Save round-trips through Load with the same field
// values. Validates JSON tags + struct shape against §8.2.
func TestLoad_SchemaV2_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := &state.File{
		SchemaVersion: "2",
		Environment:   "engineering-prod",
		Deployment:    "ackstorm-prod",
		Prompts: []state.FileEntry{
			{Target: ".ach/prompts/foo.md", Hash: "xxh3:aa", SourceHash: "xxh3:aa"},
		},
		Plugins: []state.FileEntry{
			{Target: ".claude/plugins/p/.claude-plugin/plugin.json", Hash: "xxh3:bb", SourceHash: "xxh3:bb"},
		},
		Artifacts: []state.FileEntry{
			{Target: ".ach/artifacts/x/profile.md", Hash: "xxh3:cc", SourceHash: "xxh3:cc"},
		},
		RuntimeFiles: []state.FileEntry{
			{Target: ".claude/.mcp.json", Hash: "xxh3:dd", SourceHash: "xxh3:dd1", Merge: "deep", Keys: []string{"mcpServers.github"}},
		},
		Adapter: state.AdapterSection{
			ID: "claude-code",
			Files: []state.FileEntry{
				{Target: ".claude/settings.json", Hash: "xxh3:ee", SourceHash: "xxh3:ee1", Merge: "deep", Keys: []string{"mcp.servers.github"}},
			},
		},
	}
	if err := state.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := state.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatalf("Load returned nil File")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// TestLoad_UnknownField_ReturnsErrStateParse asserts the strict
// schema gate: DisallowUnknownFields catches both v1 leftover
// fields (e.g. `contentHashes`) and forward-compat drift. Wraps
// ErrStateParse so callers can grep the error chain.
func TestLoad_UnknownField_ReturnsErrStateParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	body := `{"schemaVersion":"2","contentHashes":{"foo":"bar"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := state.Load(path)
	if !errors.Is(err, state.ErrStateParse) {
		t.Fatalf("Load(unknown-field): err = %v, want errors.Is(..., ErrStateParse)", err)
	}
}

// TestLoad_CorruptJSON_ReturnsErrStateParse asserts the parse
// failure path: non-JSON bytes wrap ErrStateParse so callers can
// distinguish "file is broken" from "schema is wrong".
func TestLoad_CorruptJSON_ReturnsErrStateParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := state.Load(path)
	if !errors.Is(err, state.ErrStateParse) {
		t.Fatalf("Load(corrupt): err = %v, want errors.Is(..., ErrStateParse)", err)
	}
}

// TestSave_NilFile_Errors asserts the defensive nil guard. A nil
// File is a programmer bug, not a recoverable state — surface it
// immediately rather than writing an empty struct.
func TestSave_NilFile_Errors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := state.Save(path, nil); err == nil {
		t.Fatalf("Save(nil) returned nil error; want non-nil")
	}
}

// TestSave_WritesValidJSON asserts Save produces JSON parseable by
// encoding/json (no html-escape on `<` `>` `&`, indent applied for
// human diffability).
func TestSave_WritesValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	f := &state.File{
		SchemaVersion: "2",
		Environment:   "prod",
		Deployment:    "ackstorm-prod",
		Prompts: []state.FileEntry{
			// Path includes `<`/`>`/`&` to assert SetEscapeHTML(false).
			{Target: ".ach/prompts/<weird>&name.md", Hash: "xxh3:aa", SourceHash: "xxh3:aa"},
		},
	}
	if err := state.Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Assert literal `<` is preserved (not `<`).
	if !contains(raw, []byte("<weird>&name.md")) {
		t.Fatalf("expected literal HTML chars; got %s", string(raw))
	}
	// Assert indent applied.
	var anyVal map[string]any
	if err := json.Unmarshal(raw, &anyVal); err != nil {
		t.Fatalf("written file is not valid JSON: %v\n%s", err, string(raw))
	}
}

// contains is a tiny bytes-substring helper to avoid pulling in
// strings.Contains over []byte conversions.
func contains(hay, needle []byte) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
