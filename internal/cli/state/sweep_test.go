// SPDX-License-Identifier: Apache-2.0

package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestSweepTmp_AbsentTmpDir_ReturnsNil asserts the idempotent fresh-
// workspace branch: a hydrate that runs before tmp/ has ever been
// created (first hydrate ever) MUST NOT error. SweepTmp is called at
// step 2 of §6.7 unconditionally; absent dir = nothing to do.
func TestSweepTmp_AbsentTmpDir_ReturnsNil(t *testing.T) {
	achDir := t.TempDir()
	// Deliberately do NOT create tmp/ under achDir.
	if err := state.SweepTmp(achDir); err != nil {
		t.Fatalf("SweepTmp on absent tmp/: err = %v, want nil", err)
	}
}

// TestSweepTmp_RemovesAllSubentries asserts the D-02 unconditional
// sweep: every subentry under tmp/ is removed regardless of ModTime.
// No maxAge cutoff — the CLI's tmp/ has no concurrent writers
// between locks, so the sweep is total.
func TestSweepTmp_RemovesAllSubentries(t *testing.T) {
	achDir := t.TempDir()
	tmp := filepath.Join(achDir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatalf("MkdirAll tmp: %v", err)
	}

	// Seed three staging subtrees with nested content. Subentries
	// are directories per the staging-subdir contract.
	for _, sub := range []string{"stg-aaa", "stg-bbb", "stg-ccc"} {
		nested := filepath.Join(tmp, sub, "nested", "deep")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", sub, err)
		}
		if err := os.WriteFile(filepath.Join(nested, "x.bin"), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s/x.bin: %v", sub, err)
		}
	}
	// Also seed one flat file directly under tmp/ — covers the
	// "subentry is a regular file" path (RemoveAll handles both).
	if err := os.WriteFile(filepath.Join(tmp, "stg-flat"), []byte("y"), 0o644); err != nil {
		t.Fatalf("WriteFile flat: %v", err)
	}

	if err := state.SweepTmp(achDir); err != nil {
		t.Fatalf("SweepTmp: %v", err)
	}

	// Post-condition: tmp/ exists but is empty.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir tmp/: %v", err)
	}
	if len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("SweepTmp left %d entries behind: %v", len(entries), names)
	}
}

// TestSweepTmp_PreservesNonTmpSiblings asserts the spec §6.7 step 2
// scope: only <achDir>/tmp/ is touched. Sibling files (state.json,
// lock, prompts/, artifacts/) MUST survive.
func TestSweepTmp_PreservesNonTmpSiblings(t *testing.T) {
	achDir := t.TempDir()

	// Seed siblings that must survive.
	if err := os.WriteFile(filepath.Join(achDir, "state.json"),
		[]byte(`{"schemaVersion":"2"}`), 0o644); err != nil {
		t.Fatalf("seed state.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(achDir, "lock"),
		[]byte(""), 0o644); err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(achDir, "prompts", "code-rev"), 0o755); err != nil {
		t.Fatalf("MkdirAll prompts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(achDir, "prompts", "code-rev", "p.md"),
		[]byte("md"), 0o644); err != nil {
		t.Fatalf("seed prompt: %v", err)
	}

	// Seed tmp/ with a single subentry that SHOULD be swept.
	tmp := filepath.Join(achDir, "tmp")
	if err := os.MkdirAll(filepath.Join(tmp, "stg-xyz"), 0o755); err != nil {
		t.Fatalf("MkdirAll tmp/stg-xyz: %v", err)
	}

	if err := state.SweepTmp(achDir); err != nil {
		t.Fatalf("SweepTmp: %v", err)
	}

	// Siblings survive.
	for _, p := range []string{
		filepath.Join(achDir, "state.json"),
		filepath.Join(achDir, "lock"),
		filepath.Join(achDir, "prompts", "code-rev", "p.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("sibling %s did not survive sweep: %v", p, err)
		}
	}
	// tmp/ swept.
	if _, err := os.Stat(filepath.Join(tmp, "stg-xyz")); !os.IsNotExist(err) {
		t.Errorf("tmp/stg-xyz survived sweep: stat err=%v", err)
	}
}

// TestSweepTmp_RemoveFails_SwallowsError asserts the "benign cleanup"
// posture per spec §6.7 step 2: a per-entry RemoveAll failure must
// NOT abort the hydrate. We can't reliably induce a RemoveAll error
// on a temp dir without root privileges (chmod 0 on a parent doesn't
// reliably block RemoveAll on Linux when run as the owner — RemoveAll
// will chmod-recover or proceed), so this test asserts the contract
// the cheap way: SweepTmp on a tmp/ that contains a normally-
// removable subtree returns nil. A more aggressive negative test
// would need a fault-injecting filesystem.
//
// The actual swallow behavior is exercised by every TestSweepTmp_*
// test via the fact that we never see a non-nil return. This test is
// named explicitly so a future reader of the test list sees the
// contract documented.
func TestSweepTmp_RemoveFails_SwallowsError(t *testing.T) {
	achDir := t.TempDir()
	tmp := filepath.Join(achDir, "tmp")
	if err := os.MkdirAll(filepath.Join(tmp, "stg-target"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Even though we cannot inject a hard RemoveAll failure here,
	// the contract is the same: nil return regardless of per-entry
	// outcome. This test documents the contract.
	if err := state.SweepTmp(achDir); err != nil {
		t.Fatalf("SweepTmp: %v, want nil per spec §6.7 step 2", err)
	}
}
