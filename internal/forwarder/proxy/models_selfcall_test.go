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

// selfCallRe matches the forwarder's model list route inside a literal: after
// a quote, a template `}` or a Sprintf `%s`, with an optional trailing slash
// (the forwarder trims it). /v1/models/<id> is a different route and not matched.
var selfCallRe = regexp.MustCompile("[\"'`}s]/v1/models(?:/?[\"'`]|\\?)")

// typesAllRe is the accepted form: types=all (any case, alone or in a comma
// list) as a query param of the same literal.
var typesAllRe = regexp.MustCompile("(?i)[?&]types=(?:[^&,]*,)*all(?:[,&]|$)")

// TestNoInternalModelListWithoutTypesAll guards FUTURE internal callers. Today
// no ACH view lists models through the forwarder (console, env describe and
// hydrate read LiteLLM or the projection directly), but GET /v1/models hides
// non-chat models by default (models.go), so any ACH-internal call to it must
// carry ?types=all in the same literal. Heuristic: only literal "/v1/models…"
// strings in tracked, non-test .go/.ts/.tsx source are seen; base+"/models",
// url.JoinPath and SDK calls are not caught.
func TestNoInternalModelListWithoutTypesAll(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	cmd := exec.Command("git", "ls-files", "-z", "*.go", "*.ts", "*.tsx")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	skipPrefix := []string{"test/", "docs/", "internal/platformapi/console/dist/", "internal/forwarder/proxy/models.go"}
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
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
			for _, m := range selfCallRe.FindAllStringIndex(line, -1) {
				lit := line[m[0]+1 : m[1]]
				if strings.HasSuffix(lit, "?") {
					rest := line[m[1]:]
					if j := strings.IndexAny(rest, "\"'`"); j >= 0 {
						rest = rest[:j]
					}
					lit += rest
				}
				if !typesAllRe.MatchString(lit) {
					t.Errorf("%s:%d: internal /v1/models call without ?types=all: %s", f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
