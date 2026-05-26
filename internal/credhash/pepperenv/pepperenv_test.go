/*
Copyright 2026 ACKstorm.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pepperenv

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestLoad_Missing(t *testing.T) {
	t.Setenv(EnvVarName, "")
	_, err := Load()
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("expected ErrMissing; got %v", err)
	}
	if !strings.Contains(err.Error(), EnvVarName) {
		t.Errorf("error should mention env var name; got %q", err.Error())
	}
}

func TestLoad_Placeholder(t *testing.T) {
	t.Setenv(EnvVarName, PlaceholderPrefix+"ignore-me")
	_, err := Load()
	if !errors.Is(err, ErrPlaceholder) {
		t.Fatalf("expected ErrPlaceholder; got %v", err)
	}
	// Error text must not include the pepper VALUE (only the prefix).
	if strings.Contains(err.Error(), "ignore-me") {
		t.Errorf("error must not leak pepper value; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), PlaceholderPrefix) {
		t.Errorf("error should mention placeholder prefix; got %q", err.Error())
	}
}

func TestLoad_Valid(t *testing.T) {
	const realPepper = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	t.Setenv(EnvVarName, realPepper)
	got, err := Load()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !bytes.Equal(got, []byte(realPepper)) {
		t.Errorf("pepper mismatch: got %d bytes", len(got))
	}
}
