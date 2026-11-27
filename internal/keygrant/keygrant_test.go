// SPDX-License-Identifier: Apache-2.0

package keygrant

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

const id = "ai.run@ackstorm.com"

func grant(name string, envs, sas []string) db.KeyGrantRow {
	return db.KeyGrantRow{Namespace: "ach", Name: name, Identity: id, Environments: envs, ServiceAccounts: sas}
}

func TestCovering_OneGrantMustNameAllThree(t *testing.T) {
	grants := []db.KeyGrantRow{
		grant("a", []string{"prod"}, []string{"agents/other"}),
		grant("b", []string{"dev"}, []string{"agents/doctor"}),
	}
	if got := Covering(grants, id, "prod", "agents/doctor"); len(got) != 0 {
		t.Fatalf("lists from two grants were combined: %v", got)
	}
	grants = append(grants, grant("c", []string{"prod"}, []string{"agents/doctor"}))
	if got := Covering(grants, id, "prod", "agents/doctor"); len(got) != 1 || got[0].Name != "c" {
		t.Fatalf("want only grant c, got %v", got)
	}
	if got := Covering(grants, "ceo@ackstorm.com", "prod", "agents/doctor"); len(got) != 0 {
		t.Fatalf("an identity no grant names was allowed: %v", got)
	}
}

func TestMiss(t *testing.T) {
	cases := []struct {
		name   string
		grants []db.KeyGrantRow
		want   string
	}{
		{"none", nil, "no_grant_for_identity"},
		{"environment", []db.KeyGrantRow{grant("a", []string{"dev"}, []string{"agents/doctor"})}, "environment_not_granted"},
		{"requester", []db.KeyGrantRow{grant("a", []string{"prod"}, []string{"agents/other"})}, "requester_not_granted"},
	}
	for _, tc := range cases {
		if got := Miss(tc.grants, "prod"); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestPlan(t *testing.T) {
	grants := []db.KeyGrantRow{grant("a", []string{"prod"}, []string{"agents/doctor"})}
	keys := []db.EkKeyInfo{
		{KeyID: "ekid_keep", OwnerEmail: id, Environment: "prod", Requester: "agents/doctor"},
		{KeyID: "ekid_gone", OwnerEmail: id, Environment: "prod", Requester: "agents/removed"},
	}
	if revoke := Plan(grants, keys); len(revoke) != 1 || revoke[0].KeyID != "ekid_gone" {
		t.Fatalf("revoke = %v", revoke)
	}
}
