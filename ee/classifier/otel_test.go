// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import (
	"reflect"
	"testing"
)

// Synthetic fixtures for the three measured input shapes. No real content.
func TestToOTelShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		raw       string
		person    []string
		toolNames []string
		roles     []string
	}{
		"openai chat": {
			raw: `[{"role":"system","content":"sys"},{"role":"user","content":"fix the pod"},
				{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"shell","arguments":"{\"command\":\"kubectl get pods\"}"}}]},
				{"role":"tool","tool_call_id":"c1","content":"pod-a Running"},{"role":"assistant","content":"done"}]`,
			person: []string{"fix the pod"}, toolNames: []string{"shell"},
			roles: []string{"system", "user", "assistant", "tool", "assistant"},
		},
		"openai responses": {
			raw: `[{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev"}]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]},
				{"type":"function_call","call_id":"f1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
				{"type":"function_call_output","call_id":"f1","output":"a b"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"two files"}]}]`,
			person: []string{"list files"}, toolNames: []string{"exec_command"},
			roles: []string{"system", "user", "assistant", "tool", "assistant"},
		},
		"anthropic": {
			raw: `[{"role":"system","content":"sys"},{"role":"user","content":[{"type":"text","text":"edit the readme"}]},
				{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"README.md"}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]`,
			person: []string{"edit the readme"}, toolNames: []string{"Edit"},
			roles: []string{"system", "user", "assistant", "user"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := ToOTel([]byte(tc.raw))
			var roles, names []string
			for _, x := range m {
				roles = append(roles, x.Role)
				for _, p := range x.Parts {
					if p.Type == "tool_call" {
						names = append(names, p.Name)
					}
				}
			}
			if got := PersonTexts(m); !reflect.DeepEqual(got, tc.person) {
				t.Errorf("person = %q, want %q (tool results are never person text)", got, tc.person)
			}
			if !reflect.DeepEqual(names, tc.toolNames) {
				t.Errorf("tool calls = %q, want %q", names, tc.toolNames)
			}
			if !reflect.DeepEqual(roles, tc.roles) {
				t.Errorf("roles = %q, want %q", roles, tc.roles)
			}
		})
	}
}

func TestToOTelDecodesStringArguments(t *testing.T) {
	m := ToOTel([]byte(`[{"type":"function_call","name":"x","arguments":"{\"cmd\":\"ls\"}"}]`))
	if got := m[0].Parts[0].Arguments; !reflect.DeepEqual(got, map[string]any{"cmd": "ls"}) {
		t.Errorf("arguments = %#v", got)
	}
}

// A logged request is client input: a malformed tool call must not panic the job.
func TestToOTelMalformedToolCallsDoNotPanic(t *testing.T) {
	for _, raw := range []string{
		`[{"role":"assistant","content":"x","tool_calls":[null]}]`,
		`[{"role":"assistant","content":"x","tool_calls":["s",1,{"function":"f"}]}]`,
		`[{"role":"assistant","content":[null,"s",{"type":"tool_use"}]}]`,
	} {
		ToOTel([]byte(raw))
	}
}

func TestToOTelGarbageIsEmpty(t *testing.T) {
	for _, raw := range []string{``, `nope`, `[]`, `[1,"s",null,{"x":1}]`, `{"messages":[]}`} {
		if m := ToOTel([]byte(raw)); len(m) != 0 {
			t.Errorf("%q -> %v, want empty", raw, m)
		}
	}
}

func TestParsedToolCalls(t *testing.T) {
	got := ParsedToolCalls([]string{"shell", "read"},
		[]string{`{"id":"1","type":"function","index":0,"arguments":"{\"command\":\"ls\"}"}`, `not json`})
	want := []Part{{Type: "tool_call", Name: "shell", Arguments: map[string]any{"command": "ls"}}, {Type: "tool_call", Name: "read"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}
