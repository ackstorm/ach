// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/audit"
)

// decodeOne decodes the first JSON object written to buf (slog
// JSONHandler emits one newline-terminated record per Info call) and
// returns the resulting map for attribute assertions.
func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimRight(buf.String(), "\n")
	if line == "" {
		t.Fatalf("emit wrote nothing to buffer")
	}
	// If multiple lines were emitted, take the first (tests emit one).
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw=%q", err, line)
	}
	return m
}

// TestEmitAuditBasic asserts the round-trip: an Event composed with
// the §18.2 schema fields produces a single JSON record carrying
// audit=true (from NewLogger) + msg=<action> + the action/outcome/
// actor/request_id attributes + the key.id attribute (because KeyID
// is non-empty).
func TestEmitAuditBasic(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	audit.EmitAudit(context.Background(), logger, audit.Event{
		Action:    audit.ActionSSOLogin,
		Outcome:   audit.OutcomeCreated,
		Actor:     "ns-a/user@x",
		RequestID: "req_abc",
		KeyID:     "pkid_xyz",
	})

	m := decodeOne(t, buf)
	if got, _ := m["audit"].(bool); !got {
		t.Fatalf("audit=true missing or wrong type: %#v", m["audit"])
	}
	for k, want := range map[string]string{
		"msg":        "platform.sso.login",
		"action":     "platform.sso.login",
		"outcome":    "created",
		"actor":      "ns-a/user@x",
		"request_id": "req_abc",
		"key.id":     "pkid_xyz",
	} {
		got, _ := m[k].(string)
		if got != want {
			t.Fatalf("attr %q = %q, want %q (full record: %v)", k, got, want, m)
		}
	}
}

// TestEmitAuditTargetIncluded asserts target.kind + target.name are
// emitted when Event.Target is non-nil.
func TestEmitAuditTargetIncluded(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	audit.EmitAudit(context.Background(), logger, audit.Event{
		Action:    audit.ActionEkCreate,
		Outcome:   audit.OutcomeCreated,
		Actor:     "ns-a/user@x",
		RequestID: "req_1",
		KeyID:     "ekid_42",
		Target:    &audit.Target{Kind: "environment", Name: "prod"},
	})

	m := decodeOne(t, buf)
	if got, _ := m["target.kind"].(string); got != "environment" {
		t.Fatalf("target.kind = %q, want %q (record: %v)", got, "environment", m)
	}
	if got, _ := m["target.name"].(string); got != "prod" {
		t.Fatalf("target.name = %q, want %q (record: %v)", got, "prod", m)
	}
}

// TestEmitAuditTargetOmitted asserts that target.kind / target.name
// are ABSENT from the record when Event.Target is nil (some events —
// e.g. SSO state-mismatch failures — do not have a resource target).
func TestEmitAuditTargetOmitted(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	audit.EmitAudit(context.Background(), logger, audit.Event{
		Action:    audit.ActionSSOLogin,
		Outcome:   audit.OutcomeStateInvalid,
		Actor:     "ns-a/-",
		RequestID: "req_2",
		KeyID:     "",
		Target:    nil,
	})

	m := decodeOne(t, buf)
	if _, ok := m["target.kind"]; ok {
		t.Fatalf("target.kind must be absent when Target is nil, got record: %v", m)
	}
	if _, ok := m["target.name"]; ok {
		t.Fatalf("target.name must be absent when Target is nil, got record: %v", m)
	}
}

// TestEmitAuditExtraRoundTrip asserts caller-supplied Extra k/v pairs
// reach the JSON record verbatim.
func TestEmitAuditExtraRoundTrip(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	audit.EmitAudit(context.Background(), logger, audit.Event{
		Action:    audit.ActionAdminRefresh,
		Outcome:   audit.OutcomeCreated,
		Actor:     "ns-a/admin@x",
		RequestID: "req_3",
		KeyID:     "pkid_admin",
		Extra:     map[string]string{"team": "default"},
	})

	m := decodeOne(t, buf)
	if got, _ := m["team"].(string); got != "default" {
		t.Fatalf("extra attr team = %q, want %q (record: %v)", got, "default", m)
	}
}

// TestEmitAuditEmptyKeyIDOmitted asserts the key.id attribute is
// absent when Event.KeyID == "" (e.g. an SSO failure that never
// resolves a key_id).
func TestEmitAuditEmptyKeyIDOmitted(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	audit.EmitAudit(context.Background(), logger, audit.Event{
		Action:    audit.ActionSSOLogin,
		Outcome:   audit.OutcomeStateInvalid,
		Actor:     "ns-a/-",
		RequestID: "req_4",
		KeyID:     "",
	})

	m := decodeOne(t, buf)
	if _, ok := m["key.id"]; ok {
		t.Fatalf("key.id must be absent when KeyID is empty, got record: %v", m)
	}
}

// TestEmitAudit_FirstClassFields asserts the G20 first-class governance/
// forensics fields (Environment, SourceIP, UserAgent, Route) are emitted
// under their canonical attribute keys when set.
func TestEmitAudit_FirstClassFields(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	audit.EmitAudit(context.Background(), logger, audit.Event{
		Action:      audit.ActionHydrate,
		Outcome:     audit.OutcomeCreated,
		Actor:       "ns-a/user@x",
		RequestID:   "req_g20",
		Environment: "prod",
		SourceIP:    "1.2.3.4",
		UserAgent:   "ach-cli/1.0",
		Route:       "/platform/hydrate",
	})

	m := decodeOne(t, buf)
	for k, want := range map[string]string{
		"environment":       "prod",
		"source.ip":         "1.2.3.4",
		"source.user_agent": "ach-cli/1.0",
		"route":             "/platform/hydrate",
	} {
		got, _ := m[k].(string)
		if got != want {
			t.Fatalf("attr %q = %q, want %q (full record: %v)", k, got, want, m)
		}
	}
}

// TestEmitAuditPlaintextDisciplineDocumented asserts the helper does
// NOT scrub Event.Extra — the documented audit-safety contract is
// caller-side discipline (per audit/doc.go). This test verifies the
// transport-raw behavior: if the caller (wrongly) puts a plaintext
// into Extra, it lands in the JSON record verbatim. The doc comment
// on EmitAudit MUST forbid this; the function does not.
//
// Test purpose: regression catch for any future "auto-scrub" change
// that would silently mask handler bugs.
func TestEmitAuditPlaintextDisciplineDocumented(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	// This is a CONTRACT VIOLATION (Extra MUST NOT carry plaintext)
	// but the helper does not enforce it — discipline over scrubbing.
	audit.EmitAudit(context.Background(), logger, audit.Event{
		Action:    audit.ActionSSOLogin,
		Outcome:   audit.OutcomeCreated,
		Actor:     "ns-a/user@x",
		RequestID: "req_5",
		KeyID:     "pkid_xyz",
		Extra:     map[string]string{"violator_field": "raw-string"},
	})

	m := decodeOne(t, buf)
	if got, _ := m["violator_field"].(string); got != "raw-string" {
		t.Fatalf("helper unexpectedly scrubbed Extra; got %q, want raw passthrough (record: %v)",
			got, m)
	}
}
