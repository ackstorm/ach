// SPDX-License-Identifier: Apache-2.0

package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolvePath computes the canonical <ach-dir>/state.json location per
// CLI spec §8.1. BOTH scopes namespace the directory by Environment so a
// single project (or $HOME) can hold multiple Environments side-by-side
// without their caches/state colliding:
//
//   - Workspace scope (global=false): <workspaceCwd>/.ach/<environment>/state.json.
//     workspaceCwd is the directory `ach-cli hydrate` was invoked from
//     (the caller resolves it via os.Getwd at the cobra layer); this
//     function does not call os.Getwd itself so tests can inject any
//     path without chdir.
//
//   - Global scope (global=true): $HOME/.ach/<environment>/state.json.
//
// The environment string MUST be non-empty in BOTH scopes — it is the
// per-environment namespace segment. An empty environment returns
// ErrInvalidPath (the caller layer gates --environment before reaching the
// engine; see cmd/ach-cli/cmd/hydrate.go).
//
// NOTE: only the <ach-dir> (cache + state.json) is per-environment. The
// adapter-native projection (toolRoot: <workspaceCwd>/.claude, …, or $HOME
// under --global) is single-path by construction — agents read fixed config
// locations — so two Environments hydrated into one project UNION in the
// same .claude/ (each surgically tracked + independently uninstallable). The
// §8.3 environment guard still protects any shared <ach-dir>.
//
// Both branches keep <ach-dir>'s sibling artifacts (lock, tmp/, runtime/,
// prompt/, plugin/, artifact/) at the same parent — the §8.7 atomic-rename
// invariant assumes state.json and its tmp-staged sibling live on the same
// filesystem. Callers should not separately resolve the parent directory;
// <ach-dir> is filepath.Dir(returned path).
func ResolvePath(workspaceCwd, environment string, global bool) (string, error) {
	if environment == "" {
		return "", fmt.Errorf("%w: a non-empty environment is required (state is namespaced by environment per spec §8.1)", ErrInvalidPath)
	}
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".ach", environment, "state.json"), nil
	}

	if workspaceCwd == "" {
		return "", fmt.Errorf("%w: workspace scope requires a non-empty workspaceCwd", ErrInvalidPath)
	}
	return filepath.Join(workspaceCwd, ".ach", environment, "state.json"), nil
}
