// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// TestPublishFile_UnownedReplaceTarget: a whole-file target that exists with
// other content and has no state row is the user's own file. publishFile
// refuses it with exit.CollisionRefuse and leaves it untouched; --force
// replaces it; a file that already holds the same content is adopted.
func TestPublishFile_UnownedReplaceTarget(t *testing.T) {
	fw := adapter.FileWrite{
		Path:    ".claude/agents/code-reviewer.md",
		Content: []byte("# from the plugin\n"),
		Merge:   adapter.MergeReplace,
	}
	seed := func(t *testing.T, body string) (root, abs string) {
		t.Helper()
		root = t.TempDir()
		abs = filepath.Join(root, filepath.FromSlash(fw.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, abs
	}
	const mine = "# my own agent\n"

	for name, d := range map[string]*adapterDispatcherImpl{
		"refused":         {platformID: "claude-code"},
		"refused dry-run": {platformID: "claude-code", dryRun: true},
	} {
		t.Run(name, func(t *testing.T) {
			root, abs := seed(t, mine)
			_, err := d.publishFile(fw, nil, root)
			var ce *exit.CodedError
			if !errors.As(err, &ce) || ce.Code != exit.CollisionRefuse {
				t.Fatalf("publishFile error = %v; want *exit.CodedError Code=CollisionRefuse", err)
			}
			if body, _ := os.ReadFile(abs); string(body) != mine {
				t.Errorf("on-disk content = %q; want the user's file untouched", body)
			}
		})
	}

	t.Run("force replaces", func(t *testing.T) {
		root, abs := seed(t, mine)
		d := &adapterDispatcherImpl{platformID: "claude-code", force: true}
		if _, err := d.publishFile(fw, nil, root); err != nil {
			t.Fatalf("publishFile --force: %v", err)
		}
		if body, _ := os.ReadFile(abs); string(body) != string(fw.Content) {
			t.Errorf("on-disk content = %q; want replaced", body)
		}
	})

	t.Run("identical content adopted", func(t *testing.T) {
		root, _ := seed(t, string(fw.Content))
		d := &adapterDispatcherImpl{platformID: "claude-code"}
		if _, err := d.publishFile(fw, nil, root); err != nil {
			t.Fatalf("publishFile over identical content: %v", err)
		}
	})
}
