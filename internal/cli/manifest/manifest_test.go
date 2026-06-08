// SPDX-License-Identifier: Apache-2.0

package manifest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/manifest"
)

// goldenHydratePath is the relative path from this test file to the
// repo's golden hydrate.json fixture. The golden lives at
// <repo>/examples/hydrate.json; the test file lives at
// <repo>/internal/cli/manifest/manifest_test.go.
const goldenHydratePath = "../../../examples/hydrate.json"

// TestDecode_GoldenHydrate asserts that the on-disk golden artifact
// (the literal bytes the W3-P3 e2e --raw test diffs against) round-
// trips through Decode without loss. Every field the orchestrator and
// adapters read MUST survive: SchemaVersion, Environment,
// Runtime.{Models,MCPServers,A2AAgents}, Context.{Prompts,Plugins,
// Artifacts}, and ContentRef.{ID,Name,DownloadURL,Endpoint}.
//
// Runtime entries MUST carry non-empty Endpoint (adapters consume
// this for runtime-config URL construction per ADAPT-03); context
// entries MUST carry non-empty DownloadURL.
func TestDecode_GoldenHydrate(t *testing.T) {
	raw, err := os.ReadFile(goldenHydratePath)
	if err != nil {
		t.Fatalf("read golden %s: %v", goldenHydratePath, err)
	}
	m, err := manifest.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode golden: %v", err)
	}

	if m.SchemaVersion != "v1alpha1" {
		t.Errorf("SchemaVersion = %q, want v1alpha1", m.SchemaVersion)
	}
	if m.Environment != "demo" {
		t.Errorf("Environment = %q, want demo", m.Environment)
	}
	if m.Runtime == nil {
		t.Fatal("Runtime is nil")
	}
	if m.Context == nil {
		t.Fatal("Context is nil")
	}

	// Runtime arms — all three present in the golden with non-empty
	// Endpoint values. The plan's <verify> mandates MCPServers[0].
	if got, want := len(m.Runtime.Models), 1; got != want {
		t.Errorf("len(Runtime.Models) = %d, want %d", got, want)
	} else if m.Runtime.Models[0].Endpoint == "" {
		t.Error("Runtime.Models[0].Endpoint is empty — must round-trip")
	} else if m.Runtime.Models[0].ID != "demo-model" {
		t.Errorf("Runtime.Models[0].ID = %q, want demo-model", m.Runtime.Models[0].ID)
	}
	if got, want := len(m.Runtime.MCPServers), 2; got != want {
		t.Errorf("len(Runtime.MCPServers) = %d, want %d", got, want)
	} else {
		if m.Runtime.MCPServers[0].Endpoint == "" {
			t.Error("Runtime.MCPServers[0].Endpoint is empty — must round-trip (ADAPT-03)")
		}
		if m.Runtime.MCPServers[0].Endpoint != "http://localhost:8080/mcp/demo-mcp-jwt" {
			t.Errorf("Runtime.MCPServers[0].Endpoint = %q, want http://localhost:8080/mcp/demo-mcp-jwt",
				m.Runtime.MCPServers[0].Endpoint)
		}
		if m.Runtime.MCPServers[1].Endpoint == "" {
			t.Error("Runtime.MCPServers[1].Endpoint is empty — must round-trip")
		}
	}
	if got, want := len(m.Runtime.A2AAgents), 1; got != want {
		t.Errorf("len(Runtime.A2AAgents) = %d, want %d", got, want)
	} else if m.Runtime.A2AAgents[0].Endpoint == "" {
		t.Error("Runtime.A2AAgents[0].Endpoint is empty — must round-trip")
	}

	// Context arms — non-empty DownloadURL on every entry.
	if got, want := len(m.Context.Prompts), 1; got != want {
		t.Errorf("len(Context.Prompts) = %d, want %d", got, want)
	} else {
		if m.Context.Prompts[0].DownloadURL == "" {
			t.Error("Context.Prompts[0].DownloadURL is empty — must round-trip")
		}
		if m.Context.Prompts[0].Name != "claude-code-system-prompt" {
			t.Errorf("Context.Prompts[0].Name = %q, want claude-code-system-prompt",
				m.Context.Prompts[0].Name)
		}
		// Context entries MUST NOT carry Endpoint (round-trips to "").
		if m.Context.Prompts[0].Endpoint != "" {
			t.Errorf("Context.Prompts[0].Endpoint = %q, want empty (context entries have no endpoint)",
				m.Context.Prompts[0].Endpoint)
		}
	}
	// Plugins: the demo env declares caveman + the marketplace-sourced
	// feature-dev@conflict-mkt-a (see examples/hydrate.json).
	if got, want := len(m.Context.Plugins), 2; got != want {
		t.Errorf("len(Context.Plugins) = %d, want %d", got, want)
	} else if m.Context.Plugins[0].DownloadURL == "" {
		t.Error("Context.Plugins[0].DownloadURL is empty — must round-trip")
	}
	if got, want := len(m.Context.Artifacts), 1; got != want {
		t.Errorf("len(Context.Artifacts) = %d, want %d", got, want)
	} else if m.Context.Artifacts[0].DownloadURL == "" {
		t.Error("Context.Artifacts[0].DownloadURL is empty — must round-trip")
	}
	// Skills: the demo env declares pdf + docx + the marketplace-sourced
	// pdf@anthropic-skills; each must round-trip with a non-empty DownloadURL.
	if got, want := len(m.Context.Skills), 3; got != want {
		t.Errorf("len(Context.Skills) = %d, want %d", got, want)
	} else if m.Context.Skills[0].DownloadURL == "" {
		t.Error("Context.Skills[0].DownloadURL is empty — must round-trip")
	}
}

