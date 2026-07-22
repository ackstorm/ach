// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserShellAliasNormalises(t *testing.T) {
	if got := UserShellAlias("  JC@Example.com "); got != "ach-user-jc@example.com" {
		t.Fatalf("UserShellAlias = %q", got)
	}
}

func TestNewUserShellRequestSetsIDAndSentinels(t *testing.T) {
	req := NewUserShellRequest("JC@Example.com")
	if req.TeamID != "ach-user-jc@example.com" || req.TeamAlias != "ach-user-jc@example.com" {
		t.Fatalf("id/alias = %q/%q", req.TeamID, req.TeamAlias)
	}
	b, _ := json.Marshal(req)
	// deny-all: the one impossible model, agents nil-UUID, MCP explicit empty.
	for _, want := range []string{`"__deny_all__"`, `"00000000-0000-0000-0000-000000000000"`, `"mcp_servers":[]`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}

func TestIsUserShellManagedAndShaped(t *testing.T) {
	managed := TeamListEntry{
		TeamAlias: "ach-user-jc@example.com",
		Models:    []string{ShellTeamDenyAllModel},
		Metadata:  json.RawMessage(`{"ach_managed":"user-shell","ach_user":"jc@example.com"}`),
	}
	if !IsUserShellManaged(managed, "jc@example.com") {
		t.Fatal("managed shell not recognised")
	}
	if IsUserShellManaged(managed, "other@example.com") {
		t.Fatal("cross-user false positive")
	}
	shapedOnly := TeamListEntry{TeamAlias: "ach-user-jc@example.com", Models: []string{ShellTeamDenyAllModel}}
	if IsUserShellManaged(shapedOnly, "jc@example.com") {
		t.Fatal("no metadata must not be 'managed'")
	}
	if !IsUserShellShaped(shapedOnly, "jc@example.com") {
		t.Fatal("shape not recognised")
	}
}
