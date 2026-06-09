// SPDX-License-Identifier: Apache-2.0

package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// ResolvePlatformPath is ResolvePath narrowed to ONE platform: it returns
// <ach-dir>/state-<platform>.json, the per-platform state file. The hydrate
// engine tracks each agent target in its own state file so a multi-target
// hydrate (`--target codex,opencode`) does not let one platform's render
// overwrite another's projection bucket (step12WriteState replaces buckets
// wholesale). <ach-dir> stays the per-environment dir (filepath.Dir of the
// returned path == filepath.Dir(ResolvePath(...))), so the platform-independent
// content cache (prompt/, artifact/, plugin/) is shared across targets.
//
// platform MUST be a single non-empty path segment (no separators, not "."/"..");
// callers pass a canonical adapter id from hydrate.ResolvePlatform.
func ResolvePlatformPath(workspaceCwd, environment, platform string, global bool) (string, error) {
	if err := validatePlatformSegment(platform); err != nil {
		return "", err
	}
	base, err := ResolvePath(workspaceCwd, environment, global)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), "state-"+platform+".json"), nil
}

// ListStatePaths enumerates every state file for an environment: each
// per-platform state-<platform>.json plus a legacy flat state.json if present
// (pre-per-platform layout — still tracked so `env status`/`env uninstall` see
// it). Returns absolute paths sorted for determinism; an absent env dir yields
// an empty slice (not an error).
func ListStatePaths(workspaceCwd, environment string, global bool) ([]string, error) {
	base, err := ResolvePath(workspaceCwd, environment, global)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(base)
	matches, err := filepath.Glob(filepath.Join(dir, "state-*.json"))
	if err != nil {
		return nil, err
	}
	out := append([]string{}, matches...)
	// Include the legacy flat state.json (no platform suffix) when present.
	if _, statErr := os.Stat(base); statErr == nil {
		out = append(out, base)
	}
	sort.Strings(out)
	return out, nil
}

// validatePlatformSegment rejects a platform value that is not a single, safe
// path segment — defense-in-depth against a crafted id reaching the filename.
func validatePlatformSegment(platform string) error {
	if platform == "" {
		return fmt.Errorf("%w: a non-empty platform is required for a per-platform state path", ErrInvalidPath)
	}
	if platform == "." || platform == ".." ||
		strings.ContainsAny(platform, `/\`) || platform != filepath.Base(platform) {
		return fmt.Errorf("%w: invalid platform segment %q", ErrInvalidPath, platform)
	}
	return nil
}
