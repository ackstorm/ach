// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"strings"

	"github.com/ackstorm/ach/internal/litellm"
)

// TeamPolicy is the chart's login enrolment (platformApi.teams). Default
// teams are mandatory (provisionUser fails the login without them); the
// per-user and per-SSO-group teams are best-effort and additive: a team that
// does not exist in LiteLLM is skipped and retried at the next login, and a
// removed mapping never detaches anyone. LiteLLM is the source of truth —
// ACH never creates these teams.
type TeamPolicy struct {
	Default    []string
	ByUser     map[string][]string // lower-cased email -> team aliases
	BySSOGroup map[string][]string // lower-cased Dex group -> team aliases
}

// extra returns the aliases the user should additionally join: per-user plus
// one entry per matching SSO group, de-duplicated, minus the default teams.
func (p TeamPolicy) extra(email string, groups []string) []string {
	seen := map[string]bool{}
	for _, a := range p.Default {
		seen[a] = true
	}
	var out []string
	add := func(aliases []string) {
		for _, a := range aliases {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	add(p.ByUser[strings.ToLower(email)])
	for _, g := range groups {
		add(p.BySSOGroup[strings.ToLower(g)])
	}
	return out
}

// enrollExtraTeams never fails the login: what is missing is an entitlement,
// not a credential, and the next login retries.
func enrollExtraTeams(ctx context.Context, deps Deps, userID, email string, groups []string) {
	for _, alias := range deps.Teams.extra(email, groups) {
		teams, err := deps.LiteLLM.ListTeamsByAlias(ctx, alias)
		if err != nil {
			deps.Logger.Warn("login: team lookup failed; skipped", "team", alias, "err", err)
			continue
		}
		if len(teams) == 0 {
			deps.Logger.Info("login: team does not exist; skipped", "team", alias, "user", email)
			continue
		}
		if err := deps.LiteLLM.TeamMemberAdd(ctx, teams[0].TeamID, userID, "user"); err != nil && !litellm.IsHTTPBadRequest(err) {
			deps.Logger.Warn("login: team enrolment failed", "team", alias, "user", email, "err", err)
		}
	}
}
