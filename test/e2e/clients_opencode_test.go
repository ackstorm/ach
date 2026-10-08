//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestClientsOpenCode — the OpenCode client surface on the single e2e origin:
// /.well-known/opencode installs the plugin with this deployment's options, and the
// per-user config answers a real OAuth access token with the user's models
// under the chart's provider name, and an unverifiable token with the
// baseline (still 200).
func TestClientsOpenCode(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(base + "/.well-known/opencode")
	if err != nil {
		t.Fatal(err)
	}
	var wk struct {
		Auth struct {
			Command []string `json:"command"`
			Env     *string  `json:"env"`
		} `json:"auth"`
		Config struct {
			Plugin           [][]json.RawMessage `json:"plugin"`
			EnabledProviders []string            `json:"enabled_providers"`
		} `json:"config"`
	}
	err = json.NewDecoder(resp.Body).Decode(&wk)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || len(wk.Auth.Command) == 0 || wk.Auth.Env == nil ||
		len(wk.Config.Plugin) != 1 || len(wk.Config.Plugin[0]) != 2 {
		t.Fatalf("well-known: %d %v %+v", resp.StatusCode, err, wk)
	}
	var opts map[string]string
	_ = json.Unmarshal(wk.Config.Plugin[0][1], &opts)
	if opts["provider"] != "ai-platform" || opts["api"] != base+"/v1" || opts["platform"] != base ||
		len(wk.Config.EnabledProviders) != 1 || wk.Config.EnabledProviders[0] != "ai-platform" {
		t.Fatalf("plugin options = %v, enabled_providers = %v", opts, wk.Config.EnabledProviders)
	}

	get := func(authz string) (int, http.Header, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, base+"/clients/opencode/config", nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, resp.Header, m
	}

	jwt := oauthLogin(t, base, "").Access
	// On a fresh cluster the user's shell team may be brand new: it reaches demo's
	// models only after the operator's next demo reconcile attaches it (the poke
	// makes that immediate), and LiteLLM then keeps answering /model_group/info
	// from its in-memory pre-attach state for a few minutes (measured 3.5-4 min
	// with LiteLLM v1.99.1, independent of DEFAULT_ACCESS_GROUP_CACHE_TTL). A
	// settled user passes on the first read.
	pokeDemoEnvironment(t)
	var m map[string]any
	var models map[string]any
	for deadline := time.Now().Add(5 * time.Minute); ; time.Sleep(2 * time.Second) {
		var code int
		var hdr http.Header
		code, hdr, m = get("Bearer " + jwt)
		if code != 200 || hdr.Get("Cache-Control") != "no-store" || m["auth"] != "ok" || m["stale"] != false {
			t.Fatalf("config: %d %v %v", code, hdr, m)
		}
		cfg, _ := m["config"].(map[string]any)
		prov, _ := cfg["provider"].(map[string]any)
		ai, _ := prov["ai-platform"].(map[string]any)
		models, _ = ai["models"].(map[string]any)
		if len(models) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no models under provider ai-platform within 5m: %v", m)
		}
	}
	for name, raw := range models {
		if lim, _ := raw.(map[string]any)["limit"].(map[string]any); lim["context"].(float64) <= 0 {
			t.Fatalf("model %s: limit %v", name, lim)
		}
	}
	if mcp, has := m["config"].(map[string]any)["mcp"]; has {
		t.Fatalf("config carries mcp: %v", mcp)
	}
	sk, _ := m["skills"].([]any)
	if len(sk) != 1 || sk[0].(map[string]any)["name"] != "ai-platform-api" {
		t.Fatalf("skills = %v", m["skills"])
	}
	if md, _ := sk[0].(map[string]any)["files"].(map[string]any)["SKILL.md"].(string); !strings.HasPrefix(md, "---\nname: ai-platform-api\n") || strings.Contains(md, "{{") {
		t.Fatalf("SKILL.md = %q", md)
	}

	code, _, m := get("Bearer x.y.z")
	if code != 200 || m["auth"] != "invalid" || m["user"] != nil {
		t.Fatalf("baseline: %d %v", code, m)
	}
	if code, _, _ = get(""); code != 401 {
		t.Fatalf("no bearer: %d, want 401", code)
	}
}
