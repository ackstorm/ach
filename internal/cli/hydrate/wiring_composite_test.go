// SPDX-License-Identifier: Apache-2.0

package hydrate

// Internal-package (white-box) tests for the D-06 forward composite arm
// in publishFile, the per-plugin marker builder (D-07), and the
// syncComposite empty-Keys backward-compat fallback. These exercise the
// unexported publishFile / pluginMarkerRE / syncComposite directly because
// the claude/gemini ProjectionRules MergeComposite + MergeDeep rows (which
// would let the behaviors be driven through the exported Render) land in
// plans 02-03/02-04 — this plan ships the publish path they depend on.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/state"
)

// compositeFW builds a MergeComposite FileWrite for plugin id carrying body.
func compositeFW(id, path, body string) adapter.FileWrite {
	return adapter.FileWrite{
		Path:    path,
		Content: []byte(body),
		Merge:   adapter.MergeComposite,
		Keys:    []string{id},
	}
}

// readFileT reads abs or fails the test.
func readFileT(t *testing.T, abs string) string {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	return string(b)
}

// TestPublishFile_Composite_Insert: composite into an absent host memory
// file writes exactly one per-plugin marked block containing fw.Content
// verbatim, at mode 0o644.
func TestPublishFile_Composite_Insert(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "claude-code"}

	fw := compositeFW("caveman", "CLAUDE.md", "# caveman rules\nbe excellent\n")
	entry, err := d.publishFile(fw, nil, toolRoot)
	if err != nil {
		t.Fatalf("publishFile composite insert: %v", err)
	}

	abs := filepath.Join(toolRoot, "CLAUDE.md")
	got := readFileT(t, abs)
	want := "<!-- ach:begin:caveman -->\n# caveman rules\nbe excellent\n<!-- ach:end:caveman -->\n"
	if got != want {
		t.Errorf("composite insert body:\n got=%q\nwant=%q", got, want)
	}

	// Mode 0o644 — host memory files carry no credential (D-06).
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("composite file mode = %o; want 0o644", info.Mode().Perm())
	}

	// Recorded state row carries Keys=[plugin-name] and Merge=composite.
	if entry.Merge != mergeStrComposite {
		t.Errorf("entry.Merge = %q; want %q", entry.Merge, mergeStrComposite)
	}
	if len(entry.Keys) != 1 || entry.Keys[0] != "caveman" {
		t.Errorf("entry.Keys = %v; want [caveman]", entry.Keys)
	}
}

// TestPublishFile_Composite_ReplaceIsolated: a second hydrate of the SAME
// plugin replaces only its block; a DIFFERENT plugin's pre-existing block
// AND surrounding user prose are byte-preserved.
func TestPublishFile_Composite_ReplaceIsolated(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "claude-code"}
	abs := filepath.Join(toolRoot, "CLAUDE.md")

	// Seed a file with USER PROSE, plug-a's block, more prose, plug-b's block.
	seed := "# user header\n" +
		"<!-- ach:begin:plug-a -->\nA OLD\n<!-- ach:end:plug-a -->\n" +
		"user middle prose\n" +
		"<!-- ach:begin:plug-b -->\nB STABLE\n<!-- ach:end:plug-b -->\n" +
		"user footer\n"
	if err := os.WriteFile(abs, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Re-publish plug-a with NEW content.
	fw := compositeFW("plug-a", "CLAUDE.md", "A NEW\n")
	if _, err := d.publishFile(fw, nil, toolRoot); err != nil {
		t.Fatalf("publishFile composite replace: %v", err)
	}

	got := readFileT(t, abs)
	if strings.Contains(got, "A OLD") {
		t.Errorf("plug-a old block not replaced:\n%s", got)
	}
	if !strings.Contains(got, "<!-- ach:begin:plug-a -->\nA NEW\n<!-- ach:end:plug-a -->") {
		t.Errorf("plug-a new block missing:\n%s", got)
	}
	// plug-b's block + all user prose byte-preserved.
	for _, want := range []string{
		"# user header\n",
		"<!-- ach:begin:plug-b -->\nB STABLE\n<!-- ach:end:plug-b -->\n",
		"user middle prose\n",
		"user footer\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("isolation broke: %q missing from\n%s", want, got)
		}
	}
}

