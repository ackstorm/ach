// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// selfCallRe matches a string literal naming the forwarder's model list route
// itself (not /v1/models/<id>) — a call, not a comment that mentions it.
var selfCallRe = regexp.MustCompile("[\"'`]/v1/models[?\"'`]")

// typesAllRe is the accepted form: types=all as a query param of that literal.
var typesAllRe = regexp.MustCompile("[\"'`]/v1/models\\?(?:[^\"'`]*&)?types=all(?:[&\"'`])")

// TestNoInternalModelListWithoutTypesAll guards FUTURE internal callers. Today
// no ACH view lists models through the forwarder (console, env describe and
// hydrate read LiteLLM or the projection directly), but GET /v1/models hides
// non-chat models by default (models.go), so any ACH-internal call to it must
// carry ?types=all in the same literal. Heuristic: only literal "/v1/models…"
// strings in tracked, non-test .go/.ts/.tsx source are seen; base+"/models",
// url.JoinPath and SDK calls are not caught.
func TestNoInternalModelListWithoutTypesAll(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	cmd := exec.Command("git", "ls-files", "*.go", "*.ts", "*.tsx")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	skipPrefix := []string{"test/", "docs/", "internal/platformapi/console/dist/", "internal/forwarder/proxy/models.go"}
	for _, f := range strings.Fields(string(out)) {
		skip := strings.HasSuffix(f, "_test.go") || strings.Contains(f, ".test.")
		for _, p := range skipPrefix {
			skip = skip || strings.HasPrefix(f, p)
		}
		if skip {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // tracked but deleted in the working tree
		}
		for i, line := range strings.Split(string(b), "\n") {
			if selfCallRe.MatchString(line) && !typesAllRe.MatchString(line) {
				t.Errorf("%s:%d: internal /v1/models call without ?types=all: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
