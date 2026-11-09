// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/config"
)

func decodeJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	return m
}

func TestToken_JSON(t *testing.T) {
	dir := loginTestEnv(t)
	path := filepath.Join(dir, "ach", "config.yaml")
	exp := time.Now().Add(time.Hour).Unix()
	payload, _ := json.Marshal(map[string]any{"exp": exp})
	jwt := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	if err := config.Save(path, &config.File{Default: "p", Profiles: map[string]*config.Profile{"p": {
		URL: "https://ach.example.com",
		OAuth: &config.OAuthCreds{ClientID: "oc", AccessToken: jwt, RefreshToken: "r1",
			ExpiresAt: time.Now().Add(time.Hour)},
	}}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, _, err := executeCommand(t, newTokenCmd(), "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, stdout)
	if m["token"] != jwt || m["type"] != "jwt" ||
		m["expires_at"] != time.Unix(exp, 0).UTC().Format(time.RFC3339) {
		t.Errorf("jwt: %v", m)
	}

	if err := config.Save(path, &config.File{Default: "p", Profiles: map[string]*config.Profile{"p": {
		URL: "https://ach.example.com", Key: testEK,
	}}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, _, err = executeCommand(t, newTokenCmd(), "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	m = decodeJSON(t, stdout)
	if m["token"] != testEK || m["type"] != "ek" {
		t.Errorf("ek: %v", m)
	}
	if v, ok := m["expires_at"]; !ok || v != nil {
		t.Errorf("ek expires_at must be present and null: %v", m)
	}
}

const whoamiProfile = "p-json"

func TestWhoami_JSON(t *testing.T) {
	dir := whoamiTestEnv(t)
	ts := bootstrapServer(t, map[string]any{
		"email": "jc@example.com", "keys_used": 3, "max_keys": 5,
		"budget": map[string]any{"spend": 12.4, "max_budget": 100, "budget_duration": "30d"},
	}, nil)
	seedConfig(t, dir, whoamiProfile, oauthProfile(ts.URL))
	out, _, _, err := executeWhoami(t, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, out)
	b, _ := m["budget"].(map[string]any)
	k, _ := m["keys"].(map[string]any)
	if m["profile"] != whoamiProfile || m["url"] != ts.URL || m["user"] != "jc@example.com" ||
		m["auth"] != authSession || b["spend"] != 12.4 || b["max_budget"] != 100.0 ||
		b["budget_duration"] != "30d" || k["held"] != 3.0 || k["allowed"] != 5.0 {
		t.Errorf("got %v", m)
	}
	if v, ok := b["reset_at"]; !ok || v != nil {
		t.Errorf("reset_at must be present and null: %v", b)
	}

	// No budget, no key counts: both objects are null, not omitted.
	ts2 := bootstrapServer(t, map[string]any{"email": "jc@example.com"}, nil)
	seedConfig(t, dir, whoamiProfile, oauthProfile(ts2.URL))
	out, _, _, err = executeWhoami(t, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	m = decodeJSON(t, out)
	for _, f := range []string{"budget", "keys"} {
		if v, ok := m[f]; !ok || v != nil {
			t.Errorf("%s must be present and null: %v", f, m)
		}
	}
}

func TestKeysCreate_JSON(t *testing.T) {
	for _, noSave := range []bool{false, true} {
		credTestEnv(t)
		srv := newKeysTestServer(t)
		seedKeysProfile(t, srv.URL, nil)
		args := []string{"create", "demo", "--name", "laptop", "-o", "json"}
		if noSave {
			args = append(args, "--no-save")
		}
		stdout, stderr, _, err := executeKeys(t, "", args...)
		if err != nil {
			t.Fatal(err)
		}
		m := decodeJSON(t, stdout)
		if m["name"] != "laptop" || m["id"] != "ekid_new" || m["environment"] != "demo" || m["key"] != testEKSaved {
			t.Errorf("noSave=%v: %v", noSave, m)
		}
		if v, ok := m["expires_at"]; !ok || v != nil {
			t.Errorf("expires_at must be present and null: %v", m)
		}
		if !strings.Contains(stderr, "revoke with") {
			t.Errorf("revoke hint must stay on stderr: %q", stderr)
		}
	}
}

func TestEnvDescribe_JSON(t *testing.T) {
	dir := envTestEnv(t)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/platform/environments/demo" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "demo", "status": "Available", "description": "d"})
	}))
	defer ts.Close()
	seedEnvConfig(t, dir, "prod", &config.Profile{URL: ts.URL, Key: testEK})
	swapHTTPClientForTest(t, &envHTTPClient, ts.Client())
	out, _, _, err := executeEnv(t, "describe", "demo", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, out)
	if m["name"] != "demo" || m["status"] != "Available" || m["description"] != "d" {
		t.Errorf("got %v", m)
	}
}

func TestProfileListShow_JSON(t *testing.T) {
	credTestEnv(t)
	seedProfiles(t)
	out, _, _, err := executeProfile(t, "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(list) != 2 || list[0]["name"] != "ci" || list[0]["auth"] != "key" || list[0]["current"] != false ||
		list[0]["saved_keys"] != 0.0 || list[1]["name"] != "prod" || list[1]["url"] != "https://prod.example" ||
		list[1]["auth"] != authSession || list[1]["current"] != true || list[1]["saved_keys"] != 1.0 {
		t.Errorf("list: %v", list)
	}
	shown, _, _, err := executeProfile(t, "show", "ci", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, shown)
	if m["name"] != "ci" || m["auth"] != "key" || m["current"] != false || m["saved_keys"] != 0.0 {
		t.Errorf("show: %v", m)
	}
	for _, secret := range []string{testEK, testEKSaved, "ekid_lap", "h.p.s"} {
		if strings.Contains(out, secret) || strings.Contains(shown, secret) {
			t.Errorf("secret %q leaked", secret)
		}
	}
}
