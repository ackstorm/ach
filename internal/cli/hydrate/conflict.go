// SPDX-License-Identifier: Apache-2.0

package hydrate

import "github.com/ackstorm/ach/internal/cli/conflict"

// ConflictPolicy selects how the plugin projection leg resolves a
// cross-plugin destination collision (two plugins projecting to the same
// workspace path). Alias of the shared internal/cli/conflict enum. Default
// `namespace` keeps both by leaf-prefixing; `refuse` reproduces the
// pre-Phase-1 CR-01 fail-fast.
type ConflictPolicy = conflict.Policy

const (
	ConflictNamespace = conflict.Namespace
	ConflictSkip      = conflict.Skip
	ConflictOverwrite = conflict.Overwrite
	ConflictRefuse    = conflict.Refuse
)

// ParseConflictPolicy maps the `--conflict` flag value to a policy.
func ParseConflictPolicy(s string) (ConflictPolicy, error) { return conflict.Parse(s) }
