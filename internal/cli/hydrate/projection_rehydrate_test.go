// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// TestProjection_ReHydrate_NativeDestAndByteNoOp is the FMT-05 / ROADMAP SC5
// idempotence proof for the projection path, with the SC2 native-destination
// assertion baked in.
//
// It stages an extracted plugin tree carrying a passthrough kind
// (rules/foo.md) under <achDir>/plugin/<name>, runs a full projection via the
// dispatcher Render (projectPlugins=true), then runs a SECOND Render against
// the SAME unchanged source + the state.Plugins[] the first run recorded.
// Asserts:
//
//	(a) SC2: the projected file lands at the claude-code NATIVE destination
//	    <toolRoot>/.claude/rules/foo.md — NOT the verbatim source-relative
//	    path <toolRoot>/rules/foo.md (a consistently-wrong remap fails here);
//	(b) FMT-05: the on-disk projected bytes are byte-identical across runs;
//	(c) the recorded state.Plugins[] FileEntry (Target/Hash/SourceHash/Merge)
//	    is byte-identical across runs;
//	(d) the second run is a content no-op (bytes.Equal before/after).
//
// This is the precondition for the Phase 4 --sync drift detection and the
// SAFE-04 auto-claim cascade.
func TestProjection_ReHydrate_NativeDestAndByteNoOp(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	const ruleBody = "# rule foo\nalways be excellent to each other\n"
	stagePluginTree(t, achDir, "caveman", map[string]string{
		"rules/foo.md": ruleBody,
	})

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false)
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime:       &manifest.RuntimeBlock{},
		Context:       &manifest.ContextBlock{},
	}

	// --- 1st hydrate (fresh, no prior state) ---
	res1, err := disp.Render(context.Background(), m, nil, achDir, toolRoot, true)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	if len(res1.ProjectedFiles) != 1 {
		t.Fatalf("first Render ProjectedFiles = %d; want 1", len(res1.ProjectedFiles))
	}
	pf1 := res1.ProjectedFiles[0]

	// (a) SC2: native destination, NOT the verbatim source path.
	nativeAbs := filepath.Join(toolRoot, ".claude", "rules", "foo.md")
	if pf1.Target != ".claude/rules/foo.md" {
		t.Fatalf("projected Target = %q; want native .claude/rules/foo.md (SC2 routing)", pf1.Target)
	}
	bytes1, rerr := os.ReadFile(nativeAbs)
	if rerr != nil {
		t.Fatalf("projected file missing at native dest %s: %v", nativeAbs, rerr)
	}
	if string(bytes1) != ruleBody {
		t.Errorf("projected content = %q; want verbatim passthrough %q", bytes1, ruleBody)
	}
	if _, serr := os.Stat(filepath.Join(toolRoot, "rules", "foo.md")); serr == nil {
		t.Errorf("SC2 violation: projected file leaked to verbatim <toolRoot>/rules/foo.md")
	}

	// Compose the prior state.Plugins[] exactly as commit.go step12 records it.
	prior := &state.File{SchemaVersion: "2", Environment: "demo"}
	prior.Plugins = append(prior.Plugins, state.FileEntry{
		Target:     pf1.Target,
		Hash:       pf1.Hash,
		SourceHash: pf1.SourceHash,
		Merge:      pf1.Merge,
		Keys:       pf1.Keys,
	})

	// --- 2nd hydrate (unchanged source + prior state) ---
	res2, err := disp.Render(context.Background(), m, prior, achDir, toolRoot, true)
	if err != nil {
		t.Fatalf("second Render (no-op): %v", err)
	}
	if len(res2.ProjectedFiles) != 1 {
		t.Fatalf("second Render ProjectedFiles = %d; want 1", len(res2.ProjectedFiles))
	}
	pf2 := res2.ProjectedFiles[0]

	// (b) FMT-05: on-disk bytes byte-identical across runs.
	bytes2, rerr := os.ReadFile(nativeAbs)
	if rerr != nil {
		t.Fatalf("projected file missing after second Render: %v", rerr)
	}
	if !bytes.Equal(bytes1, bytes2) {
		t.Errorf("projected bytes changed across re-hydrate:\n  before=%q\n  after=%q", bytes1, bytes2)
	}

	// (c) state.Plugins[] FileEntry byte-identical across runs.
	if pf2.Target != pf1.Target || pf2.Hash != pf1.Hash ||
		pf2.SourceHash != pf1.SourceHash || pf2.Merge != pf1.Merge {
		t.Errorf("projected FileEntry changed across re-hydrate:\n  first=%+v\n  second=%+v", pf1, pf2)
	}
	// Passthrough D-08 invariant: Hash == SourceHash for verbatim copy.
	if pf1.Hash != pf1.SourceHash {
		t.Errorf("passthrough Hash (%q) != SourceHash (%q); want equal (D-08)", pf1.Hash, pf1.SourceHash)
	}
}
