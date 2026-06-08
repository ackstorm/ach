// SPDX-License-Identifier: Apache-2.0

// Cross-cutting tests for the 06-07 synthetic-mode enforcement
// refactor. Each subcommand previously carried its own ad-hoc inline
// `if os.Getenv("ACH_BASE_URL") != "" && ...` check; this file proves
// the centralized internal/cli/synthetic.GuardCommand fires the same
// way (or stronger) for the cases that weren't previously covered:
//
//   - --profile rejection in synthetic on every subcommand
//   - --env-key rejection in synthetic on hydrate/whoami/env list/
//     env-describe/env-keys list/env-keys revoke
//   - half-set (ACH_BASE_URL set, NO credential) rejection on every
//     subcommand
//
// The original per-subcommand tests stay green (login/logout/config/
// env-keys-create synthetic-rejection); these are the gap-fillers.

package cmd

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

// TestSyntheticGuard_ProfileFlagRejected covers every subcommand
// that accepts --profile AND is in the synthetic.GuardCommand
// allow-set (the disposition under synthetic must be "allowed except
// for --profile / --env-key / half-set"). The deny-set
// (login/logout/config) is tested separately via the per-subcommand
// SyntheticMode_Exit1 tests already in place — those commands reject
// regardless of --profile because the gate denies first.
func TestSyntheticGuard_ProfileFlagRejected(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T) (string, string, exit.Code, error)
	}{
		{
			name: "whoami",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeWhoami(t, "--profile", "prod")
			},
		},
		{
			name: "hydrate",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeHydrate(t, "--profile", "prod",
					"demo", "--no-warnings")
			},
		},
		{
			name: "env-list",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnv(t, "list", "--profile", "prod")
			},
		},
		{
			name: "env-describe",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnv(t, "describe", "demo", "--profile", "prod")
			},
		},
		{
			name: "env-keys-create",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnvKeys(t, "", "create",
					"--environment", "demo", "--name", "x",
					"--no-save", "--profile", "prod")
			},
		},
		{
			name: "env-keys-list",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnvKeys(t, "", "list", "--profile", "prod")
			},
		},
		{
			name: "env-keys-revoke",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnvKeys(t, "", "revoke", "ekid_abc",
					"--yes", "--profile", "prod")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whoamiTestEnv(t) // resets XDG + clears ACH_*.
			t.Setenv("ACH_BASE_URL", "https://hub.test")
			t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

			_, _, code, err := tc.run(t)
			if err == nil {
				t.Fatalf("%s: expected --profile rejection in synthetic; got nil err", tc.name)
			}
			if code != exit.General {
				t.Errorf("%s: code = %d; want 1", tc.name, code)
			}
			if !strings.Contains(err.Error(), "--profile") {
				t.Errorf("%s: err missing '--profile' hint: %q", tc.name, err.Error())
			}
		})
	}
}

// TestSyntheticGuard_EnvKeyFlagRejected covers every read-side
// subcommand that accepts --env-key. Synthetic + --env-key must exit 1
// (ek_ requires the config registry — CLI-09 / spec §3.3).
func TestSyntheticGuard_EnvKeyFlagRejected(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T) (string, string, exit.Code, error)
	}{
		{
			name: "whoami",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeWhoami(t, "--env-key", "local-laptop")
			},
		},
		{
			name: "hydrate",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeHydrate(t, "--env-key", "local-laptop",
					"demo", "--no-warnings")
			},
		},
		{
			name: "env-list",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnv(t, "list", "--env-key", "local-laptop")
			},
		},
		{
			name: "env-describe",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnv(t, "describe", "demo", "--env-key", "local-laptop")
			},
		},
		{
			name: "env-keys-list",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnvKeys(t, "", "list", "--env-key", "local-laptop")
			},
		},
		{
			name: "env-keys-revoke",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnvKeys(t, "", "revoke", "ekid_abc",
					"--yes", "--env-key", "local-laptop")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whoamiTestEnv(t)
			t.Setenv("ACH_BASE_URL", "https://hub.test")
			t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")

			_, _, code, err := tc.run(t)
			if err == nil {
				t.Fatalf("%s: expected --env-key rejection in synthetic; got nil err", tc.name)
			}
			if code != exit.General {
				t.Errorf("%s: code = %d; want 1", tc.name, code)
			}
			if !strings.Contains(err.Error(), "--env-key") {
				t.Errorf("%s: err missing '--env-key' hint: %q", tc.name, err.Error())
			}
		})
	}
}

// TestSyntheticGuard_HalfSetRejected covers every subcommand under
// the half-set condition (ACH_BASE_URL set, NO credential resolves).
// Every subcommand must exit 1 with the half-set message — no silent
// fallback to bare-mode disk config (T-06-07-01).
func TestSyntheticGuard_HalfSetRejected(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T) (string, string, exit.Code, error)
	}{
		{
			name: "login",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeLogin(t, "--profile", "prod",
					"--base-url", "https://hub.test", "--no-browser")
			},
		},
		{
			name: "logout",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeLogout(t)
			},
		},
		{
			name: "config-list",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeConfig(t, "list")
			},
		},
		{
			name: "whoami",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeWhoami(t)
			},
		},
		{
			name: "hydrate",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeHydrate(t, "demo", "--no-warnings")
			},
		},
		{
			name: "env-list",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnv(t, "list")
			},
		},
		{
			name: "env-keys-create",
			run: func(t *testing.T) (string, string, exit.Code, error) {
				return executeEnvKeys(t, "", "create",
					"--environment", "demo", "--name", "x", "--no-save")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whoamiTestEnv(t)
			// Half-set: only ACH_BASE_URL set, ACH_API_KEY left blank.
			t.Setenv("ACH_BASE_URL", "https://hub.test")

			_, _, code, err := tc.run(t)
			if err == nil {
				t.Fatalf("%s: expected half-set rejection; got nil err", tc.name)
			}
			if code != exit.General {
				t.Errorf("%s: code = %d; want 1", tc.name, code)
			}
			if !strings.Contains(err.Error(), "half-set") {
				t.Errorf("%s: err missing 'half-set' hint: %q", tc.name, err.Error())
			}
		})
	}
}
