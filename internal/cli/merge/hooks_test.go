// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"reflect"
	"testing"
)

func TestEnsureAndRemoveHookCommand(t *testing.T) {
	user := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo hi"}}}
	doc := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{user},
		"Stop":         []any{},
	}}
	ours := map[string]any{"type": "command", "command": "cat x"}
	for i, want := range []bool{true, false} {
		changed, err := EnsureHookCommand(doc, "SessionStart", ours)
		if err != nil || changed != want {
			t.Fatalf("ensure #%d: changed=%v err=%v", i, changed, err)
		}
	}
	if n := len(doc["hooks"].(map[string]any)["SessionStart"].([]any)); n != 2 {
		t.Fatalf("groups %d", n)
	}
	if changed, err := RemoveHookCommand(doc, "SessionStart", "cat x"); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	want := map[string]any{"hooks": map[string]any{"SessionStart": []any{user}, "Stop": []any{}}}
	if !reflect.DeepEqual(doc, want) {
		t.Fatalf("doc %v", doc)
	}
}

func TestRemoveHookCommand_DropsEmptiedContainers(t *testing.T) {
	doc := map[string]any{"other": 1}
	if _, err := EnsureHookCommand(doc, "SessionStart", map[string]any{"command": "cat x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveHookCommand(doc, "SessionStart", "cat x"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc, map[string]any{"other": 1}) {
		t.Fatalf("doc %v", doc)
	}
}

func TestHookCommand_RefusesForeignShapes(t *testing.T) {
	for _, doc := range []map[string]any{
		{"hooks": []any{}},
		{"hooks": map[string]any{"SessionStart": "x"}},
		{"hooks": map[string]any{"SessionStart": []any{"x"}}},
	} {
		if _, err := EnsureHookCommand(doc, "SessionStart", map[string]any{"command": "c"}); err == nil {
			t.Errorf("ensure accepted %v", doc)
		}
		if _, err := RemoveHookCommand(doc, "SessionStart", "c"); err == nil {
			t.Errorf("remove accepted %v", doc)
		}
	}
}
