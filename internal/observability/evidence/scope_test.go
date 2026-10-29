// SPDX-License-Identifier: Apache-2.0

package evidence

import (
	"errors"
	"testing"
)

func TestNewUserScopeNormalisesAndRejectsEmpty(t *testing.T) {
	s, err := NewUserScope("  Ana@Corp.COM ")
	if err != nil || s.Email() != "ana@corp.com" {
		t.Fatalf("%v %q", err, s.Email())
	}
	if _, err := NewUserScope(" "); !errors.Is(err, ErrNoScope) {
		t.Fatalf("empty must be ErrNoScope, got %v", err)
	}
}
