// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"reflect"
	"testing"
)

func TestEnsureAndRemoveArrayEntry(t *testing.T) {
	doc := map[string]any{"instructions": []any{"CONTRIBUTING.md"}, "mcp": map[string]any{}}
	if ch, err := EnsureArrayEntry(doc, "instructions", "x/*.md"); err != nil || !ch {
		t.Fatalf("ensure: %v %v", ch, err)
	}
	if ch, _ := EnsureArrayEntry(doc, "instructions", "x/*.md"); ch {
		t.Fatal("second ensure must be a no-op")
	}
	if !reflect.DeepEqual(doc["instructions"], []any{"CONTRIBUTING.md", "x/*.md"}) {
		t.Fatalf("got %v", doc["instructions"])
	}
	if ch, err := RemoveArrayEntry(doc, "instructions", "x/*.md"); err != nil || !ch {
		t.Fatalf("remove: %v %v", ch, err)
	}
	if !reflect.DeepEqual(doc["instructions"], []any{"CONTRIBUTING.md"}) {
		t.Fatalf("user entry lost: %v", doc["instructions"])
	}
	only := map[string]any{"instructions": []any{"x/*.md"}}
	_, _ = RemoveArrayEntry(only, "instructions", "x/*.md")
	if _, ok := only["instructions"]; ok {
		t.Fatal("an emptied array key is deleted")
	}
	bad := map[string]any{"instructions": "CONTRIBUTING.md"}
	if _, err := EnsureArrayEntry(bad, "instructions", "x/*.md"); err == nil {
		t.Fatal("a non-array value must be an error, never overwritten")
	}
}
