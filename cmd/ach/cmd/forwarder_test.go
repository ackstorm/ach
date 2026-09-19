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

func TestForwarderConfig_IssuerAndRawKeyHeaders(t *testing.T) {
	setRequiredForwarderEnv(t)
	cfg, err := validateForwarderConfig()
	if err != nil || cfg.Issuer != "https://api.example.com" || len(cfg.RawKeyHeaders) != 0 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}

	t.Setenv("ACH_OAUTH_ISSUER", "https://ach.example.com")
	t.Setenv("ACH_RAW_KEY_HEADERS", " X-GenAI-API-Key , x-other ,,")
	cfg, err = validateForwarderConfig()
	if err != nil || cfg.Issuer != "https://ach.example.com" ||
		strings.Join(cfg.RawKeyHeaders, ",") != "x-genai-api-key,x-other" {
		t.Fatalf("set: %+v %v", cfg, err)
	}

	t.Setenv("ACH_RAW_KEY_HEADERS", "x-genai-api-key,Authorization")
	if _, err = validateForwarderConfig(); err == nil || !strings.Contains(err.Error(), "authorization") {
		t.Fatalf("ACH slot in raw list must be refused: %v", err)
	}
	t.Setenv("ACH_RAW_KEY_HEADERS", "")
	t.Setenv("ACH_OAUTH_ISSUER", "ach.example.com")
	if _, err = validateForwarderConfig(); err == nil {
		t.Fatal("relative issuer must be refused")
	}
}
