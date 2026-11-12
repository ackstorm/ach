// SPDX-License-Identifier: Apache-2.0

package console

import (
	"context"

	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/teams"
)

// App is one tile on the console's Apps page (chart platformApi.console.apps).
// Hiding a tile is not access control: each app keeps its own login.
type App struct {
	Group    string   `json:"group,omitempty"`
	Name     string   `json:"name"`
	Subtitle string   `json:"subtitle,omitempty"`
	URL      string   `json:"url"`
	Icon     string   `json:"icon,omitempty"`
	Teams    []string `json:"teams,omitempty"` // any of these team aliases; empty = everyone
	Admin    bool     `json:"admin,omitempty"` // platform admins only
}

// visibleApps returns the tiles kc may see, in configured order, without their
// teams and admin fields: who else a tile targets is not the caller's to read. Team
// membership is read only when a tile asks for it; a failed read hides the
// team tiles instead of failing the bootstrap.
func (d Deps) visibleApps(ctx context.Context, kc middleware.KeyContext) []App {
	out := []App{}
	var callerTeams []string
	looked := false
	for _, a := range d.Apps {
		if a.Admin && !kc.IsAdmin {
			continue
		}
		if len(a.Teams) > 0 {
			if !looked {
				looked = true
				var err error
				if callerTeams, err = teams.LookupCallerTeams(ctx, d.LiteLLM, kc.OwnerEmail); err != nil {
					d.Logger.Warn("console.bootstrap: team membership unavailable, team apps hidden", "err", err)
				}
			}
			if !teams.HasIntersect(a.Teams, callerTeams) {
				continue
			}
		}
		a.Teams, a.Admin = nil, false
		out = append(out, a)
	}
	return out
}
