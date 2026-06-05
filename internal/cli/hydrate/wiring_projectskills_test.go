// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"os"
	"path/filepath"
	"testing"
)

// stageSkill writes files under <achDir>/skill/<name>/ (the extracted
// standalone-Skill stage root projectSkills reads from).
func stageSkill(t *testing.T, achDir, name string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(achDir, "skill", name, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", abs, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
}

// TestProjectSkills_StripsArchiveWrapper proves the P1 fix: a skill whose
// tarball nested everything under a REST archive wrapper ("<repo>-<sha>/")
// projects to the one-level .claude/skills/<name>/SKILL.md layout adapters
// expect — NOT one directory too deep under the wrapper.
func TestProjectSkills_StripsArchiveWrapper(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	// verifySkillContents accepts SKILL.md one dir deep; the extractor lays the
	// wrapper down verbatim as skill/branding/branding-abc1234/SKILL.md.
	stageSkill(t, achDir, "branding", map[string]string{
		"branding-abc1234/SKILL.md":       "---\nname: branding\ndescription: x\n---\nbody",
		"branding-abc1234/scripts/run.sh": "#!/bin/sh\necho hi\n",
	})

	d := &adapterDispatcherImpl{platformID: "fakeskills"}
	var result RenderResult
	if err := d.projectSkills(fakeSkillsAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectSkills: %v", err)
	}

	wantPath := filepath.Join(toolRoot, ".claude", "skills", "branding", "SKILL.md")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected %s; stat err=%v", wantPath, err)
	}
	if _, err := os.Stat(filepath.Join(toolRoot, ".claude", "skills", "branding", "scripts", "run.sh")); err != nil {
		t.Errorf("expected scripts/run.sh under skills/branding; err=%v", err)
	}
	// The wrapper segment MUST NOT survive into the projected layout.
	tooDeep := filepath.Join(toolRoot, ".claude", "skills", "branding", "branding-abc1234")
	if _, err := os.Stat(tooDeep); err == nil {
		t.Errorf("wrapper dir leaked one level too deep: %s exists", tooDeep)
	}
	if len(result.ProjectedSkillFiles) == 0 {
		t.Errorf("ProjectedSkillFiles empty; want >=1")
	}
}

// TestProjectSkills_RootSKILLMD proves the non-wrapped case still projects to
// the same one-level layout.
func TestProjectSkills_RootSKILLMD(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	stageSkill(t, achDir, "pdf-processing", map[string]string{
		"SKILL.md":       "---\nname: pdf-processing\ndescription: y\n---\nbody",
		"references/a.md": "ref",
	})

	d := &adapterDispatcherImpl{platformID: "fakeskills"}
	var result RenderResult
	if err := d.projectSkills(fakeSkillsAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectSkills: %v", err)
	}
	for _, rel := range []string{"SKILL.md", "references/a.md"} {
		p := filepath.Join(toolRoot, ".claude", "skills", "pdf-processing", filepath.FromSlash(rel))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s; err=%v", p, err)
		}
	}
}
