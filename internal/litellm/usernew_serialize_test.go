// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"strings"
	"testing"
)

// auto_create_key must serialize to an explicit `false` (not be dropped)
// when the caller sets it via BoolPtr(false). With omitempty on a nil
// *bool, an UNSET field is omitted entirely (LiteLLM keeps its default
// auto_create_key=true) — that is the legacy behaviour we are moving away
// from at the call sites.
func TestUserNewRequest_AutoCreateKeyFalseSerializes(t *testing.T) {
	req := &UserNewRequest{
		UserEmail:     "jc@example.com",
		UserID:        "jc@example.com",
		Teams:         []string{"default"},
		AutoCreateKey: BoolPtr(false),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"auto_create_key":false`) {
		t.Errorf("expected auto_create_key:false in payload, got %s", got)
	}
	if !strings.Contains(got, `"user_id":"jc@example.com"`) {
		t.Errorf("expected user_id=email in payload, got %s", got)
	}
}

// When AutoCreateKey is nil, omitempty drops it (back-compat: the field is
// absent so LiteLLM applies its server-side default).
func TestUserNewRequest_AutoCreateKeyNilOmitted(t *testing.T) {
	req := &UserNewRequest{UserEmail: "jc@example.com"}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "auto_create_key") {
		t.Errorf("nil AutoCreateKey must be omitted, got %s", string(raw))
	}
}
