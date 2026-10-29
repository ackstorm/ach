// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// catalog decodes the wire shape GET /platform/environments returns, so the
// test also pins CatalogView's json tags.
func catalog(t *testing.T) []CatalogView {
	t.Helper()
	var envs []CatalogView
	err := json.Unmarshal([]byte(`[
	  {"name":"finops","description":"Cloud cost reports",
	   "context":{"plugins":["cloud-costs"],"skills":["budget-alerts"],"prompts":["finops-review"]},
	   "runtime":{"mcpServers":["aws-billing"],"models":["ackstorm.smart"]}},
	  {"name":"design","description":"Web and 3D work",
	   "context":{"skills":["threejs-animation","web-design"],"artifacts":["brand-kit"]},
	   "runtime":{"a2aAgents":["design-reviewer"]}}
	]`), &envs)
	if err != nil {
		t.Fatal(err)
	}
	return envs
}

func TestSearch(t *testing.T) {
	envs := catalog(t)
	for _, tc := range []struct {
		query string
		want  []SearchHit
	}{
		{"FinOps", []SearchHit{
			{"environment", "finops", "finops"},
			{"prompt", "finops-review", "finops"},
		}},
		{"cost", []SearchHit{
			{"environment", "finops", "finops"}, // description
			{"plugin", "cloud-costs", "finops"},
		}},
		{"billing", []SearchHit{{"mcp", "aws-billing", "finops"}}},
		{"smart", []SearchHit{{"model", "ackstorm.smart", "finops"}}},
		{"brand", []SearchHit{{"artifact", "brand-kit", "design"}}},
		{"reviewer", []SearchHit{{"a2a", "design-reviewer", "design"}}},
		// Every word must match: "3d" only the description, "web" both.
		{"web 3d", []SearchHit{{"environment", "design", "design"}}},
		{"design", []SearchHit{
			{"environment", "design", "design"},
			{"skill", "web-design", "design"},
			{"a2a", "design-reviewer", "design"},
		}},
		{"nothing-here", nil},
		{"   ", nil},
	} {
		if got := Search(envs, tc.query); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Search(%q) = %v; want %v", tc.query, got, tc.want)
		}
	}
}

func TestFormatSearch(t *testing.T) {
	if got := FormatSearch(nil); got != "No matches.\n" {
		t.Errorf("empty: %q", got)
	}
	got := FormatSearch([]SearchHit{{"skill", "web-design", "design"}})
	for _, want := range []string{"KIND", "ENVIRONMENT", "web-design", "--only <kind>/<name>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if got := FormatSearch([]SearchHit{{"mcp", "aws-billing", "finops"}}); strings.Contains(got, "--only") {
		t.Errorf("install hint on a non-installable hit:\n%s", got)
	}
}
