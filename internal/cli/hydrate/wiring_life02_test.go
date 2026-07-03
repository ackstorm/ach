// SPDX-License-Identifier: Apache-2.0

package hydrate

// LIFE-02 / D-30 white-box truth-table coverage for the Plugins[] projected
// resource bucket.
//
// publishFile classifies a co-owned / projected file against its prior state
// record via compareDrift(prior, onDiskHash, freshSourceHash). The
// source-change axis (sourceMatches) compares compareDrift's THIRD argument
// against prior.SourceHash. For a CONVERTED projected file (D-23: Hash ==
// emitted-output xxh3, SourceHash == pre-conversion source xxh3) Hash !=
// SourceHash, so passing the emitted-output freshHash as the third argument
// spuriously trips the source-change axis on every converted file.
//
// LIFE-02 = publishFile must pass the FRESH SOURCE hash (fw.SourceHash, falling
// back to the emitted freshHash when empty — the SAME rule the state-recording
// block uses) as compareDrift's third argument, so the STATE-04 four-outcome
// table classifies converted projected resources correctly.
//
// Each case calls publishFile directly with a converted prior (Hash != SourceHash)
// and an on-disk file staged to a known hash, asserting the OBSERVABLE effect on
// the file system: a NoOp skips the rewrite (on-disk bytes preserved verbatim), an
// UpstreamOnlyOverwrite re-merges (on-disk replaced by the emitted content), and a
// LocalEditPreserve / ConflictPreserve returns *exit.CodedError{Code: Drift}
// unless force. The fixture deliberately makes the emitted output bytes DIFFER
// from the on-disk bytes so a spurious rewrite is observable on disk — the only
// way the wrong Compare third argument leaks through to behavior.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/state"
)

// life02Fixture stages a converted projected file at path under toolRoot with
// onDiskContent already present, and returns the FileWrite (emitted output),
// the prior state.FileEntry, and the absolute on-disk path. The prior records a
// CONVERTED entry: Hash (output) != SourceHash (pre-conversion source).
//
//	emittedContent  — what the adapter would write this run (output bytes)
//	srcHash         — fresh pre-conversion SOURCE hash for this run (fw.SourceHash)
//	priorOutHash    — prior.Hash (output recorded last run)
//	priorSrcHash    — prior.SourceHash (source recorded last run)
//	onDiskContent   — what currently sits on disk
func life02Fixture(t *testing.T, toolRoot, path string, emittedContent []byte, srcHash, priorOutHash, priorSrcHash string, onDiskContent []byte) (adapter.FileWrite, *state.FileEntry, string) {
	t.Helper()
	abs := filepath.Join(toolRoot, path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if onDiskContent != nil {
		if err := os.WriteFile(abs, onDiskContent, 0o644); err != nil {
			t.Fatalf("stage on-disk: %v", err)
		}
	}
	fw := adapter.FileWrite{
		Path:       path,
		Content:    emittedContent,
		Merge:      adapter.MergeReplace, // file-owned projected resource (D-23 codex/opencode converted)
		SourceHash: srcHash,
	}
	prior := &state.FileEntry{
		Target:     path,
		Hash:       priorOutHash,
		SourceHash: priorSrcHash,
		Merge:      mergeStrReplace,
	}
	return fw, prior, abs
}

// TestLife02_ConvertedNoChange_IsNoOp is the REGRESSION GUARD: a converted
// projected file (Hash != SourceHash) whose source is unchanged and whose
// on-disk output is unchanged must classify as NoOp — publishFile skips the
// rewrite and the on-disk bytes are preserved verbatim. BEFORE the fix
// publishFile passes the emitted-output freshHash (not fw.SourceHash) as
// Compare's third argument, so sourceMatches = freshHash == prior.SourceHash is
// false (freshHash is the OUTPUT hash, prior.SourceHash is the SOURCE hash) →
// spurious UpstreamOnlyOverwrite → the file is re-merged to the emitted bytes.
//
// The fixture makes the emitted output bytes DIFFER from the unchanged on-disk
// bytes (both hashing to prior.Hash is impossible, so instead the prior.Hash is
// keyed to the ON-DISK bytes and the emitted bytes are a distinct regeneration);
// a spurious rewrite therefore replaces the on-disk bytes with the emitted bytes
// and the assertion catches it.
func TestLife02_ConvertedNoChange_IsNoOp(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "codex"}

	onDisk := []byte("converted = true\nname = \"agent\"\n")
	emitted := []byte("converted = true\nname  =  \"agent\"\n") // byte-distinct regeneration
	onDiskHash := hash.HashBytes(onDisk)
	if hash.HashBytes(emitted) == onDiskHash {
		t.Fatalf("test setup: emitted and on-disk bytes must hash differently to observe a spurious rewrite")
	}
	srcHash := hash.HashBytes([]byte("# original source agent\n"))
	if srcHash == onDiskHash {
		t.Fatalf("test setup: src and output hashes must differ (converted)")
	}

	// prior.Hash keyed to the on-disk bytes (no local edit), prior.SourceHash to
	// the unchanged source. fw.SourceHash == prior.SourceHash (source unchanged).
	fw, prior, abs := life02Fixture(t, toolRoot, ".codex/agents/foo.toml",
		emitted, srcHash, onDiskHash, srcHash, onDisk)

	if _, err := d.publishFile(fw, prior, toolRoot); err != nil {
		t.Fatalf("publishFile converted-no-change: %v want NoOp skip", err)
	}

	// On-disk content MUST be preserved verbatim (NoOp skip). If the wiring
	// passes freshHash instead of fw.SourceHash, this is a spurious
	// UpstreamOnlyOverwrite and the file is rewritten to `emitted`.
	body, _ := os.ReadFile(abs)
	if string(body) != string(onDisk) {
		t.Errorf("on-disk content rewritten to %q; want preserved %q (spurious UpstreamOnlyOverwrite — Compare got freshHash not fw.SourceHash)", body, onDisk)
	}
}

