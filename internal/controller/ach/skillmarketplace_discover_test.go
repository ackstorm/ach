// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"bytes"
	"sort"
	"testing"
)

func TestDiscoverSkillsInTree(t *testing.T) {
	entries := map[string]string{
		"pdf-processing/SKILL.md":       "---\nname: pdf-processing\ndescription: pdf things\n---\nbody",
		"pdf-processing/scripts/run.py": "print('x')",
		"data-analysis/SKILL.md":        "---\nname: data-analysis\ndescription: data things\n---\nbody",
		"README.md":                     "top-level readme, not a skill",
		"broken/SKILL.md":               "---\nname: mismatch\ndescription: name != dir\n---\n",
		"nodesc/SKILL.md":               "---\nname: nodesc\n---\nno description",
		"Bad_Name/SKILL.md":             "---\nname: Bad_Name\ndescription: invalid name\n---\n",
	}
	want := []string{"data-analysis", "pdf-processing"} // others excluded

	// Case A: git-transport layout (root-relative).
	// Case B: REST-archive layout wrapped in "<repo>-<sha>/".
	for _, tc := range []struct {
		label string
		wrap  string
	}{{"git", ""}, {"rest-wrapped", "myrepo-abc1234/"}} {
		m := map[string]string{}
		for k, v := range entries {
			m[tc.wrap+k] = v
		}
		tree := skillTarGz(t, m)
		root, got, err := discoverSkillsInTree(tree, "")
		if err != nil {
			t.Fatalf("%s discover: %v", tc.label, err)
		}
		if tc.wrap == "" && root != "" || tc.wrap != "" && root != "myrepo-abc1234" {
			t.Errorf("%s archiveRoot = %q", tc.label, root)
		}
		var names []string
		for _, s := range got {
			names = append(names, s.Name)
		}
		sort.Strings(names)
		if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
			t.Errorf("%s discovered = %v, want %v", tc.label, names, want)
		}
	}
}

func TestSliceSkillSubtree(t *testing.T) {
	entries := map[string]string{
		"myrepo-abc/pdf-processing/SKILL.md":       "---\nname: pdf-processing\ndescription: x\n---\nb",
		"myrepo-abc/pdf-processing/scripts/run.py": "print('x')",
		"myrepo-abc/data-analysis/SKILL.md":        "---\nname: data-analysis\ndescription: y\n---\nb",
	}
	tree := skillTarGz(t, entries)
	root, _, err := discoverSkillsInTree(tree, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	sub, err := sliceSkillSubtree(tree, root, "pdf-processing")
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	// Sliced archive is rooted at "pdf-processing/" (wrapper stripped) and MUST
	// validate via verifySkillContents.
	if err := verifySkillContents(bytes.NewReader(sub)); err != nil {
		t.Errorf("sliced subtree failed verifySkillContents: %v", err)
	}
}

// TestDiscoverSkillsInTree_SubPath exercises the anthropics/skills layout:
// skills nested under a "skills/" dir inside a REST-wrapped archive. Discovery
// with subPath="skills" must find them; the per-skill slice (subPath/<dir>)
// re-roots at the skill name and validates.
func TestDiscoverSkillsInTree_SubPath(t *testing.T) {
	entries := map[string]string{
		"anthropics-skills-deadbee/skills/pdf/SKILL.md":        "---\nname: pdf\ndescription: pdf things\n---\nb",
		"anthropics-skills-deadbee/skills/pdf/scripts/run.py":  "print('x')",
		"anthropics-skills-deadbee/skills/docx/SKILL.md":       "---\nname: docx\ndescription: docx things\n---\nb",
		"anthropics-skills-deadbee/README.md":                  "top-level readme",
		"anthropics-skills-deadbee/.claude-plugin/plugin.json": "{}",
	}
	tree := skillTarGz(t, entries)
	root, got, err := discoverSkillsInTree(tree, "skills")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if root != "anthropics-skills-deadbee" {
		t.Errorf("archiveRoot = %q", root)
	}
	names := []string{}
	for _, s := range got {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "docx" || names[1] != "pdf" {
		t.Fatalf("discovered = %v, want [docx pdf]", names)
	}
	// Slice the pdf skill (subPath/<dir>) → re-rooted at "pdf/", validates.
	sub, err := sliceSkillSubtree(tree, root, "skills/pdf")
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	if err := verifySkillContents(bytes.NewReader(sub)); err != nil {
		t.Errorf("sliced subPath subtree failed verifySkillContents: %v", err)
	}
}