// TestPublishFile_Composite_MarkerInjection: a plugin AGENTS.md whose body
// carries a FORGED <!-- ach:begin:evil -->...<!-- ach:end:evil --> region
// does NOT let the engine treat the forged region as plugin "evil"'s block.
// The forged text is written verbatim INSIDE this plugin's own outer
// markers, and pluginMarkerRE("evil") does not select a region this plugin
// did not legitimately own (no hijack / escape — T-02-03).
func TestPublishFile_Composite_MarkerInjection(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "claude-code"}
	abs := filepath.Join(toolRoot, "CLAUDE.md")

	forged := "intro\n<!-- ach:begin:evil -->payload<!-- ach:end:evil -->\noutro\n"
	fw := compositeFW("victim", "CLAUDE.md", forged)
	if _, err := d.publishFile(fw, nil, toolRoot); err != nil {
		t.Fatalf("publishFile injection: %v", err)
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// The forged region is enclosed within victim's OUTER markers.
	wantOuter := "<!-- ach:begin:victim -->\n" + forged + "<!-- ach:end:victim -->\n"
	if string(body) != wantOuter {
		t.Errorf("forged content not wrapped in victim's outer markers:\n got=%q\nwant=%q", body, wantOuter)
	}

	// pluginMarkerRE("victim") selects the WHOLE outer block (from the real
	// victim begin to the real victim end) — NOT truncated at the forged
	// inner markers.
	victimRegion := pluginMarkerRE("victim").Find(body)
	if string(victimRegion) != wantOuter {
		t.Errorf("victim region truncated/hijacked by forged markers:\n got=%q\nwant=%q", victimRegion, wantOuter)
	}

	// pluginMarkerRE("evil") matches ONLY the forged inner text as a literal
	// region (the regex's optional trailing \n is consumed) — it does NOT and
	// cannot select victim's real block. Crucially, matching "evil" must not
	// let an attacker subtract victim's content: a syncComposite for a
	// (non-existent) "evil" plugin would only touch the forged literal,
	// leaving victim's real markers intact.
	evilRegion := pluginMarkerRE("evil").Find(body)
	if !strings.HasPrefix(string(evilRegion), "<!-- ach:begin:evil -->payload<!-- ach:end:evil -->") {
		t.Errorf("evil region = %q; want only the inert forged literal", evilRegion)
	}
	// The evil region must NOT span/escape into victim's real boundary.
	if strings.Contains(string(evilRegion), "ach:begin:victim") ||
		strings.Contains(string(evilRegion), "ach:end:victim") {
		t.Errorf("forged evil marker escaped into victim's real markers: %q", evilRegion)
	}
}

// TestPublishFile_Composite_Drift: editing prose OUTSIDE the marked region
// does not raise drift (NoOp); editing INSIDE the marked region raises
// ShouldExit2 (LocalEditPreserve) unless --force.
func TestPublishFile_Composite_Drift(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "claude-code"}
	abs := filepath.Join(toolRoot, "CLAUDE.md")

	// First publish to establish the block + its hash.
	fw := compositeFW("caveman", "CLAUDE.md", "RULE BODY\n")
	entry, err := d.publishFile(fw, nil, toolRoot)
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	prior := &state.FileEntry{
		Target:     entry.Target,
		Hash:       entry.Hash,
		SourceHash: entry.SourceHash,
		Merge:      entry.Merge,
		Keys:       entry.Keys,
	}

	// (a) Edit prose OUTSIDE the block → must NOT flag drift; re-publish is a
	// no-op for the block, and the outside prose survives.
	body := readFileT(t, abs)
	withOutsideEdit := "USER ADDED THIS\n" + body
	if err := os.WriteFile(abs, []byte(withOutsideEdit), 0o644); err != nil {
		t.Fatalf("outside edit: %v", err)
	}
	if _, err := d.publishFile(fw, prior, toolRoot); err != nil {
		t.Fatalf("re-publish after outside edit must not drift, got: %v", err)
	}
	after := readFileT(t, abs)
	if !strings.Contains(after, "USER ADDED THIS") {
		t.Errorf("outside prose lost across no-op re-publish:\n%s", after)
	}

	// (b) Edit INSIDE the block → drift (no --force) must refuse.
	tampered := pluginMarkerRE("caveman").ReplaceAllString(after,
		"<!-- ach:begin:caveman -->\nUSER TAMPERED\n<!-- ach:end:caveman -->\n")
	if err := os.WriteFile(abs, []byte(tampered), 0o644); err != nil {
		t.Fatalf("inside edit: %v", err)
	}
	if _, err := d.publishFile(fw, prior, toolRoot); err == nil {
		t.Errorf("re-publish after inside-block edit: want drift error, got nil")
	}

	// (c) Same tamper + --force → overwrites the block (no error).
	df := &adapterDispatcherImpl{platformID: "claude-code", force: true}
	if _, err := df.publishFile(fw, prior, toolRoot); err != nil {
		t.Errorf("--force re-publish after inside edit: %v", err)
	}
	forced := readFileT(t, abs)
	if strings.Contains(forced, "USER TAMPERED") {
		t.Errorf("--force did not overwrite tampered block:\n%s", forced)
	}
}

