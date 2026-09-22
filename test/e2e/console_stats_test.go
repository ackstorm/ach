//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// TestConsoleStats — unified-console Phase 3, AC-16 / D-13. Task 5 of the
// console-phase3 plan.

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestConsoleStats drives GET /platform/console/{stats,latency} against the
// kept cluster and asserts the D-13/AC-16 contract: both routes fold the
// caller's OWN LiteLLM window ("data_scope":"user", never Environment-
// scoped), and the totals include traffic sent through an ek_ owned by the
// same user, not just the pk_ itself. Reuses TestPkScopeMeasure's traffic
// recipe (pk_scope_measure_test.go:50-75): one /v1/chat/completions call
// through the forwarder as the OAuth pk_ (Authorization: Bearer JWT) and one
// as an ek_ bound to the "demo" Environment (x-ach-key).
func TestConsoleStats(t *testing.T) {
	gateway := "http://" + phase4GatewayAuthority(t)
	creds := oauthLogin(t, gateway, "")
	ekA := mustAcquireEkBoundToEnv(t, "demo")

	chat := func(header, cred string) {
		req, _ := http.NewRequest(http.MethodPost, gateway+"/v1/chat/completions",
			strings.NewReader(`{"model":"demo-model","messages":[{"role":"user","content":"console stats e2e"}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(header, cred)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("chat via %s: %v", header, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		t.Logf("traffic %s: %d", header, resp.StatusCode)
	}
	chat("Authorization", "Bearer "+creds.Access)
	chat("x-ach-key", ekA)

	get := func(t *testing.T, path string, hdr map[string]string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, gateway+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		return resp.StatusCode, m
	}
	bearer := map[string]string{"Authorization": "Bearer " + creds.Access}

	// LiteLLM writes spend logs asynchronously — bound the wait, never a
	// naked loop (within() polls every 2s, fails the test past the deadline).
	var stats map[string]any
	within(t, 90*time.Second, "console stats include EK traffic (AC-16)", func() bool {
		code, s := get(t, "/platform/console/stats", bearer)
		if code != http.StatusOK {
			return false
		}
		totals, _ := s["totals"].(map[string]any)
		if totals == nil {
			return false
		}
		requests, _ := totals["requests"].(float64)
		if requests < 2 {
			return false
		}
		stats = s
		return true
	})
	if stats == nil {
		t.Fatal("console stats never reflected the pk_ + ek_ traffic within 90s")
	}
	if stats["data_scope"] != "user" {
		t.Fatalf("stats data_scope = %v, want %q", stats["data_scope"], "user")
	}
	keys, _ := stats["keys"].([]any)
	var sawPersonal, sawEk bool
	for _, k := range keys {
		row, _ := k.(map[string]any)
		switch row["key_alias"] {
		case "personal":
			sawPersonal = true
		case "demo-ek":
			sawEk = true
		}
	}
	if !sawPersonal {
		t.Errorf("stats.keys never carried a \"personal\" row for the pk_: %v", keys)
	}
	if !sawEk {
		t.Errorf("stats.keys never carried the ek_'s own name \"demo-ek\": %v", keys)
	}

	code, lat := get(t, "/platform/console/latency", bearer)
	if code != http.StatusOK {
		t.Fatalf("latency status = %d, want 200: %v", code, lat)
	}
	if lat["data_scope"] != "user" {
		t.Fatalf("latency data_scope = %v, want %q", lat["data_scope"], "user")
	}
	if lat["available"] != true {
		t.Fatalf("latency available = %v, want true: %v", lat["available"], lat)
	}

	// The Phase 1 cookie path (web console session) answers the same query
	// as the bearer path — same user, same window. consoleLogin runs a full
	// OAuth+Dex round trip, and LiteLLM keeps writing spend logs
	// asynchronously in the meantime (the shared e2e cluster may also be
	// carrying other tests' traffic for this same user concurrently), so the
	// two snapshots are not byte-identical in practice — assert monotonic
	// (never less) rather than equal, which still catches a cookie path that
	// answers some other scope or a stale/zero read.
	cookie := consoleLogin(t, gateway)
	code, cookieStats := get(t, "/platform/console/stats", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
	if code != http.StatusOK || cookieStats["data_scope"] != "user" {
		t.Fatalf("cookie stats: %d %v", code, cookieStats)
	}
	cookieTotals, _ := cookieStats["totals"].(map[string]any)
	bearerTotals, _ := stats["totals"].(map[string]any)
	cookieRequests, _ := cookieTotals["requests"].(float64)
	bearerRequests, _ := bearerTotals["requests"].(float64)
	if cookieRequests < bearerRequests {
		t.Fatalf("cookie totals.requests = %v, bearer totals.requests = %v — same user, want cookie >= bearer",
			cookieRequests, bearerRequests)
	}
	if cookieRequests < 2 {
		t.Fatalf("cookie totals.requests = %v, want >= 2 (pk_ + ek_ traffic)", cookieRequests)
	}

	// An ek_ presented to the console analytics routes is refused — they
	// require a personal (pk_) identity.
	code, ekResp := get(t, "/platform/console/stats", map[string]string{"x-ach-key": ekA})
	if code != http.StatusUnauthorized {
		t.Fatalf("ek_ on /platform/console/stats: status = %d, want 401: %v", code, ekResp)
	}
}
