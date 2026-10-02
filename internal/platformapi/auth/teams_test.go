// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
)

func teamsFake(existing ...string) *fakeLiteLLM {
	flm := newFakeLiteLLM()
	flm.listTeamsBehaviour = func(alias string) ([]litellm.TeamListEntry, error) {
		for _, e := range existing {
			if e == alias {
				return []litellm.TeamListEntry{{TeamID: "id-" + alias}}, nil
			}
		}
		return nil, nil
	}
	return flm
}

func TestTeamPolicyExtra(t *testing.T) {
	p := TeamPolicy{
		Default:    []string{"default"},
		ByUser:     map[string][]string{"pepe@x.com": {"run", "default"}},
		BySSOGroup: map[string][]string{"ops@x.com": {"run", "ops"}, "dream@x.com": {"dream"}},
	}
	got := p.extra("Pepe@X.com", []string{"OPS@x.com", "other"})
	want := []string{"run", "ops"} // dedup, "default" dropped, unknown group ignored
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extra = %v, want %v", got, want)
	}
}

func TestProvisionUser_ExtraTeams(t *testing.T) {
	deps := func(flm *fakeLiteLLM) Deps {
		d := provisionDeps(flm)
		d.Teams = TeamPolicy{Default: []string{"default"}, BySSOGroup: map[string][]string{"ops@x.com": {"ops"}, "dream@x.com": {"dream"}}}
		return d
	}
	t.Run("existing team joined, missing team skipped, login ok", func(t *testing.T) {
		flm := teamsFake("default", "ops") // "dream" does not exist
		if _, err := provisionUser(context.Background(), deps(flm), "pepe@x.com", []string{"ops@x.com", "dream@x.com"}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(flm.rec.teamMemberAdds, []string{"id-default", "id-ops"}) {
			t.Fatalf("adds = %v", flm.rec.teamMemberAdds)
		}
	})
	t.Run("next login picks the team up once it exists", func(t *testing.T) {
		flm := teamsFake("default", "ops", "dream")
		flm.userInfoBehaviour = func(email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u", UserEmail: email}, nil
		}
		if _, err := provisionUser(context.Background(), deps(flm), "pepe@x.com", []string{"dream@x.com"}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(flm.rec.teamMemberAdds, []string{"id-default", "id-dream"}) {
			t.Fatalf("adds = %v", flm.rec.teamMemberAdds)
		}
	})
	t.Run("LiteLLM error on an extra team never fails the login", func(t *testing.T) {
		flm := teamsFake("default", "ops")
		base := flm.listTeamsBehaviour
		flm.listTeamsBehaviour = func(a string) ([]litellm.TeamListEntry, error) {
			if a == "ops" {
				return nil, errors.New("litellm down")
			}
			return base(a)
		}
		if _, err := provisionUser(context.Background(), deps(flm), "pepe@x.com", []string{"ops@x.com"}); err != nil {
			t.Fatalf("login must survive: %v", err)
		}
	})
	t.Run("missing DEFAULT team still fails the login", func(t *testing.T) {
		flm := teamsFake() // nothing exists
		if _, err := provisionUser(context.Background(), deps(flm), "pepe@x.com", nil); err == nil {
			t.Fatal("want default_team_missing")
		}
	})
}
