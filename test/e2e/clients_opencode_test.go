//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"net/http"
	"os/exec"
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
	if out, err := exec.Command("kubectl", "-n", "ach-system", "annotate", "environment/demo",
		"ach.ackstorm.ai/e2e-poke="+time.Now().UTC().Format(time.RFC3339Nano), "--overwrite").CombinedOutput(); err != nil {
		t.Fatalf("kubectl annotate environment/demo: %v\n%s", err, out)
	}
	modelsFrom := func(m map[string]any) map[string]any {
		cfg, _ := m["config"].(map[string]any)
		prov, _ := cfg["provider"].(map[string]any)
		ai, _ := prov["ai-platform"].(map[string]any)
		models, _ := ai["models"].(map[string]any)
		return models
	}
	modelNames := func(models map[string]any) []string {
		names := make([]string, 0, len(models))
		for name := range models {
			names = append(names, name)
		}
		return names
	}
	deadline := time.Now().Add(30 * time.Second)
	var code int
	var hdr http.Header
	var m map[string]any
	var models map[string]any
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("demo-model not ready within 30s (models=%v)", modelNames(models))
		}
		client.Timeout = remaining
		code, hdr, m = get("Bearer " + jwt)
		client.Timeout = 30 * time.Second
		if code != 200 || hdr.Get("Cache-Control") != "no-store" || m["auth"] != "ok" || m["stale"] != false {
			t.Fatalf("config readiness: status=%d no-store=%t auth=%v stale=%v", code, hdr.Get("Cache-Control") == "no-store", m["auth"], m["stale"])
		}
		models = modelsFrom(m)
		if _, ok := models["demo-model"]; ok {
			break
		}
		if time.Until(deadline) <= 0 {
			t.Fatalf("demo-model not ready within 30s (models=%v)", modelNames(models))
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(models) == 0 {
		t.Fatalf("no models under provider ai-platform: %v", modelNames(models))
	}
	for name, raw := range models {
		if lim, _ := raw.(map[string]any)["limit"].(map[string]any); lim["context"].(float64) <= 0 {
			t.Fatalf("model %s: limit %v", name, lim)
		}
	}
	if sk, _ := m["skills"].([]any); len(sk) != 1 || sk[0].(map[string]any)["name"] != "genai-api" {
		t.Fatalf("skills = %v", m["skills"])
	}

	code, _, m = get("Bearer x.y.z")
	if code != 200 || m["auth"] != "invalid" || m["user"] != nil {
		t.Fatalf("baseline: %d %v", code, m)
	}
	if code, _, _ = get(""); code != 401 {
		t.Fatalf("no bearer: %d, want 401", code)
	}
}
