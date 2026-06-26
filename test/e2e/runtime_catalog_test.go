//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Live-cluster assertion for the admin-only runtime catalog (Tasks 1-5 of the
// runtime-catalog feature). Proves the operator → Postgres → admin-endpoint
// path end-to-end against the kept kind cluster:
//
//   - an admin pk_ GET /platform/admin/runtime/models returns 200 with a
//     non-empty item list that includes the seeded `demo-model` (the operator
//     Snapshotter persisted the LiteLLM registry into runtime_catalog_entries);
//   - an ek_ on the same endpoint is rejected 401 invalid_key_type (the
//     AdminOnly key-type gate fires before the allowlist check).
//
// The admin allowlist for the suite (kilgore@kilgore.trout) is set in
// test/e2e/cluster/02-ach/ach.values.yaml.

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestRuntimeCatalogAdminOnly is order-independent: it acquires its own pk_ via
// the mock SSO flow and reads the platform-api base URL the other phase-6
// helpers use.
func TestRuntimeCatalogAdminOnly(t *testing.T) {
	phase6SuiteGuard(t)

	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	modelsURL := baseURL + "/platform/admin/runtime/models"

	t.Run("admin_pk_lists_models", func(t *testing.T) {
		client := &http.Client{Timeout: 10 * time.Second}

		// The catalog is populated by the operator's 5-min Snapshotter; by the
		// time the demo Environment is Available (gated during cluster-up) the
		// snapshot — and thus the table — already holds demo-model. Bounded
		// retry absorbs any residual first-refresh lag; explicit failure on
		// exhaustion (no naked polling).
		const maxAttempts = 20
		var items []catalogItem
		var lastStatus int
		var lastBody string
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("x-ach-key", pk)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", modelsURL, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatus, lastBody = resp.StatusCode, string(body)

			if resp.StatusCode == http.StatusOK {
				var decoded struct {
					Items []catalogItem `json:"items"`
				}
				if err := json.Unmarshal(body, &decoded); err != nil {
					t.Fatalf("decode models response: %v; body=%s", err, lastBody)
				}
				if len(decoded.Items) > 0 {
					items = decoded.Items
					break
				}
			}
			time.Sleep(3 * time.Second)
		}

		if len(items) == 0 {
			t.Fatalf("admin runtime catalog never returned models after %d attempts; last status=%d body=%s",
				maxAttempts, lastStatus, lastBody)
		}

		found := false
		for _, it := range items {
			if it.Kind != "model" {
				t.Errorf("non-model entry under /runtime/models: %+v", it)
			}
			if it.Name == "demo-model" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected seeded model %q in catalog; got %+v", "demo-model", items)
		}
	})

	t.Run("teams_listed", func(t *testing.T) {
		teamsURL := baseURL + "/platform/admin/runtime/teams"
		client := &http.Client{Timeout: 10 * time.Second}

		// The catalog is populated by the operator's 5-min Snapshotter; the
		// demo Environment authorizes team "default" (created by EnsureDefaultTeam).
		// Bounded retry absorbs any residual first-refresh lag.
		const maxAttempts = 20
		var items []catalogItem
		var lastStatus int
		var lastBody string
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			req, err := http.NewRequest(http.MethodGet, teamsURL, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("x-ach-key", pk)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", teamsURL, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatus, lastBody = resp.StatusCode, string(body)

			if resp.StatusCode == http.StatusOK {
				var decoded struct {
					Items []catalogItem `json:"items"`
				}
				if err := json.Unmarshal(body, &decoded); err != nil {
					t.Fatalf("decode teams response: %v; body=%s", err, lastBody)
				}
				if len(decoded.Items) > 0 {
					items = decoded.Items
					break
				}
			}
			time.Sleep(3 * time.Second)
		}

		if len(items) == 0 {
			t.Fatalf("admin runtime catalog never returned teams after %d attempts; last status=%d body=%s",
				maxAttempts, lastStatus, lastBody)
		}

		found := false
		for _, it := range items {
			if it.Kind != "team" {
				t.Errorf("non-team entry under /runtime/teams: %+v", it)
			}
			if it.Name == "default" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected seeded team %q in catalog; got %+v", "default", items)
		}
	})

	t.Run("ek_rejected_invalid_key_type", func(t *testing.T) {
		// A VALID ek_ is required so the request passes Authn (key resolves)
		// and reaches the AdminOnly key-type gate; a garbage key would 401 at
		// Authn with a different code. mustAcquireEkBoundToEnv skips the subtest
		// if LiteLLM limits block ek_ minting.
		ek := mustAcquireEkBoundToEnv(t, "demo")

		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("x-ach-key", ek)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s with ek_: %v", modelsURL, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("ek_ on admin runtime endpoint: got status %d, want 401; body=%s",
				resp.StatusCode, string(body))
		}
		var decoded struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode error envelope: %v; body=%s", err, string(body))
		}
		if decoded.Error.Code != "invalid_key_type" {
			t.Errorf("ek_ rejection code: got %q, want invalid_key_type; body=%s",
				decoded.Error.Code, string(body))
		}
	})
}

type catalogItem struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}
