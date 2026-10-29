// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import "encoding/json"

// Message is an OpenTelemetry GenAI chat message: the input contract of the
// classifier (open-telemetry/semantic-conventions-genai,
// model/gen-ai/gen-ai-input-messages.json). Only the part types the
// classifier uses are modelled; the rest are dropped by the adapter.
type Message struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// Part types: "text", "tool_call", "tool_call_response", "reasoning".
type Part struct {
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments any    `json:"arguments,omitempty"`
}

// Session is everything the classifier sees of one session. How it was
// stored (client, Langfuse version) never reaches this type.
type Session struct {
	ID, UserID, Client string
	Messages           []Message
	ToolCalls          []Part // every call of the session, in order
	Generations        int
}

// ToOTel maps one call's raw `input` to OTel messages. Three shapes are
// measured (2026-10-27): OpenAI chat (opencode, langchainjs, AsyncOpenAI),
// OpenAI Responses (codex) and Anthropic blocks (claude-cli). A new source
// is a new case here; nothing else changes.
func ToOTel(raw []byte) []Message {
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		var wrapped struct {
			Messages []map[string]any `json:"messages"`
			Input    []map[string]any `json:"input"`
		}
		if json.Unmarshal(raw, &wrapped) != nil {
			return nil
		}
		items = append(wrapped.Messages, wrapped.Input...)
	}
	var out []Message
	for _, m := range items {
		if m == nil {
			continue
		}
		switch m["type"] { // OpenAI Responses non-message items
		case "function_call":
			out = append(out, Message{Role: "assistant", Parts: []Part{{Type: "tool_call", Name: str(m["name"]), Arguments: args(m["arguments"])}}})
			continue
		case "function_call_output":
			out = append(out, Message{Role: "tool", Parts: []Part{{Type: "tool_call_response"}}})
			continue
		}
		r, ok := m["role"].(string)
		if !ok {
			continue
		}
		role := otelRole(r)
		var parts []Part
		if role == "tool" { // OpenAI chat tool result
			parts = append(parts, Part{Type: "tool_call_response"})
		} else {
			parts = append(parts, textParts(m["content"])...)
			if blocks, ok := m["content"].([]any); ok { // Anthropic blocks
				for _, b := range blocks {
					bm, _ := b.(map[string]any)
					switch bm["type"] {
					case "tool_use":
						parts = append(parts, Part{Type: "tool_call", Name: str(bm["name"]), Arguments: bm["input"]})
					case "tool_result":
						parts = append(parts, Part{Type: "tool_call_response"})
					case "thinking":
						parts = append(parts, Part{Type: "reasoning"})
					}
				}
			}
			tcs, _ := m["tool_calls"].([]any) // OpenAI chat
			for _, tc := range tcs {
				tcm, _ := tc.(map[string]any)
				f, _ := tcm["function"].(map[string]any)
				parts = append(parts, Part{Type: "tool_call", Name: str(f["name"]), Arguments: args(f["arguments"])})
			}
		}
		if len(parts) > 0 {
			out = append(out, Message{Role: role, Parts: parts})
		}
	}
	return out
}

// ParsedToolCalls maps Langfuse's parsed columns, the same shape for every
// client: names[i] goes with calls[i], a JSON {id, type, index, arguments}.
func ParsedToolCalls(names, calls []string) []Part {
	out := make([]Part, 0, len(names))
	for i, n := range names {
		p := Part{Type: "tool_call", Name: n}
		if i < len(calls) {
			var c struct {
				Arguments any `json:"arguments"`
			}
			if json.Unmarshal([]byte(calls[i]), &c) == nil {
				p.Arguments = args(c.Arguments)
			}
		}
		out = append(out, p)
	}
	return out
}

// roleUser is the OTel role of the person; unknown roles map to it.
const roleUser = "user"

func otelRole(r string) string {
	switch r {
	case roleUser, "assistant", "system", "tool":
		return r
	case "developer":
		return "system"
	}
	return roleUser
}

func textParts(content any) []Part {
	if s, ok := content.(string); ok {
		if trimmed(s) == "" {
			return nil
		}
		return []Part{{Type: "text", Content: s}}
	}
	var out []Part
	blocks, _ := content.([]any)
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		switch bm["type"] {
		case "text", "input_text", "output_text": // Anthropic, OpenAI Responses
			if s := str(bm["text"]); trimmed(s) != "" {
				out = append(out, Part{Type: "text", Content: s})
			}
		}
	}
	return out
}

// args decodes a JSON-string arguments value; anything else passes through.
func args(a any) any {
	s, ok := a.(string)
	if !ok {
		return a
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

func str(v any) string { s, _ := v.(string); return s }

func trimmed(s string) string {
	for i := range s {
		if s[i] != ' ' && s[i] != '\n' && s[i] != '\t' && s[i] != '\r' {
			return s[i:]
		}
	}
	return ""
}
