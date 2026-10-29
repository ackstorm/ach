// SPDX-License-Identifier: Apache-2.0

// `ach-cli` is the user-facing client binary for the ACH control plane.
// Sibling to `ach` (services-only). Exit-code dispatch is shared via
// internal/cli/exit.DispatchAndRender per CLI spec §9.3.
package main

import (
	"os"

	"github.com/ackstorm/ach/cmd/ach-cli/cmd"
	"github.com/ackstorm/ach/internal/cli/exit"
)

func main() {
	os.Exit(int(exit.DispatchAndRender(cmd.Execute(), os.Stderr)))
}
