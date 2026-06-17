// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"bytes"
	"strings"
	"testing"

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