// TestLife02_SourceOnlyChange_UpstreamOnlyOverwrite proves a source-only upstream
// change (on-disk == prior output, but fresh source != prior source) re-merges:
// UpstreamOnlyOverwrite, no drift error, on-disk replaced by the emitted output.
func TestLife02_SourceOnlyChange_UpstreamOnlyOverwrite(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "codex"}

	priorOut := []byte("converted = \"old\"\n")
	newOut := []byte("converted = \"new\"\n")
	priorOutHash := hash.HashBytes(priorOut)
	priorSrcHash := hash.HashBytes([]byte("# old source\n"))
	newSrcHash := hash.HashBytes([]byte("# new source\n"))

	// on-disk == prior output (no local edit); fresh source differs.
	fw, prior, abs := life02Fixture(t, toolRoot, ".codex/agents/foo.toml",
		newOut, newSrcHash, priorOutHash, priorSrcHash, priorOut)

	if _, err := d.publishFile(fw, prior, toolRoot); err != nil {
		t.Fatalf("publishFile source-only: %v want re-merge (no drift)", err)
	}
	body, _ := os.ReadFile(abs)
	if string(body) != string(newOut) {
		t.Errorf("on-disk content = %q; want re-merged %q", body, newOut)
	}
}

// TestLife02_UserEdit_LocalEditPreserve proves a user edit to a projected file
// (on-disk != prior output) with source unchanged classifies as
// LocalEditPreserve and publishFile returns *exit.CodedError{Code: Drift} unless
// force; with force it overwrites.
func TestLife02_UserEdit_LocalEditPreserve(t *testing.T) {
	srcHash := hash.HashBytes([]byte("# unchanged source\n"))
	priorOut := []byte("converted = \"orig\"\n")
	priorOutHash := hash.HashBytes(priorOut)
	userEdited := []byte("converted = \"USER EDITED\"\n")
	newOut := []byte("converted = \"regen\"\n")

	// --- without force: drift error, on-disk preserved ---
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "codex"}
	fw, prior, abs := life02Fixture(t, toolRoot, ".codex/agents/foo.toml",
		newOut, srcHash, priorOutHash, srcHash, userEdited)

	_, err := d.publishFile(fw, prior, toolRoot)
	if err == nil {
		t.Fatalf("publishFile user-edit: nil error; want drift error (exit 2)")
	}
	var ce *exit.CodedError
	if !errors.As(err, &ce) || ce.Code != exit.Drift {
		t.Fatalf("publishFile user-edit error = %v; want *exit.CodedError Code=Drift", err)
	}
	if body, _ := os.ReadFile(abs); string(body) != string(userEdited) {
		t.Errorf("on-disk content = %q; want user edit preserved %q", body, userEdited)
	}

	// --- with force: overwrites ---
	toolRoot2 := t.TempDir()
	dForce := &adapterDispatcherImpl{platformID: "codex", force: true}
	fw2, prior2, abs2 := life02Fixture(t, toolRoot2, ".codex/agents/foo.toml",
		newOut, srcHash, priorOutHash, srcHash, userEdited)
	if _, err := dForce.publishFile(fw2, prior2, toolRoot2); err != nil {
		t.Fatalf("publishFile user-edit --force: %v want overwrite", err)
	}
	if body, _ := os.ReadFile(abs2); string(body) != string(newOut) {
		t.Errorf("force on-disk content = %q; want overwritten %q", body, newOut)
	}
}

