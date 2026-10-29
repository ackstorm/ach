// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import (
	"encoding/json"
	"slices"
	"strings"
)

const (
	textBudget  = 24000 // ~6K tokens
	turnCap     = 1500
	argCap      = 150
	personShare = 0.6 // of the budget; tool calls fill the rest
	minPerson   = 200 // chars of person text below which a session with no tool calls is trivial
	minCalls    = 2   // a single call is a one-shot helper or probe, not a conversation (plan §4.3)
)

// PersonTexts is the text the person typed. Tool results are never person text.
func PersonTexts(msgs []Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role != roleUser {
			continue
		}
		for _, p := range m.Parts {
			if p.Type == "text" {
				out = append(out, p.Content)
			}
		}
	}
	return out
}

// IsTrivial: too little to classify: one model call, or ("Hi", "/quota") little
// person text and no tool calls. A dry run labelled 6 of 15 single-call sessions
// "other" (2026-10-27); the golden set, which required 2 calls, had none.
func IsTrivial(s Session) bool {
	if s.Generations < minCalls {
		return true
	}
	n := 0
	for _, t := range PersonTexts(s.Messages) {
		n += len(t)
	}
	return n < minPerson && len(s.ToolCalls) == 0
}

// BuildText is what the model sees: the person's requests (opening and latest
// first) and the session's tool calls spread evenly, within textBudget.
func BuildText(s Session) string {
	person := PersonTexts(s.Messages)
	for i, t := range person {
		person[i] = cut(t, turnCap)
	}
	order := make([]int, len(person))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return min(a, len(person)-1-a) - min(b, len(person)-1-b)
	})
	keep, used := map[int]bool{}, 0
	for _, k := range order {
		if used+len(person[k]) <= int(textBudget*personShare) {
			keep[k] = true
			used += len(person[k])
		}
	}
	var b strings.Builder
	for i, t := range person {
		if keep[i] {
			b.WriteString("user: " + t + "\n\n")
		}
	}
	calls := make([]string, 0, len(s.ToolCalls))
	total := 0
	for _, c := range s.ToolCalls {
		line := "[tool " + c.Name + ": " + argText(c.Arguments) + "]"
		calls = append(calls, line)
		total += len(line) + 1
	}
	if room := textBudget - used; total > room && len(calls) > 0 {
		n := max(1, room/(argCap+30))
		if n < len(calls) {
			spread := make([]string, 0, n)
			for i := range n {
				spread = append(spread, calls[i*(len(calls)-1)/max(1, n-1)])
			}
			calls = spread
		}
	}
	if len(calls) > 0 {
		b.WriteString("assistant actions:\n" + strings.Join(calls, "\n"))
	}
	return strings.TrimSpace(b.String())
}

func argText(a any) string {
	if a == nil {
		return ""
	}
	s, ok := a.(string)
	if !ok {
		raw, _ := json.Marshal(a)
		s = string(raw)
	}
	return cut(strings.Join(strings.Fields(s), " "), argCap)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
