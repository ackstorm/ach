// SPDX-License-Identifier: Apache-2.0

package litellmtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
)

// The canned values the sign-in tests rely on.
func TestNoopClientCannedValues(t *testing.T) {
	noop := &NoopClient{}

	t.Run("UserNew echoes email + synthesizes user_id", func(t *testing.T) {
		got, err := noop.UserNew(context.Background(), &litellm.UserNewRequest{UserEmail: "a@b.c"})
		if err != nil {
			t.Fatalf("UserNew: %v", err)
		}
		if got == nil || got.UserEmail != "a@b.c" {
			t.Errorf("want UserEmail=a@b.c, got %+v", got)
		}
		// Synthesized user_id contract: prefixed with "noop-" so callers
		// can distinguish a stubbed value from a real LiteLLM-assigned id.
		if !strings.HasPrefix(got.UserID, "noop-") {
			t.Errorf("UserID: want 'noop-' prefix, got %q", got.UserID)
		}
	})

	t.Run("UserInfoByEmail returns litellm.ErrNotFound by default", func(t *testing.T) {
		got, err := noop.UserInfoByEmail(context.Background(), "any@x")
		if !errors.Is(err, litellm.ErrNotFound) {
			t.Errorf("want litellm.ErrNotFound, got %v", err)
		}
		if got != nil {
			t.Errorf("want nil litellm.UserInfo, got %+v", got)
		}
	})

	t.Run("TeamMemberAdd returns nil unconditionally", func(t *testing.T) {
		if err := noop.TeamMemberAdd(context.Background(), "default", "u-1", "user"); err != nil {
			t.Errorf("want nil, got %v", err)
		}
	})

	t.Run("KeyGenerate echoes Key and synthesizes Token", func(t *testing.T) {
		got, err := noop.KeyGenerate(context.Background(), &litellm.KeyGenerateRequest{
			Key:    "pk_xxx",
			UserID: "u-1",
		})
		if err != nil {
			t.Fatalf("KeyGenerate: %v", err)
		}
		if got == nil {
			t.Fatal("nil response")
		}
		if got.Key != "pk_xxx" {
			t.Errorf("Key: want pk_xxx (echoed), got %q", got.Key)
		}
		if got.Token != "noop-token-u-1" {
			t.Errorf("Token: want noop-token-u-1 (synthesized), got %q", got.Token)
		}
		if got.UserID != "u-1" {
			t.Errorf("UserID: want u-1, got %q", got.UserID)
		}
	})
}