// TestDecode_SchemaV2_ReturnsErrSchemaMismatch asserts that any
// schemaVersion != "v1alpha1" surfaces as ErrSchemaMismatch (via %w),
// so callers can `errors.Is(err, manifest.ErrSchemaMismatch)` and map
// to exit.SchemaMismatch (5).
func TestDecode_SchemaV2_ReturnsErrSchemaMismatch(t *testing.T) {
	in := `{"schemaVersion":"v2","environment":"demo","runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},"context":{"prompts":[],"plugins":[],"artifacts":[]}}`
	_, err := manifest.Decode(strings.NewReader(in))
	if err == nil {
		t.Fatal("Decode returned nil error on schemaVersion=v2; want ErrSchemaMismatch")
	}
	if !errors.Is(err, manifest.ErrSchemaMismatch) {
		t.Errorf("err = %v; want errors.Is(err, ErrSchemaMismatch)", err)
	}
}

// TestDecode_NilRuntime_ReturnsErrSchemaMismatch asserts that a missing
// runtime block (JSON `null` or absent key) surfaces as
// ErrSchemaMismatch.
func TestDecode_NilRuntime_ReturnsErrSchemaMismatch(t *testing.T) {
	in := `{"schemaVersion":"v1alpha1","environment":"demo","runtime":null,"context":{"prompts":[],"plugins":[],"artifacts":[]}}`
	_, err := manifest.Decode(strings.NewReader(in))
	if err == nil {
		t.Fatal("Decode returned nil error on runtime=null; want ErrSchemaMismatch")
	}
	if !errors.Is(err, manifest.ErrSchemaMismatch) {
		t.Errorf("err = %v; want errors.Is(err, ErrSchemaMismatch)", err)
	}
}

// TestDecode_NilContext_ReturnsErrSchemaMismatch asserts that a missing
// context block surfaces as ErrSchemaMismatch.
func TestDecode_NilContext_ReturnsErrSchemaMismatch(t *testing.T) {
	in := `{"schemaVersion":"v1alpha1","environment":"demo","runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},"context":null}`
	_, err := manifest.Decode(strings.NewReader(in))
	if err == nil {
		t.Fatal("Decode returned nil error on context=null; want ErrSchemaMismatch")
	}
	if !errors.Is(err, manifest.ErrSchemaMismatch) {
		t.Errorf("err = %v; want errors.Is(err, ErrSchemaMismatch)", err)
	}
}

