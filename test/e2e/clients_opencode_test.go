//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestClientsOpenCode — the OpenCode client surface on the single e2e origin:
// the plugin tarball carries this deployment's platform.json, and the
// per-user config answers a real OAuth access token with the user's models
// under the chart's provider name, and an unverifiable token with the
// baseline (still 200).
func TestClientsOpenCode(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(base + "/clients/opencode/plugin")
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("plugin: %d not gzip: %v", resp.StatusCode, err)
	}
	var platform map[string]string
	for tr := tar.NewReader(gz); ; {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if h.Name == "package/platform.json" {
			b, _ := io.ReadAll(tr)
			_ = json.Unmarshal(b, &platform)
		}
	}
	_ = resp.Body.Close()
	if platform["provider"] != "ai-platform" || platform["api"] == "" || platform["platform"] == "" {
		t.Fatalf("platform.json = %v", platform)
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
	code, hdr, m := get("Bearer " + jwt)
	if code != 200 || hdr.Get("Cache-Control") != "no-store" || m["auth"] != "ok" || m["stale"] != false {
		t.Fatalf("config: %d %v %v", code, hdr, m)
	}
	cfg, _ := m["config"].(map[string]any)
	prov, _ := cfg["provider"].(map[string]any)
	ai, _ := prov["ai-platform"].(map[string]any)
	models, _ := ai["models"].(map[string]any)
	if len(models) == 0 {
		t.Fatalf("no models under provider ai-platform: %v", m)
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
