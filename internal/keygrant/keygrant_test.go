// SPDX-License-Identifier: Apache-2.0

package keygrant

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

const id = "ai.run@ackstorm.com"

func grant(name string, envs, sas []string, budget *float64, duration string) db.KeyGrantRow {
	return db.KeyGrantRow{Namespace: "ach", Name: name, Identity: id,
		Environments: envs, ServiceAccounts: sas, MaxBudget: budget, BudgetDuration: duration}
}

func TestCovering_OneGrantMustNameAllThree(t *testing.T) {
	grants := []db.KeyGrantRow{
		grant("a", []string{"prod"}, []string{"agents/other"}, nil, ""),
		grant("b", []string{"dev"}, []string{"agents/doctor"}, nil, ""),
	}
	if got := Covering(grants, id, "prod", "agents/doctor"); len(got) != 0 {
		t.Fatalf("lists from two grants were combined: %v", got)
	}
	grants = append(grants, grant("c", []string{"prod"}, []string{"agents/doctor"}, nil, ""))
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
		{"environment", []db.KeyGrantRow{grant("a", []string{"dev"}, []string{"agents/doctor"}, nil, "")}, "environment_not_granted"},
		{"requester", []db.KeyGrantRow{grant("a", []string{"prod"}, []string{"agents/other"}, nil, "")}, "requester_not_granted"},
	}
	for _, tc := range cases {
		if got := Miss(tc.grants, "prod"); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestBudget_LowestWins(t *testing.T) {
	fifty, twenty := 50.0, 20.0
	if Budget([]db.KeyGrantRow{grant("a", nil, nil, nil, "")}) != nil {
		t.Fatal("no grant sets a budget: want nil")
	}
	b := Budget([]db.KeyGrantRow{
		grant("a", nil, nil, &fifty, "30d"),
		grant("b", nil, nil, nil, ""),
		grant("c", nil, nil, &twenty, "7d"),
	})
	if b == nil || b.MaxBudget != 20 || b.BudgetDuration != "7d" {
		t.Fatalf("want 20/7d, got %+v", b)
	}
}

func TestPlan(t *testing.T) {
	ten := 10.0
	grants := []db.KeyGrantRow{grant("a", []string{"prod"}, []string{"agents/doctor"}, &ten, "30d")}
	keys := []db.EkKeyInfo{
		{KeyID: "ekid_keep", OwnerEmail: id, Environment: "prod", Requester: "agents/doctor"},
		{KeyID: "ekid_gone", OwnerEmail: id, Environment: "prod", Requester: "agents/removed"},
	}
	revoke, budgets := Plan(grants, keys)
	if len(revoke) != 1 || revoke[0].KeyID != "ekid_gone" {
		t.Fatalf("revoke = %v", revoke)
	}
	if b, ok := budgets["ekid_keep"]; !ok || b.MaxBudget != 10 || len(budgets) != 1 {
		t.Fatalf("budgets = %v", budgets)
	}
}
