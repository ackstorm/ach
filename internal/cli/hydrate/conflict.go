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

// String renders the flag spelling.
func (p ConflictPolicy) String() string {
	switch p {
	case ConflictNamespace:
		return "namespace"
	case ConflictSkip:
		return "skip"
	case ConflictOverwrite:
		return "overwrite"
	case ConflictRefuse:
		return "refuse"
	default:
		return "namespace"
	}
}

// ParseConflictPolicy maps the `--conflict` flag value to a policy.
// Empty -> ConflictNamespace (the default). Unknown -> error.
func ParseConflictPolicy(s string) (ConflictPolicy, error) {
	switch s {
	case "", "namespace":
		return ConflictNamespace, nil
	case "skip":
		return ConflictSkip, nil
	case "overwrite":
		return ConflictOverwrite, nil
	case "refuse":
		return ConflictRefuse, nil
	default:
		switch toLowerASCII(s) {
		case "namespace":
			return ConflictNamespace, nil
		case "skip":
			return ConflictSkip, nil
		case "overwrite":
			return ConflictOverwrite, nil
		case "refuse":
			return ConflictRefuse, nil
		}
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
