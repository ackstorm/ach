// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"fmt"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/conflict"
	"github.com/ackstorm/ach/internal/cli/namespace"
)

// ConflictPolicy mirrors the governed `env hydrate` --conflict policy (shared
// enum: internal/cli/conflict) so the two install paths behave consistently.
// NOTE the local installer is stateful and sequential: the FIRST-installed
// resource keeps its natural name; a LATER install that clashes is the one
// prefixed (we never retroactively rename an already-installed file). Only
// MergeReplace writes can clash: additive merges combine rather than clobber.
type ConflictPolicy = conflict.Policy

const (
	ConflictNamespace = conflict.Namespace
	ConflictSkip      = conflict.Skip
	ConflictOverwrite = conflict.Overwrite
	ConflictRefuse    = conflict.Refuse
)

// ParseConflictPolicy maps the `--conflict` flag value to a policy.
func ParseConflictPolicy(s string) (ConflictPolicy, error) { return conflict.Parse(s) }

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
func ResolveConflicts(writes []PlannedWrite, owners map[string]string, policy ConflictPolicy, plugin, root string) ([]PlannedWrite, []ConflictAction, error) {
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
			nw.Path = namespace.LeafAtRoot(root, w.Path, plugin)
			out = append(out, nw)
			actions = append(actions, ConflictAction{Policy: policy, Path: w.Path, NewPath: nw.Path, Owner: owner})
		}
	}
	return out, actions, nil
}
