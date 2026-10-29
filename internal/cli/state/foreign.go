// SPDX-License-Identifier: Apache-2.0

package state

import (
	"os"
	"path/filepath"
)

// ForeignSkillNames reports the projected skill names that OTHER environments
// hydrated into the same workspace already own for one platform, mapped to the
// owning environment name.
//
// Two Environments hydrated into one project UNION in the same .claude/ (see
// ResolvePath) — deliberate, and the reason specialist environments compose.
// But a skill carrying the same name in both resolves to the SAME destination
// directory, so the second hydrate silently overwrites the first and the two
// flip-flop on every run. The hydrate engine consults this set to env-prefix
// the later skill instead, leaving the incumbent's name untouched.
//
// achDir is the CURRENT environment's <ach-dir> (…/.ach/<env>): its parent is
// the shared .ach root and its base is the environment to exclude. Only the
// per-platform state-<platform>.json is read — a name claimed for a different
// target is not a collision here. A sibling state file that cannot be read or
// parsed (corrupt, older schema) is SKIPPED rather than failing the hydrate;
// it contributes no claims. Directory order is os.ReadDir's (sorted), so the
// first claimant of a duplicated name wins deterministically.
func ForeignSkillNames(achDir, platform string) map[string]string {
	entries, err := os.ReadDir(filepath.Dir(achDir))
	if err != nil {
		return nil
	}
	self := filepath.Base(achDir)
	owners := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == self {
			continue
		}
		f, err := Load(filepath.Join(filepath.Dir(achDir), e.Name(), "state-"+platform+".json"))
		if err != nil || f == nil {
			continue
		}
		for _, sk := range f.Skills {
			if _, taken := owners[sk.Source]; sk.Source != "" && !taken {
				owners[sk.Source] = e.Name()
			}
		}
	}
	return owners
}
