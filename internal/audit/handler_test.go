// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/audit"
)

// TestNewLogger_EmitsAuditTrue asserts the D-17 contract: every record
// emitted by the audit logger carries a top-level audit=true (bool, not
// string) so log shippers can split via that predicate.
func TestNewLogger_EmitsAuditTrue(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)
	logger.Info("event")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw=%q", err, buf.String())
	}
	if got, ok := m["audit"]; !ok {
		t.Fatalf("audit attribute missing from record: %v", m)
	} else if v, isBool := got.(bool); !isBool || !v {
		t.Fatalf("audit attribute = %#v, want bool true", got)
	}
	if msg, _ := m["msg"].(string); msg != "event" {
		t.Fatalf("msg = %q, want %q", msg, "event")
	}
}

// TestNewLogger_PreservesUserAttrs asserts the D-18 event shape
// round-trips: caller-supplied attributes appear in the JSON output
// verbatim alongside the audit=true predicate.
func TestNewLogger_PreservesUserAttrs(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)
	logger.Info("operator.orphan-cleanup",
		"target.kind", "litellm_key",
		"target.name", "sk-abc123",
		"outcome", "success",
	)

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	want := map[string]string{
		"target.kind": "litellm_key",
		"target.name": "sk-abc123",
		"outcome":     "success",
	}
	for k, v := range want {
		got, _ := m[k].(string)
		if got != v {
			t.Fatalf("attr %q = %q, want %q (full record: %v)", k, got, v, m)
		}
	}
	if v, ok := m["audit"].(bool); !ok || !v {
		t.Fatalf("audit=true lost after user attrs; got %v", m["audit"])
	}
	if msg, _ := m["msg"].(string); msg != "operator.orphan-cleanup" {
		t.Fatalf("msg = %q, want %q", msg, "operator.orphan-cleanup")
	}
}

// TestNewLogger_LevelFiltering asserts the slog.LevelInfo floor: Debug
// records emit nothing; Info records emit at least one byte.
func TestNewLogger_LevelFiltering(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	logger.Debug("should-not-emit", "k", "v")
	if buf.Len() != 0 {
		t.Fatalf("Debug emitted bytes (len=%d) but level should be Info-only: %q",
			buf.Len(), buf.String())
	}

	logger.Info("should-emit")
	if buf.Len() == 0 {
		t.Fatalf("Info emitted no bytes; expected at least one JSON line")
	}
}

// TestNewLogger_MultipleEntries asserts each Info call produces one
// newline-terminated JSON object (slog.JSONHandler default).
func TestNewLogger_MultipleEntries(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := audit.NewLogger(buf)

	logger.Info("e1")
	logger.Info("e2")
	logger.Info("e3")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	nonEmpty := lines[:0]
	for _, line := range lines {
		if line != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if len(nonEmpty) != 3 {
		t.Fatalf("got %d non-empty lines, want 3: %q", len(nonEmpty), buf.String())
	}
	for i, line := range nonEmpty {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d not valid JSON: %v (%q)", i, err, line)
		}
	}
}

// TestNewLogger_AcceptsIoDiscard asserts the constructor accepts
// io.Discard without panic; downstream emission is a no-op but does
// not error.
func TestNewLogger_AcceptsIoDiscard(t *testing.T) {
	logger := audit.NewLogger(io.Discard)
	logger.Info("event", "k", "v") // must not panic
}
