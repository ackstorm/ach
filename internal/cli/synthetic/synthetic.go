// SPDX-License-Identifier: Apache-2.0

package synthetic

import (
	"fmt"
	"os"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// SyntheticDeploymentLabel is the literal string the Phase 7 state.json
// writer records as the deployment name when synthetic mode is active
// (CLI-07 last clause). Surfaced as an exported constant so every
// caller across phases agrees on the value — drift here breaks the
// Phase 7 state schema.
const SyntheticDeploymentLabel = "(env)"

// Gate is a closed-enum tag a subcommand passes to GuardCommand to
// declare its disposition under synthetic mode. Typed-int prevents
// accidental string/int confusion at call sites.
type Gate int

// Gate constants. The DENY set (Login, Logout, Config, EnvKeysCreate)
// is rejected outright in synthetic mode. The ALLOW set
// (Hydrate, Whoami, EnvList, EnvDescribe, EnvKeysList, EnvKeysRevoke,
// Admin) is permitted, but every gate (deny + allow) is still subject
// to the --deployment / --env-key / half-set checks.
const (
	// GateLogin — `ach login`. Denied in synthetic (no disk registry
	// to write the pk_ into).
	GateLogin Gate = iota + 1

	// GateLogout — `ach logout`. Denied in synthetic (no disk pk_ to
	// wipe).
	GateLogout

	// GateConfig — every `ach config` child (list/show/use/remove/
	// rename). Denied in synthetic (no disk registry to read or
	// mutate).
	GateConfig

	// GateEnvKeysCreate — `ach env-keys create`. Denied in synthetic
	// UNLESS Params.NoSaveFlag is set (D-08: --no-save opts out of
	// persist; ek_ flows to stdout only — useful for CI vault piping).
	GateEnvKeysCreate

	// GateHydrate — `ach hydrate`. Allowed in synthetic (pk_ →
	// /platform/hydrate is the primary CI use case).
	GateHydrate

	// GateWhoami — `ach whoami`. Allowed in synthetic (read-only
	// identity probe).
	GateWhoami

	// GateEnvList — `ach env list`. Allowed in synthetic.
	GateEnvList

	// GateEnvDescribe — `ach env describe`. Allowed in synthetic.
	GateEnvDescribe

	// GateEnvKeysList — `ach env-keys list`. Allowed in synthetic.
	GateEnvKeysList

	// GateEnvKeysRevoke — `ach env-keys revoke`. Allowed in synthetic.
	GateEnvKeysRevoke

	// GateAdmin — every `ach admin` child (Phase 7/06-08). Allowed in
	// synthetic provided the admin pk_ resolves; --deployment /
	// --env-key still rejected per the cross-gate rules.
	GateAdmin
)

// Params is the bag a caller passes to GuardCommand alongside its
// Gate. The four flag-resolved values are read by the caller off the
// cobra flag set; env vars are read by GuardCommand itself via Getenv
// (so the test seam stays inside this package).
type Params struct {
	// Gate is the subcommand identity (REQUIRED).
	Gate Gate

	// APIKeyFlag is the --api-key value as resolved off the cobra
	// flag. Empty when --api-key was not passed. When non-empty,
	// participates in the IsActive synthetic-mode trigger (D-11)
	// alongside ACH_BASE_URL.
	APIKeyFlag string

	// EnvKeyFlag is the --env-key value. Non-empty under synthetic
	// triggers a rejection on every read-side gate.
	EnvKeyFlag string

	// DeploymentFlag is the --deployment value. Non-empty under
	// synthetic triggers a rejection on every gate.
	DeploymentFlag string

	// NoSaveFlag is the --no-save value. Only GateEnvKeysCreate reads
	// it; other gates leave the zero value (false). When true under
	// synthetic + GateEnvKeysCreate, the gate is allowed (D-08).
	NoSaveFlag bool
}

// Getenv is the env-var read seam. Production points at os.Getenv;
// tests are expected to use t.Setenv (which mutates the real env
// transparently for the duration of t), so overriding this var is
// rarely needed. Exposed so a hostile package or a fuzz harness can
// substitute a deterministic lookup without touching the process env.
var Getenv = os.Getenv

// allowedInSyntheticGates is the constant lookup table for "this gate
// is permitted under fully-synthetic mode". It does NOT short-circuit
// the --deployment / --env-key cross-gate checks — those still fire.
// Half-set always rejects regardless of this table.
var allowedInSyntheticGates = map[Gate]bool{
	GateHydrate:       true,
	GateWhoami:        true,
	GateEnvList:       true,
	GateEnvDescribe:   true,
	GateEnvKeysList:   true,
	GateEnvKeysRevoke: true,
	GateAdmin:         true,
}

// readOnlyGatesRejectingEnvKey is the subset of allowed-in-synthetic
// gates that additionally reject --env-key / ACH_ENV_KEY under
// synthetic. CLI-09: ek_ requires the config registry, which is
// unavailable in synthetic mode by definition.
var readOnlyGatesRejectingEnvKey = map[Gate]bool{
	GateHydrate:       true,
	GateWhoami:        true,
	GateEnvList:       true,
	GateEnvDescribe:   true,
	GateEnvKeysList:   true,
	GateEnvKeysRevoke: true,
	GateAdmin:         true,
}

// gateName returns a short human label for a Gate value, used to
// compose error messages. Unknown gates fall back to "<gate>" so the
// message stays useful even if a caller passes a zero-value Gate.
func gateName(g Gate) string {
	switch g {
	case GateLogin:
		return "login"
	case GateLogout:
		return "logout"
	case GateConfig:
		return "config"
	case GateEnvKeysCreate:
		return "env-keys create"
	case GateHydrate:
		return "hydrate"
	case GateWhoami:
		return "whoami"
	case GateEnvList:
		return "env list"
	case GateEnvDescribe:
		return "env describe"
	case GateEnvKeysList:
		return "env-keys list"
	case GateEnvKeysRevoke:
		return "env-keys revoke"
	case GateAdmin:
		return "admin"
	default:
		return "<gate>"
	}
}

// IsActive reports whether synthetic mode is fully active for this
// invocation. The trigger is ACH_BASE_URL AND a credential — the
// credential resolves from ACH_API_KEY OR p.APIKeyFlag (D-11). Pure
// predicate; safe to call repeatedly.
func IsActive(p Params) bool {
	if Getenv("ACH_BASE_URL") == "" {
		return false
	}
	if Getenv("ACH_API_KEY") != "" {
		return true
	}
	return p.APIKeyFlag != ""
}

// IsHalfSet reports the half-set state: ACH_BASE_URL is set but NO
// credential resolves. This is the explicit error branch that
// prevents a user who set only ACH_BASE_URL from silently falling
// back to bare-mode disk-config (T-06-07-01).
func IsHalfSet(p Params) bool {
	if Getenv("ACH_BASE_URL") == "" {
		return false
	}
	return !IsActive(p)
}

// GuardCommand is the single composite check every subcommand RunE
// invokes. The check order is fixed (more specific → more general):
//
//  1. Half-set → reject with the half-set message regardless of gate.
//  2. Not synthetic-active → allow (bare-mode invocation; nothing for
//     synthetic to enforce).
//  3. Gate-in-deny-set → reject (login / logout / config), OR
//     GateEnvKeysCreate without --no-save → reject (D-08).
//  4. Synthetic + --deployment / ACH_DEPLOYMENT → reject regardless
//     of gate.
//  5. Synthetic + (allow-set ∩ env-key-reject-set) + --env-key /
//     ACH_ENV_KEY → reject.
//  6. Otherwise → allow.
//
// Returns *exit.CodedError{Code: General, ...} for rejections; nil for
// allowed invocations. Callers wrap the result with `if err != nil
// { return err }` so cmd/ach/main.go's errors.As branch maps to exit 1.
func GuardCommand(p Params) error {
	// (1) Half-set: hard reject regardless of gate.
	if IsHalfSet(p) {
		return &exit.CodedError{
			Code: exit.General,
			Msg: "synthetic mode is half-set: ACH_BASE_URL is set but no credential resolved " +
				"(set ACH_API_KEY or pass --api-key; or `unset ACH_BASE_URL` to use the disk config)",
		}
	}

	// (2) Bare mode: nothing for synthetic to enforce.
	if !IsActive(p) {
		return nil
	}

	// (3) Synthetic-active + deny-set: hard reject.
	switch p.Gate {
	case GateLogin, GateLogout, GateConfig:
		return &exit.CodedError{
			Code: exit.General,
			Msg: fmt.Sprintf(
				"ach %s is not available in synthetic mode "+
					"(ACH_BASE_URL + credential set; see CLI spec §3.3)",
				gateName(p.Gate)),
		}
	case GateEnvKeysCreate:
		if !p.NoSaveFlag {
			return &exit.CodedError{
				Code: exit.General,
				Msg: "ach env-keys create requires --no-save in synthetic mode " +
					"(ACH_BASE_URL + credential set; no writable config file — D-08)",
			}
		}
	}

	// (4) Synthetic-active + --deployment / ACH_DEPLOYMENT — reject
	// regardless of gate. The conceptual deployment under synthetic is
	// SyntheticDeploymentLabel ("(env)") and cannot be overridden.
	if p.DeploymentFlag != "" || Getenv("ACH_DEPLOYMENT") != "" {
		return &exit.CodedError{
			Code: exit.General,
			Msg: "--deployment / ACH_DEPLOYMENT cannot be used in synthetic mode " +
				"(deployment is fixed to \"(env)\"; see CLI spec §3.3)",
		}
	}

	// (5) Synthetic-active + read-only gate + --env-key / ACH_ENV_KEY
	// — reject (CLI-09: ek_ labels require the config registry, which
	// synthetic has no access to).
	if readOnlyGatesRejectingEnvKey[p.Gate] {
		if p.EnvKeyFlag != "" || Getenv("ACH_ENV_KEY") != "" {
			return &exit.CodedError{
				Code: exit.General,
				Msg: "--env-key / ACH_ENV_KEY cannot be used in synthetic mode " +
					"(ek_ labels require the config registry; CLI-09 / spec §3.3)",
			}
		}
	}

	// Allow-set silent fallthrough — the gate is permitted, all
	// cross-gate checks passed.
	_ = allowedInSyntheticGates // referenced for godoc; the actual gating logic above is sufficient.
	return nil
}
