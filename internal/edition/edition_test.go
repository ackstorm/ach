// SPDX-License-Identifier: Apache-2.0

package edition

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func reset() { commands, hooks = nil, nil }

func TestEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": false, "false": false, "true": true, "1": true} {
		got, err := Enabled(func(string) string { return v })
		if err != nil || got != want {
			t.Fatalf("Enabled(%q) = %v, %v; want %v", v, got, err, want)
		}
	}
	if _, err := Enabled(func(string) string { return "yes" }); err == nil || !strings.Contains(err.Error(), `ACH_ENTERPRISE: "yes" is not a boolean`) {
		t.Fatalf("Enabled(yes) err = %v", err)
	}
}

func TestRequire(t *testing.T) {
	off := func(string) string { return "" }
	if err := Require("ach classifier", off); err == nil || err.Error() != "ach classifier is an ACH Enterprise feature: set Helm enterprise.enabled=true (ACH_ENTERPRISE=true)" {
		t.Fatalf("Require off = %v", err)
	}
	if err := Require("ach classifier", func(string) string { return "true" }); err != nil {
		t.Fatalf("Require on = %v", err)
	}
}

func TestCheckEnvRefusesEnterpriseConfigWhenDisabled(t *testing.T) {
	env := map[string]string{"ACH_OBSERVABILITY_THANOS_URL": "http://thanos"}
	err := CheckEnv(func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "ACH_OBSERVABILITY_THANOS_URL") || !strings.Contains(err.Error(), "enterprise.enabled") {
		t.Fatalf("CheckEnv = %v", err)
	}
	env["ACH_ENTERPRISE"] = "true"
	if err := CheckEnv(func(k string) string { return env[k] }); err != nil {
		t.Fatalf("CheckEnv enabled = %v", err)
	}
}

func TestCheckEnvRefusesClickHouseWhenDisabled(t *testing.T) {
	env := map[string]string{"ACH_OBSERVABILITY_CLICKHOUSE_URL": "http://clickhouse:8123"}
	err := CheckEnv(func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "ACH_OBSERVABILITY_CLICKHOUSE_URL") {
		t.Fatalf("CheckEnv = %v", err)
	}
	env["ACH_ENTERPRISE"] = "true"
	if err := CheckEnv(func(k string) string { return env[k] }); err != nil {
		t.Fatalf("CheckEnv enabled = %v", err)
	}
}

func TestEnterpriseOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	EnterpriseOnly(rec, httptest.NewRequest(http.MethodGet, "/platform/observability/cost", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"enterprise_only"`) {
		t.Fatalf("EnterpriseOnly = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRegisteredCommandIsAttached(t *testing.T) {
	reset()
	RegisterCommand(&cobra.Command{Use: "classifier", RunE: func(*cobra.Command, []string) error { return nil }})
	root := &cobra.Command{Use: "ach"}
	Commands(root)
	c, _, _ := root.Find([]string{"classifier"})
	if c.Name() != "classifier" {
		t.Fatalf("registered command not attached: %q", c.Name())
	}
}
