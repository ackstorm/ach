// SPDX-License-Identifier: Apache-2.0

package synthetic_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// clearAchEnv unsets every ACH_* env var that synthetic.GuardCommand
// inspects so each test starts hermetic. t.Setenv with the empty string
// is the documented way to scrub an env var for the duration of t.
func clearAchEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ACH_BASE_URL", "")
	t.Setenv("ACH_API_KEY", "")
	t.Setenv("ACH_ENV_KEY", "")
	t.Setenv("ACH_DEPLOYMENT", "")
}

// TestSyntheticDeploymentLabel asserts the exact constant value Phase 7
// will consume (CLI-07 last clause). Drift here breaks Phase 7's
// state.json schema. Test 12.
func TestSyntheticDeploymentLabel(t *testing.T) {
	if synthetic.SyntheticDeploymentLabel != "(env)" {
		t.Errorf("SyntheticDeploymentLabel = %q; want %q",
			synthetic.SyntheticDeploymentLabel, "(env)")
	}
}

// TestIsActive_TrueWhenBaseURLAndAPIKeySet — Test 1.
func TestIsActive_TrueWhenBaseURLAndAPIKeySet(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	if !synthetic.IsActive(synthetic.Params{Gate: synthetic.GateLogin}) {
		t.Error("IsActive = false; want true when both env vars set")
	}
}

// TestIsActive_TrueWhenBaseURLAndAPIKeyFlag — Test 1b: --api-key flag
// (D-11) also activates synthetic when ACH_BASE_URL is set.
func TestIsActive_TrueWhenBaseURLAndAPIKeyFlag(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")

	p := synthetic.Params{
		Gate:       synthetic.GateHydrate,
		APIKeyFlag: "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	}
	if !synthetic.IsActive(p) {
		t.Error("IsActive = false; want true when --api-key resolves")
	}
}

// TestIsActive_FalseWhenOnlyBaseURLSet — Test 2 (partial).
func TestIsActive_FalseWhenOnlyBaseURLSet(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")

	if synthetic.IsActive(synthetic.Params{Gate: synthetic.GateLogin}) {
		t.Error("IsActive = true; want false when only ACH_BASE_URL set")
	}
}

// TestIsActive_FalseWhenOnlyAPIKeySet — Test 2 (other half).
func TestIsActive_FalseWhenOnlyAPIKeySet(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	if synthetic.IsActive(synthetic.Params{Gate: synthetic.GateLogin}) {
		t.Error("IsActive = true; want false when only ACH_API_KEY set")
	}
}

// TestIsHalfSet_TrueWhenBaseURLOnly — Test 3.
func TestIsHalfSet_TrueWhenBaseURLOnly(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")

	if !synthetic.IsHalfSet(synthetic.Params{Gate: synthetic.GateWhoami}) {
		t.Error("IsHalfSet = false; want true when ACH_BASE_URL set without credential")
	}
}

// TestIsHalfSet_FalseInOtherModes — Test 4: not-synthetic, fully
// synthetic, and bare modes all yield IsHalfSet=false.
func TestIsHalfSet_FalseInOtherModes(t *testing.T) {
	// Bare mode — no env vars at all.
	clearAchEnv(t)
	if synthetic.IsHalfSet(synthetic.Params{Gate: synthetic.GateWhoami}) {
		t.Error("bare mode: IsHalfSet = true; want false")
	}

	// Fully synthetic — both env vars set.
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")
	if synthetic.IsHalfSet(synthetic.Params{Gate: synthetic.GateWhoami}) {
		t.Error("fully synthetic: IsHalfSet = true; want false")
	}

	// Half-set via --api-key flag (still resolves to active, not half).
	t.Setenv("ACH_API_KEY", "")
	p := synthetic.Params{
		Gate:       synthetic.GateHydrate,
		APIKeyFlag: "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	}
	if synthetic.IsHalfSet(p) {
		t.Error("--api-key flag synthetic: IsHalfSet = true; want false")
	}
}

// assertCoded asserts err is *exit.CodedError with Code==General and
// returns it for further inspection of Msg. Centralized so each gate
// test stays short.
func assertCoded(t *testing.T, err error, hint string) *exit.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: err = nil; want *exit.CodedError", hint)
	}
	var cErr *exit.CodedError
	if !errors.As(err, &cErr) {
		t.Fatalf("%s: err = %T (%v); want *exit.CodedError", hint, err, err)
	}
	if cErr.Code != exit.General {
		t.Errorf("%s: code = %d; want %d (General)", hint, cErr.Code, exit.General)
	}
	return cErr
}

