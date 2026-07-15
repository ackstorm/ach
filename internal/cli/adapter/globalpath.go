// SPDX-License-Identifier: Apache-2.0

package adapter

import "strings"

// Opencode's global-scope config root is XDG (~/.config/opencode/), not
// ~/.opencode/. Both install paths (env hydrate + local install) remap
// at commit time so RenderRuntime/ProjectionRules stay scope-agnostic.
const (
	opencodeProjectPrefix = ".opencode/"
	opencodeGlobalPrefix  = ".config/opencode/"
)

// RemapGlobalPath adjusts a workspace-relative path for --global scope.
// Pure prefix substitution on an already-traversal-guarded relative path
// (T-01-01 ran upstream), so no ".." can be reintroduced (T-03-02).
func RemapGlobalPath(adapterID, path string) string {
	if adapterID == "opencode" && strings.HasPrefix(path, opencodeProjectPrefix) {
		return opencodeGlobalPrefix + strings.TrimPrefix(path, opencodeProjectPrefix)
	}
	return path
}
