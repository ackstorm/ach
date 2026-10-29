// SPDX-License-Identifier: Apache-2.0

// Package skillstage provides the shared, k8s-free staging primitive that
// nests an extracted standalone Skill under a synthetic skills/<name>/ tree so
// the adapter projection rule (`skills/**/* → .claude/skills/**/*`) — which
// classifies on the FIRST path element — fires.
//
// Both the hydrate engine (internal/cli/hydrate) and the local-package manager
// (internal/cli/localpkg/manager) stage skills this way; the logic lives here
// once to keep the two install paths byte-for-byte identical.
package skillstage

import (
	"fmt"
	"os"
	"path/filepath"
)

// ContentDir locates the directory under an extracted skill's nameDir that
// holds SKILL.md at its root, stripping at most one wrapper directory.
//
// VerifySkillContents accepts SKILL.md at the tar root OR one directory deep
// (REST repo-archive fetches nest everything under "<repo>-<sha>/"), so a
// fetched skill extracts to either nameDir/SKILL.md or nameDir/<wrapper>/SKILL.md.
// Returns (nameDir, true) when SKILL.md is already at the root, (<wrapper>, true)
// when it sits exactly one level deep, and ("", false) when absent at both.
func ContentDir(nameDir string) (contentDir string, ok bool, err error) {
	if st, statErr := os.Stat(filepath.Join(nameDir, "SKILL.md")); statErr == nil && !st.IsDir() {
		return nameDir, true, nil
	}
	entries, err := os.ReadDir(nameDir)
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(nameDir, e.Name())
		if st, serr := os.Stat(filepath.Join(sub, "SKILL.md")); serr == nil && !st.IsDir() {
			return sub, true, nil
		}
	}
	return "", false, nil
}

// Nest rebases the skill content held under extractedDir onto
// stageRoot/skills/<name>/ so the adapter's first-path-element projection
// classifier matches the `skills/**` rule.
//
// It locates the SKILL.md-bearing content directory via ContentDir (handling
// the at-most-one REST-archive wrapper); when none is found it returns
// (false, nil) — the caller decides whether that is an error. Otherwise it
// creates stageRoot/skills and renames the content directory into place,
// returning (true, nil).
//
// Name validation is the CALLER's responsibility: name is joined into the
// destination path, so callers MUST validate it as a safe single path segment
// before calling Nest.
func Nest(stageRoot, name, extractedDir string) (nested bool, err error) {
	contentDir, ok, err := ContentDir(extractedDir)
	if err != nil {
		return false, fmt.Errorf("skillstage: inspect skill %q: %w", name, err)
	}
	if !ok {
		return false, nil
	}
	skillsDir := filepath.Join(stageRoot, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return false, fmt.Errorf("skillstage: stage skills dir %s: %w", skillsDir, err)
	}
	if err := os.Rename(contentDir, filepath.Join(skillsDir, name)); err != nil {
		return false, fmt.Errorf("skillstage: nest skill %q under skills/: %w", name, err)
	}
	return true, nil
}
