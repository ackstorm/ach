// SPDX-License-Identifier: Apache-2.0

package skillstage_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/skillstage"
)

// writeFile creates parent dirs and writes a file under base.
func writeFile(t *testing.T, base, rel, content string) {
	t.Helper()
	abs := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

// TestContentDir_RootSKILLMD: SKILL.md at the nameDir root returns nameDir.
func TestContentDir_RootSKILLMD(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: pdf\ndescription: x\n---\nbody")

	got, ok, err := skillstage.ContentDir(dir)
	if err != nil {
		t.Fatalf("ContentDir: %v", err)
	}
	if !ok {
		t.Fatalf("ContentDir ok=false, want true")
	}
	if got != dir {
		t.Errorf("ContentDir = %q, want %q", got, dir)
	}
}

// TestContentDir_OneDirDeep: a single REST-archive wrapper dir returns the wrapper.
func TestContentDir_OneDirDeep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "branding-abc1234/SKILL.md", "---\nname: branding\ndescription: x\n---\nb")

	got, ok, err := skillstage.ContentDir(dir)
	if err != nil {
		t.Fatalf("ContentDir: %v", err)
	}
	if !ok {
		t.Fatalf("ContentDir ok=false, want true")
	}
	want := filepath.Join(dir, "branding-abc1234")
	if got != want {
		t.Errorf("ContentDir = %q, want %q", got, want)
	}
}

// TestContentDir_Absent: no SKILL.md at root or one deep returns (\"\", false).
func TestContentDir_Absent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hi")
	writeFile(t, dir, "sub/notes.md", "deeper")

	got, ok, err := skillstage.ContentDir(dir)
	if err != nil {
		t.Fatalf("ContentDir: %v", err)
	}
	if ok {
		t.Errorf("ContentDir ok=true (got %q), want false", got)
	}
	if got != "" {
		t.Errorf("ContentDir = %q, want empty", got)
	}
}

// TestNest_ProducesSkillsName: Nest yields <stageRoot>/skills/<name>/SKILL.md.
func TestNest_ProducesSkillsName(t *testing.T) {
	extracted := t.TempDir()
	writeFile(t, extracted, "SKILL.md", "---\nname: pdf\ndescription: x\n---\nbody")
	writeFile(t, extracted, "references/a.md", "ref")

	stageRoot := t.TempDir()
	nested, err := skillstage.Nest(stageRoot, "pdf-processing", extracted)
	if err != nil {
		t.Fatalf("Nest: %v", err)
	}
	if !nested {
		t.Fatalf("Nest nested=false, want true")
	}
	for _, rel := range []string{"SKILL.md", "references/a.md"} {
		p := filepath.Join(stageRoot, "skills", "pdf-processing", filepath.FromSlash(rel))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s; err=%v", p, err)
		}
	}
}

// TestNest_StripsWrapper: a one-dir-deep wrapper is rebased away.
func TestNest_StripsWrapper(t *testing.T) {
	extracted := t.TempDir()
	writeFile(t, extracted, "branding-abc1234/SKILL.md", "---\nname: branding\ndescription: x\n---\nb")
	writeFile(t, extracted, "branding-abc1234/scripts/run.sh", "#!/bin/sh\n")

	stageRoot := t.TempDir()
	nested, err := skillstage.Nest(stageRoot, "branding", extracted)
	if err != nil {
		t.Fatalf("Nest: %v", err)
	}
	if !nested {
		t.Fatalf("Nest nested=false, want true")
	}
	if _, err := os.Stat(filepath.Join(stageRoot, "skills", "branding", "SKILL.md")); err != nil {
		t.Errorf("expected skills/branding/SKILL.md; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(stageRoot, "skills", "branding", "scripts", "run.sh")); err != nil {
		t.Errorf("expected skills/branding/scripts/run.sh; err=%v", err)
	}
	// The wrapper segment MUST NOT survive.
	if _, err := os.Stat(filepath.Join(stageRoot, "skills", "branding", "branding-abc1234")); err == nil {
		t.Errorf("wrapper dir leaked one level too deep")
	}
}

// TestNest_AbsentReturnsFalse: no SKILL.md → (false, nil), no skills/ created.
func TestNest_AbsentReturnsFalse(t *testing.T) {
	extracted := t.TempDir()
	writeFile(t, extracted, "README.md", "hi")

	stageRoot := t.TempDir()
	nested, err := skillstage.Nest(stageRoot, "x", extracted)
	if err != nil {
		t.Fatalf("Nest: %v", err)
	}
	if nested {
		t.Errorf("Nest nested=true, want false")
	}
	if _, err := os.Stat(filepath.Join(stageRoot, "skills")); err == nil {
		t.Errorf("skills/ created despite no content dir")
	}
}
