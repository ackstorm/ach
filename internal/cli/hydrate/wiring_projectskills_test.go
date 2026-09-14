// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
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
		"SKILL.md":        "---\nname: pdf-processing\ndescription: y\n---\nbody",
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

// writeForeignSkillState lays down a sibling environment's per-platform state
// file claiming the named skills, as a prior `env hydrate <other>` would.
func writeForeignSkillState(t *testing.T, achRoot, env, platform string, skills ...string) {
	t.Helper()
	f := &state.File{SchemaVersion: "3", Environment: env}
	for _, name := range skills {
		f.Skills = append(f.Skills, state.FileEntry{
			Target: filepath.Join(".claude", "skills", name, "SKILL.md"),
			Source: name,
		})
	}
	dir := filepath.Join(achRoot, env)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := state.Save(filepath.Join(dir, "state-"+platform+".json"), f); err != nil {
		t.Fatalf("save foreign state: %v", err)
	}
}

// TestProjectSkills_ForeignEnvCollisionPrefixes proves the multi-environment
// case: a skill name a sibling Environment already projected into the shared
// toolRoot is staged as <env>-<name>, so both survive instead of the second
// hydrate overwriting the first. Every file of the skill moves together.
func TestProjectSkills_ForeignEnvCollisionPrefixes(t *testing.T) {
	achRoot := t.TempDir()
	toolRoot := t.TempDir()
	achDir := filepath.Join(achRoot, "images")
	writeForeignSkillState(t, achRoot, "slides", "fakeskills", "pdf")

	stageSkill(t, achDir, "pdf", map[string]string{
		"SKILL.md":        "---\nname: pdf\ndescription: y\n---\nbody",
		"references/a.md": "ref",
	})

	d := &adapterDispatcherImpl{platformID: "fakeskills"}
	var result RenderResult
	if err := d.projectSkills(fakeSkillsAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectSkills: %v", err)
	}
	for _, rel := range []string{"SKILL.md", "references/a.md"} {
		p := filepath.Join(toolRoot, ".claude", "skills", "images-pdf", filepath.FromSlash(rel))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected prefixed %s; err=%v", p, err)
		}
	}
	// The incumbent environment's bare name must NOT be written over.
	if _, err := os.Stat(filepath.Join(toolRoot, ".claude", "skills", "pdf")); err == nil {
		t.Errorf("bare skills/pdf written; the sibling environment owns that name")
	}
	for _, e := range result.ProjectedSkillFiles {
		if e.Source != "images-pdf" {
			t.Errorf("state Source = %q, want images-pdf", e.Source)
		}
	}
}

// TestProjectSkills_ForeignEnvNoCollisionKeepsBareName proves the prefix is
// conditional: a sibling Environment owning OTHER skill names leaves ours bare.
// A single-environment workspace must never see renamed skills.
func TestProjectSkills_ForeignEnvNoCollisionKeepsBareName(t *testing.T) {
	achRoot := t.TempDir()
	toolRoot := t.TempDir()
	achDir := filepath.Join(achRoot, "images")
	writeForeignSkillState(t, achRoot, "slides", "fakeskills", "deck")

	stageSkill(t, achDir, "pdf", map[string]string{
		"SKILL.md": "---\nname: pdf\ndescription: y\n---\nbody",
	})

	d := &adapterDispatcherImpl{platformID: "fakeskills"}
	var result RenderResult
	if err := d.projectSkills(fakeSkillsAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectSkills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(toolRoot, ".claude", "skills", "pdf", "SKILL.md")); err != nil {
		t.Errorf("expected bare skills/pdf/SKILL.md; err=%v", err)
	}
}

// TestProjectSkills_ForeignStateIgnoresOtherPlatform proves the claim set is
// per-platform: a sibling Environment hydrated for a DIFFERENT target writes a
// different destination tree, so it is not a collision.
func TestProjectSkills_ForeignStateIgnoresOtherPlatform(t *testing.T) {
	achRoot := t.TempDir()
	toolRoot := t.TempDir()
	achDir := filepath.Join(achRoot, "images")
	writeForeignSkillState(t, achRoot, "slides", "codex", "pdf")

	stageSkill(t, achDir, "pdf", map[string]string{
		"SKILL.md": "---\nname: pdf\ndescription: y\n---\nbody",
	})

	d := &adapterDispatcherImpl{platformID: "fakeskills"}
	var result RenderResult
	if err := d.projectSkills(fakeSkillsAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectSkills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(toolRoot, ".claude", "skills", "pdf", "SKILL.md")); err != nil {
		t.Errorf("expected bare skills/pdf/SKILL.md for a different-platform claim; err=%v", err)
	}
}

// TestProjectSkills_PrefixIsSticky proves the name does not flap: once we
// project <env>-<name>, our own state keeps it prefixed even after the sibling
// Environment drops its copy (no foreign claim left).
func TestProjectSkills_PrefixIsSticky(t *testing.T) {
	achRoot := t.TempDir()
	toolRoot := t.TempDir()
	achDir := filepath.Join(achRoot, "images")

	stageSkill(t, achDir, "pdf", map[string]string{
		"SKILL.md": "---\nname: pdf\ndescription: y\n---\nbody",
	})

	prior := &state.File{SchemaVersion: "3", Environment: "images", Skills: []state.FileEntry{{
		Target: filepath.Join(".claude", "skills", "images-pdf", "SKILL.md"),
		Source: "images-pdf",
	}}}

	d := &adapterDispatcherImpl{platformID: "fakeskills"}
	var result RenderResult
	if err := d.projectSkills(fakeSkillsAdapter{}, prior, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectSkills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(toolRoot, ".claude", "skills", "images-pdf", "SKILL.md")); err != nil {
		t.Errorf("expected sticky prefixed skills/images-pdf/SKILL.md; err=%v", err)
	}
}
