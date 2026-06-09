// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"fmt"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/namespace"
)

// ConflictPolicy selects how a local install resolves a destination clash — a
// planned write whose target path is already owned by a DIFFERENT installed ref
// at the same target (tracked in installed.json). It mirrors the governed
// `env hydrate` --conflict policy (same spellings, same default) so the two
// install paths behave consistently.
//
// Only MergeReplace writes can clash: additive merges (MergeDeep keyed JSON/TOML,
// MergeComposite marker blocks) combine with the existing content rather than
// clobber it, so they are never treated as conflicts.
type ConflictPolicy int

const (
	// ConflictNamespace leaf-prefixes the colliding write (<plugin>-<name>) so
	// both resources survive. Default. NOTE the local installer is stateful and
	// sequential: the FIRST-installed resource keeps its natural name; a LATER
	// install that clashes is the one prefixed (we never retroactively rename an
	// already-installed file).
	ConflictNamespace ConflictPolicy = iota
	// ConflictSkip drops the clashing write, keeping the already-installed file.
	ConflictSkip
	// ConflictOverwrite lets the new write win (last-wins); the caller surfaces
	// the clobber via collisionWarn.
	ConflictOverwrite
	// ConflictRefuse aborts the install on any clash.
	ConflictRefuse
)

// Flag spellings for each policy (the `--conflict` values).
const (
	conflictNamespaceStr = "namespace"
	conflictSkipStr      = "skip"
	conflictOverwriteStr = "overwrite"
	conflictRefuseStr    = "refuse"
)

// String renders the flag spelling.
func (p ConflictPolicy) String() string {
	switch p {
	case ConflictSkip:
		return conflictSkipStr
	case ConflictOverwrite:
		return conflictOverwriteStr
	case ConflictRefuse:
		return conflictRefuseStr
	case ConflictNamespace:
		return conflictNamespaceStr
	default:
		return conflictNamespaceStr
	}
}

// ParseConflictPolicy maps the `--conflict` flag value to a policy
// (case-insensitive). Empty → ConflictNamespace (the default). Unknown → error.
func ParseConflictPolicy(s string) (ConflictPolicy, error) {
	switch lowerASCII(s) {
	case "", conflictNamespaceStr:
		return ConflictNamespace, nil
	case conflictSkipStr:
		return ConflictSkip, nil
	case conflictOverwriteStr:
		return ConflictOverwrite, nil
	case conflictRefuseStr:
		return ConflictRefuse, nil
	default:
		return ConflictNamespace, fmt.Errorf("invalid --conflict %q; want namespace|skip|overwrite|refuse", s)
	}
}

// ConflictAction records one resolved clash for the caller to report.
type ConflictAction struct {
	Policy  ConflictPolicy
	Path    string // the original (clashing) destination path
	NewPath string // the namespaced destination (ConflictNamespace only; else "")
	Owner   string // the ref that already owns Path
}

// ResolveConflicts applies policy to writes given owners (target-relative path →
// owning ref) for OTHER installed refs at this target. It returns the writes to
// actually commit and the list of actions taken (for reporting). A clash under
// ConflictRefuse returns an error and no writes.
//
// Non-clashing writes and non-MergeReplace writes (additive merges) always pass
// through unchanged and record no action.
func ResolveConflicts(writes []PlannedWrite, owners map[string]string, policy ConflictPolicy, plugin string) ([]PlannedWrite, []ConflictAction, error) {
	out := make([]PlannedWrite, 0, len(writes))
	var actions []ConflictAction

	for _, w := range writes {
		owner, clashes := owners[w.Path]
		if !clashes || w.Merge != adapter.MergeReplace {
			out = append(out, w)
			continue
		}
		switch policy {
		case ConflictOverwrite:
			// Last-wins; collisionWarn surfaces the clobber post-commit.
			out = append(out, w)
		case ConflictSkip:
			actions = append(actions, ConflictAction{Policy: policy, Path: w.Path, Owner: owner})
		case ConflictRefuse:
			return nil, nil, fmt.Errorf("%s already owned by %s (--conflict refuse)", w.Path, owner)
		case ConflictNamespace:
			nw := w
			nw.Path = namespace.Leaf(w.Path, plugin)
			out = append(out, nw)
			actions = append(actions, ConflictAction{Policy: policy, Path: w.Path, NewPath: nw.Path, Owner: owner})
		}
	}
	return out, actions, nil
}

// lowerASCII lowercases ASCII letters (avoids importing strings in this leaf).
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
