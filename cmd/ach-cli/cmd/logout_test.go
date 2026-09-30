// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// executeLogout runs newLogoutCmd with args and returns stdout,
// stderr, exit code, raw error.
func executeLogout(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	cmd := newLogoutCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return outBuf.String(), errBuf.String(), exit.OK, nil
	}
	var cErr *exit.CodedError
	if errors.As(err, &cErr) {
		return outBuf.String(), errBuf.String(), cErr.Code, err
	}
	return outBuf.String(), errBuf.String(), exit.General, err
}

// TestLogout_ClearsSessionAndKey_KeepsSavedKeys: logout clears OAuth and
// Key, keeps URL, default and the saved keys.
func TestLogout_ClearsSessionAndKey_KeepsSavedKeys(t *testing.T) {
	dir := whoamiTestEnv(t)
	p := oauthProfile("https://hub.example")
	p.Key = testEK
	p.Keys = map[string]config.SavedKey{"laptop": {ID: "ekid_1", Key: testEKSaved}}
	path := seedConfig(t, dir, "prod", p)

	stdout, _, code, err := executeLogout(t)
	if err != nil || code != exit.OK {
		t.Fatalf("logout: code=%d err=%v", code, err)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	dep := f.Profiles["prod"]
	if f.Default != "prod" || dep == nil || dep.URL != "https://hub.example" {
		t.Fatalf("profile/URL/default must be kept: %+v", f)
	}
	if dep.OAuth != nil || dep.Key != "" {
		t.Errorf("OAuth=%v Key=%q; want both cleared", dep.OAuth, dep.Key)
	}
	if dep.Keys["laptop"] != (config.SavedKey{ID: "ekid_1", Key: testEKSaved}) {
		t.Errorf("saved keys clobbered: %+v", dep.Keys)
	}
	if !strings.Contains(stdout, `Signed out of "prod" (saved keys kept; revoke them with keys revoke)`) {
		t.Errorf("stdout = %q", stdout)
	}
}

// TestLogout_SyntheticMode_Exit1 is Test 10: synthetic mode → exit 1.
func TestLogout_SyntheticMode_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	t.Setenv("ACH_URL", "https://synth.example")
	t.Setenv("ACH_KEY", testEK)

	_, _, code, err := executeLogout(t)
	if err == nil {
		t.Fatal("expected synthetic-mode rejection")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "synthetic") {
		t.Errorf("err missing 'synthetic'; %q", err.Error())
	}
}

// TestLogout_NoProfile_Exit1 is Test 11: no resolvable profile
// → exit 1.
func TestLogout_NoProfile_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	// No seed config; XDG_CONFIG_HOME is empty.

	_, _, code, err := executeLogout(t)
	if err == nil {
		t.Fatal("expected no-profile error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
}
