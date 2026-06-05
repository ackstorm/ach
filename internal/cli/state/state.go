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
// ErrSchemaMismatch on every non-"3" value (callers map to exit 5).
type File struct {
	SchemaVersion string         `json:"schemaVersion"`
	Environment   string         `json:"environment"`
	Profile       string         `json:"profile"`
	Prompts       []FileEntry    `json:"prompts,omitempty"`
	Plugins       []FileEntry    `json:"plugins,omitempty"`
	Artifacts     []FileEntry    `json:"artifacts,omitempty"`
	Skills        []FileEntry    `json:"skills,omitempty"`
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
	// state.json's `schemaVersion` field is not "3". Maps to exit 5
	// per CLI spec §8.2. Per D-13, no v1 migration is attempted —
	// callers must pass --force to overwrite (the state is then
	// treated as empty and rewritten on next commit).
	ErrSchemaMismatch = errors.New("state: schemaVersion != \"3\"")

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
// ErrSchemaMismatch-wrapped error when schemaVersion != "3". Returns
// an ErrStateParse-wrapped error when the JSON decode fails or an
// unknown top-level field is present (DisallowUnknownFields gate —
// strict §8.2 schema discipline).
//
// Two-phase parse (WR-03 / 07-W5-06):
//
//   - Phase 1: best-effort schemaVersion check. A non-strict
//     json.Unmarshal extracts only the top-level `schemaVersion` field.
//     If present and != "3", Load returns ErrSchemaMismatch immediately
//     — without attempting the strict decode. This is the load-bearing
//     branch for the user-facing `--force` recovery contract documented
//     in CLAUDE.md's "schemaVersion != \"3\"" failure-mode entry: a
//     v1 state.json (carrying the removed `contentHashes` field, or
//     any other non-"3" schemaVersion) maps to exit 5, which the
//     caller (`hydrate/commit.go:step3ReadState`) bypasses with
//     `--force` to overwrite the stale file. If the strict decode ran
//     first, a v1 file's unknown fields would surface as ErrStateParse
//     (exit 1, no `--force` escape hatch) — the wrong recovery posture.
//
//   - Phase 2: strict DisallowUnknownFields decode. Runs only after
//     phase 1 admits the file. Catches the legitimate "corrupt v2
//     state.json" arm: a CURRENT-version file with an unknown top-
//     level field (forward-compat drift, internal corruption). Returns
//     ErrStateParse — exit 1 with NO `--force` escape. This is
//     correctness-preserving: an unknown field in a current-version
//     state file is a bug, not a user-recoverable migration, and the
//     engine refuses to silently rewrite it.
//
// The final schemaVersion check on the decoded *File covers the edge
// case where phase 1's best-effort Unmarshal failed to populate sv
// (e.g. JSON malformed outside the schemaVersion field) and phase 2's
// strict decode subsequently saw an empty SchemaVersion. Belt-and-
// braces against a phase-1 false negative.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Phase 1: best-effort schemaVersion gate. Run BEFORE the strict
	// decode so a v1 file (legacy `contentHashes`) returns
	// ErrSchemaMismatch (exit 5, --force bypass) instead of
	// ErrStateParse (exit 1, no bypass). Ignoring the Unmarshal error
	// is intentional — phase 2's strict decode is the authoritative
	// parse, and we only need a probable schemaVersion value here.
	var sv struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	_ = json.Unmarshal(raw, &sv)
	if sv.SchemaVersion != "" && sv.SchemaVersion != "3" {
		return nil, fmt.Errorf("%w: got %q, want \"3\"", ErrSchemaMismatch, sv.SchemaVersion)
	}

	// Phase 2: strict DisallowUnknownFields decode. Reached only when
	// phase 1 admitted the file. Catches v2-with-unknown-field (forward-
	// compat drift, corruption) — returns ErrStateParse (exit 1, no
	// --force escape, the correctness-preserving posture for a bug in
	// a current-version state file).
	var f File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStateParse, err)
	}

	if f.SchemaVersion != "3" {
		return nil, fmt.Errorf("%w: got %q, want \"3\"", ErrSchemaMismatch, f.SchemaVersion)
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
