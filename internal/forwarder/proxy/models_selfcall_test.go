// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// selfCallRe matches a string literal that starts with the forwarder's model
// list route — a real call, not a comment that mentions it.
var selfCallRe = regexp.MustCompile("[\"'`]/v1/models")

// TestNoInternalModelListWithoutTypesAll: GET /v1/models hides non-chat models
// by default (models.go) for chat clients like LibreChat. When ACH itself
// lists models through the forwarder (console, describe, hydrate…) it must
// see the whole catalog, so every such call in non-test source carries
// ?types=all on the same line.
func TestNoInternalModelListWithoutTypesAll(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	skipDir := map[string]bool{".git": true, ".gocache": true, "node_modules": true, "dist": true, "docs": true, "test": true}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		ext := filepath.Ext(name)
		if (ext != ".go" && ext != ".ts" && ext != ".tsx") ||
			strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") ||
			p == filepath.Join(root, "internal", "forwarder", "proxy", "models.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if selfCallRe.MatchString(line) && !strings.Contains(line, "types=all") {
				t.Errorf("%s:%d: internal /v1/models call without ?types=all: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
