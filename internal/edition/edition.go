// SPDX-License-Identifier: Apache-2.0

// Package edition is the seam the Enterprise code plugs into. Enterprise
// packages live under ee/ (ACKstorm Enterprise License) and register here from
// init(); only cmd/ach/ee.go imports them. They are always linked and run only
// when ACH_ENTERPRISE (Helm enterprise.enabled) is true.
package edition

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// enterpriseEnv is configuration only the Enterprise features read;
// platform-api refuses to start with any of it set while they are disabled.
var enterpriseEnv = []string{"ACH_OBSERVABILITY_CLICKHOUSE_URL", "ACH_OBSERVABILITY_THANOS_URL", "ACH_CLASSIFIER_TAXONOMY_FILE"}

// PlatformAPIEnv is what core hands an Enterprise platform-api hook.
type PlatformAPIEnv struct {
	Enterprise bool                 // ACH_ENTERPRISE; false mounts every Enterprise route as EnterpriseOnly
	ClickHouse *evidence.ClickHouse // nil when ACH_OBSERVABILITY_CLICKHOUSE_URL is unset
	Projects   []string
	LiteLLM    *litellm.RESTClient // admin client
}

// PlatformAPIHook reads its own configuration and returns the routes to mount
// behind Authn. An error stops platform-api at startup.
type PlatformAPIHook func(env PlatformAPIEnv) (mount func(chi.Router), err error)

var (
	commands []*cobra.Command
	hooks    []PlatformAPIHook
)

// RegisterCommand adds an Enterprise subcommand for Commands to attach to root.
func RegisterCommand(c *cobra.Command) { commands = append(commands, c) }

// RegisterPlatformAPI adds an Enterprise hook run when platform-api starts.
func RegisterPlatformAPI(h PlatformAPIHook) { hooks = append(hooks, h) }

// PlatformAPIHooks returns the registered platform-api hooks, in order.
func PlatformAPIHooks() []PlatformAPIHook { return hooks }

// Enabled reports whether ACH_ENTERPRISE turns the Enterprise features on.
func Enabled(getenv func(string) string) (bool, error) {
	v := getenv("ACH_ENTERPRISE")
	if v == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("ACH_ENTERPRISE: %q is not a boolean", v)
	}
	return on, nil
}

// Require fails unless the Enterprise features are enabled.
func Require(feature string, getenv func(string) string) error {
	on, err := Enabled(getenv)
	if err != nil {
		return err
	}
	if !on {
		return fmt.Errorf("%s is an ACH Enterprise feature: set Helm enterprise.enabled=true (ACH_ENTERPRISE=true)", feature)
	}
	return nil
}

// EnterpriseOnly answers an Enterprise route while the features are disabled.
func EnterpriseOnly(w http.ResponseWriter, r *http.Request) {
	render.Error(w, http.StatusForbidden, "enterprise_only",
		"this feature requires ACH Enterprise (Helm enterprise.enabled=true)", middleware.RequestIDFromCtx(r.Context()))
}

// Commands adds the registered Enterprise commands to root.
func Commands(root *cobra.Command) {
	for _, c := range commands {
		root.AddCommand(c)
	}
}

// CheckEnv validates ACH_ENTERPRISE and refuses Enterprise-only configuration
// while the Enterprise features are disabled.
func CheckEnv(getenv func(string) string) error {
	on, err := Enabled(getenv)
	if err != nil || on {
		return err
	}
	for _, k := range enterpriseEnv {
		if getenv(k) != "" {
			return fmt.Errorf("%s is Enterprise configuration: set Helm enterprise.enabled=true (ACH_ENTERPRISE=true)", k)
		}
	}
	return nil
}
