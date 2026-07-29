// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestShellTeamAlias(t *testing.T) {
	if got := ShellTeamAlias("platform"); got != "ach-env-platform" {
		t.Fatalf("ShellTeamAlias = %q, want ach-env-platform", got)
	}
}

// TestNewShellTeamRequestSentinels is the load-bearing test of this change:
// empty model/agent lists fail OPEN in LiteLLM, so the request must carry
// both sentinels and explicit (non-nil) empty MCP lists.
func TestNewShellTeamRequestSentinels(t *testing.T) {
	req := NewShellTeamRequest("demo", nil)
	if req.TeamAlias != "ach-env-demo" {
		t.Fatalf("TeamAlias = %q", req.TeamAlias)
	}
	if len(req.Models) != 1 || req.Models[0] != ShellTeamDenyAllModel {
		t.Fatalf("Models = %v, want [%s]", req.Models, ShellTeamDenyAllModel)
	}
	op := req.ObjectPermission
	if op == nil {
		t.Fatal("ObjectPermission is nil — the team would allow every agent")
	}
	if len(op.Agents) != 1 || op.Agents[0] != ShellTeamDenyAllAgent {
		t.Fatalf("Agents = %v, want [%s]", op.Agents, ShellTeamDenyAllAgent)
	}
	if op.MCPServers == nil || len(op.MCPServers) != 0 {
		t.Fatalf("MCPServers = %v, want an explicit empty list", op.MCPServers)
	}
	if op.MCPAccessGroups == nil || len(op.MCPAccessGroups) != 0 {
		t.Fatalf("MCPAccessGroups = %v, want an explicit empty list", op.MCPAccessGroups)
	}
	if op.AgentAccessGroups == nil || len(op.AgentAccessGroups) != 0 {
		t.Fatalf("AgentAccessGroups = %v, want an explicit empty list", op.AgentAccessGroups)
	}
	if req.Metadata[ShellTeamManagedMetadataKey] != ShellTeamManagedMetadataValue {
		t.Fatalf("Metadata[%s] = %v, want %q — a freshly created shell must be marked ACH-managed",
			ShellTeamManagedMetadataKey, req.Metadata[ShellTeamManagedMetadataKey], ShellTeamManagedMetadataValue)
	}
	if req.Metadata[ShellTeamManagedEnvKey] != "demo" {
		t.Fatalf("Metadata[%s] = %v, want %q", ShellTeamManagedEnvKey, req.Metadata[ShellTeamManagedEnvKey], "demo")
	}
}

func TestNewShellTeamRequestSetsTeamIDToAlias(t *testing.T) {
	req := NewShellTeamRequest("demo", nil)
	if req.TeamID != "ach-env-demo" || req.TeamAlias != "ach-env-demo" {
		t.Fatalf("env shell id/alias = %q/%q, want ach-env-demo", req.TeamID, req.TeamAlias)
	}
}

func TestShellTeamDrifted(t *testing.T) {
	healthy := TeamListEntry{
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
	}
	if ShellTeamDrifted(healthy, nil) {
		t.Fatal("healthy shell reported as drifted")
	}

	cases := map[string]TeamListEntry{
		"models absent on a resolved read (fail-open on every model)": {
			Models:           nil,
			ObjectPermission: ShellTeamPermissions(),
		},
		"models cleared (fail-open on every model)": {
			Models:           []string{},
			ObjectPermission: ShellTeamPermissions(),
		},
		"a real model was granted directly": {
			Models:           []string{"gemini.gemini-flash-latest"},
			ObjectPermission: ShellTeamPermissions(),
		},
		"agents cleared (fail-open on every agent)": {
			Models:           []string{ShellTeamDenyAllModel},
			ObjectPermission: &TeamObjectPermission{Agents: []string{}},
		},
		"an mcp server was granted directly": {
			Models: []string{ShellTeamDenyAllModel},
			ObjectPermission: &TeamObjectPermission{
				MCPServers: []string{"mcp-slack"},
				Agents:     []string{ShellTeamDenyAllAgent},
			},
		},
	}
	for name, e := range cases {
		if !ShellTeamDrifted(e, nil) {
			t.Errorf("%s: reported as healthy, want drifted", name)
		}
	}

	// A read-back that carries NEITHER field is unverifiable, not drifted —
	// reporting drift there would make the operator write on every reconcile
	// forever against a LiteLLM whose list endpoint omits object_permission.
	if ShellTeamDrifted(TeamListEntry{TeamID: "t-1", TeamAlias: "ach-env-demo"}, nil) {
		t.Fatal("unverifiable read-back reported as drifted")
	}

	// A /v2/team/list-shaped read: models resolved (and healthy), the
	// object_permission relation not. That single unresolved dimension must
	// not be reported as drift.
	if ShellTeamDrifted(TeamListEntry{
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: nil,
	}, nil) {
		t.Fatal("healthy models with unresolved object_permission reported as drifted")
	}
}

