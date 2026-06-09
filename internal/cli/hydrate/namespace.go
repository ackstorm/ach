// SPDX-License-Identifier: Apache-2.0

package hydrate

import "github.com/ackstorm/ach/internal/cli/namespace"

// namespaceLeaf delegates to the shared namespace.Leaf scheme so the governed
// hydrate projection leg and the local package manager de-collide identically
// (<plugin>-<name>). See internal/cli/namespace for the rule.
func namespaceLeaf(p, plugin string) string {
	return namespace.Leaf(p, plugin)
}
