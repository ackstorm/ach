// SPDX-License-Identifier: Apache-2.0

// Package openwork is the OpenWork organization server ("Den") contract,
// ported from alitellm-auth (src/api/app/openwork.py @ 4e38245) minus
// Cloud MCP (spec §12). The desktop app, pointed at this deployment,
// signs in with the console session the user already holds and then
// receives enforced desktop policy and the deployment's branding.
//
// The Den API is served at BOTH /api/den and /openwork/api/den: OpenWork's
// Settings input keeps only the origin of the organization server URL, so
// a desktop configured by hand calls <origin>/api/den; a bootstrap file or
// deep link may still carry the /openwork path. The handoff page and the
// brand marks live under /openwork only. Off unless openwork.enabled.
package openwork

import (
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/config"
)

// Config is the openwork: values block (ACH_OPENWORK_* on the Pod).
type Config struct {
	Enabled             bool
	OrgName             string
	OrgSlug             string
	BrandAppName        string
	BrandLogoURL        string
	BrandIconURL        string
	AccentColor         string
	BlockedCommands     []string
	BlockBrowserUploads bool
	// GrantTTL bounds the single-use handoff grant; TokenTTL the desktop's
	// session token (spec §12 defaults: five minutes, thirty days).
	GrantTTL time.Duration
	TokenTTL time.Duration
}

// FromEnv reads ACH_OPENWORK_*.
func FromEnv() Config {
	c := Config{
		Enabled:             config.EnvBool("ACH_OPENWORK_ENABLED", false),
		OrgName:             config.EnvOr("ACH_OPENWORK_ORG_NAME", "ACH"),
		OrgSlug:             config.EnvOr("ACH_OPENWORK_ORG_SLUG", "ach"),
		BrandAppName:        config.EnvOr("ACH_OPENWORK_BRAND_APP_NAME", "ACH"),
		BrandLogoURL:        config.EnvOr("ACH_OPENWORK_BRAND_LOGO_URL", ""),
		BrandIconURL:        config.EnvOr("ACH_OPENWORK_BRAND_ICON_URL", ""),
		AccentColor:         config.EnvOr("ACH_OPENWORK_ACCENT_COLOR", ""),
		BlockBrowserUploads: config.EnvBool("ACH_OPENWORK_BLOCK_BROWSER_UPLOADS", false),
		GrantTTL:            5 * time.Minute,
		TokenTTL:            30 * 24 * time.Hour,
	}
	for _, cmd := range strings.Split(config.EnvOr("ACH_OPENWORK_BLOCKED_COMMANDS", ""), ",") {
		if cmd = strings.TrimSpace(cmd); cmd != "" {
			c.BlockedCommands = append(c.BlockedCommands, cmd)
		}
	}
	return c
}