// TestIsShellTeamManaged: the Fix 2 ownership gate. Only metadata carrying
// BOTH the marker key/value AND the matching environment name counts —
// absent, unparseable, or mismatched metadata must fail safe to "not managed"
// so ensureShellTeam/deleteShellTeam refuse to touch a team they cannot prove
// they own.
func TestIsShellTeamManaged(t *testing.T) {
	marked := func(env string) []byte {
		raw, err := json.Marshal(map[string]string{
			ShellTeamManagedMetadataKey: ShellTeamManagedMetadataValue,
			ShellTeamManagedEnvKey:      env,
		})
		if err != nil {
			t.Fatalf("marshal metadata: %v", err)
		}
		return raw
	}

	if !IsShellTeamManaged(TeamListEntry{Metadata: marked("demo")}, "demo") {
		t.Error("correctly-marked entry reported as not managed")
	}
	if IsShellTeamManaged(TeamListEntry{}, "demo") {
		t.Error("absent metadata reported as managed")
	}
	if IsShellTeamManaged(TeamListEntry{Metadata: []byte("not json")}, "demo") {
		t.Error("unparseable metadata reported as managed")
	}
	if IsShellTeamManaged(TeamListEntry{Metadata: marked("other-env")}, "demo") {
		t.Error("metadata marked for a different environment reported as managed")
	}
	if IsShellTeamManaged(TeamListEntry{Metadata: []byte(`{"some_other_key":"x"}`)}, "demo") {
		t.Error("metadata missing the ACH marker reported as managed")
	}
}

// TestIsShellTeamManagedNonStringSiblings: LiteLLM stores its own management
// fields in the SAME metadata blob as ACH's ownership markers, and most of them
// are not strings. Saving any team in the LiteLLM UI writes the whole default
// block — guardrails ([]), model_rpm_limit ({}), disable_global_guardrails
// (false) — even when no enterprise feature is configured (LiteLLM issue
// #20304). Decoding the blob into map[string]string fails on the FIRST
// non-string value and takes the ACH markers down with it, so a single UI save
// would make the operator disown its own shell team: permanent repair churn on
// every reconcile and the ownership gate silently degraded to IsShellTeamShaped.
//
// The two payloads below were captured verbatim from the live proxy on
// 2026-07-28 (teams "test-do-not-use" and "run").
func TestIsShellTeamManagedNonStringSiblings(t *testing.T) {
	for name, siblings := range map[string]string{
		"guardrail attached via UI": `"guardrails":["test1"],"model_rpm_limit":{},"model_tpm_limit":{},` +
			`"disable_global_guardrails":false,"allowed_passthrough_routes":[],` +
			`"opted_out_global_guardrails":[],"soft_budget_alerting_emails":[]`,
		"UI saved with no guardrail": `"guardrails":[],"model_rpm_limit":{},"model_tpm_limit":{},` +
			`"disable_global_guardrails":false,"allowed_passthrough_routes":[],` +
			`"opted_out_global_guardrails":[],"soft_budget_alerting_emails":[]`,
	} {
		raw := []byte(`{"` + ShellTeamManagedMetadataKey + `":"` + ShellTeamManagedMetadataValue +
			`","` + ShellTeamManagedEnvKey + `":"demo",` + siblings + `}`)
		if !IsShellTeamManaged(TeamListEntry{Metadata: raw}, "demo") {
			t.Errorf("%s: ACH-marked shell reported as not managed", name)
		}
		if IsShellTeamManaged(TeamListEntry{Metadata: raw}, "other-env") {
			t.Errorf("%s: entry marked for a different environment reported as managed", name)
		}
	}

	// A non-string value ON the marker key itself must never satisfy the gate.
	raw := []byte(`{"` + ShellTeamManagedMetadataKey + `":["env-shell"],"` +
		ShellTeamManagedEnvKey + `":"demo"}`)
	if IsShellTeamManaged(TeamListEntry{Metadata: raw}, "demo") {
		t.Error("non-string marker value reported as managed")
	}
}

