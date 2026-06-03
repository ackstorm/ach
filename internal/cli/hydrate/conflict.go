// SPDX-License-Identifier: Apache-2.0

// ConflictPolicy selects how the plugin projection leg resolves a
// cross-plugin destination collision (two plugins projecting to the same
// workspace path). Default `namespace` keeps both by leaf-prefixing;
// `refuse` reproduces the pre-Phase-1 CR-01 fail-fast.
package hydrate

import "fmt"

type ConflictPolicy int

const (
	// ConflictNamespace leaf-prefixes the colliding writes (<plugin>-<name>)
	// so both plugins' resources survive. Default.
	ConflictNamespace ConflictPolicy = iota
	// ConflictSkip drops the later-sorted plugin's colliding write, keeping
	// the first.
	ConflictSkip
	// ConflictOverwrite lets the later-sorted plugin's write win (last-wins).
	ConflictOverwrite
	// ConflictRefuse fails the hydrate on any cross-plugin collision
	// (the pre-Phase-1 CR-01 behavior).
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
// (case-insensitive). Empty -> ConflictNamespace (the default). Unknown ->
// error.
func ParseConflictPolicy(s string) (ConflictPolicy, error) {
	switch toLowerASCII(s) {
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

// toLowerASCII lowercases ASCII letters without importing strings (keep
// the package dependency surface minimal in this leaf file).
func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
