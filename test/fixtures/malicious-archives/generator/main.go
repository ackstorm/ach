// SPDX-License-Identifier: Apache-2.0

// Generator is a thin wrapper that materializes the SAFE-01
// malicious-archive fixture set to a target directory for manual
// inspection / debugging. The Phase 7 extract unit tests do NOT
// invoke this binary — they import the maliciousfixtures package and
// call BuildAll(t.TempDir()) per test. This main exists for humans
// who want the fixtures on disk to feed an external tar-safety
// verifier or to byte-diff against a different implementation.
//
// Usage:
//
//	./scripts/dev.sh go run ./test/fixtures/malicious-archives/generator \
//	    ./test/fixtures/malicious-archives/
//
// The output directory is positional; defaults to the current cwd.
// Fixtures are byte-stable across runs (deterministic UID/GID/ModTime).
package main

import (
	"fmt"
	"os"

	maliciousfixtures "github.com/ackstorm/ach/test/fixtures/malicious-archives"
)

func main() {
	dir := "."
	if len(os.Args) >= 2 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "generator: mkdir %s: %v\n", dir, err)
		os.Exit(1)
	}
	out, err := maliciousfixtures.BuildAll(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generator: BuildAll: %v\n", err)
		os.Exit(1)
	}
	for _, name := range maliciousfixtures.Names {
		fmt.Println(out[name])
	}
}