// TestGuardCommand_GateLogin_SyntheticReject — Test 5.
func TestGuardCommand_GateLogin_SyntheticReject(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateLogin})
	cErr := assertCoded(t, err, "GateLogin synthetic")
	if !strings.Contains(cErr.Msg, "login") || !strings.Contains(cErr.Msg, "synthetic") {
		t.Errorf("msg = %q; want substrings 'login' and 'synthetic'", cErr.Msg)
	}
}

// TestGuardCommand_GateLogoutConfig_SyntheticReject — Test 6.
func TestGuardCommand_GateLogoutConfig_SyntheticReject(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	for _, tc := range []struct {
		name string
		gate synthetic.Gate
		hint string
	}{
		{"logout", synthetic.GateLogout, "logout"},
		{"config", synthetic.GateConfig, "config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := synthetic.GuardCommand(synthetic.Params{Gate: tc.gate})
			cErr := assertCoded(t, err, tc.name)
			if !strings.Contains(cErr.Msg, tc.hint) {
				t.Errorf("msg = %q; want substring %q", cErr.Msg, tc.hint)
			}
			if !strings.Contains(cErr.Msg, "synthetic") {
				t.Errorf("msg = %q; want substring 'synthetic'", cErr.Msg)
			}
		})
	}
}

// TestGuardCommand_GateEnvKeysCreate_RequiresNoSave — Test 6 (env-keys
// create branch) + Test 7 (allows --no-save).
func TestGuardCommand_GateEnvKeysCreate_RequiresNoSave(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	// Without --no-save → reject.
	err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateEnvKeysCreate})
	cErr := assertCoded(t, err, "env-keys create no --no-save")
	if !strings.Contains(cErr.Msg, "--no-save") {
		t.Errorf("msg = %q; want substring '--no-save'", cErr.Msg)
	}

	// With --no-save → allow.
	if err := synthetic.GuardCommand(synthetic.Params{
		Gate:       synthetic.GateEnvKeysCreate,
		NoSaveFlag: true,
	}); err != nil {
		t.Errorf("env-keys create + --no-save: err = %v; want nil", err)
	}
}

// TestGuardCommand_GateHydrate_SyntheticAllow — Test 8.
func TestGuardCommand_GateHydrate_SyntheticAllow(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	if err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateHydrate}); err != nil {
		t.Errorf("GateHydrate synthetic: err = %v; want nil", err)
	}
}

// TestGuardCommand_DeploymentRejectedInSynthetic — Test 9.
func TestGuardCommand_DeploymentRejectedInSynthetic(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	// Via flag.
	err := synthetic.GuardCommand(synthetic.Params{
		Gate:           synthetic.GateHydrate,
		DeploymentFlag: "prod",
	})
	cErr := assertCoded(t, err, "deployment flag in synthetic")
	if !strings.Contains(cErr.Msg, "--deployment") || !strings.Contains(cErr.Msg, "ACH_DEPLOYMENT") {
		t.Errorf("msg = %q; want substrings '--deployment' and 'ACH_DEPLOYMENT'", cErr.Msg)
	}

	// Via env var.
	t.Setenv("ACH_DEPLOYMENT", "prod")
	err = synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateHydrate})
	cErr = assertCoded(t, err, "ACH_DEPLOYMENT in synthetic")
	if !strings.Contains(cErr.Msg, "--deployment") {
		t.Errorf("msg = %q; want substring '--deployment'", cErr.Msg)
	}
}

