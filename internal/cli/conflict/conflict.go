// SPDX-License-Identifier: Apache-2.0

// Package conflict holds the shared --conflict policy enum used by both the
// governed `env hydrate` projection leg (internal/cli/hydrate) and the local
// package manager (internal/cli/localpkg/manager) so the two install paths
// accept the same spellings and default identically.
package conflict

import (
	"fmt"
	"strings"
)

// Policy selects how a destination collision is resolved.
type Policy int

const (
	// Namespace leaf-prefixes the colliding write (<plugin>-<name>) so both
	// resources survive. Default.
	Namespace Policy = iota
	// Skip drops the later/clashing write, keeping the first.
	Skip
	// Overwrite lets the later write win (last-wins).
	Overwrite
	// Refuse fails the operation on any collision.
	Refuse
)

// Flag spellings for each policy (the `--conflict` values).
const (
	namespaceStr = "namespace"
	skipStr      = "skip"
	overwriteStr = "overwrite"
	refuseStr    = "refuse"
)

// String renders the flag spelling.
func (p Policy) String() string {
	switch p {
	case Skip:
		return skipStr
	case Overwrite:
		return overwriteStr
	case Refuse:
		return refuseStr
	default:
		return namespaceStr
	}
}

// Parse maps the `--conflict` flag value to a policy (case-insensitive).
// Empty → Namespace (the default). Unknown → error.
func Parse(s string) (Policy, error) {
	switch strings.ToLower(s) {
	case "", namespaceStr:
		return Namespace, nil
	case skipStr:
		return Skip, nil
	case overwriteStr:
		return Overwrite, nil
	case refuseStr:
		return Refuse, nil
	default:
		return Namespace, fmt.Errorf("invalid --conflict %q; want namespace|skip|overwrite|refuse", s)
	}
}
