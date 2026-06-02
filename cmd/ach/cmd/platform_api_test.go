// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"testing"
)

// setRequiredPlatformAPIEnv seeds all env vars required by
// validatePlatformAPIConfig so tests can override individual vars
// without worrying about parse failures elsewhere.
func setRequiredPlatformAPIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ACH_BASE_URL", "http://localhost:8080")
	t.Setenv("ACH_DB_URL", "postgres://ach:ach@localhost:5432/ach?sslmode=disable")
	t.Setenv("ACH_CREDENTIAL_HASH_PEPPER", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv("ACH_LITELLM_BASE_URL", "http://litellm:4000")
	t.Setenv("ACH_LITELLM_MASTER_KEY", "sk-test")
	t.Setenv("ACH_DEX_ISSUER_URL", "http://dex:5556/dex")
	t.Setenv("ACH_DEX_CLIENT_ID", "ach")
	t.Setenv("ACH_DEX_CLIENT_SECRET", "secret")
	t.Setenv("ACH_DEX_REDIRECT_URL", "http://localhost:8080/auth/callback")
	t.Setenv("ACH_REDIS_ADDR", "localhost:6379")
	t.Setenv("POD_NAMESPACE", "ach")
}

// TestValidatePlatformAPIConfig_RedisDB exercises the cmd-layer fix:
// ACH_REDIS_DB=0 must be accepted (valid Redis logical DB), and invalid
// values must be rejected with a surfaced error. Before the fix,
// MustEnvIntPositive silently coerced 0 and the error was dropped with
// `_`, so none of these assertions would have held correctly.
func TestValidatePlatformAPIConfig_RedisDB(t *testing.T) {
	t.Run("zero accepted", func(t *testing.T) {
		setRequiredPlatformAPIEnv(t)
		t.Setenv("ACH_REDIS_DB", "0")
		cfg, err := validatePlatformAPIConfig()
		if err != nil {
			t.Fatalf("ACH_REDIS_DB=0 should be accepted, got err=%v", err)
		}
		if cfg.RedisDB != 0 {
			t.Fatalf("RedisDB=%d, want 0", cfg.RedisDB)
		}
	})
	t.Run("positive accepted", func(t *testing.T) {
		setRequiredPlatformAPIEnv(t)
		t.Setenv("ACH_REDIS_DB", "3")
		cfg, err := validatePlatformAPIConfig()
		if err != nil {
			t.Fatalf("ACH_REDIS_DB=3 should be accepted, got err=%v", err)
		}
		if cfg.RedisDB != 3 {
			t.Fatalf("RedisDB=%d, want 3", cfg.RedisDB)
		}
	})
	t.Run("negative rejected", func(t *testing.T) {
		setRequiredPlatformAPIEnv(t)
		t.Setenv("ACH_REDIS_DB", "-1")
		if _, err := validatePlatformAPIConfig(); err == nil {
			t.Fatalf("ACH_REDIS_DB=-1 should fail, got nil err")
		}
	})
	t.Run("non-numeric rejected", func(t *testing.T) {
		setRequiredPlatformAPIEnv(t)
		t.Setenv("ACH_REDIS_DB", "abc")
		if _, err := validatePlatformAPIConfig(); err == nil {
			t.Fatalf("ACH_REDIS_DB=abc should fail, got nil err")
		}
	})
}