// TestGuardCommand_EnvKeyRejectedInSyntheticOnReadCommands — Test 10.
// Covers hydrate, whoami, env list/describe, env-keys list/revoke.
func TestGuardCommand_EnvKeyRejectedInSyntheticOnReadCommands(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	gates := []struct {
		name string
		gate synthetic.Gate
	}{
		{"hydrate", synthetic.GateHydrate},
		{"whoami", synthetic.GateWhoami},
		{"env-list", synthetic.GateEnvList},
		{"env-describe", synthetic.GateEnvDescribe},
		{"env-keys-list", synthetic.GateEnvKeysList},
		{"env-keys-revoke", synthetic.GateEnvKeysRevoke},
	}

	for _, g := range gates {
		t.Run(g.name+"_flag", func(t *testing.T) {
			err := synthetic.GuardCommand(synthetic.Params{
				Gate:       g.gate,
				EnvKeyFlag: "local-laptop",
			})
			cErr := assertCoded(t, err, g.name+" --env-key flag")
			if !strings.Contains(cErr.Msg, "--env-key") {
				t.Errorf("msg = %q; want substring '--env-key'", cErr.Msg)
			}
		})
	}

	t.Run("hydrate_env", func(t *testing.T) {
		t.Setenv("ACH_ENV_KEY", "local-laptop")
		err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateHydrate})
		cErr := assertCoded(t, err, "hydrate ACH_ENV_KEY")
		if !strings.Contains(cErr.Msg, "ACH_ENV_KEY") {
			t.Errorf("msg = %q; want substring 'ACH_ENV_KEY'", cErr.Msg)
		}
	})
}

// TestGuardCommand_HalfSet_AnyGate — Test 11.
func TestGuardCommand_HalfSet_AnyGate(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	// No ACH_API_KEY, no --api-key flag → half-set.

	for _, g := range []synthetic.Gate{
		synthetic.GateLogin,
		synthetic.GateLogout,
		synthetic.GateConfig,
		synthetic.GateEnvKeysCreate,
		synthetic.GateHydrate,
		synthetic.GateWhoami,
		synthetic.GateEnvList,
		synthetic.GateEnvDescribe,
		synthetic.GateEnvKeysList,
		synthetic.GateEnvKeysRevoke,
		synthetic.GateAdmin,
	} {
		err := synthetic.GuardCommand(synthetic.Params{Gate: g})
		cErr := assertCoded(t, err, "half-set")
		if !strings.Contains(cErr.Msg, "half-set") {
			t.Errorf("gate %d: msg = %q; want substring 'half-set'", g, cErr.Msg)
		}
	}
}

// TestGuardCommand_BareMode_AllGatesAllowed — defensive: when no
// synthetic env vars are set, GuardCommand is a no-op regardless of
// the gate. Ensures we don't accidentally reject config-disk mode.
func TestGuardCommand_BareMode_AllGatesAllowed(t *testing.T) {
	clearAchEnv(t)

	for _, g := range []synthetic.Gate{
		synthetic.GateLogin,
		synthetic.GateLogout,
		synthetic.GateConfig,
		synthetic.GateEnvKeysCreate,
		synthetic.GateHydrate,
		synthetic.GateWhoami,
		synthetic.GateEnvList,
		synthetic.GateEnvDescribe,
		synthetic.GateEnvKeysList,
		synthetic.GateEnvKeysRevoke,
		synthetic.GateAdmin,
	} {
		if err := synthetic.GuardCommand(synthetic.Params{
			Gate:           g,
			DeploymentFlag: "prod",
			EnvKeyFlag:     "local-laptop",
		}); err != nil {
			t.Errorf("bare mode gate %d: err = %v; want nil", g, err)
		}
	}
}

// TestGuardCommand_ReadOnlyGatesAllowedInSyntheticWithoutEnvKey —
// the allowed-in-synthetic set runs cleanly without --env-key /
// ACH_ENV_KEY / --deployment.
func TestGuardCommand_ReadOnlyGatesAllowedInSyntheticWithoutEnvKey(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_BASE_URL", "https://hub.test")
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

	for _, g := range []synthetic.Gate{
		synthetic.GateHydrate,
		synthetic.GateWhoami,
		synthetic.GateEnvList,
		synthetic.GateEnvDescribe,
		synthetic.GateEnvKeysList,
		synthetic.GateEnvKeysRevoke,
		synthetic.GateAdmin,
	} {
		if err := synthetic.GuardCommand(synthetic.Params{Gate: g}); err != nil {
			t.Errorf("synthetic gate %d: err = %v; want nil", g, err)
		}
	}
}
