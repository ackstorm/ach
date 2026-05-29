// SPDX-License-Identifier: Apache-2.0

package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// File is the §8.2 schema v2 root. The json tags use ",omitempty" on
// every collection so a fresh File round-trips to a minimal document
// without empty arrays.
//
// Per D-13 (clean-break v2), this struct is the ONLY shape the parser
// accepts. schemaVersion "1" is not readable; Load returns
// ErrSchemaMismatch on every non-"2" value (callers map to exit 5).
type File struct {
	SchemaVersion string         `json:"schemaVersion"`
	Environment   string         `json:"environment"`
	Deployment    string         `json:"deployment"`
	Prompts       []FileEntry    `json:"prompts,omitempty"`
	Plugins       []FileEntry    `json:"plugins,omitempty"`
	Artifacts     []FileEntry    `json:"artifacts,omitempty"`
	RuntimeFiles  []FileEntry    `json:"runtimeFiles,omitempty"`
	Adapter       AdapterSection `json:"adapter,omitempty"`
}

// FileEntry is the per-file projection record under each resource
// bucket (and under adapter.files). `target` is workspace-relative,
// `hash` is the xxh3 of bytes on disk, `sourceHash` is the xxh3 of the
// upstream input bytes (before any adapter transformation). For
// pass-through resources hash == sourceHash; for adapter-transformed
// files they differ.
//
// `merge` and `keys` are optional and present only for adapter-written
// shared files (e.g. `.claude/.mcp.json` with `merge: "deep"` and
// `keys: ["mcpServers.github"]`). They drive `--sync`'s inverse-merge
// path per spec §8.5.
type FileEntry struct {
	Target     string   `json:"target"`
	Hash       string   `json:"hash"`
	SourceHash string   `json:"sourceHash"`
	Merge      string   `json:"merge,omitempty"`
	Keys       []string `json:"keys,omitempty"`
}

// AdapterSection records the platform adapter id used at last
// hydration plus the file entries the adapter wrote. Populated only
// when runtime was in scope on the most recent hydration; left
// untouched when only context was hydrated (spec §8.2 field rules).
type AdapterSection struct {
	ID    string      `json:"id,omitempty"`
	Files []FileEntry `json:"files,omitempty"`
}

// Sentinel errors. Callers gate behavior via errors.Is and map to
// §9.3 exit codes through *exit.CodedError (the caller layer — this
// package does not import internal/cli/exit to avoid a cycle).
var (
	// ErrSchemaMismatch is returned by Load when the on-disk
	// state.json's `schemaVersion` field is not "2". Maps to exit 5
	// per CLI spec §8.2. Per D-13, no v1 migration is attempted —
	// callers must pass --force to overwrite (the state is then
	// treated as empty and rewritten on next commit).
	ErrSchemaMismatch = errors.New("state: schemaVersion != \"2\"")

	// ErrStateParse wraps encoding/json decode failures so callers
	// can distinguish "file is corrupt JSON" from "schema is wrong
	// version" or "unknown field present". Maps to exit 1 (General)
	// by default — the caller layer can promote to 5 if the parse
	// failure is structural.
	ErrStateParse = errors.New("state: parse failed")

	// ErrInvalidPath is returned by ResolvePath when the inputs
	// cannot resolve to a concrete on-disk path (e.g. global scope
	// with an empty environment name).
	ErrInvalidPath = errors.New("state: invalid path inputs")
)

// Load reads + parses <ach-dir>/state.json. Returns (nil, nil) when
// the file is absent (fresh workspace — first hydrate). Returns an
// ErrSchemaMismatch-wrapped error when schemaVersion != "2". Returns
// an ErrStateParse-wrapped error when the JSON decode fails or an
// unknown top-level field is present (DisallowUnknownFields gate —
// strict §8.2 schema discipline).
//
// The DisallowUnknownFields check catches both v1 leftover fields
// (`contentHashes`) and forward-compat drift; either path correctly
// exits 5 at the caller layer.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var f File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStateParse, err)
	}

	if f.SchemaVersion != "2" {
		return nil, fmt.Errorf("%w: got %q, want \"2\"", ErrSchemaMismatch, f.SchemaVersion)
	}

	return &f, nil
}

// Save serializes f to JSON and publishes atomically at path via
// WriteAtomic (tmp + fsync(fd) + rename + fsync(parent_dir) per
// STATE-07 / spec §8.7). Rejects a nil File defensively. Passes
// 0o644 to WriteAtomic: state.json carries no plaintext secrets per
// spec §8.2 — unlike `~/.config/ach/config.yaml` (0o600) and the
// adapter runtime-config files written by internal/cli/hydrate
// (0o600 per CR-01 / 07-W5-02). Save is the SOLE legitimate
// 0o644-mode WriteAtomic caller in the tree; every other call site
// (all four in internal/cli/hydrate/wiring.go) must pass 0o600.
//
// The JSON is rendered with 2-space indent for human diffability —
// downstream `git diff` against a checked-in (or sample) state file
// stays readable. The encoder DOES NOT html-escape (SetEscapeHTML
// false) so `<`, `>`, `&` in target paths render literally instead of
// `<` etc.
func Save(path string, f *File) error {
	if f == nil {
		return errors.New("state: Save called with nil File")
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		return err
	}

	return WriteAtomic(path, buf.Bytes(), 0o644)
}