// TestSyncComposite_EmptyKeys_GenericFallback (backward-compat): syncComposite
// over a host memory file containing ONLY a generic
// <!-- ach:begin -->...<!-- ach:end --> region, invoked with a FileEntry whose
// Keys is empty, removes that generic-marker region while preserving user
// prose outside it (D-07 empty-Keys fallback via genericMarkerRE).
func TestSyncComposite_EmptyKeys_GenericFallback(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "CLAUDE.md")
	seed := "# top prose\n<!-- ach:begin -->OLD BLOCK<!-- ach:end -->\nbottom prose\n"
	if err := os.WriteFile(abs, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Empty Keys → generic fallback.
	preserved, err := syncComposite(state.FileEntry{Target: abs, Merge: mergeStrComposite}, abs, SyncOptions{})
	if err != nil {
		t.Fatalf("syncComposite empty-keys: %v", err)
	}
	if preserved {
		t.Errorf("syncComposite empty-keys: preserved=true; want pruned (generic region removed)")
	}
	got := readFileT(t, abs)
	if strings.Contains(got, "OLD BLOCK") || strings.Contains(got, "ach:begin") {
		t.Errorf("generic region not removed:\n%s", got)
	}
	for _, want := range []string{"# top prose\n", "bottom prose\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("user prose %q lost:\n%s", want, got)
		}
	}
}

// TestSyncComposite_PerID_RemovesOnlyNamedBlock proves the per-id inverse
// targets exactly the named plugin's block, leaving a sibling plugin's block
// intact (D-07: Phase-4 subtracts exactly one plugin's block).
func TestSyncComposite_PerID_RemovesOnlyNamedBlock(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "CLAUDE.md")
	seed := "<!-- ach:begin:plug-a -->\nA\n<!-- ach:end:plug-a -->\n" +
		"<!-- ach:begin:plug-b -->\nB\n<!-- ach:end:plug-b -->\n"
	if err := os.WriteFile(abs, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	preserved, err := syncComposite(
		state.FileEntry{Target: abs, Merge: mergeStrComposite, Keys: []string{"plug-a"}},
		abs, SyncOptions{})
	if err != nil {
		t.Fatalf("syncComposite per-id: %v", err)
	}
	if preserved {
		t.Errorf("syncComposite per-id: preserved=true; want pruned")
	}
	got := readFileT(t, abs)
	if strings.Contains(got, "ach:begin:plug-a") {
		t.Errorf("plug-a block not removed:\n%s", got)
	}
	if !strings.Contains(got, "<!-- ach:begin:plug-b -->\nB\n<!-- ach:end:plug-b -->") {
		t.Errorf("plug-b block wrongly removed:\n%s", got)
	}
}

// TestPublishFile_Composite_RehydrateNoOp proves FMT-05 byte idempotence: a
// second publish of the same plugin against its recorded prior state is a byte
// no-op AND re-asserts 0o644 even if the mode leaked to 0o600.
func TestPublishFile_Composite_RehydrateNoOp(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "claude-code"}
	abs := filepath.Join(toolRoot, "CLAUDE.md")

	fw := compositeFW("caveman", "CLAUDE.md", "BODY\n")
	entry, err := d.publishFile(fw, nil, toolRoot)
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	before := readFileT(t, abs)
	prior := &state.FileEntry{
		Target: entry.Target, Hash: entry.Hash, SourceHash: entry.SourceHash,
		Merge: entry.Merge, Keys: entry.Keys,
	}

	// Leak mode to 0o600 between hydrates.
	if err := os.Chmod(abs, 0o600); err != nil {
		t.Fatalf("chmod leak: %v", err)
	}
	if _, err := d.publishFile(fw, prior, toolRoot); err != nil {
		t.Fatalf("no-op re-publish: %v", err)
	}
	after := readFileT(t, abs)
	if before != after {
		t.Errorf("composite bytes changed across re-hydrate:\n before=%q\n after=%q", before, after)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("no-op skip downgraded composite mode to %o; want 0o644 (MergeKind-guarded chmod)", info.Mode().Perm())
	}
}

// sanity: the recorded fresh hash equals the hash of the marked block bytes
// (drift is computed over the marked region only). Guards the D-06 hashing
// contract against a regression to whole-file hashing.
func TestPublishFile_Composite_DriftHashIsMarkedRegion(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "claude-code"}

	fw := compositeFW("caveman", "CLAUDE.md", "BODY\n")
	entry, err := d.publishFile(fw, nil, toolRoot)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	block := "<!-- ach:begin:caveman -->\nBODY\n<!-- ach:end:caveman -->\n"
	want := hash.HashBytes([]byte(block))
	if entry.Hash != want {
		t.Errorf("composite Hash = %q; want hash of marked block %q", entry.Hash, want)
	}
}
