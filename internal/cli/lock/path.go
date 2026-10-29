// SPDX-License-Identifier: Apache-2.0

package lock

import "path/filepath"

// Path returns the canonical lock-file location inside `<ach-dir>`.
// The same `<ach-dir>` root is shared with state.json (CLI spec
// §6.7 / D-09 / STATE-06): one directory carries every
// per-workspace artifact the CLI owns, so a `rm -rf <ach-dir>` is a
// clean reset.
//
// Callers MUST pass an already-resolved `<ach-dir>` — Path does not
// expand `~`, env vars, or apply XDG fallback (the hydrate command's
// flag/env layer owns that resolution; see CLI spec §3.1).
func Path(achDir string) string {
	return filepath.Join(achDir, "lock")
}
