// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestRuntimeBlockGuardrailsAxis pins guardrails as a distinct runtime axis:
// it round-trips under the key "guardrails" and never bleeds into a sibling.
func TestRuntimeBlockGuardrailsAxis(t *testing.T) {
	rb := RuntimeBlock{
		Models:     []string{"openai/gpt-4"},
		MCPServers: []string{"raw-github"},
		A2AAgents:  []string{"triage"},
		Guardrails: []string{"pii-filter"},
	}
	b, err := json.Marshal(rb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"guardrails":["pii-filter"]`) {
		t.Fatalf("guardrails key missing from %s", b)
	}
	var back RuntimeBlock
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !slices.Equal(back.Guardrails, []string{"pii-filter"}) {
		t.Fatalf("guardrails = %v", back.Guardrails)
	}
	if slices.Contains(back.Models, "pii-filter") ||
		slices.Contains(back.MCPServers, "pii-filter") ||
		slices.Contains(back.A2AAgents, "pii-filter") {
		t.Fatal("guardrail name leaked into another runtime axis")
	}
}

// TestUnresolvedRuntimeGuardrailsAxis mirrors the above for the status arm.
func TestUnresolvedRuntimeGuardrailsAxis(t *testing.T) {
	b, err := json.Marshal(UnresolvedRuntime{Guardrails: []string{"typo-guard"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"guardrails":["typo-guard"]`) {
		t.Fatalf("unresolvedRuntime.guardrails missing from %s", b)
	}
}
