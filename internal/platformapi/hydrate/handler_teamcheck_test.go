// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
	achteams "github.com/ackstorm/ach/internal/platformapi/teams"
)

// fakeLiteLLMOwner is a minimal litellm.Client for the D-30 team-check
// tests below. Only UserInfoByEmail is exercised by
// achteams.LookupCallerTeams; embedding NoopClient satisfies the rest of
// the (large) interface with harmless stubs — same idiom as
// internal/platformapi/teams/lookup_test.go's fakeLiteLLM, minimized via
// embedding instead of hand-stubbing every method.
type fakeLiteLLMOwner struct {
	litellm.NoopClient
	teams []string
	err   error
}

func (f *fakeLiteLLMOwner) UserInfoByEmail(_ context.Context, _ string) (*litellm.UserInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &litellm.UserInfo{Teams: f.teams}, nil
}

var _ litellm.Client = (*fakeLiteLLMOwner)(nil)

// TestHydrateTeamCheck_EkOwner: D-30 widens the Hub §15.1 step-4
// team-intersection check in HydrateHandler (the `if !keyCtx.IsAdmin`
// block at handler.go:247) from pk_-only to ek_ too, keyed on the SAME
// field both key types carry on middleware.KeyContext — OwnerEmail (the
// caller's own email for pk_, the row's owner_email for ek_; an ek_ never
// carries IsAdmin=true per middleware/doc.go, so it always reaches this
// check).
//
// deps.Store in HydrateHandler is a concrete *store.Store with no
// interface seam (see handler_notice_test.go's documented reasoning for
// this same package), so the full handler is only exercisable against a
// real Postgres (integration). This test instead drives the exact two
// calls the widened branch makes — achteams.LookupCallerTeams then
// achteams.HasIntersect — directly against a constructed
// *db.EnvironmentRow, the same "call the real builders directly" pattern
// handler_notice_test.go already established.
func TestHydrateTeamCheck_EkOwner(t *testing.T) {
	env := &db.EnvironmentRow{Name: "demo", AuthorizedTeams: []string{"team-a"}}

	t.Run("owner in authorized team -> allow", func(t *testing.T) {
		ll := &fakeLiteLLMOwner{teams: []string{"team-a"}}
		teams, err := achteams.LookupCallerTeams(context.Background(), ll, "owner@x.com")
		if err != nil {
			t.Fatalf("LookupCallerTeams: %v", err)
		}
		if !achteams.HasIntersect(env.AuthorizedTeams, teams) {
			t.Fatalf("owner in team must intersect, got teams=%v", teams)
		}
	})

	t.Run("owner lost access -> 403 unauthorized_team", func(t *testing.T) {
		ll := &fakeLiteLLMOwner{teams: []string{"team-z"}}
		teams, err := achteams.LookupCallerTeams(context.Background(), ll, "owner@x.com")
		if err != nil {
			t.Fatalf("LookupCallerTeams: %v", err)
		}
		if achteams.HasIntersect(env.AuthorizedTeams, teams) {
			t.Fatalf("owner out of team must not intersect, got teams=%v", teams)
		}
	})

	t.Run("litellm down -> 503, not a verdict", func(t *testing.T) {
		ll := &fakeLiteLLMOwner{err: errors.New("connection refused")}
		_, err := achteams.LookupCallerTeams(context.Background(), ll, "owner@x.com")
		// The handler maps any non-nil error here to 503 litellm_unreachable
		// (handler.go:249-263) — never a 403 authorization verdict.
		if err == nil {
			t.Fatal("expected a transport error, got nil")
		}
	})
}
