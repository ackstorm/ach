// SPDX-License-Identifier: Apache-2.0

package hydrate

// ValidatePluginName re-exports the unexported validatePluginName guard so
// the external hydrate_test package (projection_collision_test.go) can
// exercise the plugin-name segment guard (T-01-04) directly with the
// traversal/absolute/multi-segment vectors that os.ReadDir would never
// surface as a real dirent — proving the defense-in-depth contract, not
// just the on-disk attack vector already closed by SAFE-01/02.
var ValidatePluginName = validatePluginName
