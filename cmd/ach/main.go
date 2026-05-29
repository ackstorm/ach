// SPDX-License-Identifier: Apache-2.0

// `ach` is the single-binary entrypoint for both the ACH control plane
// services (operator / platform-api / forwarder / content-service /
// migrate) and the operator/developer CLI (login / hydrate / env /
// env-keys / config / admin — landing across Phase 6).
//
// Exit-code dispatch (CLI spec §9.3, Phase 6 D-16):
//
//   - cmd.Execute() runs the cobra root and returns the first error
//     bubbled up from a subcommand's RunE.
//   - *httpclient.ServerError (decoded §15.5 envelope from any HTTP
//     subcommand) maps to its §9.3 row via exit.MapServerError.
//   - *exit.CodedError (CLI-side typed error: mutex creds, synth
//     incompat, config parse) carries its Code directly.
//   - Anything else falls through to exit.General (1).
//
// All error rendering happens at this entry point so subcommands stay
// chi/cobra-style "return err"; only this main calls os.Exit. cobra's
// own argument-parse errors (e.g. "unknown command 'foo'") flow
// through the fallback branch verbatim — cobra has already printed
// them before returning.
package main

import (
	"os"

	"github.com/ackstorm/ach/cmd/ach/cmd"
	"github.com/ackstorm/ach/internal/cli/exit"
)

func main() {
	os.Exit(int(exit.DispatchAndRender(cmd.Execute(), os.Stderr)))
}
