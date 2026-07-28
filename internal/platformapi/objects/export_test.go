// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/db"
)

// TestExportEnvironmentYAML_Canonical asserts the export carries only the
// canonical apiVersion/kind/metadata/spec surface — never status, conditions,
// resourceVersion, or uid — even when the row has populated condition columns.
// It also asserts the render is deterministic (byte-identical across calls).
func TestExportEnvironmentYAML_Canonical(t *testing.T) {
	row := db.EnvironmentRow{
		Namespace:          "ach",
		Name:               "dev",
		AuthorizedTeams:    []string{"team-a"},
		RuntimeModels:      []string{"gpt-4"},
		ContextPrompts:     []string{"welcome"},
		Notice:             "re-login after rotation",
		Description:        "developer sandbox",
		ResourceVersion:    "12345",
		AvailableCondition: []byte(`{"type":"Available","status":"True"}`),
	}

	out, err := ExportEnvironmentYAML(row, "ach")
	if err != nil {
		t.Fatalf("ExportEnvironmentYAML: %v", err)
	}
	s := string(out)

	for _, want := range []string{"apiVersion", "kind", "metadata", "spec", "dev", "team-a", "gpt-4"} {
		if !strings.Contains(s, want) {
			t.Errorf("export missing %q\n---\n%s", want, s)
		}
	}

	for _, forbidden := range []string{"status", "available_condition", "AvailableCondition", "resourceVersion", "resource_version", "uid"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("export leaked forbidden field %q\n---\n%s", forbidden, s)
		}
	}

	out2, err := ExportEnvironmentYAML(row, "ach")
	if err != nil {
		t.Fatalf("ExportEnvironmentYAML (2nd): %v", err)
	}
	if !bytes.Equal(out, out2) {
		t.Errorf("export is not deterministic:\n---first---\n%s\n---second---\n%s", out, out2)
	}
}

// TestExportEnvironmentYAMLPreservesGuardrails is the GitOps round-trip guard.
//
// The YAML export is NOT fenced by origin — it renders operator-owned rows too
// — and it is the documented path for committing an Environment
// (export -> commit -> kubectl apply). rowToSpec covers every EnvironmentSpec
// field today, so the export is lossless; a spec field missing from it would
// make that cycle silently DELETE the guardrails, i.e. remove a protection
// mechanism by copy-paste.
func TestExportEnvironmentYAMLPreservesGuardrails(t *testing.T) {
	row := db.EnvironmentRow{
		Namespace:         "ach",
		Name:              "demo",
		AuthorizedTeams:   []string{"platform"},
		RuntimeModels:     []string{"openai/gpt-4"},
		RuntimeGuardrails: []string{"pii-filter", "credential-filter"},
	}
	out, err := ExportEnvironmentYAML(row, "ach")
	if err != nil {
		t.Fatalf("ExportEnvironmentYAML: %v", err)
	}
	if !strings.Contains(string(out), "pii-filter") ||
		!strings.Contains(string(out), "credential-filter") {
		t.Fatalf("guardrails dropped from export:\n%s", out)
	}

	// Round-trip: the exported YAML must decode into a spec carrying the same
	// guardrails — that is literally what `kubectl apply` sends the operator.
	var back struct {
		Spec v1alpha1.EnvironmentSpec `json:"spec"`
	}
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if !slices.Equal(back.Spec.Runtime.Guardrails, row.RuntimeGuardrails) {
		t.Fatalf("round-trip guardrails = %v, want %v",
			back.Spec.Runtime.Guardrails, row.RuntimeGuardrails)
	}
}
