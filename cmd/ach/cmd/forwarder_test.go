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

func TestForwarderConfig_TrustedIdP(t *testing.T) {
	setRequiredForwarderEnv(t)
	cfg, err := validateForwarderConfig()
	if err != nil || cfg.TrustedIdP != nil {
		t.Fatalf("unset must be off: %+v %v", cfg, err)
	}
	t.Setenv("ACH_TRUSTED_IDP_ISSUER", "https://auth.example.com/")
	if _, err = validateForwarderConfig(); err == nil || !strings.Contains(err.Error(), "ACH_TRUSTED_IDP_AUDIENCES") {
		t.Fatalf("issuer without audiences must refuse to start: %v", err)
	}
	t.Setenv("ACH_TRUSTED_IDP_AUDIENCES", `[]`)
	if _, err = validateForwarderConfig(); err == nil {
		t.Fatal("empty audience list must refuse to start")
	}
	t.Setenv("ACH_TRUSTED_IDP_AUDIENCES", `["chat"]`)
	cfg, err = validateForwarderConfig()
	if err != nil || cfg.TrustedIdP == nil || cfg.TrustedIdP.Issuer != "https://auth.example.com/" ||
		cfg.TrustedIdP.Claim != "email" || len(cfg.TrustedIdP.Audiences) != 1 {
		t.Fatalf("set: %+v %v", cfg, err)
	}
	t.Setenv("ACH_TRUSTED_IDP_CLAIM", "preferred_username")
	if cfg, err = validateForwarderConfig(); err != nil || cfg.TrustedIdP.Claim != "preferred_username" {
		t.Fatalf("claim: %+v %v", cfg, err)
	}
}
