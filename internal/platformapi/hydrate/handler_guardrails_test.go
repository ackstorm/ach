// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestToRuntimeBlockGuardrailsOmittedWhenEmpty: the guardrails arm is additive
// and MUST be omitted when empty. internal/cli/manifest decodes with
// DisallowUnknownFields, so an always-present key would hard-fail every ach-cli
// built before this field existed — including on Environments that declare no
// guardrails at all, i.e. every Environment that exists today.
func TestToRuntimeBlockGuardrailsOmittedWhenEmpty(t *testing.T) {
	for name, guardrails := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			rb := toRuntimeBlockFromRow(&db.EnvironmentRow{
				RuntimeModels:     []string{"gpt-4"},
				RuntimeGuardrails: guardrails,
			}, "http://x")
			b, err := json.Marshal(rb)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(b), "guardrails") {
				t.Fatalf("guardrails key must be absent, got %s", b)
			}
			// API-04: the other three stay `[]`, never null.
			for _, k := range []string{`"models":[`, `"mcpServers":[]`, `"a2aAgents":[]`} {
				if !strings.Contains(string(b), k) {
					t.Errorf("missing %s in %s", k, b)
				}
			}
		})
	}
}

// TestToRuntimeBlockGuardrailsNamesOnly: when present, guardrails are an array
// of STRINGS — not {id,endpoint} objects. A guardrail is applied by LiteLLM and
// never called by the client, so there is no endpoint to publish.
func TestToRuntimeBlockGuardrailsNamesOnly(t *testing.T) {
	rb := toRuntimeBlockFromRow(&db.EnvironmentRow{
		RuntimeGuardrails: []string{"pii-filter", "credential-filter"},
	}, "http://x")
	if !slices.Equal(rb.Guardrails, []string{"pii-filter", "credential-filter"}) {
		t.Fatalf("guardrails = %v", rb.Guardrails)
	}
	b, err := json.Marshal(rb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"guardrails":["pii-filter","credential-filter"]`) {
		t.Fatalf("want a string array, got %s", b)
	}
	if strings.Contains(string(b), `"endpoint"`) && strings.Contains(string(b), "pii-filter") {
		t.Errorf("guardrail must not carry an endpoint: %s", b)
	}
}
