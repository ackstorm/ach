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

// TestLogout_WipesPK_PreservesURL is Test 9: ach logout removes pk
// from active deployment, leaves URL + EK map intact, preserves
// default.
func TestLogout_WipesPK_PreservesURL(t *testing.T) {
	dir := whoamiTestEnv(t) // reuse synthetic-clean env helper
	path := seedConfig(t, dir, "prod", &config.Deployment{
		URL: "https://hub.example",
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
		EK:  map[string]string{"demo": "ek_aaaaaaaaaaaaaaaaaaaaafghij"},
	})

	stdout, _, code, err := executeLogout(t)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}

	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Default != "prod" {
		t.Errorf("default = %q; want prod (preserved)", f.Default)
	}
	dep := f.Deployments["prod"]
	if dep == nil {
		t.Fatal("deployments.prod removed (should only wipe pk)")
	}
	if dep.URL != "https://hub.example" {
		t.Errorf("url wiped: %q", dep.URL)
	}
	if dep.PK != "" {
		t.Errorf("pk = %q; want empty (D-06)", dep.PK)
	}
	if dep.EK["demo"] != "ek_aaaaaaaaaaaaaaaaaaaaafghij" {
		t.Errorf("ek map clobbered: %+v", dep.EK)
	}
	if !strings.Contains(stdout, "prod") {
		t.Errorf("stdout missing deployment name; got: %s", stdout)
	}
}

// TestLogout_SyntheticMode_Exit1 is Test 10: synthetic mode → exit 1.
func TestLogout_SyntheticMode_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	t.Setenv("ACH_BASE_URL", "https://synth.example")
	t.Setenv("ACH_API_KEY", "pk_synthetic_test_key_aaaaaaaaaa")

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

// TestLogout_NoDeployment_Exit1 is Test 11: no resolvable deployment
// → exit 1.
func TestLogout_NoDeployment_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	// No seed config; XDG_CONFIG_HOME is empty.

	_, _, code, err := executeLogout(t)
	if err == nil {
		t.Fatal("expected no-deployment error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
}
