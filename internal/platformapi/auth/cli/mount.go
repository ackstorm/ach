// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	achmetrics "github.com/ackstorm/ach/internal/metrics"
)

// Deps is the dependency bag for the device-code endpoints. server.go
// composes it from the top-level platformapi.Deps in the unauth
// carve-out region (alongside /platform/auth/login and
// /platform/auth/sso/callback). Both handlers are anonymous — neither
// /init nor /token sits under the Authn middleware; the session_id is
// the only credential /token honors.
//
// Mount placement is OUTSIDE the Authn-gated r.Group per D-02 + Pattern
// P7. /init must be anonymous (it starts the flow); /token uses the
// session_id alone as the bearer-equivalent (one-shot consumption via
// Redis GETDEL).
type Deps struct {
	// Redis is the go-redis client whose namespace holds
	// "ach:cli-session:<id>" entries. Shared with the operator-side
	// cached resolver and the envkeys revoke-cache; collision-free
	// thanks to the "ach:cli-session:" prefix.
	Redis *redis.Client

	// Audit is the audit.NewLogger handle. Used by TokenHandler on the
	// successful /token branch to emit one platform.cli.login event.
	Audit *slog.Logger

	// Logger is the operational logger. Used for non-audit lines (e.g.
	// transient Redis errors logged before render.Error). MUST NOT
	// receive sess.Plaintext (Pattern S5).
	Logger *slog.Logger

	// Namespace composes the audit `actor` field per Hub §18.3:
	// "<namespace>/<sso-email>". Sourced from POD_NAMESPACE (downward
	// API) at server-construction time.
	Namespace string

	// BaseURL is the deployment-configured https:// ingress URL. /init
	// composes verification_url = BaseURL + "/platform/auth/login?
	// session_id=<id>".
	BaseURL string

	// SessionTTL is the upper bound on a single device-code session
	// lifetime. Defaults to DefaultSessionTTL (5min) when zero.
	SessionTTL time.Duration

	// PollInterval is the recommended poll cadence surfaced in
	// InitResponse so the client doesn't hard-code it. Defaults to
	// DefaultPollInterval (2s) when zero.
	PollInterval time.Duration

	// Metrics is the platform-api collector set (G7); nil-tolerant. Used to
	// increment platform_api_login_total{outcome} on the /token branch.
	Metrics *achmetrics.PlatformAPICollectors
}

// sessionTTL returns the configured TTL or the package default.
func (d Deps) sessionTTL() time.Duration {
	if d.SessionTTL <= 0 {
		return DefaultSessionTTL
	}
	return d.SessionTTL
}

// pollInterval returns the configured cadence or the package default.
func (d Deps) pollInterval() time.Duration {
	if d.PollInterval <= 0 {
		return DefaultPollInterval
	}
	return d.PollInterval
}

// Mount returns the chi.Router subtree constructor for the
// /platform/auth/cli endpoint family. Wired in server.go like this:
//
//	r.Route("/platform/auth/cli", cli.Mount(cli.Deps{
//	    Redis:     deps.Redis,
//	    Audit:     deps.Audit,
//	    Logger:    deps.Logger,
//	    Namespace: deps.Namespace,
//	    BaseURL:   deps.BaseURL,
//	}))
//
// The two registered routes correspond to the D-02 device-code dance:
//
//   - POST /init  — anonymously mints session_id + verification_url.
//   - POST /token — polls session via Peek/Consume (Redis GETDEL).
//
// NO middleware.Authn — both endpoints are anonymous (init is the start
// of the auth flow; token gates by session_id alone).
func Mount(deps Deps) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/init", InitHandler(deps))
		r.Post("/token", TokenHandler(deps))
	}
}