// TestLife02_BothChanged_ConflictPreserve proves both-changed (on-disk != prior
// output AND fresh source != prior source) classifies as ConflictPreserve and is
// preserved (drift error) unless force.
func TestLife02_BothChanged_ConflictPreserve(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "codex"}

	priorOut := []byte("converted = \"orig\"\n")
	priorOutHash := hash.HashBytes(priorOut)
	priorSrcHash := hash.HashBytes([]byte("# old source\n"))
	newSrcHash := hash.HashBytes([]byte("# new source\n"))
	userEdited := []byte("converted = \"USER EDITED\"\n")
	newOut := []byte("converted = \"regen\"\n")

	fw, prior, abs := life02Fixture(t, toolRoot, ".codex/agents/foo.toml",
		newOut, newSrcHash, priorOutHash, priorSrcHash, userEdited)

	_, err := d.publishFile(fw, prior, toolRoot)
	var ce *exit.CodedError
	if !errors.As(err, &ce) || ce.Code != exit.Drift {
		t.Fatalf("publishFile both-changed error = %v; want *exit.CodedError Code=Drift", err)
	}
	if body, _ := os.ReadFile(abs); string(body) != string(userEdited) {
		t.Errorf("on-disk content = %q; want user edit preserved %q", body, userEdited)
	}
}

// TestLife02_PassthroughInvariant proves the fw.SourceHash=="" fallback: a
// passthrough projected file (Hash == SourceHash) keeps the Phase-1/2 behavior
// — freshSourceHash falls back to freshHash, so an unchanged passthrough file
// classifies as NoOp exactly as before, preserving the on-disk bytes.
func TestLife02_PassthroughInvariant(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "claude-code"}

	body := []byte("# rule foo\nbe excellent\n")
	bodyHash := hash.HashBytes(body)

	// Passthrough: fw.SourceHash empty; prior recorded Hash == SourceHash; on-disk
	// == prior output. Unchanged → NoOp skip → bytes preserved.
	fw := adapter.FileWrite{
		Path:    ".claude/rules/foo.md",
		Content: body,
		Merge:   adapter.MergeReplace,
		// SourceHash intentionally empty (passthrough).
	}
	abs := filepath.Join(toolRoot, fw.Path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		t.Fatalf("stage: %v", err)
	}
	prior := &state.FileEntry{Target: fw.Path, Hash: bodyHash, SourceHash: bodyHash, Merge: mergeStrReplace}

	entry, err := d.publishFile(fw, prior, toolRoot)
	if err != nil {
		t.Fatalf("publishFile passthrough: %v", err)
	}
	if entry.Hash != entry.SourceHash {
		t.Errorf("passthrough entry Hash (%q) != SourceHash (%q); want equal", entry.Hash, entry.SourceHash)
	}
	if got, _ := os.ReadFile(abs); string(got) != string(body) {
		t.Errorf("passthrough on-disk = %q; want preserved %q", got, body)
	}
}