// TestIsShellTeamShaped: the Fix 2 migration-adoption check — alias plus the
// exact deny-all Models sentinel, independent of metadata.
func TestIsShellTeamShaped(t *testing.T) {
	if !IsShellTeamShaped(TeamListEntry{TeamAlias: "ach-env-demo", Models: []string{ShellTeamDenyAllModel}}, "demo") {
		t.Error("shell-shaped entry reported as not shaped")
	}
	if IsShellTeamShaped(TeamListEntry{TeamAlias: "ach-env-demo", Models: []string{"gpt-4"}}, "demo") {
		t.Error("a team with a real model granted reported as shaped")
	}
	if IsShellTeamShaped(TeamListEntry{TeamAlias: "some-other-alias", Models: []string{ShellTeamDenyAllModel}}, "demo") {
		t.Error("a different alias reported as shaped")
	}
	if IsShellTeamShaped(TeamListEntry{TeamAlias: "ach-env-demo"}, "demo") {
		t.Error("absent Models (nil, not the sentinel) reported as shaped")
	}
}

// TestNewShellTeamRequestCarriesGuardrails: the env shell transmits its
// guardrails; an empty list omits the key entirely. LiteLLM premium-gates a
// NON-EMPTY guardrails write (403 without an Enterprise licence), so a
// guardrail-free Environment must not send the field at all.
func TestNewShellTeamRequestCarriesGuardrails(t *testing.T) {
	b, err := json.Marshal(NewShellTeamRequest("demo", []string{"pii-filter"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"guardrails":["pii-filter"]`) {
		t.Fatalf("guardrails missing from %s", b)
	}

	for name, g := range map[string][]string{"nil": nil, "empty": {}} {
		b, err := json.Marshal(NewShellTeamRequest("demo", g))
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		if strings.Contains(string(b), "guardrails") {
			t.Errorf("%s must omit the key, got %s", name, b)
		}
	}
}

// TestTeamUpdateRequestGuardrailsAlwaysExplicit: unlike NewShellTeamRequest
// (create path, omitempty), TeamUpdateRequest.Guardrails has no omitempty —
// ACH does not rely on the unverified "omitted key = keep prior value"
// assumption for this field (see references/litellm-permission-model.md
// §11). The caller (ensureShellTeam) is responsible for passing a non-nil
// slice on removal; TestEnsureShellTeamCarriesGuardrails covers that.
func TestTeamUpdateRequestGuardrailsAlwaysExplicit(t *testing.T) {
	b, err := json.Marshal(TeamUpdateRequest{TeamID: "t-1", Guardrails: []string{"pii-filter"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"guardrails":["pii-filter"]`) {
		t.Fatalf("guardrails missing from %s", b)
	}

	b, err = json.Marshal(TeamUpdateRequest{TeamID: "t-1", Guardrails: []string{}})
	if err != nil {
		t.Fatalf("empty marshal: %v", err)
	}
	if !strings.Contains(string(b), `"guardrails":[]`) {
		t.Errorf("empty must explicitly clear via [], got %s", b)
	}
}

// TestUserShellNeverCarriesGuardrails: D2 — coverage is EK-only. A pk_ lives in
// the user shell, which spans every Environment its owner is entitled to, so
// there is no per-Environment attachment point on it.
func TestUserShellNeverCarriesGuardrails(t *testing.T) {
	b, err := json.Marshal(NewUserShellRequest("jc@example.com"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "guardrails") {
		t.Fatalf("user shell must never carry guardrails: %s", b)
	}
}

// TestTeamGuardrails reads metadata.guardrails, tolerating the non-string
// sibling fields the LiteLLM UI writes alongside ACH's ownership markers.
func TestTeamGuardrails(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want []string
	}{
		"present": {
			`{"ach_managed":"env-shell","guardrails":["pii-filter","credential-filter"],` +
				`"model_rpm_limit":{},"disable_global_guardrails":false}`,
			[]string{"pii-filter", "credential-filter"},
		},
		"absent":      {`{"ach_managed":"env-shell"}`, nil},
		"empty array": {`{"guardrails":[]}`, []string{}},
		"unparseable": {`not json`, nil},
		"wrong type":  {`{"guardrails":"pii-filter"}`, nil},
		"mixed types": {`{"guardrails":["ok",42]}`, []string{"ok"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := TeamGuardrails(json.RawMessage(tc.raw))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("TeamGuardrails = %v, want %v", got, tc.want)
			}
		})
	}
	if got := TeamGuardrails(nil); got != nil {
		t.Errorf("nil metadata = %v", got)
	}
}

// TestShellTeamDriftedGuardrails closes S2. ShellTeamDrifted compared only
// models + object_permission, so REMOVING a guardrail from the spec would never
// trigger a repair and the guardrail would keep running forever.
func TestShellTeamDriftedGuardrails(t *testing.T) {
	healthy := func(meta string) TeamListEntry {
		return TeamListEntry{
			Models:           []string{ShellTeamDenyAllModel},
			ObjectPermission: ShellTeamPermissions(),
			Metadata:         json.RawMessage(meta),
		}
	}
	const base = `{"ach_managed":"env-shell","ach_environment":"demo"`

	for name, tc := range map[string]struct {
		meta  string
		want  []string
		drift bool
	}{
		"same set, same order": {base + `,"guardrails":["a","b"]}`, []string{"a", "b"}, false},
		"same set, diff order": {base + `,"guardrails":["a","b"]}`, []string{"b", "a"}, false},
		"added":                {base + `,"guardrails":["a"]}`, []string{"a", "b"}, true},
		"removed":              {base + `,"guardrails":["a","b"]}`, []string{"a"}, true},
		"cleared":              {base + `,"guardrails":["a"]}`, nil, true},
		"none wanted none set": {base + `}`, nil, false},
		"wanted but absent":    {base + `}`, []string{"a"}, true},
		// LiteLLM's storage keeps duplicates verbatim while its enforcement
		// path builds a set — so both sides must be deduped or this is
		// permanent phantom drift and a rewrite every reconcile.
		"dupe upstream": {base + `,"guardrails":["a","a"]}`, []string{"a"}, false},
		"dupe wanted":   {base + `,"guardrails":["a"]}`, []string{"a", "a"}, false},
		"dupe both":     {base + `,"guardrails":["a","a"]}`, []string{"a", "a"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ShellTeamDrifted(healthy(tc.meta), tc.want); got != tc.drift {
				t.Fatalf("ShellTeamDrifted = %v, want %v", got, tc.drift)
			}
		})
	}

	// The pre-existing unverifiable-read-back contract must survive: a bare
	// team-list row carries neither models nor object_permission and must not
	// be reported as drifted when no guardrails are wanted either.
	if ShellTeamDrifted(TeamListEntry{}, nil) {
		t.Error("bare team-list row reported as drifted")
	}
	// ... but a wanted guardrail on such a row IS drift — the write must happen.
	if !ShellTeamDrifted(TeamListEntry{}, []string{"a"}) {
		t.Error("wanted guardrail on a bare row must drift so it gets written")
	}
}
