// SPDX-License-Identifier: Apache-2.0

// Synthetic mode (ACH_URL + ACH_KEY) across the command tree: every
// network command refuses --profile and a --key name; every session /
// profile command refuses outright.

package cmd

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
)

func synthTestEnv(t *testing.T) {
	t.Helper()
	credTestEnv(t)
	t.Setenv("ACH_URL", "https://hub.test")
	t.Setenv("ACH_KEY", testEK)
}

// networkCommands are one invocation per network command family.
var networkCommands = map[string][]string{
	"whoami":       {"whoami"},
	"env list":     {"env", "list"},
	"env describe": {"env", "describe", "demo"},
	"env hydrate":  {"env", "hydrate", "demo", "--no-warnings"},
	"keys create":  {"keys", "create", "demo", "--name", "ci", "--no-save"},
	"keys list":    {"keys", "list"},
	"keys revoke":  {"keys", "revoke", "ekid_abc", "--yes"},
	"admin list":   {"admin", "list", "plugins"},
	"env fetch":    {"env", "fetch", "demo", "prompt", "p"},
}

func runSynthRoot(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	root := newRootCmdForTest()
	root.AddCommand(newWhoamiCmd(), newEnvCmd(), newAdminCmd(), newLoginCmd(),
		newLogoutCmd(), newTokenCmd(), newProfileCmd())
	return executeCommand(t, root, args...)
}

func TestSyntheticGuard_ProfileFlagRejected(t *testing.T) {
	for name, args := range networkCommands {
		t.Run(name, func(t *testing.T) {
			synthTestEnv(t)
			_, _, code, err := runSynthRoot(t, append(args, "--profile", "prod")...)
			if err == nil || code != exit.General || !strings.Contains(err.Error(), "--profile") {
				t.Fatalf("code=%d err=%v; want exit 1 naming --profile", code, err)
			}
		})
	}
}

func TestSyntheticGuard_KeyNameRejected(t *testing.T) {
	for name, args := range networkCommands {
		t.Run(name, func(t *testing.T) {
			synthTestEnv(t)
			_, _, code, err := runSynthRoot(t, append(args, "--key", "laptop")...)
			if err == nil || code != exit.General || !strings.Contains(err.Error(), "saved key name needs a profile") {
				t.Fatalf("code=%d err=%v; want exit 1 for a key name", code, err)
			}
		})
	}
}

func TestSyntheticGuard_SessionCommandsRefused(t *testing.T) {
	for _, args := range [][]string{
		{"login", "https://hub.test"}, {"logout"}, {"token"},
		{"profile", "list"}, {"profile", "show"}, {"profile", "use", "p"},
		{"profile", "add", "ci", "--url", "https://h", "--key", testEK},
		{"profile", "rename", "a", "b"}, {"profile", "remove", "p", "--force"},
		{"keys", "create", "demo", "--name", "ci"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			synthTestEnv(t)
			_, _, code, err := runSynthRoot(t, args...)
			if err == nil || code != exit.General || !strings.Contains(err.Error(), "synthetic mode") {
				t.Fatalf("code=%d err=%v; want exit 1 (synthetic mode)", code, err)
			}
		})
	}
}
