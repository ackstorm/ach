// SPDX-License-Identifier: Apache-2.0

//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestEnvironmentSpec_RoundTrip asserts the verbatim spec column (000024)
// survives the operator upsert, every read path, and the UI insert/update —
// it is what the Objects API export renders, so a lost write here silently
// drops spec.budget and the runtime group tags from an export.
func TestEnvironmentSpec_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	spec := []byte(`{"runtime":{"mcpServerGroups":["default"]},"budget":{"maxBudget":5}}`)
	assertSpec := func(label string, got []byte, want []byte) {
		t.Helper()
		var g, w any
		if err := json.Unmarshal(got, &g); err != nil {
			t.Fatalf("%s: stored spec %q is not JSON: %v", label, got, err)
		}
		_ = json.Unmarshal(want, &w)
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s: spec = %s, want %s", label, got, want)
		}
	}

	row := baseGuardrailRow("spec-cr", []string{})
	row.Spec = spec
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "spec-cr")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: got=%v err=%v", got, err)
	}
	assertSpec("Get", got.Spec, spec)
	list, err := db.ListEnvironments(ctx, pool, "ach-system")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListEnvironments: %+v err=%v", list, err)
	}
	assertSpec("List", list[0].Spec, spec)
	all, err := db.ListEnvironmentsIncludingDraining(ctx, pool, "ach-system")
	if err != nil || len(all) != 1 {
		t.Fatalf("ListEnvironmentsIncludingDraining: %+v err=%v", all, err)
	}
	assertSpec("ListIncludingDraining", all[0].Spec, spec)

	ui := baseGuardrailRow("spec-ui", []string{})
	ui.Spec = spec
	if err := db.InsertUIEnvironment(ctx, pool, ui); err != nil {
		t.Fatalf("InsertUIEnvironment: %v", err)
	}
	ui.Spec = []byte(`{"runtime":{"modelGroups":["openai"]}}`)
	if err := db.UpdateUIEnvironment(ctx, pool, ui); err != nil {
		t.Fatalf("UpdateUIEnvironment: %v", err)
	}
	got, err = db.GetEnvironmentByName(ctx, pool, "ach-system", "spec-ui")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName(ui): got=%v err=%v", got, err)
	}
	assertSpec("UI update", got.Spec, ui.Spec)
}
