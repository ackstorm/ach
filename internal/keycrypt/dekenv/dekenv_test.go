// SPDX-License-Identifier: Apache-2.0

package dekenv

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestLoad_Valid(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv(EnvVarName, base64.StdEncoding.EncodeToString(key))
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("len = %d, want 32", len(got))
	}
}

func TestLoad_Missing(t *testing.T) {
	t.Setenv(EnvVarName, "")
	if _, err := Load(); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
}

func TestLoad_Placeholder(t *testing.T) {
	t.Setenv(EnvVarName, PlaceholderPrefix+"whatever")
	if _, err := Load(); !errors.Is(err, ErrPlaceholder) {
		t.Fatalf("want ErrPlaceholder, got %v", err)
	}
}

func TestLoad_WrongLength(t *testing.T) {
	t.Setenv(EnvVarName, base64.StdEncoding.EncodeToString(make([]byte, 16)))
	if _, err := Load(); err == nil {
		t.Fatal("want error for 16-byte key")
	}
}

func TestLoad_NotBase64(t *testing.T) {
	t.Setenv(EnvVarName, "!!!not base64!!!")
	if _, err := Load(); err == nil {
		t.Fatal("want error for non-base64 value")
	}
}
