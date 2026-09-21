// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

func TestEffectiveState_Priority(t *testing.T) {
	cases := []struct {
		persisted string // what ListKeys returned (already 'expired' when the clock says so)
		access    accessVerdict
		want      string
		reasons   []string
	}{
		{"revoked", accessLost, "revoked", nil},
		{"expired", accessLost, "expired", []string{"expired", "no_access"}},
		{"suspended", accessLost, "suspended", []string{"suspended", "no_access"}},
		{"suspended", accessGranted, "suspended", []string{"suspended"}},
		{"active", accessLost, "invalid", []string{"no_access"}},
		{"active", accessUnverified, "active", []string{"access_unverified"}},
		{"active", accessGranted, "active", nil},
	}
	for _, c := range cases {
		got, reasons := EffectiveState(db.KeyListItem{Type: "ek", Status: c.persisted}, c.access)
		if got != c.want || !reflect.DeepEqual(reasons, c.reasons) {
			t.Errorf("%s/%v: %s %v, want %s %v", c.persisted, c.access, got, reasons, c.want, c.reasons)
		}
	}
	if got, _ := EffectiveState(db.KeyListItem{Type: "pk", Status: "active"}, accessLost); got != "active" {
		t.Fatal("pk_ rows never derive invalid")
	}
}
