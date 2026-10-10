// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// selfCallRe matches the forwarder's model list route inside a literal: the
// path, an optional trailing slash (the forwarder trims it), then a closing
// quote or a query. No prefix class: any base (host:port, %v, a template) may
// precede it. /v1/models/<id> is a different route and not matched.
var selfCallRe = regexp.MustCompile("/v1/models(?:/?[\"'`]|\\?)")

// isSubPath reports whether the /v1/models at line[at:] hangs under another
// path segment ("/agents/v1/models"), not a host, port or format verb. A
// "//" before the word means a URL host ("https://host/v1/models").
func isSubPath(line string, at int) bool {
	j := at
	for j > 0 && (line[j-1] == '-' || line[j-1] == '_' || line[j-1] >= '0' && line[j-1] <= '9' ||
		line[j-1] >= 'a' && line[j-1] <= 'z' || line[j-1] >= 'A' && line[j-1] <= 'Z') {
		j--
	}
	return j < at && j > 0 && line[j-1] == '/' && (j < 2 || line[j-2] != '/')
}

// badSelfCall reports whether line holds a /v1/models literal that the
// forwarder would filter: no ?types=, or a first ?types= without "all"
// (prepareModelList reads only the first, with wantsAllModels).
func badSelfCall(line string) bool {
	for _, m := range selfCallRe.FindAllStringIndex(line, -1) {
		if isSubPath(line, m[0]) {
			continue
		}
		if !strings.HasSuffix(line[:m[1]], "?") {
			return true
		}
		rest := line[m[1]:]
		if j := strings.IndexAny(rest, "\"'`"); j >= 0 {
			rest = rest[:j]
		}
		q, _ := url.ParseQuery(rest)
		if !wantsAllModels(q.Get("types")) {
			return true
		}
	}
	return false
}

func TestBadSelfCall(t *testing.T) {
	for line, want := range map[string]bool{
		`"http://ach-forwarder:8080/v1/models"`: true,
		`"https://ach.e2e.local/v1/models"`:     true,
		`fmt.Sprintf("%v/v1/models", base)`:     true,
		"`${base}/v1/models`":                   true,
		`"/v1/models/"`:                         true,
		`"/v1/models?types=chat"`:               true,
		`"/v1/models?types=chat&types=all"`:     true,
		`"/agents/v1/models"`:                   false,
		`"/apis/v1/models"`:                     false,
		`"/v1/models?types=all"`:                false,
		`"/v1/models?x=1&types=chat,%20all"`:    false,
		`"https://host/v1/models?types=ALL"`:    false,
		`"/v1/models/gpt-4"`:                    false,
	} {
		if got := badSelfCall(line); got != want {
			t.Errorf("badSelfCall(%s) = %v, want %v", line, got, want)
		}
	}
}

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
	files := strings.Split(string(out), "\x00")
	if !slices.Contains(files, "internal/forwarder/proxy/models.go") {
		t.Fatalf("git ls-files did not list models.go: scanned nothing useful (%d files)", len(files))
	}
	skipPrefix := []string{"test/", "docs/", "internal/platformapi/console/dist/", "internal/forwarder/proxy/models.go"}
	for _, f := range files {
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
			if badSelfCall(line) {
				t.Errorf("%s:%d: internal /v1/models call without ?types=all: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
