// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"strings"
	"testing"
)

func setRequiredForwarderEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ACH_BASE_URL", "https://api.example.com")
	t.Setenv("ACH_DB_URL", "postgres://ach:ach@localhost:5432/ach?sslmode=disable")
	t.Setenv("ACH_CREDENTIAL_HASH_PEPPER", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv("ACH_KEY_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("ACH_REDIS_ADDR", "localhost:6379")
	t.Setenv("POD_NAMESPACE", "ach")
}

func TestForwarderConfig_CredentialHeaders(t *testing.T) {
	setRequiredForwarderEnv(t)
	cfg, err := validateForwarderConfig()
	if err != nil || len(cfg.CredentialHeaders) != 0 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	t.Setenv("ACH_CREDENTIAL_HEADERS",
		`[{"name":"X-Ach-Key","mode":"resolve"},{"name":"x-genai-api-key","mode":"passthrough"}]`)
	cfg, err = validateForwarderConfig()
	if err != nil || len(cfg.CredentialHeaders) != 2 || cfg.CredentialHeaders[0].Name != "x-ach-key" ||
		cfg.CredentialHeaders[1].Mode != "passthrough" {
		t.Fatalf("set: %+v %v", cfg, err)
	}
	t.Setenv("ACH_CREDENTIAL_HEADERS", `[{"name":"authorization","mode":"resolve"}]`)
	if _, err = validateForwarderConfig(); err == nil || !strings.Contains(err.Error(), "authorization") {
		t.Fatalf("authorization in the list must be refused: %v", err)
	}
}

func TestForwarderConfig_Profile(t *testing.T) {
	setRequiredForwarderEnv(t)
	if cfg, err := validateForwarderConfig(); err != nil || cfg.Profile != "full" {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	t.Setenv("ACH_PROFILE", "identity")
	if _, err := validateForwarderConfig(); err == nil {
		t.Fatal("identity without ACH_LITELLM_BASE_URL must fail")
	}
	t.Setenv("ACH_LITELLM_BASE_URL", "http://litellm:4000")
	if cfg, err := validateForwarderConfig(); err != nil || cfg.Profile != "identity" ||
		cfg.LiteLLMBaseURL != "http://litellm:4000" {
		t.Fatalf("identity: %+v %v", cfg, err)
	}
	t.Setenv("ACH_PROFILE", "lite")
	if _, err := validateForwarderConfig(); err == nil {
		t.Fatal("unknown profile must fail")
	}
}
