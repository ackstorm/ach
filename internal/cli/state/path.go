// SPDX-License-Identifier: Apache-2.0

package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolvePath computes the canonical <ach-dir>/state.json location per
// CLI spec §8.1:
//
//   - Workspace scope (global=false): <workspaceCwd>/.ach/state.json.
//     workspaceCwd is the directory `ach-cli hydrate` was invoked from
//     (the caller resolves it via os.Getwd at the cobra layer); this
//     function does not call os.Getwd itself so tests can inject any
//     path without chdir.
//
//   - Global scope (global=true): $HOME/.ach/<environment>/state.json.
//     The environment string MUST be non-empty in global scope (it
//     namespaces the directory by Environment per §8.1), so an empty
//     environment in global scope returns ErrInvalidPath.
//
// Both branches keep <ach-dir>'s sibling artifacts (lock, tmp/,
// prompts/, artifacts/) at the same parent — the §8.7 atomic-rename
// invariant assumes state.json and its tmp-staged sibling live on the
// same filesystem. Callers should not separately resolve the parent
// directory; <ach-dir> is filepath.Dir(returned path).
func ResolvePath(workspaceCwd, environment string, global bool) (string, error) {
	if global {
		if environment == "" {
			return "", fmt.Errorf("%w: global scope requires a non-empty environment", ErrInvalidPath)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".ach", environment, "state.json"), nil
	}

	if workspaceCwd == "" {
		return "", fmt.Errorf("%w: workspace scope requires a non-empty workspaceCwd", ErrInvalidPath)
	}
	return filepath.Join(workspaceCwd, ".ach", "state.json"), nil
}
