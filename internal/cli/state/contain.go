// SPDX-License-Identifier: Apache-2.0

package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// ErrNotContained is returned when a workspace path, or a target tracked in
// a state file, leaves the root ACH may touch. A repository can commit .ach/
// and the agent config directories, symlinks and state files included, so
// neither is trusted.
var ErrNotContained = errors.New("state: path leaves the workspace")

// maxLinkHops matches the Linux limit on symlinks followed in one resolution.
const maxLinkHops = 40

// CheckContained verifies that path stays under root once every symlink on
// the way is followed. A relative root or path resolves against the working
// directory, as the file operation that follows would, so a path built from a
// relative --dir compares correctly. A symlink that stays under root is
// allowed, components that do not exist yet are fine, and a dangling link
// counts by where it points: opening one creates its target. The root itself
// may be reached through a symlink, being the user's own choice.
//
// Project scope only. --global callers skip it: the root is the user's own
// home, there is no repository, and dotfile managers legitimately link
// ~/.claude and the like out of it.
func CheckContained(root, path string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !isUnder(absRoot, absPath) {
		return fmt.Errorf("%w: %s is outside %s", ErrNotContained, path, root)
	}

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing on disk yet, so nothing to follow
	}
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return err
	}
	resolved, err := resolveLinks(realRoot, rel)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotContained, path, err)
	}
	if !isUnder(realRoot, resolved) {
		return fmt.Errorf("%w: %s resolves to %s", ErrNotContained, path, resolved)
	}
	return nil
}

// resolveLinks walks rel below dir with Lstat, expanding each symlink in
// place. filepath.EvalSymlinks stops at the first missing component, which
// would hide a dangling link. Everything past a missing component is taken
// as written.
func resolveLinks(dir, rel string) (string, error) {
	sep := string(filepath.Separator)
	pending := strings.Split(rel, sep)
	for hops := 0; len(pending) > 0; {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			dir = filepath.Dir(dir)
			continue
		}
		next := filepath.Join(dir, part)
		info, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			dir = next
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			dir = next
			continue
		}
		if hops++; hops > maxLinkHops {
			return "", errors.New("too many levels of symbolic links")
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			volume := filepath.VolumeName(target)
			dir, target = volume+sep, target[len(volume):]
		}
		pending = append(strings.Split(target, sep), pending...)
	}
	return dir, nil
}

func isUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

// ValidateTargets refuses a state file tracking a target that is absolute or
// climbs out of its base with "..", whatever its hash says. absRoots lists the
// directories an absolute target may live under: --global records a
// destination redirected by a tool's config-dir variable as absolute. Project
// scope passes none.
func ValidateTargets(f *File, absRoots ...string) error {
	for _, e := range WalkEntries(f) {
		ok := filepath.IsLocal(e.Target)
		if filepath.IsAbs(e.Target) {
			ok = slices.ContainsFunc(absRoots, func(root string) bool { return isUnder(root, e.Target) })
		}
		if !ok {
			return fmt.Errorf("%w: state file tracks %q", ErrNotContained, e.Target)
		}
	}
	return nil
}
