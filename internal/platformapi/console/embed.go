// SPDX-License-Identifier: Apache-2.0

package console

import "embed"

// dist is the Vite build output. "all:" keeps .gitkeep embeddable so a
// checkout without a UI build still compiles; SPA then answers 404
// "console not built" instead of failing at go build.
//
//go:embed all:dist
var dist embed.FS

// distDir is the directory dist is rooted at.
const distDir = "dist"