// TestDecode_EmptyRuntimeArrays_OK asserts that a runtime block with
// all-empty arrays is valid per §15.2 (the always-present-with-[]
// posture). The Environment may be configured with no models, MCP
// servers, or A2A agents — that is a legitimate hydrate state.
func TestDecode_EmptyRuntimeArrays_OK(t *testing.T) {
	in := `{"schemaVersion":"v1alpha1","environment":"demo","runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},"context":{"prompts":[],"plugins":[],"artifacts":[]}}`
	m, err := manifest.Decode(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m.Runtime == nil || m.Context == nil {
		t.Fatal("Runtime or Context is nil after decode")
	}
	if len(m.Runtime.Models) != 0 ||
		len(m.Runtime.MCPServers) != 0 ||
		len(m.Runtime.A2AAgents) != 0 {
		t.Errorf("expected all runtime arrays empty; got %+v", m.Runtime)
	}
}

// TestDecode_UnknownField_Rejects asserts the DisallowUnknownFields
// strict-shape posture: an unknown top-level field is a decode error
// (wrapped via fmt.Errorf) and is NOT ErrSchemaMismatch — the two
// failure modes must be distinguishable for the caller.
func TestDecode_UnknownField_Rejects(t *testing.T) {
	in := `{"schemaVersion":"v1alpha1","environment":"demo","runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},"context":{"prompts":[],"plugins":[],"artifacts":[]},"bogusTopLevelField":1}`
	_, err := manifest.Decode(strings.NewReader(in))
	if err == nil {
		t.Fatal("Decode returned nil error on unknown field; want decode error")
	}
	if errors.Is(err, manifest.ErrSchemaMismatch) {
		t.Errorf("err = %v; should NOT be ErrSchemaMismatch — unknown-field is a decode-level error", err)
	}
	if !strings.Contains(err.Error(), "bogusTopLevelField") {
		t.Errorf("err = %v; want message to name the unknown field", err)
	}
}

// TestFetch_PostShape_BuildsCorrectBody asserts that Fetch sends a
// POST to /platform/hydrate with body `{"environment": "<env>"}` when
// the environment arg is non-empty.
func TestFetch_PostShape_BuildsCorrectBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("server: read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("server: decode body: %v (raw=%q)", err, string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schemaVersion":"v1alpha1","environment":"demo","runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},"context":{"prompts":[],"plugins":[],"artifacts":[]}}`))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test"}
	m, err := manifest.Fetch(context.Background(), c, "demo")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/platform/hydrate" {
		t.Errorf("path = %q, want /platform/hydrate", gotPath)
	}
	if gotBody["environment"] != "demo" {
		t.Errorf("body = %+v, want {environment: demo}", gotBody)
	}
	if m.Environment != "demo" {
		t.Errorf("decoded Environment = %q, want demo", m.Environment)
	}
}

// TestFetch_EmptyEnvironment_SendsEmptyObject asserts the
// ek_-without-flag path: when environment == "", Fetch sends `{}` and
// the server-side body has no `environment` key.
func TestFetch_EmptyEnvironment_SendsEmptyObject(t *testing.T) {
	var gotRaw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		gotRaw, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("server: read body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schemaVersion":"v1alpha1","environment":"","runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},"context":{"prompts":[],"plugins":[],"artifacts":[]}}`))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "ek_test"}
	if _, err := manifest.Fetch(context.Background(), c, ""); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := strings.TrimSpace(string(gotRaw)); got != "{}" {
		t.Errorf("body = %q, want {}", got)
	}
}

// TestFetch_NonStringEnvironmentField_StillEncodes is a regression
// guard: the env-name string must JSON-encode cleanly even if it
// contains characters that need escaping.
func TestFetch_EnvironmentWithEscapes_Encodes(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schemaVersion":"v1alpha1","environment":"x","runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},"context":{"prompts":[],"plugins":[],"artifacts":[]}}`))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test"}
	if _, err := manifest.Fetch(context.Background(), c, `weird"env`); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotBody["environment"] != `weird"env` {
		t.Errorf("body = %+v, want environment=weird\"env", gotBody)
	}
}

// TestFetch_ServerError_BubblesUp asserts that a non-2xx response
// surfaces as *httpclient.ServerError (the caller layer maps it to
// the appropriate Phase 6 exit code via exit.MapServerError).
func TestFetch_ServerError_BubblesUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad key"},"request_id":"req_test"}`))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test"}
	_, err := manifest.Fetch(context.Background(), c, "demo")
	if err == nil {
		t.Fatal("Fetch returned nil error on 401; want *httpclient.ServerError")
	}
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Errorf("err = %v (%T); want *httpclient.ServerError", err, err)
	} else if sErr.Status != http.StatusUnauthorized {
		t.Errorf("ServerError.Status = %d, want %d", sErr.Status, http.StatusUnauthorized)
	}
}
