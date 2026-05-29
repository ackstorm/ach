// SPDX-License-Identifier: Apache-2.0

// Blank-import the 4 v1alpha1 closed-set adapter subpackages so each
// init() side-effect Register call fires before main() reaches the
// hydrate engine's adapter.Lookup / adapter.Iter dispatch points.
//
// This file's only purpose is the registration loading — keeping it
// separate from hydrate.go avoids polluting the cobra wiring file
// with imports it never uses by name.
//
// ADAPT-01 (CONTEXT.md / spec §7.2): the closed set is fixed for
// v1alpha1. Adding a 5th adapter requires a new subpackage AND a
// new line here; removing requires updating the autodetect message
// constants. Both are deliberate friction surfaces — the closed-set
// posture is the contract.

package cmd

import (
	_ "github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	_ "github.com/ackstorm/ach/internal/cli/adapter/codex"
	_ "github.com/ackstorm/ach/internal/cli/adapter/gemini"
	_ "github.com/ackstorm/ach/internal/cli/adapter/opencode"
)
