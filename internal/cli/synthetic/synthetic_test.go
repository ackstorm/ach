// SPDX-License-Identifier: Apache-2.0

package synthetic_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

var rawEK = "ek-" + strings.Repeat("a", 64)

func clearAchEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"ACH_URL", "ACH_KEY", "ACH_PROFILE"} {
		t.Setenv(k, "")
	}
}

func synth(t *testing.T) {
	t.Helper()
	clearAchEnv(t)
	t.Setenv("ACH_URL", "https://hub.test")
	t.Setenv("ACH_KEY", rawEK)
}

func wantGeneral(t *testing.T, err error, sub string) {
	t.Helper()
	var ce *exit.CodedError
	if !errors.As(err, &ce) || ce.Code != exit.General || !strings.Contains(ce.Msg, sub) {
		t.Fatalf("err = %v; want exit 1 containing %q", err, sub)
	}
}

func TestIsActive(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_URL", "https://hub.test")
	if synthetic.IsActive() {
		t.Fatal("ACH_URL alone must not activate synthetic mode (login prefill only)")
	}
	t.Setenv("ACH_KEY", rawEK)
	if !synthetic.IsActive() {
		t.Fatal("ACH_URL + ACH_KEY must activate synthetic mode")
	}
}

func TestGuardCommand_OutsideSyntheticAllowsEverything(t *testing.T) {
	clearAchEnv(t)
	t.Setenv("ACH_URL", "https://hub.test")
	for _, g := range []synthetic.Gate{synthetic.GateAPI, synthetic.GateSession, synthetic.GateKeysCreate} {
		if err := synthetic.GuardCommand(synthetic.Params{Gate: g, ProfileFlag: "p", KeyFlag: "name"}); err != nil {
			t.Errorf("gate %d: %v", g, err)
		}
	}
}

func TestGuardCommand_Synthetic(t *testing.T) {
	synth(t)
	if err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateAPI}); err != nil {
		t.Fatalf("API gate with raw ACH_KEY: %v", err)
	}
	if err := synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateAPI, KeyFlag: "ek-" + strings.Repeat("b", 64)}); err != nil {
		t.Fatalf("raw --key: %v", err)
	}
	wantGeneral(t, synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateSession}), "not available in synthetic mode")
	wantGeneral(t, synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateKeysCreate}), "--no-save")
	wantGeneral(t, synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateAPI, ProfileFlag: "p"}), "--profile")
	wantGeneral(t, synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateAPI, KeyFlag: "laptop"}), "saved key name needs a profile")
}

func TestGuardCommand_SyntheticRefusesACHProfileAndNamedACHKey(t *testing.T) {
	synth(t)
	t.Setenv("ACH_PROFILE", "p")
	wantGeneral(t, synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateAPI}), "ACH_PROFILE")
	t.Setenv("ACH_PROFILE", "")
	t.Setenv("ACH_KEY", "laptop")
	wantGeneral(t, synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateAPI}), "ACH_KEY")
}
